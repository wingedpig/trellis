// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventHistory_Add(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 100,
		MaxAge:    time.Hour,
	})
	defer history.Close()

	event := Event{
		ID:        "1",
		Type:      "service.started",
		Timestamp: time.Now(),
	}

	err := history.Add(event)
	assert.NoError(t, err)

	events, err := history.Query(EventFilter{})
	require.NoError(t, err)
	assert.Len(t, events, 1)
	assert.Equal(t, "1", events[0].ID)
}

func TestEventHistory_MaxEvents(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 5,
		MaxAge:    time.Hour,
	})
	defer history.Close()

	// Add more events than max
	for i := 0; i < 10; i++ {
		history.Add(Event{
			ID:        string(rune('0' + i)),
			Type:      "service.started",
			Timestamp: time.Now(),
		})
	}

	events, err := history.Query(EventFilter{})
	require.NoError(t, err)

	// Should only have the last 5 events
	assert.Len(t, events, 5)

	// Oldest events should be removed (keep newest)
	for i, e := range events {
		expectedID := string(rune('0' + (5 + i)))
		assert.Equal(t, expectedID, e.ID)
	}
}

func TestEventHistory_MaxAge(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 100,
		MaxAge:    100 * time.Millisecond,
	})
	defer history.Close()

	// Add old event
	history.Add(Event{
		ID:        "old",
		Type:      "service.started",
		Timestamp: time.Now().Add(-200 * time.Millisecond),
	})

	// Add recent event
	history.Add(Event{
		ID:        "new",
		Type:      "service.started",
		Timestamp: time.Now(),
	})

	// Prune should remove old event
	history.Prune()

	events, err := history.Query(EventFilter{})
	require.NoError(t, err)
	assert.Len(t, events, 1)
	assert.Equal(t, "new", events[0].ID)
}

func TestEventHistory_Query_Types(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 100,
		MaxAge:    time.Hour,
	})
	defer history.Close()

	// Add various events
	events := []Event{
		{ID: "1", Type: "service.started", Timestamp: time.Now()},
		{ID: "2", Type: "service.stopped", Timestamp: time.Now()},
		{ID: "3", Type: "service.crashed", Timestamp: time.Now()},
		{ID: "4", Type: "workflow.started", Timestamp: time.Now()},
		{ID: "5", Type: "workflow.finished", Timestamp: time.Now()},
	}

	for _, e := range events {
		history.Add(e)
	}

	// Query service events only
	result, err := history.Query(EventFilter{Types: []string{"service.*"}})
	require.NoError(t, err)
	assert.Len(t, result, 3)

	// Query specific type
	result, err = history.Query(EventFilter{Types: []string{"workflow.finished"}})
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "5", result[0].ID)

	// Query multiple patterns
	result, err = history.Query(EventFilter{Types: []string{"service.started", "workflow.*"}})
	require.NoError(t, err)
	assert.Len(t, result, 3)
}

func TestEventHistory_Query_Worktree(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 100,
		MaxAge:    time.Hour,
	})
	defer history.Close()

	events := []Event{
		{ID: "1", Type: "service.started", Worktree: "main", Timestamp: time.Now()},
		{ID: "2", Type: "service.started", Worktree: "feature", Timestamp: time.Now()},
		{ID: "3", Type: "service.stopped", Worktree: "main", Timestamp: time.Now()},
	}

	for _, e := range events {
		history.Add(e)
	}

	// Query main worktree
	result, err := history.Query(EventFilter{Worktree: "main"})
	require.NoError(t, err)
	assert.Len(t, result, 2)

	// Query feature worktree
	result, err = history.Query(EventFilter{Worktree: "feature"})
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestEventHistory_Query_TimeRange(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 100,
		MaxAge:    time.Hour,
	})
	defer history.Close()

	now := time.Now()
	events := []Event{
		{ID: "1", Type: "service.started", Timestamp: now.Add(-30 * time.Minute)},
		{ID: "2", Type: "service.started", Timestamp: now.Add(-15 * time.Minute)},
		{ID: "3", Type: "service.started", Timestamp: now.Add(-5 * time.Minute)},
	}

	for _, e := range events {
		history.Add(e)
	}

	// Query since 20 minutes ago
	result, err := history.Query(EventFilter{Since: now.Add(-20 * time.Minute)})
	require.NoError(t, err)
	assert.Len(t, result, 2)

	// Query until 10 minutes ago
	result, err = history.Query(EventFilter{Until: now.Add(-10 * time.Minute)})
	require.NoError(t, err)
	assert.Len(t, result, 2)

	// Query specific range
	result, err = history.Query(EventFilter{
		Since: now.Add(-20 * time.Minute),
		Until: now.Add(-10 * time.Minute),
	})
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "2", result[0].ID)
}

func TestEventHistory_Query_Limit(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 100,
		MaxAge:    time.Hour,
	})
	defer history.Close()

	for i := 0; i < 10; i++ {
		history.Add(Event{
			ID:        string(rune('0' + i)),
			Type:      "service.started",
			Timestamp: time.Now(),
		})
	}

	result, err := history.Query(EventFilter{Limit: 3})
	require.NoError(t, err)
	assert.Len(t, result, 3)
}

func TestEventHistory_Query_CombinedFilters(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 100,
		MaxAge:    time.Hour,
	})
	defer history.Close()

	now := time.Now()
	events := []Event{
		{ID: "1", Type: "service.started", Worktree: "main", Timestamp: now.Add(-30 * time.Minute)},
		{ID: "2", Type: "service.stopped", Worktree: "main", Timestamp: now.Add(-15 * time.Minute)},
		{ID: "3", Type: "service.started", Worktree: "feature", Timestamp: now.Add(-10 * time.Minute)},
		{ID: "4", Type: "workflow.started", Worktree: "main", Timestamp: now.Add(-5 * time.Minute)},
	}

	for _, e := range events {
		history.Add(e)
	}

	// Query: service.* events on main worktree in last 20 minutes
	result, err := history.Query(EventFilter{
		Types:    []string{"service.*"},
		Worktree: "main",
		Since:    now.Add(-20 * time.Minute),
	})
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "2", result[0].ID)
}

func TestEventHistory_Prune(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 100,
		MaxAge:    50 * time.Millisecond,
	})
	defer history.Close()

	// Add event
	history.Add(Event{
		ID:        "1",
		Type:      "service.started",
		Timestamp: time.Now(),
	})

	// Wait for event to age out
	time.Sleep(100 * time.Millisecond)

	// Prune
	err := history.Prune()
	require.NoError(t, err)

	// Event should be removed
	events, err := history.Query(EventFilter{})
	require.NoError(t, err)
	assert.Len(t, events, 0)
}

func TestEventHistory_Order(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 100,
		MaxAge:    time.Hour,
	})
	defer history.Close()

	now := time.Now()
	events := []Event{
		{ID: "3", Type: "service.started", Timestamp: now.Add(2 * time.Second)},
		{ID: "1", Type: "service.started", Timestamp: now},
		{ID: "2", Type: "service.started", Timestamp: now.Add(1 * time.Second)},
	}

	for _, e := range events {
		history.Add(e)
	}

	// Results should be ordered by timestamp (oldest first)
	result, err := history.Query(EventFilter{})
	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.Equal(t, "1", result[0].ID)
	assert.Equal(t, "2", result[1].ID)
	assert.Equal(t, "3", result[2].ID)
}

func TestEventHistory_Concurrency(t *testing.T) {
	history := NewEventHistory(EventHistoryConfig{
		MaxEvents: 1000,
		MaxAge:    time.Hour,
	})
	defer history.Close()

	done := make(chan bool, 20)

	// Concurrent writers
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				history.Add(Event{
					ID:        string(rune(id*100 + j)),
					Type:      "service.started",
					Timestamp: time.Now(),
				})
			}
			done <- true
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				history.Query(EventFilter{})
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}
}

func TestEventHistory_Integration_WithBus(t *testing.T) {
	bus := NewMemoryEventBus(MemoryBusConfig{
		HistoryMaxEvents: 10,
		HistoryMaxAge:    time.Hour,
	})
	defer bus.Close()

	// Publish events
	for i := 0; i < 15; i++ {
		bus.Publish(context.Background(), Event{
			Type:     "service.started",
			Worktree: "main",
		})
	}

	// Query history - should only have last 10
	history, err := bus.History(EventFilter{})
	require.NoError(t, err)
	assert.Len(t, history, 10)
}
