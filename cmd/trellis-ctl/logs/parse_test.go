// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		input     string
		wantErr   bool
		checkFunc func(t *testing.T, result time.Time)
	}{
		// Relative durations
		{
			name:  "seconds",
			input: "30s",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := now.Add(-30 * time.Second)
				diff := result.Sub(expected)
				if diff < -time.Second || diff > time.Second {
					t.Errorf("expected ~%v ago, got %v ago", 30*time.Second, now.Sub(result))
				}
			},
		},
		{
			name:  "minutes",
			input: "5m",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := now.Add(-5 * time.Minute)
				diff := result.Sub(expected)
				if diff < -time.Second || diff > time.Second {
					t.Errorf("expected ~%v ago, got %v ago", 5*time.Minute, now.Sub(result))
				}
			},
		},
		{
			name:  "hours",
			input: "2h",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := now.Add(-2 * time.Hour)
				diff := result.Sub(expected)
				if diff < -time.Second || diff > time.Second {
					t.Errorf("expected ~%v ago, got %v ago", 2*time.Hour, now.Sub(result))
				}
			},
		},
		{
			name:  "days",
			input: "3d",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := now.Add(-3 * 24 * time.Hour)
				diff := result.Sub(expected)
				if diff < -time.Second || diff > time.Second {
					t.Errorf("expected ~%v ago, got %v ago", 3*24*time.Hour, now.Sub(result))
				}
			},
		},
		{
			name:  "weeks",
			input: "1w",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := now.Add(-7 * 24 * time.Hour)
				diff := result.Sub(expected)
				if diff < -time.Second || diff > time.Second {
					t.Errorf("expected ~%v ago, got %v ago", 7*24*time.Hour, now.Sub(result))
				}
			},
		},
		// ISO timestamps
		{
			name:  "RFC3339 timestamp",
			input: "2024-01-15T10:30:00Z",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
				if !result.Equal(expected) {
					t.Errorf("expected %v, got %v", expected, result)
				}
			},
		},
		{
			name:  "timestamp without timezone",
			input: "2024-01-15T10:30:00",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
				if !result.Equal(expected) {
					t.Errorf("expected %v, got %v", expected, result)
				}
			},
		},
		{
			name:  "date only",
			input: "2024-01-15",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
				if !result.Equal(expected) {
					t.Errorf("expected %v, got %v", expected, result)
				}
			},
		},
		// Error cases
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid format",
			input:   "abc",
			wantErr: true,
		},
		{
			name:    "missing unit",
			input:   "123",
			wantErr: true,
		},
		{
			name:    "invalid unit",
			input:   "5x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input   string
		want    LogLevel
		wantErr bool
	}{
		// Valid levels
		{"trace", LevelTrace, false},
		{"TRACE", LevelTrace, false},
		{"debug", LevelDebug, false},
		{"DEBUG", LevelDebug, false},
		{"info", LevelInfo, false},
		{"INFO", LevelInfo, false},
		{"warn", LevelWarn, false},
		{"WARN", LevelWarn, false},
		{"warning", LevelWarn, false},
		{"WARNING", LevelWarn, false},
		{"error", LevelError, false},
		{"ERROR", LevelError, false},
		{"err", LevelError, false},
		{"ERR", LevelError, false},
		{"fatal", LevelFatal, false},
		{"FATAL", LevelFatal, false},
		{"critical", LevelFatal, false},
		{"CRITICAL", LevelFatal, false},
		{"crit", LevelFatal, false},
		// With whitespace
		{"  info  ", LevelInfo, false},
		// Invalid levels
		{"unknown", LevelUnknown, true},
		{"verbose", LevelUnknown, true},
		{"", LevelUnknown, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseLevel(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for input %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for input %q: %v", tt.input, err)
				return
			}
			if result != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, result, tt.want)
			}
		})
	}
}

func TestParseLevelFilter(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantLevels  []LogLevel
		wantMinLevel LogLevel
		wantErr     bool
	}{
		{
			name:        "empty",
			input:       "",
			wantLevels:  nil,
			wantMinLevel: LevelUnset,
		},
		{
			name:        "single level",
			input:       "error",
			wantLevels:  []LogLevel{LevelError},
			wantMinLevel: LevelUnset,
		},
		{
			name:        "multiple levels",
			input:       "warn,error",
			wantLevels:  []LogLevel{LevelWarn, LevelError},
			wantMinLevel: LevelUnset,
		},
		{
			name:        "multiple levels with spaces",
			input:       "warn, error, fatal",
			wantLevels:  []LogLevel{LevelWarn, LevelError, LevelFatal},
			wantMinLevel: LevelUnset,
		},
		{
			name:        "min level info+",
			input:       "info+",
			wantLevels:  nil,
			wantMinLevel: LevelInfo,
		},
		{
			name:        "min level warn+",
			input:       "warn+",
			wantLevels:  nil,
			wantMinLevel: LevelWarn,
		},
		{
			name:        "min level error+",
			input:       "error+",
			wantLevels:  nil,
			wantMinLevel: LevelError,
		},
		{
			name:    "invalid level",
			input:   "invalid",
			wantErr: true,
		},
		{
			name:    "invalid min level",
			input:   "invalid+",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			levels, minLevel, err := ParseLevelFilter(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if len(levels) != len(tt.wantLevels) {
				t.Errorf("got %d levels, want %d", len(levels), len(tt.wantLevels))
				return
			}
			for i, l := range levels {
				if l != tt.wantLevels[i] {
					t.Errorf("level[%d] = %v, want %v", i, l, tt.wantLevels[i])
				}
			}
			if minLevel != tt.wantMinLevel {
				t.Errorf("minLevel = %v, want %v", minLevel, tt.wantMinLevel)
			}
		})
	}
}

func TestParseFieldFilter(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantField string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "simple field=value",
			input:     "host=prod1",
			wantField: "host",
			wantValue: "prod1",
		},
		{
			name:      "numeric value",
			input:     "status=500",
			wantField: "status",
			wantValue: "500",
		},
		{
			name:      "value with equals",
			input:     "query=a=b",
			wantField: "query",
			wantValue: "a=b",
		},
		{
			name:      "with spaces",
			input:     "  host=prod1  ",
			wantField: "host",
			wantValue: "prod1",
		},
		{
			name:    "no equals",
			input:   "hostprod1",
			wantErr: true,
		},
		{
			name:    "empty field",
			input:   "=value",
			wantErr: true,
		},
		{
			name:      "empty value",
			input:     "field=",
			wantField: "field",
			wantValue: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, value, err := ParseFieldFilter(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if field != tt.wantField {
				t.Errorf("field = %q, want %q", field, tt.wantField)
			}
			if value != tt.wantValue {
				t.Errorf("value = %q, want %q", value, tt.wantValue)
			}
		})
	}
}

func TestParseOutputFormat(t *testing.T) {
	tests := []struct {
		input   string
		want    OutputFormat
		wantErr bool
	}{
		{"", FormatPlain, false},
		{"plain", FormatPlain, false},
		{"text", FormatPlain, false},
		{"json", FormatJSON, false},
		{"JSON", FormatJSON, false},
		{"jsonl", FormatJSONL, false},
		{"JSONL", FormatJSONL, false},
		{"ndjson", FormatJSONL, false},
		{"csv", FormatCSV, false},
		{"CSV", FormatCSV, false},
		{"raw", FormatRaw, false},
		{"RAW", FormatRaw, false},
		{"invalid", FormatPlain, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseOutputFormat(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for input %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result != tt.want {
				t.Errorf("ParseOutputFormat(%q) = %v, want %v", tt.input, result, tt.want)
			}
		})
	}
}

func TestLogLevelString(t *testing.T) {
	tests := []struct {
		level LogLevel
		want  string
	}{
		{LevelTrace, "TRACE"},
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
		{LevelFatal, "FATAL"},
		{LevelUnknown, "UNKNOWN"},
		{LogLevel(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.level.String(); got != tt.want {
				t.Errorf("LogLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}
