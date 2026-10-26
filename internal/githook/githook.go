// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package githook installs the prepare-commit-msg hook that links commits
// made by Trellis agent sessions to their session and open case.
//
// Trellis sets SessionEnv on every Claude and Codex process it starts, so a
// `git commit` the agent runs inherits it. The hook does nothing when the
// variable is unset, which keeps commits made outside agent sessions
// untouched.
package githook

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SessionEnv names the environment variable carrying "<agent>:<session-id>"
// for the agent process Trellis started.
const SessionEnv = "TRELLIS_SESSION"

// Marker identifies a hook as trellis-managed. Hooks without the marker are
// treated as user-owned and never overwritten or removed.
const Marker = "managed-by: trellis"

const hookName = "prepare-commit-msg"

// sideName is where the script goes when the repo already has its own
// prepare-commit-msg: next to it, for the user's hook to call.
const sideName = hookName + ".trellis"

// Script returns the hook for a repo whose open cases live in casesRelDir
// (relative to each worktree root).
func Script(casesRelDir string) string {
	return `#!/bin/sh
# ` + Marker + ` (cases.commit_hook)
#
# Adds Trellis-Session (and Trellis-Case, when the worktree has an open case)
# trailers to commits made by Trellis agent sessions. Does nothing unless
# ` + SessionEnv + ` is set, so other commits are untouched. Trellis rewrites
# this file on start while cases.commit_hook is on and removes it when off;
# delete the marker line above to keep your own edits.

[ -n "$` + SessionEnv + `" ] || exit 0
case "$2" in merge|squash) exit 0 ;; esac

msg="$1"
git interpret-trailers --in-place --if-exists addIfDifferent \
	--trailer "Trellis-Session: $` + SessionEnv + `" "$msg" || exit 0

top=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
case_id=
for f in "$top"/` + shellQuote(filepath.ToSlash(casesRelDir)) + `/*/case.json; do
	[ -f "$f" ] || continue
	# Exactly one open case per worktree; more is ambiguous, so skip.
	[ -z "$case_id" ] || exit 0
	case_id=$(basename "$(dirname "$f")")
done
[ -n "$case_id" ] || exit 0
git interpret-trailers --in-place --if-exists addIfDifferent \
	--trailer "Trellis-Case: $case_id" "$msg" || exit 0
`
}

// shellQuote single-quotes s for the shell.
func shellQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

// hookPath returns where git looks for the repo's prepare-commit-msg hook.
// The hooks directory lives in the common git dir, so one hook covers every
// worktree. It errors when core.hooksPath is set: that directory is usually
// a tool's (husky) or committed to the repo, so it isn't ours to write.
func hookPath(ctx context.Context, repoDir string) (string, error) {
	if out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "config", "--get", "core.hooksPath").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return "", fmt.Errorf("core.hooksPath is set to %s; add Trellis's %s hook there yourself (see docs/pages/cases.md)", p, hookName)
		}
	}
	out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("not a git repo: %s", repoDir)
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoDir, dir)
	}
	return filepath.Join(dir, "hooks", hookName), nil
}

// Install writes the hook into repoDir's hooks directory, refreshing a
// trellis-managed copy that has drifted. When the repo already has its own
// prepare-commit-msg, the script is written beside it as
// prepare-commit-msg.trellis instead, and the returned error says how to call
// it unless the user's hook already does. Returns true when a file was
// written.
func Install(ctx context.Context, repoDir, casesRelDir string) (bool, error) {
	target, err := hookPath(ctx, repoDir)
	if err != nil {
		return false, err
	}
	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err == nil && !bytes.Contains(existing, []byte(Marker)) {
		side := filepath.Join(filepath.Dir(target), sideName)
		changed, err := writeIfChanged(side, casesRelDir)
		if err != nil || bytes.Contains(existing, []byte(sideName)) {
			return changed, err
		}
		return changed, fmt.Errorf("%s is not Trellis's; to link agent commits, add this line to it:\n\t\"$(dirname \"$0\")/%s\" \"$@\"", target, sideName)
	}
	return writeIfChanged(target, casesRelDir)
}

func writeIfChanged(path, casesRelDir string) (bool, error) {
	want := []byte(Script(casesRelDir))
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, want) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, want, 0o755); err != nil {
		return false, err
	}
	// WriteFile keeps an existing file's mode; the hook must be executable.
	return true, os.Chmod(path, 0o755)
}

// Remove deletes trellis-managed hooks (the hook itself or the side file).
// User-owned hooks are left alone. Returns true when a file was removed.
func Remove(ctx context.Context, repoDir string) (bool, error) {
	target, err := hookPath(ctx, repoDir)
	if err != nil {
		return false, nil // nothing of ours can be installed there
	}
	removed := false
	for _, p := range []string{target, filepath.Join(filepath.Dir(target), sideName)} {
		existing, err := os.ReadFile(p)
		if os.IsNotExist(err) || (err == nil && !bytes.Contains(existing, []byte(Marker))) {
			continue
		}
		if err != nil {
			return removed, err
		}
		if err := os.Remove(p); err != nil {
			return removed, err
		}
		removed = true
	}
	return removed, nil
}
