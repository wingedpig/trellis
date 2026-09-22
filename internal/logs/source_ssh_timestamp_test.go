// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"regexp"
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

func TestSSHHistoryTimestampFormats(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{
		"02/Jan/2006:15:04:05 -0700", // nginx access
		"2006/01/02 15:04:05",        // nginx error
		"2006-01-02 15:04:05 MST",    // postgres
		"02 Jan 2006 15:04:05.000",   // redis
		time.RFC3339Nano,
	} {
		t.Run(layout, func(t *testing.T) {
			viewer, err := NewViewer(config.LogViewerConfig{
				Name: "test", Timezone: loc.String(),
				Source: config.LogSourceConfig{Type: "ssh", Host: "unused", Path: "/logs"},
				Parser: config.LogParserConfig{Type: "json", TimestampFormat: layout},
			})
			if err != nil {
				t.Fatal(err)
			}
			src := viewer.source.(*SSHSource)
			for _, window := range []struct {
				name       string
				start, end time.Time
			}{
				{"partial hour", time.Date(2026, 9, 9, 13, 30, 0, 0, loc), time.Date(2026, 9, 9, 13, 45, 0, 0, loc)},
				{"cross midnight", time.Date(2026, 9, 9, 23, 30, 0, 0, loc), time.Date(2026, 9, 10, 0, 30, 0, 0, loc)},
				{"full days across DST", time.Date(2026, 10, 31, 0, 0, 0, 0, loc), time.Date(2026, 11, 2, 23, 59, 0, 0, loc)},
			} {
				t.Run(window.name, func(t *testing.T) {
					pattern := src.buildTimestampGrepPattern(window.start.UTC(), window.end.UTC())
					if pattern == "" {
						t.Fatal("recognized layout should have a prefilter")
					}
					re := regexp.MustCompile(pattern)
					for ts := window.start; !ts.After(window.end); ts = ts.Add(time.Minute) {
						if !re.MatchString("prefix [" + ts.Format(layout) + "] message") {
							t.Fatalf("pattern %q dropped in-range timestamp %s", pattern, ts)
						}
					}
					for _, outside := range []time.Time{window.start.Add(-24 * time.Hour), window.end.Add(24 * time.Hour)} {
						if re.MatchString(outside.Format(layout)) {
							t.Errorf("pattern %q matched out-of-range day %s", pattern, outside)
						}
					}
				})
			}
		})
	}
}

func TestSSHHistoryUnknownTimestampFormat(t *testing.T) {
	src := &SSHSource{timestampFormat: "Jan _2 15:04:05"}
	start := time.Date(2026, 9, 9, 13, 30, 0, 0, time.UTC)
	if got := src.buildTimestampGrepPattern(start, start.Add(time.Hour)); got != "" {
		t.Fatalf("unrecognized layout must not discard lines with an ISO prefilter: %q", got)
	}
	src.timestampFormat = ""
	if got := src.buildTimestampGrepPattern(time.Time{}, start); got != "" {
		t.Fatalf("unbounded start must not enumerate dates from year one: %q", got)
	}
}
