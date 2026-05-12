// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package agentmsg defines a small shared vocabulary for representing
// AI-agent chat messages across providers (Claude, Codex, ...).
//
// The goals are deliberately narrow:
//
//  1. Allow the per-agent packages to keep their own rich, provider-specific
//     content-block representations (Claude has tool_use/tool_result blocks
//     with raw JSON inputs; Codex has command_execution/file_change items
//     with their own shapes). Each agent owns its own wire types.
//
//  2. Provide a single canonical "plain text" rendering that both agents can
//     produce so a message copied from one chat pastes cleanly into the
//     other. This is the core of the cross-agent verification workflow.
//
// Anything beyond that — full transcript schemas, persistence formats,
// streaming protocols — stays inside each provider's package.
package agentmsg

import (
	"fmt"
	"strings"
)

// Role identifies who produced a turn. Mirrors the Anthropic/OpenAI
// vocabulary because both providers use the same two values.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Block is a single chunk of rendered content within a Message.
// Agents map their native block types onto these for cross-agent rendering.
//
// Kind values (extend as needed; renderers must tolerate unknown kinds):
//
//	"text"        — Text holds the prose
//	"thinking"    — internal reasoning; Text holds the prose
//	"tool_call"   — Title is the tool name, Body is a one-line summary
//	"tool_result" — Title is the tool name (if known), Body is output
//	"code"        — Body is code; Title may hold a language hint
type Block struct {
	Kind  string
	Title string
	Body  string
}

// Message is the cross-agent representation of one conversational turn.
type Message struct {
	Role   string
	Agent  string  // "claude" | "codex" | ...; informational, used in quotes
	Blocks []Block
}

// QuoteOptions controls how RenderQuote formats a message for clipboard pasting.
type QuoteOptions struct {
	// IncludeToolDetails preserves tool_call / tool_result blocks. When false
	// (default) they are summarized as "[tool: name]" so the receiving agent
	// gets the analysis text without irrelevant tool plumbing.
	IncludeToolDetails bool

	// MaxBodyChars truncates each block body to this length. 0 means no limit.
	MaxBodyChars int
}

// RenderPlain produces a markdown-flavored plain-text rendering of a message.
// Suitable for transcripts, exports, and as the body of a quote.
func RenderPlain(m Message, opts QuoteOptions) string {
	var sb strings.Builder
	for i, b := range m.Blocks {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		writeBlock(&sb, b, opts)
	}
	return strings.TrimSpace(sb.String())
}

// RenderQuote produces a markdown blockquote suitable for pasting into the
// other agent's input box. Each line is prefixed with "> " and the block is
// preceded by an attribution header.
//
// Example output:
//
//	> [from claude · assistant]
//	> The bug is in foo.go:42 — the loop condition is off-by-one.
func RenderQuote(m Message, opts QuoteOptions) string {
	body := RenderPlain(m, opts)
	if body == "" {
		return ""
	}

	header := fmt.Sprintf("[from %s · %s]", labelAgent(m.Agent), labelRole(m.Role))

	var out strings.Builder
	out.WriteString("> ")
	out.WriteString(header)
	out.WriteString("\n")
	for line := range strings.SplitSeq(body, "\n") {
		out.WriteString("> ")
		out.WriteString(line)
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func writeBlock(sb *strings.Builder, b Block, opts QuoteOptions) {
	body := b.Body
	if opts.MaxBodyChars > 0 && len(body) > opts.MaxBodyChars {
		body = body[:opts.MaxBodyChars] + "…"
	}

	switch b.Kind {
	case "text", "":
		sb.WriteString(body)
	case "thinking":
		sb.WriteString("_(thinking)_ ")
		sb.WriteString(body)
	case "tool_call":
		if !opts.IncludeToolDetails {
			fmt.Fprintf(sb, "[tool: %s]", nonEmpty(b.Title, "unknown"))
			return
		}
		fmt.Fprintf(sb, "[tool call: %s]", nonEmpty(b.Title, "unknown"))
		if body != "" {
			sb.WriteString("\n")
			sb.WriteString(body)
		}
	case "tool_result":
		if !opts.IncludeToolDetails {
			fmt.Fprintf(sb, "[tool result: %s]", nonEmpty(b.Title, "unknown"))
			return
		}
		fmt.Fprintf(sb, "[tool result: %s]\n", nonEmpty(b.Title, "unknown"))
		sb.WriteString("```\n")
		sb.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```")
	case "code":
		sb.WriteString("```")
		if b.Title != "" {
			sb.WriteString(b.Title)
		}
		sb.WriteString("\n")
		sb.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```")
	default:
		// Unknown kinds: print as labeled text so nothing is silently lost.
		fmt.Fprintf(sb, "[%s] %s", b.Kind, body)
	}
}

func labelAgent(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func labelRole(s string) string {
	switch s {
	case RoleUser:
		return "user"
	case RoleAssistant:
		return "assistant"
	case "":
		return "message"
	default:
		return s
	}
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
