// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package codex manages OpenAI Codex CLI sessions for Trellis. Each session
// owns a long-running `codex app-server` subprocess and speaks JSON-RPC 2.0
// to it over stdio. Mirrors the role of internal/claude but adapted to the
// Codex app-server protocol (thread/turn/item) instead of Claude's NDJSON
// stream-json.
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wingedpig/trellis/internal/agentmsg"
)

// Message is the persisted form of one conversational turn.
//
// Codex's per-turn output can include multiple items (assistant message
// fragments, command executions, file changes, reasoning, etc.) in order.
// We persist them as Items so the UI can re-render the full turn faithfully.
type Message struct {
	Role      string    `json:"role"` // "user" | "assistant"
	Items     []Item    `json:"items"`
	Timestamp time.Time `json:"timestamp"`
	TurnID    string    `json:"turn_id,omitempty"` // Codex turn id (for fork-from-this-turn)
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

// StreamEvent is a fan-out unit pushed to subscribers (the WebSocket bridge).
//
// The shape is designed for direct passthrough to a JS client. Type values:
//
//	"turn_started", "turn_completed", "turn_failed"
//	"item_started", "item_completed"
//	"agent_message_delta", "command_output_delta"
//	"approval_request"   (server is asking for permission; carries RequestID)
//	"thread_started"
//	"diff_updated", "plan_updated"
//	"error"              (fatal — session needs restart)
type StreamEvent struct {
	Type      string          `json:"type"`
	ThreadID  string          `json:"thread_id,omitempty"`
	TurnID    string          `json:"turn_id,omitempty"`
	ItemID    string          `json:"item_id,omitempty"`
	Item      *Item           `json:"item,omitempty"`
	Delta     string          `json:"delta,omitempty"`
	Stream    string          `json:"stream,omitempty"` // "stdout" | "stderr"
	Error     string          `json:"error,omitempty"`
	Method    string          `json:"method,omitempty"` // for approval requests
	RequestID string          `json:"request_id,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// TokenUsage mirrors Codex's `thread/tokenUsage/updated` payload.
type TokenUsage struct {
	TotalTokens          int `json:"total_tokens"`
	InputTokens          int `json:"input_tokens"`
	CachedInputTokens    int `json:"cached_input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
}

// Session holds per-session Codex state with a long-running app-server.
type Session struct {
	mu sync.Mutex

	id           string
	displayName  string
	worktreeName string
	threadID     string // Codex thread id; empty until thread/start succeeds
	workDir      string
	createdAt    time.Time
	trashedAt    *time.Time

	messages    []Message
	subscribers map[chan StreamEvent]struct{}

	// Most recent thread-level token usage from thread/tokenUsage/updated.
	tokenUsage TokenUsage

	// Process / RPC state
	cmd    *exec.Cmd
	cancel context.CancelFunc
	rpc    *Client
	rpcGen int // generation counter so stale Run goroutines don't clobber state

	// Turn lifecycle
	started    bool   // app-server process running and initialized
	generating bool   // a turn is in flight
	currentTurnID string
	currentItems  map[string]*Item // by item id, accumulating during the turn
	currentOrder  []string         // item ids in arrival order

	// Pending approval requests (carried across reconnects so a fresh
	// WebSocket can re-show the prompt). Keyed by RequestID.
	pendingApprovals map[string]StreamEvent
	// approvalChans correlates a pending approval RequestID to the channel
	// the JSON-RPC handler is parked on, so AnswerApproval can deliver the
	// user's decision back to that handler.
	approvalChans map[string]chan ApprovalDecision

	// Persistence
	persistFn    func()
	messagesFile string
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
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].Role == agentmsg.RoleUser {
			info.LastUserInput = s.messages[i].Timestamp
			break
		}
	}
	return info
}

// Messages returns a copy of conversation history.
func (s *Session) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// MessagesWithPending includes the in-progress assistant turn (items
// accumulated so far) so a reconnecting client sees streamed content.
func (s *Session) MessagesWithPending() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.messages))
	copy(out, s.messages)

	if s.generating && len(s.currentOrder) > 0 {
		items := make([]Item, 0, len(s.currentOrder))
		for _, id := range s.currentOrder {
			if it, ok := s.currentItems[id]; ok && it != nil {
				items = append(items, *it)
			}
		}
		if len(items) > 0 {
			out = append(out, Message{
				Role:      agentmsg.RoleAssistant,
				Items:     items,
				Timestamp: time.Now(),
				TurnID:    s.currentTurnID,
			})
		}
	}
	return out
}

// IsGenerating returns whether a turn is in flight.
func (s *Session) IsGenerating() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generating
}

// TokenUsage returns the most recent thread-level token usage.
func (s *Session) TokenUsage() TokenUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenUsage
}

// PendingApprovals returns any approval prompts the server has sent that
// haven't been responded to yet. Used to re-display them on reconnect.
func (s *Session) PendingApprovals() []StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StreamEvent, 0, len(s.pendingApprovals))
	for _, e := range s.pendingApprovals {
		out = append(out, e)
	}
	return out
}

// Subscribe returns a channel that receives stream events. Stays open across
// process restarts; close via Unsubscribe.
func (s *Session) Subscribe() chan StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan StreamEvent, 10000)
	s.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe removes and closes a subscriber channel.
func (s *Session) Unsubscribe(ch chan StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subscribers[ch]; ok {
		delete(s.subscribers, ch)
		close(ch)
	}
}

func (s *Session) closeAllSubscribers() {
	for ch := range s.subscribers {
		close(ch)
	}
	s.subscribers = make(map[chan StreamEvent]struct{})
}

func (s *Session) fanOut(ev StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
}

// persistMessage appends a message to disk asynchronously. Caller may hold s.mu.
func (s *Session) persistMessage(msg Message) {
	if s.messagesFile == "" {
		return
	}
	file := s.messagesFile
	id := s.id
	go func() {
		if err := appendMessage(file, msg); err != nil {
			log.Printf("codex [%s]: failed to persist message: %v", id, err)
		}
	}()
}

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
			log.Printf("codex [%s]: failed to rewrite messages: %v", id, err)
		}
	}()
}

// ---------------- process lifecycle ----------------

// EnsureProcess starts the codex app-server if not already running and
// initializes a thread (creating a new one or resuming the persisted one).
func (s *Session) EnsureProcess(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	workDir := s.workDir
	resumeID := s.threadID
	gen := s.rpcGen + 1
	s.rpcGen = gen
	s.mu.Unlock()

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "codex", "app-server")
	cmd.Dir = workDir
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start codex app-server: %w", err)
	}

	rpc := NewClient(stdout, stdin, s.handleNotification, s.handleServerRequest)

	s.mu.Lock()
	s.cmd = cmd
	s.cancel = cancel
	s.rpc = rpc
	s.started = true
	s.mu.Unlock()

	// Run the JSON-RPC read loop. When it exits (process died / EOF), reset
	// session state so the next Send re-spawns.
	go func() {
		err := rpc.Run(cmdCtx)
		_ = cmd.Wait()
		s.mu.Lock()
		if s.rpcGen == gen {
			s.started = false
			s.generating = false
			s.cancel = nil
			s.cmd = nil
			s.rpc = nil
			// Commit any in-progress turn so reconnecting clients see it
			if len(s.currentOrder) > 0 {
				items := make([]Item, 0, len(s.currentOrder))
				for _, id := range s.currentOrder {
					if it, ok := s.currentItems[id]; ok && it != nil {
						items = append(items, *it)
					}
				}
				if len(items) > 0 {
					msg := Message{
						Role:      agentmsg.RoleAssistant,
						Items:     items,
						Timestamp: time.Now(),
						TurnID:    s.currentTurnID,
					}
					s.messages = append(s.messages, msg)
					s.persistMessage(msg)
				}
				s.currentItems = nil
				s.currentOrder = nil
				s.currentTurnID = ""
			}
		}
		s.mu.Unlock()
		if err != nil && err != io.EOF {
			log.Printf("codex [%s]: rpc loop ended: %v", s.id, err)
		}
	}()

	// 1. initialize handshake
	if _, err := rpc.Call(cmdCtx, "initialize", initializeParams{
		ClientInfo:   clientInfo{Name: "trellis", Version: "1"},
		Capabilities: struct{}{},
	}); err != nil {
		s.shutdownProcess()
		return fmt.Errorf("initialize: %w", err)
	}
	// 2. notify initialized
	if err := rpc.Notify("initialized", struct{}{}); err != nil {
		s.shutdownProcess()
		return fmt.Errorf("initialized: %w", err)
	}

	// 3. Get a thread id. If we have a persisted one, ask the server to
	//    resume it (loads the rollout). If resume fails or we have none,
	//    fall through to allocateThreadID which calls thread/start.
	if resumeID != "" {
		if _, err := rpc.Call(cmdCtx, "thread/resume", threadResumeParams{
			ThreadID:     resumeID,
			Cwd:          workDir,
			ExcludeTurns: true,
		}); err != nil {
			log.Printf("codex [%s]: resume failed (%v), starting fresh thread", s.id, err)
			s.mu.Lock()
			s.threadID = ""
			persist := s.persistFn
			s.mu.Unlock()
			if persist != nil {
				persist()
			}
		} else {
			s.fanOut(StreamEvent{Type: "thread_started", ThreadID: resumeID})
		}
	}

	return nil
}

// allocateThreadID returns the session's threadId, calling thread/start to
// create a new one if needed.
func (s *Session) allocateThreadID(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.threadID != "" {
		id := s.threadID
		s.mu.Unlock()
		return id, nil
	}
	rpc := s.rpc
	workDir := s.workDir
	s.mu.Unlock()

	if rpc == nil {
		return "", fmt.Errorf("rpc not initialized")
	}
	raw, err := rpc.Call(ctx, "thread/start", threadStartParams{Cwd: workDir})
	if err != nil {
		return "", fmt.Errorf("thread/start: %w", err)
	}
	var res threadStartResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("decode thread/start: %w", err)
	}
	if res.Thread.ID == "" {
		return "", fmt.Errorf("thread/start returned empty thread id")
	}
	s.mu.Lock()
	s.threadID = res.Thread.ID
	persist := s.persistFn
	s.mu.Unlock()
	if persist != nil {
		persist()
	}
	s.fanOut(StreamEvent{Type: "thread_started", ThreadID: res.Thread.ID})
	return res.Thread.ID, nil
}

// Send writes a user message and starts a turn.
func (s *Session) Send(ctx context.Context, prompt string) error {
	if err := s.EnsureProcess(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	if s.generating {
		s.mu.Unlock()
		return fmt.Errorf("already generating")
	}
	s.generating = true
	s.currentItems = make(map[string]*Item)
	s.currentOrder = nil
	s.currentTurnID = ""
	rpc := s.rpc
	threadID := s.threadID

	userMsg := Message{
		Role:      agentmsg.RoleUser,
		Items:     []Item{{Type: "userMessage", Text: prompt}},
		Timestamp: time.Now(),
	}
	s.messages = append(s.messages, userMsg)
	s.persistMessage(userMsg)
	s.mu.Unlock()

	if rpc == nil {
		s.markNotGenerating()
		return fmt.Errorf("rpc not initialized")
	}

	// Ensure we have a threadId; brand-new sessions create one via thread/start.
	if threadID == "" {
		var err error
		threadID, err = s.allocateThreadID(ctx)
		if err != nil {
			s.markNotGenerating()
			return err
		}
	}

	// turn/start returns synchronously (response carries turnId), but we also
	// receive turn/started as a notification. We don't need the response —
	// the notification path is authoritative for streaming.
	if _, err := rpc.Call(ctx, "turn/start", turnStartParams{
		ThreadID: threadID,
		Cwd:      s.workDir,
		Input: []UserInput{{
			Type:         "text",
			Text:         prompt,
			TextElements: []interface{}{},
		}},
	}); err != nil {
		s.markNotGenerating()
		return fmt.Errorf("turn/start: %w", err)
	}
	return nil
}

// Cancel interrupts the current turn (if any) and stops the process.
// The next Send will re-spawn.
func (s *Session) Cancel() {
	s.mu.Lock()
	rpc := s.rpc
	threadID := s.threadID
	turnID := s.currentTurnID
	s.mu.Unlock()
	if rpc != nil && turnID != "" {
		_, _ = rpc.Call(context.Background(), "turn/interrupt", turnInterruptParams{
			ThreadID: threadID,
			TurnID:   turnID,
		})
	}
	s.shutdownProcess()
}

// shutdownProcess kills the app-server cleanly. Idempotent.
func (s *Session) shutdownProcess() {
	s.mu.Lock()
	cancel := s.cancel
	rpc := s.rpc
	s.cancel = nil
	s.rpc = nil
	s.cmd = nil
	s.started = false
	s.generating = false
	s.mu.Unlock()
	if rpc != nil {
		rpc.Close()
	}
	if cancel != nil {
		cancel()
	}
}

func (s *Session) markNotGenerating() {
	s.mu.Lock()
	s.generating = false
	s.mu.Unlock()
}

// ---------------- notification & request dispatch ----------------

// handleNotification is called by the JSON-RPC client for every notification
// from the app-server. We translate Codex's wire shapes into StreamEvents
// for the WebSocket bridge and update message state.
func (s *Session) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "thread/started":
		var p threadStartedNotification
		_ = json.Unmarshal(params, &p)
		s.fanOut(StreamEvent{Type: "thread_started", ThreadID: p.Thread.ID})

	case "turn/started":
		var p turnEvent
		_ = json.Unmarshal(params, &p)
		s.mu.Lock()
		s.generating = true
		if s.currentTurnID == "" {
			s.currentTurnID = p.Turn.ID
		}
		s.mu.Unlock()
		s.fanOut(StreamEvent{Type: "turn_started", ThreadID: p.ThreadID, TurnID: p.Turn.ID})

	case "turn/completed":
		var p turnEvent
		_ = json.Unmarshal(params, &p)
		s.commitTurn(p.Turn.ID, "")
		s.fanOut(StreamEvent{Type: "turn_completed", ThreadID: p.ThreadID, TurnID: p.Turn.ID})

	case "turn/failed":
		var p turnEvent
		_ = json.Unmarshal(params, &p)
		errMsg := string(p.Turn.Error)
		s.commitTurn(p.Turn.ID, errMsg)
		s.fanOut(StreamEvent{Type: "turn_failed", ThreadID: p.ThreadID, TurnID: p.Turn.ID, Error: errMsg})

	case "item/started":
		var p itemEvent
		_ = json.Unmarshal(params, &p)
		s.recordItem(p.Item)
		ev := StreamEvent{Type: "item_started", ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.Item.ID, Item: itemPtr(p.Item)}
		s.fanOut(ev)

	case "item/completed":
		var p itemEvent
		_ = json.Unmarshal(params, &p)
		s.recordItem(p.Item)
		ev := StreamEvent{Type: "item_completed", ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.Item.ID, Item: itemPtr(p.Item)}
		s.fanOut(ev)

	case "item/agentMessage/delta":
		var p agentMessageDelta
		_ = json.Unmarshal(params, &p)
		s.appendItemText(p.ItemID, p.Delta)
		s.fanOut(StreamEvent{Type: "agent_message_delta", ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.ItemID, Delta: p.Delta})

	case "item/commandExecution/outputDelta":
		var p commandExecutionOutputDelta
		_ = json.Unmarshal(params, &p)
		s.appendItemOutput(p.ItemID, p.Delta)
		s.fanOut(StreamEvent{Type: "command_output_delta", ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.ItemID, Stream: p.Stream, Delta: p.Delta})

	case "turn/diff/updated":
		s.fanOut(StreamEvent{Type: "diff_updated", Params: params})

	case "turn/plan/updated":
		s.fanOut(StreamEvent{Type: "plan_updated", Params: params})

	case "thread/tokenUsage/updated":
		var p struct {
			ThreadID   string `json:"threadId"`
			TurnID     string `json:"turnId"`
			TokenUsage struct {
				Total struct {
					TotalTokens           int `json:"totalTokens"`
					InputTokens           int `json:"inputTokens"`
					CachedInputTokens     int `json:"cachedInputTokens"`
					OutputTokens          int `json:"outputTokens"`
					ReasoningOutputTokens int `json:"reasoningOutputTokens"`
				} `json:"total"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(params, &p) == nil {
			usage := TokenUsage{
				TotalTokens:           p.TokenUsage.Total.TotalTokens,
				InputTokens:           p.TokenUsage.Total.InputTokens,
				CachedInputTokens:     p.TokenUsage.Total.CachedInputTokens,
				OutputTokens:          p.TokenUsage.Total.OutputTokens,
				ReasoningOutputTokens: p.TokenUsage.Total.ReasoningOutputTokens,
			}
			s.mu.Lock()
			s.tokenUsage = usage
			s.mu.Unlock()
			s.fanOut(StreamEvent{Type: "token_usage", Params: params})
		}

	default:
		// Unknown — pass through with raw params so the UI can decide.
		s.fanOut(StreamEvent{Type: method, Method: method, Params: params})
	}
}

// handleServerRequest is called when the app-server sends a JSON-RPC request
// (typically an approval prompt). We stash it as a pending approval, fan
// out a StreamEvent so the UI can prompt the user, and *block* until the
// user responds. The user's response is delivered via a channel keyed by
// the request id.
//
// Concurrency note: ServerRequestHandler runs on its own goroutine inside
// the JSON-RPC client (see handleServerRequest in jsonrpc.go), so blocking
// here doesn't stall the read loop for other notifications/responses.
func (s *Session) handleServerRequest(method string, params json.RawMessage) (any, *RPCError) {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		// Allocate an internal id so the user can respond out-of-band via
		// the WebSocket. We never actually use this id in the JSON-RPC
		// reply — we send the reply synchronously when the user picks.
		reqID := uuid.New().String()
		respCh := make(chan ApprovalDecision, 1)

		ev := StreamEvent{
			Type:      "approval_request",
			Method:    method,
			RequestID: reqID,
			Params:    params,
		}

		s.mu.Lock()
		if s.pendingApprovals == nil {
			s.pendingApprovals = make(map[string]StreamEvent)
		}
		s.pendingApprovals[reqID] = ev
		s.approvalChans[reqID] = respCh
		s.mu.Unlock()

		s.fanOut(ev)

		// Wait for the user's response. Use a long timeout so an idle prompt
		// doesn't hang forever, but long enough that the user can step away.
		select {
		case decision := <-respCh:
			s.mu.Lock()
			delete(s.pendingApprovals, reqID)
			delete(s.approvalChans, reqID)
			s.mu.Unlock()
			return decision, nil
		case <-time.After(30 * time.Minute):
			s.mu.Lock()
			delete(s.pendingApprovals, reqID)
			delete(s.approvalChans, reqID)
			s.mu.Unlock()
			return ApprovalDecision{Decision: "cancel"}, nil
		}

	default:
		// Unknown server-initiated request; reject so it doesn't hang.
		return nil, &RPCError{Code: -32601, Message: "method not found"}
	}
}

// AnswerApproval is called by the WebSocket handler when the user picks
// "accept" / "decline" / etc. for a pending approval prompt.
func (s *Session) AnswerApproval(requestID string, decision string) error {
	s.mu.Lock()
	ch, ok := s.approvalChans[requestID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown approval request: %s", requestID)
	}
	select {
	case ch <- ApprovalDecision{Decision: translateDecision(decision)}:
		return nil
	default:
		return fmt.Errorf("approval already answered")
	}
}

// recordItem stores or updates an item in the current turn's accumulator.
// Skips "userMessage" items — those are echoes of the user's input which
// we've already persisted in Send.
func (s *Session) recordItem(it Item) {
	if it.ID == "" || it.Type == "userMessage" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentItems == nil {
		s.currentItems = make(map[string]*Item)
	}
	if existing, ok := s.currentItems[it.ID]; ok && existing != nil {
		// Merge: completed event has authoritative final fields
		merged := *existing
		if it.Type != "" {
			merged.Type = it.Type
		}
		if it.Status != "" {
			merged.Status = it.Status
		}
		if it.Text != "" {
			merged.Text = it.Text
		}
		if it.Output != "" {
			merged.Output = it.Output
		}
		if it.ExitCode != nil {
			merged.ExitCode = it.ExitCode
		}
		if it.Path != "" {
			merged.Path = it.Path
		}
		if it.Diff != "" {
			merged.Diff = it.Diff
		}
		if it.Change != "" {
			merged.Change = it.Change
		}
		if len(it.Command) > 0 {
			merged.Command = it.Command
		}
		s.currentItems[it.ID] = &merged
	} else {
		copy := it
		s.currentItems[it.ID] = &copy
		s.currentOrder = append(s.currentOrder, it.ID)
	}
}

func (s *Session) appendItemText(itemID, delta string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentItems == nil {
		s.currentItems = make(map[string]*Item)
	}
	it, ok := s.currentItems[itemID]
	if !ok {
		it = &Item{ID: itemID, Type: "agent_message"}
		s.currentItems[itemID] = it
		s.currentOrder = append(s.currentOrder, itemID)
	}
	it.Text += delta
}

func (s *Session) appendItemOutput(itemID, delta string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentItems == nil {
		s.currentItems = make(map[string]*Item)
	}
	it, ok := s.currentItems[itemID]
	if !ok {
		it = &Item{ID: itemID, Type: "command_execution"}
		s.currentItems[itemID] = it
		s.currentOrder = append(s.currentOrder, itemID)
	}
	it.Output += delta
}

// commitTurn finalizes the current turn — appends an assistant Message
// containing the accumulated items, and clears the accumulator.
func (s *Session) commitTurn(turnID, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.currentOrder) > 0 {
		items := make([]Item, 0, len(s.currentOrder))
		for _, id := range s.currentOrder {
			if it, ok := s.currentItems[id]; ok && it != nil {
				items = append(items, *it)
			}
		}
		msg := Message{
			Role:      agentmsg.RoleAssistant,
			Items:     items,
			Timestamp: time.Now(),
			TurnID:    turnID,
		}
		s.messages = append(s.messages, msg)
		s.persistMessage(msg)
	}
	s.currentItems = nil
	s.currentOrder = nil
	s.currentTurnID = ""
	s.generating = false
}

// approvalChans needs to be on Session; declared here to keep the struct
// definition focused. We add it via a side-init in Manager when constructing
// sessions.

func itemPtr(it Item) *Item { return &it }

// ---------------- Manager ----------------

// Manager manages Codex sessions across worktrees.
type Manager struct {
	mu            sync.Mutex
	sessions      map[string]*Session
	worktreeIndex map[string][]string

	stateDir     string
	sessionsFile string
	messagesDir  string
}

// NewManager creates a new Codex session manager.
// stateDir is the directory for persistence (e.g. .trellis/codex). Pass "" to
// disable persistence.
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

func (m *Manager) loadFromDisk() {
	records, err := loadRecords(m.sessionsFile)
	if err != nil {
		log.Printf("codex: failed to load sessions: %v", err)
		return
	}
	purged := 0
	for _, rec := range records {
		if rec.TrashedAt != nil && time.Since(*rec.TrashedAt) > 7*24*time.Hour {
			if m.messagesDir != "" {
				os.Remove(filepath.Join(m.messagesDir, rec.ID+".jsonl"))
			}
			purged++
			continue
		}
		s := newSessionStruct(rec.ID)
		s.worktreeName = rec.WorktreeName
		s.displayName = rec.DisplayName
		s.threadID = rec.ThreadID
		s.workDir = rec.WorkDir
		s.createdAt = rec.CreatedAt
		s.trashedAt = rec.TrashedAt
		s.persistFn = m.makePersistFn()
		if m.messagesDir != "" {
			s.messagesFile = filepath.Join(m.messagesDir, rec.ID+".jsonl")
			if msgs, err := loadMessages(s.messagesFile); err != nil {
				log.Printf("codex: failed to load messages for %s: %v", rec.ID, err)
			} else if len(msgs) > 0 {
				s.messages = msgs
			}
		}
		m.sessions[rec.ID] = s
		m.worktreeIndex[rec.WorktreeName] = append(m.worktreeIndex[rec.WorktreeName], rec.ID)
	}
	if purged > 0 {
		log.Printf("codex: purged %d expired trashed sessions", purged)
		go m.persist()
	}
	if len(records)-purged > 0 {
		log.Printf("codex: loaded %d persisted sessions", len(records)-purged)
	}
}

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
			ThreadID:     s.threadID,
			WorkDir:      s.workDir,
			CreatedAt:    s.createdAt,
			TrashedAt:    s.trashedAt,
		})
		s.mu.Unlock()
	}
	m.mu.Unlock()
	if err := saveRecords(m.sessionsFile, records); err != nil {
		log.Printf("codex: failed to persist sessions: %v", err)
	}
}

func (m *Manager) makePersistFn() func() {
	return func() { m.persist() }
}

// CreateSession creates a new session for a worktree.
func (m *Manager) CreateSession(worktreeName, workDir, displayName string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := uuid.New().String()
	if displayName == "" {
		displayName = fmt.Sprintf("Session %d", len(m.worktreeIndex[worktreeName])+1)
	}

	s := newSessionStruct(id)
	s.worktreeName = worktreeName
	s.displayName = displayName
	s.workDir = workDir
	s.createdAt = time.Now()
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

// ListSessions returns active (non-trashed) sessions for a worktree, newest-first.
func (m *Manager) ListSessions(worktreeName string) []*SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.worktreeIndex[worktreeName]
	result := make([]*SessionInfo, 0, len(ids))
	for _, id := range ids {
		s, ok := m.sessions[id]
		if !ok {
			continue
		}
		s.mu.Lock()
		trashed := s.trashedAt != nil
		s.mu.Unlock()
		if trashed {
			continue
		}
		info := s.Info()
		result = append(result, &info)
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

// ListTrashedSessions returns trashed sessions for a worktree.
func (m *Manager) ListTrashedSessions(worktreeName string) []*SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.worktreeIndex[worktreeName]
	result := make([]*SessionInfo, 0)
	for _, id := range ids {
		s, ok := m.sessions[id]
		if !ok {
			continue
		}
		s.mu.Lock()
		trashed := s.trashedAt != nil
		s.mu.Unlock()
		if !trashed {
			continue
		}
		info := s.Info()
		result = append(result, &info)
	}
	return result
}

// AllSessions returns info for every active (non-trashed) session.
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

// RenameSession updates a session's display name.
func (m *Manager) RenameSession(sessionID, name string) error {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("session not found")
	}
	s.mu.Lock()
	s.displayName = name
	s.mu.Unlock()
	m.persist()
	return nil
}

// TrashSession soft-deletes; preserves data, kills the process.
func (m *Manager) TrashSession(sessionID string) error {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("session not found")
	}
	s.shutdownProcess()
	s.mu.Lock()
	now := time.Now()
	s.trashedAt = &now
	s.closeAllSubscribers()
	s.mu.Unlock()
	m.persist()
	return nil
}

// RestoreSession brings a trashed session back to active state.
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

// DeleteSession permanently removes a session and its message file.
func (m *Manager) DeleteSession(sessionID string) {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.sessions, sessionID)
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

	s.shutdownProcess()
	s.mu.Lock()
	s.closeAllSubscribers()
	msgFile := s.messagesFile
	s.mu.Unlock()
	if msgFile != "" {
		os.Remove(msgFile)
	}
	m.persist()
}

// MoveSession rebinds a session to a new worktree/workdir. Stops any running
// process; the next Send re-spawns in the new directory.
func (m *Manager) MoveSession(sessionID, newWorktreeName, newWorkDir string) error {
	if newWorktreeName == "" || newWorkDir == "" {
		return fmt.Errorf("worktree name and workdir required")
	}
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("session not found")
	}
	old := s.worktreeName
	if old != newWorktreeName {
		ids := m.worktreeIndex[old]
		for i, id := range ids {
			if id == sessionID {
				m.worktreeIndex[old] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
		if len(m.worktreeIndex[old]) == 0 {
			delete(m.worktreeIndex, old)
		}
		m.worktreeIndex[newWorktreeName] = append(m.worktreeIndex[newWorktreeName], sessionID)
	}
	m.mu.Unlock()

	s.shutdownProcess()
	s.mu.Lock()
	s.worktreeName = newWorktreeName
	s.workDir = newWorkDir
	s.closeAllSubscribers()
	s.mu.Unlock()
	m.persist()
	return nil
}

// ForkSession creates a new session pre-populated with messages[0..messageIndex]
// from the source.
//
// Codex's `thread/fork` clones the entire source thread server-side — there
// is no "fork up to turn X" parameter. So we use it only when the user is
// forking at the *last* message (the common "branch off from here" case),
// which preserves true server-side continuity.
//
// For intermediate forks, we fall back to a local clone: the new session
// shows the prefix in the UI, but Codex starts a fresh thread on first
// Send and won't know about the prior context server-side.
func (m *Manager) ForkSession(sourceID string, messageIndex int, displayName string) (*Session, error) {
	m.mu.Lock()
	src, ok := m.sessions[sourceID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("source session not found")
	}

	src.mu.Lock()
	if len(src.messages) == 0 {
		src.mu.Unlock()
		return nil, fmt.Errorf("source session has no messages to fork")
	}
	if messageIndex < 0 {
		messageIndex = 0
	}
	if messageIndex >= len(src.messages) {
		messageIndex = len(src.messages) - 1
	}
	atLastMessage := messageIndex == len(src.messages)-1
	prefix := make([]Message, messageIndex+1)
	copy(prefix, src.messages[:messageIndex+1])
	worktreeName := src.worktreeName
	workDir := src.workDir
	srcThreadID := src.threadID
	srcRPC := src.rpc
	src.mu.Unlock()

	if displayName == "" {
		displayName = "Fork"
	}
	newSession := m.CreateSession(worktreeName, workDir, displayName)
	newSession.mu.Lock()
	newSession.messages = prefix
	newSession.persistAllMessages()
	newSession.mu.Unlock()

	// Try a server-side fork only when forking at the latest message, since
	// Codex's thread/fork doesn't support truncation. Need an active rpc
	// connection on the source — if the source's process isn't running we
	// could spin one up, but that's expensive and a rare path. Fall back
	// to local clone otherwise.
	if atLastMessage && srcRPC != nil && srcThreadID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, err := srcRPC.Call(ctx, "thread/fork", threadForkParams{
			ThreadID:     srcThreadID,
			Cwd:          workDir,
			ExcludeTurns: true,
		})
		if err != nil {
			log.Printf("codex: thread/fork failed (%v), falling back to local clone", err)
		} else {
			var res threadForkResult
			if err := json.Unmarshal(raw, &res); err == nil && res.Thread.ID != "" {
				newSession.mu.Lock()
				newSession.threadID = res.Thread.ID
				newSession.mu.Unlock()
				m.persist()
			}
		}
	}

	return newSession, nil
}

// GetOrCreateSession returns the first active session for a worktree, creating one if none.
func (m *Manager) GetOrCreateSession(worktreeName, workDir string) *Session {
	m.mu.Lock()
	for _, id := range m.worktreeIndex[worktreeName] {
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

// Shutdown stops all sessions.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		s.shutdownProcess()
		s.mu.Lock()
		s.closeAllSubscribers()
		s.mu.Unlock()
	}
}

// newSessionStruct returns a Session with maps initialized. Other fields are
// expected to be set by the caller.
func newSessionStruct(id string) *Session {
	return &Session{
		id:               id,
		subscribers:      make(map[chan StreamEvent]struct{}),
		pendingApprovals: make(map[string]StreamEvent),
		approvalChans:    make(map[string]chan ApprovalDecision),
	}
}
