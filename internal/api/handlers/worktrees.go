// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/worktree"
)

// WorktreeHandler handles worktree-related API requests.
type WorktreeHandler struct {
	mgr worktree.Manager
}

// WorktreeResponse wraps WorktreeInfo with an Active field for API responses.
type WorktreeResponse struct {
	Path     string `json:"Path"`
	Branch   string `json:"Branch"`
	Commit   string `json:"Commit"`
	Detached bool   `json:"Detached"`
	IsBare   bool   `json:"IsBare"`
	Dirty    bool   `json:"Dirty"`
	Ahead    int    `json:"Ahead"`
	Behind   int    `json:"Behind"`
	Active   bool   `json:"Active"`
}

// toWorktreeResponse converts a WorktreeInfo to a WorktreeResponse.
func toWorktreeResponse(wt worktree.WorktreeInfo, isActive bool) WorktreeResponse {
	return WorktreeResponse{
		Path:     wt.Path,
		Branch:   wt.Branch,
		Commit:   wt.Commit,
		Detached: wt.Detached,
		IsBare:   wt.IsBare,
		Dirty:    wt.Dirty,
		Ahead:    wt.Ahead,
		Behind:   wt.Behind,
		Active:   isActive,
	}
}

// NewWorktreeHandler creates a new worktree handler.
func NewWorktreeHandler(mgr worktree.Manager) *WorktreeHandler {
	return &WorktreeHandler{mgr: mgr}
}

// listWorktreeResponses refreshes worktree data and returns responses for all worktrees.
func (h *WorktreeHandler) listWorktreeResponses() ([]WorktreeResponse, error) {
	if err := h.mgr.Refresh(); err != nil {
		return nil, err
	}

	worktrees, err := h.mgr.List()
	if err != nil {
		return nil, err
	}

	active := h.mgr.Active()
	responses := make([]WorktreeResponse, len(worktrees))
	for i, wt := range worktrees {
		isActive := active != nil && active.Path == wt.Path
		responses[i] = toWorktreeResponse(wt, isActive)
	}
	return responses, nil
}

// List returns all worktrees.
func (h *WorktreeHandler) List(w http.ResponseWriter, r *http.Request) {
	responses, err := h.listWorktreeResponses()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrWorktreeError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"worktrees": responses,
	})
}

// Get returns a single worktree by name.
func (h *WorktreeHandler) Get(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	wt, ok := h.mgr.GetByName(name)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	active := h.mgr.Active()
	isActive := active != nil && active.Path == wt.Path

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"worktree": toWorktreeResponse(wt, isActive),
	})
}

// Activate activates a worktree.
func (h *WorktreeHandler) Activate(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	result, err := h.mgr.Activate(r.Context(), name)
	if err != nil {
		// Even on error, return partial results if available
		if result != nil {
			WriteErrorWithDetails(w, http.StatusBadRequest, ErrWorktreeError, err.Error(), map[string]interface{}{
				"worktree":     toWorktreeResponse(result.Worktree, false),
				"hook_results": result.HookResults,
				"duration":     result.Duration,
			})
			return
		}
		WriteError(w, http.StatusBadRequest, ErrWorktreeError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"worktree":     toWorktreeResponse(result.Worktree, true),
		"hook_results": result.HookResults,
		"duration":     result.Duration,
	})
}

// CreateRequest represents a request to create a worktree.
type CreateRequest struct {
	BranchName string `json:"branch_name"`
	SwitchTo   bool   `json:"switch_to"`
}

// Create creates a new worktree.
func (h *WorktreeHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid request body")
		return
	}

	if req.BranchName == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "branch_name is required")
		return
	}

	if err := h.mgr.Create(r.Context(), req.BranchName, req.SwitchTo); err != nil {
		WriteError(w, http.StatusBadRequest, ErrWorktreeError, err.Error())
		return
	}

	// Get the newly created worktree
	// Sanitize branch name the same way the manager does (replace / with -)
	sanitizedBranch := strings.ReplaceAll(req.BranchName, "/", "-")
	worktreeName := h.mgr.ProjectName() + "-" + sanitizedBranch
	wt, ok := h.mgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusInternalServerError, ErrWorktreeError, "worktree created but lookup failed")
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"worktree": toWorktreeResponse(wt, req.SwitchTo),
	})
}

// RemoveRequest represents a request to remove a worktree.
type RemoveRequest struct {
	DeleteBranch bool `json:"delete_branch"`
}

// Remove removes a worktree.
func (h *WorktreeHandler) Remove(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Parse delete_branch from query param or body
	deleteBranch := r.URL.Query().Get("delete_branch") == "1"

	// Also check request body if present
	if r.Body != nil && r.ContentLength > 0 {
		var req RemoveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			deleteBranch = req.DeleteBranch
		}
	}

	if err := h.mgr.Remove(r.Context(), name, deleteBranch); err != nil {
		WriteError(w, http.StatusBadRequest, ErrWorktreeError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"removed":        name,
		"branch_deleted": deleteBranch,
	})
}

// Info returns information about worktrees for the UI.
func (h *WorktreeHandler) Info(w http.ResponseWriter, r *http.Request) {
	responses, err := h.listWorktreeResponses()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrWorktreeError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"worktrees":    responses,
		"project_name": h.mgr.ProjectName(),
		"binaries_dir": h.mgr.BinariesPath(),
	})
}
