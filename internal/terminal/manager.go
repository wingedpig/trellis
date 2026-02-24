// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// RealManager implements the Manager interface.
type RealManager struct {
	mu            sync.RWMutex
	tmux          TmuxExecutor
	cfg           TerminalConfig
	sessions      map[string]bool
	pipes         map[string]*pipeReader
	remoteWindows []RemoteWindowConfig
	projectPrefix string // Session prefix used to filter sessions (project name with dots replaced)
	store         *WindowStore
}

// pipeReader manages a named pipe for terminal output.
type pipeReader struct {
	path   string
	file   *os.File
	closed atomic.Bool
	mu     sync.Mutex // protects file field only
}

// NewManager creates a new terminal manager.
func NewManager(tmux TmuxExecutor, cfg TerminalConfig) *RealManager {
	m := &RealManager{
		tmux:          tmux,
		cfg:           cfg,
		sessions:      make(map[string]bool),
		pipes:         make(map[string]*pipeReader),
		remoteWindows: cfg.RemoteWindows,
		projectPrefix: ToTmuxSessionName(cfg.ProjectName),
	}
	if cfg.StateDir != "" {
		m.store = NewWindowStore(filepath.Join(cfg.StateDir, "windows.json"))
	}
	return m
}

// resolveWindowTarget resolves a window name to an unambiguous target (session:index).
// This prevents issues when multiple windows have similar names (e.g., "dev" and "dev!").
func (m *RealManager) resolveWindowTarget(ctx context.Context, session, window string) string {
	windows, err := m.tmux.ListWindows(ctx, session)
	if err != nil {
		// Fallback to name-based targeting
		return fmt.Sprintf("%s:%s", session, window)
	}

	// Find the first window with matching name
	for _, w := range windows {
		if w.Name == window {
			return fmt.Sprintf("%s:%d", session, w.Index)
		}
	}

	// Fallback to name-based targeting
	return fmt.Sprintf("%s:%s", session, window)
}

// CreateSession creates a tmux session for a worktree.
func (m *RealManager) CreateSession(ctx context.Context, worktree, workdir string, windows []WindowConfig) error {
	session := ToTmuxSessionName(worktree)

	// Check if session already exists
	if m.tmux.HasSession(ctx, session) {
		return fmt.Errorf("session %s already exists", session)
	}

	// Determine first window name
	firstWindowName := "dev"
	if len(windows) > 0 {
		firstWindowName = windows[0].Name
	}

	// Create the session with first window (like runner does)
	// First window always uses default login shell - no command
	if err := m.tmux.NewSession(ctx, session, workdir, firstWindowName); err != nil {
		return err
	}

	// Set TRELLIS_API environment variable if configured
	if m.cfg.APIBaseURL != "" {
		m.tmux.SetEnvironment(ctx, session, "TRELLIS_API", m.cfg.APIBaseURL)
	}

	// Set scroll-on-clear off to preserve scrollback during screen clears (tmux 3.2+)
	// This prevents losing scrollback history when applications like Claude redraw on resize
	// Ignore errors - option may not exist on older tmux versions
	m.tmux.SetOption(ctx, session, "scroll-on-clear", "off")

	m.mu.Lock()
	m.sessions[session] = true
	m.mu.Unlock()

	// Create remaining windows (skip first, it was created with session)
	for i := 1; i < len(windows); i++ {
		wc := windows[i]
		// Only pass command for non-shell commands
		var command []string
		if wc.Command != "" && !isShellCommand(wc.Command) {
			command = []string{wc.Command}
		}

		if err := m.tmux.NewWindow(ctx, session, wc.Name, workdir, command); err != nil {
			// Log but don't fail - some windows may fail
			continue
		}
	}

	return nil
}

// EnsureSession ensures a session exists with all expected windows, creating if needed.
func (m *RealManager) EnsureSession(ctx context.Context, worktree, workdir string, windows []WindowConfig) error {
	session := ToTmuxSessionName(worktree)

	if m.tmux.HasSession(ctx, session) {
		m.mu.Lock()
		m.sessions[session] = true
		m.mu.Unlock()

		// Ensure TRELLIS_API is set (in case it wasn't before or API URL changed)
		if m.cfg.APIBaseURL != "" {
			m.tmux.SetEnvironment(ctx, session, "TRELLIS_API", m.cfg.APIBaseURL)
		}

		// Set scroll-on-clear off to preserve scrollback during screen clears (tmux 3.2+)
		// Ignore errors - option may not exist on older tmux versions
		m.tmux.SetOption(ctx, session, "scroll-on-clear", "off")

		// Session exists - ensure all expected windows exist
		existingWindows, err := m.tmux.ListWindows(ctx, session)
		if err != nil {
			return nil // Session exists, can't list windows, just continue
		}

		// Build map of existing window names
		existing := make(map[string]bool)
		for _, w := range existingWindows {
			existing[w.Name] = true
		}

		// Create any missing windows
		for _, wc := range windows {
			if !existing[wc.Name] {
				// Only pass command for non-shell commands
				var command []string
				if wc.Command != "" && !isShellCommand(wc.Command) {
					command = []string{wc.Command}
				}

				if err := m.tmux.NewWindow(ctx, session, wc.Name, workdir, command); err != nil {
					// Log but don't fail - some windows may fail
					continue
				}
			}
		}

		return nil
	}

	return m.CreateSession(ctx, worktree, workdir, windows)
}

// isShellCommand returns true if the command is a shell (should use default login shell instead).
func isShellCommand(cmd string) bool {
	shells := []string{"/bin/bash", "/bin/sh", "/bin/zsh", "/usr/bin/bash", "/usr/bin/zsh", "bash", "zsh", "sh"}
	for _, shell := range shells {
		if cmd == shell {
			return true
		}
	}
	return false
}

// KillSession kills a tmux session.
func (m *RealManager) KillSession(ctx context.Context, worktree string) error {
	session := ToTmuxSessionName(worktree)

	// Close any pipe readers
	m.mu.Lock()
	for key, pipe := range m.pipes {
		if len(key) > len(session) && key[:len(session)+1] == session+":" {
			pipe.Close()
			delete(m.pipes, key)
		}
	}
	delete(m.sessions, session)
	m.mu.Unlock()

	return m.tmux.KillSession(ctx, session)
}

// sanitizeForPath replaces characters that are unsafe for filesystem paths.
func sanitizeForPath(s string) string {
	// Replace slashes, spaces, and other problematic characters with underscores
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '/' || c == ' ' || c == '\\' || c == ':' || c == '*' || c == '?' || c == '"' || c == '<' || c == '>' || c == '|' {
			result[i] = '_'
		} else {
			result[i] = c
		}
	}
	return string(result)
}

// AttachReader attaches to a terminal and returns a reader for output.
func (m *RealManager) AttachReader(ctx context.Context, session, window string) (io.ReadCloser, error) {
	session = ToTmuxSessionName(session)
	// Use name-based key for caching (consistent across sessions)
	key := fmt.Sprintf("%s:%s", session, window)
	// Use index-based target for tmux commands (avoids ambiguity with similar names)
	target := m.resolveWindowTarget(ctx, session, window)

	m.mu.Lock()
	if pipe, ok := m.pipes[key]; ok {
		m.mu.Unlock()
		if pipe != nil {
			return pipe, nil
		}
		// Another goroutine is creating the pipe; wait and retry
		// This is rare, so a simple sleep+retry is acceptable
		for i := 0; i < 50; i++ { // 5 seconds max
			time.Sleep(100 * time.Millisecond)
			m.mu.Lock()
			if pipe, ok := m.pipes[key]; ok && pipe != nil {
				m.mu.Unlock()
				return pipe, nil
			}
			m.mu.Unlock()
		}
		return nil, fmt.Errorf("timeout waiting for pipe creation")
	}

	// Mark that we're creating a pipe for this key to prevent races
	// We use a nil placeholder; other goroutines will see the key exists
	m.pipes[key] = nil
	m.mu.Unlock()

	// Sanitize session and window names for filesystem path safety
	pipePath := fmt.Sprintf("/tmp/trellis-pipe-%s-%s.fifo", sanitizeForPath(session), sanitizeForPath(window))

	// Remove existing pipe
	os.Remove(pipePath)

	// Stop any existing pipe-pane
	m.tmux.StopPipePane(ctx, target)

	// Create the FIFO
	if err := createFIFO(pipePath); err != nil {
		m.mu.Lock()
		delete(m.pipes, key)
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to create FIFO: %w", err)
	}

	// Start pipe-pane
	if err := m.tmux.StartPipePane(ctx, target, pipePath); err != nil {
		os.Remove(pipePath)
		m.mu.Lock()
		delete(m.pipes, key)
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to start pipe-pane: %w", err)
	}

	// Open the pipe for reading (non-blocking to avoid deadlock)
	// We use O_RDONLY|O_NONBLOCK, then clear NONBLOCK after open
	fd, err := syscall.Open(pipePath, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		m.tmux.StopPipePane(ctx, target)
		os.Remove(pipePath)
		m.mu.Lock()
		delete(m.pipes, key)
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to open FIFO: %w", err)
	}
	// Clear non-blocking flag so reads block normally
	syscall.SetNonblock(fd, false)
	file := os.NewFile(uintptr(fd), pipePath)

	pipe := &pipeReader{
		path: pipePath,
		file: file,
	}

	m.mu.Lock()
	m.pipes[key] = pipe
	m.mu.Unlock()

	return pipe, nil
}

// SendInput sends input to a terminal.
func (m *RealManager) SendInput(ctx context.Context, session, window string, data []byte) error {
	session = ToTmuxSessionName(session)
	target := m.resolveWindowTarget(ctx, session, window)

	text := string(data)

	// Handle Enter key specially
	if text == "\r" {
		return m.tmux.SendKeys(ctx, target, "Enter", false)
	}

	// Use paste-buffer for other input (handles special chars)
	err := m.tmux.SendText(ctx, target, text)
	if err != nil {
		// Fallback to send-keys with literal flag
		return m.tmux.SendKeys(ctx, target, text, true)
	}
	return nil
}

// Resize resizes a terminal window.
func (m *RealManager) Resize(ctx context.Context, session, window string, cols, rows int) error {
	session = ToTmuxSessionName(session)
	target := m.resolveWindowTarget(ctx, session, window)
	return m.tmux.ResizeWindow(ctx, target, cols, rows)
}

// ListSessions lists all tmux sessions plus remote windows.
// Only returns sessions that belong to this project (matching the project prefix).
func (m *RealManager) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	// Get all sessions from tmux directly
	sessionNames, err := m.tmux.ListSessions(ctx)
	if err != nil {
		return nil, err
	}

	var sessions []SessionInfo
	for _, session := range sessionNames {
		// Filter sessions by project prefix
		if m.projectPrefix != "" && !strings.HasPrefix(session, m.projectPrefix) {
			continue
		}

		windows, err := m.tmux.ListWindows(ctx, session)
		if err != nil {
			continue
		}

		sessions = append(sessions, SessionInfo{
			Name:    session,
			Windows: windows,
		})
	}

	// Add remote windows as separate "sessions"
	for _, rw := range m.remoteWindows {
		sessions = append(sessions, SessionInfo{
			Name: rw.Name,
			Windows: []WindowInfo{
				{Index: 0, Name: rw.Name, Active: true},
			},
			IsRemote: true,
		})
	}

	return sessions, nil
}

// GetScrollback gets the scrollback buffer for a terminal.
func (m *RealManager) GetScrollback(ctx context.Context, session, window string) ([]byte, error) {
	session = ToTmuxSessionName(session)
	target := m.resolveWindowTarget(ctx, session, window)
	return m.tmux.CapturePane(ctx, target, true)
}

// GetCursorPosition gets the cursor position.
func (m *RealManager) GetCursorPosition(ctx context.Context, session, window string) (x, y int, err error) {
	session = ToTmuxSessionName(session)
	target := m.resolveWindowTarget(ctx, session, window)
	return m.tmux.GetCursorPosition(ctx, target)
}

// GetRemoteWindow gets the remote window config by name.
func (m *RealManager) GetRemoteWindow(name string) *RemoteWindowConfig {
	for i := range m.remoteWindows {
		if m.remoteWindows[i].Name == name {
			return &m.remoteWindows[i]
		}
	}
	return nil
}

// SaveWindow persists a window name for a session.
func (m *RealManager) SaveWindow(session, window string) {
	if m.store == nil {
		return
	}
	data, err := m.store.Load()
	if err != nil {
		log.Printf("Warning: failed to load window state: %v", err)
		return
	}
	// Avoid duplicates
	for _, w := range data[session] {
		if w == window {
			return
		}
	}
	data[session] = append(data[session], window)
	if err := m.store.Save(data); err != nil {
		log.Printf("Warning: failed to save window state: %v", err)
	}
}

// RemoveWindow removes a persisted window name for a session.
func (m *RealManager) RemoveWindow(session, window string) {
	if m.store == nil {
		return
	}
	data, err := m.store.Load()
	if err != nil {
		log.Printf("Warning: failed to load window state: %v", err)
		return
	}
	windows := data[session]
	for i, w := range windows {
		if w == window {
			data[session] = append(windows[:i], windows[i+1:]...)
			break
		}
	}
	if len(data[session]) == 0 {
		delete(data, session)
	}
	if err := m.store.Save(data); err != nil {
		log.Printf("Warning: failed to save window state: %v", err)
	}
}

// RenameWindowState renames a persisted window.
func (m *RealManager) RenameWindowState(session, oldName, newName string) {
	if m.store == nil {
		return
	}
	data, err := m.store.Load()
	if err != nil {
		log.Printf("Warning: failed to load window state: %v", err)
		return
	}
	for i, w := range data[session] {
		if w == oldName {
			data[session][i] = newName
			break
		}
	}
	if err := m.store.Save(data); err != nil {
		log.Printf("Warning: failed to save window state: %v", err)
	}
}

// LoadSavedWindows returns all persisted session→window mappings.
func (m *RealManager) LoadSavedWindows() WindowsData {
	if m.store == nil {
		return nil
	}
	data, err := m.store.Load()
	if err != nil {
		log.Printf("Warning: failed to load saved windows: %v", err)
		return nil
	}
	return data
}

// Read implements io.Reader for pipeReader.
func (p *pipeReader) Read(buf []byte) (int, error) {
	// Check closed atomically without holding the mutex
	if p.closed.Load() {
		return 0, io.EOF
	}

	// Get file reference under lock, but don't hold lock during read
	p.mu.Lock()
	f := p.file
	p.mu.Unlock()

	if f == nil {
		return 0, io.EOF
	}

	n, err := f.Read(buf)
	// If closed while reading, return EOF
	if p.closed.Load() && err != nil {
		return n, io.EOF
	}
	return n, err
}

// Close implements io.Closer for pipeReader.
func (p *pipeReader) Close() error {
	// Use atomic swap to ensure only one Close succeeds
	if p.closed.Swap(true) {
		return nil // Already closed
	}

	p.mu.Lock()
	f := p.file
	p.file = nil
	p.mu.Unlock()

	if f != nil {
		f.Close() // This will unblock any blocked Read
	}
	os.Remove(p.path)
	return nil
}

// createFIFO creates a named pipe (FIFO).
func createFIFO(path string) error {
	cmd := exec.Command("mkfifo", path)
	return cmd.Run()
}
