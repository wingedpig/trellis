// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SessionRecord is a persisted session entry.
type SessionRecord struct {
	ID           string     `json:"id"`
	WorktreeName string     `json:"worktree_name"`
	DisplayName  string     `json:"display_name"`
	SessionID    string     `json:"session_id"`  // Claude CLI session_id for --session-id resume
	WorkDir      string     `json:"work_dir"`
	CreatedAt    time.Time  `json:"created_at"`
	TrashedAt    *time.Time `json:"trashed_at,omitempty"`
}

// loadRecords reads session records from disk.
func loadRecords(filePath string) ([]SessionRecord, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions file: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var records []SessionRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse sessions file: %w", err)
	}
	return records, nil
}

// loadMessages reads messages from a per-session file.
// Supports both JSONL format (one message per line) and legacy JSON array format.
func loadMessages(filePath string) ([]Message, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read messages file: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	// Detect format: JSON array starts with '[', JSONL starts with '{'
	for _, b := range data {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if b == '[' {
			// Legacy JSON array format
			var msgs []Message
			if err := json.Unmarshal(data, &msgs); err != nil {
				return nil, fmt.Errorf("parse messages file (json): %w", err)
			}
			return msgs, nil
		}
		break
	}

	// JSONL format: one JSON object per line
	var msgs []Message
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open messages file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // Up to 10MB per line
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			// Tolerate a partial last line from a crash
			break
		}
		msgs = append(msgs, msg)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan messages file: %w", err)
	}
	return msgs, nil
}

// appendMessage appends a single message as a JSON line to the messages file.
func appendMessage(filePath string, msg Message) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create messages dir: %w", err)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open messages file for append: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("append message: %w", err)
	}
	return nil
}

// rewriteMessages overwrites the messages file with the given messages in JSONL format.
// Used for destructive operations like Reset and ImportSession.
func rewriteMessages(filePath string, msgs []Message) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create messages dir: %w", err)
	}

	tmpPath := filePath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp messages file: %w", err)
	}

	enc := json.NewEncoder(f)
	for _, msg := range msgs {
		if err := enc.Encode(msg); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("encode message: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp messages file: %w", err)
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename messages file: %w", err)
	}
	return nil
}

// saveRecords writes session records to disk atomically.
func saveRecords(filePath string, records []SessionRecord) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sessions: %w", err)
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}

	// Atomic write: temp file + rename
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write temp sessions file: %w", err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename sessions file: %w", err)
	}
	return nil
}
