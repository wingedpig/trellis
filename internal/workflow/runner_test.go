// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package workflow

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/events"
)

// waitForCompletion polls the runner status until the workflow completes or times out
func waitForCompletion(t *testing.T, runner Runner, runID string, timeout time.Duration) *WorkflowStatus {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, ok := runner.Status(runID)
		if !ok {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if status.State != StateRunning {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("workflow %s did not complete within %v", runID, timeout)
	return nil
}

// MockServiceController for testing
type MockServiceController struct {
	StopCalled           atomic.Int32
	StartCalled          atomic.Int32
	RestartCalled        atomic.Int32
	StopWatchedCalled    atomic.Int32
	RestartWatchedCalled atomic.Int32
	StoppedServices      []string
	StopErr              error
	StartErr             error
	RestartErr           error
}

func (m *MockServiceController) StopServices(ctx context.Context, names []string) error {
	m.StopCalled.Add(1)
	m.StoppedServices = names
	return m.StopErr
}

func (m *MockServiceController) StartAllServices(ctx context.Context) error {
	m.StartCalled.Add(1)
	return m.StartErr
}

func (m *MockServiceController) RestartAllServices(ctx context.Context) error {
	m.RestartCalled.Add(1)
	return m.RestartErr
}

func (m *MockServiceController) StopWatchedServices(ctx context.Context) error {
	m.StopWatchedCalled.Add(1)
	return m.StopErr
}

func (m *MockServiceController) RestartWatchedServices(ctx context.Context) error {
	m.RestartWatchedCalled.Add(1)
	return m.RestartErr
}

func (m *MockServiceController) ClearAllLogs(ctx context.Context) error {
	return nil
}

func newTestBus() *events.MemoryEventBus {
	return events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: 100,
		HistoryMaxAge:    time.Hour,
	})
}

func TestRunner_New(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{ID: "test", Name: "Run Tests", Command: []string{"echo", "test"}},
	}

	runner := NewRunner(workflows, bus, nil, "")
	require.NotNil(t, runner)
}

func TestRunner_List(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{ID: "test", Name: "Run Tests", Command: []string{"echo", "test"}},
		{ID: "build", Name: "Build All", Command: []string{"echo", "build"}},
	}

	runner := NewRunner(workflows, bus, nil, "")
	list := runner.List()

	// Should include user workflows + built-in workflows
	assert.GreaterOrEqual(t, len(list), 2)

	// Verify user workflows are present
	foundTest, foundBuild := false, false
	for _, wf := range list {
		if wf.ID == "test" {
			foundTest = true
		}
		if wf.ID == "build" {
			foundBuild = true
		}
	}
	assert.True(t, foundTest, "test workflow should be in list")
	assert.True(t, foundBuild, "build workflow should be in list")
}

func TestRunner_Get(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{ID: "test", Name: "Run Tests", Command: []string{"echo", "test"}},
	}

	runner := NewRunner(workflows, bus, nil, "")

	wf, ok := runner.Get("test")
	assert.True(t, ok)
	assert.Equal(t, "Run Tests", wf.Name)

	_, ok = runner.Get("nonexistent")
	assert.False(t, ok)
}

func TestRunner_Run_Success(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{ID: "echo", Name: "Echo Test", Command: []string{"echo", "hello"}},
	}

	runner := NewRunner(workflows, bus, nil, "")
	initialStatus, err := runner.Run(context.Background(), "echo")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.Equal(t, StateSuccess, status.State)
	assert.True(t, status.Success)
	assert.Equal(t, 0, status.ExitCode)
	assert.Contains(t, status.Output, "hello")
}

func TestRunner_Run_Failure(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{ID: "fail", Name: "Fail Test", Command: []string{"sh", "-c", "exit 1"}},
	}

	runner := NewRunner(workflows, bus, nil, "")
	initialStatus, err := runner.Run(context.Background(), "fail")

	require.NoError(t, err) // Run itself doesn't error, the workflow does
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.Equal(t, StateFailed, status.State)
	assert.False(t, status.Success)
	assert.Equal(t, 1, status.ExitCode)
}

func TestRunner_Run_NotFound(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{}

	runner := NewRunner(workflows, bus, nil, "")
	_, err := runner.Run(context.Background(), "nonexistent")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRunner_Run_Timeout(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{
			ID:      "slow",
			Name:    "Slow Test",
			Command: []string{"sleep", "10"},
			Timeout: 100 * time.Millisecond,
		},
	}

	runner := NewRunner(workflows, bus, nil, "")
	initialStatus, err := runner.Run(context.Background(), "slow")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.Equal(t, StateFailed, status.State)
	assert.Contains(t, status.Error, "timeout")
}

func TestRunner_Run_WithRequiresStopped(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	svc := &MockServiceController{}

	workflows := []WorkflowConfig{
		{
			ID:              "db-reset",
			Name:            "Reset DB",
			Command:         []string{"echo", "reset"},
			RequiresStopped: []string{"api", "worker"},
		},
	}

	runner := NewRunner(workflows, bus, svc, "")
	initialStatus, err := runner.Run(context.Background(), "db-reset")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.Equal(t, int32(1), svc.StopCalled.Load())
	assert.ElementsMatch(t, []string{"api", "worker"}, svc.StoppedServices)
}

func TestRunner_Run_WithRestartServices(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	svc := &MockServiceController{}

	workflows := []WorkflowConfig{
		{
			ID:              "build",
			Name:            "Build",
			Command:         []string{"echo", "build"},
			RestartServices: true,
		},
	}

	runner := NewRunner(workflows, bus, svc, "")
	initialStatus, err := runner.Run(context.Background(), "build")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.True(t, status.Success)
	// restart_services now calls RestartWatchedServices (only watched services)
	assert.Equal(t, int32(1), svc.RestartWatchedCalled.Load())
}

func TestRunner_Run_RestartOnlyOnSuccess(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	svc := &MockServiceController{}

	workflows := []WorkflowConfig{
		{
			ID:              "fail-build",
			Name:            "Fail Build",
			Command:         []string{"sh", "-c", "exit 1"},
			RestartServices: true,
		},
	}

	runner := NewRunner(workflows, bus, svc, "")
	initialStatus, err := runner.Run(context.Background(), "fail-build")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.False(t, status.Success)
	// Services should not be restarted on failure
	assert.Equal(t, int32(0), svc.RestartWatchedCalled.Load())
}

func TestRunner_Run_EmitsEvents(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	var startedReceived, finishedReceived atomic.Bool

	bus.Subscribe("workflow.started", func(ctx context.Context, e events.Event) error {
		startedReceived.Store(true)
		return nil
	})

	bus.Subscribe("workflow.finished", func(ctx context.Context, e events.Event) error {
		finishedReceived.Store(true)
		return nil
	})

	workflows := []WorkflowConfig{
		{ID: "test", Name: "Test", Command: []string{"echo", "test"}},
	}

	runner := NewRunner(workflows, bus, nil, "")
	initialStatus, err := runner.Run(context.Background(), "test")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)
	time.Sleep(100 * time.Millisecond) // Extra time for events to propagate

	assert.True(t, startedReceived.Load())
	assert.True(t, finishedReceived.Load())
}

func TestRunner_Run_WithParser(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{
			ID:           "compile",
			Name:         "Compile",
			Command:      []string{"sh", "-c", "echo './main.go:10:5: undefined: foo'"},
			OutputParser: "go",
		},
	}

	runner := NewRunner(workflows, bus, nil, "")
	initialStatus, err := runner.Run(context.Background(), "compile")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	require.Len(t, status.ParsedLines, 1)
	assert.Equal(t, "./main.go", status.ParsedLines[0].File)
	assert.Equal(t, 10, status.ParsedLines[0].Line)
}

func TestRunner_Run_WorkingDir(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	tmpDir := t.TempDir()

	workflows := []WorkflowConfig{
		{ID: "pwd", Name: "PWD", Command: []string{"pwd"}},
	}

	runner := NewRunner(workflows, bus, nil, tmpDir)
	initialStatus, err := runner.Run(context.Background(), "pwd")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.Contains(t, status.Output, tmpDir)
}

func TestRunner_Run_OverrideWorkingDir(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	tmpDir := t.TempDir()

	workflows := []WorkflowConfig{
		{ID: "pwd", Name: "PWD", Command: []string{"pwd"}},
	}

	runner := NewRunner(workflows, bus, nil, "/somewhere/else")
	initialStatus, err := runner.RunWithOptions(context.Background(), "pwd", RunOptions{
		WorkingDir: tmpDir,
	})

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.Contains(t, status.Output, tmpDir)
}

func TestRunner_Status(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{ID: "test", Name: "Test", Command: []string{"sleep", "1"}},
	}

	runner := NewRunner(workflows, bus, nil, "")

	// Start a workflow (runs async)
	initialStatus, err := runner.Run(context.Background(), "test")
	require.NoError(t, err)

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Check status while running
	status, ok := runner.Status(initialStatus.ID)
	assert.True(t, ok)
	assert.Equal(t, StateRunning, status.State)

	// Wait for completion
	waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)
}

func TestRunner_Cancel(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{ID: "slow", Name: "Slow", Command: []string{"sleep", "10"}},
	}

	runner := NewRunner(workflows, bus, nil, "")

	// Start a workflow (runs async)
	initialStatus, err := runner.Run(context.Background(), "slow")
	require.NoError(t, err)

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Cancel it
	err = runner.Cancel(initialStatus.ID)
	assert.NoError(t, err)

	// Wait for it to finish (should be canceled)
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)
	assert.Equal(t, StateCanceled, status.State)
}

func TestRunner_BuiltinWorkflows(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	svc := &MockServiceController{}

	// Test _restart_all
	workflows := []WorkflowConfig{}
	runner := NewRunner(workflows, bus, svc, "")

	// Built-in workflows should be available
	wf, ok := runner.Get("_restart_all")
	assert.True(t, ok)
	assert.Equal(t, "Restart All Services", wf.Name)

	wf, ok = runner.Get("_stop_all")
	assert.True(t, ok)
	assert.Equal(t, "Stop All Services", wf.Name)
}

func TestRunner_Run_BuiltinRestartAll(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	svc := &MockServiceController{}

	workflows := []WorkflowConfig{}
	runner := NewRunner(workflows, bus, svc, "")

	status, err := runner.Run(context.Background(), "_restart_all")
	require.NoError(t, err)
	assert.True(t, status.Success)
	assert.Equal(t, int32(1), svc.RestartCalled.Load())
}

func TestRunner_Run_MultipleCommands_Success(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{
			ID:   "multi",
			Name: "Multi Command",
			Commands: [][]string{
				{"echo", "first"},
				{"echo", "second"},
				{"echo", "third"},
			},
		},
	}

	runner := NewRunner(workflows, bus, nil, "")
	initialStatus, err := runner.Run(context.Background(), "multi")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.Equal(t, StateSuccess, status.State)
	assert.True(t, status.Success)
	assert.Equal(t, 0, status.ExitCode)
	// Check output contains all commands
	assert.Contains(t, status.Output, "first")
	assert.Contains(t, status.Output, "second")
	assert.Contains(t, status.Output, "third")
	// Check headers are present
	assert.Contains(t, status.Output, "=== Command 1/3:")
	assert.Contains(t, status.Output, "=== Command 2/3:")
	assert.Contains(t, status.Output, "=== Command 3/3:")
}

func TestRunner_Run_MultipleCommands_FailureStopsSequence(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{
			ID:   "fail-mid",
			Name: "Fail in Middle",
			Commands: [][]string{
				{"echo", "first"},
				{"sh", "-c", "exit 1"},
				{"echo", "should-not-run"},
			},
		},
	}

	runner := NewRunner(workflows, bus, nil, "")
	initialStatus, err := runner.Run(context.Background(), "fail-mid")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.Equal(t, StateFailed, status.State)
	assert.False(t, status.Success)
	assert.Equal(t, 1, status.ExitCode)
	// First command should have run
	assert.Contains(t, status.Output, "first")
	// Third command should NOT have run
	assert.NotContains(t, status.Output, "should-not-run")
	// Error should mention command 2
	assert.Contains(t, status.Error, "command 2")
}

func TestRunner_Run_MultipleCommands_BackwardsCompat(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	// Using single Command field should still work
	workflows := []WorkflowConfig{
		{
			ID:      "single",
			Name:    "Single Command",
			Command: []string{"echo", "legacy"},
		},
	}

	runner := NewRunner(workflows, bus, nil, "")
	initialStatus, err := runner.Run(context.Background(), "single")

	require.NoError(t, err)
	require.NotEmpty(t, initialStatus.ID)

	// Wait for completion
	status := waitForCompletion(t, runner, initialStatus.ID, 5*time.Second)

	assert.Equal(t, StateSuccess, status.State)
	assert.True(t, status.Success)
	assert.Contains(t, status.Output, "legacy")
	// No command headers for single command
	assert.NotContains(t, status.Output, "=== Command")
}

// TestRunner_MultipleSubscribers_Completion tests that all subscribers receive
// completion notifications even under backpressure (slow subscribers).
func TestRunner_MultipleSubscribers_Completion(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	workflows := []WorkflowConfig{
		{
			ID:       "test",
			Name:     "Test Workflow",
			Commands: [][]string{{"echo", "hello"}},
		},
	}

	runner := NewRunner(workflows, bus, nil, "")

	// Start the workflow first
	initialStatus, err := runner.Run(context.Background(), "test")
	require.NoError(t, err)
	runID := initialStatus.ID

	// Create multiple subscribers with small buffers to simulate backpressure
	const numSubscribers = 5
	channels := make([]chan OutputUpdate, numSubscribers)
	completions := make([]chan bool, numSubscribers)

	for i := 0; i < numSubscribers; i++ {
		ch := make(chan OutputUpdate, 1) // Small buffer to test backpressure
		err := runner.Subscribe(runID, ch)
		require.NoError(t, err)
		channels[i] = ch
		completions[i] = make(chan bool, 1)

		// Each subscriber goroutine waits for Done=true
		go func(idx int, ch chan OutputUpdate) {
			for update := range ch {
				if update.Done {
					completions[idx] <- true
					return
				}
			}
			// Channel closed without Done - still counts as completion
			completions[idx] <- true
		}(i, ch)
	}

	// Wait for all subscribers to receive completion
	for i := 0; i < numSubscribers; i++ {
		select {
		case <-completions[i]:
			// Good - subscriber received completion
		case <-time.After(10 * time.Second):
			t.Errorf("Subscriber %d did not receive completion notification", i)
		}
	}

	// Cleanup
	for i := 0; i < numSubscribers; i++ {
		runner.Unsubscribe(runID, channels[i])
	}
}

func TestRunner_Run_WithInputs(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	// Create workflow with template placeholders
	workflows := []WorkflowConfig{
		{
			ID:       "deploy",
			Name:     "Deploy",
			Commands: [][]string{{"echo", "Deploying to {{ .Inputs.environment }} version {{ .Inputs.version }}"}},
			Inputs: []WorkflowInput{
				{Name: "environment", Type: "select", Options: []string{"staging", "production"}},
				{Name: "version", Type: "text"},
			},
		},
	}

	runner := NewRunner(workflows, bus, nil, "")

	// Run with inputs
	opts := RunOptions{
		Inputs: map[string]any{
			"environment": "production",
			"version":     "v1.2.3",
		},
	}

	status, err := runner.RunWithOptions(context.Background(), "deploy", opts)
	require.NoError(t, err)
	assert.Equal(t, "Deploy", status.Name)

	// Wait for completion
	time.Sleep(100 * time.Millisecond)

	finalStatus, ok := runner.Status(status.ID)
	require.True(t, ok)
	assert.Equal(t, StateSuccess, finalStatus.State)
	assert.Contains(t, finalStatus.Output, "Deploying to production version v1.2.3")
}

func TestRunner_Run_WithInputs_Conditional(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	// Create workflow with conditional template
	workflows := []WorkflowConfig{
		{
			ID:       "build",
			Name:     "Build",
			Commands: [][]string{{"echo", "Building{{ if .Inputs.dry_run }} (dry run){{ end }}"}},
			Inputs: []WorkflowInput{
				{Name: "dry_run", Type: "checkbox", Default: false},
			},
		},
	}

	runner := NewRunner(workflows, bus, nil, "")

	// Test with dry_run=true
	opts := RunOptions{
		Inputs: map[string]any{
			"dry_run": true,
		},
	}

	status, err := runner.RunWithOptions(context.Background(), "build", opts)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	finalStatus, ok := runner.Status(status.ID)
	require.True(t, ok)
	assert.Equal(t, StateSuccess, finalStatus.State)
	assert.Contains(t, finalStatus.Output, "Building (dry run)")

	// Test with dry_run=false
	opts2 := RunOptions{
		Inputs: map[string]any{
			"dry_run": false,
		},
	}

	status2, err := runner.RunWithOptions(context.Background(), "build", opts2)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	finalStatus2, ok := runner.Status(status2.ID)
	require.True(t, ok)
	assert.Equal(t, StateSuccess, finalStatus2.State)
	assert.Contains(t, finalStatus2.Output, "Building\n")
	assert.NotContains(t, finalStatus2.Output, "dry run")
}

func TestRunner_Run_WithInputValidation(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	// Create workflow with validation constraints
	workflows := []WorkflowConfig{
		{
			ID:       "fetch",
			Name:     "Fetch Data",
			Commands: [][]string{{"echo", "Fetching {{ .Inputs.table }} {{ .Inputs.id }}"}},
			Inputs: []WorkflowInput{
				{Name: "table", Type: "text", AllowedValues: []string{"users", "groups", "messages"}},
				{Name: "id", Type: "text", Pattern: `^[0-9]+$`, Required: true},
			},
		},
	}

	runner := NewRunner(workflows, bus, nil, "")

	// Valid inputs should work
	opts := RunOptions{
		Inputs: map[string]any{
			"table": "users",
			"id":    "12345",
		},
	}

	status, err := runner.RunWithOptions(context.Background(), "fetch", opts)
	require.NoError(t, err)
	assert.Equal(t, "Fetch Data", status.Name)

	time.Sleep(100 * time.Millisecond)
	finalStatus, ok := runner.Status(status.ID)
	require.True(t, ok)
	assert.Equal(t, StateSuccess, finalStatus.State)
	assert.Contains(t, finalStatus.Output, "Fetching users 12345")

	// Invalid table should fail
	opts2 := RunOptions{
		Inputs: map[string]any{
			"table": "secrets",
			"id":    "12345",
		},
	}
	_, err = runner.RunWithOptions(context.Background(), "fetch", opts2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")

	// Invalid ID pattern should fail
	opts3 := RunOptions{
		Inputs: map[string]any{
			"table": "users",
			"id":    "abc; rm -rf /",
		},
	}
	_, err = runner.RunWithOptions(context.Background(), "fetch", opts3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match pattern")

	// Missing required ID should fail
	opts4 := RunOptions{
		Inputs: map[string]any{
			"table": "users",
		},
	}
	_, err = runner.RunWithOptions(context.Background(), "fetch", opts4)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestExpandCommandsWithInputs(t *testing.T) {
	tests := []struct {
		name     string
		commands [][]string
		inputs   map[string]any
		expected [][]string
	}{
		{
			name:     "no inputs",
			commands: [][]string{{"echo", "hello"}},
			inputs:   nil,
			expected: [][]string{{"echo", "hello"}},
		},
		{
			name:     "simple substitution",
			commands: [][]string{{"echo", "{{ .Inputs.name }}"}},
			inputs:   map[string]any{"name": "world"},
			expected: [][]string{{"echo", "world"}},
		},
		{
			name:     "multiple inputs",
			commands: [][]string{{"deploy.sh", "--env={{ .Inputs.env }}", "--version={{ .Inputs.version }}"}},
			inputs:   map[string]any{"env": "prod", "version": "v1.0"},
			expected: [][]string{{"deploy.sh", "--env=prod", "--version=v1.0"}},
		},
		{
			name:     "conditional",
			commands: [][]string{{"echo", "{{ if .Inputs.verbose }}--verbose{{ end }}"}},
			inputs:   map[string]any{"verbose": true},
			expected: [][]string{{"echo", "--verbose"}},
		},
		{
			name:     "conditional false",
			commands: [][]string{{"echo", "{{ if .Inputs.verbose }}--verbose{{ end }}"}},
			inputs:   map[string]any{"verbose": false},
			expected: [][]string{{"echo", ""}},
		},
		{
			name:     "no template markers",
			commands: [][]string{{"echo", "plain text"}},
			inputs:   map[string]any{"unused": "value"},
			expected: [][]string{{"echo", "plain text"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := expandCommandsWithInputs(tt.commands, tt.inputs)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExpandConfirmMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		inputs   map[string]any
		expected string
	}{
		{
			name:     "empty message",
			message:  "",
			inputs:   map[string]any{"env": "prod"},
			expected: "",
		},
		{
			name:     "no inputs",
			message:  "Deploy to {{ .Inputs.env }}?",
			inputs:   nil,
			expected: "Deploy to {{ .Inputs.env }}?",
		},
		{
			name:     "simple substitution",
			message:  "Deploy {{ .Inputs.version }} to {{ .Inputs.env }}?",
			inputs:   map[string]any{"env": "production", "version": "v1.2.3"},
			expected: "Deploy v1.2.3 to production?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandConfirmMessage(tt.message, tt.inputs)
			assert.Equal(t, tt.expected, result)
		})
	}
}
