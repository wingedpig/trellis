// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"sort"
	"sync"
	"time"
)

// EventHistoryConfig configures event history.
type EventHistoryConfig struct {
	MaxEvents int
	MaxAge    time.Duration
}

// EventHistory manages event retention.
type EventHistory struct {
	mu        sync.RWMutex
	events    []Event
	maxEvents int
	maxAge    time.Duration
	matcher   *PatternMatcher
}

// NewEventHistory creates a new event history.
func NewEventHistory(cfg EventHistoryConfig) *EventHistory {
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = 10000
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = time.Hour
	}

	return &EventHistory{
		events:    make([]Event, 0),
		maxEvents: cfg.MaxEvents,
		maxAge:    cfg.MaxAge,
		matcher:   NewPatternMatcher(),
	}
}

// Add stores an event in history.
func (h *EventHistory) Add(event Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Add event
	h.events = append(h.events, event)

	// Enforce max events limit
	if len(h.events) > h.maxEvents {
		h.events = h.events[len(h.events)-h.maxEvents:]
	}

	return nil
}

// Query retrieves events matching filter.
func (h *EventHistory) Query(filter EventFilter) ([]Event, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]Event, 0)

	for _, event := range h.events {
		if h.matchesFilter(event, filter) {
			result = append(result, event)
		}
	}

	// Sort by timestamp (oldest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})

	// Apply limit
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[len(result)-filter.Limit:]
	}

	return result, nil
}

// matchesFilter checks if an event matches the filter criteria.
func (h *EventHistory) matchesFilter(event Event, filter EventFilter) bool {
	// Type filter
	if len(filter.Types) > 0 {
		matched := false
		for _, pattern := range filter.Types {
			if h.matcher.Match(event.Type, pattern) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Worktree filter
	if filter.Worktree != "" && event.Worktree != filter.Worktree {
		return false
	}

	// Since filter
	if !filter.Since.IsZero() && event.Timestamp.Before(filter.Since) {
		return false
	}

	// Until filter
	if !filter.Until.IsZero() && event.Timestamp.After(filter.Until) {
		return false
	}

	return true
}

// Prune removes events older than max age or exceeding max count.
func (h *EventHistory) Prune() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	cutoff := time.Now().Add(-h.maxAge)
	filtered := make([]Event, 0, len(h.events))

	for _, event := range h.events {
		if event.Timestamp.After(cutoff) {
			filtered = append(filtered, event)
		}
	}

	// Enforce max events limit
	if len(filtered) > h.maxEvents {
		filtered = filtered[len(filtered)-h.maxEvents:]
	}

	h.events = filtered
	return nil
}

// Close releases resources.
func (h *EventHistory) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = nil
	return nil
}
