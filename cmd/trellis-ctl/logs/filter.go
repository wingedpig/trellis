// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"fmt"
	"regexp"
	"strings"
)

// Filter filters log entries based on the provided options.
type Filter struct {
	opts        FilterOptions
	grepRegex   *regexp.Regexp
}

// NewFilter creates a new Filter with the given options.
func NewFilter(opts FilterOptions) (*Filter, error) {
	f := &Filter{opts: opts}

	// Compile grep pattern if provided
	if opts.GrepPattern != "" {
		re, err := regexp.Compile(opts.GrepPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid grep pattern: %w", err)
		}
		f.grepRegex = re
	}

	return f, nil
}

// Match returns true if the log entry matches all filter criteria.
func (f *Filter) Match(entry *LogEntry) bool {
	// Time filters
	if !f.opts.Since.IsZero() && entry.Timestamp.Before(f.opts.Since) {
		return false
	}
	if !f.opts.Until.IsZero() && entry.Timestamp.After(f.opts.Until) {
		return false
	}

	// Level filter
	if !f.matchLevel(entry) {
		return false
	}

	// Grep filter
	if !f.matchGrep(entry) {
		return false
	}

	// Field filters
	if !f.matchFields(entry) {
		return false
	}

	return true
}

// matchLevel checks if the entry matches level filter criteria.
func (f *Filter) matchLevel(entry *LogEntry) bool {
	entryLevel := GetLevelFromEntry(entry)

	// Check minimum level (level+ syntax)
	// Exclude LevelUnknown since it shouldn't pass severity-based filters
	if f.opts.MinLevel != LevelUnset {
		return entryLevel >= f.opts.MinLevel && entryLevel != LevelUnknown
	}

	// Check specific levels
	if len(f.opts.Levels) == 0 {
		return true // No level filter
	}

	for _, level := range f.opts.Levels {
		if entryLevel == level {
			return true
		}
	}
	return false
}

// matchGrep checks if the entry matches the grep pattern.
func (f *Filter) matchGrep(entry *LogEntry) bool {
	if f.grepRegex == nil {
		return true
	}

	// Search in message and raw
	if f.grepRegex.MatchString(entry.Message) {
		return true
	}
	if f.grepRegex.MatchString(entry.Raw) {
		return true
	}

	return false
}

// matchFields checks if the entry matches all field filters.
func (f *Filter) matchFields(entry *LogEntry) bool {
	if len(f.opts.FieldFilters) == 0 {
		return true
	}

	for field, expected := range f.opts.FieldFilters {
		actual, ok := f.getFieldValue(entry, field)
		if !ok {
			return false
		}
		if !f.matchFieldValue(actual, expected) {
			return false
		}
	}

	return true
}

// getFieldValue retrieves a field value from the entry.
func (f *Filter) getFieldValue(entry *LogEntry, field string) (string, bool) {
	// Check built-in fields first
	switch strings.ToLower(field) {
	case "level":
		return entry.Level, true
	case "message", "msg":
		return entry.Message, true
	case "source":
		return entry.Source, true
	case "raw":
		return entry.Raw, true
	case "timestamp", "time":
		return entry.Timestamp.String(), true
	}

	// Check custom fields
	if entry.Fields != nil {
		if val, ok := entry.Fields[field]; ok {
			return fmt.Sprintf("%v", val), true
		}
	}

	return "", false
}

// matchFieldValue checks if actual matches expected (supports wildcards).
func (f *Filter) matchFieldValue(actual, expected string) bool {
	// Exact match
	if actual == expected {
		return true
	}

	// Case-insensitive match
	if strings.EqualFold(actual, expected) {
		return true
	}

	// Wildcard support (simple glob-style)
	if strings.Contains(expected, "*") {
		pattern := "^" + regexp.QuoteMeta(expected) + "$"
		pattern = strings.ReplaceAll(pattern, `\*`, ".*")
		if re, err := regexp.Compile(pattern); err == nil {
			return re.MatchString(actual)
		}
	}

	return false
}

// FilterEntries filters a slice of log entries.
// If Before or After context is specified, includes surrounding lines for matches.
func FilterEntries(entries []LogEntry, opts FilterOptions) ([]LogEntry, error) {
	filter, err := NewFilter(opts)
	if err != nil {
		return nil, err
	}

	// If no context requested, use simple filtering
	if opts.Before == 0 && opts.After == 0 {
		var result []LogEntry
		for _, entry := range entries {
			if filter.Match(&entry) {
				result = append(result, entry)
			}
		}
		return result, nil
	}

	// Context mode: find matches first, then include surrounding lines
	// Create a filter without grep to apply time/level/field filters first
	baseOpts := opts
	baseOpts.GrepPattern = ""
	baseOpts.Before = 0
	baseOpts.After = 0
	baseFilter, err := NewFilter(baseOpts)
	if err != nil {
		return nil, err
	}

	// Apply base filters first (time, level, field)
	var baseFiltered []LogEntry
	for _, entry := range entries {
		if baseFilter.Match(&entry) {
			baseFiltered = append(baseFiltered, entry)
		}
	}

	// If no grep pattern, context doesn't make sense - return base filtered
	if opts.GrepPattern == "" {
		return baseFiltered, nil
	}

	// Find indices of grep matches
	var matchIndices []int
	for i, entry := range baseFiltered {
		if filter.matchGrep(&entry) {
			matchIndices = append(matchIndices, i)
		}
	}

	if len(matchIndices) == 0 {
		return nil, nil
	}

	// Build set of indices to include (matches + context)
	includeSet := make(map[int]bool)
	for _, matchIdx := range matchIndices {
		// Add lines before
		start := matchIdx - opts.Before
		if start < 0 {
			start = 0
		}
		// Add lines after
		end := matchIdx + opts.After
		if end >= len(baseFiltered) {
			end = len(baseFiltered) - 1
		}
		// Mark all indices in range
		for i := start; i <= end; i++ {
			includeSet[i] = true
		}
	}

	// Collect entries in order
	var result []LogEntry
	for i, entry := range baseFiltered {
		if includeSet[i] {
			result = append(result, entry)
		}
	}

	return result, nil
}
