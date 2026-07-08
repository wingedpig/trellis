// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureResumeTarget covers the logic that guarantees --resume always has a
// valid on-disk target, which is what keeps a restart (notably the auto-approve
// toggle's respawn) from aborting the next turn with "No conversation found".
// HOME is redirected so WriteCLISessionFile/CLIProjectDir never touch the real
// ~/.claude.
func TestEnsureResumeTarget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	work := t.TempDir()
	s := &Session{id: "unit-test"}

	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "remember: bluebird"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "ok"}}},
	}
	projDir, err := CLIProjectDir(work)
	if err != nil {
		t.Fatalf("CLIProjectDir: %v", err)
	}
	fileExists := func(sid string) bool {
		_, err := os.Stat(filepath.Join(projDir, sid+".jsonl"))
		return err == nil
	}

	// No resume ID: nothing to do.
	if got := s.ensureResumeTarget("", work, msgs); got != "" {
		t.Fatalf("empty resumeSID: got %q, want \"\"", got)
	}

	// CLI transcript present on disk: keep it untouched so its richer history
	// (tool results, thinking) survives.
	existing, err := WriteCLISessionFile(work, work, "", msgs)
	if err != nil {
		t.Fatalf("seed CLI session file: %v", err)
	}
	if !fileExists(existing) {
		t.Fatalf("seed file %s not on disk", existing)
	}
	if got := s.ensureResumeTarget(existing, work, msgs); got != existing {
		t.Fatalf("existing target: got %q, want unchanged %q", got, existing)
	}

	// Target missing but messages present: rebuild from them and return a fresh,
	// on-disk ID. This is the young-session case the bug hit.
	got := s.ensureResumeTarget("ghost-does-not-exist", work, msgs)
	if got == "" || got == "ghost-does-not-exist" {
		t.Fatalf("missing target with messages: got %q, want a rebuilt ID", got)
	}
	if !fileExists(got) {
		t.Fatalf("rebuilt target %s was not written to disk", got)
	}

	// Target missing and no messages to rebuild from: start fresh (clear the ID).
	if got := s.ensureResumeTarget("ghost-does-not-exist", work, nil); got != "" {
		t.Fatalf("missing target, no messages: got %q, want \"\"", got)
	}
}
