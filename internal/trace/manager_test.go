// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/logs"
)

// mockLogManager implements just enough of logs.Manager for testing
type mockLogManager struct {
	viewers map[string]*mockViewer
}

func newMockLogManager() *mockLogManager {
	return &mockLogManager{
		viewers: make(map[string]*mockViewer),
	}
}

func (m *mockLogManager) addViewer(name string, entries []logs.LogEntry, err error) {
	m.viewers[name] = &mockViewer{entries: entries, err: err}
}

type mockViewer struct {
	entries []logs.LogEntry
	err     error
}

func (v *mockViewer) GetHistoricalEntries(ctx context.Context, start, end time.Time, filter *logs.Filter, limit int, grep string, grepBefore, grepAfter int) ([]logs.LogEntry, error) {
	if v.err != nil {
		return nil, v.err
	}
	return v.entries, nil
}

func TestManager_GetGroups(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Trace: config.TraceConfig{
			ReportsDir: tmpDir,
			MaxAge:  "7d",
		},
		TraceGroups: []config.TraceGroupConfig{
			{Name: "group1", LogViewers: []string{"viewer1", "viewer2"}},
			{Name: "group2", LogViewers: []string{"viewer3"}},
		},
	}

	mgr, err := NewManager(nil, cfg, nil)
	require.NoError(t, err)

	groups := mgr.GetGroups()
	assert.Len(t, groups, 2)
	assert.Equal(t, "group1", groups[0].Name)
	assert.Equal(t, []string{"viewer1", "viewer2"}, groups[0].LogViewers)
	assert.Equal(t, "group2", groups[1].Name)
}

func TestManager_FindGroup(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Trace: config.TraceConfig{
			ReportsDir: tmpDir,
		},
		TraceGroups: []config.TraceGroupConfig{
			{Name: "api-flow", LogViewers: []string{"api", "db"}},
		},
	}

	mgr, err := NewManager(nil, cfg, nil)
	require.NoError(t, err)

	// Found
	group, err := mgr.findGroup("api-flow")
	require.NoError(t, err)
	assert.Equal(t, "api-flow", group.Name)

	// Not found
	_, err = mgr.findGroup("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "trace group not found")
}

func TestManager_ParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"7d", 7 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"1h", time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			d, err := parseDuration(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, d)
			}
		})
	}
}

func TestManager_BuildSummary(t *testing.T) {
	mgr := &Manager{}

	entries := []TraceEntry{
		{Source: "viewer1", Level: "info"},
		{Source: "viewer1", Level: "error"},
		{Source: "viewer2", Level: "info"},
		{Source: "viewer2", Level: "info"},
		{Source: "viewer2", Level: "warn"},
	}

	duration := 1500 * time.Millisecond
	summary := mgr.buildSummary(entries, duration)

	assert.Equal(t, 5, summary.TotalEntries)
	assert.Equal(t, int64(1500), summary.DurationMS)
	assert.Equal(t, 2, summary.BySource["viewer1"])
	assert.Equal(t, 3, summary.BySource["viewer2"])
	assert.Equal(t, 3, summary.ByLevel["info"])
	assert.Equal(t, 1, summary.ByLevel["error"])
	assert.Equal(t, 1, summary.ByLevel["warn"])
}

func TestManager_MergeResults(t *testing.T) {
	mgr := &Manager{}

	now := time.Now()
	results := map[string][]logs.LogEntry{
		"viewer1": {
			{Timestamp: now.Add(-3 * time.Minute), Message: "msg1", Level: logs.LevelInfo},
			{Timestamp: now.Add(-1 * time.Minute), Message: "msg3", Level: logs.LevelError},
		},
		"viewer2": {
			{Timestamp: now.Add(-2 * time.Minute), Message: "msg2", Level: logs.LevelWarn},
		},
	}

	entries := mgr.mergeResults(results)

	// Should be sorted by timestamp
	assert.Len(t, entries, 3)
	assert.Equal(t, "msg1", entries[0].Message)
	assert.Equal(t, "msg2", entries[1].Message)
	assert.Equal(t, "msg3", entries[2].Message)

	// Source should be set
	assert.Equal(t, "viewer1", entries[0].Source)
	assert.Equal(t, "viewer2", entries[1].Source)
	assert.Equal(t, "viewer1", entries[2].Source)
}

func TestManager_ReportOperations(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Trace: config.TraceConfig{
			ReportsDir: tmpDir,
			MaxAge:  "7d",
		},
	}

	mgr, err := NewManager(nil, cfg, nil)
	require.NoError(t, err)

	// Initially no reports
	reports, err := mgr.ListReports()
	require.NoError(t, err)
	assert.Empty(t, reports)

	// Save a report directly via storage
	report := &TraceReport{
		Version:   "1.0",
		Name:      "test-report",
		TraceID:   "trace123",
		Group:     "test-group",
		CreatedAt: time.Now(),
		Summary:   TraceSummary{TotalEntries: 5},
		Entries: []TraceEntry{
			{Message: "entry1"},
		},
	}
	_, err = mgr.storage.Save(report)
	require.NoError(t, err)

	// List should return the report
	reports, err = mgr.ListReports()
	require.NoError(t, err)
	assert.Len(t, reports, 1)
	assert.Equal(t, "test-report", reports[0].Name)

	// Get should return the full report
	loaded, err := mgr.GetReport("test-report")
	require.NoError(t, err)
	assert.Equal(t, "trace123", loaded.TraceID)
	assert.Len(t, loaded.Entries, 1)

	// Delete
	err = mgr.DeleteReport("test-report")
	require.NoError(t, err)

	// Should be gone
	reports, err = mgr.ListReports()
	require.NoError(t, err)
	assert.Empty(t, reports)
}

func TestManager_CleanupOldReports(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Trace: config.TraceConfig{
			ReportsDir: tmpDir,
			MaxAge:  "24h",
		},
	}

	mgr, err := NewManager(nil, cfg, nil)
	require.NoError(t, err)

	now := time.Now()

	// Create old and new reports
	oldReport := &TraceReport{
		Version:   "1.0",
		Name:      "old",
		CreatedAt: now.Add(-48 * time.Hour),
	}
	newReport := &TraceReport{
		Version:   "1.0",
		Name:      "new",
		CreatedAt: now,
	}

	_, err = mgr.storage.Save(oldReport)
	require.NoError(t, err)
	_, err = mgr.storage.Save(newReport)
	require.NoError(t, err)

	// Run cleanup
	err = mgr.CleanupOldReports()
	require.NoError(t, err)

	// Only new report should remain
	reports, err := mgr.ListReports()
	require.NoError(t, err)
	assert.Len(t, reports, 1)
	assert.Equal(t, "new", reports[0].Name)
}

func TestManager_DefaultConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Test with empty config values
	cfg := &config.Config{
		Trace: config.TraceConfig{
			ReportsDir: tmpDir,
			// Retention not set - should default
		},
	}

	mgr, err := NewManager(nil, cfg, nil)
	require.NoError(t, err)

	// Retention should default to 7 days
	assert.Equal(t, 7*24*time.Hour, mgr.retention)
}

func TestManager_InvalidMaxAge(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Trace: config.TraceConfig{
			ReportsDir: tmpDir,
			MaxAge:     "invalid",
		},
	}

	_, err := NewManager(nil, cfg, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid max_age duration")
}

func TestManager_ExtractIDs(t *testing.T) {
	mgr := &Manager{
		logViewerConfig: map[string]config.LogViewerConfig{
			"api-logs": {Name: "api-logs", Parser: config.LogParserConfig{ID: "request_id"}},
			"db-logs":  {Name: "db-logs", Parser: config.LogParserConfig{ID: "req_id"}},
			"no-id":    {Name: "no-id"}, // No ID field configured
		},
	}

	entries := []TraceEntry{
		{
			Source:  "api-logs",
			Message: "Request started",
			Fields:  map[string]any{"request_id": "req-001", "user": "alice"},
		},
		{
			Source:  "api-logs",
			Message: "Request completed",
			Fields:  map[string]any{"request_id": "req-002", "user": "bob"},
		},
		{
			Source:  "api-logs",
			Message: "Another request",
			Fields:  map[string]any{"request_id": "req-001"}, // Duplicate ID
		},
		{
			Source:  "db-logs",
			Message: "Query executed",
			Fields:  map[string]any{"req_id": "req-003"},
		},
		{
			Source:  "no-id",
			Message: "No ID configured",
			Fields:  map[string]any{"some_field": "value"},
		},
		{
			Source:  "unknown",
			Message: "Unknown viewer",
			Fields:  map[string]any{"request_id": "req-999"},
		},
	}

	ids := mgr.extractIDs(entries)

	// Should extract unique IDs from configured viewers only
	assert.Len(t, ids, 3) // req-001, req-002, req-003

	// Convert to map for easier checking
	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}
	assert.True(t, idSet["req-001"])
	assert.True(t, idSet["req-002"])
	assert.True(t, idSet["req-003"])
	assert.False(t, idSet["req-999"]) // Unknown viewer
}

func TestManager_ExtractIDs_EmptyFields(t *testing.T) {
	mgr := &Manager{
		logViewerConfig: map[string]config.LogViewerConfig{
			"api-logs": {Name: "api-logs", Parser: config.LogParserConfig{ID: "request_id"}},
		},
	}

	entries := []TraceEntry{
		{
			Source:  "api-logs",
			Message: "No fields",
			Fields:  nil,
		},
		{
			Source:  "api-logs",
			Message: "Empty ID",
			Fields:  map[string]any{"request_id": ""},
		},
		{
			Source:  "api-logs",
			Message: "Wrong type",
			Fields:  map[string]any{"request_id": 12345},
		},
		{
			Source:  "api-logs",
			Message: "Missing ID field",
			Fields:  map[string]any{"other_field": "value"},
		},
	}

	ids := mgr.extractIDs(entries)
	assert.Empty(t, ids)
}

func TestManager_BuildIDPattern(t *testing.T) {
	mgr := &Manager{}

	tests := []struct {
		name     string
		ids      []string
		expected string
	}{
		{
			name:     "empty",
			ids:      []string{},
			expected: "",
		},
		{
			name:     "single ID",
			ids:      []string{"req-001"},
			expected: "req-001",
		},
		{
			name:     "multiple IDs",
			ids:      []string{"req-001", "req-002", "req-003"},
			expected: "(req-001|req-002|req-003)",
		},
		{
			name:     "IDs with special regex chars",
			ids:      []string{"req.001", "req*002", "req[003]"},
			expected: "(req\\.001|req\\*002|req\\[003\\])",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern := mgr.buildIDPattern(tt.ids)
			assert.Equal(t, tt.expected, pattern)
		})
	}
}

func TestRegexEscape(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"with.dot", "with\\.dot"},
		{"with*star", "with\\*star"},
		{"with+plus", "with\\+plus"},
		{"with?question", "with\\?question"},
		{"with(parens)", "with\\(parens\\)"},
		{"with[brackets]", "with\\[brackets\\]"},
		{"with{braces}", "with\\{braces\\}"},
		{"with^caret", "with\\^caret"},
		{"with$dollar", "with\\$dollar"},
		{"with|pipe", "with\\|pipe"},
		{"with\\backslash", "with\\\\backslash"},
		{"complex.test*[123]", "complex\\.test\\*\\[123\\]"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := regexEscape(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManager_DeduplicateEntries(t *testing.T) {
	mgr := &Manager{}

	now := time.Now()
	entries := []TraceEntry{
		{
			Timestamp: now.Add(-3 * time.Minute),
			Source:    "viewer1",
			Message:   "First",
			Raw:       "raw line 1",
		},
		{
			Timestamp: now.Add(-2 * time.Minute),
			Source:    "viewer2",
			Message:   "Second",
			Raw:       "raw line 2",
		},
		{
			Timestamp: now.Add(-1 * time.Minute),
			Source:    "viewer1",
			Message:   "First duplicate", // Same source + raw = duplicate
			Raw:       "raw line 1",
		},
		{
			Timestamp: now,
			Source:    "viewer2",
			Message:   "Same raw different source", // Different source = not duplicate
			Raw:       "raw line 1",
		},
	}

	result := mgr.deduplicateEntries(entries)

	// Should have 3 entries (1 duplicate removed)
	assert.Len(t, result, 3)

	// Should be sorted by timestamp
	assert.Equal(t, "raw line 1", result[0].Raw)
	assert.Equal(t, "viewer1", result[0].Source)
	assert.Equal(t, "raw line 2", result[1].Raw)
	assert.Equal(t, "raw line 1", result[2].Raw)
	assert.Equal(t, "viewer2", result[2].Source) // Different source
}

func TestManager_DeduplicateEntries_Empty(t *testing.T) {
	mgr := &Manager{}

	result := mgr.deduplicateEntries([]TraceEntry{})
	assert.Empty(t, result)
}

func TestManager_LogViewerConfigLoaded(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Trace: config.TraceConfig{
			ReportsDir: tmpDir,
		},
		LogViewers: []config.LogViewerConfig{
			{Name: "api-logs", Parser: config.LogParserConfig{ID: "request_id"}},
			{Name: "db-logs", Parser: config.LogParserConfig{ID: "correlation_id"}},
			{Name: "nginx-logs"}, // No ID
		},
	}

	mgr, err := NewManager(nil, cfg, nil)
	require.NoError(t, err)

	// Verify configs are loaded
	assert.Len(t, mgr.logViewerConfig, 3)
	assert.Equal(t, "request_id", mgr.logViewerConfig["api-logs"].Parser.ID)
	assert.Equal(t, "correlation_id", mgr.logViewerConfig["db-logs"].Parser.ID)
	assert.Equal(t, "", mgr.logViewerConfig["nginx-logs"].Parser.ID)
}
