// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
)

// Manager manages all log viewers.
type Manager struct {
	mu               sync.RWMutex
	viewers          map[string]*Viewer
	bus              events.EventBus
	monitorCancel    map[string]context.CancelFunc // cancel functions for monitor goroutines
	configMonitorWg  sync.WaitGroup                // wait group for config-based viewer monitor goroutines
	serviceMonitorWg sync.WaitGroup                // wait group for service viewer monitor goroutines
	ctx              context.Context               // parent context for starting viewers
	idleTimeout      time.Duration                 // duration after which idle viewers are stopped
	cleanupCancel    context.CancelFunc            // cancel function for cleanup goroutine
}

// NewManager creates a new log viewer manager.
func NewManager(bus events.EventBus, settings config.LogViewerSettings) *Manager {
	// Parse idle timeout
	idleTimeout := 5 * time.Minute // default
	if settings.IdleTimeout != "" && settings.IdleTimeout != "0" {
		if d, err := time.ParseDuration(settings.IdleTimeout); err == nil {
			idleTimeout = d
		}
	} else if settings.IdleTimeout == "0" {
		idleTimeout = 0 // disabled
	}

	return &Manager{
		viewers:       make(map[string]*Viewer),
		bus:           bus,
		monitorCancel: make(map[string]context.CancelFunc),
		idleTimeout:   idleTimeout,
	}
}

// Initialize creates viewers from configuration.
func (m *Manager) Initialize(configs []config.LogViewerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cfg := range configs {
		viewer, err := NewViewer(cfg)
		if err != nil {
			return fmt.Errorf("creating viewer %s: %w", cfg.Name, err)
		}
		m.viewers[cfg.Name] = viewer
	}

	return nil
}

// AddViewer registers a programmatically-created viewer (e.g., service log viewers).
func (m *Manager) AddViewer(viewer *Viewer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.viewers[viewer.Name()] = viewer
}

// RemoveServiceViewers stops and removes all viewers whose names start with "svc:".
func (m *Manager) RemoveServiceViewers() {
	m.mu.Lock()

	var toRemove []string
	for name := range m.viewers {
		if strings.HasPrefix(name, "svc:") {
			toRemove = append(toRemove, name)
		}
	}

	// Track how many goroutines we're cancelling so we can wait for just those
	var wg sync.WaitGroup

	// Cancel monitor goroutines for service viewers
	for _, name := range toRemove {
		if cancel, ok := m.monitorCancel[name]; ok {
			wg.Add(1)
			go func(c context.CancelFunc) {
				c()
				wg.Done()
			}(cancel)
			delete(m.monitorCancel, name)
		}
	}

	// Stop and remove service viewers
	for _, name := range toRemove {
		if viewer, ok := m.viewers[name]; ok {
			if err := viewer.Stop(); err != nil {
				log.Printf("Failed to stop service viewer %s: %v", name, err)
			}
			delete(m.viewers, name)
		}
	}

	m.mu.Unlock()

	// Wait for the cancel calls to complete (goroutines will exit on their own time
	// via monitorWg, but we don't block on non-service viewers)
	wg.Wait()
}

// Start stores the context for on-demand viewer startup.
// Viewers are started lazily when first accessed via GetAndStart() or EnsureStarted().
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctx = ctx

	// Start cleanup goroutine if idle timeout is enabled
	if m.idleTimeout > 0 {
		cleanupCtx, cancel := context.WithCancel(ctx)
		m.cleanupCancel = cancel
		go m.cleanupLoop(cleanupCtx)
		log.Printf("Log manager ready with %d viewers (lazy startup, idle timeout: %v)", len(m.viewers), m.idleTimeout)
	} else {
		log.Printf("Log manager ready with %d viewers (lazy startup, no idle timeout)", len(m.viewers))
	}

	return nil
}

// EnsureStarted starts a viewer if it's not already running.
// Returns an error if the viewer doesn't exist or fails to start.
func (m *Manager) EnsureStarted(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	viewer, ok := m.viewers[name]
	if !ok {
		return fmt.Errorf("viewer not found: %s", name)
	}

	// Update last accessed time (even if already running)
	viewer.Touch()

	// Skip if already running
	if _, running := m.monitorCancel[name]; running {
		return nil
	}

	if m.ctx == nil {
		return fmt.Errorf("manager not started")
	}

	log.Printf("Starting log viewer %s on-demand (source: %s)", name, viewer.source.Name())
	if err := viewer.Start(m.ctx); err != nil {
		log.Printf("Failed to start log viewer %s: %v", name, err)
		m.emitEvent("log.error", map[string]any{
			"viewer": name,
			"error":  err.Error(),
		})
		return fmt.Errorf("starting viewer %s: %w", name, err)
	}

	log.Printf("Log viewer %s started successfully", name)
	m.emitEvent("log.connected", map[string]any{
		"viewer": name,
		"source": viewer.source.Name(),
	})

	// Create a per-viewer context for the monitor goroutine
	monitorCtx, cancel := context.WithCancel(m.ctx)
	m.monitorCancel[name] = cancel

	// Use separate wait groups for service vs config-based viewers
	// so UpdateConfigs can wait only for config-based viewers
	if strings.HasPrefix(name, "svc:") {
		m.serviceMonitorWg.Add(1)
		go m.monitorErrorsService(monitorCtx, name, viewer)
	} else {
		m.configMonitorWg.Add(1)
		go m.monitorErrorsConfig(monitorCtx, name, viewer)
	}

	return nil
}

// Stop stops all viewers.
func (m *Manager) Stop() {
	m.mu.Lock()

	// Cancel cleanup goroutine
	if m.cleanupCancel != nil {
		m.cleanupCancel()
		m.cleanupCancel = nil
	}

	// Cancel all monitor goroutines
	for name, cancel := range m.monitorCancel {
		cancel()
		delete(m.monitorCancel, name)
	}

	// Stop all viewers
	for name, viewer := range m.viewers {
		if err := viewer.Stop(); err != nil {
			log.Printf("Failed to stop log viewer %s: %v", name, err)
		}
	}

	m.mu.Unlock()

	// Wait for all monitor goroutines to exit (outside lock to avoid deadlock)
	m.configMonitorWg.Wait()
	m.serviceMonitorWg.Wait()
}

// UpdateConfigs stops explicit (non-service) viewers and reinitializes with new configs.
// Service viewers (names starting with "svc:") are preserved — they are managed
// separately via RemoveServiceViewers/AddViewer.
func (m *Manager) UpdateConfigs(configs []config.LogViewerConfig) error {
	m.mu.Lock()

	// Cancel monitor goroutines for non-service viewers only
	for name, cancel := range m.monitorCancel {
		if !strings.HasPrefix(name, "svc:") {
			cancel()
			delete(m.monitorCancel, name)
		}
	}

	// Stop non-service viewers
	for name, viewer := range m.viewers {
		if !strings.HasPrefix(name, "svc:") {
			if err := viewer.Stop(); err != nil {
				log.Printf("Failed to stop log viewer %s during config update: %v", name, err)
			}
		}
	}

	m.mu.Unlock()

	// Wait only for config-based monitor goroutines to exit (outside lock to avoid deadlock)
	// Service viewers are preserved, so we don't wait for their goroutines
	m.configMonitorWg.Wait()

	// Now reinitialize with new configs
	m.mu.Lock()
	defer m.mu.Unlock()

	// Preserve service viewers, clear explicit viewers
	preserved := make(map[string]*Viewer)
	for name, viewer := range m.viewers {
		if strings.HasPrefix(name, "svc:") {
			preserved[name] = viewer
		}
	}
	m.viewers = preserved

	// Create new viewers from updated config
	for _, cfg := range configs {
		viewer, err := NewViewer(cfg)
		if err != nil {
			return fmt.Errorf("creating viewer %s: %w", cfg.Name, err)
		}
		m.viewers[cfg.Name] = viewer
	}

	log.Printf("Updated %d log viewers with new config (preserved %d service viewers)", len(configs), len(preserved))
	return nil
}

// Get returns a viewer by name without starting it.
func (m *Manager) Get(name string) (*Viewer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	viewer, ok := m.viewers[name]
	return viewer, ok
}

// GetAndStart returns a viewer by name, starting it if necessary.
// This is the preferred method for accessing viewers that need to be running.
func (m *Manager) GetAndStart(name string) (*Viewer, error) {
	if err := m.EnsureStarted(name); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	viewer, ok := m.viewers[name]
	if !ok {
		return nil, fmt.Errorf("viewer not found: %s", name)
	}
	return viewer, nil
}

// List returns all viewer names.
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.viewers))
	for name := range m.viewers {
		names = append(names, name)
	}
	return names
}

// ListStatus returns status for all viewers.
func (m *Manager) ListStatus() []ViewerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]ViewerStatus, 0, len(m.viewers))
	for _, viewer := range m.viewers {
		statuses = append(statuses, viewer.Status())
	}
	return statuses
}

// Subscribe subscribes to entries from a viewer, starting it if necessary.
func (m *Manager) Subscribe(name string, ch chan<- LogEntry) error {
	viewer, err := m.GetAndStart(name)
	if err != nil {
		return err
	}
	viewer.Subscribe(ch)
	return nil
}

// Unsubscribe unsubscribes from a viewer.
func (m *Manager) Unsubscribe(name string, ch chan<- LogEntry) error {
	viewer, ok := m.Get(name)
	if !ok {
		return fmt.Errorf("viewer not found: %s", name)
	}
	viewer.Unsubscribe(ch)
	return nil
}

// monitorErrorsConfig monitors config-based viewer errors and emits events.
func (m *Manager) monitorErrorsConfig(ctx context.Context, name string, viewer *Viewer) {
	defer m.configMonitorWg.Done()
	m.monitorErrorsImpl(ctx, name, viewer)
}

// monitorErrorsService monitors service viewer errors and emits events.
func (m *Manager) monitorErrorsService(ctx context.Context, name string, viewer *Viewer) {
	defer m.serviceMonitorWg.Done()
	m.monitorErrorsImpl(ctx, name, viewer)
}

// monitorErrorsImpl is the shared implementation for monitoring viewer errors.
func (m *Manager) monitorErrorsImpl(ctx context.Context, name string, viewer *Viewer) {
	wasConnected := true // Assume connected on start

	for {
		select {
		case <-ctx.Done():
			// Emit disconnected event on shutdown
			if wasConnected {
				m.emitEvent("log.disconnected", map[string]any{
					"viewer": viewer.Name(),
					"source": viewer.source.Name(),
				})
			}
			return
		case err, ok := <-viewer.Errors():
			if !ok {
				// Channel closed, viewer is stopping
				if wasConnected {
					m.emitEvent("log.disconnected", map[string]any{
						"viewer": viewer.Name(),
						"source": viewer.source.Name(),
					})
				}
				return
			}
			log.Printf("Log viewer %s error: %v", viewer.Name(), err)
			m.emitEvent("log.error", map[string]any{
				"viewer": viewer.Name(),
				"error":  err.Error(),
			})

			// Check if source is now disconnected
			status := viewer.source.Status()
			if !status.Connected && wasConnected {
				wasConnected = false
				m.emitEvent("log.disconnected", map[string]any{
					"viewer": viewer.Name(),
					"source": viewer.source.Name(),
					"error":  err.Error(),
				})
			} else if status.Connected && !wasConnected {
				wasConnected = true
				m.emitEvent("log.connected", map[string]any{
					"viewer": viewer.Name(),
					"source": viewer.source.Name(),
				})
			}
		}
	}
}

// emitEvent publishes an event to the event bus.
func (m *Manager) emitEvent(eventType string, payload map[string]any) {
	if m.bus == nil {
		return
	}
	event := events.Event{
		Type:    eventType,
		Payload: payload,
	}
	if err := m.bus.Publish(context.Background(), event); err != nil {
		log.Printf("Failed to publish %s event: %v", eventType, err)
	}
}

// cleanupLoop periodically checks for and stops idle viewers.
func (m *Manager) cleanupLoop(ctx context.Context) {
	// Check every minute (or half the idle timeout, whichever is smaller)
	interval := time.Minute
	if m.idleTimeout < 2*time.Minute {
		interval = m.idleTimeout / 2
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.stopIdleViewers()
		}
	}
}

// stopIdleViewers stops viewers that have been idle longer than the timeout
// and have no active subscribers.
func (m *Manager) stopIdleViewers() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for name, viewer := range m.viewers {
		// Skip if not running
		if _, running := m.monitorCancel[name]; !running {
			continue
		}

		// Skip if has active subscribers
		if viewer.SubscriberCount() > 0 {
			continue
		}

		// Check if idle
		lastAccessed := viewer.LastAccessed()
		if lastAccessed.IsZero() || now.Sub(lastAccessed) < m.idleTimeout {
			continue
		}

		// Stop the idle viewer
		log.Printf("Stopping idle log viewer %s (last accessed: %v ago)", name, now.Sub(lastAccessed).Round(time.Second))

		// Cancel monitor goroutine
		if cancel, ok := m.monitorCancel[name]; ok {
			cancel()
			delete(m.monitorCancel, name)
		}

		// Stop viewer
		if err := viewer.Stop(); err != nil {
			log.Printf("Failed to stop idle viewer %s: %v", name, err)
		}

		m.emitEvent("log.disconnected", map[string]any{
			"viewer": name,
			"source": viewer.source.Name(),
			"reason": "idle timeout",
		})
	}
}
