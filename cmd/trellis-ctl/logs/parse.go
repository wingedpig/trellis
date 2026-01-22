// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseDuration parses a duration string like "1h", "30m", "2d", a clock time like "6:30am",
// or an ISO timestamp.
// Supported formats:
//   - Relative: 1h, 30m, 2d, 1w (hours, minutes, days, weeks ago)
//   - Clock time: 6:00am, 6:30pm, 14:00, 14:30 (today)
//   - ISO timestamp: 2024-01-15T10:30:00Z, 2024-01-15
func ParseDuration(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty duration string")
	}

	// Try parsing as ISO 8601 timestamp first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try without timezone
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, nil
	}
	// Try date only
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}

	// Try parsing as clock time (e.g., "6:00am", "6:30pm", "14:00")
	if t, ok := parseClockTime(s); ok {
		return t, nil
	}

	// Parse as relative duration
	re := regexp.MustCompile(`^(\d+)([smhdw])$`)
	matches := re.FindStringSubmatch(s)
	if matches == nil {
		return time.Time{}, fmt.Errorf("invalid duration format: %q (use e.g., 1h, 30m, 6:30am, or ISO timestamp)", s)
	}

	value, _ := strconv.Atoi(matches[1])
	unit := matches[2]

	var duration time.Duration
	switch unit {
	case "s":
		duration = time.Duration(value) * time.Second
	case "m":
		duration = time.Duration(value) * time.Minute
	case "h":
		duration = time.Duration(value) * time.Hour
	case "d":
		duration = time.Duration(value) * 24 * time.Hour
	case "w":
		duration = time.Duration(value) * 7 * 24 * time.Hour
	}

	return time.Now().Add(-duration), nil
}

// parseClockTime parses clock times like "6:00am", "6:30pm", "14:00", "14:30"
// and returns the time on today's date.
func parseClockTime(s string) (time.Time, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// Try 12-hour format with am/pm: "6:00am", "6:30pm", "12:00am"
	re12 := regexp.MustCompile(`^(\d{1,2}):(\d{2})(am|pm)$`)
	if matches := re12.FindStringSubmatch(s); matches != nil {
		hour, _ := strconv.Atoi(matches[1])
		minute, _ := strconv.Atoi(matches[2])
		ampm := matches[3]

		if hour < 1 || hour > 12 || minute < 0 || minute > 59 {
			return time.Time{}, false
		}

		// Convert to 24-hour
		if ampm == "am" {
			if hour == 12 {
				hour = 0 // 12:00am = 00:00
			}
		} else { // pm
			if hour != 12 {
				hour += 12 // 1pm = 13:00, but 12pm = 12:00
			}
		}

		return today.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute), true
	}

	// Try 24-hour format: "14:00", "14:30", "9:00"
	re24 := regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	if matches := re24.FindStringSubmatch(s); matches != nil {
		hour, _ := strconv.Atoi(matches[1])
		minute, _ := strconv.Atoi(matches[2])

		if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return time.Time{}, false
		}

		return today.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute), true
	}

	return time.Time{}, false
}

// ParseLevel parses a log level string to LogLevel.
func ParseLevel(s string) (LogLevel, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TRACE":
		return LevelTrace, nil
	case "DEBUG":
		return LevelDebug, nil
	case "INFO":
		return LevelInfo, nil
	case "WARN", "WARNING":
		return LevelWarn, nil
	case "ERROR", "ERR":
		return LevelError, nil
	case "FATAL", "CRITICAL", "CRIT":
		return LevelFatal, nil
	default:
		return LevelUnknown, fmt.Errorf("unknown log level: %q", s)
	}
}

// ParseLevelFilter parses a level filter string.
// Supports formats:
//   - "error" - single level
//   - "warn,error" - multiple levels
//   - "info+" - minimum level (info and above)
func ParseLevelFilter(s string) (levels []LogLevel, minLevel LogLevel, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, LevelUnset, nil
	}

	// Check for "level+" syntax
	if strings.HasSuffix(s, "+") {
		levelStr := strings.TrimSuffix(s, "+")
		level, err := ParseLevel(levelStr)
		if err != nil {
			return nil, LevelUnset, err
		}
		return nil, level, nil
	}

	// Parse comma-separated levels
	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		level, err := ParseLevel(part)
		if err != nil {
			return nil, LevelUnset, err
		}
		levels = append(levels, level)
	}

	return levels, LevelUnset, nil
}

// ParseFieldFilter parses a field filter string like "host=prod1" or "status=500".
func ParseFieldFilter(s string) (field, value string, err error) {
	s = strings.TrimSpace(s)
	idx := strings.Index(s, "=")
	if idx < 1 {
		return "", "", fmt.Errorf("invalid field filter format: %q (use field=value)", s)
	}
	return s[:idx], s[idx+1:], nil
}

// ParseOutputFormat parses an output format string.
func ParseOutputFormat(s string) (OutputFormat, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "plain", "text":
		return FormatPlain, nil
	case "json":
		return FormatJSON, nil
	case "jsonl", "ndjson":
		return FormatJSONL, nil
	case "csv":
		return FormatCSV, nil
	case "raw":
		return FormatRaw, nil
	default:
		return FormatPlain, fmt.Errorf("unknown output format: %q", s)
	}
}

// GetLevelFromEntry returns the LogLevel for a log entry.
func GetLevelFromEntry(entry *LogEntry) LogLevel {
	level, err := ParseLevel(entry.Level)
	if err != nil {
		return LevelUnknown
	}
	return level
}

// ParseLogLine parses a raw log line using the given parser config.
// Returns a LogEntry with parsed fields populated.
func ParseLogLine(line string, cfg ParserConfig, source string) LogEntry {
	entry := LogEntry{
		Raw:    line,
		Source: source,
		Fields: make(map[string]interface{}),
	}

	if cfg.Type == "" || cfg.Type == "none" {
		// No parsing, just use raw line as message
		entry.Message = line
		entry.Timestamp = time.Now()
		return entry
	}

	var parsed map[string]interface{}

	switch cfg.Type {
	case "json":
		parsed = parseJSON(line)
	case "logfmt":
		parsed = parseLogfmt(line)
	default:
		entry.Message = line
		entry.Timestamp = time.Now()
		return entry
	}

	if parsed == nil {
		// Parsing failed, use raw line
		entry.Message = line
		entry.Timestamp = time.Now()
		return entry
	}

	// Copy all fields
	entry.Fields = parsed

	// Extract timestamp
	if cfg.TimestampField != "" {
		if ts, ok := parsed[cfg.TimestampField]; ok {
			entry.Timestamp = parseTimestamp(ts)
			delete(entry.Fields, cfg.TimestampField)
		}
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// Extract level
	if cfg.LevelField != "" {
		if lvl, ok := parsed[cfg.LevelField]; ok {
			entry.Level = fmt.Sprintf("%v", lvl)
			delete(entry.Fields, cfg.LevelField)
		}
	}

	// Extract message
	if cfg.MessageField != "" {
		if msg, ok := parsed[cfg.MessageField]; ok {
			entry.Message = fmt.Sprintf("%v", msg)
			delete(entry.Fields, cfg.MessageField)
		}
	}
	if entry.Message == "" {
		entry.Message = line
	}

	return entry
}

// parseJSON parses a JSON log line into a map.
func parseJSON(line string) map[string]interface{} {
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		return nil
	}
	return result
}

// parseLogfmt parses a logfmt log line (key=value pairs) into a map.
func parseLogfmt(line string) map[string]interface{} {
	result := make(map[string]interface{})
	re := regexp.MustCompile(`(\w+)=(?:"([^"]*)"|(\S+))`)
	matches := re.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return nil
	}
	for _, match := range matches {
		key := match[1]
		value := match[2]
		if value == "" {
			value = match[3]
		}
		result[key] = value
	}
	return result
}

// parseTimestamp attempts to parse a timestamp from various formats.
func parseTimestamp(v interface{}) time.Time {
	switch ts := v.(type) {
	case string:
		// Try common formats
		formats := []string{
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02T15:04:05.000Z",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
		}
		for _, format := range formats {
			if t, err := time.Parse(format, ts); err == nil {
				return t
			}
		}
		// Try parsing with local timezone
		if t, err := time.ParseInLocation("2006-01-02T15:04:05.999999999-07:00", ts, time.Local); err == nil {
			return t
		}
	case float64:
		// Unix timestamp (seconds or milliseconds)
		if ts > 1e12 {
			return time.UnixMilli(int64(ts))
		}
		return time.Unix(int64(ts), 0)
	case int64:
		if ts > 1e12 {
			return time.UnixMilli(ts)
		}
		return time.Unix(ts, 0)
	}
	return time.Time{}
}
