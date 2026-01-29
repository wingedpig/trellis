// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"strings"
	"sync"

	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/logs"
)

const defaultLogBufferSize = 1000

// LogBuffer is a thread-safe ring buffer for log lines with subscription support.
// It optionally parses log lines and applies derive operations.
type LogBuffer struct {
	mu          sync.RWMutex
	lines       []string
	entries     []*logs.LogEntry // Parsed entries (parallel to lines)
	capacity    int
	size        int
	head        int // next write position
	sequence    int64
	subscribers map[chan LogLine]struct{}
	subMu       sync.RWMutex

	// Parser and deriver for structured logging
	parser  logs.LogParser
	deriver *logs.Deriver
}

// LogLine represents a single log line with sequence number.
type LogLine struct {
	Line     string
	Sequence int64
	Entry    *logs.LogEntry `json:"entry,omitempty"` // Parsed entry (if parser configured)
}

// SetParser configures the parser and deriver for this buffer.
func (b *LogBuffer) SetParser(parserCfg config.LogParserConfig, deriveCfg map[string]config.DeriveConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if parserCfg.Type != "" {
		parser, err := logs.NewParser(parserCfg)
		if err == nil {
			b.parser = parser
		}
	}

	if len(deriveCfg) > 0 {
		b.deriver = logs.NewDeriver(deriveCfg)
	}
}

// NewLogBuffer creates a new log buffer with the given capacity.
func NewLogBuffer(capacity int) *LogBuffer {
	if capacity <= 0 {
		capacity = defaultLogBufferSize
	}
	return &LogBuffer{
		lines:       make([]string, capacity),
		entries:     make([]*logs.LogEntry, capacity),
		capacity:    capacity,
		subscribers: make(map[chan LogLine]struct{}),
	}
}

// Write adds a single line to the buffer and notifies subscribers.
func (b *LogBuffer) Write(line string) {
	b.mu.Lock()

	// Parse the line if parser is configured
	var entry *logs.LogEntry
	if b.parser != nil {
		parsed := b.parser.Parse(line)
		entry = &parsed
		// Apply derive operations
		if b.deriver != nil {
			b.deriver.Apply(entry)
		}
	}

	b.lines[b.head] = line
	b.entries[b.head] = entry
	b.head = (b.head + 1) % b.capacity
	if b.size < b.capacity {
		b.size++
	}
	b.sequence++
	seq := b.sequence
	b.mu.Unlock()

	// Notify subscribers (non-blocking)
	b.subMu.RLock()
	for ch := range b.subscribers {
		select {
		case ch <- LogLine{Line: line, Sequence: seq, Entry: entry}:
		default:
			// Channel full, skip (subscriber too slow)
		}
	}
	b.subMu.RUnlock()
}

// Subscribe returns a channel that receives new log lines.
// The channel has a buffer of 100 lines.
func (b *LogBuffer) Subscribe() chan LogLine {
	ch := make(chan LogLine, 100)
	b.subMu.Lock()
	b.subscribers[ch] = struct{}{}
	b.subMu.Unlock()
	return ch
}

// Unsubscribe removes a subscription channel.
func (b *LogBuffer) Unsubscribe(ch chan LogLine) {
	b.subMu.Lock()
	delete(b.subscribers, ch)
	b.subMu.Unlock()
	close(ch)
}

// CloseAllSubscribers closes all subscriber channels and resets the subscriber map.
// This is used when replacing a process to ensure orphaned subscribers exit cleanly.
func (b *LogBuffer) CloseAllSubscribers() {
	b.subMu.Lock()
	for ch := range b.subscribers {
		close(ch)
	}
	b.subscribers = make(map[chan LogLine]struct{})
	b.subMu.Unlock()
}

// Sequence returns the current sequence number.
func (b *LogBuffer) Sequence() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sequence
}

// WriteLines splits content by newlines and adds each line.
func (b *LogBuffer) WriteLines(content string) {
	if content == "" {
		return
	}

	// Remove trailing newline to avoid empty line at end
	content = strings.TrimSuffix(content, "\n")

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		b.Write(line)
	}
}

// WriteBytes splits byte content by newlines and adds each line.
func (b *LogBuffer) WriteBytes(data []byte) {
	b.WriteLines(string(data))
}

// Lines returns the last n lines from the buffer.
func (b *LogBuffer) Lines(n int) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if n <= 0 || b.size == 0 {
		return []string{}
	}

	if n > b.size {
		n = b.size
	}

	result := make([]string, n)

	// Calculate starting position
	// head points to next write position, so most recent is at head-1
	// We want the last n lines
	start := (b.head - n + b.capacity) % b.capacity

	for i := 0; i < n; i++ {
		idx := (start + i) % b.capacity
		result[i] = b.lines[idx]
	}

	return result
}

// All returns all lines in the buffer.
func (b *LogBuffer) All() []string {
	return b.Lines(b.size)
}

// Entries returns the last n parsed entries from the buffer.
// Returns nil entries for lines that weren't parsed (no parser configured).
func (b *LogBuffer) Entries(n int) []*logs.LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if n <= 0 || b.size == 0 {
		return []*logs.LogEntry{}
	}

	if n > b.size {
		n = b.size
	}

	result := make([]*logs.LogEntry, n)
	start := (b.head - n + b.capacity) % b.capacity

	for i := 0; i < n; i++ {
		idx := (start + i) % b.capacity
		result[i] = b.entries[idx]
	}

	return result
}

// HasParser returns true if a parser is configured.
func (b *LogBuffer) HasParser() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.parser != nil
}

// Clear removes all lines from the buffer.
func (b *LogBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.size = 0
	b.head = 0
	// Clear the slices to allow GC
	for i := range b.lines {
		b.lines[i] = ""
		b.entries[i] = nil
	}
}

// Size returns the number of lines in the buffer.
func (b *LogBuffer) Size() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.size
}

// Capacity returns the maximum number of lines the buffer can hold.
func (b *LogBuffer) Capacity() int {
	return b.capacity
}
