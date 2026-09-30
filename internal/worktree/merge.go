// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package worktree

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/wingedpig/trellis/internal/events"
)

// MergeStrategy selects how a branch lands on the default branch.
type MergeStrategy string

const (
	// MergeFastForward rebases the branch onto the default branch in its own
	// worktree, then fast-forwards the default branch. History stays linear
	// and pre_merge hooks test exactly the tree that lands.
	MergeFastForward MergeStrategy = "ff"
	// MergeCommit merges the branch with a merge commit (--no-ff).
	MergeCommit MergeStrategy = "merge"
	// MergeSquash squashes the branch into a single commit on the default branch.
	MergeSquash MergeStrategy = "squash"
)

// ParseMergeStrategy validates a strategy name. Empty means MergeFastForward.
func ParseMergeStrategy(s string) (MergeStrategy, error) {
	switch MergeStrategy(s) {
	case "", MergeFastForward:
		return MergeFastForward, nil
	case MergeCommit, MergeSquash:
		return MergeStrategy(s), nil
	}
	return "", fmt.Errorf("unknown merge strategy %q (want ff, merge, or squash)", s)
}

// MergeOptions configures Merge.
type MergeOptions struct {
	Strategy MergeStrategy
	// Message is the commit message for the merge or squash commit. Ignored
	// for fast-forward. When empty a message is derived from the branch.
	Message string
	// IgnorePaths are worktree-relative paths (files or directories) excluded
	// from the branch worktree's clean check, e.g. the live cases directory.
	IgnorePaths []string
}

// MergeResult describes a completed (or no-op) merge.
type MergeResult struct {
	Worktree     string        `json:"worktree"`
	Branch       string        `json:"branch"`
	Base         string        `json:"base"`
	BaseWorktree string        `json:"base_worktree"`
	Strategy     MergeStrategy `json:"strategy"`
	// Merged is false when the branch had no commits beyond the base; nothing
	// was changed and no hooks ran.
	Merged  bool `json:"merged"`
	Commits int  `json:"commits"`
	// Head is the default branch's commit after the merge.
	Head           string       `json:"head,omitempty"`
	PreMergeHooks  []HookResult `json:"pre_merge_hooks,omitempty"`
	PostMergeHooks []HookResult `json:"post_merge_hooks,omitempty"`
	// PostMergeError is set when a post_merge hook failed. The merge itself
	// has already happened and is not undone.
	PostMergeError string `json:"post_merge_error,omitempty"`
}

// MergeError is returned when a merge is refused or fails. Hooks holds any
// hook results gathered before the failure, and Conflicts the conflicting
// files when a rebase or merge stopped on conflicts (after it was aborted).
type MergeError struct {
	Msg       string
	Hooks     []HookResult
	Conflicts []string
}

func (e *MergeError) Error() string { return e.Msg }

// Merge lands the named worktree's branch on the repository's default branch,
// which must be checked out in some worktree (normally the main repository).
// Both worktrees must be clean. The branch worktree is left in place; see
// RemoveWithOptions for cleanup.
func (m *WorktreeManager) Merge(ctx context.Context, name string, opts MergeOptions) (*MergeResult, error) {
	strategy, err := ParseMergeStrategy(string(opts.Strategy))
	if err != nil {
		return nil, err
	}

	// One merge at a time: two merges into the same base would race between
	// the rebase and the fast-forward.
	m.mergeMu.Lock()
	defer m.mergeMu.Unlock()

	if err := m.Refresh(); err != nil {
		return nil, fmt.Errorf("refresh worktrees: %w", err)
	}
	wt, ok := m.GetByName(name)
	if !ok {
		return nil, fmt.Errorf("worktree %q not found", name)
	}
	base := m.DefaultBranch()
	switch {
	case wt.Path == m.repoDir:
		return nil, fmt.Errorf("cannot merge the main repository into itself")
	case wt.Detached || wt.Branch == "":
		return nil, fmt.Errorf("worktree %q is in detached HEAD state", wt.Name())
	case wt.Branch == base:
		return nil, fmt.Errorf("worktree %q is on the default branch %q", wt.Name(), base)
	}

	baseWt, ok := m.worktreeOnBranch(base)
	if !ok {
		return nil, fmt.Errorf("default branch %q is not checked out in any worktree", base)
	}

	if dirty := dirtyPaths(ctx, wt.Path, true, opts.IgnorePaths); len(dirty) > 0 {
		return nil, fmt.Errorf("worktree %q has uncommitted changes: %s", wt.Name(), summarizePaths(dirty))
	}
	if dirty := dirtyPaths(ctx, baseWt.Path, false, nil); len(dirty) > 0 {
		return nil, fmt.Errorf("default branch worktree %q has uncommitted changes: %s", baseWt.Name(), summarizePaths(dirty))
	}

	result := &MergeResult{
		Worktree:     wt.Name(),
		Branch:       wt.Branch,
		Base:         base,
		BaseWorktree: baseWt.Name(),
		Strategy:     strategy,
	}

	count, err := revCount(ctx, wt.Path, base+".."+wt.Branch)
	if err != nil {
		return nil, err
	}
	result.Commits = count
	if count == 0 {
		return result, nil
	}

	env := []string{
		"TRELLIS_MERGE_BRANCH=" + wt.Branch,
		"TRELLIS_MERGE_BASE=" + base,
		"TRELLIS_MERGE_STRATEGY=" + string(strategy),
		"TRELLIS_MERGE_WORKTREE_PATH=" + wt.Path,
	}

	if strategy == MergeFastForward {
		if out, err := gitIn(ctx, wt.Path, "rebase", base); err != nil {
			conflicts := conflictedFiles(ctx, wt.Path)
			gitIn(ctx, wt.Path, "rebase", "--abort")
			return nil, &MergeError{
				Msg:       fmt.Sprintf("rebasing %s onto %s failed; rebase aborted, worktree unchanged. Rebase and resolve conflicts in the worktree, then finish again: %s", wt.Branch, base, gitMessage(out)),
				Conflicts: conflicts,
			}
		}
	}

	if len(m.cfg.Lifecycle.PreMerge) > 0 && m.lifecycle != nil {
		hooks, err := m.lifecycle.RunPreMerge(ctx, &wt, m.cfg.Lifecycle.PreMerge, env)
		result.PreMergeHooks = hooks
		if err != nil {
			return nil, &MergeError{Msg: "pre_merge " + err.Error() + "; nothing merged", Hooks: hooks}
		}
	}

	switch strategy {
	case MergeFastForward:
		if out, err := gitIn(ctx, baseWt.Path, "merge", "--ff-only", wt.Branch); err != nil {
			return nil, &MergeError{Msg: "fast-forward of " + base + " failed: " + gitMessage(out), Hooks: result.PreMergeHooks}
		}
	case MergeCommit:
		msg := opts.Message
		if msg == "" {
			msg = fmt.Sprintf("Merge branch '%s'", wt.Branch)
		}
		if out, err := gitIn(ctx, baseWt.Path, "merge", "--no-ff", "-m", msg, wt.Branch); err != nil {
			conflicts := conflictedFiles(ctx, baseWt.Path)
			gitIn(ctx, baseWt.Path, "merge", "--abort")
			return nil, &MergeError{Msg: "merge failed; merge aborted: " + gitMessage(out), Hooks: result.PreMergeHooks, Conflicts: conflicts}
		}
	case MergeSquash:
		msg := opts.Message
		if msg == "" {
			msg = squashMessage(ctx, wt.Path, base, wt.Branch, count)
		}
		if out, err := gitIn(ctx, baseWt.Path, "merge", "--squash", wt.Branch); err != nil {
			conflicts := conflictedFiles(ctx, baseWt.Path)
			gitIn(ctx, baseWt.Path, "reset", "--merge")
			return nil, &MergeError{Msg: "squash merge failed; changes reset: " + gitMessage(out), Hooks: result.PreMergeHooks, Conflicts: conflicts}
		}
		if out, err := gitIn(ctx, baseWt.Path, "commit", "-m", msg); err != nil {
			gitIn(ctx, baseWt.Path, "reset", "--merge")
			return nil, &MergeError{Msg: "squash commit failed; changes reset: " + gitMessage(out), Hooks: result.PreMergeHooks}
		}
	}
	result.Merged = true
	if out, err := gitIn(ctx, baseWt.Path, "rev-parse", "HEAD"); err == nil {
		result.Head = strings.TrimSpace(out)
	}

	if m.bus != nil {
		m.bus.Publish(ctx, events.Event{
			Type:     events.EventWorktreeMerged,
			Worktree: wt.Name(),
			Payload: map[string]interface{}{
				"name":     wt.Name(),
				"branch":   wt.Branch,
				"base":     base,
				"strategy": string(strategy),
				"commits":  count,
				"head":     result.Head,
			},
		})
	}

	if len(m.cfg.Lifecycle.PostMerge) > 0 && m.lifecycle != nil {
		hooks, err := m.lifecycle.RunPostMerge(ctx, &baseWt, m.cfg.Lifecycle.PostMerge, env)
		result.PostMergeHooks = hooks
		if err != nil {
			result.PostMergeError = "post_merge " + err.Error()
		}
	}

	m.Refresh()
	return result, nil
}

// worktreeOnBranch returns the worktree that has branch checked out.
func (m *WorktreeManager) worktreeOnBranch(branch string) (WorktreeInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, wt := range m.worktrees {
		if wt.Branch == branch && !wt.IsBare {
			return wt, true
		}
	}
	return WorktreeInfo{}, false
}

// RemoveOptions configures RemoveWithOptions.
type RemoveOptions struct {
	DeleteBranch bool
	// SafeBranchDelete deletes the branch with `git branch -d`, which git
	// refuses for a branch not merged into HEAD. The refusal is reported in
	// RemoveResult.BranchError and the branch is kept.
	SafeBranchDelete bool
}

// RemoveResult describes a completed removal.
type RemoveResult struct {
	PreRemoveHooks []HookResult `json:"pre_remove_hooks,omitempty"`
	BranchDeleted  bool         `json:"branch_deleted"`
	BranchError    string       `json:"branch_error,omitempty"`
}

// RemoveError is returned when a pre_remove hook blocks a removal.
type RemoveError struct {
	Msg   string
	Hooks []HookResult
}

func (e *RemoveError) Error() string { return e.Msg }

// dirtyPaths lists changed paths in a worktree from `git status --porcelain`.
// Untracked files count only when includeUntracked is set. Paths equal to or
// under an ignore entry are skipped.
func dirtyPaths(ctx context.Context, dir string, includeUntracked bool, ignore []string) []string {
	untracked := "--untracked-files=no"
	if includeUntracked {
		untracked = "--untracked-files=all"
	}
	out, err := gitIn(ctx, dir, "status", "--porcelain", untracked)
	if err != nil {
		return []string{"(git status failed: " + gitMessage(out) + ")"}
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		p := line[3:]
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		p = strings.Trim(p, `"`)
		if !underAny(p, ignore) {
			paths = append(paths, p)
		}
	}
	return paths
}

func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		d = strings.TrimSuffix(d, "/")
		if d != "" && (p == d || strings.HasPrefix(p, d+"/")) {
			return true
		}
	}
	return false
}

func summarizePaths(paths []string) string {
	const max = 5
	if len(paths) <= max {
		return strings.Join(paths, ", ")
	}
	return strings.Join(paths[:max], ", ") + fmt.Sprintf(" and %d more", len(paths)-max)
}

func revCount(ctx context.Context, dir, rng string) (int, error) {
	out, err := gitIn(ctx, dir, "rev-list", "--count", rng)
	if err != nil {
		return 0, fmt.Errorf("git rev-list %s: %s", rng, gitMessage(out))
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

func conflictedFiles(ctx context.Context, dir string) []string {
	out, err := gitIn(ctx, dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// squashMessage reuses the sole commit's message, or summarizes several.
func squashMessage(ctx context.Context, dir, base, branch string, count int) string {
	if count == 1 {
		if out, err := gitIn(ctx, dir, "log", "-1", "--format=%B", branch); err == nil && strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out)
		}
	}
	msg := fmt.Sprintf("Squash merge branch '%s'", branch)
	if out, err := gitIn(ctx, dir, "log", "--reverse", "--format=* %s", base+".."+branch); err == nil && strings.TrimSpace(out) != "" {
		msg += "\n\n" + strings.TrimSpace(out)
	}
	return msg
}

// gitIn runs git in dir and returns combined output.
func gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gitMessage picks the most informative line of git output: the first
// error, fatal, or conflict line, else the first line.
func gitMessage(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "CONFLICT") {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(lines[0])
}
