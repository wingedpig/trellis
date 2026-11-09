// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wingedpig/trellis/internal/config"
	serverlogs "github.com/wingedpig/trellis/internal/logs"
)

// ParseDuration parses a -since value (the start of a window). See
// ParseTimeBound for the accepted formats.
func ParseDuration(s string) (time.Time, error) {
	return ParseTimeBound(s, false)
}

// ParseUntil parses an -until value (the end of a window). A bare date or
// "today"/"yesterday" means the end of that day, so "-since 2025-01-10
// -until 2025-01-10" covers the whole day.
func ParseUntil(s string) (time.Time, error) {
	return ParseTimeBound(s, true)
}

// ParseTimeBound parses a time given on the command line. Times without an
// explicit offset are in the local timezone.
// Supported formats:
//   - Relative: 1h, 30m, 2d, 1w (hours, minutes, days, weeks ago)
//   - Clock time: 6:00am, 6:30pm, 6am, 14:00, 14:30:15 (today)
//   - Date: 2024-01-15 (start of day, or end of day when isEnd)
//   - Date + time: 2024-01-15 6:00am, 2024-01-15 14:30, 2024-01-15T14:30:00
//   - RFC 3339: 2024-01-15T10:30:00Z, 2024-01-15T10:30:00-08:00
//   - Keywords: now, today, yesterday
func ParseTimeBound(s string, isEnd bool) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}
	now := time.Now()
	lower := strings.ToLower(s)

	dayBound := func(day time.Time) time.Time {
		start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
		if isEnd {
			return start.AddDate(0, 0, 1).Add(-time.Nanosecond)
		}
		return start
	}

	switch lower {
	case "now":
		return now, nil
	case "today":
		return dayBound(now), nil
	case "yesterday":
		return dayBound(now.AddDate(0, 0, -1)), nil
	}

	// Timestamps with an explicit offset.
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05Z07:00", "2006-01-02T15:04Z07:00", "2006-01-02 15:04Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	// Timestamps without an offset are local time.
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return dayBound(t), nil
	}
	// Date followed by a clock time: "2024-01-15 6:00am".
	if date, clock, ok := strings.Cut(s, " "); ok {
		if d, err := time.ParseInLocation("2006-01-02", date, time.Local); err == nil {
			if off, ok := parseClockOffset(clock); ok {
				return d.Add(off), nil
			}
		}
	}

	if off, ok := parseClockOffset(s); ok {
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).Add(off), nil
	}

	// Relative duration
	matches := relativeRe.FindStringSubmatch(s)
	if matches == nil {
		return time.Time{}, fmt.Errorf("invalid time %q (use e.g. 1h, 30m, 6:30am, 2025-01-10, \"2025-01-10 6:00am\", today, yesterday, or an RFC 3339 timestamp)", s)
	}

	value, _ := strconv.Atoi(matches[1])
	var duration time.Duration
	switch matches[2] {
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
	return now.Add(-duration), nil
}

var (
	relativeRe = regexp.MustCompile(`^(\d+)([smhdw])$`)
	clock12Re  = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?(?::(\d{2}))?\s*(am|pm)$`)
	clock24Re  = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?$`)
)

// parseClockOffset parses a clock time ("6:00am", "6am", "6:30:15pm",
// "14:00", "14:30:15") into an offset from midnight.
func parseClockOffset(s string) (time.Duration, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	var hour, minute, second int
	if m := clock12Re.FindStringSubmatch(s); m != nil {
		hour, _ = strconv.Atoi(m[1])
		minute, _ = strconv.Atoi(m[2])
		second, _ = strconv.Atoi(m[3])
		if hour < 1 || hour > 12 {
			return 0, false
		}
		if m[4] == "am" {
			if hour == 12 {
				hour = 0 // 12:00am = 00:00
			}
		} else if hour != 12 {
			hour += 12 // 1pm = 13:00, but 12pm = 12:00
		}
	} else if m := clock24Re.FindStringSubmatch(s); m != nil {
		hour, _ = strconv.Atoi(m[1])
		minute, _ = strconv.Atoi(m[2])
		second, _ = strconv.Atoi(m[3])
		if hour > 23 {
			return 0, false
		}
	} else {
		return 0, false
	}
	if minute > 59 || second > 59 {
		return 0, false
	}
	return time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute + time.Duration(second)*time.Second, true
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
// Delegates to the server's log parsers for consistent behavior.
func ParseLogLine(line string, cfg ParserConfig, source string) LogEntry {
	// Build server parser config from CLI config
	serverCfg := config.LogParserConfig{
		Type:      cfg.Type,
		Timestamp: cfg.TimestampField,
		Level:     cfg.LevelField,
		Message:   cfg.MessageField,
	}

	parser, err := serverlogs.NewParser(serverCfg, nil)
	if err != nil {
		// Fallback: return raw line
		return LogEntry{
			Raw:       line,
			Source:    source,
			Message:   line,
			Timestamp: time.Now(),
			Fields:    make(map[string]interface{}),
		}
	}

	serverEntry := parser.Parse(line)

	// Convert server LogEntry to CLI LogEntry
	fields := make(map[string]interface{}, len(serverEntry.Fields))
	for k, v := range serverEntry.Fields {
		fields[k] = v
	}

	return LogEntry{
		Timestamp: serverEntry.Timestamp,
		Level:     string(serverEntry.Level),
		Message:   serverEntry.Message,
		Raw:       serverEntry.Raw,
		Source:    source,
		Fields:    fields,
	}
}
