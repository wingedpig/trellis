// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Storage handles reading and writing trace reports to disk.
type Storage struct {
	dir string
}

// NewStorage creates a new storage instance.
func NewStorage(dir string) (*Storage, error) {
	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating reports directory: %w", err)
	}
	return &Storage{dir: dir}, nil
}

// Save writes a trace report to disk.
func (s *Storage) Save(report *TraceReport) (string, error) {
	filename := s.reportPath(report.Name)

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling report: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return "", fmt.Errorf("writing report: %w", err)
	}

	return filename, nil
}

// Load reads a trace report from disk.
func (s *Storage) Load(name string) (*TraceReport, error) {
	filename := s.reportPath(name)

	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("report not found: %s", name)
		}
		return nil, fmt.Errorf("reading report: %w", err)
	}

	var report TraceReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parsing report: %w", err)
	}

	return &report, nil
}

// List returns summaries of all saved reports.
func (s *Storage) List() ([]ReportSummary, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading reports directory: %w", err)
	}

	var summaries []ReportSummary
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".json")
		report, err := s.Load(name)
		if err != nil {
			// Skip invalid reports
			continue
		}

		summaries = append(summaries, ReportSummary{
			Name:       name, // Use filename (sanitized), not the name stored in the report
			TraceID:    report.TraceID,
			Group:      report.Group,
			Status:     report.Status,
			CreatedAt:  report.CreatedAt,
			EntryCount: len(report.Entries),
			TimeStart:  report.TimeRange.Start,
			TimeEnd:    report.TimeRange.End,
		})
	}

	return summaries, nil
}

// Delete removes a report from disk.
func (s *Storage) Delete(name string) error {
	filename := s.reportPath(name)

	if err := os.Remove(filename); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("report not found: %s", name)
		}
		return fmt.Errorf("deleting report: %w", err)
	}

	return nil
}

// DeleteOlderThan removes all reports created before the given time.
func (s *Storage) DeleteOlderThan(cutoff time.Time) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading reports directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".json")
		report, err := s.Load(name)
		if err != nil {
			continue
		}

		if report.CreatedAt.Before(cutoff) {
			if err := s.Delete(name); err != nil {
				// Log but continue
				continue
			}
		}
	}

	return nil
}

// reportPath returns the full path for a report file.
func (s *Storage) reportPath(name string) string {
	// Sanitize name to prevent path traversal
	safeName := strings.ReplaceAll(name, "/", "_")
	safeName = strings.ReplaceAll(safeName, "\\", "_")
	safeName = strings.ReplaceAll(safeName, "..", "_")
	return filepath.Join(s.dir, safeName+".json")
}
