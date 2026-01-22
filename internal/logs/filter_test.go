// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"testing"
	"time"
)

func TestParseFilter(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		shouldErr bool
		isEmpty   bool
	}{
		{"empty query", "", false, true},
		{"simple field match", "level:error", false, false},
		{"or match", "level:error,warn", false, false},
		{"contains", "msg:~timeout", false, false},
		{"greater than", "duration:>100", false, false},
		{"less than", "status:<500", false, false},
		{"greater or equal", "status:>=400", false, false},
		{"less or equal", "status:<=200", false, false},
		{"negation", "-level:debug", false, false},
		{"quoted text", `"connection refused"`, false, false},
		{"multiple clauses", "level:error host:db01", false, false},
		{"timestamp relative", "ts:>-5m", false, false},
		{"unclosed quote", `"unclosed`, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := ParseFilter(tt.query)
			if (err != nil) != tt.shouldErr {
				t.Errorf("ParseFilter(%q) error = %v, shouldErr = %v", tt.query, err, tt.shouldErr)
			}
			if !tt.shouldErr && filter.IsEmpty() != tt.isEmpty {
				t.Errorf("ParseFilter(%q).IsEmpty() = %v, want %v", tt.query, filter.IsEmpty(), tt.isEmpty)
			}
		})
	}
}

func TestFilterMatch(t *testing.T) {
	baseEntry := LogEntry{
		Timestamp: time.Now(),
		Level:     LevelError,
		Message:   "Connection refused to host db01",
		Fields: map[string]any{
			"host":       "db01",
			"duration":   "150ms",
			"status":     float64(500),
			"request_id": "abc123",
		},
	}

	tests := []struct {
		name    string
		query   string
		entry   LogEntry
		matches bool
	}{
		{"level exact match", "level:error", baseEntry, true},
		{"level no match", "level:info", baseEntry, false},
		{"level or match", "level:error,warn", baseEntry, true},
		{"level or no match", "level:info,debug", baseEntry, false},
		{"field exact match", "host:db01", baseEntry, true},
		{"field no match", "host:db02", baseEntry, false},
		{"contains match", "msg:~refused", baseEntry, true},
		{"contains no match", "msg:~timeout", baseEntry, false},
		{"contains case insensitive", "msg:~REFUSED", baseEntry, true},
		{"negation match", "-level:info", baseEntry, true},
		{"negation no match", "-level:error", baseEntry, false},
		{"quoted text match", `"Connection refused"`, baseEntry, true},
		{"quoted text no match", `"timeout"`, baseEntry, false},
		{"multiple clauses all match", "level:error host:db01", baseEntry, true},
		{"multiple clauses one fails", "level:error host:db02", baseEntry, false},
		{"numeric greater than", "status:>400", baseEntry, true},
		{"numeric greater than fail", "status:>600", baseEntry, false},
		{"numeric less than", "status:<600", baseEntry, true},
		{"numeric greater or equal", "status:>=500", baseEntry, true},
		{"numeric less or equal", "status:<=500", baseEntry, true},
		{"request_id match", "request_id:abc123", baseEntry, true},
		{"unknown field no match", "unknown:value", baseEntry, false},
		{"simple word search", "refused", baseEntry, true},
		{"empty filter matches all", "", baseEntry, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := ParseFilter(tt.query)
			if err != nil {
				t.Fatalf("ParseFilter(%q) error: %v", tt.query, err)
			}
			result := filter.Match(tt.entry)
			if result != tt.matches {
				t.Errorf("Filter(%q).Match() = %v, want %v", tt.query, result, tt.matches)
			}
		})
	}
}

func TestFilterTimestamp(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		query   string
		time    time.Time
		matches bool
	}{
		{"relative greater than (recent)", "ts:>-1h", now.Add(-30 * time.Minute), true},
		{"relative greater than (old)", "ts:>-1h", now.Add(-2 * time.Hour), false},
		{"relative less than (old)", "ts:<-1h", now.Add(-2 * time.Hour), true},
		{"relative less than (recent)", "ts:<-1h", now.Add(-30 * time.Minute), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := ParseFilter(tt.query)
			if err != nil {
				t.Fatalf("ParseFilter(%q) error: %v", tt.query, err)
			}
			entry := LogEntry{Timestamp: tt.time}
			result := filter.Match(entry)
			if result != tt.matches {
				t.Errorf("Filter(%q).Match() with time %v = %v, want %v", tt.query, tt.time, result, tt.matches)
			}
		})
	}
}

func TestFilterDurationComparison(t *testing.T) {
	tests := []struct {
		name       string
		fieldValue string
		query      string
		matches    bool
	}{
		{"ms vs ms greater", "150ms", "duration:>100ms", true},
		{"ms vs ms less", "50ms", "duration:>100ms", false},
		{"s vs ms greater", "1s", "duration:>500ms", true}, // 1s = 1000ms
		{"s vs ms less", "0.1s", "duration:>500ms", false}, // 0.1s = 100ms
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := ParseFilter(tt.query)
			if err != nil {
				t.Fatalf("ParseFilter(%q) error: %v", tt.query, err)
			}
			entry := LogEntry{
				Fields: map[string]any{"duration": tt.fieldValue},
			}
			result := filter.Match(entry)
			if result != tt.matches {
				t.Errorf("Filter(%q).Match(duration=%q) = %v, want %v", tt.query, tt.fieldValue, result, tt.matches)
			}
		})
	}
}

func TestFilterString(t *testing.T) {
	tests := []struct {
		query    string
		expected string
	}{
		{"level:error", "level:error"},
		{"-level:debug", "-level:debug"},
		{"msg:~timeout", "msg:~timeout"},
		{"status:>400", "status:>400"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			filter, err := ParseFilter(tt.query)
			if err != nil {
				t.Fatalf("ParseFilter(%q) error: %v", tt.query, err)
			}
			result := filter.String()
			if result != tt.expected {
				t.Errorf("Filter(%q).String() = %q, want %q", tt.query, result, tt.expected)
			}
		})
	}
}

func TestParseNumber(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
		hasError bool
	}{
		{"100", 100, false},
		{"3.14", 3.14, false},
		{"100ms", 100, false},
		{"1s", 1000, false},    // Converted to ms
		{"5m", 300000, false},  // Converted to ms
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseNumber(tt.input)
			if (err != nil) != tt.hasError {
				t.Errorf("parseNumber(%q) error = %v, hasError = %v", tt.input, err, tt.hasError)
			}
			if !tt.hasError && result != tt.expected {
				t.Errorf("parseNumber(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseClause(t *testing.T) {
	tests := []struct {
		token  string
		negate bool
		field  string
	}{
		{"level:error", false, "level"},
		{"-level:debug", true, "level"},
		{"word", false, ""}, // Full-text search
		{"-word", true, ""}, // Negated full-text
	}

	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			clause, err := parseClause(tt.token)
			if err != nil {
				t.Fatalf("parseClause(%q) error: %v", tt.token, err)
			}
			if clause.negate != tt.negate {
				t.Errorf("parseClause(%q).negate = %v, want %v", tt.token, clause.negate, tt.negate)
			}
			if clause.field != tt.field {
				t.Errorf("parseClause(%q).field = %q, want %q", tt.token, clause.field, tt.field)
			}
		})
	}
}

func TestFilterTimestampFormats(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		entry   LogEntry
		matches bool
	}{
		{
			name:  "ISO format timestamp filter",
			query: "ts:>2024-01-15T00:00:00Z",
			entry: LogEntry{
				Timestamp: time.Date(2024, 1, 16, 10, 0, 0, 0, time.UTC),
			},
			matches: true,
		},
		{
			name:  "ISO format no match",
			query: "ts:>2024-01-15T00:00:00Z",
			entry: LogEntry{
				Timestamp: time.Date(2024, 1, 10, 10, 0, 0, 0, time.UTC),
			},
			matches: false,
		},
		{
			name:  "Relative minutes",
			query: "ts:>-5m",
			entry: LogEntry{
				Timestamp: time.Now().Add(-2 * time.Minute),
			},
			matches: true,
		},
		{
			name:  "Relative hours",
			query: "ts:>-2h",
			entry: LogEntry{
				Timestamp: time.Now().Add(-1 * time.Hour),
			},
			matches: true,
		},
		{
			name:  "Relative 24 hours",
			query: "ts:>-24h",
			entry: LogEntry{
				Timestamp: time.Now().Add(-12 * time.Hour),
			},
			matches: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := ParseFilter(tt.query)
			if err != nil {
				t.Fatalf("ParseFilter(%q) error: %v", tt.query, err)
			}
			result := filter.Match(tt.entry)
			if result != tt.matches {
				t.Errorf("Filter(%q).Match() = %v, want %v", tt.query, result, tt.matches)
			}
		})
	}
}

func TestFilterCompareOperators(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		value   any
		matches bool
	}{
		{"gt int true", "status:>200", float64(300), true},
		{"gt int false", "status:>200", float64(100), false},
		{"lt int true", "status:<400", float64(200), true},
		{"lt int false", "status:<400", float64(500), false},
		{"gte eq", "status:>=200", float64(200), true},
		{"gte greater", "status:>=200", float64(300), true},
		{"gte less", "status:>=200", float64(100), false},
		{"lte eq", "status:<=200", float64(200), true},
		{"lte less", "status:<=200", float64(100), true},
		{"lte greater", "status:<=200", float64(300), false},
		// String field with numeric comparison
		{"string num gt", "count:>50", "100", true},
		{"string num lt", "count:<50", "25", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := ParseFilter(tt.query)
			if err != nil {
				t.Fatalf("ParseFilter(%q) error: %v", tt.query, err)
			}
			entry := LogEntry{
				Fields: map[string]any{"status": tt.value, "count": tt.value},
			}
			result := filter.Match(entry)
			if result != tt.matches {
				t.Errorf("Filter(%q).Match(value=%v) = %v, want %v", tt.query, tt.value, result, tt.matches)
			}
		})
	}
}

func TestFilterOrClause(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		entry   LogEntry
		matches bool
	}{
		{
			name:  "first matches",
			query: "level:error,warn,info",
			entry: LogEntry{Level: LevelError},
			matches: true,
		},
		{
			name:  "second matches",
			query: "level:error,warn,info",
			entry: LogEntry{Level: LevelWarn},
			matches: true,
		},
		{
			name:  "third matches",
			query: "level:error,warn,info",
			entry: LogEntry{Level: LevelInfo},
			matches: true,
		},
		{
			name:  "none matches",
			query: "level:error,warn",
			entry: LogEntry{Level: LevelDebug},
			matches: false,
		},
		{
			name:  "field or match",
			query: "host:db01,db02,db03",
			entry: LogEntry{Fields: map[string]any{"host": "db02"}},
			matches: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := ParseFilter(tt.query)
			if err != nil {
				t.Fatalf("ParseFilter(%q) error: %v", tt.query, err)
			}
			result := filter.Match(tt.entry)
			if result != tt.matches {
				t.Errorf("Filter(%q).Match() = %v, want %v", tt.query, result, tt.matches)
			}
		})
	}
}

func TestClauseString(t *testing.T) {
	tests := []struct {
		query    string
		expected string
	}{
		{"level:error,warn", "level:error,warn"},
		{"level:<=200", "level:<=200"},
		{"level:>=100", "level:>=100"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			filter, err := ParseFilter(tt.query)
			if err != nil {
				t.Fatalf("ParseFilter(%q) error: %v", tt.query, err)
			}
			result := filter.String()
			if result != tt.expected {
				t.Errorf("Filter(%q).String() = %q, want %q", tt.query, result, tt.expected)
			}
		})
	}
}
