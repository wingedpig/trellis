// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/terminal"
	"github.com/wingedpig/trellis/internal/worktree"
)

// VSCodeConfig holds VS Code/code-server configuration.
type VSCodeConfig struct {
	Binary      string
	Port        int
	UserDataDir string // Path to VS Code user data (settings, keybindings, etc.)
}

// VSCodeHandler handles VS Code proxy requests.
type VSCodeHandler struct {
	config      VSCodeConfig
	proxy       *httputil.ReverseProxy
	workdir     string
	worktreeMgr worktree.Manager

	mu      sync.Mutex
	cmd     *exec.Cmd
	started bool
}

// NewVSCodeHandler creates a new VS Code handler.
func NewVSCodeHandler(config VSCodeConfig, workdir string, worktreeMgr worktree.Manager) *VSCodeHandler {
	if config.Port == 0 {
		config.Port = 8443
	}
	if config.Binary == "" {
		config.Binary = "code-server"
	}

	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", config.Port),
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Custom director to handle path rewriting and proxy headers
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Strip /vscode prefix
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/vscode")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		// Set proxy headers for code-server
		req.Header.Set("X-Forwarded-Host", req.Host)
		req.Header.Set("X-Forwarded-Proto", "http")
		if req.TLS != nil {
			req.Header.Set("X-Forwarded-Proto", "https")
		}
		// Preserve origin for WebSocket
		if origin := req.Header.Get("Origin"); origin != "" {
			req.Header.Set("X-Forwarded-Origin", origin)
		}
		req.Host = target.Host
	}

	// Enable response flushing for streaming
	proxy.FlushInterval = -1

	// Modify response headers to fix redirects
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Rewrite Location headers to include /vscode prefix
		if loc := resp.Header.Get("Location"); loc != "" {
			if strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "/vscode") {
				resp.Header.Set("Location", "/vscode"+loc)
			}
		}
		return nil
	}

	// Handle errors gracefully
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("VSCode proxy error: %v", err)
		http.Error(w, "VS Code is not available. Is code-server running?", http.StatusBadGateway)
	}

	return &VSCodeHandler{
		config:      config,
		proxy:       proxy,
		workdir:     workdir,
		worktreeMgr: worktreeMgr,
	}
}

// Start starts the code-server subprocess.
func (h *VSCodeHandler) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.started {
		return nil
	}

	// Check if code-server binary exists
	path, err := exec.LookPath(h.config.Binary)
	if err != nil {
		return fmt.Errorf("code-server binary not found: %s", h.config.Binary)
	}

	args := []string{
		"--bind-addr", fmt.Sprintf("127.0.0.1:%d", h.config.Port),
		"--auth", "none", // Trellis handles auth
		"--disable-telemetry",
		"--disable-update-check",
		"--disable-workspace-trust",
	}
	if h.config.UserDataDir != "" {
		// Expand ~ to home directory
		userDataDir := h.config.UserDataDir
		if strings.HasPrefix(userDataDir, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				userDataDir = home + userDataDir[1:]
			}
		}
		args = append(args, "--user-data-dir", userDataDir)
	}
	if h.workdir != "" {
		args = append(args, h.workdir)
	}

	h.cmd = exec.CommandContext(ctx, path, args...)
	h.cmd.Stdout = os.Stdout
	h.cmd.Stderr = os.Stderr

	if err := h.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start code-server: %w", err)
	}

	h.started = true

	// Wait for code-server to be ready
	if err := h.waitForReady(ctx, 30*time.Second); err != nil {
		h.Stop()
		return fmt.Errorf("code-server failed to start: %w", err)
	}

	log.Printf("code-server started on port %d", h.config.Port)
	return nil
}

// waitForReady waits for code-server to be accessible.
func (h *VSCodeHandler) waitForReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", h.config.Port)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for code-server on %s", addr)
}

// Stop stops the code-server subprocess.
func (h *VSCodeHandler) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.cmd != nil && h.cmd.Process != nil {
		h.cmd.Process.Kill()
		h.cmd.Wait()
	}

	h.started = false
	h.cmd = nil
	return nil
}

// IsStarted returns whether code-server has been started.
func (h *VSCodeHandler) IsStarted() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.started
}

// ServeHTTP proxies requests to code-server.
func (h *VSCodeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.IsStarted() {
		http.Error(w, "VS Code is not available. code-server is not running.", http.StatusServiceUnavailable)
		return
	}

	// If accessing /vscode/ or /vscode without a folder param, redirect to include the folder
	path := strings.TrimPrefix(r.URL.Path, "/vscode")
	if (path == "" || path == "/") && r.URL.Query().Get("folder") == "" {
		// Determine folder from session parameter or fall back to workdir
		folder := h.workdir
		if session := r.URL.Query().Get("session"); session != "" && h.worktreeMgr != nil {
			// Look up the worktree by session name
			// Session names are the worktree directory name with dots converted to underscores
			// e.g., worktree "groups.io-logchanges" -> session "groups_io-logchanges"
			worktrees, _ := h.worktreeMgr.List()
			found := false
			for _, wt := range worktrees {
				// Convert worktree directory name to tmux session format
				expectedSession := terminal.ToTmuxSessionName(wt.Name())
				if expectedSession == session {
					folder = wt.Path
					found = true
					log.Printf("VSCode: using worktree path for session %s: %s", session, folder)
					break
				}
			}
			if !found {
				log.Printf("VSCode: session %s not found, using default workdir", session)
			}
		}

		if folder != "" {
			redirectURL := "/vscode/?folder=" + url.QueryEscape(folder)

			// If file parameter is provided, we need to open the file at a specific line
			// code-server doesn't have great URL support for this, so we pass it via a
			// custom parameter that the frontend can use to execute a command
			if file := r.URL.Query().Get("file"); file != "" {
				// Build full path if relative
				fullPath := file
				if !strings.HasPrefix(file, "/") {
					fullPath = folder + "/" + file
				}
				line := r.URL.Query().Get("line")
				col := r.URL.Query().Get("col")

				// Pass file info to frontend - code-server will need to handle this
				// Try multiple approaches:
				// 1. Use the 'goto' parameter (may work in newer versions)
				gotoPath := fullPath
				if line != "" {
					gotoPath += ":" + line
					if col != "" {
						gotoPath += ":" + col
					}
				}
				redirectURL += "&goto=" + url.QueryEscape(gotoPath)
			}

			log.Printf("VSCode redirect: %s -> %s", r.URL.Path, redirectURL)
			http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
			return
		}
	}

	h.proxy.ServeHTTP(w, r)
}

// OpenFile opens a file at a specific line/column using code-server -r
func (h *VSCodeHandler) OpenFile(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Query().Get("file")
	line := r.URL.Query().Get("line")
	col := r.URL.Query().Get("col")
	session := r.URL.Query().Get("session")

	if file == "" {
		http.Error(w, "file parameter required", http.StatusBadRequest)
		return
	}

	// Determine the folder from session
	folder := h.workdir
	if session != "" && h.worktreeMgr != nil {
		worktrees, _ := h.worktreeMgr.List()
		for _, wt := range worktrees {
			if terminal.ToTmuxSessionName(wt.Name()) == session {
				folder = wt.Path
				break
			}
		}
	}

	// Build full path if relative
	fullPath := file
	if !strings.HasPrefix(file, "/") && folder != "" {
		fullPath = folder + "/" + file
	}

	// Add line:col if provided
	target := fullPath
	if line != "" {
		target += ":" + line
		if col != "" {
			target += ":" + col
		}
	}

	// Use code-server -r to open the file in the running instance
	cmd := exec.Command(h.config.Binary, "-r", target)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("VSCode OpenFile error: %v, output: %s", err, output)
		http.Error(w, fmt.Sprintf("Failed to open file: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("VSCode: opened %s", target)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success": true}`))
}

// CheckBinary verifies that code-server binary exists.
func CheckCodeServerBinary(binary string) error {
	if binary == "" {
		binary = "code-server"
	}
	_, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("code-server binary not found: %s. Install from https://coder.com/docs/code-server/install", binary)
	}
	return nil
}
