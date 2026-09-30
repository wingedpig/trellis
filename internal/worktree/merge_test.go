// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/config"
)

// mergeFixture is a real repo ("proj", on main) with one feature worktree.
type mergeFixture struct {
	t       *testing.T
	repoDir string
	featDir string
	mgr     *WorktreeManager
}

func newMergeFixture(t *testing.T, lc config.LifecycleConfig) *mergeFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	f := &mergeFixture{t: t, repoDir: filepath.Join(tmp, "proj"), featDir: filepath.Join(tmp, "proj-feature")}
	require.NoError(t, os.Mkdir(f.repoDir, 0755))
	f.git(f.repoDir, "init", "-q", "-b", "main")
	f.git(f.repoDir, "config", "user.email", "test@example.com")
	f.git(f.repoDir, "config", "user.name", "Test")
	f.git(f.repoDir, "config", "commit.gpgsign", "false")
	f.commit(f.repoDir, "README", "hi\n", "init")

	bus := newTestBus()
	t.Cleanup(func() { bus.Close() })
	f.mgr = NewManager(NewRealGitExecutor(), bus, config.WorktreeConfig{Lifecycle: lc}, f.repoDir, tmp, "proj")
	require.NoError(t, f.mgr.SetActive("main"))
	require.NoError(t, f.mgr.Create(context.Background(), "feature", false))
	return f
}

func (f *mergeFixture) git(dir string, args ...string) string {
	f.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(f.t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func (f *mergeFixture) commit(dir, file, content, msg string) {
	f.t.Helper()
	require.NoError(f.t, os.WriteFile(filepath.Join(dir, file), []byte(content), 0644))
	f.git(dir, "add", file)
	f.git(dir, "commit", "-q", "-m", msg)
}

func TestMerge_FastForwardRebasesOntoMovedBase(t *testing.T) {
	f := newMergeFixture(t, config.LifecycleConfig{})
	f.commit(f.featDir, "a.txt", "a\n", "add a")
	f.commit(f.repoDir, "b.txt", "b\n", "add b on main")

	res, err := f.mgr.Merge(context.Background(), "feature", MergeOptions{})
	require.NoError(t, err)
	assert.True(t, res.Merged)
	assert.Equal(t, 1, res.Commits)
	assert.Equal(t, "main", res.Base)
	assert.Equal(t, "proj", res.BaseWorktree)

	// Linear history: main's head is the rebased feature commit.
	assert.Equal(t, f.git(f.featDir, "rev-parse", "HEAD"), f.git(f.repoDir, "rev-parse", "HEAD"))
	assert.Equal(t, res.Head, f.git(f.repoDir, "rev-parse", "HEAD"))
	assert.Equal(t, "0", f.git(f.repoDir, "rev-list", "--count", "--merges", "HEAD"))
}

func TestMerge_MergeCommitAndSquash(t *testing.T) {
	t.Run("merge", func(t *testing.T) {
		f := newMergeFixture(t, config.LifecycleConfig{})
		f.commit(f.featDir, "a.txt", "a\n", "add a")
		f.commit(f.repoDir, "b.txt", "b\n", "add b on main")
		_, err := f.mgr.Merge(context.Background(), "feature", MergeOptions{Strategy: MergeCommit})
		require.NoError(t, err)
		assert.Equal(t, "Merge branch 'feature'", f.git(f.repoDir, "log", "-1", "--format=%s"))
		assert.Equal(t, "1", f.git(f.repoDir, "rev-list", "--count", "--merges", "HEAD"))
	})
	t.Run("squash", func(t *testing.T) {
		f := newMergeFixture(t, config.LifecycleConfig{})
		f.commit(f.featDir, "a.txt", "a\n", "add a")
		f.commit(f.featDir, "c.txt", "c\n", "add c")
		res, err := f.mgr.Merge(context.Background(), "feature", MergeOptions{Strategy: MergeSquash})
		require.NoError(t, err)
		assert.Equal(t, 2, res.Commits)
		msg := f.git(f.repoDir, "log", "-1", "--format=%B")
		assert.Contains(t, msg, "Squash merge branch 'feature'")
		assert.Contains(t, msg, "* add a\n* add c")
		assert.Equal(t, "2", f.git(f.repoDir, "rev-list", "--count", "HEAD")) // init + squash
		assert.FileExists(t, filepath.Join(f.repoDir, "c.txt"))
	})
}

func TestMerge_NothingToMerge(t *testing.T) {
	f := newMergeFixture(t, config.LifecycleConfig{
		PreMerge: []config.HookConfig{{Name: "never", Command: []string{"false"}}},
	})
	res, err := f.mgr.Merge(context.Background(), "feature", MergeOptions{})
	require.NoError(t, err)
	assert.False(t, res.Merged)
	assert.Equal(t, 0, res.Commits)
	assert.Empty(t, res.PreMergeHooks, "hooks must not run when there is nothing to merge")
}

func TestMerge_RefusesDirtyWorktrees(t *testing.T) {
	f := newMergeFixture(t, config.LifecycleConfig{})
	f.commit(f.featDir, "a.txt", "a\n", "add a")

	// Untracked file in the branch worktree would be lost on removal.
	require.NoError(t, os.WriteFile(filepath.Join(f.featDir, "scratch.txt"), []byte("x"), 0644))
	_, err := f.mgr.Merge(context.Background(), "feature", MergeOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scratch.txt")

	// ...unless it is under an ignored path.
	require.NoError(t, os.MkdirAll(filepath.Join(f.featDir, "trellis", "cases", "c1"), 0755))
	require.NoError(t, os.Rename(filepath.Join(f.featDir, "scratch.txt"), filepath.Join(f.featDir, "trellis", "cases", "c1", "case.json")))
	_, err = f.mgr.Merge(context.Background(), "feature", MergeOptions{IgnorePaths: []string{"trellis/cases"}, Strategy: MergeCommit})
	require.NoError(t, err)

	// Tracked modifications in the base worktree block a merge; untracked don't.
	f2 := newMergeFixture(t, config.LifecycleConfig{})
	f2.commit(f2.featDir, "a.txt", "a\n", "add a")
	require.NoError(t, os.WriteFile(filepath.Join(f2.repoDir, "untracked.txt"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(f2.repoDir, "README"), []byte("edited\n"), 0644))
	_, err = f2.mgr.Merge(context.Background(), "feature", MergeOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "README")
	assert.NotContains(t, err.Error(), "untracked.txt")
}

func TestMerge_RebaseConflictAborts(t *testing.T) {
	f := newMergeFixture(t, config.LifecycleConfig{})
	f.commit(f.featDir, "README", "feature\n", "edit on feature")
	f.commit(f.repoDir, "README", "main\n", "edit on main")
	before := f.git(f.featDir, "rev-parse", "HEAD")
	mainBefore := f.git(f.repoDir, "rev-parse", "HEAD")

	_, err := f.mgr.Merge(context.Background(), "feature", MergeOptions{})
	var me *MergeError
	require.True(t, errors.As(err, &me), "want MergeError, got %v", err)
	assert.Equal(t, []string{"README"}, me.Conflicts)
	assert.Equal(t, before, f.git(f.featDir, "rev-parse", "HEAD"), "rebase must be aborted")
	assert.Equal(t, mainBefore, f.git(f.repoDir, "rev-parse", "HEAD"))
	assert.Empty(t, f.git(f.featDir, "status", "--porcelain"))
}

func TestMerge_HookFailuresAndEnv(t *testing.T) {
	t.Run("pre_merge failure aborts", func(t *testing.T) {
		f := newMergeFixture(t, config.LifecycleConfig{
			PreMerge: []config.HookConfig{{Name: "tests", Command: []string{"sh", "-c", "echo failing; exit 1"}}},
		})
		f.commit(f.featDir, "a.txt", "a\n", "add a")
		mainBefore := f.git(f.repoDir, "rev-parse", "HEAD")
		_, err := f.mgr.Merge(context.Background(), "feature", MergeOptions{})
		var me *MergeError
		require.True(t, errors.As(err, &me))
		require.Len(t, me.Hooks, 1)
		assert.Contains(t, me.Hooks[0].Output, "failing")
		assert.Equal(t, mainBefore, f.git(f.repoDir, "rev-parse", "HEAD"))
	})
	t.Run("post_merge failure is reported, merge stands", func(t *testing.T) {
		f := newMergeFixture(t, config.LifecycleConfig{
			PreMerge:  []config.HookConfig{{Name: "env", Command: []string{"sh", "-c", `echo "$TRELLIS_HOOK_PHASE $TRELLIS_MERGE_BRANCH $TRELLIS_MERGE_BASE $(pwd)"`}}},
			PostMerge: []config.HookConfig{{Name: "deploy", Command: []string{"sh", "-c", `echo "$TRELLIS_HOOK_PHASE $(pwd)"; exit 3`}}},
		})
		f.commit(f.featDir, "a.txt", "a\n", "add a")
		res, err := f.mgr.Merge(context.Background(), "feature", MergeOptions{})
		require.NoError(t, err)
		assert.True(t, res.Merged)
		require.Len(t, res.PreMergeHooks, 1)
		featReal, _ := filepath.EvalSymlinks(f.featDir)
		repoReal, _ := filepath.EvalSymlinks(f.repoDir)
		assert.Equal(t, "pre_merge feature main "+featReal, strings.TrimSpace(res.PreMergeHooks[0].Output))
		require.Len(t, res.PostMergeHooks, 1)
		assert.Equal(t, "post_merge "+repoReal, strings.TrimSpace(res.PostMergeHooks[0].Output))
		assert.Contains(t, res.PostMergeError, "deploy")
	})
}

func TestMerge_RefusesMainAndUnknown(t *testing.T) {
	f := newMergeFixture(t, config.LifecycleConfig{})
	_, err := f.mgr.Merge(context.Background(), "main", MergeOptions{})
	assert.Error(t, err)
	_, err = f.mgr.Merge(context.Background(), "nope", MergeOptions{})
	assert.Error(t, err)
	_, err = f.mgr.Merge(context.Background(), "feature", MergeOptions{Strategy: "rebase"})
	assert.Error(t, err)
}

func TestRemoveWithOptions_SafeDeleteAndPreRemove(t *testing.T) {
	t.Run("safe delete keeps an unmerged branch", func(t *testing.T) {
		f := newMergeFixture(t, config.LifecycleConfig{})
		f.commit(f.featDir, "a.txt", "a\n", "add a")
		res, err := f.mgr.RemoveWithOptions(context.Background(), "feature", RemoveOptions{DeleteBranch: true, SafeBranchDelete: true})
		require.NoError(t, err)
		assert.False(t, res.BranchDeleted)
		assert.Contains(t, res.BranchError, "not fully merged")
		assert.NoDirExists(t, f.featDir)
		f.git(f.repoDir, "rev-parse", "--verify", "feature") // still there
	})
	t.Run("safe delete removes a merged branch", func(t *testing.T) {
		f := newMergeFixture(t, config.LifecycleConfig{})
		f.commit(f.featDir, "a.txt", "a\n", "add a")
		_, err := f.mgr.Merge(context.Background(), "feature", MergeOptions{})
		require.NoError(t, err)
		res, err := f.mgr.RemoveWithOptions(context.Background(), "feature", RemoveOptions{DeleteBranch: true, SafeBranchDelete: true})
		require.NoError(t, err)
		assert.True(t, res.BranchDeleted)
	})
	t.Run("pre_remove failure blocks removal", func(t *testing.T) {
		f := newMergeFixture(t, config.LifecycleConfig{
			PreRemove: []config.HookConfig{{Name: "guard", Command: []string{"sh", "-c", `test "$TRELLIS_HOOK_PHASE" = pre_remove && exit 1`}}},
		})
		_, err := f.mgr.RemoveWithOptions(context.Background(), "feature", RemoveOptions{DeleteBranch: true})
		var re *RemoveError
		require.True(t, errors.As(err, &re))
		assert.Len(t, re.Hooks, 1)
		assert.DirExists(t, f.featDir)
	})
}
