// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/wingedpig/trellis/internal/cases"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/worktree"
)

// ClaudeHandler handles Claude Code WebSocket connections.
type ClaudeHandler struct {
	manager     *claude.Manager
	worktreeMgr worktree.Manager
	caseMgr     *cases.Manager
}

// NewClaudeHandler creates a new Claude handler.
func NewClaudeHandler(manager *claude.Manager, worktreeMgr worktree.Manager, caseMgr *cases.Manager) *ClaudeHandler {
	return &ClaudeHandler{
		manager:     manager,
		worktreeMgr: worktreeMgr,
		caseMgr:     caseMgr,
	}
}

// clientMessage is a message from the client.
type clientMessage struct {
	Type    string          `json:"type"`
	Content string          `json:"content,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"` // For permission responses
}

// serverMessage is a message to the client.
type serverMessage struct {
	Type                     string              `json:"type"`
	Messages                 []claude.Message    `json:"messages,omitempty"`
	Event                    *claude.StreamEvent `json:"event,omitempty"`
	Message                  string              `json:"message,omitempty"`
	Generating               bool                `json:"generating,omitempty"`
	InputTokens              int                 `json:"input_tokens,omitempty"`
	InputTokensBase          int                 `json:"input_tokens_base,omitempty"`
	CacheCreationInputTokens int                 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                 `json:"cache_read_input_tokens,omitempty"`
	SlashCommands            []string            `json:"slash_commands,omitempty"`
	Skills                   []string            `json:"skills,omitempty"`
}

// WebSocket handles a Claude chat WebSocket connection for a specific session.
func (h *ClaudeHandler) WebSocket(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	session := h.manager.GetSession(sessionID)
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	h.serveSession(w, r, session)
}

// WebSocketByWorktree handles a Claude chat WebSocket using the first/default session for a worktree.
func (h *ClaudeHandler) WebSocketByWorktree(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]

	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		http.Error(w, "worktree not found", http.StatusNotFound)
		return
	}

	session := h.manager.GetOrCreateSession(worktreeName, wt.Path)
	h.serveSession(w, r, session)
}

// serveSession runs the WebSocket loop for a given session.
func (h *ClaudeHandler) serveSession(w http.ResponseWriter, r *http.Request, session *claude.Session) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Write mutex for thread-safe WebSocket writes
	var writeMu sync.Mutex
	writeJSON := func(msg serverMessage) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(msg)
	}

	// Send conversation history (including any in-progress assistant turn) and cached slash commands
	cmds, skills := session.SlashCommands()
	base, cacheCreate, cacheRead := session.TokenBreakdown()
	writeJSON(serverMessage{
		Type:                     "history",
		Messages:                 session.MessagesWithPending(),
		Generating:               session.IsGenerating(),
		InputTokens:              session.InputTokens(),
		InputTokensBase:          base,
		CacheCreationInputTokens: cacheCreate,
		CacheReadInputTokens:     cacheRead,
		SlashCommands:            cmds,
		Skills:                   skills,
	})

	// Re-send pending permission prompt if one was waiting when the client disconnected
	if pending := session.PendingControlRequest(); pending != nil {
		writeJSON(serverMessage{Type: "stream", Event: pending})
	}

	// Subscribe BEFORE starting the process so the init event is captured
	subCh := session.Subscribe()
	go func() {
		for event := range subCh {
			writeJSON(serverMessage{Type: "stream", Event: &event})
			// When a turn completes, notify the client
			if event.Type == "result" {
				writeJSON(serverMessage{Type: "done"})
			}
		}
	}()

	// Start the claude process eagerly so init event (with slash commands) arrives immediately
	if err := session.EnsureProcess(context.Background()); err != nil {
		log.Printf("claude: failed to start process for session %s: %v", session.ID(), err)
	}

	// Set up ping/pong
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	pingTicker := time.NewTicker(54 * time.Second)
	defer pingTicker.Stop()

	go func() {
		for range pingTicker.C {
			writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	// Read client messages into a channel so the main loop is non-blocking
	readCh := make(chan clientMessage, 10)
	wsClosed := make(chan struct{})
	go func() {
		defer close(wsClosed)
		for {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg clientMessage
			if json.Unmarshal(msgBytes, &msg) == nil {
				readCh <- msg
			}
		}
	}()

	// Main event loop
	for {
		select {
		case msg := <-readCh:
			switch msg.Type {
			case "message":
				if msg.Content == "" || session.IsGenerating() {
					continue
				}

				writeJSON(serverMessage{Type: "status", Generating: true})

				// Send is non-blocking — writes to stdin and returns.
				// Response events arrive via the subscriber channel.
				if err := session.Send(context.Background(), msg.Content); err != nil {
					log.Printf("claude: send error for session %s: %v", session.ID(), err)
					writeJSON(serverMessage{Type: "error", Message: err.Error()})
					writeJSON(serverMessage{Type: "done"})
				}

			case "permission_response":
				// Forward permission response to claude's stdin
				session.ClearPendingControlRequest()
				if msg.Data != nil {
					if err := session.WriteStdinRaw(msg.Data); err != nil {
						log.Printf("claude: permission response error: %v", err)
					}
				}

			case "cancel":
				session.Cancel()
				writeJSON(serverMessage{Type: "done"})

			case "reset":
				session.Cancel()
				session.Reset()
				writeJSON(serverMessage{Type: "history", Messages: nil})
			}

		case <-wsClosed:
			session.Unsubscribe(subCh)
			return
		}
	}
}

// ExportSessionAPI exports a Claude session as a transcript.
func (h *ClaudeHandler) ExportSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	level := r.URL.Query().Get("level")
	if level == "" {
		level = "full"
	}
	if level != "full" && level != "summary" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "level must be 'full' or 'summary'")
		return
	}

	transcript, err := h.manager.ExportSession(sessionID, level)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(transcript)
}

// ImportSessionAPI imports a transcript into a worktree, creating a new session.
func (h *ClaudeHandler) ImportSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]

	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	var transcript claude.Transcript
	if err := json.NewDecoder(r.Body).Decode(&transcript); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}

	session, err := h.manager.ImportSession(worktreeName, wt.Path, wt.Branch, &transcript)
	if err != nil {
		if _, ok := err.(*claude.TranscriptError); ok {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, session.Info())
}

// ListSessions returns all Claude sessions for a worktree.
func (h *ClaudeHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]

	sessions := h.manager.ListSessions(worktreeName)
	WriteJSON(w, http.StatusOK, sessions)
}

// CreateSessionAPI creates a new Claude session for a worktree.
func (h *ClaudeHandler) CreateSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]

	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		http.Error(w, "worktree not found", http.StatusNotFound)
		return
	}

	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		WriteError(w, http.StatusBadRequest, ErrInternalError, "invalid JSON: "+err.Error())
		return
	}

	session := h.manager.CreateSession(worktreeName, wt.Path, body.DisplayName)
	WriteJSON(w, http.StatusCreated, session.Info())
}

// RenameSessionAPI renames a Claude session's display name.
func (h *ClaudeHandler) RenameSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, ErrInternalError, "invalid JSON: "+err.Error())
		return
	}
	if body.DisplayName == "" {
		http.Error(w, "display_name required", http.StatusBadRequest)
		return
	}

	if err := h.manager.RenameSession(sessionID, body.DisplayName); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteSessionAPI moves a Claude session to trash (soft delete).
func (h *ClaudeHandler) DeleteSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	if err := h.manager.TrashSession(sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListTrashedSessionsAPI returns all trashed Claude sessions for a worktree.
func (h *ClaudeHandler) ListTrashedSessionsAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]

	sessions := h.manager.ListTrashedSessions(worktreeName)
	WriteJSON(w, http.StatusOK, sessions)
}

// RestoreSessionAPI restores a trashed Claude session.
func (h *ClaudeHandler) RestoreSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	if err := h.manager.RestoreSession(sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PermanentDeleteSessionAPI permanently deletes a Claude session.
func (h *ClaudeHandler) PermanentDeleteSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	session := h.manager.GetSession(sessionID)
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	h.manager.DeleteSession(sessionID)
	w.WriteHeader(http.StatusNoContent)
}

// GitStatus returns the git status for a worktree.
func (h *ClaudeHandler) GitStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]

	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	ctx := r.Context()
	git := worktree.NewRealGitExecutor()
	status, err := git.Status(ctx, wt.Path)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "git status failed: "+err.Error())
		return
	}

	branchInfo, _ := git.BranchInfo(ctx, wt.Path)
	branch := branchInfo.Name
	if branchInfo.Detached {
		branch = branchInfo.Commit
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"clean":     status.Clean,
		"modified":  status.Modified,
		"added":     status.Added,
		"deleted":   status.Deleted,
		"renamed":   status.Renamed,
		"untracked": status.Untracked,
		"branch":    branch,
	})
}

// SessionCase checks if a session is already linked to an open case.
func (h *ClaudeHandler) SessionCase(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]

	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "session_id query parameter is required")
		return
	}

	if h.caseMgr == nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, "case manager not configured")
		return
	}

	c := h.caseMgr.FindCaseBySession(wt.Path, sessionID)
	if c == nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, "no open case linked to this session")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"case_id": c.ID,
		"title":   c.Title,
		"kind":    c.Kind,
	})
}

// wrapUpRequest is the request body for the WrapUp handler.
type wrapUpRequest struct {
	SessionID     string           `json:"session_id"`
	CaseID        string           `json:"case_id"`
	Title         string           `json:"title"`
	Kind          string           `json:"kind"`
	CommitMessage string           `json:"commit_message"`
	Files         []string         `json:"files"`
	Links         []cases.CaseLink `json:"links"`
}

// WrapUp orchestrates the full wrap-up workflow: create/update case, update transcripts, archive, commit.
func (h *ClaudeHandler) WrapUp(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]

	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	var req wrapUpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.CommitMessage == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "commit_message is required")
		return
	}

	if h.caseMgr == nil {
		WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "case manager not configured")
		return
	}

	ctx := r.Context()
	var caseID string

	// Step 1: Create or load case
	if req.CaseID != "" {
		// Use existing case
		if _, err := h.caseMgr.Get(wt.Path, req.CaseID); err != nil {
			WriteError(w, http.StatusNotFound, ErrNotFound, fmt.Sprintf("case not found: %s", req.CaseID))
			return
		}
		caseID = req.CaseID
	} else {
		// Create new case + save transcript
		if req.Title == "" {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "title is required when creating a new case")
			return
		}
		if req.Kind == "" {
			req.Kind = "task"
		}

		c, err := h.caseMgr.Create(wt.Path, req.Title, req.Kind, worktreeName, wt.Branch, wt.Commit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, ErrInternalError, "create case: "+err.Error())
			return
		}
		caseID = c.ID

		// Save transcript for the session
		if req.SessionID != "" && h.manager != nil {
			transcript, err := h.manager.ExportSession(req.SessionID, "full")
			if err == nil {
				refID := uuid.New().String()[:8]
				_ = h.caseMgr.SaveTranscript(wt.Path, caseID, refID, req.Title, req.SessionID, transcript)
			}
		}
	}

	// Step 2: Update ALL transcripts linked to the case
	if h.manager != nil {
		if c, err := h.caseMgr.Get(wt.Path, caseID); err == nil {
			for _, ref := range c.Claude {
				if ref.SourceSessionID == "" {
					continue
				}
				transcript, err := h.manager.ExportSession(ref.SourceSessionID, "full")
				if err != nil {
					continue
				}
				_ = h.caseMgr.UpdateTranscript(wt.Path, caseID, ref.ID, transcript)
			}
		}
	}

	// Step 3: Merge links
	if len(req.Links) > 0 {
		if c, err := h.caseMgr.Get(wt.Path, caseID); err == nil {
			merged := c.Links
			for _, newLink := range req.Links {
				merged = append(merged, newLink)
			}
			_ = h.caseMgr.Update(wt.Path, caseID, cases.CaseUpdate{Links: merged})
		}
	}

	// Step 4: Archive case
	if err := h.caseMgr.Archive(wt.Path, caseID); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "archive case: "+err.Error())
		return
	}

	// Step 5: git add selected files + archived case directory
	archivedCaseRelDir := filepath.Join(h.caseMgr.ArchivedRelDir(), caseID)
	gitArgs := []string{"-C", wt.Path, "add", archivedCaseRelDir}
	for _, f := range req.Files {
		// Sanitize: prevent path traversal
		if strings.Contains(f, "..") {
			continue
		}
		gitArgs = append(gitArgs, f)
	}
	if _, err := worktree.RunCommand(ctx, gitArgs...); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "git add: "+err.Error())
		return
	}

	// Step 6: git commit
	commitOut, err := worktree.RunCommand(ctx, "-C", wt.Path, "commit", "-m", req.CommitMessage)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "git commit: "+commitOut+" "+err.Error())
		return
	}

	// Extract commit hash
	commitHash := ""
	hashOut, err := worktree.RunCommand(ctx, "-C", wt.Path, "rev-parse", "--short", "HEAD")
	if err == nil {
		commitHash = strings.TrimSpace(hashOut)
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"case_id":     caseID,
		"commit_hash": commitHash,
	})
}
