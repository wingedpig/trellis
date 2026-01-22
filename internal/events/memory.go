// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ErrBusClosed is returned when operating on a closed bus.
var ErrBusClosed = errors.New("event bus is closed")

// ErrSubscriptionNotFound is returned when unsubscribing with invalid ID.
var ErrSubscriptionNotFound = errors.New("subscription not found")

// MemoryBusConfig configures the memory event bus.
type MemoryBusConfig struct {
	HistoryMaxEvents int
	HistoryMaxAge    time.Duration
}

// MemoryEventBus is an in-memory event bus implementation.
type MemoryEventBus struct {
	mu              sync.RWMutex
	subscriptions   map[SubscriptionID]*subscription
	history         *EventHistory
	matcher         *PatternMatcher
	closed          atomic.Bool
	wg              sync.WaitGroup
	nextID          uint64
	defaultWorktree string
	stopPruner      chan struct{}
}

type subscription struct {
	id      SubscriptionID
	pattern CompiledPattern
	handler EventHandler
	async   bool
	ch      chan Event
	stopCh  chan struct{}
}

// NewMemoryEventBus creates a new in-memory event bus.
func NewMemoryEventBus(cfg MemoryBusConfig) *MemoryEventBus {
	bus := &MemoryEventBus{
		subscriptions: make(map[SubscriptionID]*subscription),
		history: NewEventHistory(EventHistoryConfig{
			MaxEvents: cfg.HistoryMaxEvents,
			MaxAge:    cfg.HistoryMaxAge,
		}),
		matcher:    NewPatternMatcher(),
		stopPruner: make(chan struct{}),
	}

	// Start background pruner to enforce max_age
	pruneInterval := cfg.HistoryMaxAge / 10
	if pruneInterval < time.Minute {
		pruneInterval = time.Minute
	}
	if pruneInterval > time.Hour {
		pruneInterval = time.Hour
	}

	bus.wg.Add(1)
	go func() {
		defer bus.wg.Done()
		ticker := time.NewTicker(pruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-bus.stopPruner:
				return
			case <-ticker.C:
				bus.history.Prune()
			}
		}
	}()

	return bus
}

// SetDefaultWorktree sets the default worktree name for events that don't specify one.
func (bus *MemoryEventBus) SetDefaultWorktree(worktree string) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	bus.defaultWorktree = worktree
}

// Publish emits an event to all matching subscribers.
func (bus *MemoryEventBus) Publish(ctx context.Context, event Event) error {
	if bus.closed.Load() {
		return ErrBusClosed
	}

	// Assign ID, timestamp, and worktree if not set
	if event.ID == "" {
		event.ID = bus.generateID()
	}
	if event.Version == "" {
		event.Version = "1.0"
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.Worktree == "" {
		bus.mu.RLock()
		event.Worktree = bus.defaultWorktree
		bus.mu.RUnlock()
	}

	// Store in history
	bus.history.Add(event)

	// Notify subscribers
	bus.mu.RLock()
	subs := make([]*subscription, 0, len(bus.subscriptions))
	for _, sub := range bus.subscriptions {
		subs = append(subs, sub)
	}
	bus.mu.RUnlock()

	for _, sub := range subs {
		if sub.pattern.Match(event.Type) {
			if sub.async {
				// Non-blocking send to async subscribers
				select {
				case sub.ch <- event:
				default:
					log.Printf("EventBus: dropped %s - async subscriber buffer full", event.Type)
				}
			} else {
				// Synchronous call with panic protection
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("Event handler panic for %s: %v", event.Type, r)
						}
					}()
					sub.handler(ctx, event)
				}()
			}
		}
	}

	return nil
}

// Subscribe registers a synchronous handler for events matching pattern.
func (bus *MemoryEventBus) Subscribe(pattern string, handler EventHandler) (SubscriptionID, error) {
	if bus.closed.Load() {
		return "", ErrBusClosed
	}

	compiled, err := bus.matcher.Compile(pattern)
	if err != nil {
		return "", err
	}

	id := SubscriptionID(bus.generateID())

	sub := &subscription{
		id:      id,
		pattern: compiled,
		handler: handler,
		async:   false,
	}

	bus.mu.Lock()
	bus.subscriptions[id] = sub
	bus.mu.Unlock()

	return id, nil
}

// SubscribeAsync registers an async handler with buffered channel.
func (bus *MemoryEventBus) SubscribeAsync(pattern string, handler EventHandler, bufferSize int) (SubscriptionID, error) {
	if bus.closed.Load() {
		return "", ErrBusClosed
	}

	compiled, err := bus.matcher.Compile(pattern)
	if err != nil {
		return "", err
	}

	if bufferSize <= 0 {
		bufferSize = 100
	}

	id := SubscriptionID(bus.generateID())
	ch := make(chan Event, bufferSize)
	stopCh := make(chan struct{})

	sub := &subscription{
		id:      id,
		pattern: compiled,
		handler: handler,
		async:   true,
		ch:      ch,
		stopCh:  stopCh,
	}

	bus.mu.Lock()
	bus.subscriptions[id] = sub
	bus.mu.Unlock()

	// Start goroutine to process events
	bus.wg.Add(1)
	go func() {
		defer bus.wg.Done()
		for {
			select {
			case <-stopCh:
				return
			case event := <-ch:
				// Wrap handler with panic protection like the sync path
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("Async event handler panic for %s: %v", event.Type, r)
						}
					}()
					handler(context.Background(), event)
				}()
			}
		}
	}()

	return id, nil
}

// Unsubscribe removes a subscription.
func (bus *MemoryEventBus) Unsubscribe(id SubscriptionID) error {
	bus.mu.Lock()
	sub, ok := bus.subscriptions[id]
	if !ok {
		bus.mu.Unlock()
		return ErrSubscriptionNotFound
	}
	delete(bus.subscriptions, id)
	bus.mu.Unlock()

	// Stop async handler if running
	if sub.async && sub.stopCh != nil {
		close(sub.stopCh)
	}

	return nil
}

// History retrieves past events matching filter.
func (bus *MemoryEventBus) History(filter EventFilter) ([]Event, error) {
	return bus.history.Query(filter)
}

// Close shuts down the event bus gracefully.
func (bus *MemoryEventBus) Close() error {
	if bus.closed.Swap(true) {
		return nil // Already closed
	}

	// Stop the background pruner
	close(bus.stopPruner)

	// Stop all async handlers
	bus.mu.Lock()
	for _, sub := range bus.subscriptions {
		if sub.async && sub.stopCh != nil {
			close(sub.stopCh)
		}
	}
	bus.subscriptions = make(map[SubscriptionID]*subscription)
	bus.mu.Unlock()

	// Wait for goroutines to finish
	bus.wg.Wait()

	// Close history
	bus.history.Close()

	return nil
}

// generateID generates a unique ID.
func (bus *MemoryEventBus) generateID() string {
	n := atomic.AddUint64(&bus.nextID, 1)
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b) + "-" + strconv.FormatUint(n, 10)
}
