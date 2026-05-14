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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/wingedpig/trellis/internal/cases"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/trace"
	"github.com/wingedpig/trellis/internal/worktree"
)

// CodexHandler handles Codex chat WebSocket and HTTP endpoints. Mirrors
// ClaudeHandler in shape; the per-event details differ because Codex's
// app-server protocol uses thread/turn/item events rather than Claude's
// NDJSON content blocks.
type CodexHandler struct {
	manager     *codex.Manager
	claudeMgr   *claude.Manager // for cross-agent operations like wrap-up capturing related claude sessions
	worktreeMgr worktree.Manager
	caseMgr     *cases.Manager
	traceMgr    *trace.Manager
	bus         events.EventBus
}

// NewCodexHandler creates a new Codex handler.
func NewCodexHandler(manager *codex.Manager, claudeMgr *claude.Manager, worktreeMgr worktree.Manager, caseMgr *cases.Manager, traceMgr *trace.Manager, bus events.EventBus) *CodexHandler {
	return &CodexHandler{
		manager:     manager,
		claudeMgr:   claudeMgr,
		worktreeMgr: worktreeMgr,
		caseMgr:     caseMgr,
		traceMgr:    traceMgr,
		bus:         bus,
	}
}

// codexClientMessage is a message from the JS client.
type codexClientMessage struct {
	Type      string `json:"type"`
	Content   string `json:"content,omitempty"`
	RequestID string `json:"request_id,omitempty"` // for approval responses
	Decision  string `json:"decision,omitempty"`   // "accept" | "acceptForSession" | "decline" | "cancel"
}

// codexServerMessage is a message to the JS client.
type codexServerMessage struct {
	Type       string             `json:"type"`
	Messages   []codex.Message    `json:"messages,omitempty"`
	Event      *codex.StreamEvent `json:"event,omitempty"`
	Generating bool               `json:"generating,omitempty"`
	Message    string             `json:"message,omitempty"`
	TokenUsage *codex.TokenUsage  `json:"token_usage,omitempty"`
}

// WebSocket handles a chat WebSocket for a specific session.
func (h *CodexHandler) WebSocket(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	session := h.manager.GetSession(vars["session"])
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	h.serveSession(w, r, session)
}

// WebSocketByWorktree falls back to the first/default session for a worktree.
func (h *CodexHandler) WebSocketByWorktree(w http.ResponseWriter, r *http.Request) {
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

func (h *CodexHandler) serveSession(w http.ResponseWriter, r *http.Request, session *codex.Session) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var writeMu sync.Mutex
	writeJSON := func(msg codexServerMessage) error {
		data, err := json.Marshal(msg)
		if err != nil {
			log.Printf("codex: marshal server msg (type=%s): %v", msg.Type, err)
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, data)
	}

	// Initial history dump including in-progress turn + last token usage.
	// MessagesForWire caps oversized command output / diffs so we don't ship
	// tens of MB and lock up the browser's main thread on JSON.parse + GC.
	// Truncated items carry markers; the client fetches full content lazily
	// via /api/v1/codex/sessions/{id}/items/{itemId}/output.
	usage := session.TokenUsage()
	writeJSON(codexServerMessage{
		Type:       "history",
		Messages:   session.MessagesForWire(),
		Generating: session.IsGenerating(),
		TokenUsage: &usage,
	})

	// Re-send any pending approval prompts.
	for _, ev := range session.PendingApprovals() {
		writeJSON(codexServerMessage{Type: "stream", Event: &ev})
	}

	// Subscribe BEFORE EnsureProcess so thread_started is captured.
	subCh := session.Subscribe()
	go func() {
		for ev := range subCh {
			writeJSON(codexServerMessage{Type: "stream", Event: &ev})
			if ev.Type == "turn_completed" || ev.Type == "turn_failed" {
				writeJSON(codexServerMessage{Type: "done"})
			}
		}
	}()

	// Eagerly start app-server so thread/start runs before first user input.
	if err := session.EnsureProcess(context.Background()); err != nil {
		log.Printf("codex: EnsureProcess for %s: %v", session.ID(), err)
		writeJSON(codexServerMessage{Type: "error", Message: err.Error()})
	}

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

	readCh := make(chan codexClientMessage, 10)
	wsClosed := make(chan struct{})
	go func() {
		defer close(wsClosed)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg codexClientMessage
			if json.Unmarshal(raw, &msg) == nil {
				readCh <- msg
			}
		}
	}()

	for {
		select {
		case msg := <-readCh:
			switch msg.Type {
			case "message":
				if msg.Content == "" || session.IsGenerating() {
					continue
				}
				writeJSON(codexServerMessage{Type: "status", Generating: true})
				if err := session.Send(context.Background(), msg.Content); err != nil {
					log.Printf("codex: send error for %s: %v", session.ID(), err)
					writeJSON(codexServerMessage{Type: "error", Message: err.Error()})
					writeJSON(codexServerMessage{Type: "done"})
				}
			case "approval_response":
				if msg.RequestID == "" || msg.Decision == "" {
					continue
				}
				if err := session.AnswerApproval(msg.RequestID, msg.Decision); err != nil {
					log.Printf("codex: approval error: %v", err)
				}
			case "cancel":
				session.Cancel()
				writeJSON(codexServerMessage{Type: "done"})
			}
		case <-wsClosed:
			session.Unsubscribe(subCh)
			return
		}
	}
}

// ListSessions returns all Codex sessions for a worktree.
func (h *CodexHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	WriteJSON(w, http.StatusOK, h.manager.ListSessions(vars["worktree"]))
}

// ItemOutput returns the full Output / Diff content for a single item.
// The initial history dump caps these fields to keep the wire payload small;
// the client calls this on first expand to fetch the full content.
func (h *CodexHandler) ItemOutput(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	session := h.manager.GetSession(vars["session"])
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	item, ok := session.FindItem(vars["item"])
	if !ok {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{
		"output": item.Output,
		"diff":   item.Diff,
	})
}

// CreateSessionAPI creates a new Codex session for a worktree.
func (h *CodexHandler) CreateSessionAPI(w http.ResponseWriter, r *http.Request) {
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

// RenameSessionAPI updates a session's display name.
func (h *CodexHandler) RenameSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
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
	if err := h.manager.RenameSession(vars["session"], body.DisplayName); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteSessionAPI moves a session to trash (soft delete).
func (h *CodexHandler) DeleteSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	if err := h.manager.TrashSession(vars["session"]); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PermanentDeleteSessionAPI permanently removes a session.
func (h *CodexHandler) PermanentDeleteSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	if h.manager.GetSession(vars["session"]) == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	h.manager.DeleteSession(vars["session"])
	w.WriteHeader(http.StatusNoContent)
}

// RestoreSessionAPI brings a trashed session back.
func (h *CodexHandler) RestoreSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	if err := h.manager.RestoreSession(vars["session"]); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListTrashedSessionsAPI returns trashed sessions for a worktree.
func (h *CodexHandler) ListTrashedSessionsAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	WriteJSON(w, http.StatusOK, h.manager.ListTrashedSessions(vars["worktree"]))
}

// ForkSessionAPI creates a new session pre-populated with messages[0..idx].
func (h *CodexHandler) ForkSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	var body struct {
		MessageIndex int    `json:"message_index"`
		DisplayName  string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.MessageIndex < 0 {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "message_index must be >= 0")
		return
	}
	session, err := h.manager.ForkSession(vars["session"], body.MessageIndex, body.DisplayName)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, session.Info())
}

// ExportSessionAPI exports a session as a transcript.
func (h *CodexHandler) ExportSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	level := r.URL.Query().Get("level")
	if level == "" {
		level = "full"
	}
	if level != "full" && level != "summary" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "level must be 'full' or 'summary'")
		return
	}
	t, err := h.manager.ExportSession(vars["session"], level)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(t)
}

// GitStatus returns the git status for a worktree. Mirrors the Claude handler
// — kept as a Codex-namespaced endpoint so the move-session UI can reach it
// without a worktree-level dependency from a Codex chat page.
func (h *CodexHandler) GitStatus(w http.ResponseWriter, r *http.Request) {
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

// MoveSessionAPI moves a Codex session to a fresh worktree along with
// selected uncommitted files from the source worktree. Same shape as
// Claude's MoveSessionAPI; helper functions safeJoinInsideRoot /
// copyFilePreserveMode are shared at the handlers package level.
func (h *CodexHandler) MoveSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	var body struct {
		Branch string   `json:"branch"`
		Files  []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Branch == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "branch is required")
		return
	}

	session := h.manager.GetSession(sessionID)
	if session == nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, "session not found")
		return
	}
	sourceWorktreeName := session.WorktreeName()
	sourceWT, ok := h.worktreeMgr.GetByName(sourceWorktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "source worktree not found")
		return
	}

	type selectedFile struct {
		rel       string
		srcAbs    string
		untracked bool
	}
	selected := make([]selectedFile, 0, len(body.Files))
	for _, f := range body.Files {
		if f == "" {
			continue
		}
		abs, err := safeJoinInsideRoot(sourceWT.Path, f)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("invalid file %q: %s", f, err.Error()))
			return
		}
		info, err := os.Lstat(abs)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("file %q not found", f))
			return
		}
		if info.Mode()&os.ModeSymlink != 0 {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("file %q is a symlink (unsupported)", f))
			return
		}
		if info.IsDir() {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("file %q is a directory (unsupported)", f))
			return
		}
		selected = append(selected, selectedFile{
			rel:    filepath.ToSlash(filepath.Clean(f)),
			srcAbs: abs,
		})
	}

	ctx := r.Context()
	git := worktree.NewRealGitExecutor()
	status, err := git.Status(ctx, sourceWT.Path)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "git status failed: "+err.Error())
		return
	}
	untrackedSet := make(map[string]struct{}, len(status.Untracked))
	for _, u := range status.Untracked {
		untrackedSet[filepath.ToSlash(filepath.Clean(u))] = struct{}{}
	}
	for i := range selected {
		if _, ok := untrackedSet[selected[i].rel]; ok {
			selected[i].untracked = true
		}
	}

	if err := h.worktreeMgr.Create(ctx, body.Branch, false); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "create worktree: "+err.Error())
		return
	}
	newWorktreeDir := h.worktreeMgr.ProjectName() + "-" + strings.ReplaceAll(body.Branch, "/", "-")
	newWT, ok := h.worktreeMgr.GetByName(newWorktreeDir)
	if !ok {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "new worktree not found after create")
		return
	}
	newWorktreeKey := strings.TrimPrefix(newWT.Name(), h.worktreeMgr.ProjectName()+"-")

	for _, sf := range selected {
		dstAbs := filepath.Join(newWT.Path, filepath.FromSlash(sf.rel))
		if err := copyFilePreserveMode(sf.srcAbs, dstAbs); err != nil {
			WriteError(w, http.StatusInternalServerError, ErrInternalError,
				fmt.Sprintf("copy %q: %s", sf.rel, err.Error()))
			return
		}
	}

	var revertErrs []string
	for _, sf := range selected {
		if sf.untracked {
			if err := os.Remove(sf.srcAbs); err != nil {
				revertErrs = append(revertErrs, fmt.Sprintf("%s: %s", sf.rel, err.Error()))
			}
			continue
		}
		if out, err := worktree.RunCommand(ctx, "-C", sourceWT.Path, "checkout", "--", sf.rel); err != nil {
			revertErrs = append(revertErrs, fmt.Sprintf("%s: %s %s", sf.rel, err.Error(), strings.TrimSpace(out)))
		}
	}

	if err := h.manager.MoveSession(sessionID, newWorktreeKey, newWT.Path); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "move session: "+err.Error())
		return
	}

	movedFiles := make([]string, len(selected))
	for i, sf := range selected {
		movedFiles[i] = sf.rel
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:     events.EventCodexSessionMoved,
			Worktree: newWorktreeKey,
			Payload: map[string]interface{}{
				"session_id":       sessionID,
				"from_worktree":    sourceWorktreeName,
				"to_worktree":      newWorktreeKey,
				"to_worktree_path": newWT.Path,
				"to_branch":        newWT.Branch,
				"files":            movedFiles,
				"revert_errors":    revertErrs,
			},
		})
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"session_id":    sessionID,
		"worktree":      newWorktreeKey,
		"branch":        newWT.Branch,
		"path":          newWT.Path,
		"revert_errors": revertErrs,
	})
}

// SessionCase checks if a session is already linked to an open case.
func (h *CodexHandler) SessionCase(w http.ResponseWriter, r *http.Request) {
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

	c := h.caseMgr.FindCaseByCodexSession(wt.Path, sessionID)
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

// ListTraceReports returns available trace report summaries (for the wrap-up modal).
func (h *CodexHandler) ListTraceReports(w http.ResponseWriter, r *http.Request) {
	if h.traceMgr == nil {
		WriteJSON(w, http.StatusOK, map[string]interface{}{"reports": []interface{}{}})
		return
	}
	reports, err := h.traceMgr.ListReports()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "list trace reports: "+err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"reports": reports})
}

// codexWrapUpRequest is the request body for the WrapUp handler.
type codexWrapUpRequest struct {
	SessionID       string              `json:"session_id"`
	CaseID          string              `json:"case_id"`
	Title           string              `json:"title"`
	Kind            string              `json:"kind"`
	CommitMessage   string              `json:"commit_message"`
	Files           []string            `json:"files"`
	Links           []cases.CaseLink    `json:"links"`
	Traces          []string            `json:"traces"`
	RelatedSessions []relatedSessionRef `json:"related_sessions,omitempty"`
}

// WrapUp orchestrates the full wrap-up workflow for a Codex session: create
// or load case, save/update transcript, archive case, commit. Mirrors
// ClaudeHandler.WrapUp.
func (h *CodexHandler) WrapUp(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]

	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	var req codexWrapUpRequest
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

	// Step 1: load existing or create new case
	if req.CaseID != "" {
		if _, err := h.caseMgr.Get(wt.Path, req.CaseID); err != nil {
			WriteError(w, http.StatusNotFound, ErrNotFound, fmt.Sprintf("case not found: %s", req.CaseID))
			return
		}
		caseID = req.CaseID
	} else {
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

		if req.SessionID != "" && h.manager != nil {
			transcript, err := h.manager.ExportSession(req.SessionID, "full")
			if err == nil {
				refID := uuid.New().String()[:8]
				_ = h.caseMgr.SaveCodexTranscript(wt.Path, caseID, refID, req.Title, req.SessionID, transcript)
			}
		}
	}

	// Step 2: refresh ALL codex transcripts attached to the case
	if h.manager != nil {
		if c, err := h.caseMgr.Get(wt.Path, caseID); err == nil {
			for _, ref := range c.Codex {
				if ref.SourceSessionID == "" {
					continue
				}
				transcript, err := h.manager.ExportSession(ref.SourceSessionID, "full")
				if err != nil {
					continue
				}
				_ = h.caseMgr.UpdateCodexTranscript(wt.Path, caseID, ref.ID, transcript)
			}
		}
	}

	// Step 3: merge links
	if len(req.Links) > 0 {
		if c, err := h.caseMgr.Get(wt.Path, caseID); err == nil {
			merged := c.Links
			merged = append(merged, req.Links...)
			_ = h.caseMgr.Update(wt.Path, caseID, cases.CaseUpdate{Links: merged})
		}
	}

	// Step 4: save selected traces
	if len(req.Traces) > 0 && h.traceMgr != nil {
		for _, traceName := range req.Traces {
			report, err := h.traceMgr.GetReport(traceName)
			if err != nil {
				continue
			}
			refID := uuid.New().String()[:8]
			_ = h.caseMgr.SaveTrace(wt.Path, caseID, refID, report)
		}
	}

	// Step 4b: Capture related sessions from the *other* agent.
	for _, rel := range req.RelatedSessions {
		captureRelatedSession(rel, wt.Path, caseID, h.caseMgr, h.claudeMgr, h.manager)
	}

	// Step 5: archive case
	if err := h.caseMgr.Archive(wt.Path, caseID); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "archive case: "+err.Error())
		return
	}

	// Step 6: git add files + archived case dir
	archivedCaseRelDir := filepath.Join(h.caseMgr.ArchivedRelDir(), caseID)
	gitArgs := []string{"-C", wt.Path, "add", archivedCaseRelDir}
	for _, f := range req.Files {
		if strings.Contains(f, "..") {
			continue
		}
		gitArgs = append(gitArgs, f)
	}
	if _, err := worktree.RunCommand(ctx, gitArgs...); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "git add: "+err.Error())
		return
	}

	// Step 7: commit
	commitOut, err := worktree.RunCommand(ctx, "-C", wt.Path, "commit", "-m", req.CommitMessage)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "git commit: "+commitOut+" "+err.Error())
		return
	}

	commitHash := ""
	if hashOut, err := worktree.RunCommand(ctx, "-C", wt.Path, "rev-parse", "--short", "HEAD"); err == nil {
		commitHash = strings.TrimSpace(hashOut)
	}

	// Step 8: trash the codex session
	if req.SessionID != "" && h.manager != nil {
		_ = h.manager.TrashSession(req.SessionID)
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"case_id":     caseID,
		"commit_hash": commitHash,
	})
}

// ImportSessionAPI imports a transcript into a worktree, creating a new session.
func (h *CodexHandler) ImportSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]
	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	var t codex.Transcript
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}
	session, err := h.manager.ImportSession(worktreeName, wt.Path, &t)
	if err != nil {
		if _, ok := err.(*codex.TranscriptError); ok {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, session.Info())
}
