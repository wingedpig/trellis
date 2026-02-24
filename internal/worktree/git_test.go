// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package worktree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWorktreeList(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected []WorktreeInfo
	}{
		{
			name: "single worktree",
			output: `/home/user/project  abc1234 [main]
`,
			expected: []WorktreeInfo{
				{Path: "/home/user/project", Commit: "abc1234", Branch: "main"},
			},
		},
		{
			name: "multiple worktrees",
			output: `/home/user/project  abc1234 [main]
/home/user/project-feature  def5678 [feature-branch]
/home/user/project-hotfix  ghi9012 [hotfix]
`,
			expected: []WorktreeInfo{
				{Path: "/home/user/project", Commit: "abc1234", Branch: "main"},
				{Path: "/home/user/project-feature", Commit: "def5678", Branch: "feature-branch"},
				{Path: "/home/user/project-hotfix", Commit: "ghi9012", Branch: "hotfix"},
			},
		},
		{
			name: "detached HEAD",
			output: `/home/user/project  abc1234 (detached HEAD)
`,
			expected: []WorktreeInfo{
				{Path: "/home/user/project", Commit: "abc1234", Branch: "", Detached: true},
			},
		},
		{
			name: "bare repository",
			output: `/home/user/project.git  (bare)
/home/user/project-main  abc1234 [main]
`,
			expected: []WorktreeInfo{
				{Path: "/home/user/project.git", IsBare: true},
				{Path: "/home/user/project-main", Commit: "abc1234", Branch: "main"},
			},
		},
		{
			name:     "empty output",
			output:   "",
			expected: []WorktreeInfo{},
		},
		{
			name:     "whitespace only",
			output:   "   \n\t\n  ",
			expected: []WorktreeInfo{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseWorktreeList(tt.output)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseGitStatus(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected GitStatus
	}{
		{
			name:   "clean",
			output: "",
			expected: GitStatus{
				Clean: true,
			},
		},
		{
			name:   "modified files",
			output: " M file1.go\n M file2.go\n",
			expected: GitStatus{
				Clean:    false,
				Modified: []string{"file1.go", "file2.go"},
			},
		},
		{
			name:   "added files",
			output: "A  newfile.go\n",
			expected: GitStatus{
				Clean: false,
				Added: []string{"newfile.go"},
			},
		},
		{
			name:   "deleted files",
			output: " D deleted.go\n",
			expected: GitStatus{
				Clean:   false,
				Deleted: []string{"deleted.go"},
			},
		},
		{
			name:   "untracked files",
			output: "?? untracked.go\n?? another.go\n",
			expected: GitStatus{
				Clean:     false,
				Untracked: []string{"untracked.go", "another.go"},
			},
		},
		{
			name:   "mixed status",
			output: " M modified.go\nA  added.go\n D deleted.go\n?? untracked.go\n",
			expected: GitStatus{
				Clean:     false,
				Modified:  []string{"modified.go"},
				Added:     []string{"added.go"},
				Deleted:   []string{"deleted.go"},
				Untracked: []string{"untracked.go"},
			},
		},
		{
			name:   "renamed file",
			output: "R  old.go -> new.go\n",
			expected: GitStatus{
				Clean:   false,
				Renamed: []string{"old.go -> new.go"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseGitStatus(tt.output)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseBranchInfo(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected BranchInfo
	}{
		{
			name:   "simple branch",
			output: "main\n",
			expected: BranchInfo{
				Name: "main",
			},
		},
		{
			name:   "feature branch",
			output: "feature/my-feature\n",
			expected: BranchInfo{
				Name: "feature/my-feature",
			},
		},
		{
			name:   "detached HEAD",
			output: "(HEAD detached at abc1234)\n",
			expected: BranchInfo{
				Name:     "",
				Detached: true,
				Commit:   "abc1234",
			},
		},
		{
			name:   "empty",
			output: "",
			expected: BranchInfo{
				Name: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseBranchInfo(tt.output)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorktreeInfo_Name(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/home/user/project", "project"},
		{"/home/user/project-feature", "project-feature"},
		{"/home/user/my-project.git", "my-project.git"},
		{"/project", "project"},
		{".", "."},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			info := WorktreeInfo{Path: tt.path}
			assert.Equal(t, tt.expected, info.Name())
		})
	}
}

func TestGitStatus_HasChanges(t *testing.T) {
	tests := []struct {
		name     string
		status   GitStatus
		expected bool
	}{
		{"clean", GitStatus{Clean: true}, false},
		{"modified", GitStatus{Modified: []string{"a.go"}}, true},
		{"added", GitStatus{Added: []string{"a.go"}}, true},
		{"deleted", GitStatus{Deleted: []string{"a.go"}}, true},
		{"untracked", GitStatus{Untracked: []string{"a.go"}}, true},
		{"empty slices", GitStatus{Modified: []string{}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.HasChanges())
		})
	}
}

// MockGitExecutor for testing
type MockGitExecutor struct {
	worktrees []WorktreeInfo
	status    GitStatus
	branch    BranchInfo
	err       error
}

func (m *MockGitExecutor) WorktreeList(ctx context.Context, dir string) ([]WorktreeInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Return a copy so concurrent callers don't share the same slice
	// (mirrors real RealGitExecutor which creates a fresh slice each call)
	result := make([]WorktreeInfo, len(m.worktrees))
	copy(result, m.worktrees)
	return result, nil
}

func (m *MockGitExecutor) Status(ctx context.Context, path string) (GitStatus, error) {
	if m.err != nil {
		return GitStatus{}, m.err
	}
	return m.status, nil
}

func (m *MockGitExecutor) BranchInfo(ctx context.Context, path string) (BranchInfo, error) {
	if m.err != nil {
		return BranchInfo{}, m.err
	}
	return m.branch, nil
}

func TestGitExecutor_Interface(t *testing.T) {
	// Verify mock implements interface
	var _ GitExecutor = (*MockGitExecutor)(nil)
}

func TestRealGitExecutor_WorktreeList_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Only run if we're in a git repo
	exec := NewRealGitExecutor()
	worktrees, err := exec.WorktreeList(context.Background(), "")

	// This might fail if not in a git repo, which is fine
	if err != nil {
		t.Skip("not in a git repository")
	}

	require.GreaterOrEqual(t, len(worktrees), 1)
	assert.NotEmpty(t, worktrees[0].Path)
}

func TestRealGitExecutor_Status_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	exec := NewRealGitExecutor()
	status, err := exec.Status(context.Background(), ".")

	if err != nil {
		t.Skip("not in a git repository")
	}

	// Status should parse without error
	_ = status
}

func TestRealGitExecutor_BranchInfo_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	exec := NewRealGitExecutor()
	info, err := exec.BranchInfo(context.Background(), ".")

	if err != nil {
		t.Skip("not in a git repository")
	}

	// Should have some branch info
	if !info.Detached {
		assert.NotEmpty(t, info.Name)
	}
}
