// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Filter represents a parsed filter query.
type Filter struct {
	clauses []filterClause
}

// filterClause represents a single filter condition.
type filterClause struct {
	field    string       // Field name (empty for full-text search)
	op       filterOp     // Comparison operator
	values   []string     // Values to match (multiple for OR)
	negate   bool         // Negate the match
	contains bool         // Contains match (vs exact)
	regex    *regexp.Regexp // Compiled regex for contains
}

// filterOp represents a comparison operator.
type filterOp int

const (
	opEqual filterOp = iota
	opContains
	opGreater
	opGreaterOrEqual
	opLess
	opLessOrEqual
)

// ParseFilter parses a filter query string.
// Supported syntax:
//   - field:value       - Exact match
//   - field:val1,val2   - OR match
//   - field:~"text"     - Contains
//   - field:>"value"    - Greater than
//   - field:<"value"    - Less than
//   - field:>="value"   - Greater or equal
//   - field:<="value"   - Less or equal
//   - -field:value      - Exclude
//   - "quoted text"     - Message contains
//   - term1 term2       - AND (space-separated)
func ParseFilter(query string) (*Filter, error) {
	if query == "" {
		return &Filter{}, nil
	}

	var clauses []filterClause
	remaining := strings.TrimSpace(query)

	for remaining != "" {
		remaining = strings.TrimLeft(remaining, " ")
		if remaining == "" {
			break
		}

		// Handle quoted string (full-text search in message)
		if remaining[0] == '"' {
			end := strings.Index(remaining[1:], "\"")
			if end == -1 {
				return nil, fmt.Errorf("unclosed quote in filter")
			}
			text := remaining[1 : end+1]
			clauses = append(clauses, filterClause{
				field:    "",
				op:       opContains,
				values:   []string{strings.ToLower(text)},
				contains: true,
			})
			remaining = remaining[end+2:]
			continue
		}

		// Find next token boundary
		nextSpace := strings.Index(remaining, " ")
		var token string
		if nextSpace == -1 {
			token = remaining
			remaining = ""
		} else {
			token = remaining[:nextSpace]
			remaining = remaining[nextSpace+1:]
		}

		if token == "" {
			continue
		}

		clause, err := parseClause(token)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, clause)
	}

	return &Filter{clauses: clauses}, nil
}

// parseClause parses a single filter clause.
func parseClause(token string) (filterClause, error) {
	clause := filterClause{op: opEqual}

	// Check for negation
	if strings.HasPrefix(token, "-") {
		clause.negate = true
		token = token[1:]
	}

	// Find field:value separator
	colonIdx := strings.Index(token, ":")
	if colonIdx == -1 {
		// Simple word - treat as message contains
		clause.field = ""
		clause.op = opContains
		clause.values = []string{strings.ToLower(token)}
		clause.contains = true
		return clause, nil
	}

	clause.field = token[:colonIdx]
	value := token[colonIdx+1:]

	// Parse operator and value
	if strings.HasPrefix(value, "~") {
		clause.op = opContains
		clause.contains = true
		value = strings.Trim(value[1:], "\"")
		clause.values = []string{strings.ToLower(value)}
	} else if strings.HasPrefix(value, ">=") {
		clause.op = opGreaterOrEqual
		value = strings.Trim(value[2:], "\"")
		clause.values = []string{value}
	} else if strings.HasPrefix(value, "<=") {
		clause.op = opLessOrEqual
		value = strings.Trim(value[2:], "\"")
		clause.values = []string{value}
	} else if strings.HasPrefix(value, ">") {
		clause.op = opGreater
		value = strings.Trim(value[1:], "\"")
		clause.values = []string{value}
	} else if strings.HasPrefix(value, "<") {
		clause.op = opLess
		value = strings.Trim(value[1:], "\"")
		clause.values = []string{value}
	} else {
		// Check for OR values (comma-separated)
		clause.values = strings.Split(value, ",")
	}

	return clause, nil
}

// Match returns true if the entry matches the filter.
func (f *Filter) Match(entry LogEntry) bool {
	if len(f.clauses) == 0 {
		return true
	}

	// All clauses must match (AND)
	for _, clause := range f.clauses {
		if !clause.match(entry) {
			return false
		}
	}
	return true
}

// match checks if a single clause matches the entry.
func (c *filterClause) match(entry LogEntry) bool {
	result := c.matchInternal(entry)
	if c.negate {
		return !result
	}
	return result
}

// matchInternal performs the actual match logic.
func (c *filterClause) matchInternal(entry LogEntry) bool {
	// Get the field value
	var fieldValue string
	switch c.field {
	case "", "message", "msg":
		fieldValue = entry.Message
	case "level":
		fieldValue = string(entry.Level)
	case "timestamp", "ts", "time":
		return c.matchTimestamp(entry.Timestamp)
	default:
		// Look in fields
		if v, ok := entry.Fields[c.field]; ok {
			fieldValue = fmt.Sprintf("%v", v)
		} else {
			return false
		}
	}

	// Match based on operator
	switch c.op {
	case opContains:
		lowerField := strings.ToLower(fieldValue)
		for _, v := range c.values {
			if strings.Contains(lowerField, v) {
				return true
			}
		}
		return false

	case opEqual:
		for _, v := range c.values {
			if strings.EqualFold(fieldValue, v) {
				return true
			}
		}
		return false

	case opGreater, opGreaterOrEqual, opLess, opLessOrEqual:
		return c.compareValues(fieldValue, c.values[0])
	}

	return false
}

// matchTimestamp matches against the timestamp field.
func (c *filterClause) matchTimestamp(ts time.Time) bool {
	if len(c.values) == 0 {
		return false
	}

	value := c.values[0]

	// Parse relative time (e.g., "-5m", "-1h")
	var targetTime time.Time
	if strings.HasPrefix(value, "-") {
		dur, err := time.ParseDuration(value)
		if err == nil {
			targetTime = time.Now().Add(dur)
		}
	} else {
		// Try parsing as absolute time
		for _, format := range []string{
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
			"2006-01-02",
		} {
			if t, err := time.Parse(format, value); err == nil {
				targetTime = t
				break
			}
		}
	}

	if targetTime.IsZero() {
		return false
	}

	switch c.op {
	case opGreater:
		return ts.After(targetTime)
	case opGreaterOrEqual:
		return !ts.Before(targetTime)
	case opLess:
		return ts.Before(targetTime)
	case opLessOrEqual:
		return !ts.After(targetTime)
	default:
		return ts.Equal(targetTime)
	}
}

// compareValues compares two values based on the operator.
func (c *filterClause) compareValues(fieldValue, targetValue string) bool {
	// Try numeric comparison first
	if fieldNum, err1 := parseNumber(fieldValue); err1 == nil {
		if targetNum, err2 := parseNumber(targetValue); err2 == nil {
			switch c.op {
			case opGreater:
				return fieldNum > targetNum
			case opGreaterOrEqual:
				return fieldNum >= targetNum
			case opLess:
				return fieldNum < targetNum
			case opLessOrEqual:
				return fieldNum <= targetNum
			}
		}
	}

	// Fall back to string comparison
	cmp := strings.Compare(fieldValue, targetValue)
	switch c.op {
	case opGreater:
		return cmp > 0
	case opGreaterOrEqual:
		return cmp >= 0
	case opLess:
		return cmp < 0
	case opLessOrEqual:
		return cmp <= 0
	}
	return false
}

// parseNumber parses a string as a number, handling duration suffixes.
func parseNumber(s string) (float64, error) {
	// Check for duration suffix (e.g., "100ms", "5s")
	if strings.HasSuffix(s, "ms") {
		if v, err := strconv.ParseFloat(s[:len(s)-2], 64); err == nil {
			return v, nil
		}
	}
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ms") {
		if v, err := strconv.ParseFloat(s[:len(s)-1], 64); err == nil {
			return v * 1000, nil // Convert to ms for comparison
		}
	}
	if strings.HasSuffix(s, "m") && !strings.HasSuffix(s, "ms") {
		if v, err := strconv.ParseFloat(s[:len(s)-1], 64); err == nil {
			return v * 60000, nil // Convert to ms
		}
	}

	return strconv.ParseFloat(s, 64)
}

// IsEmpty returns true if the filter has no clauses.
func (f *Filter) IsEmpty() bool {
	return len(f.clauses) == 0
}

// String returns the string representation of the filter.
func (f *Filter) String() string {
	if len(f.clauses) == 0 {
		return ""
	}

	var parts []string
	for _, c := range f.clauses {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, " ")
}

// String returns the string representation of a clause.
func (c *filterClause) String() string {
	var prefix string
	if c.negate {
		prefix = "-"
	}

	var op string
	switch c.op {
	case opContains:
		op = ":~"
	case opGreater:
		op = ":>"
	case opGreaterOrEqual:
		op = ":>="
	case opLess:
		op = ":<"
	case opLessOrEqual:
		op = ":<="
	default:
		op = ":"
	}

	if c.field == "" {
		return fmt.Sprintf(`%s"%s"`, prefix, strings.Join(c.values, ","))
	}

	return fmt.Sprintf("%s%s%s%s", prefix, c.field, op, strings.Join(c.values, ","))
}
