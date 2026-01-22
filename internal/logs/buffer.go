// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"sync"
	"sync/atomic"
	"time"
)

// Buffer is a thread-safe ring buffer for log entries.
type Buffer struct {
	mu       sync.RWMutex
	entries  []LogEntry
	head     int    // Next write position
	size     int    // Current number of entries
	maxSize  int    // Maximum capacity
	sequence uint64 // Monotonically increasing sequence number
}

// NewBuffer creates a new log entry buffer.
func NewBuffer(maxSize int) *Buffer {
	if maxSize <= 0 {
		maxSize = 100000 // Default 100k entries
	}
	return &Buffer{
		entries: make([]LogEntry, maxSize),
		maxSize: maxSize,
	}
}

// Add adds an entry to the buffer.
func (b *Buffer) Add(entry LogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Assign sequence number
	entry.Sequence = atomic.AddUint64(&b.sequence, 1)

	// Write at head position
	b.entries[b.head] = entry
	b.head = (b.head + 1) % b.maxSize

	if b.size < b.maxSize {
		b.size++
	}
}

// AddBatch adds multiple entries to the buffer.
func (b *Buffer) AddBatch(entries []LogEntry) {
	if len(entries) == 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for i := range entries {
		entries[i].Sequence = atomic.AddUint64(&b.sequence, 1)
		b.entries[b.head] = entries[i]
		b.head = (b.head + 1) % b.maxSize
		if b.size < b.maxSize {
			b.size++
		}
	}
}

// Get returns entries from the buffer.
// If limit is 0, returns all entries.
// Entries are returned in chronological order (oldest first).
func (b *Buffer) Get(limit int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return nil
	}

	count := b.size
	if limit > 0 && limit < count {
		count = limit
	}

	result := make([]LogEntry, count)

	// Calculate start position
	start := b.head - b.size
	if start < 0 {
		start += b.maxSize
	}

	// If limit is set, skip to recent entries
	if limit > 0 && b.size > limit {
		skip := b.size - limit
		start = (start + skip) % b.maxSize
	}

	// Copy entries in chronological order
	for i := 0; i < count; i++ {
		idx := (start + i) % b.maxSize
		result[i] = b.entries[idx]
	}

	return result
}

// GetFiltered returns entries matching the filter.
func (b *Buffer) GetFiltered(filter *Filter, limit int) []LogEntry {
	if filter == nil || filter.IsEmpty() {
		return b.Get(limit)
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return nil
	}

	var result []LogEntry

	// Calculate start position
	start := b.head - b.size
	if start < 0 {
		start += b.maxSize
	}

	// Scan in chronological order
	for i := 0; i < b.size; i++ {
		idx := (start + i) % b.maxSize
		entry := b.entries[idx]
		if filter.Match(entry) {
			result = append(result, entry)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}

	return result
}

// GetAfter returns entries after the given sequence number.
func (b *Buffer) GetAfter(afterSeq uint64, limit int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return nil
	}

	var result []LogEntry

	// Calculate start position
	start := b.head - b.size
	if start < 0 {
		start += b.maxSize
	}

	// Scan in chronological order
	for i := 0; i < b.size; i++ {
		idx := (start + i) % b.maxSize
		entry := b.entries[idx]
		if entry.Sequence > afterSeq {
			result = append(result, entry)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}

	return result
}

// GetBefore returns entries before the given sequence number (for scrollback).
// Returns entries in chronological order (oldest first).
func (b *Buffer) GetBefore(beforeSeq uint64, limit int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return nil
	}

	// Collect entries with sequence < beforeSeq, in reverse order (newest first)
	var reversed []LogEntry

	// Calculate start position (oldest entry)
	start := b.head - b.size
	if start < 0 {
		start += b.maxSize
	}

	// Scan in reverse chronological order (newest first) to find entries before beforeSeq
	for i := b.size - 1; i >= 0; i-- {
		idx := (start + i) % b.maxSize
		entry := b.entries[idx]
		if entry.Sequence < beforeSeq {
			reversed = append(reversed, entry)
			if limit > 0 && len(reversed) >= limit {
				break
			}
		}
	}

	// Reverse to get chronological order (oldest first)
	result := make([]LogEntry, len(reversed))
	for i, entry := range reversed {
		result[len(reversed)-1-i] = entry
	}

	return result
}

// GetBeforeTime returns entries before the given timestamp.
func (b *Buffer) GetBeforeTime(before time.Time, limit int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return nil
	}

	// Collect in reverse order (newest first), then reverse at end
	// This avoids O(n²) prepend allocations
	var reversed []LogEntry

	// Scan in reverse chronological order (newest first)
	for i := b.size - 1; i >= 0; i-- {
		idx := (b.head - b.size + i + b.maxSize) % b.maxSize
		entry := b.entries[idx]
		if entry.Timestamp.Before(before) {
			reversed = append(reversed, entry)
			if limit > 0 && len(reversed) >= limit {
				break
			}
		}
	}

	// Reverse to get chronological order (oldest first)
	result := make([]LogEntry, len(reversed))
	for i, entry := range reversed {
		result[len(reversed)-1-i] = entry
	}

	return result
}

// GetRange returns entries in the given time range.
func (b *Buffer) GetRange(start, end time.Time, limit int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return nil
	}

	var result []LogEntry

	// Calculate buffer start position
	bufStart := b.head - b.size
	if bufStart < 0 {
		bufStart += b.maxSize
	}

	// Scan in chronological order
	for i := 0; i < b.size; i++ {
		idx := (bufStart + i) % b.maxSize
		entry := b.entries[idx]
		if !entry.Timestamp.Before(start) && !entry.Timestamp.After(end) {
			result = append(result, entry)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}

	return result
}

// Size returns the current number of entries.
func (b *Buffer) Size() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.size
}

// MaxSize returns the maximum capacity.
func (b *Buffer) MaxSize() int {
	return b.maxSize
}

// CurrentSequence returns the current sequence number.
func (b *Buffer) CurrentSequence() uint64 {
	return atomic.LoadUint64(&b.sequence)
}

// Clear removes all entries from the buffer.
func (b *Buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.head = 0
	b.size = 0
}

// OldestTimestamp returns the timestamp of the oldest entry.
func (b *Buffer) OldestTimestamp() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return time.Time{}
	}

	start := b.head - b.size
	if start < 0 {
		start += b.maxSize
	}
	return b.entries[start].Timestamp
}

// NewestTimestamp returns the timestamp of the newest entry.
func (b *Buffer) NewestTimestamp() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		return time.Time{}
	}

	// Head points to next write position, so newest is head-1
	idx := b.head - 1
	if idx < 0 {
		idx = b.maxSize - 1
	}
	return b.entries[idx].Timestamp
}
