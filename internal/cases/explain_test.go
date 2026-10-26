// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package cases

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/claude"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// explainFixture is a repo with an open case whose Claude transcript wrote
// line 3 of hello.go with the Edit tool.
func explainFixture(t *testing.T) (*Manager, string, *CaseJSON) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	wt := t.TempDir()
	gitT(t, wt, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(wt, ".gitignore"), []byte("cases/\n"), 0o644)
	gitT(t, wt, "add", ".gitignore")
	gitT(t, wt, "commit", "-q", "-m", "init")

	m := NewManager("cases")
	c, err := m.Create(wt, "Greeting", "feature", "wt", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	edit, _ := json.Marshal(map[string]string{
		"file_path":  filepath.Join(wt, "hello.go"),
		"old_string": "",
		"new_string": "\tfmt.Println(\"hello, provenance\")",
	})
	now := time.Now()
	tr := &claude.Transcript{Messages: []claude.Message{
		{Role: "user", Timestamp: now, Content: []claude.ContentBlock{{Type: "text", Text: "Print a greeting"}}},
		{Role: "assistant", Timestamp: now, Content: []claude.ContentBlock{
			{Type: "text", Text: "Adding the greeting to main."},
			{Type: "tool_use", Name: "Edit", Input: edit},
		}},
		{Role: "user", Timestamp: now, Content: []claude.ContentBlock{{Type: "tool_result", Content: "ok"}}},
	}}
	if err := m.SaveTranscript(wt, c.ID, "t1", "session one", "sess-1", tr); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt, "hello.go"), []byte("package main\nfunc main() {\n\tfmt.Println(\"hello, provenance\")\n}\n"), 0o644)
	gitT(t, wt, "add", "hello.go")
	return m, wt, c
}

func TestExplain_trailerLinksCaseAndTurn(t *testing.T) {
	m, wt, c := explainFixture(t)
	gitT(t, wt, "commit", "-q", "-m", "Add greeting", "--trailer", CommitTrailer+": "+c.ID)

	p, err := m.Explain(context.Background(), wt, "hello.go", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.CaseID != c.ID || p.LinkedBy != "trailer" {
		t.Fatalf("case = %q via %q, want %q via trailer", p.CaseID, p.LinkedBy, c.ID)
	}
	if len(p.Turns) != 1 {
		t.Fatalf("turns = %+v, want 1", p.Turns)
	}
	turn := p.Turns[0]
	if !turn.MatchedLine || turn.Tool != "Edit" || turn.Prompt != "Print a greeting" || turn.Note != "Adding the greeting to main." {
		t.Errorf("turn = %+v", turn)
	}
}

func TestExplain_legacyTagAndCommitLookup(t *testing.T) {
	m, wt, c := explainFixture(t)
	gitT(t, wt, "commit", "-q", "-m", "Add greeting [case: "+c.ID+"]")
	sha := gitT(t, wt, "rev-parse", "HEAD")

	p, err := m.Explain(context.Background(), wt, "", 0, sha[:10])
	if err != nil {
		t.Fatal(err)
	}
	if p.CaseID != c.ID || p.LinkedBy != "case-tag" {
		t.Errorf("case = %q via %q, want %q via case-tag", p.CaseID, p.LinkedBy, c.ID)
	}
}

// A commit made outside Trellis carries no link; the transcript match finds
// the case anyway.
func TestExplain_unlinkedCommitFallsBackToTranscript(t *testing.T) {
	m, wt, c := explainFixture(t)
	gitT(t, wt, "commit", "-q", "-m", "agent commit")

	p, err := m.Explain(context.Background(), wt, "hello.go", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.CaseID != c.ID || p.LinkedBy != "transcript" || len(p.Turns) != 1 {
		t.Errorf("case = %q via %q, turns %d; want %q via transcript, 1 turn", p.CaseID, p.LinkedBy, len(p.Turns), c.ID)
	}

	// Generic lines don't fall back: "}" would match every edit.
	p, err = m.Explain(context.Background(), wt, "hello.go", 4, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.CaseID != "" || len(p.Turns) != 0 {
		t.Errorf("generic line linked to %q with %d turns, want none", p.CaseID, len(p.Turns))
	}
}

func TestExplain_uncommittedLineUsesOpenCase(t *testing.T) {
	m, wt, c := explainFixture(t)
	gitT(t, wt, "commit", "-q", "-m", "x")
	os.WriteFile(filepath.Join(wt, "hello.go"), []byte("package main\nfunc main() {\n\tfmt.Println(\"hello, provenance\")\n\tfmt.Println(\"uncommitted extra line\")\n}\n"), 0o644)

	p, err := m.Explain(context.Background(), wt, "hello.go", 4, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Commit == nil || !p.Commit.Uncommitted || p.CaseID != c.ID || p.LinkedBy != "open-case" {
		t.Errorf("commit %+v, case %q via %q", p.Commit, p.CaseID, p.LinkedBy)
	}
}

// A commit an agent made itself carries only the hook's session trailer; the
// case holding that session's transcript is found through it.
func TestExplain_sessionTrailer(t *testing.T) {
	m, wt, c := explainFixture(t)
	gitT(t, wt, "commit", "-q", "-m", "agent commit", "--trailer", SessionTrailer+": claude:sess-1")

	p, err := m.Explain(context.Background(), wt, "hello.go", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.CaseID != c.ID || p.LinkedBy != "session" || p.Session != "claude:sess-1" {
		t.Fatalf("case = %q via %q, session %q", p.CaseID, p.LinkedBy, p.Session)
	}
	if len(p.Turns) != 1 || p.Turns[0].Session != "claude:sess-1" {
		t.Errorf("turns = %+v", p.Turns)
	}
}

func TestSessionCommits(t *testing.T) {
	m, wt, c := explainFixture(t)
	ctx := context.Background()
	commit := func(msg string, trailers ...string) {
		t.Helper()
		args := []string{"commit", "-q", "--allow-empty", "-m", msg}
		for _, tr := range trailers {
			args = append(args, "--trailer", tr)
		}
		gitT(t, wt, args...)
	}
	gitT(t, wt, "commit", "-q", "-m", "attached session", "--trailer", SessionTrailer+": claude:sess-1")
	commit("other session, this case", SessionTrailer+": codex:cx-2", CommitTrailer+": "+c.ID)
	commit("unrelated session", SessionTrailer+": claude:stranger")
	commit("attached session, other case", SessionTrailer+": claude:sess-1", CommitTrailer+": 2026-01-01__old")
	commit("server commit", CommitTrailer+": "+c.ID)
	commit("by hand")

	entries, unattached, err := m.SessionCommits(ctx, wt, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.SessionID+" "+e.Message)
	}
	want := []string{
		"claude:sess-1 attached session\n\nTrellis-Session: claude:sess-1",
		"codex:cx-2 other session, this case\n\nTrellis-Session: codex:cx-2\nTrellis-Case: " + c.ID,
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("entries:\n%q\nwant:\n%q", got, want)
	}
	if len(entries[0].FilesChanged) != 1 || entries[0].FilesChanged[0] != "hello.go" {
		t.Errorf("files = %v", entries[0].FilesChanged)
	}
	if len(unattached) != 1 || unattached[0] != "codex:cx-2" {
		t.Errorf("unattached = %v", unattached)
	}

	// A session passed in (the one committing through Trellis) counts too.
	if entries, _, _ := m.SessionCommits(ctx, wt, c.ID, "claude:stranger"); len(entries) != 3 {
		t.Errorf("with extra session: %d entries, want 3", len(entries))
	}

	// Recorded commits aren't found again, nor are rebased copies of them.
	if err := m.AppendCommits(wt, c.ID, entries); err != nil {
		t.Fatal(err)
	}
	gitT(t, wt, "commit", "-q", "--allow-empty", "-m", "attached session", "--trailer", SessionTrailer+": claude:sess-1")
	if entries, _, _ := m.SessionCommits(ctx, wt, c.ID); len(entries) != 0 {
		t.Errorf("after recording: %+v", entries)
	}
	cj, _ := m.Get(wt, c.ID)
	if len(cj.Commits) != 2 || !cj.Commits[0].CommittedAt.Before(cj.Commits[1].CommittedAt.Add(time.Second)) {
		t.Errorf("commits = %+v", cj.Commits)
	}
}
