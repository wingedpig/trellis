// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// LogSource represents a source of log data.
type LogSource interface {
	// Name returns the source name.
	Name() string

	// Start begins reading from the source and sends lines to the channel.
	// The channel is closed when the source stops or encounters an error.
	// The returned error is for startup failures; runtime errors are sent to errCh.
	Start(ctx context.Context, lineCh chan<- string, errCh chan<- error) error

	// Stop gracefully stops the source.
	Stop() error

	// Status returns the current connection status.
	Status() SourceStatus

	// ListRotatedFiles returns available rotated log files.
	// Files are returned newest first.
	ListRotatedFiles(ctx context.Context) ([]RotatedFile, error)

	// ReadRange reads log lines from a time range in rotated files.
	// Lines are sent to the channel in chronological order.
	// If grep is non-empty, only lines matching the pattern are returned.
	// grepBefore/grepAfter specify context lines around matches (like grep -B/-A).
	ReadRange(ctx context.Context, start, end time.Time, lineCh chan<- string, grep string, grepBefore, grepAfter int) error
}

// SourceStatus represents the connection status of a log source.
type SourceStatus struct {
	Connected   bool      `json:"connected"`
	Error       string    `json:"error,omitempty"`
	LastConnect time.Time `json:"last_connect,omitempty"`
	LastError   time.Time `json:"last_error,omitempty"`
	BytesRead   int64     `json:"bytes_read"`
	LinesRead   int64     `json:"lines_read"`
}

// BackwardReader is an optional capability for sources whose underlying log
// store is a seekable byte stream (local files, remote files over SSH). It
// lets the viewer page backward through the log a fixed number of lines at
// a time without re-scanning earlier history on every request.
//
// Sources that don't have seekable streams (Docker logs, Kubernetes,
// streaming commands) don't implement this; the viewer falls back to its
// time-window-based historical reader for those.
type BackwardReader interface {
	// ReadBackward returns the most recent `maxLines` raw log lines whose
	// byte position is strictly less than cursor.Offset within the file at
	// cursor.FileIndex (and earlier files if the current file is exhausted
	// before we have enough lines).
	//
	// On the very first call, cursor.Offset may be -1 to mean "start at end
	// of file". Returned Lines are in chronological order (oldest first).
	// NextCursor points at where the next backward read should start;
	// callers should pass it back verbatim on the next call. Done is true
	// when no older history is available — further calls would return
	// nothing.
	ReadBackward(ctx context.Context, cursor BackwardCursor, maxLines int) (BackwardResult, error)

	// SeekToTime returns an initial cursor positioned such that reading
	// backward from it yields lines with timestamp strictly less than
	// target. Used when the client falls through from the in-memory ring
	// buffer to byte-offset paging — otherwise byte-offset would start at
	// end-of-file and replay content the in-memory buffer already showed.
	//
	// Implementations typically binary-search the active file using
	// parseTS to extract timestamps from probed lines. probeTimestamp
	// should return the zero time for lines it can't parse — those are
	// treated as "old enough" (bias toward returning more history rather
	// than less).
	//
	// Returns the zero cursor if seeking isn't possible (empty file, no
	// parseable lines, target older than any line in the file).
	SeekToTime(ctx context.Context, target time.Time, parseTS func(line string) time.Time) (BackwardCursor, error)
}

// BackwardCursor identifies a position in the log history for backward
// paging. FileIndex is 0 for the currently-active file, 1+ for rotated
// files in newest-first order. Offset is the byte position within that
// file; -1 means "start at end of file" (used for the very first call).
type BackwardCursor struct {
	FileIndex int   `json:"file_index"`
	Offset    int64 `json:"offset"`
	// FilePath is informational: lets clients detect when active rotation
	// has shifted the file under our feet and they should reset the cursor.
	FilePath string `json:"file_path,omitempty"`
}

// BackwardResult is the response from a BackwardReader.ReadBackward call.
type BackwardResult struct {
	Lines      []string       // chronological order
	NextCursor BackwardCursor // pass back unchanged on the next call
	Done       bool           // no older history is available
	// SkippedCompressed is true when the driver advanced past one or
	// more `.gz` rotated files without reading them. The caller can use
	// this to decide whether the time-window decompress-and-grep
	// fallback is worth running — if no compressed files were skipped,
	// there's no older content the byte-offset reader missed.
	SkippedCompressed bool
}

// RotatedFile represents a rotated log file.
type RotatedFile struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	Compressed bool      `json:"compressed"`
	StartTime  time.Time `json:"start_time,omitempty"` // Estimated start time
	EndTime    time.Time `json:"end_time,omitempty"`   // Estimated end time
}

// SourceType represents the type of log source.
type SourceType string

const (
	SourceTypeSSH        SourceType = "ssh"
	SourceTypeFile       SourceType = "file"
	SourceTypeCommand    SourceType = "command"
	SourceTypeDocker     SourceType = "docker"
	SourceTypeKubernetes SourceType = "kubernetes"
)

// sourceBase provides the common fields and status-tracking methods shared
// by all LogSource implementations.
type sourceBase struct {
	cfg    config.LogSourceConfig
	mu     sync.RWMutex
	status SourceStatus
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// setConnected updates the status to connected.
func (b *sourceBase) setConnected() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status.Connected = true
	b.status.LastConnect = time.Now()
	b.status.Error = ""
}

// setError updates the status with an error.
func (b *sourceBase) setError(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status.Connected = false
	b.status.Error = err.Error()
	b.status.LastError = time.Now()
}

// incrementLines increments the lines read counter.
func (b *sourceBase) incrementLines() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status.LinesRead++
}

// Status returns the current connection status.
func (b *sourceBase) Status() SourceStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.status
}

// Stop gracefully stops the source by cancelling the context and waiting.
func (b *sourceBase) Stop() error {
	if b.cancel != nil {
		b.cancel()
	}
	b.wg.Wait()
	return nil
}

// filterRelevantFiles filters rotated files based on time bounds.
// Files must be sorted newest first by ModTime.
// Returns the filtered list and the newest modification time seen.
func filterRelevantFiles(files []RotatedFile, start, end time.Time) ([]RotatedFile, time.Time) {
	var relevant []RotatedFile
	var newestModTime time.Time

	for i, file := range files {
		if file.ModTime.After(newestModTime) {
			newestModTime = file.ModTime
		}

		// Skip files that were last modified before our start time
		// (all entries in this file are older than what we want)
		if !start.IsZero() && file.ModTime.Before(start) {
			continue
		}

		// Estimate when this file's entries started (previous file's ModTime)
		// If this file started after our end time, skip it
		if !end.IsZero() && i+1 < len(files) {
			estimatedStart := files[i+1].ModTime
			if estimatedStart.After(end) {
				continue
			}
		}

		relevant = append(relevant, file)
	}

	return relevant, newestModTime
}

// NewSource creates a new LogSource from configuration.
func NewSource(cfg config.LogSourceConfig) (LogSource, error) {
	switch SourceType(cfg.Type) {
	case SourceTypeFile:
		return NewFileSource(cfg)
	case SourceTypeCommand:
		return NewCommandSource(cfg)
	case SourceTypeSSH:
		return NewSSHSource(cfg)
	case SourceTypeDocker:
		return NewDockerSource(cfg)
	case SourceTypeKubernetes:
		return NewKubernetesSource(cfg)
	default:
		return nil, fmt.Errorf("unknown source type: %s", cfg.Type)
	}
}

// LineReader provides a common interface for reading lines.
type LineReader interface {
	io.Closer
	ReadLine() (string, error)
}

// DecompressCommand returns the command to decompress a file based on extension.
func DecompressCommand(filename string) string {
	switch {
	case hasAnySuffix(filename, ".zst", ".zstd"):
		return "zstd -dc"
	case hasAnySuffix(filename, ".gz", ".gzip"):
		return "gzip -dc"
	case hasAnySuffix(filename, ".bz2", ".bzip2"):
		return "bzip2 -dc"
	case hasAnySuffix(filename, ".xz"):
		return "xz -dc"
	case hasAnySuffix(filename, ".lz4"):
		return "lz4 -dc"
	default:
		return ""
	}
}

// perFileTimeout is the maximum time allowed for reading a single log file.
const perFileTimeout = 60 * time.Second

// fileContext creates a context with a per-file timeout that is independent of
// the parent's deadline. This prevents a shared trace-level deadline from
// starving later files. Explicit cancellation (e.g. user cancel, shutdown)
// still propagates, but deadline expiration does not.
func fileContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), perFileTimeout)
	stop := context.AfterFunc(parent, func() {
		// Only propagate explicit cancellation, not deadline expiration.
		// DeadlineExceeded means the parent's timeout fired — each file
		// has its own timeout so we don't need to inherit that.
		// Canceled means an explicit cancel() call (user cancel, shutdown,
		// limit reached, errgroup failure) which we do want to propagate.
		if parent.Err() == context.Canceled {
			cancel()
		}
	})
	return ctx, func() { stop(); cancel() }
}

// hasAnySuffix checks if s ends with any of the given suffixes.
func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}
