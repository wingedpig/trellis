// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// LogClient provides access to log viewer operations.
//
// Log viewers aggregate logs from external sources like system logs,
// third-party services, or custom log files. Unlike service logs which
// are captured directly by Trellis, log viewers read from external sources.
//
// Access this client through [Client.Logs]:
//
//	viewers, err := client.Logs.List(ctx)
//	entries, err := client.Logs.GetEntries(ctx, "nginx", nil)
type LogClient struct {
	c *Client
}

// List returns all configured log viewers.
func (l *LogClient) List(ctx context.Context) ([]LogViewer, error) {
	data, err := l.c.get(ctx, "/api/v1/logs")
	if err != nil {
		return nil, err
	}

	var viewers []LogViewer
	if err := json.Unmarshal(data, &viewers); err != nil {
		return nil, fmt.Errorf("failed to parse log viewers: %w", err)
	}

	return viewers, nil
}

// GetEntries returns log entries from a log viewer's live buffer.
//
// The live buffer contains recent log entries held in memory. For historical
// entries from rotated log files, use [LogClient.GetHistoryEntries].
//
// Use opts to filter entries by time range, level, or grep pattern.
func (l *LogClient) GetEntries(ctx context.Context, name string, opts *LogEntriesOptions) ([]LogEntry, error) {
	path := "/api/v1/logs/" + name + "/entries"

	if opts != nil {
		params := url.Values{}
		if opts.Limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", opts.Limit))
		}
		if !opts.Since.IsZero() {
			params.Set("after", opts.Since.Format(time.RFC3339))
		}
		if !opts.Until.IsZero() {
			params.Set("before", opts.Until.Format(time.RFC3339))
		}
		if opts.Level != "" {
			params.Set("level", opts.Level)
		}
		if opts.Grep != "" {
			params.Set("grep", opts.Grep)
		}
		if len(params) > 0 {
			path += "?" + params.Encode()
		}
	}

	data, err := l.c.get(ctx, path)
	if err != nil {
		return nil, err
	}

	var result struct {
		Entries []LogEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse log entries: %w", err)
	}

	return result.Entries, nil
}

// GetHistoryEntries returns parsed log entries from a log viewer's history files.
//
// History entries come from rotated log files on disk, allowing access to
// older logs that have been rotated out of the live buffer.
//
// Use opts to filter by time range and grep pattern. The Before/After options
// in opts specify context lines around grep matches.
func (l *LogClient) GetHistoryEntries(ctx context.Context, name string, opts *LogEntriesOptions) ([]LogEntry, error) {
	params := url.Values{}

	if opts != nil {
		if opts.Limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", opts.Limit))
		}
		if !opts.Since.IsZero() {
			params.Set("start", opts.Since.Format(time.RFC3339))
		}
		if !opts.Until.IsZero() {
			params.Set("end", opts.Until.Format(time.RFC3339))
		}
		if opts.Grep != "" {
			params.Set("grep", opts.Grep)
		}
		if opts.Before > 0 {
			params.Set("before", fmt.Sprintf("%d", opts.Before))
		}
		if opts.After > 0 {
			params.Set("after", fmt.Sprintf("%d", opts.After))
		}
	}

	path := "/api/v1/logs/" + name + "/history"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	data, err := l.c.get(ctx, path)
	if err != nil {
		return nil, err
	}

	var result struct {
		Entries []LogEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse historical entries: %w", err)
	}

	return result.Entries, nil
}

// GetHistory returns raw log lines from a log viewer's history files.
//
// Unlike [LogClient.GetHistoryEntries] which returns parsed entries, this method
// returns the raw log data as bytes. Use this when you need the original
// unprocessed log content.
func (l *LogClient) GetHistory(ctx context.Context, name string, lines int) ([]byte, error) {
	path := fmt.Sprintf("/api/v1/logs/%s/history?lines=%d", name, lines)
	return l.c.get(ctx, path)
}
