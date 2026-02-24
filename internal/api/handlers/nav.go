// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"log"
	"net/http"
	"strings"

	"github.com/wingedpig/trellis/internal/cases"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/logs"
	"github.com/wingedpig/trellis/internal/service"
	"github.com/wingedpig/trellis/internal/terminal"
	"github.com/wingedpig/trellis/internal/worktree"
)

// NavHandler handles navigation API requests.
type NavHandler struct {
	terminalMgr   terminal.Manager
	serviceMgr    service.Manager
	logMgr        *logs.Manager
	claudeMgr     *claude.Manager
	caseMgr       *cases.Manager
	worktreeMgr   worktree.Manager
	links         []LinkConfig
	shortcuts     []ShortcutConfig
	notifications NotificationConfig
	projectName   string
}

// NewNavHandler creates a new navigation handler.
func NewNavHandler(terminalMgr terminal.Manager, serviceMgr service.Manager, logMgr *logs.Manager, claudeMgr *claude.Manager, caseMgr *cases.Manager, worktreeMgr worktree.Manager, links []LinkConfig, shortcuts []ShortcutConfig, notifications NotificationConfig, projectName string) *NavHandler {
	return &NavHandler{
		terminalMgr:   terminalMgr,
		serviceMgr:    serviceMgr,
		logMgr:        logMgr,
		claudeMgr:     claudeMgr,
		caseMgr:       caseMgr,
		worktreeMgr:   worktreeMgr,
		links:         links,
		shortcuts:     shortcuts,
		notifications: notifications,
		projectName:   projectName,
	}
}

// NavTerminal represents a terminal option in navigation.
type NavTerminal struct {
	URL      string `json:"url"`
	Display  string `json:"display"`
	IsRemote bool   `json:"isRemote"`
}

// NavService represents a service option in navigation.
type NavService struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// NavLink represents a link option in navigation.
type NavLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// NavLogViewer represents a log viewer option in navigation.
type NavLogViewer struct {
	Name string `json:"name"`
}

// NavShortcut represents a keyboard shortcut in navigation.
type NavShortcut struct {
	Key    string `json:"key"`
	Window string `json:"window"`
}

// NavNotifications represents notification settings in navigation.
type NavNotifications struct {
	Enabled      bool     `json:"enabled"`
	Events       []string `json:"events"`
	FailuresOnly bool     `json:"failures_only"`
	Sound        bool     `json:"sound"`
}

// NavCase represents a case option in navigation.
type NavCase struct {
	URL       string `json:"url"`
	Display   string `json:"display"`
	Worktree  string `json:"worktree"`
	CaseID    string `json:"caseId"`
	Kind      string `json:"kind"`
}

// NavClaudeSession represents a Claude session option in navigation.
type NavClaudeSession struct {
	URL       string `json:"url"`
	Display   string `json:"display"`
	Worktree  string `json:"worktree"`
	SessionID string `json:"sessionId"`
}

// NavWorktree represents a worktree option in navigation.
type NavWorktree struct {
	URL     string `json:"url"`
	Display string `json:"display"`
	Name    string `json:"name"`
}

// NavOptionsResponse is the response for the nav/options endpoint.
type NavOptionsResponse struct {
	Terminals      []NavTerminal      `json:"terminals"`
	Services       []NavService       `json:"services"`
	Links          []NavLink          `json:"links"`
	LogViewers     []NavLogViewer     `json:"logViewers"`
	Shortcuts      []NavShortcut      `json:"shortcuts"`
	Notifications  NavNotifications   `json:"notifications"`
	ClaudeSessions []NavClaudeSession `json:"claudeSessions"`
	Cases          []NavCase          `json:"cases"`
	Worktrees      []NavWorktree      `json:"worktrees"`
}

// Options returns all navigation options.
func (h *NavHandler) Options(w http.ResponseWriter, r *http.Request) {
	resp := NavOptionsResponse{
		Terminals:      []NavTerminal{},
		Services:       []NavService{},
		Links:          []NavLink{},
		LogViewers:     []NavLogViewer{},
		Shortcuts:      []NavShortcut{},
		ClaudeSessions: []NavClaudeSession{},
		Cases:          []NavCase{},
		Worktrees:      []NavWorktree{},
		Notifications:  NavNotifications{
			Enabled:      h.notifications.Enabled,
			Events:       h.notifications.Events,
			FailuresOnly: h.notifications.FailuresOnly,
			Sound:        h.notifications.Sound,
		},
	}

	// Get terminals
	if h.terminalMgr != nil {
		sessions, err := h.terminalMgr.ListSessions(r.Context())
		if err == nil {
			for _, sess := range sessions {
				isRemote := sess.IsRemote
				displayName := sess.Name
				if !isRemote {
					displayName = h.sessionToDisplayName(sess.Name)
				}

				for _, win := range sess.Windows {
					var url, display string
					if isRemote {
						url = "/terminal/remote/" + win.Name
						display = "!" + displayName
					} else {
						worktree := h.sessionToWorktree(sess.Name)
						url = "/terminal/local/" + worktree + "/" + win.Name
						display = "@" + displayName + " - " + win.Name
					}
					resp.Terminals = append(resp.Terminals, NavTerminal{
						URL:      url,
						Display:  display,
						IsRemote: isRemote,
					})
				}
			}
		}
	}

	// Get services
	if h.serviceMgr != nil {
		for _, svc := range h.serviceMgr.List() {
			resp.Services = append(resp.Services, NavService{
				Name:   svc.Name,
				Status: svc.Status.State.String(),
			})
		}
	}

	// Get links
	for _, link := range h.links {
		resp.Links = append(resp.Links, NavLink{
			Name: link.Name,
			URL:  link.URL,
		})
	}

	// Get log viewers (exclude internal svc: viewers used by trace system)
	if h.logMgr != nil {
		for _, name := range h.logMgr.List() {
			if strings.HasPrefix(name, "svc:") {
				continue
			}
			resp.LogViewers = append(resp.LogViewers, NavLogViewer{
				Name: name,
			})
		}
	}

	// Get shortcuts
	for _, sc := range h.shortcuts {
		resp.Shortcuts = append(resp.Shortcuts, NavShortcut{
			Key:    sc.Key,
			Window: sc.Window,
		})
	}

	// Get worktrees
	if h.worktreeMgr != nil {
		wts, err := h.worktreeMgr.List()
		if err != nil {
			log.Printf("Nav: failed to list worktrees: %v", err)
		}
		log.Printf("Nav: found %d worktrees (projectName=%q)", len(wts), h.projectName)
		for _, wt := range wts {
			name := h.worktreeToDisplayName(wt)
			resp.Worktrees = append(resp.Worktrees, NavWorktree{
				URL:     "/worktree/" + name,
				Display: name,
				Name:    name,
			})
		}
	}

	// Get Claude sessions
	if h.claudeMgr != nil {
		sessions := h.claudeMgr.AllSessions()
		for _, sess := range sessions {
			resp.ClaudeSessions = append(resp.ClaudeSessions, NavClaudeSession{
				URL:       "/claude/" + sess.WorktreeName + "/" + sess.ID,
				Display:   "@" + sess.WorktreeName + " - " + sess.DisplayName,
				Worktree:  sess.WorktreeName,
				SessionID: sess.ID,
			})
		}
	}

	// Get cases across all worktrees
	if h.caseMgr != nil && h.worktreeMgr != nil {
		wts, err := h.worktreeMgr.List()
		if err == nil {
			for _, wt := range wts {
				wtName := h.worktreeToDisplayName(wt)
				caseList, err := h.caseMgr.List(wt.Path)
				if err != nil {
					continue
				}
				for _, c := range caseList {
					resp.Cases = append(resp.Cases, NavCase{
						URL:      "/case/" + wtName + "/" + c.ID,
						Display:  "@" + wtName + " - " + c.Title,
						Worktree: wtName,
						CaseID:   c.ID,
						Kind:     c.Kind,
					})
				}
			}
		}
	}

	WriteJSON(w, http.StatusOK, resp)
}

// worktreeToDisplayName converts a WorktreeInfo to a display name for navigation.
// Returns "main" for the main worktree, otherwise the branch/suffix portion.
func (h *NavHandler) worktreeToDisplayName(wt worktree.WorktreeInfo) string {
	dirName := wt.Name()
	if h.projectName != "" && dirName == h.projectName {
		return "main"
	}
	// Also check with dots converted to underscores (tmux format)
	if h.projectName != "" {
		projectTmux := strings.ReplaceAll(h.projectName, ".", "_")
		worktreeTmux := strings.ReplaceAll(dirName, ".", "_")
		if worktreeTmux == projectTmux {
			return "main"
		}
	}
	// Strip project prefix
	if h.projectName != "" && strings.HasPrefix(dirName, h.projectName+"-") {
		return dirName[len(h.projectName)+1:]
	}
	return dirName
}

// sessionToDisplayName converts a tmux session name to a display name.
func (h *NavHandler) sessionToDisplayName(sessionName string) string {
	if h.projectName == "" {
		return sessionName
	}
	// Convert project name to tmux format (. → _)
	projectPrefix := strings.ReplaceAll(h.projectName, ".", "_")
	if sessionName == projectPrefix {
		return "main"
	}
	// Strip project prefix and hyphen
	if strings.HasPrefix(sessionName, projectPrefix+"-") {
		return sessionName[len(projectPrefix)+1:]
	}
	return sessionName
}

// sessionToWorktree converts a tmux session name to a worktree name.
func (h *NavHandler) sessionToWorktree(sessionName string) string {
	if h.projectName == "" {
		return sessionName
	}
	// Convert project name to tmux format (. → _)
	projectPrefix := strings.ReplaceAll(h.projectName, ".", "_")
	if sessionName == projectPrefix {
		return "main"
	}
	// Strip project prefix and hyphen
	if strings.HasPrefix(sessionName, projectPrefix+"-") {
		return sessionName[len(projectPrefix)+1:]
	}
	return sessionName
}
