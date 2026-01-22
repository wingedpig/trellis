// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package worktree

import (
	"context"
	"path/filepath"
)

// WorktreeInfo contains information about a git worktree.
type WorktreeInfo struct {
	Path     string
	Commit   string // Current commit SHA (head)
	Branch   string
	Detached bool
	IsBare   bool
	Dirty    bool // Whether working tree has uncommitted changes
	Ahead    int  // Commits ahead of default branch (main/master)
	Behind   int  // Commits behind default branch (main/master)
}

// Name returns the directory name of the worktree.
func (w *WorktreeInfo) Name() string {
	return filepath.Base(w.Path)
}

// GitStatus represents the status of a git working directory.
type GitStatus struct {
	Clean     bool
	Modified  []string
	Added     []string
	Deleted   []string
	Renamed   []string
	Untracked []string
}

// HasChanges returns true if there are any changes in the working directory.
func (s *GitStatus) HasChanges() bool {
	if s.Clean {
		return false
	}
	return len(s.Modified) > 0 || len(s.Added) > 0 ||
		len(s.Deleted) > 0 || len(s.Renamed) > 0 ||
		len(s.Untracked) > 0
}

// BranchInfo contains information about the current branch.
type BranchInfo struct {
	Name     string
	Detached bool
	Commit   string
}

// ActivateResult contains the results of a worktree activation.
type ActivateResult struct {
	Worktree    WorktreeInfo
	HookResults []HookResult
	Duration    string
}

// GitExecutor is the interface for git operations.
type GitExecutor interface {
	WorktreeList(ctx context.Context, dir string) ([]WorktreeInfo, error)
	Status(ctx context.Context, path string) (GitStatus, error)
	BranchInfo(ctx context.Context, path string) (BranchInfo, error)
}

// Manager is the interface for worktree management.
type Manager interface {
	List() ([]WorktreeInfo, error)
	Active() *WorktreeInfo
	SetActive(name string) error                                     // Set active without hooks (for startup)
	Activate(ctx context.Context, name string) (*ActivateResult, error) // Set active with lifecycle hooks
	Create(ctx context.Context, branchName string, switchTo bool) error
	Remove(ctx context.Context, name string, deleteBranch bool) error
	Refresh() error
	GetByName(name string) (WorktreeInfo, bool)
	GetByPath(path string) (WorktreeInfo, bool)
	Count() int
	Status() (GitStatus, error)
	BinariesPath() string
	ProjectName() string
}
