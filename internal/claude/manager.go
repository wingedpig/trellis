// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ContentBlock mirrors Claude's content block types.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	DiffHTML  string          `json:"diff_html,omitempty"`
}

// Message represents a chat message with role and content blocks.
type Message struct {
	Role      string         `json:"role"`
	Content   []ContentBlock `json:"content"`
	Timestamp time.Time      `json:"timestamp"`
}

// PermissionDenial represents a tool that was denied permission during a turn.
type PermissionDenial struct {
	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
}

// StreamEvent is a parsed NDJSON line from claude --output-format stream-json.
type StreamEvent struct {
	Type              string             `json:"type"`
	Subtype           string             `json:"subtype,omitempty"`
	SessionID         string             `json:"session_id,omitempty"`
	Message           json.RawMessage    `json:"message,omitempty"`
	Result            string             `json:"result,omitempty"`
	IsError           bool               `json:"is_error,omitempty"`
	Errors            []string           `json:"errors,omitempty"`
	Cost              float64            `json:"total_cost_usd,omitempty"`
	Tools             json.RawMessage    `json:"tools,omitempty"`
	PermissionDenials []PermissionDenial `json:"permission_denials,omitempty"`
	// control_request fields (permission prompts from --permission-prompt-tool stdio)
	RequestID string          `json:"request_id,omitempty"`
	Request   json.RawMessage `json:"request,omitempty"`
	// system init fields
	SlashCommands []string `json:"slash_commands,omitempty"`
	Skills        []string `json:"skills,omitempty"`
	// system status events (e.g. compacting)
	Status string `json:"status,omitempty"`
	// stream_event inner event (from --include-partial-messages)
	Event json.RawMessage `json:"event,omitempty"`
}

// ParsedMessage is the message field from an assistant StreamEvent.
type ParsedMessage struct {
	Content []ContentBlock `json:"content"`
}

// stdinUserMessage is the JSON format for sending user messages to claude's stdin.
type stdinUserMessage struct {
	Type      string            `json:"type"`
	SessionID string            `json:"session_id,omitempty"`
	Message   stdinMessageInner `json:"message"`
}

type stdinMessageInner struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// SessionInfo is an exported, JSON-friendly summary of a session.
type SessionInfo struct {
	ID            string     `json:"id"`
	WorktreeName  string     `json:"worktree_name"`
	DisplayName   string     `json:"display_name"`
	LastUserInput time.Time  `json:"last_user_input,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	TrashedAt     *time.Time `json:"trashed_at,omitempty"`
}

// Session holds per-session Claude state with a long-running claude process.
type Session struct {
	mu            sync.Mutex
	stdinMu       sync.Mutex // Protects stdin writes
	id            string     // UUID for this session
	displayName   string     // User-visible label
	worktreeName  string     // Which worktree this belongs to
	claudeSID     string     // Claude CLI session_id for --session-id resume
	workDir       string
	createdAt     time.Time
	trashedAt     *time.Time
	messages      []Message
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	cancel        context.CancelFunc
	generating    bool
	subscribers   map[chan StreamEvent]struct{}
	started       bool
	processGen    int // Generation counter to prevent stale readLoop cleanup
	currentBlocks []ContentBlock
	// Stream event accumulation (from --include-partial-messages)
	hasStreamEvents    bool          // True once stream_event events are seen
	currentStreamBlock *ContentBlock // Block being built from deltas
	streamPartialJSON  string        // Accumulated JSON for tool_use input
	// Token usage tracking (from message_start events)
	inputTokens              int // Most recent input_tokens from API (total)
	inputTokensBase          int
	cacheCreationInputTokens int
	cacheReadInputTokens     int
	// Cached from system init event
	slashCommands []string
	skills        []string
	// Pending control_request event (permission prompt waiting for user response).
	// Stored so reconnecting clients can re-display the prompt.
	pendingControlRequest *StreamEvent
	// Callback to persist after session ID is captured from init
	persistFn    func()
	messagesFile string // path for per-session message persistence
}

// ID returns the session UUID.
func (s *Session) ID() string { return s.id }

// DisplayName returns the user-visible name.
func (s *Session) DisplayName() string { return s.displayName }

// WorktreeName returns which worktree this session belongs to.
func (s *Session) WorktreeName() string { return s.worktreeName }

// Info returns an exported summary.
func (s *Session) Info() SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	info := SessionInfo{
		ID:           s.id,
		WorktreeName: s.worktreeName,
		DisplayName:  s.displayName,
		CreatedAt:    s.createdAt,
		TrashedAt:    s.trashedAt,
	}
	// Find the last user message timestamp
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].Role == "user" {
			info.LastUserInput = s.messages[i].Timestamp
			break
		}
	}
	return info
}

// Manager manages Claude sessions across worktrees.
type Manager struct {
	mu             sync.Mutex
	sessions       map[string]*Session   // session UUID -> session
	worktreeIndex  map[string][]string   // worktree name -> list of session IDs
	stateDir       string                // directory for persistence
	sessionsFile   string                // full path to sessions.json
	messagesDir    string                // directory for per-session message files
}

// NewManager creates a new Claude session manager.
// stateDir is the directory for persistence (e.g., .trellis/claude).
// Pass "" to disable persistence.
func NewManager(stateDir string) *Manager {
	m := &Manager{
		sessions:      make(map[string]*Session),
		worktreeIndex: make(map[string][]string),
		stateDir:      stateDir,
	}
	if stateDir != "" {
		m.sessionsFile = filepath.Join(stateDir, "sessions.json")
		m.messagesDir = filepath.Join(stateDir, "messages")
		m.loadFromDisk()
	}
	return m
}

// loadFromDisk restores session metadata (not processes) from persisted records.
func (m *Manager) loadFromDisk() {
	records, err := loadRecords(m.sessionsFile)
	if err != nil {
		log.Printf("claude: failed to load sessions: %v", err)
		return
	}
	purged := 0
	for _, rec := range records {
		// Auto-purge sessions trashed more than 7 days ago
		if rec.TrashedAt != nil && time.Since(*rec.TrashedAt) > 7*24*time.Hour {
			if m.messagesDir != "" {
				os.Remove(filepath.Join(m.messagesDir, rec.ID+".jsonl"))
				os.Remove(filepath.Join(m.messagesDir, rec.ID+".json"))
			}
			purged++
			continue
		}

		s := &Session{
			id:           rec.ID,
			worktreeName: rec.WorktreeName,
			displayName:  rec.DisplayName,
			claudeSID:    rec.SessionID,
			workDir:      rec.WorkDir,
			createdAt:    rec.CreatedAt,
			trashedAt:    rec.TrashedAt,
			subscribers:  make(map[chan StreamEvent]struct{}),
		}
		s.persistFn = m.makePersistFn()
		if m.messagesDir != "" {
			// Prefer .jsonl (new format), fall back to .json (legacy)
			jsonlPath := filepath.Join(m.messagesDir, rec.ID+".jsonl")
			jsonPath := filepath.Join(m.messagesDir, rec.ID+".json")
			s.messagesFile = jsonlPath
			if msgs, err := loadMessages(jsonlPath); err != nil {
				log.Printf("claude: failed to load messages for session %s: %v", rec.ID, err)
			} else if len(msgs) > 0 {
				s.messages = msgs
			} else if msgs, err := loadMessages(jsonPath); err != nil {
				log.Printf("claude: failed to load messages for session %s: %v", rec.ID, err)
			} else if len(msgs) > 0 {
				s.messages = msgs
			}
		}
		m.sessions[rec.ID] = s
		m.worktreeIndex[rec.WorktreeName] = append(m.worktreeIndex[rec.WorktreeName], rec.ID)
	}
	if purged > 0 {
		log.Printf("claude: purged %d expired trashed sessions", purged)
		go m.persist()
	}
	if len(records)-purged > 0 {
		log.Printf("claude: loaded %d persisted sessions", len(records)-purged)
	}
}

// persist saves all session metadata to disk.
func (m *Manager) persist() {
	if m.sessionsFile == "" {
		return
	}
	m.mu.Lock()
	records := make([]SessionRecord, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.mu.Lock()
		records = append(records, SessionRecord{
			ID:           s.id,
			WorktreeName: s.worktreeName,
			DisplayName:  s.displayName,
			SessionID:    s.claudeSID,
			WorkDir:      s.workDir,
			CreatedAt:    s.createdAt,
			TrashedAt:    s.trashedAt,
		})
		s.mu.Unlock()
	}
	m.mu.Unlock()

	if err := saveRecords(m.sessionsFile, records); err != nil {
		log.Printf("claude: failed to persist sessions: %v", err)
	}
}

// makePersistFn returns a callback that persists manager state.
func (m *Manager) makePersistFn() func() {
	return func() { m.persist() }
}

// CreateSession creates a new session for a worktree.
func (m *Manager) CreateSession(worktreeName, workDir, displayName string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := uuid.New().String()
	if displayName == "" {
		// Auto-name: "Session N" based on count for this worktree
		count := len(m.worktreeIndex[worktreeName])
		displayName = fmt.Sprintf("Session %d", count+1)
	}

	s := &Session{
		id:           id,
		worktreeName: worktreeName,
		displayName:  displayName,
		workDir:      workDir,
		createdAt:    time.Now(),
		subscribers:  make(map[chan StreamEvent]struct{}),
	}
	s.persistFn = m.makePersistFn()
	if m.messagesDir != "" {
		s.messagesFile = filepath.Join(m.messagesDir, id+".jsonl")
	}
	m.sessions[id] = s
	m.worktreeIndex[worktreeName] = append(m.worktreeIndex[worktreeName], id)

	go m.persist()
	return s
}

// GetSession returns a session by UUID.
func (m *Manager) GetSession(sessionID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[sessionID]
}

// ListSessions returns session info for all active (non-trashed) sessions belonging to a worktree.
func (m *Manager) ListSessions(worktreeName string) []*SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := m.worktreeIndex[worktreeName]
	result := make([]*SessionInfo, 0, len(ids))
	for _, id := range ids {
		if s, ok := m.sessions[id]; ok {
			s.mu.Lock()
			trashed := s.trashedAt != nil
			s.mu.Unlock()
			if trashed {
				continue
			}
			info := s.Info()
			result = append(result, &info)
		}
	}
	return result
}

// RenameSession updates a session's display name.
func (m *Manager) RenameSession(sessionID, newName string) error {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("session not found")
	}
	s.mu.Lock()
	s.displayName = newName
	s.mu.Unlock()
	m.persist()
	return nil
}

// DeleteSession kills a session's process and removes it from the manager.
func (m *Manager) DeleteSession(sessionID string) {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.sessions, sessionID)
	// Remove from worktree index
	wt := s.worktreeName
	ids := m.worktreeIndex[wt]
	for i, id := range ids {
		if id == sessionID {
			m.worktreeIndex[wt] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	if len(m.worktreeIndex[wt]) == 0 {
		delete(m.worktreeIndex, wt)
	}
	m.mu.Unlock()

	// Kill process
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.closeAllSubscribers()
	msgFile := s.messagesFile
	s.mu.Unlock()

	// Remove messages file
	if msgFile != "" {
		os.Remove(msgFile)
	}

	m.persist()
}

// TrashSession soft-deletes a session by setting its trashedAt timestamp.
// The process is killed and subscribers closed, but the session data is preserved.
func (m *Manager) TrashSession(sessionID string) error {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("session not found")
	}

	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.closeAllSubscribers()
	now := time.Now()
	s.trashedAt = &now
	s.started = false
	s.generating = false
	s.stdin = nil
	s.cmd = nil
	s.cancel = nil
	s.mu.Unlock()

	m.persist()
	return nil
}

// RestoreSession restores a trashed session back to active state.
func (m *Manager) RestoreSession(sessionID string) error {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("session not found")
	}

	s.mu.Lock()
	s.trashedAt = nil
	s.mu.Unlock()

	m.persist()
	return nil
}

// ListTrashedSessions returns session info for all trashed sessions belonging to a worktree.
func (m *Manager) ListTrashedSessions(worktreeName string) []*SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := m.worktreeIndex[worktreeName]
	result := make([]*SessionInfo, 0)
	for _, id := range ids {
		if s, ok := m.sessions[id]; ok {
			s.mu.Lock()
			trashed := s.trashedAt != nil
			s.mu.Unlock()
			if !trashed {
				continue
			}
			info := s.Info()
			result = append(result, &info)
		}
	}
	return result
}

// AllSessions returns info for all active (non-trashed) sessions across all worktrees.
func (m *Manager) AllSessions() []*SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]*SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.mu.Lock()
		trashed := s.trashedAt != nil
		s.mu.Unlock()
		if trashed {
			continue
		}
		info := s.Info()
		result = append(result, &info)
	}
	return result
}

// GetOrCreateSession returns the first active (non-trashed) session for a worktree, creating one if none exists.
// This provides backwards compatibility for WebSocket connections that don't specify a session.
func (m *Manager) GetOrCreateSession(worktreeName, workDir string) *Session {
	m.mu.Lock()
	ids := m.worktreeIndex[worktreeName]
	for _, id := range ids {
		if s, ok := m.sessions[id]; ok {
			s.mu.Lock()
			trashed := s.trashedAt != nil
			s.mu.Unlock()
			if !trashed {
				m.mu.Unlock()
				return s
			}
		}
	}
	m.mu.Unlock()

	return m.CreateSession(worktreeName, workDir, "")
}

// Shutdown kills all running Claude processes and closes all subscribers.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.sessions {
		s.mu.Lock()
		if s.cancel != nil {
			s.cancel()
		}
		s.closeAllSubscribers()
		s.mu.Unlock()
	}
}

// persistMessage appends a single message to disk. Must be called with s.mu held.
func (s *Session) persistMessage(msg Message) {
	if s.messagesFile == "" {
		return
	}
	file := s.messagesFile
	id := s.id
	go func() {
		if err := appendMessage(file, msg); err != nil {
			log.Printf("claude: failed to persist message for session %s: %v", id, err)
		}
	}()
}

// persistAllMessages rewrites the entire messages file. Must be called with s.mu held.
// Used for destructive operations like Reset and ImportSession.
func (s *Session) persistAllMessages() {
	if s.messagesFile == "" {
		return
	}
	msgs := make([]Message, len(s.messages))
	copy(msgs, s.messages)
	file := s.messagesFile
	id := s.id
	go func() {
		if err := rewriteMessages(file, msgs); err != nil {
			log.Printf("claude: failed to rewrite messages for session %s: %v", id, err)
		}
	}()
}

// Messages returns the conversation history.
func (s *Session) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Message, len(s.messages))
	copy(result, s.messages)
	return result
}

// MessagesWithPending returns conversation history including any in-progress assistant turn.
// This is used when sending history to a newly-connected WebSocket so the client
// can see content that has already been streamed (e.g., tool_use blocks being executed).
func (s *Session) MessagesWithPending() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Message, len(s.messages))
	copy(result, s.messages)

	if s.generating && len(s.currentBlocks) > 0 {
		pending := make([]ContentBlock, len(s.currentBlocks))
		copy(pending, s.currentBlocks)
		// Include partially-built block if mid-stream
		if s.currentStreamBlock != nil {
			partial := *s.currentStreamBlock
			if partial.Type == "tool_use" && s.streamPartialJSON != "" {
				partial.Input = json.RawMessage(s.streamPartialJSON)
			}
			pending = append(pending, partial)
		}
		result = append(result, Message{
			Role:      "assistant",
			Content:   pending,
			Timestamp: time.Now(),
		})
	}

	return result
}

// SlashCommands returns the cached slash commands and skills.
func (s *Session) SlashCommands() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slashCommands, s.skills
}

// IsGenerating returns whether the session is currently generating.
func (s *Session) IsGenerating() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generating
}

// InputTokens returns the most recent input token count from the API.
func (s *Session) InputTokens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inputTokens
}

// TokenBreakdown returns the individual token components.
func (s *Session) TokenBreakdown() (base, cacheCreate, cacheRead int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inputTokensBase, s.cacheCreationInputTokens, s.cacheReadInputTokens
}

// PendingControlRequest returns the pending control_request event, if any.
// Used to re-display permission prompts to reconnecting clients.
func (s *Session) PendingControlRequest() *StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingControlRequest
}

// ClearPendingControlRequest clears the stored pending control_request.
// Called after the client responds to the permission prompt.
func (s *Session) ClearPendingControlRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingControlRequest = nil
}

// Subscribe returns a channel that receives stream events.
// The channel stays open across process restarts and is only closed
// when Unsubscribe is called or the Manager is shut down.
func (s *Session) Subscribe() chan StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan StreamEvent, 100)
	s.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber channel. Safe to call if already removed.
func (s *Session) Unsubscribe(ch chan StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subscribers[ch]; ok {
		delete(s.subscribers, ch)
		close(ch)
	}
}

// closeAllSubscribers closes and removes all subscriber channels.
// Must be called with s.mu held.
func (s *Session) closeAllSubscribers() {
	for ch := range s.subscribers {
		close(ch)
	}
	s.subscribers = make(map[chan StreamEvent]struct{})
}

// fanOut sends an event to all subscribers.
func (s *Session) fanOut(event StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subscribers {
		select {
		case ch <- event:
		default:
			// Drop if subscriber buffer is full
		}
	}
}

// ensureProcess starts the long-running claude process if not already running.
func (s *Session) ensureProcess(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	workDir := s.workDir
	resumeSID := s.claudeSID
	gen := s.processGen + 1
	s.processGen = gen
	s.mu.Unlock()

	args := []string{
		"--output-format", "stream-json",
		"--verbose",
		"--input-format", "stream-json",
		"--permission-prompt-tool", "stdio",
		"--permission-mode", "default",
		"--include-partial-messages",
	}

	// Resume a previous conversation if we have a Claude session ID
	if resumeSID != "" {
		args = append(args, "--resume", resumeSID)
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "claude", args...)
	cmd.Dir = workDir
	cmd.Stderr = os.Stderr

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start claude: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.stdin = stdinPipe
	s.cancel = cancel
	s.started = true
	s.mu.Unlock()

	go s.readLoop(stdoutPipe, cmd, gen)

	return nil
}

// readLoop reads NDJSON events from claude's stdout continuously.
func (s *Session) readLoop(stdout io.Reader, cmd *exec.Cmd, gen int) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event StreamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			log.Printf("claude: failed to parse NDJSON: %v", err)
			continue
		}

		log.Printf("claude [%s]: event: %s", s.id, line)

		// Handle resume failures: if Claude can't find the conversation,
		// rebuild the CLI session from existing messages so the next
		// attempt resumes with full history.
		if event.Type == "result" && event.IsError {
			for _, errMsg := range event.Errors {
				if strings.Contains(errMsg, "No conversation found with session ID") {
					s.mu.Lock()
					msgs := make([]Message, len(s.messages))
					copy(msgs, s.messages)
					workDir := s.workDir
					s.mu.Unlock()

					if len(msgs) > 0 {
						newSID, err := WriteCLISessionFile(workDir, workDir, "", msgs)
						if err != nil {
							log.Printf("claude [%s]: failed to rebuild CLI session: %v, starting fresh", s.id, err)
							newSID = ""
						} else {
							log.Printf("claude [%s]: rebuilt CLI session as %s (%d messages)", s.id, newSID, len(msgs))
						}
						s.mu.Lock()
						s.claudeSID = newSID
						s.mu.Unlock()
					} else {
						log.Printf("claude [%s]: stale session ID with no messages, starting fresh", s.id)
						s.mu.Lock()
						s.claudeSID = ""
						s.mu.Unlock()
					}

					persistFn := s.persistFn
					if persistFn != nil {
						persistFn()
					}
					break
				}
			}
		}

		// Capture session ID from init or result events and persist
		if event.SessionID != "" && !event.IsError {
			s.mu.Lock()
			changed := s.claudeSID != event.SessionID
			s.claudeSID = event.SessionID
			persistFn := s.persistFn
			s.mu.Unlock()
			if changed && persistFn != nil {
				persistFn()
			}
		}

		// Cache slash commands and skills from system init
		if event.Type == "system" && event.Subtype == "init" {
			s.mu.Lock()
			if len(event.SlashCommands) > 0 {
				s.slashCommands = event.SlashCommands
			}
			if len(event.Skills) > 0 {
				s.skills = event.Skills
			}
			s.mu.Unlock()
		}

		// Log truly unknown event types for debugging
		switch event.Type {
		case "system", "assistant", "result", "control_request", "stream_event", "user":
			// Known types
		default:
			log.Printf("claude: unknown event type %q: %s", event.Type, line)
		}

		// Accumulate content blocks from stream_event (--include-partial-messages)
		if event.Type == "stream_event" && event.Event != nil {
			s.handleStreamEvent(event.Event)
		}

		// Extract usage and content from assistant events
		if event.Type == "assistant" && event.Message != nil {
			var msg struct {
				Content []ContentBlock `json:"content"`
				Usage   struct {
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					OutputTokens             int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(event.Message, &msg) == nil {
				s.mu.Lock()
				total := msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens
				if total > 0 {
					s.inputTokens = total
					s.inputTokensBase = msg.Usage.InputTokens
					s.cacheCreationInputTokens = msg.Usage.CacheCreationInputTokens
					s.cacheReadInputTokens = msg.Usage.CacheReadInputTokens
				}
				// Only accumulate content blocks when not using stream events
				if !s.hasStreamEvents && len(msg.Content) > 0 {
					workDir := s.workDir
					s.mu.Unlock()
					for i := range msg.Content {
						enrichEditBlock(&msg.Content[i], workDir)
						enrichWriteBlock(&msg.Content[i], workDir)
					}
					s.mu.Lock()
					s.currentBlocks = append(s.currentBlocks, msg.Content...)
				}
				s.mu.Unlock()
			}
		}

		// Handle turn completion
		if event.Type == "result" {
			s.mu.Lock()
			if len(s.currentBlocks) > 0 {
				msg := Message{
					Role:      "assistant",
					Content:   s.currentBlocks,
					Timestamp: time.Now(),
				}
				s.messages = append(s.messages, msg)
				s.persistMessage(msg)
			}
			s.generating = false
			s.currentBlocks = nil
			s.pendingControlRequest = nil
			s.mu.Unlock()
		}

		// Persist in-progress assistant blocks on control_request so they
		// survive a restart. The turn is paused waiting for user input,
		// making this a natural checkpoint.
		if event.Type == "control_request" {
			eventCopy := event
			s.mu.Lock()
			if len(s.currentBlocks) > 0 {
				msg := Message{
					Role:      "assistant",
					Content:   s.currentBlocks,
					Timestamp: time.Now(),
				}
				s.messages = append(s.messages, msg)
				s.currentBlocks = nil
				s.persistMessage(msg)
			}
			s.pendingControlRequest = &eventCopy
			s.mu.Unlock()
		}

		// Fan out to all subscribers
		s.fanOut(event)
	}

	// Process exited - wait for it and clean up
	cmd.Wait()

	s.mu.Lock()
	// Only clean up session state if we're still the current generation.
	// A newer process may have already been started by ensureProcess.
	if s.processGen == gen {
		s.started = false
		s.generating = false
		s.stdin = nil
		s.cmd = nil
		s.cancel = nil
	}
	s.mu.Unlock()
}

// handleStreamEvent processes an inner event from a stream_event wrapper,
// accumulating content blocks for message history.
func (s *Session) handleStreamEvent(raw json.RawMessage) {
	var inner struct {
		Type         string          `json:"type"`
		ContentBlock json.RawMessage `json:"content_block,omitempty"`
		Delta        json.RawMessage `json:"delta,omitempty"`
		Message      json.RawMessage `json:"message,omitempty"`
	}
	if json.Unmarshal(raw, &inner) != nil {
		return
	}

	s.mu.Lock()
	s.hasStreamEvents = true
	s.mu.Unlock()

	switch inner.Type {
	case "message_start":
		// Extract input_tokens for context window tracking
		// Sum input_tokens + cache tokens for total context usage
		if inner.Message != nil {
			var msg struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(inner.Message, &msg) == nil {
				total := msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens
				if total > 0 {
					s.mu.Lock()
					s.inputTokens = total
					s.inputTokensBase = msg.Usage.InputTokens
					s.cacheCreationInputTokens = msg.Usage.CacheCreationInputTokens
					s.cacheReadInputTokens = msg.Usage.CacheReadInputTokens
					s.mu.Unlock()
				}
			}
		}
		return
	case "content_block_start":
		if inner.ContentBlock == nil {
			return
		}
		var cb struct {
			Type string `json:"type"`
			ID   string `json:"id,omitempty"`
			Name string `json:"name,omitempty"`
		}
		if json.Unmarshal(inner.ContentBlock, &cb) != nil {
			return
		}
		s.mu.Lock()
		switch cb.Type {
		case "text":
			s.currentStreamBlock = &ContentBlock{Type: "text"}
		case "tool_use":
			s.currentStreamBlock = &ContentBlock{Type: "tool_use", ID: cb.ID, Name: cb.Name}
			s.streamPartialJSON = ""
		}
		s.mu.Unlock()

	case "content_block_delta":
		if inner.Delta == nil {
			return
		}
		var d struct {
			Type        string `json:"type"`
			Text        string `json:"text,omitempty"`
			PartialJSON string `json:"partial_json,omitempty"`
		}
		if json.Unmarshal(inner.Delta, &d) != nil {
			return
		}
		s.mu.Lock()
		if s.currentStreamBlock != nil {
			switch d.Type {
			case "text_delta":
				s.currentStreamBlock.Text += d.Text
			case "input_json_delta":
				s.streamPartialJSON += d.PartialJSON
			}
		}
		s.mu.Unlock()

	case "content_block_stop":
		s.mu.Lock()
		if s.currentStreamBlock != nil {
			if s.currentStreamBlock.Type == "tool_use" && s.streamPartialJSON != "" {
				s.currentStreamBlock.Input = json.RawMessage(s.streamPartialJSON)
			}
			block := *s.currentStreamBlock
			workDir := s.workDir
			s.currentStreamBlock = nil
			s.streamPartialJSON = ""
			s.mu.Unlock()

			enrichEditBlock(&block, workDir)
			enrichWriteBlock(&block, workDir)

			s.mu.Lock()
			s.currentBlocks = append(s.currentBlocks, block)
			s.mu.Unlock()

			// Fan out diff enrichment for live WebSocket clients
			if block.DiffHTML != "" {
				payload, _ := json.Marshal(struct {
					ToolUseID string `json:"tool_use_id"`
					DiffHTML  string `json:"diff_html"`
				}{block.ID, block.DiffHTML})
				s.fanOut(StreamEvent{Type: "diff_enrichment", Message: json.RawMessage(payload)})
			}
			return
		}
		s.mu.Unlock()
	}
}

// EnsureProcess starts the claude process if not already running.
// Called on WebSocket connect so the init event arrives before user types.
func (s *Session) EnsureProcess(ctx context.Context) error {
	return s.ensureProcess(ctx)
}

// Send sends a prompt to Claude by writing to the process stdin.
// This is non-blocking — the response arrives via subscriber channels.
func (s *Session) Send(ctx context.Context, prompt string) error {
	s.mu.Lock()
	if s.generating {
		s.mu.Unlock()
		return fmt.Errorf("already generating")
	}
	s.generating = true
	s.currentBlocks = nil

	// Add user message to history
	userMsg := Message{
		Role:      "user",
		Content:   []ContentBlock{{Type: "text", Text: prompt}},
		Timestamp: time.Now(),
	}
	s.messages = append(s.messages, userMsg)
	s.persistMessage(userMsg)
	s.mu.Unlock()

	// Ensure process is running
	if err := s.ensureProcess(ctx); err != nil {
		s.mu.Lock()
		s.generating = false
		s.mu.Unlock()
		return err
	}

	// Write user message to stdin
	s.mu.Lock()
	sid := s.claudeSID
	s.mu.Unlock()

	msg := stdinUserMessage{
		Type:      "user",
		SessionID: sid,
		Message: stdinMessageInner{
			Role:    "user",
			Content: []ContentBlock{{Type: "text", Text: prompt}},
		},
	}

	if err := s.writeStdin(msg); err != nil {
		s.mu.Lock()
		s.generating = false
		s.mu.Unlock()
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

// WriteStdinRaw writes raw JSON to claude's stdin (for permission responses).
func (s *Session) WriteStdinRaw(data json.RawMessage) error {
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()

	s.mu.Lock()
	stdin := s.stdin
	s.mu.Unlock()

	if stdin == nil {
		return fmt.Errorf("process not running")
	}

	_, err := stdin.Write(append(data, '\n'))
	return err
}

// writeStdin marshals msg as JSON and writes to stdin.
func (s *Session) writeStdin(msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}

	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()

	s.mu.Lock()
	stdin := s.stdin
	s.mu.Unlock()

	if stdin == nil {
		return fmt.Errorf("process not running")
	}

	_, err = stdin.Write(append(data, '\n'))
	return err
}

// Cancel kills the running Claude process.
// The process will be restarted on the next Send call.
func (s *Session) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.generating = false
	s.started = false
	s.stdin = nil
	s.cmd = nil
	s.cancel = nil
}

// ExportSession creates a transcript export for the given session.
// level can be "full" (default) or "summary".
func (m *Manager) ExportSession(sessionID, level string) (*Transcript, error) {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session not found")
	}

	s.mu.Lock()
	messages := make([]Message, len(s.messages))
	copy(messages, s.messages)
	source := TranscriptSource{
		TrellisSessionID: s.id,
		ClaudeSessionID:  s.claudeSID,
		Worktree:         s.worktreeName,
		DisplayName:      s.displayName,
		ProjectPath:      s.workDir,
		CreatedAt:        s.createdAt,
	}
	s.mu.Unlock()

	if level == "summary" {
		messages = SummarizeMessages(messages)
	}

	return &Transcript{
		Schema:     TranscriptSchema,
		ExportedAt: time.Now(),
		Source:     source,
		Messages:   messages,
		Stats:      ComputeStats(messages),
	}, nil
}

// ImportSession imports a transcript into a worktree, creating a new session
// with the conversation pre-populated. It writes the conversation to Claude CLI's
// JSONL format so --resume can pick it up for true continuation.
func (m *Manager) ImportSession(worktreeName, workDir, gitBranch string, transcript *Transcript) (*Session, error) {
	if err := ValidateTranscript(transcript); err != nil {
		return nil, err
	}

	// Derive display name from transcript
	displayName := transcript.Source.DisplayName
	if displayName == "" {
		displayName = "Imported session"
	}
	displayName += " (imported)"

	// Create a new Trellis session
	session := m.CreateSession(worktreeName, workDir, displayName)

	// Write messages into Claude CLI's JSONL format for --resume
	projectPath := workDir
	cliSessionID, err := WriteCLISession(projectPath, workDir, gitBranch, transcript.Messages)
	if err != nil {
		return session, fmt.Errorf("write CLI session: %w", err)
	}

	// Store the Claude CLI session ID so ensureProcess uses --resume
	session.mu.Lock()
	session.claudeSID = cliSessionID
	session.messages = make([]Message, len(transcript.Messages))
	copy(session.messages, transcript.Messages)
	session.persistAllMessages()
	session.mu.Unlock()

	// Persist the updated session metadata (with claudeSID)
	m.persist()

	return session, nil
}

// Reset clears the session and kills the process to start a new conversation.
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.claudeSID = ""
	s.messages = nil
	s.currentBlocks = nil
	s.generating = false
	s.started = false
	s.stdin = nil
	s.cmd = nil
	s.cancel = nil
	s.persistAllMessages()
}
