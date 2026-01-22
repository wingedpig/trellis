// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"fmt"
	"regexp"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// RegexParser parses log lines using a regular expression with named capture groups.
type RegexParser struct {
	BaseParser
	pattern *regexp.Regexp
}

// NewRegexParser creates a new regex parser.
func NewRegexParser(cfg config.LogParserConfig) (*RegexParser, error) {
	if cfg.Pattern == "" {
		return nil, fmt.Errorf("regex parser requires pattern")
	}

	pattern, err := regexp.Compile(cfg.Pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	return &RegexParser{
		BaseParser: BaseParser{cfg: cfg},
		pattern:    pattern,
	}, nil
}

// Name returns the parser name.
func (p *RegexParser) Name() string {
	return "regex"
}

// Parse parses a log line using the regex pattern.
func (p *RegexParser) Parse(line string) LogEntry {
	entry := LogEntry{
		Raw:       line,
		Timestamp: time.Now(),
		Fields:    make(map[string]any),
	}

	match := p.pattern.FindStringSubmatch(line)
	if match == nil {
		// Return unparsed entry
		entry.Message = line
		return entry
	}

	// Extract named groups
	names := p.pattern.SubexpNames()
	data := make(map[string]string)
	for i, name := range names {
		if i > 0 && name != "" && i < len(match) {
			data[name] = match[i]
		}
	}

	// Extract timestamp
	tsField := p.GetFieldName("timestamp")
	if tsVal, ok := data[tsField]; ok {
		if ts, err := p.ParseTimestamp(tsVal); err == nil {
			entry.Timestamp = ts
		}
		delete(data, tsField)
	}

	// Extract level - try configured field first, then common named groups
	levelField := p.GetFieldName("level")
	levelAliases := []string{"level", "lvl"}
	if levelField != "" {
		levelAliases = []string{levelField}
	}
	for _, alias := range levelAliases {
		if levelVal, ok := data[alias]; ok {
			entry.Level = NormalizeLevel(levelVal)
			delete(data, alias)
			break
		}
	}

	// Extract message - try configured field first, then common named groups
	msgField := p.GetFieldName("message")
	msgAliases := []string{"message", "msg", "log"}
	if msgField != "" {
		msgAliases = []string{msgField}
	}
	for _, alias := range msgAliases {
		if msgVal, ok := data[alias]; ok {
			entry.Message = msgVal
			delete(data, alias)
			break
		}
	}

	// Store remaining fields
	for k, v := range data {
		entry.Fields[k] = v
	}

	return entry
}

// SyslogParser parses RFC 3164 or RFC 5424 syslog format.
type SyslogParser struct {
	BaseParser
	rfc3164 *regexp.Regexp
	rfc5424 *regexp.Regexp
	format  string
}

// NewSyslogParser creates a new syslog parser.
func NewSyslogParser(cfg config.LogParserConfig) *SyslogParser {
	// RFC 3164: <priority>Mmm dd hh:mm:ss hostname tag: message
	rfc3164 := regexp.MustCompile(`^<(\d+)>(\w{3}\s+\d+\s+\d+:\d+:\d+)\s+(\S+)\s+(\S+?):\s*(.*)$`)

	// RFC 5424: <priority>version timestamp hostname app-name procid msgid structured-data msg
	rfc5424 := regexp.MustCompile(`^<(\d+)>(\d+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S*)\s*(.*)$`)

	return &SyslogParser{
		BaseParser: BaseParser{cfg: cfg},
		rfc3164:    rfc3164,
		rfc5424:    rfc5424,
		format:     cfg.Pattern, // Use pattern field for format hint
	}
}

// Name returns the parser name.
func (p *SyslogParser) Name() string {
	return "syslog"
}

// Parse parses a syslog log line.
func (p *SyslogParser) Parse(line string) LogEntry {
	entry := LogEntry{
		Raw:       line,
		Timestamp: time.Now(),
		Fields:    make(map[string]any),
	}

	// Try RFC 5424 first if specified or auto
	if p.format == "rfc5424" || p.format == "" {
		if match := p.rfc5424.FindStringSubmatch(line); match != nil {
			entry.Level = priorityToLevel(match[1])
			if ts, err := p.ParseTimestamp(match[3]); err == nil {
				entry.Timestamp = ts
			}
			entry.Fields["hostname"] = match[4]
			entry.Fields["app"] = match[5]
			entry.Fields["procid"] = match[6]
			entry.Fields["msgid"] = match[7]
			entry.Message = match[9]
			return entry
		}
	}

	// Try RFC 3164
	if match := p.rfc3164.FindStringSubmatch(line); match != nil {
		entry.Level = priorityToLevel(match[1])
		if ts, err := p.ParseTimestamp(match[2]); err == nil {
			// RFC 3164 doesn't include year, use current year
			if ts.Year() == 0 {
				ts = ts.AddDate(time.Now().Year(), 0, 0)
			}
			entry.Timestamp = ts
		}
		entry.Fields["hostname"] = match[3]
		entry.Fields["tag"] = match[4]
		entry.Message = match[5]
		return entry
	}

	// Return unparsed
	entry.Message = line
	return entry
}

// priorityToLevel converts syslog priority to LogLevel.
func priorityToLevel(priority string) LogLevel {
	var p int
	fmt.Sscanf(priority, "%d", &p)

	// Syslog severity is priority % 8
	severity := p % 8
	switch severity {
	case 0, 1, 2: // emergency, alert, critical
		return LevelFatal
	case 3: // error
		return LevelError
	case 4: // warning
		return LevelWarn
	case 5, 6: // notice, info
		return LevelInfo
	case 7: // debug
		return LevelDebug
	default:
		return LevelInfo
	}
}
