// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
)

// WorktreeClient provides access to git worktree operations.
//
// Worktrees allow developers to have multiple checkouts of the same repository,
// each on a different branch. Trellis can switch between worktrees, automatically
// rebuilding and restarting services as needed.
//
// Access this client through [Client.Worktrees]:
//
//	worktrees, err := client.Worktrees.List(ctx)
type WorktreeClient struct {
	c *Client
}

// List returns all configured worktrees.
//
// The returned list includes the currently active worktree (marked with Active: true)
// and all other available worktrees.
func (w *WorktreeClient) List(ctx context.Context) ([]Worktree, error) {
	data, err := w.c.get(ctx, "/api/v1/worktrees")
	if err != nil {
		return nil, err
	}

	var resp struct {
		Worktrees []Worktree `json:"worktrees"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse worktrees: %w", err)
	}

	return resp.Worktrees, nil
}

// Get returns a specific worktree by name.
//
// The name is typically the last component of the worktree path
// (e.g., "feature-branch" for a worktree at /path/to/feature-branch).
func (w *WorktreeClient) Get(ctx context.Context, name string) (*Worktree, error) {
	data, err := w.c.get(ctx, "/api/v1/worktrees/"+name)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Worktree Worktree `json:"worktree"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse worktree: %w", err)
	}

	return &resp.Worktree, nil
}

// Activate switches to the specified worktree.
//
// Activating a worktree makes it the current working environment. This may
// trigger rebuilds and service restarts depending on the Trellis configuration.
//
// Returns information about the activation including how long it took.
func (w *WorktreeClient) Activate(ctx context.Context, name string) (*ActivateResult, error) {
	data, err := w.c.post(ctx, "/api/v1/worktrees/"+name+"/activate")
	if err != nil {
		return nil, err
	}

	var result ActivateResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse activation result: %w", err)
	}

	return &result, nil
}

// RemoveOptions configures worktree removal behavior.
type RemoveOptions struct {
	// DeleteBranch also deletes the git branch when removing the worktree.
	DeleteBranch bool `json:"delete_branch"`
}

// Remove removes a worktree from Trellis management.
//
// If opts.DeleteBranch is true, the associated git branch is also deleted.
// The currently active worktree cannot be removed.
func (w *WorktreeClient) Remove(ctx context.Context, name string, opts *RemoveOptions) error {
	path := "/api/v1/worktrees/" + name
	if opts != nil && opts.DeleteBranch {
		path += "?delete_branch=1"
	}
	_, err := w.c.delete(ctx, path)
	return err
}
