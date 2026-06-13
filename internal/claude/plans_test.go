// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/json"
	"testing"
)

func TestMaybeCapturePlanFromInput(t *testing.T) {
	m := NewManager(t.TempDir())
	s := m.CreateSession("wt", "/tmp", "Test")

	s.maybeCapturePlan(ContentBlock{
		Type:  "tool_use",
		Name:  "ExitPlanMode",
		Input: json.RawMessage(`{"plan":"# The Plan\n\n1. Do it"}`),
	})

	pv, ok := s.LatestPlan()
	if !ok {
		t.Fatal("expected a captured plan")
	}
	if pv.Content != "# The Plan\n\n1. Do it" || pv.Source != "agent" || pv.Version != 1 {
		t.Fatalf("unexpected plan version: %+v", pv)
	}

	// Identical re-capture is a no-op.
	s.maybeCapturePlan(ContentBlock{
		Type:  "tool_use",
		Name:  "ExitPlanMode",
		Input: json.RawMessage(`{"plan":"# The Plan\n\n1. Do it"}`),
	})
	if got := len(s.Plans()); got != 1 {
		t.Fatalf("expected 1 plan version after duplicate capture, got %d", got)
	}

	// Non-plan blocks are ignored.
	s.maybeCapturePlan(ContentBlock{Type: "tool_use", Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)})
	if got := len(s.Plans()); got != 1 {
		t.Fatalf("expected 1 plan version, got %d", got)
	}
}

func TestMaybeCapturePlanFromPrecedingWrite(t *testing.T) {
	m := NewManager(t.TempDir())
	s := m.CreateSession("wt", "/tmp", "Test")

	// Claude wrote the plan to a .md file earlier in the turn, then called
	// ExitPlanMode without a plan input.
	s.mu.Lock()
	s.currentBlocks = []ContentBlock{
		{Type: "tool_use", Name: "Write", Input: json.RawMessage(`{"file_path":"/tmp/plan.md","content":"# Plan from file"}`)},
	}
	s.mu.Unlock()

	s.maybeCapturePlan(ContentBlock{
		Type:  "tool_use",
		Name:  "ExitPlanMode",
		Input: json.RawMessage(`{"allowedPrompts":[]}`),
	})

	pv, ok := s.LatestPlan()
	if !ok {
		t.Fatal("expected a captured plan from preceding Write")
	}
	if pv.Content != "# Plan from file" {
		t.Fatalf("unexpected plan content: %q", pv.Content)
	}
}

func TestPlanPersistenceAcrossManagers(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	s := m.CreateSession("wt", "/tmp", "Test")
	s.maybeCapturePlan(ContentBlock{
		Type:  "tool_use",
		Name:  "ExitPlanMode",
		Input: json.RawMessage(`{"plan":"v1"}`),
	})
	s.UpdatePlan("v2 (edited)")

	// Reload from disk via a fresh manager.
	m2 := NewManager(dir)
	s2 := m2.GetSession(s.ID())
	if s2 == nil {
		t.Fatal("session not reloaded")
	}
	plans := s2.Plans()
	if len(plans) != 2 {
		t.Fatalf("expected 2 plan versions after reload, got %d", len(plans))
	}
	if plans[0].Content != "v1" || plans[1].Content != "v2 (edited)" || plans[1].Source != "user" {
		t.Fatalf("unexpected plans after reload: %+v", plans)
	}
	if plans[1].Version != 2 {
		t.Fatalf("expected version 2, got %d", plans[1].Version)
	}
}
