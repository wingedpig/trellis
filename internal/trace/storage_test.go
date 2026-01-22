// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorage_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewStorage(tmpDir)
	require.NoError(t, err)

	now := time.Now().Truncate(time.Second) // Truncate for JSON round-trip

	report := &TraceReport{
		Version:   "1.0",
		Name:      "test-report",
		TraceID:   "abc123",
		Group:     "test-group",
		CreatedAt: now,
		TimeRange: TimeRange{
			Start: now.Add(-1 * time.Hour),
			End:   now,
		},
		Summary: TraceSummary{
			TotalEntries: 10,
			BySource:     map[string]int{"viewer1": 5, "viewer2": 5},
			ByLevel:      map[string]int{"info": 8, "error": 2},
			DurationMS:   1234,
		},
		Entries: []TraceEntry{
			{
				Timestamp: now.Add(-30 * time.Minute),
				Source:    "viewer1",
				Level:     "info",
				Message:   "test message 1",
				Raw:       "raw 1",
				IsContext: false,
			},
			{
				Timestamp: now.Add(-15 * time.Minute),
				Source:    "viewer2",
				Level:     "error",
				Message:   "test message 2",
				Raw:       "raw 2",
				IsContext: true,
			},
		},
	}

	// Save
	path, err := storage.Save(report)
	require.NoError(t, err)
	assert.Contains(t, path, "test-report.json")
	assert.FileExists(t, path)

	// Load
	loaded, err := storage.Load("test-report")
	require.NoError(t, err)
	assert.Equal(t, report.Version, loaded.Version)
	assert.Equal(t, report.Name, loaded.Name)
	assert.Equal(t, report.TraceID, loaded.TraceID)
	assert.Equal(t, report.Group, loaded.Group)
	assert.Equal(t, report.Summary.TotalEntries, loaded.Summary.TotalEntries)
	assert.Len(t, loaded.Entries, 2)
	assert.Equal(t, report.Entries[0].Message, loaded.Entries[0].Message)
}

func TestStorage_LoadNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewStorage(tmpDir)
	require.NoError(t, err)

	_, err = storage.Load("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "report not found")
}

func TestStorage_List(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewStorage(tmpDir)
	require.NoError(t, err)

	// Empty list
	summaries, err := storage.List()
	require.NoError(t, err)
	assert.Empty(t, summaries)

	// Add reports
	now := time.Now().Truncate(time.Second)
	reports := []*TraceReport{
		{
			Version:   "1.0",
			Name:      "report-1",
			TraceID:   "id1",
			Group:     "group1",
			CreatedAt: now,
			Entries:   []TraceEntry{{Message: "e1"}, {Message: "e2"}},
		},
		{
			Version:   "1.0",
			Name:      "report-2",
			TraceID:   "id2",
			Group:     "group2",
			CreatedAt: now.Add(-1 * time.Hour),
			Entries:   []TraceEntry{{Message: "e1"}},
		},
	}

	for _, r := range reports {
		_, err := storage.Save(r)
		require.NoError(t, err)
	}

	// List
	summaries, err = storage.List()
	require.NoError(t, err)
	assert.Len(t, summaries, 2)

	// Check summaries contain correct data
	names := make(map[string]bool)
	for _, s := range summaries {
		names[s.Name] = true
		if s.Name == "report-1" {
			assert.Equal(t, "id1", s.TraceID)
			assert.Equal(t, "group1", s.Group)
			assert.Equal(t, 2, s.EntryCount)
		}
	}
	assert.True(t, names["report-1"])
	assert.True(t, names["report-2"])
}

func TestStorage_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewStorage(tmpDir)
	require.NoError(t, err)

	// Save a report
	report := &TraceReport{
		Version: "1.0",
		Name:    "to-delete",
		TraceID: "xyz",
	}
	_, err = storage.Save(report)
	require.NoError(t, err)

	// Verify it exists
	_, err = storage.Load("to-delete")
	require.NoError(t, err)

	// Delete
	err = storage.Delete("to-delete")
	require.NoError(t, err)

	// Verify it's gone
	_, err = storage.Load("to-delete")
	assert.Error(t, err)
}

func TestStorage_DeleteNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewStorage(tmpDir)
	require.NoError(t, err)

	err = storage.Delete("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "report not found")
}

func TestStorage_DeleteOlderThan(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewStorage(tmpDir)
	require.NoError(t, err)

	now := time.Now().Truncate(time.Second)

	// Create old and new reports
	oldReport := &TraceReport{
		Version:   "1.0",
		Name:      "old-report",
		CreatedAt: now.Add(-48 * time.Hour),
	}
	newReport := &TraceReport{
		Version:   "1.0",
		Name:      "new-report",
		CreatedAt: now,
	}

	_, err = storage.Save(oldReport)
	require.NoError(t, err)
	_, err = storage.Save(newReport)
	require.NoError(t, err)

	// Delete reports older than 24 hours
	cutoff := now.Add(-24 * time.Hour)
	err = storage.DeleteOlderThan(cutoff)
	require.NoError(t, err)

	// Old report should be gone
	_, err = storage.Load("old-report")
	assert.Error(t, err)

	// New report should still exist
	_, err = storage.Load("new-report")
	require.NoError(t, err)
}

func TestStorage_PathSanitization(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewStorage(tmpDir)
	require.NoError(t, err)

	// Names with path traversal attempts should be sanitized
	report := &TraceReport{
		Version: "1.0",
		Name:    "../../../etc/passwd",
	}

	path, err := storage.Save(report)
	require.NoError(t, err)

	// Should be saved in the reports dir, not outside
	assert.True(t, filepath.HasPrefix(path, tmpDir))
	assert.NotContains(t, path, "..")
}

func TestStorage_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	newDir := filepath.Join(tmpDir, "traces", "reports")

	// Directory doesn't exist yet
	_, err := os.Stat(newDir)
	assert.True(t, os.IsNotExist(err))

	// Creating storage should create the directory
	_, err = NewStorage(newDir)
	require.NoError(t, err)

	// Directory should now exist
	info, err := os.Stat(newDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestStorage_SkipsNonJSONFiles(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewStorage(tmpDir)
	require.NoError(t, err)

	// Create a non-JSON file
	err = os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("hello"), 0644)
	require.NoError(t, err)

	// Create a valid report
	report := &TraceReport{Version: "1.0", Name: "valid"}
	_, err = storage.Save(report)
	require.NoError(t, err)

	// List should only return the valid report
	summaries, err := storage.List()
	require.NoError(t, err)
	assert.Len(t, summaries, 1)
	assert.Equal(t, "valid", summaries[0].Name)
}
