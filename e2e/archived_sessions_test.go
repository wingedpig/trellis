// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/api"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
)

// archiveRow is the subset of an archive search result these tests assert on.
type archiveRow struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	WorktreeName string `json:"worktree_name"`
	ArchivedAt   string `json:"archived_at"`
	Score        int    `json:"score"`
}

// TestArchivedSessionsAPI drives the archive through the real router: archive,
// list, search, unarchive. It also pins the route shapes — /archived-sessions
// sits next to /{worktree}/sessions in the same path space, so a registration
// order mistake would show up here as a 404 or the wrong handler answering.
func TestArchivedSessionsAPI(t *testing.T) {
	deps := createTestDependencies(t)
	claudeMgr := claude.NewManager(t.TempDir())
	codexMgr := codex.NewManager(t.TempDir())
	deps.ClaudeManager = claudeMgr
	deps.CodexManager = codexMgr

	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	filed := claudeMgr.CreateSession("wt", t.TempDir(), "Flusher fix")
	kept := claudeMgr.CreateSession("wt", t.TempDir(), "Still working")
	codexFiled := codexMgr.CreateSession("wt", t.TempDir(), "Codex cleanup")

	// Nothing is archived yet.
	assert.Empty(t, getArchive(t, server.URL, "claude", ""))

	post(t, server.URL+"/api/v1/claude/sessions/"+filed.ID()+"/archive", http.StatusNoContent)
	post(t, server.URL+"/api/v1/codex/sessions/"+codexFiled.ID()+"/archive", http.StatusNoContent)

	// The archived session leaves the worktree listing; the other stays.
	active := listSessions(t, server.URL, "claude", "wt")
	require.Len(t, active, 1)
	assert.Equal(t, kept.ID(), active[0].ID)

	// Archive listing, per agent.
	claudeArchive := getArchive(t, server.URL, "claude", "")
	require.Len(t, claudeArchive, 1)
	assert.Equal(t, filed.ID(), claudeArchive[0].ID)
	assert.NotEmpty(t, claudeArchive[0].ArchivedAt)

	codexArchive := getArchive(t, server.URL, "codex", "")
	require.Len(t, codexArchive, 1)
	assert.Equal(t, codexFiled.ID(), codexArchive[0].ID)

	// Search by name, and a query that matches nothing.
	hits := getArchive(t, server.URL, "claude", "q=flusher")
	require.Len(t, hits, 1)
	assert.Equal(t, filed.ID(), hits[0].ID)
	assert.Greater(t, hits[0].Score, 0)
	assert.Empty(t, getArchive(t, server.URL, "claude", "q=nonexistent"))

	// Worktree filter.
	assert.Len(t, getArchive(t, server.URL, "claude", "worktree=wt"), 1)
	assert.Empty(t, getArchive(t, server.URL, "claude", "worktree=other"))

	// Unarchive puts it back in the normal listing.
	post(t, server.URL+"/api/v1/claude/sessions/"+filed.ID()+"/unarchive", http.StatusNoContent)
	assert.Empty(t, getArchive(t, server.URL, "claude", ""))
	assert.Len(t, listSessions(t, server.URL, "claude", "wt"), 2)

	// Unknown session IDs are a 404, not a silent success.
	post(t, server.URL+"/api/v1/claude/sessions/does-not-exist/archive", http.StatusNotFound)
	post(t, server.URL+"/api/v1/codex/sessions/does-not-exist/unarchive", http.StatusNotFound)
}

// TestArchivedSessionsPage checks the archive page renders and preselects the
// filters it was linked with.
func TestArchivedSessionsPage(t *testing.T) {
	deps := createTestDependencies(t)
	claudeMgr := claude.NewManager(t.TempDir())
	deps.ClaudeManager = claudeMgr
	deps.CodexManager = codex.NewManager(t.TempDir())

	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	s := claudeMgr.CreateSession("archived-only-wt", t.TempDir(), "Session 1")
	require.NoError(t, claudeMgr.ArchiveSession(s.ID()))

	resp, err := http.Get(server.URL + "/archived-sessions?agent=claude&worktree=archived-only-wt")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	assert.Contains(t, html, "Archived sessions")
	assert.Contains(t, html, `<option value="claude" selected>`)
	// The worktree exists only as the home of an archived session, and still
	// has to be offered in the filter.
	assert.Contains(t, html, `<option value="archived-only-wt" selected>`)
}

func post(t *testing.T, url string, wantStatus int) {
	t.Helper()
	resp, err := http.Post(url, "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, wantStatus, resp.StatusCode, "POST %s", url)
}

func getArchive(t *testing.T, base, agent, query string) []archiveRow {
	t.Helper()
	u := base + "/api/v1/" + agent + "/archived-sessions"
	if query != "" {
		u += "?" + query
	}
	resp, err := http.Get(u)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Data []archiveRow `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.Data
}

func listSessions(t *testing.T, base, agent, worktree string) []archiveRow {
	t.Helper()
	resp, err := http.Get(base + "/api/v1/" + agent + "/" + url.PathEscape(worktree) + "/sessions")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Data []archiveRow `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.Data
}
