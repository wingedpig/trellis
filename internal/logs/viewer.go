// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// Viewer coordinates a log source, parser, and buffer.
// It streams entries to subscribers and manages the lifecycle.
type Viewer struct {
	name    string
	cfg     config.LogViewerConfig
	source  LogSource
	parser  LogParser
	deriver *Deriver
	buffer  *Buffer

	mu           sync.RWMutex
	subscribers  map[chan<- LogEntry]struct{}
	running      bool
	cancel       context.CancelFunc
	errCh        chan error
	lastAccessed time.Time
}

// NewViewer creates a new log viewer.
func NewViewer(cfg config.LogViewerConfig) (*Viewer, error) {
	source, err := NewSource(cfg.Source)
	if err != nil {
		return nil, err
	}

	return NewViewerWithSource(cfg, source)
}

// NewViewerWithSource creates a new log viewer with a pre-built LogSource.
// This is used for service log viewers where the source is a ServiceSource
// rather than a source created from config.
func NewViewerWithSource(cfg config.LogViewerConfig, source LogSource) (*Viewer, error) {
	parser, err := NewParser(cfg.Parser)
	if err != nil {
		return nil, err
	}

	// Create deriver for computed fields
	var deriver *Deriver
	if len(cfg.Derive) > 0 {
		deriver = NewDeriver(cfg.Derive)
	}

	maxEntries := cfg.Buffer.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 100000 // Default 100k
	}

	return &Viewer{
		name:        cfg.Name,
		cfg:         cfg,
		source:      source,
		parser:      parser,
		deriver:     deriver,
		buffer:      NewBuffer(maxEntries),
		subscribers: make(map[chan<- LogEntry]struct{}),
	}, nil
}

// Name returns the viewer name.
func (v *Viewer) Name() string {
	return v.name
}

// Config returns the viewer configuration.
func (v *Viewer) Config() config.LogViewerConfig {
	return v.cfg
}

// Start begins streaming logs from the source.
func (v *Viewer) Start(ctx context.Context) error {
	v.mu.Lock()
	if v.running {
		v.mu.Unlock()
		return nil
	}
	v.running = true
	v.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	v.cancel = cancel

	lineCh := make(chan string, 1000)
	v.errCh = make(chan error, 10)

	if err := v.source.Start(ctx, lineCh, v.errCh); err != nil {
		v.mu.Lock()
		v.running = false
		v.mu.Unlock()
		return err
	}

	go v.processLines(ctx, lineCh)

	return nil
}

// Stop stops the viewer.
func (v *Viewer) Stop() error {
	v.mu.Lock()
	if !v.running {
		v.mu.Unlock()
		return nil
	}
	v.running = false
	v.mu.Unlock()

	if v.cancel != nil {
		v.cancel()
	}

	return v.source.Stop()
}

// IsRunning returns whether the viewer is running.
func (v *Viewer) IsRunning() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.running
}

// Touch updates the last accessed time.
func (v *Viewer) Touch() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lastAccessed = time.Now()
}

// LastAccessed returns the last accessed time.
func (v *Viewer) LastAccessed() time.Time {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lastAccessed
}

// SubscriberCount returns the number of active subscribers.
func (v *Viewer) SubscriberCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.subscribers)
}

// Status returns the viewer status.
func (v *Viewer) Status() ViewerStatus {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return ViewerStatus{
		Name:         v.name,
		Running:      v.running,
		Source:       v.source.Status(),
		BufferSize:   v.buffer.Size(),
		BufferMax:    v.buffer.MaxSize(),
		Subscribers:  len(v.subscribers),
		OldestEntry:  v.buffer.OldestTimestamp(),
		NewestEntry:  v.buffer.NewestTimestamp(),
	}
}

// ViewerStatus represents the status of a log viewer.
type ViewerStatus struct {
	Name        string       `json:"name"`
	Running     bool         `json:"running"`
	Source      SourceStatus `json:"source"`
	BufferSize  int          `json:"buffer_size"`
	BufferMax   int          `json:"buffer_max"`
	Subscribers int          `json:"subscribers"`
	OldestEntry time.Time    `json:"oldest_entry,omitempty"`
	NewestEntry time.Time    `json:"newest_entry,omitempty"`
}

// Subscribe adds a subscriber to receive new entries.
func (v *Viewer) Subscribe(ch chan<- LogEntry) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.subscribers[ch] = struct{}{}
}

// Unsubscribe removes a subscriber.
func (v *Viewer) Unsubscribe(ch chan<- LogEntry) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.subscribers, ch)
}

// GetEntries returns buffered entries matching the filter.
func (v *Viewer) GetEntries(filter *Filter, limit int) []LogEntry {
	return v.buffer.GetFiltered(filter, limit)
}

// GetEntriesAfter returns entries after the given sequence number.
func (v *Viewer) GetEntriesAfter(afterSeq uint64, limit int) []LogEntry {
	return v.buffer.GetAfter(afterSeq, limit)
}

// GetEntriesBefore returns entries before the given sequence number (for scrollback).
func (v *Viewer) GetEntriesBefore(beforeSeq uint64, limit int) []LogEntry {
	return v.buffer.GetBefore(beforeSeq, limit)
}

// GetEntriesRange returns entries in the given time range.
func (v *Viewer) GetEntriesRange(start, end time.Time, limit int) []LogEntry {
	return v.buffer.GetRange(start, end, limit)
}

// errStreamLimitReached is a sentinel returned by the StreamHistoricalEntries
// callback to signal that the configured limit has been hit and the stream
// should stop producing.
var errStreamLimitReached = errors.New("stream limit reached")

// StreamHistoricalEntries reads entries from rotated files and pushes them
// through fn one at a time. Filtering, time-range pruning, and limit handling
// match GetHistoricalEntries. If fn returns a non-nil error, the stream stops
// and that error is returned (a nil-returning fn means "keep going").
//
// Use this to avoid buffering the entire historical result set in memory —
// for example when streaming NDJSON to an HTTP client.
func (v *Viewer) StreamHistoricalEntries(ctx context.Context, start, end time.Time, filter *Filter, limit int, grep string, grepBefore, grepAfter int, fn func(LogEntry) error) error {
	log.Printf("StreamHistoricalEntries: start=%v end=%v limit=%d grep=%q before=%d after=%d", start, end, limit, grep, grepBefore, grepAfter)

	lineCh := make(chan string, 1000)
	errCh := make(chan error, 1)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		defer close(lineCh)
		if err := v.source.ReadRange(ctx, start, end, lineCh, grep, grepBefore, grepAfter); err != nil {
			log.Printf("StreamHistoricalEntries: ReadRange error: %v (ctx.Err=%v)", err, ctx.Err())
			if !errors.Is(err, context.Canceled) {
				errCh <- err
			}
		}
		close(errCh)
	}()

	emitted := 0
	linesReceived := 0
	var fnErr error
	for line := range lineCh {
		linesReceived++
		entry := v.parser.Parse(line)
		entry.Source = v.name

		if v.deriver != nil {
			v.deriver.Apply(&entry)
		}

		if entry.Timestamp.Before(start) || entry.Timestamp.After(end) {
			continue
		}

		if filter != nil && !filter.Match(entry) {
			continue
		}

		if err := fn(entry); err != nil {
			fnErr = err
			break
		}
		emitted++
		if limit > 0 && emitted >= limit {
			log.Printf("StreamHistoricalEntries: limit %d reached after %d lines", limit, linesReceived)
			fnErr = errStreamLimitReached
			break
		}
	}

	// Drain so the producer goroutine can exit
	if fnErr != nil {
		cancel()
		for range lineCh {
		}
	}

	if err := <-errCh; err != nil {
		log.Printf("StreamHistoricalEntries: returning error: %v", err)
		return err
	}

	log.Printf("StreamHistoricalEntries: emitted %d entries from %d lines", emitted, linesReceived)
	if fnErr != nil && !errors.Is(fnErr, errStreamLimitReached) {
		return fnErr
	}
	return nil
}

// GetHistoricalEntries loads entries from rotated files into a slice.
// Prefer StreamHistoricalEntries for large result sets to avoid buffering.
func (v *Viewer) GetHistoricalEntries(ctx context.Context, start, end time.Time, filter *Filter, limit int, grep string, grepBefore, grepAfter int) ([]LogEntry, error) {
	var entries []LogEntry
	err := v.StreamHistoricalEntries(ctx, start, end, filter, limit, grep, grepBefore, grepAfter, func(e LogEntry) error {
		entries = append(entries, e)
		return nil
	})
	return entries, err
}

// ListRotatedFiles returns available rotated log files.
func (v *Viewer) ListRotatedFiles(ctx context.Context) ([]RotatedFile, error) {
	return v.source.ListRotatedFiles(ctx)
}

// Errors returns the error channel.
func (v *Viewer) Errors() <-chan error {
	return v.errCh
}

// processLines processes incoming log lines.
func (v *Viewer) processLines(ctx context.Context, lineCh <-chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lineCh:
			if !ok {
				return
			}
			v.processLine(line)
		}
	}
}

// processLine parses a line and broadcasts to subscribers.
func (v *Viewer) processLine(line string) {
	entry := v.parser.Parse(line)
	entry.Source = v.name

	// Apply derived fields
	if v.deriver != nil {
		v.deriver.Apply(&entry)
	}

	// Add to buffer
	v.buffer.Add(entry)

	// Broadcast to subscribers
	v.mu.RLock()
	for ch := range v.subscribers {
		select {
		case ch <- entry:
		default:
			// Subscriber buffer full, skip
		}
	}
	v.mu.RUnlock()
}

// CurrentSequence returns the current sequence number.
func (v *Viewer) CurrentSequence() uint64 {
	return v.buffer.CurrentSequence()
}
