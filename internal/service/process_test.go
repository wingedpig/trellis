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
)

func TestProcess_Start(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"echo", "hello"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	err := proc.Start(context.Background())
	require.NoError(t, err)

	// Wait for process to complete
	time.Sleep(100 * time.Millisecond)

	status := proc.Status()
	assert.Equal(t, StatusStopped, status.State)
	assert.Equal(t, 0, status.ExitCode)
}

func TestProcess_StartAlreadyRunning(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sleep", "10"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)
	defer proc.Stop(context.Background())

	err := proc.Start(context.Background())
	require.NoError(t, err)

	// Try to start again
	err = proc.Start(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already running")
}

func TestProcess_Stop(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sleep", "60"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	err := proc.Start(context.Background())
	require.NoError(t, err)

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	err = proc.Stop(context.Background())
	require.NoError(t, err)

	status := proc.Status()
	assert.Equal(t, StatusStopped, status.State)
}

func TestProcess_StopNotRunning(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"echo", "hello"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	// Stop without starting should be fine (idempotent)
	err := proc.Stop(context.Background())
	assert.NoError(t, err)
}

func TestProcess_StopWithGracePeriod(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:        "test-service",
		Command:     []string{"sleep", "60"},
		WorkDir:     "/tmp",
		StopSignal:  "SIGTERM",
		StopTimeout: "100ms",
	}

	proc := NewProcess(cfg, nil)

	err := proc.Start(context.Background())
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	err = proc.Stop(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	// Should stop quickly, not wait full grace period if process handles signal
	assert.Less(t, elapsed, 500*time.Millisecond)
}

func TestProcess_Status_Running(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sleep", "10"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)
	defer proc.Stop(context.Background())

	err := proc.Start(context.Background())
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	status := proc.Status()
	assert.Equal(t, StatusRunning, status.State)
	assert.NotZero(t, status.PID)
	assert.False(t, status.StartedAt.IsZero())
}

func TestProcess_Status_Stopped(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"echo", "hello"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	status := proc.Status()
	assert.Equal(t, StatusStopped, status.State)
	assert.Zero(t, status.PID)
}

func TestProcess_Logs(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sh", "-c", "echo line1; echo line2; echo line3"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	err := proc.Start(context.Background())
	require.NoError(t, err)

	// Wait for command to complete
	time.Sleep(200 * time.Millisecond)

	lines := proc.Logs(10)
	require.GreaterOrEqual(t, len(lines), 3)

	// Should contain our output
	found := 0
	for _, line := range lines {
		if line == "line1" || line == "line2" || line == "line3" {
			found++
		}
	}
	assert.GreaterOrEqual(t, found, 3)
}

func TestProcess_LogsLimit(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sh", "-c", "for i in 1 2 3 4 5; do echo line$i; done"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	err := proc.Start(context.Background())
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	lines := proc.Logs(2)
	assert.Len(t, lines, 2)
}

func TestProcess_ExitCode(t *testing.T) {
	tests := []struct {
		name     string
		command  []string
		expected int
	}{
		{"exit 0", []string{"sh", "-c", "exit 0"}, 0},
		{"exit 1", []string{"sh", "-c", "exit 1"}, 1},
		{"exit 42", []string{"sh", "-c", "exit 42"}, 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.ServiceConfig{
				Name:    "test-service",
				Command: tt.command,
				WorkDir: "/tmp",
			}

			proc := NewProcess(cfg, nil)

			err := proc.Start(context.Background())
			require.NoError(t, err)

			// Wait for exit
			time.Sleep(200 * time.Millisecond)

			status := proc.Status()
			assert.Equal(t, tt.expected, status.ExitCode)
		})
	}
}

func TestProcess_Environment(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sh", "-c", "echo $TEST_VAR"},
		WorkDir: "/tmp",
		Env: map[string]string{
			"TEST_VAR": "hello_world",
		},
	}

	proc := NewProcess(cfg, nil)

	err := proc.Start(context.Background())
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	lines := proc.Logs(10)
	found := false
	for _, line := range lines {
		if line == "hello_world" {
			found = true
			break
		}
	}
	assert.True(t, found, "Environment variable not found in output")
}

func TestProcess_WorkDir(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"pwd"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	err := proc.Start(context.Background())
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	lines := proc.Logs(10)
	found := false
	for _, line := range lines {
		if line == "/tmp" || line == "/private/tmp" { // macOS shows /private/tmp
			found = true
			break
		}
	}
	assert.True(t, found, "Work directory not correct in output")
}

func TestProcess_OnExit_Callback(t *testing.T) {
	var exitCode atomic.Int32
	callbackDone := make(chan struct{})

	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sh", "-c", "exit 42"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)
	proc.OnExit(func(code int) {
		exitCode.Store(int32(code))
		close(callbackDone)
	})

	err := proc.Start(context.Background())
	require.NoError(t, err)

	// Wait for exit callback
	select {
	case <-callbackDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for exit callback")
	}

	assert.Equal(t, int32(42), exitCode.Load())
}

func TestProcess_Restart(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sleep", "60"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	// Start
	err := proc.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	firstPID := proc.Status().PID
	require.NotZero(t, firstPID)

	// Stop
	err = proc.Stop(context.Background())
	require.NoError(t, err)

	// Start again
	err = proc.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	defer proc.Stop(context.Background())

	secondPID := proc.Status().PID
	require.NotZero(t, secondPID)
	assert.NotEqual(t, firstPID, secondPID)
}

func TestProcess_ContextCancellation(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sleep", "60"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	err := proc.Start(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Process should stop
	time.Sleep(200 * time.Millisecond)

	status := proc.Status()
	assert.Equal(t, StatusStopped, status.State)
}

func TestProcess_StderrCapture(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sh", "-c", "echo stdout; echo stderr >&2"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	err := proc.Start(context.Background())
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	lines := proc.Logs(10)

	// Both stdout and stderr should be captured
	foundStdout := false
	foundStderr := false
	for _, line := range lines {
		if line == "stdout" {
			foundStdout = true
		}
		if line == "stderr" {
			foundStderr = true
		}
	}
	assert.True(t, foundStdout, "stdout not captured")
	assert.True(t, foundStderr, "stderr not captured")
}

func TestProcess_Signal(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"sleep", "60"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)
	defer proc.Stop(context.Background())

	err := proc.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	// Send SIGTERM
	err = proc.Signal("SIGTERM")
	require.NoError(t, err)

	// Process should stop
	time.Sleep(200 * time.Millisecond)

	status := proc.Status()
	assert.Equal(t, StatusStopped, status.State)
}

func TestProcess_InvalidCommand(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{"/nonexistent/binary"},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	err := proc.Start(context.Background())
	assert.Error(t, err)
}

func TestProcess_EmptyCommand(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:    "test-service",
		Command: []string{},
		WorkDir: "/tmp",
	}

	proc := NewProcess(cfg, nil)

	err := proc.Start(context.Background())
	assert.Error(t, err)
}

func TestProcessState_String(t *testing.T) {
	tests := []struct {
		state    ProcessState
		expected string
	}{
		{StatusStopped, "stopped"},
		{StatusStarting, "starting"},
		{StatusRunning, "running"},
		{StatusStopping, "stopping"},
		{StatusCrashed, "crashed"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}
