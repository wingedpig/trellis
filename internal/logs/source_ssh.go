// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// SSHSource reads logs from a remote host via SSH.
type SSHSource struct {
	sourceBase
}

// NewSSHSource creates a new SSH-based log source.
func NewSSHSource(cfg config.LogSourceConfig) (*SSHSource, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("ssh source requires host")
	}
	if cfg.Path == "" {
		return nil, fmt.Errorf("ssh source requires path")
	}
	return &SSHSource{sourceBase: sourceBase{cfg: cfg}}, nil
}

// Name returns the source name.
func (s *SSHSource) Name() string {
	return fmt.Sprintf("ssh:%s:%s", s.cfg.Host, s.cfg.Path)
}

// Start begins tailing the remote file.
func (s *SSHSource) Start(ctx context.Context, lineCh chan<- string, errCh chan<- error) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(lineCh)

		s.tailRemote(ctx, lineCh, errCh)
	}()

	return nil
}

// tailRemote tails the remote file via SSH.
func (s *SSHSource) tailRemote(ctx context.Context, lineCh chan<- string, errCh chan<- error) {
	// Build the log file path
	logFile := s.cfg.Path
	if s.cfg.Current != "" {
		logFile = path.Join(s.cfg.Path, s.cfg.Current)
	}

	// Use tail -F for rotation support
	remoteCmd := fmt.Sprintf("tail -F -n 1000 %s", logFile)

	cmd := exec.CommandContext(ctx, "ssh", s.cfg.Host, remoteCmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.setError(err)
		errCh <- fmt.Errorf("creating pipe: %w", err)
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		s.setError(err)
		errCh <- fmt.Errorf("creating stderr pipe: %w", err)
		return
	}

	if err := cmd.Start(); err != nil {
		s.setError(err)
		errCh <- fmt.Errorf("starting ssh: %w", err)
		return
	}

	s.setConnected()

	// Read stderr for connection errors
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			// tail -F outputs file rotation messages to stderr
			if !strings.Contains(line, "file truncated") &&
				!strings.Contains(line, "has become inaccessible") &&
				!strings.Contains(line, "following end of new file") {
				// Real error
				s.setError(fmt.Errorf("ssh stderr: %s", line))
			}
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		case lineCh <- scanner.Text():
			s.incrementLines()
		}
	}

	if err := scanner.Err(); err != nil {
		s.setError(err)
		errCh <- fmt.Errorf("reading: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == nil {
			s.setError(err)
			errCh <- fmt.Errorf("ssh exited: %w", err)
		}
	}
}


// ListRotatedFiles returns available rotated log files on the remote host.
func (s *SSHSource) ListRotatedFiles(ctx context.Context) ([]RotatedFile, error) {
	if s.cfg.RotatedPattern == "" {
		return nil, nil
	}

	pattern := s.cfg.RotatedPattern
	// Make pattern relative to log directory
	if !strings.HasPrefix(pattern, "/") {
		pattern = path.Join(s.cfg.Path, pattern)
	}

	// Get file list with mtime and size in a single SSH command using ls
	// --time-style=full-iso gives: 2024-01-15 10:30:00.123456789 -0500
	remoteCmd := fmt.Sprintf("/usr/bin/ls -l --time-style=full-iso %s 2>/dev/null", pattern)
	cmd := exec.CommandContext(ctx, "ssh", s.cfg.Host, remoteCmd)
	output, err := cmd.Output()
	if err != nil {
		// Empty result if no files match (ls returns exit code 2)
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			return nil, nil
		}
		return nil, fmt.Errorf("listing remote files: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var files []RotatedFile

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse ls -l --time-style=full-iso output:
		// -rw-r--r-- 1 user group 1234567 2024-01-15 10:30:00.123456789 -0500 /path/to/file
		// Fields: perms, links, owner, group, size, date, time, tz, path
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}

		// Skip directories (first char is 'd')
		if fields[0][0] == 'd' {
			continue
		}

		var size int64
		fmt.Sscanf(fields[4], "%d", &size)

		// Parse timestamp: "2024-01-15 10:30:00.123456789 -0500"
		timeStr := fields[5] + " " + fields[6] + " " + fields[7]
		mtime, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", timeStr)
		if err != nil {
			// Try without nanoseconds
			mtime, err = time.Parse("2006-01-02 15:04:05 -0700", timeStr)
			if err != nil {
				continue
			}
		}

		// Path is everything after the timezone field
		filePath := strings.Join(fields[8:], " ")

		// File is compressed if decompress is explicitly configured OR auto-detected from extension
		compressed := s.cfg.Decompress != "" || DecompressCommand(filePath) != ""
		files = append(files, RotatedFile{
			Name:       path.Base(filePath),
			Path:       filePath,
			Size:       size,
			ModTime:    mtime,
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
func (s *SSHSource) ReadRange(ctx context.Context, start, end time.Time, lineCh chan<- string, grep string, grepBefore, grepAfter int) error {
	log.Printf("SSH ReadRange: start=%v end=%v grep=%q before=%d after=%d", start, end, grep, grepBefore, grepAfter)

	files, err := s.ListRotatedFiles(ctx)
	if err != nil {
		log.Printf("SSH ReadRange: ListRotatedFiles error: %v", err)
		return err
	}
	log.Printf("SSH ReadRange: found %d rotated files", len(files))

	// Build grep pattern for timestamp filtering
	// This creates patterns like "2026-01-12T06:" for each hour in the range
	timestampPattern := s.buildTimestampGrepPattern(start, end)
	log.Printf("SSH ReadRange: timestamp pattern=%q, user grep=%q", timestampPattern, grep)

	relevantFiles, newestRotatedModTime := filterRelevantFiles(files, start, end)

	log.Printf("SSH ReadRange: %d relevant rotated files, newestRotatedModTime=%v", len(relevantFiles), newestRotatedModTime)
	for _, f := range relevantFiles {
		log.Printf("SSH ReadRange: relevant file: %s (ModTime=%v)", f.Path, f.ModTime)
	}

	// Read files from oldest to newest for chronological output
	for i := len(relevantFiles) - 1; i >= 0; i-- {
		select {
		case <-ctx.Done():
			log.Printf("SSH ReadRange: context canceled before processing file %d", i)
			return ctx.Err()
		default:
		}

		file := relevantFiles[i]
		log.Printf("SSH ReadRange: processing rotated file %s", file.Path)
		if err := s.grepRemoteFile(ctx, file, timestampPattern, grep, grepBefore, grepAfter, lineCh); err != nil {
			return fmt.Errorf("reading %s: %w", file.Name, err)
		}
	}

	// If the time range extends beyond the newest rotated file, also read the current file
	// (it contains entries newer than the rotated files)
	willReadCurrent := s.cfg.Current != "" && (end.IsZero() || end.After(newestRotatedModTime))
	log.Printf("SSH ReadRange: will read current file: %v (Current=%q, end=%v, newestRotatedModTime=%v)",
		willReadCurrent, s.cfg.Current, end, newestRotatedModTime)
	if willReadCurrent {
		currentPath := s.cfg.Current
		if !strings.HasPrefix(currentPath, "/") {
			currentPath = path.Join(s.cfg.Path, currentPath)
		}
		currentFile := RotatedFile{
			Name:       s.cfg.Current,
			Path:       currentPath,
			Compressed: false,
		}
		if err := s.grepRemoteFile(ctx, currentFile, timestampPattern, grep, grepBefore, grepAfter, lineCh); err != nil {
			return fmt.Errorf("reading current file %s: %w", currentFile.Name, err)
		}
	}

	return nil
}

// buildTimestampGrepPattern builds a grep pattern to match timestamps in the given range.
// For JSON logs, this matches patterns like "2026-01-10T" for full days or "2026-01-10T06:" for specific hours.
// Times are converted to local time since most logs use local timestamps.
func (s *SSHSource) buildTimestampGrepPattern(start, end time.Time) string {
	if start.IsZero() && end.IsZero() {
		return "" // No filtering
	}

	if end.IsZero() {
		end = time.Now()
	}

	// Convert to local time since most logs use local timestamps
	start = start.Local()
	end = end.Local()

	var patterns []string

	// Process day by day
	currentDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())

	for !currentDay.After(endDay) {
		isFirstDay := currentDay.Year() == start.Year() && currentDay.Month() == start.Month() && currentDay.Day() == start.Day()
		isLastDay := currentDay.Year() == end.Year() && currentDay.Month() == end.Month() && currentDay.Day() == end.Day()

		// Check if this is a full day
		// First day is full if start is at midnight (hour 0, minute 0)
		// Last day is full if end is at 23:xx (we're lenient on minutes/seconds)
		// Middle days are always full
		startsAtMidnight := start.Hour() == 0 && start.Minute() == 0
		endsAtEndOfDay := end.Hour() == 23

		isFullDay := true
		if isFirstDay && !startsAtMidnight {
			isFullDay = false
		}
		if isLastDay && !endsAtEndOfDay {
			isFullDay = false
		}

		if isFullDay {
			// Full day: just use date prefix (e.g., "2026-01-10T")
			patterns = append(patterns, currentDay.Format("2006-01-02T"))
		} else {
			// Partial day: enumerate the hours within range
			hourStart := 0
			hourEnd := 23

			if isFirstDay {
				hourStart = start.Hour()
			}
			if isLastDay {
				hourEnd = end.Hour()
			}

			for h := hourStart; h <= hourEnd; h++ {
				t := time.Date(currentDay.Year(), currentDay.Month(), currentDay.Day(), h, 0, 0, 0, currentDay.Location())
				patterns = append(patterns, t.Format("2006-01-02T15:"))
			}
		}

		currentDay = currentDay.Add(24 * time.Hour)
	}

	if len(patterns) == 0 {
		return ""
	}

	if len(patterns) == 1 {
		return patterns[0]
	}

	// Multiple patterns: join with | for extended regex (grep -E)
	return "(" + strings.Join(patterns, "|") + ")"
}

// grepRemoteFile runs grep on a remote file and sends matching lines to the channel.
// timestampPattern filters by timestamp, userGrep is an additional user-specified pattern.
// grepBefore/grepAfter add context lines around user grep matches.
func (s *SSHSource) grepRemoteFile(ctx context.Context, file RotatedFile, timestampPattern, userGrep string, grepBefore, grepAfter int, lineCh chan<- string) error {
	var remoteCmd string

	// Build grep context flags for user grep
	var contextFlags string
	if grepBefore > 0 {
		contextFlags += fmt.Sprintf(" -B %d", grepBefore)
	}
	if grepAfter > 0 {
		contextFlags += fmt.Sprintf(" -A %d", grepAfter)
	}

	// Build the grep pipeline
	// If both patterns exist: grep 'timestamp' file | grep [-B N] [-A N] 'user'
	// If only timestamp: grep 'timestamp' file
	// If only user: grep [-B N] [-A N] 'user' file
	// If neither: cat file

	if timestampPattern == "" && userGrep == "" {
		// No pattern - read entire file (fallback)
		if file.Compressed {
			decompressCmd := s.cfg.Decompress
			if decompressCmd == "" {
				decompressCmd = DecompressCommand(file.Path)
			}
			if decompressCmd == "" {
				return fmt.Errorf("no decompress command for %s", file.Path)
			}
			remoteCmd = fmt.Sprintf("%s %s", decompressCmd, file.Path)
		} else {
			remoteCmd = fmt.Sprintf("cat %s", file.Path)
		}
	} else {
		// Build grep command with one or both patterns
		if file.Compressed {
			decompressCmd := s.cfg.Decompress
			if decompressCmd == "" {
				decompressCmd = DecompressCommand(file.Path)
			}
			if decompressCmd == "" {
				return fmt.Errorf("no decompress command for %s", file.Path)
			}
			// Decompress and pipe through grep(s)
			// Use -E for extended regex (needed for alternation patterns like (a|b|c))
			if timestampPattern != "" && userGrep != "" {
				remoteCmd = fmt.Sprintf("%s %s | grep -E '%s' | grep -E%s '%s' || true", decompressCmd, file.Path, timestampPattern, contextFlags, userGrep)
			} else if timestampPattern != "" {
				remoteCmd = fmt.Sprintf("%s %s | grep -E '%s' || true", decompressCmd, file.Path, timestampPattern)
			} else {
				remoteCmd = fmt.Sprintf("%s %s | grep -E%s '%s' || true", decompressCmd, file.Path, contextFlags, userGrep)
			}
		} else {
			// Uncompressed file - use grep directly
			// Use -E for extended regex (needed for alternation patterns like (a|b|c))
			if timestampPattern != "" && userGrep != "" {
				remoteCmd = fmt.Sprintf("grep -E '%s' %s | grep -E%s '%s' || true", timestampPattern, file.Path, contextFlags, userGrep)
			} else if timestampPattern != "" {
				remoteCmd = fmt.Sprintf("grep -E '%s' %s || true", timestampPattern, file.Path)
			} else {
				remoteCmd = fmt.Sprintf("grep -E%s '%s' %s || true", contextFlags, userGrep, file.Path)
			}
		}
	}

	log.Printf("SSH grep: host=%s file=%s cmd=%q", s.cfg.Host, file.Path, remoteCmd)

	cmd := exec.CommandContext(ctx, "ssh", s.cfg.Host, remoteCmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		log.Printf("SSH grep: start failed: %v", err)
		return err
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	lineCount := 0
	for scanner.Scan() {
		lineCount++
		select {
		case <-ctx.Done():
			log.Printf("SSH grep: context canceled after %d lines, err=%v", lineCount, ctx.Err())
			cmd.Wait()
			return ctx.Err()
		case lineCh <- scanner.Text():
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("SSH grep: scanner error after %d lines: %v", lineCount, err)
		cmd.Wait()
		return err
	}

	waitErr := cmd.Wait()
	log.Printf("SSH grep: completed, %d lines, wait error: %v", lineCount, waitErr)

	// If context was canceled, cmd.Wait() returns "signal: killed" (from SIGKILL sent by exec.CommandContext)
	// rather than context.Canceled. Check if context is done and return its error instead.
	if waitErr != nil && ctx.Err() != nil {
		log.Printf("SSH grep: context done (%v), ignoring wait error", ctx.Err())
		return ctx.Err()
	}

	return waitErr
}

