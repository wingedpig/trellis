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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wingedpig/trellis/internal/events"
)

// debugEvents gates raw NDJSON event logging in readLoop. Raw events can
// carry tool output (including secrets) and bloat the log, so they are only
// logged when explicitly opted in via TRELLIS_DEBUG_EVENTS=1.
var debugEvents = os.Getenv("TRELLIS_DEBUG_EVENTS") != ""

// maxEventLogBytes caps how much of a raw event line is logged.
const maxEventLogBytes = 2048

// truncateForLog shortens a raw event line for logging.
func truncateForLog(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return fmt.Sprintf("%s...(+%d bytes)", b[:n], len(b)-n)
}

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
	// control_response payload (CLI replies to control requests we send on
	// stdin, e.g. interrupt and set_model)
	Response json.RawMessage `json:"response,omitempty"`
	// system init fields
	SlashCommands []string `json:"slash_commands,omitempty"`
	Skills        []string `json:"skills,omitempty"`
	// system status events (e.g. compacting)
	Status string `json:"status,omitempty"`
	// system status result for completed operations (e.g. compact_result: "success")
	CompactResult string `json:"compact_result,omitempty"`
	// system compact_boundary metadata (pre/post tokens, duration, trigger)
	CompactMetadata json.RawMessage `json:"compact_metadata,omitempty"`
	// stream_event inner event (from --include-partial-messages)
	Event json.RawMessage `json:"event,omitempty"`
	// parent_tool_use_id is set on events emitted by a Task subagent; it points
	// at the Task tool_use that spawned the subagent. Used to route subagent
	// activity to the right Task block instead of the main transcript.
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
	// Activity carries a short human-readable label on synthetic
	// "subagent_activity" events fanned out to live clients.
	Activity string `json:"activity,omitempty"`
	// Step is the subagent's running step count on "subagent_activity" events.
	Step int `json:"step,omitempty"`
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

// stdinControlRequest is the JSON format for control requests sent to claude's
// stdin (interrupt, set_model). The CLI acknowledges with a control_response
// event carrying the same request_id.
type stdinControlRequest struct {
	Type      string            `json:"type"` // always "control_request"
	RequestID string            `json:"request_id"`
	Request   stdinControlInner `json:"request"`
}

type stdinControlInner struct {
	Subtype string `json:"subtype"`
	// Model is set for set_model requests: a model alias or full id. Omitted
	// resets the CLI to its default model.
	Model string `json:"model,omitempty"`
}

// Request-id prefixes for control requests we originate, so control_response
// events can be routed back to the right fallback handling in readLoop.
const (
	interruptReqPrefix = "trellis-int-"
	setModelReqPrefix  = "trellis-sm-"
)

// interruptTimeout is how long Interrupt waits for the CLI to abort the
// in-flight turn before falling back to killing the process.
const interruptTimeout = 10 * time.Second

// SessionInfo is an exported, JSON-friendly summary of a session.
type SessionInfo struct {
	ID            string     `json:"id"`
	WorktreeName  string     `json:"worktree_name"`
	DisplayName   string     `json:"display_name"`
	LastUserInput time.Time  `json:"last_user_input,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	TrashedAt     *time.Time `json:"trashed_at,omitempty"`
	CostUSD       float64    `json:"cost_usd,omitempty"`
	Model         string     `json:"model,omitempty"`
}

// Session holds per-session Claude state with a long-running claude process.
type Session struct {
	mu            sync.Mutex
	stdinMu       sync.Mutex // Protects stdin writes
	startMu       sync.Mutex // Serializes ensureProcess so concurrent callers can't double-spawn
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
	// turnGen increments on each Send. Interrupt's kill-fallback watchdog uses
	// it to verify the turn it interrupted is still the one in flight, so it
	// never kills the process out from under a newer turn.
	turnGen int
	// interruptRequested is true while a user-initiated interrupt is pending
	// for the current turn. The result event of an interrupted turn reports
	// is_error, which must not surface as an "error" inbox state — the user
	// asked for the stop. Cleared at the start of each turn and on turn end.
	interruptRequested bool
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
	// Cost and model tracking. The CLI reports total_cost_usd cumulatively
	// per process, so costUSD accumulates deltas across process restarts.
	costUSD         float64
	lastProcessCost float64 // last total_cost_usd seen from the current process
	model           string  // most recent model id seen on this session
	// modelOverride is a model alias (e.g. "opus", "sonnet") forced onto this
	// session. Empty means the CLI default. Applied via --model at process
	// spawn; a running process is switched live with a set_model
	// control_request so the process (and its background tasks) survives.
	modelOverride string
	// Cached from system init event
	slashCommands []string
	skills        []string
	// Pending control_request events (permission prompts waiting for user
	// response), in arrival order. Several can be outstanding at once —
	// concurrent background agents each block on their own prompt — so this
	// must not be a single slot: overwriting lost every prompt but the last,
	// leaving those agents waiting forever. Entries are removed when the
	// client answers (by request_id), when the CLI retracts one via
	// control_cancel_request, or when the process exits (a dead process can
	// never receive the response). NOT cleared on turn end: background-agent
	// prompts outlive the turn that spawned them.
	pendingControlRequests []*StreamEvent
	// Inbox unread tracking. unread becomes true when this session transitions
	// running → needs-you while no client is viewing it (viewerCount == 0).
	// Cleared when a client opens the session WS or the session goes back to
	// running. lastPublishedState is the most-recent state value emitted via
	// publishStateLocked; used to detect true running↔needs-you transitions.
	unread             bool
	viewerCount        int
	lastPublishedState string
	// currentActivity is a short human-readable description of what the
	// session is doing right now ("Running go test", "Editing schema.go").
	// Updated at tool boundaries and published via EventSessionActivity so the
	// inbox can show a live activity line. Empty when idle.
	currentActivity string
	// subagentSteps counts tool invocations per running Task subagent, keyed by
	// the spawning Task tool_use id. Drives the live step count shown in the
	// Task block. Reset at the start of each turn.
	subagentSteps map[string]int
	// lastTurnError is true when the most recently completed turn ended in an
	// error. Cleared on the next Send. Drives the inbox "error" reason.
	lastTurnError bool
	// Callback to persist after session ID is captured from init
	persistFn    func()
	messagesFile string // path for per-session message persistence
	// Plan artifacts captured from ExitPlanMode (and user edits), persisted
	// to plansFile as a JSON array of versions.
	plans     []PlanVersion
	plansFile string
	// Back-reference for publishing inbox state-change events. May be nil
	// in tests that construct Session directly.
	manager *Manager
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
		CostUSD:      s.costUSD,
		Model:        s.model,
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
	plansDir       string                // directory for per-session plan files
	bus            events.EventBus       // optional; for publishing inbox state changes
}

// SetEventBus wires the bus used for publishing inbox session-state events.
// May be called once after construction. Safe to leave unset (publishing
// no-ops), which is the path taken by tests.
func (m *Manager) SetEventBus(bus events.EventBus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bus = bus
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
		m.plansDir = filepath.Join(stateDir, "plans")
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
			if m.plansDir != "" {
				os.Remove(filepath.Join(m.plansDir, rec.ID+".json"))
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
			createdAt:     rec.CreatedAt,
			trashedAt:     rec.TrashedAt,
			costUSD:       rec.CostUSD,
			model:         rec.Model,
			modelOverride: rec.ModelOverride,
			subscribers:   make(map[chan StreamEvent]struct{}),
			manager:       m,
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
		if m.plansDir != "" {
			s.plansFile = filepath.Join(m.plansDir, rec.ID+".json")
			if plans, err := loadPlans(s.plansFile); err != nil {
				log.Printf("claude: failed to load plans for session %s: %v", rec.ID, err)
			} else {
				s.plans = plans
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
			CreatedAt:     s.createdAt,
			TrashedAt:     s.trashedAt,
			CostUSD:       s.costUSD,
			Model:         s.model,
			ModelOverride: s.modelOverride,
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

// nextSessionNameLocked picks the next "Session N" suffix that isn't already
// in use by an active session for this worktree. Using "active count + 1"
// collides when an interior session was trashed (e.g. trashing #2 of {1,2,3}
// would propose "Session 3" again). Caller must hold m.mu.
func (m *Manager) nextSessionNameLocked(worktreeName string) string {
	maxN := 0
	for _, sid := range m.worktreeIndex[worktreeName] {
		s, ok := m.sessions[sid]
		if !ok {
			continue
		}
		s.mu.Lock()
		trashed := s.trashedAt != nil
		name := s.displayName
		s.mu.Unlock()
		if trashed {
			continue
		}
		const prefix = "Session "
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := name[len(prefix):]
		if n, err := strconv.Atoi(suffix); err == nil && n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("Session %d", maxN+1)
}

// CreateSession creates a new session for a worktree.
func (m *Manager) CreateSession(worktreeName, workDir, displayName string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := uuid.New().String()
	if displayName == "" {
		displayName = m.nextSessionNameLocked(worktreeName)
	}

	s := &Session{
		id:           id,
		worktreeName: worktreeName,
		displayName:  displayName,
		workDir:      workDir,
		createdAt:    time.Now(),
		subscribers:  make(map[chan StreamEvent]struct{}),
		manager:      m,
	}
	s.persistFn = m.makePersistFn()
	if m.messagesDir != "" {
		s.messagesFile = filepath.Join(m.messagesDir, id+".jsonl")
	}
	if m.plansDir != "" {
		s.plansFile = filepath.Join(m.plansDir, id+".json")
	}
	m.sessions[id] = s
	m.worktreeIndex[worktreeName] = append(m.worktreeIndex[worktreeName], id)

	go m.persist()
	s.mu.Lock()
	s.publishStateLocked()
	s.mu.Unlock()
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
	sort.Slice(result, func(i, j int) bool {
		ti := result[i].LastUserInput
		if ti.IsZero() {
			ti = result[i].CreatedAt
		}
		tj := result[j].LastUserInput
		if tj.IsZero() {
			tj = result[j].CreatedAt
		}
		return ti.After(tj)
	})
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
	plansFile := s.plansFile
	s.mu.Unlock()

	// Remove messages and plans files
	if msgFile != "" {
		os.Remove(msgFile)
	}
	if plansFile != "" {
		os.Remove(plansFile)
	}

	m.persist()
}

// MoveSession rebinds a session to a new worktree and working directory.
// Any running claude process is stopped; the next Send will restart it in newWorkDir.
// The session's conversation history and claudeSID are preserved.
func (m *Manager) MoveSession(sessionID, newWorktreeName, newWorkDir string) error {
	if newWorktreeName == "" {
		return fmt.Errorf("newWorktreeName is required")
	}
	if newWorkDir == "" {
		return fmt.Errorf("newWorkDir is required")
	}

	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("session not found")
	}
	oldWt := s.worktreeName
	if oldWt != newWorktreeName {
		ids := m.worktreeIndex[oldWt]
		for i, id := range ids {
			if id == sessionID {
				m.worktreeIndex[oldWt] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
		if len(m.worktreeIndex[oldWt]) == 0 {
			delete(m.worktreeIndex, oldWt)
		}
		m.worktreeIndex[newWorktreeName] = append(m.worktreeIndex[newWorktreeName], sessionID)
	}
	m.mu.Unlock()

	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.closeAllSubscribers()
	s.started = false
	s.generating = false
	s.stdin = nil
	s.cmd = nil
	s.cancel = nil
	s.worktreeName = newWorktreeName
	s.workDir = newWorkDir
	s.publishStateLocked()
	s.mu.Unlock()

	m.persist()
	return nil
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
	s.publishStateLocked()
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
	s.publishStateLocked()
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
	for i, m := range s.messages {
		result[i] = sanitizeMessage(m)
	}
	return result
}

// MessagesWithPending returns conversation history including any in-progress assistant turn.
// This is used when sending history to a newly-connected WebSocket so the client
// can see content that has already been streamed (e.g., tool_use blocks being executed).
//
// Every returned block is sanitized for invalid RawMessage values — including
// blocks already committed to s.messages. The history is shipped through
// json.Marshal as a single payload, and one bad RawMessage anywhere fails
// the whole payload, so it isn't enough to clean only the in-progress blocks.
func (s *Session) MessagesWithPending() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]Message, len(s.messages))
	for i, msg := range s.messages {
		result[i] = sanitizeMessage(msg)
	}

	// Include the in-progress assistant turn whenever there's anything to show —
	// either previously-completed blocks in currentBlocks OR a block currently
	// streaming. Requiring currentBlocks>0 loses partial text whenever the turn
	// starts with a block type we don't track (e.g., thinking), since that
	// leaves currentBlocks empty while text accumulates on currentStreamBlock.
	if s.generating && (len(s.currentBlocks) > 0 || s.currentStreamBlock != nil) {
		pending := make([]ContentBlock, len(s.currentBlocks))
		copy(pending, s.currentBlocks)
		for i := range pending {
			pending[i].Input = safeRawInput(pending[i].Input)
		}
		// Include partially-built block if mid-stream. Its Input is the
		// streaming buffer which may be partial JSON; include only if valid.
		if s.currentStreamBlock != nil {
			partial := *s.currentStreamBlock
			if partial.Type == "tool_use" && s.streamPartialJSON != "" {
				partial.Input = safeRawInput(json.RawMessage(s.streamPartialJSON))
			} else {
				partial.Input = safeRawInput(partial.Input)
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

// sanitizeMessage returns a copy of msg with every block's Input field
// scrubbed of non-JSON. The original block (still in s.messages) is left
// alone — defensive sanitation should not erase data from the in-memory
// record, only from what we ship over the wire.
func sanitizeMessage(msg Message) Message {
	out := msg
	if len(msg.Content) == 0 {
		return out
	}
	out.Content = make([]ContentBlock, len(msg.Content))
	for i, b := range msg.Content {
		bc := b
		bc.Input = safeRawInput(bc.Input)
		out.Content[i] = bc
	}
	return out
}

// safeRawInput returns input if it is nil or valid JSON, otherwise nil. This
// prevents json.Marshal from failing when a tool_use block holds streaming-
// partial JSON that hasn't accumulated into a valid value yet.
func safeRawInput(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return nil
	}
	if !json.Valid(input) {
		return nil
	}
	return input
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

// CostUSD returns the accumulated API cost for this session in USD.
func (s *Session) CostUSD() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.costUSD
}

// Model returns the most recent model id seen for this session.
func (s *Session) Model() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model
}

// ModelAliases are the model shortcuts offered in the UI picker. The claude
// CLI's --model flag accepts these aliases and resolves each to the latest
// model in that family.
var ModelAliases = []string{"opus", "sonnet", "haiku", "fable"}

// IsValidModelAlias reports whether alias is one trellis is willing to pass to
// --model. The empty string (CLI default) is also valid.
func IsValidModelAlias(alias string) bool {
	if alias == "" {
		return true
	}
	for _, m := range ModelAliases {
		if m == alias {
			return true
		}
	}
	return false
}

// ModelOverride returns the model alias forced via --model, or "" for the
// CLI default.
func (s *Session) ModelOverride() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelOverride
}

// SetModel forces this session onto a model alias (e.g. "opus", "sonnet").
// Pass "" to clear the override and fall back to the CLI default.
//
// A running process is switched live with a set_model control_request so the
// process — and any background tasks it is tracking — survives; the alias is
// also recorded so the next spawn passes it via --model. Only if the live
// switch cannot be delivered (stdin write fails, or the CLI rejects it via a
// control_response error) is the process restarted the old way.
func (s *Session) SetModel(alias string) error {
	if !IsValidModelAlias(alias) {
		return fmt.Errorf("unknown model alias %q", alias)
	}
	s.mu.Lock()
	if s.modelOverride == alias {
		s.mu.Unlock()
		return nil
	}
	s.modelOverride = alias
	running := s.stdin != nil
	persist := s.persistFn
	s.mu.Unlock()
	if persist != nil {
		persist()
	}
	if !running {
		// No process; --model applies on the next spawn.
		return nil
	}
	// The CLI accepts the same aliases as --model; an omitted model field
	// resets to the default. A control_response error routes back through
	// readLoop, which falls back to restartProcess.
	req := stdinControlRequest{
		Type:      "control_request",
		RequestID: setModelReqPrefix + uuid.New().String(),
		Request:   stdinControlInner{Subtype: "set_model", Model: alias},
	}
	if err := s.writeStdin(req); err != nil {
		log.Printf("claude [%s]: set_model write failed, restarting process: %v", s.id, err)
		s.restartProcess()
	}
	return nil
}

// restartProcess kills the running process and resets run state; the next
// Send re-spawns via ensureProcess (resuming the conversation with --resume).
// Subscribers stay attached so connected clients keep their WebSocket.
func (s *Session) restartProcess() {
	s.mu.Lock()
	wasGenerating := s.generating
	if s.cancel != nil {
		s.cancel()
	}
	s.started = false
	s.generating = false
	s.interruptRequested = false
	s.pendingControlRequests = nil
	s.stdin = nil
	s.cmd = nil
	s.cancel = nil
	s.publishStateLocked()
	s.mu.Unlock()
	// See Cancel: clients only leave the generating state on a result event.
	if wasGenerating {
		s.fanOut(StreamEvent{Type: "result", Subtype: "process_restarted"})
	}
}

// PendingControlRequests returns the pending control_request events (oldest
// first). Used to re-display permission prompts to reconnecting clients.
func (s *Session) PendingControlRequests() []*StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*StreamEvent, len(s.pendingControlRequests))
	copy(out, s.pendingControlRequests)
	return out
}

// HasPendingControlRequest reports whether any permission prompt is waiting
// for a user response.
func (s *Session) HasPendingControlRequest() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pendingControlRequests) > 0
}

// removePendingControlRequestLocked drops the pending prompt with the given
// request_id. Caller must hold s.mu. Returns true if one was removed.
func (s *Session) removePendingControlRequestLocked(requestID string) bool {
	for i, p := range s.pendingControlRequests {
		if p.RequestID == requestID {
			s.pendingControlRequests = append(s.pendingControlRequests[:i], s.pendingControlRequests[i+1:]...)
			return true
		}
	}
	return false
}

// publishStateLocked emits a session.state_changed event reflecting the
// session's current derived state. Caller must hold s.mu. Safe no-op if no
// event bus has been wired into the manager.
//
// State is "needs_you" whenever the agent is not actively generating OR a
// permission prompt is pending. Otherwise "running".
//
// Side effects: updates s.unread and s.lastPublishedState based on the
// running ↔ needs-you transition rule. unread is set when running →
// needs-you happens with no viewer present, and cleared whenever state
// returns to running.
func (s *Session) publishStateLocked() {
	if s.manager == nil || s.manager.bus == nil {
		return
	}
	state := events.SessionStateRunning
	if !s.generating || len(s.pendingControlRequests) > 0 {
		state = events.SessionStateNeedsYou
	}
	if state == events.SessionStateNeedsYou &&
		s.lastPublishedState == events.SessionStateRunning &&
		s.viewerCount == 0 {
		s.unread = true
	} else if state == events.SessionStateRunning {
		s.unread = false
	}
	s.lastPublishedState = state

	bus := s.manager.bus
	payload := map[string]interface{}{
		"session_id":   s.id,
		"agent":        "claude",
		"worktree":     s.worktreeName,
		"display_name": s.displayName,
		"state":        state,
		"reason":       s.reasonLocked(),
		"unread":       s.unread,
		"trashed":      s.trashedAt != nil,
	}
	_ = bus.Publish(context.Background(), events.Event{
		Type:     events.EventSessionStateChanged,
		Worktree: s.worktreeName,
		Payload:  payload,
	})
}

// reasonLocked derives the fine-grained inbox reason that refines the coarse
// running/needs-you state for presentation. Caller must hold s.mu.
func (s *Session) reasonLocked() string {
	if len(s.pendingControlRequests) > 0 {
		return events.ReasonNeedsApproval
	}
	if s.generating {
		return events.ReasonRunning
	}
	if s.lastTurnError {
		return events.ReasonError
	}
	return events.ReasonAwaitingInput
}

// Reason returns the fine-grained inbox reason (presentation only).
func (s *Session) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reasonLocked()
}

// CurrentActivity returns the latest human-readable activity description, or
// "" when the session is idle.
func (s *Session) CurrentActivity() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentActivity
}

// setActivityLocked updates the current-activity string and, if it changed,
// publishes a lightweight EventSessionActivity. Caller must hold s.mu. The
// publish is non-blocking (async subscribers drop on a full buffer), matching
// publishStateLocked, so it's safe to call under the lock.
func (s *Session) setActivityLocked(label string) {
	if s.currentActivity == label {
		return
	}
	s.currentActivity = label
	if s.manager == nil || s.manager.bus == nil {
		return
	}
	_ = s.manager.bus.Publish(context.Background(), events.Event{
		Type:     events.EventSessionActivity,
		Worktree: s.worktreeName,
		Payload: map[string]interface{}{
			"session_id": s.id,
			"activity":   label,
		},
	})
}

// toolActivityLabel produces a short human-readable description of a tool_use
// invocation for the inbox activity line. name is the tool name; input is its
// (possibly empty) raw JSON input.
func toolActivityLabel(name string, input json.RawMessage) string {
	switch name {
	case "Bash":
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(input, &in)
		if cmd := strings.TrimSpace(in.Command); cmd != "" {
			return "Running " + clip(firstLine(cmd), 48)
		}
		return "Running command"
	case "Read":
		return "Reading " + fileBase(input)
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return "Editing " + fileBase(input)
	case "Grep":
		var in struct {
			Pattern string `json:"pattern"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Pattern != "" {
			return "Searching " + clip(in.Pattern, 32)
		}
		return "Searching"
	case "Glob":
		return "Finding files"
	case "Task":
		var in struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Description != "" {
			return "Task: " + clip(in.Description, 40)
		}
		return "Running subagent"
	case "WebFetch", "WebSearch":
		return "Searching the web"
	case "TodoWrite":
		return "Updating plan"
	case "":
		return ""
	default:
		return name
	}
}

// fileBase extracts a file path from common tool inputs and returns its base
// name, or "file" when none is present.
func fileBase(input json.RawMessage) string {
	var in struct {
		FilePath     string `json:"file_path"`
		Path         string `json:"path"`
		NotebookPath string `json:"notebook_path"`
	}
	_ = json.Unmarshal(input, &in)
	p := in.FilePath
	if p == "" {
		p = in.Path
	}
	if p == "" {
		p = in.NotebookPath
	}
	if p == "" {
		return "file"
	}
	return filepath.Base(p)
}

// firstLine returns the first line of s.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// clip truncates s to at most n runes, appending an ellipsis when shortened.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// IsUnread reports whether this session has a missed running→needs-you
// transition that the user hasn't viewed yet.
func (s *Session) IsUnread() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unread
}

// BeginView marks the session as being actively viewed by one more client.
// Clears the unread flag if it was set and re-publishes state if so. Pair
// with EndView.
func (s *Session) BeginView() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewerCount++
	if s.unread {
		s.unread = false
		s.publishStateLocked()
	}
}

// EndView decrements the active-viewer count. No publish needed — unread
// state doesn't change just because a viewer disconnected.
func (s *Session) EndView() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.viewerCount > 0 {
		s.viewerCount--
	}
}

// ClearPendingControlRequest removes the pending prompt the client just
// answered, identified by request_id. An empty id clears all pending prompts
// (safety valve for malformed responses so the session can't stick in
// needs_approval).
func (s *Session) ClearPendingControlRequest(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if requestID == "" {
		s.pendingControlRequests = nil
	} else {
		s.removePendingControlRequestLocked(requestID)
	}
	s.publishStateLocked()
}

// Subscribe returns a channel that receives stream events.
// The channel stays open across process restarts and is only closed
// when Unsubscribe is called or the Manager is shut down.
func (s *Session) Subscribe() chan StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan StreamEvent, 10000)
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

// stderrTail forwards the CLI's stderr to trellis's own stderr while keeping
// the last few KB, so an unexpected process exit can be logged together with
// the CLI's final error output (API failures, node crashes, …).
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

const stderrTailMax = 4096

func (t *stderrTail) Write(p []byte) (int, error) {
	os.Stderr.Write(p)
	t.mu.Lock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > stderrTailMax {
		t.buf = t.buf[len(t.buf)-stderrTailMax:]
	}
	t.mu.Unlock()
	return len(p), nil
}

func (t *stderrTail) Tail() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
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
	// Serialize startup. Two concurrent callers (two WS connects, or a connect
	// racing a Send) must not both pass the !started check below and each call
	// cmd.Start(); the second would overwrite s.cmd/s.cancel and orphan the
	// first process (never killed, never reaped). startMu is distinct from s.mu
	// and is never held by readLoop, so this cannot deadlock.
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	workDir := s.workDir
	resumeSID := s.claudeSID
	modelOverride := s.modelOverride
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

	// Force a specific model when the session has an override (alias).
	if modelOverride != "" {
		args = append(args, "--model", modelOverride)
	}

	// The caller's ctx gates only the spawn attempt. The process context is
	// deliberately NOT derived from it: callers pass request- or
	// dispatch-scoped contexts (pair/checklist dispatch uses a short-timeout
	// ctx), and exec.CommandContext SIGKILLs the child the moment its context
	// is canceled — which killed sessions spawned through those paths and
	// orphaned every background task the CLI was tracking. The process must
	// live until an explicit kill path (Cancel, restartProcess, MoveSession,
	// Trash/Delete, Shutdown) calls s.cancel.
	if err := ctx.Err(); err != nil {
		return err
	}
	cmdCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(cmdCtx, "claude", args...)
	cmd.Dir = workDir
	stderr := &stderrTail{}
	cmd.Stderr = stderr

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
	// A fresh process reports total_cost_usd from zero again.
	s.lastProcessCost = 0
	s.mu.Unlock()

	go s.readLoop(stdoutPipe, cmd, gen, stderr)

	return nil
}

// readLoop reads NDJSON events from claude's stdout continuously.
func (s *Session) readLoop(stdout io.Reader, cmd *exec.Cmd, gen int, stderr *stderrTail) {
	// bufio.Reader has no fixed token cap — unlike bufio.Scanner which dies
	// with "token too long" on large events (big tool results, file contents).
	reader := bufio.NewReaderSize(stdout, 1024*1024)
	var readErr error

	for {
		line, err := readLine(reader)
		if err != nil {
			readErr = err
			break
		}
		if len(line) == 0 {
			continue
		}

		var event StreamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			log.Printf("claude: failed to parse NDJSON: %v", err)
			continue
		}

		if debugEvents {
			log.Printf("claude [%s]: event: %s", s.id, truncateForLog(line, maxEventLogBytes))
		}

		// Events emitted by a Task subagent carry parent_tool_use_id. Route them
		// to a lightweight live "subagent_activity" signal and skip the normal
		// processing below so they never pollute the main transcript or the
		// main session's activity label.
		if event.ParentToolUseID != "" {
			s.handleSubagentEvent(event)
			continue
		}

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
		case "system", "assistant", "result", "control_request", "control_response", "control_cancel_request", "stream_event", "user":
			// Known types
		case "rate_limit_event":
			// Emitted as rate limits are approached/hit; intentionally not
			// surfaced in the UI (see claude.js handleStreamEvent).
		default:
			log.Printf("claude: unknown event type %q: %s", event.Type, line)
		}

		// Acknowledgements for control requests we sent (interrupt,
		// set_model). Success needs no action — interrupts end via the turn's
		// result event, model switches apply silently. A rejected set_model
		// falls back to the restart path so --model applies on respawn.
		if event.Type == "control_response" && event.Response != nil {
			var resp struct {
				Subtype   string `json:"subtype"`
				RequestID string `json:"request_id"`
				Error     string `json:"error"`
			}
			if json.Unmarshal(event.Response, &resp) == nil && resp.Subtype == "error" {
				log.Printf("claude [%s]: control request %s rejected: %s", s.id, resp.RequestID, resp.Error)
				if strings.HasPrefix(resp.RequestID, setModelReqPrefix) {
					s.restartProcess()
				}
			}
		}

		// Accumulate content blocks from stream_event (--include-partial-messages)
		if event.Type == "stream_event" && event.Event != nil {
			s.handleStreamEvent(event.Event)
		}

		// Extract usage and content from assistant events
		if event.Type == "assistant" && event.Message != nil {
			var msg struct {
				Content []ContentBlock `json:"content"`
				Model   string         `json:"model"`
				Usage   struct {
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					OutputTokens             int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(event.Message, &msg) == nil {
				s.mu.Lock()
				if msg.Model != "" {
					s.model = msg.Model
				}
				total := msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens
				if total > 0 {
					s.inputTokens = total
					s.inputTokensBase = msg.Usage.InputTokens
					s.cacheCreationInputTokens = msg.Usage.CacheCreationInputTokens
					s.cacheReadInputTokens = msg.Usage.CacheReadInputTokens
				}
				// Only accumulate content blocks when not using stream events
				var planBlocks []ContentBlock
				if !s.hasStreamEvents && len(msg.Content) > 0 {
					workDir := s.workDir
					s.mu.Unlock()
					for i := range msg.Content {
						enrichEditBlock(&msg.Content[i], workDir)
						enrichWriteBlock(&msg.Content[i], workDir)
					}
					s.mu.Lock()
					s.currentBlocks = append(s.currentBlocks, msg.Content...)
					for _, b := range msg.Content {
						if b.Type == "tool_use" && b.Name == "ExitPlanMode" {
							planBlocks = append(planBlocks, b)
						}
					}
				}
				s.mu.Unlock()
				for _, b := range planBlocks {
					s.maybeCapturePlan(b)
				}
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
			if event.Cost > 0 {
				if event.Cost >= s.lastProcessCost {
					s.costUSD += event.Cost - s.lastProcessCost
				} else {
					// The CLI restarted its accounting; count the full amount.
					s.costUSD += event.Cost
				}
				s.lastProcessCost = event.Cost
			}
			s.generating = false
			// An interrupted turn reports is_error (error_during_execution),
			// but the user asked for the stop — don't surface it as an error.
			s.lastTurnError = event.IsError && !s.interruptRequested
			s.interruptRequested = false
			s.currentBlocks = nil
			// Pending permission prompts deliberately survive turn end:
			// background agents block on prompts long after the turn that
			// spawned them completes. Clearing here silently killed those
			// agents. Moot prompts are retracted by the CLI itself via
			// control_cancel_request.
			s.setActivityLocked("")
			s.publishStateLocked()
			persist := s.persistFn
			s.mu.Unlock()
			if persist != nil {
				persist()
			}
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
			// Replace any stale prompt with the same request_id, then append.
			s.removePendingControlRequestLocked(eventCopy.RequestID)
			s.pendingControlRequests = append(s.pendingControlRequests, &eventCopy)
			s.publishStateLocked()
			s.mu.Unlock()
		}

		// The CLI retracts a pending prompt that became moot (e.g. its turn
		// was interrupted). Drop it so the session doesn't stick in
		// needs_approval; the event also fans out so live clients can disable
		// the prompt block.
		if event.Type == "control_cancel_request" && event.RequestID != "" {
			s.mu.Lock()
			if s.removePendingControlRequestLocked(event.RequestID) {
				s.publishStateLocked()
			}
			s.mu.Unlock()
		}

		// Fan out to all subscribers
		s.fanOut(event)
	}

	if readErr != nil && readErr != io.EOF {
		log.Printf("claude [%s]: read error: %v", s.id, readErr)
	}

	// Process exited (or read errored) - wait for it and clean up.
	waitErr := cmd.Wait()

	wasGenerating := false
	cleanedUp := false
	s.mu.Lock()
	// Only clean up session state if we're still the current generation.
	// A newer process may have already been started by ensureProcess.
	if s.processGen == gen {
		cleanedUp = true
		wasGenerating = s.generating
		// If the stream died mid-turn (no "result" event ever arrived), commit
		// any accumulated in-progress blocks as a completed assistant message
		// so reconnecting clients can still see what was generated. Include
		// the partial currentStreamBlock too — otherwise text accumulating on
		// it (e.g., when the turn started with a thinking block) is lost.
		pending := s.currentBlocks
		if s.currentStreamBlock != nil {
			partial := *s.currentStreamBlock
			if partial.Type == "tool_use" && s.streamPartialJSON != "" && json.Valid([]byte(s.streamPartialJSON)) {
				partial.Input = json.RawMessage(s.streamPartialJSON)
			}
			pending = append(pending, partial)
		}
		if len(pending) > 0 {
			msg := Message{
				Role:      "assistant",
				Content:   pending,
				Timestamp: time.Now(),
			}
			s.messages = append(s.messages, msg)
			s.persistMessage(msg)
			s.currentBlocks = nil
			s.currentStreamBlock = nil
			s.streamPartialJSON = ""
		}
		s.started = false
		s.generating = false
		s.interruptRequested = false
		// A dead process can never receive permission responses; drop its
		// pending prompts so the session doesn't stick in needs_approval.
		s.pendingControlRequests = nil
		s.stdin = nil
		s.cmd = nil
		s.cancel = nil
		s.setActivityLocked("")
		s.publishStateLocked()
	}
	s.mu.Unlock()

	// Always log process exits with their cause. Explicit kill paths (Cancel,
	// restartProcess, Move/Trash/Delete, Shutdown) show up as signal: killed;
	// anything else here is the CLI dying on its own — the prime suspect when
	// background tasks are later reported as orphaned — so make it visible.
	exitDesc := "exit status 0"
	if waitErr != nil {
		exitDesc = waitErr.Error()
	}
	log.Printf("claude [%s]: process exited (%s, gen %d, mid-turn=%v, current=%v)",
		s.id, exitDesc, gen, wasGenerating, cleanedUp)
	// "signal: killed" is our own kill paths; anything else that isn't a
	// clean exit deserves its stderr for diagnosis.
	if waitErr != nil && exitDesc != "signal: killed" {
		if tail := stderr.Tail(); tail != "" {
			log.Printf("claude [%s]: stderr tail: %s", s.id, truncateForLog([]byte(tail), 1024))
		}
	}

	// A turn that dies with the process never gets a result event, and the WS
	// layer only emits "done" on result events — without this, connected
	// clients sit on "generating" forever after a process crash.
	if cleanedUp && wasGenerating {
		s.fanOut(StreamEvent{Type: "result", Subtype: "process_exited", IsError: true})
	}
}

// readLine reads a single newline-terminated line from r, returning the line
// without the trailing newline. It handles arbitrarily large lines by growing
// as needed (unlike bufio.Scanner). Returns io.EOF with any remaining bytes
// on end-of-stream.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			buf = append(buf, chunk...)
			continue
		}
		if err != nil {
			if len(buf) == 0 && len(chunk) == 0 {
				return nil, err
			}
			buf = append(buf, chunk...)
			return buf, err
		}
		buf = append(buf, chunk...)
		// Strip trailing newline (and optional \r).
		if n := len(buf); n > 0 && buf[n-1] == '\n' {
			buf = buf[:n-1]
			if n := len(buf); n > 0 && buf[n-1] == '\r' {
				buf = buf[:n-1]
			}
		}
		return buf, nil
	}
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
		s.mu.Lock()
		s.setActivityLocked("Thinking…")
		s.mu.Unlock()
		// Extract input_tokens for context window tracking
		// Sum input_tokens + cache tokens for total context usage
		if inner.Message != nil {
			var msg struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(inner.Message, &msg) == nil {
				total := msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens
				if total > 0 || msg.Model != "" {
					s.mu.Lock()
					if msg.Model != "" {
						s.model = msg.Model
					}
					if total > 0 {
						s.inputTokens = total
						s.inputTokensBase = msg.Usage.InputTokens
						s.cacheCreationInputTokens = msg.Usage.CacheCreationInputTokens
						s.cacheReadInputTokens = msg.Usage.CacheReadInputTokens
					}
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
			s.setActivityLocked("Responding…")
		case "tool_use":
			s.currentStreamBlock = &ContentBlock{Type: "tool_use", ID: cb.ID, Name: cb.Name}
			s.streamPartialJSON = ""
			// Provisional label (no input yet); enriched at content_block_stop.
			s.setActivityLocked(toolActivityLabel(cb.Name, nil))
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
			// Only commit the accumulated partial_json as Input if it
			// parses. The CLI normally sends a valid object by stop time,
			// but model glitches and stream interruptions have been seen
			// to leave the buffer in a half-built state — and once an
			// invalid RawMessage is committed to s.messages, every
			// history marshal on reconnect fails with "invalid character
			// … looking for beginning of object key string". The
			// stream-died-mid-turn path (line ~1031) already guards this
			// way; this path needs to do the same.
			if s.currentStreamBlock.Type == "tool_use" && s.streamPartialJSON != "" {
				if json.Valid([]byte(s.streamPartialJSON)) {
					s.currentStreamBlock.Input = json.RawMessage(s.streamPartialJSON)
				} else {
					log.Printf("claude [%s]: dropping invalid tool_use input JSON (%d bytes) on content_block_stop", s.id, len(s.streamPartialJSON))
				}
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
			if block.Type == "tool_use" {
				s.setActivityLocked(toolActivityLabel(block.Name, block.Input))
			}
			s.mu.Unlock()

			// Persist the plan artifact when a plan-mode turn completes
			s.maybeCapturePlan(block)

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

// handleSubagentEvent turns a parent_tool_use_id-tagged event from a Task
// subagent into a lightweight, live "subagent_activity" signal for the
// matching Task block. It derives a short label (the subagent's current tool)
// and a running step count, then fans the result out to live clients. It never
// mutates the main transcript or the main session activity.
func (s *Session) handleSubagentEvent(event StreamEvent) {
	parent := event.ParentToolUseID
	label := ""
	stepped := false

	switch event.Type {
	case "stream_event":
		if event.Event == nil {
			return
		}
		var inner struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if json.Unmarshal(event.Event, &inner) != nil {
			return
		}
		if inner.Type != "content_block_start" {
			return // ignore deltas/start/stop chatter; tool starts carry the signal
		}
		switch inner.ContentBlock.Type {
		case "tool_use":
			label = toolActivityLabel(inner.ContentBlock.Name, nil)
			stepped = true
		case "text", "thinking":
			label = "Responding…"
		default:
			return
		}
	case "assistant":
		if event.Message == nil {
			return
		}
		var msg struct {
			Content []struct {
				Type string          `json:"type"`
				Name string          `json:"name"`
				Text string          `json:"text"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		}
		if json.Unmarshal(event.Message, &msg) != nil {
			return
		}
		for _, b := range msg.Content {
			if b.Type == "tool_use" {
				label = toolActivityLabel(b.Name, b.Input)
				break
			}
			if b.Type == "text" && b.Text != "" && label == "" {
				label = "Responding…"
			}
		}
	default:
		return // user (tool_result), system, etc. — no label change
	}

	if label == "" {
		return
	}

	s.mu.Lock()
	if s.subagentSteps == nil {
		s.subagentSteps = make(map[string]int)
	}
	if stepped {
		s.subagentSteps[parent]++
	}
	step := s.subagentSteps[parent]
	s.mu.Unlock()

	s.fanOut(StreamEvent{
		Type:            "subagent_activity",
		ParentToolUseID: parent,
		Activity:        label,
		Step:            step,
	})
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
	s.turnGen++
	s.interruptRequested = false
	s.currentBlocks = nil
	s.hasStreamEvents = false
	s.lastTurnError = false
	s.subagentSteps = nil

	// Add user message to history
	userMsg := Message{
		Role:      "user",
		Content:   []ContentBlock{{Type: "text", Text: prompt}},
		Timestamp: time.Now(),
	}
	s.messages = append(s.messages, userMsg)
	s.persistMessage(userMsg)
	s.setActivityLocked("Thinking…")
	s.publishStateLocked()
	s.mu.Unlock()

	// Ensure process is running
	if err := s.ensureProcess(ctx); err != nil {
		s.mu.Lock()
		s.generating = false
		s.publishStateLocked()
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
		s.publishStateLocked()
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

// Cancel kills the running Claude process. This is the hard stop: any
// background tasks the CLI is tracking die with the process. Prefer Interrupt
// for user-initiated "stop this turn" — it leaves the process alive. The
// process will be restarted on the next Send call.
func (s *Session) Cancel() {
	s.mu.Lock()
	wasGenerating := s.generating
	if s.cancel != nil {
		s.cancel()
	}
	s.generating = false
	s.interruptRequested = false
	s.pendingControlRequests = nil
	s.started = false
	s.stdin = nil
	s.cmd = nil
	s.cancel = nil
	s.setActivityLocked("")
	s.publishStateLocked()
	s.mu.Unlock()
	// No result event ever arrives for a turn killed with the process, and the
	// WS layer only emits "done" on result events — fan out a synthetic one so
	// every connected client leaves the generating state.
	if wasGenerating {
		s.fanOut(StreamEvent{Type: "result", Subtype: "turn_cancelled"})
	}
}

// Interrupt asks the running claude process to abort the in-flight turn via a
// stream-json control_request. Unlike Cancel, the process — and any background
// tasks the CLI is tracking — stays alive; only the current turn stops. The
// aborted turn still emits a result event (subtype error_during_execution),
// which ends the turn through the normal readLoop path.
//
// Returns true when an interrupt was dispatched; false when there was nothing
// to interrupt (no process or no turn in flight) or the write failed and the
// process was killed instead — either way the caller should treat the stop as
// already complete.
//
// If the CLI hasn't honored the interrupt within interruptTimeout, the
// process is killed as a fallback so the session can't get stuck generating.
func (s *Session) Interrupt() bool {
	s.mu.Lock()
	if s.stdin == nil || !s.generating {
		s.mu.Unlock()
		return false
	}
	s.interruptRequested = true
	turn := s.turnGen
	s.mu.Unlock()

	req := stdinControlRequest{
		Type:      "control_request",
		RequestID: interruptReqPrefix + uuid.New().String(),
		Request:   stdinControlInner{Subtype: "interrupt"},
	}
	if err := s.writeStdin(req); err != nil {
		log.Printf("claude [%s]: interrupt write failed, killing process: %v", s.id, err)
		s.Cancel()
		return false
	}

	time.AfterFunc(interruptTimeout, func() {
		s.mu.Lock()
		stuck := s.generating && s.turnGen == turn
		s.mu.Unlock()
		if stuck {
			log.Printf("claude [%s]: interrupt not honored within %s; killing process", s.id, interruptTimeout)
			s.Cancel()
		}
	})
	return true
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
	for i, m := range s.messages {
		messages[i] = sanitizeMessage(m)
	}
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

// ForkSession creates a new session in the same worktree as sourceSessionID,
// pre-populated with the source's messages[0..messageIndex] (inclusive). This
// is a "split at this point" operation: the new session inherits the prefix,
// the source session is untouched, and the new session's Claude CLI JSONL is
// written so --resume continues from exactly that point.
//
// messageIndex is 0-based and inclusive: fork at index 3 → new session has
// messages[0..3] (4 messages). Out-of-range indices are clamped.
func (m *Manager) ForkSession(sourceSessionID string, messageIndex int, displayName string) (*Session, error) {
	m.mu.Lock()
	src, ok := m.sessions[sourceSessionID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("source session not found")
	}

	src.mu.Lock()
	if len(src.messages) == 0 {
		src.mu.Unlock()
		return nil, fmt.Errorf("source session has no messages to fork")
	}
	// Clamp the requested index.
	if messageIndex < 0 {
		messageIndex = 0
	}
	if messageIndex >= len(src.messages) {
		messageIndex = len(src.messages) - 1
	}
	// Never carry a trailing unanswered user question into the fork: walk back
	// over trailing user messages so the new session ends on the last actual
	// response. The UI only offers Fork on assistant messages, so this normally
	// no-ops; it also guards direct API callers passing a user-message index.
	end := messageIndex
	for end >= 0 && src.messages[end].Role == "user" {
		end--
	}
	if end < 0 {
		src.mu.Unlock()
		return nil, fmt.Errorf("cannot fork before the first response")
	}
	prefix := make([]Message, end+1)
	copy(prefix, src.messages[:end+1])
	worktreeName := src.worktreeName
	workDir := src.workDir
	src.mu.Unlock()

	if displayName == "" {
		displayName = "Fork"
	}

	newSession := m.CreateSession(worktreeName, workDir, displayName)

	cliSessionID, err := WriteCLISessionFile(workDir, workDir, "", prefix)
	if err != nil {
		return newSession, fmt.Errorf("write CLI session: %w", err)
	}

	newSession.mu.Lock()
	newSession.claudeSID = cliSessionID
	newSession.messages = prefix
	newSession.persistAllMessages()
	newSession.mu.Unlock()

	m.persist()
	return newSession, nil
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
	s.publishStateLocked()
	s.persistAllMessages()
}
