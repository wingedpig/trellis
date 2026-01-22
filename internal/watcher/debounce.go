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
func (d *Debouncer) Debounce(key string, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Cancel existing timer if any
	if timer, exists := d.timers[key]; exists {
		timer.Stop()
	}

	// Create new timer
	d.timers[key] = time.AfterFunc(d.duration, func() {
		d.mu.Lock()
		delete(d.timers, key)
		d.mu.Unlock()
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

// Stop cancels all pending debounced functions.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for key, timer := range d.timers {
		timer.Stop()
		delete(d.timers, key)
	}
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
