// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"testing"

	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
)

// Wrap-up files the finished session in the archive rather than the trash:
// its transcript is already committed to the case, so the session is exactly
// the kind of record the archive keeps searchable. Trash would delete it
// after 7 days.
func TestWrapUpArchivesActiveSession(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		m := claude.NewManager(t.TempDir())
		t.Cleanup(m.Shutdown)
		s := m.CreateSession("wt", t.TempDir(), "Session 1")

		claudeAdapter{m: m}.archiveSession(s.ID())

		if got := m.ListArchivedSessions("wt"); len(got) != 1 || got[0].ID != s.ID() {
			t.Fatalf("session was not archived: %v", got)
		}
		if got := m.ListTrashedSessions("wt"); len(got) != 0 {
			t.Fatalf("session went to the trash instead: %v", got)
		}
		if got := m.ListSessions("wt"); len(got) != 0 {
			t.Fatalf("archived session still in the worktree listing: %v", got)
		}
	})

	t.Run("codex", func(t *testing.T) {
		m := codex.NewManager(t.TempDir())
		t.Cleanup(m.Shutdown)
		s := m.CreateSession("wt", t.TempDir(), "Session 1")

		codexAdapter{m: m}.archiveSession(s.ID())

		if got := m.ListArchivedSessions("wt"); len(got) != 1 || got[0].ID != s.ID() {
			t.Fatalf("session was not archived: %v", got)
		}
		if got := m.ListTrashedSessions("wt"); len(got) != 0 {
			t.Fatalf("session went to the trash instead: %v", got)
		}
		if got := m.ListSessions("wt"); len(got) != 0 {
			t.Fatalf("archived session still in the worktree listing: %v", got)
		}
	})
}

// A nil manager or empty session id is a no-op, not a panic — wrap-up calls
// this best-effort.
func TestArchiveSessionAdapterTolerantOfMissingInputs(t *testing.T) {
	claudeAdapter{}.archiveSession("some-id")
	codexAdapter{}.archiveSession("some-id")

	m := claude.NewManager(t.TempDir())
	t.Cleanup(m.Shutdown)
	claudeAdapter{m: m}.archiveSession("")
}
