// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPatternMatcher_Match(t *testing.T) {
	matcher := NewPatternMatcher()

	tests := []struct {
		name      string
		pattern   string
		eventType string
		matches   bool
	}{
		// Exact matches
		{
			name:      "exact match",
			pattern:   "service.started",
			eventType: "service.started",
			matches:   true,
		},
		{
			name:      "exact no match",
			pattern:   "service.started",
			eventType: "service.stopped",
			matches:   false,
		},

		// Wildcard at end (service.*)
		{
			name:      "wildcard end matches started",
			pattern:   "service.*",
			eventType: "service.started",
			matches:   true,
		},
		{
			name:      "wildcard end matches crashed",
			pattern:   "service.*",
			eventType: "service.crashed",
			matches:   true,
		},
		{
			name:      "wildcard end no match different prefix",
			pattern:   "service.*",
			eventType: "workflow.finished",
			matches:   false,
		},

		// Wildcard at start (*.finished)
		{
			name:      "wildcard start matches workflow",
			pattern:   "*.finished",
			eventType: "workflow.finished",
			matches:   true,
		},
		{
			name:      "wildcard start matches service",
			pattern:   "*.finished",
			eventType: "service.finished",
			matches:   true,
		},
		{
			name:      "wildcard start no match different suffix",
			pattern:   "*.finished",
			eventType: "workflow.started",
			matches:   false,
		},

		// Match all
		{
			name:      "match all",
			pattern:   "*",
			eventType: "anything.here",
			matches:   true,
		},
		{
			name:      "match all single word",
			pattern:   "*",
			eventType: "event",
			matches:   true,
		},

		// Nested events
		{
			name:      "wildcard end nested",
			pattern:   "worktree.*",
			eventType: "worktree.hook.started",
			matches:   true,
		},
		{
			name:      "exact nested match",
			pattern:   "worktree.hook.started",
			eventType: "worktree.hook.started",
			matches:   true,
		},
		{
			name:      "exact nested no match",
			pattern:   "worktree.hook.started",
			eventType: "worktree.hook.finished",
			matches:   false,
		},

		// Edge cases
		{
			name:      "empty pattern",
			pattern:   "",
			eventType: "service.started",
			matches:   false,
		},
		{
			name:      "empty event type",
			pattern:   "service.*",
			eventType: "",
			matches:   false,
		},
		{
			name:      "both empty",
			pattern:   "",
			eventType: "",
			matches:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matcher.Match(tt.eventType, tt.pattern)
			assert.Equal(t, tt.matches, result)
		})
	}
}

func TestPatternMatcher_Compile(t *testing.T) {
	matcher := NewPatternMatcher()

	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{"exact pattern", "service.started", false},
		{"wildcard end", "service.*", false},
		{"wildcard start", "*.finished", false},
		{"match all", "*", false},
		{"empty pattern", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled, err := matcher.Compile(tt.pattern)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, compiled)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, compiled)
			}
		})
	}
}

func TestCompiledPattern_Match(t *testing.T) {
	matcher := NewPatternMatcher()

	// Compile pattern once, match multiple times
	pattern, err := matcher.Compile("service.*")
	require.NoError(t, err)

	tests := []struct {
		eventType string
		matches   bool
	}{
		{"service.started", true},
		{"service.stopped", true},
		{"service.crashed", true},
		{"workflow.started", false},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			assert.Equal(t, tt.matches, pattern.Match(tt.eventType))
		})
	}
}

func TestPatternMatcher_MatchMultiplePatterns(t *testing.T) {
	matcher := NewPatternMatcher()

	// Test matching against multiple patterns
	patterns := []string{"service.started", "service.crashed", "workflow.*"}

	tests := []struct {
		eventType string
		matches   bool
	}{
		{"service.started", true},
		{"service.crashed", true},
		{"service.stopped", false},
		{"workflow.started", true},
		{"workflow.finished", true},
		{"worktree.activated", false},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			matched := false
			for _, pattern := range patterns {
				if matcher.Match(tt.eventType, pattern) {
					matched = true
					break
				}
			}
			assert.Equal(t, tt.matches, matched)
		})
	}
}

func TestPatternMatcher_Concurrency(t *testing.T) {
	matcher := NewPatternMatcher()

	// Compile pattern
	pattern, err := matcher.Compile("service.*")
	require.NoError(t, err)

	// Test concurrent matching
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			for j := 0; j < 1000; j++ {
				pattern.Match("service.started")
				matcher.Match("service.stopped", "service.*")
			}
			done <- true
		}()
	}

	for i := 0; i < 100; i++ {
		<-done
	}
}
