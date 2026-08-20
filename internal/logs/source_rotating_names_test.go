// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"regexp"
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// TestBuildTimestampGrepPatternSpaceSeparator verifies the remote grep window
// matches logs that separate the date and time with a space instead of an ISO
// "T" — postgres's %t prefix, for example. A "T"-only pattern silently matched
// nothing on those files, so every historical range query came back empty.
func TestBuildTimestampGrepPatternSpaceSeparator(t *testing.T) {
	src, err := NewSSHSource(config.LogSourceConfig{Type: "ssh", Host: "h", Path: "/p"}, time.UTC)
	if err != nil {
		t.Fatalf("NewSSHSource: %v", err)
	}
	start := time.Date(2026, 7, 22, 15, 30, 0, 0, time.UTC)
	end := time.Date(2026, 7, 22, 15, 45, 0, 0, time.UTC)
	re, err := regexp.Compile(src.buildTimestampGrepPattern(start, end))
	if err != nil {
		t.Fatalf("compiling pattern: %v", err)
	}
	pg := "2026-07-22 15:31:02 UTC [12345]: [3-1] user=groupsio,db=groupsio,app=web,client=10.1.2.3 LOG:  duration: 2443.123 ms"
	if !re.MatchString(pg) {
		t.Errorf("pattern %q did not match postgres line %q", re, pg)
	}
	iso := `{"time":"2026-07-22T15:31:02Z","level":"info","msg":"hi"}`
	if !re.MatchString(iso) {
		t.Errorf("pattern %q no longer matches ISO-8601 line %q", re, iso)
	}
	if re.MatchString("2026-07-22 16:31:02 UTC [12345]: [3-1] user=,db=, LOG:  out of window") {
		t.Errorf("pattern %q matched an hour outside the window", re)
	}
}

// TestActivePatternTailCommand verifies the remote tail for a source that
// cycles through a set of filenames follows the whole set (so a rollover to a
// different name needs no restart), suppresses tail's per-file banners, and
// starts at end-of-file rather than replaying every file in the set.
func TestActivePatternTailCommand(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.LogSourceConfig
		want string
	}{
		{
			name: "active pattern follows the set from end-of-file",
			cfg:  config.LogSourceConfig{Type: "ssh", Host: "h", Path: "/var/log/postgresql", ActivePattern: "postgresql-*.log"},
			want: "tail -F -q -n 0 /var/log/postgresql/postgresql-*.log",
		},
		{
			name: "stable current file keeps the seeded tail",
			cfg:  config.LogSourceConfig{Type: "ssh", Host: "h", Path: "/var/log/app", Current: "current"},
			want: "tail -F -n 1000 /var/log/app/current",
		},
	}
	for _, tc := range tests {
		src, err := NewSSHSource(tc.cfg, time.UTC)
		if err != nil {
			t.Fatalf("%s: NewSSHSource: %v", tc.name, err)
		}
		if got := src.tailCommand(); got != tc.want {
			t.Errorf("%s: tailCommand() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPostgresPrefixParses verifies the regex parser the postgres viewers use
// pulls the timestamp, level, and connection identity out of a line written
// with log_line_prefix = '%t [%p]: [%l-1] user=%u,db=%d,app=%a,client=%h '.
func TestPostgresPrefixParses(t *testing.T) {
	cfg := config.LogParserConfig{
		Type:            "regex",
		Pattern:         postgresLinePattern,
		Timestamp:       "timestamp",
		Level:           "level",
		Message:         "message",
		TimestampFormat: "2006-01-02 15:04:05 MST",
	}
	p, err := NewRegexParser(cfg)
	if err != nil {
		t.Fatalf("NewRegexParser: %v", err)
	}
	p.setLocation(time.UTC)

	tests := []struct {
		name    string
		line    string
		level   LogLevel
		db      string
		message string
	}{
		{
			name:    "backend query",
			line:    "2026-07-22 15:31:02 UTC [12345]: [3-1] user=groupsio,db=groupsio,app=web,client=10.1.2.3 LOG:  duration: 2443.123 ms  statement: SELECT 1",
			level:   LevelInfo,
			db:      "groupsio",
			message: "duration: 2443.123 ms  statement: SELECT 1",
		},
		{
			// Background workers log with every identity field empty.
			name:    "checkpointer",
			line:    "2026-07-22 15:31:02 UTC [999]: [1-1] user=,db=,app=,client= LOG:  checkpoint complete: wrote 123 buffers",
			level:   LevelInfo,
			db:      "",
			message: "checkpoint complete: wrote 123 buffers",
		},
		{
			name:    "fatal",
			line:    "2026-07-22 15:31:02 UTC [777]: [2-1] user=groupsio,db=groupsio,app=karld,client=10.1.2.9 FATAL:  sorry, too many clients already",
			level:   LevelFatal,
			db:      "groupsio",
			message: "sorry, too many clients already",
		},
	}

	for _, tc := range tests {
		entry := p.Parse(tc.line)
		if entry.Level != tc.level {
			t.Errorf("%s: level = %q, want %q", tc.name, entry.Level, tc.level)
		}
		if entry.Message != tc.message {
			t.Errorf("%s: message = %q, want %q", tc.name, entry.Message, tc.message)
		}
		if got, _ := entry.Fields["db"].(string); got != tc.db {
			t.Errorf("%s: db = %q, want %q", tc.name, got, tc.db)
		}
		want := time.Date(2026, 7, 22, 15, 31, 2, 0, time.UTC)
		if !entry.Timestamp.Equal(want) {
			t.Errorf("%s: timestamp = %v, want %v", tc.name, entry.Timestamp, want)
		}
	}
}

// postgresLinePattern must stay in sync with the pattern the db10/db11/db12
// viewers use in trellis.hjson.
const postgresLinePattern = `^(?P<timestamp>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} \S+) \[(?P<pid>\d+)\]: \[(?P<sessline>\d+)-1\] user=(?P<user>[^,]*),db=(?P<db>[^,]*),app=(?P<app>.*?),client=(?P<client>\S*) (?P<level>[A-Z][A-Z0-9]*):\s+(?P<message>.*)$`
