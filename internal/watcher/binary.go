// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/wingedpig/trellis/internal/events"
)

// BinaryWatcher watches binary files for changes and emits events.
type BinaryWatcher struct {
	mu            sync.RWMutex
	bus           events.EventBus
	watcher       *fsnotify.Watcher
	debouncer     *Debouncer
	watches       map[string][]string  // service name -> watched paths
	pathToService map[string]string    // path -> service name (reverse lookup)
	paths         map[string]int       // path -> watch count (for ref counting)
	lastRestart   map[string]time.Time // service name -> last restart time (cooldown)
	closed        bool
	closeCh       chan struct{}
	wg            sync.WaitGroup
}

// NewBinaryWatcher creates a new binary watcher.
func NewBinaryWatcher(bus events.EventBus, debounce time.Duration) (*BinaryWatcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	w := &BinaryWatcher{
		bus:           bus,
		watcher:       fsWatcher,
		debouncer:     NewDebouncer(debounce),
		watches:       make(map[string][]string),
		pathToService: make(map[string]string),
		paths:         make(map[string]int),
		lastRestart:   make(map[string]time.Time),
		closeCh:       make(chan struct{}),
	}

	// Start event processing
	w.wg.Add(1)
	go w.processEvents()

	return w, nil
}

// Watch starts watching files for changes for a service.
// The paths slice should include the binary and any additional config files.
func (w *BinaryWatcher) Watch(serviceName string, paths []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("watcher is closed")
	}

	if len(paths) == 0 {
		return nil
	}

	// If already watching this service, unwatch old paths first
	if oldPaths, exists := w.watches[serviceName]; exists {
		for _, oldPath := range oldPaths {
			w.removeWatch(oldPath)
			delete(w.pathToService, oldPath)
		}
	}

	// Resolve and watch each path
	var absPaths []string
	for _, p := range paths {
		absPath, err := filepath.Abs(p)
		if err != nil {
			absPath = p
		}

		if err := w.addWatch(absPath); err != nil {
			// Log warning but continue with other paths
			continue
		}

		absPaths = append(absPaths, absPath)
		w.pathToService[absPath] = serviceName
	}

	if len(absPaths) > 0 {
		w.watches[serviceName] = absPaths
	}
	return nil
}

// Unwatch stops watching files for a service.
func (w *BinaryWatcher) Unwatch(serviceName string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	paths, exists := w.watches[serviceName]
	if !exists {
		return fmt.Errorf("service %s not being watched", serviceName)
	}

	for _, path := range paths {
		w.removeWatch(path)
		delete(w.pathToService, path)
	}
	delete(w.watches, serviceName)
	w.debouncer.Cancel(serviceName)

	return nil
}

// SetDebounce sets the debounce duration.
func (w *BinaryWatcher) SetDebounce(d time.Duration) {
	w.debouncer.SetDuration(d)
}

// Watching returns the list of services being watched.
func (w *BinaryWatcher) Watching() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make([]string, 0, len(w.watches))
	for svc := range w.watches {
		result = append(result, svc)
	}
	return result
}

// Close stops the watcher and releases resources.
func (w *BinaryWatcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	close(w.closeCh)
	w.mu.Unlock()

	w.debouncer.Stop()
	w.watcher.Close()
	w.wg.Wait()

	return nil
}

func (w *BinaryWatcher) addWatch(path string) error {
	w.paths[path]++
	if w.paths[path] == 1 {
		// First watch on this path
		if err := w.watcher.Add(path); err != nil {
			w.paths[path]--
			if w.paths[path] == 0 {
				delete(w.paths, path)
			}
			return err
		}
	}
	return nil
}

func (w *BinaryWatcher) removeWatch(path string) {
	w.paths[path]--
	if w.paths[path] <= 0 {
		w.watcher.Remove(path)
		delete(w.paths, path)
	}
}

func (w *BinaryWatcher) processEvents() {
	defer w.wg.Done()

	for {
		select {
		case <-w.closeCh:
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			// Log error but continue
			_ = err
		}
	}
}

func (w *BinaryWatcher) handleEvent(event fsnotify.Event) {
	// Only care about writes and creates - NOT chmod
	// Chmod events fire when binaries are executed, causing restart loops
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
		return
	}

	// Look up which service this file belongs to
	w.mu.RLock()
	serviceName, exists := w.pathToService[event.Name]
	w.mu.RUnlock()

	if exists {
		w.triggerChange(serviceName, event.Name)
	}
}

const restartCooldown = 5 * time.Second

func (w *BinaryWatcher) triggerChange(serviceName string, changedPath string) {
	w.debouncer.Debounce(serviceName, func() {
		w.mu.Lock()
		lastRestart := w.lastRestart[serviceName]

		// Cooldown: ignore events within 5 seconds of last restart
		if time.Since(lastRestart) < restartCooldown {
			w.mu.Unlock()
			return
		}
		w.lastRestart[serviceName] = time.Now()
		w.mu.Unlock()

		// Check file exists and get info
		info, err := os.Stat(changedPath)
		var modTime time.Time
		if err == nil {
			modTime = info.ModTime()
		}

		if w.bus != nil {
			w.bus.Publish(context.Background(), events.Event{
				Type: "binary.changed",
				Payload: map[string]interface{}{
					"service":    serviceName,
					"path":       changedPath,
					"modTime":    modTime,
					"modTimeStr": modTime.Format(time.RFC3339),
				},
			})
		}
	})
}
