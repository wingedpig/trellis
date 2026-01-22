// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package crashes

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/logs"
)

func TestManager_SaveAndGet(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(Config{ReportsDir: dir}, nil, nil, nil, "id", nil, "")
	require.NoError(t, err)

	crash := Crash{
		Version:   "1.0",
		ID:        "20240101-120000.000",
		Service:   "api",
		Timestamp: time.Now(),
		TraceID:   "abc123",
		ExitCode:  1,
		Error:     "segfault",
		Worktree: WorktreeInfo{
			Name:   "main",
			Branch: "main",
			Path:   "/project",
		},
		Summary: CrashStats{
			TotalEntries: 2,
			BySource:     map[string]int{"api": 2},
			ByLevel:      map[string]int{"info": 2},
		},
		Entries: []CrashEntry{
			{Timestamp: time.Now(), Source: "api", Level: "info", Message: "line1", Raw: "line1"},
			{Timestamp: time.Now(), Source: "api", Level: "info", Message: "line2", Raw: "line2"},
		},
	}

	err = mgr.Save(crash)
	require.NoError(t, err)

	loaded, err := mgr.Get("20240101-120000.000")
	require.NoError(t, err)
	assert.Equal(t, crash.ID, loaded.ID)
	assert.Equal(t, crash.Service, loaded.Service)
	assert.Equal(t, crash.TraceID, loaded.TraceID)
	assert.Equal(t, crash.ExitCode, loaded.ExitCode)
}

func TestManager_List(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(Config{ReportsDir: dir}, nil, nil, nil, "id", nil, "")
	require.NoError(t, err)

	// Save multiple crashes
	for i := 0; i < 3; i++ {
		crash := Crash{
			ID:        time.Now().Add(time.Duration(i) * time.Second).Format("20060102-150405.000"),
			Service:   "api",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, mgr.Save(crash))
	}

	summaries, err := mgr.List()
	require.NoError(t, err)
	assert.Len(t, summaries, 3)

	// Should be sorted newest first
	assert.True(t, summaries[0].Timestamp.After(summaries[1].Timestamp))
	assert.True(t, summaries[1].Timestamp.After(summaries[2].Timestamp))
}

func TestManager_Newest(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(Config{ReportsDir: dir}, nil, nil, nil, "id", nil, "")
	require.NoError(t, err)

	// Save crashes with different timestamps
	older := Crash{
		ID:        "20240101-100000.000",
		Service:   "api",
		Timestamp: time.Now().Add(-1 * time.Hour),
	}
	require.NoError(t, mgr.Save(older))

	newer := Crash{
		ID:        "20240101-120000.000",
		Service:   "worker",
		Timestamp: time.Now(),
	}
	require.NoError(t, mgr.Save(newer))

	newest, err := mgr.Newest()
	require.NoError(t, err)
	assert.Equal(t, "20240101-120000.000", newest.ID)
	assert.Equal(t, "worker", newest.Service)
}

func TestManager_Delete(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(Config{ReportsDir: dir}, nil, nil, nil, "id", nil, "")
	require.NoError(t, err)

	crash := Crash{
		ID:      "20240101-120000.000",
		Service: "api",
	}
	require.NoError(t, mgr.Save(crash))

	// Verify it exists
	_, err = mgr.Get("20240101-120000.000")
	require.NoError(t, err)

	// Delete it
	err = mgr.Delete("20240101-120000.000")
	require.NoError(t, err)

	// Verify it's gone
	_, err = mgr.Get("20240101-120000.000")
	assert.Error(t, err)

	// Deleting non-existent should error
	err = mgr.Delete("nonexistent")
	assert.Error(t, err)
}

func TestManager_Clear(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(Config{ReportsDir: dir}, nil, nil, nil, "id", nil, "")
	require.NoError(t, err)

	// Save multiple crashes
	for i := 0; i < 3; i++ {
		crash := Crash{
			ID:      time.Now().Add(time.Duration(i) * time.Second).Format("20060102-150405.000"),
			Service: "api",
		}
		require.NoError(t, mgr.Save(crash))
	}

	summaries, _ := mgr.List()
	assert.Len(t, summaries, 3)

	err = mgr.Clear()
	require.NoError(t, err)

	summaries, _ = mgr.List()
	assert.Len(t, summaries, 0)
}

func TestManager_Cleanup_MaxCount(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(Config{ReportsDir: dir, MaxCount: 2}, nil, nil, nil, "id", nil, "")
	require.NoError(t, err)

	// Save 3 crashes
	for i := 0; i < 3; i++ {
		crash := Crash{
			ID:        time.Now().Add(time.Duration(i) * time.Second).Format("20060102-150405.000"),
			Service:   "api",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, mgr.Save(crash))
		time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	}

	// Manually trigger cleanup
	mgr.cleanup()

	summaries, _ := mgr.List()
	assert.Len(t, summaries, 2)
}

func TestManager_Cleanup_MaxAge(t *testing.T) {
	dir := t.TempDir()
	// Use a 10 minute max age for testing
	mgr, err := NewManager(Config{ReportsDir: dir, MaxAge: 10 * time.Minute}, nil, nil, nil, "id", nil, "")
	require.NoError(t, err)

	// Save an old crash (older than max age)
	oldTime := time.Now().Add(-20 * time.Minute)
	oldID := oldTime.Format("20060102-150405.000")
	oldCrash := Crash{ID: oldID, Service: "api", Timestamp: oldTime}
	require.NoError(t, mgr.Save(oldCrash))

	// Save a recent crash (within max age)
	newTime := time.Now()
	newID := newTime.Format("20060102-150405.000")
	newCrash := Crash{ID: newID, Service: "api", Timestamp: newTime}
	require.NoError(t, mgr.Save(newCrash))

	// Verify both exist before cleanup
	summaries, _ := mgr.List()
	require.Len(t, summaries, 2)

	// Trigger cleanup - the old crash should be removed
	mgr.cleanup()

	summaries, _ = mgr.List()
	require.Len(t, summaries, 1)
	assert.Equal(t, newID, summaries[0].ID)
}

func TestExtractTraceID(t *testing.T) {
	tests := []struct {
		name     string
		logLine  string
		idField  string
		expected string
	}{
		{
			name:     "JSON log with id field",
			logLine:  `{"id":"abc123","msg":"hello"}`,
			idField:  "id",
			expected: "abc123",
		},
		{
			name:     "JSON log with trace_id field",
			logLine:  `{"trace_id":"xyz789","msg":"hello"}`,
			idField:  "trace_id",
			expected: "xyz789",
		},
		{
			name:     "JSON log without id field",
			logLine:  `{"msg":"hello"}`,
			idField:  "id",
			expected: "",
		},
		{
			name:     "non-JSON log with quoted id",
			logLine:  `some text "id": "abc123" more text`,
			idField:  "id",
			expected: "abc123",
		},
		{
			name:     "plain text log",
			logLine:  `just some text without any id`,
			idField:  "id",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTraceID(tt.logLine, tt.idField)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFilterEntriesByTraceID(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(Config{ReportsDir: dir}, nil, nil, nil, "id", nil, "")
	require.NoError(t, err)

	now := time.Now()
	allLogs := map[string][]*logs.LogEntry{
		"api": {
			{Timestamp: now, Message: "start", Fields: map[string]any{"id": "req-001"}},
			{Timestamp: now, Message: "other request", Fields: map[string]any{"id": "req-002"}},
			{Timestamp: now, Message: "processing", Fields: map[string]any{"id": "req-001"}},
			{Timestamp: now, Message: "crash!", Fields: map[string]any{"id": "req-001"}},
		},
		"worker": {
			{Timestamp: now, Message: "received job", Fields: map[string]any{"id": "req-001"}},
			{Timestamp: now, Message: "different job", Fields: map[string]any{"id": "req-003"}},
			{Timestamp: now, Message: "job done", Fields: map[string]any{"id": "req-001"}},
		},
		"db": {
			{Timestamp: now, Message: "no id field here", Fields: map[string]any{}},
			{Timestamp: now, Message: "db query", Fields: map[string]any{"id": "req-002"}},
		},
	}

	result := mgr.filterEntriesByTraceID(allLogs, "req-001")

	// Should only have entries matching req-001
	// 3 from api + 2 from worker = 5 total
	assert.Len(t, result, 5)

	// Count by source
	sourceCount := make(map[string]int)
	for _, e := range result {
		sourceCount[e.Source]++
	}
	assert.Equal(t, 3, sourceCount["api"])
	assert.Equal(t, 2, sourceCount["worker"])
	assert.Equal(t, 0, sourceCount["db"])
}

func TestFilterEntriesByTraceID_PerServiceIDField(t *testing.T) {
	dir := t.TempDir()
	serviceIDFields := map[string]string{
		"legacy": "request_id", // Different field name
	}
	mgr, err := NewManager(Config{ReportsDir: dir}, nil, nil, nil, "id", serviceIDFields, "")
	require.NoError(t, err)

	now := time.Now()
	allLogs := map[string][]*logs.LogEntry{
		"api": {
			{Timestamp: now, Message: "api log", Fields: map[string]any{"id": "req-001"}},
		},
		"legacy": {
			{Timestamp: now, Message: "legacy log", Fields: map[string]any{"request_id": "req-001"}},
			{Timestamp: now, Message: "wrong field", Fields: map[string]any{"id": "req-001"}}, // Has "id" but legacy uses "request_id"
		},
	}

	result := mgr.filterEntriesByTraceID(allLogs, "req-001")

	// Should have 1 from api + 1 from legacy (only the one with request_id)
	assert.Len(t, result, 2)

	// Check the entries
	var apiCount, legacyCount int
	for _, e := range result {
		if e.Source == "api" {
			apiCount++
			assert.Equal(t, "api log", e.Message)
		}
		if e.Source == "legacy" {
			legacyCount++
			assert.Equal(t, "legacy log", e.Message)
		}
	}
	assert.Equal(t, 1, apiCount)
	assert.Equal(t, 1, legacyCount)
}

func TestGetIDFieldForService(t *testing.T) {
	dir := t.TempDir()

	// Test with per-service overrides
	serviceIDFields := map[string]string{
		"api":    "request_id",
		"worker": "job_id",
	}
	mgr, err := NewManager(Config{ReportsDir: dir}, nil, nil, nil, "trace_id", serviceIDFields, "")
	require.NoError(t, err)

	// Service with override should use its ID field
	assert.Equal(t, "request_id", mgr.getIDFieldForService("api"))
	assert.Equal(t, "job_id", mgr.getIDFieldForService("worker"))

	// Service without override should use default
	assert.Equal(t, "trace_id", mgr.getIDFieldForService("other"))

	// Test with no default - falls back to "id"
	mgr2, err := NewManager(Config{ReportsDir: dir}, nil, nil, nil, "", nil, "")
	require.NoError(t, err)
	assert.Equal(t, "id", mgr2.getIDFieldForService("any"))
}

func TestFindRequestTraceIDFromEntries(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(Config{ReportsDir: dir}, nil, nil, nil, "id", nil, "")
	require.NoError(t, err)

	now := time.Now()
	tests := []struct {
		name     string
		logs     map[string][]*logs.LogEntry
		service  string
		expected string
	}{
		{
			name: "finds different trace ID before crash",
			logs: map[string][]*logs.LogEntry{
				"api": {
					{Timestamp: now, Message: "processing", Fields: map[string]any{"id": "request1"}},
					{Timestamp: now, Message: "still processing", Fields: map[string]any{"id": "request1"}},
					{Timestamp: now, Message: "crash detected", Fields: map[string]any{"id": "crash-id"}},
				},
			},
			service:  "api",
			expected: "request1",
		},
		{
			name: "returns crash ID if no different ID found",
			logs: map[string][]*logs.LogEntry{
				"api": {
					{Timestamp: now, Message: "line1", Fields: map[string]any{"id": "same-id"}},
					{Timestamp: now, Message: "line2", Fields: map[string]any{"id": "same-id"}},
					{Timestamp: now, Message: "crash", Fields: map[string]any{"id": "same-id"}},
				},
			},
			service:  "api",
			expected: "same-id",
		},
		{
			name: "handles empty logs",
			logs: map[string][]*logs.LogEntry{
				"api": {},
			},
			service:  "api",
			expected: "",
		},
		{
			name: "handles missing service",
			logs: map[string][]*logs.LogEntry{
				"other": {{Timestamp: now, Message: "msg", Fields: map[string]any{"id": "abc"}}},
			},
			service:  "api",
			expected: "",
		},
		{
			name: "finds trace ID when crash line has no ID",
			logs: map[string][]*logs.LogEntry{
				"api": {
					{Timestamp: now, Message: "processing", Fields: map[string]any{"id": "request1"}},
					{Timestamp: now, Message: "still processing", Fields: map[string]any{"id": "request1"}},
					{Timestamp: now, Message: "CRASH!", Fields: map[string]any{"stack": "panic..."}},
				},
			},
			service:  "api",
			expected: "request1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mgr.findRequestTraceIDFromEntries(tt.logs, tt.service)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManager_DirectoryCreation(t *testing.T) {
	dir := t.TempDir()
	crashDir := filepath.Join(dir, "nested", "crashes")

	_, err := NewManager(Config{ReportsDir: crashDir}, nil, nil, nil, "id", nil, "")
	require.NoError(t, err)

	// Directory should be created
	info, err := os.Stat(crashDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

