// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"fmt"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// LogParser parses raw log lines into structured LogEntry objects.
type LogParser interface {
	// Parse parses a raw log line into a LogEntry.
	// If parsing fails, returns an entry with the raw line and current timestamp.
	Parse(line string) LogEntry

	// Name returns the parser name.
	Name() string
}

// ParserType represents the type of log parser.
type ParserType string

const (
	ParserTypeJSON   ParserType = "json"
	ParserTypeLogfmt ParserType = "logfmt"
	ParserTypeRegex  ParserType = "regex"
	ParserTypeSyslog ParserType = "syslog"
	ParserTypeNone   ParserType = "none"
)

// NewParser creates a new LogParser from configuration.
func NewParser(cfg config.LogParserConfig) (LogParser, error) {
	switch ParserType(cfg.Type) {
	case ParserTypeJSON, "":
		return NewJSONParser(cfg), nil
	case ParserTypeLogfmt:
		return NewLogfmtParser(cfg), nil
	case ParserTypeRegex:
		return NewRegexParser(cfg)
	case ParserTypeSyslog:
		return NewSyslogParser(cfg), nil
	case ParserTypeNone:
		return NewNoneParser(), nil
	default:
		return nil, fmt.Errorf("unknown parser type: %s", cfg.Type)
	}
}

// BaseParser provides common functionality for parsers.
type BaseParser struct {
	cfg config.LogParserConfig
}

// ParseTimestamp parses a timestamp string using the configured format.
func (p *BaseParser) ParseTimestamp(value string) (time.Time, error) {
	if value == "" {
		return time.Now(), nil
	}

	// Try configured format first
	if p.cfg.TimestampFormat != "" {
		t, err := time.Parse(p.cfg.TimestampFormat, value)
		if err == nil {
			return t, nil
		}
	}

	// Try common formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		"Jan 02 15:04:05",
		"Jan  2 15:04:05",
	}

	for _, format := range formats {
		t, err := time.Parse(format, value)
		if err == nil {
			return t, nil
		}
	}

	// Try Unix timestamp
	var unixSec int64
	if _, err := fmt.Sscanf(value, "%d", &unixSec); err == nil {
		// Check if it's milliseconds
		if unixSec > 1e12 {
			return time.Unix(unixSec/1000, (unixSec%1000)*1e6), nil
		}
		return time.Unix(unixSec, 0), nil
	}

	return time.Now(), fmt.Errorf("unable to parse timestamp: %s", value)
}

// GetFieldName returns the field name for a standard field (timestamp, level, message).
// Returns empty string for level/message if not configured (they're optional).
// Returns "timestamp" as default since timestamp is required.
func (p *BaseParser) GetFieldName(field string) string {
	switch field {
	case "timestamp":
		if p.cfg.Timestamp != "" {
			return p.cfg.Timestamp
		}
		return "timestamp"
	case "level":
		// Level is optional - return empty if not configured
		return p.cfg.Level
	case "message":
		// Message is optional - return empty if not configured
		return p.cfg.Message
	default:
		return field
	}
}
