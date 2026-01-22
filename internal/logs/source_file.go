// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// FileSource reads logs from a local file.
type FileSource struct {
	cfg    config.LogSourceConfig
	mu     sync.RWMutex
	status SourceStatus
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewFileSource creates a new file-based log source.
func NewFileSource(cfg config.LogSourceConfig) (*FileSource, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("file source requires path")
	}
	return &FileSource{cfg: cfg}, nil
}

// Name returns the source name.
func (s *FileSource) Name() string {
	return fmt.Sprintf("file:%s", s.cfg.Path)
}

// Start begins tailing the file.
func (s *FileSource) Start(ctx context.Context, lineCh chan<- string, errCh chan<- error) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(lineCh)

		s.tailFile(ctx, lineCh, errCh)
	}()

	return nil
}

// tailFile tails the file and sends lines to the channel.
func (s *FileSource) tailFile(ctx context.Context, lineCh chan<- string, errCh chan<- error) {
	path := s.cfg.Path

	// Use tail -F to follow the file (handles rotation)
	cmd := exec.CommandContext(ctx, "tail", "-F", "-n", "1000", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.setError(err)
		errCh <- fmt.Errorf("creating pipe: %w", err)
		return
	}

	if err := cmd.Start(); err != nil {
		s.setError(err)
		errCh <- fmt.Errorf("starting tail: %w", err)
		return
	}

	s.setConnected()

	reader := bufio.NewReaderSize(stdout, 64*1024)
	const maxLineSize = 1024 * 1024 // 1MB max line size

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			// Remove trailing newline
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			// Truncate very long lines
			if len(line) > maxLineSize {
				line = line[:maxLineSize] + "... [truncated]"
			}
			select {
			case <-ctx.Done():
				cmd.Wait()
				return
			case lineCh <- line:
				s.incrementLines()
			}
		}
		if err != nil {
			if err == io.EOF {
				// Normal end of file
				break
			}
			// For other errors, report but continue reading if possible
			s.setError(err)
			errCh <- fmt.Errorf("reading: %w", err)
			// Check if context is done
			select {
			case <-ctx.Done():
				cmd.Wait()
				return
			default:
				// Continue trying to read
			}
		}
	}

	cmd.Wait()
}

// Stop gracefully stops the source.
func (s *FileSource) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return nil
}

// Status returns the current connection status.
func (s *FileSource) Status() SourceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// ListRotatedFiles returns available rotated log files.
func (s *FileSource) ListRotatedFiles(ctx context.Context) ([]RotatedFile, error) {
	if s.cfg.RotatedPattern == "" {
		return nil, nil
	}

	pattern := s.cfg.RotatedPattern
	// If pattern is relative, make it relative to the log directory
	if !filepath.IsAbs(pattern) {
		dir := filepath.Dir(s.cfg.Path)
		pattern = filepath.Join(dir, pattern)
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob pattern: %w", err)
	}

	var files []RotatedFile
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}

		// File is compressed if decompress is explicitly configured OR auto-detected from extension
		compressed := s.cfg.Decompress != "" || DecompressCommand(match) != ""
		files = append(files, RotatedFile{
			Name:       filepath.Base(match),
			Path:       match,
			Size:       info.Size(),
			ModTime:    info.ModTime(),
			Compressed: compressed,
		})
	}

	// Sort by modification time, newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

	return files, nil
}

// ReadRange reads log lines from a time range in rotated files.
// For FileSource, grep filtering is done client-side after reading (local files are fast).
func (s *FileSource) ReadRange(ctx context.Context, start, end time.Time, lineCh chan<- string, grep string, grepBefore, grepAfter int) error {
	// Note: grep parameters are ignored for local files - filtering is done client-side
	_, _, _ = grep, grepBefore, grepAfter
	files, err := s.ListRotatedFiles(ctx)
	if err != nil {
		return err
	}

	// Filter files based on time bounds to avoid scanning unnecessary files.
	// Files are sorted newest first by ModTime.
	// A file's ModTime is when it was last written (end of its entries).
	// A file's start time can be estimated as the ModTime of the next older file.
	//
	// Skip files where:
	// - ModTime < start (file's last entry is before our range started)
	// - EstimatedStartTime > end (file's first entry is after our range ended)
	var relevantFiles []RotatedFile
	var newestRotatedModTime time.Time

	for i, file := range files {
		if file.ModTime.After(newestRotatedModTime) {
			newestRotatedModTime = file.ModTime
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
				// This file's entries all started after our end time
				continue
			}
		}

		relevantFiles = append(relevantFiles, file)
	}

	// Read files from oldest to newest for chronological output
	for i := len(relevantFiles) - 1; i >= 0; i-- {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		file := relevantFiles[i]
		if err := s.readFile(ctx, file, lineCh); err != nil {
			return fmt.Errorf("reading %s: %w", file.Name, err)
		}
	}

	// If the time range extends beyond the newest rotated file, also read the current file
	// (it contains entries newer than the rotated files)
	if s.cfg.Current != "" && (end.IsZero() || end.After(newestRotatedModTime)) {
		currentPath := s.cfg.Current
		if !filepath.IsAbs(currentPath) {
			currentPath = filepath.Join(filepath.Dir(s.cfg.Path), currentPath)
		}
		currentFile := RotatedFile{
			Name:       s.cfg.Current,
			Path:       currentPath,
			Compressed: false,
		}
		if err := s.readFile(ctx, currentFile, lineCh); err != nil {
			return fmt.Errorf("reading current file %s: %w", currentFile.Name, err)
		}
	}

	return nil
}

// readFile reads a single file and sends lines to the channel.
func (s *FileSource) readFile(ctx context.Context, file RotatedFile, lineCh chan<- string) error {
	var reader io.ReadCloser

	if file.Compressed {
		decompressCmd := s.cfg.Decompress
		if decompressCmd == "" {
			decompressCmd = DecompressCommand(file.Path)
		}
		if decompressCmd == "" {
			return fmt.Errorf("no decompress command for %s", file.Path)
		}

		parts := strings.Fields(decompressCmd)
		cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], file.Path)...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		reader = &cmdReader{Reader: stdout, cmd: cmd}
	} else {
		f, err := os.Open(file.Path)
		if err != nil {
			return err
		}
		reader = f
	}
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case lineCh <- scanner.Text():
		}
	}

	return scanner.Err()
}

// cmdReader wraps a reader from a command and waits for completion on close.
type cmdReader struct {
	io.Reader
	cmd *exec.Cmd
}

func (r *cmdReader) Close() error {
	return r.cmd.Wait()
}

// setConnected updates the status to connected.
func (s *FileSource) setConnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Connected = true
	s.status.LastConnect = time.Now()
	s.status.Error = ""
}

// setError updates the status with an error.
func (s *FileSource) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Connected = false
	s.status.Error = err.Error()
	s.status.LastError = time.Now()
}

// incrementLines increments the lines read counter.
func (s *FileSource) incrementLines() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LinesRead++
}
