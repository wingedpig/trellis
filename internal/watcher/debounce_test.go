// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package watcher

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDebouncer_Basic(t *testing.T) {
	var callCount atomic.Int32

	d := NewDebouncer(50 * time.Millisecond)

	d.Debounce("key1", func() {
		callCount.Add(1)
	})

	// Wait for debounce to fire
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(1), callCount.Load())
}

func TestDebouncer_MultipleCallsSameKey(t *testing.T) {
	var callCount atomic.Int32

	d := NewDebouncer(50 * time.Millisecond)

	// Multiple rapid calls with same key
	for i := 0; i < 10; i++ {
		d.Debounce("key1", func() {
			callCount.Add(1)
		})
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for debounce to fire
	time.Sleep(100 * time.Millisecond)

	// Should only fire once
	assert.Equal(t, int32(1), callCount.Load())
}

func TestDebouncer_DifferentKeys(t *testing.T) {
	var count1, count2 atomic.Int32

	d := NewDebouncer(50 * time.Millisecond)

	d.Debounce("key1", func() {
		count1.Add(1)
	})

	d.Debounce("key2", func() {
		count2.Add(1)
	})

	// Wait for debounce to fire
	time.Sleep(100 * time.Millisecond)

	// Each key should fire independently
	assert.Equal(t, int32(1), count1.Load())
	assert.Equal(t, int32(1), count2.Load())
}

func TestDebouncer_ResetOnCall(t *testing.T) {
	var callCount atomic.Int32

	d := NewDebouncer(50 * time.Millisecond)

	// First call
	d.Debounce("key1", func() {
		callCount.Add(1)
	})

	// Wait 30ms, then call again (resets timer)
	time.Sleep(30 * time.Millisecond)
	d.Debounce("key1", func() {
		callCount.Add(1)
	})

	// Wait 30ms - shouldn't fire yet (only 30ms since last call)
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, int32(0), callCount.Load())

	// Wait another 50ms - should fire now
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), callCount.Load())
}

func TestDebouncer_Cancel(t *testing.T) {
	var callCount atomic.Int32

	d := NewDebouncer(50 * time.Millisecond)

	d.Debounce("key1", func() {
		callCount.Add(1)
	})

	// Cancel before it fires
	d.Cancel("key1")

	// Wait for would-be debounce
	time.Sleep(100 * time.Millisecond)

	// Should not have fired
	assert.Equal(t, int32(0), callCount.Load())
}

func TestDebouncer_CancelNonexistent(t *testing.T) {
	d := NewDebouncer(50 * time.Millisecond)

	// Should not panic
	d.Cancel("nonexistent")
}

func TestDebouncer_Stop(t *testing.T) {
	var callCount atomic.Int32

	d := NewDebouncer(50 * time.Millisecond)

	d.Debounce("key1", func() {
		callCount.Add(1)
	})
	d.Debounce("key2", func() {
		callCount.Add(1)
	})

	// Stop all pending
	d.Stop()

	// Wait for would-be debounce
	time.Sleep(100 * time.Millisecond)

	// None should have fired
	assert.Equal(t, int32(0), callCount.Load())
}

func TestDebouncer_SetDuration(t *testing.T) {
	var callCount atomic.Int32

	d := NewDebouncer(100 * time.Millisecond)

	d.Debounce("key1", func() {
		callCount.Add(1)
	})

	// Change duration to shorter
	d.SetDuration(20 * time.Millisecond)

	// New debounce should use new duration
	d.Debounce("key2", func() {
		callCount.Add(1)
	})

	// Wait for shorter debounce
	time.Sleep(50 * time.Millisecond)

	// key2 should have fired (20ms), key1 still pending (100ms)
	assert.Equal(t, int32(1), callCount.Load())

	// Wait for key1
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(2), callCount.Load())
}

func TestDebouncer_Concurrency(t *testing.T) {
	var callCount atomic.Int32

	d := NewDebouncer(20 * time.Millisecond)
	done := make(chan bool, 100)

	// Concurrent calls
	for i := 0; i < 100; i++ {
		go func(key int) {
			d.Debounce("key", func() {
				callCount.Add(1)
			})
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}

	// Wait for debounce
	time.Sleep(50 * time.Millisecond)

	// Should only fire once (all same key)
	assert.Equal(t, int32(1), callCount.Load())
}

func TestDebouncer_LatestCallback(t *testing.T) {
	var value atomic.Int32

	d := NewDebouncer(50 * time.Millisecond)

	// Multiple calls with different values - only last should be used
	for i := 1; i <= 5; i++ {
		final := int32(i)
		d.Debounce("key", func() {
			value.Store(final)
		})
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for debounce
	time.Sleep(100 * time.Millisecond)

	// Should have the value from the last call
	assert.Equal(t, int32(5), value.Load())
}

func TestDebouncer_ZeroDuration(t *testing.T) {
	var callCount atomic.Int32

	// Zero duration should use default
	d := NewDebouncer(0)

	d.Debounce("key", func() {
		callCount.Add(1)
	})

	// Should still debounce with default duration
	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, int32(0), callCount.Load())

	// Wait longer for default
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), callCount.Load())
}

func TestDebouncer_NegativeDuration(t *testing.T) {
	var callCount atomic.Int32

	// Negative duration should use default
	d := NewDebouncer(-100 * time.Millisecond)

	d.Debounce("key", func() {
		callCount.Add(1)
	})

	// Wait for default
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), callCount.Load())
}
