// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/wingedpig/trellis/internal/terminal"
)

// Per-target mutexes to serialize tmux send-keys commands per pane.
// Different tmux panes don't conflict; only input to the same pane needs serialization.
var tmuxTargetMutexes sync.Map // map[string]*sync.Mutex

func getTargetMutex(target string) *sync.Mutex {
	val, _ := tmuxTargetMutexes.LoadOrStore(target, &sync.Mutex{})
	return val.(*sync.Mutex)
}

// terminalMessage represents a message from the terminal frontend.
type terminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Rows int    `json:"rows"`
	Cols int    `json:"cols"`
}

// TerminalHandler handles terminal-related API requests.
type TerminalHandler struct {
	mgr   terminal.Manager
	mu    sync.Mutex
	conns map[*websocket.Conn]struct{} // Active WebSocket connections
}

// NewTerminalHandler creates a new terminal handler.
func NewTerminalHandler(mgr terminal.Manager) *TerminalHandler {
	return &TerminalHandler{
		mgr:   mgr,
		conns: make(map[*websocket.Conn]struct{}),
	}
}

// trackConn registers a WebSocket connection for shutdown tracking.
func (h *TerminalHandler) trackConn(conn *websocket.Conn) {
	h.mu.Lock()
	h.conns[conn] = struct{}{}
	h.mu.Unlock()
}

// untrackConn removes a WebSocket connection from shutdown tracking.
func (h *TerminalHandler) untrackConn(conn *websocket.Conn) {
	h.mu.Lock()
	delete(h.conns, conn)
	h.mu.Unlock()
}

// Shutdown closes all active WebSocket connections to allow graceful server shutdown.
func (h *TerminalHandler) Shutdown() {
	h.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(h.conns))
	for conn := range h.conns {
		conns = append(conns, conn)
	}
	h.mu.Unlock()

	if len(conns) > 0 {
		log.Printf("Terminal handler: closing %d active WebSocket connections", len(conns))
	}

	for _, conn := range conns {
		// Send close message and close connection
		conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"),
			time.Now().Add(time.Second))
		conn.Close()
	}
}

// ListSessions returns all terminal sessions.
func (h *TerminalHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.mgr.ListSessions(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrTerminalError, err.Error())
		return
	}

	// Wrap in object with sessions key for frontend compatibility
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
	})
}

// WebSocket handles the WebSocket connection for terminal I/O.
func (h *TerminalHandler) WebSocket(w http.ResponseWriter, r *http.Request) {
	// Get session and window from query
	session := r.URL.Query().Get("session")
	window := r.URL.Query().Get("window")
	isRemote := r.URL.Query().Get("remote") == "1"

	log.Printf("Terminal WebSocket: request for %s:%s (remote=%v)", session, window, isRemote)

	if session == "" || window == "" {
		http.Error(w, "session and window parameters required", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Terminal WebSocket: upgrade failed: %v", err)
		return
	}
	h.trackConn(conn)
	defer func() {
		h.untrackConn(conn)
		conn.Close()
	}()
	log.Printf("Terminal WebSocket: upgrade successful")

	// Configure keepalive with ping/pong
	const pongWait = 60 * time.Second
	const pingPeriod = (pongWait * 9) / 10
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Mutex to protect concurrent WebSocket writes (gorilla/websocket requires single writer)
	var writeMu sync.Mutex

	// Start ping ticker in goroutine
	pingTicker := time.NewTicker(pingPeriod)
	defer pingTicker.Stop()
	stopPing := make(chan bool, 1)
	defer func() {
		select {
		case stopPing <- true:
		default:
		}
	}()
	go func() {
		for {
			select {
			case <-pingTicker.C:
				writeMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second))
				writeMu.Unlock()
				if err != nil {
					log.Printf("Terminal WebSocket: ping failed: %v", err)
					return
				}
			case <-stopPing:
				return
			}
		}
	}()

	if isRemote {
		h.handleRemoteTerminal(conn, window, pongWait, &writeMu)
	} else {
		h.handleLocalTerminal(conn, r, session, window, pongWait, &writeMu)
	}
}

// handleRemoteTerminal handles WebSocket connection for remote terminals (SSH).
// Each connection gets a fresh PTY - no session persistence.
func (h *TerminalHandler) handleRemoteTerminal(conn *websocket.Conn, name string, pongWait time.Duration, writeMu *sync.Mutex) {
	log.Printf("Terminal WebSocket: handleRemoteTerminal for %q", name)

	// Get the remote window config
	remoteWin := h.mgr.GetRemoteWindow(name)
	if remoteWin == nil {
		log.Printf("Terminal WebSocket: remote window %q not found", name)
		writeMu.Lock()
		conn.WriteMessage(websocket.TextMessage, []byte("Error: remote window not found\r\n"))
		writeMu.Unlock()
		return
	}

	command := remoteWin.GetCommand()
	if len(command) == 0 {
		writeMu.Lock()
		conn.WriteMessage(websocket.TextMessage, []byte("Error: no command configured\r\n"))
		writeMu.Unlock()
		return
	}

	// Prepare command, injecting mouse mode for tmux
	command = prepareRemoteCommand(command)
	log.Printf("Terminal WebSocket: starting command for %q: %v", name, command)

	// Start the command with a PTY
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Printf("Terminal WebSocket: failed to start PTY: %v", err)
		writeMu.Lock()
		conn.WriteMessage(websocket.TextMessage, []byte("Error: "+err.Error()+"\r\n"))
		writeMu.Unlock()
		return
	}

	// Track if PTY is still running
	ptyExited := make(chan struct{})

	// Clean up on exit
	defer func() {
		ptmx.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
		log.Printf("Terminal WebSocket: cleaned up remote session for %s", name)
	}()

	// Read from PTY and send to WebSocket
	go func() {
		defer close(ptyExited)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				log.Printf("Terminal WebSocket: PTY read ended for %s: %v", name, err)
				// Notify client that session ended, then close gracefully
				writeMu.Lock()
				if err == io.EOF {
					conn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[33mSession ended\x1b[0m\r\n"))
				} else {
					conn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31mConnection lost: "+err.Error()+"\x1b[0m\r\n"))
				}
				// Send close frame - this will cause ReadMessage to return an error
				conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session ended"))
				writeMu.Unlock()
				return
			}
			if n > 0 {
				validUTF8 := strings.ToValidUTF8(string(buf[:n]), "")
				writeMu.Lock()
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				err := conn.WriteMessage(websocket.TextMessage, []byte(validUTF8))
				writeMu.Unlock()
				if err != nil {
					log.Printf("Terminal WebSocket: write error for %s: %v", name, err)
					return
				}
			}
		}
	}()

	// Read from WebSocket and send to PTY
	for {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			// Check if this was due to PTY exit (expected) vs other error
			select {
			case <-ptyExited:
				log.Printf("Terminal WebSocket: connection closed after PTY exit for %s", name)
			default:
				log.Printf("Terminal WebSocket: read error for %s: %v", name, err)
			}
			break
		}

		if messageType == websocket.TextMessage {
			var msg terminalMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				log.Printf("Terminal WebSocket: failed to parse JSON: %v", err)
				continue
			}

			switch msg.Type {
			case "input":
				if _, err := ptmx.WriteString(msg.Data); err != nil {
					log.Printf("Terminal WebSocket: PTY write error: %v", err)
				}

			case "resize":
				if msg.Cols > 0 && msg.Rows > 0 {
					pty.Setsize(ptmx, &pty.Winsize{
						Rows: uint16(msg.Rows),
						Cols: uint16(msg.Cols),
					})
				}
			}
		}
	}
}

// handleLocalTerminal handles WebSocket connection for local tmux terminals.
func (h *TerminalHandler) handleLocalTerminal(conn *websocket.Conn, r *http.Request, session, window string, pongWait time.Duration, writeMu *sync.Mutex) {
	ctx := r.Context()
	tmuxSession := terminal.ToTmuxSessionName(session)
	log.Printf("Terminal WebSocket: target=%s:%s", tmuxSession, window)

	// First check if the session exists (matches runner's approach)
	checkCmd := exec.Command("tmux", "has-session", "-t", tmuxSession)
	if err := checkCmd.Run(); err != nil {
		errMsg := fmt.Sprintf("Session %s does not exist", tmuxSession)
		log.Printf("Terminal WebSocket: %s", errMsg)
		writeMu.Lock()
		conn.WriteMessage(websocket.TextMessage, []byte(errMsg+"\r\n"))
		writeMu.Unlock()
		return
	}

	// Check if the window exists, if not create it (matches runner's approach)
	// Also get window index for unambiguous targeting (avoids issues with similar window names)
	checkWindowCmd := exec.Command("tmux", "list-windows", "-t", tmuxSession, "-F", "#{window_index}:#{window_name}")
	windowsOutput, err := checkWindowCmd.Output()
	windowExists := false
	windowIndex := -1
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(windowsOutput)), "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && parts[1] == window {
				windowExists = true
				windowIndex, _ = strconv.Atoi(parts[0])
				break
			}
		}
	}

	if !windowExists {
		log.Printf("Terminal WebSocket: Window %s does not exist in session %s, creating it", window, tmuxSession)
		// Get the working directory from the first window
		getCwdCmd := exec.Command("tmux", "display-message", "-t", tmuxSession+":0", "-p", "#{pane_current_path}")
		cwdOutput, _ := getCwdCmd.Output()
		cwd := strings.TrimSpace(string(cwdOutput))
		if cwd == "" {
			cwd = "~"
		}

		// Create the window
		createWindowCmd := exec.Command("tmux", "new-window", "-t", tmuxSession, "-n", window, "-c", cwd)
		createWindowCmd.Env = append(os.Environ(), "TMUX=")
		if err := createWindowCmd.Run(); err != nil {
			errMsg := fmt.Sprintf("Failed to create window %s: %v", window, err)
			log.Printf("Terminal WebSocket: %s", errMsg)
			writeMu.Lock()
			conn.WriteMessage(websocket.TextMessage, []byte("Error: "+errMsg+"\r\n"))
			writeMu.Unlock()
			return
		}
		log.Printf("Terminal WebSocket: Successfully created window %s", window)

		// Get the index of the newly created window
		getIndexCmd := exec.Command("tmux", "list-windows", "-t", tmuxSession, "-F", "#{window_index}:#{window_name}")
		if indexOutput, err := getIndexCmd.Output(); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(indexOutput)), "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 && parts[1] == window {
					windowIndex, _ = strconv.Atoi(parts[0])
					break
				}
			}
		}
	}

	// Build unambiguous target using window index (avoids issues when multiple windows have similar names)
	var target string
	if windowIndex >= 0 {
		target = fmt.Sprintf("%s:%d", tmuxSession, windowIndex)
	} else {
		// Fallback to name-based targeting if we couldn't get the index
		target = fmt.Sprintf("%s:%s", tmuxSession, window)
	}
	log.Printf("Terminal WebSocket: resolved target=%s", target)

	// Wait for initial resize message from client before sending scrollback
	// This ensures tmux window is sized correctly before capturing scrollback
	log.Printf("Terminal WebSocket: waiting for initial resize from client")
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, initialMsg, err := conn.ReadMessage()
	conn.SetReadDeadline(time.Time{}) // Clear deadline
	if err != nil {
		log.Printf("Terminal WebSocket: failed to read initial resize: %v", err)
	} else {
		var msg terminalMessage
		if err := json.Unmarshal(initialMsg, &msg); err == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
			log.Printf("Terminal WebSocket: initial resize to %dx%d", msg.Cols, msg.Rows)
			h.mgr.Resize(ctx, session, window, msg.Cols, msg.Rows)
		} else {
			log.Printf("Terminal WebSocket: unexpected initial message: %s", string(initialMsg))
		}
	}

	// Get cursor position BEFORE sending scrollback (tmux position is authoritative)
	cursorX, cursorY, cursorErr := h.mgr.GetCursorPosition(ctx, session, window)
	if cursorErr == nil {
		log.Printf("Terminal WebSocket: tmux cursor at (%d, %d)", cursorX, cursorY)
	}

	// Send initial scrollback (now captured at correct size)
	scrollback, err := h.mgr.GetScrollback(ctx, session, window)
	if err == nil && len(scrollback) > 0 {
		log.Printf("Terminal WebSocket: sending scrollback, %d bytes", len(scrollback))
		validOutput := strings.ToValidUTF8(string(scrollback), "")
		// Trim single trailing newline - capture-pane adds one final newline
		validOutput = strings.TrimSuffix(validOutput, "\n")
		writeMu.Lock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		conn.WriteMessage(websocket.TextMessage, []byte(validOutput))
		writeMu.Unlock()
		log.Printf("Terminal WebSocket: scrollback sent (%d bytes after trim)", len(validOutput))
	} else if err != nil {
		log.Printf("Terminal WebSocket: failed to get scrollback: %v", err)
	} else {
		log.Printf("Terminal WebSocket: no scrollback data")
	}

	// Position cursor using the tmux cursor position (relative to visible viewport)
	if cursorErr == nil {
		// ANSI escape: ESC [ row ; col H (1-based coordinates)
		cursorEsc := fmt.Sprintf("\x1b[%d;%dH", cursorY+1, cursorX+1)
		writeMu.Lock()
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		conn.WriteMessage(websocket.TextMessage, []byte(cursorEsc))
		writeMu.Unlock()
		log.Printf("Terminal WebSocket: positioned cursor at row=%d col=%d", cursorY+1, cursorX+1)
	}

	// Set up pipe-pane for streaming output
	// Use unique pipe name per connection to avoid FIFO file conflicts
	// NOTE: tmux pipe-pane only supports one output destination per pane, so
	// a new connection will take over streaming from any existing connection.
	// Multiple viewers of the same pane are not supported.
	pipeName := fmt.Sprintf("/tmp/trellis-pipe-%s-%s-%d.fifo", terminal.ToTmuxSessionName(session), window, time.Now().UnixNano())

	// Stop any existing pipe-pane (takes over streaming from previous client)
	exec.Command("tmux", "pipe-pane", "-t", target, "").Run()
	log.Printf("Terminal WebSocket: setting up pipe-pane for %s (new viewer takes over)", target)

	// Create named pipe
	if err := exec.Command("mkfifo", pipeName).Run(); err != nil {
		log.Printf("Terminal WebSocket: failed to create pipe: %v", err)
		writeMu.Lock()
		conn.WriteMessage(websocket.TextMessage, []byte("Error: failed to create pipe\r\n"))
		writeMu.Unlock()
		return
	}
	defer exec.Command("rm", "-f", pipeName).Run()
	defer exec.Command("tmux", "pipe-pane", "-t", target, "").Run()

	// Start pipe-pane to capture output (use cat like runner does)
	// Quote pipeName to handle any shell-sensitive characters in session/window names
	pipeCmd := fmt.Sprintf("cat >> '%s'", pipeName)
	if err := exec.Command("tmux", "pipe-pane", "-t", target, "-o", pipeCmd).Run(); err != nil {
		log.Printf("Terminal WebSocket: failed to start pipe-pane: %v", err)
		writeMu.Lock()
		conn.WriteMessage(websocket.TextMessage, []byte("Error: failed to start pipe-pane\r\n"))
		writeMu.Unlock()
		return
	}

	// Read from pipe in goroutine
	// Goroutine exits naturally when pipe-pane is stopped (defer above), which closes
	// the write end of the pipe and causes Read to return EOF
	go func() {
		file, err := os.Open(pipeName)
		if err != nil {
			log.Printf("Terminal WebSocket: failed to open pipe: %v", err)
			return
		}
		defer file.Close()

		// Use a buffered reader to handle UTF-8 properly (matches runner)
		reader := bufio.NewReader(file)
		buf := make([]byte, 4096)

		for {
			n, err := reader.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("Terminal WebSocket: pipe read error: %v", err)
				}
				return
			}
			if n > 0 {
				validUTF8 := strings.ToValidUTF8(string(buf[:n]), "")
				writeMu.Lock()
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				err := conn.WriteMessage(websocket.TextMessage, []byte(validUTF8))
				writeMu.Unlock()
				if err != nil {
					log.Printf("Terminal WebSocket: write error: %v", err)
					return
				}
			}
		}
	}()

	// Rate limiter for input
	var lastInput time.Time

	// Read from WebSocket and send to tmux
	for {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Terminal WebSocket: unexpected close: %v", err)
			}
			break
		}

		if messageType == websocket.TextMessage {
			var msg terminalMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				log.Printf("Terminal WebSocket: failed to parse JSON: %v", err)
				continue
			}

			switch msg.Type {
			case "input":
				// Strip bracketed paste mode sequences (matches runner's approach)
				data := msg.Data
				if strings.HasPrefix(data, "00~") && strings.HasSuffix(data, "01~") {
					data = strings.TrimPrefix(data, "00~")
					data = strings.TrimSuffix(data, "01~")
				}
				// Also check for full escape sequences
				if strings.HasPrefix(data, "\x1b[200~") && strings.HasSuffix(data, "\x1b[201~") {
					data = strings.TrimPrefix(data, "\x1b[200~")
					data = strings.TrimSuffix(data, "\x1b[201~")
				}

				// Rate limit (outside mutex so other terminals aren't blocked)
				if time.Since(lastInput) < time.Millisecond {
					time.Sleep(time.Millisecond)
				}

				// Serialize tmux commands per target pane
				targetMu := getTargetMutex(target)
				targetMu.Lock()

				// Send input to tmux (with 5s timeout to prevent hangs)
				cmdCtx, cmdCancel := context.WithTimeout(context.Background(), 5*time.Second)
				if data == "\r" {
					// Enter key - execute command
					exec.CommandContext(cmdCtx, "tmux", "send-keys", "-t", target, "Enter").Run()
				} else if data == "\n" {
					// Newline (Shift-Enter) - add without executing
					exec.CommandContext(cmdCtx, "tmux", "send-keys", "-t", target, "-l", data).Run()
				} else if len(data) == 1 && data[0] < 32 {
					// Control character (Ctrl+A through Ctrl+Z, etc.)
					// Use send-keys with tmux key name format: C-a, C-b, etc.
					ctrlChar := data[0]
					var keyName string
					if ctrlChar < 27 {
						// Ctrl+A (1) through Ctrl+Z (26)
						keyName = fmt.Sprintf("C-%c", 'a'+ctrlChar-1)
					} else {
						// Other control chars - send literally
						keyName = fmt.Sprintf("C-%c", ctrlChar+64)
					}
					exec.CommandContext(cmdCtx, "tmux", "send-keys", "-t", target, keyName).Run()
				} else if strings.HasPrefix(data, "\x1b") {
					// Escape sequence (arrow keys, function keys, etc.)
					// Send literally so tmux interprets it
					exec.CommandContext(cmdCtx, "tmux", "send-keys", "-t", target, "-l", data).Run()
				} else {
					// Regular text - use load-buffer/paste-buffer for special chars like semicolons
					cmd := exec.CommandContext(cmdCtx, "tmux", "load-buffer", "-")
					cmd.Stdin = strings.NewReader(data)
					if err := cmd.Run(); err != nil {
						log.Printf("Terminal: failed to load buffer: %v", err)
					} else {
						exec.CommandContext(cmdCtx, "tmux", "paste-buffer", "-d", "-t", target).Run()
					}
				}
				cmdCancel()

				lastInput = time.Now()
				targetMu.Unlock()

			case "resize":
				if msg.Cols > 0 && msg.Rows > 0 {
					log.Printf("Terminal WebSocket: resize %s:%s to %dx%d", session, window, msg.Cols, msg.Rows)
					h.mgr.Resize(ctx, session, window, msg.Cols, msg.Rows)
				}
			}
		}
	}
}

// prepareRemoteCommand prepares a remote command for proper terminal operation.
// For SSH commands running tmux, it enables mouse mode so scrolling works in xterm.js.
func prepareRemoteCommand(cmd []string) []string {
	if len(cmd) == 0 {
		return cmd
	}

	result := make([]string, len(cmd))
	copy(result, cmd)

	// Look for tmux commands and enable mouse mode for scrolling
	for i, arg := range result {
		if strings.Contains(arg, "tmux") && (strings.Contains(arg, "new") || strings.Contains(arg, "attach") || strings.Contains(arg, "new-session")) {
			// Check if mouse is already configured
			if !strings.Contains(arg, "mouse") {
				// Append mouse mode to tmux command
				// Handle both 'tmux new ...' and 'tmux new ... \; set ...' patterns
				if strings.Contains(arg, "\\;") || strings.Contains(arg, " ;") {
					// Already has set commands, append mouse mode
					result[i] = arg + " \\; set -g mouse on"
				} else {
					// No set commands yet, add one
					result[i] = arg + " \\; set -g mouse on"
				}
			}
			break
		}
	}

	return result
}

// isTimeoutError checks if the error is a timeout error.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "timeout") ||
		strings.Contains(err.Error(), "i/o timeout")
}
