// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// KubernetesSource reads logs from a Kubernetes pod.
type KubernetesSource struct {
	cfg       config.LogSourceConfig
	mu        sync.RWMutex
	status    SourceStatus
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	namespace string
	pod       string
	container string
}

// NewKubernetesSource creates a new Kubernetes log source.
func NewKubernetesSource(cfg config.LogSourceConfig) (*KubernetesSource, error) {
	if cfg.Pod == "" {
		return nil, fmt.Errorf("kubernetes source requires pod name")
	}

	namespace := cfg.Namespace
	if namespace == "" {
		namespace = "default"
	}

	return &KubernetesSource{
		cfg:       cfg,
		namespace: namespace,
		pod:       cfg.Pod,
		container: cfg.Container,
	}, nil
}

// Name returns the source name.
func (s *KubernetesSource) Name() string {
	if s.container != "" {
		return fmt.Sprintf("k8s:%s/%s/%s", s.namespace, s.pod, s.container)
	}
	return fmt.Sprintf("k8s:%s/%s", s.namespace, s.pod)
}

// Start begins reading logs from the Kubernetes pod.
func (s *KubernetesSource) Start(ctx context.Context, lineCh chan<- string, errCh chan<- error) error {
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

// streamLogs runs kubectl logs and streams output.
func (s *KubernetesSource) streamLogs(ctx context.Context, lineCh chan<- string, errCh chan<- error) {
	args := []string{
		"logs",
		"-n", s.namespace,
	}

	// Add --follow if configured
	if s.cfg.IsFollow() {
		args = append(args, "-f")
	}

	// Add --since if configured
	if s.cfg.Since != "" {
		args = append(args, "--since", s.cfg.Since)
	}

	// Add timestamps
	args = append(args, "--timestamps")

	// Add pod name
	args = append(args, s.pod)

	// Add container name if specified
	if s.container != "" {
		args = append(args, "-c", s.container)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
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
		errCh <- fmt.Errorf("starting kubectl logs: %w", err)
		return
	}

	s.setConnected()

	// Read stderr to capture kubectl errors
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			// kubectl may output errors like "Error from server (NotFound)"
			if strings.Contains(line, "Error") || strings.Contains(line, "error") {
				s.setError(fmt.Errorf("%s", line))
				errCh <- fmt.Errorf("kubectl: %s", line)
			}
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		select {
		case <-ctx.Done():
			return
		case lineCh <- line:
			s.incrementLines()
		}
	}

	if err := scanner.Err(); err != nil {
		s.setError(err)
		errCh <- fmt.Errorf("reading kubectl logs: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == nil {
			s.setError(err)
			errCh <- fmt.Errorf("kubectl logs exited: %w", err)
		}
	}
}

// Stop gracefully stops the source.
func (s *KubernetesSource) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return nil
}

// Status returns the current connection status.
func (s *KubernetesSource) Status() SourceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// ListRotatedFiles returns available rotated log files.
// Kubernetes sources don't support rotated files.
func (s *KubernetesSource) ListRotatedFiles(ctx context.Context) ([]RotatedFile, error) {
	return nil, nil
}

// ReadRange reads log lines from a time range.
// Uses kubectl logs --since-time for historical access.
func (s *KubernetesSource) ReadRange(ctx context.Context, start, end time.Time, lineCh chan<- string, grep string, grepBefore, grepAfter int) error {
	_, _, _ = grep, grepBefore, grepAfter // grep filtering done client-side for Kubernetes
	args := []string{
		"logs",
		"-n", s.namespace,
		"--timestamps",
		"--since-time", start.Format(time.RFC3339),
		s.pod,
	}

	if s.container != "" {
		args = append(args, "-c", s.container)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting kubectl logs: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Parse timestamp from line to filter by end time
		// Format: "2024-01-15T10:30:00.000000000Z message"
		if len(line) >= 30 {
			if ts, err := time.Parse(time.RFC3339Nano, line[:30]); err == nil {
				if ts.After(end) {
					break // Stop when we pass the end time
				}
			}
		}

		select {
		case <-ctx.Done():
			cmd.Process.Kill()
			return ctx.Err()
		case lineCh <- line:
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading: %w", err)
	}

	return cmd.Wait()
}

// setConnected updates the status to connected.
func (s *KubernetesSource) setConnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Connected = true
	s.status.LastConnect = time.Now()
	s.status.Error = ""
}

// setError updates the status with an error.
func (s *KubernetesSource) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Connected = false
	s.status.Error = err.Error()
	s.status.LastError = time.Now()
}

// incrementLines increments the lines read counter.
func (s *KubernetesSource) incrementLines() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LinesRead++
}
