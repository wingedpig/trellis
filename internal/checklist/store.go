// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package checklist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store persists Run records as one JSON file per run under dir. Writes are
// atomic (write-then-rename) so a crash mid-write never leaves a half-written
// record. The disk record is the authoritative source of truth (mirrors
// internal/pair.Store; PHASE_LOOP_SPEC §7).
type Store struct {
	dir string
	mu  sync.Mutex // serializes writes to the same file
}

// NewStore creates a store rooted at dir. The directory is created if it
// doesn't exist. Pass "" to disable persistence (all calls become no-ops).
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return &Store{}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create checklist dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Save writes r to disk atomically. Updates r.LastPersistedAt.
func (s *Store) Save(r *Run) error {
	if s.dir == "" || r == nil {
		return nil
	}
	r.LastPersistedAt = time.Now()

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}

	final := filepath.Join(s.dir, r.ID+".json")
	tmp := final + ".tmp"

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Load reads a run record by id. Returns os.ErrNotExist if not found.
func (s *Store) Load(id string) (*Run, error) {
	if s.dir == "" {
		return nil, os.ErrNotExist
	}
	path := filepath.Join(s.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("unmarshal run %s: %w", id, err)
	}
	return &r, nil
}

// LoadAll returns every record on disk, newest-first by CreatedAt. Corrupt
// files are skipped rather than failing the whole load.
func (s *Store) LoadAll() ([]*Run, error) {
	if s.dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("readdir: %w", err)
	}
	var runs []*Run
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		r, err := s.Load(id)
		if err != nil {
			continue
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	return runs, nil
}

// Delete removes a run record from disk.
func (s *Store) Delete(id string) error {
	if s.dir == "" {
		return nil
	}
	path := filepath.Join(s.dir, id+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
