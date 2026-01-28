// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
)

func newTestBus() *events.MemoryEventBus {
	return events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: 100,
		HistoryMaxAge:    time.Hour,
	})
}

func TestManager_New(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "api", Command: []string{"echo", "api"}, WorkDir: "/tmp"},
		{Name: "worker", Command: []string{"echo", "worker"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	list := mgr.List()
	assert.Len(t, list, 2)
}

func TestManager_Start(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "test-service", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	err := mgr.Start(context.Background(), "test-service")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	status, err := mgr.Status("test-service")
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, status.State)
}

func TestManager_Start_NotFound(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mgr := NewManager(nil, bus, nil)

	err := mgr.Start(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestManager_Stop(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "test-service", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	err := mgr.Start(context.Background(), "test-service")
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	err = mgr.Stop(context.Background(), "test-service")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	status, err := mgr.Status("test-service")
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, status.State)
}

func TestManager_Stop_NotFound(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mgr := NewManager(nil, bus, nil)

	err := mgr.Stop(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestManager_Restart(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "test-service", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	err := mgr.Start(context.Background(), "test-service")
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	status, _ := mgr.Status("test-service")
	firstPID := status.PID

	err = mgr.Restart(context.Background(), "test-service", RestartManual)
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	status, _ = mgr.Status("test-service")
	assert.NotEqual(t, firstPID, status.PID)
	assert.Equal(t, StatusRunning, status.State)
}

func TestManager_Restart_NotRunning(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "test-service", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	// Restart should start the service if not running
	err := mgr.Restart(context.Background(), "test-service", RestartManual)
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	status, _ := mgr.Status("test-service")
	assert.Equal(t, StatusRunning, status.State)
}

func TestManager_Status(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "test-service", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	status, err := mgr.Status("test-service")
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, status.State)
}

func TestManager_Status_NotFound(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mgr := NewManager(nil, bus, nil)

	_, err := mgr.Status("nonexistent")
	assert.Error(t, err)
}

func TestManager_Logs(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "test-service", Command: []string{"sh", "-c", "echo hello; echo world"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	err := mgr.Start(context.Background(), "test-service")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	lines, err := mgr.Logs("test-service", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, lines)
}

func TestManager_Logs_NotFound(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mgr := NewManager(nil, bus, nil)

	_, err := mgr.Logs("nonexistent", 10)
	assert.Error(t, err)
}

func TestManager_List(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "api", Command: []string{"echo", "api"}, WorkDir: "/tmp"},
		{Name: "worker", Command: []string{"echo", "worker"}, WorkDir: "/tmp"},
		{Name: "db", Command: []string{"echo", "db"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	list := mgr.List()
	assert.Len(t, list, 3)

	names := make(map[string]bool)
	for _, info := range list {
		names[info.Name] = true
	}
	assert.True(t, names["api"])
	assert.True(t, names["worker"])
	assert.True(t, names["db"])
}

func TestManager_StartAll(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "api", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
		{Name: "worker", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	err := mgr.StartAll(context.Background())
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	for _, svc := range services {
		status, err := mgr.Status(svc.Name)
		require.NoError(t, err)
		assert.Equal(t, StatusRunning, status.State, "service %s not running", svc.Name)
	}
}

func TestManager_StopAll(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "api", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
		{Name: "worker", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)

	err := mgr.StartAll(context.Background())
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)

	err = mgr.StopAll(context.Background())
	require.NoError(t, err)

	for _, svc := range services {
		status, err := mgr.Status(svc.Name)
		require.NoError(t, err)
		assert.Equal(t, StatusStopped, status.State, "service %s not stopped", svc.Name)
	}
}

func TestManager_EventPublishing(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "test-service", Command: []string{"sh", "-c", "exit 0"}, WorkDir: "/tmp"},
	}

	var startedCount, stoppedCount atomic.Int32

	bus.Subscribe("service.started", func(ctx context.Context, e events.Event) error {
		startedCount.Add(1)
		return nil
	})

	bus.Subscribe("service.stopped", func(ctx context.Context, e events.Event) error {
		stoppedCount.Add(1)
		return nil
	})

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	err := mgr.Start(context.Background(), "test-service")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, int32(1), startedCount.Load())
	// Service exits immediately, so stopped should also be emitted
	assert.GreaterOrEqual(t, stoppedCount.Load(), int32(1))
}

func TestManager_RestartOnCrash(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{
			Name:          "test-service",
			Command:       []string{"sh", "-c", "exit 1"},
			WorkDir:       "/tmp",
			RestartPolicy: "on-failure",
			RestartDelay:  "50ms",
			MaxRestarts:   2,
		},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	err := mgr.Start(context.Background(), "test-service")
	require.NoError(t, err)

	// Wait for restarts to happen
	time.Sleep(500 * time.Millisecond)

	status, _ := mgr.Status("test-service")
	// After max restarts exceeded, should be stopped or crashed
	assert.GreaterOrEqual(t, status.RestartCount, 1)
}

func TestManager_RestartPolicy_Always(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{
			Name:          "test-service",
			Command:       []string{"sh", "-c", "exit 0"},
			WorkDir:       "/tmp",
			RestartPolicy: "always",
			RestartDelay:  "50ms",
			MaxRestarts:   2,
		},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	err := mgr.Start(context.Background(), "test-service")
	require.NoError(t, err)

	time.Sleep(400 * time.Millisecond)

	status, _ := mgr.Status("test-service")
	// "always" policy should restart even on clean exit
	assert.GreaterOrEqual(t, status.RestartCount, 1)
}

func TestManager_RestartPolicy_Never(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{
			Name:          "test-service",
			Command:       []string{"sh", "-c", "exit 1"},
			WorkDir:       "/tmp",
			RestartPolicy: "never",
		},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	err := mgr.Start(context.Background(), "test-service")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	status, _ := mgr.Status("test-service")
	assert.Equal(t, 0, status.RestartCount)
}

func TestManager_Dependencies(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "db", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
		{Name: "api", Command: []string{"sleep", "60"}, WorkDir: "/tmp", DependsOn: []string{"db"}},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	// Starting api should start db first
	err := mgr.Start(context.Background(), "api")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	dbStatus, _ := mgr.Status("db")
	apiStatus, _ := mgr.Status("api")

	assert.Equal(t, StatusRunning, dbStatus.State)
	assert.Equal(t, StatusRunning, apiStatus.State)
}

func TestManager_StopWithDependencies(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "db", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
		{Name: "api", Command: []string{"sleep", "60"}, WorkDir: "/tmp", DependsOn: []string{"db"}},
	}

	mgr := NewManager(services, bus, nil)

	// Start both
	mgr.StartAll(context.Background())
	time.Sleep(200 * time.Millisecond)

	// Stopping db should stop api first (reverse dependency)
	err := mgr.Stop(context.Background(), "db")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	dbStatus, _ := mgr.Status("db")
	apiStatus, _ := mgr.Status("api")

	assert.Equal(t, StatusStopped, dbStatus.State)
	assert.Equal(t, StatusStopped, apiStatus.State)
}

func TestManager_Disabled(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	disabled := true
	services := []config.ServiceConfig{
		{Name: "disabled-service", Command: []string{"sleep", "60"}, WorkDir: "/tmp", Disabled: &disabled},
		{Name: "enabled-service", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	err := mgr.StartAll(context.Background())
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	disabledStatus, _ := mgr.Status("disabled-service")
	enabledStatus, _ := mgr.Status("enabled-service")

	assert.Equal(t, StatusStopped, disabledStatus.State) // Should not start
	assert.Equal(t, StatusRunning, enabledStatus.State)
}

func TestManager_GetService(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "test-service", Command: []string{"echo", "hi"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)

	svc, exists := mgr.GetService("test-service")
	assert.True(t, exists)
	assert.Equal(t, "test-service", svc.Name)

	_, exists = mgr.GetService("nonexistent")
	assert.False(t, exists)
}

func TestRestartTrigger_String(t *testing.T) {
	tests := []struct {
		trigger  RestartTrigger
		expected string
	}{
		{RestartManual, "manual"},
		{RestartCrash, "crash"},
		{RestartWatch, "watch"},
		{RestartDependency, "dependency"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.trigger.String())
		})
	}
}

func TestManager_Concurrency(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "svc1", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
		{Name: "svc2", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
		{Name: "svc3", Command: []string{"sleep", "60"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	// Concurrent operations
	done := make(chan bool, 30)

	// Start all concurrently
	for _, svc := range services {
		go func(name string) {
			mgr.Start(context.Background(), name)
			done <- true
		}(svc.Name)
	}

	// Concurrent status checks
	for i := 0; i < 10; i++ {
		go func() {
			for _, svc := range services {
				mgr.Status(svc.Name)
			}
			done <- true
		}()
	}

	// Concurrent list
	for i := 0; i < 10; i++ {
		go func() {
			mgr.List()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 23; i++ {
		<-done
	}
}

func TestManager_LogSize(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	services := []config.ServiceConfig{
		{Name: "test-service", Command: []string{"sh", "-c", "echo hello; echo world"}, WorkDir: "/tmp"},
	}

	mgr := NewManager(services, bus, nil)
	defer mgr.StopAll(context.Background())

	// Empty buffer initially
	size, err := mgr.LogSize("test-service")
	require.NoError(t, err)
	assert.Equal(t, 0, size)

	// Start service to generate some output
	err = mgr.Start(context.Background(), "test-service")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// Buffer should have some lines now
	size, err = mgr.LogSize("test-service")
	require.NoError(t, err)
	assert.Greater(t, size, 0)
}

func TestManager_LogSize_NotFound(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mgr := NewManager(nil, bus, nil)

	_, err := mgr.LogSize("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
