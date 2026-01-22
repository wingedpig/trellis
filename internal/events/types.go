// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package events provides the event bus for Trellis.
package events

import (
	"context"
	"time"
)

// Event represents an immutable event record.
type Event struct {
	ID        string                 `json:"id"`
	Version   string                 `json:"version"`
	Type      string                 `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Worktree  string                 `json:"worktree"`
	Payload   map[string]interface{} `json:"payload"`
}

// EventHandler processes received events.
type EventHandler func(ctx context.Context, event Event) error

// SubscriptionID uniquely identifies a subscription.
type SubscriptionID string

// EventFilter for querying event history.
type EventFilter struct {
	Types    []string  // Event types to match (supports wildcards)
	Worktree string    // Filter by worktree
	Since    time.Time // Events after this time
	Until    time.Time // Events before this time
	Limit    int       // Maximum events to return
}

// EventBus is the core event pub/sub system.
type EventBus interface {
	// Publish emits an event to all matching subscribers.
	Publish(ctx context.Context, event Event) error

	// Subscribe registers a synchronous handler for events matching pattern.
	Subscribe(pattern string, handler EventHandler) (SubscriptionID, error)

	// SubscribeAsync registers an async handler with buffered channel.
	SubscribeAsync(pattern string, handler EventHandler, bufferSize int) (SubscriptionID, error)

	// Unsubscribe removes a subscription.
	Unsubscribe(id SubscriptionID) error

	// History retrieves past events matching filter.
	History(filter EventFilter) ([]Event, error)

	// SetDefaultWorktree sets the default worktree for events that don't specify one.
	SetDefaultWorktree(worktree string)

	// Close shuts down the event bus gracefully.
	Close() error
}

// Common event types
const (
	// Service events
	EventServiceStarted   = "service.started"
	EventServiceStopped   = "service.stopped"
	EventServiceCrashed   = "service.crashed"
	EventServiceRestarted = "service.restarted"

	// Worktree events
	EventWorktreeDeactivating = "worktree.deactivating"
	EventWorktreeActivated    = "worktree.activated"
	EventWorktreeCreated      = "worktree.created"
	EventWorktreeDeleted      = "worktree.deleted"
	EventWorktreeHookStarted  = "worktree.hook.started"
	EventWorktreeHookFinished = "worktree.hook.finished"

	// Workflow events
	EventWorkflowStarted  = "workflow.started"
	EventWorkflowFinished = "workflow.finished"

	// Binary events
	EventBinaryChanged = "binary.changed"

	// Trace events
	EventTraceStarted   = "trace.started"
	EventTraceCompleted = "trace.completed"
	EventTraceFailed    = "trace.failed"

	// Notification events (for AI assistants and external tools)
	EventNotifyDone    = "notify.done"    // Task completed
	EventNotifyBlocked = "notify.blocked" // Waiting for user input
	EventNotifyError   = "notify.error"   // Something failed
)

// RestartTrigger indicates why a service was restarted.
type RestartTrigger string

const (
	RestartTriggerBinaryChange   RestartTrigger = "binary_change"
	RestartTriggerManual         RestartTrigger = "manual"
	RestartTriggerWorktreeSwitch RestartTrigger = "worktree_switch"
	RestartTriggerCrash          RestartTrigger = "crash"
)
