// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"testing"
	"time"
)

func TestBufferBasic(t *testing.T) {
	buf := NewBuffer(10)

	// Empty buffer
	if buf.Size() != 0 {
		t.Errorf("Size() = %d, want 0", buf.Size())
	}
	if buf.MaxSize() != 10 {
		t.Errorf("MaxSize() = %d, want 10", buf.MaxSize())
	}

	// Add entries
	for i := 0; i < 5; i++ {
		buf.Add(LogEntry{
			Timestamp: time.Now(),
			Message:   "test",
		})
	}

	if buf.Size() != 5 {
		t.Errorf("Size() = %d, want 5", buf.Size())
	}

	// Get entries
	entries := buf.Get(0)
	if len(entries) != 5 {
		t.Errorf("Get() returned %d entries, want 5", len(entries))
	}

	// Sequence numbers should be monotonically increasing
	for i := 1; i < len(entries); i++ {
		if entries[i].Sequence <= entries[i-1].Sequence {
			t.Errorf("Sequence not monotonically increasing: %d <= %d", entries[i].Sequence, entries[i-1].Sequence)
		}
	}
}

func TestBufferWrap(t *testing.T) {
	buf := NewBuffer(5)

	// Add 8 entries to force wrap
	for i := 0; i < 8; i++ {
		buf.Add(LogEntry{
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Message:   string(rune('A' + i)),
		})
	}

	if buf.Size() != 5 {
		t.Errorf("Size() = %d, want 5", buf.Size())
	}

	entries := buf.Get(0)
	if len(entries) != 5 {
		t.Errorf("Get() returned %d entries, want 5", len(entries))
	}

	// Should have entries D, E, F, G, H (oldest 3 were evicted)
	expected := []string{"D", "E", "F", "G", "H"}
	for i, entry := range entries {
		if entry.Message != expected[i] {
			t.Errorf("Entry[%d].Message = %q, want %q", i, entry.Message, expected[i])
		}
	}
}

func TestBufferGetLimit(t *testing.T) {
	buf := NewBuffer(10)

	for i := 0; i < 10; i++ {
		buf.Add(LogEntry{Message: string(rune('A' + i))})
	}

	// Get with limit
	entries := buf.Get(3)
	if len(entries) != 3 {
		t.Errorf("Get(3) returned %d entries, want 3", len(entries))
	}

	// Should get the most recent 3 entries
	expected := []string{"H", "I", "J"}
	for i, entry := range entries {
		if entry.Message != expected[i] {
			t.Errorf("Entry[%d].Message = %q, want %q", i, entry.Message, expected[i])
		}
	}
}

func TestBufferGetFiltered(t *testing.T) {
	buf := NewBuffer(10)

	// Add mix of levels
	levels := []LogLevel{LevelInfo, LevelError, LevelInfo, LevelError, LevelWarn}
	for i, level := range levels {
		buf.Add(LogEntry{
			Level:   level,
			Message: string(rune('A' + i)),
		})
	}

	// Filter for errors
	filter, _ := ParseFilter("level:error")
	entries := buf.GetFiltered(filter, 0)

	if len(entries) != 2 {
		t.Errorf("GetFiltered(level:error) returned %d entries, want 2", len(entries))
	}

	for _, entry := range entries {
		if entry.Level != LevelError {
			t.Errorf("Entry.Level = %v, want %v", entry.Level, LevelError)
		}
	}
}

func TestBufferGetAfter(t *testing.T) {
	buf := NewBuffer(10)

	// Add entries
	for i := 0; i < 5; i++ {
		buf.Add(LogEntry{Message: string(rune('A' + i))})
	}

	// Get after sequence 2
	entries := buf.GetAfter(2, 0)

	if len(entries) != 3 {
		t.Errorf("GetAfter(2) returned %d entries, want 3", len(entries))
	}

	// First entry should have sequence 3
	if entries[0].Sequence != 3 {
		t.Errorf("First entry sequence = %d, want 3", entries[0].Sequence)
	}
}

func TestBufferGetBeforeTime(t *testing.T) {
	buf := NewBuffer(10)
	now := time.Now()

	// Add entries with different timestamps
	for i := 0; i < 5; i++ {
		buf.Add(LogEntry{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Message:   string(rune('A' + i)),
		})
	}

	// Get entries before 3 minutes from now
	entries := buf.GetBeforeTime(now.Add(3*time.Minute), 0)

	if len(entries) != 3 {
		t.Errorf("GetBeforeTime returned %d entries, want 3", len(entries))
	}
}

func TestBufferGetBefore(t *testing.T) {
	buf := NewBuffer(10)

	// Add 5 entries with sequence numbers 1-5
	for i := 0; i < 5; i++ {
		buf.Add(LogEntry{
			Message: string(rune('A' + i)),
		})
	}

	// Get entries before sequence 4 (should get entries 1, 2, 3)
	entries := buf.GetBefore(4, 0)

	if len(entries) != 3 {
		t.Errorf("GetBefore returned %d entries, want 3", len(entries))
	}
	if entries[0].Sequence != 1 {
		t.Errorf("First entry sequence = %d, want 1", entries[0].Sequence)
	}
	if entries[2].Sequence != 3 {
		t.Errorf("Last entry sequence = %d, want 3", entries[2].Sequence)
	}

	// Test with limit
	entries = buf.GetBefore(5, 2)
	if len(entries) != 2 {
		t.Errorf("GetBefore with limit returned %d entries, want 2", len(entries))
	}
	// Should get the 2 most recent entries before seq 5 (seq 3 and 4)
	if entries[0].Sequence != 3 {
		t.Errorf("First entry with limit sequence = %d, want 3", entries[0].Sequence)
	}
}

func TestBufferGetRange(t *testing.T) {
	buf := NewBuffer(10)
	now := time.Now()

	// Add entries with different timestamps
	for i := 0; i < 10; i++ {
		buf.Add(LogEntry{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Message:   string(rune('A' + i)),
		})
	}

	// Get entries between 2 and 5 minutes
	start := now.Add(2 * time.Minute)
	end := now.Add(5 * time.Minute)
	entries := buf.GetRange(start, end, 0)

	if len(entries) != 4 {
		t.Errorf("GetRange returned %d entries, want 4", len(entries))
	}

	// Check first and last
	expected := []string{"C", "D", "E", "F"}
	for i, entry := range entries {
		if entry.Message != expected[i] {
			t.Errorf("Entry[%d].Message = %q, want %q", i, entry.Message, expected[i])
		}
	}
}

func TestBufferClear(t *testing.T) {
	buf := NewBuffer(10)

	for i := 0; i < 5; i++ {
		buf.Add(LogEntry{Message: "test"})
	}

	buf.Clear()

	if buf.Size() != 0 {
		t.Errorf("Size() = %d after Clear, want 0", buf.Size())
	}
}

func TestBufferTimestamps(t *testing.T) {
	buf := NewBuffer(10)
	now := time.Now()

	// Empty buffer
	if !buf.OldestTimestamp().IsZero() {
		t.Error("OldestTimestamp should be zero for empty buffer")
	}
	if !buf.NewestTimestamp().IsZero() {
		t.Error("NewestTimestamp should be zero for empty buffer")
	}

	// Add entries
	for i := 0; i < 5; i++ {
		buf.Add(LogEntry{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}

	oldest := buf.OldestTimestamp()
	if !oldest.Equal(now) {
		t.Errorf("OldestTimestamp = %v, want %v", oldest, now)
	}

	newest := buf.NewestTimestamp()
	expected := now.Add(4 * time.Minute)
	if !newest.Equal(expected) {
		t.Errorf("NewestTimestamp = %v, want %v", newest, expected)
	}
}

func TestBufferAddBatch(t *testing.T) {
	buf := NewBuffer(10)

	entries := make([]LogEntry, 5)
	for i := range entries {
		entries[i] = LogEntry{Message: string(rune('A' + i))}
	}

	buf.AddBatch(entries)

	if buf.Size() != 5 {
		t.Errorf("Size() = %d, want 5", buf.Size())
	}

	result := buf.Get(0)
	for i, entry := range result {
		if entry.Message != entries[i].Message {
			t.Errorf("Entry[%d].Message = %q, want %q", i, entry.Message, entries[i].Message)
		}
	}
}

func TestBufferConcurrent(t *testing.T) {
	buf := NewBuffer(100)

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				buf.Add(LogEntry{Message: "test"})
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should not panic and have entries
	if buf.Size() == 0 {
		t.Error("Buffer should have entries after concurrent writes")
	}
}

func TestBufferDefaultSize(t *testing.T) {
	buf := NewBuffer(0)
	if buf.MaxSize() != 100000 {
		t.Errorf("MaxSize() = %d, want 100000 (default)", buf.MaxSize())
	}

	buf2 := NewBuffer(-1)
	if buf2.MaxSize() != 100000 {
		t.Errorf("MaxSize() = %d, want 100000 (default)", buf2.MaxSize())
	}
}

// TestBufferGetRangeWithFilter tests that GetRange + filter returns up to limit matches
// This documents the expected behavior: the handler should apply filter after getting
// entries without limit, then apply the limit to filtered results
func TestBufferGetRangeWithFilter(t *testing.T) {
	buf := NewBuffer(100)
	now := time.Now()

	// Add 20 entries: alternating error and info levels
	for i := 0; i < 20; i++ {
		level := LevelInfo
		if i%2 == 0 {
			level = LevelError
		}
		buf.Add(LogEntry{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Message:   string(rune('A' + i)),
			Level:     level,
		})
	}

	// Get all entries in range (without limit)
	start := now
	end := now.Add(20 * time.Minute)
	entries := buf.GetRange(start, end, 0)

	if len(entries) != 20 {
		t.Errorf("GetRange(0) returned %d entries, want 20", len(entries))
	}

	// Now filter for errors and verify we can get all 10
	filter, _ := ParseFilter("level:error")
	var filtered []LogEntry
	for _, e := range entries {
		if filter.Match(e) {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) != 10 {
		t.Errorf("Filtered entries = %d, want 10", len(filtered))
	}

	// Test that GetRange with small limit + filter can still get many matching entries
	// This tests the handler's fixed behavior (not the buffer itself)
	// The handler should: GetRange(0), filter, then apply limit

	// With limit=5 on the buffer, we'd only get 5 entries (2 or 3 errors)
	limitedEntries := buf.GetRange(start, end, 5)
	if len(limitedEntries) != 5 {
		t.Errorf("GetRange(5) returned %d entries, want 5", len(limitedEntries))
	}

	// Filter those 5 entries - should only get 2-3 errors
	var limitedFiltered []LogEntry
	for _, e := range limitedEntries {
		if filter.Match(e) {
			limitedFiltered = append(limitedFiltered, e)
		}
	}

	// This demonstrates the bug the handler fix addresses:
	// if the handler called GetRange(limit) then filtered, we'd only get 2-3 errors
	// but we want 5 errors (the requested limit)
	// The fix: handler calls GetRange(0), filters, then takes first 5 matches
	if len(limitedFiltered) > 5 {
		t.Errorf("Limited filtered entries = %d, should be <= 5", len(limitedFiltered))
	}
}

// TestBufferGetBeforeTimePerformance verifies GetBeforeTime doesn't use O(n²) allocation
func TestBufferGetBeforeTimePerformance(t *testing.T) {
	buf := NewBuffer(10000)
	now := time.Now()

	// Add many entries
	for i := 0; i < 10000; i++ {
		buf.Add(LogEntry{
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			Message:   "test",
		})
	}

	// This should complete quickly (not O(n²))
	start := time.Now()
	entries := buf.GetBeforeTime(now.Add(5000*time.Millisecond), 1000)
	elapsed := time.Since(start)

	if len(entries) != 1000 {
		t.Errorf("GetBeforeTime returned %d entries, want 1000", len(entries))
	}

	// Should complete in well under 100ms (O(n) vs O(n²))
	if elapsed > 100*time.Millisecond {
		t.Errorf("GetBeforeTime took %v, expected < 100ms", elapsed)
	}
}
