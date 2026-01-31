// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package workflow

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/wingedpig/trellis/internal/events"
)

// Built-in workflow IDs
const (
	BuiltinStartAll    = "_start_all"
	BuiltinRestartAll  = "_restart_all"
	BuiltinStopAll     = "_stop_all"
	BuiltinStopWatched = "_stop_watched"
	BuiltinClearLogs   = "_clear_logs"
)

// How long to keep completed run states for polling
const completedRunTTL = 60 * time.Second

// RealRunner implements the Runner interface.
type RealRunner struct {
	mu          sync.RWMutex
	workflows   map[string]WorkflowConfig
	bus         events.EventBus
	svc         ServiceController
	parsers     *ParserRegistry
	workingDir  string
	currentRuns map[string]*runState
	cancelFuncs map[string]context.CancelFunc
	done        chan struct{} // signals shutdown to background goroutines
}

type runState struct {
	status      *WorkflowStatus
	mu          sync.RWMutex
	completed   bool
	expiresAt   time.Time
	subscribers map[chan<- OutputUpdate]struct{}
	subMu       sync.RWMutex
}

// NewRunner creates a new workflow runner.
func NewRunner(workflows []WorkflowConfig, bus events.EventBus, svc ServiceController, workingDir string) Runner {
	r := &RealRunner{
		workflows:   make(map[string]WorkflowConfig),
		bus:         bus,
		svc:         svc,
		parsers:     NewParserRegistry(),
		workingDir:  workingDir,
		currentRuns: make(map[string]*runState),
		cancelFuncs: make(map[string]context.CancelFunc),
		done:        make(chan struct{}),
	}

	// Register user workflows
	for _, wf := range workflows {
		r.workflows[wf.ID] = wf
	}

	// Register built-in workflows
	r.registerBuiltins()

	// Start cleanup goroutine
	go r.cleanupExpiredRuns()

	return r
}

func (r *RealRunner) cleanupExpiredRuns() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			r.mu.Lock()
			now := time.Now()
			for runID, state := range r.currentRuns {
				if state.completed && now.After(state.expiresAt) {
					delete(r.currentRuns, runID)
					delete(r.cancelFuncs, runID)
				}
			}
			r.mu.Unlock()
		}
	}
}

// Close shuts down the runner, cancelling all running workflows and stopping background goroutines.
func (r *RealRunner) Close() error {
	// Signal cleanup goroutine to stop
	close(r.done)

	// Cancel all running workflows
	r.mu.Lock()
	for runID, cancel := range r.cancelFuncs {
		cancel()
		delete(r.cancelFuncs, runID)
	}
	r.mu.Unlock()

	return nil
}

// UpdateConfig updates the workflow configs and working directory.
// Called when worktree is activated to use new paths.
func (r *RealRunner) UpdateConfig(workflows []WorkflowConfig, workingDir string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Update working directory
	r.workingDir = workingDir

	// Clear existing user workflows (keep builtins)
	for id := range r.workflows {
		if !strings.HasPrefix(id, "_") {
			delete(r.workflows, id)
		}
	}

	// Add updated workflows
	for _, wf := range workflows {
		r.workflows[wf.ID] = wf
	}
}

func (r *RealRunner) registerBuiltins() {
	r.workflows[BuiltinStartAll] = WorkflowConfig{
		ID:   BuiltinStartAll,
		Name: "Start All Services",
	}
	r.workflows[BuiltinRestartAll] = WorkflowConfig{
		ID:   BuiltinRestartAll,
		Name: "Restart All Services",
	}
	r.workflows[BuiltinStopAll] = WorkflowConfig{
		ID:   BuiltinStopAll,
		Name: "Stop All Services",
	}
	r.workflows[BuiltinStopWatched] = WorkflowConfig{
		ID:   BuiltinStopWatched,
		Name: "Stop Watched Services",
	}
	r.workflows[BuiltinClearLogs] = WorkflowConfig{
		ID:   BuiltinClearLogs,
		Name: "Clear Logs",
	}
}

// List returns all configured workflows.
func (r *RealRunner) List() []WorkflowConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]WorkflowConfig, 0, len(r.workflows))
	for _, wf := range r.workflows {
		result = append(result, wf)
	}
	return result
}

// Get returns a workflow by ID.
func (r *RealRunner) Get(id string) (WorkflowConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	wf, ok := r.workflows[id]
	return wf, ok
}

// Run executes a workflow by ID.
func (r *RealRunner) Run(ctx context.Context, id string) (*WorkflowStatus, error) {
	return r.RunWithOptions(ctx, id, RunOptions{})
}

// RunWithOptions executes a workflow with additional options.
// For regular workflows, this starts execution asynchronously and returns immediately.
// The caller should poll Status() to get updates.
func (r *RealRunner) RunWithOptions(ctx context.Context, id string, opts RunOptions) (*WorkflowStatus, error) {
	wf, ok := r.Get(id)
	if !ok {
		return nil, fmt.Errorf("workflow %q not found", id)
	}

	// Validate inputs against workflow constraints
	if len(wf.Inputs) > 0 && opts.Inputs != nil {
		if err := ValidateInputs(wf.Inputs, opts.Inputs); err != nil {
			return nil, err
		}
	}

	// Handle built-in workflows synchronously (they're fast)
	if strings.HasPrefix(id, "_") {
		return r.runBuiltin(ctx, wf)
	}

	// Create run state
	runID := fmt.Sprintf("%s-%d", id, time.Now().UnixNano())
	status := &WorkflowStatus{
		ID:        runID,
		Name:      wf.Name,
		State:     StateRunning,
		StartedAt: time.Now(),
	}

	state := &runState{
		status:      status,
		subscribers: make(map[chan<- OutputUpdate]struct{}),
	}
	// Use background context so workflow survives HTTP request, but propagate caller cancellation
	runCtx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()
	r.currentRuns[runID] = state
	r.cancelFuncs[runID] = cancel
	r.mu.Unlock()

	// Propagate caller context cancellation to the run context
	// This allows CLI/in-process callers to cancel via their context
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-runCtx.Done():
			// runCtx was cancelled directly (via Cancel API)
		}
	}()

	// Emit started event
	r.emitEvent(ctx, "workflow.started", map[string]interface{}{
		"workflow_id": runID,
		"name":        wf.Name,
	})

	// Run asynchronously
	go func() {
		defer func() {
			// Cancel runCtx to ensure the context propagation goroutine exits
			cancel()

			// Mark as completed but keep around for polling
			r.mu.Lock()
			state.completed = true
			state.expiresAt = time.Now().Add(completedRunTTL)
			delete(r.cancelFuncs, runID)
			r.mu.Unlock()
		}()

		// Stop required services
		if len(wf.RequiresStopped) > 0 && r.svc != nil {
			if err := r.svc.StopServices(runCtx, wf.RequiresStopped); err != nil {
				state.mu.Lock()
				status.State = StateFailed
				status.Error = fmt.Sprintf("failed to stop services: %v", err)
				status.FinishedAt = time.Now()
				status.Duration = status.FinishedAt.Sub(status.StartedAt)
				statusCopy := *status
				state.mu.Unlock()
				r.emitFinished(runCtx, &statusCopy, wf)
				state.notifyComplete(runID, &statusCopy)
				return
			}
		}

		// Execute the workflow with streaming output
		r.executeStreaming(runCtx, wf, state, opts)

		// Restart services if configured and successful
		state.mu.RLock()
		success := status.Success
		state.mu.RUnlock()

		if wf.RestartServices && success && r.svc != nil {
			// Only restart services that have watching enabled (not databases, etc.)
			// Use background context so services outlive the workflow context (which gets
			// cancelled in the defer above)
			if err := r.svc.RestartWatchedServices(context.Background()); err != nil {
				state.mu.Lock()
				status.Error = fmt.Sprintf("workflow succeeded but service restart failed: %v", err)
				state.mu.Unlock()
			}
		}

		// Emit finished event and notify subscribers
		state.mu.RLock()
		statusCopy := *status
		state.mu.RUnlock()
		r.emitFinished(runCtx, &statusCopy, wf)
		state.notifyComplete(runID, &statusCopy)
	}()

	// Return immediately with the initial status
	return status, nil
}

func (r *RealRunner) executeStreaming(ctx context.Context, wf WorkflowConfig, state *runState, opts RunOptions) {
	status := state.status

	// Apply timeout if configured
	execCtx := ctx
	var cancelTimeout context.CancelFunc
	if wf.Timeout > 0 {
		execCtx, cancelTimeout = context.WithTimeout(ctx, wf.Timeout)
		defer cancelTimeout()
	}

	// Get commands to execute
	commands := wf.GetCommands()
	if len(commands) == 0 {
		state.mu.Lock()
		status.State = StateFailed
		status.Error = "no command specified"
		status.FinishedAt = time.Now()
		status.Duration = status.FinishedAt.Sub(status.StartedAt)
		state.mu.Unlock()
		return
	}

	// Expand command templates with inputs
	if len(opts.Inputs) > 0 {
		var err error
		commands, err = expandCommandsWithInputs(commands, opts.Inputs)
		if err != nil {
			state.mu.Lock()
			status.State = StateFailed
			status.Error = fmt.Sprintf("failed to expand inputs: %v", err)
			status.FinishedAt = time.Now()
			status.Duration = status.FinishedAt.Sub(status.StartedAt)
			state.mu.Unlock()
			return
		}
	}

	// Set working directory
	workDir := r.workingDir
	if opts.WorkingDir != "" {
		workDir = opts.WorkingDir
	}

	// Output tracking across all commands
	const maxOutputSize = 10 * 1024 * 1024 // 10MB limit
	var outputBuilder strings.Builder
	outputTruncated := false

	// Execute each command in sequence
	for cmdIdx, cmdArgs := range commands {
		// Check for context cancellation before starting next command
		if execCtx.Err() != nil {
			state.mu.Lock()
			if execCtx.Err() == context.Canceled {
				status.State = StateCanceled
				status.Error = "canceled"
			} else {
				status.State = StateFailed
				status.Error = "timeout exceeded"
			}
			status.Success = false
			status.FinishedAt = time.Now()
			status.Duration = status.FinishedAt.Sub(status.StartedAt)
			state.mu.Unlock()
			return
		}

		// Add command header for multi-command workflows
		if len(commands) > 1 {
			header := fmt.Sprintf("=== Command %d/%d: %s ===\n", cmdIdx+1, len(commands), strings.Join(cmdArgs, " "))
			state.mu.Lock()
			if !outputTruncated {
				outputBuilder.WriteString(header)
				status.Output = outputBuilder.String()
				status.OutputHTML = FormatOutput(status.Output, nil, wf.OutputParser)
			}
			state.mu.Unlock()
		}

		cmd := exec.CommandContext(execCtx, cmdArgs[0], cmdArgs[1:]...)
		if workDir != "" {
			cmd.Dir = workDir
		}

		// Apply environment variables from options
		if len(opts.Env) > 0 {
			cmd.Env = os.Environ()
			for k, v := range opts.Env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}

		// Create pipes for streaming output
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			state.mu.Lock()
			status.State = StateFailed
			status.Error = fmt.Sprintf("failed to create stdout pipe: %v", err)
			status.FinishedAt = time.Now()
			status.Duration = status.FinishedAt.Sub(status.StartedAt)
			state.mu.Unlock()
			return
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			state.mu.Lock()
			status.State = StateFailed
			status.Error = fmt.Sprintf("failed to create stderr pipe: %v", err)
			status.FinishedAt = time.Now()
			status.Duration = status.FinishedAt.Sub(status.StartedAt)
			state.mu.Unlock()
			return
		}

		// Start the command
		if err := cmd.Start(); err != nil {
			state.mu.Lock()
			status.State = StateFailed
			status.Error = fmt.Sprintf("failed to start command: %v", err)
			status.FinishedAt = time.Now()
			status.Duration = status.FinishedAt.Sub(status.StartedAt)
			state.mu.Unlock()
			return
		}

		// Stream output from both stdout and stderr
		var wg sync.WaitGroup
		wg.Add(2)

		// Throttle HTML formatting to avoid O(n²) on large outputs
		var lastFormatTime time.Time
		const formatThrottle = 100 * time.Millisecond

		streamOutput := func(reader io.Reader) {
			defer wg.Done()
			scanner := bufio.NewScanner(reader)
			buf := make([]byte, 64*1024)
			scanner.Buffer(buf, 1024*1024)

			for scanner.Scan() {
				line := scanner.Text() + "\n"
				state.mu.Lock()
				if !outputTruncated {
					if outputBuilder.Len()+len(line) > maxOutputSize {
						outputBuilder.WriteString("\n... output truncated (exceeded 10MB) ...\n")
						outputTruncated = true
						status.Output = outputBuilder.String()
						// Update HTML immediately when truncation occurs
						status.OutputHTML = FormatOutput(status.Output, nil, wf.OutputParser)
						lastFormatTime = time.Now()
					} else {
						outputBuilder.WriteString(line)
						status.Output = outputBuilder.String()
						// Throttle HTML formatting to avoid O(n²) for large outputs
						now := time.Now()
						if now.Sub(lastFormatTime) >= formatThrottle {
							status.OutputHTML = FormatOutput(status.Output, nil, wf.OutputParser)
							lastFormatTime = now
						}
					}
				}
				runID := status.ID
				state.mu.Unlock()

				// Notify subscribers of new output (skip for go_test_json as raw JSON is not useful)
				if wf.OutputParser != "go_test_json" {
					state.notifySubscribers(runID, line)
				}
			}

			// If scanner stopped due to error (e.g., line too long), drain to prevent deadlock
			if scanner.Err() != nil {
				state.mu.Lock()
				if !outputTruncated {
					outputBuilder.WriteString("\n... output line exceeded 1MB limit, some output discarded ...\n")
					outputTruncated = true
					status.Output = outputBuilder.String()
				}
				state.mu.Unlock()
				// Drain remaining output to prevent pipe deadlock
				io.Copy(io.Discard, reader)
			}
		}

		go streamOutput(stdout)
		go streamOutput(stderr)

		wg.Wait()
		err = cmd.Wait()

		// Check for failure - stop sequence on first error
		if err != nil {
			state.mu.Lock()
			status.FinishedAt = time.Now()
			status.Duration = status.FinishedAt.Sub(status.StartedAt)
			status.Success = false

			// Check if the error was due to context cancellation/timeout
			if execCtx.Err() != nil {
				if execCtx.Err() == context.Canceled {
					status.State = StateCanceled
					status.Error = "canceled"
				} else {
					status.State = StateFailed
					status.Error = "timeout exceeded"
				}
			} else if exitErr, ok := err.(*exec.ExitError); ok {
				status.State = StateFailed
				status.ExitCode = exitErr.ExitCode()
				status.Error = fmt.Sprintf("command %d exited with code %d", cmdIdx+1, exitErr.ExitCode())
			} else {
				status.State = StateFailed
				status.Error = fmt.Sprintf("command %d failed: %v", cmdIdx+1, err)
			}
			// Parse and format output even on failure
			if wf.OutputParser != "" {
				parser := r.parsers.Get(wf.OutputParser)
				status.ParsedLines = parser.Parse(status.Output)
			}
			status.OutputHTML = FormatOutput(status.Output, status.ParsedLines, wf.OutputParser)
			state.mu.Unlock()
			return
		}

		// Add newline between commands
		if len(commands) > 1 && cmdIdx < len(commands)-1 {
			state.mu.Lock()
			if !outputTruncated {
				outputBuilder.WriteString("\n")
				status.Output = outputBuilder.String()
				status.OutputHTML = FormatOutput(status.Output, nil, wf.OutputParser)
			}
			state.mu.Unlock()
		}
	}

	// All commands succeeded
	state.mu.Lock()
	status.FinishedAt = time.Now()
	status.Duration = status.FinishedAt.Sub(status.StartedAt)
	status.State = StateSuccess
	status.Success = true
	status.ExitCode = 0

	// Parse output if parser configured
	if wf.OutputParser != "" {
		parser := r.parsers.Get(wf.OutputParser)
		status.ParsedLines = parser.Parse(status.Output)
	}

	// Format output as HTML with links
	status.OutputHTML = FormatOutput(status.Output, status.ParsedLines, wf.OutputParser)
	state.mu.Unlock()
}

func (r *RealRunner) runBuiltin(ctx context.Context, wf WorkflowConfig) (*WorkflowStatus, error) {
	status := &WorkflowStatus{
		ID:        wf.ID,
		Name:      wf.Name,
		State:     StateRunning,
		StartedAt: time.Now(),
	}

	var err error

	switch wf.ID {
	case BuiltinStartAll:
		if r.svc != nil {
			err = r.svc.StartAllServices(ctx)
		}
	case BuiltinRestartAll:
		if r.svc != nil {
			err = r.svc.RestartAllServices(ctx)
		}
	case BuiltinStopAll:
		if r.svc != nil {
			err = r.svc.StopServices(ctx, nil) // nil means all
		}
	case BuiltinStopWatched:
		if r.svc != nil {
			err = r.svc.StopWatchedServices(ctx)
		}
	case BuiltinClearLogs:
		if r.svc != nil {
			err = r.svc.ClearAllLogs(ctx)
		}
	}

	status.FinishedAt = time.Now()
	status.Duration = status.FinishedAt.Sub(status.StartedAt)

	if err != nil {
		status.State = StateFailed
		status.Success = false
		status.Error = err.Error()
	} else {
		status.State = StateSuccess
		status.Success = true
	}

	r.emitFinished(ctx, status, wf)
	return status, nil
}

// Status returns the current status of a workflow run.
func (r *RealRunner) Status(runID string) (*WorkflowStatus, bool) {
	r.mu.RLock()
	state, ok := r.currentRuns[runID]
	r.mu.RUnlock()

	if !ok {
		return nil, false
	}

	state.mu.RLock()
	defer state.mu.RUnlock()

	// Return a copy
	statusCopy := *state.status
	return &statusCopy, true
}

// Cancel cancels a running workflow.
func (r *RealRunner) Cancel(runID string) error {
	r.mu.RLock()
	cancel, ok := r.cancelFuncs[runID]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("workflow run %q not found", runID)
	}

	cancel()
	return nil
}

func (r *RealRunner) emitEvent(ctx context.Context, eventType string, payload map[string]interface{}) {
	if r.bus == nil {
		return
	}

	r.bus.Publish(ctx, events.Event{
		Type:    eventType,
		Payload: payload,
	})
}

func (r *RealRunner) emitFinished(ctx context.Context, status *WorkflowStatus, wf WorkflowConfig) {
	payload := map[string]interface{}{
		"workflow_id": status.ID,
		"name":        wf.Name,
		"success":     status.Success,
		"duration":    status.Duration.String(),
		"output":      status.Output,
	}
	if status.Error != "" {
		payload["error"] = status.Error
	}
	// Add test counts for go_test_json parser
	if wf.OutputParser == "go_test_json" && len(status.ParsedLines) > 0 {
		var passed, failed, skipped int
		for _, line := range status.ParsedLines {
			switch line.Type {
			case "test_pass":
				passed++
			case "test_fail":
				failed++
			case "test_skip":
				skipped++
			}
		}
		payload["tests_passed"] = passed
		payload["tests_failed"] = failed
		payload["tests_skipped"] = skipped
		payload["tests_total"] = passed + failed + skipped
	}
	r.emitEvent(ctx, "workflow.finished", payload)
}

// Subscribe registers a channel to receive output updates for a workflow run.
func (r *RealRunner) Subscribe(runID string, ch chan<- OutputUpdate) error {
	r.mu.RLock()
	state, exists := r.currentRuns[runID]
	r.mu.RUnlock()

	if !exists {
		return fmt.Errorf("workflow run %s not found", runID)
	}

	state.subMu.Lock()
	state.subscribers[ch] = struct{}{}
	state.subMu.Unlock()

	// If already completed, send final status immediately
	state.mu.RLock()
	if state.completed {
		statusCopy := *state.status
		state.mu.RUnlock()
		select {
		case ch <- OutputUpdate{RunID: runID, Done: true, Status: &statusCopy}:
		default:
		}
	} else {
		state.mu.RUnlock()
	}

	return nil
}

// Unsubscribe removes a channel from receiving output updates.
func (r *RealRunner) Unsubscribe(runID string, ch chan<- OutputUpdate) {
	r.mu.RLock()
	state, exists := r.currentRuns[runID]
	r.mu.RUnlock()

	if !exists {
		return
	}

	state.subMu.Lock()
	delete(state.subscribers, ch)
	state.subMu.Unlock()
}

// notifySubscribers sends an output update to all subscribers.
func (state *runState) notifySubscribers(runID string, line string) {
	state.subMu.RLock()
	defer state.subMu.RUnlock()

	update := OutputUpdate{
		RunID: runID,
		Line:  line,
	}

	for ch := range state.subscribers {
		select {
		case ch <- update:
		default:
			// Channel full, skip this subscriber
		}
	}
}

// notifyComplete sends the final status to all subscribers.
// Uses a blocking send with timeout to ensure critical completion notifications are delivered.
func (state *runState) notifyComplete(runID string, status *WorkflowStatus) {
	state.subMu.RLock()
	defer state.subMu.RUnlock()

	update := OutputUpdate{
		RunID:  runID,
		Done:   true,
		Status: status,
	}

	// Use per-subscriber timeout - each subscriber gets their own 5 second window
	for ch := range state.subscribers {
		timer := time.NewTimer(5 * time.Second)
		select {
		case ch <- update:
			// Successfully sent
			timer.Stop()
		case <-timer.C:
			// Timeout waiting for this subscriber, continue with others
		}
	}
}

// inputTemplateData is the data structure passed to templates for input expansion.
type inputTemplateData struct {
	Inputs map[string]any
}

// expandCommandsWithInputs expands Go templates in command arguments using the provided inputs.
func expandCommandsWithInputs(commands [][]string, inputs map[string]any) ([][]string, error) {
	if len(inputs) == 0 {
		return commands, nil
	}

	data := inputTemplateData{Inputs: inputs}
	result := make([][]string, len(commands))

	for i, cmd := range commands {
		expandedCmd := make([]string, len(cmd))
		for j, arg := range cmd {
			expanded, err := expandTemplate(arg, data)
			if err != nil {
				return nil, fmt.Errorf("failed to expand template in command arg %q: %w", arg, err)
			}
			expandedCmd[j] = expanded
		}
		result[i] = expandedCmd
	}

	return result, nil
}

// expandTemplate expands a single template string with the provided data.
func expandTemplate(text string, data inputTemplateData) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}

	tmpl, err := template.New("").Parse(text)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// expandConfirmMessage expands the confirm message template with inputs.
func expandConfirmMessage(message string, inputs map[string]any) string {
	if message == "" || len(inputs) == 0 {
		return message
	}

	expanded, err := expandTemplate(message, inputTemplateData{Inputs: inputs})
	if err != nil {
		return message // Return original on error
	}
	return expanded
}
