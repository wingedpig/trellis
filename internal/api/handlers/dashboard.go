// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"

	"github.com/wingedpig/trellis/internal/service"
	"github.com/wingedpig/trellis/internal/worktree"
	"github.com/wingedpig/trellis/views"
)

// DashboardHandler handles dashboard requests.
type DashboardHandler struct {
	services  service.Manager
	worktrees worktree.Manager
}

// NewDashboardHandler creates a new dashboard handler.
func NewDashboardHandler(services service.Manager, worktrees worktree.Manager) *DashboardHandler {
	return &DashboardHandler{
		services:  services,
		worktrees: worktrees,
	}
}

// Index renders the dashboard.
func (h *DashboardHandler) Index(w http.ResponseWriter, r *http.Request) {
	var activeWorktree *worktree.WorktreeInfo
	var worktrees []worktree.WorktreeInfo
	if h.worktrees != nil {
		activeWorktree = h.worktrees.Active()
		worktrees, _ = h.worktrees.List()
	}

	page := &views.DashboardPage{
		BasePage: views.BasePage{
			Title:    "Status",
			Worktree: activeWorktree,
		},
		Services:  h.services.List(),
		Worktrees: worktrees,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}
