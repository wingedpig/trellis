// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	_ "net/http/pprof"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/api/handlers"
	"github.com/wingedpig/trellis/internal/api/middleware"
	"github.com/wingedpig/trellis/internal/api/version"
	"github.com/wingedpig/trellis/internal/cases"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/crashes"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/logs"
	"github.com/wingedpig/trellis/internal/service"
	"github.com/wingedpig/trellis/internal/terminal"
	"github.com/wingedpig/trellis/internal/trace"
	"github.com/wingedpig/trellis/internal/workflow"
	"github.com/wingedpig/trellis/internal/worktree"
	"github.com/wingedpig/trellis/static"
)

// ServerConfig holds configuration for the API server.
type ServerConfig struct {
	Host    string
	Port    int
	TLSCert string // Path to TLS certificate file
	TLSKey  string // Path to TLS private key file
}

// Dependencies holds all dependencies for API handlers.
type Dependencies struct {
	ServiceManager  service.Manager
	WorktreeManager worktree.Manager
	WorkflowRunner  workflow.Runner
	TerminalManager terminal.Manager
	EventBus        events.EventBus
	LogManager      *logs.Manager                // Log viewer manager
	TraceManager    *trace.Manager               // Distributed trace manager
	CrashManager    *crashes.Manager             // Crash history manager
	ClaudeManager   *claude.Manager              // Claude Code session manager
	CaseManager     *cases.Manager               // Case objects manager
	VSCodeHandler   *handlers.VSCodeHandler
	Shortcuts       []handlers.ShortcutConfig    // Keyboard shortcuts for terminal windows
	Notifications   handlers.NotificationConfig  // Browser notification settings
	Links           []handlers.LinkConfig        // Links for terminal picker
	Version         string                       // Application version string
}

// NewRouter creates a new API router.
func NewRouter(deps Dependencies) *mux.Router {
	r := mux.NewRouter()

	// Apply global middleware
	r.Use(middleware.Logging)
	r.Use(middleware.Recovery)
	r.Use(middleware.CORS)
	r.Use(version.Middleware)

	// Static file serving from embedded filesystem
	staticFS, _ := fs.Sub(static.Files, ".")
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// VS Code handler (code-server proxy)
	if deps.VSCodeHandler != nil {
		r.PathPrefix("/vscode/").Handler(deps.VSCodeHandler)
		r.PathPrefix("/vscode").Handler(deps.VSCodeHandler)
	}

	// UI Page handlers
	pageHandler := handlers.NewPageHandler(deps.ServiceManager, deps.WorktreeManager, deps.WorkflowRunner, deps.EventBus, deps.TerminalManager, deps.LogManager, deps.TraceManager, deps.CrashManager, deps.ClaudeManager, deps.CaseManager, deps.Shortcuts, deps.Notifications, deps.Links, deps.Version)
	registerPageRoutes(r, pageHandler)

	// API v1 routes
	api := r.PathPrefix("/api/v1").Subrouter()

	// Service handlers
	serviceHandler := handlers.NewServiceHandler(deps.ServiceManager)
	api.HandleFunc("/services", serviceHandler.List).Methods("GET")
	api.HandleFunc("/services/{name}", serviceHandler.Get).Methods("GET")
	api.HandleFunc("/services/{name}/start", serviceHandler.Start).Methods("POST")
	api.HandleFunc("/services/{name}/stop", serviceHandler.Stop).Methods("POST")
	api.HandleFunc("/services/{name}/restart", serviceHandler.Restart).Methods("POST")
	api.HandleFunc("/services/{name}/logs", serviceHandler.Logs).Methods("GET")
	api.HandleFunc("/services/{name}/logs", serviceHandler.ClearLogs).Methods("DELETE")
	api.HandleFunc("/services/{name}/logs/stream", serviceHandler.StreamLogs).Methods("GET")

	// Worktree handlers
	worktreeHandler := handlers.NewWorktreeHandler(deps.WorktreeManager)
	api.HandleFunc("/worktrees", worktreeHandler.List).Methods("GET")
	api.HandleFunc("/worktrees", worktreeHandler.Create).Methods("POST")
	api.HandleFunc("/worktrees/info", worktreeHandler.Info).Methods("GET")
	api.HandleFunc("/worktrees/{name}", worktreeHandler.Get).Methods("GET")
	api.HandleFunc("/worktrees/{name}", worktreeHandler.Remove).Methods("DELETE")
	api.HandleFunc("/worktrees/{name}/activate", worktreeHandler.Activate).Methods("POST")

	// Workflow handlers
	workflowHandler := handlers.NewWorkflowHandler(deps.WorkflowRunner, deps.WorktreeManager)
	api.HandleFunc("/workflows", workflowHandler.List).Methods("GET")
	api.HandleFunc("/workflows/{id}", workflowHandler.Get).Methods("GET")
	api.HandleFunc("/workflows/{id}/run", workflowHandler.Run).Methods("POST")
	api.HandleFunc("/workflows/{id}/status", workflowHandler.Status).Methods("GET")
	api.HandleFunc("/workflows/{runID}/stream", workflowHandler.Stream).Methods("GET")

	// Event handlers
	eventHandler := handlers.NewEventHandler(deps.EventBus)
	api.HandleFunc("/events", eventHandler.History).Methods("GET")
	api.HandleFunc("/events/ws", eventHandler.WebSocket).Methods("GET")

	// Notify handler (for AI assistants and external tools)
	notifyHandler := handlers.NewNotifyHandler(deps.EventBus)
	api.HandleFunc("/notify", notifyHandler.Notify).Methods("POST")

	// Log viewer handlers
	if deps.LogManager != nil {
		logHandler := handlers.NewLogHandler(deps.LogManager)
		api.HandleFunc("/logs", logHandler.List).Methods("GET")
		api.HandleFunc("/logs/{name}", logHandler.Get).Methods("GET")
		api.HandleFunc("/logs/{name}/entries", logHandler.GetEntries).Methods("GET")
		api.HandleFunc("/logs/{name}/history", logHandler.GetHistory).Methods("GET")
		api.HandleFunc("/logs/{name}/files", logHandler.ListRotatedFiles).Methods("GET")
		api.HandleFunc("/logs/{name}/stream", logHandler.Stream).Methods("GET")
		api.HandleFunc("/logs/{name}/stream/sse", logHandler.StreamSSE).Methods("GET")
	}

	// Trace handlers
	if deps.TraceManager != nil {
		traceHandler := handlers.NewTraceHandler(deps.TraceManager)
		api.HandleFunc("/trace", traceHandler.Execute).Methods("POST")
		api.HandleFunc("/trace/groups", traceHandler.ListGroups).Methods("GET")
		api.HandleFunc("/trace/reports", traceHandler.ListReports).Methods("GET")
		api.HandleFunc("/trace/reports/{name:.+}", traceHandler.GetReport).Methods("GET")
		api.HandleFunc("/trace/reports/{name:.+}", traceHandler.DeleteReport).Methods("DELETE")
	}

	// Crash handlers
	if deps.CrashManager != nil {
		crashHandler := handlers.NewCrashesHandler(deps.CrashManager)
		api.HandleFunc("/crashes", crashHandler.List).Methods("GET")
		api.HandleFunc("/crashes", crashHandler.Clear).Methods("DELETE")
		api.HandleFunc("/crashes/newest", crashHandler.Newest).Methods("GET")
		api.HandleFunc("/crashes/{id}", crashHandler.Get).Methods("GET")
		api.HandleFunc("/crashes/{id}", crashHandler.Delete).Methods("DELETE")
	}

	return r
}

// registerPageRoutes registers all UI page routes on the given router.
func registerPageRoutes(r *mux.Router, pageHandler *handlers.PageHandler) {
	r.HandleFunc("/", pageHandler.Home).Methods("GET")
	r.HandleFunc("/services/{name}", pageHandler.ServiceDetail).Methods("GET")
	// New terminal URL structure
	r.HandleFunc("/terminal/local/{worktree}/{window}", pageHandler.TerminalLocal).Methods("GET")
	r.HandleFunc("/terminal/remote/{name}", pageHandler.TerminalRemote).Methods("GET")
	r.HandleFunc("/terminal/service/{name}", pageHandler.TerminalService).Methods("GET")
	r.HandleFunc("/terminal/editor/{worktree}", pageHandler.TerminalEditor).Methods("GET")
	r.HandleFunc("/terminal/output/{worktree}", pageHandler.TerminalOutput).Methods("GET")
	r.HandleFunc("/terminal/logviewer/{name}", pageHandler.TerminalLogViewer).Methods("GET")
	// Legacy redirect for old URL format
	r.HandleFunc("/terminal/{session}/{window}", pageHandler.TerminalLegacyRedirect).Methods("GET")
	r.HandleFunc("/status", pageHandler.Status).Methods("GET")
	r.HandleFunc("/worktrees", pageHandler.Worktrees).Methods("GET")
	r.HandleFunc("/events", pageHandler.Events).Methods("GET")
	// Trace pages
	r.HandleFunc("/trace", pageHandler.Trace).Methods("GET")
	r.HandleFunc("/trace/report/{name:.+}", pageHandler.TraceReport).Methods("GET")
	// Crash history pages
	r.HandleFunc("/crashes", pageHandler.Crashes).Methods("GET")
	r.HandleFunc("/crashes/{id}", pageHandler.CrashDetail).Methods("GET")
	// Worktree home page
	r.HandleFunc("/worktree/{name}", pageHandler.WorktreeHome).Methods("GET")
	// Case detail page
	r.HandleFunc("/case/{worktree}/{id}", pageHandler.CaseDetail).Methods("GET")
	r.HandleFunc("/case/{worktree}/{id}/trace/{trace_id}", pageHandler.CaseTraceView).Methods("GET")
	// Claude Code chat pages
	r.HandleFunc("/claude/{worktree}/{session}", pageHandler.ClaudePage).Methods("GET")
	r.HandleFunc("/claude/{worktree}", pageHandler.ClaudeRedirect).Methods("GET")
}

// NewRouterWithTerminalHandler creates a router with a pre-created terminal handler.
func NewRouterWithTerminalHandler(deps Dependencies, terminalHandler *handlers.TerminalHandler) *mux.Router {
	r := mux.NewRouter()

	// Apply global middleware
	r.Use(middleware.Logging)
	r.Use(middleware.Recovery)
	r.Use(middleware.CORS)
	r.Use(version.Middleware)

	// Static file serving from embedded filesystem
	staticFS, _ := fs.Sub(static.Files, ".")
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// VS Code handler (code-server proxy)
	if deps.VSCodeHandler != nil {
		r.PathPrefix("/vscode/").Handler(deps.VSCodeHandler)
		r.PathPrefix("/vscode").Handler(deps.VSCodeHandler)
	}

	// UI Page handlers
	pageHandler := handlers.NewPageHandler(deps.ServiceManager, deps.WorktreeManager, deps.WorkflowRunner, deps.EventBus, deps.TerminalManager, deps.LogManager, deps.TraceManager, deps.CrashManager, deps.ClaudeManager, deps.CaseManager, deps.Shortcuts, deps.Notifications, deps.Links, deps.Version)
	registerPageRoutes(r, pageHandler)

	// API v1 routes
	api := r.PathPrefix("/api/v1").Subrouter()

	// Service handlers
	serviceHandler := handlers.NewServiceHandler(deps.ServiceManager)
	api.HandleFunc("/services", serviceHandler.List).Methods("GET")
	api.HandleFunc("/services/{name}", serviceHandler.Get).Methods("GET")
	api.HandleFunc("/services/{name}/start", serviceHandler.Start).Methods("POST")
	api.HandleFunc("/services/{name}/stop", serviceHandler.Stop).Methods("POST")
	api.HandleFunc("/services/{name}/restart", serviceHandler.Restart).Methods("POST")
	api.HandleFunc("/services/{name}/logs", serviceHandler.Logs).Methods("GET")
	api.HandleFunc("/services/{name}/logs", serviceHandler.ClearLogs).Methods("DELETE")
	api.HandleFunc("/services/{name}/logs/stream", serviceHandler.StreamLogs).Methods("GET")

	// Worktree handlers
	worktreeHandler := handlers.NewWorktreeHandler(deps.WorktreeManager)
	api.HandleFunc("/worktrees", worktreeHandler.List).Methods("GET")
	api.HandleFunc("/worktrees", worktreeHandler.Create).Methods("POST")
	api.HandleFunc("/worktrees/info", worktreeHandler.Info).Methods("GET")
	api.HandleFunc("/worktrees/{name}", worktreeHandler.Get).Methods("GET")
	api.HandleFunc("/worktrees/{name}", worktreeHandler.Remove).Methods("DELETE")
	api.HandleFunc("/worktrees/{name}/activate", worktreeHandler.Activate).Methods("POST")

	// Workflow handlers
	workflowHandler := handlers.NewWorkflowHandler(deps.WorkflowRunner, deps.WorktreeManager)
	api.HandleFunc("/workflows", workflowHandler.List).Methods("GET")
	api.HandleFunc("/workflows/{id}", workflowHandler.Get).Methods("GET")
	api.HandleFunc("/workflows/{id}/run", workflowHandler.Run).Methods("POST")
	api.HandleFunc("/workflows/{id}/status", workflowHandler.Status).Methods("GET")
	api.HandleFunc("/workflows/{runID}/stream", workflowHandler.Stream).Methods("GET")

	// Event handlers
	eventHandler := handlers.NewEventHandler(deps.EventBus)
	api.HandleFunc("/events", eventHandler.History).Methods("GET")
	api.HandleFunc("/events/ws", eventHandler.WebSocket).Methods("GET")

	// Notify handler (for AI assistants and external tools)
	notifyHandler := handlers.NewNotifyHandler(deps.EventBus)
	api.HandleFunc("/notify", notifyHandler.Notify).Methods("POST")

	// Terminal handlers (using the provided handler)
	api.HandleFunc("/terminal/sessions", terminalHandler.ListSessions).Methods("GET")
	api.HandleFunc("/terminal/ws", terminalHandler.WebSocket).Methods("GET")
	api.HandleFunc("/terminal/{worktree}/windows", terminalHandler.CreateWindow).Methods("POST")
	api.HandleFunc("/terminal/{worktree}/windows/{window}", terminalHandler.RenameWindow).Methods("PATCH")
	api.HandleFunc("/terminal/{worktree}/windows/{window}", terminalHandler.DeleteWindow).Methods("DELETE")

	// Navigation handlers (combined picker options)
	var projectName string
	if deps.WorktreeManager != nil {
		projectName = deps.WorktreeManager.ProjectName()
	}
	navHandler := handlers.NewNavHandler(deps.TerminalManager, deps.ServiceManager, deps.LogManager, deps.ClaudeManager, deps.CaseManager, deps.WorktreeManager, deps.Links, deps.Shortcuts, deps.Notifications, projectName)
	api.HandleFunc("/nav/options", navHandler.Options).Methods("GET")

	// Case handlers
	if deps.CaseManager != nil {
		caseHandler := handlers.NewCaseHandler(deps.CaseManager, deps.ClaudeManager, deps.TraceManager, deps.WorktreeManager)
		api.HandleFunc("/cases/{worktree}", caseHandler.List).Methods("GET")
		api.HandleFunc("/cases/{worktree}/archived", caseHandler.ListArchived).Methods("GET")
		api.HandleFunc("/cases/{worktree}", caseHandler.Create).Methods("POST")
		api.HandleFunc("/cases/{worktree}/{id}", caseHandler.Get).Methods("GET")
		api.HandleFunc("/cases/{worktree}/{id}/notes", caseHandler.GetNotes).Methods("GET")
		api.HandleFunc("/cases/{worktree}/{id}", caseHandler.Update).Methods("PATCH")
		api.HandleFunc("/cases/{worktree}/{id}", caseHandler.Delete).Methods("DELETE")
		api.HandleFunc("/cases/{worktree}/{id}/archive", caseHandler.Archive).Methods("POST")
		api.HandleFunc("/cases/{worktree}/{id}/reopen", caseHandler.Reopen).Methods("POST")
		api.HandleFunc("/cases/{worktree}/{id}/evidence", caseHandler.AttachEvidence).Methods("POST")
		api.HandleFunc("/cases/{worktree}/{id}/transcript", caseHandler.SaveTranscript).Methods("POST")
		api.HandleFunc("/cases/{worktree}/{id}/transcript/{claude_id}", caseHandler.UpdateTranscript).Methods("PUT")
		api.HandleFunc("/cases/{worktree}/{id}/transcript/{claude_id}/continue", caseHandler.ContinueTranscript).Methods("POST")
		api.HandleFunc("/cases/{worktree}/{id}/trace", caseHandler.SaveTrace).Methods("POST")
		api.HandleFunc("/cases/{worktree}/{id}/trace/{trace_id}", caseHandler.DeleteTrace).Methods("DELETE")
	}

	// VS Code file opening (requires VSCodeHandler)
	if deps.VSCodeHandler != nil {
		api.HandleFunc("/vscode/open", deps.VSCodeHandler.OpenFile).Methods("POST")
	}

	// Claude Code handlers
	if deps.ClaudeManager != nil {
		claudeHandler := handlers.NewClaudeHandler(deps.ClaudeManager, deps.WorktreeManager, deps.CaseManager, deps.TraceManager)
		api.HandleFunc("/claude/{worktree}/sessions", claudeHandler.ListSessions).Methods("GET")
		api.HandleFunc("/claude/{worktree}/sessions", claudeHandler.CreateSessionAPI).Methods("POST")
		api.HandleFunc("/claude/sessions/{session}", claudeHandler.RenameSessionAPI).Methods("PATCH")
		api.HandleFunc("/claude/sessions/{session}", claudeHandler.DeleteSessionAPI).Methods("DELETE")
		api.HandleFunc("/claude/sessions/{session}/ws", claudeHandler.WebSocket).Methods("GET")
		api.HandleFunc("/claude/sessions/{session}/export", claudeHandler.ExportSessionAPI).Methods("GET")
		api.HandleFunc("/claude/sessions/{session}/restore", claudeHandler.RestoreSessionAPI).Methods("POST")
		api.HandleFunc("/claude/sessions/{session}/permanent", claudeHandler.PermanentDeleteSessionAPI).Methods("DELETE")
		api.HandleFunc("/claude/{worktree}/sessions/import", claudeHandler.ImportSessionAPI).Methods("POST")
		api.HandleFunc("/claude/{worktree}/sessions/trash", claudeHandler.ListTrashedSessionsAPI).Methods("GET")
		api.HandleFunc("/claude/{worktree}/git-status", claudeHandler.GitStatus).Methods("GET")
		api.HandleFunc("/claude/{worktree}/session-case", claudeHandler.SessionCase).Methods("GET")
		api.HandleFunc("/claude/{worktree}/wrap-up", claudeHandler.WrapUp).Methods("POST")
		api.HandleFunc("/claude/{worktree}/trace-reports", claudeHandler.ListTraceReports).Methods("GET")
		// Backwards compat: worktree-level WebSocket uses first session
		api.HandleFunc("/claude/{worktree}/ws", claudeHandler.WebSocketByWorktree).Methods("GET")
	}

	// Log viewer handlers
	if deps.LogManager != nil {
		logHandler := handlers.NewLogHandler(deps.LogManager)
		api.HandleFunc("/logs", logHandler.List).Methods("GET")
		api.HandleFunc("/logs/{name}", logHandler.Get).Methods("GET")
		api.HandleFunc("/logs/{name}/entries", logHandler.GetEntries).Methods("GET")
		api.HandleFunc("/logs/{name}/history", logHandler.GetHistory).Methods("GET")
		api.HandleFunc("/logs/{name}/files", logHandler.ListRotatedFiles).Methods("GET")
		api.HandleFunc("/logs/{name}/stream", logHandler.Stream).Methods("GET")
		api.HandleFunc("/logs/{name}/stream/sse", logHandler.StreamSSE).Methods("GET")
	}

	// Trace handlers
	if deps.TraceManager != nil {
		traceHandler := handlers.NewTraceHandler(deps.TraceManager)
		api.HandleFunc("/trace", traceHandler.Execute).Methods("POST")
		api.HandleFunc("/trace/groups", traceHandler.ListGroups).Methods("GET")
		api.HandleFunc("/trace/reports", traceHandler.ListReports).Methods("GET")
		api.HandleFunc("/trace/reports/{name:.+}", traceHandler.GetReport).Methods("GET")
		api.HandleFunc("/trace/reports/{name:.+}", traceHandler.DeleteReport).Methods("DELETE")
	}

	// Crash handlers
	if deps.CrashManager != nil {
		crashHandler := handlers.NewCrashesHandler(deps.CrashManager)
		api.HandleFunc("/crashes", crashHandler.List).Methods("GET")
		api.HandleFunc("/crashes", crashHandler.Clear).Methods("DELETE")
		api.HandleFunc("/crashes/newest", crashHandler.Newest).Methods("GET")
		api.HandleFunc("/crashes/{id}", crashHandler.Get).Methods("GET")
		api.HandleFunc("/crashes/{id}", crashHandler.Delete).Methods("DELETE")
	}

	// Debug/profiling endpoints
	r.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)

	return r
}

// Server represents the API server.
type Server struct {
	router          *mux.Router
	cfg             ServerConfig
	server          *http.Server
	terminalHandler *handlers.TerminalHandler
}

// NewServer creates a new API server.
func NewServer(cfg ServerConfig, deps Dependencies) *Server {
	terminalHandler := handlers.NewTerminalHandler(deps.TerminalManager, deps.WorktreeManager)
	return &Server{
		router:          NewRouterWithTerminalHandler(deps, terminalHandler),
		cfg:             cfg,
		terminalHandler: terminalHandler,
	}
}

// Router returns the underlying router.
func (s *Server) Router() *mux.Router {
	return s.router
}

// ListenAndServe starts the server.
// If TLS is configured (tls_cert and tls_key), uses HTTPS.
// If cert/key files don't exist, they are auto-generated.
func (s *Server) ListenAndServe() error {
	addr := s.cfg.Host + ":" + strconv.Itoa(s.cfg.Port)
	s.server = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}

	// Check if TLS is configured
	tlsEnabled, err := CheckTLSConfig(s.cfg.TLSCert, s.cfg.TLSKey)
	if err != nil {
		return fmt.Errorf("TLS configuration error: %w", err)
	}

	if tlsEnabled {
		certPath := expandPath(s.cfg.TLSCert)
		keyPath := expandPath(s.cfg.TLSKey)
		log.Printf("API server listening on https://%s (TLS enabled)", addr)
		return s.server.ListenAndServeTLS(certPath, keyPath)
	}

	log.Printf("API server listening on http://%s", addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	// Shutdown terminal handler first to clean up remote sessions
	if s.terminalHandler != nil {
		s.terminalHandler.Shutdown()
	}

	if s.server == nil {
		return nil
	}

	log.Println("Shutting down API server...")

	// Create a timeout context if none provided
	shutdownCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		shutdownCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	return s.server.Shutdown(shutdownCtx)
}
