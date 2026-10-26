// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package githook

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	// Tests may themselves run inside a Trellis agent session.
	cmd.Env = append(cmd.Env, SessionEnv+"=")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitT(t, dir, nil, "init", "-q", "-b", "main")
	gitT(t, dir, nil, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func trailers(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitT(t, dir, nil, "log", "-1", "--format=%(trailers:only,unfold)"))
}

func TestHookAddsTrailersOnlyForAgentCommits(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	if changed, err := Install(ctx, dir, "trellis/cases"); err != nil || !changed {
		t.Fatalf("Install = %v, %v", changed, err)
	}
	if changed, _ := Install(ctx, dir, "trellis/cases"); changed {
		t.Error("second Install rewrote an identical hook")
	}
	agent := []string{SessionEnv + "=claude:sess-1"}

	// Human commit: untouched.
	gitT(t, dir, nil, "commit", "-q", "--allow-empty", "-m", "by hand")
	if tr := trailers(t, dir); tr != "" {
		t.Errorf("human commit got trailers %q", tr)
	}

	// Agent commit, no open case: session trailer only.
	gitT(t, dir, agent, "commit", "-q", "--allow-empty", "-m", "agent, no case")
	if tr := trailers(t, dir); tr != "Trellis-Session: claude:sess-1" {
		t.Errorf("trailers = %q", tr)
	}

	// Agent commit with an open case: both, once, even on amend.
	os.MkdirAll(filepath.Join(dir, "trellis", "cases", "2026-10-26__thing"), 0o755)
	os.WriteFile(filepath.Join(dir, "trellis", "cases", "2026-10-26__thing", "case.json"), []byte("{}"), 0o644)
	gitT(t, dir, agent, "commit", "-q", "--allow-empty", "-m", "agent, open case")
	gitT(t, dir, agent, "commit", "-q", "--allow-empty", "--amend", "--no-edit")
	want := "Trellis-Session: claude:sess-1\nTrellis-Case: 2026-10-26__thing"
	if tr := trailers(t, dir); tr != want {
		t.Errorf("trailers = %q, want %q", tr, want)
	}

	if removed, err := Remove(ctx, dir); err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	gitT(t, dir, agent, "commit", "-q", "--allow-empty", "-m", "after removal")
	if tr := trailers(t, dir); tr != "" {
		t.Errorf("removed hook still ran: %q", tr)
	}
}

func TestInstallBesideUserHook(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	hooks := filepath.Join(dir, ".git", "hooks")
	os.MkdirAll(hooks, 0o755)
	user := filepath.Join(hooks, hookName)
	os.WriteFile(user, []byte("#!/bin/sh\nexit 0\n"), 0o755)

	_, err := Install(ctx, dir, "trellis/cases")
	if err == nil || !strings.Contains(err.Error(), sideName) {
		t.Fatalf("Install err = %v, want instructions naming %s", err, sideName)
	}
	if b, _ := os.ReadFile(user); string(b) != "#!/bin/sh\nexit 0\n" {
		t.Error("user hook was modified")
	}
	if _, err := os.Stat(filepath.Join(hooks, sideName)); err != nil {
		t.Fatalf("side script not written: %v", err)
	}

	// Once the user's hook calls the side script, Install is quiet and the
	// chained hook works.
	os.WriteFile(user, []byte("#!/bin/sh\n\"$(dirname \"$0\")/"+sideName+"\" \"$@\"\n"), 0o755)
	if _, err := Install(ctx, dir, "trellis/cases"); err != nil {
		t.Errorf("Install with chained user hook: %v", err)
	}
	gitT(t, dir, []string{SessionEnv + "=codex:c-9"}, "commit", "-q", "--allow-empty", "-m", "chained")
	if tr := trailers(t, dir); tr != "Trellis-Session: codex:c-9" {
		t.Errorf("trailers = %q", tr)
	}

	if removed, _ := Remove(ctx, dir); !removed {
		t.Error("side script not removed")
	}
	if _, err := os.Stat(user); err != nil {
		t.Error("Remove deleted the user's hook")
	}
}

func TestInstallRefusesHooksPath(t *testing.T) {
	dir := newRepo(t)
	gitT(t, dir, nil, "config", "core.hooksPath", ".husky")
	if _, err := Install(context.Background(), dir, "trellis/cases"); err == nil || !strings.Contains(err.Error(), "core.hooksPath") {
		t.Errorf("Install err = %v, want core.hooksPath refusal", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".husky")); !os.IsNotExist(err) {
		t.Error("wrote into core.hooksPath")
	}
}
