// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package usage

import (
	"sort"
	"strings"
	"time"
)

// Totals accumulates token counts and cost across entries.
type Totals struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	Calls            int     `json:"calls"`
}

func (t *Totals) add(e Entry) {
	t.InputTokens += e.Input
	t.OutputTokens += e.Output
	t.CacheReadTokens += e.CacheRead
	t.CacheWriteTokens += e.Cache5m + e.Cache1h
	t.CostUSD += e.CostUSD
	t.Calls++
}

// DailyUsage is one calendar day (local time) across all projects.
type DailyUsage struct {
	Date   string   `json:"date"` // YYYY-MM-DD
	Models []string `json:"models"`
	Totals
}

// WorktreeUsage is usage attributed to one of this project's worktrees.
type WorktreeUsage struct {
	Worktree string `json:"worktree"`
	Path     string `json:"path"`
	Totals
}

// SessionUsage is one agent session within this project's worktrees.
type SessionUsage struct {
	SessionID    string    `json:"session_id"`
	Agent        string    `json:"agent"`
	Worktree     string    `json:"worktree"`
	LastActivity time.Time `json:"last_activity"`
	Models       []string  `json:"models"`
	Totals
}

// Report is the aggregate usage view served to the UI. Daily/Today/Total
// cover all Claude Code usage on this machine; Worktrees and Sessions are
// scoped to the worktree paths passed in.
type Report struct {
	Days         int               `json:"days"`
	GeneratedAt  time.Time         `json:"generated_at"`
	Today        Totals            `json:"today"`
	Total        Totals            `json:"total"`
	TodayByAgent map[string]Totals `json:"today_by_agent"`
	TotalByAgent map[string]Totals `json:"total_by_agent"`
	Daily        []DailyUsage      `json:"daily"`
	Worktrees    []WorktreeUsage   `json:"worktrees"`
	Sessions     []SessionUsage    `json:"sessions"`
}

// maxSessionRows bounds the sessions table; sessions are sorted by cost so
// the cut drops only the cheapest tail.
const maxSessionRows = 50

// Report aggregates usage over the trailing number of days. worktreePaths
// maps absolute worktree paths to display names and scopes the per-worktree
// and per-session sections; pass nil to skip those sections.
func (m *Manager) Report(days int, worktreePaths map[string]string) *Report {
	if days <= 0 {
		days = 30
	}
	now := time.Now()
	since := now.AddDate(0, 0, -days+1)
	since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location())
	entries := m.Entries(since)

	rep := &Report{
		Days:         days,
		GeneratedAt:  now,
		TodayByAgent: make(map[string]Totals),
		TotalByAgent: make(map[string]Totals),
	}
	today := now.Format("2006-01-02")

	dayMap := make(map[string]*DailyUsage)
	dayModels := make(map[string]map[string]struct{})
	wtMap := make(map[string]*WorktreeUsage) // keyed by worktree path
	sessMap := make(map[string]*SessionUsage)
	sessModels := make(map[string]map[string]struct{})

	for _, e := range entries {
		local := e.Timestamp.Local()
		day := local.Format("2006-01-02")

		rep.Total.add(e)
		agentTotal := rep.TotalByAgent[e.Agent]
		agentTotal.add(e)
		rep.TotalByAgent[e.Agent] = agentTotal
		if day == today {
			rep.Today.add(e)
			agentToday := rep.TodayByAgent[e.Agent]
			agentToday.add(e)
			rep.TodayByAgent[e.Agent] = agentToday
		}

		d := dayMap[day]
		if d == nil {
			d = &DailyUsage{Date: day}
			dayMap[day] = d
			dayModels[day] = make(map[string]struct{})
		}
		d.add(e)
		dayModels[day][e.Model] = struct{}{}

		wtPath, wtName, ok := matchWorktree(e.Cwd, worktreePaths)
		if !ok {
			continue
		}
		w := wtMap[wtPath]
		if w == nil {
			w = &WorktreeUsage{Worktree: wtName, Path: wtPath}
			wtMap[wtPath] = w
		}
		w.add(e)

		if e.SessionID != "" {
			sessKey := e.Agent + ":" + e.SessionID
			s := sessMap[sessKey]
			if s == nil {
				s = &SessionUsage{SessionID: e.SessionID, Agent: e.Agent, Worktree: wtName}
				sessMap[sessKey] = s
				sessModels[sessKey] = make(map[string]struct{})
			}
			s.add(e)
			if local.After(s.LastActivity) {
				s.LastActivity = local
			}
			sessModels[sessKey][e.Model] = struct{}{}
		}
	}

	for day, d := range dayMap {
		d.Models = sortedKeys(dayModels[day])
		rep.Daily = append(rep.Daily, *d)
	}
	sort.Slice(rep.Daily, func(i, j int) bool { return rep.Daily[i].Date > rep.Daily[j].Date })

	for _, w := range wtMap {
		rep.Worktrees = append(rep.Worktrees, *w)
	}
	sort.Slice(rep.Worktrees, func(i, j int) bool { return rep.Worktrees[i].CostUSD > rep.Worktrees[j].CostUSD })

	for id, s := range sessMap {
		s.Models = sortedKeys(sessModels[id])
		rep.Sessions = append(rep.Sessions, *s)
	}
	sort.Slice(rep.Sessions, func(i, j int) bool { return rep.Sessions[i].CostUSD > rep.Sessions[j].CostUSD })
	if len(rep.Sessions) > maxSessionRows {
		rep.Sessions = rep.Sessions[:maxSessionRows]
	}

	return rep
}

// Today returns today's totals across all projects.
func (m *Manager) Today() Totals {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var t Totals
	for _, e := range m.Entries(start) {
		t.add(e)
	}
	return t
}

// matchWorktree finds the worktree whose path is a prefix of cwd, preferring
// the longest match so nested worktree directories attribute correctly.
func matchWorktree(cwd string, worktreePaths map[string]string) (path, name string, ok bool) {
	if cwd == "" {
		return "", "", false
	}
	best := ""
	for p := range worktreePaths {
		if p == "" {
			continue
		}
		if cwd == p || strings.HasPrefix(cwd, strings.TrimSuffix(p, "/")+"/") {
			if len(p) > len(best) {
				best = p
			}
		}
	}
	if best == "" {
		return "", "", false
	}
	return best, worktreePaths[best], true
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
