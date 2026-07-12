// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"errors"
	"log"
	"regexp"
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

	mu            sync.RWMutex
	subscribers   map[chan<- LogEntry]struct{}
	running       bool
	cancel        context.CancelFunc
	errCh         chan error
	lastAccessed  time.Time
	lastEmptyAt   time.Time // when the subscriber count last dropped to zero
	rateBuckets   [rateWindowSecs]int
	rateBucketSec int64 // unix second of the newest bucket
}

// rateWindowSecs is the sliding window (in seconds) over which the ingest
// rate is measured.
const rateWindowSecs = 10

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

	// Most sources replay a backlog of recent lines on every start (tail -F
	// -n 1000, docker/kubectl logs from the top). The tail stops whenever
	// the last watcher disconnects, so without this each reopen of the
	// viewer would append another copy of the same file content to the
	// surviving buffer — and sequence-keyed scrollback would then serve
	// those duplicate generations forever instead of falling through to the
	// byte-offset file reader. Start each run with a clean buffer; Clear
	// preserves the sequence counter, and anything discarded is still
	// readable from the file via the backward/history readers.
	if cs, ok := v.source.(ContinuousSource); !ok || !cs.ContinuousStart() {
		v.buffer.Clear()
	}

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
	// Rate() takes the write lock, so compute it before acquiring the read lock.
	rate := v.Rate()

	v.mu.RLock()
	defer v.mu.RUnlock()

	return ViewerStatus{
		Name:          v.name,
		Mode:          v.cfg.GetMode(),
		Running:       v.running,
		Source:        v.source.Status(),
		BufferSize:    v.buffer.Size(),
		BufferMax:     v.buffer.MaxSize(),
		Subscribers:   len(v.subscribers),
		EntriesPerSec: rate,
		OldestEntry:   v.buffer.OldestTimestamp(),
		NewestEntry:   v.buffer.NewestTimestamp(),
	}
}

// ViewerStatus represents the status of a log viewer.
type ViewerStatus struct {
	Name          string       `json:"name"`
	Mode          string       `json:"mode"`
	Running       bool         `json:"running"`
	Source        SourceStatus `json:"source"`
	BufferSize    int          `json:"buffer_size"`
	BufferMax     int          `json:"buffer_max"`
	Subscribers   int          `json:"subscribers"`
	EntriesPerSec float64      `json:"entries_per_sec"`
	OldestEntry   time.Time    `json:"oldest_entry,omitempty"`
	NewestEntry   time.Time    `json:"newest_entry,omitempty"`
}

// Subscribe adds a subscriber to receive new entries.
func (v *Viewer) Subscribe(ch chan<- LogEntry) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.subscribers[ch] = struct{}{}
	v.lastEmptyAt = time.Time{}
}

// Unsubscribe removes a subscriber.
func (v *Viewer) Unsubscribe(ch chan<- LogEntry) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.subscribers, ch)
	if len(v.subscribers) == 0 {
		v.lastEmptyAt = time.Now()
	}
}

// LastSubscriberGone returns when the subscriber count last dropped to zero.
// The zero time means either a subscriber is active or none ever connected.
func (v *Viewer) LastSubscriberGone() time.Time {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if len(v.subscribers) > 0 {
		return time.Time{}
	}
	return v.lastEmptyAt
}

// CanReadBackward reports whether the source supports byte-offset backward
// reads (file and SSH sources). Explore-mode connections use this to serve
// recent entries without starting the tail.
func (v *Viewer) CanReadBackward() bool {
	_, ok := v.source.(BackwardReader)
	return ok
}

// GetEntries returns buffered entries matching the filter.
func (v *Viewer) GetEntries(filter *Filter, limit int) []LogEntry {
	return v.buffer.GetFiltered(filter, limit)
}

// GetEntriesAfter returns entries after the given sequence number.
func (v *Viewer) GetEntriesAfter(afterSeq uint64, limit int) []LogEntry {
	return v.buffer.GetAfter(afterSeq, limit)
}

// GetLastEntriesAfter returns up to `limit` of the newest entries after the
// given sequence number plus a count of older entries dropped by the cap.
// Used for catch-up replay when a paused stream resumes.
func (v *Viewer) GetLastEntriesAfter(afterSeq uint64, limit int) ([]LogEntry, int) {
	return v.buffer.GetLastAfter(afterSeq, limit)
}

// GetEntriesBefore returns entries before the given sequence number (for scrollback).
func (v *Viewer) GetEntriesBefore(beforeSeq uint64, limit int) []LogEntry {
	return v.buffer.GetBefore(beforeSeq, limit)
}

// ReadEntriesBackward pages backward through the source's historical files
// using byte-offset reads, parses the raw lines, applies the given filter,
// and returns up to `limit` matching entries. Returns the lines (newest
// first within result), the next cursor, and a Done flag indicating no
// older history is available.
//
// Only sources that implement BackwardReader (FileSource, SSHSource) are
// supported; for other source types the caller should fall back to the
// time-window-based StreamHistoricalEntries. If the source can produce raw
// lines from compressed files but the byte-offset reader skipped them, the
// caller is expected to fall back too.
//
// Filtering is applied after parsing; we keep reading more lines from the
// source until either `limit` matching entries have accumulated or the
// source is exhausted. To keep total work bounded, the function will not
// scan more than `maxLinesScanned` raw lines per call.
func (v *Viewer) ReadEntriesBackward(ctx context.Context, cursor BackwardCursor, limit int, filter *Filter, beforeTime time.Time) ([]LogEntry, BackwardCursor, bool, bool, error) {
	reader, ok := v.source.(BackwardReader)
	if !ok {
		return nil, cursor, false, false, errors.New("source does not support backward reading")
	}

	// On a fresh cursor (the client just transitioned from the in-memory
	// ring buffer to byte-offset paging), the natural starting point is
	// end-of-file — but that's exactly the region the in-memory buffer
	// already covered. Skip past it by binary-searching the file for the
	// first byte whose line has timestamp >= beforeTime; reading backward
	// from there yields only content older than what the client already
	// shows.
	//
	// When SeekToTime returns Offset==0 it means the target is older than
	// every line in the file — so there's nothing to read backward. We
	// honor that by advancing the cursor PAST the active file (FileIndex
	// = -1 sentinel handled below) rather than falling back to Offset<0
	// which would re-read the file from end and hand the client lines it
	// already has.
	if cursor.Offset < 0 && !beforeTime.IsZero() {
		// Use the parser's ExtractTimestamp if available — it returns
		// the zero time when the line had no parseable timestamp,
		// which is what findOffsetForTime needs to skip unparseable
		// probes. Falling back to Parse().Timestamp gives us time.Now()
		// for unparseable lines, which would bias the binary search
		// toward 0 and re-introduce the today-leak / halt bugs.
		parseTS := func(line string) time.Time {
			if ex, ok := v.parser.(TimestampExtractor); ok {
				if ts, ok := ex.ExtractTimestamp(line); ok {
					return ts
				}
				return time.Time{}
			}
			return v.parser.Parse(line).Timestamp
		}
		seeked, err := reader.SeekToTime(ctx, beforeTime, parseTS)
		if err != nil {
			log.Printf("logs[%s]: SeekToTime error (%v); reading from end of file (may produce overlap)", v.name, err)
		} else if seeked.Offset > 0 {
			cursor = seeked
		} else {
			// Offset==0 means "target is older than everything in this
			// file". Mark the cursor so readBackwardAcrossFiles advances
			// past the active file to whatever's behind it.
			log.Printf("logs[%s]: SeekToTime says target %v is older than active file; advancing past", v.name, beforeTime)
			cursor = BackwardCursor{FileIndex: 1, Offset: -1}
		}
	}

	const maxLinesScanned = 10000
	scanned := 0
	cur := cursor
	var entries []LogEntry
	skippedCompressed := false
	for len(entries) < limit && scanned < maxLinesScanned {
		want := limit - len(entries)
		// With a filter, lines may be rejected at a high rate; ask for a
		// larger raw batch to amortize per-call overhead. Without a
		// filter every parsed line is kept, so honor `limit` exactly.
		if filter != nil && want < 100 {
			want = 100
		}
		res, err := reader.ReadBackward(ctx, cur, want)
		if err != nil {
			log.Printf("logs[%s]: ReadBackward error at cursor %+v: %v", v.name, cur, err)
			return entries, cur, false, skippedCompressed, err
		}
		scanned += len(res.Lines)
		cur = res.NextCursor
		if res.SkippedCompressed {
			skippedCompressed = true
		}
		// Parse + filter. Stop appending once we've reached `limit` even
		// if there are more lines in this batch (only happens for
		// filtered reads where we asked for more than `limit` raw lines).
		for _, raw := range res.Lines {
			if len(entries) >= limit {
				break
			}
			e := v.parser.Parse(raw)
			e.Source = v.name
			if v.deriver != nil {
				v.deriver.Apply(&e)
			}
			if filter != nil && !filter.Match(e) {
				continue
			}
			entries = append(entries, e)
		}
		if res.Done {
			return entries, cur, true, skippedCompressed, nil
		}
		if len(res.Lines) == 0 {
			// Defensive: source returned nothing but didn't set Done. Stop
			// so we don't spin.
			break
		}
	}
	return entries, cur, false, skippedCompressed, nil
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

	// Determine whether the source filters grep server-side. Sources that
	// don't (file, docker, k8s, command, service) require client-side grep
	// here — otherwise the user's pattern would be silently discarded and
	// every line in the time window would be returned (e.g., trace ID searches
	// would return the full window with no filtering).
	var grepRe *regexp.Regexp
	if grep != "" {
		serverHandles := false
		if g, ok := v.source.(ServerSideGrep); ok && g.HandlesGrep() {
			serverHandles = true
		}
		if !serverHandles {
			re, err := regexp.Compile(grep)
			if err != nil {
				return err
			}
			grepRe = re
		}
	}

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

		// Client-side grep fallback for sources that don't filter
		// themselves. -B/-A context lines are not reconstructed here; that
		// stays an SSH-only feature for now.
		if grepRe != nil && !grepRe.MatchString(line) {
			continue
		}

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

	// Add to buffer. Broadcast the returned copy — it carries the assigned
	// sequence number, which subscribers rely on for dedup and replay.
	entry = v.buffer.Add(entry)

	v.countEntry(time.Now())

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

// countEntry records one ingested entry in the per-second rate buckets.
func (v *Viewer) countEntry(now time.Time) {
	sec := now.Unix()
	v.mu.Lock()
	defer v.mu.Unlock()
	v.advanceRateBuckets(sec)
	v.rateBuckets[sec%rateWindowSecs]++
}

// advanceRateBuckets clears buckets for seconds that have passed since the
// last update. Caller must hold v.mu.
func (v *Viewer) advanceRateBuckets(sec int64) {
	if v.rateBucketSec == 0 {
		v.rateBucketSec = sec
		return
	}
	gap := sec - v.rateBucketSec
	if gap <= 0 {
		return
	}
	if gap >= rateWindowSecs {
		for i := range v.rateBuckets {
			v.rateBuckets[i] = 0
		}
	} else {
		for s := v.rateBucketSec + 1; s <= sec; s++ {
			v.rateBuckets[s%rateWindowSecs] = 0
		}
	}
	v.rateBucketSec = sec
}

// Rate returns the average entries/sec ingested over the sliding window.
func (v *Viewer) Rate() float64 {
	sec := time.Now().Unix()
	v.mu.Lock()
	defer v.mu.Unlock()
	v.advanceRateBuckets(sec)
	total := 0
	for _, n := range v.rateBuckets {
		total += n
	}
	return float64(total) / float64(rateWindowSecs)
}

// CurrentSequence returns the current sequence number.
func (v *Viewer) CurrentSequence() uint64 {
	return v.buffer.CurrentSequence()
}
