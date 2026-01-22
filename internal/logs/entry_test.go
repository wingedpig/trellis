// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"testing"
)

func TestNormalizeLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected LogLevel
	}{
		{"trace", LevelTrace},
		{"TRACE", LevelTrace},
		{"trc", LevelTrace},
		{"debug", LevelDebug},
		{"DEBUG", LevelDebug},
		{"dbg", LevelDebug},
		{"info", LevelInfo},
		{"INFO", LevelInfo},
		{"information", LevelInfo},
		{"warn", LevelWarn},
		{"WARN", LevelWarn},
		{"warning", LevelWarn},
		{"error", LevelError},
		{"ERROR", LevelError},
		{"err", LevelError},
		{"fatal", LevelFatal},
		{"FATAL", LevelFatal},
		{"panic", LevelFatal},
		{"critical", LevelFatal},
		{"unknown", LevelInfo}, // Default
		{"", LevelInfo},        // Default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := NormalizeLevel(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeLevel(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestLogLevelSeverity(t *testing.T) {
	tests := []struct {
		level    LogLevel
		severity int
	}{
		{LevelTrace, 0},
		{LevelDebug, 1},
		{LevelInfo, 2},
		{LevelWarn, 3},
		{LevelError, 4},
		{LevelFatal, 5},
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			if tt.level.Severity() != tt.severity {
				t.Errorf("%v.Severity() = %d, want %d", tt.level, tt.level.Severity(), tt.severity)
			}
		})
	}

	// Test ordering
	if !LevelError.IsMoreSevereThan(LevelWarn) {
		t.Error("Error should be more severe than Warn")
	}
	if LevelInfo.IsMoreSevereThan(LevelWarn) {
		t.Error("Info should not be more severe than Warn")
	}
	if !LevelWarn.IsAtLeast(LevelInfo) {
		t.Error("Warn should be at least Info")
	}
	if !LevelInfo.IsAtLeast(LevelInfo) {
		t.Error("Info should be at least Info")
	}
}

func TestLogLevelString(t *testing.T) {
	if LevelError.String() != "error" {
		t.Errorf("LevelError.String() = %q, want %q", LevelError.String(), "error")
	}
}
