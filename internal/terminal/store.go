// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WindowsData maps tmux session names to their window name lists.
type WindowsData map[string][]string

// WindowStore persists terminal window names to disk so they can be
// restored after a reboot (tmux sessions are lost on reboot).
type WindowStore struct {
	filePath string
}

// NewWindowStore creates a new window store at the given file path.
func NewWindowStore(filePath string) *WindowStore {
	return &WindowStore{filePath: filePath}
}

// Load reads the saved windows from disk. Returns an empty map if the file
// does not exist.
func (s *WindowStore) Load() (WindowsData, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(WindowsData), nil
		}
		return nil, fmt.Errorf("read windows file: %w", err)
	}
	if len(data) == 0 {
		return make(WindowsData), nil
	}
	var windows WindowsData
	if err := json.Unmarshal(data, &windows); err != nil {
		return nil, fmt.Errorf("parse windows file: %w", err)
	}
	return windows, nil
}

// Save writes the windows data to disk atomically (write tmp + rename).
func (s *WindowStore) Save(windows WindowsData) error {
	data, err := json.MarshalIndent(windows, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal windows: %w", err)
	}

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create windows dir: %w", err)
	}

	tmpPath := s.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write temp windows file: %w", err)
	}
	if err := os.Rename(tmpPath, s.filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename windows file: %w", err)
	}
	return nil
}
