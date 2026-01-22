// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package crashes

import "time"

// Crash represents a captured service crash with context.
// The format mirrors TraceReport for consistency.
type Crash struct {
	Version   string       `json:"version"`            // Report format version
	ID        string       `json:"id"`                 // Unique crash ID (timestamp-based)
	Service   string       `json:"service"`            // Name of crashed service
	Timestamp time.Time    `json:"timestamp"`          // When the crash occurred
	TraceID   string       `json:"trace_id"`           // Request trace ID (if found)
	ExitCode  int          `json:"exit_code"`          // Process exit code
	Error     string       `json:"error"`              // Error message/reason
	Worktree  WorktreeInfo `json:"worktree"`           // Worktree context
	Summary   CrashStats   `json:"summary"`            // Summary statistics
	Entries   []CrashEntry `json:"entries"`            // Log entries sorted by time
	Trigger   string       `json:"trigger"`            // What triggered the capture (e.g., "service.crashed")
}

// CrashStats contains summary statistics for a crash report.
type CrashStats struct {
	TotalEntries int            `json:"total_entries"` // Total entries found
	BySource     map[string]int `json:"by_source"`     // Entries per service
	ByLevel      map[string]int `json:"by_level"`      // Entries per log level
}

// CrashEntry represents a single log entry in a crash report.
// Mirrors TraceEntry for consistency.
type CrashEntry struct {
	Timestamp time.Time      `json:"timestamp"`        // Entry timestamp
	Source    string         `json:"source"`           // Service name
	Level     string         `json:"level,omitempty"`  // Log level
	Message   string         `json:"message"`          // Log message
	Fields    map[string]any `json:"fields,omitempty"` // Additional fields
	Raw       string         `json:"raw"`              // Original log line
}

// WorktreeInfo contains worktree context for a crash.
type WorktreeInfo struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
}

// CrashSummary is a minimal representation for listing crashes.
type CrashSummary struct {
	ID        string    `json:"id"`
	Service   string    `json:"service"`
	Timestamp time.Time `json:"timestamp"`
	TraceID   string    `json:"trace_id"`
	ExitCode  int       `json:"exit_code"`
	Error     string    `json:"error"`
}
