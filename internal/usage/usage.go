// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package usage computes Claude Code token usage and cost from the transcript
// JSONL files Claude Code writes under its projects directories (the same
// data source the ccusage CLI uses). Files are parsed lazily and cached by
// (path, mtime, size), so repeated scans only re-read changed files.
package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Agent identifies which coding agent produced a usage entry.
const (
	AgentClaude = "claude"
	AgentCodex  = "codex"
)

// Entry is one deduplicated assistant API call from a transcript.
type Entry struct {
	Key       string // dedup key (message id + request id); "" when unavailable
	Agent     string // AgentClaude or AgentCodex
	Timestamp time.Time
	Model     string
	Cwd       string
	SessionID string
	Input     int // base (uncached) input tokens
	Output    int
	CacheRead int
	Cache5m   int // 5-minute-TTL cache writes (Claude only)
	Cache1h   int // 1-hour-TTL cache writes (Claude only)
	CostUSD   float64
}

// Manager scans Claude Code and Codex CLI data directories for usage data.
type Manager struct {
	mu        sync.Mutex
	dirs      []string // Claude Code projects directories
	codexDirs []string // Codex CLI sessions directories
	cache     map[string]*fileEntries
}

type fileEntries struct {
	modTime time.Time
	size    int64
	entries []Entry
}

// NewManager creates a manager scanning the standard Claude Code data
// directories (~/.claude/projects and ~/.config/claude/projects, honoring
// CLAUDE_CONFIG_DIR like Claude Code and ccusage do) and the Codex CLI
// sessions directory (~/.codex/sessions, honoring CODEX_HOME).
func NewManager() *Manager {
	return &Manager{
		dirs:      defaultDirs(),
		codexDirs: defaultCodexDirs(),
		cache:     make(map[string]*fileEntries),
	}
}

// NewManagerWithDirs creates a manager scanning explicit Claude projects and
// Codex sessions directories. Used by tests.
func NewManagerWithDirs(claudeDirs, codexDirs []string) *Manager {
	return &Manager{
		dirs:      claudeDirs,
		codexDirs: codexDirs,
		cache:     make(map[string]*fileEntries),
	}
}

func defaultDirs() []string {
	if env := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); env != "" {
		var dirs []string
		for _, d := range strings.Split(env, ",") {
			if d = strings.TrimSpace(d); d != "" {
				dirs = append(dirs, filepath.Join(expandHome(d), "projects"))
			}
		}
		return dirs
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude", "projects"),
		filepath.Join(home, ".config", "claude", "projects"),
	}
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// Entries returns all deduplicated usage entries with timestamps at or after
// since. Files whose mtime predates since are skipped entirely (a transcript
// only ever grows, so its entries cannot postdate its mtime).
func (m *Manager) Entries(since time.Time) []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	var all []Entry
	seen := make(map[string]struct{})
	collect := func(path string, modTime time.Time, size int64, parse func(string) []Entry) {
		for _, e := range m.fileEntriesLocked(path, modTime, size, parse) {
			if e.Timestamp.Before(since) {
				continue
			}
			if e.Key != "" {
				if _, dup := seen[e.Key]; dup {
					continue
				}
				seen[e.Key] = struct{}{}
			}
			all = append(all, e)
		}
	}

	for _, dir := range m.dirs {
		projects, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, proj := range projects {
			if !proj.IsDir() {
				continue
			}
			projDir := filepath.Join(dir, proj.Name())
			files, err := os.ReadDir(projDir)
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
					continue
				}
				info, err := f.Info()
				if err != nil || info.ModTime().Before(since) {
					continue
				}
				collect(filepath.Join(projDir, f.Name()), info.ModTime(), info.Size(), parseFile)
			}
		}
	}

	// Codex rollouts live in nested YYYY/MM/DD directories.
	for _, dir := range m.codexDirs {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.ModTime().Before(since) {
				return nil
			}
			collect(path, info.ModTime(), info.Size(), parseCodexFile)
			return nil
		})
	}
	return all
}

// fileEntriesLocked returns parsed entries for a file, using the cache when
// the file is unchanged. Caller must hold m.mu.
func (m *Manager) fileEntriesLocked(path string, modTime time.Time, size int64, parse func(string) []Entry) []Entry {
	if cached, ok := m.cache[path]; ok && cached.modTime.Equal(modTime) && cached.size == size {
		return cached.entries
	}
	entries := parse(path)
	m.cache[path] = &fileEntries{modTime: modTime, size: size, entries: entries}
	return entries
}

// transcriptLine is the subset of a Claude Code transcript line we need.
type transcriptLine struct {
	Type      string  `json:"type"`
	Timestamp string  `json:"timestamp"`
	Cwd       string  `json:"cwd"`
	SessionID string  `json:"sessionId"`
	RequestID string  `json:"requestId"`
	CostUSD   float64 `json:"costUSD"` // precomputed cost (older Claude Code versions)
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreation            struct {
				Ephemeral5m int `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// assistantMarker pre-filters lines before the (much more expensive) JSON
// parse. Claude Code writes compact JSON, so the marker is byte-exact.
var assistantMarker = []byte(`"type":"assistant"`)

func parseFile(path string) []Entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []Entry
	seen := make(map[string]struct{}) // within-file dedup (streamed content blocks repeat usage)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, assistantMarker) {
			continue
		}
		var tl transcriptLine
		if json.Unmarshal(line, &tl) != nil {
			continue
		}
		if tl.Type != "assistant" {
			continue
		}
		u := tl.Message.Usage
		if u.InputTokens == 0 && u.OutputTokens == 0 &&
			u.CacheCreationInputTokens == 0 && u.CacheReadInputTokens == 0 {
			continue
		}
		// Synthetic entries (error placeholders) carry no real usage.
		if tl.Message.Model == "" || tl.Message.Model == "<synthetic>" {
			continue
		}

		key := ""
		if tl.Message.ID != "" {
			key = tl.Message.ID + ":" + tl.RequestID
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}

		ts, err := time.Parse(time.RFC3339Nano, tl.Timestamp)
		if err != nil {
			continue
		}

		c5, c1 := u.CacheCreation.Ephemeral5m, u.CacheCreation.Ephemeral1h
		if c5 == 0 && c1 == 0 && u.CacheCreationInputTokens > 0 {
			// Older transcripts lack the TTL breakdown; bill as 5-minute writes.
			c5 = u.CacheCreationInputTokens
		}

		e := Entry{
			Key:       key,
			Agent:     AgentClaude,
			Timestamp: ts,
			Model:     tl.Message.Model,
			Cwd:       tl.Cwd,
			SessionID: tl.SessionID,
			Input:     u.InputTokens,
			Output:    u.OutputTokens,
			CacheRead: u.CacheReadInputTokens,
			Cache5m:   c5,
			Cache1h:   c1,
			CostUSD:   tl.CostUSD,
		}
		if e.CostUSD == 0 {
			e.CostUSD = costFor(e)
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		// A line over the 16MB cap aborts the scan; keep what parsed so far.
		log.Printf("usage: partial parse of %s: %v", path, err)
	}
	return entries
}
