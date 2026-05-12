// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// SessionRecord is the persisted form of a Codex session in sessions.json.
type SessionRecord struct {
	ID           string     `json:"id"`             // Trellis session UUID
	WorktreeName string     `json:"worktree_name"`
	DisplayName  string     `json:"display_name"`
	ThreadID     string     `json:"thread_id"`      // Codex app-server thread id
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

// saveRecords writes session records to disk atomically.
func saveRecords(filePath string, records []SessionRecord) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sessions: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}
	tmp := filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp sessions file: %w", err)
	}
	if err := os.Rename(tmp, filePath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename sessions file: %w", err)
	}
	return nil
}

// loadMessages reads messages from a per-session JSONL file. Tolerates a
// truncated last line (e.g., from a crash mid-write).
//
// Uses bufio.Reader rather than bufio.Scanner so individual lines can grow
// arbitrarily large — Codex's commandExecution.output field can hold many
// MB of stdout per turn, easily exceeding bufio.Scanner's max-token cap.
// Hitting that cap previously caused the load to fail and the session to
// appear empty after restart.
func loadMessages(filePath string) ([]Message, error) {
	f, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open messages file: %w", err)
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 1024*1024)
	var msgs []Message
	for {
		line, err := readLineRPC(br)
		if len(line) > 0 {
			var msg Message
			if uerr := json.Unmarshal(line, &msg); uerr != nil {
				// Tolerate a partial last line from a mid-write crash.
				break
			}
			msgs = append(msgs, msg)
		}
		if err != nil {
			if err == io.EOF {
				return msgs, nil
			}
			return msgs, fmt.Errorf("read messages file: %w", err)
		}
	}
	return msgs, nil
}

// appendMessage appends a single message as a JSON line.
func appendMessage(filePath string, msg Message) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("create messages dir: %w", err)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	data = append(data, '\n')
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open messages file for append: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("append message: %w", err)
	}
	return nil
}

// rewriteMessages replaces the entire messages file atomically.
func rewriteMessages(filePath string, msgs []Message) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("create messages dir: %w", err)
	}
	tmp := filePath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp messages file: %w", err)
	}
	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("encode message: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp messages file: %w", err)
	}
	if err := os.Rename(tmp, filePath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename messages file: %w", err)
	}
	return nil
}
