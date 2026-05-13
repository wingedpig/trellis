// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// bytesReaderAt is a readerAt over an in-memory byte slice. Lets us drive
// the backward-paging primitives without touching the filesystem.
type bytesReaderAt struct{ data []byte }

func (b *bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off > int64(len(b.data)) {
		return 0, fmt.Errorf("read at %d out of range", off)
	}
	n := copy(p, b.data[off:])
	if n < len(p) {
		// Mirror os.File semantics: short read is OK at EOF.
		return n, nil
	}
	return n, nil
}

// ----- splitLinesKeepNL ---------------------------------------------------

func TestSplitLinesKeepNL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"one_line_with_nl", "abc\n", []string{"abc\n"}},
		{"one_line_no_nl", "abc", []string{"abc"}},
		{"two_lines", "abc\ndef\n", []string{"abc\n", "def\n"}},
		{"two_lines_trailing_partial", "abc\ndef", []string{"abc\n", "def"}},
		{"just_newlines", "\n\n\n", []string{"\n", "\n", "\n"}},
		{"empty_then_content", "\nabc\n", []string{"\n", "abc\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitLinesKeepNL([]byte(c.in))
			gotStrs := make([]string, len(got))
			for i, b := range got {
				gotStrs[i] = string(b)
			}
			if len(gotStrs) != len(c.want) {
				t.Fatalf("len = %d, want %d (got %q)", len(gotStrs), len(c.want), gotStrs)
			}
			for i := range gotStrs {
				if gotStrs[i] != c.want[i] {
					t.Errorf("[%d] = %q, want %q", i, gotStrs[i], c.want[i])
				}
			}
		})
	}
}

// ----- readBackwardFromReader --------------------------------------------

func TestReadBackwardFromReader_Empty(t *testing.T) {
	r := &bytesReaderAt{data: nil}
	lines, off, err := readBackwardFromReader(r, 0, 10, 64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 0 || off != 0 {
		t.Fatalf("got lines=%v off=%d, want empty/0", lines, off)
	}
}

// Request fewer lines than the file holds — we should get exactly the
// most recent N lines and a non-zero newOffset.
func TestReadBackwardFromReader_RequestSubset(t *testing.T) {
	content := "a\nb\nc\nd\ne\n"
	r := &bytesReaderAt{data: []byte(content)}
	lines, newOff, err := readBackwardFromReader(r, int64(len(content)), 2, 64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(lines, "|"), "d|e"; got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
	// "d\ne\n" = 4 bytes. newOff = 10 - 4 = 6 → start of "d".
	if newOff != 6 {
		t.Errorf("newOff = %d, want 6", newOff)
	}
}

// Request more than the file holds — we should consume everything and
// land at offset 0.
func TestReadBackwardFromReader_RequestAll(t *testing.T) {
	content := "a\nb\nc\n"
	r := &bytesReaderAt{data: []byte(content)}
	lines, newOff, err := readBackwardFromReader(r, int64(len(content)), 100, 64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(lines, "|"), "a|b|c"; got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if newOff != 0 {
		t.Errorf("newOff = %d, want 0", newOff)
	}
}

// File ends without trailing newline — the last "line" should still be
// returned.
func TestReadBackwardFromReader_NoTrailingNewline(t *testing.T) {
	content := "a\nb\nc"
	r := &bytesReaderAt{data: []byte(content)}
	lines, _, err := readBackwardFromReader(r, int64(len(content)), 100, 64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(lines, "|"), "a|b|c"; got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

// blockSize smaller than a line forces the loop to iterate; the function
// must still drop the leading partial line correctly.
func TestReadBackwardFromReader_SmallBlockSize(t *testing.T) {
	content := "alpha\nbeta\ngamma\ndelta\n"
	r := &bytesReaderAt{data: []byte(content)}
	// blockSize=4: each chunk is smaller than a single line. We request
	// 2 lines so the loop must read multiple chunks before having enough
	// newlines, AND we must NOT confuse the partial-leading-line drop
	// across chunk boundaries.
	lines, newOff, err := readBackwardFromReader(r, int64(len(content)), 2, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(lines, "|"), "gamma|delta"; got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
	// "gamma\ndelta\n" = 12 bytes. newOff = len - 12 = 11.
	if want := int64(len(content) - 12); newOff != want {
		t.Errorf("newOff = %d, want %d", newOff, want)
	}
}

// A single line longer than blockSize must still be returned in full when
// we read all the way to offset 0.
func TestReadBackwardFromReader_LongLine(t *testing.T) {
	long := strings.Repeat("x", 1000)
	content := long + "\n"
	r := &bytesReaderAt{data: []byte(content)}
	lines, newOff, err := readBackwardFromReader(r, int64(len(content)), 5, 64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 || lines[0] != long {
		t.Errorf("got lines=%v, want one line of length %d", lines, len(long))
	}
	if newOff != 0 {
		t.Errorf("newOff = %d, want 0", newOff)
	}
}

// newOffset must point at the start of the first returned line, so a
// follow-up call with endOffset=newOffset returns the previous page with
// no overlap.
func TestReadBackwardFromReader_NewOffsetIsConsistent(t *testing.T) {
	content := "a\nb\nc\nd\ne\nf\n"
	r := &bytesReaderAt{data: []byte(content)}

	// Page 1: last 2 lines.
	lines1, off1, err := readBackwardFromReader(r, int64(len(content)), 2, 64)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	// Page 2: starting from off1, 2 more.
	lines2, off2, err := readBackwardFromReader(r, off1, 2, 64)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	full := append([]string{}, lines2...)
	full = append(full, lines1...)
	if got, want := strings.Join(full, "|"), "c|d|e|f"; got != want {
		t.Errorf("combined = %q, want %q", got, want)
	}
	if off2 != 4 {
		t.Errorf("off2 = %d, want 4 (start of 'c')", off2)
	}
}

// ----- probeTimestampAt + findOffsetForTime ------------------------------

// makeTimestampedFile produces a synthetic log: one line per second
// starting at base, with parseable JSON timestamps. Returns the raw
// content and a (line index → byte offset) map for assertions.
func makeTimestampedFile(base time.Time, count int) ([]byte, []int64) {
	var sb strings.Builder
	offsets := make([]int64, count)
	for i := 0; i < count; i++ {
		offsets[i] = int64(sb.Len())
		ts := base.Add(time.Duration(i) * time.Second)
		fmt.Fprintf(&sb, `{"ts":"%s","msg":"line %d"}`, ts.Format(time.RFC3339Nano), i)
		sb.WriteByte('\n')
	}
	return []byte(sb.String()), offsets
}

func tsParser(line string) time.Time {
	// Minimal extractor: pull whatever is between `"ts":"` and `"`.
	const key = `"ts":"`
	i := strings.Index(line, key)
	if i < 0 {
		return time.Time{}
	}
	rest := line[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, rest[:j])
	if err != nil {
		return time.Time{}
	}
	return t
}

func TestProbeTimestampAt(t *testing.T) {
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, offsets := makeTimestampedFile(base, 10)
	r := &bytesReaderAt{data: data}

	// Probe near the middle: result should be from somewhere in [start,
	// start+probeSize) and the returned timestamp must match the line at
	// the returned offset.
	probeOff := offsets[5]
	ts, lineOff, ok := probeTimestampAt(r, probeOff, 256, tsParser)
	if !ok {
		t.Fatalf("probe failed at offset %d", probeOff)
	}
	if lineOff <= probeOff {
		t.Errorf("expected lineOff > probeOff (since we skip the partial leading line): got %d, probeOff %d", lineOff, probeOff)
	}
	// The line at lineOff must parse to one of our generated timestamps.
	if ts.IsZero() {
		t.Errorf("returned zero timestamp")
	}
}

func TestProbeTimestampAt_NoNewline(t *testing.T) {
	// 1 KB blob with no newlines.
	r := &bytesReaderAt{data: bytes.Repeat([]byte("x"), 1024)}
	_, _, ok := probeTimestampAt(r, 0, 256, tsParser)
	if ok {
		t.Errorf("expected probe to fail on a buffer with no newlines")
	}
}

func TestFindOffsetForTime_Empty(t *testing.T) {
	r := &bytesReaderAt{data: nil}
	got := findOffsetForTime(context.Background(), r, 0, time.Now(), tsParser)
	if got != 0 {
		t.Errorf("findOffsetForTime on empty file = %d, want 0", got)
	}
}

func TestFindOffsetForTime_TargetOlderThanAll(t *testing.T) {
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 100)
	r := &bytesReaderAt{data: data}

	// Target way older than the file. Every line is >= target, so the
	// answer is offset 0 — reading backward from 0 yields nothing.
	got := findOffsetForTime(context.Background(), r, int64(len(data)), base.Add(-24*time.Hour), tsParser)
	if got != 0 {
		t.Errorf("got %d, want 0 (all lines >= target)", got)
	}
}

func TestFindOffsetForTime_TargetNewerThanAll(t *testing.T) {
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 100)
	r := &bytesReaderAt{data: data}

	// Target way newer than the file. Every line is < target. Reading
	// backward from the returned offset should yield the entire file.
	got := findOffsetForTime(context.Background(), r, int64(len(data)), base.Add(24*time.Hour), tsParser)
	if got != int64(len(data)) {
		t.Errorf("got %d, want %d (all lines < target)", got, len(data))
	}
}

// The key correctness test for the user's bug: scrolling from "today"
// into "yesterday" must NOT replay today's lines. findOffsetForTime
// pointed *at* the boundary line; reading backward from there must yield
// only lines strictly before the target.
func TestFindOffsetForTime_NoOverlapAcrossBoundary(t *testing.T) {
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, offsets := makeTimestampedFile(base, 1000)
	r := &bytesReaderAt{data: data}

	// Target = timestamp of line 600. Lines 600..999 are >= target, lines
	// 0..599 are < target. findOffsetForTime should return offsets[600].
	target := base.Add(600 * time.Second)
	got := findOffsetForTime(context.Background(), r, int64(len(data)), target, tsParser)
	if got != offsets[600] {
		t.Errorf("got %d, want %d (offset of line 600)", got, offsets[600])
	}

	// Reading backward from `got` yields lines 599, 598, ...
	lines, _, err := readBackwardFromReader(r, got, 5, 64*1024)
	if err != nil {
		t.Fatalf("readBackward: %v", err)
	}
	// We asked for 5; should be lines 595..599 in chronological order.
	for i, line := range lines {
		want := 595 + i
		ts := tsParser(line)
		expected := base.Add(time.Duration(want) * time.Second)
		if !ts.Equal(expected) {
			t.Errorf("line[%d] ts = %v, want %v (line %d)", i, ts, expected, want)
		}
		if !ts.Before(target) {
			t.Errorf("line[%d] ts = %v, not before target %v — would cause duplicate", i, ts, target)
		}
	}
}

// ----- readBackwardAcrossFiles -------------------------------------------

// fakeOpener implements readBackwardOpener for testing the multi-file
// driver against in-memory data.
type fakeOpener struct {
	files []fakeFile
}

type fakeFile struct {
	path       string
	content    []byte
	compressed bool
}

func (o *fakeOpener) openBackward(_ context.Context, idx int) (readerAt, func(), int64, string, bool, bool) {
	if idx < 0 || idx >= len(o.files) {
		return nil, nil, 0, "", false, false
	}
	f := o.files[idx]
	if f.compressed {
		return nil, nil, int64(len(f.content)), f.path, true, true
	}
	return &bytesReaderAt{data: f.content}, nil, int64(len(f.content)), f.path, false, true
}

func TestReadBackwardAcrossFiles_SingleFile(t *testing.T) {
	o := &fakeOpener{files: []fakeFile{
		{path: "active", content: []byte("a\nb\nc\nd\ne\n")},
	}}
	res, err := readBackwardAcrossFiles(context.Background(), o, 1, BackwardCursor{Offset: -1}, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(res.Lines, "|"), "d|e"; got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if res.Done {
		t.Errorf("Done = true, want false (file not exhausted)")
	}
	if res.NextCursor.FileIndex != 0 || res.NextCursor.Offset == -1 {
		t.Errorf("NextCursor = %+v, want FileIndex=0 with positive offset", res.NextCursor)
	}
}

func TestReadBackwardAcrossFiles_ResumeCursor(t *testing.T) {
	content := []byte("a\nb\nc\nd\ne\n")
	o := &fakeOpener{files: []fakeFile{{path: "active", content: content}}}

	// Page 1: 2 lines.
	res1, err := readBackwardAcrossFiles(context.Background(), o, 1, BackwardCursor{Offset: -1}, 2)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	// Page 2: resume from res1.NextCursor.
	res2, err := readBackwardAcrossFiles(context.Background(), o, 1, res1.NextCursor, 2)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	combined := append([]string{}, res2.Lines...)
	combined = append(combined, res1.Lines...)
	if got, want := strings.Join(combined, "|"), "b|c|d|e"; got != want {
		t.Errorf("combined = %q, want %q", got, want)
	}
}

func TestReadBackwardAcrossFiles_CrossFileBoundary(t *testing.T) {
	o := &fakeOpener{files: []fakeFile{
		{path: "active", content: []byte("c\nd\n")},  // newest
		{path: "rot.1", content: []byte("a\nb\n")},   // older
	}}

	// Request more lines than the active file holds — must cross into
	// the older file.
	res, err := readBackwardAcrossFiles(context.Background(), o, 2, BackwardCursor{Offset: -1}, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(res.Lines, "|"), "a|b|c|d"; got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if !res.Done {
		t.Errorf("Done = false, want true (both files exhausted)")
	}
}

func TestReadBackwardAcrossFiles_CompressedSkipped(t *testing.T) {
	o := &fakeOpener{files: []fakeFile{
		{path: "active", content: []byte("c\nd\n")},
		{path: "rot.1.gz", compressed: true, content: []byte("x\ny\n")}, // skipped
		{path: "rot.2", content: []byte("a\nb\n")},                       // read
	}}
	res, err := readBackwardAcrossFiles(context.Background(), o, 3, BackwardCursor{Offset: -1}, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Compressed file's content must NOT appear.
	if strings.Contains(strings.Join(res.Lines, "|"), "x") {
		t.Errorf("compressed file content leaked into result: %v", res.Lines)
	}
	if got, want := strings.Join(res.Lines, "|"), "a|b|c|d"; got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if !res.Done {
		t.Errorf("Done = false, want true")
	}
}

func TestReadBackwardAcrossFiles_AllCompressed(t *testing.T) {
	o := &fakeOpener{files: []fakeFile{
		{path: "rot.1.gz", compressed: true, content: []byte("a\n")},
		{path: "rot.2.gz", compressed: true, content: []byte("b\n")},
	}}
	res, err := readBackwardAcrossFiles(context.Background(), o, 2, BackwardCursor{Offset: -1}, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Lines) != 0 {
		t.Errorf("lines = %v, want empty (all files compressed)", res.Lines)
	}
	if !res.Done {
		t.Errorf("Done = false, want true")
	}
}

func TestReadBackwardAcrossFiles_RespectsLimit(t *testing.T) {
	// Three files, requesting only 2 lines — should not read beyond
	// what's needed and should return cursor mid-file.
	o := &fakeOpener{files: []fakeFile{
		{path: "active", content: []byte("e\nf\ng\nh\n")},
		{path: "rot.1", content: []byte("a\nb\nc\nd\n")},
	}}
	res, err := readBackwardAcrossFiles(context.Background(), o, 2, BackwardCursor{Offset: -1}, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(res.Lines, "|"), "g|h"; got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if res.Done {
		t.Errorf("Done = true, want false (haven't exhausted)")
	}
	if res.NextCursor.FileIndex != 0 {
		t.Errorf("NextCursor.FileIndex = %d, want 0 (still in active file)", res.NextCursor.FileIndex)
	}
}
