// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wingedpig/trellis/internal/agentmsg"
)

// ArchiveQuery narrows a search over archived sessions. The zero value
// matches every archived session in every worktree (browse mode).
type ArchiveQuery struct {
	// Worktree limits the search to one worktree; "" searches all of them.
	// Archived sessions outlive the worktree they were created in, so the
	// unfiltered form is the one that still finds old work.
	Worktree string
	// Terms are lowercased, deduplicated search terms. Empty means browse
	// mode: every archived session matches and scores 0.
	Terms []string
	// From and To bound CreatedAt. Zero values are unbounded.
	From, To time.Time
	// IncludeTools widens the scan from prose (the user's and Codex's own
	// words) to commands, their output and file diffs. Off by default: that
	// output is the bulk of a transcript and the noisiest part of it.
	IncludeTools bool
}

// SessionSearchResult is one archived session matched by SearchArchivedSessions.
type SessionSearchResult struct {
	SessionInfo
	// Score ranks the match; 0 in browse mode.
	Score int `json:"score"`
	// Snippet is the text around the first term hit, or the session's opening
	// user message in browse mode.
	Snippet      string `json:"snippet,omitempty"`
	MessageCount int    `json:"message_count"`
}

// SearchTerms splits a raw query into lowercased, deduplicated terms,
// dropping fragments too short to be selective.
func SearchTerms(q string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, f := range strings.Fields(strings.ToLower(q)) {
		if len(f) < 2 || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// SearchArchivedSessions returns the archived sessions matching q, best match
// first. Sessions in the trash are never included: archive and trash are
// mutually exclusive states.
func (m *Manager) SearchArchivedSessions(q ArchiveQuery) []SessionSearchResult {
	m.mu.Lock()
	candidates := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		candidates = append(candidates, s)
	}
	m.mu.Unlock()

	results := make([]SessionSearchResult, 0, len(candidates))
	for _, s := range candidates {
		s.mu.Lock()
		archived := s.archivedAt != nil && s.trashedAt == nil
		worktree := s.worktreeName
		s.mu.Unlock()
		if !archived {
			continue
		}
		if q.Worktree != "" && worktree != q.Worktree {
			continue
		}
		info := s.Info()
		if !q.From.IsZero() && info.CreatedAt.Before(q.From) {
			continue
		}
		if !q.To.IsZero() && info.CreatedAt.After(q.To) {
			continue
		}
		score, snippet, count, ok := s.scoreArchive(q)
		if !ok {
			continue
		}
		results = append(results, SessionSearchResult{
			SessionInfo:  info,
			Score:        score,
			Snippet:      snippet,
			MessageCount: count,
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return archiveRecency(results[i].SessionInfo).After(archiveRecency(results[j].SessionInfo))
	})
	return results
}

// archiveRecency is the timestamp an archived session sorts by when scores
// tie: when it last did anything, falling back to when it was created.
func archiveRecency(info SessionInfo) time.Time {
	if !info.LastActivityAt.IsZero() {
		return info.LastActivityAt
	}
	return info.CreatedAt
}

// infosOf projects search results down to plain session infos.
func infosOf(results []SessionSearchResult) []*SessionInfo {
	out := make([]*SessionInfo, 0, len(results))
	for i := range results {
		info := results[i].SessionInfo
		out = append(out, &info)
	}
	return out
}

// isToolItem reports whether an item is machine output (a command run or a
// file edit) rather than prose written by the user or the agent.
func isToolItem(t string) bool {
	return t == "command_execution" || t == "file_change"
}

// scoreArchive ranks one session against q. ok is false when the session
// doesn't match: every term must appear somewhere in it, so a long transcript
// can't match on one incidental word out of a multi-word query.
func (s *Session) scoreArchive(q ArchiveQuery) (score int, snippet string, msgCount int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msgCount = len(s.messages)
	if len(q.Terms) == 0 {
		return 0, openingLineLocked(s.messages), msgCount, true
	}

	matched := make(map[string]bool, len(q.Terms))
	hit := func(weight int, text string) {
		if text == "" {
			return
		}
		low := strings.ToLower(text)
		for _, t := range q.Terms {
			if !strings.Contains(low, t) {
				continue
			}
			score += weight
			matched[t] = true
			if snippet == "" {
				snippet = snippetAround(text, t)
			}
		}
	}

	// The name the user gave the session is the strongest signal it carries.
	hit(8, s.displayName)
	for _, msg := range s.messages {
		weight := 3
		if msg.Role == agentmsg.RoleUser {
			weight = 5
		}
		for _, it := range msg.Items {
			if !isToolItem(it.Type) {
				hit(weight, it.Text)
				continue
			}
			if !q.IncludeTools {
				continue
			}
			hit(1, string(it.Command))
			hit(1, it.Output)
			hit(1, it.Path)
			hit(1, it.Diff)
		}
	}

	if len(matched) != len(q.Terms) {
		return 0, "", msgCount, false
	}
	return score, snippet, msgCount, true
}

// openingLineLocked previews a session by its first user message. Caller must
// hold s.mu.
func openingLineLocked(msgs []Message) string {
	for _, msg := range msgs {
		if msg.Role != agentmsg.RoleUser {
			continue
		}
		for _, it := range msg.Items {
			if !isToolItem(it.Type) && strings.TrimSpace(it.Text) != "" {
				return truncSnippet(collapseSpace(it.Text), 200)
			}
		}
	}
	return ""
}

// snippetAround returns the text surrounding the first occurrence of term,
// with ellipses where it was cut.
func snippetAround(text, term string) string {
	const window = 90
	flat := collapseSpace(text)
	i := strings.Index(strings.ToLower(flat), term)
	if i < 0 {
		return truncSnippet(flat, 2*window)
	}
	start := i - window
	prefix := ""
	if start > 0 {
		prefix = "…"
	} else {
		start = 0
	}
	end := i + len(term) + window
	suffix := ""
	if end < len(flat) {
		suffix = "…"
	} else {
		end = len(flat)
	}
	return prefix + flat[runeStart(flat, start):runeEnd(flat, end)] + suffix
}

// runeStart / runeEnd snap a byte offset outward to a rune boundary, so
// cutting a snippet out of the middle of a transcript can't leave a mangled
// multi-byte character behind.
func runeStart(s string, i int) int {
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

func runeEnd(s string, i int) int {
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return i
}

// collapseSpace flattens runs of whitespace so a snippet stays on one line.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncSnippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:runeStart(s, n)] + "…"
}
