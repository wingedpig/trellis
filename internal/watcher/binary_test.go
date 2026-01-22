// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/events"
)

func newTestBus() *events.MemoryEventBus {
	return events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: 100,
		HistoryMaxAge:    time.Hour,
	})
}

func TestBinaryWatcher_New(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	assert.NotNil(t, w)
}

func TestBinaryWatcher_Watch(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	// Create temp file
	tmpFile, err := os.CreateTemp("", "test-binary-*")
	require.NoError(t, err)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// Watch the file
	err = w.Watch("test-service", []string{tmpFile.Name()})
	require.NoError(t, err)

	// Should be tracked
	watching := w.Watching()
	assert.Contains(t, watching, "test-service")
}

func TestBinaryWatcher_WatchNonexistent(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	// Watch nonexistent file - no error, but service not tracked since no valid paths
	err = w.Watch("test-service", []string{"/tmp/nonexistent-binary-12345"})
	require.NoError(t, err)

	// Service should not be tracked since no valid paths were added
	watching := w.Watching()
	assert.NotContains(t, watching, "test-service")
}

func TestBinaryWatcher_WatchDuplicate(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	tmpFile, err := os.CreateTemp("", "test-binary-*")
	require.NoError(t, err)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	err = w.Watch("test-service", []string{tmpFile.Name()})
	require.NoError(t, err)

	// Watching same service again should update paths
	tmpFile2, err := os.CreateTemp("", "test-binary-2-*")
	require.NoError(t, err)
	tmpFile2.Close()
	defer os.Remove(tmpFile2.Name())

	err = w.Watch("test-service", []string{tmpFile2.Name()})
	require.NoError(t, err)

	watching := w.Watching()
	assert.Len(t, watching, 1)
}

func TestBinaryWatcher_Unwatch(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	tmpFile, err := os.CreateTemp("", "test-binary-*")
	require.NoError(t, err)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	w.Watch("test-service", []string{tmpFile.Name()})

	err = w.Unwatch("test-service")
	require.NoError(t, err)

	watching := w.Watching()
	assert.NotContains(t, watching, "test-service")
}

func TestBinaryWatcher_UnwatchNonexistent(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	err = w.Unwatch("nonexistent")
	assert.Error(t, err)
}

func TestBinaryWatcher_FileChange_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	bus := newTestBus()
	defer bus.Close()

	var eventReceived atomic.Bool
	var receivedService string

	bus.Subscribe("binary.changed", func(ctx context.Context, e events.Event) error {
		eventReceived.Store(true)
		if svc, ok := e.Payload["service"].(string); ok {
			receivedService = svc
		}
		return nil
	})

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	// Create temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-binary")
	err = os.WriteFile(tmpFile, []byte("original"), 0755)
	require.NoError(t, err)

	// Watch the file
	err = w.Watch("test-service", []string{tmpFile})
	require.NoError(t, err)

	// Give watcher time to start
	time.Sleep(100 * time.Millisecond)

	// Modify the file
	err = os.WriteFile(tmpFile, []byte("modified"), 0755)
	require.NoError(t, err)

	// Wait for event (debounce + processing)
	time.Sleep(200 * time.Millisecond)

	assert.True(t, eventReceived.Load(), "binary.changed event should be received")
	assert.Equal(t, "test-service", receivedService)
}

func TestBinaryWatcher_MultipleServices_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	bus := newTestBus()
	defer bus.Close()

	changedServices := make(map[string]bool)
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}

	bus.Subscribe("binary.changed", func(ctx context.Context, e events.Event) error {
		if svc, ok := e.Payload["service"].(string); ok {
			<-mu
			changedServices[svc] = true
			mu <- struct{}{}
		}
		return nil
	})

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	tmpDir := t.TempDir()

	// Create and watch multiple files
	file1 := filepath.Join(tmpDir, "binary1")
	file2 := filepath.Join(tmpDir, "binary2")

	os.WriteFile(file1, []byte("v1"), 0755)
	os.WriteFile(file2, []byte("v1"), 0755)

	w.Watch("service1", []string{file1})
	w.Watch("service2", []string{file2})

	time.Sleep(100 * time.Millisecond)

	// Modify only file1
	os.WriteFile(file1, []byte("v2"), 0755)

	time.Sleep(200 * time.Millisecond)

	<-mu
	assert.True(t, changedServices["service1"])
	assert.False(t, changedServices["service2"])
	mu <- struct{}{}
}

func TestBinaryWatcher_SetDebounce(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	w, err := NewBinaryWatcher(bus, 100*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	w.SetDebounce(50 * time.Millisecond)
	// No error means success
}

func TestBinaryWatcher_Close(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)

	tmpFile, err := os.CreateTemp("", "test-binary-*")
	require.NoError(t, err)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	w.Watch("test-service", []string{tmpFile.Name()})

	err = w.Close()
	require.NoError(t, err)

	// Double close should be safe
	err = w.Close()
	assert.NoError(t, err)
}

func TestBinaryWatcher_Watching(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	assert.Empty(t, w.Watching())

	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "binary1")
	file2 := filepath.Join(tmpDir, "binary2")

	os.WriteFile(file1, []byte(""), 0755)
	os.WriteFile(file2, []byte(""), 0755)

	w.Watch("service1", []string{file1})
	w.Watch("service2", []string{file2})

	watching := w.Watching()
	assert.Len(t, watching, 2)
	assert.Contains(t, watching, "service1")
	assert.Contains(t, watching, "service2")
}

func TestBinaryWatcher_AtomicRename_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	bus := newTestBus()
	defer bus.Close()

	var eventReceived atomic.Bool

	bus.Subscribe("binary.changed", func(ctx context.Context, e events.Event) error {
		eventReceived.Store(true)
		return nil
	})

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	tmpDir := t.TempDir()
	binaryFile := filepath.Join(tmpDir, "binary")
	tempFile := filepath.Join(tmpDir, "binary.tmp")

	// Create original binary
	os.WriteFile(binaryFile, []byte("v1"), 0755)

	// Watch the binary
	w.Watch("test-service", []string{binaryFile})
	time.Sleep(100 * time.Millisecond)

	// Atomic rename (common build pattern)
	os.WriteFile(tempFile, []byte("v2"), 0755)
	os.Rename(tempFile, binaryFile)

	// Wait for event
	time.Sleep(200 * time.Millisecond)

	assert.True(t, eventReceived.Load(), "should detect atomic rename")
}

func TestBinaryWatcher_RapidChanges_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	bus := newTestBus()
	defer bus.Close()

	var eventCount atomic.Int32

	bus.Subscribe("binary.changed", func(ctx context.Context, e events.Event) error {
		eventCount.Add(1)
		return nil
	})

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	tmpDir := t.TempDir()
	binaryFile := filepath.Join(tmpDir, "binary")

	os.WriteFile(binaryFile, []byte("v0"), 0755)
	w.Watch("test-service", []string{binaryFile})
	time.Sleep(100 * time.Millisecond)

	// Rapid writes
	for i := 0; i < 10; i++ {
		os.WriteFile(binaryFile, []byte("v"+string(rune('0'+i))), 0755)
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for debounce to settle
	time.Sleep(150 * time.Millisecond)

	// Should have only 1 event due to debouncing
	assert.Equal(t, int32(1), eventCount.Load())
}

func TestBinaryWatcher_MultipleFilesPerService_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	bus := newTestBus()
	defer bus.Close()

	var eventReceived atomic.Bool
	var changedPath string

	bus.Subscribe("binary.changed", func(ctx context.Context, e events.Event) error {
		eventReceived.Store(true)
		if path, ok := e.Payload["path"].(string); ok {
			changedPath = path
		}
		return nil
	})

	w, err := NewBinaryWatcher(bus, 50*time.Millisecond)
	require.NoError(t, err)
	defer w.Close()

	tmpDir := t.TempDir()
	binaryFile := filepath.Join(tmpDir, "binary")
	configFile := filepath.Join(tmpDir, "config.yaml")

	os.WriteFile(binaryFile, []byte("binary"), 0755)
	os.WriteFile(configFile, []byte("config: value"), 0644)

	// Watch both binary and config file
	w.Watch("test-service", []string{binaryFile, configFile})
	time.Sleep(100 * time.Millisecond)

	// Modify the config file (not the binary)
	os.WriteFile(configFile, []byte("config: updated"), 0644)

	time.Sleep(200 * time.Millisecond)

	assert.True(t, eventReceived.Load(), "should detect config file change")
	assert.Equal(t, configFile, changedPath, "changed path should be config file")
}
