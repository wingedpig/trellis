// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"time"

	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/logs"
)

// ProcessState represents the state of a service process.
type ProcessState int

const (
	StatusStopped ProcessState = iota
	StatusStarting
	StatusRunning
	StatusStopping
	StatusCrashed
)

func (s ProcessState) String() string {
	switch s {
	case StatusStopped:
		return "stopped"
	case StatusStarting:
		return "starting"
	case StatusRunning:
		return "running"
	case StatusStopping:
		return "stopping"
	case StatusCrashed:
		return "crashed"
	default:
		return "unknown"
	}
}

// MarshalJSON implements json.Marshaler to output the string representation.
func (s ProcessState) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// ServiceStatus contains the current status of a service.
type ServiceStatus struct {
	State        ProcessState
	PID          int
	ExitCode     int
	StartedAt    time.Time
	StoppedAt    time.Time
	RestartCount int
	Error        string
}

// ServiceInfo contains information about a service.
type ServiceInfo struct {
	Name    string
	Status  ServiceStatus
	Enabled bool
	// Logging configuration (from config)
	ParserType     string
	Columns        []string                   // Deprecated: use Layout
	ColumnWidths   map[string]int             // Deprecated: use Layout
	Layout         []config.LayoutColumnConfig // Full layout configuration
	TimestampField string
	LevelField     string
	MessageField   string
}

// RestartTrigger identifies what caused a restart.
type RestartTrigger int

const (
	RestartManual RestartTrigger = iota
	RestartCrash
	RestartWatch
	RestartDependency
)

func (r RestartTrigger) String() string {
	switch r {
	case RestartManual:
		return "manual"
	case RestartCrash:
		return "crash"
	case RestartWatch:
		return "watch"
	case RestartDependency:
		return "dependency"
	default:
		return "unknown"
	}
}

// Manager is the interface for managing services.
type Manager interface {
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Restart(ctx context.Context, name string, trigger RestartTrigger) error
	Status(name string) (ServiceStatus, error)
	Logs(name string, lines int) ([]string, error)
	LogSize(name string) (int, error)                            // Get number of lines in log buffer
	ParsedLogs(name string, lines int) ([]*logs.LogEntry, error) // Get parsed log entries
	HasParser(name string) bool                                  // Check if service has log parser
	ClearLogs(name string) error
	SubscribeLogs(name string) (chan LogLine, error)   // Subscribe to live log updates
	UnsubscribeLogs(name string, ch chan LogLine)      // Unsubscribe from log updates
	List() []ServiceInfo
	StartAll(ctx context.Context) error
	StopAll(ctx context.Context) error
	StartWatched(ctx context.Context) error // Start only services with watching enabled
	StopWatched(ctx context.Context) error  // Stop only services with watching enabled
	GetService(name string) (ServiceInfo, bool)
	UpdateConfigs(configs []config.ServiceConfig) // Update service configs (for worktree switching)
}

// CrashReason categorizes why a service crashed.
type CrashReason int

const (
	CrashReasonNone CrashReason = iota
	CrashReasonPanic
	CrashReasonFatal
	CrashReasonLogFatal
	CrashReasonError
	CrashReasonOOM
	CrashReasonSignal
	CrashReasonTimeout
	CrashReasonUnknown
)

func (r CrashReason) String() string {
	switch r {
	case CrashReasonNone:
		return "none"
	case CrashReasonPanic:
		return "panic"
	case CrashReasonFatal:
		return "fatal"
	case CrashReasonLogFatal:
		return "log.fatal"
	case CrashReasonError:
		return "error"
	case CrashReasonOOM:
		return "oom"
	case CrashReasonSignal:
		return "signal"
	case CrashReasonTimeout:
		return "timeout"
	case CrashReasonUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// CrashResult contains the analysis of a service crash.
type CrashResult struct {
	Reason     CrashReason
	Details    string
	Location   string
	StackTrace []string
	ExitCode   int
}

// Summary returns a human-readable summary of the crash.
func (r *CrashResult) Summary() string {
	summary := r.Reason.String()
	if r.Details != "" {
		summary += ": " + r.Details
	}
	if r.Location != "" {
		summary += " at " + r.Location
	}
	return summary
}
