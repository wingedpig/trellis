// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/wingedpig/trellis/internal/logs"
)

func TestTraceEntryFromLogEntry(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		entry     logs.LogEntry
		source    string
		isContext bool
		want      TraceEntry
	}{
		{
			name: "basic entry",
			entry: logs.LogEntry{
				Timestamp: now,
				Level:     logs.LevelInfo,
				Message:   "test message",
				Raw:       "raw log line",
				Fields:    map[string]any{"key": "value"},
			},
			source:    "test-viewer",
			isContext: false,
			want: TraceEntry{
				Timestamp: now,
				Source:    "test-viewer",
				Level:     "info",
				Message:   "test message",
				Raw:       "raw log line",
				Fields:    map[string]any{"key": "value"},
				IsContext: false,
			},
		},
		{
			name: "context entry",
			entry: logs.LogEntry{
				Timestamp: now,
				Level:     logs.LevelError,
				Message:   "error message",
				Raw:       "error raw line",
			},
			source:    "another-viewer",
			isContext: true,
			want: TraceEntry{
				Timestamp: now,
				Source:    "another-viewer",
				Level:     "error",
				Message:   "error message",
				Raw:       "error raw line",
				IsContext: true,
			},
		},
		{
			name: "entry with no level",
			entry: logs.LogEntry{
				Timestamp: now,
				Message:   "no level message",
				Raw:       "raw",
			},
			source:    "viewer",
			isContext: false,
			want: TraceEntry{
				Timestamp: now,
				Source:    "viewer",
				Level:     "",
				Message:   "no level message",
				Raw:       "raw",
				IsContext: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TraceEntryFromLogEntry(tt.entry, tt.source, tt.isContext)
			assert.Equal(t, tt.want.Timestamp, got.Timestamp)
			assert.Equal(t, tt.want.Source, got.Source)
			assert.Equal(t, tt.want.Level, got.Level)
			assert.Equal(t, tt.want.Message, got.Message)
			assert.Equal(t, tt.want.Raw, got.Raw)
			assert.Equal(t, tt.want.IsContext, got.IsContext)
			if tt.want.Fields != nil {
				assert.Equal(t, tt.want.Fields, got.Fields)
			}
		})
	}
}
