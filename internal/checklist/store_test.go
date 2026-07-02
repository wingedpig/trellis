// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package checklist

import (
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/pair"
)

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	run := &Run{
		ID:            "run-1",
		CreatedAt:     time.Now().Truncate(time.Second),
		State:         StateRunning,
		Step:          StepReview,
		PhasesDone:    2,
		Implementer:   pair.AgentRef{Agent: "claude", Worktree: "main", SessionID: "impl"},
		Reviewer:      pair.AgentRef{Agent: "codex", Worktree: "main", SessionID: "rev"},
		Config:        DefaultConfig(),
		CurrentPairID: "pair-9",
		Phases: []PhaseRecord{
			{N: 1, Status: "converged", PairID: "pair-7"},
			{N: 2, Status: "converged", PairID: "pair-8"},
			{N: 3, Status: "running", PairID: "pair-9", Summary: "did the thing"},
		},
	}

	if err := s.Save(run); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if run.LastPersistedAt.IsZero() {
		t.Fatalf("Save should set LastPersistedAt")
	}

	got, err := s.Load("run-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.State != StateRunning || got.Step != StepReview {
		t.Fatalf("state/step mismatch: %+v", got)
	}
	if got.PhasesDone != 2 || got.CurrentPairID != "pair-9" {
		t.Fatalf("phasesDone/currentPair mismatch: %+v", got)
	}
	if len(got.Phases) != 3 || got.Phases[2].Summary != "did the thing" {
		t.Fatalf("phases mismatch: %+v", got.Phases)
	}
	if got.Implementer.SessionID != "impl" || got.Reviewer.SessionID != "rev" {
		t.Fatalf("agent refs mismatch: %+v / %+v", got.Implementer, got.Reviewer)
	}

	all, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("LoadAll len = %d, want 1", len(all))
	}

	if err := s.Delete("run-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Load("run-1"); err == nil {
		t.Fatalf("Load after Delete should fail")
	}
}
