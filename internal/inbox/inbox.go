// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package inbox provides the aggregated view of all active Claude and Codex
// sessions for the floating inbox window. It merges per-agent session lists,
// derives a coarse running-vs-needs-you state, and tracks the timestamp of the
// last state transition for each session so the UI can sort by recency.
package inbox

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
	"github.com/wingedpig/trellis/internal/events"
)

// SessionRow is one row in the inbox view.
type SessionRow struct {
	ID          string `json:"id"`
	Agent       string `json:"agent"` // "claude" | "codex"
	Worktree    string `json:"worktree"`
	DisplayName string `json:"display_name"`
	State       string `json:"state"` // coarse: "running" | "needs_you"
	// Reason refines State for presentation only: "running" | "awaiting_input"
	// | "needs_approval" | "error". Never affects sort/transition detection.
	Reason string `json:"reason"`
	// Activity is a short human-readable description of what the session is
	// doing right now ("Running go test"), or "" when idle.
	Activity          string    `json:"activity"`
	Unread            bool      `json:"unread"`  // missed running→needs-you transition
	Trashed           bool      `json:"trashed"` // true while a removal animates client-side
	LastStateChangeAt time.Time `json:"last_state_change_at"`
}

// Aggregator builds inbox session rows from the two managers and tracks
// the last state-transition timestamp per session by subscribing to the
// event bus.
type Aggregator struct {
	claude *claude.Manager
	codex  *codex.Manager

	mu         sync.RWMutex
	lastChange map[string]time.Time // session ID -> last real state-transition ts
	lastState  map[string]string    // session ID -> last observed state (for transition detection)
}

// NewAggregator constructs an Aggregator and registers an async subscriber
// on the bus so the lastChange map stays fresh. Call Close to release the
// subscription (in practice the aggregator lives for the process lifetime).
func NewAggregator(claudeMgr *claude.Manager, codexMgr *codex.Manager, bus events.EventBus) (*Aggregator, error) {
	a := &Aggregator{
		claude:     claudeMgr,
		codex:      codexMgr,
		lastChange: make(map[string]time.Time),
		lastState:  make(map[string]string),
	}
	if bus != nil {
		_, err := bus.SubscribeAsync(events.EventSessionStateChanged, func(_ context.Context, ev events.Event) error {
			id, _ := ev.Payload["session_id"].(string)
			if id == "" {
				return nil
			}
			state, _ := ev.Payload["state"].(string)
			a.mu.Lock()
			// Only bump lastChange on a real running ↔ needs-you transition.
			// Unread-only updates re-emit the event with the same state, and
			// we don't want those to reorder the inbox.
			if a.lastState[id] != state {
				a.lastState[id] = state
				a.lastChange[id] = ev.Timestamp
			}
			a.mu.Unlock()
			return nil
		}, 256)
		if err != nil {
			return nil, err
		}
	}
	return a, nil
}

// List returns the current merged set of inbox rows. Trashed sessions are
// excluded. The order is unspecified — clients sort client-side.
func (a *Aggregator) List() []SessionRow {
	a.mu.RLock()
	lastChange := make(map[string]time.Time, len(a.lastChange))
	for k, v := range a.lastChange {
		lastChange[k] = v
	}
	a.mu.RUnlock()

	rows := make([]SessionRow, 0)

	for _, info := range a.claude.AllSessions() {
		s := a.claude.GetSession(info.ID)
		if s == nil {
			continue
		}
		state := events.SessionStateRunning
		if !s.IsGenerating() || s.HasPendingControlRequest() {
			state = events.SessionStateNeedsYou
		}
		ts := lastChange[info.ID]
		if ts.IsZero() {
			ts = info.CreatedAt
		}
		rows = append(rows, SessionRow{
			ID:                info.ID,
			Agent:             "claude",
			Worktree:          info.WorktreeName,
			DisplayName:       info.DisplayName,
			State:             state,
			Reason:            s.Reason(),
			Activity:          s.CurrentActivity(),
			Unread:            s.IsUnread(),
			LastStateChangeAt: ts,
		})
	}

	for _, info := range a.codex.AllSessions() {
		s := a.codex.GetSession(info.ID)
		if s == nil {
			continue
		}
		state := events.SessionStateRunning
		if !s.IsGenerating() || len(s.PendingApprovals()) > 0 {
			state = events.SessionStateNeedsYou
		}
		ts := lastChange[info.ID]
		if ts.IsZero() {
			ts = info.CreatedAt
		}
		rows = append(rows, SessionRow{
			ID:                info.ID,
			Agent:             "codex",
			Worktree:          info.WorktreeName,
			DisplayName:       info.DisplayName,
			State:             state,
			Reason:            s.Reason(),
			Activity:          s.CurrentActivity(),
			Unread:            s.IsUnread(),
			LastStateChangeAt: ts,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].State != rows[j].State {
			return rows[i].State == events.SessionStateNeedsYou
		}
		return rows[i].LastStateChangeAt.After(rows[j].LastStateChangeAt)
	})
	return rows
}
