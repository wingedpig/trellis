// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package logs provides structured log viewing capabilities with multiple sources and parsers.
package logs

import (
	"time"
)

// LogLevel represents a log severity level.
type LogLevel string

const (
	LevelTrace LogLevel = "trace"
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
	LevelFatal LogLevel = "fatal"
)

// LogEntry represents a single parsed log entry.
type LogEntry struct {
	// Timestamp is the parsed timestamp (or receive time if unparseable).
	Timestamp time.Time `json:"timestamp"`
	// Level is the log level (info, warn, error, debug, trace, fatal).
	Level LogLevel `json:"level,omitempty"`
	// Message is the main log message.
	Message string `json:"message"`
	// Fields contains additional structured fields from the log.
	Fields map[string]any `json:"fields,omitempty"`
	// Raw is the original unparsed line.
	Raw string `json:"raw"`
	// Source is the log viewer name that produced this entry.
	Source string `json:"source"`
	// Offset is the position in the source (for historical access).
	Offset int64 `json:"offset,omitempty"`
	// Sequence is a monotonically increasing counter for ordering.
	Sequence uint64 `json:"sequence"`
}

// NormalizeLevel converts various level strings to a standard LogLevel.
func NormalizeLevel(level string) LogLevel {
	switch level {
	case "trace", "TRACE", "trc", "TRC":
		return LevelTrace
	case "debug", "DEBUG", "dbg", "DBG":
		return LevelDebug
	case "info", "INFO", "inf", "INF", "information", "INFORMATION":
		return LevelInfo
	case "warn", "WARN", "warning", "WARNING", "wrn", "WRN":
		return LevelWarn
	case "error", "ERROR", "err", "ERR":
		return LevelError
	case "fatal", "FATAL", "critical", "CRITICAL", "panic", "PANIC":
		return LevelFatal
	default:
		return LevelInfo
	}
}

// LevelSeverity returns a numeric severity for level comparison.
// Higher numbers are more severe.
func (l LogLevel) Severity() int {
	switch l {
	case LevelTrace:
		return 0
	case LevelDebug:
		return 1
	case LevelInfo:
		return 2
	case LevelWarn:
		return 3
	case LevelError:
		return 4
	case LevelFatal:
		return 5
	default:
		return 2
	}
}

// String returns the string representation of the level.
func (l LogLevel) String() string {
	return string(l)
}

// IsMoreSevereThan returns true if this level is more severe than other.
func (l LogLevel) IsMoreSevereThan(other LogLevel) bool {
	return l.Severity() > other.Severity()
}

// IsAtLeast returns true if this level is at least as severe as other.
func (l LogLevel) IsAtLeast(other LogLevel) bool {
	return l.Severity() >= other.Severity()
}
