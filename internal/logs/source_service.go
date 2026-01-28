// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ServiceLogProvider is an interface for accessing service log buffers.
// It decouples the logs package from the service package to avoid import cycles.
type ServiceLogProvider interface {
	ServiceLogs(name string, n int) ([]string, error)
	ServiceLogSize(name string) (int, error)
}

// ServiceSource implements LogSource for in-memory service log buffers.
// It reads log lines from a running service's LogBuffer via the ServiceLogProvider
// interface, enabling the trace system to search dev service logs.
type ServiceSource struct {
	serviceName string
	provider    ServiceLogProvider

	mu     sync.Mutex
	status SourceStatus
}

// NewServiceSource creates a new ServiceSource for the named service.
func NewServiceSource(serviceName string, provider ServiceLogProvider) *ServiceSource {
	return &ServiceSource{
		serviceName: serviceName,
		provider:    provider,
	}
}

// Name returns the source name.
func (s *ServiceSource) Name() string {
	return "service:" + s.serviceName
}

// Start marks the source as connected and blocks until the context is cancelled.
// Service sources don't stream through the viewer pipeline; data is accessed via ReadRange.
func (s *ServiceSource) Start(ctx context.Context, lineCh chan<- string, errCh chan<- error) error {
	s.mu.Lock()
	s.status.Connected = true
	s.status.LastConnect = time.Now()
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		s.status.Connected = false
		s.mu.Unlock()
		close(lineCh)
	}()

	return nil
}

// Stop is a no-op for service sources.
func (s *ServiceSource) Stop() error {
	return nil
}

// Status returns the current connection status.
func (s *ServiceSource) Status() SourceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// ListRotatedFiles returns nil — service sources have no rotated files.
func (s *ServiceSource) ListRotatedFiles(ctx context.Context) ([]RotatedFile, error) {
	return nil, nil
}

// ReadRange reads log lines from the service's in-memory buffer.
// It retrieves all lines from the buffer and sends them through the channel,
// optionally filtering by grep pattern. Time filtering is handled by the
// Viewer after parsing.
func (s *ServiceSource) ReadRange(ctx context.Context, start, end time.Time, lineCh chan<- string, grep string, grepBefore, grepAfter int) error {
	// Get the buffer size so we can request all lines
	size, err := s.provider.ServiceLogSize(s.serviceName)
	if err != nil {
		return err
	}

	if size == 0 {
		return nil
	}

	// Get all lines from the buffer
	lines, err := s.provider.ServiceLogs(s.serviceName, size)
	if err != nil {
		return err
	}

	// Compile grep pattern if provided
	var re *regexp.Regexp
	if grep != "" {
		re, err = regexp.Compile(grep)
		if err != nil {
			// Fall back to literal match if regex fails
			re = regexp.MustCompile(regexp.QuoteMeta(grep))
		}
	}

	// Send matching lines through the channel
	for _, line := range lines {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Apply grep filter
		if re != nil && !re.MatchString(line) {
			continue
		}

		// Skip trellis internal messages
		if strings.HasPrefix(line, "[trellis]") {
			continue
		}

		select {
		case lineCh <- line:
			s.mu.Lock()
			s.status.LinesRead++
			s.status.BytesRead += int64(len(line))
			s.mu.Unlock()
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}
