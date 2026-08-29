// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// newTestManager builds a manager whose background persists are drained
// before the test's temp directory is cleaned up. CreateSession persists in a
// goroutine, so without the drain a late write recreates the directory under
// t.TempDir()'s cleanup and the test fails on it.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(t.TempDir())
	t.Cleanup(m.Shutdown)
	return m
}

// seed gives a session a conversation without going near the real CLI.
func seed(s *Session, msgs ...Message) {
	s.mu.Lock()
	s.messages = append(s.messages, msgs...)
	s.mu.Unlock()
}

func userMsg(text string) Message {
	return Message{Role: "user", Content: []ContentBlock{{Type: "text", Text: text}}, Timestamp: time.Now()}
}

func assistantMsg(text string) Message {
	return Message{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: text}}, Timestamp: time.Now()}
}

func TestArchiveHidesSessionFromListings(t *testing.T) {
	m := newTestManager(t)
	kept := m.CreateSession("wt", t.TempDir(), "Keep")
	filed := m.CreateSession("wt", t.TempDir(), "File away")

	if err := m.ArchiveSession(filed.ID()); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	if got := m.ListSessions("wt"); len(got) != 1 || got[0].ID != kept.ID() {
		t.Fatalf("ListSessions = %v, want only %s", got, kept.ID())
	}
	if got := m.AllSessions(); len(got) != 1 {
		t.Fatalf("AllSessions returned %d sessions, want 1", len(got))
	}
	// An archived session is not in the trash either — the two are distinct.
	if got := m.ListTrashedSessions("wt"); len(got) != 0 {
		t.Fatalf("ListTrashedSessions returned %d, want 0", len(got))
	}
	archived := m.ListArchivedSessions("wt")
	if len(archived) != 1 || archived[0].ID != filed.ID() {
		t.Fatalf("ListArchivedSessions = %v, want only %s", archived, filed.ID())
	}
	if archived[0].ArchivedAt == nil {
		t.Fatal("archived session has no ArchivedAt timestamp")
	}
}

func TestUnarchiveRestoresToNormalListing(t *testing.T) {
	m := newTestManager(t)
	s := m.CreateSession("wt", t.TempDir(), "Session 1")

	if err := m.ArchiveSession(s.ID()); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if err := m.UnarchiveSession(s.ID()); err != nil {
		t.Fatalf("UnarchiveSession: %v", err)
	}

	if got := m.ListSessions("wt"); len(got) != 1 || got[0].ID != s.ID() {
		t.Fatalf("ListSessions after unarchive = %v, want the session back", got)
	}
	if got := m.ListArchivedSessions("wt"); len(got) != 0 {
		t.Fatalf("ListArchivedSessions after unarchive returned %d, want 0", len(got))
	}
}

// Archive and trash are mutually exclusive, so a session is always in exactly
// one place and restoring from trash never drops it back into the archive.
func TestArchiveAndTrashAreExclusive(t *testing.T) {
	m := newTestManager(t)
	s := m.CreateSession("wt", t.TempDir(), "Session 1")

	if err := m.ArchiveSession(s.ID()); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if err := m.TrashSession(s.ID()); err != nil {
		t.Fatalf("TrashSession: %v", err)
	}
	if got := m.ListArchivedSessions("wt"); len(got) != 0 {
		t.Fatalf("trashed session still in the archive: %v", got)
	}
	if got := m.ListTrashedSessions("wt"); len(got) != 1 {
		t.Fatalf("ListTrashedSessions = %d, want 1", len(got))
	}

	if err := m.RestoreSession(s.ID()); err != nil {
		t.Fatalf("RestoreSession: %v", err)
	}
	if got := m.ListSessions("wt"); len(got) != 1 {
		t.Fatalf("restored session is not in the normal listing: %v", got)
	}
}

func TestArchiveSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	s := m.CreateSession("wt", t.TempDir(), "Session 1")
	if err := m.ArchiveSession(s.ID()); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	m.Shutdown() // drain pending writes before reopening the same state dir
	reloaded := NewManager(dir)
	t.Cleanup(reloaded.Shutdown)
	archived := reloaded.ListArchivedSessions("wt")
	if len(archived) != 1 || archived[0].ID != s.ID() {
		t.Fatalf("archive did not survive reload: %v", archived)
	}
	if got := reloaded.ListSessions("wt"); len(got) != 0 {
		t.Fatalf("archived session came back as active after reload: %v", got)
	}
}

func TestSearchArchivedSessions(t *testing.T) {
	m := newTestManager(t)

	hit := m.CreateSession("wt", t.TempDir(), "Session 1")
	seed(hit, userMsg("the SSE flusher drops events"), assistantMsg("Fixed the flusher."))
	miss := m.CreateSession("wt", t.TempDir(), "Session 2")
	seed(miss, userMsg("rename the config loader"))
	active := m.CreateSession("wt", t.TempDir(), "Session 3")
	seed(active, userMsg("the SSE flusher again"))

	for _, s := range []*Session{hit, miss} {
		if err := m.ArchiveSession(s.ID()); err != nil {
			t.Fatalf("ArchiveSession: %v", err)
		}
	}

	got := m.SearchArchivedSessions(ArchiveQuery{Terms: SearchTerms("SSE flusher")})
	if len(got) != 1 || got[0].ID != hit.ID() {
		t.Fatalf("search = %v, want only the archived flusher session", got)
	}
	if got[0].Score == 0 {
		t.Error("matching session scored 0")
	}
	if got[0].Snippet == "" {
		t.Error("matching session has no snippet")
	}

	// Every term has to land somewhere in the session, so an extra word that
	// appears nowhere rules it out.
	if got := m.SearchArchivedSessions(ArchiveQuery{Terms: SearchTerms("SSE nonexistent")}); len(got) != 0 {
		t.Errorf("partial match returned %d results, want 0", len(got))
	}

	// Browse mode: no terms lists the whole archive, newest activity first.
	if got := m.SearchArchivedSessions(ArchiveQuery{}); len(got) != 2 {
		t.Errorf("browse mode returned %d results, want 2", len(got))
	}

	// Worktree scoping.
	if got := m.SearchArchivedSessions(ArchiveQuery{Worktree: "other"}); len(got) != 0 {
		t.Errorf("search of an unrelated worktree returned %d results, want 0", len(got))
	}
}

func TestSearchArchivedSessionsToolScope(t *testing.T) {
	m := newTestManager(t)
	s := m.CreateSession("wt", t.TempDir(), "Session 1")
	seed(s, Message{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "text", Text: "Running the suite."},
			{Type: "tool_use", Name: "Bash", Input: []byte(`{"command":"go test ./internal/quarkloop"}`)},
		},
		Timestamp: time.Now(),
	})
	if err := m.ArchiveSession(s.ID()); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	if got := m.SearchArchivedSessions(ArchiveQuery{Terms: SearchTerms("quarkloop")}); len(got) != 0 {
		t.Errorf("prose-only search matched tool input: %v", got)
	}
	if got := m.SearchArchivedSessions(ArchiveQuery{Terms: SearchTerms("quarkloop"), IncludeTools: true}); len(got) != 1 {
		t.Errorf("include-tools search returned %d results, want 1", len(got))
	}
}

func TestSearchArchivedSessionsDateBounds(t *testing.T) {
	m := newTestManager(t)
	s := m.CreateSession("wt", t.TempDir(), "Session 1")
	if err := m.ArchiveSession(s.ID()); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	past := time.Now().Add(-24 * time.Hour)
	future := time.Now().Add(24 * time.Hour)
	if got := m.SearchArchivedSessions(ArchiveQuery{From: past, To: future}); len(got) != 1 {
		t.Errorf("session outside its own date range: %d results, want 1", len(got))
	}
	if got := m.SearchArchivedSessions(ArchiveQuery{From: future}); len(got) != 0 {
		t.Errorf("from-bound in the future returned %d results, want 0", len(got))
	}
	if got := m.SearchArchivedSessions(ArchiveQuery{To: past}); len(got) != 0 {
		t.Errorf("to-bound in the past returned %d results, want 0", len(got))
	}
}

func TestSnippetAroundKeepsRunesIntact(t *testing.T) {
	// The match sits far enough in that both cuts land mid-rune without the
	// boundary snapping.
	text := strings.Repeat("é", 200) + " needle " + strings.Repeat("ø", 200)
	got := snippetAround(text, "needle")
	if !utf8.ValidString(got) {
		t.Fatalf("snippet is not valid UTF-8: %q", got)
	}
	if !strings.Contains(got, "needle") {
		t.Fatalf("snippet lost the match: %q", got)
	}
}

// Concurrent persists used to race on a shared sessions.json.tmp: the loser's
// rename failed with ENOENT and its snapshot was dropped. The failure is
// swallowed and only logged, so this asserts on the log.
func TestConcurrentPersistDoesNotRace(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	t.Cleanup(m.Shutdown)
	for i := 0; i < 5; i++ {
		m.CreateSession("wt", t.TempDir(), fmt.Sprintf("Session %d", i+1))
	}

	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.persist()
		}()
	}
	wg.Wait()

	if out := logs.String(); out != "" {
		t.Fatalf("concurrent persist logged errors:\n%s", out)
	}

	// Every session must still be on disk after the dust settles.
	records, err := loadRecords(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("loadRecords: %v", err)
	}
	if len(records) != 5 {
		t.Fatalf("persisted %d records, want 5", len(records))
	}
}
