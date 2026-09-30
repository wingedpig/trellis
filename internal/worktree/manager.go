// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/validate"
)

// WorktreeManager manages git worktrees.
type WorktreeManager struct {
	mu            sync.RWMutex
	activateMu    sync.Mutex // Serializes Activate operations
	mergeMu       sync.Mutex // Serializes Merge operations
	git           GitExecutor
	bus           events.EventBus
	cfg           config.WorktreeConfig
	repoDir       string // Directory to run git commands in
	createDir     string // Directory where new worktrees are created
	worktrees     []WorktreeInfo
	active        *WorktreeInfo
	binPath       string
	lifecycle     *LifecycleRunner
	projectName   string
	defaultBranch string // Cached default branch (main/master), refreshed in Refresh()
	expander      *config.TemplateExpander
}

// NewManager creates a new worktree manager.
// repoDir is the directory to run git commands in (for worktree discovery).
// createDir is the directory where new worktrees are created.
func NewManager(git GitExecutor, bus events.EventBus, cfg config.WorktreeConfig, repoDir, createDir, projectName string) *WorktreeManager {
	mgr := &WorktreeManager{
		git:         git,
		bus:         bus,
		cfg:         cfg,
		repoDir:     repoDir,
		createDir:   createDir,
		lifecycle:   NewLifecycleRunner(bus, repoDir),
		projectName: projectName,
		expander:    config.NewTemplateExpander(),
	}

	// Initial load of worktrees
	mgr.Refresh()

	return mgr
}

// List returns all known worktrees.
func (m *WorktreeManager) List() ([]WorktreeInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]WorktreeInfo, len(m.worktrees))
	copy(result, m.worktrees)
	return result, nil
}

// Active returns the currently active worktree.
func (m *WorktreeManager) Active() *WorktreeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.active == nil {
		return nil
	}
	// Return a copy
	active := *m.active
	return &active
}

// SetActive sets the active worktree without running lifecycle hooks.
// Use this for initial startup; use Activate for mid-session switches.
func (m *WorktreeManager) SetActive(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the worktree
	var target *WorktreeInfo
	for i := range m.worktrees {
		if m.worktrees[i].Name() == name || m.worktrees[i].Branch == name {
			target = &m.worktrees[i]
			break
		}
	}

	if target == nil {
		return fmt.Errorf("worktree %q not found", name)
	}

	m.active = target
	m.binPath = m.activeBinariesPath(target)
	return nil
}

// Activate sets the active worktree by name, running lifecycle hooks.
func (m *WorktreeManager) Activate(ctx context.Context, name string) (*ActivateResult, error) {
	// Serialize activation operations to prevent race conditions
	m.activateMu.Lock()
	defer m.activateMu.Unlock()

	start := time.Now()

	// Find the worktree (with lock)
	m.mu.RLock()
	var target *WorktreeInfo
	for i := range m.worktrees {
		if m.worktrees[i].Name() == name || m.worktrees[i].Branch == name {
			wt := m.worktrees[i] // Copy to avoid pointer to loop variable
			target = &wt
			break
		}
	}
	// Capture oldActive data now (not the pointer) to avoid race with Refresh
	var oldActiveName, oldActivePath, oldActiveBranch string
	hasOldActive := m.active != nil
	if hasOldActive {
		oldActiveName = m.active.Name()
		oldActivePath = m.active.Path
		oldActiveBranch = m.active.Branch
	}
	m.mu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("worktree %q not found", name)
	}

	result := &ActivateResult{
		Worktree: *target,
	}

	// Run pre_activate lifecycle hooks WITHOUT holding the data lock
	// This allows hooks to query worktree status without deadlocking
	if len(m.cfg.Lifecycle.PreActivate) > 0 && m.lifecycle != nil {
		hookResults, err := m.lifecycle.RunPreActivate(ctx, target, m.cfg.Lifecycle.PreActivate)
		result.HookResults = hookResults
		if err != nil {
			result.Duration = time.Since(start).String()
			return result, fmt.Errorf("pre_activate hooks failed: %w", err)
		}
	}

	// Now acquire lock to mutate state
	m.mu.Lock()

	// Re-validate target still exists after hooks (worktree could have been removed)
	var currentTarget *WorktreeInfo
	for i := range m.worktrees {
		if m.worktrees[i].Name() == name || m.worktrees[i].Branch == name {
			currentTarget = &m.worktrees[i]
			break
		}
	}

	if currentTarget == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("worktree %q was removed during activation", name)
	}

	// Set new active
	m.active = currentTarget
	m.binPath = m.activeBinariesPath(m.active)

	// Copy current target info for event (before releasing lock)
	activatedName := currentTarget.Name()
	activatedPath := currentTarget.Path
	activatedBranch := currentTarget.Branch

	m.mu.Unlock()

	result.Duration = time.Since(start).String()

	// Emit events OUTSIDE lock to prevent deadlock if subscribers call back into worktree APIs
	// Deactivate current if any (only after hooks succeed and target validated)
	if hasOldActive && m.bus != nil {
		m.bus.Publish(ctx, events.Event{
			Type:     events.EventWorktreeDeactivating,
			Worktree: oldActiveName,
			Payload: map[string]interface{}{
				"name":   oldActiveName,
				"path":   oldActivePath,
				"branch": oldActiveBranch,
			},
		})
	}

	// Emit activated event
	if m.bus != nil {
		m.bus.Publish(ctx, events.Event{
			Type:     "worktree.activated",
			Worktree: activatedName,
			Payload: map[string]interface{}{
				"name":   activatedName,
				"path":   activatedPath,
				"branch": activatedBranch,
			},
		})
	}

	return result, nil
}

// Refresh reloads the worktree list from git.
func (m *WorktreeManager) Refresh() error {
	ctx := context.Background()
	worktrees, err := m.git.WorktreeList(ctx, m.repoDir)
	if err != nil {
		return err
	}

	// Get the default branch for commit comparison
	defaultBranch := GetDefaultBranch(ctx, m.repoDir)

	// Populate status fields for each worktree
	for i := range worktrees {
		wt := &worktrees[i]

		// Skip bare repos for status checks
		if wt.IsBare {
			continue
		}

		// Get dirty status
		wt.Dirty = IsDirty(ctx, wt.Path)

		// Get ahead/behind default branch (skip the default branch itself)
		if !wt.Detached && wt.Branch != "" && wt.Branch != defaultBranch {
			wt.Ahead, wt.Behind = GetAheadBehind(ctx, wt.Path, defaultBranch)
		}
	}

	m.mu.Lock()
	m.worktrees = worktrees
	m.defaultBranch = defaultBranch

	// Update active if it still exists
	if m.active != nil {
		found := false
		for i := range worktrees {
			if worktrees[i].Path == m.active.Path {
				m.active = &worktrees[i]
				found = true
				break
			}
		}
		if !found {
			m.active = nil
		}
	}
	m.mu.Unlock()

	return nil
}

// GetByName returns a worktree by name. Accepts:
//   - Directory name (e.g., "trellis-logchanges")
//   - Branch name (e.g., "logchanges" or "feature/logchanges")
//   - Friendly name "main" for the main worktree
//   - Friendly name without project prefix (e.g., "logchanges" matches "trellis-logchanges")
func (m *WorktreeManager) GetByName(name string) (WorktreeInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Handle "main" specially - matches worktree where directory name equals project name
	if name == "main" {
		for _, wt := range m.worktrees {
			if wt.Name() == m.projectName {
				return wt, true
			}
		}
	}

	// Try exact match on directory name or branch name
	for _, wt := range m.worktrees {
		if wt.Name() == name || wt.Branch == name {
			return wt, true
		}
	}

	// Try with project prefix (e.g., "logchanges" -> "trellis-logchanges")
	if m.projectName != "" {
		fullName := m.projectName + "-" + name
		for _, wt := range m.worktrees {
			if wt.Name() == fullName {
				return wt, true
			}
		}
	}

	return WorktreeInfo{}, false
}

// DefaultBranch returns the repository's default branch (main/master), cached
// from the most recent Refresh.
func (m *WorktreeManager) DefaultBranch() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultBranch
}

// GetByPath returns a worktree by its path.
func (m *WorktreeManager) GetByPath(path string) (WorktreeInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, wt := range m.worktrees {
		if wt.Path == path {
			return wt, true
		}
	}
	return WorktreeInfo{}, false
}

// Count returns the number of worktrees.
func (m *WorktreeManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.worktrees)
}

// Status returns the git status of the active worktree.
func (m *WorktreeManager) Status() (GitStatus, error) {
	m.mu.RLock()
	active := m.active
	m.mu.RUnlock()

	if active == nil {
		return GitStatus{}, fmt.Errorf("no active worktree")
	}

	return m.git.Status(context.Background(), active.Path)
}

// BinariesPath returns the path to binaries for the active worktree.
func (m *WorktreeManager) BinariesPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.binPath
}

// activeBinariesPath resolves the binaries path to record for a newly
// activated worktree. Expansion failure is not fatal to activation: it is
// logged and the path is left empty so nothing acts on a wrong location.
func (m *WorktreeManager) activeBinariesPath(wt *WorktreeInfo) string {
	binPath, err := m.expandBinariesPath(wt)
	if err != nil {
		fmt.Printf("Warning: %v\n", err)
		return ""
	}
	return binPath
}

// expandBinariesPath resolves worktree.binaries.path for the given worktree.
// The configured value is a Go text/template, like every other templated
// config value, so it goes through the shared config expander rather than
// substituting a fixed set of placeholders. That matters for templates with
// control flow such as {{if .Worktree.Name}}...{{else}}...{{end}}, which a
// placeholder substitution leaves unexpanded.
func (m *WorktreeManager) expandBinariesPath(wt *WorktreeInfo) (string, error) {
	path := m.cfg.Binaries.Path
	if path == "" {
		return filepath.Join(wt.Path, "bin"), nil
	}

	expander := m.expander
	if expander == nil {
		expander = config.NewTemplateExpander()
	}
	ctx := &config.TemplateContext{
		Worktree: config.WorktreeTemplateData{
			Root:   wt.Path,
			Name:   wt.Name(),
			Branch: wt.Branch,
		},
		Project: config.ProjectTemplateData{
			Name: m.projectName,
			Root: m.repoDir,
		},
	}
	expanded, err := expander.Expand(path, ctx)
	if err != nil {
		return "", fmt.Errorf("failed to expand worktree.binaries.path %q: %w", path, err)
	}
	return expanded, nil
}

// ProjectName returns the project name.
func (m *WorktreeManager) ProjectName() string {
	return m.projectName
}

// Create creates a new worktree with the given branch name.
func (m *WorktreeManager) Create(ctx context.Context, branchName string, switchTo bool) error {
	// Validate branch name before it reaches git argv (rejects leading '-'
	// flag/argument injection and other unsafe characters).
	if err := validate.Name("branch", branchName); err != nil {
		return err
	}

	// Determine the worktree directory name (projectName-branchName)
	// Sanitize branch name: replace slashes with dashes for filesystem compatibility
	sanitizedBranch := strings.ReplaceAll(branchName, "/", "-")

	// Check for naming collision: branch names like "feature/foo" and "feature-foo"
	// would both sanitize to "feature-foo"
	if sanitizedBranch != branchName {
		// The branch name was modified, check if the sanitized version conflicts
		// with a branch that already has that exact name
		conflictBranch := sanitizedBranch
		checkConflict := exec.CommandContext(ctx, "git", "-C", m.repoDir, "rev-parse", "--verify", conflictBranch)
		if checkConflict.Run() == nil {
			return fmt.Errorf("branch name %q would create directory %q-%s which conflicts with existing branch %q",
				branchName, m.projectName, sanitizedBranch, conflictBranch)
		}
	}

	worktreeName := m.projectName + "-" + sanitizedBranch
	worktreePath := filepath.Join(m.createDir, worktreeName)

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree directory already exists: %s (branch names with '/' are converted to '-' for filesystem compatibility)", worktreePath)
	}

	// Check if branch already exists
	checkBranch := exec.CommandContext(ctx, "git", "-C", m.repoDir, "rev-parse", "--verify", branchName)
	if err := checkBranch.Run(); err == nil {
		return fmt.Errorf("branch %q already exists", branchName)
	}

	// Create the branch and worktree
	cmd := exec.CommandContext(ctx, "git", "-C", m.repoDir, "worktree", "add", "-b", branchName, worktreePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create worktree: %s: %w", string(output), err)
	}

	// Create binaries directory for the new worktree
	wt := &WorktreeInfo{
		Path:   worktreePath,
		Branch: branchName,
	}
	if binPath, err := m.expandBinariesPath(wt); err != nil {
		// Non-fatal, just log
		fmt.Printf("Warning: %v\n", err)
	} else if err := os.MkdirAll(binPath, 0755); err != nil {
		// Non-fatal, just log
		fmt.Printf("Warning: failed to create binaries directory %s: %v\n", binPath, err)
	}

	// Emit event
	if m.bus != nil {
		m.bus.Publish(ctx, events.Event{
			Type:     "worktree.created",
			Worktree: worktreeName,
			Payload: map[string]interface{}{
				"name":   worktreeName,
				"path":   worktreePath,
				"branch": branchName,
			},
		})
	}

	// Run on_create lifecycle hooks (one-time setup for new worktrees)
	if len(m.cfg.Lifecycle.OnCreate) > 0 && m.lifecycle != nil {
		if _, err := m.lifecycle.RunOnCreate(ctx, wt, m.cfg.Lifecycle.OnCreate); err != nil {
			// Log but don't fail - worktree is created, just hooks failed
			fmt.Printf("Warning: on_create hooks failed for %s: %v\n", worktreeName, err)
		}
	}

	// Refresh worktree list
	if err := m.Refresh(); err != nil {
		return fmt.Errorf("failed to refresh worktree list: %w", err)
	}

	// Switch to new worktree if requested
	if switchTo {
		if _, err := m.Activate(ctx, worktreeName); err != nil {
			return fmt.Errorf("worktree created but failed to activate: %w", err)
		}
	}

	return nil
}

// Remove removes a worktree and optionally force-deletes the branch.
func (m *WorktreeManager) Remove(ctx context.Context, name string, deleteBranch bool) error {
	_, err := m.RemoveWithOptions(ctx, name, RemoveOptions{DeleteBranch: deleteBranch})
	return err
}

// RemoveWithOptions runs pre_remove hooks, then removes a worktree and
// optionally its branch.
func (m *WorktreeManager) RemoveWithOptions(ctx context.Context, name string, opts RemoveOptions) (*RemoveResult, error) {
	result := &RemoveResult{}

	// Get the worktree info
	wt, found := m.GetByName(name)
	if !found {
		return nil, fmt.Errorf("worktree %q not found", name)
	}

	// Check if this is the active worktree
	active := m.Active()
	if active != nil && active.Path == wt.Path {
		return nil, fmt.Errorf("cannot remove the active worktree")
	}

	// Check if this is the main repository
	if wt.Path == m.repoDir {
		return nil, fmt.Errorf("cannot remove the main repository")
	}

	// Run pre_remove hooks. Skipped when the directory is already gone
	// (hooks could only fail, and would block cleaning up the stale entry).
	if len(m.cfg.Lifecycle.PreRemove) > 0 && m.lifecycle != nil {
		if _, err := os.Stat(wt.Path); err == nil {
			hooks, err := m.lifecycle.RunPreRemove(ctx, &wt, m.cfg.Lifecycle.PreRemove)
			result.PreRemoveHooks = hooks
			if err != nil {
				return nil, &RemoveError{Msg: "pre_remove " + err.Error() + "; worktree not removed", Hooks: hooks}
			}
		}
	}

	// Kill any tmux session for this worktree (use canonical wt.Name(), not
	// request alias). Exact-match the target ("=" prefix): if the session is
	// already gone, tmux's prefix fallback would otherwise kill a
	// similarly-named sibling (e.g. "proj-foo" resolving to "proj-foo-bar").
	sessionName := strings.ReplaceAll(wt.Name(), ".", "_")
	killTmux := exec.CommandContext(ctx, "tmux", "kill-session", "-t", "="+sessionName)
	killTmux.Run() // Ignore error - session may not exist

	// Remove binaries directory. If the path cannot be expanded, leave the
	// filesystem alone rather than deleting a guessed location. Also refuse
	// to delete the active worktree's binaries, which a template that does
	// not vary per worktree would otherwise resolve to.
	if binPath, err := m.expandBinariesPath(&wt); err != nil {
		fmt.Printf("Warning: %v\n", err)
	} else if binPath != "" && binPath != m.BinariesPath() {
		if err := os.RemoveAll(binPath); err != nil {
			fmt.Printf("Warning: failed to remove binaries directory %s: %v\n", binPath, err)
		}
	}

	// Remove the worktree
	cmd := exec.CommandContext(ctx, "git", "-C", m.repoDir, "worktree", "remove", "--force", wt.Path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to remove worktree: %s: %w", string(output), err)
	}

	// Optionally delete the branch
	if opts.DeleteBranch && wt.Branch != "" && wt.Branch != "main" && wt.Branch != "master" {
		flag := "-D"
		if opts.SafeBranchDelete {
			flag = "-d"
		}
		deleteBranchCmd := exec.CommandContext(ctx, "git", "-C", m.repoDir, "branch", flag, wt.Branch)
		if output, err := deleteBranchCmd.CombinedOutput(); err != nil {
			result.BranchError = strings.TrimSpace(string(output))
			fmt.Printf("Warning: failed to delete branch %s: %s\n", wt.Branch, string(output))
		} else {
			result.BranchDeleted = true
		}
	}

	// Refresh the worktree list BEFORE publishing the event so any
	// subscriber that consults the manager (e.g. the session-cleanup
	// subscriber in app.go that uses GetByName to detect orphaned sessions)
	// sees the post-delete state and doesn't accidentally find the
	// just-removed worktree still listed.
	if err := m.Refresh(); err != nil {
		return nil, err
	}

	// Emit event
	if m.bus != nil {
		m.bus.Publish(ctx, events.Event{
			Type:     events.EventWorktreeDeleted,
			Worktree: name,
			Payload: map[string]interface{}{
				"name":          name,
				"canonicalName": wt.Name(),
				"path":          wt.Path,
				"branch":        wt.Branch,
				"branchDeleted": result.BranchDeleted,
			},
		})
	}

	return result, nil
}
