// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CrashClient provides access to crash history operations.
type CrashClient struct {
	c *Client
}

// CrashSummary represents a crash record summary.
type CrashSummary struct {
	ID        string    `json:"id"`
	Service   string    `json:"service"`
	Timestamp time.Time `json:"timestamp"`
	TraceID   string    `json:"trace_id"`
	ExitCode  int       `json:"exit_code"`
	Error     string    `json:"error"`
}

// Crash represents a full crash record with all context.
// Mirrors trace.TraceReport format for consistency.
type Crash struct {
	Version   string            `json:"version"`
	ID        string            `json:"id"`
	Service   string            `json:"service"`
	Timestamp time.Time         `json:"timestamp"`
	TraceID   string            `json:"trace_id"`
	ExitCode  int               `json:"exit_code"`
	Error     string            `json:"error"`
	Worktree  CrashWorktreeInfo `json:"worktree"`
	Summary   CrashStats        `json:"summary"`
	Entries   []CrashEntry      `json:"entries"`
	Trigger   string            `json:"trigger"`
}

// CrashStats contains summary statistics for a crash report.
type CrashStats struct {
	TotalEntries int            `json:"total_entries"`
	BySource     map[string]int `json:"by_source"`
	ByLevel      map[string]int `json:"by_level"`
}

// CrashEntry represents a single log entry in a crash report.
type CrashEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Source    string         `json:"source"`
	Level     string         `json:"level,omitempty"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
	Raw       string         `json:"raw"`
}

// CrashWorktreeInfo contains worktree context for a crash.
type CrashWorktreeInfo struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
}

// List returns all crashes, sorted by timestamp (newest first).
func (c *CrashClient) List(ctx context.Context) ([]CrashSummary, error) {
	data, err := c.c.get(ctx, "/api/v1/crashes")
	if err != nil {
		return nil, err
	}

	var summaries []CrashSummary
	if err := json.Unmarshal(data, &summaries); err != nil {
		return nil, fmt.Errorf("failed to parse crashes: %w", err)
	}

	return summaries, nil
}

// Get retrieves a specific crash by ID.
func (c *CrashClient) Get(ctx context.Context, id string) (*Crash, error) {
	data, err := c.c.get(ctx, "/api/v1/crashes/"+id)
	if err != nil {
		return nil, err
	}

	var crash Crash
	if err := json.Unmarshal(data, &crash); err != nil {
		return nil, fmt.Errorf("failed to parse crash: %w", err)
	}

	return &crash, nil
}

// Newest returns the most recent crash.
func (c *CrashClient) Newest(ctx context.Context) (*Crash, error) {
	data, err := c.c.get(ctx, "/api/v1/crashes/newest")
	if err != nil {
		return nil, err
	}

	// Handle null response
	if string(data) == "null" {
		return nil, nil
	}

	var crash Crash
	if err := json.Unmarshal(data, &crash); err != nil {
		return nil, fmt.Errorf("failed to parse crash: %w", err)
	}

	return &crash, nil
}

// Delete removes a crash by ID.
func (c *CrashClient) Delete(ctx context.Context, id string) error {
	_, err := c.c.delete(ctx, "/api/v1/crashes/"+id)
	return err
}

// Clear removes all crashes.
func (c *CrashClient) Clear(ctx context.Context) error {
	_, err := c.c.delete(ctx, "/api/v1/crashes")
	return err
}
