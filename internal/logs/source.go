// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"fmt"
	"io"
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

// hasAnySuffix checks if s ends with any of the given suffixes.
func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}
