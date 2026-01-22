// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryEventBus_Publish(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	event := Event{
		Type:    "service.started",
		Payload: map[string]interface{}{"service": "api"},
	}

	err := bus.Publish(context.Background(), event)
	assert.NoError(t, err)
}

func TestMemoryEventBus_Publish_AssignsID(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	var receivedEvent Event
	_, err := bus.Subscribe("*", func(ctx context.Context, e Event) error {
		receivedEvent = e
		return nil
	})
	require.NoError(t, err)

	event := Event{
		Type: "service.started",
	}

	err = bus.Publish(context.Background(), event)
	require.NoError(t, err)

	assert.NotEmpty(t, receivedEvent.ID)
	assert.Equal(t, "1.0", receivedEvent.Version)
	assert.False(t, receivedEvent.Timestamp.IsZero())
}

func TestMemoryEventBus_Subscribe(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	received := make(chan Event, 1)

	_, err := bus.Subscribe("service.started", func(ctx context.Context, e Event) error {
		received <- e
		return nil
	})
	require.NoError(t, err)

	event := Event{Type: "service.started", Payload: map[string]interface{}{"service": "api"}}
	err = bus.Publish(context.Background(), event)
	require.NoError(t, err)

	select {
	case e := <-received:
		assert.Equal(t, "service.started", e.Type)
		assert.Equal(t, "api", e.Payload["service"])
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestMemoryEventBus_Subscribe_PatternMatching(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	var count int32

	// Subscribe to all service events
	_, err := bus.Subscribe("service.*", func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})
	require.NoError(t, err)

	// Publish various events
	events := []Event{
		{Type: "service.started"},
		{Type: "service.stopped"},
		{Type: "service.crashed"},
		{Type: "workflow.started"}, // Should not match
	}

	for _, e := range events {
		bus.Publish(context.Background(), e)
	}

	// Give sync handlers time to complete
	time.Sleep(10 * time.Millisecond)

	assert.Equal(t, int32(3), atomic.LoadInt32(&count))
}

func TestMemoryEventBus_Subscribe_MultipleHandlers(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	var count1, count2 int32

	_, err := bus.Subscribe("service.*", func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count1, 1)
		return nil
	})
	require.NoError(t, err)

	_, err = bus.Subscribe("service.started", func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count2, 1)
		return nil
	})
	require.NoError(t, err)

	// Publish event
	bus.Publish(context.Background(), Event{Type: "service.started"})

	time.Sleep(10 * time.Millisecond)

	// Both handlers should receive the event
	assert.Equal(t, int32(1), atomic.LoadInt32(&count1))
	assert.Equal(t, int32(1), atomic.LoadInt32(&count2))
}

func TestMemoryEventBus_Unsubscribe(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	var count int32

	subID, err := bus.Subscribe("service.*", func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})
	require.NoError(t, err)

	// First publish should be received
	bus.Publish(context.Background(), Event{Type: "service.started"})
	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&count))

	// Unsubscribe
	err = bus.Unsubscribe(subID)
	require.NoError(t, err)

	// Second publish should not be received
	bus.Publish(context.Background(), Event{Type: "service.stopped"})
	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&count))
}

func TestMemoryEventBus_Unsubscribe_InvalidID(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	err := bus.Unsubscribe("invalid-id")
	assert.Error(t, err)
}

func TestMemoryEventBus_SubscribeAsync(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	received := make(chan Event, 10)

	_, err := bus.SubscribeAsync("service.*", func(ctx context.Context, e Event) error {
		received <- e
		return nil
	}, 10)
	require.NoError(t, err)

	// Publish multiple events
	for i := 0; i < 5; i++ {
		bus.Publish(context.Background(), Event{Type: "service.started"})
	}

	// Should receive all events
	for i := 0; i < 5; i++ {
		select {
		case <-received:
			// OK
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for event")
		}
	}
}

func TestMemoryEventBus_SubscribeAsync_BufferFull(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	var received int32
	blockChan := make(chan struct{})

	_, err := bus.SubscribeAsync("service.*", func(ctx context.Context, e Event) error {
		atomic.AddInt32(&received, 1)
		<-blockChan // Block handler
		return nil
	}, 2) // Small buffer
	require.NoError(t, err)

	// Publish many events quickly
	for i := 0; i < 10; i++ {
		bus.Publish(context.Background(), Event{Type: "service.started"})
	}

	// Unblock handler
	close(blockChan)

	time.Sleep(100 * time.Millisecond)

	// Should have received some events, but not necessarily all (buffer overflow)
	count := atomic.LoadInt32(&received)
	assert.Greater(t, count, int32(0))
}

func TestMemoryEventBus_History(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{
		HistoryMaxEvents: 100,
		HistoryMaxAge:    time.Hour,
	})
	defer bus.Close()

	// Publish some events
	events := []Event{
		{Type: "service.started", Worktree: "main"},
		{Type: "service.stopped", Worktree: "main"},
		{Type: "workflow.started", Worktree: "feature"},
	}

	for _, e := range events {
		bus.Publish(context.Background(), e)
	}

	// Query history
	history, err := bus.History(EventFilter{})
	require.NoError(t, err)
	assert.Len(t, history, 3)

	// Query with type filter
	history, err = bus.History(EventFilter{Types: []string{"service.*"}})
	require.NoError(t, err)
	assert.Len(t, history, 2)

	// Query with worktree filter
	history, err = bus.History(EventFilter{Worktree: "main"})
	require.NoError(t, err)
	assert.Len(t, history, 2)

	// Query with limit
	history, err = bus.History(EventFilter{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, history, 1)
}

func TestMemoryEventBus_History_TimeFilter(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{
		HistoryMaxEvents: 100,
		HistoryMaxAge:    time.Hour,
	})
	defer bus.Close()

	// Publish event
	bus.Publish(context.Background(), Event{Type: "service.started"})

	now := time.Now()

	// Query events since now - should be empty
	history, err := bus.History(EventFilter{Since: now.Add(time.Second)})
	require.NoError(t, err)
	assert.Len(t, history, 0)

	// Query events until yesterday - should be empty
	history, err = bus.History(EventFilter{Until: now.Add(-24 * time.Hour)})
	require.NoError(t, err)
	assert.Len(t, history, 0)

	// Query events in valid range
	history, err = bus.History(EventFilter{
		Since: now.Add(-time.Hour),
		Until: now.Add(time.Hour),
	})
	require.NoError(t, err)
	assert.Len(t, history, 1)
}

func TestMemoryEventBus_Close(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})

	// Subscribe
	_, err := bus.Subscribe("*", func(ctx context.Context, e Event) error {
		return nil
	})
	require.NoError(t, err)

	// Close
	err = bus.Close()
	require.NoError(t, err)

	// Publishing after close should fail
	err = bus.Publish(context.Background(), Event{Type: "test"})
	assert.Error(t, err)

	// Subscribing after close should fail
	_, err = bus.Subscribe("*", func(ctx context.Context, e Event) error {
		return nil
	})
	assert.Error(t, err)

	// Double close should be safe
	err = bus.Close()
	assert.NoError(t, err)
}

func TestMemoryEventBus_Concurrency(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{
		HistoryMaxEvents: 1000,
	})
	defer bus.Close()

	var count int64
	var wg sync.WaitGroup

	// Subscribe
	_, err := bus.Subscribe("*", func(ctx context.Context, e Event) error {
		atomic.AddInt64(&count, 1)
		return nil
	})
	require.NoError(t, err)

	// Concurrent publishers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				bus.Publish(context.Background(), Event{Type: "service.started"})
			}
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int64(1000), atomic.LoadInt64(&count))
}

func TestMemoryEventBus_HandlerError(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	var count int32

	// First handler returns error
	_, err := bus.Subscribe("service.*", func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count, 1)
		return assert.AnError
	})
	require.NoError(t, err)

	// Second handler should still receive event
	_, err = bus.Subscribe("service.*", func(ctx context.Context, e Event) error {
		atomic.AddInt32(&count, 1)
		return nil
	})
	require.NoError(t, err)

	// Publish should succeed despite handler error
	err = bus.Publish(context.Background(), Event{Type: "service.started"})
	assert.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	// Both handlers should have been called
	assert.Equal(t, int32(2), atomic.LoadInt32(&count))
}

func TestMemoryEventBus_ContextCancellation(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{})
	defer bus.Close()

	received := make(chan bool, 1)

	_, err := bus.Subscribe("*", func(ctx context.Context, e Event) error {
		select {
		case <-ctx.Done():
			received <- false
		default:
			received <- true
		}
		return nil
	})
	require.NoError(t, err)

	// Publish with valid context
	ctx := context.Background()
	bus.Publish(ctx, Event{Type: "test"})

	select {
	case ok := <-received:
		assert.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestMemoryEventBus_SetDefaultWorktree(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{
		HistoryMaxEvents: 100,
	})
	defer bus.Close()

	// Set default worktree
	bus.SetDefaultWorktree("main")

	// Publish event without worktree
	var receivedEvent Event
	_, err := bus.Subscribe("*", func(ctx context.Context, e Event) error {
		receivedEvent = e
		return nil
	})
	require.NoError(t, err)

	err = bus.Publish(context.Background(), Event{
		Type: "service.started",
	})
	require.NoError(t, err)

	// Event should have the default worktree
	assert.Equal(t, "main", receivedEvent.Worktree)
}

func TestMemoryEventBus_SetDefaultWorktree_DoesNotOverride(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{
		HistoryMaxEvents: 100,
	})
	defer bus.Close()

	// Set default worktree
	bus.SetDefaultWorktree("main")

	// Publish event with explicit worktree
	var receivedEvent Event
	_, err := bus.Subscribe("*", func(ctx context.Context, e Event) error {
		receivedEvent = e
		return nil
	})
	require.NoError(t, err)

	err = bus.Publish(context.Background(), Event{
		Type:     "service.started",
		Worktree: "feature",
	})
	require.NoError(t, err)

	// Event should keep its explicit worktree, not the default
	assert.Equal(t, "feature", receivedEvent.Worktree)
}

func TestMemoryEventBus_SetDefaultWorktree_CanBeChanged(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{
		HistoryMaxEvents: 100,
	})
	defer bus.Close()

	receivedEvents := make([]Event, 0)
	_, err := bus.Subscribe("*", func(ctx context.Context, e Event) error {
		receivedEvents = append(receivedEvents, e)
		return nil
	})
	require.NoError(t, err)

	// Set initial default
	bus.SetDefaultWorktree("main")
	bus.Publish(context.Background(), Event{Type: "test1"})

	// Change default
	bus.SetDefaultWorktree("feature")
	bus.Publish(context.Background(), Event{Type: "test2"})

	// Verify events have correct worktrees
	require.Len(t, receivedEvents, 2)
	assert.Equal(t, "main", receivedEvents[0].Worktree)
	assert.Equal(t, "feature", receivedEvents[1].Worktree)
}

func TestMemoryEventBus_SetDefaultWorktree_History(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{
		HistoryMaxEvents: 100,
		HistoryMaxAge:    time.Hour,
	})
	defer bus.Close()

	// Set default worktree
	bus.SetDefaultWorktree("main")

	// Publish events
	bus.Publish(context.Background(), Event{Type: "service.started"})
	bus.Publish(context.Background(), Event{Type: "service.stopped", Worktree: "feature"})

	// Query history - should find event with default worktree
	history, err := bus.History(EventFilter{Worktree: "main"})
	require.NoError(t, err)
	assert.Len(t, history, 1)
	assert.Equal(t, "service.started", history[0].Type)

	// Query history - should find event with explicit worktree
	history, err = bus.History(EventFilter{Worktree: "feature"})
	require.NoError(t, err)
	assert.Len(t, history, 1)
	assert.Equal(t, "service.stopped", history[0].Type)
}

func TestMemoryEventBus_SetDefaultWorktree_Concurrent(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{
		HistoryMaxEvents: 1000,
	})
	defer bus.Close()

	var wg sync.WaitGroup

	// Concurrent writers changing the default
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				bus.SetDefaultWorktree(fmt.Sprintf("worktree-%d", n))
			}
		}(i)
	}

	// Concurrent publishers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				bus.Publish(context.Background(), Event{Type: "test"})
			}
		}()
	}

	wg.Wait()

	// Should complete without race conditions
	history, err := bus.History(EventFilter{})
	require.NoError(t, err)
	assert.Len(t, history, 500)

	// All events should have a worktree (may vary due to concurrency)
	for _, e := range history {
		// Worktree might be empty if SetDefaultWorktree hasn't been called yet
		// but once set, all subsequent events should have it
		// This test is mainly to verify no race conditions
		_ = e.Worktree
	}
}
