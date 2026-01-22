// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFormatterPlain(t *testing.T) {
	var buf bytes.Buffer
	formatter, err := NewFormatter(&buf, OutputOptions{Format: FormatPlain})
	if err != nil {
		t.Fatalf("NewFormatter failed: %v", err)
	}

	entry := LogEntry{
		Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Level:     "INFO",
		Message:   "Test message",
	}

	if err := formatter.FormatEntry(&entry); err != nil {
		t.Fatalf("FormatEntry failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "2024-01-15 10:30:00") {
		t.Errorf("expected timestamp in output, got: %s", output)
	}
	if !strings.Contains(output, "INFO") {
		t.Errorf("expected INFO in output, got: %s", output)
	}
	if !strings.Contains(output, "Test message") {
		t.Errorf("expected message in output, got: %s", output)
	}
}

func TestFormatterJSONL(t *testing.T) {
	var buf bytes.Buffer
	formatter, err := NewFormatter(&buf, OutputOptions{Format: FormatJSONL})
	if err != nil {
		t.Fatalf("NewFormatter failed: %v", err)
	}

	entry := LogEntry{
		Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Level:     "ERROR",
		Message:   "Connection failed",
		Source:    "backend",
	}

	if err := formatter.FormatEntry(&entry); err != nil {
		t.Fatalf("FormatEntry failed: %v", err)
	}

	// Parse the JSON output
	var parsed LogEntry
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if parsed.Level != "ERROR" {
		t.Errorf("expected level ERROR, got %s", parsed.Level)
	}
	if parsed.Message != "Connection failed" {
		t.Errorf("expected message 'Connection failed', got %s", parsed.Message)
	}
}

func TestFormatterJSONArray(t *testing.T) {
	var buf bytes.Buffer
	formatter, err := NewFormatter(&buf, OutputOptions{Format: FormatJSON})
	if err != nil {
		t.Fatalf("NewFormatter failed: %v", err)
	}

	entries := []LogEntry{
		{
			Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			Level:     "INFO",
			Message:   "First message",
		},
		{
			Timestamp: time.Date(2024, 1, 15, 10, 31, 0, 0, time.UTC),
			Level:     "WARN",
			Message:   "Second message",
		},
	}

	if err := formatter.FormatEntries(entries); err != nil {
		t.Fatalf("FormatEntries failed: %v", err)
	}

	// Parse the JSON array
	var parsed []LogEntry
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if len(parsed) != 2 {
		t.Errorf("expected 2 entries, got %d", len(parsed))
	}
	if parsed[0].Message != "First message" {
		t.Errorf("expected 'First message', got %s", parsed[0].Message)
	}
}

func TestFormatterCSV(t *testing.T) {
	var buf bytes.Buffer
	formatter, err := NewFormatter(&buf, OutputOptions{Format: FormatCSV})
	if err != nil {
		t.Fatalf("NewFormatter failed: %v", err)
	}

	entries := []LogEntry{
		{
			Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			Level:     "INFO",
			Message:   "Test message",
			Source:    "service",
			Raw:       "raw log line",
		},
	}

	if err := formatter.FormatEntries(entries); err != nil {
		t.Fatalf("FormatEntries failed: %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines (header + data), got %d", len(lines))
	}

	// Check header
	if !strings.HasPrefix(lines[0], "timestamp,level,message,source,raw") {
		t.Errorf("unexpected header: %s", lines[0])
	}

	// Check data has correct structure
	if !strings.Contains(lines[1], "2024-01-15") {
		t.Errorf("expected timestamp in data line: %s", lines[1])
	}
	if !strings.Contains(lines[1], "INFO") {
		t.Errorf("expected INFO in data line: %s", lines[1])
	}
}

func TestFormatterRaw(t *testing.T) {
	var buf bytes.Buffer
	formatter, err := NewFormatter(&buf, OutputOptions{Format: FormatRaw})
	if err != nil {
		t.Fatalf("NewFormatter failed: %v", err)
	}

	entry := LogEntry{
		Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Level:     "INFO",
		Message:   "Parsed message",
		Raw:       "2024-01-15 10:30:00 INFO Original raw line",
	}

	if err := formatter.FormatEntry(&entry); err != nil {
		t.Fatalf("FormatEntry failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	if output != entry.Raw {
		t.Errorf("expected raw line %q, got %q", entry.Raw, output)
	}
}

func TestFormatterTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		entry    LogEntry
		want     string
	}{
		{
			name:     "simple template",
			template: "{{.level}}: {{.message}}",
			entry: LogEntry{
				Level:   "ERROR",
				Message: "Something went wrong",
			},
			want: "ERROR: Something went wrong",
		},
		{
			name:     "timestamp template",
			template: "[{{.timestamp}}] {{.message}}",
			entry: LogEntry{
				Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
				Message:   "Test",
			},
			want: "[2024-01-15 10:30:00.000] Test",
		},
		{
			name:     "with fields",
			template: "{{.fields.request_id}} {{.message}}",
			entry: LogEntry{
				Message: "Request handled",
				Fields:  map[string]interface{}{"request_id": "abc123"},
			},
			want: "abc123 Request handled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			formatter, err := NewFormatter(&buf, OutputOptions{
				Format:   FormatTemplate,
				Template: tt.template,
			})
			if err != nil {
				t.Fatalf("NewFormatter failed: %v", err)
			}

			if err := formatter.FormatEntry(&tt.entry); err != nil {
				t.Fatalf("FormatEntry failed: %v", err)
			}

			output := strings.TrimSpace(buf.String())
			if output != tt.want {
				t.Errorf("got %q, want %q", output, tt.want)
			}
		})
	}
}

func TestFormatterTemplateInvalid(t *testing.T) {
	_, err := NewFormatter(&bytes.Buffer{}, OutputOptions{
		Format:   FormatTemplate,
		Template: "{{.invalid",
	})
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestCalculateStats(t *testing.T) {
	entries := []LogEntry{
		{Level: "DEBUG", Message: "Starting"},
		{Level: "INFO", Message: "Ready"},
		{Level: "INFO", Message: "Processing"},
		{Level: "WARN", Message: "Slow query"},
		{Level: "ERROR", Message: "Connection timeout"},
		{Level: "ERROR", Message: "Connection timeout"},
		{Level: "ERROR", Message: "Invalid input"},
		{Level: "INFO", Message: "Completed"},
	}

	stats := CalculateStats(entries, time.Hour)

	// Check total
	if stats.TotalEntries != 8 {
		t.Errorf("TotalEntries = %d, want 8", stats.TotalEntries)
	}

	// Check level counts
	if stats.LevelCounts["INFO"] != 3 {
		t.Errorf("INFO count = %d, want 3", stats.LevelCounts["INFO"])
	}
	if stats.LevelCounts["ERROR"] != 3 {
		t.Errorf("ERROR count = %d, want 3", stats.LevelCounts["ERROR"])
	}
	if stats.LevelCounts["WARN"] != 1 {
		t.Errorf("WARN count = %d, want 1", stats.LevelCounts["WARN"])
	}

	// Check error rate (3/8 = 37.5%)
	expectedErrorRate := 37.5
	if stats.ErrorRate < expectedErrorRate-0.1 || stats.ErrorRate > expectedErrorRate+0.1 {
		t.Errorf("ErrorRate = %.1f%%, want ~%.1f%%", stats.ErrorRate, expectedErrorRate)
	}

	// Check top errors
	if len(stats.TopErrors) == 0 {
		t.Error("expected TopErrors to be populated")
	}
	if stats.TopErrors[0].Message != "Connection timeout" {
		t.Errorf("top error = %q, want 'Connection timeout'", stats.TopErrors[0].Message)
	}
	if stats.TopErrors[0].Count != 2 {
		t.Errorf("top error count = %d, want 2", stats.TopErrors[0].Count)
	}
}

func TestCalculateStatsEmpty(t *testing.T) {
	stats := CalculateStats([]LogEntry{}, time.Hour)

	if stats.TotalEntries != 0 {
		t.Errorf("TotalEntries = %d, want 0", stats.TotalEntries)
	}
	if stats.ErrorRate != 0 {
		t.Errorf("ErrorRate = %f, want 0", stats.ErrorRate)
	}
}

func TestFormatStats(t *testing.T) {
	stats := &LogStats{
		TotalEntries:  1000,
		Duration:      time.Hour,
		EntriesPerMin: 16.67,
		ErrorRate:     2.5,
		WarnRate:      5.0,
		LevelCounts: map[string]int{
			"DEBUG": 100,
			"INFO":  875,
			"WARN":  50,
			"ERROR": 25,
		},
		TopErrors: []ErrorCount{
			{Message: "Connection failed", Count: 15},
			{Message: "Timeout", Count: 10},
		},
	}

	var buf bytes.Buffer
	FormatStats(&buf, stats, "backend")

	output := buf.String()

	// Check key elements are present
	expectations := []string{
		"Log Statistics for 'backend'",
		"1.0h",
		"Total entries:",
		"1000",
		"Error rate:",
		"2.5%",
		"Level distribution:",
		"INFO:",
		"Top errors:",
		"Connection failed",
	}

	for _, exp := range expectations {
		if !strings.Contains(output, exp) {
			t.Errorf("expected output to contain %q", exp)
		}
	}
}

func TestEmptyLevelDefaultsToInfo(t *testing.T) {
	var buf bytes.Buffer
	formatter, _ := NewFormatter(&buf, OutputOptions{Format: FormatPlain})

	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     "",
		Message:   "Test",
	}

	formatter.FormatEntry(&entry)
	output := buf.String()

	if !strings.Contains(output, "INFO") {
		t.Errorf("expected empty level to default to INFO, got: %s", output)
	}
}
