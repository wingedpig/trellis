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
	path := "/api/v1/logs/" + url.PathEscape(name) + "/entries"

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
			params.Set("filter", "level:"+opts.Level)
		}
		// Grep is a regexp over the raw line, matching GetHistoryEntries.
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
		if opts.Timeout > 0 {
			params.Set("timeout", opts.Timeout.String())
		}
	}

	path := "/api/v1/logs/" + url.PathEscape(name) + "/history"
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

	path := "/api/v1/logs/" + url.PathEscape(name) + "/history?" + params.Encode()
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

// BubbleUp compares the entries matching opts.Selection against the rest of
// the same time window and ranks the field values whose share differs most —
// "what is different about these lines?".
func (l *LogClient) BubbleUp(ctx context.Context, opts BubbleUpOptions) (*BubbleUpResult, error) {
	params := url.Values{}
	params.Set("viewers", strings.Join(opts.Viewers, ","))
	params.Set("selection", opts.Selection)
	if opts.Baseline != "" {
		params.Set("baseline", opts.Baseline)
	}
	if !opts.Since.IsZero() {
		params.Set("start", opts.Since.Format(time.RFC3339))
	}
	if !opts.Until.IsZero() {
		params.Set("end", opts.Until.Format(time.RFC3339))
	}
	if len(opts.Fields) > 0 {
		params.Set("fields", strings.Join(opts.Fields, ","))
	}
	if len(opts.Exclude) > 0 {
		params.Set("exclude", strings.Join(opts.Exclude, ","))
	}
	if opts.Top > 0 {
		params.Set("top", fmt.Sprintf("%d", opts.Top))
	}
	if opts.PerField > 0 {
		params.Set("per_field", fmt.Sprintf("%d", opts.PerField))
	}

	data, err := l.c.get(ctx, "/api/v1/logs/bubbleup?"+params.Encode())
	if err != nil {
		return nil, err
	}
	var result BubbleUpResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse bubbleup result: %w", err)
	}
	return &result, nil
}

// Stats summarizes a numeric field (count, mean, min, max, p50/p90/p99)
// across one or more log viewers, optionally grouped by other fields. The
// HTTP client's per-request Timeout is bypassed — opts.Timeout bounds the
// server's read, and ctx bounds the call.
func (l *LogClient) Stats(ctx context.Context, opts LogStatsOptions) (*LogStatsResult, error) {
	params := url.Values{}
	params.Set("viewers", strings.Join(opts.Viewers, ","))
	params.Set("field", opts.Field)
	if len(opts.By) > 0 {
		params.Set("by", strings.Join(opts.By, ","))
	}
	if opts.Where != "" {
		params.Set("where", opts.Where)
	}
	if opts.Grep != "" {
		params.Set("grep", opts.Grep)
	}
	setWindow(params, opts.Since, opts.Until, opts.Timeout)
	if opts.Top > 0 {
		params.Set("top", fmt.Sprintf("%d", opts.Top))
	}
	data, err := l.c.getUntimed(ctx, "/api/v1/logs/stats?"+params.Encode())
	if err != nil {
		return nil, err
	}
	var result LogStatsResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse stats result: %w", err)
	}
	return &result, nil
}

// Requests summarizes requests across one or more log viewers: for each
// request whose start line matches, its start and end lines' fields, its
// duration, and per-pattern line counts. The HTTP client's per-request
// Timeout is bypassed — opts.Timeout bounds the server's read, and ctx
// bounds the call.
func (l *LogClient) Requests(ctx context.Context, opts LogRequestsOptions) (*LogRequestsResult, error) {
	params := url.Values{}
	params.Set("viewers", strings.Join(opts.Viewers, ","))
	if opts.IDField != "" {
		params.Set("id_field", opts.IDField)
	}
	if opts.IDPattern != "" {
		params.Set("id_pattern", opts.IDPattern)
	}
	params.Set("start_match", opts.StartMatch)
	if opts.EndMatch != "" {
		params.Set("end_match", opts.EndMatch)
	}
	for _, c := range opts.Counts {
		params.Add("count", c)
	}
	setWindow(params, opts.Since, opts.Until, opts.Timeout)
	if opts.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	data, err := l.c.getUntimed(ctx, "/api/v1/logs/requests?"+params.Encode())
	if err != nil {
		return nil, err
	}
	var result LogRequestsResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse requests result: %w", err)
	}
	return &result, nil
}

func setWindow(params url.Values, since, until time.Time, timeout time.Duration) {
	if !since.IsZero() {
		params.Set("start", since.Format(time.RFC3339))
	}
	if !until.IsZero() {
		params.Set("end", until.Format(time.RFC3339))
	}
	if timeout > 0 {
		params.Set("timeout", timeout.String())
	}
}
