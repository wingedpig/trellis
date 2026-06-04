// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package genai wraps one-shot Claude CLI invocations (`claude -p`) for
// generating commit messages and case summaries.
//
// Trellis already shells out to the `claude` binary for interactive sessions
// (internal/claude/manager.go). This package re-uses that pattern for
// non-interactive prompts: each call starts a fresh `claude -p` subprocess,
// pipes the prompt in on stdin, and parses the JSON envelope from stdout.
//
// No API key plumbing is required — generation relies on the user's existing
// Claude Code authentication.
package genai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// limitedBuffer collects writes up to max bytes and silently discards the
// rest, reporting full success so the subprocess never sees a write error.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if remaining := b.max - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *limitedBuffer) String() string { return b.buf.String() }

// envelope matches the JSON shape emitted by `claude -p --output-format json`.
// We only need a few fields; unknown fields are ignored by the decoder.
type envelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Model   string `json:"model"`
}

// runClaude invokes `claude -p --output-format json`, writing the prompt to
// stdin, and returns the assistant's text result plus the model identifier.
// The supplied context bounds the call.
func runClaude(ctx context.Context, prompt string, cwd string) (result, model string, err error) {
	args := []string{
		"-p",
		"--output-format", "json",
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Stdin = strings.NewReader(prompt)
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Size-limited buffers so a runaway subprocess can't exhaust memory.
	// Legitimate output is a small JSON envelope.
	stdout := &limitedBuffer{max: 8 << 20} // 8MB
	stderr := &limitedBuffer{max: 1 << 20} // 1MB
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("claude -p: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var env envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		return "", "", fmt.Errorf("parse claude envelope: %w (output: %s)", err, truncate(stdout.String(), 400))
	}
	if env.IsError || env.Subtype != "" && env.Subtype != "success" {
		return "", env.Model, fmt.Errorf("claude reported error: %s", env.Result)
	}
	return env.Result, env.Model, nil
}

// extractJSON pulls the first balanced JSON object out of s. Some models
// return prose around the JSON despite instructions; this is a defensive
// fallback so a stray "Here is the JSON:" prefix doesn't break parsing.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return s
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if esc {
			esc = false
			continue
		}
		if inStr {
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
