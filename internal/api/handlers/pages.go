// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/crashes"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/logs"
	"github.com/wingedpig/trellis/internal/service"
	"github.com/wingedpig/trellis/internal/terminal"
	"github.com/wingedpig/trellis/internal/trace"
	"github.com/wingedpig/trellis/internal/workflow"
	"github.com/wingedpig/trellis/internal/worktree"
	"github.com/wingedpig/trellis/views"
)

// convertLayout converts config.LayoutColumnConfig to views.LayoutColumn.
func convertLayout(layout []config.LayoutColumnConfig) []views.LayoutColumn {
	if len(layout) == 0 {
		return nil
	}
	result := make([]views.LayoutColumn, len(layout))
	for i, col := range layout {
		result[i] = views.LayoutColumn{
			Field:     col.Field,
			Type:      col.Type,
			Keys:      col.Keys,
			MaxPairs:  col.MaxPairs,
			MinWidth:  col.MinWidth,
			MaxWidth:  col.MaxWidth,
			Align:     col.Align,
			Optional:  col.Optional,
			Timestamp: col.Timestamp,
		}
	}
	return result
}

// ShortcutConfig represents a keyboard shortcut configuration.
// Window must start with a prefix: ~log, #service, @worktree - window, !remote
type ShortcutConfig struct {
	Key    string
	Window string
}

// NotificationConfig represents browser notification settings.
type NotificationConfig struct {
	Enabled      bool
	Events       []string
	FailuresOnly bool
	Sound        bool
}

// LinkConfig represents a link configuration.
type LinkConfig struct {
	Name string
	URL  string
}

// PageHandler handles UI page requests.
type PageHandler struct {
	services      service.Manager
	worktrees     worktree.Manager
	workflows     workflow.Runner
	eventBus      events.EventBus
	terminals     terminal.Manager
	logManager    *logs.Manager
	traceManager  *trace.Manager
	crashManager  *crashes.Manager
	defaultWindow string
	shortcuts     []ShortcutConfig
	notifications NotificationConfig
	links         []LinkConfig
}

// NewPageHandler creates a new page handler.
func NewPageHandler(services service.Manager, worktrees worktree.Manager, workflows workflow.Runner, eventBus events.EventBus, terminals terminal.Manager, logManager *logs.Manager, traceManager *trace.Manager, crashManager *crashes.Manager, defaultWindow string, shortcuts []ShortcutConfig, notifications NotificationConfig, links []LinkConfig) *PageHandler {
	return &PageHandler{
		services:      services,
		worktrees:     worktrees,
		workflows:     workflows,
		eventBus:      eventBus,
		terminals:     terminals,
		logManager:    logManager,
		traceManager:  traceManager,
		crashManager:  crashManager,
		defaultWindow: defaultWindow,
		shortcuts:     shortcuts,
		notifications: notifications,
		links:         links,
	}
}

// Dashboard renders the dashboard page.
func (h *PageHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	services := h.services.List()
	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})

	page := &views.DashboardPage{
		BasePage: views.BasePage{
			Title: "Status",
		},
		Services: services,
	}

	if h.worktrees != nil {
		page.Worktree = h.worktrees.Active()
		wts, _ := h.worktrees.List()
		page.Worktrees = wts
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// ServiceDetail renders the service detail page.
func (h *PageHandler) ServiceDetail(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	status, err := h.services.Status(name)
	if err != nil {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	logs, _ := h.services.Logs(name, 100)

	page := &views.ServiceDetailPage{
		BasePage: views.BasePage{
			Title: "Service: " + name,
		},
		ServiceName: name,
		Status:      status,
		Logs:        logs,
	}

	if h.worktrees != nil {
		page.Worktree = h.worktrees.Active()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// Terminal redirects to the default terminal window of the active worktree.
func (h *PageHandler) Terminal(w http.ResponseWriter, r *http.Request) {
	// Build redirect URL to the default window of the active worktree
	if h.worktrees != nil && h.defaultWindow != "" {
		worktree := h.activeWorktreeName()
		http.Redirect(w, r, "/terminal/local/"+worktree+"/"+h.defaultWindow, http.StatusFound)
		return
	}

	// Fallback: show terminal list if we can't determine the default
	var terminals []views.TerminalInfo

	if h.terminals != nil {
		sessions, _ := h.terminals.ListSessions(context.Background())
		for _, sess := range sessions {
			for _, win := range sess.Windows {
				worktree := ""
				if !sess.IsRemote {
					worktree = h.sessionToWorktree(sess.Name)
				}
				terminals = append(terminals, views.TerminalInfo{
					Session:  sess.Name,
					Window:   win.Name,
					IsRemote: sess.IsRemote,
					Worktree: worktree,
				})
			}
		}
		// Sort alphabetically by window name
		sort.Slice(terminals, func(i, j int) bool {
			return terminals[i].Window < terminals[j].Window
		})
	}

	page := &views.TerminalPage{
		BasePage: views.BasePage{
			Title: "Terminal",
		},
		Terminals: terminals,
	}

	if h.worktrees != nil {
		page.Worktree = h.worktrees.Active()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// TerminalLocal renders a local terminal window.
func (h *PageHandler) TerminalLocal(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktree := vars["worktree"]
	window := vars["window"]

	// Convert worktree name to tmux session name
	session := h.worktreeToSession(worktree)

	h.renderTerminalPage(w, r, "local", session, window, false, "", worktree)
}

// TerminalRemote renders a remote terminal window.
func (h *PageHandler) TerminalRemote(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	// Get active worktree for context
	worktree := h.activeWorktreeName()
	h.renderTerminalPage(w, r, "remote", "", name, true, "", worktree)
}

// TerminalService renders a service logs view.
func (h *PageHandler) TerminalService(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	// Get active worktree for context
	worktree := h.activeWorktreeName()
	h.renderTerminalPage(w, r, "service", "", "", false, name, worktree)
}

// TerminalEditor renders the editor view.
func (h *PageHandler) TerminalEditor(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktree := vars["worktree"]
	session := h.worktreeToSession(worktree)
	h.renderTerminalPage(w, r, "editor", session, "", false, "", worktree)
}

// TerminalOutput renders the workflow output view.
func (h *PageHandler) TerminalOutput(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktree := vars["worktree"]
	session := h.worktreeToSession(worktree)
	h.renderTerminalPage(w, r, "output", session, "", false, "", worktree)
}

// TerminalLogViewer renders the log viewer view.
func (h *PageHandler) TerminalLogViewer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	// Get active worktree for context
	worktree := h.activeWorktreeName()
	h.renderTerminalPage(w, r, "logviewer", "", "", false, "", worktree, name)
}

// TerminalLegacyRedirect redirects old terminal URLs to new format.
func (h *PageHandler) TerminalLegacyRedirect(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	session := vars["session"]
	window := vars["window"]
	isRemote := r.URL.Query().Get("remote") == "1"

	if isRemote {
		http.Redirect(w, r, "/terminal/remote/"+window, http.StatusMovedPermanently)
	} else {
		// Convert session name back to worktree name
		worktree := h.sessionToWorktree(session)
		http.Redirect(w, r, "/terminal/local/"+worktree+"/"+window, http.StatusMovedPermanently)
	}
}

// worktreeToSession converts a worktree name to a tmux session name.
func (h *PageHandler) worktreeToSession(worktree string) string {
	if h.worktrees == nil {
		return worktree
	}
	projectName := h.worktrees.ProjectName()
	if projectName == "" {
		return worktree
	}
	// Convert project name to tmux format (. → _)
	projectPrefix := strings.ReplaceAll(projectName, ".", "_")
	if worktree == "main" {
		return projectPrefix
	}
	return projectPrefix + "-" + worktree
}

// sessionToWorktree converts a tmux session name to a worktree name.
func (h *PageHandler) sessionToWorktree(session string) string {
	if h.worktrees == nil {
		return session
	}
	projectName := h.worktrees.ProjectName()
	if projectName == "" {
		return session
	}
	// Convert project name to tmux format (. → _)
	projectPrefix := strings.ReplaceAll(projectName, ".", "_")
	if session == projectPrefix {
		return "main"
	}
	// Strip project prefix and hyphen
	if strings.HasPrefix(session, projectPrefix+"-") {
		return session[len(projectPrefix)+1:]
	}
	return session
}

// activeWorktreeName returns the name of the active worktree for URL paths.
// Returns "main" for the main worktree, otherwise the branch/suffix portion.
func (h *PageHandler) activeWorktreeName() string {
	if h.worktrees == nil {
		return "main"
	}
	active := h.worktrees.Active()
	if active == nil {
		return "main"
	}

	projectName := h.worktrees.ProjectName()
	worktreeDirName := active.Name()

	// Check if this is the main worktree by comparing directory name to project name
	if projectName != "" && worktreeDirName == projectName {
		return "main"
	}

	// Also check with dots converted to underscores (tmux format)
	if projectName != "" {
		projectTmux := strings.ReplaceAll(projectName, ".", "_")
		worktreeTmux := strings.ReplaceAll(worktreeDirName, ".", "_")
		if worktreeTmux == projectTmux {
			return "main"
		}
	}

	// For non-main worktrees, strip the project prefix to get just the branch name
	// e.g., "groups.io-demovideos" -> "demovideos"
	if projectName != "" && strings.HasPrefix(worktreeDirName, projectName+"-") {
		return worktreeDirName[len(projectName)+1:]
	}

	return worktreeDirName
}

// buildLogViewerList builds a sorted list of log viewer info from the log manager.
func (h *PageHandler) buildLogViewerList() []views.LogViewerInfo {
	if h.logManager == nil {
		return nil
	}

	var logViewers []views.LogViewerInfo
	for _, name := range h.logManager.List() {
		info := views.LogViewerInfo{
			Name: name,
		}
		if viewer, ok := h.logManager.Get(name); ok {
			cfg := viewer.Config()
			info.Columns = cfg.GetColumns()
			info.ColumnWidths = cfg.GetColumnWidths()
			info.Layout = convertLayout(cfg.Layout)
			info.TimestampField = cfg.Parser.Timestamp
			info.LevelField = cfg.Parser.Level
			info.MessageField = cfg.Parser.Message
			info.FileField = cfg.Parser.File
			info.LineField = cfg.Parser.Line
		}
		logViewers = append(logViewers, info)
	}
	sort.Slice(logViewers, func(i, j int) bool {
		return logViewers[i].Name < logViewers[j].Name
	})
	return logViewers
}

// renderTerminalPage renders the terminal page with the given parameters.
func (h *PageHandler) renderTerminalPage(w http.ResponseWriter, r *http.Request, viewType, session, window string, isRemote bool, serviceName, worktreeName string, logViewerName ...string) {
	// Convert shortcuts to view format
	shortcuts := make([]views.ShortcutInfo, len(h.shortcuts))
	for i, s := range h.shortcuts {
		shortcuts[i] = views.ShortcutInfo{
			Key:    s.Key,
			Window: s.Window,
		}
	}

	// Convert notifications to view format
	notifications := views.NotificationSettings{
		Enabled:      h.notifications.Enabled,
		Events:       h.notifications.Events,
		FailuresOnly: h.notifications.FailuresOnly,
		Sound:        h.notifications.Sound,
	}

	// Get services list for the picker
	var services []views.ServiceInfo
	if h.services != nil {
		for _, svc := range h.services.List() {
			info := views.ServiceInfo{
				Name:   svc.Name,
				Status: svc.Status.State.String(),
			}
			// Include logging configuration for structured log display
			if svc.ParserType != "" {
				info.ParserType = svc.ParserType
				info.TimestampField = svc.TimestampField
				info.LevelField = svc.LevelField
				info.MessageField = svc.MessageField
				info.FileField = svc.FileField
				info.LineField = svc.LineField
			}
			if len(svc.Layout) > 0 {
				info.Layout = convertLayout(svc.Layout)
			}
			if len(svc.Columns) > 0 {
				info.Columns = svc.Columns
				info.ColumnWidths = svc.ColumnWidths
			}
			services = append(services, info)
		}
		// Sort by name
		sort.Slice(services, func(i, j int) bool {
			return services[i].Name < services[j].Name
		})
	}

	// Extract log viewer name from variadic parameter
	var lvName string
	if len(logViewerName) > 0 {
		lvName = logViewerName[0]
	}

	// Determine title
	var title string
	switch viewType {
	case "local":
		title = "Terminal: " + window
	case "remote":
		title = "Remote: " + window
	case "service":
		title = "Service: " + serviceName
	case "logviewer":
		title = "Log Viewer: " + lvName
	case "editor":
		title = "Editor"
	case "output":
		title = "Output"
	default:
		title = "Terminal"
	}

	// Convert links to view format
	links := make([]views.LinkInfo, len(h.links))
	for i, l := range h.links {
		links[i] = views.LinkInfo{
			Name: l.Name,
			URL:  l.URL,
		}
	}

	// Get log viewers for the picker
	logViewers := h.buildLogViewerList()

	page := &views.TerminalWindowPage{
		BasePage: views.BasePage{
			Title: title,
		},
		Session:       session,
		Window:        window,
		IsRemote:      isRemote,
		ViewType:      viewType,
		ServiceName:   serviceName,
		WorktreeName:  worktreeName,
		LogViewerName: lvName,
		Shortcuts:     shortcuts,
		Notifications: notifications,
		Services:      services,
		Links:         links,
		LogViewers:    logViewers,
	}

	if h.worktrees != nil {
		page.Worktree = h.worktrees.Active()
		page.ProjectName = h.worktrees.ProjectName()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// Status renders the service status page.
func (h *PageHandler) Status(w http.ResponseWriter, r *http.Request) {
	var active *worktree.WorktreeInfo
	if h.worktrees != nil {
		active = h.worktrees.Active()
	}

	var services []service.ServiceInfo
	if h.services != nil {
		services = h.services.List()
	}

	page := &views.StatusPage{
		BasePage: views.BasePage{
			Title:    "Status",
			Worktree: active,
		},
		Services: services,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// Worktrees renders the worktrees page.
func (h *PageHandler) Worktrees(w http.ResponseWriter, r *http.Request) {
	var wts []worktree.WorktreeInfo
	var active *worktree.WorktreeInfo
	var projectName, binariesDir string

	if h.worktrees != nil {
		// Refresh to get current dirty/ahead/behind status
		_ = h.worktrees.Refresh()
		wts, _ = h.worktrees.List()
		active = h.worktrees.Active()
		projectName = h.worktrees.ProjectName()
		binariesDir = h.worktrees.BinariesPath()
	}

	page := &views.WorktreesPage{
		BasePage: views.BasePage{
			Title:    "Worktrees",
			Worktree: active,
		},
		Worktrees:   wts,
		Active:      active,
		ProjectName: projectName,
		BinariesDir: binariesDir,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// Workflows renders the workflows page.
func (h *PageHandler) Workflows(w http.ResponseWriter, r *http.Request) {
	workflows := h.workflows.List()

	// Sort workflows alphabetically by name
	sort.Slice(workflows, func(i, j int) bool {
		return workflows[i].Name < workflows[j].Name
	})

	var activeWorktree *worktree.WorktreeInfo
	if h.worktrees != nil {
		activeWorktree = h.worktrees.Active()
	}

	page := &views.WorkflowsPage{
		BasePage: views.BasePage{
			Title:    "Workflows",
			Worktree: activeWorktree,
		},
		Workflows: workflows,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// Events renders the events page.
func (h *PageHandler) Events(w http.ResponseWriter, r *http.Request) {
	var recentEvents []events.Event
	if h.eventBus != nil {
		recentEvents, _ = h.eventBus.History(events.EventFilter{
			Limit: 100,
		})
	}

	var activeWorktree *worktree.WorktreeInfo
	if h.worktrees != nil {
		activeWorktree = h.worktrees.Active()
	}

	page := &views.EventsPage{
		BasePage: views.BasePage{
			Title:    "Events",
			Worktree: activeWorktree,
		},
		Events: recentEvents,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// Trace renders the distributed trace page.
func (h *PageHandler) Trace(w http.ResponseWriter, r *http.Request) {
	var groups []trace.TraceGroup
	var reports []trace.ReportSummary

	if h.traceManager != nil {
		groups = h.traceManager.GetGroups()
		reports, _ = h.traceManager.ListReports()
	}

	// Sort reports by created_at descending (newest first)
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].CreatedAt.After(reports[j].CreatedAt)
	})

	var activeWorktree *worktree.WorktreeInfo
	if h.worktrees != nil {
		activeWorktree = h.worktrees.Active()
	}

	page := &views.TracePage{
		BasePage: views.BasePage{
			Title:    "Distributed Trace",
			Worktree: activeWorktree,
		},
		Groups:  groups,
		Reports: reports,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// TraceReport renders a specific trace report.
func (h *PageHandler) TraceReport(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if h.traceManager == nil {
		http.Error(w, "Trace manager not configured", http.StatusNotFound)
		return
	}

	report, err := h.traceManager.GetReport(name)
	if err != nil {
		http.Error(w, "Report not found", http.StatusNotFound)
		return
	}

	var activeWorktree *worktree.WorktreeInfo
	if h.worktrees != nil {
		activeWorktree = h.worktrees.Active()
	}

	// Get log viewer configs for the sources in this trace
	logViewers := h.buildLogViewerList()

	page := &views.TraceReportPage{
		BasePage: views.BasePage{
			Title:    "Trace: " + name,
			Worktree: activeWorktree,
		},
		Report:     report,
		LogViewers: logViewers,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// Crashes renders the crash history page.
func (h *PageHandler) Crashes(w http.ResponseWriter, r *http.Request) {
	var crashList []crashes.CrashSummary

	if h.crashManager != nil {
		crashList, _ = h.crashManager.List()
	}

	var activeWorktree *worktree.WorktreeInfo
	if h.worktrees != nil {
		activeWorktree = h.worktrees.Active()
	}

	page := &views.CrashesPage{
		BasePage: views.BasePage{
			Title:    "Crash Reports",
			Worktree: activeWorktree,
		},
		Crashes: crashList,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}

// CrashDetail renders a single crash detail page.
func (h *PageHandler) CrashDetail(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	if h.crashManager == nil {
		http.Error(w, "Crash manager not available", http.StatusServiceUnavailable)
		return
	}

	crash, err := h.crashManager.Get(id)
	if err != nil {
		http.Error(w, "Crash not found", http.StatusNotFound)
		return
	}

	var activeWorktree *worktree.WorktreeInfo
	if h.worktrees != nil {
		activeWorktree = h.worktrees.Active()
	}

	page := &views.CrashDetailPage{
		BasePage: views.BasePage{
			Title:    "Crash: " + crash.Service,
			Worktree: activeWorktree,
		},
		Crash: crash,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.WriteRender(w)
}
