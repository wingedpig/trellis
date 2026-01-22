// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"errors"
	"strings"
)

// PatternMatcher handles event pattern matching.
type PatternMatcher struct{}

// NewPatternMatcher creates a new pattern matcher.
func NewPatternMatcher() *PatternMatcher {
	return &PatternMatcher{}
}

// Match checks if an event type matches a pattern.
// Patterns support wildcards:
// - "service.*" matches "service.started", "service.crashed", etc.
// - "*.finished" matches "workflow.finished", "service.finished", etc.
// - "*" matches everything
func (pm *PatternMatcher) Match(eventType, pattern string) bool {
	if pattern == "" || eventType == "" {
		return false
	}

	// Match all
	if pattern == "*" {
		return true
	}

	// Exact match
	if pattern == eventType {
		return true
	}

	// Wildcard at end (service.*)
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, ".*")
		return strings.HasPrefix(eventType, prefix+".")
	}

	// Wildcard at start (*.finished)
	if strings.HasPrefix(pattern, "*.") {
		suffix := strings.TrimPrefix(pattern, "*.")
		return strings.HasSuffix(eventType, "."+suffix)
	}

	return false
}

// Compile pre-compiles a pattern for efficient matching.
func (pm *PatternMatcher) Compile(pattern string) (CompiledPattern, error) {
	if pattern == "" {
		return nil, errors.New("empty pattern")
	}

	return &compiledPattern{
		pattern: pattern,
		matcher: pm,
	}, nil
}

// CompiledPattern is a pre-compiled pattern for efficient matching.
type CompiledPattern interface {
	Match(eventType string) bool
}

type compiledPattern struct {
	pattern string
	matcher *PatternMatcher
}

func (cp *compiledPattern) Match(eventType string) bool {
	return cp.matcher.Match(eventType, cp.pattern)
}
