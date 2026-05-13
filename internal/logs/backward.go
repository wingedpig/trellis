// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"bytes"
	"context"
	"time"
)

// readBackwardFromReader pages backward through a seekable byte stream and
// returns up to `maxLines` complete lines whose ending position is strictly
// less than `endOffset`. Shared between FileSource (os.File via ReaderAt)
// and any other source that can expose a ReaderAt over its content.
//
// reader.ReadAt is called with chunks of `blockSize` bytes; the function
// keeps reading backward until it has gathered at least `maxLines + 1`
// newlines OR it reaches the start of the stream. The leading partial line
// (the bytes before the first newline in the assembled buffer) is dropped
// when we didn't read all the way to offset 0 — otherwise it would be a
// truncated entry. The returned newOffset points at the position of the
// first byte of the first returned line, so callers can pass it back as
// `endOffset` to continue paging.
//
// Returns the lines in chronological order (oldest first).
type readerAt interface {
	ReadAt(p []byte, off int64) (n int, err error)
}

func readBackwardFromReader(reader readerAt, endOffset int64, maxLines int, blockSize int) (lines []string, newOffset int64, err error) {
	if maxLines <= 0 || endOffset <= 0 {
		return nil, endOffset, nil
	}
	if blockSize <= 0 {
		blockSize = 64 * 1024
	}

	pos := endOffset
	newlineCount := 0
	// Build the buffer by prepending each chunk we read.
	var buf []byte
	for pos > 0 && newlineCount <= maxLines {
		read := int64(blockSize)
		if read > pos {
			read = pos
		}
		pos -= read
		chunk := make([]byte, read)
		if _, rerr := reader.ReadAt(chunk, pos); rerr != nil {
			return nil, endOffset, rerr
		}
		newlineCount += bytes.Count(chunk, []byte{'\n'})
		// Prepend chunk to buf.
		buf = append(chunk, buf...)
	}

	// Split into lines. We keep newlines so we can recover byte counts.
	all := splitLinesKeepNL(buf)
	// If we didn't read all the way to offset 0, the first element is a
	// partial trailing line (the bytes between pos and the first newline);
	// drop it.
	if pos > 0 && len(all) > 0 {
		all = all[1:]
	}
	if len(all) > maxLines {
		all = all[len(all)-maxLines:]
	}

	// Compute the new offset = position of the first byte of all[0].
	// = endOffset - sum(len(line) for line in all)
	consumed := 0
	for _, ln := range all {
		consumed += len(ln)
	}
	newOffset = endOffset - int64(consumed)

	// Strip trailing newline from each line for the parser.
	out := make([]string, 0, len(all))
	for _, ln := range all {
		out = append(out, trimTrailingNL(ln))
	}
	return out, newOffset, nil
}

// splitLinesKeepNL splits b into lines, retaining each line's trailing '\n'
// (so callers can recover the original byte lengths). A trailing bytes
// without a terminating '\n' becomes the last element.
func splitLinesKeepNL(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i+1])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

func trimTrailingNL(b []byte) string {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
		if n := len(b); n > 0 && b[n-1] == '\r' {
			b = b[:n-1]
		}
	}
	return string(b)
}

// findOffsetForTime binary-searches a seekable byte stream for the byte
// offset whose first complete line has timestamp >= target. Reading
// backward from this offset yields only lines with timestamp < target.
//
// Used when transitioning from in-memory to byte-offset paging: starting
// the backward read at end-of-file would replay lines the client already
// has from the ring buffer, producing the "today's entries reappear when
// scrolling into yesterday" bug.
//
// Returns fileSize when every line in the file is < target (nothing to
// skip past), or 0 when every line is >= target. Errors during ReadAt or
// context cancellation return whatever bound we've converged to so far.
const seekProbeSize = 4096

func findOffsetForTime(ctx context.Context, reader readerAt, fileSize int64, target time.Time, parseTS func(string) time.Time) int64 {
	if fileSize <= 0 || target.IsZero() {
		return fileSize
	}

	lo, hi := int64(0), fileSize
	for hi-lo > 2*seekProbeSize {
		if err := ctx.Err(); err != nil {
			return lo
		}
		mid := (lo + hi) / 2
		ts, lineOff, ok := probeTimestampAt(reader, mid, seekProbeSize, parseTS)
		if !ok {
			// The probe window at mid had no parseable lines (e.g. a
			// region of multi-line stack traces or rsyslog-prefixed
			// lines with no extractable timestamp). Walk forward with
			// exponentially-growing steps to find the nearest parseable
			// line, and use IT as the probe result. If no parseable
			// line exists between mid and hi, treat the whole region as
			// older than target (advance lo) — biasing the search
			// toward the half we haven't ruled out.
			ts, lineOff, ok = probeForwardForParseable(reader, mid, hi, parseTS)
			if !ok {
				lo = mid + 1
				continue
			}
		}
		if ts.Before(target) {
			lo = lineOff + 1
		} else {
			hi = lineOff
		}
	}

	// Narrowed region; linear scan to find the exact boundary.
	span := hi - lo
	if span <= 0 {
		return lo
	}
	buf := make([]byte, span)
	if n, err := reader.ReadAt(buf, lo); err != nil && n == 0 {
		return lo
	} else {
		buf = buf[:n]
	}
	pos := 0
	// If lo > 0, the first line in buf is partial — skip it.
	if lo > 0 {
		if nl := bytesIndexNL(buf); nl >= 0 {
			pos = nl + 1
		} else {
			return hi
		}
	}
	for pos < len(buf) {
		end := pos + bytesIndexNL(buf[pos:])
		if end < pos {
			// No more newlines in buf; treat trailing partial as "we got
			// past it" — return hi.
			return hi
		}
		line := string(buf[pos:end])
		ts := parseTS(line)
		if !ts.IsZero() && !ts.Before(target) {
			return lo + int64(pos)
		}
		pos = end + 1
	}
	return lo + int64(pos)
}

// probeTimestampAt reads probeSize bytes starting at offset, walks lines
// forward (skipping the leading partial), parses each one's timestamp,
// and returns the FIRST line whose timestamp is non-zero — along with its
// byte offset.
//
// Walking past unparseable lines (continuations like multi-line stack
// traces, blank lines, JSON pretty-printed content, etc.) is critical for
// the binary search to converge correctly. If we only tried the first
// line and gave up on a miss, every probe that landed in a stack trace
// would falsely bias the search toward smaller offsets and findOffsetFor-
// Time would drift to 0, sending the caller back to end-of-file.
func probeTimestampAt(reader readerAt, offset int64, probeSize int, parseTS func(string) time.Time) (time.Time, int64, bool) {
	buf := make([]byte, probeSize)
	n, err := reader.ReadAt(buf, offset)
	if err != nil && n == 0 {
		return time.Time{}, 0, false
	}
	buf = buf[:n]
	// Skip the leading partial line.
	nl := bytesIndexNL(buf)
	if nl < 0 {
		return time.Time{}, 0, false
	}
	pos := nl + 1
	for pos < len(buf) {
		rel := bytesIndexNL(buf[pos:])
		var end int
		if rel < 0 {
			// Last line in our probe; treat as truncated and try to
			// parse what we have. If it doesn't parse, give up — the
			// caller may need a bigger probe.
			end = len(buf)
		} else {
			end = pos + rel
		}
		line := string(buf[pos:end])
		ts := parseTS(line)
		if !ts.IsZero() {
			return ts, offset + int64(pos), true
		}
		if rel < 0 {
			return time.Time{}, 0, false
		}
		pos = end + 1
	}
	return time.Time{}, 0, false
}

// probeForwardForParseable walks forward from `from` toward `limit` with
// exponentially-growing steps, probing each landing position for a
// parseable timestamp. Returns the FIRST parseable line's timestamp and
// byte offset, or ok=false if nothing parseable exists in [from, limit).
//
// Used by findOffsetForTime to escape stretches of unparseable lines
// (multi-line stack traces, rsyslog-prefixed entries that don't unmarshal
// as JSON, etc.) inside the binary search range. Without this, every
// probe in such a stretch would falsely bias hi → mid and the search
// would converge to the wrong end of the file.
//
// Steps double each iteration: 64KB, 128KB, 256KB, 512KB, ..., capped at
// 8MB. For a 30MB unparseable region this needs ~9 probes; for a 100MB
// one ~12 probes. Each probe is one ReadAt — over SSH that's ~50ms each.
func probeForwardForParseable(reader readerAt, from, limit int64, parseTS func(string) time.Time) (time.Time, int64, bool) {
	const (
		startStep = 64 * 1024
		maxStep   = 8 * 1024 * 1024
	)
	step := int64(startStep)
	for pos := from + step; pos < limit; pos += step {
		ts, lineOff, ok := probeTimestampAt(reader, pos, seekProbeSize, parseTS)
		if ok {
			return ts, lineOff, true
		}
		if step < maxStep {
			step *= 2
		}
	}
	return time.Time{}, 0, false
}

func bytesIndexNL(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

// readBackwardAcrossFiles is the shared driver used by source-level
// ReadBackward implementations. fileAt(idx) returns the file at that index
// (0 = current/active, then rotated newest-first); openAt opens it for
// reading; sizeOf returns its size. The driver walks files newest-first
// starting at cursor.FileIndex / cursor.Offset, accumulates up to maxLines
// uncompressed lines, and returns the next cursor.
//
// Compressed files are SKIPPED for v1 — we advance the cursor past them
// rather than decompressing inline. The caller's caller (the WS handler)
// is expected to fall back to the time-window historical reader for any
// history that lives in compressed files; for the typical "scrollback into
// the active file plus the most recent rotation" case this is fine.
type backwardFile struct {
	Path       string
	Size       int64
	Compressed bool
}

// readBackwardOpener opens file index `idx` for backward reading. Returns
// the readerAt, a close function, and the file's size.
type readBackwardOpener interface {
	openBackward(ctx context.Context, idx int) (r readerAt, close func(), size int64, path string, compressed bool, ok bool)
}

func readBackwardAcrossFiles(ctx context.Context, opener readBackwardOpener, fileCount int, cursor BackwardCursor, maxLines int) (BackwardResult, error) {
	if maxLines <= 0 {
		return BackwardResult{NextCursor: cursor}, nil
	}

	idx := cursor.FileIndex
	if idx < 0 {
		idx = 0
	}
	offset := cursor.Offset

	var collected []string
	skippedCompressed := false
	for idx < fileCount {
		if err := ctx.Err(); err != nil {
			return BackwardResult{Lines: collected, NextCursor: BackwardCursor{FileIndex: idx, Offset: offset}, SkippedCompressed: skippedCompressed}, err
		}
		reader, closer, size, path, compressed, ok := opener.openBackward(ctx, idx)
		if !ok {
			// Couldn't open — advance past this file.
			idx++
			offset = -1
			continue
		}

		if compressed {
			if closer != nil {
				closer()
			}
			// v1: skip compressed history for byte-offset reading.
			skippedCompressed = true
			idx++
			offset = -1
			continue
		}

		if offset < 0 || offset > size {
			offset = size
		}
		if offset == 0 {
			if closer != nil {
				closer()
			}
			idx++
			offset = -1
			continue
		}

		wanted := maxLines - len(collected)
		if wanted <= 0 {
			if closer != nil {
				closer()
			}
			return BackwardResult{Lines: collected, NextCursor: BackwardCursor{FileIndex: idx, Offset: offset, FilePath: path}, SkippedCompressed: skippedCompressed}, nil
		}

		lines, newOff, err := readBackwardFromReader(reader, offset, wanted, 64*1024)
		if closer != nil {
			closer()
		}
		if err != nil {
			return BackwardResult{Lines: collected, NextCursor: BackwardCursor{FileIndex: idx, Offset: offset, FilePath: path}, SkippedCompressed: skippedCompressed}, err
		}
		// Prepend this file's lines (older than what we have so far) — but
		// within this single read pass they're already in chronological
		// order, so prepend as a whole.
		collected = append(lines, collected...)
		offset = newOff

		if len(collected) >= maxLines {
			return BackwardResult{Lines: collected, NextCursor: BackwardCursor{FileIndex: idx, Offset: offset, FilePath: path}, SkippedCompressed: skippedCompressed}, nil
		}
		// File exhausted at this offset — move to next-older file.
		if offset <= 0 {
			idx++
			offset = -1
		}
	}

	return BackwardResult{Lines: collected, NextCursor: BackwardCursor{FileIndex: idx, Offset: -1}, Done: true, SkippedCompressed: skippedCompressed}, nil
}
