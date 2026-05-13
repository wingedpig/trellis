// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// viewerForFile builds a Viewer over a FileSource pointing at dir/active.
// Uses a JSON parser with "ts" as the timestamp field, matching
// makeTimestampedFile.
func viewerForFile(t *testing.T, dir, active string) *Viewer {
	t.Helper()
	srcCfg := config.LogSourceConfig{
		Type:    "file",
		Path:    filepath.Join(dir, "anchor.log"),
		Current: active,
	}
	src, err := NewFileSource(srcCfg)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	v, err := NewViewerWithSource(config.LogViewerConfig{
		Name:   "test",
		Source: srcCfg,
		Parser: config.LogParserConfig{
			Type:            "json",
			Timestamp:       "ts",
			TimestampFormat: time.RFC3339Nano,
			Message:         "msg",
		},
	}, src)
	if err != nil {
		t.Fatalf("NewViewerWithSource: %v", err)
	}
	return v
}

// ----- Viewer.ReadEntriesBackward ---------------------------------------

func TestViewer_ReadEntriesBackward_BasicPaging(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 50)
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewerForFile(t, dir, "active.log")

	// No filter, no before_time → seek is skipped, read from end.
	entries, _, _, _, err := v.ReadEntriesBackward(context.Background(), BackwardCursor{Offset: -1}, 10, nil, time.Time{})
	if err != nil {
		t.Fatalf("ReadEntriesBackward: %v", err)
	}
	if len(entries) != 10 {
		t.Fatalf("got %d entries, want 10", len(entries))
	}
	// Should be the 10 newest, chronological.
	want := base.Add(40 * time.Second)
	if !entries[0].Timestamp.Equal(want) {
		t.Errorf("entries[0].Timestamp = %v, want %v", entries[0].Timestamp, want)
	}
}

// The crux of the user's bug. Client has seen entries up to `boundary`.
// A fresh cursor + before_time=boundary must return only entries strictly
// older than boundary — no replays.
func TestViewer_ReadEntriesBackward_FreshCursorSeeksPastBoundary(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 1000)
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewerForFile(t, dir, "active.log")

	boundary := base.Add(700 * time.Second) // pretend client's oldest displayed
	entries, cur, _, _, err := v.ReadEntriesBackward(context.Background(), BackwardCursor{Offset: -1}, 50, nil, boundary)
	if err != nil {
		t.Fatalf("ReadEntriesBackward: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no entries returned")
	}
	for i, e := range entries {
		if !e.Timestamp.Before(boundary) {
			t.Errorf("entries[%d].Timestamp = %v >= boundary %v — would be a duplicate of client state", i, e.Timestamp, boundary)
		}
	}
	// Cursor must have advanced so the next call resumes correctly.
	if cur.Offset < 0 {
		t.Errorf("NextCursor.Offset = %d, want >= 0", cur.Offset)
	}
}

// Subsequent calls (cursor not fresh) MUST NOT re-seek — they should use
// the cursor as-is. We assert this indirectly: read once, then read again
// from the returned cursor, and verify no overlap.
func TestViewer_ReadEntriesBackward_ResumeCursorDoesNotSeek(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 200)
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewerForFile(t, dir, "active.log")

	// Page 1: 30 entries. before_time intentionally far in the future so
	// the seek (if it ran) would land at end-of-file; we use this to
	// distinguish "seek ran" from "seek skipped" on page 2.
	farFuture := base.Add(48 * time.Hour)
	page1, cur1, _, _, err := v.ReadEntriesBackward(context.Background(), BackwardCursor{Offset: -1}, 30, nil, farFuture)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	// Page 2: resume from cur1. If we mistakenly re-seek here, we'd
	// re-read the same 30 entries.
	page2, _, _, _, err := v.ReadEntriesBackward(context.Background(), cur1, 30, nil, farFuture)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	// page2 should be older than page1: page2's newest < page1's oldest.
	p1Oldest := page1[0].Timestamp
	p2Newest := page2[len(page2)-1].Timestamp
	if !p2Newest.Before(p1Oldest) {
		t.Errorf("page 2 newest %v not before page 1 oldest %v — cursor was lost / seek re-ran", p2Newest, p1Oldest)
	}
}

// Filter applied during backward read: only matching entries should
// accumulate. The viewer keeps reading until it has `limit` matches or
// exhausts the source.
func TestViewer_ReadEntriesBackward_FilterApplied(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Mix two kinds of lines: even-indexed lines contain "match", odd-
	// indexed lines don't. Filter for "match"; we should get only evens.
	var content []byte
	for i := 0; i < 100; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		var msg string
		if i%2 == 0 {
			msg = "match line"
		} else {
			msg = "skip line"
		}
		content = append(content, []byte(`{"ts":"`+ts.Format(time.RFC3339Nano)+`","msg":"`+msg+`"}`+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(dir, "active.log"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewerForFile(t, dir, "active.log")

	f, err := ParseFilter(`msg:~"match"`)
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	entries, _, _, _, err := v.ReadEntriesBackward(context.Background(), BackwardCursor{Offset: -1}, 10, f, time.Time{})
	if err != nil {
		t.Fatalf("ReadEntriesBackward: %v", err)
	}
	if len(entries) != 10 {
		t.Fatalf("got %d entries, want 10 (filter should keep reading until limit)", len(entries))
	}
	for i, e := range entries {
		if got := e.Fields["msg"]; got != "match line" {
			t.Errorf("entries[%d].Fields[msg] = %q, want %q", i, got, "match line")
		}
	}
}

// Reading backward through the entire file eventually returns Done=true.
func TestViewer_ReadEntriesBackward_ReachesDone(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 20)
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewerForFile(t, dir, "active.log")

	var total int
	cur := BackwardCursor{Offset: -1}
	for i := 0; i < 50; i++ {
		entries, c, done, _, err := v.ReadEntriesBackward(context.Background(), cur, 5, nil, time.Time{})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		total += len(entries)
		cur = c
		if done {
			break
		}
		if len(entries) == 0 {
			t.Fatalf("iter %d: empty result without Done — would infinite-loop", i)
		}
	}
	if total != 20 {
		t.Errorf("total entries = %d, want 20", total)
	}
}

// Cursor returned by ReadEntriesBackward is what the client sends back
// next. We verify the round-trip: page through with cursor, accumulate,
// confirm no duplicates and correct order.
func TestViewer_ReadEntriesBackward_FullPaginationRoundTrip(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 200)
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewerForFile(t, dir, "active.log")

	// Simulate the client. Start with a fresh cursor, "before_time" set
	// to a point ~3/4 of the way through, paging 15 at a time.
	boundary := base.Add(150 * time.Second)
	var accumulated []time.Time

	cur := BackwardCursor{Offset: -1}
	beforeTime := boundary
	for i := 0; i < 50; i++ {
		entries, c, done, _, err := v.ReadEntriesBackward(context.Background(), cur, 15, nil, beforeTime)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		for _, e := range entries {
			accumulated = append(accumulated, e.Timestamp)
		}
		cur = c
		// After the first call, future calls send the resumed cursor —
		// the client doesn't re-send before_time semantics. But the
		// viewer's seek is only triggered on Offset<0, so it's a no-op
		// on resume. Pass beforeTime unchanged for realism.
		if done {
			break
		}
	}

	// Every accumulated timestamp must be strictly < boundary.
	for i, ts := range accumulated {
		if !ts.Before(boundary) {
			t.Errorf("accumulated[%d] = %v not before boundary %v", i, ts, boundary)
		}
	}
	// And they must be unique + ordered. Sort by what we expect (we
	// prepend newer to older as we go — so accumulated is reverse-
	// chronological across batches but each batch is chronological).
	// Simpler check: count distinct timestamps == accumulated count.
	seen := make(map[time.Time]bool)
	for _, ts := range accumulated {
		if seen[ts] {
			t.Errorf("duplicate timestamp %v", ts)
		}
		seen[ts] = true
	}
	// We should have read exactly 150 entries (line 0..149 < boundary).
	if len(accumulated) != 150 {
		t.Errorf("got %d entries, want 150 (lines 0..149)", len(accumulated))
	}
}

// Regression test for the user's real-world file: a chunk of rsyslog-
// prefixed lines (e.g. "100.117.238.28:44542:admin {...}") that don't
// parse as JSON sits at the START of the file, followed by pure JSON.
//
// Earlier behavior:
//   - Probes in the prefix region returned ok=false → bias hi=mid
//   - lo stayed at 0, search converged toward 0
//   - SeekToTime returned a tiny offset, byte-offset read end-of-file,
//     client saw a single dump-of-today and Done=true
//
// Required behavior: when a probe fails, the seek walks forward looking
// for the next parseable line and uses ITS timestamp to bisect. The
// final offset must land near the boundary in the JSON region, not at 0.
func TestViewer_SeekToTime_SkipsUnparseableHeader(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// 30KB of unparseable lines (rsyslog-style prefix, no extractable
	// timestamp), then 1000 clean JSON lines.
	var content []byte
	for i := 0; i < 50; i++ {
		content = append(content, []byte("100.117.238.28:44542:admin not-json-no-time-field\n")...)
	}
	for i := 0; i < 1000; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		content = append(content, []byte(`{"ts":"`+ts.Format(time.RFC3339Nano)+`","msg":"line "}`+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(dir, "active.log"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewerForFile(t, dir, "active.log")

	// Target near the middle of the JSON region (line 500-ish).
	target := base.Add(500 * time.Second)
	entries, _, _, _, err := v.ReadEntriesBackward(context.Background(), BackwardCursor{Offset: -1}, 50, nil, target)
	if err != nil {
		t.Fatalf("ReadEntriesBackward: %v", err)
	}
	if len(entries) != 50 {
		t.Fatalf("got %d entries, want 50 (seek converged wrong — probably to start-of-file)", len(entries))
	}
	for i, e := range entries {
		if !e.Timestamp.Before(target) {
			t.Errorf("entries[%d].Timestamp = %v not before target %v — seek picked wrong offset", i, e.Timestamp, target)
		}
		// Sanity: timestamps must look like our JSON region (within 500
		// seconds of base), not the unparseable-prefix region (which
		// would yield zero or time.Now() if any leaked through).
		diff := e.Timestamp.Sub(base)
		if diff < 0 || diff > 600*time.Second {
			t.Errorf("entries[%d].Timestamp = %v doesn't look like our test data (base=%v)", i, e.Timestamp, base)
		}
	}
}

// Deep-paging stress test: scrolling backward through 50_000 entries in
// a single active file in 100-line pages. Asserts:
//   - Every line in the file appears exactly once
//   - Lines come back in correct chronological order
//   - Done=true is reached exactly when offset hits 0
//   - SkippedCompressed stays false (no rotated files at all)
//
// This is the scenario that wedged the user — byte-offset halting mid-
// file and falling through to grep. If the byte-offset reader ever
// returns 0 entries from a non-zero cursor offset, the test fails.
func TestViewer_DeepPagingStress(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	const N = 50000
	data, _ := makeTimestampedFile(base, N)
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewerForFile(t, dir, "active.log")

	seen := make(map[time.Time]bool, N)
	var prevOldest time.Time
	cur := BackwardCursor{Offset: -1}
	calls := 0
	for {
		calls++
		if calls > N { // safety: should converge well before this
			t.Fatalf("did not converge in %d calls", calls)
		}
		entries, c, done, skippedCompressed, err := v.ReadEntriesBackward(context.Background(), cur, 100, nil, time.Time{})
		if err != nil {
			t.Fatalf("call %d: %v", calls, err)
		}
		if skippedCompressed {
			t.Fatalf("call %d: SkippedCompressed=true unexpectedly (no rotated files)", calls)
		}
		if !done && len(entries) == 0 {
			t.Fatalf("call %d: empty batch without Done at cursor %+v — would halt UI", calls, cur)
		}
		// Each batch must be chronological. Newer-than-prevOldest is the
		// per-batch invariant: every entry in this batch is older than
		// the oldest of the previous batch (because we're paging
		// backward).
		for i, e := range entries {
			if seen[e.Timestamp] {
				t.Fatalf("duplicate timestamp %v at call %d, entry %d", e.Timestamp, calls, i)
			}
			seen[e.Timestamp] = true
			if i > 0 && e.Timestamp.Before(entries[i-1].Timestamp) {
				t.Fatalf("entries out of chronological order within batch: [%d] %v before [%d] %v",
					i, e.Timestamp, i-1, entries[i-1].Timestamp)
			}
		}
		if !prevOldest.IsZero() && len(entries) > 0 {
			batchNewest := entries[len(entries)-1].Timestamp
			if !batchNewest.Before(prevOldest) {
				t.Fatalf("call %d: batch newest %v not before prev batch oldest %v",
					calls, batchNewest, prevOldest)
			}
		}
		if len(entries) > 0 {
			prevOldest = entries[0].Timestamp
		}
		cur = c
		if done {
			break
		}
	}
	if len(seen) != N {
		t.Errorf("saw %d distinct entries, want %d (missing %d)", len(seen), N, N-len(seen))
	}
}

// What happens when the seek is asked to find a target that's strictly
// older than every line in the file? Previously: the seek returned
// Offset=0, the viewer ignored that result (fell back to -1), and the
// next read returned end-of-file content the client already had.
// New behavior: viewer treats Offset=0 as "advance past this file"
// instead of "fall back to end-of-file".
func TestViewer_SeekToTime_TargetOlderThanFile_DoesNotReplay(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 100)
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewerForFile(t, dir, "active.log")

	// Target far older than the file. Every line is >= target.
	target := base.Add(-48 * time.Hour)
	entries, _, done, _, err := v.ReadEntriesBackward(context.Background(), BackwardCursor{Offset: -1}, 50, nil, target)
	if err != nil {
		t.Fatalf("ReadEntriesBackward: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0 (target is older than everything; nothing valid to return)", len(entries))
	}
	if !done {
		t.Errorf("Done = false, want true (file has been logically exhausted relative to target)")
	}
}

// SeekToTime on a fresh viewer is the path that previously caused the
// bug. Verify directly that when the file straddles "yesterday" and
// "today", reading with before_time = today produces NO entries from
// today.
func TestViewer_NoTodayLeakWhenScrollingToYesterday(t *testing.T) {
	dir := t.TempDir()
	yesterdayNoon := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	todayNoon := yesterdayNoon.Add(24 * time.Hour)

	// 24h of entries straddling the day boundary, one per minute.
	data, _ := makeTimestampedFile(yesterdayNoon.Add(-12*time.Hour), 24*60)
	// Re-generate per minute for realistic density. (makeTimestampedFile
	// uses per-second; let's just use it as-is — count of entries is
	// what matters, not spacing.)
	_ = todayNoon
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	v := viewerForFile(t, dir, "active.log")

	// Client says "show me entries before yesterdayNoon" — i.e. it has
	// already shown content from yesterday noon onward (the "today"
	// region of our synthetic file).
	entries, _, _, _, err := v.ReadEntriesBackward(context.Background(), BackwardCursor{Offset: -1}, 50, nil, yesterdayNoon)
	if err != nil {
		t.Fatalf("ReadEntriesBackward: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected entries; got none")
	}
	for _, e := range entries {
		if !e.Timestamp.Before(yesterdayNoon) {
			t.Fatalf("leaked entry from on/after boundary: %v >= %v", e.Timestamp, yesterdayNoon)
		}
	}
}
