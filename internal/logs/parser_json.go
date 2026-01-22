// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"encoding/json"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// JSONParser parses JSON-formatted log lines.
type JSONParser struct {
	BaseParser
}

// NewJSONParser creates a new JSON parser.
func NewJSONParser(cfg config.LogParserConfig) *JSONParser {
	return &JSONParser{BaseParser: BaseParser{cfg: cfg}}
}

// Name returns the parser name.
func (p *JSONParser) Name() string {
	return "json"
}

// Parse parses a JSON log line.
func (p *JSONParser) Parse(line string) LogEntry {
	entry := LogEntry{
		Raw:       line,
		Timestamp: time.Now(),
		Fields:    make(map[string]any),
	}

	// Parse JSON
	var data map[string]any
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		// Return unparsed entry
		entry.Message = line
		return entry
	}

	// Extract timestamp (keep in Fields for layout access)
	tsField := p.GetFieldName("timestamp")
	if tsVal, ok := data[tsField]; ok {
		switch v := tsVal.(type) {
		case string:
			if ts, err := p.ParseTimestamp(v); err == nil {
				entry.Timestamp = ts
			}
		case float64:
			// Unix timestamp (seconds or milliseconds)
			if v > 1e12 {
				entry.Timestamp = time.Unix(int64(v/1000), int64(v)%1000*1e6)
			} else {
				entry.Timestamp = time.Unix(int64(v), 0)
			}
		}
		// Don't delete - keep in Fields so layout can reference by original field name
	}

	// Extract level (keep in Fields for layout access)
	levelField := p.GetFieldName("level")
	if levelVal, ok := data[levelField]; ok {
		if levelStr, ok := levelVal.(string); ok {
			entry.Level = NormalizeLevel(levelStr)
		}
		// Don't delete - keep in Fields so layout can reference by original field name
	}

	// Extract message - try configured field first, then common aliases
	// Keep in Fields for layout access
	msgField := p.GetFieldName("message")
	messageAliases := []string{"message", "msg", "log"}
	if msgField != "" {
		messageAliases = []string{msgField}
	}

	for _, alias := range messageAliases {
		if msgVal, ok := data[alias]; ok {
			if msgStr, ok := msgVal.(string); ok {
				entry.Message = msgStr
				// Don't delete - keep in Fields so layout can reference by original field name
				break
			}
		}
	}

	// Store all fields (including timestamp, level, message for layout access)
	entry.Fields = data

	return entry
}
