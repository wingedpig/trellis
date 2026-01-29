// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/logs"
)

// ServiceManager manages multiple services.
type ServiceManager struct {
	mu       sync.RWMutex
	services map[string]*managedService
	bus      events.EventBus
	analyzer *CrashAnalyzer
}

type managedService struct {
	config        config.ServiceConfig
	process       *Process
	restartCount  int
	enabled       bool
	restartTimer  *time.Timer // pending auto-restart timer
}

// NewManager creates a new service manager.
func NewManager(configs []config.ServiceConfig, bus events.EventBus, _ interface{}) *ServiceManager {
	mgr := &ServiceManager{
		services: make(map[string]*managedService),
		bus:      bus,
		analyzer: NewCrashAnalyzer(),
	}

	for _, cfg := range configs {
		svc := &managedService{
			config:  cfg,
			process: NewProcess(cfg, bus),
			enabled: cfg.IsEnabled(),
		}
		mgr.services[cfg.Name] = svc
	}

	return mgr
}

// Start starts a service by name.
func (m *ServiceManager) Start(ctx context.Context, name string) error {
	return m.startInternal(ctx, name, make(map[string]bool), true)
}

// startInternal starts a service with dependency cycle detection.
// emitEvent controls whether to emit service.started event (false for restarts).
func (m *ServiceManager) startInternal(ctx context.Context, name string, visiting map[string]bool, emitEvent bool) error {
	// Check for dependency cycle
	if visiting[name] {
		return fmt.Errorf("dependency cycle detected: service %q depends on itself", name)
	}
	visiting[name] = true
	defer delete(visiting, name)

	m.mu.Lock()
	svc, ok := m.services[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("service %q not found", name)
	}

	// If already running, return success (idempotent)
	if svc.process.Status().State == StatusRunning {
		m.mu.Unlock()
		return nil
	}

	// Cancel any pending auto-restart timer
	if svc.restartTimer != nil {
		svc.restartTimer.Stop()
		svc.restartTimer = nil
	}

	// Capture process and config references while holding lock to avoid races
	// with UpdateConfigs which can swap these out
	proc := svc.process
	deps := make([]string, len(svc.config.DependsOn))
	copy(deps, svc.config.DependsOn)

	// Capture restart policy config for the exit handler
	restartPolicy := svc.config.GetRestartPolicy()
	maxRestarts := svc.config.MaxRestarts
	if maxRestarts == 0 {
		maxRestarts = 5
	}
	restartDelay := 1 * time.Second
	if svc.config.RestartDelay != "" {
		if d, err := time.ParseDuration(svc.config.RestartDelay); err == nil {
			restartDelay = d
		}
	}
	m.mu.Unlock()

	// Start dependencies first (with cycle detection)
	for _, dep := range deps {
		// Look up the dependency config to check if it's enabled
		m.mu.Lock()
		depSvc, depExists := m.services[dep]
		m.mu.Unlock()

		if !depExists {
			return fmt.Errorf("dependency %q not found", dep)
		}

		// Skip disabled dependencies (don't fail, just skip)
		if !depSvc.config.IsEnabled() {
			log.Printf("Service %s: skipping disabled dependency %s", name, dep)
			continue
		}

		if err := m.startInternal(ctx, dep, visiting, emitEvent); err != nil {
			return fmt.Errorf("failed to start dependency %q: %w", dep, err)
		}
	}

	// Set up exit handler for restart policy
	// Pass the process reference and config snapshot so handleExit uses the correct
	// values even if UpdateConfigs swaps them out before the process exits
	proc.OnExit(func(exitCode int) {
		m.handleExit(name, exitCode, proc, restartPolicy, maxRestarts, restartDelay)
	})

	if err := proc.Start(ctx); err != nil {
		log.Printf("Service %s failed to start: %v", name, err)
		return err
	}

	log.Printf("Service %s started (PID %d)", name, proc.Status().PID)

	// Publish event (unless suppressed for restart)
	if m.bus != nil && emitEvent {
		m.bus.Publish(ctx, events.Event{
			Type: events.EventServiceStarted,
			Payload: map[string]interface{}{
				"service": name,
				"pid":     proc.Status().PID,
			},
		})
	}

	return nil
}

// stoppingTracker tracks services being stopped with thread-safe access.
// Used to prevent duplicate stops when StopAll runs parallel goroutines
// that may recursively stop the same dependent services.
type stoppingTracker struct {
	mu       sync.Mutex
	stopping map[string]bool
}

func newStoppingTracker() *stoppingTracker {
	return &stoppingTracker{stopping: make(map[string]bool)}
}

// markStopping marks a service as being stopped. Returns false if already being stopped.
func (t *stoppingTracker) markStopping(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopping[name] {
		return false
	}
	t.stopping[name] = true
	return true
}

// Stop stops a service by name.
func (m *ServiceManager) Stop(ctx context.Context, name string) error {
	return m.stopInternal(ctx, name, newStoppingTracker())
}

// stopInternal stops a service with cycle detection.
// The tracker prevents infinite recursion when circular dependencies exist
// (e.g., A depends on B, B depends on A) and duplicate stops when StopAll
// runs parallel goroutines that recursively stop the same dependent services.
func (m *ServiceManager) stopInternal(ctx context.Context, name string, tracker *stoppingTracker) error {
	// Check for cycle or already being stopped - if so, skip
	if !tracker.markStopping(name) {
		return nil
	}

	m.mu.Lock()
	svc, ok := m.services[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("service %q not found", name)
	}

	// Cancel any pending auto-restart timer
	if svc.restartTimer != nil {
		svc.restartTimer.Stop()
		svc.restartTimer = nil
	}
	m.mu.Unlock()

	// Stop dependents first (services that depend on this one)
	// Collect errors from dependent stops
	var dependentErrors []error
	m.mu.RLock()
	for svcName, s := range m.services {
		for _, dep := range s.config.DependsOn {
			if dep == name && s.process.Status().State == StatusRunning {
				m.mu.RUnlock()
				if err := m.stopInternal(ctx, svcName, tracker); err != nil {
					dependentErrors = append(dependentErrors, fmt.Errorf("dependent %s: %w", svcName, err))
				}
				m.mu.RLock()
				break
			}
		}
	}
	m.mu.RUnlock()

	// If any dependent failed to stop, return error before attempting to stop this service
	if len(dependentErrors) > 0 {
		return fmt.Errorf("failed to stop %d dependent(s): %v", len(dependentErrors), dependentErrors[0])
	}

	// Check if service was running before stop (to avoid duplicate events)
	wasRunning := svc.process.Status().State == StatusRunning

	err := svc.process.Stop(ctx)

	// Only publish event if the service was actually running and stopped successfully
	if m.bus != nil && err == nil && wasRunning {
		m.bus.Publish(ctx, events.Event{
			Type: events.EventServiceStopped,
			Payload: map[string]interface{}{
				"service":  name,
				"exitCode": svc.process.Status().ExitCode,
			},
		})
	}

	return err
}

// Restart restarts a service.
func (m *ServiceManager) Restart(ctx context.Context, name string, trigger RestartTrigger) error {
	m.mu.Lock()
	svc, ok := m.services[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("service %q not found", name)
	}
	m.mu.Unlock()

	// Stop if running
	if svc.process.Status().State == StatusRunning {
		if err := m.Stop(ctx, name); err != nil {
			return err
		}
	}

	// Clear restart count on manual restart
	if trigger == RestartManual {
		m.mu.Lock()
		svc.restartCount = 0
		m.mu.Unlock()
	}

	// Start (without emitting service.started - we'll emit service.restarted instead)
	if err := m.startInternal(ctx, name, make(map[string]bool), false); err != nil {
		return err
	}

	// Publish restart event (only this one, not service.started)
	if m.bus != nil {
		m.bus.Publish(ctx, events.Event{
			Type: events.EventServiceRestarted,
			Payload: map[string]interface{}{
				"service": name,
				"trigger": trigger.String(),
			},
		})
	}

	return nil
}

// Status returns the status of a service.
func (m *ServiceManager) Status(name string) (ServiceStatus, error) {
	m.mu.RLock()
	svc, ok := m.services[name]
	m.mu.RUnlock()

	if !ok {
		return ServiceStatus{}, fmt.Errorf("service %q not found", name)
	}

	status := svc.process.Status()
	status.RestartCount = svc.restartCount
	return status, nil
}

// Logs returns the log lines for a service.
func (m *ServiceManager) Logs(name string, lines int) ([]string, error) {
	m.mu.RLock()
	svc, ok := m.services[name]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("service %q not found", name)
	}

	return svc.process.Logs(lines), nil
}

// LogSize returns the number of lines in the service's log buffer.
func (m *ServiceManager) LogSize(name string) (int, error) {
	m.mu.RLock()
	svc, ok := m.services[name]
	m.mu.RUnlock()

	if !ok {
		return 0, fmt.Errorf("service %q not found", name)
	}

	return svc.process.LogSize(), nil
}

// ParsedLogs returns parsed log entries for a service.
func (m *ServiceManager) ParsedLogs(name string, lines int) ([]*logs.LogEntry, error) {
	m.mu.RLock()
	svc, ok := m.services[name]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("service %q not found", name)
	}

	return svc.process.ParsedLogs(lines), nil
}

// HasParser returns true if the service has a log parser configured.
func (m *ServiceManager) HasParser(name string) bool {
	m.mu.RLock()
	svc, ok := m.services[name]
	m.mu.RUnlock()

	if !ok {
		return false
	}

	return svc.process.HasParser()
}

// ClearLogs clears logs for a specific service.
func (m *ServiceManager) ClearLogs(name string) error {
	m.mu.RLock()
	svc, ok := m.services[name]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("service %q not found", name)
	}

	svc.process.ClearLogs()
	return nil
}

// SubscribeLogs returns a channel that receives new log lines for a service.
func (m *ServiceManager) SubscribeLogs(name string) (chan LogLine, error) {
	m.mu.RLock()
	svc, ok := m.services[name]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("service %q not found", name)
	}

	return svc.process.SubscribeLogs(), nil
}

// UnsubscribeLogs removes a log subscription for a service.
func (m *ServiceManager) UnsubscribeLogs(name string, ch chan LogLine) {
	m.mu.RLock()
	svc, ok := m.services[name]
	m.mu.RUnlock()

	if !ok {
		return
	}

	svc.process.UnsubscribeLogs(ch)
}

// List returns information about all services.
func (m *ServiceManager) List() []ServiceInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ServiceInfo, 0, len(m.services))
	for name, svc := range m.services {
		status := svc.process.Status()
		status.RestartCount = svc.restartCount
		info := ServiceInfo{
			Name:    name,
			Status:  status,
			Enabled: svc.enabled,
		}
		// Include logging configuration
		if svc.config.Logging.Parser.Type != "" {
			info.ParserType = svc.config.Logging.Parser.Type
			info.TimestampField = svc.config.Logging.Parser.Timestamp
			info.LevelField = svc.config.Logging.Parser.Level
			info.MessageField = svc.config.Logging.Parser.Message
		}
		if len(svc.config.Logging.Layout) > 0 {
			info.Layout = svc.config.Logging.Layout
		}
		if cols := svc.config.Logging.GetColumns(); len(cols) > 0 {
			info.Columns = cols
			info.ColumnWidths = svc.config.Logging.GetColumnWidths()
		}
		result = append(result, info)
	}
	return result
}

// StartAll starts all enabled services.
func (m *ServiceManager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	names := make([]string, 0, len(m.services))
	for name, svc := range m.services {
		if svc.enabled {
			names = append(names, name)
		}
	}
	m.mu.RUnlock()

	// Start in dependency order
	started := make(map[string]bool)
	var startOrder []string

	// Simple topological sort
	for len(started) < len(names) {
		madeProgress := false
		for _, name := range names {
			if started[name] {
				continue
			}

			m.mu.RLock()
			svc := m.services[name]
			m.mu.RUnlock()

			canStart := true
			for _, dep := range svc.config.DependsOn {
				if !started[dep] {
					canStart = false
					break
				}
			}

			if canStart {
				startOrder = append(startOrder, name)
				started[name] = true
				madeProgress = true
			}
		}

		if !madeProgress {
			// Circular dependency or missing dependency, start remaining
			for _, name := range names {
				if !started[name] {
					startOrder = append(startOrder, name)
					started[name] = true
				}
			}
		}
	}

	// Start services in order
	var errs []error
	for _, name := range startOrder {
		if err := m.Start(ctx, name); err != nil {
			// Log error but continue with other services
			log.Printf("Failed to start service %s: %v", name, err)
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to start %d service(s): %v", len(errs), errs[0])
	}
	return nil
}

// StopAll stops all running services.
func (m *ServiceManager) StopAll(ctx context.Context) error {
	m.mu.RLock()
	names := make([]string, 0, len(m.services))
	for name, svc := range m.services {
		if svc.process.Status().State == StatusRunning {
			names = append(names, name)
		}
	}
	m.mu.RUnlock()

	// Use shared tracker to prevent duplicate stops when parallel goroutines
	// recursively stop the same dependent services
	tracker := newStoppingTracker()

	// Stop in parallel
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var errs []error

	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if err := m.stopInternal(ctx, n, tracker); err != nil {
				errMu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", n, err))
				errMu.Unlock()
			}
		}(name)
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("failed to stop %d service(s): %v", len(errs), errs[0])
	}
	return nil
}

// StopWatched stops all running services that have watching enabled.
// Services with watching: false are not stopped.
func (m *ServiceManager) StopWatched(ctx context.Context) error {
	m.mu.RLock()
	names := make([]string, 0, len(m.services))
	for name, svc := range m.services {
		if svc.process.Status().State == StatusRunning && svc.config.IsWatching() {
			names = append(names, name)
		}
	}
	m.mu.RUnlock()

	// Use shared tracker to prevent duplicate stops when parallel goroutines
	// recursively stop the same dependent services
	tracker := newStoppingTracker()

	// Stop in parallel
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var errs []error

	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if err := m.stopInternal(ctx, n, tracker); err != nil {
				errMu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", n, err))
				errMu.Unlock()
			}
		}(name)
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("failed to stop %d service(s): %v", len(errs), errs[0])
	}
	return nil
}

// StartWatched starts all enabled services that have watching enabled.
// Services with watching: false are not started.
func (m *ServiceManager) StartWatched(ctx context.Context) error {
	m.mu.RLock()
	names := make([]string, 0, len(m.services))
	for name, svc := range m.services {
		if svc.enabled && svc.config.IsWatching() {
			names = append(names, name)
		}
	}
	m.mu.RUnlock()

	// Start in dependency order (same logic as StartAll)
	started := make(map[string]bool)
	var startOrder []string

	for len(started) < len(names) {
		progress := false
		for _, name := range names {
			if started[name] {
				continue
			}

			m.mu.RLock()
			svc := m.services[name]
			m.mu.RUnlock()

			// Check if all dependencies are started
			depsReady := true
			for _, dep := range svc.config.DependsOn {
				// Only wait for dependencies that are in our watched set
				isWatchedDep := false
				for _, n := range names {
					if n == dep {
						isWatchedDep = true
						break
					}
				}
				if isWatchedDep && !started[dep] {
					depsReady = false
					break
				}
			}

			if depsReady {
				startOrder = append(startOrder, name)
				started[name] = true
				progress = true
			}
		}

		if !progress && len(started) < len(names) {
			// Circular dependency or missing dependency - add remaining
			for _, name := range names {
				if !started[name] {
					startOrder = append(startOrder, name)
					started[name] = true
				}
			}
		}
	}

	var errs []error
	for _, name := range startOrder {
		if err := m.Start(ctx, name); err != nil {
			log.Printf("Failed to start service %s: %v", name, err)
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to start %d service(s): %v", len(errs), errs[0])
	}
	return nil
}

// GetService returns service info by name.
func (m *ServiceManager) GetService(name string) (ServiceInfo, bool) {
	m.mu.RLock()
	svc, ok := m.services[name]
	m.mu.RUnlock()

	if !ok {
		return ServiceInfo{}, false
	}

	status := svc.process.Status()
	status.RestartCount = svc.restartCount
	info := ServiceInfo{
		Name:    name,
		Status:  status,
		Enabled: svc.enabled,
	}
	// Include logging configuration
	if svc.config.Logging.Parser.Type != "" {
		info.ParserType = svc.config.Logging.Parser.Type
		info.TimestampField = svc.config.Logging.Parser.Timestamp
		info.LevelField = svc.config.Logging.Parser.Level
		info.MessageField = svc.config.Logging.Parser.Message
	}
	if len(svc.config.Logging.Layout) > 0 {
		info.Layout = svc.config.Logging.Layout
	}
	if cols := svc.config.Logging.GetColumns(); len(cols) > 0 {
		info.Columns = cols
		info.ColumnWidths = svc.config.Logging.GetColumnWidths()
	}
	return info, true
}

// handleExit handles process exit with the config snapshot captured at start time.
// This avoids races with UpdateConfigs which can swap the process/config.
func (m *ServiceManager) handleExit(name string, exitCode int, proc *Process, policy string, maxRestarts int, restartDelay time.Duration) {
	m.mu.Lock()
	svc, ok := m.services[name]
	if !ok {
		m.mu.Unlock()
		return
	}

	shouldRestart := false
	switch policy {
	case "always":
		shouldRestart = svc.restartCount < maxRestarts
	case "on-failure":
		shouldRestart = exitCode != 0 && svc.restartCount < maxRestarts
	case "never", "":
		shouldRestart = false
	}

	if shouldRestart {
		svc.restartCount++
	}
	m.mu.Unlock()

	// Get logs from the process that actually exited (passed as parameter),
	// not from the potentially-swapped svc.process
	var logs []string
	if exitCode != 0 {
		logs = proc.Logs(50)
	}

	// Publish event: crashed (unexpected exit) or stopped (clean exit)
	// These are mutually exclusive per spec
	if m.bus != nil {
		if exitCode != 0 {
			// Unexpected exit - publish crashed event
			result := m.analyzer.Analyze(logs, exitCode)

			m.bus.Publish(context.Background(), events.Event{
				Type: events.EventServiceCrashed,
				Payload: map[string]interface{}{
					"service":  name,
					"exitCode": exitCode,
					"reason":   result.Reason.String(),
					"details":  result.Details,
				},
			})
		} else {
			// Clean exit - publish stopped event
			m.bus.Publish(context.Background(), events.Event{
				Type: events.EventServiceStopped,
				Payload: map[string]interface{}{
					"service":  name,
					"exitCode": exitCode,
				},
			})
		}
	}

	// Restart if needed
	if shouldRestart {
		m.mu.Lock()
		// Re-lookup service since UpdateConfigs could have run while we were unlocked
		svc, ok = m.services[name]
		if ok {
			// Cancel any existing timer before setting a new one
			if svc.restartTimer != nil {
				svc.restartTimer.Stop()
			}
			svc.restartTimer = time.AfterFunc(restartDelay, func() {
				m.mu.Lock()
				s, exists := m.services[name]
				if exists {
					s.restartTimer = nil
				}
				// Re-check if service still exists and is enabled at timer fire time.
				// UpdateConfigs may have disabled or removed the service while we waited.
				if !exists || !s.enabled {
					m.mu.Unlock()
					return
				}
				m.mu.Unlock()
				m.Restart(context.Background(), name, RestartCrash)
			})
		}
		m.mu.Unlock()
	}
}

// UpdateConfigs updates the service configurations.
// This is used when switching worktrees to update paths.
// Services should be stopped before calling this.
func (m *ServiceManager) UpdateConfigs(configs []config.ServiceConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Printf("UpdateConfigs called with %d services", len(configs))

	// Create a map of new configs by name
	newConfigs := make(map[string]config.ServiceConfig)
	for _, cfg := range configs {
		newConfigs[cfg.Name] = cfg
	}

	// Update existing services and add new ones
	for name, cfg := range newConfigs {
		if svc, ok := m.services[name]; ok {
			// Update existing service config and create new process
			log.Printf("Updating service %s: old workdir=%s, new workdir=%s", name, svc.config.WorkDir, cfg.WorkDir)
			svc.process.CloseLogSubscribers() // Close orphaned subscribers before replacing
			svc.config = cfg
			svc.process = NewProcess(cfg, m.bus)
			svc.restartCount = 0 // Reset restart count for new worktree
			// Refresh enabled flag from new config
			svc.enabled = cfg.Disabled == nil || !*cfg.Disabled
		} else {
			// Add new service
			enabled := true
			if cfg.Disabled != nil && *cfg.Disabled {
				enabled = false
			}
			m.services[name] = &managedService{
				config:  cfg,
				process: NewProcess(cfg, m.bus),
				enabled: enabled,
			}
		}
	}

	// Remove services that are no longer in config
	for name := range m.services {
		if _, ok := newConfigs[name]; !ok {
			delete(m.services, name)
		}
	}

	log.Printf("Updated service configs: %d services", len(m.services))
}
