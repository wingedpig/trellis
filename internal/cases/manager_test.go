// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package cases

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newManagerWithTempDir(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	return NewManager("cases"), dir
}

func TestCreateAndGet_basic(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	c, err := m.Create(wt, "Fix login crash", "bug", "wt1", "main", "abc1234")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Title != "Fix login crash" {
		t.Errorf("title = %q, want %q", c.Title, "Fix login crash")
	}
	got, err := m.Get(wt, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("ID mismatch: %q vs %q", got.ID, c.ID)
	}
}

func TestCreate_rejectsSecondOpenCase(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	if _, err := m.Create(wt, "First", "feature", "wt1", "main", ""); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := m.Create(wt, "Second", "feature", "wt1", "main", "")
	if err != ErrOpenCaseExists {
		t.Fatalf("second create: want ErrOpenCaseExists, got %v", err)
	}
}

func TestCreate_allowedAfterArchive(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	c, err := m.Create(wt, "First", "feature", "wt1", "main", "")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := m.Archive(wt, c.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := m.Create(wt, "Second", "feature", "wt1", "main", ""); err != nil {
		t.Fatalf("second create after archive: %v", err)
	}
}

func TestOpenCaseExistsAndFirstOpenCase(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	if m.OpenCaseExists(wt) {
		t.Error("OpenCaseExists on empty worktree: want false")
	}
	if got, _ := m.FirstOpenCase(wt); got != nil {
		t.Error("FirstOpenCase on empty worktree: want nil")
	}
	c, _ := m.Create(wt, "Hello", "task", "wt1", "main", "")
	if !m.OpenCaseExists(wt) {
		t.Error("OpenCaseExists after Create: want true")
	}
	got, err := m.FirstOpenCase(wt)
	if err != nil {
		t.Fatalf("FirstOpenCase: %v", err)
	}
	if got == nil || got.ID != c.ID {
		t.Errorf("FirstOpenCase: got %v, want id %q", got, c.ID)
	}
}

func TestAppendCommit_andRoundTrip(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	c, _ := m.Create(wt, "Commit timeline", "feature", "wt1", "main", "")

	entries := []CommitEntry{
		{SHA: "aaaa1111", ShortSHA: "aaaa111", CommittedAt: time.Now(), Message: "feat: thing", Description: "Added thing.", FilesChanged: []string{"a.go"}},
		{SHA: "bbbb2222", ShortSHA: "bbbb222", CommittedAt: time.Now(), Message: "fix: bug", Description: "Fixed regression.", FilesChanged: []string{"b.go", "c.go"}},
	}
	for _, e := range entries {
		if err := m.AppendCommit(wt, c.ID, e); err != nil {
			t.Fatalf("AppendCommit: %v", err)
		}
	}
	got, err := m.Get(wt, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Commits) != 2 {
		t.Fatalf("commits len = %d, want 2", len(got.Commits))
	}
	if got.Commits[0].SHA != "aaaa1111" || got.Commits[1].SHA != "bbbb2222" {
		t.Errorf("commits SHAs unexpected: %+v", got.Commits)
	}
	if len(got.Commits[1].FilesChanged) != 2 {
		t.Errorf("FilesChanged not preserved")
	}
}

func TestSummary_setAndUpdate(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	c, _ := m.Create(wt, "Summary test", "bug", "wt1", "main", "")

	summary := &CaseSummary{
		Synopsis:    "one line",
		Symptoms:    "obvious",
		RootCause:   "missing init",
		Resolution:  "initialize earlier",
		Components:  []string{"auth", "session"},
		Keywords:    []string{"ENOENT", "TLS"},
		GeneratedAt: time.Now(),
		Model:       "claude-test",
	}
	if err := m.SetSummary(wt, c.ID, summary); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}
	got, _ := m.Get(wt, c.ID)
	if got.Summary == nil || got.Summary.Synopsis != "one line" {
		t.Fatalf("SetSummary did not persist: %+v", got.Summary)
	}
	if len(got.Summary.Keywords) != 2 {
		t.Errorf("keywords not preserved")
	}

	// Partial update.
	newSyn := "two lines"
	if err := m.UpdateSummary(wt, c.ID, SummaryUpdate{Synopsis: &newSyn}); err != nil {
		t.Fatalf("UpdateSummary: %v", err)
	}
	got, _ = m.Get(wt, c.ID)
	if got.Summary.Synopsis != "two lines" {
		t.Errorf("synopsis not updated")
	}
	if got.Summary.RootCause != "missing init" {
		t.Errorf("unchanged field was overwritten: %q", got.Summary.RootCause)
	}
}

func TestUpdateSummary_worksOnArchived(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	c, _ := m.Create(wt, "Archived edit", "task", "wt1", "main", "")
	syn := "first"
	_ = m.SetSummary(wt, c.ID, &CaseSummary{Synopsis: syn, GeneratedAt: time.Now()})
	if err := m.Archive(wt, c.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	newSyn := "edited"
	if err := m.UpdateSummary(wt, c.ID, SummaryUpdate{Synopsis: &newSyn}); err != nil {
		t.Fatalf("UpdateSummary on archived: %v", err)
	}
	got, err := m.Get(wt, c.ID)
	if err != nil {
		t.Fatalf("Get archived: %v", err)
	}
	if got.Summary == nil || got.Summary.Synopsis != "edited" {
		t.Errorf("archived synopsis not updated: %+v", got.Summary)
	}
}

func TestReplaceSummary_worksOnArchived(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	c, _ := m.Create(wt, "Replace test", "task", "wt1", "main", "")
	_ = m.SetSummary(wt, c.ID, &CaseSummary{Synopsis: "before"})
	_ = m.Archive(wt, c.ID)
	if err := m.ReplaceSummary(wt, c.ID, &CaseSummary{Synopsis: "after", GeneratedAt: time.Now()}); err != nil {
		t.Fatalf("ReplaceSummary: %v", err)
	}
	got, _ := m.Get(wt, c.ID)
	if got.Summary.Synopsis != "after" {
		t.Errorf("ReplaceSummary did not take effect")
	}
}

func TestIsArchived(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	c, _ := m.Create(wt, "Stateful", "task", "wt1", "main", "")
	if m.IsArchived(wt, c.ID) {
		t.Error("freshly-created case reported archived")
	}
	if err := m.Archive(wt, c.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !m.IsArchived(wt, c.ID) {
		t.Error("archived case reported not-archived")
	}
}

func TestReopen_refusesIfAnotherCaseIsOpen(t *testing.T) {
	m, wt := newManagerWithTempDir(t)

	first, _ := m.Create(wt, "Original", "feature", "wt1", "main", "")
	if err := m.Archive(wt, first.ID); err != nil {
		t.Fatalf("archive first: %v", err)
	}

	// Open a second, unrelated case in the same worktree.
	if _, err := m.Create(wt, "Second", "feature", "wt1", "main", ""); err != nil {
		t.Fatalf("second create: %v", err)
	}

	// Reopen of the first must now refuse — otherwise the worktree would
	// briefly hold two open cases, breaking the invariant.
	err := m.Reopen(wt, first.ID)
	if err != cases_ErrOpenCaseExists() {
		t.Fatalf("reopen with open case present: want ErrOpenCaseExists, got %v", err)
	}
	if !m.IsArchived(wt, first.ID) {
		t.Error("refused reopen left the case in a bad state — still expected archived")
	}
}

// cases_ErrOpenCaseExists is a tiny shim so the test reads cleanly without
// stuttering "cases.cases.ErrOpenCaseExists" through the import alias.
func cases_ErrOpenCaseExists() error { return ErrOpenCaseExists }

func TestSchemaBackwardCompat_oldCaseJsonStillLoads(t *testing.T) {
	// Verify that a case.json written without commits[] or summary{} still
	// loads cleanly — Commits comes back nil-or-empty, Summary nil.
	m, wt := newManagerWithTempDir(t)
	caseDir := filepath.Join(wt, "cases", "2025-01-01__legacy")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{
		"schema": "trellis.case.v1",
		"id": "2025-01-01__legacy",
		"title": "Legacy",
		"kind": "bug",
		"status": "open",
		"created_at": "2025-01-01T00:00:00Z",
		"updated_at": "2025-01-01T00:00:00Z",
		"worktree": {"name": "wt1", "branch": "main"}
	}`
	if err := os.WriteFile(filepath.Join(caseDir, "case.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := m.Get(wt, "2025-01-01__legacy")
	if err != nil {
		t.Fatalf("Get legacy: %v", err)
	}
	if got.Commits != nil && len(got.Commits) != 0 {
		t.Errorf("legacy Commits should be empty, got %d", len(got.Commits))
	}
	if got.Summary != nil {
		t.Errorf("legacy Summary should be nil, got %+v", got.Summary)
	}
}
