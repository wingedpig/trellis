// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/cases"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/worktree"
)

func postTask(t *testing.T, fn http.HandlerFunc, path, body string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	rec := httptest.NewRecorder()
	fn(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	return rec, resp
}

func errMessage(resp map[string]interface{}) string {
	e, _ := resp["error"].(map[string]interface{})
	msg, _ := e["message"].(string)
	return msg
}

func TestTaskStart_ValidatesBeforeCreatingWorktree(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want       string
	}{
		{"missing branch", `{"agent":"claude"}`, http.StatusBadRequest, "branch is required"},
		{"unknown agent", `{"branch":"b1","agent":"gemini"}`, http.StatusBadRequest, "unknown agent"},
		{"bad claude model", `{"branch":"b1","model":"gpt-6-astra"}`, http.StatusBadRequest, "unknown claude model"},
		{"codex unavailable", `{"branch":"b1","agent":"codex"}`, http.StatusServiceUnavailable, "codex is not available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wtMgr := newMockWorktreeManager()
			before := len(wtMgr.worktrees)
			h := NewTaskHandler(wtMgr, claude.NewManager(t.TempDir()), nil, nil, nil)
			rec, resp := postTask(t, h.Start, "/api/v1/tasks/start", tc.body)
			assert.Equal(t, tc.status, rec.Code)
			assert.Contains(t, errMessage(resp), tc.want)
			assert.Len(t, wtMgr.worktrees, before, "no worktree may be created on a rejected request")
		})
	}
}

func TestTaskStart_CreatesWorktreeAndClaudeSessionByDefault(t *testing.T) {
	wtMgr := newMockWorktreeManager()
	claudeMgr := claude.NewManager(t.TempDir())
	h := NewTaskHandler(wtMgr, claudeMgr, nil, nil, nil)

	rec, resp := postTask(t, h.Start, "/api/v1/tasks/start", `{"branch":"fix-1","session_name":"investigate"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, "claude", data["agent"])
	assert.Equal(t, false, data["prompt_sent"])

	// Keyed like the homepage URL (project prefix stripped), so the session
	// shows on the worktree's home page and in the picker as "@fix-1".
	assert.Empty(t, claudeMgr.ListSessions("test-project-fix-1"))
	sessions := claudeMgr.ListSessions("fix-1")
	require.Len(t, sessions, 1)
	assert.Equal(t, "investigate", sessions[0].DisplayName)
	assert.Equal(t, sessions[0].ID, data["session_id"])
	assert.Equal(t, "/claude/fix-1/"+sessions[0].ID, data["session_url"])
}

func TestTaskFinish_Validation(t *testing.T) {
	wtMgr := newMockWorktreeManager()
	h := NewTaskHandler(wtMgr, nil, nil, nil, nil)

	rec, _ := postTask(t, h.Finish, "/api/v1/tasks/finish", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec, resp := postTask(t, h.Finish, "/api/v1/tasks/finish", `{"worktree":"feature-x","strategy":"octopus"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, errMessage(resp), "unknown merge strategy")

	rec, _ = postTask(t, h.Finish, "/api/v1/tasks/finish", `{"worktree":"nope"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTaskFinish_RefusesToDeleteUnwrappedCase(t *testing.T) {
	dir := t.TempDir()
	wtMgr := newMockWorktreeManager()
	wtMgr.worktrees = append(wtMgr.worktrees, worktree.WorktreeInfo{Path: dir, Branch: "with-case"})
	caseMgr := cases.NewManager("trellis/cases")
	_, err := caseMgr.Create(dir, "Digest crash", "bug", "with-case", "with-case", "")
	require.NoError(t, err)

	h := NewTaskHandler(wtMgr, nil, nil, caseMgr, nil)
	rec, resp := postTask(t, h.Finish, "/api/v1/tasks/finish", `{"worktree":"with-case"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, errMessage(resp), `"Digest crash"`)

	// keep_worktree gets past the case check (the mock then refuses to merge).
	rec, resp = postTask(t, h.Finish, "/api/v1/tasks/finish", `{"worktree":"with-case","keep_worktree":true}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, errMessage(resp), "merge not supported by mock")
}
