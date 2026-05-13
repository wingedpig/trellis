// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// writeLogFile creates a temp file with the given lines (each terminated
// by '\n'). Returns the absolute path.
func writeLogFile(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// fileSourceForTest builds a FileSource pointing at a directory with the
// given active filename. RotatedPattern is set so ListRotatedFiles will
// find rotated.* files alongside.
func fileSourceForTest(t *testing.T, dir, active string) *FileSource {
	t.Helper()
	cfg := config.LogSourceConfig{
		Type:           "file",
		Path:           filepath.Join(dir, "anchor.log"), // Path is treated as a file; Dir() points at our dir
		Current:        active,
		RotatedPattern: "rotated.*",
	}
	src, err := NewFileSource(cfg)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	return src
}

// readAll repeatedly calls ReadBackward, accumulating lines until Done.
// Returns lines in chronological order (oldest first).
func readAll(t *testing.T, src *FileSource, page int) []string {
	t.Helper()
	var all []string
	cur := BackwardCursor{Offset: -1}
	for i := 0; i < 1000; i++ { // safety cap
		res, err := src.ReadBackward(context.Background(), cur, page)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		all = append(res.Lines, all...)
		cur = res.NextCursor
		if res.Done {
			return all
		}
		if len(res.Lines) == 0 {
			t.Fatalf("iter %d: empty result but not done — would infinite-loop", i)
		}
	}
	t.Fatalf("did not converge in 1000 iterations")
	return nil
}

// ----- FileSource.ReadBackward -------------------------------------------

func TestFileSource_ReadBackward_SingleFile(t *testing.T) {
	dir := t.TempDir()
	writeLogFile(t, dir, "active.log", []string{"a", "b", "c", "d", "e"})
	src := fileSourceForTest(t, dir, "active.log")

	res, err := src.ReadBackward(context.Background(), BackwardCursor{Offset: -1}, 3)
	if err != nil {
		t.Fatalf("ReadBackward: %v", err)
	}
	if got, want := strings.Join(res.Lines, "|"), "c|d|e"; got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if res.Done {
		t.Errorf("Done = true; want false (2 lines remain)")
	}
}

func TestFileSource_ReadBackward_PaginateAcrossRotation(t *testing.T) {
	dir := t.TempDir()
	// Active has the newest content; rotated.1 is older.
	writeLogFile(t, dir, "active.log", []string{"e", "f", "g"})
	rotated := writeLogFile(t, dir, "rotated.1", []string{"a", "b", "c", "d"})
	// Backdate rotated.1 so the newest-first sort puts it after active.
	older := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(rotated, older, older); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	src := fileSourceForTest(t, dir, "active.log")
	got := readAll(t, src, 2) // small page size forces multi-call paging
	want := []string{"a", "b", "c", "d", "e", "f", "g"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got = %v, want = %v", got, want)
	}
}

func TestFileSource_ReadBackward_EmptyActiveFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "active.log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	src := fileSourceForTest(t, dir, "active.log")
	res, err := src.ReadBackward(context.Background(), BackwardCursor{Offset: -1}, 10)
	if err != nil {
		t.Fatalf("ReadBackward: %v", err)
	}
	if len(res.Lines) != 0 {
		t.Errorf("lines = %v, want empty", res.Lines)
	}
	if !res.Done {
		t.Errorf("Done = false, want true (no content anywhere)")
	}
}

func TestFileSource_ReadBackward_ResumeCursorAdvancesByPage(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%02d", i)
	}
	writeLogFile(t, dir, "active.log", lines)
	src := fileSourceForTest(t, dir, "active.log")

	// Page through 5 at a time.
	var got []string
	cur := BackwardCursor{Offset: -1}
	for {
		res, err := src.ReadBackward(context.Background(), cur, 5)
		if err != nil {
			t.Fatalf("ReadBackward: %v", err)
		}
		got = append(res.Lines, got...)
		cur = res.NextCursor
		if res.Done {
			break
		}
	}
	if strings.Join(got, "|") != strings.Join(lines, "|") {
		t.Errorf("got %v, want %v", got, lines)
	}
}

// ----- FileSource.SeekToTime ---------------------------------------------

func TestFileSource_SeekToTime_FindsBoundary(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 1000)
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	src := fileSourceForTest(t, dir, "active.log")

	// Target = line 600's timestamp. Reading backward from the seek
	// result must yield only lines strictly before that time.
	target := base.Add(600 * time.Second)
	cur, err := src.SeekToTime(context.Background(), target, tsParser)
	if err != nil {
		t.Fatalf("SeekToTime: %v", err)
	}
	if cur.Offset <= 0 {
		t.Fatalf("expected non-zero offset, got %d", cur.Offset)
	}

	res, err := src.ReadBackward(context.Background(), cur, 10)
	if err != nil {
		t.Fatalf("ReadBackward: %v", err)
	}
	for i, line := range res.Lines {
		ts := tsParser(line)
		if ts.IsZero() {
			t.Fatalf("line %d unparseable: %q", i, line)
		}
		if !ts.Before(target) {
			t.Errorf("line %d ts %v is not < target %v (would be duplicate)", i, ts, target)
		}
	}
}

func TestFileSource_SeekToTime_TargetNewerThanAll(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 100)
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	src := fileSourceForTest(t, dir, "active.log")

	// Target far in the future: every line is < target, so the seek
	// should land at end-of-file and reading backward yields the whole
	// file.
	cur, err := src.SeekToTime(context.Background(), base.Add(48*time.Hour), tsParser)
	if err != nil {
		t.Fatalf("SeekToTime: %v", err)
	}
	if cur.Offset != int64(len(data)) {
		t.Errorf("Offset = %d, want %d (file size)", cur.Offset, len(data))
	}
}

func TestFileSource_SeekToTime_TargetOlderThanAll(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	data, _ := makeTimestampedFile(base, 100)
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	src := fileSourceForTest(t, dir, "active.log")

	// Target way before the file: every line is >= target. Seek should
	// land at offset 0 (nothing to read backward).
	cur, err := src.SeekToTime(context.Background(), base.Add(-48*time.Hour), tsParser)
	if err != nil {
		t.Fatalf("SeekToTime: %v", err)
	}
	if cur.Offset != 0 {
		t.Errorf("Offset = %d, want 0", cur.Offset)
	}
}

// The user's bug, end-to-end at the source level: a fresh cursor + a
// target in the middle of the active file must NOT return any lines newer
// than the target.
func TestFileSource_NoDuplicatesAcrossBoundary(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC) // "today"
	yesterday := base.Add(-12 * time.Hour)
	data, _ := makeTimestampedFile(yesterday, 2000) // straddles into today
	if err := os.WriteFile(filepath.Join(dir, "active.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	src := fileSourceForTest(t, dir, "active.log")

	// Client says: "I've shown entries up to `base` (today, noon). Give
	// me what's before." Seek-then-read should return only pre-`base`
	// entries.
	target := base
	cur, err := src.SeekToTime(context.Background(), target, tsParser)
	if err != nil {
		t.Fatalf("SeekToTime: %v", err)
	}
	res, err := src.ReadBackward(context.Background(), cur, 100)
	if err != nil {
		t.Fatalf("ReadBackward: %v", err)
	}
	if len(res.Lines) == 0 {
		t.Fatalf("no lines returned")
	}
	for _, line := range res.Lines {
		ts := tsParser(line)
		if !ts.Before(target) {
			t.Fatalf("line ts %v >= target %v — the seek failed, would replay client content", ts, target)
		}
	}
}
