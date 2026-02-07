// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// CommandSource reads logs from a command's stdout.
type CommandSource struct {
	sourceBase
}

// NewCommandSource creates a new command-based log source.
func NewCommandSource(cfg config.LogSourceConfig) (*CommandSource, error) {
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("command source requires command")
	}
	return &CommandSource{sourceBase: sourceBase{cfg: cfg}}, nil
}

// Name returns the source name.
func (s *CommandSource) Name() string {
	return fmt.Sprintf("command:%s", strings.Join(s.cfg.Command, " "))
}

// Start begins running the command and reading its output.
func (s *CommandSource) Start(ctx context.Context, lineCh chan<- string, errCh chan<- error) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(lineCh)

		s.runCommand(ctx, lineCh, errCh)
	}()

	return nil
}

// runCommand runs the command and sends output lines to the channel.
func (s *CommandSource) runCommand(ctx context.Context, lineCh chan<- string, errCh chan<- error) {
	args := s.cfg.Command
	if len(args) == 0 {
		errCh <- fmt.Errorf("no command specified")
		return
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.setError(err)
		errCh <- fmt.Errorf("creating stdout pipe: %w", err)
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
		errCh <- fmt.Errorf("starting command: %w", err)
		return
	}

	s.setConnected()

	// Read stderr in a goroutine to prevent blocking
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			// Log stderr but don't treat as log entries
			// Could optionally send to errCh
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
		// Context cancellation is expected
		if ctx.Err() == nil {
			s.setError(err)
			errCh <- fmt.Errorf("command exited: %w", err)
		}
	}
}

// ListRotatedFiles returns available rotated log files.
// Command sources don't support rotated files.
func (s *CommandSource) ListRotatedFiles(ctx context.Context) ([]RotatedFile, error) {
	return nil, nil
}

// ReadRange reads log lines from a time range.
// Command sources don't support historical access.
func (s *CommandSource) ReadRange(ctx context.Context, start, end time.Time, lineCh chan<- string, grep string, grepBefore, grepAfter int) error {
	return fmt.Errorf("command source does not support historical access")
}

