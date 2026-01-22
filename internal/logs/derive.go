// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package logs provides structured log viewing capabilities with multiple sources and parsers.
package logs

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// Deriver applies derive configurations to log entries, computing derived fields.
type Deriver struct {
	configs map[string]config.DeriveConfig
}

// NewDeriver creates a new Deriver from derive configurations.
func NewDeriver(configs map[string]config.DeriveConfig) *Deriver {
	return &Deriver{configs: configs}
}

// Apply computes all derived fields for a log entry and adds them to entry.Fields.
// This modifies the entry in place.
func (d *Deriver) Apply(entry *LogEntry) {
	if d == nil || len(d.configs) == 0 {
		return
	}

	if entry.Fields == nil {
		entry.Fields = make(map[string]any)
	}

	for fieldName, cfg := range d.configs {
		value := d.computeField(entry, cfg)
		if value != "" {
			entry.Fields[fieldName] = value
		}
	}
}

// computeField computes a single derived field value.
func (d *Deriver) computeField(entry *LogEntry, cfg config.DeriveConfig) string {
	switch cfg.Op {
	case "timefmt":
		return d.opTimeFmt(entry, cfg)
	case "fmt":
		return d.opFmt(entry, cfg)
	default:
		return ""
	}
}

// opTimeFmt formats a timestamp field using a Go time layout.
// Parses the input string as a timestamp and reformats it.
// Config: { from: "time", op: "timefmt", args: { format: "15:04:05.000" } }
func (d *Deriver) opTimeFmt(entry *LogEntry, cfg config.DeriveConfig) string {
	format, _ := cfg.Args["format"].(string)
	if format == "" {
		format = "15:04:05" // default format
	}

	// Get source value - first try fields, then fall back to entry.Timestamp
	var t time.Time

	if cfg.From != "" {
		// Try to get from fields first
		if v, ok := entry.Fields[cfg.From]; ok {
			switch tv := v.(type) {
			case time.Time:
				t = tv
			case string:
				// Parse the timestamp string
				t = parseTimeString(tv)
				if t.IsZero() {
					return "" // Can't parse, return empty
				}
			default:
				// Try string conversion for other types
				if s := stringValue(v); s != "" {
					t = parseTimeString(s)
				}
			}
		}
	}

	// Fall back to entry.Timestamp if no value found from fields
	// This handles cases where the parser extracted the timestamp field
	// (e.g., "time" field gets parsed into entry.Timestamp and removed from Fields)
	if t.IsZero() {
		t = entry.Timestamp
	}

	if t.IsZero() {
		return ""
	}
	return t.Format(format)
}

// opFmt formats using a template with {field} placeholders.
// Config: { op: "fmt", args: { template: "{basename(file)}:{line}" } }
func (d *Deriver) opFmt(entry *LogEntry, cfg config.DeriveConfig) string {
	template, _ := cfg.Args["template"].(string)
	if template == "" {
		return ""
	}

	// Replace {field} and {func(field)} placeholders
	re := regexp.MustCompile(`\{([^}]+)\}`)
	result := re.ReplaceAllStringFunc(template, func(match string) string {
		// Remove braces
		expr := match[1 : len(match)-1]
		return d.evaluateExpr(entry, expr)
	})

	return result
}

// evaluateExpr evaluates a template expression like "field" or "basename(field)".
func (d *Deriver) evaluateExpr(entry *LogEntry, expr string) string {
	// Check for function call: func(field)
	if idx := strings.Index(expr, "("); idx > 0 && strings.HasSuffix(expr, ")") {
		funcName := expr[:idx]
		arg := expr[idx+1 : len(expr)-1]
		fieldValue := d.getFieldValue(entry, arg)
		return d.applyFunction(funcName, fieldValue)
	}

	// Simple field reference
	return d.getFieldValue(entry, expr)
}

// getFieldValue gets a field value from the entry.
func (d *Deriver) getFieldValue(entry *LogEntry, field string) string {
	// Check built-in fields first
	switch field {
	case "timestamp", "time":
		return entry.Timestamp.Format(time.RFC3339Nano)
	case "level":
		return string(entry.Level)
	case "message", "msg":
		return entry.Message
	case "raw":
		return entry.Raw
	case "source":
		return entry.Source
	}

	// Check custom fields
	if v, ok := entry.Fields[field]; ok {
		return stringValue(v)
	}

	return ""
}

// stringValue converts a value to string.
func stringValue(v any) string {
	switch tv := v.(type) {
	case string:
		return tv
	case int:
		return intToString(tv)
	case int32:
		return intToString(int(tv))
	case int64:
		return int64ToString(tv)
	case uint:
		return int64ToString(int64(tv))
	case uint32:
		return int64ToString(int64(tv))
	case uint64:
		return int64ToString(int64(tv))
	case float32:
		return float64ToString(float64(tv))
	case float64:
		return float64ToString(tv)
	case bool:
		if tv {
			return "true"
		}
		return "false"
	default:
		// Handle json.Number and other types with String() method
		if s, ok := v.(interface{ String() string }); ok {
			return s.String()
		}
		return ""
	}
}

func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func int64ToString(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func float64ToString(f float64) string {
	// Simple conversion - handles common cases
	if f == float64(int64(f)) {
		return int64ToString(int64(f))
	}
	// For non-integers, use a simple approach
	// This is a simplified version; production code might use strconv
	intPart := int64(f)
	fracPart := f - float64(intPart)
	if fracPart < 0 {
		fracPart = -fracPart
	}
	// Get 3 decimal places
	fracPart *= 1000
	fracInt := int64(fracPart + 0.5)
	result := int64ToString(intPart) + "."
	if fracInt < 10 {
		result += "00"
	} else if fracInt < 100 {
		result += "0"
	}
	result += int64ToString(fracInt)
	return strings.TrimRight(strings.TrimRight(result, "0"), ".")
}

// applyFunction applies a function to a value.
func (d *Deriver) applyFunction(funcName, value string) string {
	switch funcName {
	case "basename":
		return filepath.Base(value)
	case "dirname":
		return filepath.Dir(value)
	case "upper":
		return strings.ToUpper(value)
	case "lower":
		return strings.ToLower(value)
	case "trim":
		return strings.TrimSpace(value)
	default:
		return value
	}
}

// parseTimeString attempts to parse a time string in common formats.
func parseTimeString(s string) time.Time {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		"Jan 2 15:04:05",
		"Jan  2 15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t
		}
	}

	return time.Time{}
}
