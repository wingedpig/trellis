// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCrashAnalyzer_DetectPanic(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	logs := []string{
		"Starting server...",
		"Listening on :8080",
		"panic: runtime error: invalid memory address or nil pointer dereference",
		"[signal SIGSEGV: segmentation violation code=0x1]",
		"goroutine 1 [running]:",
		"main.main()",
		"	/app/main.go:42 +0x123",
	}

	result := analyzer.Analyze(logs, 1)

	assert.Equal(t, CrashReasonPanic, result.Reason)
	assert.Contains(t, result.Details, "nil pointer dereference")
	assert.Contains(t, result.Location, "main.go:42")
}

func TestCrashAnalyzer_DetectFatal(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	logs := []string{
		"Initializing...",
		"fatal error: concurrent map writes",
		"runtime stack:",
		"goroutine 42 [running]:",
	}

	result := analyzer.Analyze(logs, 1)

	assert.Equal(t, CrashReasonFatal, result.Reason)
	assert.Contains(t, result.Details, "concurrent map writes")
}

func TestCrashAnalyzer_DetectLogFatal(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	logs := []string{
		"Starting server...",
		"2024/01/15 10:30:00 FATAL: could not connect to database",
	}

	result := analyzer.Analyze(logs, 1)

	assert.Equal(t, CrashReasonLogFatal, result.Reason)
	assert.Contains(t, result.Details, "could not connect to database")
}

func TestCrashAnalyzer_DetectError(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	logs := []string{
		"Processing request...",
		"Error: connection refused to localhost:5432",
		"Shutting down...",
	}

	result := analyzer.Analyze(logs, 1)

	assert.Equal(t, CrashReasonError, result.Reason)
	assert.Contains(t, result.Details, "connection refused")
}

func TestCrashAnalyzer_DetectOOM(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	logs := []string{
		"Processing large dataset...",
		"fatal error: runtime: out of memory",
	}

	result := analyzer.Analyze(logs, 1)

	assert.Equal(t, CrashReasonOOM, result.Reason)
}

func TestCrashAnalyzer_DetectSignal(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	tests := []struct {
		name   string
		logs   []string
		signal string
	}{
		{
			name:   "SIGTERM",
			logs:   []string{"Received signal: SIGTERM", "Shutting down..."},
			signal: "SIGTERM",
		},
		{
			name:   "SIGKILL",
			logs:   []string{"signal: killed"},
			signal: "SIGKILL",
		},
		{
			name:   "SIGINT",
			logs:   []string{"signal: interrupt"},
			signal: "SIGINT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzer.Analyze(tt.logs, 0)
			assert.Equal(t, CrashReasonSignal, result.Reason)
			assert.Contains(t, result.Details, tt.signal)
		})
	}
}

func TestCrashAnalyzer_DetectTimeout(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	logs := []string{
		"Processing request...",
		"context deadline exceeded",
	}

	result := analyzer.Analyze(logs, 1)

	assert.Equal(t, CrashReasonTimeout, result.Reason)
}

func TestCrashAnalyzer_NoLogs(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	result := analyzer.Analyze(nil, 137)

	// Exit code 137 = 128 + 9 (SIGKILL), so should be detected as signal
	assert.Equal(t, CrashReasonSignal, result.Reason)
	assert.Equal(t, 137, result.ExitCode)
}

func TestCrashAnalyzer_CleanExit(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	logs := []string{
		"Starting server...",
		"Server running on :8080",
		"Graceful shutdown complete",
	}

	result := analyzer.Analyze(logs, 0)

	assert.Equal(t, CrashReasonNone, result.Reason)
}

func TestCrashAnalyzer_ExitCodeAnalysis(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	tests := []struct {
		name     string
		exitCode int
		expected CrashReason
	}{
		{"exit 0", 0, CrashReasonNone},
		{"exit 1", 1, CrashReasonError},
		{"exit 2", 2, CrashReasonError},
		{"SIGHUP (128+1)", 129, CrashReasonSignal},
		{"SIGINT (128+2)", 130, CrashReasonSignal},
		{"SIGKILL (128+9)", 137, CrashReasonSignal},
		{"SIGTERM (128+15)", 143, CrashReasonSignal},
		{"SIGSEGV (128+11)", 139, CrashReasonSignal},
		{"OOM (137)", 137, CrashReasonSignal}, // Often OOM killer uses SIGKILL
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzer.Analyze(nil, tt.exitCode)
			assert.Equal(t, tt.expected, result.Reason)
			assert.Equal(t, tt.exitCode, result.ExitCode)
		})
	}
}

func TestCrashAnalyzer_PanicStackTrace(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	logs := []string{
		"panic: interface conversion: interface {} is nil, not string",
		"",
		"goroutine 1 [running]:",
		"main.processData(0x0)",
		"	/app/processor.go:123 +0x456",
		"main.handleRequest(0xc0001a2000)",
		"	/app/handler.go:45 +0x789",
		"main.main()",
		"	/app/main.go:15 +0xabc",
	}

	result := analyzer.Analyze(logs, 2)

	assert.Equal(t, CrashReasonPanic, result.Reason)
	assert.Contains(t, result.Details, "interface conversion")
	assert.Contains(t, result.Location, "processor.go:123")
	assert.NotEmpty(t, result.StackTrace)
}

func TestCrashAnalyzer_MultiplePatternsFirstWins(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	// Log contains both panic and error - panic should win as it's more specific
	logs := []string{
		"Error: starting up",
		"panic: something went wrong",
		"Error: during cleanup",
	}

	result := analyzer.Analyze(logs, 1)

	assert.Equal(t, CrashReasonPanic, result.Reason)
}

func TestCrashAnalyzer_ExtractLocation(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	tests := []struct {
		name     string
		logs     []string
		expected string
	}{
		{
			name: "Go stack trace",
			logs: []string{
				"panic: test",
				"goroutine 1 [running]:",
				"main.foo()",
				"	/app/main.go:42 +0x123",
			},
			expected: "main.go:42",
		},
		{
			name: "Compiler error style",
			logs: []string{
				"Error: compilation failed",
				"./main.go:15:10: undefined: Foo",
			},
			expected: "main.go:15",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzer.Analyze(tt.logs, 1)
			assert.Contains(t, result.Location, tt.expected)
		})
	}
}

func TestCrashResult_Summary(t *testing.T) {
	result := &CrashResult{
		Reason:   CrashReasonPanic,
		Details:  "nil pointer dereference",
		Location: "main.go:42",
		ExitCode: 2,
	}

	summary := result.Summary()

	assert.Contains(t, summary, "panic")
	assert.Contains(t, summary, "nil pointer dereference")
	assert.Contains(t, summary, "main.go:42")
}

func TestCrashReason_String(t *testing.T) {
	tests := []struct {
		reason   CrashReason
		expected string
	}{
		{CrashReasonNone, "none"},
		{CrashReasonPanic, "panic"},
		{CrashReasonFatal, "fatal"},
		{CrashReasonLogFatal, "log.fatal"},
		{CrashReasonError, "error"},
		{CrashReasonOOM, "oom"},
		{CrashReasonSignal, "signal"},
		{CrashReasonTimeout, "timeout"},
		{CrashReasonUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.reason.String())
		})
	}
}

func TestCrashAnalyzer_BindAddress(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	logs := []string{
		"Starting server...",
		"listen tcp :8080: bind: address already in use",
	}

	result := analyzer.Analyze(logs, 1)

	assert.Equal(t, CrashReasonError, result.Reason)
	assert.Contains(t, result.Details, "address already in use")
}

func TestCrashAnalyzer_PermissionDenied(t *testing.T) {
	analyzer := NewCrashAnalyzer()

	logs := []string{
		"Opening file...",
		"open /etc/passwd: permission denied",
	}

	result := analyzer.Analyze(logs, 1)

	assert.Equal(t, CrashReasonError, result.Reason)
	assert.Contains(t, result.Details, "permission denied")
}
