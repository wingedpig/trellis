// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// replaySource is a minimal LogSource that emits the same fixed backlog of
// lines every time it starts — the shape of `tail -F -n 1000` re-sending
// recent file content on each tail restart.
type replaySource struct {
	lines []string

	mu     sync.Mutex
	starts int
}

func (s *replaySource) Name() string { return "replay" }

func (s *replaySource) Start(ctx context.Context, lineCh chan<- string, errCh chan<- error) error {
	s.mu.Lock()
	s.starts++
	s.mu.Unlock()
	go func() {
		defer close(lineCh)
		for _, l := range s.lines {
			select {
			case lineCh <- l:
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
	}()
	return nil
}

func (s *replaySource) Stop() error          { return nil }
func (s *replaySource) Status() SourceStatus { return SourceStatus{} }
func (s *replaySource) ListRotatedFiles(ctx context.Context) ([]RotatedFile, error) {
	return nil, nil
}
func (s *replaySource) ReadRange(ctx context.Context, start, end time.Time, lineCh chan<- string, grep string, grepBefore, grepAfter int) error {
	return nil
}

// continuousSource emits its lines only on the first start, like a
// ServiceSource fed by the service runner: restarts replay nothing.
type continuousSource struct {
	replaySource
}

func (s *continuousSource) Start(ctx context.Context, lineCh chan<- string, errCh chan<- error) error {
	s.mu.Lock()
	first := s.starts == 0
	s.starts++
	s.mu.Unlock()
	go func() {
		defer close(lineCh)
		if first {
			for _, l := range s.lines {
				select {
				case lineCh <- l:
				case <-ctx.Done():
					return
				}
			}
		}
		<-ctx.Done()
	}()
	return nil
}

func (s *continuousSource) ContinuousStart() bool { return true }

func waitForCondition(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

// TestViewerRestartClearsReplayedBacklog covers the scrollback-duplicates
// bug: the tail stops when the last watcher disconnects and replays its
// backlog on the next start. Without clearing the buffer on start, each
// restart appended another copy of the same lines (with fresh sequence
// numbers), and sequence-keyed scrollback served those duplicate
// generations forever.
func TestViewerRestartClearsReplayedBacklog(t *testing.T) {
	src := &replaySource{lines: []string{
		`{"msg":"a"}`,
		`{"msg":"b"}`,
		`{"msg":"c"}`,
	}}
	viewer, err := NewViewerWithSource(config.LogViewerConfig{
		Name:   "restart-test",
		Buffer: config.LogBufferConfig{MaxEntries: 100},
	}, src)
	if err != nil {
		t.Fatalf("NewViewerWithSource failed: %v", err)
	}

	ctx := context.Background()
	if err := viewer.Start(ctx); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	waitForCondition(t, "first backlog buffered", func() bool {
		return viewer.buffer.Size() == len(src.lines)
	})

	if err := viewer.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Restart: the source replays the same three lines. The buffer must
	// hold exactly one copy, with sequence numbers continuing past the
	// first generation's.
	if err := viewer.Start(ctx); err != nil {
		t.Fatalf("second Start failed: %v", err)
	}
	waitForCondition(t, "replayed backlog buffered after restart", func() bool {
		entries := viewer.buffer.Get(0)
		return len(entries) == len(src.lines) &&
			entries[0].Sequence == uint64(len(src.lines)+1)
	})

	entries := viewer.buffer.Get(0)
	if got := len(entries); got != len(src.lines) {
		t.Fatalf("buffer holds %d entries after restart, want %d (no duplicate generation)", got, len(src.lines))
	}
	for i, e := range entries {
		wantSeq := uint64(len(src.lines) + 1 + i)
		if e.Sequence != wantSeq {
			t.Errorf("entry %d sequence = %d, want %d (counter must survive the clear)", i, e.Sequence, wantSeq)
		}
	}

	if err := viewer.Stop(); err != nil {
		t.Fatalf("final Stop failed: %v", err)
	}
}

// TestViewerRestartRealTailNoDuplicates exercises the same scenario through
// the real FileSource: `tail -F -n 1000` genuinely replays the file's lines
// on every start, so a restart must leave exactly one copy in the buffer.
func TestViewerRestartRealTailNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	const n = 50
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	for i := 0; i < n; i++ {
		fmt.Fprintf(f, "{\"time\":%q,\"msg\":\"line %03d\"}\n", base.Add(time.Duration(i)*time.Second).Format(time.RFC3339Nano), i)
	}
	f.Close()

	viewer, err := NewViewer(config.LogViewerConfig{
		Name:   "restart-real-tail",
		Source: config.LogSourceConfig{Type: "file", Path: path},
		Buffer: config.LogBufferConfig{MaxEntries: 1000},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := viewer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, "first tail run buffered", func() bool {
		return viewer.buffer.Size() == n
	})

	if err := viewer.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := viewer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, "replayed tail buffered after restart", func() bool {
		return viewer.buffer.Size() == n
	})

	// Straggler window, then confirm a single clean copy with no
	// duplicate content anywhere in scrollback range.
	time.Sleep(300 * time.Millisecond)
	entries := viewer.GetEntries(nil, 0)
	if len(entries) != n {
		t.Fatalf("got %d entries after restart, want %d", len(entries), n)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Raw] {
			t.Fatalf("duplicate line in buffer after restart: %s", e.Raw)
		}
		seen[e.Raw] = true
	}
	newest := entries[len(entries)-1].Sequence
	if older := viewer.GetEntriesBefore(newest, 1000); len(older) != n-1 {
		t.Fatalf("GetEntriesBefore returned %d entries, want %d", len(older), n-1)
	}

	if err := viewer.Stop(); err != nil {
		t.Fatalf("final Stop failed: %v", err)
	}
}

// TestViewerRestartKeepsBufferForContinuousSource: sources that replay
// nothing on start (ServiceSource) must keep their buffered history across
// restarts — clearing would discard lines that can't be re-read.
func TestViewerRestartKeepsBufferForContinuousSource(t *testing.T) {
	src := &continuousSource{replaySource{lines: []string{
		`{"msg":"a"}`,
		`{"msg":"b"}`,
	}}}
	viewer, err := NewViewerWithSource(config.LogViewerConfig{
		Name:   "continuous-restart-test",
		Buffer: config.LogBufferConfig{MaxEntries: 100},
	}, src)
	if err != nil {
		t.Fatalf("NewViewerWithSource failed: %v", err)
	}

	ctx := context.Background()
	if err := viewer.Start(ctx); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	waitForCondition(t, "lines buffered", func() bool {
		return viewer.buffer.Size() == len(src.lines)
	})

	if err := viewer.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if err := viewer.Start(ctx); err != nil {
		t.Fatalf("second Start failed: %v", err)
	}

	if got := viewer.buffer.Size(); got != len(src.lines) {
		t.Fatalf("buffer holds %d entries after restart, want %d (continuous sources must not be cleared)", got, len(src.lines))
	}

	if err := viewer.Stop(); err != nil {
		t.Fatalf("final Stop failed: %v", err)
	}
}
