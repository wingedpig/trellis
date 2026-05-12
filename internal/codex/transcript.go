// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wingedpig/trellis/internal/agentmsg"
)

// TranscriptSchema is the schema identifier for the export format.
const TranscriptSchema = "trellis.codex.transcript.v1"

// Transcript is the full export format for a Codex session.
type Transcript struct {
	Schema     string           `json:"schema"`
	ExportedAt time.Time        `json:"exported_at"`
	Source     TranscriptSource `json:"source"`
	Messages   []Message        `json:"messages"`
	Stats      TranscriptStats  `json:"stats"`
}

// TranscriptSource is metadata about where the transcript came from.
type TranscriptSource struct {
	TrellisSessionID string    `json:"trellis_session_id"`
	CodexThreadID    string    `json:"codex_thread_id,omitempty"`
	Worktree         string    `json:"worktree,omitempty"`
	Branch           string    `json:"branch,omitempty"`
	DisplayName      string    `json:"display_name,omitempty"`
	ProjectPath      string    `json:"project_path,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// TranscriptStats contains conversation statistics.
type TranscriptStats struct {
	MessageCount   int `json:"message_count"`
	UserTurns      int `json:"user_turns"`
	AssistantTurns int `json:"assistant_turns"`
	CommandRuns    int `json:"command_runs"`
	FileChanges    int `json:"file_changes"`
}

// ComputeStats calculates stats from a message list.
func ComputeStats(messages []Message) TranscriptStats {
	var s TranscriptStats
	s.MessageCount = len(messages)
	for _, m := range messages {
		switch m.Role {
		case agentmsg.RoleUser:
			s.UserTurns++
		case agentmsg.RoleAssistant:
			s.AssistantTurns++
			for _, it := range m.Items {
				switch it.Type {
				case "command_execution":
					s.CommandRuns++
				case "file_change":
					s.FileChanges++
				}
			}
		}
	}
	return s
}

// SummarizeMessages returns a copy of messages with command output and large
// diffs stripped. Used for the "summary" export level.
func SummarizeMessages(messages []Message) []Message {
	out := make([]Message, len(messages))
	for i, m := range messages {
		summarized := Message{
			Role:      m.Role,
			Timestamp: m.Timestamp,
			TurnID:    m.TurnID,
		}
		items := make([]Item, 0, len(m.Items))
		for _, it := range m.Items {
			switch it.Type {
			case "command_execution":
				items = append(items, Item{
					ID:     it.ID,
					Type:   "command_execution",
					Status: it.Status,
				})
			case "file_change":
				items = append(items, Item{
					ID:     it.ID,
					Type:   "file_change",
					Path:   it.Path,
					Change: it.Change,
				})
			default:
				items = append(items, it)
			}
		}
		summarized.Items = items
		out[i] = summarized
	}
	return out
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

// ValidateTranscript checks schema and message presence.
func ValidateTranscript(t *Transcript) error {
	if t.Schema != TranscriptSchema {
		return &TranscriptError{msg: "unsupported transcript schema: " + t.Schema}
	}
	if len(t.Messages) == 0 {
		return &TranscriptError{msg: "transcript has no messages"}
	}
	return nil
}

// FirstUserPreview returns the first ~maxLen chars of the first user
// message's text, single-line.
func FirstUserPreview(messages []Message, maxLen int) string {
	for _, m := range messages {
		if m.Role != agentmsg.RoleUser {
			continue
		}
		for _, it := range m.Items {
			if it.Text != "" {
				return truncateLine(it.Text, maxLen)
			}
		}
	}
	return ""
}

func truncateLine(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > maxLen {
		return s[:maxLen] + "…"
	}
	return s
}

// TranscriptError is returned for invalid transcript data.
type TranscriptError struct{ msg string }

func (e *TranscriptError) Error() string { return e.msg }

// TranscriptMeta holds everything from a Transcript except the messages.
// Used as the JSON sidecar in the split format.
type TranscriptMeta struct {
	Schema     string           `json:"schema"`
	ExportedAt time.Time        `json:"exported_at"`
	Source     TranscriptSource `json:"source"`
	Stats      TranscriptStats  `json:"stats"`
}

// WriteTranscriptSplit writes a transcript as a JSONL messages file plus a
// JSON metadata sidecar. Both writes are atomic (tmp + rename).
func WriteTranscriptSplit(jsonlPath, metaPath string, t *Transcript) error {
	if err := os.MkdirAll(filepath.Dir(jsonlPath), 0o755); err != nil {
		return fmt.Errorf("create transcripts dir: %w", err)
	}

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

	meta := TranscriptMeta{
		Schema:     TranscriptSchema,
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

// ReadTranscriptSplit reads a transcript from a JSONL messages file and a
// JSON metadata sidecar. If the metadata file is missing or corrupt, it
// degrades gracefully by computing stats from the messages.
func ReadTranscriptSplit(jsonlPath, metaPath string) (*Transcript, error) {
	msgs, err := loadMessages(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("read transcript messages: %w", err)
	}
	if len(msgs) == 0 {
		return nil, &TranscriptError{msg: "transcript has no messages"}
	}
	t := &Transcript{
		Schema:   TranscriptSchema,
		Messages: msgs,
	}
	if metaData, err := os.ReadFile(metaPath); err == nil {
		var meta TranscriptMeta
		if json.Unmarshal(metaData, &meta) == nil {
			t.ExportedAt = meta.ExportedAt
			t.Source = meta.Source
			t.Stats = meta.Stats
			return t, nil
		}
	}
	t.Stats = ComputeStats(msgs)
	return t, nil
}

// ExportSession returns a Transcript for the session.
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
		CodexThreadID:    s.threadID,
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

// ImportSession creates a new session pre-populated with a transcript's
// messages. The thread is started fresh on first Send — Codex won't have
// the prior context server-side, but the user sees the history.
func (m *Manager) ImportSession(worktreeName, workDir string, t *Transcript) (*Session, error) {
	if err := ValidateTranscript(t); err != nil {
		return nil, err
	}
	displayName := t.Source.DisplayName
	if displayName == "" {
		displayName = "Imported session"
	}
	displayName += " (imported)"

	s := m.CreateSession(worktreeName, workDir, displayName)
	s.mu.Lock()
	s.messages = make([]Message, len(t.Messages))
	copy(s.messages, t.Messages)
	s.persistAllMessages()
	s.mu.Unlock()
	m.persist()
	return s, nil
}
