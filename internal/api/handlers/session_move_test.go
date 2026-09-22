// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/worktree"
)

func moveSessionRequest(t *testing.T, deps moveSessionDeps, primary moveSessionRef, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/sessions/"+primary.SessionID+"/move", bytes.NewReader(data))
	req = mux.SetURLVars(req, map[string]string{"session": primary.SessionID})
	rec := httptest.NewRecorder()
	if primary.Agent == "claude" {
		NewClaudeHandler(deps.claudeMgr, deps.codexMgr, deps.worktreeMgr, nil, nil, deps.bus).MoveSessionAPI(rec, req)
	} else {
		NewCodexHandler(deps.codexMgr, deps.claudeMgr, deps.worktreeMgr, nil, nil, deps.bus).MoveSessionAPI(rec, req)
	}
	return rec
}

func TestMoveSessionAPI(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, multiple := range []bool{false, true} {
			name := agent + "/single"
			if multiple {
				name = agent + "/mixed"
			}
			t.Run(name, func(t *testing.T) {
				source := initRepo(t)
				original, err := os.ReadFile(filepath.Join(source, "keep.go"))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(source, "keep.go"), []byte("modified\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(source, "selected.sh"), []byte("selected\n"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(source, "leave.txt"), []byte("leave\n"), 0o644))

				cm := claude.NewManager(t.TempDir())
				xm := codex.NewManager(t.TempDir())
				t.Cleanup(cm.Shutdown)
				t.Cleanup(xm.Shutdown)
				bus := events.NewMemoryEventBus(events.MemoryBusConfig{HistoryMaxEvents: 100})
				t.Cleanup(func() { _ = bus.Close() })
				wm := worktree.NewManager(worktree.NewRealGitExecutor(), bus, config.WorktreeConfig{}, source, t.TempDir(), "project")
				deps := moveSessionDeps{claudeMgr: cm, codexMgr: xm, worktreeMgr: wm, bus: bus}

				claudePrimary := cm.CreateSession("main", source, "Claude primary")
				codexPrimary := xm.CreateSession("main", source, "Codex primary")
				claudeExtra := cm.CreateSession("main", source, "Claude extra")
				codexExtra := xm.CreateSession("main", source, "Codex extra")
				claudeKept := cm.CreateSession("main", source, "Claude kept")
				codexKept := xm.CreateSession("main", source, "Codex kept")
				primary := moveSessionRef{Agent: agent, SessionID: claudePrimary.ID()}
				if agent == "codex" {
					primary.SessionID = codexPrimary.ID()
				}
				want := []moveSessionRef{primary}
				body := map[string]interface{}{
					"branch": "feature/move",
					"files":  []string{"keep.go", "selected.sh"},
				}
				if multiple {
					extras := []moveSessionRef{
						{Agent: "claude", SessionID: claudeExtra.ID()},
						{Agent: "codex", SessionID: codexExtra.ID()},
					}
					want = append(want, extras...)
					// Repeated entries, including the primary, must only move once.
					body["additional_sessions"] = append(extras, primary, extras[0])
				}
				rec := moveSessionRequest(t, deps, primary, body)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				var response struct {
					Data struct {
						SessionID    string           `json:"session_id"`
						Sessions     []moveSessionRef `json:"sessions"`
						Worktree     string           `json:"worktree"`
						Path         string           `json:"path"`
						RevertErrors []string         `json:"revert_errors"`
					} `json:"data"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
				assert.Equal(t, primary.SessionID, response.Data.SessionID)
				assert.Equal(t, want, response.Data.Sessions)
				assert.Equal(t, "feature-move", response.Data.Worktree)
				assert.Empty(t, response.Data.RevertErrors)
				worktrees, err := wm.List()
				require.NoError(t, err)
				assert.Len(t, worktrees, 2, "the selection should create exactly one worktree")
				for _, ref := range want {
					if ref.Agent == "claude" {
						assert.Equal(t, response.Data.Worktree, cm.GetSession(ref.SessionID).Info().WorktreeName)
					} else {
						assert.Equal(t, response.Data.Worktree, xm.GetSession(ref.SessionID).Info().WorktreeName)
					}
				}
				assert.Equal(t, "main", claudeKept.Info().WorktreeName)
				assert.Equal(t, "main", codexKept.Info().WorktreeName)
				if !multiple {
					assert.Equal(t, "main", claudeExtra.Info().WorktreeName)
					assert.Equal(t, "main", codexExtra.Info().WorktreeName)
				}
				assert.Len(t, cm.ListSessions(response.Data.Worktree), boolCount(agent == "claude")+boolCount(multiple))
				assert.Len(t, xm.ListSessions(response.Data.Worktree), boolCount(agent == "codex")+boolCount(multiple))
				movedEvents, err := bus.History(events.EventFilter{Types: []string{events.EventClaudeSessionMoved, events.EventCodexSessionMoved}})
				require.NoError(t, err)
				assert.Len(t, movedEvents, len(want))
				for _, event := range movedEvents {
					assert.Equal(t, "main", event.Payload["from_worktree"])
					assert.Equal(t, response.Data.Worktree, event.Worktree)
				}

				content, err := os.ReadFile(filepath.Join(source, "keep.go"))
				require.NoError(t, err)
				assert.Equal(t, original, content)
				assert.NoFileExists(t, filepath.Join(source, "selected.sh"))
				assert.FileExists(t, filepath.Join(source, "leave.txt"))
				assert.NoFileExists(t, filepath.Join(response.Data.Path, "leave.txt"))
				for file, wantContent := range map[string]string{"keep.go": "modified\n", "selected.sh": "selected\n"} {
					content, err := os.ReadFile(filepath.Join(response.Data.Path, file))
					require.NoError(t, err)
					assert.Equal(t, wantContent, string(content))
				}
				info, err := os.Stat(filepath.Join(response.Data.Path, "selected.sh"))
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
			})
		}
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestMoveSessionAPIRejectsInvalidSelectionBeforeChanges(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			source := initRepo(t)
			cm := claude.NewManager(t.TempDir())
			xm := codex.NewManager(t.TempDir())
			t.Cleanup(cm.Shutdown)
			t.Cleanup(xm.Shutdown)
			wm := worktree.NewManager(worktree.NewRealGitExecutor(), nil, config.WorktreeConfig{}, source, t.TempDir(), "project")
			deps := moveSessionDeps{claudeMgr: cm, codexMgr: xm, worktreeMgr: wm}
			c := cm.CreateSession("main", source, "Claude")
			x := xm.CreateSession("main", source, "Codex")
			primary := moveSessionRef{Agent: agent, SessionID: c.ID()}
			if agent == "codex" {
				primary.SessionID = x.ID()
			}
			otherC := cm.CreateSession("other", source, "Other Claude")
			otherX := xm.CreateSession("other", source, "Other Codex")
			archived := cm.CreateSession("main", source, "Archived")
			require.NoError(t, cm.ArchiveSession(archived.ID()))
			trashed := xm.CreateSession("main", source, "Trashed")
			require.NoError(t, xm.TrashSession(trashed.ID()))
			require.NoError(t, os.WriteFile(filepath.Join(source, "selected.txt"), []byte("keep me"), 0o644))
			for _, tc := range []struct {
				name   string
				ref    moveSessionRef
				status int
			}{
				{"unsupported agent", moveSessionRef{"other", c.ID()}, http.StatusBadRequest},
				{"missing ID", moveSessionRef{"claude", ""}, http.StatusBadRequest},
				{"unknown Claude", moveSessionRef{"claude", "missing"}, http.StatusNotFound},
				{"unknown Codex", moveSessionRef{"codex", "missing"}, http.StatusNotFound},
				{"other Claude worktree", moveSessionRef{"claude", otherC.ID()}, http.StatusBadRequest},
				{"other Codex worktree", moveSessionRef{"codex", otherX.ID()}, http.StatusBadRequest},
				{"archived", moveSessionRef{"claude", archived.ID()}, http.StatusBadRequest},
				{"trashed", moveSessionRef{"codex", trashed.ID()}, http.StatusBadRequest},
			} {
				t.Run(tc.name, func(t *testing.T) {
					rec := moveSessionRequest(t, deps, primary, map[string]interface{}{
						"branch":              "feature/move",
						"files":               []string{"selected.txt"},
						"additional_sessions": []moveSessionRef{{"claude", c.ID()}, {"codex", x.ID()}, tc.ref},
					})
					assert.Equal(t, tc.status, rec.Code, rec.Body.String())
					worktrees, err := wm.List()
					require.NoError(t, err)
					assert.Len(t, worktrees, 1)
					assert.Equal(t, "main", c.Info().WorktreeName)
					assert.Equal(t, "main", x.Info().WorktreeName)
					assert.FileExists(t, filepath.Join(source, "selected.txt"))
				})
			}
		})
	}
}

func TestMoveSessionAPIUntrackedDirectories(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, directory := range []bool{true, false} {
			name := agent + "/directory"
			if !directory {
				name = agent + "/individual nested file"
			}
			t.Run(name, func(t *testing.T) {
				source := initRepo(t)
				require.NoError(t, os.MkdirAll(filepath.Join(source, "devtools"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(source, "devtools/README.md"), []byte("tracked"), 0o644))
				_, err := worktree.RunCommand(context.Background(), "-C", source, "add", "devtools/README.md")
				require.NoError(t, err)
				_, err = worktree.RunCommand(context.Background(), "-C", source, "commit", "-m", "Add devtools")
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(source, ".git/info/exclude"), []byte("*.cache\n"), 0o644))
				files := []string{
					"devtools/gendpa/main.go",
					"devtools/gendpa/nested/helper with spaces.go",
					"devtools/gendpa/nested/line\nname.txt",
					"devtools/gendpa/generated.cache",
					"devtools/gendpa-extra/keep.txt",
				}
				for _, file := range files {
					abs := filepath.Join(source, file)
					require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
					require.NoError(t, os.WriteFile(abs, []byte(file), 0o755))
				}
				status, err := worktree.NewRealGitExecutor().Status(context.Background(), source)
				require.NoError(t, err)
				require.Contains(t, status.Untracked, "devtools/gendpa/", "reproduce Git's collapsed directory entry")

				cm := claude.NewManager(t.TempDir())
				xm := codex.NewManager(t.TempDir())
				t.Cleanup(cm.Shutdown)
				t.Cleanup(xm.Shutdown)
				c := cm.CreateSession("main", source, "Claude")
				x := xm.CreateSession("main", source, "Codex")
				primary := moveSessionRef{"claude", c.ID()}
				extra := moveSessionRef{"codex", x.ID()}
				if agent == "codex" {
					primary, extra = extra, primary
				}
				wm := worktree.NewManager(worktree.NewRealGitExecutor(), nil, config.WorktreeConfig{}, source, t.TempDir(), "project")
				selected := []string{"devtools/gendpa/", "devtools/gendpa/nested/", files[0]}
				movedCount := 3
				if !directory {
					selected = []string{files[0]}
					movedCount = 1
				}
				rec := moveSessionRequest(t, moveSessionDeps{claudeMgr: cm, codexMgr: xm, worktreeMgr: wm}, primary, map[string]interface{}{
					"branch":              "feature/directory",
					"files":               selected,
					"additional_sessions": []moveSessionRef{extra},
				})
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				var response struct {
					Data struct {
						Path         string   `json:"path"`
						RevertErrors []string `json:"revert_errors"`
					} `json:"data"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
				assert.Empty(t, response.Data.RevertErrors, "overlapping selections must only remove each file once")
				assert.Equal(t, "feature-directory", c.Info().WorktreeName)
				assert.Equal(t, "feature-directory", x.Info().WorktreeName)
				for i, file := range files {
					if i < movedCount {
						content, err := os.ReadFile(filepath.Join(response.Data.Path, file))
						require.NoError(t, err)
						assert.Equal(t, file, string(content))
						assert.NoFileExists(t, filepath.Join(source, file))
					} else {
						content, err := os.ReadFile(filepath.Join(source, file))
						require.NoError(t, err)
						assert.Equal(t, file, string(content), "ignored and unselected files must stay in the source")
						assert.NoFileExists(t, filepath.Join(response.Data.Path, file))
					}
				}
				info, err := os.Stat(filepath.Join(response.Data.Path, files[0]))
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
				if directory {
					assert.NoDirExists(t, filepath.Join(source, "devtools/gendpa/nested"))
				}
				assert.FileExists(t, filepath.Join(source, "devtools/README.md"))
			})
		}
	}
}

func TestMoveSessionAPIDirectoryRejectsSymlinksBeforeChanges(t *testing.T) {
	source := initRepo(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("keep outside"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(source, "new-folder"), 0o755))
	selected := filepath.Join(source, "new-folder/first.txt")
	require.NoError(t, os.WriteFile(selected, []byte("keep selected"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(source, "new-folder/link.txt")))
	cm := claude.NewManager(t.TempDir())
	t.Cleanup(cm.Shutdown)
	session := cm.CreateSession("main", source, "Claude")
	wm := worktree.NewManager(worktree.NewRealGitExecutor(), nil, config.WorktreeConfig{}, source, t.TempDir(), "project")
	rec := moveSessionRequest(t, moveSessionDeps{claudeMgr: cm, worktreeMgr: wm}, moveSessionRef{"claude", session.ID()}, map[string]interface{}{
		"branch": "feature/symlink",
		"files":  []string{"new-folder/"},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "symlink")
	assert.FileExists(t, selected)
	content, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, "keep outside", string(content))
	assert.Equal(t, "main", session.Info().WorktreeName)
	worktrees, err := wm.List()
	require.NoError(t, err)
	assert.Len(t, worktrees, 1)
}
