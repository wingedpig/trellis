// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockServiceLogProvider implements ServiceLogProvider for testing.
type mockServiceLogProvider struct {
	logs    map[string][]string
	sizeErr error
	logsErr error
}

func newMockProvider(services map[string][]string) *mockServiceLogProvider {
	return &mockServiceLogProvider{logs: services}
}

func (m *mockServiceLogProvider) ServiceLogs(name string, n int) ([]string, error) {
	if m.logsErr != nil {
		return nil, m.logsErr
	}
	lines, ok := m.logs[name]
	if !ok {
		return nil, fmt.Errorf("service %q not found", name)
	}
	if n > len(lines) {
		n = len(lines)
	}
	return lines[len(lines)-n:], nil
}

func (m *mockServiceLogProvider) ServiceLogSize(name string) (int, error) {
	if m.sizeErr != nil {
		return 0, m.sizeErr
	}
	lines, ok := m.logs[name]
	if !ok {
		return 0, fmt.Errorf("service %q not found", name)
	}
	return len(lines), nil
}

func TestNewServiceSource(t *testing.T) {
	provider := newMockProvider(nil)
	src := NewServiceSource("api", provider)

	assert.NotNil(t, src)
	assert.Equal(t, "api", src.serviceName)
	assert.Equal(t, provider, src.provider)
}

func TestServiceSourceName(t *testing.T) {
	src := NewServiceSource("worker", newMockProvider(nil))
	assert.Equal(t, "service:worker", src.Name())
}

func TestServiceSourceName_Various(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"api", "service:api"},
		{"worker", "service:worker"},
		{"my-service", "service:my-service"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := NewServiceSource(tt.name, newMockProvider(nil))
			assert.Equal(t, tt.expected, src.Name())
		})
	}
}

func TestServiceSourceStatus_Initial(t *testing.T) {
	src := NewServiceSource("api", newMockProvider(nil))

	status := src.Status()
	assert.False(t, status.Connected)
	assert.Zero(t, status.LinesRead)
	assert.Zero(t, status.BytesRead)
	assert.True(t, status.LastConnect.IsZero())
}

func TestServiceSourceStart(t *testing.T) {
	src := NewServiceSource("api", newMockProvider(nil))

	ctx, cancel := context.WithCancel(context.Background())
	lineCh := make(chan string, 10)
	errCh := make(chan error, 10)

	err := src.Start(ctx, lineCh, errCh)
	require.NoError(t, err)

	// Should be connected after start
	status := src.Status()
	assert.True(t, status.Connected)
	assert.False(t, status.LastConnect.IsZero())

	// Cancel context to trigger cleanup
	cancel()

	// Wait for the goroutine to close the channel
	time.Sleep(50 * time.Millisecond)

	// Should be disconnected after context cancel
	status = src.Status()
	assert.False(t, status.Connected)

	// lineCh should be closed
	_, ok := <-lineCh
	assert.False(t, ok, "lineCh should be closed")
}

func TestServiceSourceStop(t *testing.T) {
	src := NewServiceSource("api", newMockProvider(nil))

	// Stop is a no-op and should not error
	err := src.Stop()
	assert.NoError(t, err)
}

func TestServiceSourceListRotatedFiles(t *testing.T) {
	src := NewServiceSource("api", newMockProvider(nil))

	files, err := src.ListRotatedFiles(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, files)
}

func TestServiceSourceReadRange_BasicLines(t *testing.T) {
	provider := newMockProvider(map[string][]string{
		"api": {
			"line 1",
			"line 2",
			"line 3",
		},
	})
	src := NewServiceSource("api", provider)

	lineCh := make(chan string, 10)
	err := src.ReadRange(context.Background(), time.Time{}, time.Now(), lineCh, "", 0, 0)
	require.NoError(t, err)
	close(lineCh)

	var received []string
	for line := range lineCh {
		received = append(received, line)
	}

	assert.Equal(t, []string{"line 1", "line 2", "line 3"}, received)

	// Check status updated
	status := src.Status()
	assert.Equal(t, int64(3), status.LinesRead)
	assert.Greater(t, status.BytesRead, int64(0))
}

func TestServiceSourceReadRange_EmptyBuffer(t *testing.T) {
	provider := newMockProvider(map[string][]string{
		"api": {},
	})
	src := NewServiceSource("api", provider)

	lineCh := make(chan string, 10)
	err := src.ReadRange(context.Background(), time.Time{}, time.Now(), lineCh, "", 0, 0)
	require.NoError(t, err)
	close(lineCh)

	var received []string
	for line := range lineCh {
		received = append(received, line)
	}

	assert.Empty(t, received)
}

func TestServiceSourceReadRange_GrepFilter(t *testing.T) {
	provider := newMockProvider(map[string][]string{
		"api": {
			`{"msg":"request received","id":"req-123"}`,
			`{"msg":"processing order","id":"ord-456"}`,
			`{"msg":"request completed","id":"req-123"}`,
			`{"msg":"error occurred","id":"req-789"}`,
		},
	})
	src := NewServiceSource("api", provider)

	lineCh := make(chan string, 10)
	err := src.ReadRange(context.Background(), time.Time{}, time.Now(), lineCh, "req-123", 0, 0)
	require.NoError(t, err)
	close(lineCh)

	var received []string
	for line := range lineCh {
		received = append(received, line)
	}

	assert.Len(t, received, 2)
	assert.Contains(t, received[0], "req-123")
	assert.Contains(t, received[1], "req-123")
}

func TestServiceSourceReadRange_GrepRegex(t *testing.T) {
	provider := newMockProvider(map[string][]string{
		"api": {
			"INFO: starting up",
			"ERROR: connection failed",
			"WARN: retrying",
			"ERROR: timeout",
		},
	})
	src := NewServiceSource("api", provider)

	lineCh := make(chan string, 10)
	err := src.ReadRange(context.Background(), time.Time{}, time.Now(), lineCh, "ERROR|WARN", 0, 0)
	require.NoError(t, err)
	close(lineCh)

	var received []string
	for line := range lineCh {
		received = append(received, line)
	}

	assert.Len(t, received, 3)
}

func TestServiceSourceReadRange_SkipsTrellisMessages(t *testing.T) {
	provider := newMockProvider(map[string][]string{
		"api": {
			"[trellis] Service started",
			"real log line 1",
			"[trellis] Service restarted",
			"real log line 2",
		},
	})
	src := NewServiceSource("api", provider)

	lineCh := make(chan string, 10)
	err := src.ReadRange(context.Background(), time.Time{}, time.Now(), lineCh, "", 0, 0)
	require.NoError(t, err)
	close(lineCh)

	var received []string
	for line := range lineCh {
		received = append(received, line)
	}

	assert.Len(t, received, 2)
	assert.Equal(t, "real log line 1", received[0])
	assert.Equal(t, "real log line 2", received[1])
}

func TestServiceSourceReadRange_ContextCancellation(t *testing.T) {
	// Create a provider with many lines
	lines := make([]string, 1000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	provider := newMockProvider(map[string][]string{
		"api": lines,
	})
	src := NewServiceSource("api", provider)

	ctx, cancel := context.WithCancel(context.Background())

	// Use a small channel buffer so it blocks
	lineCh := make(chan string, 1)

	// Cancel immediately
	cancel()

	err := src.ReadRange(ctx, time.Time{}, time.Now(), lineCh, "", 0, 0)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestServiceSourceReadRange_ServiceNotFound(t *testing.T) {
	provider := newMockProvider(map[string][]string{})
	src := NewServiceSource("nonexistent", provider)

	lineCh := make(chan string, 10)
	err := src.ReadRange(context.Background(), time.Time{}, time.Now(), lineCh, "", 0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestServiceSourceReadRange_ProviderError(t *testing.T) {
	provider := &mockServiceLogProvider{
		sizeErr: fmt.Errorf("connection error"),
	}
	src := NewServiceSource("api", provider)

	lineCh := make(chan string, 10)
	err := src.ReadRange(context.Background(), time.Time{}, time.Now(), lineCh, "", 0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection error")
}

func TestServiceSourceReadRange_LogsError(t *testing.T) {
	provider := &mockServiceLogProvider{
		logs:    map[string][]string{"api": {"line1"}},
		logsErr: fmt.Errorf("logs retrieval failed"),
	}
	src := NewServiceSource("api", provider)

	lineCh := make(chan string, 10)
	err := src.ReadRange(context.Background(), time.Time{}, time.Now(), lineCh, "", 0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "logs retrieval failed")
}

func TestServiceSourceReadRange_InvalidRegexFallsBackToLiteral(t *testing.T) {
	provider := newMockProvider(map[string][]string{
		"api": {
			"line with [bracket",
			"line without bracket",
			"another [bracket line",
		},
	})
	src := NewServiceSource("api", provider)

	lineCh := make(chan string, 10)
	// Invalid regex "[bracket" should fall back to literal match
	err := src.ReadRange(context.Background(), time.Time{}, time.Now(), lineCh, "[bracket", 0, 0)
	require.NoError(t, err)
	close(lineCh)

	var received []string
	for line := range lineCh {
		received = append(received, line)
	}

	assert.Len(t, received, 2)
	assert.Contains(t, received[0], "[bracket")
	assert.Contains(t, received[1], "[bracket")
}

func TestServiceSourceReadRange_StatusUpdates(t *testing.T) {
	provider := newMockProvider(map[string][]string{
		"api": {"line1", "line2", "line3"},
	})
	src := NewServiceSource("api", provider)

	// Verify initial status
	status := src.Status()
	assert.Equal(t, int64(0), status.LinesRead)
	assert.Equal(t, int64(0), status.BytesRead)

	lineCh := make(chan string, 10)
	err := src.ReadRange(context.Background(), time.Time{}, time.Now(), lineCh, "", 0, 0)
	require.NoError(t, err)
	close(lineCh)

	// Drain channel
	for range lineCh {
	}

	// Verify status was updated
	status = src.Status()
	assert.Equal(t, int64(3), status.LinesRead)
	expectedBytes := int64(len("line1") + len("line2") + len("line3"))
	assert.Equal(t, expectedBytes, status.BytesRead)
}
