// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package watcher

import (
	"sync"
	"time"
)

const defaultDebounceDuration = 100 * time.Millisecond

// Debouncer provides debounced function execution.
type Debouncer struct {
	mu       sync.Mutex
	duration time.Duration
	timers   map[string]*time.Timer
	stopped  bool
	running  sync.WaitGroup // in-flight callbacks
}

// NewDebouncer creates a new debouncer with the given duration.
func NewDebouncer(duration time.Duration) *Debouncer {
	if duration <= 0 {
		duration = defaultDebounceDuration
	}
	return &Debouncer{
		duration: duration,
		timers:   make(map[string]*time.Timer),
	}
}

// Debounce schedules a function to be called after the debounce duration.
// If called again with the same key before the duration elapses, the timer is reset.
// After Stop, Debounce is a no-op.
func (d *Debouncer) Debounce(key string, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.stopped {
		return
	}

	// Cancel existing timer if any
	if timer, exists := d.timers[key]; exists {
		timer.Stop()
	}

	// Create new timer
	d.timers[key] = time.AfterFunc(d.duration, func() {
		d.mu.Lock()
		if d.stopped {
			d.mu.Unlock()
			return
		}
		delete(d.timers, key)
		// Track the callback so Stop can wait for it; Add happens under
		// d.mu strictly before stopped is set, so it can't race Stop's Wait.
		d.running.Add(1)
		d.mu.Unlock()
		defer d.running.Done()
		fn()
	})
}

// Cancel cancels a pending debounced function for the given key.
func (d *Debouncer) Cancel(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if timer, exists := d.timers[key]; exists {
		timer.Stop()
		delete(d.timers, key)
	}
}

// Stop cancels all pending debounced functions, waits for any in-flight
// callbacks to finish, and marks the debouncer terminally stopped (further
// Debounce calls are no-ops). This guarantees no callback runs after Stop
// returns.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	d.stopped = true
	for key, timer := range d.timers {
		timer.Stop()
		delete(d.timers, key)
	}
	d.mu.Unlock()

	d.running.Wait()
}

// SetDuration changes the debounce duration for future debounces.
// Existing timers are not affected.
func (d *Debouncer) SetDuration(duration time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if duration <= 0 {
		duration = defaultDebounceDuration
	}
	d.duration = duration
}
