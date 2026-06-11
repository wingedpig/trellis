// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/wingedpig/trellis/internal/usage"
	"github.com/wingedpig/trellis/internal/worktree"
)

// UsageHandler serves Claude Code token usage and cost reports.
type UsageHandler struct {
	mgr         *usage.Manager
	worktreeMgr worktree.Manager
	projectName string
}

// NewUsageHandler creates a usage handler.
func NewUsageHandler(mgr *usage.Manager, worktreeMgr worktree.Manager, projectName string) *UsageHandler {
	return &UsageHandler{mgr: mgr, worktreeMgr: worktreeMgr, projectName: projectName}
}

// worktreePaths maps absolute worktree paths to their display names, using
// the same project-prefix-stripped naming the rest of the UI uses.
func (h *UsageHandler) worktreePaths() map[string]string {
	paths := make(map[string]string)
	if h.worktreeMgr == nil {
		return paths
	}
	wts, err := h.worktreeMgr.List()
	if err != nil {
		return paths
	}
	for _, wt := range wts {
		name := wt.Name()
		if h.projectName != "" {
			if name == h.projectName {
				name = "main"
			} else if strings.HasPrefix(name, h.projectName+"-") {
				name = name[len(h.projectName)+1:]
			}
		}
		paths[wt.Path] = name
	}
	return paths
}

// Summary returns the aggregate usage report.
// GET /api/v1/usage/summary?days=30
func (h *UsageHandler) Summary(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 365 {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "days must be between 1 and 365")
			return
		}
		days = n
	}
	WriteJSON(w, http.StatusOK, h.mgr.Report(days, h.worktreePaths()))
}

// Today returns today's totals across all projects (for the header badge).
// GET /api/v1/usage/today
func (h *UsageHandler) Today(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, h.mgr.Today())
}
