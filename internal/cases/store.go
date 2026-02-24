// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package cases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// loadCase reads and unmarshals a case.json file.
func loadCase(path string) (*CaseJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read case.json: %w", err)
	}
	var c CaseJSON
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse case.json: %w", err)
	}
	return &c, nil
}

// saveCase atomically writes a case.json file using tmp+rename.
// It sets UpdatedAt to now before writing.
func saveCase(path string, c *CaseJSON) error {
	c.UpdatedAt = time.Now()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal case.json: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename tmp to case.json: %w", err)
	}

	return nil
}

// scanCases globs */case.json in the given directory and returns lightweight summaries.
func scanCases(dir string) ([]CaseInfo, error) {
	pattern := filepath.Join(dir, "*", "case.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob cases: %w", err)
	}

	var cases []CaseInfo
	for _, match := range matches {
		c, err := loadCase(match)
		if err != nil {
			continue // skip malformed cases
		}
		cases = append(cases, CaseInfo{
			ID:        c.ID,
			Title:     c.Title,
			Kind:      c.Kind,
			Status:    c.Status,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
			Worktree:  c.Worktree.Name,
		})
	}

	return cases, nil
}
