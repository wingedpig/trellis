// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"context"
	"io"
)

// TmuxExecutor executes tmux commands.
type TmuxExecutor interface {
	// HasSession checks if a session exists.
	HasSession(ctx context.Context, session string) bool
	// ListSessions lists all tmux sessions.
	ListSessions(ctx context.Context) ([]string, error)
	// NewSession creates a new tmux session with an optional first window name.
	NewSession(ctx context.Context, session, workdir, firstWindowName string) error
	// KillSession kills a tmux session.
	KillSession(ctx context.Context, session string) error
	// NewWindow creates a new window in a session.
	NewWindow(ctx context.Context, session, window, workdir string, command []string) error
	// KillWindow kills a window in a session.
	KillWindow(ctx context.Context, session, window string) error
	// ListWindows lists windows in a session.
	ListWindows(ctx context.Context, session string) ([]WindowInfo, error)
	// CapturePane captures the pane content.
	CapturePane(ctx context.Context, target string, withHistory bool) ([]byte, error)
	// SendKeys sends keys to a pane.
	SendKeys(ctx context.Context, target string, keys string, literal bool) error
	// SendText sends text via paste-buffer (handles special chars).
	SendText(ctx context.Context, target string, text string) error
	// StartPipePane starts pipe-pane for output streaming.
	StartPipePane(ctx context.Context, target, pipePath string) error
	// StopPipePane stops pipe-pane.
	StopPipePane(ctx context.Context, target string) error
	// ResizeWindow resizes a window.
	ResizeWindow(ctx context.Context, target string, cols, rows int) error
	// GetCursorPosition gets the cursor position in a pane.
	GetCursorPosition(ctx context.Context, target string) (x, y int, err error)
	// SetEnvironment sets an environment variable in a session.
	SetEnvironment(ctx context.Context, session, name, value string) error
	// SetOption sets a tmux option for a session.
	SetOption(ctx context.Context, session, name, value string) error
}

// WindowInfo contains information about a tmux window.
type WindowInfo struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// TerminalConfig holds terminal configuration.
type TerminalConfig struct {
	Backend        string
	HistoryLimit   int
	DefaultShell   string
	DefaultWindows []WindowConfig
	RemoteWindows  []RemoteWindowConfig
	ProjectName    string // Project name used to filter sessions
	APIBaseURL     string // Trellis API base URL for trellis-ctl
}

// WindowConfig defines a window to create.
type WindowConfig struct {
	Name    string
	Command string
}

// RemoteWindowConfig defines a remote window.
type RemoteWindowConfig struct {
	Name        string
	Command     []string
	SSHHost     string
	TmuxSession string
}

// GetCommand returns the command to execute for this remote window.
func (r *RemoteWindowConfig) GetCommand() []string {
	if len(r.Command) > 0 {
		return r.Command
	}
	if r.SSHHost != "" && r.TmuxSession != "" {
		tmuxCmd := "tmux new -A -s " + r.TmuxSession + " \\; set -g status off"
		return []string{"ssh", "-t", r.SSHHost, tmuxCmd}
	}
	return nil
}

// Manager manages terminal sessions.
type Manager interface {
	// CreateSession creates a tmux session for a worktree.
	CreateSession(ctx context.Context, worktree, workdir string, windows []WindowConfig) error
	// EnsureSession ensures a session exists, creating if needed.
	EnsureSession(ctx context.Context, worktree, workdir string, windows []WindowConfig) error
	// KillSession kills a tmux session.
	KillSession(ctx context.Context, worktree string) error
	// AttachReader attaches to a terminal and returns a reader for output.
	AttachReader(ctx context.Context, session, window string) (io.ReadCloser, error)
	// SendInput sends input to a terminal.
	SendInput(ctx context.Context, session, window string, data []byte) error
	// Resize resizes a terminal window.
	Resize(ctx context.Context, session, window string, cols, rows int) error
	// ListSessions lists all managed sessions.
	ListSessions(ctx context.Context) ([]SessionInfo, error)
	// GetScrollback gets the scrollback buffer for a terminal.
	GetScrollback(ctx context.Context, session, window string) ([]byte, error)
	// GetCursorPosition gets the cursor position.
	GetCursorPosition(ctx context.Context, session, window string) (x, y int, err error)
	// GetRemoteWindow gets the remote window config by name.
	GetRemoteWindow(name string) *RemoteWindowConfig
}

// SessionInfo contains information about a terminal session.
type SessionInfo struct {
	Name     string       `json:"name"`
	Worktree string       `json:"worktree,omitempty"`
	Windows  []WindowInfo `json:"windows"`
	IsRemote bool         `json:"isRemote"`
}

// RemoteManager manages remote terminal connections.
type RemoteManager interface {
	// Connect connects to a remote terminal.
	Connect(ctx context.Context, name string) (io.ReadWriteCloser, error)
	// Disconnect disconnects from a remote terminal.
	Disconnect(name string) error
	// IsConnected checks if a remote terminal is connected.
	IsConnected(name string) bool
	// GetScrollback gets the scrollback buffer.
	GetScrollback(name string) []byte
}

// TerminalStream represents a bidirectional terminal stream.
type TerminalStream interface {
	io.ReadWriteCloser
	// Resize resizes the terminal.
	Resize(cols, rows int) error
}

// ToTmuxSessionName converts a worktree name to a valid tmux session name.
func ToTmuxSessionName(worktree string) string {
	// Replace dots with underscores (tmux doesn't like dots in session names)
	result := make([]byte, 0, len(worktree))
	for i := 0; i < len(worktree); i++ {
		if worktree[i] == '.' {
			result = append(result, '_')
		} else {
			result = append(result, worktree[i])
		}
	}
	return string(result)
}

// ToDisplayName converts a session name to a display name with prefix.
func ToDisplayName(session string, isMain bool, isRemote bool) string {
	if isRemote {
		return "!" + session
	}
	if isMain {
		return "@main"
	}
	return "@" + session
}
