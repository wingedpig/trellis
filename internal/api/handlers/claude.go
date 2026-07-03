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

// ClaudeHandler handles Claude Code WebSocket connections.
type ClaudeHandler struct {
	upgraderHolder
	manager     *claude.Manager
	codexMgr    *codex.Manager // for cross-agent operations like wrap-up capturing related codex sessions
	worktreeMgr worktree.Manager
	caseMgr     *cases.Manager
	traceMgr    *trace.Manager
	bus         events.EventBus
}

// NewClaudeHandler creates a new Claude handler.
func NewClaudeHandler(manager *claude.Manager, codexMgr *codex.Manager, worktreeMgr worktree.Manager, caseMgr *cases.Manager, traceMgr *trace.Manager, bus events.EventBus) *ClaudeHandler {
	return &ClaudeHandler{
		manager:     manager,
		codexMgr:    codexMgr,
		worktreeMgr: worktreeMgr,
		caseMgr:     caseMgr,
		traceMgr:    traceMgr,
		bus:         bus,
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
	CostUSD                  float64             `json:"cost_usd,omitempty"`
	Model                    string              `json:"model,omitempty"`
	ModelOverride            string              `json:"model_override,omitempty"`
	SlashCommands            []string            `json:"slash_commands,omitempty"`
	Skills                   []string            `json:"skills,omitempty"`
	Activity                 string              `json:"activity,omitempty"`
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
	conn, err := h.ws().Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	session.BeginView()
	defer session.EndView()

	// Write mutex for thread-safe WebSocket writes. We marshal to bytes before
	// acquiring the lock and use WriteMessage rather than WriteJSON so a failed
	// encode cannot leave an empty TextMessage frame on the wire — gorilla's
	// streaming encoder closes (and therefore flushes) its frame even when
	// json.Encoder errors out partway.
	var writeMu sync.Mutex
	writeJSON := func(msg serverMessage) error {
		data, err := json.Marshal(msg)
		if err != nil {
			log.Printf("claude: failed to marshal server message (type=%s): %v", msg.Type, err)
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, data)
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
		CostUSD:                  session.CostUSD(),
		Model:                    session.Model(),
		ModelOverride:            session.ModelOverride(),
		SlashCommands:            cmds,
		Skills:                   skills,
		Activity:                 session.CurrentActivity(),
	})

	// Re-send pending permission prompts (oldest first) that were waiting
	// when the client disconnected. Several can be outstanding at once when
	// concurrent background agents each block on their own prompt.
	for _, pending := range session.PendingControlRequests() {
		writeJSON(serverMessage{Type: "stream", Event: pending})
	}

	// Subscribe BEFORE starting the process so the init event is captured
	subCh := session.Subscribe()
	go func() {
		for event := range subCh {
			writeJSON(serverMessage{Type: "stream", Event: &event})
			// When a turn completes, notify the client with the session's
			// accumulated cost (authoritative across process restarts,
			// unlike the per-process total_cost_usd on the event itself).
			if event.Type == "result" {
				writeJSON(serverMessage{Type: "done", CostUSD: session.CostUSD(), Model: session.Model()})
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

	// Forward the session's authoritative live-activity label (e.g. "Editing
	// schema.go", "Running subagent") so the client can show what the agent is
	// doing right now, even during quiet stretches with no streamed text.
	if h.bus != nil {
		activityCh := make(chan string, 16)
		sub, err := h.bus.SubscribeAsync(events.EventSessionActivity, func(_ context.Context, ev events.Event) error {
			if ev.Payload == nil {
				return nil
			}
			if sid, _ := ev.Payload["session_id"].(string); sid != session.ID() {
				return nil
			}
			label, _ := ev.Payload["activity"].(string)
			select {
			case activityCh <- label:
			default:
			}
			return nil
		}, 64)
		if err == nil {
			defer h.bus.Unsubscribe(sub)
			go func() {
				for {
					select {
					case label := <-activityCh:
						writeJSON(serverMessage{Type: "activity", Activity: label})
					case <-wsClosed:
						return
					}
				}
			}()
		}
	}

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
				// Forward permission response to claude's stdin, clearing
				// only the prompt it answers — others may still be pending
				// for concurrent background agents.
				var resp struct {
					Response struct {
						RequestID string `json:"request_id"`
					} `json:"response"`
				}
				_ = json.Unmarshal(msg.Data, &resp)
				session.ClearPendingControlRequest(resp.Response.RequestID)
				if msg.Data != nil {
					if err := session.WriteStdinRaw(msg.Data); err != nil {
						log.Printf("claude: permission response error: %v", err)
					}
				}

			case "cancel":
				// Interrupt the in-flight turn but keep the claude process —
				// and any background tasks it is tracking — alive. The
				// interrupted turn ends with a result event, which emits
				// "done" through the subscriber goroutine above. Only when
				// there was nothing to interrupt does the client need an
				// immediate done to leave the generating state.
				if !session.Interrupt() {
					writeJSON(serverMessage{Type: "done", CostUSD: session.CostUSD(), Model: session.Model()})
				}

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

// SetModelAPI forces the session onto a model alias. A running process is
// switched live (set_model control_request); otherwise the alias applies via
// --model on the next spawn.
func (h *ClaudeHandler) SetModelAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, ErrInternalError, "invalid JSON: "+err.Error())
		return
	}

	session := h.manager.GetSession(sessionID)
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if err := session.SetModel(body.Model); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetPlanAPI returns a session's plan history (captured from ExitPlanMode
// plus any user edits), oldest version first.
func (h *ClaudeHandler) GetPlanAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	session := h.manager.GetSession(sessionID)
	if session == nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, "session not found")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"plans": session.Plans(),
	})
}

// UpdatePlanAPI appends a user-edited version to a session's plan history.
func (h *ClaudeHandler) UpdatePlanAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	session := h.manager.GetSession(sessionID)
	if session == nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, "session not found")
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, ErrInternalError, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		http.Error(w, "content required", http.StatusBadRequest)
		return
	}

	pv := session.UpdatePlan(body.Content)
	WriteJSON(w, http.StatusOK, pv)
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

// MoveSessionAPI moves a Claude session to a fresh worktree along with selected
// uncommitted files from the source worktree. The source worktree is reverted
// for tracked files and untracked selected files are removed.
func (h *ClaudeHandler) MoveSessionAPI(w http.ResponseWriter, r *http.Request) {
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

	// Resolve the session and its source worktree.
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

	// Validate selected file paths stay within the source worktree and exist.
	type selectedFile struct {
		rel       string // forward-slash relative path (git-style)
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

	// Classify each selected file as tracked or untracked via the source git status.
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

	// Create the new worktree.
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
	// The rest of the app (claude sessions, terminals, cases) keys worktrees
	// by whatever string the homepage uses in its URL — the project-prefix-
	// stripped form. Key the moved session the same way so it shows up on the
	// new worktree's home page.
	newWorktreeKey := strings.TrimPrefix(newWT.Name(), h.worktreeMgr.ProjectName()+"-")

	// Copy selected files into the new worktree, preserving relative paths and mode.
	for _, sf := range selected {
		dstAbs := filepath.Join(newWT.Path, filepath.FromSlash(sf.rel))
		if err := copyFilePreserveMode(sf.srcAbs, dstAbs); err != nil {
			WriteError(w, http.StatusInternalServerError, ErrInternalError,
				fmt.Sprintf("copy %q: %s", sf.rel, err.Error()))
			return
		}
	}

	// Revert each selected file in the source: tracked → git checkout --; untracked → remove.
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

	// Rebind the session to the new worktree using the stripped key.
	if err := h.manager.MoveSession(sessionID, newWorktreeKey, newWT.Path); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "move session: "+err.Error())
		return
	}

	movedFiles := make([]string, len(selected))
	for i, sf := range selected {
		movedFiles[i] = sf.rel
	}

	// Emit event for UI refresh.
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:     events.EventClaudeSessionMoved,
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

	resp := map[string]interface{}{
		"session_id":    sessionID,
		"worktree":      newWorktreeKey,
		"branch":        newWT.Branch,
		"path":          newWT.Path,
		"revert_errors": revertErrs,
	}
	WriteJSON(w, http.StatusOK, resp)
}

// safeJoinInsideRoot validates that relPath, when joined with root, stays inside root.
// Rejects absolute paths and paths that escape via "..".
func safeJoinInsideRoot(root, relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("absolute path not allowed")
	}
	cleanedRoot := filepath.Clean(root)
	joined := filepath.Clean(filepath.Join(cleanedRoot, relPath))
	rel, err := filepath.Rel(cleanedRoot, joined)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes worktree")
	}
	return joined, nil
}

// copyFilePreserveMode copies a regular file from src to dst, creating parent
// directories as needed and preserving the source file mode.
func copyFilePreserveMode(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ForkSessionAPI creates a new session in the same worktree as the source,
// pre-populated with the source's messages up to (and including) the
// specified message index. The source session is untouched.
func (h *ClaudeHandler) ForkSessionAPI(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

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

	session, err := h.manager.ForkSession(sessionID, body.MessageIndex, body.DisplayName)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, session.Info())
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

// GitStatus returns the git status for a worktree, filtered to hide paths
// inside the live cases directory. Those paths are managed by the case
// lifecycle and are explicitly not committable while the case is open —
// surfacing them in the commit modal would only let the user check them and
// then get rejected by commitToCase's path guard.
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

	writeFilteredGitStatus(w, h.caseMgr, status, branch)
}

// writeFilteredGitStatus drops any path inside the live cases directory and
// recomputes `clean` from the filtered lists. Shared by the Claude and Codex
// git-status handlers so they behave identically.
func writeFilteredGitStatus(w http.ResponseWriter, caseMgr *cases.Manager, status worktree.GitStatus, branch string) {
	modified := status.Modified
	added := status.Added
	deleted := status.Deleted
	renamed := status.Renamed
	untracked := status.Untracked

	if caseMgr != nil {
		live := caseMgr.CasesRelDir()
		modified = filterCasesPaths(modified, live)
		added = filterCasesPaths(added, live)
		deleted = filterCasesPaths(deleted, live)
		renamed = filterCasesPaths(renamed, live)
		untracked = filterCasesPaths(untracked, live)
	}

	clean := len(modified) == 0 && len(added) == 0 && len(deleted) == 0 && len(renamed) == 0 && len(untracked) == 0

	WriteJSON(w, http.StatusOK, map[string]any{
		"clean":     clean,
		"modified":  modified,
		"added":     added,
		"deleted":   deleted,
		"renamed":   renamed,
		"untracked": untracked,
		"branch":    branch,
	})
}

// filterCasesPaths returns paths that are NOT inside the live cases dir.
// Handles both the directory-form entry (e.g. "trellis/cases/" returned by
// `git status` for an entirely-untracked tree) and any individual files
// inside it.
func filterCasesPaths(paths []string, liveCasesRelDir string) []string {
	if liveCasesRelDir == "" {
		return paths
	}
	dir := strings.TrimSuffix(liveCasesRelDir, "/")
	prefix := dir + "/"
	out := paths[:0:len(paths)]
	for _, p := range paths {
		clean := strings.TrimSuffix(p, "/")
		if clean == dir || strings.HasPrefix(p, prefix) {
			continue
		}
		out = append(out, p)
	}
	return out
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

// ListTraceReports returns available trace report summaries.
func (h *ClaudeHandler) ListTraceReports(w http.ResponseWriter, r *http.Request) {
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

// relatedSessionRef is an entry in commitRequest.RelatedSessions: a session
// from the *other* agent in the same worktree that the user wants captured
// (transcript saved to the case, then session trashed) as part of a
// wrap-up. Lets the user wrap up cross-agent collaborative work in one shot.
type relatedSessionRef struct {
	Agent     string `json:"agent"` // "claude" | "codex"
	SessionID string `json:"session_id"`
}

// captureRelatedSession exports a session from the named agent, saves its
// transcript to the case, and trashes the session. Best-effort — failures
// are silent and don't abort the wrap-up.
//
// Called by commitToCase when archive == true and req.RelatedSessions is
// non-empty.
func captureRelatedSession(rel relatedSessionRef, worktreePath, caseID string, caseMgr *cases.Manager, claudeMgr *claude.Manager, codexMgr *codex.Manager) {
	if caseMgr == nil || rel.SessionID == "" {
		return
	}
	switch rel.Agent {
	case "claude":
		if claudeMgr == nil {
			return
		}
		t, err := claudeMgr.ExportSession(rel.SessionID, "full")
		if err != nil {
			return
		}
		title := t.Source.DisplayName
		if title == "" {
			title = "Session " + rel.SessionID[:min(8, len(rel.SessionID))]
		}
		refID := uuid.New().String()[:8]
		_ = caseMgr.SaveTranscript(worktreePath, caseID, refID, title, rel.SessionID, t)
		seedCasePlanFromClaudeSession(caseMgr, claudeMgr, worktreePath, caseID, rel.SessionID)
		_ = claudeMgr.TrashSession(rel.SessionID)
	case "codex":
		if codexMgr == nil {
			return
		}
		t, err := codexMgr.ExportSession(rel.SessionID, "full")
		if err != nil {
			return
		}
		title := t.Source.DisplayName
		if title == "" {
			title = "Session " + rel.SessionID[:min(8, len(rel.SessionID))]
		}
		refID := uuid.New().String()[:8]
		_ = caseMgr.SaveCodexTranscript(worktreePath, caseID, refID, title, rel.SessionID, t)
		_ = codexMgr.TrashSession(rel.SessionID)
	}
}

// WrapUp orchestrates the full wrap-up workflow: create/update case, update
// transcripts, generate summary, archive, commit. Implemented as a thin
// wrapper over the shared commitToCase orchestrator with archive: true.
func (h *ClaudeHandler) WrapUp(w http.ResponseWriter, r *http.Request) {
	var req commitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}
	deps := commitDeps{
		caseMgr:     h.caseMgr,
		traceMgr:    h.traceMgr,
		claudeMgr:   h.manager,
		codexMgr:    h.codexMgr,
		worktreeMgr: h.worktreeMgr,
	}
	resp, code, errCode, errMsg := commitToCase(r.Context(), r, claudeAdapter{m: h.manager}, deps, req, true)
	if errMsg != "" {
		WriteError(w, code, errCode, errMsg)
		return
	}
	WriteJSON(w, code, resp)
}

// Commit handles intermediate commits made against an open case (or creates
// the case on first commit). Same orchestrator as WrapUp but archive: false.
func (h *ClaudeHandler) Commit(w http.ResponseWriter, r *http.Request) {
	var req commitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}
	deps := commitDeps{
		caseMgr:     h.caseMgr,
		traceMgr:    h.traceMgr,
		claudeMgr:   h.manager,
		codexMgr:    h.codexMgr,
		worktreeMgr: h.worktreeMgr,
	}
	resp, code, errCode, errMsg := commitToCase(r.Context(), r, claudeAdapter{m: h.manager}, deps, req, false)
	if errMsg != "" {
		WriteError(w, code, errCode, errMsg)
		return
	}
	WriteJSON(w, code, resp)
}

// GenerateCommitMessage returns a Claude-generated commit message and case
// description for the staged diff in the worktree.
func (h *ClaudeHandler) GenerateCommitMessage(w http.ResponseWriter, r *http.Request) {
	deps := commitDeps{
		caseMgr:     h.caseMgr,
		traceMgr:    h.traceMgr,
		claudeMgr:   h.manager,
		codexMgr:    h.codexMgr,
		worktreeMgr: h.worktreeMgr,
	}
	generateCommitMessageHTTP(w, r, claudeAdapter{m: h.manager}, deps)
}

// GenerateSummary previews the case summary the wrap-up would generate.
func (h *ClaudeHandler) GenerateSummary(w http.ResponseWriter, r *http.Request) {
	deps := commitDeps{
		caseMgr:     h.caseMgr,
		traceMgr:    h.traceMgr,
		claudeMgr:   h.manager,
		codexMgr:    h.codexMgr,
		worktreeMgr: h.worktreeMgr,
	}
	generateSummaryHTTP(w, r, claudeAdapter{m: h.manager}, deps)
}

// DeriveComponents returns the deterministic wrap-up component list (no LLM),
// so the wrap-up modal can paint its chips instantly.
func (h *ClaudeHandler) DeriveComponents(w http.ResponseWriter, r *http.Request) {
	deps := commitDeps{
		caseMgr:     h.caseMgr,
		traceMgr:    h.traceMgr,
		claudeMgr:   h.manager,
		codexMgr:    h.codexMgr,
		worktreeMgr: h.worktreeMgr,
	}
	deriveComponentsHTTP(w, r, deps)
}
