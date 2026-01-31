// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"path/filepath"
	"time"
)

// Service represents a Trellis-managed service.
//
// Services are long-running processes that Trellis supervises. Trellis handles
// starting, stopping, restarting, and log capture for each service.
type Service struct {
	// Name is the unique identifier for the service.
	Name string `json:"Name"`

	// Status contains the current runtime status of the service.
	Status ServiceStatus `json:"Status"`

	// Enabled indicates whether the service should be automatically started.
	Enabled bool `json:"Enabled"`

	// BinaryPath is the path to the service executable.
	BinaryPath string `json:"BinaryPath,omitempty"`

	// BinaryModTime is the modification time of the service binary.
	// This can be used to detect when the binary has been rebuilt.
	BinaryModTime time.Time `json:"BinaryModTime,omitempty"`

	// ParserType specifies the log parser to use (e.g., "json", "logfmt", "text").
	ParserType string `json:"ParserType,omitempty"`

	// TimestampField is the JSON field name containing the timestamp (for JSON logs).
	TimestampField string `json:"TimestampField,omitempty"`

	// LevelField is the JSON field name containing the log level (for JSON logs).
	LevelField string `json:"LevelField,omitempty"`

	// MessageField is the JSON field name containing the log message (for JSON logs).
	MessageField string `json:"MessageField,omitempty"`
}

// ServiceStatus represents the current runtime status of a service.
type ServiceStatus struct {
	// State is the current state of the service.
	// See ServiceState* constants for possible values.
	State string `json:"State"`

	// PID is the process ID when the service is running.
	// Zero when the service is not running.
	PID int `json:"PID"`

	// ExitCode is the exit code from the last time the service stopped.
	// Only meaningful when State is "stopped" or "crashed".
	ExitCode int `json:"ExitCode"`

	// StartedAt is when the service was last started.
	StartedAt time.Time `json:"StartedAt"`

	// StoppedAt is when the service was last stopped.
	StoppedAt time.Time `json:"StoppedAt"`

	// RestartCount is how many times the service has been restarted.
	RestartCount int `json:"RestartCount"`

	// Error contains the error message if the service failed to start or crashed.
	Error string `json:"Error"`
}

// ServiceState constants define the possible states of a service.
const (
	// ServiceStateRunning indicates the service is currently running.
	ServiceStateRunning = "running"

	// ServiceStateStopped indicates the service is stopped normally.
	ServiceStateStopped = "stopped"

	// ServiceStateCrashed indicates the service exited unexpectedly.
	ServiceStateCrashed = "crashed"

	// ServiceStateStarting indicates the service is in the process of starting.
	ServiceStateStarting = "starting"

	// ServiceStateStopping indicates the service is in the process of stopping.
	ServiceStateStopping = "stopping"
)

// Worktree represents a git worktree managed by Trellis.
//
// Worktrees allow developers to have multiple checkouts of the same repository,
// each on a different branch. Trellis can switch between worktrees, automatically
// rebuilding and restarting services as needed.
type Worktree struct {
	// Path is the filesystem path to the worktree.
	Path string `json:"Path"`

	// Branch is the name of the branch checked out in this worktree.
	Branch string `json:"Branch"`

	// Commit is the current commit SHA.
	Commit string `json:"Commit"`

	// Detached is true if the worktree is in detached HEAD state.
	Detached bool `json:"Detached"`

	// IsBare is true if this is the bare repository (not a worktree).
	IsBare bool `json:"IsBare"`

	// Dirty is true if the worktree has uncommitted changes.
	Dirty bool `json:"Dirty"`

	// Ahead is the number of commits ahead of the upstream branch.
	Ahead int `json:"Ahead"`

	// Behind is the number of commits behind the upstream branch.
	Behind int `json:"Behind"`

	// Active is true if this is the currently active worktree.
	Active bool `json:"Active"`
}

// Name returns the worktree name, which is the last component of the path.
//
// For example, a worktree at "/home/user/project-feature" would have the name "project-feature".
func (w Worktree) Name() string {
	return filepath.Base(w.Path)
}

// ActivateResult is returned when activating a worktree.
type ActivateResult struct {
	// Worktree contains the details of the newly activated worktree.
	Worktree Worktree `json:"worktree"`

	// Duration is how long the activation took (human-readable).
	Duration string `json:"duration"`
}

// Workflow represents a workflow definition.
//
// Workflows are predefined command sequences that can be executed on demand,
// such as build, test, deploy, or database migration scripts.
type Workflow struct {
	// ID is the unique identifier used to run the workflow.
	ID string `json:"ID"`

	// Name is the human-readable display name.
	Name string `json:"Name"`

	// Description is a description for CLI help and discoverability.
	Description string `json:"Description"`

	// Command is a single command to execute (mutually exclusive with Commands).
	Command []string `json:"Command"`

	// Commands is a list of commands to execute in sequence.
	Commands [][]string `json:"Commands"`

	// Confirm indicates whether user confirmation is required before running.
	Confirm bool `json:"Confirm"`

	// ConfirmMessage is the message shown when asking for confirmation.
	ConfirmMessage string `json:"ConfirmMessage"`

	// RequiresStopped lists services that must be stopped before running.
	RequiresStopped []string `json:"RequiresStopped"`

	// RestartServices indicates whether to restart services after completion.
	RestartServices bool `json:"RestartServices"`

	// Inputs defines the input parameters for this workflow.
	Inputs []WorkflowInput `json:"Inputs"`
}

// WorkflowInput defines a parameter that can be passed when running a workflow.
type WorkflowInput struct {
	// Name is the variable name used in command templates.
	Name string `json:"Name"`

	// Type is the input type: "text", "select", "checkbox", or "datepicker".
	Type string `json:"Type"`

	// Label is the human-readable display label.
	Label string `json:"Label"`

	// Description is a description for CLI help.
	Description string `json:"Description"`

	// Placeholder is placeholder text for text inputs.
	Placeholder string `json:"Placeholder"`

	// Options are the available choices for select inputs.
	Options []string `json:"Options"`

	// AllowedValues is a whitelist of allowed values for validation.
	AllowedValues []string `json:"AllowedValues"`

	// Pattern is a regex pattern for validation.
	Pattern string `json:"Pattern"`

	// Default is the default value.
	Default any `json:"Default"`

	// Required indicates whether this input must be provided.
	Required bool `json:"Required"`
}

// WorkflowStatus represents the current status of a workflow execution.
type WorkflowStatus struct {
	// ID is the workflow identifier.
	ID string `json:"ID"`

	// Name is the workflow display name.
	Name string `json:"Name"`

	// State is the current execution state.
	// See WorkflowState* constants for possible values.
	State string `json:"State"`

	// StartedAt is when the workflow started executing.
	StartedAt time.Time `json:"StartedAt"`

	// FinishedAt is when the workflow finished (success or failure).
	FinishedAt time.Time `json:"FinishedAt"`

	// Duration is how long the workflow took to execute.
	Duration time.Duration `json:"Duration"`

	// Success indicates whether the workflow completed successfully.
	Success bool `json:"Success"`

	// ExitCode is the exit code of the last command.
	ExitCode int `json:"ExitCode"`

	// Output contains the combined stdout/stderr output.
	Output string `json:"Output"`

	// OutputHTML contains the output with ANSI codes converted to HTML.
	OutputHTML string `json:"OutputHTML"`

	// Error contains the error message if the workflow failed.
	Error string `json:"Error"`
}

// WorkflowState constants define the possible states of a workflow execution.
const (
	// WorkflowStateIdle indicates the workflow is not running.
	WorkflowStateIdle = "idle"

	// WorkflowStatePending indicates the workflow is pending execution.
	WorkflowStatePending = "pending"

	// WorkflowStateRunning indicates the workflow is currently executing.
	WorkflowStateRunning = "running"

	// WorkflowStateSuccess indicates the workflow finished successfully.
	WorkflowStateSuccess = "success"

	// WorkflowStateFailed indicates the workflow failed.
	WorkflowStateFailed = "failed"

	// WorkflowStateCanceled indicates the workflow was canceled.
	WorkflowStateCanceled = "canceled"
)

// Event represents a Trellis event from the event log.
//
// Events track system activity such as service starts/stops, worktree switches,
// workflow executions, and other notable occurrences.
type Event struct {
	// ID is the unique event identifier.
	ID string `json:"id"`

	// Type identifies the kind of event (e.g., "service.started", "worktree.activated").
	Type string `json:"type"`

	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`

	// Worktree is the name of the worktree where the event occurred.
	Worktree string `json:"worktree"`

	// Payload contains event-specific data.
	Payload map[string]interface{} `json:"payload"`
}

// TraceRequest specifies parameters for executing a distributed trace query.
//
// A trace searches across multiple log sources for entries containing a specific ID,
// allowing correlation of requests across services.
type TraceRequest struct {
	// ID is the trace/correlation ID to search for.
	ID string `json:"id"`

	// Group is the name of the trace group (set of log viewers) to search.
	Group string `json:"group"`

	// Start is the beginning of the time range to search.
	Start time.Time `json:"start"`

	// End is the end of the time range to search.
	End time.Time `json:"end"`

	// Name is an optional name for the trace report.
	// If not specified, a name is auto-generated.
	Name string `json:"name,omitempty"`

	// ExpandByID enables two-pass searching: first find matching entries,
	// then expand the time window to find related entries.
	ExpandByID bool `json:"expand_by_id"`
}

// TraceResult is the initial response when starting a trace query.
//
// The trace executes asynchronously. Use [TraceClient.GetReport] to poll
// for completion and retrieve the full results.
type TraceResult struct {
	// Name is the name assigned to this trace report.
	Name string `json:"name"`

	// Status indicates the trace state ("running", "completed", "failed").
	Status string `json:"status"`

	// TotalEntries is the number of matching log entries found so far.
	TotalEntries int `json:"total_entries"`

	// Sources maps source names to the number of entries from each.
	Sources map[string]int `json:"sources"`

	// DurationMS is how long the trace has been running in milliseconds.
	DurationMS int64 `json:"duration_ms"`

	// Error contains an error message if the trace failed.
	Error string `json:"error,omitempty"`
}

// TraceReport is a completed distributed trace report.
//
// A trace report contains all log entries matching the trace ID across
// multiple services, sorted by timestamp.
type TraceReport struct {
	// Version is the report format version.
	Version string `json:"version"`

	// Name is the unique name of this report.
	Name string `json:"name"`

	// TraceID is the ID that was searched for.
	TraceID string `json:"trace_id"`

	// Group is the trace group that was searched.
	Group string `json:"group"`

	// Status is the final status ("completed" or "failed").
	Status string `json:"status"`

	// CreatedAt is when the trace was executed.
	CreatedAt time.Time `json:"created_at"`

	// TimeRange is the time window that was searched.
	TimeRange TimeRange `json:"time_range"`

	// Summary contains aggregate statistics about the trace results.
	Summary TraceSummary `json:"summary"`

	// Entries contains all matching log entries, sorted by timestamp.
	Entries []TraceEntry `json:"entries"`

	// Error contains an error message if the trace failed.
	Error string `json:"error,omitempty"`
}

// TimeRange represents a time window for trace queries.
type TimeRange struct {
	// Start is the beginning of the time range.
	Start time.Time `json:"start"`

	// End is the end of the time range.
	End time.Time `json:"end"`
}

// TraceSummary contains aggregate statistics for a trace report.
type TraceSummary struct {
	// TotalEntries is the total number of log entries in the trace.
	TotalEntries int `json:"total_entries"`

	// BySource maps source names to entry counts.
	BySource map[string]int `json:"by_source"`

	// ByLevel maps log levels to entry counts.
	ByLevel map[string]int `json:"by_level"`

	// DurationMS is how long the trace query took in milliseconds.
	DurationMS int64 `json:"duration_ms"`
}

// TraceEntry is a single log entry in a trace report.
type TraceEntry struct {
	// Timestamp is when the log entry was recorded.
	Timestamp time.Time `json:"timestamp"`

	// Source is the name of the log source (service or log viewer).
	Source string `json:"source"`

	// Level is the log level (e.g., "INFO", "ERROR").
	Level string `json:"level,omitempty"`

	// Message is the parsed log message.
	Message string `json:"message"`

	// Fields contains additional structured fields from the log entry.
	Fields map[string]interface{} `json:"fields,omitempty"`

	// Raw is the original unparsed log line.
	Raw string `json:"raw"`

	// IsContext is true for entries included as context (not direct matches).
	IsContext bool `json:"is_context"`
}

// TraceReportSummary is a compact summary of a saved trace report.
//
// Used when listing available trace reports.
type TraceReportSummary struct {
	// Name is the unique report name.
	Name string `json:"name"`

	// TraceID is the ID that was searched for.
	TraceID string `json:"trace_id"`

	// Group is the trace group that was searched.
	Group string `json:"group"`

	// CreatedAt is when the trace was executed.
	CreatedAt time.Time `json:"created_at"`

	// EntryCount is the number of entries in the report.
	EntryCount int `json:"entry_count"`
}

// TraceGroup defines a group of log viewers for distributed tracing.
//
// Trace groups allow searching across related log sources with a single query.
type TraceGroup struct {
	// Name is the unique identifier for this group.
	Name string `json:"name"`

	// LogViewers lists the log viewer names included in this group.
	LogViewers []string `json:"log_viewers"`
}

// NotifyType represents the type of notification to send.
type NotifyType string

// Notification type constants.
const (
	// NotifyDone indicates a task has completed successfully.
	// Typically triggers a success sound or visual indicator.
	NotifyDone NotifyType = "done"

	// NotifyBlocked indicates a task is waiting for user input.
	// Typically triggers an attention-grabbing notification.
	NotifyBlocked NotifyType = "blocked"

	// NotifyError indicates an error has occurred.
	// Typically triggers an error sound or alert.
	NotifyError NotifyType = "error"
)

// NotifyRequest is the request body for sending a notification.
type NotifyRequest struct {
	// Message is the notification text to display.
	Message string `json:"message"`

	// Type is the notification type (done, blocked, error).
	Type string `json:"type"`
}

// NotifyResponse is returned after sending a notification.
type NotifyResponse struct {
	// ID is the unique identifier for this notification.
	ID string `json:"id"`

	// Type is the notification type that was sent.
	Type string `json:"type"`

	// Timestamp is when the notification was sent.
	Timestamp string `json:"timestamp"`
}

// LogViewer represents a configured log viewer.
//
// Log viewers aggregate logs from external sources like system logs,
// third-party services, or custom log files.
type LogViewer struct {
	// Name is the unique identifier for this log viewer.
	Name string `json:"name"`

	// Description is a human-readable description of what this viewer shows.
	Description string `json:"description,omitempty"`
}

// LogEntry represents a single parsed log entry.
type LogEntry struct {
	// Timestamp is when the log entry was recorded.
	Timestamp time.Time `json:"timestamp"`

	// Source identifies where the log entry came from.
	Source string `json:"source,omitempty"`

	// Level is the log level (e.g., "INFO", "WARN", "ERROR").
	Level string `json:"level,omitempty"`

	// Message is the parsed log message text.
	Message string `json:"message"`

	// Fields contains additional structured data from the log entry.
	Fields map[string]interface{} `json:"fields,omitempty"`

	// Raw is the original unparsed log line.
	Raw string `json:"raw"`
}

// LogEntriesOptions specifies options for fetching log entries.
type LogEntriesOptions struct {
	// Limit is the maximum number of entries to return.
	Limit int

	// Since filters to entries after this time.
	Since time.Time

	// Until filters to entries before this time.
	Until time.Time

	// Level filters to entries at or above this level.
	Level string

	// Grep filters to entries matching this pattern.
	Grep string

	// Before specifies how many context lines to include before each grep match.
	Before int

	// After specifies how many context lines to include after each grep match.
	After int
}
