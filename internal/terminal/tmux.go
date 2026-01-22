// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// RealTmuxExecutor executes real tmux commands.
type RealTmuxExecutor struct{}

// NewRealTmuxExecutor creates a new tmux executor.
func NewRealTmuxExecutor() *RealTmuxExecutor {
	return &RealTmuxExecutor{}
}

// HasSession checks if a session exists.
func (e *RealTmuxExecutor) HasSession(ctx context.Context, session string) bool {
	cmd := exec.CommandContext(ctx, "tmux", "has-session", "-t", session)
	return cmd.Run() == nil
}

// ListSessions lists all tmux sessions.
func (e *RealTmuxExecutor) ListSessions(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}")
	output, err := cmd.Output()
	if err != nil {
		// No sessions is not an error
		if strings.Contains(err.Error(), "no server running") {
			return nil, nil
		}
		return nil, err
	}

	var sessions []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions, nil
}

// NewSession creates a new tmux session with an optional first window name.
func (e *RealTmuxExecutor) NewSession(ctx context.Context, session, workdir, firstWindowName string) error {
	args := []string{"new-session", "-d", "-s", session}
	if firstWindowName != "" {
		args = append(args, "-n", firstWindowName)
	}
	if workdir != "" {
		args = append(args, "-c", workdir)
	}

	cmd := exec.CommandContext(ctx, "tmux", args...)
	// Ensure we're not inside another tmux session
	cmd.Env = filterTMUXEnv(os.Environ())

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux new-session failed: %s: %v", stderr.String(), err)
	}
	return nil
}

// KillSession kills a tmux session.
func (e *RealTmuxExecutor) KillSession(ctx context.Context, session string) error {
	cmd := exec.CommandContext(ctx, "tmux", "kill-session", "-t", session)
	return cmd.Run()
}

// NewWindow creates a new window in a session.
func (e *RealTmuxExecutor) NewWindow(ctx context.Context, session, window, workdir string, command []string) error {
	args := []string{"new-window", "-t", session, "-n", window}
	if workdir != "" {
		args = append(args, "-c", workdir)
	}
	if len(command) > 0 {
		args = append(args, command...)
	}

	cmd := exec.CommandContext(ctx, "tmux", args...)
	cmd.Env = filterTMUXEnv(os.Environ())

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux new-window failed: %s: %v", stderr.String(), err)
	}
	return nil
}

// KillWindow kills a window in a session.
func (e *RealTmuxExecutor) KillWindow(ctx context.Context, session, window string) error {
	target := fmt.Sprintf("%s:%s", session, window)
	cmd := exec.CommandContext(ctx, "tmux", "kill-window", "-t", target)
	return cmd.Run()
}

// ListWindows lists windows in a session.
func (e *RealTmuxExecutor) ListWindows(ctx context.Context, session string) ([]WindowInfo, error) {
	// Use #{?window_active,*,} to show * for active windows (like default tmux output)
	cmd := exec.CommandContext(ctx, "tmux", "list-windows", "-t", session, "-F", "#{window_index}: #{window_name}#{?window_active,*,}")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseWindowList(string(output)), nil
}

// CapturePane captures the pane content.
func (e *RealTmuxExecutor) CapturePane(ctx context.Context, target string, withHistory bool) ([]byte, error) {
	args := []string{"capture-pane", "-t", target, "-p", "-e"}
	if withHistory {
		args = append(args, "-S", "-")
	}

	cmd := exec.CommandContext(ctx, "tmux", args...)
	return cmd.Output()
}

// SendKeys sends keys to a pane.
func (e *RealTmuxExecutor) SendKeys(ctx context.Context, target string, keys string, literal bool) error {
	args := []string{"send-keys", "-t", target}
	if literal {
		args = append(args, "-l")
	}
	args = append(args, keys)

	cmd := exec.CommandContext(ctx, "tmux", args...)
	return cmd.Run()
}

// SendText sends text via paste-buffer (handles special characters).
func (e *RealTmuxExecutor) SendText(ctx context.Context, target string, text string) error {
	// Use load-buffer and paste-buffer for text with special characters
	loadCmd := exec.CommandContext(ctx, "tmux", "load-buffer", "-")
	loadCmd.Stdin = strings.NewReader(text)
	if err := loadCmd.Run(); err != nil {
		return err
	}

	pasteCmd := exec.CommandContext(ctx, "tmux", "paste-buffer", "-d", "-t", target)
	return pasteCmd.Run()
}

// StartPipePane starts pipe-pane for output streaming.
func (e *RealTmuxExecutor) StartPipePane(ctx context.Context, target, pipePath string) error {
	pipeCmd := fmt.Sprintf("cat >> %s", pipePath)
	cmd := exec.CommandContext(ctx, "tmux", "pipe-pane", "-t", target, "-o", pipeCmd)
	return cmd.Run()
}

// StopPipePane stops pipe-pane.
func (e *RealTmuxExecutor) StopPipePane(ctx context.Context, target string) error {
	cmd := exec.CommandContext(ctx, "tmux", "pipe-pane", "-t", target, "")
	return cmd.Run()
}

// ResizeWindow resizes a window.
func (e *RealTmuxExecutor) ResizeWindow(ctx context.Context, target string, cols, rows int) error {
	cmd := exec.CommandContext(ctx, "tmux", "resize-window", "-t", target,
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows))
	if err := cmd.Run(); err != nil {
		log.Printf("tmux resize-window failed for %s to %dx%d: %v", target, cols, rows, err)
		return err
	}
	return nil
}

// GetCursorPosition gets the cursor position in a pane.
func (e *RealTmuxExecutor) GetCursorPosition(ctx context.Context, target string) (x, y int, err error) {
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-t", target, "-p", "#{cursor_x} #{cursor_y}")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	x, y = parseCursorPosition(string(output))
	return x, y, nil
}

// SetEnvironment sets an environment variable in a session.
func (e *RealTmuxExecutor) SetEnvironment(ctx context.Context, session, name, value string) error {
	cmd := exec.CommandContext(ctx, "tmux", "set-environment", "-t", session, name, value)
	return cmd.Run()
}

// SetOption sets a tmux option for a session.
func (e *RealTmuxExecutor) SetOption(ctx context.Context, session, name, value string) error {
	cmd := exec.CommandContext(ctx, "tmux", "set-option", "-t", session, name, value)
	return cmd.Run()
}

// filterTMUXEnv filters out TMUX environment variable.
func filterTMUXEnv(env []string) []string {
	result := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "TMUX=") {
			result = append(result, e)
		}
	}
	return result
}

// parseWindowList parses tmux list-windows output.
// Expected format from ListWindows: "INDEX: NAME[*]" (using custom -F format)
func parseWindowList(output string) []WindowInfo {
	var windows []WindowInfo
	// Pattern matches our custom format: "0: window-name" or "0: window-name*"
	// The * suffix indicates the active window
	pattern := regexp.MustCompile(`^(\d+):\s+(.+)$`)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		matches := pattern.FindStringSubmatch(line)
		if len(matches) >= 3 {
			idx, _ := strconv.Atoi(matches[1])
			name := matches[2]

			// Check for active marker (*) at end
			active := strings.HasSuffix(name, "*")

			// Strip trailing * marker
			name = strings.TrimSuffix(name, "*")

			windows = append(windows, WindowInfo{
				Index:  idx,
				Name:   name,
				Active: active,
			})
		}
	}

	return windows
}

// parseCursorPosition parses cursor position from tmux output.
func parseCursorPosition(output string) (x, y int) {
	parts := strings.Fields(strings.TrimSpace(output))
	if len(parts) >= 2 {
		x, _ = strconv.Atoi(parts[0])
		y, _ = strconv.Atoi(parts[1])
	}
	return x, y
}
