// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
)

// archiveQueryParams are the URL parameters shared by both agents' archived
// session endpoints:
//
//	q              - free-text query over session names and transcript prose
//	worktree       - limit to one worktree; absent searches every worktree
//	from, to       - ISO date or RFC3339; bounds on created_at
//	include_tools  - "1" to also scan tool calls, command output and diffs
type archiveQueryParams struct {
	Worktree     string
	Terms        []string
	From, To     time.Time
	IncludeTools bool
}

// parseArchiveQuery reads the shared archive-search parameters off a request.
// Unparseable dates are ignored rather than rejected — a half-typed date in
// the search form shouldn't blank the results.
func parseArchiveQuery(r *http.Request) archiveQueryParams {
	q := r.URL.Query()
	p := archiveQueryParams{
		Worktree:     q.Get("worktree"),
		IncludeTools: q.Get("include_tools") == "1",
	}
	if raw := q.Get("q"); raw != "" {
		p.Terms = claude.SearchTerms(raw)
	}
	if raw := q.Get("from"); raw != "" {
		if t, err := parseFlexTime(raw); err == nil {
			p.From = t
		}
	}
	if raw := q.Get("to"); raw != "" {
		if t, err := parseFlexTime(raw); err == nil {
			// Inclusive: a bare date means the end of that day.
			if len(raw) == 10 {
				t = t.Add(24*time.Hour - time.Nanosecond)
			}
			p.To = t
		}
	}
	return p
}

// SearchArchivedSessionsAPI returns the archived Claude sessions matching the
// query, best match first. With no query it lists the whole archive.
//
// GET /api/v1/claude/archived-sessions
func (h *ClaudeHandler) SearchArchivedSessionsAPI(w http.ResponseWriter, r *http.Request) {
	p := parseArchiveQuery(r)
	results := h.manager.SearchArchivedSessions(claude.ArchiveQuery{
		Worktree:     p.Worktree,
		Terms:        p.Terms,
		From:         p.From,
		To:           p.To,
		IncludeTools: p.IncludeTools,
	})
	WriteJSON(w, http.StatusOK, results)
}

// ArchiveSessionAPI files a Claude session into the archive.
//
// POST /api/v1/claude/sessions/{session}/archive
func (h *ClaudeHandler) ArchiveSessionAPI(w http.ResponseWriter, r *http.Request) {
	if err := h.manager.ArchiveSession(mux.Vars(r)["session"]); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnarchiveSessionAPI returns an archived Claude session to the normal listing.
//
// POST /api/v1/claude/sessions/{session}/unarchive
func (h *ClaudeHandler) UnarchiveSessionAPI(w http.ResponseWriter, r *http.Request) {
	if err := h.manager.UnarchiveSession(mux.Vars(r)["session"]); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SearchArchivedSessionsAPI returns the archived Codex sessions matching the
// query, best match first. With no query it lists the whole archive.
//
// GET /api/v1/codex/archived-sessions
func (h *CodexHandler) SearchArchivedSessionsAPI(w http.ResponseWriter, r *http.Request) {
	p := parseArchiveQuery(r)
	results := h.manager.SearchArchivedSessions(codex.ArchiveQuery{
		Worktree:     p.Worktree,
		Terms:        p.Terms,
		From:         p.From,
		To:           p.To,
		IncludeTools: p.IncludeTools,
	})
	WriteJSON(w, http.StatusOK, results)
}

// ArchiveSessionAPI files a Codex session into the archive.
//
// POST /api/v1/codex/sessions/{session}/archive
func (h *CodexHandler) ArchiveSessionAPI(w http.ResponseWriter, r *http.Request) {
	if err := h.manager.ArchiveSession(mux.Vars(r)["session"]); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnarchiveSessionAPI returns an archived Codex session to the normal listing.
//
// POST /api/v1/codex/sessions/{session}/unarchive
func (h *CodexHandler) UnarchiveSessionAPI(w http.ResponseWriter, r *http.Request) {
	if err := h.manager.UnarchiveSession(mux.Vars(r)["session"]); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
