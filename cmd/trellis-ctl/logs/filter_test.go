// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"testing"
	"time"
)

func TestFilterMatch(t *testing.T) {
	now := time.Now()
	hourAgo := now.Add(-time.Hour)
	twoHoursAgo := now.Add(-2 * time.Hour)

	baseEntry := LogEntry{
		Timestamp: hourAgo,
		Level:     "INFO",
		Message:   "User logged in successfully",
		Raw:       "2024-01-15 10:30:00 INFO User logged in successfully",
		Source:    "auth-service",
		Fields: map[string]interface{}{
			"user_id": "12345",
			"host":    "prod1",
			"status":  200,
		},
	}

	tests := []struct {
		name    string
		entry   LogEntry
		opts    FilterOptions
		want    bool
		wantErr bool
	}{
		// Time filtering
		{
			name:  "no filters - match all",
			entry: baseEntry,
			opts:  FilterOptions{MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "since - entry after cutoff",
			entry: baseEntry,
			opts:  FilterOptions{Since: twoHoursAgo, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "since - entry before cutoff",
			entry: baseEntry,
			opts:  FilterOptions{Since: now.Add(-30 * time.Minute), MinLevel: LevelUnset},
			want:  false,
		},
		{
			name:  "until - entry before cutoff",
			entry: baseEntry,
			opts:  FilterOptions{Until: now, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "until - entry after cutoff",
			entry: baseEntry,
			opts:  FilterOptions{Until: twoHoursAgo, MinLevel: LevelUnset},
			want:  false,
		},
		{
			name:  "since and until - entry in range",
			entry: baseEntry,
			opts:  FilterOptions{Since: twoHoursAgo, Until: now, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "since and until - entry before range",
			entry: LogEntry{Timestamp: now.Add(-3 * time.Hour), Level: "INFO", Message: "Old entry"},
			opts:  FilterOptions{Since: twoHoursAgo, Until: now, MinLevel: LevelUnset},
			want:  false,
		},

		// Level filtering - specific levels
		{
			name:  "level filter - match",
			entry: baseEntry,
			opts:  FilterOptions{Levels: []LogLevel{LevelInfo}, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "level filter - no match",
			entry: baseEntry,
			opts:  FilterOptions{Levels: []LogLevel{LevelError}, MinLevel: LevelUnset},
			want:  false,
		},
		{
			name:  "level filter - multiple levels match",
			entry: baseEntry,
			opts:  FilterOptions{Levels: []LogLevel{LevelWarn, LevelInfo, LevelError}, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name: "level filter - error entry",
			entry: LogEntry{
				Timestamp: hourAgo,
				Level:     "ERROR",
				Message:   "Connection failed",
			},
			opts: FilterOptions{Levels: []LogLevel{LevelError}, MinLevel: LevelUnset},
			want: true,
		},

		// Level filtering - minimum level
		{
			name:  "min level - info+ matches info",
			entry: baseEntry,
			opts:  FilterOptions{MinLevel: LevelInfo},
			want:  true,
		},
		{
			name:  "min level - warn+ doesn't match info",
			entry: baseEntry,
			opts:  FilterOptions{MinLevel: LevelWarn},
			want:  false,
		},
		{
			name: "min level - warn+ matches error",
			entry: LogEntry{
				Timestamp: hourAgo,
				Level:     "ERROR",
				Message:   "Error occurred",
			},
			opts: FilterOptions{MinLevel: LevelWarn},
			want: true,
		},

		// Grep filtering
		{
			name:  "grep - match in message",
			entry: baseEntry,
			opts:  FilterOptions{GrepPattern: "logged in", MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "grep - no match",
			entry: baseEntry,
			opts:  FilterOptions{GrepPattern: "failed", MinLevel: LevelUnset},
			want:  false,
		},
		{
			name:  "grep - match in raw",
			entry: baseEntry,
			opts:  FilterOptions{GrepPattern: "2024-01-15", MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "grep - regex pattern",
			entry: baseEntry,
			opts:  FilterOptions{GrepPattern: "User.*successfully", MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "grep - case sensitive",
			entry: baseEntry,
			opts:  FilterOptions{GrepPattern: "user logged", MinLevel: LevelUnset},
			want:  false, // lowercase 'u' won't match 'User'
		},
		{
			name:  "grep - case insensitive regex",
			entry: baseEntry,
			opts:  FilterOptions{GrepPattern: "(?i)user logged", MinLevel: LevelUnset},
			want:  true,
		},

		// Field filtering
		{
			name:  "field filter - exact match",
			entry: baseEntry,
			opts:  FilterOptions{FieldFilters: map[string]string{"host": "prod1"}, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "field filter - no match",
			entry: baseEntry,
			opts:  FilterOptions{FieldFilters: map[string]string{"host": "prod2"}, MinLevel: LevelUnset},
			want:  false,
		},
		{
			name:  "field filter - numeric value",
			entry: baseEntry,
			opts:  FilterOptions{FieldFilters: map[string]string{"status": "200"}, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "field filter - built-in level field",
			entry: baseEntry,
			opts:  FilterOptions{FieldFilters: map[string]string{"level": "INFO"}, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "field filter - built-in source field",
			entry: baseEntry,
			opts:  FilterOptions{FieldFilters: map[string]string{"source": "auth-service"}, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "field filter - missing field",
			entry: baseEntry,
			opts:  FilterOptions{FieldFilters: map[string]string{"nonexistent": "value"}, MinLevel: LevelUnset},
			want:  false,
		},
		{
			name:  "field filter - wildcard",
			entry: baseEntry,
			opts:  FilterOptions{FieldFilters: map[string]string{"host": "prod*"}, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "field filter - multiple fields all match",
			entry: baseEntry,
			opts:  FilterOptions{FieldFilters: map[string]string{"host": "prod1", "status": "200"}, MinLevel: LevelUnset},
			want:  true,
		},
		{
			name:  "field filter - multiple fields one fails",
			entry: baseEntry,
			opts:  FilterOptions{FieldFilters: map[string]string{"host": "prod1", "status": "500"}, MinLevel: LevelUnset},
			want:  false,
		},

		// Combined filters
		{
			name:  "combined - all match",
			entry: baseEntry,
			opts: FilterOptions{
				Since:        twoHoursAgo,
				Levels:       []LogLevel{LevelInfo},
				MinLevel:     LevelUnset,
				GrepPattern:  "logged",
				FieldFilters: map[string]string{"host": "prod1"},
			},
			want: true,
		},
		{
			name:  "combined - time fails",
			entry: baseEntry,
			opts: FilterOptions{
				Since:        now.Add(-30 * time.Minute),
				Levels:       []LogLevel{LevelInfo},
				MinLevel:     LevelUnset,
				GrepPattern:  "logged",
				FieldFilters: map[string]string{"host": "prod1"},
			},
			want: false,
		},
		{
			name:  "combined - level fails",
			entry: baseEntry,
			opts: FilterOptions{
				Since:        twoHoursAgo,
				Levels:       []LogLevel{LevelError},
				MinLevel:     LevelUnset,
				GrepPattern:  "logged",
				FieldFilters: map[string]string{"host": "prod1"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := NewFilter(tt.opts)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error creating filter: %v", err)
				return
			}

			got := filter.Match(&tt.entry)
			if got != tt.want {
				t.Errorf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterInvalidGrepPattern(t *testing.T) {
	opts := FilterOptions{
		GrepPattern: "[invalid(regex",
	}

	_, err := NewFilter(opts)
	if err == nil {
		t.Error("expected error for invalid regex pattern")
	}
}

func TestFilterEntries(t *testing.T) {
	now := time.Now()

	entries := []LogEntry{
		{Timestamp: now.Add(-3 * time.Hour), Level: "DEBUG", Message: "Starting up"},
		{Timestamp: now.Add(-2 * time.Hour), Level: "INFO", Message: "Server ready"},
		{Timestamp: now.Add(-1 * time.Hour), Level: "WARN", Message: "High memory usage"},
		{Timestamp: now.Add(-30 * time.Minute), Level: "ERROR", Message: "Connection failed"},
		{Timestamp: now.Add(-10 * time.Minute), Level: "INFO", Message: "Reconnected"},
	}

	tests := []struct {
		name      string
		opts      FilterOptions
		wantCount int
	}{
		{
			name:      "no filter",
			opts:      FilterOptions{MinLevel: LevelUnset},
			wantCount: 5,
		},
		{
			name:      "level error only",
			opts:      FilterOptions{Levels: []LogLevel{LevelError}, MinLevel: LevelUnset},
			wantCount: 1,
		},
		{
			name:      "level warn+",
			opts:      FilterOptions{MinLevel: LevelWarn},
			wantCount: 2, // WARN and ERROR
		},
		{
			name:      "since 90 minutes ago",
			opts:      FilterOptions{Since: now.Add(-90 * time.Minute), MinLevel: LevelUnset},
			wantCount: 3,
		},
		{
			name:      "grep connection",
			opts:      FilterOptions{GrepPattern: "(?i)connect", MinLevel: LevelUnset},
			wantCount: 2, // "Connection failed" and "Reconnected"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FilterEntries(entries, tt.opts)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if len(result) != tt.wantCount {
				t.Errorf("FilterEntries() returned %d entries, want %d", len(result), tt.wantCount)
			}
		})
	}
}

func TestMatchFieldValueWildcard(t *testing.T) {
	filter, _ := NewFilter(FilterOptions{})

	tests := []struct {
		actual   string
		expected string
		want     bool
	}{
		{"prod1", "prod1", true},
		{"prod1", "prod2", false},
		{"prod1", "prod*", true},
		{"production", "prod*", true},
		{"dev", "prod*", false},
		{"host-001", "*-001", true},
		{"host-002", "*-001", false},
		{"test-host-123", "*host*", true},
		{"PROD1", "prod1", true}, // case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.actual+"_"+tt.expected, func(t *testing.T) {
			got := filter.matchFieldValue(tt.actual, tt.expected)
			if got != tt.want {
				t.Errorf("matchFieldValue(%q, %q) = %v, want %v", tt.actual, tt.expected, got, tt.want)
			}
		})
	}
}

func TestGetLevelFromEntry(t *testing.T) {
	tests := []struct {
		entry LogEntry
		want  LogLevel
	}{
		{LogEntry{Level: "INFO"}, LevelInfo},
		{LogEntry{Level: "info"}, LevelInfo},
		{LogEntry{Level: "ERROR"}, LevelError},
		{LogEntry{Level: "warn"}, LevelWarn},
		{LogEntry{Level: ""}, LevelUnknown},
		{LogEntry{Level: "invalid"}, LevelUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.entry.Level, func(t *testing.T) {
			got := GetLevelFromEntry(&tt.entry)
			if got != tt.want {
				t.Errorf("GetLevelFromEntry() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterEntriesWithContext(t *testing.T) {
	now := time.Now()

	// Create 10 sequential log entries
	entries := []LogEntry{
		{Timestamp: now.Add(-9 * time.Minute), Level: "INFO", Message: "Entry 0"},
		{Timestamp: now.Add(-8 * time.Minute), Level: "INFO", Message: "Entry 1"},
		{Timestamp: now.Add(-7 * time.Minute), Level: "INFO", Message: "Entry 2"},
		{Timestamp: now.Add(-6 * time.Minute), Level: "INFO", Message: "Entry 3"},
		{Timestamp: now.Add(-5 * time.Minute), Level: "ERROR", Message: "Error occurred here"}, // index 4
		{Timestamp: now.Add(-4 * time.Minute), Level: "INFO", Message: "Entry 5"},
		{Timestamp: now.Add(-3 * time.Minute), Level: "INFO", Message: "Entry 6"},
		{Timestamp: now.Add(-2 * time.Minute), Level: "INFO", Message: "Entry 7"},
		{Timestamp: now.Add(-1 * time.Minute), Level: "INFO", Message: "Entry 8"},
		{Timestamp: now, Level: "INFO", Message: "Entry 9"},
	}

	tests := []struct {
		name         string
		opts         FilterOptions
		wantCount    int
		wantMessages []string
	}{
		{
			name: "grep without context",
			opts: FilterOptions{
				GrepPattern: "Error occurred",
				MinLevel:    LevelUnset,
			},
			wantCount:    1,
			wantMessages: []string{"Error occurred here"},
		},
		{
			name: "grep with -B 2 (2 lines before)",
			opts: FilterOptions{
				GrepPattern: "Error occurred",
				MinLevel:    LevelUnset,
				Before:      2,
			},
			wantCount:    3, // Entry 2, Entry 3, Error
			wantMessages: []string{"Entry 2", "Entry 3", "Error occurred here"},
		},
		{
			name: "grep with -A 2 (2 lines after)",
			opts: FilterOptions{
				GrepPattern: "Error occurred",
				MinLevel:    LevelUnset,
				After:       2,
			},
			wantCount:    3, // Error, Entry 5, Entry 6
			wantMessages: []string{"Error occurred here", "Entry 5", "Entry 6"},
		},
		{
			name: "grep with -C 2 (2 lines before and after)",
			opts: FilterOptions{
				GrepPattern: "Error occurred",
				MinLevel:    LevelUnset,
				Before:      2,
				After:       2,
			},
			wantCount:    5, // Entry 2, Entry 3, Error, Entry 5, Entry 6
			wantMessages: []string{"Entry 2", "Entry 3", "Error occurred here", "Entry 5", "Entry 6"},
		},
		{
			name: "grep at start with -B 10 (more before than available)",
			opts: FilterOptions{
				GrepPattern: "Entry 0",
				MinLevel:    LevelUnset,
				Before:      10,
			},
			wantCount:    1, // Only Entry 0 (nothing before it)
			wantMessages: []string{"Entry 0"},
		},
		{
			name: "grep at end with -A 10 (more after than available)",
			opts: FilterOptions{
				GrepPattern: "Entry 9",
				MinLevel:    LevelUnset,
				After:       10,
			},
			wantCount:    1, // Only Entry 9 (nothing after it)
			wantMessages: []string{"Entry 9"},
		},
		{
			name: "no grep pattern with context - returns base filtered",
			opts: FilterOptions{
				MinLevel: LevelError,
				Before:   2,
				After:    2,
			},
			wantCount:    1, // Context doesn't apply without grep, just ERROR entry
			wantMessages: []string{"Error occurred here"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FilterEntries(entries, tt.opts)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if len(result) != tt.wantCount {
				t.Errorf("FilterEntries() returned %d entries, want %d", len(result), tt.wantCount)
				for i, e := range result {
					t.Logf("  [%d] %s", i, e.Message)
				}
				return
			}
			// Verify messages match expected
			for i, msg := range tt.wantMessages {
				if result[i].Message != msg {
					t.Errorf("entry[%d].Message = %q, want %q", i, result[i].Message, msg)
				}
			}
		})
	}
}

func TestFilterEntriesContextOverlap(t *testing.T) {
	now := time.Now()

	// Create entries with two matches close together
	entries := []LogEntry{
		{Timestamp: now.Add(-7 * time.Minute), Level: "INFO", Message: "Entry 0"},
		{Timestamp: now.Add(-6 * time.Minute), Level: "INFO", Message: "Entry 1"},
		{Timestamp: now.Add(-5 * time.Minute), Level: "ERROR", Message: "First error"},  // match
		{Timestamp: now.Add(-4 * time.Minute), Level: "INFO", Message: "Entry 3"},
		{Timestamp: now.Add(-3 * time.Minute), Level: "ERROR", Message: "Second error"}, // match
		{Timestamp: now.Add(-2 * time.Minute), Level: "INFO", Message: "Entry 5"},
		{Timestamp: now.Add(-1 * time.Minute), Level: "INFO", Message: "Entry 6"},
	}

	// With -C 1, we'd get: Entry 1, First error, Entry 3, Second error, Entry 5
	// The Entry 3 is both after first match and before second match (no duplicates)
	opts := FilterOptions{
		GrepPattern: "error",
		MinLevel:    LevelUnset,
		Before:      1,
		After:       1,
	}

	result, err := FilterEntries(entries, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be 5 unique entries, no duplicates
	if len(result) != 5 {
		t.Errorf("FilterEntries() returned %d entries, want 5", len(result))
		for i, e := range result {
			t.Logf("  [%d] %s", i, e.Message)
		}
	}

	expected := []string{"Entry 1", "First error", "Entry 3", "Second error", "Entry 5"}
	for i, msg := range expected {
		if result[i].Message != msg {
			t.Errorf("entry[%d].Message = %q, want %q", i, result[i].Message, msg)
		}
	}
}
