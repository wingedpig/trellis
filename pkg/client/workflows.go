// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
)

// WorkflowClient provides access to workflow execution.
//
// Workflows are predefined command sequences that can be executed on demand,
// such as build, test, deploy, or database migration scripts.
//
// Access this client through [Client.Workflows]:
//
//	workflows, err := client.Workflows.List(ctx)
//	status, err := client.Workflows.Run(ctx, "build", nil)
type WorkflowClient struct {
	c *Client
}

// List returns all configured workflows.
func (w *WorkflowClient) List(ctx context.Context) ([]Workflow, error) {
	data, err := w.c.get(ctx, "/api/v1/workflows")
	if err != nil {
		return nil, err
	}

	var workflows []Workflow
	if err := json.Unmarshal(data, &workflows); err != nil {
		return nil, fmt.Errorf("failed to parse workflows: %w", err)
	}

	return workflows, nil
}

// Get returns a specific workflow definition by ID.
func (w *WorkflowClient) Get(ctx context.Context, id string) (*Workflow, error) {
	data, err := w.c.get(ctx, "/api/v1/workflows/"+id)
	if err != nil {
		return nil, err
	}

	var wf Workflow
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("failed to parse workflow: %w", err)
	}

	return &wf, nil
}

// RunOptions configures workflow execution.
type RunOptions struct {
	// Worktree specifies which worktree to run the workflow in.
	// If empty, uses the currently active worktree.
	Worktree string `json:"worktree,omitempty"`

	// Inputs provides values for workflow input parameters.
	Inputs map[string]any `json:"inputs,omitempty"`
}

// Run starts executing a workflow.
//
// The workflow runs asynchronously. This method returns immediately with
// the initial status. Use [WorkflowClient.Status] to poll for completion.
//
// Example:
//
//	status, err := client.Workflows.Run(ctx, "build", nil)
//	for status.State == "running" {
//	    time.Sleep(500 * time.Millisecond)
//	    status, err = client.Workflows.Status(ctx, status.ID)
//	}
func (w *WorkflowClient) Run(ctx context.Context, id string, opts *RunOptions) (*WorkflowStatus, error) {
	path := "/api/v1/workflows/" + id + "/run"
	if opts != nil && opts.Worktree != "" {
		path += "?worktree=" + opts.Worktree
	}

	var data json.RawMessage
	var err error

	// Use POST with JSON body if inputs are provided
	if opts != nil && len(opts.Inputs) > 0 {
		body := map[string]any{"inputs": opts.Inputs}
		data, err = w.c.postJSON(ctx, path, body)
	} else {
		data, err = w.c.post(ctx, path)
	}
	if err != nil {
		return nil, err
	}

	var status WorkflowStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("failed to parse workflow status: %w", err)
	}

	return &status, nil
}

// Status returns the current execution status of a workflow.
//
// Use this to poll for workflow completion after calling [WorkflowClient.Run].
// The workflow is complete when State is "complete" or "failed".
func (w *WorkflowClient) Status(ctx context.Context, id string) (*WorkflowStatus, error) {
	data, err := w.c.get(ctx, "/api/v1/workflows/"+id+"/status")
	if err != nil {
		return nil, err
	}

	var status WorkflowStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("failed to parse workflow status: %w", err)
	}

	return &status, nil
}
