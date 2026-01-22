// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
)

// HookResult contains the result of a lifecycle hook execution.
type HookResult struct {
	Name     string
	Success  bool
	Duration time.Duration
	Output   string
	Error    error
}

// LifecycleRunner executes lifecycle hooks.
type LifecycleRunner struct {
	bus     events.EventBus
	workdir string
}

// NewLifecycleRunner creates a new lifecycle runner.
func NewLifecycleRunner(bus events.EventBus, workdir string) *LifecycleRunner {
	return &LifecycleRunner{
		bus:     bus,
		workdir: workdir,
	}
}

// RunOnCreate runs all on_create hooks.
func (r *LifecycleRunner) RunOnCreate(ctx context.Context, wt *WorktreeInfo, hooks []config.HookConfig) ([]HookResult, error) {
	return r.runHooks(ctx, wt, "on_create", hooks)
}

// RunPreActivate runs all pre_activate hooks.
func (r *LifecycleRunner) RunPreActivate(ctx context.Context, wt *WorktreeInfo, hooks []config.HookConfig) ([]HookResult, error) {
	return r.runHooks(ctx, wt, "pre_activate", hooks)
}

// runHooks executes a list of hooks in sequence.
func (r *LifecycleRunner) runHooks(ctx context.Context, wt *WorktreeInfo, phase string, hooks []config.HookConfig) ([]HookResult, error) {
	results := make([]HookResult, 0, len(hooks))

	for _, hook := range hooks {
		result := r.runHook(ctx, wt, phase, hook)
		results = append(results, result)

		if !result.Success {
			return results, fmt.Errorf("hook %q failed: %w", hook.Name, result.Error)
		}
	}

	return results, nil
}

// runHook executes a single hook.
func (r *LifecycleRunner) runHook(ctx context.Context, wt *WorktreeInfo, phase string, hook config.HookConfig) HookResult {
	start := time.Now()

	// Determine timeout
	timeout := 5 * time.Minute // default
	if hook.Timeout != "" {
		if d, err := time.ParseDuration(hook.Timeout); err == nil {
			timeout = d
		}
	}

	// Create timeout context
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Emit hook started event
	if r.bus != nil {
		r.bus.Publish(ctx, events.Event{
			Type:     "worktree.hook.started",
			Worktree: wt.Name(),
			Payload: map[string]interface{}{
				"worktree":  wt.Name(),
				"hook_name": hook.Name,
				"phase":     phase,
				"command":   hook.Command,
			},
		})
	}

	// Build command
	if len(hook.Command) == 0 {
		return HookResult{
			Name:     hook.Name,
			Success:  false,
			Duration: time.Since(start),
			Error:    fmt.Errorf("empty command"),
		}
	}

	cmd := exec.CommandContext(hookCtx, hook.Command[0], hook.Command[1:]...)

	// Set working directory to worktree path
	cmd.Dir = wt.Path

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run command
	err := cmd.Run()
	duration := time.Since(start)

	// Combine output
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	result := HookResult{
		Name:     hook.Name,
		Success:  err == nil,
		Duration: duration,
		Output:   output,
		Error:    err,
	}

	// Emit hook finished event
	if r.bus != nil {
		payload := map[string]interface{}{
			"worktree":  wt.Name(),
			"hook_name": hook.Name,
			"phase":     phase,
			"success":   result.Success,
			"duration":  result.Duration.String(),
		}
		if output != "" {
			payload["output"] = output
		}
		if err != nil {
			payload["error"] = err.Error()
		}

		r.bus.Publish(ctx, events.Event{
			Type:     "worktree.hook.finished",
			Worktree: wt.Name(),
			Payload:  payload,
		})
	}

	return result
}
