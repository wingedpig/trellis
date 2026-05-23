// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package pair

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

// Store persists Pair records as one JSON file per pair under dir. Writes are
// atomic (write-then-rename) so a crash mid-write never leaves a half-written
// record. The disk record is the authoritative source of truth for pair
// state (PAIRING_SPEC §8.1).
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
		return nil, fmt.Errorf("create pair dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Save writes p to disk atomically. Updates p.LastPersistedAt.
func (s *Store) Save(p *Pair) error {
	if s.dir == "" || p == nil {
		return nil
	}
	p.LastPersistedAt = time.Now()

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pair: %w", err)
	}

	final := filepath.Join(s.dir, p.ID+".json")
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

// Load reads a pair record by id. Returns os.ErrNotExist if not found.
func (s *Store) Load(id string) (*Pair, error) {
	if s.dir == "" {
		return nil, os.ErrNotExist
	}
	path := filepath.Join(s.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Pair
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("unmarshal pair %s: %w", id, err)
	}
	return &p, nil
}

// LoadAll returns every record on disk, in newest-first order by CreatedAt.
// Records that fail to parse are logged via the error return only if all
// fail; otherwise the bad ones are silently skipped so one corrupt file
// doesn't take out the whole feature.
func (s *Store) LoadAll() ([]*Pair, error) {
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
	var pairs []*Pair
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		p, err := s.Load(id)
		if err != nil {
			// Skip corrupt files rather than failing the whole load.
			continue
		}
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].CreatedAt.After(pairs[j].CreatedAt)
	})
	return pairs, nil
}

// Delete removes a pair record from disk.
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
