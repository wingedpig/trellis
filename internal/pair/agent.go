// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package pair

import (
	"context"
	"fmt"
	"strings"

	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
)

// Agents is the minimal manager-access surface the driver needs. Held by the
// Registry and passed to each PairRuntime. Both managers may be nil in tests,
// in which case the relevant operations return an error.
type Agents struct {
	Claude *claude.Manager
	Codex  *codex.Manager
}

// sessionStatus is the agent-agnostic view of a session: does it exist, is it
// trashed, and is it currently idle (i.e., ready to relay)?
type sessionStatus struct {
	Exists      bool
	Trashed     bool
	DisplayName string
	Idle        bool // not generating AND no pending permission/approval
}

// LookupStatus returns the current status of a session referenced by ref.
// A non-existent session returns Exists=false; a sessionStatus value of zero
// is the safe default.
//
// Idle is stricter than the inbox's "needs_you" state. The inbox treats a
// session as needs_you whenever it's NOT generating OR has a pending
// permission/approval prompt — both are things the user might want to act
// on. For pairing, however, "Idle" must mean truly done with the turn:
// not generating AND no pending prompts. A session blocked on a permission
// dialog has not finished its turn — relaying its partial output as if it
// had would race the user's permission decision and cross-feed the
// reviewer mid-thought.
func (a *Agents) LookupStatus(ref AgentRef) sessionStatus {
	switch ref.Agent {
	case "claude":
		if a.Claude == nil {
			return sessionStatus{}
		}
		s := a.Claude.GetSession(ref.SessionID)
		if s == nil {
			return sessionStatus{}
		}
		info := s.Info()
		return sessionStatus{
			Exists:      true,
			Trashed:     info.TrashedAt != nil,
			DisplayName: s.DisplayName(),
			Idle:        !s.IsGenerating() && !s.HasPendingControlRequest(),
		}
	case "codex":
		if a.Codex == nil {
			return sessionStatus{}
		}
		s := a.Codex.GetSession(ref.SessionID)
		if s == nil {
			return sessionStatus{}
		}
		info := s.Info()
		return sessionStatus{
			Exists:      true,
			Trashed:     info.TrashedAt != nil,
			DisplayName: s.DisplayName(),
			Idle:        !s.IsGenerating() && len(s.PendingApprovals()) == 0,
		}
	}
	return sessionStatus{}
}

// CaptureLastAssistantText returns the most recent assistant turn's text from
// the session, stripped of tool-call/scratchpad metadata (PAIRING_SPEC §5.2).
// Returns "" if there is no assistant turn or the turn produced no text.
func (a *Agents) CaptureLastAssistantText(ref AgentRef) (string, error) {
	switch ref.Agent {
	case "claude":
		if a.Claude == nil {
			return "", fmt.Errorf("claude manager not available")
		}
		s := a.Claude.GetSession(ref.SessionID)
		if s == nil {
			return "", fmt.Errorf("claude session %s not found", ref.SessionID)
		}
		msgs := s.Messages()
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			if m.Role != "assistant" {
				continue
			}
			var b strings.Builder
			for _, blk := range m.Content {
				if blk.Type == "text" && blk.Text != "" {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(blk.Text)
				}
			}
			return strings.TrimSpace(b.String()), nil
		}
		return "", nil
	case "codex":
		if a.Codex == nil {
			return "", fmt.Errorf("codex manager not available")
		}
		s := a.Codex.GetSession(ref.SessionID)
		if s == nil {
			return "", fmt.Errorf("codex session %s not found", ref.SessionID)
		}
		msgs := s.Messages()
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			if m.Role != "assistant" {
				continue
			}
			var b strings.Builder
			for _, it := range m.Items {
				// Codex's wire format uses camelCase ("agentMessage")
				// in item/started/completed events, while the
				// streaming-only fallback in codex/manager.go creates
				// "agent_message". Accept both — anything else
				// (commandExecution, fileChange, reasoning, plan) is
				// tool/scratchpad noise that should not cross the pair
				// boundary.
				if (it.Type == "agentMessage" || it.Type == "agent_message") && it.Text != "" {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(it.Text)
				}
			}
			return strings.TrimSpace(b.String()), nil
		}
		return "", nil
	}
	return "", fmt.Errorf("unknown agent %q", ref.Agent)
}

// SendUserMessage delivers prompt to the session as a user message. The send
// goes through the same path as a hand-typed message in the UI, so the
// receiving session's transcript records it normally.
func (a *Agents) SendUserMessage(ctx context.Context, ref AgentRef, prompt string) error {
	switch ref.Agent {
	case "claude":
		if a.Claude == nil {
			return fmt.Errorf("claude manager not available")
		}
		s := a.Claude.GetSession(ref.SessionID)
		if s == nil {
			return fmt.Errorf("claude session %s not found", ref.SessionID)
		}
		return s.Send(ctx, prompt)
	case "codex":
		if a.Codex == nil {
			return fmt.Errorf("codex manager not available")
		}
		s := a.Codex.GetSession(ref.SessionID)
		if s == nil {
			return fmt.Errorf("codex session %s not found", ref.SessionID)
		}
		return s.Send(ctx, prompt)
	}
	return fmt.Errorf("unknown agent %q", ref.Agent)
}
