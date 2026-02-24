// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TranscriptSchema is the schema identifier for the export format.
const TranscriptSchema = "trellis.transcript.v1"

// TranscriptSchemaV2 is the schema for the split JSONL + metadata sidecar format.
const TranscriptSchemaV2 = "trellis.transcript.v2"

// Transcript is the full export format for a Claude session.
type Transcript struct {
	Schema     string           `json:"schema"`
	ExportedAt time.Time        `json:"exported_at"`
	Source     TranscriptSource `json:"source"`
	Messages   []Message        `json:"messages"`
	Stats      TranscriptStats  `json:"stats"`
}

// TranscriptSource holds metadata about where the transcript came from.
type TranscriptSource struct {
	TrellisSessionID string `json:"trellis_session_id"`
	ClaudeSessionID  string `json:"claude_session_id,omitempty"`
	Worktree         string `json:"worktree,omitempty"`
	Branch           string `json:"branch,omitempty"`
	DisplayName      string `json:"display_name,omitempty"`
	ProjectPath      string `json:"project_path,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// TranscriptStats holds statistics about the transcript.
type TranscriptStats struct {
	MessageCount  int `json:"message_count"`
	UserTurns     int `json:"user_turns"`
	AssistantTurns int `json:"assistant_turns"`
	ToolUses      int `json:"tool_uses"`
}

// TranscriptMeta holds everything from a Transcript except the messages.
// Used as the JSON sidecar in the v2 split format.
type TranscriptMeta struct {
	Schema     string           `json:"schema"`
	ExportedAt time.Time        `json:"exported_at"`
	Source     TranscriptSource `json:"source"`
	Stats      TranscriptStats  `json:"stats"`
}

// ComputeStats calculates stats from a message list.
func ComputeStats(messages []Message) TranscriptStats {
	var stats TranscriptStats
	stats.MessageCount = len(messages)
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			// Only count actual user turns (not tool results)
			isToolResult := false
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					isToolResult = true
					break
				}
			}
			if !isToolResult {
				stats.UserTurns++
			}
		case "assistant":
			stats.AssistantTurns++
			for _, block := range msg.Content {
				if block.Type == "tool_use" {
					stats.ToolUses++
				}
			}
		}
	}
	return stats
}

// SummarizeMessages returns a copy of messages with tool inputs/outputs stripped.
// Used for the "summary" export level.
func SummarizeMessages(messages []Message) []Message {
	result := make([]Message, len(messages))
	for i, msg := range messages {
		summarized := Message{
			Role:      msg.Role,
			Timestamp: msg.Timestamp,
		}
		blocks := make([]ContentBlock, 0, len(msg.Content))
		for _, block := range msg.Content {
			switch block.Type {
			case "tool_use":
				// Keep name only, strip input
				blocks = append(blocks, ContentBlock{
					Type: "tool_use",
					Name: block.Name,
				})
			case "tool_result":
				// Strip content
				blocks = append(blocks, ContentBlock{
					Type:      "tool_result",
					ToolUseID: block.ToolUseID,
					Content:   "[redacted]",
				})
			default:
				blocks = append(blocks, block)
			}
		}
		summarized.Content = blocks
		result[i] = summarized
	}
	return result
}

// ValidateTranscript checks that a transcript has the expected schema and valid data.
func ValidateTranscript(t *Transcript) error {
	if t.Schema != TranscriptSchema && t.Schema != TranscriptSchemaV2 {
		return &TranscriptError{msg: "unsupported transcript schema: " + t.Schema}
	}
	if len(t.Messages) == 0 {
		return &TranscriptError{msg: "transcript has no messages"}
	}
	return nil
}

// ParseTranscript parses a transcript from JSON bytes.
func ParseTranscript(data []byte) (*Transcript, error) {
	var t Transcript
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, &TranscriptError{msg: "invalid transcript JSON: " + err.Error()}
	}
	if err := ValidateTranscript(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// FirstUserPreview returns the first two lines of the first real user message,
// joined into a single line. Returns empty string if no user message is found.
func FirstUserPreview(messages []Message, maxLen int) string {
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		// Skip tool result messages (not real user input)
		isToolResult := false
		for _, block := range msg.Content {
			if block.Type == "tool_result" {
				isToolResult = true
				break
			}
		}
		if isToolResult {
			continue
		}
		// Extract text from first text block
		for _, block := range msg.Content {
			if block.Type == "text" && block.Text != "" {
				return truncateLines(block.Text, 2, maxLen)
			}
		}
	}
	return ""
}

// truncateLines takes up to n non-empty lines, joins them with a space,
// and truncates to maxLen characters.
func truncateLines(s string, n, maxLen int) string {
	var lines []string
	for _, line := range strings.SplitN(s, "\n", n+1) {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
			if len(lines) >= n {
				break
			}
		}
	}
	result := strings.Join(lines, " ")
	if len(result) > maxLen {
		return result[:maxLen] + "..."
	}
	return result
}

// WriteTranscriptSplit writes a transcript as two files: a JSONL messages file
// and a JSON metadata sidecar. Both writes are atomic (tmp + rename).
func WriteTranscriptSplit(jsonlPath, metaPath string, t *Transcript) error {
	dir := filepath.Dir(jsonlPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create transcripts dir: %w", err)
	}

	// Write messages as JSONL (atomic: tmp + rename)
	tmpJSONL := jsonlPath + ".tmp"
	f, err := os.Create(tmpJSONL)
	if err != nil {
		return fmt.Errorf("create temp jsonl file: %w", err)
	}
	enc := json.NewEncoder(f)
	for _, msg := range t.Messages {
		if err := enc.Encode(msg); err != nil {
			f.Close()
			os.Remove(tmpJSONL)
			return fmt.Errorf("encode message: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpJSONL)
		return fmt.Errorf("close temp jsonl file: %w", err)
	}
	if err := os.Rename(tmpJSONL, jsonlPath); err != nil {
		os.Remove(tmpJSONL)
		return fmt.Errorf("rename jsonl file: %w", err)
	}

	// Write metadata sidecar (atomic: tmp + rename)
	meta := TranscriptMeta{
		Schema:     TranscriptSchemaV2,
		ExportedAt: t.ExportedAt,
		Source:     t.Source,
		Stats:      t.Stats,
	}
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transcript metadata: %w", err)
	}
	tmpMeta := metaPath + ".tmp"
	if err := os.WriteFile(tmpMeta, metaData, 0o644); err != nil {
		os.Remove(tmpMeta)
		return fmt.Errorf("write temp metadata file: %w", err)
	}
	if err := os.Rename(tmpMeta, metaPath); err != nil {
		os.Remove(tmpMeta)
		return fmt.Errorf("rename metadata file: %w", err)
	}

	return nil
}

// ReadTranscriptSplit reads a transcript from a JSONL messages file and a JSON
// metadata sidecar. If the metadata file is missing or corrupt, it degrades
// gracefully by computing stats from the messages.
func ReadTranscriptSplit(jsonlPath, metaPath string) (*Transcript, error) {
	msgs, err := loadMessages(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("read transcript messages: %w", err)
	}
	if len(msgs) == 0 {
		return nil, &TranscriptError{msg: "transcript has no messages"}
	}

	t := &Transcript{
		Schema:   TranscriptSchemaV2,
		Messages: msgs,
	}

	// Read metadata sidecar; degrade gracefully if missing/corrupt
	metaData, err := os.ReadFile(metaPath)
	if err == nil {
		var meta TranscriptMeta
		if json.Unmarshal(metaData, &meta) == nil {
			t.ExportedAt = meta.ExportedAt
			t.Source = meta.Source
			t.Stats = meta.Stats
			return t, nil
		}
	}

	// Fallback: compute stats from messages
	t.Stats = ComputeStats(msgs)
	return t, nil
}

// TranscriptError is returned for invalid transcript data.
type TranscriptError struct {
	msg string
}

func (e *TranscriptError) Error() string { return e.msg }
