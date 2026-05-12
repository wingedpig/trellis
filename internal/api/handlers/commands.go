// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
	"github.com/wingedpig/trellis/internal/crashes"
	"github.com/wingedpig/trellis/internal/service"
	"github.com/wingedpig/trellis/internal/workflow"
	"github.com/wingedpig/trellis/internal/worktree"
)

// CommandsHandler handles the command palette API.
// The palette is verbs only (Shift+Cmd+P, VS Code style); navigation
// destinations live in the Cmd+P picker (#navSelect).
type CommandsHandler struct {
	worktreeMgr    worktree.Manager
	serviceMgr     service.Manager
	workflowRunner workflow.Runner
	crashMgr       *crashes.Manager
	claudeMgr      *claude.Manager
	codexMgr       *codex.Manager
	projectName    string
}

// NewCommandsHandler creates a new commands handler.
func NewCommandsHandler(worktreeMgr worktree.Manager, serviceMgr service.Manager, workflowRunner workflow.Runner, crashMgr *crashes.Manager, claudeMgr *claude.Manager, codexMgr *codex.Manager) *CommandsHandler {
	var pn string
	if worktreeMgr != nil {
		pn = worktreeMgr.ProjectName()
	}
	return &CommandsHandler{
		worktreeMgr:    worktreeMgr,
		serviceMgr:     serviceMgr,
		workflowRunner: workflowRunner,
		crashMgr:       crashMgr,
		claudeMgr:      claudeMgr,
		codexMgr:       codexMgr,
		projectName:    pn,
	}
}

// Command is a single entry in the command palette. Title is in
// "Category: verb" form (VS Code style) so alphabetical sort groups
// related commands and prefix-typing narrows by category.
type Command struct {
	ID      string        `json:"id"`
	Title   string        `json:"title"`
	Confirm string        `json:"confirm,omitempty"`
	Action  CommandAction `json:"action"`
}

// CommandAction describes what the client does when a command is picked.
// Type is one of:
//   - "navigate": window.location = URL
//   - "api":      fetch(URL, {method: Method}); on success, dispatch Then
//   - "client":   call a registered client-side handler named by Name
//
// Then chains a follow-up action; the client substitutes {data.Field}
// tokens in Then.URL from the api response body.
type CommandAction struct {
	Type   string         `json:"type"`
	URL    string         `json:"url,omitempty"`
	Method string         `json:"method,omitempty"`
	Name   string         `json:"name,omitempty"`
	Then   *CommandAction `json:"then,omitempty"`
}

// List returns the full set of commands for the current context.
// Query param "worktree" scopes workflow-run commands to that worktree.
func (h *CommandsHandler) List(w http.ResponseWriter, r *http.Request) {
	currentWorktree := r.URL.Query().Get("worktree")
	if currentWorktree == "" {
		currentWorktree = "main"
	}

	cmds := make([]Command, 0, 64)

	// Worktree verbs
	if h.worktreeMgr != nil {
		cmds = append(cmds, Command{
			ID: "wt.create", Title: "Worktree: Create new…",
			Action: CommandAction{Type: "navigate", URL: "/worktrees"},
		})
		wts, err := h.worktreeMgr.List()
		if err != nil {
			log.Printf("Commands: failed to list worktrees: %v", err)
		}
		names := make([]string, 0, len(wts))
		for _, wt := range wts {
			names = append(names, h.worktreeName(wt))
		}
		sort.Strings(names)
		for _, name := range names {
			esc := url.PathEscape(name)
			cmds = append(cmds,
				Command{ID: "wt.activate." + name, Title: "Worktree: Activate " + name,
					Action: CommandAction{Type: "api", Method: "POST", URL: "/api/v1/worktrees/" + esc + "/activate"}},
				Command{ID: "wt.remove." + name, Title: "Worktree: Remove " + name,
					Confirm: "Remove worktree " + name + "?",
					Action:  CommandAction{Type: "api", Method: "DELETE", URL: "/api/v1/worktrees/" + esc}},
			)
		}
	}

	// Service verbs
	if h.serviceMgr != nil {
		cmds = append(cmds,
			Command{ID: "svc.start-all", Title: "Service: Start all",
				Action: CommandAction{Type: "api", Method: "POST", URL: "/api/v1/services/start-all"}},
			Command{ID: "svc.stop-all", Title: "Service: Stop all",
				Confirm: "Stop all services?",
				Action:  CommandAction{Type: "api", Method: "POST", URL: "/api/v1/services/stop-all"}},
		)
		svcs := h.serviceMgr.List()
		sort.Slice(svcs, func(i, j int) bool { return svcs[i].Name < svcs[j].Name })
		for _, svc := range svcs {
			esc := url.PathEscape(svc.Name)
			cmds = append(cmds,
				Command{ID: "svc.start." + svc.Name, Title: "Service: Start " + svc.Name,
					Action: CommandAction{Type: "api", Method: "POST", URL: "/api/v1/services/" + esc + "/start"}},
				Command{ID: "svc.stop." + svc.Name, Title: "Service: Stop " + svc.Name,
					Action: CommandAction{Type: "api", Method: "POST", URL: "/api/v1/services/" + esc + "/stop"}},
				Command{ID: "svc.restart." + svc.Name, Title: "Service: Restart " + svc.Name,
					Action: CommandAction{Type: "api", Method: "POST", URL: "/api/v1/services/" + esc + "/restart"}},
				Command{ID: "svc.clear-logs." + svc.Name, Title: "Service: Clear logs for " + svc.Name,
					Confirm: "Clear logs for " + svc.Name + "?",
					Action:  CommandAction{Type: "api", Method: "DELETE", URL: "/api/v1/services/" + esc + "/logs"}},
			)
		}
	}

	// Workflow verbs
	if h.workflowRunner != nil {
		wfs := h.workflowRunner.List()
		sort.Slice(wfs, func(i, j int) bool { return wfs[i].Name < wfs[j].Name })
		wtEsc := url.QueryEscape(currentWorktree)
		for _, wf := range wfs {
			if strings.HasPrefix(wf.ID, "_") {
				continue
			}
			idEsc := url.PathEscape(wf.ID)
			cmd := Command{
				ID:    "wf.run." + wf.ID,
				Title: "Workflow: Run " + wf.Name,
				Action: CommandAction{
					Type:   "api",
					Method: "POST",
					URL:    "/api/v1/workflows/" + idEsc + "/run?worktree=" + wtEsc,
					Then: &CommandAction{
						Type: "navigate",
						URL:  "/terminal/output/" + url.PathEscape(currentWorktree) + "?run={data.ID}&workflow=" + url.QueryEscape(wf.ID),
					},
				},
			}
			if wf.Confirm {
				msg := wf.ConfirmMessage
				if msg == "" {
					msg = "Are you sure you want to run this workflow?"
				}
				cmd.Confirm = msg
			}
			cmds = append(cmds, cmd)
		}
	}

	// Claude verbs — one "New session in <worktree>" per worktree.
	if h.claudeMgr != nil && h.worktreeMgr != nil {
		wts, _ := h.worktreeMgr.List()
		names := make([]string, 0, len(wts))
		for _, wt := range wts {
			names = append(names, h.worktreeName(wt))
		}
		sort.Strings(names)
		for _, name := range names {
			esc := url.PathEscape(name)
			cmds = append(cmds, Command{
				ID: "claude.new." + name, Title: "Claude: New session in " + name,
				Action: CommandAction{
					Type: "api", Method: "POST", URL: "/api/v1/claude/" + esc + "/sessions",
					Then: &CommandAction{
						Type: "navigate",
						URL:  "/claude/{data.worktree_name}/{data.id}",
					},
				},
			})
		}
	}

	// Codex verbs — one "New session in <worktree>" per worktree.
	if h.codexMgr != nil && h.worktreeMgr != nil {
		wts, _ := h.worktreeMgr.List()
		names := make([]string, 0, len(wts))
		for _, wt := range wts {
			names = append(names, h.worktreeName(wt))
		}
		sort.Strings(names)
		for _, name := range names {
			esc := url.PathEscape(name)
			cmds = append(cmds, Command{
				ID: "codex.new." + name, Title: "Codex: New session in " + name,
				Action: CommandAction{
					Type: "api", Method: "POST", URL: "/api/v1/codex/" + esc + "/sessions",
					Then: &CommandAction{
						Type: "navigate",
						URL:  "/codex/{data.worktree_name}/{data.id}",
					},
				},
			})
		}
	}

	// Crash verbs
	if h.crashMgr != nil {
		cmds = append(cmds, Command{
			ID: "crash.clear", Title: "Crash: Clear all",
			Confirm: "Clear all crash history?",
			Action:  CommandAction{Type: "api", Method: "DELETE", URL: "/api/v1/crashes"},
		})
	}

	// Global verbs (client-side)
	cmds = append(cmds,
		Command{ID: "g.shortcuts", Title: "Help: Show keyboard shortcuts",
			Action: CommandAction{Type: "client", Name: "shortcuts"}},
		Command{ID: "g.copy-url", Title: "View: Copy current URL",
			Action: CommandAction{Type: "client", Name: "copyUrl"}},
	)

	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Title < cmds[j].Title })

	WriteJSON(w, http.StatusOK, cmds)
}

// worktreeName converts a WorktreeInfo to the display/URL name.
// Mirrors NavHandler.worktreeToDisplayName.
func (h *CommandsHandler) worktreeName(wt worktree.WorktreeInfo) string {
	dirName := wt.Name()
	if h.projectName != "" && dirName == h.projectName {
		return "main"
	}
	if h.projectName != "" {
		projectTmux := strings.ReplaceAll(h.projectName, ".", "_")
		worktreeTmux := strings.ReplaceAll(dirName, ".", "_")
		if worktreeTmux == projectTmux {
			return "main"
		}
	}
	if h.projectName != "" && strings.HasPrefix(dirName, h.projectName+"-") {
		return dirName[len(h.projectName)+1:]
	}
	return dirName
}
