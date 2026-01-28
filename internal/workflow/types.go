// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package workflow

import (
	"context"
	"time"
)

// WorkflowInput defines a parameter that prompts the user before execution.
type WorkflowInput struct {
	Name        string   // Variable name for templates
	Type        string   // "text", "select", "checkbox"
	Label       string   // Display label
	Placeholder string   // Placeholder for text inputs
	Options     []string // Options for select type
	Default     any      // Default value
	Required    bool     // Whether required
}

// WorkflowConfig defines a workflow from configuration.
type WorkflowConfig struct {
	ID              string
	Name            string
	Command         []string   // Single command (backwards compat)
	Commands        [][]string // Multiple commands to run in sequence
	Timeout         time.Duration
	OutputParser    string
	Confirm         bool
	ConfirmMessage  string
	RequiresStopped []string
	RestartServices bool
	Inputs          []WorkflowInput // Input parameters to prompt user for
}

// GetCommands returns the commands to execute, preferring Commands over Command.
func (w *WorkflowConfig) GetCommands() [][]string {
	if len(w.Commands) > 0 {
		return w.Commands
	}
	if len(w.Command) > 0 {
		return [][]string{w.Command}
	}
	return nil
}

// WorkflowStatus represents the current status of a workflow.
type WorkflowStatus struct {
	ID          string
	Name        string
	State       WorkflowState
	StartedAt   time.Time
	FinishedAt  time.Time
	Duration    time.Duration
	Success     bool
	ExitCode    int
	Output      string
	OutputHTML  string // HTML-formatted output with links and <br> tags
	ParsedLines []ParsedLine
	Error       string
}

// WorkflowState represents the state of a workflow execution.
type WorkflowState string

const (
	StatePending  WorkflowState = "pending"
	StateRunning  WorkflowState = "running"
	StateSuccess  WorkflowState = "success"
	StateFailed   WorkflowState = "failed"
	StateCanceled WorkflowState = "canceled"
)

// ParsedLine represents a parsed line of output (e.g., error, test result).
type ParsedLine struct {
	Type       string // "error", "warning", "test_pass", "test_fail", "test_skip"
	File       string
	Line       int
	Column     int
	Message    string
	Package    string // for test output
	TestName   string // for test output
	RawOutput  string
	StackTrace []string
}

// OutputUpdate represents an incremental output update from a workflow.
type OutputUpdate struct {
	RunID      string
	Line       string        // New line of output
	Done       bool          // True when workflow is complete
	Status     *WorkflowStatus // Final status (only set when Done=true)
}

// Runner executes workflows.
type Runner interface {
	// Run executes a workflow by ID.
	Run(ctx context.Context, id string) (*WorkflowStatus, error)
	// RunWithOptions executes a workflow with additional options.
	RunWithOptions(ctx context.Context, id string, opts RunOptions) (*WorkflowStatus, error)
	// Status returns the current status of a workflow run.
	Status(runID string) (*WorkflowStatus, bool)
	// List returns all configured workflows.
	List() []WorkflowConfig
	// Get returns a workflow by ID.
	Get(id string) (WorkflowConfig, bool)
	// Cancel cancels a running workflow.
	Cancel(runID string) error
	// Subscribe registers a channel to receive output updates for a workflow run.
	// Returns error if the run doesn't exist.
	Subscribe(runID string, ch chan<- OutputUpdate) error
	// Unsubscribe removes a channel from receiving output updates.
	Unsubscribe(runID string, ch chan<- OutputUpdate)
	// UpdateConfig updates the workflow configs and working directory.
	// Called when worktree is activated to use new paths.
	UpdateConfig(workflows []WorkflowConfig, workingDir string)
	// Close shuts down the runner and stops background goroutines.
	Close() error
}

// RunOptions provides additional options for running a workflow.
type RunOptions struct {
	// SkipConfirm skips the confirmation dialog.
	SkipConfirm bool
	// WorkingDir overrides the working directory.
	WorkingDir string
	// Env adds environment variables.
	Env map[string]string
	// Inputs provides user-supplied input values for template expansion.
	Inputs map[string]any
}

// OutputParser parses workflow output into structured lines.
type OutputParser interface {
	// Name returns the parser name.
	Name() string
	// Parse parses the output and returns parsed lines.
	Parse(output string) []ParsedLine
	// ParseLine parses a single line of output.
	ParseLine(line string) *ParsedLine
}

// ParserRegistry maintains a registry of output parsers.
type ParserRegistry struct {
	parsers map[string]OutputParser
}

// NewParserRegistry creates a new parser registry with built-in parsers.
func NewParserRegistry() *ParserRegistry {
	r := &ParserRegistry{
		parsers: make(map[string]OutputParser),
	}
	// Register built-in parsers
	r.Register(&GoCompilerParser{})
	r.Register(&GoTestJSONParser{})
	r.Register(&GenericParser{})
	r.Register(&NoOpParser{})
	r.Register(&HTMLParser{})
	return r
}

// Register registers a parser.
func (r *ParserRegistry) Register(p OutputParser) {
	r.parsers[p.Name()] = p
}

// Get returns a parser by name.
func (r *ParserRegistry) Get(name string) OutputParser {
	if p, ok := r.parsers[name]; ok {
		return p
	}
	return r.parsers["none"]
}

// ServiceController controls services for workflow execution.
type ServiceController interface {
	// StopServices stops the specified services.
	StopServices(ctx context.Context, names []string) error
	// StartAllServices starts all services.
	StartAllServices(ctx context.Context) error
	// RestartAllServices restarts all services.
	RestartAllServices(ctx context.Context) error
	// StopWatchedServices stops all services with watching enabled.
	StopWatchedServices(ctx context.Context) error
	// RestartWatchedServices restarts all services with watching enabled.
	RestartWatchedServices(ctx context.Context) error
	// ClearAllLogs clears logs for all services.
	ClearAllLogs(ctx context.Context) error
}
