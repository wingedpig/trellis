// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

func TestJSONParser(t *testing.T) {
	cfg := config.LogParserConfig{
		Type:      "json",
		Timestamp: "ts",
		Level:     "level",
		Message:   "msg",
	}
	parser := NewJSONParser(cfg)

	t.Run("basic parsing", func(t *testing.T) {
		line := `{"ts":"2024-01-15T10:30:00Z","level":"error","msg":"Connection failed","host":"db01"}`
		entry := parser.Parse(line)

		if entry.Level != LevelError {
			t.Errorf("Level = %v, want %v", entry.Level, LevelError)
		}
		if entry.Message != "Connection failed" {
			t.Errorf("Message = %q, want %q", entry.Message, "Connection failed")
		}
		if entry.Fields["host"] != "db01" {
			t.Errorf("Fields[host] = %v, want %v", entry.Fields["host"], "db01")
		}
		if entry.Raw != line {
			t.Errorf("Raw = %q, want %q", entry.Raw, line)
		}
	})

	t.Run("unix timestamp", func(t *testing.T) {
		line := `{"ts":1705315800,"level":"info","msg":"Test"}`
		entry := parser.Parse(line)

		if entry.Level != LevelInfo {
			t.Errorf("Level = %v, want %v", entry.Level, LevelInfo)
		}
	})

	t.Run("unix timestamp milliseconds", func(t *testing.T) {
		line := `{"ts":1705315800000,"level":"info","msg":"Test"}`
		entry := parser.Parse(line)

		if entry.Level != LevelInfo {
			t.Errorf("Level = %v, want %v", entry.Level, LevelInfo)
		}
	})

	t.Run("message aliases", func(t *testing.T) {
		parser2 := NewJSONParser(config.LogParserConfig{Type: "json"})
		line := `{"log":"Test message"}`
		entry := parser2.Parse(line)

		if entry.Message != "Test message" {
			t.Errorf("Message = %q, want %q", entry.Message, "Test message")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		line := "not json at all"
		entry := parser.Parse(line)

		if entry.Message != line {
			t.Errorf("Message = %q, want %q", entry.Message, line)
		}
		if entry.Raw != line {
			t.Errorf("Raw = %q, want %q", entry.Raw, line)
		}
	})
}

func TestLogfmtParser(t *testing.T) {
	cfg := config.LogParserConfig{
		Type:      "logfmt",
		Timestamp: "time",
		Level:     "level",
		Message:   "msg",
	}
	parser := NewLogfmtParser(cfg)

	t.Run("basic parsing", func(t *testing.T) {
		line := `time=2024-01-15T10:30:00Z level=error msg="Connection failed" host=db01`
		entry := parser.Parse(line)

		if entry.Level != LevelError {
			t.Errorf("Level = %v, want %v", entry.Level, LevelError)
		}
		if entry.Message != "Connection failed" {
			t.Errorf("Message = %q, want %q", entry.Message, "Connection failed")
		}
		if entry.Fields["host"] != "db01" {
			t.Errorf("Fields[host] = %v, want %v", entry.Fields["host"], "db01")
		}
	})

	t.Run("quoted values with spaces", func(t *testing.T) {
		line := `level=info msg="Hello world" path="/var/log/app.log"`
		entry := parser.Parse(line)

		if entry.Message != "Hello world" {
			t.Errorf("Message = %q, want %q", entry.Message, "Hello world")
		}
		if entry.Fields["path"] != "/var/log/app.log" {
			t.Errorf("Fields[path] = %v, want %v", entry.Fields["path"], "/var/log/app.log")
		}
	})

	t.Run("escaped quotes", func(t *testing.T) {
		line := `level=info msg="Say \"Hello\""`
		entry := parser.Parse(line)

		if entry.Message != `Say "Hello"` {
			t.Errorf("Message = %q, want %q", entry.Message, `Say "Hello"`)
		}
	})
}

func TestRegexParser(t *testing.T) {
	cfg := config.LogParserConfig{
		Type:            "regex",
		Pattern:         `^\[(?P<timestamp>[^\]]+)\] (?P<level>\w+): (?P<message>.*)$`,
		TimestampFormat: "2006-01-02 15:04:05",
	}
	parser, err := NewRegexParser(cfg)
	if err != nil {
		t.Fatalf("NewRegexParser failed: %v", err)
	}

	t.Run("basic parsing", func(t *testing.T) {
		line := "[2024-01-15 10:30:00] ERROR: Connection failed"
		entry := parser.Parse(line)

		if entry.Level != LevelError {
			t.Errorf("Level = %v, want %v", entry.Level, LevelError)
		}
		if entry.Message != "Connection failed" {
			t.Errorf("Message = %q, want %q", entry.Message, "Connection failed")
		}
	})

	t.Run("no match", func(t *testing.T) {
		line := "random log line"
		entry := parser.Parse(line)

		if entry.Message != line {
			t.Errorf("Message = %q, want %q", entry.Message, line)
		}
	})

	t.Run("invalid pattern", func(t *testing.T) {
		_, err := NewRegexParser(config.LogParserConfig{
			Type:    "regex",
			Pattern: "[invalid",
		})
		if err == nil {
			t.Error("Expected error for invalid pattern")
		}
	})
}

func TestSyslogParser(t *testing.T) {
	parser := NewSyslogParser(config.LogParserConfig{Type: "syslog"})

	t.Run("rfc3164 format", func(t *testing.T) {
		line := "<13>Jan 15 10:30:00 myhost myapp: Connection established"
		entry := parser.Parse(line)

		if entry.Message != "Connection established" {
			t.Errorf("Message = %q, want %q", entry.Message, "Connection established")
		}
		if entry.Fields["hostname"] != "myhost" {
			t.Errorf("hostname = %v, want %v", entry.Fields["hostname"], "myhost")
		}
		if entry.Fields["tag"] != "myapp" {
			t.Errorf("tag = %v, want %v", entry.Fields["tag"], "myapp")
		}
	})

	t.Run("priority to level", func(t *testing.T) {
		// Priority 11 = facility 1 (user) * 8 + severity 3 (error)
		line := "<11>Jan 15 10:30:00 host app: Error message"
		entry := parser.Parse(line)

		if entry.Level != LevelError {
			t.Errorf("Level = %v, want %v", entry.Level, LevelError)
		}
	})
}

func TestNoneParser(t *testing.T) {
	parser := NewNoneParser()

	line := "raw log line without any structure"
	entry := parser.Parse(line)

	if entry.Message != line {
		t.Errorf("Message = %q, want %q", entry.Message, line)
	}
	if entry.Raw != line {
		t.Errorf("Raw = %q, want %q", entry.Raw, line)
	}
	if entry.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestNewParser(t *testing.T) {
	tests := []struct {
		parserType string
		pattern    string // For regex parser
		shouldErr  bool
	}{
		{"json", "", false},
		{"logfmt", "", false},
		{"regex", "", true},    // No pattern provided - should error
		{"regex", ".*", false}, // With pattern - should succeed
		{"syslog", "", false},
		{"none", "", false},
		{"", "", false},        // Default to json
		{"unknown", "", true},
	}

	for _, tt := range tests {
		name := tt.parserType
		if tt.pattern != "" {
			name = tt.parserType + "_with_pattern"
		}
		t.Run(name, func(t *testing.T) {
			cfg := config.LogParserConfig{Type: tt.parserType, Pattern: tt.pattern}
			_, err := NewParser(cfg)
			if (err != nil) != tt.shouldErr {
				t.Errorf("NewParser(%q) error = %v, shouldErr = %v", tt.parserType, err, tt.shouldErr)
			}
		})
	}
}

func TestBaseParserTimestamp(t *testing.T) {
	bp := &BaseParser{}

	tests := []struct {
		input    string
		hasError bool
	}{
		{"2024-01-15T10:30:00Z", false},
		{"2024-01-15T10:30:00.000Z", false},
		{"2024-01-15 10:30:00", false},
		{"Jan 15 10:30:00", false},
		{"1705315800", false},      // Unix seconds
		{"1705315800000", false},   // Unix milliseconds
		{"invalid", true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ts, err := bp.ParseTimestamp(tt.input)
			if (err != nil) != tt.hasError {
				t.Errorf("ParseTimestamp(%q) error = %v, hasError = %v", tt.input, err, tt.hasError)
			}
			if !tt.hasError && tt.input != "" && ts.IsZero() {
				t.Errorf("ParseTimestamp(%q) returned zero time", tt.input)
			}
		})
	}
}

func TestBaseParserCustomFormat(t *testing.T) {
	bp := &BaseParser{
		cfg: config.LogParserConfig{
			TimestampFormat: "02/01/2006 15:04:05",
		},
	}

	ts, err := bp.ParseTimestamp("15/01/2024 10:30:00")
	if err != nil {
		t.Errorf("ParseTimestamp failed: %v", err)
	}
	expected := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	if !ts.Equal(expected) {
		t.Errorf("ParseTimestamp = %v, want %v", ts, expected)
	}
}

func TestParserNames(t *testing.T) {
	t.Run("json parser name", func(t *testing.T) {
		p := NewJSONParser(config.LogParserConfig{})
		if p.Name() != "json" {
			t.Errorf("Name() = %q, want %q", p.Name(), "json")
		}
	})

	t.Run("logfmt parser name", func(t *testing.T) {
		p := NewLogfmtParser(config.LogParserConfig{})
		if p.Name() != "logfmt" {
			t.Errorf("Name() = %q, want %q", p.Name(), "logfmt")
		}
	})

	t.Run("regex parser name", func(t *testing.T) {
		p, _ := NewRegexParser(config.LogParserConfig{Pattern: ".*"})
		if p.Name() != "regex" {
			t.Errorf("Name() = %q, want %q", p.Name(), "regex")
		}
	})

	t.Run("syslog parser name", func(t *testing.T) {
		p := NewSyslogParser(config.LogParserConfig{})
		if p.Name() != "syslog" {
			t.Errorf("Name() = %q, want %q", p.Name(), "syslog")
		}
	})

	t.Run("none parser name", func(t *testing.T) {
		p := NewNoneParser()
		if p.Name() != "none" {
			t.Errorf("Name() = %q, want %q", p.Name(), "none")
		}
	})
}

func TestSyslogParserPriorityLevels(t *testing.T) {
	parser := NewSyslogParser(config.LogParserConfig{Type: "syslog"})

	tests := []struct {
		name     string
		line     string
		expected LogLevel
	}{
		{"emergency", "<8>Jan 15 10:30:00 host app: Emergency", LevelFatal},  // 8 = facility 1 * 8 + 0
		{"alert", "<9>Jan 15 10:30:00 host app: Alert", LevelFatal},          // 8 = facility 1 * 8 + 1
		{"critical", "<10>Jan 15 10:30:00 host app: Critical", LevelFatal},   // 10 = facility 1 * 8 + 2
		{"error", "<11>Jan 15 10:30:00 host app: Error", LevelError},         // 11 = facility 1 * 8 + 3
		{"warning", "<12>Jan 15 10:30:00 host app: Warning", LevelWarn},      // 12 = facility 1 * 8 + 4
		{"notice", "<13>Jan 15 10:30:00 host app: Notice", LevelInfo},        // 13 = facility 1 * 8 + 5
		{"info", "<14>Jan 15 10:30:00 host app: Info", LevelInfo},            // 14 = facility 1 * 8 + 6
		{"debug", "<15>Jan 15 10:30:00 host app: Debug", LevelDebug},         // 15 = facility 1 * 8 + 7
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := parser.Parse(tt.line)
			if entry.Level != tt.expected {
				t.Errorf("Level = %v, want %v", entry.Level, tt.expected)
			}
		})
	}
}

func TestSyslogParserNoMatch(t *testing.T) {
	parser := NewSyslogParser(config.LogParserConfig{Type: "syslog"})

	line := "not a syslog formatted line"
	entry := parser.Parse(line)

	if entry.Message != line {
		t.Errorf("Message = %q, want %q", entry.Message, line)
	}
	if entry.Raw != line {
		t.Errorf("Raw = %q, want %q", entry.Raw, line)
	}
}

func TestLogfmtParserEdgeCases(t *testing.T) {
	parser := NewLogfmtParser(config.LogParserConfig{
		Type:    "logfmt",
		Message: "msg",
		Level:   "level",
	})

	t.Run("empty value", func(t *testing.T) {
		line := `level=info msg="" key=value`
		entry := parser.Parse(line)

		// Level is extracted to entry.Level (not kept in Fields)
		if entry.Level != LevelInfo {
			t.Errorf("Level = %v, want %v", entry.Level, LevelInfo)
		}
		if entry.Message != "" {
			t.Errorf("Message = %q, want empty", entry.Message)
		}
		// key should be in fields
		if entry.Fields["key"] != "value" {
			t.Errorf("Fields[key] = %v, want value", entry.Fields["key"])
		}
	})

	t.Run("no message field", func(t *testing.T) {
		line := `level=info key=value`
		entry := parser.Parse(line)

		if entry.Message != "" {
			t.Errorf("Message = %q, want empty", entry.Message)
		}
	})

	t.Run("unquoted value with equals", func(t *testing.T) {
		line := `level=info url=http://example.com`
		entry := parser.Parse(line)

		if entry.Level != LevelInfo {
			t.Errorf("Level = %v, want %v", entry.Level, LevelInfo)
		}
		// The URL gets parsed as url=http with the rest being lost (known limitation)
	})
}

func TestRegexParserNamedGroups(t *testing.T) {
	cfg := config.LogParserConfig{
		Type:    "regex",
		Pattern: `^(?P<severity>\w+) (?P<component>\w+): (?P<message>.*)$`,
	}
	parser, err := NewRegexParser(cfg)
	if err != nil {
		t.Fatalf("NewRegexParser failed: %v", err)
	}

	line := "ERROR database: Connection timeout"
	entry := parser.Parse(line)

	if entry.Fields["severity"] != "ERROR" {
		t.Errorf("severity = %v, want ERROR", entry.Fields["severity"])
	}
	if entry.Fields["component"] != "database" {
		t.Errorf("component = %v, want database", entry.Fields["component"])
	}
	if entry.Message != "Connection timeout" {
		t.Errorf("Message = %q, want %q", entry.Message, "Connection timeout")
	}
}
