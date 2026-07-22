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

// TimestampExtractor is an optional interface a parser may implement to
// expose timestamp extraction separately from full line parsing. Returns
// ok=true ONLY if the timestamp was extracted from the line itself —
// callers like the byte-offset reader's binary search must be able to
// tell "extracted from this line" from "fell back to time.Now()" (which
// is what Parse does when timestamp extraction fails).
//
// Parsers that don't implement this — older ones or ones where the
// timestamp source is ambiguous — make the seek path treat unparseable
// lines as zero-time, biasing the binary search toward broader windows.
type TimestampExtractor interface {
	ExtractTimestamp(line string) (time.Time, bool)
}

// NewParser creates a new LogParser from configuration. loc is the timezone
// used to interpret zoneless log timestamps (nil = time.UTC, matching
// time.Parse). Formats that carry an explicit offset or "Z" are unaffected.
func NewParser(cfg config.LogParserConfig, loc *time.Location) (LogParser, error) {
	var p LogParser
	var err error
	switch ParserType(cfg.Type) {
	case ParserTypeJSON, "":
		p = NewJSONParser(cfg)
	case ParserTypeLogfmt:
		p = NewLogfmtParser(cfg)
	case ParserTypeRegex:
		p, err = NewRegexParser(cfg)
	case ParserTypeSyslog:
		p = NewSyslogParser(cfg)
	case ParserTypeNone:
		p = NewNoneParser()
	default:
		return nil, fmt.Errorf("unknown parser type: %s", cfg.Type)
	}
	if err != nil {
		return nil, err
	}
	// Parsers that carry a BaseParser (all but NoneParser) get the timezone for
	// zoneless timestamp interpretation.
	if ls, ok := p.(interface{ setLocation(*time.Location) }); ok {
		ls.setLocation(loc)
	}
	return p, nil
}

// BaseParser provides common functionality for parsers.
type BaseParser struct {
	cfg config.LogParserConfig
	// loc interprets zoneless timestamps; nil means time.UTC (the time.Parse
	// default). Set via setLocation during NewParser.
	loc *time.Location
}

// setLocation sets the timezone used to interpret zoneless timestamps.
func (p *BaseParser) setLocation(loc *time.Location) { p.loc = loc }

// location returns the timezone for zoneless timestamps, defaulting to UTC to
// match time.Parse for parsers built without an explicit location.
func (p *BaseParser) location() *time.Location {
	if p.loc == nil {
		return time.UTC
	}
	return p.loc
}

// ParseTimestamp parses a timestamp string using the configured format.
func (p *BaseParser) ParseTimestamp(value string) (time.Time, error) {
	if value == "" {
		return time.Now(), nil
	}

	loc := p.location()

	// Try configured format first
	if p.cfg.TimestampFormat != "" {
		t, err := time.ParseInLocation(p.cfg.TimestampFormat, value, loc)
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
		t, err := time.ParseInLocation(format, value, loc)
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
