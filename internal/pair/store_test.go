// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package pair

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "pairs"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	orig := &Pair{
		ID:        "abc-123",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		State:     StateRunning,
		Step:      StepAwaitReviewer,
		Implementer: AgentRef{Agent: "claude", Worktree: "main", SessionID: "s-1"},
		Reviewer:    AgentRef{Agent: "codex", Worktree: "main", SessionID: "s-2"},
		Config:      DefaultConfig(),
		RoundCount:  3,
		Rounds: []Round{
			{N: 1, Direction: "to_reviewer", At: time.Now().UTC().Truncate(time.Second)},
			{N: 2, Direction: "to_implementer", At: time.Now().UTC().Truncate(time.Second)},
		},
	}

	if err := store.Save(orig); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := store.Load("abc-123")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.ID != orig.ID || loaded.State != orig.State || loaded.Step != orig.Step {
		t.Errorf("loaded mismatch: %+v vs %+v", loaded, orig)
	}
	if loaded.Implementer.SessionID != "s-1" || loaded.Reviewer.SessionID != "s-2" {
		t.Errorf("loaded refs wrong: %+v / %+v", loaded.Implementer, loaded.Reviewer)
	}
	if loaded.RoundCount != 3 || len(loaded.Rounds) != 2 {
		t.Errorf("loaded rounds: count=%d len=%d", loaded.RoundCount, len(loaded.Rounds))
	}

	all, err := store.LoadAll()
	if err != nil {
		t.Fatalf("load all: %v", err)
	}
	if len(all) != 1 || all[0].ID != "abc-123" {
		t.Errorf("loadAll returned %d records: %+v", len(all), all)
	}

	if err := store.Delete("abc-123"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Load("abc-123"); err == nil {
		t.Errorf("expected not-found after delete")
	}
}

func TestStoreNoopWhenDirEmpty(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Save(&Pair{ID: "x"}); err != nil {
		t.Fatalf("save on noop store should not error: %v", err)
	}
	if all, err := store.LoadAll(); err != nil || all != nil {
		t.Errorf("noop loadAll: %v / %v", all, err)
	}
}
