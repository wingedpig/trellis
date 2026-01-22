// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

func TestNewViewer(t *testing.T) {
	tests := []struct {
		name      string
		cfg       config.LogViewerConfig
		shouldErr bool
	}{
		{
			name: "valid file viewer",
			cfg: config.LogViewerConfig{
				Name: "test-viewer",
				Source: config.LogSourceConfig{
					Type: "file",
					Path: "/var/log/test.log",
				},
				Parser: config.LogParserConfig{
					Type: "json",
				},
				Buffer: config.LogBufferConfig{
					MaxEntries: 1000,
				},
			},
			shouldErr: false,
		},
		{
			name: "valid command viewer",
			cfg: config.LogViewerConfig{
				Name: "cmd-viewer",
				Source: config.LogSourceConfig{
					Type:    "command",
					Command: []string{"echo", "test"},
				},
				Parser: config.LogParserConfig{
					Type: "none",
				},
			},
			shouldErr: false,
		},
		{
			name: "invalid source",
			cfg: config.LogViewerConfig{
				Name: "bad-viewer",
				Source: config.LogSourceConfig{
					Type: "unknown",
				},
			},
			shouldErr: true,
		},
		{
			name: "invalid parser",
			cfg: config.LogViewerConfig{
				Name: "bad-parser",
				Source: config.LogSourceConfig{
					Type: "file",
					Path: "/var/log/test.log",
				},
				Parser: config.LogParserConfig{
					Type: "regex",
					// Missing pattern
				},
			},
			shouldErr: true,
		},
		{
			name: "default buffer size",
			cfg: config.LogViewerConfig{
				Name: "default-buffer",
				Source: config.LogSourceConfig{
					Type: "file",
					Path: "/var/log/test.log",
				},
				Parser: config.LogParserConfig{
					Type: "json",
				},
				// No buffer config - should use default
			},
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viewer, err := NewViewer(tt.cfg)
			if (err != nil) != tt.shouldErr {
				t.Errorf("NewViewer() error = %v, shouldErr = %v", err, tt.shouldErr)
			}
			if !tt.shouldErr && viewer == nil {
				t.Error("NewViewer() returned nil viewer without error")
			}
			if !tt.shouldErr && viewer != nil {
				if viewer.Name() != tt.cfg.Name {
					t.Errorf("Name() = %q, want %q", viewer.Name(), tt.cfg.Name)
				}
			}
		})
	}
}

func TestViewerConfig(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	returnedCfg := viewer.Config()
	if returnedCfg.Name != cfg.Name {
		t.Errorf("Config().Name = %q, want %q", returnedCfg.Name, cfg.Name)
	}
}

func TestViewerNotRunning(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	if viewer.IsRunning() {
		t.Error("IsRunning() should be false before Start")
	}
}

func TestViewerStatus(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
		Buffer: config.LogBufferConfig{
			MaxEntries: 5000,
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	status := viewer.Status()
	if status.Name != "test-viewer" {
		t.Errorf("Status.Name = %q, want %q", status.Name, "test-viewer")
	}
	if status.Running {
		t.Error("Status.Running should be false before Start")
	}
	if status.BufferMax != 5000 {
		t.Errorf("Status.BufferMax = %d, want 5000", status.BufferMax)
	}
	if status.BufferSize != 0 {
		t.Errorf("Status.BufferSize = %d, want 0", status.BufferSize)
	}
	if status.Subscribers != 0 {
		t.Errorf("Status.Subscribers = %d, want 0", status.Subscribers)
	}
}

func TestViewerSubscription(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	ch := make(chan LogEntry, 10)

	// Subscribe
	viewer.Subscribe(ch)
	status := viewer.Status()
	if status.Subscribers != 1 {
		t.Errorf("Subscribers = %d after Subscribe, want 1", status.Subscribers)
	}

	// Unsubscribe
	viewer.Unsubscribe(ch)
	status = viewer.Status()
	if status.Subscribers != 0 {
		t.Errorf("Subscribers = %d after Unsubscribe, want 0", status.Subscribers)
	}
}

func TestViewerGetEntries(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
		Buffer: config.LogBufferConfig{
			MaxEntries: 100,
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	// Add some entries directly to buffer for testing
	// (normally this happens via processLine)
	for i := 0; i < 10; i++ {
		viewer.buffer.Add(LogEntry{
			Timestamp: time.Now(),
			Level:     LevelInfo,
			Message:   "test message",
			Source:    "test-viewer",
		})
	}

	// Get all entries
	entries := viewer.GetEntries(nil, 0)
	if len(entries) != 10 {
		t.Errorf("GetEntries() returned %d entries, want 10", len(entries))
	}

	// Get with limit
	entries = viewer.GetEntries(nil, 5)
	if len(entries) != 5 {
		t.Errorf("GetEntries(limit=5) returned %d entries, want 5", len(entries))
	}

	// Get with filter
	viewer.buffer.Add(LogEntry{
		Timestamp: time.Now(),
		Level:     LevelError,
		Message:   "error message",
		Source:    "test-viewer",
	})
	filter, _ := ParseFilter("level:error")
	entries = viewer.GetEntries(filter, 0)
	if len(entries) != 1 {
		t.Errorf("GetEntries(level:error) returned %d entries, want 1", len(entries))
	}
}

func TestViewerGetEntriesAfter(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	// Add entries
	for i := 0; i < 5; i++ {
		viewer.buffer.Add(LogEntry{
			Message: "test",
		})
	}

	// Get after sequence 2
	entries := viewer.GetEntriesAfter(2, 0)
	if len(entries) != 3 {
		t.Errorf("GetEntriesAfter(2) returned %d entries, want 3", len(entries))
	}
}

func TestViewerGetEntriesRange(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	now := time.Now()

	// Add entries with different timestamps
	for i := 0; i < 10; i++ {
		viewer.buffer.Add(LogEntry{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Message:   "test",
		})
	}

	// Get range
	start := now.Add(2 * time.Minute)
	end := now.Add(5 * time.Minute)
	entries := viewer.GetEntriesRange(start, end, 0)
	if len(entries) != 4 {
		t.Errorf("GetEntriesRange() returned %d entries, want 4", len(entries))
	}
}

func TestViewerCurrentSequence(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	initialSeq := viewer.CurrentSequence()
	if initialSeq != 0 {
		t.Errorf("Initial sequence = %d, want 0", initialSeq)
	}

	// Add an entry
	viewer.buffer.Add(LogEntry{Message: "test"})

	newSeq := viewer.CurrentSequence()
	if newSeq != 1 {
		t.Errorf("Sequence after add = %d, want 1", newSeq)
	}
}

func TestViewerStopWithoutStart(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	// Stop without start should not error
	err = viewer.Stop()
	if err != nil {
		t.Errorf("Stop() on non-running viewer returned error: %v", err)
	}
}

func TestViewerErrors(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	// Should return nil channel before start
	errCh := viewer.Errors()
	if errCh != nil {
		t.Error("Errors() should return nil before Start")
	}
}

// TestViewerWithMockSource tests viewer with a mock source
func TestViewerProcessLine(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/var/log/test.log",
		},
		Parser: config.LogParserConfig{
			Type:      "json",
			Timestamp: "ts",
			Level:     "level",
			Message:   "msg",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	// Subscribe to receive entries
	ch := make(chan LogEntry, 10)
	viewer.Subscribe(ch)

	// Manually call processLine (normally called by background goroutine)
	viewer.processLine(`{"ts":"2024-01-15T10:30:00Z","level":"error","msg":"test error"}`)

	// Check buffer
	if viewer.buffer.Size() != 1 {
		t.Errorf("Buffer size = %d, want 1", viewer.buffer.Size())
	}

	// Check entry was parsed correctly
	entries := viewer.buffer.Get(0)
	if len(entries) != 1 {
		t.Fatalf("Got %d entries, want 1", len(entries))
	}
	if entries[0].Level != LevelError {
		t.Errorf("Entry.Level = %v, want %v", entries[0].Level, LevelError)
	}
	if entries[0].Message != "test error" {
		t.Errorf("Entry.Message = %q, want %q", entries[0].Message, "test error")
	}
	if entries[0].Source != "test-viewer" {
		t.Errorf("Entry.Source = %q, want %q", entries[0].Source, "test-viewer")
	}

	// Check subscriber received entry
	select {
	case entry := <-ch:
		if entry.Level != LevelError {
			t.Errorf("Subscriber entry.Level = %v, want %v", entry.Level, LevelError)
		}
	default:
		t.Error("Subscriber did not receive entry")
	}
}

func TestViewerStartWithNonExistentFile(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type: "file",
			Path: "/nonexistent/path/that/should/not/exist.log",
		},
		Parser: config.LogParserConfig{
			Type: "json",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start should return nil (errors come through the channel)
	err = viewer.Start(ctx)
	if err != nil {
		// This is acceptable - some systems fail fast on non-existent paths
		t.Logf("Start returned error (acceptable): %v", err)
	}

	// Stop to clean up
	viewer.Stop()
}

func TestViewerDoubleStart(t *testing.T) {
	cfg := config.LogViewerConfig{
		Name: "test-viewer",
		Source: config.LogSourceConfig{
			Type:    "command",
			Command: []string{"sleep", "1"},
		},
		Parser: config.LogParserConfig{
			Type: "none",
		},
	}

	viewer, err := NewViewer(cfg)
	if err != nil {
		t.Fatalf("NewViewer failed: %v", err)
	}

	ctx := context.Background()

	// First start
	err = viewer.Start(ctx)
	if err != nil {
		t.Fatalf("First Start failed: %v", err)
	}

	// Second start should be a no-op
	err = viewer.Start(ctx)
	if err != nil {
		t.Errorf("Second Start returned error: %v", err)
	}

	// Clean up
	viewer.Stop()
}
