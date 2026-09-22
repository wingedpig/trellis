// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/worktree"
)

type moveSessionRef struct {
	Agent     string `json:"agent"`
	SessionID string `json:"session_id"`
}

type moveSessionDeps struct {
	claudeMgr   *claude.Manager
	codexMgr    *codex.Manager
	worktreeMgr worktree.Manager
	bus         events.EventBus
}

type sessionMove struct {
	ref          moveSessionRef
	worktreeName string
	move         func(sessionID, worktreeName, workDir string) error
}

func (h moveSessionDeps) resolveSession(ref moveSessionRef) (sessionMove, int, error) {
	var target sessionMove
	target.ref = ref
	if ref.SessionID == "" {
		return target, http.StatusBadRequest, fmt.Errorf("session_id is required")
	}
	active := false
	switch ref.Agent {
	case "claude":
		if h.claudeMgr != nil {
			if s := h.claudeMgr.GetSession(ref.SessionID); s != nil {
				info := s.Info()
				target.worktreeName = info.WorktreeName
				target.move = h.claudeMgr.MoveSession
				active = info.TrashedAt == nil && info.ArchivedAt == nil
			}
		}
	case "codex":
		if h.codexMgr != nil {
			if s := h.codexMgr.GetSession(ref.SessionID); s != nil {
				info := s.Info()
				target.worktreeName = info.WorktreeName
				target.move = h.codexMgr.MoveSession
				active = info.TrashedAt == nil && info.ArchivedAt == nil
			}
		}
	default:
		return target, http.StatusBadRequest, fmt.Errorf("agent must be 'claude' or 'codex'")
	}
	if target.move == nil {
		return target, http.StatusNotFound, fmt.Errorf("%s session %q not found", ref.Agent, ref.SessionID)
	}
	if !active {
		return target, http.StatusBadRequest, fmt.Errorf("%s session %q is archived or trashed", ref.Agent, ref.SessionID)
	}
	return target, 0, nil
}

// moveSessionsToWorktree creates one worktree for the primary session and any
// additional sessions, moving the selected uncommitted files only once.
func (h moveSessionDeps) moveSessionsToWorktree(w http.ResponseWriter, r *http.Request, agent string) {
	vars := mux.Vars(r)
	sessionID := vars["session"]

	var body struct {
		Branch             string           `json:"branch"`
		Files              []string         `json:"files"`
		AdditionalSessions []moveSessionRef `json:"additional_sessions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Branch == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "branch is required")
		return
	}

	// Validate the entire selection before creating a worktree or touching files.
	refs := append([]moveSessionRef{{Agent: agent, SessionID: sessionID}}, body.AdditionalSessions...)
	seen := make(map[moveSessionRef]bool)
	var sessions []sessionMove
	var sourceWorktreeName string
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		session, status, err := h.resolveSession(ref)
		if err != nil {
			code := ErrBadRequest
			if status == http.StatusNotFound {
				code = ErrNotFound
			}
			WriteError(w, status, code, err.Error())
			return
		}
		if len(sessions) == 0 {
			sourceWorktreeName = session.worktreeName
		} else if session.worktreeName != sourceWorktreeName {
			WriteError(w, http.StatusBadRequest, ErrBadRequest,
				fmt.Sprintf("%s session %q is not in the source worktree", ref.Agent, ref.SessionID))
			return
		}
		sessions = append(sessions, session)
	}
	sourceWT, ok := h.worktreeMgr.GetByName(sourceWorktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "source worktree not found")
		return
	}

	// Git status collapses wholly untracked directories into entries such as
	// "devtools/gendpa/". Get the individual paths, excluding ignored files, both
	// to expand those selections and to classify files inside untracked folders.
	ctx := r.Context()
	untrackedOutput, err := worktree.RunCommand(ctx, "-C", sourceWT.Path, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "list untracked files: "+err.Error())
		return
	}
	untrackedFiles := strings.Split(strings.TrimSuffix(untrackedOutput, "\x00"), "\x00")
	untrackedSet := make(map[string]bool, len(untrackedFiles))
	for _, path := range untrackedFiles {
		if path != "" {
			untrackedSet[path] = true
		}
	}

	// Validate every file before creating the worktree or changing the source.
	type selectedFile struct {
		rel       string // forward-slash relative path (git-style)
		srcAbs    string
		untracked bool
	}
	selected := make([]selectedFile, 0, len(body.Files))
	selectedPaths := make(map[string]bool)
	addFile := func(path string) error {
		abs, err := safeJoinInsideRoot(sourceWT.Path, path)
		if err != nil {
			return fmt.Errorf("invalid file %q: %w", path, err)
		}
		info, err := os.Lstat(abs)
		if err != nil {
			return fmt.Errorf("file %q not found", path)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("file %q is a symlink (unsupported)", path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("file %q is not a regular file (unsupported)", path)
		}
		rel := filepath.ToSlash(filepath.Clean(path))
		if !selectedPaths[rel] {
			selectedPaths[rel] = true
			selected = append(selected, selectedFile{rel: rel, srcAbs: abs, untracked: untrackedSet[rel]})
		}
		return nil
	}
	for _, f := range body.Files {
		if f == "" {
			continue
		}
		abs, err := safeJoinInsideRoot(sourceWT.Path, f)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("invalid file %q: %s", f, err.Error()))
			return
		}
		info, err := os.Lstat(abs)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("file %q not found", f))
			return
		}
		if info.Mode()&os.ModeSymlink != 0 {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("file %q is a symlink (unsupported)", f))
			return
		}
		if info.IsDir() {
			prefix := filepath.ToSlash(filepath.Clean(f)) + "/"
			found := false
			for _, path := range untrackedFiles {
				if !strings.HasPrefix(path, prefix) {
					continue
				}
				found = true
				if err := addFile(path); err != nil {
					WriteError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
					return
				}
			}
			if !found {
				WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("directory %q has no untracked files to move", f))
				return
			}
			continue
		}
		if err := addFile(f); err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
			return
		}
	}

	// Create the new worktree.
	if err := h.worktreeMgr.Create(ctx, body.Branch, false); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "create worktree: "+err.Error())
		return
	}
	newWorktreeDir := h.worktreeMgr.ProjectName() + "-" + strings.ReplaceAll(body.Branch, "/", "-")
	newWT, ok := h.worktreeMgr.GetByName(newWorktreeDir)
	if !ok {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "new worktree not found after create")
		return
	}
	// The rest of the app (claude sessions, terminals, cases) keys worktrees
	// by whatever string the homepage uses in its URL — the project-prefix-
	// stripped form. Key the moved session the same way so it shows up on the
	// new worktree's home page.
	newWorktreeKey := strings.TrimPrefix(newWT.Name(), h.worktreeMgr.ProjectName()+"-")

	// Copy selected files into the new worktree, preserving relative paths and mode.
	for _, sf := range selected {
		dstAbs := filepath.Join(newWT.Path, filepath.FromSlash(sf.rel))
		if err := copyFilePreserveMode(sf.srcAbs, dstAbs); err != nil {
			WriteError(w, http.StatusInternalServerError, ErrInternalError,
				fmt.Sprintf("copy %q: %s", sf.rel, err.Error()))
			return
		}
	}

	// Revert each selected file in the source: tracked → git checkout --; untracked → remove.
	var revertErrs []string
	for _, sf := range selected {
		if sf.untracked {
			if err := os.Remove(sf.srcAbs); err != nil {
				revertErrs = append(revertErrs, fmt.Sprintf("%s: %s", sf.rel, err.Error()))
			} else {
				// Remove only empty parent directories. Ignored or unselected
				// files keep their containing directories in the source.
				for dir := filepath.Dir(sf.srcAbs); dir != filepath.Clean(sourceWT.Path); dir = filepath.Dir(dir) {
					if err := os.Remove(dir); err != nil {
						break
					}
				}
			}
			continue
		}
		if out, err := worktree.RunCommand(ctx, "-C", sourceWT.Path, "checkout", "--", sf.rel); err != nil {
			revertErrs = append(revertErrs, fmt.Sprintf("%s: %s %s", sf.rel, err.Error(), strings.TrimSpace(out)))
		}
	}

	movedFiles := make([]string, len(selected))
	for i, sf := range selected {
		movedFiles[i] = sf.rel
	}

	movedSessions := make([]moveSessionRef, 0, len(sessions))
	for _, session := range sessions {
		if err := session.move(session.ref.SessionID, newWorktreeKey, newWT.Path); err != nil {
			WriteError(w, http.StatusInternalServerError, ErrInternalError,
				fmt.Sprintf("move %s session %q to worktree %q: %s", session.ref.Agent, session.ref.SessionID, newWorktreeKey, err))
			return
		}
		movedSessions = append(movedSessions, session.ref)

		// Each session emits its usual event so all affected views refresh.
		if h.bus != nil {
			eventType := events.EventClaudeSessionMoved
			if session.ref.Agent == "codex" {
				eventType = events.EventCodexSessionMoved
			}
			h.bus.Publish(ctx, events.Event{
				Type:     eventType,
				Worktree: newWorktreeKey,
				Payload: map[string]interface{}{
					"session_id":       session.ref.SessionID,
					"from_worktree":    sourceWorktreeName,
					"to_worktree":      newWorktreeKey,
					"to_worktree_path": newWT.Path,
					"to_branch":        newWT.Branch,
					"files":            movedFiles,
					"revert_errors":    revertErrs,
				},
			})
		}
	}

	resp := map[string]interface{}{
		"session_id":    sessionID,
		"sessions":      movedSessions,
		"worktree":      newWorktreeKey,
		"branch":        newWT.Branch,
		"path":          newWT.Path,
		"revert_errors": revertErrs,
	}
	WriteJSON(w, http.StatusOK, resp)
}
