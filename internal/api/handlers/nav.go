// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"
	"strings"

	"github.com/wingedpig/trellis/internal/logs"
	"github.com/wingedpig/trellis/internal/service"
	"github.com/wingedpig/trellis/internal/terminal"
)

// NavHandler handles navigation API requests.
type NavHandler struct {
	terminalMgr   terminal.Manager
	serviceMgr    service.Manager
	logMgr        *logs.Manager
	links         []LinkConfig
	shortcuts     []ShortcutConfig
	notifications NotificationConfig
	projectName   string
}

// NewNavHandler creates a new navigation handler.
func NewNavHandler(terminalMgr terminal.Manager, serviceMgr service.Manager, logMgr *logs.Manager, links []LinkConfig, shortcuts []ShortcutConfig, notifications NotificationConfig, projectName string) *NavHandler {
	return &NavHandler{
		terminalMgr:   terminalMgr,
		serviceMgr:    serviceMgr,
		logMgr:        logMgr,
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

// NavOptionsResponse is the response for the nav/options endpoint.
type NavOptionsResponse struct {
	Terminals     []NavTerminal    `json:"terminals"`
	Services      []NavService     `json:"services"`
	Links         []NavLink        `json:"links"`
	LogViewers    []NavLogViewer   `json:"logViewers"`
	Shortcuts     []NavShortcut    `json:"shortcuts"`
	Notifications NavNotifications `json:"notifications"`
}

// Options returns all navigation options.
func (h *NavHandler) Options(w http.ResponseWriter, r *http.Request) {
	resp := NavOptionsResponse{
		Terminals:  []NavTerminal{},
		Services:   []NavService{},
		Links:      []NavLink{},
		LogViewers: []NavLogViewer{},
		Shortcuts:  []NavShortcut{},
		Notifications: NavNotifications{
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

	// Get log viewers
	if h.logMgr != nil {
		for _, name := range h.logMgr.List() {
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

	WriteJSON(w, http.StatusOK, resp)
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
