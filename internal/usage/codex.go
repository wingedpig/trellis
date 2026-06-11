// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Codex CLI writes one rollout JSONL per session under
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl. Usage comes from
// event_msg lines with payload.type == "token_count": each carries the
// thread-cumulative total_token_usage and the per-call last_token_usage.
// Model and cwd come from turn_context lines; session id from session_meta.

func defaultCodexDirs() []string {
	if env := strings.TrimSpace(os.Getenv("CODEX_HOME")); env != "" {
		return []string{filepath.Join(expandHome(env), "sessions")}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".codex", "sessions")}
}

// codexLine is the subset of a Codex rollout line we need.
type codexLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		// session_meta
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
		// turn_context
		Model string `json:"model"`
		// event_msg
		Type string `json:"type"`
		Info *struct {
			TotalTokenUsage codexTokenUsage `json:"total_token_usage"`
			LastTokenUsage  codexTokenUsage `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

type codexTokenUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

var tokenCountMarker = []byte(`"token_count"`)
var turnContextMarker = []byte(`"turn_context"`)
var sessionMetaMarker = []byte(`"session_meta"`)

func parseCodexFile(path string) []Entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []Entry
	var sessionID, cwd string
	model := "gpt-5"
	var prevTotal codexTokenUsage
	havePrev := false

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		isMeta := bytes.Contains(line, sessionMetaMarker)
		isTurn := bytes.Contains(line, turnContextMarker)
		isCount := bytes.Contains(line, tokenCountMarker)
		if !isMeta && !isTurn && !isCount {
			continue
		}
		var cl codexLine
		if json.Unmarshal(line, &cl) != nil {
			continue
		}
		switch cl.Type {
		case "session_meta":
			if cl.Payload.ID != "" {
				sessionID = cl.Payload.ID
			}
			if cl.Payload.Cwd != "" {
				cwd = cl.Payload.Cwd
			}
		case "turn_context":
			if cl.Payload.Model != "" {
				model = cl.Payload.Model
			}
			if cl.Payload.Cwd != "" {
				cwd = cl.Payload.Cwd
			}
		case "event_msg":
			if cl.Payload.Type != "token_count" || cl.Payload.Info == nil {
				continue
			}
			total := cl.Payload.Info.TotalTokenUsage
			last := cl.Payload.Info.LastTokenUsage

			// Prefer the delta of cumulative totals (robust if an API call
			// emits several progressive token_count events). For the first
			// event in a file fall back to last_token_usage: a resumed
			// thread's cumulative total includes usage already counted from
			// the previous rollout file.
			d := last
			if havePrev {
				delta := codexTokenUsage{
					InputTokens:       total.InputTokens - prevTotal.InputTokens,
					CachedInputTokens: total.CachedInputTokens - prevTotal.CachedInputTokens,
					OutputTokens:      total.OutputTokens - prevTotal.OutputTokens,
				}
				if delta.InputTokens >= 0 && delta.CachedInputTokens >= 0 && delta.OutputTokens >= 0 {
					d = delta
				}
			}
			prevTotal = total
			havePrev = true

			if d.InputTokens == 0 && d.OutputTokens == 0 {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, cl.Timestamp)
			if err != nil {
				continue
			}

			// OpenAI's input_tokens includes the cached portion; Entry.Input
			// holds the uncached remainder so cost math stays uniform.
			uncached := d.InputTokens - d.CachedInputTokens
			if uncached < 0 {
				uncached = 0
			}
			e := Entry{
				Agent:     AgentCodex,
				Timestamp: ts,
				Model:     model,
				Cwd:       cwd,
				SessionID: sessionID,
				Input:     uncached,
				Output:    d.OutputTokens,
				CacheRead: d.CachedInputTokens,
			}
			e.CostUSD = costFor(e)
			entries = append(entries, e)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("usage: partial parse of %s: %v", path, err)
	}
	return entries
}
