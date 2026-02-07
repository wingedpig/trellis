// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple command",
			input:    "go build",
			expected: []string{"go", "build"},
		},
		{
			name:     "command with multiple spaces",
			input:    "go   build   ./...",
			expected: []string{"go", "build", "./..."},
		},
		{
			name:     "double quoted argument",
			input:    `go test -run "Test Foo"`,
			expected: []string{"go", "test", "-run", "Test Foo"},
		},
		{
			name:     "single quoted argument",
			input:    `echo 'hello world'`,
			expected: []string{"echo", "hello world"},
		},
		{
			name:     "mixed quotes",
			input:    `cmd "arg one" 'arg two'`,
			expected: []string{"cmd", "arg one", "arg two"},
		},
		{
			name:     "escaped space",
			input:    `cmd arg\ with\ spaces`,
			expected: []string{"cmd", "arg with spaces"},
		},
		{
			name:     "escaped quote in double quotes",
			input:    `echo "hello \"world\""`,
			expected: []string{"echo", `hello "world"`},
		},
		{
			name:     "empty quoted string skipped",
			input:    `cmd "" arg`,
			expected: []string{"cmd", "arg"},
		},
		{
			name:     "tabs as separators",
			input:    "cmd\targ1\targ2",
			expected: []string{"cmd", "arg1", "arg2"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "only whitespace",
			input:    "   \t  ",
			expected: nil,
		},
		{
			name:     "path with spaces in quotes",
			input:    `"/path/to/my program" --config "/etc/my config.json"`,
			expected: []string{"/path/to/my program", "--config", "/etc/my config.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitCommand(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestServiceConfig_GetCommand(t *testing.T) {
	tests := []struct {
		name     string
		command  interface{}
		expected []string
	}{
		{
			name:     "string command",
			command:  "go build ./...",
			expected: []string{"go", "build", "./..."},
		},
		{
			name:     "string command with quotes",
			command:  `go test -run "Test Foo"`,
			expected: []string{"go", "test", "-run", "Test Foo"},
		},
		{
			name:     "array command",
			command:  []interface{}{"go", "build", "./..."},
			expected: []string{"go", "build", "./..."},
		},
		{
			name:     "string slice",
			command:  []string{"go", "build"},
			expected: []string{"go", "build"},
		},
		{
			name:     "nil command",
			command:  nil,
			expected: nil,
		},
		{
			name:     "empty string",
			command:  "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &ServiceConfig{Command: tt.command}
			result := svc.GetCommand()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildServiceIDFields(t *testing.T) {
	defaults := &LoggingDefaultsConfig{
		Parser: LogParserConfig{
			ID: "trace_id",
		},
	}

	services := []ServiceConfig{
		{
			Name: "api",
			// No logging config - should inherit from defaults
		},
		{
			Name: "worker",
			Logging: ServiceLoggingConfig{
				Parser: LogParserConfig{
					ID: "job_id", // Override ID field
				},
			},
		},
		{
			Name: "legacy",
			Logging: ServiceLoggingConfig{
				Parser: LogParserConfig{
					// Empty ID - should inherit from defaults
				},
			},
		},
	}

	result := BuildServiceIDFields(services, defaults)

	// api uses default trace_id
	assert.Equal(t, "trace_id", result["api"])
	// worker has override
	assert.Equal(t, "job_id", result["worker"])
	// legacy inherits from defaults
	assert.Equal(t, "trace_id", result["legacy"])
}

func TestBuildServiceIDFields_NilDefaults(t *testing.T) {
	services := []ServiceConfig{
		{
			Name: "api",
			Logging: ServiceLoggingConfig{
				Parser: LogParserConfig{
					ID: "request_id",
				},
			},
		},
	}

	result := BuildServiceIDFields(services, nil)

	// Only services with explicit ID should be in map
	assert.Equal(t, "request_id", result["api"])
}

func TestServiceLoggingApplyDefaults(t *testing.T) {
	defaults := &LoggingDefaultsConfig{
		Parser: LogParserConfig{
			Type:      "json",
			Timestamp: "ts",
			Level:     "level",
			Message:   "msg",
			ID:        "trace_id",
			Stack:     "stack",
			File:      "source",
			Line:      "lineno",
		},
	}

	t.Run("empty config inherits all defaults", func(t *testing.T) {
		cfg := ServiceLoggingConfig{}
		cfg.ApplyDefaults(defaults)

		assert.Equal(t, "json", cfg.Parser.Type)
		assert.Equal(t, "ts", cfg.Parser.Timestamp)
		assert.Equal(t, "level", cfg.Parser.Level)
		assert.Equal(t, "msg", cfg.Parser.Message)
		assert.Equal(t, "trace_id", cfg.Parser.ID)
		assert.Equal(t, "stack", cfg.Parser.Stack)
		assert.Equal(t, "source", cfg.Parser.File)
		assert.Equal(t, "lineno", cfg.Parser.Line)
	})

	t.Run("service overrides take precedence", func(t *testing.T) {
		cfg := ServiceLoggingConfig{
			Parser: LogParserConfig{
				File: "file",
				Line: "line",
			},
		}
		cfg.ApplyDefaults(defaults)

		// Overridden fields keep service values
		assert.Equal(t, "file", cfg.Parser.File)
		assert.Equal(t, "line", cfg.Parser.Line)
		// Non-overridden fields inherit defaults
		assert.Equal(t, "json", cfg.Parser.Type)
		assert.Equal(t, "ts", cfg.Parser.Timestamp)
		assert.Equal(t, "trace_id", cfg.Parser.ID)
		assert.Equal(t, "stack", cfg.Parser.Stack)
	})

	t.Run("service with own parser type skips merge", func(t *testing.T) {
		cfg := ServiceLoggingConfig{
			Parser: LogParserConfig{
				Type:      "logfmt",
				Timestamp: "time",
			},
		}
		cfg.ApplyDefaults(defaults)

		// When parser type is set, no merge happens
		assert.Equal(t, "logfmt", cfg.Parser.Type)
		assert.Equal(t, "time", cfg.Parser.Timestamp)
		assert.Equal(t, "", cfg.Parser.File)
		assert.Equal(t, "", cfg.Parser.Line)
	})

	t.Run("nil defaults is safe", func(t *testing.T) {
		cfg := ServiceLoggingConfig{}
		cfg.ApplyDefaults(nil)
		assert.Equal(t, "", cfg.Parser.Type)
	})
}

func TestLogViewerApplyDefaults(t *testing.T) {
	defaults := &LoggingDefaultsConfig{
		Parser: LogParserConfig{
			Type:  "json",
			File:  "source",
			Line:  "lineno",
			Stack: "stacktrace",
		},
	}

	t.Run("empty config inherits all defaults", func(t *testing.T) {
		cfg := LogViewerConfig{}
		cfg.ApplyDefaults(defaults)

		assert.Equal(t, "json", cfg.Parser.Type)
		assert.Equal(t, "source", cfg.Parser.File)
		assert.Equal(t, "lineno", cfg.Parser.Line)
		assert.Equal(t, "stacktrace", cfg.Parser.Stack)
	})

	t.Run("viewer overrides take precedence", func(t *testing.T) {
		cfg := LogViewerConfig{
			Parser: LogParserConfig{
				File: "filepath",
			},
		}
		cfg.ApplyDefaults(defaults)

		assert.Equal(t, "filepath", cfg.Parser.File)
		assert.Equal(t, "lineno", cfg.Parser.Line)
	})

	t.Run("viewer with own parser type skips merge", func(t *testing.T) {
		cfg := LogViewerConfig{
			Parser: LogParserConfig{
				Type: "regex",
			},
		}
		cfg.ApplyDefaults(defaults)

		assert.Equal(t, "regex", cfg.Parser.Type)
		assert.Equal(t, "", cfg.Parser.File)
	})
}
