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

// DockerSource reads logs from a Docker container.
type DockerSource struct {
	sourceBase
	container string
}

// NewDockerSource creates a new Docker log source.
func NewDockerSource(cfg config.LogSourceConfig) (*DockerSource, error) {
	if cfg.Container == "" {
		return nil, fmt.Errorf("docker source requires container name")
	}
	return &DockerSource{
		sourceBase: sourceBase{cfg: cfg},
		container:  cfg.Container,
	}, nil
}

// Name returns the source name.
func (s *DockerSource) Name() string {
	return fmt.Sprintf("docker:%s", s.container)
}

// Start begins reading logs from the Docker container.
func (s *DockerSource) Start(ctx context.Context, lineCh chan<- string, errCh chan<- error) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(lineCh)

		s.streamLogs(ctx, lineCh, errCh)
	}()

	return nil
}

// streamLogs runs docker logs and streams output.
func (s *DockerSource) streamLogs(ctx context.Context, lineCh chan<- string, errCh chan<- error) {
	args := []string{"logs"}

	// Add --follow if configured
	if s.cfg.IsFollow() {
		args = append(args, "--follow")
	}

	// Add --since if configured
	if s.cfg.Since != "" {
		args = append(args, "--since", s.cfg.Since)
	}

	// Add timestamps to output
	args = append(args, "--timestamps")

	// Add container name
	args = append(args, s.container)

	cmd := exec.CommandContext(ctx, "docker", args...)
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
		errCh <- fmt.Errorf("starting docker logs: %w", err)
		return
	}

	s.setConnected()

	// Read stderr to capture Docker errors
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			// Docker may output errors like "Error: No such container"
			if strings.HasPrefix(line, "Error") {
				s.setError(fmt.Errorf("%s", line))
				errCh <- fmt.Errorf("docker: %s", line)
			}
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		// Docker logs with --timestamps format: "2024-01-15T10:30:00.000000000Z message"
		// We'll pass the raw line and let the parser handle it
		select {
		case <-ctx.Done():
			return
		case lineCh <- line:
			s.incrementLines()
		}
	}

	if err := scanner.Err(); err != nil {
		s.setError(err)
		errCh <- fmt.Errorf("reading docker logs: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == nil {
			s.setError(err)
			errCh <- fmt.Errorf("docker logs exited: %w", err)
		}
	}
}

// ListRotatedFiles returns available rotated log files.
// Docker sources don't support rotated files directly.
func (s *DockerSource) ListRotatedFiles(ctx context.Context) ([]RotatedFile, error) {
	return nil, nil
}

// ReadRange reads log lines from a time range.
// Uses docker logs --since and --until for historical access.
func (s *DockerSource) ReadRange(ctx context.Context, start, end time.Time, lineCh chan<- string, grep string, grepBefore, grepAfter int) error {
	_, _, _ = grep, grepBefore, grepAfter // grep filtering done client-side for Docker
	args := []string{
		"logs",
		"--timestamps",
		"--since", start.Format(time.RFC3339),
		"--until", end.Format(time.RFC3339),
		s.container,
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting docker logs: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			cmd.Process.Kill()
			return ctx.Err()
		case lineCh <- scanner.Text():
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading: %w", err)
	}

	return cmd.Wait()
}

