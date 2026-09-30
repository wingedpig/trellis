// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/wingedpig/trellis/internal/cases"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/skill"
	"github.com/wingedpig/trellis/internal/worktree"
)

// How long Start waits for the initial prompt to be accepted before replying,
// and how long a deferred Finish waits for the worktree's sessions to go idle.
const (
	taskPromptWait    = 90 * time.Second
	finishIdlePoll    = 2 * time.Second
	finishIdleTimeout = 2 * time.Hour
	agentClaude       = "claude"
	agentCodex        = "codex"
)

// TaskHandler implements the task lifecycle endpoints: Start (worktree +
// agent session + initial prompt) and Finish (merge into the default branch,
// archive sessions, remove the worktree).
type TaskHandler struct {
	worktreeMgr worktree.Manager
	claudeMgr   *claude.Manager
	codexMgr    *codex.Manager
	caseMgr     *cases.Manager
	bus         events.EventBus
}

// NewTaskHandler creates a task handler. The agent and case managers may be nil.
func NewTaskHandler(worktreeMgr worktree.Manager, claudeMgr *claude.Manager, codexMgr *codex.Manager, caseMgr *cases.Manager, bus events.EventBus) *TaskHandler {
	return &TaskHandler{worktreeMgr: worktreeMgr, claudeMgr: claudeMgr, codexMgr: codexMgr, caseMgr: caseMgr, bus: bus}
}

type taskStartRequest struct {
	Branch      string `json:"branch"`
	Agent       string `json:"agent"` // "claude" (default) or "codex"
	Prompt      string `json:"prompt"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	SessionName string `json:"session_name"`
	Activate    bool   `json:"activate"`
}

// agentSession is the slice of a Claude or Codex session Start needs.
type agentSession interface {
	ID() string
	Send(ctx context.Context, prompt string) error
}

// Start creates a worktree for a new branch, opens an agent session in it,
// and sends the initial prompt if one is given.
func (h *TaskHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req taskStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid request body")
		return
	}
	req.Branch = strings.TrimSpace(req.Branch)
	if req.Branch == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "branch is required")
		return
	}
	if req.Agent == "" {
		req.Agent = agentClaude
	}

	// Validate everything about the agent before touching git, so a typo in
	// the model doesn't leave a half-made task behind.
	switch req.Agent {
	case agentClaude:
		if h.claudeMgr == nil {
			WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "claude is not available")
			return
		}
		if !claude.IsValidModelOption(req.Model, req.Effort) {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("unknown claude model option %q (effort %q)", req.Model, req.Effort))
			return
		}
	case agentCodex:
		if h.codexMgr == nil {
			WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "codex is not available")
			return
		}
		if !codex.IsValidModelOption(req.Model, req.Effort) {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("unknown codex model option %q (effort %q)", req.Model, req.Effort))
			return
		}
	default:
		WriteError(w, http.StatusBadRequest, ErrBadRequest, fmt.Sprintf("unknown agent %q (want claude or codex)", req.Agent))
		return
	}

	// Worktree creation runs on_create hooks; don't abandon them halfway if
	// the client goes away.
	ctx := context.WithoutCancel(r.Context())
	if err := h.worktreeMgr.Create(ctx, req.Branch, req.Activate); err != nil {
		WriteError(w, http.StatusBadRequest, ErrWorktreeError, err.Error())
		return
	}
	worktreeName := h.worktreeMgr.ProjectName() + "-" + strings.ReplaceAll(req.Branch, "/", "-")
	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusInternalServerError, ErrWorktreeError, "worktree created but lookup failed")
		return
	}

	// Sessions, terminals, and cases key worktrees by the homepage URL form:
	// the directory name without the project prefix (as in session_move.go).
	// Keying by the full directory name hides the session from the worktree
	// home page and mislabels it in the picker.
	worktreeKey := strings.TrimPrefix(wt.Name(), h.worktreeMgr.ProjectName()+"-")

	var session agentSession
	var warnings []string
	switch req.Agent {
	case agentClaude:
		s := h.claudeMgr.CreateSession(worktreeKey, wt.Path, req.SessionName)
		if req.Model != "" || req.Effort != "" {
			if err := s.SetModelOption(req.Model, req.Effort); err != nil {
				warnings = append(warnings, "set model: "+err.Error())
			}
		}
		session = s
	case agentCodex:
		s := h.codexMgr.CreateSession(worktreeKey, wt.Path, req.SessionName)
		if req.Model != "" || req.Effort != "" {
			if err := s.SetModel(req.Model, req.Effort); err != nil {
				warnings = append(warnings, "set model: "+err.Error())
			}
		}
		session = s
	}

	promptSent := false
	if strings.TrimSpace(req.Prompt) != "" {
		// Send starts the agent process; the session outlives this request,
		// so it gets a background context. Wait a bounded time for it to be
		// accepted so errors can be reported.
		errCh := make(chan error, 1)
		go func() { errCh <- session.Send(context.Background(), req.Prompt) }()
		select {
		case err := <-errCh:
			if err != nil {
				warnings = append(warnings, "send prompt: "+err.Error())
			} else {
				promptSent = true
			}
		case <-time.After(taskPromptWait):
			warnings = append(warnings, "agent is still starting; the prompt will be sent when it is ready")
		}
	}

	resp := map[string]interface{}{
		"worktree":    toWorktreeResponse(wt, req.Activate),
		"agent":       req.Agent,
		"session_id":  session.ID(),
		"session_url": "/" + req.Agent + "/" + worktreeKey + "/" + session.ID(),
		"prompt_sent": promptSent,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	WriteJSON(w, http.StatusCreated, resp)
}

type taskFinishRequest struct {
	Worktree     string `json:"worktree"`
	Strategy     string `json:"strategy"` // ff (default), merge, squash
	Message      string `json:"message"`
	KeepBranch   bool   `json:"keep_branch"`
	KeepWorktree bool   `json:"keep_worktree"`
}

// finishCleanup reports what happened after the merge.
type finishCleanup struct {
	Activated        string                 `json:"activated,omitempty"`
	ArchivedSessions int                    `json:"archived_sessions"`
	Removed          bool                   `json:"removed"`
	Remove           *worktree.RemoveResult `json:"remove,omitempty"`
	Error            string                 `json:"error,omitempty"`
	PreRemoveHooks   []worktree.HookResult  `json:"pre_remove_hooks,omitempty"`
}

// Finish merges a worktree's branch into the default branch, then archives
// the worktree's agent sessions and removes it. If an agent in the worktree
// is mid-turn (typically because it ran `trellis-ctl task finish` itself),
// cleanup waits until every session there is idle.
func (h *TaskHandler) Finish(w http.ResponseWriter, r *http.Request) {
	var req taskFinishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid request body")
		return
	}
	if req.Worktree == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "worktree is required")
		return
	}
	strategy, err := worktree.ParseMergeStrategy(req.Strategy)
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
		return
	}
	wt, ok := h.worktreeMgr.GetByName(req.Worktree)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	// Trellis reinstalls its skill file into every worktree, so an untracked
	// copy is not work to lose. Live case directories are untracked too, but
	// they are work: wrap-up archives and commits them.
	ignore := []string{filepath.ToSlash(skill.RelPath)}
	if h.caseMgr != nil {
		ignore = append(ignore, h.caseMgr.CasesRelDir())
		if !req.KeepWorktree {
			if live, _ := h.caseMgr.List(wt.Path); len(live) > 0 {
				titles := make([]string, 0, len(live))
				for _, c := range live {
					titles = append(titles, fmt.Sprintf("%q", c.Title))
				}
				WriteError(w, http.StatusConflict, ErrConflict, fmt.Sprintf(
					"worktree %s has a case that is not wrapped up (%s); wrap it up first or it would be deleted with the worktree (or finish with keep_worktree)",
					wt.Name(), strings.Join(titles, ", ")))
				return
			}
		}
	}

	// Hooks and git can take a while; finish even if the client disconnects.
	ctx := context.WithoutCancel(r.Context())
	result, err := h.worktreeMgr.Merge(ctx, wt.Name(), worktree.MergeOptions{
		Strategy:    strategy,
		Message:     req.Message,
		IgnorePaths: ignore,
	})
	if err != nil {
		var me *worktree.MergeError
		if errors.As(err, &me) {
			details := map[string]interface{}{}
			if len(me.Hooks) > 0 {
				details["hook_results"] = me.Hooks
			}
			if len(me.Conflicts) > 0 {
				details["conflicts"] = me.Conflicts
			}
			WriteErrorWithDetails(w, http.StatusConflict, ErrWorktreeError, me.Msg, details)
			return
		}
		WriteError(w, http.StatusBadRequest, ErrWorktreeError, err.Error())
		return
	}

	resp := map[string]interface{}{"merge": result}
	if req.KeepWorktree {
		resp["kept_worktree"] = true
		WriteJSON(w, http.StatusOK, resp)
		return
	}

	opts := worktree.RemoveOptions{
		DeleteBranch: !req.KeepBranch,
		// A squashed branch never looks merged to git; it was just verified
		// by the squash commit, so force-delete it. Otherwise let git check.
		SafeBranchDelete: strategy != worktree.MergeSquash,
	}
	if h.worktreeBusy(wt.Path) {
		resp["cleanup_deferred"] = true
		go h.cleanupWhenIdle(wt, result.BaseWorktree, opts)
		WriteJSON(w, http.StatusOK, resp)
		return
	}
	resp["cleanup"] = h.cleanup(ctx, wt, result.BaseWorktree, opts)
	WriteJSON(w, http.StatusOK, resp)
}

// cleanupWhenIdle waits for the worktree's sessions to finish their turns,
// then cleans up.
func (h *TaskHandler) cleanupWhenIdle(wt worktree.WorktreeInfo, baseWorktree string, opts worktree.RemoveOptions) {
	deadline := time.Now().Add(finishIdleTimeout)
	for h.worktreeBusy(wt.Path) {
		if time.Now().After(deadline) {
			h.publishFinishFailure(wt, "timed out waiting for agent sessions to go idle; worktree left in place")
			return
		}
		time.Sleep(finishIdlePoll)
	}
	if _, ok := h.worktreeMgr.GetByPath(wt.Path); !ok {
		return // removed by someone else meanwhile
	}
	c := h.cleanup(context.Background(), wt, baseWorktree, opts)
	if c.Error != "" {
		h.publishFinishFailure(wt, c.Error)
	}
}

// cleanup switches away from the worktree if it is active, archives its
// agent sessions, and removes it.
func (h *TaskHandler) cleanup(ctx context.Context, wt worktree.WorktreeInfo, baseWorktree string, opts worktree.RemoveOptions) *finishCleanup {
	c := &finishCleanup{}
	if active := h.worktreeMgr.Active(); active != nil && active.Path == wt.Path {
		if _, err := h.worktreeMgr.Activate(ctx, baseWorktree); err != nil {
			c.Error = fmt.Sprintf("activating %s before removal failed: %v; worktree left in place", baseWorktree, err)
			return c
		}
		c.Activated = baseWorktree
	}

	// Archive rather than let the worktree-deleted subscriber trash them:
	// archived sessions stay searchable after the worktree is gone.
	for _, id := range h.sessionIDs(wt.Path, agentClaude) {
		if err := h.claudeMgr.ArchiveSession(id); err == nil {
			c.ArchivedSessions++
		}
	}
	for _, id := range h.sessionIDs(wt.Path, agentCodex) {
		if err := h.codexMgr.ArchiveSession(id); err == nil {
			c.ArchivedSessions++
		}
	}

	res, err := h.worktreeMgr.RemoveWithOptions(ctx, wt.Name(), opts)
	if err != nil {
		var re *worktree.RemoveError
		if errors.As(err, &re) {
			c.PreRemoveHooks = re.Hooks
		}
		c.Error = err.Error()
		return c
	}
	c.Removed = true
	c.Remove = res

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:     events.EventWorktreeFinished,
			Worktree: wt.Name(),
			Payload: map[string]interface{}{
				"name":           wt.Name(),
				"branch":         wt.Branch,
				"branch_deleted": res.BranchDeleted,
			},
		})
	}
	return c
}

func (h *TaskHandler) publishFinishFailure(wt worktree.WorktreeInfo, msg string) {
	log.Printf("task finish %s: %s", wt.Name(), msg)
	if h.bus != nil {
		h.bus.Publish(context.Background(), events.Event{
			Type:     events.EventWorktreeFinishFailed,
			Worktree: wt.Name(),
			Payload:  map[string]interface{}{"name": wt.Name(), "error": msg},
		})
	}
}

// worktreeBusy reports whether any agent session in the worktree is mid-turn.
func (h *TaskHandler) worktreeBusy(path string) bool {
	for _, id := range h.sessionIDs(path, agentClaude) {
		if s := h.claudeMgr.GetSession(id); s != nil && s.IsGenerating() {
			return true
		}
	}
	for _, id := range h.sessionIDs(path, agentCodex) {
		if s := h.codexMgr.GetSession(id); s != nil && s.IsGenerating() {
			return true
		}
	}
	return false
}

// sessionIDs lists the active sessions of one agent in the worktree at path.
// Sessions record whichever worktree alias they were created under, so match
// on the resolved path rather than the name.
func (h *TaskHandler) sessionIDs(path, agent string) []string {
	var ids []string
	match := func(id, worktreeName string) {
		if wt, ok := h.worktreeMgr.GetByName(worktreeName); ok && wt.Path == path {
			ids = append(ids, id)
		}
	}
	switch {
	case agent == agentClaude && h.claudeMgr != nil:
		for _, info := range h.claudeMgr.AllSessions() {
			match(info.ID, info.WorktreeName)
		}
	case agent == agentCodex && h.codexMgr != nil:
		for _, info := range h.codexMgr.AllSessions() {
			match(info.ID, info.WorktreeName)
		}
	}
	return ids
}
