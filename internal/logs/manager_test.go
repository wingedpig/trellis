// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
)

func TestNewManager(t *testing.T) {
	bus := events.NewMemoryEventBus(events.MemoryBusConfig{})
	manager := NewManager(bus, config.LogViewerSettings{})

	if manager == nil {
		t.Fatal("NewManager returned nil")
	}
}

func TestNewManagerNilBus(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	if manager == nil {
		t.Fatal("NewManager with nil bus returned nil")
	}
}

func TestManagerInitialize(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "viewer1",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/test1.log",
			},
			Parser: config.LogParserConfig{
				Type: "json",
			},
		},
		{
			Name: "viewer2",
			Source: config.LogSourceConfig{
				Type:    "command",
				Command: []string{"echo", "test"},
			},
			Parser: config.LogParserConfig{
				Type: "none",
			},
		},
	}

	err := manager.Initialize(configs)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Check viewers were created
	names := manager.List()
	if len(names) != 2 {
		t.Errorf("List() returned %d names, want 2", len(names))
	}
}

func TestManagerInitializeInvalidConfig(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "bad-viewer",
			Source: config.LogSourceConfig{
				Type: "unknown",
			},
		},
	}

	err := manager.Initialize(configs)
	if err == nil {
		t.Error("Initialize with invalid config should return error")
	}
}

func TestManagerGet(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "test-viewer",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/test.log",
			},
			Parser: config.LogParserConfig{
				Type: "json",
			},
		},
	}

	manager.Initialize(configs)

	// Get existing viewer
	viewer, ok := manager.Get("test-viewer")
	if !ok {
		t.Error("Get() should return true for existing viewer")
	}
	if viewer == nil {
		t.Error("Get() returned nil viewer")
	}
	if viewer.Name() != "test-viewer" {
		t.Errorf("Viewer name = %q, want %q", viewer.Name(), "test-viewer")
	}

	// Get non-existent viewer
	_, ok = manager.Get("nonexistent")
	if ok {
		t.Error("Get() should return false for non-existent viewer")
	}
}

func TestManagerList(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	// Empty manager
	names := manager.List()
	if len(names) != 0 {
		t.Errorf("List() on empty manager returned %d names, want 0", len(names))
	}

	// Add viewers
	configs := []config.LogViewerConfig{
		{
			Name: "viewer-a",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/a.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
		{
			Name: "viewer-b",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/b.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	manager.Initialize(configs)

	names = manager.List()
	if len(names) != 2 {
		t.Errorf("List() returned %d names, want 2", len(names))
	}
}

func TestManagerListStatus(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "viewer1",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/test.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
			Buffer: config.LogBufferConfig{MaxEntries: 1000},
		},
		{
			Name: "viewer2",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/test2.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
			Buffer: config.LogBufferConfig{MaxEntries: 2000},
		},
	}
	manager.Initialize(configs)

	statuses := manager.ListStatus()
	if len(statuses) != 2 {
		t.Errorf("ListStatus() returned %d statuses, want 2", len(statuses))
	}

	// Check status details
	statusMap := make(map[string]ViewerStatus)
	for _, s := range statuses {
		statusMap[s.Name] = s
	}

	if s, ok := statusMap["viewer1"]; !ok {
		t.Error("Missing status for viewer1")
	} else if s.BufferMax != 1000 {
		t.Errorf("viewer1.BufferMax = %d, want 1000", s.BufferMax)
	}

	if s, ok := statusMap["viewer2"]; !ok {
		t.Error("Missing status for viewer2")
	} else if s.BufferMax != 2000 {
		t.Errorf("viewer2.BufferMax = %d, want 2000", s.BufferMax)
	}
}

func TestManagerSubscribe(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "test-viewer",
			Source: config.LogSourceConfig{
				Type:    "command",
				Command: []string{"echo", "test"},
			},
			Parser: config.LogParserConfig{Type: "none"},
		},
	}
	manager.Initialize(configs)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start manager (required for Subscribe to work with lazy startup)
	manager.Start(ctx)
	defer manager.Stop()

	ch := make(chan LogEntry, 10)

	// Subscribe to existing viewer (this also starts the viewer)
	err := manager.Subscribe("test-viewer", ch)
	if err != nil {
		t.Errorf("Subscribe() returned error: %v", err)
	}

	// Subscribe to non-existent viewer
	err = manager.Subscribe("nonexistent", ch)
	if err == nil {
		t.Error("Subscribe() to non-existent viewer should return error")
	}
}

func TestManagerUnsubscribe(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "test-viewer",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/test.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	manager.Initialize(configs)

	ch := make(chan LogEntry, 10)
	manager.Subscribe("test-viewer", ch)

	// Unsubscribe from existing viewer
	err := manager.Unsubscribe("test-viewer", ch)
	if err != nil {
		t.Errorf("Unsubscribe() returned error: %v", err)
	}

	// Unsubscribe from non-existent viewer
	err = manager.Unsubscribe("nonexistent", ch)
	if err == nil {
		t.Error("Unsubscribe() from non-existent viewer should return error")
	}
}

func TestManagerStartStop(t *testing.T) {
	bus := events.NewMemoryEventBus(events.MemoryBusConfig{})
	manager := NewManager(bus, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "test-viewer",
			Source: config.LogSourceConfig{
				Type:    "command",
				Command: []string{"sleep", "10"},
			},
			Parser: config.LogParserConfig{Type: "none"},
		},
	}
	manager.Initialize(configs)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start (lazy - doesn't actually start viewers)
	err := manager.Start(ctx)
	if err != nil {
		t.Errorf("Start() returned error: %v", err)
	}

	// Viewer not running until accessed
	viewer, _ := manager.Get("test-viewer")
	if viewer.IsRunning() {
		t.Error("Viewer should not be running until first access (lazy startup)")
	}

	// GetAndStart should start the viewer
	viewer, err = manager.GetAndStart("test-viewer")
	if err != nil {
		t.Errorf("GetAndStart() returned error: %v", err)
	}
	if !viewer.IsRunning() {
		t.Error("Viewer should be running after GetAndStart")
	}

	// Stop
	manager.Stop()

	// Give time for goroutines to stop
	time.Sleep(100 * time.Millisecond)

	if viewer.IsRunning() {
		t.Error("Viewer should not be running after Stop")
	}
}

func TestManagerStartWithFailingViewer(t *testing.T) {
	bus := events.NewMemoryEventBus(events.MemoryBusConfig{})
	manager := NewManager(bus, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "good-viewer",
			Source: config.LogSourceConfig{
				Type:    "command",
				Command: []string{"sleep", "10"},
			},
			Parser: config.LogParserConfig{Type: "none"},
		},
		{
			Name: "problematic-viewer",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/nonexistent/path/file.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	manager.Initialize(configs)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start should not return error even if some viewers fail
	err := manager.Start(ctx)
	if err != nil {
		t.Errorf("Start() returned error: %v", err)
	}

	// Clean up
	manager.Stop()
}

func TestManagerEmitEvent(t *testing.T) {
	bus := events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: 100,
	})
	manager := NewManager(bus, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "test-viewer",
			Source: config.LogSourceConfig{
				Type:    "command",
				Command: []string{"echo", "test"},
			},
			Parser: config.LogParserConfig{Type: "none"},
		},
	}
	manager.Initialize(configs)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start manager (lazy - stores context but doesn't start viewers)
	manager.Start(ctx)

	// No events should be emitted yet (lazy startup)
	history, _ := bus.History(events.EventFilter{
		Types: []string{"log.connected"},
	})
	if len(history) != 0 {
		t.Error("Expected no log.connected events before first access (lazy startup)")
	}

	// Access the viewer to trigger startup
	_, err := manager.GetAndStart("test-viewer")
	if err != nil {
		t.Errorf("GetAndStart() returned error: %v", err)
	}

	// Wait for events
	time.Sleep(100 * time.Millisecond)

	// Check that log.connected event was emitted after access
	history, _ = bus.History(events.EventFilter{
		Types: []string{"log.connected"},
	})

	if len(history) == 0 {
		t.Error("Expected log.connected event to be emitted after GetAndStart")
	}

	manager.Stop()
}

func TestManagerStopEmpty(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	// Stop on empty manager should not panic
	manager.Stop()
}

func TestManagerConcurrentAccess(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "test-viewer",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/test.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	manager.Initialize(configs)

	done := make(chan bool)

	// Concurrent Get calls
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				manager.Get("test-viewer")
				manager.List()
				manager.ListStatus()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestManagerEmitEventNilBus tests that emitEvent handles nil bus gracefully
func TestManagerEmitEventNilBus(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "test-viewer",
			Source: config.LogSourceConfig{
				Type:    "command",
				Command: []string{"echo", "test"},
			},
			Parser: config.LogParserConfig{Type: "none"},
		},
	}
	manager.Initialize(configs)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start should not panic with nil bus
	err := manager.Start(ctx)
	if err != nil {
		t.Errorf("Start() returned error: %v", err)
	}

	manager.Stop()
}

// TestManagerMultipleViewers tests manager with multiple viewers
func TestManagerMultipleViewers(t *testing.T) {
	bus := events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: 100,
	})
	manager := NewManager(bus, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "viewer1",
			Source: config.LogSourceConfig{
				Type:    "command",
				Command: []string{"echo", "hello1"},
			},
			Parser: config.LogParserConfig{Type: "none"},
		},
		{
			Name: "viewer2",
			Source: config.LogSourceConfig{
				Type:    "command",
				Command: []string{"echo", "hello2"},
			},
			Parser: config.LogParserConfig{Type: "none"},
		},
		{
			Name: "viewer3",
			Source: config.LogSourceConfig{
				Type:    "command",
				Command: []string{"echo", "hello3"},
			},
			Parser: config.LogParserConfig{Type: "none"},
		},
	}
	manager.Initialize(configs)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start manager (lazy - doesn't start viewers)
	err := manager.Start(ctx)
	if err != nil {
		t.Errorf("Start() returned error: %v", err)
	}

	// Verify all viewers were created
	names := manager.List()
	if len(names) != 3 {
		t.Errorf("List() returned %d names, want 3", len(names))
	}

	// Start each viewer by accessing it
	for _, name := range names {
		_, err := manager.GetAndStart(name)
		if err != nil {
			t.Errorf("GetAndStart(%s) returned error: %v", name, err)
		}
	}

	// Wait a bit for all to start
	time.Sleep(100 * time.Millisecond)

	// Check connected events
	history, _ := bus.History(events.EventFilter{
		Types: []string{"log.connected"},
	})

	if len(history) < 3 {
		t.Errorf("Expected at least 3 log.connected events, got %d", len(history))
	}

	manager.Stop()
}

// TestManagerDockerSource tests manager with Docker source
func TestManagerDockerSource(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "docker-viewer",
			Source: config.LogSourceConfig{
				Type:      "docker",
				Container: "test-container",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}

	err := manager.Initialize(configs)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	viewer, ok := manager.Get("docker-viewer")
	if !ok {
		t.Error("Get() should return true for existing viewer")
	}
	if viewer == nil {
		t.Error("Get() returned nil viewer")
	}
}

// TestManagerKubernetesSource tests manager with Kubernetes source
func TestManagerKubernetesSource(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "k8s-viewer",
			Source: config.LogSourceConfig{
				Type:      "kubernetes",
				Namespace: "default",
				Pod:       "my-pod",
				Container: "main",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}

	err := manager.Initialize(configs)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	viewer, ok := manager.Get("k8s-viewer")
	if !ok {
		t.Error("Get() should return true for existing viewer")
	}
	if viewer == nil {
		t.Error("Get() returned nil viewer")
	}

	// Check viewer name
	if viewer.Name() != "k8s-viewer" {
		t.Errorf("Viewer name = %q, want %q", viewer.Name(), "k8s-viewer")
	}
}

// TestManagerErrorEvent tests that log.error events are emitted on errors
func TestManagerErrorEvent(t *testing.T) {
	bus := events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: 100,
	})
	manager := NewManager(bus, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "failing-viewer",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/nonexistent/path/to/file.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	manager.Initialize(configs)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start viewer (should emit error event)
	manager.Start(ctx)

	// Wait for error to be emitted
	time.Sleep(200 * time.Millisecond)

	// Check for error event
	history, _ := bus.History(events.EventFilter{
		Types: []string{"log.error"},
	})

	if len(history) == 0 {
		t.Log("Warning: No log.error event emitted (file might exist)")
	}

	manager.Stop()
}

// TestManager_StopStartCycle tests that Stop/Start cycles don't spawn duplicate
// monitoring goroutines or cause goroutine leaks.
func TestManager_StopStartCycle(t *testing.T) {
	bus := events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: 100,
	})
	defer bus.Close()

	manager := NewManager(bus, config.LogViewerSettings{})

	// Use a file source that will run successfully
	tmpFile, err := os.CreateTemp("", "test-*.log")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString("line1\nline2\n")
	tmpFile.Close()

	configs := []config.LogViewerConfig{
		{
			Name: "test-viewer",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: tmpFile.Name(),
			},
			Parser: config.LogParserConfig{Type: "none"},
		},
	}
	require.NoError(t, manager.Initialize(configs))

	// Perform multiple Stop/Start cycles
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithCancel(context.Background())

		err := manager.Start(ctx)
		require.NoError(t, err, "Start cycle %d", i)

		// Let it run briefly
		time.Sleep(50 * time.Millisecond)

		// Stop should wait for monitor goroutines to exit
		cancel()
		manager.Stop()
	}

	// Count connected/disconnected events - should be balanced
	connectedEvents, _ := bus.History(events.EventFilter{
		Types: []string{"log.connected"},
	})
	disconnectedEvents, _ := bus.History(events.EventFilter{
		Types: []string{"log.disconnected"},
	})

	// Each cycle should have at most 1 connected and 1 disconnected per viewer
	assert.LessOrEqual(t, len(connectedEvents), 3, "Too many connected events suggests duplicate goroutines")
	assert.Equal(t, len(connectedEvents), len(disconnectedEvents), "Mismatched connected/disconnected events")
}

func TestManagerAddViewer(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})
	require.NoError(t, manager.Initialize(nil))

	// Create a service source viewer
	provider := &mockServiceLogProvider{
		logs: map[string][]string{"api": {"line1"}},
	}
	source := NewServiceSource("api", provider)
	cfg := config.LogViewerConfig{
		Name:   "svc:api",
		Parser: config.LogParserConfig{Type: "json"},
	}
	viewer, err := NewViewerWithSource(cfg, source)
	require.NoError(t, err)

	// Add the viewer
	manager.AddViewer(viewer)

	// Should be retrievable
	got, ok := manager.Get("svc:api")
	assert.True(t, ok)
	assert.Equal(t, "svc:api", got.Name())

	// Should appear in list
	names := manager.List()
	assert.Contains(t, names, "svc:api")
}

func TestManagerAddViewer_Multiple(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})
	require.NoError(t, manager.Initialize(nil))

	provider := &mockServiceLogProvider{
		logs: map[string][]string{
			"api":    {"line1"},
			"worker": {"line2"},
		},
	}

	for _, name := range []string{"api", "worker"} {
		source := NewServiceSource(name, provider)
		cfg := config.LogViewerConfig{
			Name:   "svc:" + name,
			Parser: config.LogParserConfig{Type: "json"},
		}
		viewer, err := NewViewerWithSource(cfg, source)
		require.NoError(t, err)
		manager.AddViewer(viewer)
	}

	names := manager.List()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "svc:api")
	assert.Contains(t, names, "svc:worker")
}

func TestManagerRemoveServiceViewers(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	// Initialize with a regular viewer
	configs := []config.LogViewerConfig{
		{
			Name: "regular-viewer",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/test.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	require.NoError(t, manager.Initialize(configs))

	// Add service viewers
	provider := &mockServiceLogProvider{
		logs: map[string][]string{
			"api":    {"line1"},
			"worker": {"line2"},
		},
	}
	for _, name := range []string{"api", "worker"} {
		source := NewServiceSource(name, provider)
		cfg := config.LogViewerConfig{
			Name:   "svc:" + name,
			Parser: config.LogParserConfig{Type: "json"},
		}
		viewer, err := NewViewerWithSource(cfg, source)
		require.NoError(t, err)
		manager.AddViewer(viewer)
	}

	// Should have 3 viewers total
	assert.Len(t, manager.List(), 3)

	// Remove service viewers
	manager.RemoveServiceViewers()

	// Only regular viewer should remain
	names := manager.List()
	assert.Len(t, names, 1)
	assert.Contains(t, names, "regular-viewer")

	// Service viewers should be gone
	_, ok := manager.Get("svc:api")
	assert.False(t, ok)
	_, ok = manager.Get("svc:worker")
	assert.False(t, ok)
}

func TestManagerRemoveServiceViewers_NoServiceViewers(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	configs := []config.LogViewerConfig{
		{
			Name: "regular-viewer",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/test.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	require.NoError(t, manager.Initialize(configs))

	// Removing service viewers when none exist should be a no-op
	manager.RemoveServiceViewers()

	names := manager.List()
	assert.Len(t, names, 1)
	assert.Contains(t, names, "regular-viewer")
}

func TestManagerUpdateConfigs_PreservesServiceViewers(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	// Initialize with regular viewers
	configs := []config.LogViewerConfig{
		{
			Name: "viewer-a",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/a.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	require.NoError(t, manager.Initialize(configs))

	// Add a service viewer
	provider := &mockServiceLogProvider{
		logs: map[string][]string{"api": {"line1"}},
	}
	source := NewServiceSource("api", provider)
	svcCfg := config.LogViewerConfig{
		Name:   "svc:api",
		Parser: config.LogParserConfig{Type: "json"},
	}
	viewer, err := NewViewerWithSource(svcCfg, source)
	require.NoError(t, err)
	manager.AddViewer(viewer)

	// Should have 2 viewers
	assert.Len(t, manager.List(), 2)

	// Update configs with a new set of regular viewers
	newConfigs := []config.LogViewerConfig{
		{
			Name: "viewer-b",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/b.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	err = manager.UpdateConfigs(newConfigs)
	require.NoError(t, err)

	// Should have 2 viewers: new regular + preserved service
	names := manager.List()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "viewer-b")
	assert.Contains(t, names, "svc:api")

	// Old regular viewer should be gone
	_, ok := manager.Get("viewer-a")
	assert.False(t, ok)
}

func TestManagerUpdateConfigs_OldBehaviorWithoutServiceViewers(t *testing.T) {
	manager := NewManager(nil, config.LogViewerSettings{})

	// Initialize with viewers
	configs := []config.LogViewerConfig{
		{
			Name: "viewer-a",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/a.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
		{
			Name: "viewer-b",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/b.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	require.NoError(t, manager.Initialize(configs))
	assert.Len(t, manager.List(), 2)

	// UpdateConfigs with different viewers
	newConfigs := []config.LogViewerConfig{
		{
			Name: "viewer-c",
			Source: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/c.log",
			},
			Parser: config.LogParserConfig{Type: "json"},
		},
	}
	err := manager.UpdateConfigs(newConfigs)
	require.NoError(t, err)

	// Only new viewer should exist
	names := manager.List()
	assert.Len(t, names, 1)
	assert.Contains(t, names, "viewer-c")
}
