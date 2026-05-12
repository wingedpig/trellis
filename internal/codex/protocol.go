// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"encoding/json"
	"time"
)

// Codex app-server protocol shapes (JSON-RPC 2.0).
//
// We only model the subset Trellis needs. Fields we don't consume are kept
// as json.RawMessage so they pass through to the WebSocket client unchanged.
// See https://developers.openai.com/codex/app-server for the canonical spec.

// ----- initialize -----

// initializeParams is sent once per connection before any other call.
type initializeParams struct {
	ClientInfo   clientInfo  `json:"clientInfo"`
	Capabilities interface{} `json:"capabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// initializeResult contains server identity.
type initializeResult struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
}

// ----- thread / turn -----
//
// thread/start creates a new thread; the response is {thread: Thread}.
// thread/resume loads a previously-persisted thread back into memory.
// turn/start runs a turn on an existing thread; the response is
// {turn: Turn} and the streaming events arrive via notifications.

// threadStartParams creates a new thread.
type threadStartParams struct {
	Cwd     string `json:"cwd,omitempty"`
	Sandbox string `json:"sandbox,omitempty"`
}

type threadStartResult struct {
	Thread thread `json:"thread"`
}

// thread is the abridged Thread object returned by thread/start (and embedded
// in thread/started notifications). We only consume the id.
type thread struct {
	ID string `json:"id"`
}

// threadResumeParams reopens an existing thread by id.
type threadResumeParams struct {
	ThreadID     string `json:"threadId"`
	Cwd          string `json:"cwd,omitempty"`
	ExcludeTurns bool   `json:"excludeTurns,omitempty"`
}

// threadForkParams clones an entire thread to a new id. The Codex protocol
// does not currently support forking up to a specific turn — server-side
// fork captures the source's current state in full.
type threadForkParams struct {
	ThreadID     string `json:"threadId"`
	Cwd          string `json:"cwd,omitempty"`
	ExcludeTurns bool   `json:"excludeTurns,omitempty"`
}

// threadForkResult mirrors thread/start's response shape — wraps the new
// thread under "thread".
type threadForkResult struct {
	Thread thread `json:"thread"`
}

// turnStartParams sends a user message and starts a turn.
type turnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []UserInput `json:"input"`
	Cwd      string      `json:"cwd,omitempty"`
}

// UserInput is one item in the user's turn input. text_elements is required
// (may be empty) — the spec is strict about it.
type UserInput struct {
	Type         string        `json:"type"` // "text"
	Text         string        `json:"text,omitempty"`
	TextElements []interface{} `json:"text_elements"` // always present, may be empty
}

// turnInterruptParams cancels an in-flight turn.
type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId,omitempty"`
}

// ----- notifications (server → client) -----
//
// Notification methods include: "thread/started", "turn/started",
// "turn/completed", "turn/failed", "item/started", "item/completed",
// "item/agentMessage/delta", "item/commandExecution/outputDelta",
// "turn/diff/updated", "turn/plan/updated", "thread/status/changed",
// "thread/tokenUsage/updated", and many informational ones we pass through.

// threadStartedNotification is "thread/started".
type threadStartedNotification struct {
	Thread thread `json:"thread"`
}

// turnEvent covers turn/started, turn/completed, turn/failed. The wire
// shape carries the turn nested as {threadId, turn: {id, status, error}}
// — *not* flat turnId.
type turnEvent struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID    string          `json:"id"`
		Error json.RawMessage `json:"error,omitempty"`
	} `json:"turn"`
}

// itemEvent covers item/started, item/completed.
//
// Item types include:
//
//	"agent_message"      — assistant text
//	"reasoning"          — internal reasoning (when surfaced)
//	"command_execution"  — shell command run by the agent
//	"file_change"        — file the agent created/modified
//	"mcp_tool_call"      — MCP tool invocation
//	"web_search"
//	"plan_update"
type itemEvent struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
	Item     Item            `json:"item"`
}

// Item is an event-stream item. Most fields are optional and depend on Type.
type Item struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Status string          `json:"status,omitempty"`
	Text   string          `json:"text,omitempty"`

	// Tool / command-execution fields
	Command   json.RawMessage `json:"command,omitempty"`
	Output    string          `json:"output,omitempty"`
	ExitCode  *int            `json:"exitCode,omitempty"`

	// File-change fields
	Path   string          `json:"path,omitempty"`
	Diff   string          `json:"diff,omitempty"`
	Change string          `json:"changeType,omitempty"`

	// Pass-through for fields we don't model
	Raw json.RawMessage `json:"-"`
}

// agentMessageDelta is "item/agentMessage/delta" — streaming text fragment.
type agentMessageDelta struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

// commandExecutionOutputDelta is "item/commandExecution/outputDelta".
type commandExecutionOutputDelta struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Stream   string `json:"stream"` // "stdout" | "stderr"
	Delta    string `json:"delta"`
}

// ----- approvals (server → client request) -----
//
// When the agent wants to run a command or change a file that requires
// approval, the server sends a JSON-RPC request (with id) using one of
// these methods, and waits for our response.

// commandApprovalRequest is "item/commandExecution/requestApproval".
type commandApprovalRequest struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
	ItemID   string          `json:"itemId"`
	Command  json.RawMessage `json:"command,omitempty"`
	Cwd      string          `json:"cwd,omitempty"`
	Reason   string          `json:"reason,omitempty"`
}

// fileChangeApprovalRequest is "item/fileChange/requestApproval".
type fileChangeApprovalRequest struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Path     string `json:"path,omitempty"`
	Diff     string `json:"diff,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ApprovalDecision is the response shape for command/file approval requests.
// `Decision` must be a Codex ReviewDecision string:
//
//	"approved"              — run / apply once
//	"approved_for_session"  — auto-approve similar requests for this session
//	"denied"                — reject
//	"abort"                 — stop the whole turn
//
// We translate friendlier UI strings ("accept", "acceptForSession",
// "decline", "cancel") in Session.AnswerApproval.
type ApprovalDecision struct {
	Decision string `json:"decision"`
}

// translateDecision maps UI-friendly strings to Codex ReviewDecision values.
// Unknown strings pass through unchanged so callers can use raw values too.
func translateDecision(s string) string {
	switch s {
	case "accept":
		return "approved"
	case "acceptForSession":
		return "approved_for_session"
	case "decline":
		return "denied"
	case "cancel":
		return "abort"
	}
	return s
}

// ----- internal helpers -----

// nowRFC3339 returns the current time formatted in RFC 3339 nanos, used for
// transcript timestamps.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }
