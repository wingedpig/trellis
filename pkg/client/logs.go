// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
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
		// Build filter string from Level and Grep options
		var filterParts []string
		if opts.Level != "" {
			filterParts = append(filterParts, "level:"+opts.Level)
		}
		if opts.Grep != "" {
			filterParts = append(filterParts, fmt.Sprintf("%q", opts.Grep))
		}
		if len(filterParts) > 0 {
			params.Set("filter", strings.Join(filterParts, " "))
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
//
// The client requests the streaming NDJSON response shape so headers arrive
// immediately. The HTTP client's per-request Timeout is bypassed for this
// call — pass a deadline via ctx if you want one.
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

	resp, err := l.c.stream(ctx, path, "application/x-ndjson")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Server falls back to the buffered envelope if it doesn't honor the
	// streaming Accept header (e.g., older builds). Detect that and parse
	// accordingly.
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ct), "ndjson") {
		// Buffered fallback: standard apiResponse envelope around {"entries":[...]}.
		var apiResp apiResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return nil, fmt.Errorf("failed to parse historical entries: %w", err)
		}
		if apiResp.Error != nil {
			return nil, apiResp.Error
		}
		var result struct {
			Entries []LogEntry `json:"entries"`
		}
		if err := json.Unmarshal(apiResp.Data, &result); err != nil {
			return nil, fmt.Errorf("failed to parse historical entries: %w", err)
		}
		return result.Entries, nil
	}

	var entries []LogEntry
	scanner := bufio.NewScanner(resp.Body)
	// History responses can include very long lines (multi-line stack traces
	// captured into one entry's Raw field). Grow the buffer well past the
	// 64KiB default to avoid splitting them.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Heartbeats and mid-stream errors are encoded as small JSON objects
		// with reserved underscore-prefixed keys. Peek at the first field
		// before doing a full LogEntry decode.
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(line, &probe); err != nil {
			return entries, fmt.Errorf("malformed NDJSON line: %w", err)
		}
		if _, ok := probe["_heartbeat"]; ok {
			continue
		}
		if raw, ok := probe["_error"]; ok {
			var msg string
			_ = json.Unmarshal(raw, &msg)
			return entries, fmt.Errorf("stream error: %s", msg)
		}
		var entry LogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return entries, fmt.Errorf("malformed log entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return entries, fmt.Errorf("reading history stream: %w", err)
	}
	return entries, nil
}

// GetHistory returns historical log entries from a log viewer's history files.
//
// Unlike [LogClient.GetHistoryEntries] which returns parsed entries via options,
// this method provides a simpler interface that returns entries for a time range
// ending at the current time.
//
// The start and end parameters specify the time range (RFC3339 format).
func (l *LogClient) GetHistory(ctx context.Context, name string, start, end time.Time) ([]LogEntry, error) {
	params := url.Values{}
	params.Set("start", start.Format(time.RFC3339))
	params.Set("end", end.Format(time.RFC3339))

	path := "/api/v1/logs/" + name + "/history?" + params.Encode()
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
