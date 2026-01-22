// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// RealGitExecutor executes real git commands.
type RealGitExecutor struct{}

// NewRealGitExecutor creates a new git executor.
func NewRealGitExecutor() *RealGitExecutor {
	return &RealGitExecutor{}
}

// WorktreeList returns the list of git worktrees.
// If dir is empty, uses current directory.
// Uses --porcelain format for reliable parsing of paths with spaces.
func (e *RealGitExecutor) WorktreeList(ctx context.Context, dir string) ([]WorktreeInfo, error) {
	var cmd *exec.Cmd
	if dir != "" {
		cmd = exec.CommandContext(ctx, "git", "-C", dir, "worktree", "list", "--porcelain")
	} else {
		cmd = exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	}
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return ParseWorktreeListPorcelain(string(output)), nil
}

// Status returns the git status for a path.
func (e *RealGitExecutor) Status(ctx context.Context, path string) (GitStatus, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return GitStatus{}, err
	}
	return ParseGitStatus(string(output)), nil
}

// BranchInfo returns the current branch info for a path.
func (e *RealGitExecutor) BranchInfo(ctx context.Context, path string) (BranchInfo, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "branch", "--show-current")
	output, err := cmd.Output()
	if err != nil {
		// Try symbolic-ref for detached HEAD
		cmd2 := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--short", "HEAD")
		commitOutput, err2 := cmd2.Output()
		if err2 == nil {
			return BranchInfo{
				Detached: true,
				Commit:   strings.TrimSpace(string(commitOutput)),
			}, nil
		}
		return BranchInfo{}, err
	}
	return ParseBranchInfo(string(output)), nil
}

// ParseWorktreeListPorcelain parses the output of `git worktree list --porcelain`.
// This format handles paths with spaces correctly.
// Format:
//
//	worktree /path/to/worktree
//	HEAD abc1234...
//	branch refs/heads/main
//
//	worktree /path/to/bare
//	bare
func ParseWorktreeListPorcelain(output string) []WorktreeInfo {
	result := []WorktreeInfo{}

	// Split by blank lines to get each worktree block
	blocks := strings.Split(output, "\n\n")
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		info := parseWorktreeBlock(block)
		if info.Path != "" {
			result = append(result, info)
		}
	}

	return result
}

func parseWorktreeBlock(block string) WorktreeInfo {
	var info WorktreeInfo

	lines := strings.Split(block, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "worktree "):
			info.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			info.Commit = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			// Format: branch refs/heads/main -> extract "main"
			ref := strings.TrimPrefix(line, "branch ")
			info.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "bare":
			info.IsBare = true
		case line == "detached":
			info.Detached = true
		}
	}

	return info
}

// ParseWorktreeList parses the output of `git worktree list` (non-porcelain).
// Deprecated: Use ParseWorktreeListPorcelain instead for reliable path parsing.
func ParseWorktreeList(output string) []WorktreeInfo {
	result := []WorktreeInfo{}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		info := parseWorktreeLine(line)
		if info.Path != "" {
			result = append(result, info)
		}
	}

	return result
}

func parseWorktreeLine(line string) WorktreeInfo {
	var info WorktreeInfo

	// Check for bare repository
	if strings.HasSuffix(line, "(bare)") {
		parts := strings.Fields(line)
		if len(parts) >= 1 {
			info.Path = parts[0]
			info.IsBare = true
		}
		return info
	}

	// Check for detached HEAD
	if strings.Contains(line, "(detached HEAD)") {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			info.Path = parts[0]
			info.Commit = parts[1]
			info.Detached = true
		}
		return info
	}

	// Regular worktree: /path/to/worktree abc1234 [branch]
	// Use regex to parse: path  commit [branch]
	re := regexp.MustCompile(`^(.+?)\s+([a-f0-9]+)\s+\[(.+)\]\s*$`)
	matches := re.FindStringSubmatch(line)
	if len(matches) == 4 {
		info.Path = strings.TrimSpace(matches[1])
		info.Commit = matches[2]
		info.Branch = matches[3]
		return info
	}

	// Fallback: try to parse using fields
	parts := strings.Fields(line)
	if len(parts) >= 3 {
		info.Path = parts[0]
		info.Commit = parts[1]
		// Branch is in format [branch]
		if strings.HasPrefix(parts[2], "[") && strings.HasSuffix(parts[2], "]") {
			info.Branch = parts[2][1 : len(parts[2])-1]
		}
	}

	return info
}

// ParseGitStatus parses the output of `git status --porcelain`.
func ParseGitStatus(output string) GitStatus {
	var status GitStatus

	// Only trim trailing whitespace, not leading (the status indicators include leading spaces)
	output = strings.TrimRight(output, " \t\n\r")
	if output == "" {
		status.Clean = true
		return status
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}

		// Git status porcelain format: XY PATH
		// X = index status, Y = worktree status
		// Position 2 is a space, position 3+ is the path
		indicator := line[:2]
		filename := line[3:]

		// Check specific statuses first (A, R) before general contains checks (M, D)
		// to properly classify combined statuses like AM (added+modified) or RM (renamed+modified)
		switch {
		case strings.HasPrefix(indicator, "A"):
			status.Added = append(status.Added, filename)
		case strings.HasPrefix(indicator, "R"):
			status.Renamed = append(status.Renamed, filename)
		case indicator == "??":
			status.Untracked = append(status.Untracked, filename)
		case strings.Contains(indicator, "D"):
			status.Deleted = append(status.Deleted, filename)
		case strings.Contains(indicator, "M"):
			status.Modified = append(status.Modified, filename)
		}
	}

	status.Clean = !status.HasChanges()
	return status
}

// ParseBranchInfo parses the output of `git branch --show-current`.
func ParseBranchInfo(output string) BranchInfo {
	output = strings.TrimSpace(output)

	// Check for detached HEAD format
	if strings.HasPrefix(output, "(HEAD detached at ") {
		commit := strings.TrimPrefix(output, "(HEAD detached at ")
		commit = strings.TrimSuffix(commit, ")")
		return BranchInfo{
			Detached: true,
			Commit:   commit,
		}
	}

	return BranchInfo{
		Name: output,
	}
}

// RunCommand runs a git command and returns the output.
func RunCommand(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stderr.String(), err
	}
	return stdout.String(), nil
}

// GetAheadBehind returns the number of commits ahead and behind the default branch.
// Ahead = commits in this branch not in default branch
// Behind = commits in default branch not in this branch
// Returns (0, 0) if there's any error.
func GetAheadBehind(ctx context.Context, worktreePath, defaultBranch string) (ahead, behind int) {
	// git rev-list --left-right --count defaultBranch...HEAD
	// Output format: "behind\tahead" (left is default branch, right is HEAD)
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-list", "--left-right", "--count", defaultBranch+"...HEAD")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0
	}

	// Parse "behind\tahead" format
	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) != 2 {
		return 0, 0
	}

	fmt.Sscanf(parts[0], "%d", &behind)
	fmt.Sscanf(parts[1], "%d", &ahead)
	return ahead, behind
}

// IsDirty returns true if the worktree has uncommitted changes.
func IsDirty(ctx context.Context, worktreePath string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(output))) > 0
}

// GetDefaultBranch returns the default branch name (main or master).
func GetDefaultBranch(ctx context.Context, repoDir string) string {
	// Try to get the default branch from remote
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "symbolic-ref", "refs/remotes/origin/HEAD")
	output, err := cmd.Output()
	if err == nil {
		// Format: refs/remotes/origin/main or refs/remotes/origin/master
		ref := strings.TrimSpace(string(output))
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			candidate := parts[len(parts)-1]
			// Verify this branch actually exists locally
			verify := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--verify", candidate)
			if verify.Run() == nil {
				return candidate
			}
			// origin/HEAD points to a branch that doesn't exist locally (stale ref)
			// Fall through to check main/master
		}
	}

	// Fallback: check if main or master exists (prefer main)
	checkMain := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--verify", "main")
	if checkMain.Run() == nil {
		return "main"
	}

	checkMaster := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--verify", "master")
	if checkMaster.Run() == nil {
		return "master"
	}

	return "main" // Default fallback
}
