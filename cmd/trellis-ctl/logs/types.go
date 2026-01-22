// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package logs provides log filtering, formatting, and output functionality
// for trellis-ctl.
package logs

import (
	"time"
)

// LogEntry represents a parsed log entry from the API.
type LogEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Raw       string                 `json:"raw"`
	Source    string                 `json:"source"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

// LogLevel represents a log severity level.
type LogLevel int

const (
	LevelUnset LogLevel = iota - 1 // -1, used as sentinel for "not set"
	LevelTrace                     // 0
	LevelDebug                     // 1
	LevelInfo                      // 2
	LevelWarn                      // 3
	LevelError                     // 4
	LevelFatal                     // 5
	LevelUnknown                   // 6
)

// String returns the string representation of a log level.
func (l LogLevel) String() string {
	switch l {
	case LevelTrace:
		return "TRACE"
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// FilterOptions contains all options for filtering log entries.
type FilterOptions struct {
	Since        time.Time         // Only entries after this time
	Until        time.Time         // Only entries before this time
	Levels       []LogLevel        // Only entries with these levels (empty = all)
	MinLevel     LogLevel          // Minimum level (for "level+" syntax), -1 if not set
	GrepPattern  string            // Regex pattern to match in message
	FieldFilters map[string]string // Field name -> value filters
	Before       int               // Number of lines to show before each match (-B)
	After        int               // Number of lines to show after each match (-A)
}

// OutputFormat specifies the output format for logs.
type OutputFormat int

const (
	FormatPlain OutputFormat = iota
	FormatJSON
	FormatJSONL
	FormatCSV
	FormatRaw
	FormatTemplate
)

// OutputOptions contains all options for formatting log output.
type OutputOptions struct {
	Format   OutputFormat
	Template string // Go template string for FormatTemplate
	Columns  []string // Column names for CSV output
}

// LogStats contains statistics about a set of log entries.
type LogStats struct {
	TotalEntries int            `json:"total_entries"`
	Duration     time.Duration  `json:"duration"`
	EntriesPerMin float64       `json:"entries_per_min"`
	LevelCounts  map[string]int `json:"level_counts"`
	ErrorRate    float64        `json:"error_rate"`
	WarnRate     float64        `json:"warn_rate"`
	TopErrors    []ErrorCount   `json:"top_errors,omitempty"`
}

// ErrorCount represents an error message and its occurrence count.
type ErrorCount struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

// ParserConfig holds configuration for parsing log lines.
type ParserConfig struct {
	Type           string // "json", "logfmt", "none"
	TimestampField string // Field name containing timestamp
	LevelField     string // Field name containing log level
	MessageField   string // Field name containing message
}
