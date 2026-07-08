// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/json"
	"testing"

	"github.com/wingedpig/trellis/internal/events"
)

// TestBackgroundTasksChangedParse pins the JSON field mapping against a real
// captured system/background_tasks_changed event, so a CLI field rename can't
// silently make len(Tasks) always zero (which would reintroduce the bug).
func TestBackgroundTasksChangedParse(t *testing.T) {
	const line = `{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"a729e6c6ade3b1b81","task_type":"local_agent","description":"Run sleep and echo marker"}]}`
	var ev StreamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Type != "system" || ev.Subtype != "background_tasks_changed" {
		t.Fatalf("header: got type=%q subtype=%q", ev.Type, ev.Subtype)
	}
	if len(ev.Tasks) != 1 {
		t.Fatalf("tasks: got %d, want 1", len(ev.Tasks))
	}
	if got := ev.Tasks[0]; got.TaskID != "a729e6c6ade3b1b81" || got.TaskType != "local_agent" || got.Description != "Run sleep and echo marker" {
		t.Fatalf("task fields not parsed: %+v", got)
	}
}

// TestBackgroundTaskState verifies that a background agent/task keeps the
// session "running" (reason background_agent) after the main turn's result,
// instead of flipping to idle/awaiting_input — and that it returns to idle once
// the CLI reports the tasks finished. This is the fix for a session showing
// green/done while a background agent is still working.
func TestBackgroundTaskState(t *testing.T) {
	s := &Session{id: "unit-test"} // nil manager: publish/activity are safe no-ops

	stateReason := func() (string, string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.deriveStateLocked(), s.reasonLocked()
	}

	one := []BackgroundTask{{TaskID: "a1", TaskType: "local_agent", Description: "Add routine category to runbook"}}
	two := append([]BackgroundTask{{TaskID: "b2", TaskType: "local_bash", Description: "build"}}, one...)

	// Baseline: idle session, no turn, no background work → needs_you / awaiting.
	if st, rn := stateReason(); st != events.SessionStateNeedsYou || rn != events.ReasonAwaitingInput {
		t.Fatalf("idle baseline: got (%s,%s), want (needs_you,awaiting_input)", st, rn)
	}

	// A live turn is running.
	s.mu.Lock()
	s.generating = true
	s.mu.Unlock()
	if st, rn := stateReason(); st != events.SessionStateRunning || rn != events.ReasonRunning {
		t.Fatalf("generating: got (%s,%s), want (running,running)", st, rn)
	}

	// Turn dispatched a background agent (it appears while still generating).
	s.mu.Lock()
	s.setBackgroundTasksLocked(one)
	s.mu.Unlock()
	if st, rn := stateReason(); st != events.SessionStateRunning || rn != events.ReasonRunning {
		t.Fatalf("generating+bg: got (%s,%s), want (running,running)", st, rn)
	}

	// Main turn ends (result): generating clears, but the background agent is
	// still running — the session must stay running, not go idle/done.
	s.mu.Lock()
	s.generating = false
	s.mu.Unlock()
	if st, rn := stateReason(); st != events.SessionStateRunning || rn != events.ReasonBackgroundAgent {
		t.Fatalf("turn done, bg running: got (%s,%s), want (running,background_agent)", st, rn)
	}
	if lbl := backgroundTaskLabel(one); lbl != "Background agent: Add routine category to runbook" {
		t.Fatalf("single-task label: got %q", lbl)
	}

	// A pending permission prompt outranks background work — the user must act.
	s.mu.Lock()
	s.pendingControlRequests = []*StreamEvent{{RequestID: "req-1"}}
	s.mu.Unlock()
	if st, rn := stateReason(); st != events.SessionStateNeedsYou || rn != events.ReasonNeedsApproval {
		t.Fatalf("bg + pending prompt: got (%s,%s), want (needs_you,needs_approval)", st, rn)
	}
	s.mu.Lock()
	s.pendingControlRequests = nil
	s.setBackgroundTasksLocked(two)
	s.mu.Unlock()
	if lbl := backgroundTaskLabel(two); lbl != "2 background agents working…" {
		t.Fatalf("multi-task label: got %q", lbl)
	}

	// CLI reports all background tasks finished ([]): back to idle/awaiting.
	s.mu.Lock()
	s.setBackgroundTasksLocked(nil)
	s.mu.Unlock()
	if st, rn := stateReason(); st != events.SessionStateNeedsYou || rn != events.ReasonAwaitingInput {
		t.Fatalf("bg finished: got (%s,%s), want (needs_you,awaiting_input)", st, rn)
	}
}
