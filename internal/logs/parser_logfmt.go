// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"strings"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// LogfmtParser parses logfmt-formatted log lines.
// Format: key=value key2="value with spaces" key3=value3
type LogfmtParser struct {
	BaseParser
}

// NewLogfmtParser creates a new logfmt parser.
func NewLogfmtParser(cfg config.LogParserConfig) *LogfmtParser {
	return &LogfmtParser{BaseParser: BaseParser{cfg: cfg}}
}

// Name returns the parser name.
func (p *LogfmtParser) Name() string {
	return "logfmt"
}

// Parse parses a logfmt log line.
func (p *LogfmtParser) Parse(line string) LogEntry {
	entry := LogEntry{
		Raw:       line,
		Timestamp: time.Now(),
		Fields:    make(map[string]any),
	}

	// Parse logfmt key=value pairs
	data := parseLogfmt(line)
	if len(data) == 0 {
		// Return unparsed entry
		entry.Message = line
		return entry
	}

	// Extract timestamp
	tsField := p.GetFieldName("timestamp")
	if tsVal, ok := data[tsField]; ok {
		if ts, err := p.ParseTimestamp(tsVal); err == nil {
			entry.Timestamp = ts
		}
		delete(data, tsField)
	}

	// Extract level (only if configured)
	levelField := p.GetFieldName("level")
	if levelField != "" {
		if levelVal, ok := data[levelField]; ok {
			entry.Level = NormalizeLevel(levelVal)
			delete(data, levelField)
		}
	}

	// Extract message (only if configured)
	msgField := p.GetFieldName("message")
	if msgField != "" {
		if msgVal, ok := data[msgField]; ok {
			entry.Message = msgVal
			delete(data, msgField)
		}
	}

	// Store remaining fields
	for k, v := range data {
		entry.Fields[k] = v
	}

	return entry
}

// parseLogfmt parses a logfmt string into key-value pairs.
func parseLogfmt(line string) map[string]string {
	result := make(map[string]string)

	var key string
	var value strings.Builder
	inValue := false
	inQuote := false
	escaped := false

	for i := 0; i < len(line); i++ {
		c := line[i]

		if escaped {
			value.WriteByte(c)
			escaped = false
			continue
		}

		if c == '\\' && inQuote {
			escaped = true
			continue
		}

		if c == '"' {
			if inQuote {
				inQuote = false
			} else if inValue {
				inQuote = true
			}
			continue
		}

		if c == '=' && !inValue && !inQuote {
			key = value.String()
			value.Reset()
			inValue = true
			continue
		}

		if c == ' ' && !inQuote {
			if inValue && key != "" {
				result[key] = value.String()
				key = ""
				value.Reset()
				inValue = false
			}
			continue
		}

		value.WriteByte(c)
	}

	// Handle last key-value pair
	if key != "" && inValue {
		result[key] = value.String()
	}

	return result
}
