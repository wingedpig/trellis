// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"time"

	"github.com/wingedpig/trellis/internal/logs"
)

// TraceRequest represents a request to execute a distributed trace.
type TraceRequest struct {
	TraceID    string    `json:"id"`           // ID to search for (grep pattern)
	Group      string    `json:"group"`        // Trace group name
	Name       string    `json:"name"`         // Report name (auto-generated if empty)
	Start      time.Time `json:"start"`        // Start of time range
	End        time.Time `json:"end"`          // End of time range
	ExpandByID bool      `json:"expand_by_id"` // Whether to perform ID expansion (two-pass search)
}

// TraceReport represents the results of a trace execution.
type TraceReport struct {
	Version   string       `json:"version"`         // Report format version
	Name      string       `json:"name"`            // Report name
	TraceID   string       `json:"trace_id"`        // Searched trace ID
	Group     string       `json:"group"`           // Trace group name
	Status    string       `json:"status"`          // "running", "completed", "failed"
	CreatedAt time.Time    `json:"created_at"`      // When report was created
	TimeRange TimeRange    `json:"time_range"`      // Search time range
	Summary   TraceSummary `json:"summary"`         // Summary statistics
	Entries   []TraceEntry `json:"entries"`         // Log entries sorted by time
	Error     string       `json:"error,omitempty"` // Error message if failed
}

// TimeRange represents a time range for the trace.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// TraceSummary contains summary statistics for a trace.
type TraceSummary struct {
	TotalEntries int            `json:"total_entries"` // Total entries found
	BySource     map[string]int `json:"by_source"`     // Entries per log viewer
	ByLevel      map[string]int `json:"by_level"`      // Entries per log level
	DurationMS   int64          `json:"duration_ms"`   // Time to execute trace
}

// TraceEntry represents a single log entry in a trace.
type TraceEntry struct {
	Timestamp time.Time      `json:"timestamp"`           // Entry timestamp
	Source    string         `json:"source"`              // Log viewer name
	Level     string         `json:"level,omitempty"`     // Log level
	Message   string         `json:"message"`             // Log message
	Fields    map[string]any `json:"fields,omitempty"`    // Additional fields
	Raw       string         `json:"raw"`                 // Original log line
	IsContext bool           `json:"is_context"`          // True if this is a context line
}

// TraceEntryFromLogEntry converts a logs.LogEntry to a TraceEntry.
func TraceEntryFromLogEntry(entry logs.LogEntry, source string, isContext bool) TraceEntry {
	return TraceEntry{
		Timestamp: entry.Timestamp,
		Source:    source,
		Level:     string(entry.Level),
		Message:   entry.Message,
		Fields:    entry.Fields,
		Raw:       entry.Raw,
		IsContext: isContext,
	}
}

// ReportSummary is a lightweight summary of a report for listing.
type ReportSummary struct {
	Name       string    `json:"name"`
	TraceID    string    `json:"trace_id"`
	Group      string    `json:"group"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	EntryCount int       `json:"entry_count"`
	TimeStart  time.Time `json:"time_start"`
	TimeEnd    time.Time `json:"time_end"`
}

// TraceGroup represents a configured group of log viewers.
type TraceGroup struct {
	Name       string   `json:"name"`
	LogViewers []string `json:"log_viewers"`
}

// ExecuteResult is the result returned from trace execution.
type ExecuteResult struct {
	Name         string         `json:"name"`
	Status       string         `json:"status"` // "completed" or "failed"
	TotalEntries int            `json:"total_entries"`
	Sources      map[string]int `json:"sources"`
	DurationMS   int64          `json:"duration_ms"`
	Error        string         `json:"error,omitempty"`
}
