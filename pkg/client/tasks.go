// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// TaskClient drives the task lifecycle: start work on a new branch with an
// agent session, then merge it into the default branch and clean up.
//
// Access this client through [Client.Tasks]:
//
//	res, err := client.Tasks.Start(ctx, client.TaskStartRequest{
//	    Branch: "fix-digest-crash",
//	    Prompt: "Investigate the digest crash in the newest crash report",
//	})
type TaskClient struct {
	c *Client
}

// TaskStartRequest configures [TaskClient.Start].
type TaskStartRequest struct {
	// Branch is the new branch; its worktree is created from the main repository's HEAD.
	Branch string `json:"branch"`
	// Agent is "claude" (the default) or "codex".
	Agent string `json:"agent,omitempty"`
	// Prompt, when set, is sent to the new session as its first message.
	Prompt string `json:"prompt,omitempty"`
	// Model and Effort override the session's configured default model.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// SessionName is the session's display name (default: auto-numbered).
	SessionName string `json:"session_name,omitempty"`
	// Activate switches Trellis to the new worktree (restarting services).
	Activate bool `json:"activate,omitempty"`
}

// SessionStartRequest configures [TaskClient.StartSession].
type SessionStartRequest struct {
	// Worktree is the existing worktree (name, branch, or "main") to start the session in.
	Worktree string `json:"worktree"`
	// Agent is "claude" (the default) or "codex".
	Agent string `json:"agent,omitempty"`
	// Prompt, when set, is sent to the new session as its first message.
	Prompt string `json:"prompt,omitempty"`
	// Model and Effort override the session's configured default model.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// SessionName is the session's display name (default: auto-numbered).
	SessionName string `json:"session_name,omitempty"`
}

// TaskStartResult is returned by [TaskClient.Start] and [TaskClient.StartSession].
type TaskStartResult struct {
	Worktree   Worktree `json:"worktree"`
	Agent      string   `json:"agent"`
	SessionID  string   `json:"session_id"`
	SessionURL string   `json:"session_url"` // path relative to the Trellis base URL
	PromptSent bool     `json:"prompt_sent"`
	Warnings   []string `json:"warnings,omitempty"`
}

// TaskFinishRequest configures [TaskClient.Finish].
type TaskFinishRequest struct {
	// Worktree is the worktree (or branch) to finish.
	Worktree string `json:"worktree"`
	// Strategy is "ff" (rebase then fast-forward; the default), "merge", or "squash".
	Strategy string `json:"strategy,omitempty"`
	// Message is the merge or squash commit message (default: derived from the branch).
	Message string `json:"message,omitempty"`
	// KeepBranch keeps the branch after the worktree is removed.
	KeepBranch bool `json:"keep_branch,omitempty"`
	// KeepWorktree merges but leaves the worktree and its sessions in place.
	KeepWorktree bool `json:"keep_worktree,omitempty"`
}

// HookResult is the outcome of one lifecycle hook.
type HookResult struct {
	Name     string        `json:"Name"`
	Success  bool          `json:"Success"`
	Duration time.Duration `json:"Duration"`
	Output   string        `json:"Output"`
}

// TaskMergeResult describes the merge step of a finish.
type TaskMergeResult struct {
	Worktree       string       `json:"worktree"`
	Branch         string       `json:"branch"`
	Base           string       `json:"base"`
	BaseWorktree   string       `json:"base_worktree"`
	Strategy       string       `json:"strategy"`
	Merged         bool         `json:"merged"` // false when there was nothing to merge
	Commits        int          `json:"commits"`
	Head           string       `json:"head,omitempty"`
	PreMergeHooks  []HookResult `json:"pre_merge_hooks,omitempty"`
	PostMergeHooks []HookResult `json:"post_merge_hooks,omitempty"`
	PostMergeError string       `json:"post_merge_error,omitempty"`
}

// TaskCleanupResult describes the cleanup step of a finish.
type TaskCleanupResult struct {
	Activated        string `json:"activated,omitempty"`
	ArchivedSessions int    `json:"archived_sessions"`
	Removed          bool   `json:"removed"`
	Remove           *struct {
		PreRemoveHooks []HookResult `json:"pre_remove_hooks,omitempty"`
		BranchDeleted  bool         `json:"branch_deleted"`
		BranchError    string       `json:"branch_error,omitempty"`
	} `json:"remove,omitempty"`
	Error          string       `json:"error,omitempty"`
	PreRemoveHooks []HookResult `json:"pre_remove_hooks,omitempty"`
}

// TaskFinishResult is returned by [TaskClient.Finish].
type TaskFinishResult struct {
	Merge TaskMergeResult `json:"merge"`
	// Cleanup is nil when the worktree was kept or cleanup was deferred.
	Cleanup *TaskCleanupResult `json:"cleanup,omitempty"`
	// CleanupDeferred is true when an agent session in the worktree was
	// mid-turn; the server removes the worktree once every session is idle.
	CleanupDeferred bool `json:"cleanup_deferred,omitempty"`
	KeptWorktree    bool `json:"kept_worktree,omitempty"`
}

// Start creates a worktree for a new branch and opens an agent session in it,
// sending req.Prompt as the first message when set.
func (t *TaskClient) Start(ctx context.Context, req TaskStartRequest) (*TaskStartResult, error) {
	data, err := t.c.postJSON(ctx, "/api/v1/tasks/start", req)
	if err != nil {
		return nil, err
	}
	var res TaskStartResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("failed to parse task start result: %w", err)
	}
	return &res, nil
}

// StartSession opens an agent session in an existing worktree, sending
// req.Prompt as the first message when set.
func (t *TaskClient) StartSession(ctx context.Context, req SessionStartRequest) (*TaskStartResult, error) {
	data, err := t.c.postJSON(ctx, "/api/v1/sessions/start", req)
	if err != nil {
		return nil, err
	}
	var res TaskStartResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("failed to parse session start result: %w", err)
	}
	return &res, nil
}

// Finish merges a worktree's branch into the default branch (running
// pre_merge and post_merge hooks), archives its agent sessions, and removes
// it. Merge failures are returned as *APIError with "conflicts" and
// "hook_results" in Details when available.
func (t *TaskClient) Finish(ctx context.Context, req TaskFinishRequest) (*TaskFinishResult, error) {
	data, err := t.c.postJSON(ctx, "/api/v1/tasks/finish", req)
	if err != nil {
		return nil, err
	}
	var res TaskFinishResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("failed to parse task finish result: %w", err)
	}
	return &res, nil
}
