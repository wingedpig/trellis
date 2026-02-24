// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wingedpig/trellis/internal/api"
	"github.com/wingedpig/trellis/internal/api/handlers"
	"github.com/wingedpig/trellis/internal/cases"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/crashes"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/logs"
	"github.com/wingedpig/trellis/internal/proxy"
	"github.com/wingedpig/trellis/internal/service"
	"github.com/wingedpig/trellis/internal/terminal"
	"github.com/wingedpig/trellis/internal/trace"
	"github.com/wingedpig/trellis/internal/watcher"
	"github.com/wingedpig/trellis/internal/workflow"
	"github.com/wingedpig/trellis/internal/worktree"
)

// App is the main application container.
type App struct {
	mu sync.RWMutex

	configPath       string // Path to config file (for determining repo directory)
	worktreeOverride string // Worktree name/branch override from command line
	version          string // Application version string
	originalConfig   *config.Config // Original unexpanded config (for worktree switching)
	config           *config.Config // Expanded config for current worktree
	eventBus         events.EventBus
	serviceManager   service.Manager
	worktreeManager  worktree.Manager
	workflowRunner   workflow.Runner
	terminalManager  terminal.Manager
	logManager       *logs.Manager
	traceManager     *trace.Manager
	crashManager     *crashes.Manager
	binaryWatcher    *watcher.BinaryWatcher
	vsCodeHandler    *handlers.VSCodeHandler
	claudeManager    *claude.Manager
	caseManager      *cases.Manager
	proxyManager     *proxy.Manager
	apiServer        *api.Server

	done     chan struct{}
	stopOnce sync.Once
}

// Options holds configuration options for the app.
type Options struct {
	ConfigPath string
	Host       string
	Port       int
	Worktree   string // Worktree name or branch to activate
	Debug      bool
	Version    string // Application version string
}

// New creates a new App instance.
func New(opts Options) (*App, error) {
	app := &App{
		configPath:       opts.ConfigPath,
		worktreeOverride: opts.Worktree,
		version:          opts.Version,
		done:             make(chan struct{}),
	}

	// Load configuration
	loader := config.NewLoader()
	cfg, err := loader.LoadWithDefaults(context.Background(), opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	app.originalConfig = cfg // Keep unexpanded config for worktree switching
	app.config = cfg

	// Override host/port if specified
	if opts.Host != "" {
		cfg.Server.Host = opts.Host
	}
	if opts.Port > 0 {
		cfg.Server.Port = opts.Port
	}

	// Initialize event bus
	app.eventBus = events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: cfg.Events.History.MaxEvents,
		HistoryMaxAge:    config.ParseDuration(cfg.Events.History.MaxAge, time.Hour),
	})

	return app, nil
}

// cleanupStalePipes removes leftover terminal pipe files from previous sessions.
// These can accumulate if Trellis is killed (SIGKILL) or crashes without cleanup.
func cleanupStalePipes() {
	// Match pattern: /tmp/trellis-pipe-*-*-<timestamp>.fifo
	// The timestamp is a UnixNano value (18-19 digits)
	pattern := regexp.MustCompile(`^trellis-pipe-.*-\d{18,19}\.fifo$`)

	entries, err := os.ReadDir("/tmp")
	if err != nil {
		return // Can't read /tmp, nothing to clean
	}

	var removed int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if pattern.MatchString(name) {
			path := filepath.Join("/tmp", name)
			if err := os.Remove(path); err == nil {
				removed++
			}
		}
	}

	if removed > 0 {
		log.Printf("Cleaned up %d stale terminal pipe files", removed)
	}
}

// Initialize sets up all components.
func (app *App) Initialize(ctx context.Context) error {
	cfg := app.config

	// Clean up stale pipe files from previous sessions
	cleanupStalePipes()

	// Determine repo directory for worktree discovery
	repoDir := cfg.Worktree.RepoDir
	if repoDir == "" && app.configPath != "" {
		// Default to config file's directory
		absPath, err := filepath.Abs(app.configPath)
		if err == nil {
			repoDir = filepath.Dir(absPath)
		}
	}
	log.Printf("Using repo directory for worktree discovery: %s", repoDir)

	// Determine create directory for new worktrees
	createDir := cfg.Worktree.CreateDir
	if createDir == "" {
		// Default to parent of repo directory
		createDir = filepath.Dir(repoDir)
	}
	log.Printf("Using create directory for new worktrees: %s", createDir)

	// Initialize worktree manager
	gitExecutor := worktree.NewRealGitExecutor()
	app.worktreeManager = worktree.NewManager(gitExecutor, app.eventBus, cfg.Worktree, repoDir, createDir, cfg.Project.Name)

	// Discover worktrees
	if err := app.worktreeManager.Refresh(); err != nil {
		log.Printf("Warning: failed to refresh worktrees: %v", err)
	}

	// Set active worktree (first one or main)
	worktrees, _ := app.worktreeManager.List()
	log.Printf("Discovered %d worktrees", len(worktrees))
	for _, wt := range worktrees {
		log.Printf("  - %s (%s) at %s", wt.Name(), wt.Branch, wt.Path)
	}

	if len(worktrees) > 0 {
		// Find the worktree we're running from, or fall back to main/master
		activeName := worktrees[0].Name()
		detected := false

		// First, check if worktree was specified on command line
		if app.worktreeOverride != "" {
			for _, wt := range worktrees {
				if wt.Name() == app.worktreeOverride || wt.Branch == app.worktreeOverride {
					activeName = wt.Name()
					detected = true
					log.Printf("Using worktree from command line: %s", activeName)
					break
				}
			}
			if !detected {
				log.Printf("Warning: specified worktree %q not found, using auto-detection", app.worktreeOverride)
			}
		}

		// If not specified, check if current working directory is within a worktree
		if !detected {
			cwd, err := os.Getwd()
			if err == nil {
				absCwd, _ := filepath.Abs(cwd)
				for _, wt := range worktrees {
					absWtPath, _ := filepath.Abs(wt.Path)
					// Check if CWD is the worktree or a subdirectory of it
					if absCwd == absWtPath || strings.HasPrefix(absCwd, absWtPath+string(os.PathSeparator)) {
						activeName = wt.Name()
						detected = true
						log.Printf("Detected current worktree from CWD: %s", activeName)
						break
					}
				}
			}
		}

		// If not detected from CWD, check if config file is within a worktree
		if !detected && repoDir != "" {
			absRepoDir, _ := filepath.Abs(repoDir)
			for _, wt := range worktrees {
				absWtPath, _ := filepath.Abs(wt.Path)
				if absRepoDir == absWtPath || strings.HasPrefix(absRepoDir, absWtPath+string(os.PathSeparator)) {
					activeName = wt.Name()
					detected = true
					log.Printf("Detected current worktree from config location: %s", activeName)
					break
				}
			}
		}

		// If still not detected, default to main/master
		if !detected {
			for _, wt := range worktrees {
				if wt.Branch == "main" || wt.Branch == "master" {
					activeName = wt.Name()
					log.Printf("Defaulting to main/master worktree: %s", activeName)
					break
				}
			}
		}

		// Use SetActive (no hooks) for initial startup - hooks are for mid-session switches
		if err := app.worktreeManager.SetActive(activeName); err != nil {
			log.Printf("Warning: failed to set active worktree %s: %v", activeName, err)
		} else {
			log.Printf("Active worktree: %s", activeName)
			// Set default worktree on event bus so events have proper worktree field
			app.eventBus.SetDefaultWorktree(activeName)
		}
	} else {
		log.Printf("Warning: no worktrees found, template expansion may not work correctly")
	}

	// Expand templates in config
	expander := config.NewTemplateExpander()
	templateCtx := &config.TemplateContext{
		Worktree: config.WorktreeTemplateData{
			Root: "",
		},
	}
	if active := app.worktreeManager.Active(); active != nil {
		templateCtx.Worktree.Root = active.Path
		templateCtx.Worktree.Branch = active.Branch
		templateCtx.Worktree.Name = active.Name()
		templateCtx.Worktree.Binaries = cfg.Worktree.Binaries.Path
		// Also expand binaries path if it has templates
		if expandedBin, err := expander.Expand(cfg.Worktree.Binaries.Path, templateCtx); err == nil {
			templateCtx.Worktree.Binaries = expandedBin
		}
		log.Printf("Template context: Root=%s, Name=%s, Branch=%s, Binaries=%s",
			templateCtx.Worktree.Root, templateCtx.Worktree.Name,
			templateCtx.Worktree.Branch, templateCtx.Worktree.Binaries)
	} else {
		log.Printf("Warning: no active worktree, templates will not be expanded")
	}
	expandedConfig, err := expander.ExpandConfig(cfg, templateCtx)
	if err != nil {
		return fmt.Errorf("failed to expand config templates: %w", err)
	}
	app.config = expandedConfig

	// Initialize Claude Code session manager
	claudeStateDir := filepath.Join(filepath.Dir(app.configPath), ".trellis", "claude")
	app.claudeManager = claude.NewManager(claudeStateDir)

	// Initialize case manager
	casesDir := cfg.Cases.Dir
	if casesDir == "" {
		casesDir = "trellis/cases"
	}
	app.caseManager = cases.NewManager(casesDir)

	// Initialize terminal manager
	tmuxExecutor := terminal.NewRealTmuxExecutor()
	remoteWindows := make([]terminal.RemoteWindowConfig, 0, len(cfg.Terminal.RemoteWindows))
	for _, rw := range cfg.Terminal.RemoteWindows {
		remoteWindows = append(remoteWindows, terminal.RemoteWindowConfig{
			Name:        rw.Name,
			Command:     rw.Command,
			SSHHost:     rw.SSHHost,
			TmuxSession: rw.TmuxSession,
		})
	}

	// Compute API base URL for trellis-ctl
	apiHost := cfg.Server.Host
	if apiHost == "" || apiHost == "0.0.0.0" {
		apiHost = "localhost"
	}
	apiBaseURL := fmt.Sprintf("http://%s:%d", apiHost, cfg.Server.Port)

	terminalStateDir := filepath.Join(filepath.Dir(app.configPath), ".trellis", "terminal")
	app.terminalManager = terminal.NewManager(tmuxExecutor, terminal.TerminalConfig{
		DefaultShell:  cfg.Terminal.Tmux.Shell,
		RemoteWindows: remoteWindows,
		ProjectName:   cfg.Project.Name,
		APIBaseURL:    apiBaseURL,
		StateDir:      terminalStateDir,
	})

	// Initialize service manager (use expanded config)
	app.serviceManager = service.NewManager(app.config.Services, app.eventBus, nil)

	// Initialize workflow runner (use expanded config)
	workflowConfigs := make([]workflow.WorkflowConfig, 0, len(app.config.Workflows))
	for _, wf := range app.config.Workflows {
		workflowConfigs = append(workflowConfigs, workflow.WorkflowConfig{
			ID:              wf.ID,
			Name:            wf.Name,
			Description:     wf.Description,
			Command:         getCommandAsStrings(wf.Command),
			Commands:        getCommandsAsArray(wf.Commands),
			Timeout:         config.ParseDuration(wf.Timeout, 0),
			OutputParser:    wf.OutputParser,
			Confirm:         wf.Confirm,
			ConfirmMessage:  wf.ConfirmMessage,
			RequiresStopped: wf.RequiresStopped,
			RestartServices: wf.RestartServices,
			Inputs:          convertWorkflowInputs(wf.Inputs),
		})
	}

	workingDir := ""
	if active := app.worktreeManager.Active(); active != nil {
		workingDir = active.Path
	}
	app.workflowRunner = workflow.NewRunner(
		workflowConfigs,
		app.eventBus,
		&serviceControllerAdapter{app.serviceManager},
		workingDir,
	)

	// Initialize log manager (if services exist OR log viewers configured)
	if len(cfg.LogViewers) > 0 || len(cfg.Services) > 0 {
		app.logManager = logs.NewManager(app.eventBus, cfg.LogViewerSettings)
		if len(cfg.LogViewers) > 0 {
			if err := app.logManager.Initialize(cfg.LogViewers); err != nil {
				log.Printf("Warning: failed to initialize log viewers: %v", err)
			} else {
				log.Printf("Initialized %d log viewers", len(cfg.LogViewers))
			}
		}
	}

	// Create service log viewers (svc:* viewers from service log buffers)
	if app.logManager != nil && len(cfg.Services) > 0 {
		svcViewerNames := app.createServiceLogViewers(cfg)
		app.injectServicesTraceGroup(cfg, svcViewerNames)
	}

	// Initialize trace manager (requires log manager)
	if app.logManager != nil && (len(cfg.TraceGroups) > 0) {
		var err error
		app.traceManager, err = trace.NewManager(app.logManager, cfg, app.eventBus)
		if err != nil {
			log.Printf("Warning: failed to initialize trace manager: %v", err)
		} else {
			log.Printf("Initialized trace manager with %d groups", len(cfg.TraceGroups))
		}
	}

	// Initialize crash manager
	crashDir := cfg.Crashes.ReportsDir
	if crashDir == "" {
		crashDir = filepath.Join(filepath.Dir(app.configPath), ".trellis", "crashes")
	}
	crashMaxAge := config.ParseDuration(cfg.Crashes.MaxAge, 7*24*time.Hour)
	crashMaxCount := cfg.Crashes.MaxCount
	if crashMaxCount == 0 {
		crashMaxCount = 100
	}
	// Build per-service ID fields map (each service's logging config merged with defaults)
	serviceIDFields := config.BuildServiceIDFields(app.config.Services, &cfg.LoggingDefaults)
	crashMgr, err := crashes.NewManager(
		crashes.Config{
			ReportsDir: crashDir,
			MaxAge:     crashMaxAge,
			MaxCount:   crashMaxCount,
		},
		app.serviceManager,
		app.worktreeManager,
		app.eventBus,
		cfg.LoggingDefaults.Parser.ID,
		serviceIDFields,
		cfg.LoggingDefaults.Parser.Stack,
	)
	if err != nil {
		log.Printf("Warning: failed to initialize crash manager: %v", err)
	} else {
		app.crashManager = crashMgr
		if err := app.crashManager.Subscribe(); err != nil {
			log.Printf("Warning: failed to subscribe crash manager to events: %v", err)
		} else {
			log.Printf("Initialized crash manager: %s", crashDir)
		}
	}

	// Initialize binary watcher (use expanded config for paths)
	debounce := config.ParseDuration(app.config.Watch.Debounce, 100*time.Millisecond)
	bw, err := watcher.NewBinaryWatcher(app.eventBus, debounce)
	if err != nil {
		log.Printf("Warning: failed to create binary watcher: %v", err)
	} else {
		app.binaryWatcher = bw

		// Set up binary watches using expanded config
		for _, svc := range app.config.Services {
			if !svc.IsWatching() {
				continue
			}
			var paths []string
			if binaryPath := svc.GetBinaryPath(); binaryPath != "" {
				paths = append(paths, binaryPath)
			}
			paths = append(paths, svc.WatchFiles...)
			if len(paths) > 0 {
				if err := app.binaryWatcher.Watch(svc.Name, paths); err != nil {
					log.Printf("Warning: failed to watch files for %s: %v", svc.Name, err)
				}
			}
		}
	}

	// Subscribe to binary/file change events for auto-restart
	app.eventBus.Subscribe(events.EventBinaryChanged, func(ctx context.Context, event events.Event) error {
		if serviceName, ok := event.Payload["service"].(string); ok {
			path, _ := event.Payload["path"].(string)
			log.Printf("File changed for service %s (%s), restarting...", serviceName, path)
			return app.serviceManager.Restart(ctx, serviceName, service.RestartWatch)
		}
		return nil
	})

	// Subscribe to worktree activation events to restart services with new config
	app.eventBus.Subscribe("worktree.activated", func(ctx context.Context, event events.Event) error {
		worktreeName := ""
		worktreePath := ""
		worktreeBranch := ""
		if name, ok := event.Payload["name"].(string); ok {
			worktreeName = name
		}
		if path, ok := event.Payload["path"].(string); ok {
			worktreePath = path
		}
		if branch, ok := event.Payload["branch"].(string); ok {
			worktreeBranch = branch
		}
		log.Printf("Worktree activated event received: name=%s path=%s branch=%s", worktreeName, worktreePath, worktreeBranch)

		// Update default worktree on event bus for future events
		app.eventBus.SetDefaultWorktree(worktreeName)

		// Use background context for service operations - the HTTP request context
		// may be cancelled when the response is sent, which would kill the processes
		bgCtx := context.Background()

		// Stop all services first
		if err := app.serviceManager.StopAll(bgCtx); err != nil {
			log.Printf("Warning: failed to stop services: %v", err)
		}

		// Re-expand config templates with new worktree path using ORIGINAL unexpanded config
		expander := config.NewTemplateExpander()
		templateCtx := &config.TemplateContext{
			Worktree: config.WorktreeTemplateData{
				Root:   worktreePath,
				Branch: worktreeBranch,
				Name:   worktreeName,
			},
		}
		// Expand binaries path from original config
		if expandedBin, err := expander.Expand(app.originalConfig.Worktree.Binaries.Path, templateCtx); err == nil {
			templateCtx.Worktree.Binaries = expandedBin
		}

		expandedConfig, err := expander.ExpandConfig(app.originalConfig, templateCtx)
		if err != nil {
			log.Printf("Warning: failed to expand config for new worktree: %v", err)
			// Use bgCtx to ensure services are restarted even if HTTP request context is cancelled
			return app.serviceManager.StartAll(bgCtx)
		}

		// Log the expanded service configs for debugging
		for _, svc := range expandedConfig.Services {
			log.Printf("Expanded service %s: workdir=%s cmd=%v", svc.Name, svc.WorkDir, svc.GetCommand())
		}

		// Update current config
		app.config = expandedConfig

		// Update service manager with new configs
		app.serviceManager.UpdateConfigs(expandedConfig.Services)

		// Update binary watcher paths
		if app.binaryWatcher != nil {
			// Build set of services that should be watched
			shouldWatch := make(map[string]bool)
			for _, svc := range expandedConfig.Services {
				if !svc.IsWatching() {
					continue
				}
				var paths []string
				if binaryPath := svc.GetBinaryPath(); binaryPath != "" {
					paths = append(paths, binaryPath)
				}
				paths = append(paths, svc.WatchFiles...)
				if len(paths) > 0 {
					shouldWatch[svc.Name] = true
					if err := app.binaryWatcher.Watch(svc.Name, paths); err != nil {
						log.Printf("Warning: failed to update watch for %s: %v", svc.Name, err)
					}
				}
			}
			// Remove watches for services no longer in config or disabled
			for _, name := range app.binaryWatcher.Watching() {
				if !shouldWatch[name] {
					if err := app.binaryWatcher.Unwatch(name); err != nil {
						log.Printf("Warning: failed to remove watch for %s: %v", name, err)
					}
				}
			}
		}

		// Update workflow runner with new configs and working directory
		if app.workflowRunner != nil {
			workflowConfigs := make([]workflow.WorkflowConfig, 0, len(expandedConfig.Workflows))
			for _, wf := range expandedConfig.Workflows {
				workflowConfigs = append(workflowConfigs, workflow.WorkflowConfig{
					ID:              wf.ID,
					Name:            wf.Name,
					Description:     wf.Description,
					Command:         getCommandAsStrings(wf.Command),
					Commands:        getCommandsAsArray(wf.Commands),
					Timeout:         config.ParseDuration(wf.Timeout, 0),
					OutputParser:    wf.OutputParser,
					Confirm:         wf.Confirm,
					ConfirmMessage:  wf.ConfirmMessage,
					RequiresStopped: wf.RequiresStopped,
					RestartServices: wf.RestartServices,
					Inputs:          convertWorkflowInputs(wf.Inputs),
				})
			}
			app.workflowRunner.UpdateConfig(workflowConfigs, worktreePath)
			log.Printf("Updated workflow runner for worktree: %s", worktreePath)
		}

		// Update log manager with new configs (paths may depend on worktree)
		if app.logManager != nil {
			// Remove old service viewers first
			app.logManager.RemoveServiceViewers()

			if err := app.logManager.UpdateConfigs(expandedConfig.LogViewers); err != nil {
				log.Printf("Warning: failed to update log viewers: %v", err)
			}

			// Recreate service log viewers with updated parser configs
			if len(expandedConfig.Services) > 0 {
				svcViewerNames := app.createServiceLogViewers(expandedConfig)
				app.injectServicesTraceGroup(expandedConfig, svcViewerNames)
			}
		}

		// Update trace manager with new configs
		if app.traceManager != nil {
			app.traceManager.UpdateConfigs(expandedConfig)
		}

		// Update crash manager with new service ID fields
		if app.crashManager != nil {
			serviceIDFields := config.BuildServiceIDFields(expandedConfig.Services, &expandedConfig.LoggingDefaults)
			app.crashManager.UpdateServiceIDFields(serviceIDFields)
		}

		return app.serviceManager.StartAll(bgCtx)
	})

	// Initialize VS Code handler if configured
	if cfg.Terminal.VSCode != nil && cfg.Terminal.VSCode.Binary != "" {
		workdir := ""
		if active := app.worktreeManager.Active(); active != nil {
			workdir = active.Path
			log.Printf("VS Code workdir from active worktree: %s", workdir)
		} else {
			log.Printf("Warning: no active worktree for VS Code")
		}
		app.vsCodeHandler = handlers.NewVSCodeHandler(
			handlers.VSCodeConfig{
				Binary:      cfg.Terminal.VSCode.Binary,
				Port:        cfg.Terminal.VSCode.Port,
				UserDataDir: cfg.Terminal.VSCode.UserDataDir,
			},
			workdir,
			app.worktreeManager,
		)
		// Start code-server
		if err := app.vsCodeHandler.Start(ctx); err != nil {
			log.Printf("Warning: failed to start code-server: %v", err)
		}
	}

	// Initialize proxy manager if configured (use expanded config for template values)
	if len(app.config.Proxy) > 0 {
		pm, err := proxy.NewManager(app.config.Proxy)
		if err != nil {
			return fmt.Errorf("failed to initialize proxy: %w", err)
		}
		app.proxyManager = pm
		log.Printf("Initialized %d proxy listeners", len(app.config.Proxy))
	}

	// Convert config shortcuts to handler format
	shortcuts := make([]handlers.ShortcutConfig, len(cfg.Terminal.Shortcuts))
	for i, s := range cfg.Terminal.Shortcuts {
		shortcuts[i] = handlers.ShortcutConfig{
			Key:    s.Key,
			Window: s.Window,
		}
	}

	// Convert config notifications to handler format
	notifications := handlers.NotificationConfig{
		Enabled:      cfg.UI.Notifications.Enabled,
		Events:       cfg.UI.Notifications.Events,
		FailuresOnly: cfg.UI.Notifications.FailuresOnly,
		Sound:        cfg.UI.Notifications.Sound,
	}

	// Convert config links to handler format
	links := make([]handlers.LinkConfig, len(cfg.Terminal.Links))
	for i, l := range cfg.Terminal.Links {
		links[i] = handlers.LinkConfig{
			Name: l.Name,
			URL:  l.URL,
		}
	}

	// Initialize API server
	app.apiServer = api.NewServer(
		api.ServerConfig{
			Host:    cfg.Server.Host,
			Port:    cfg.Server.Port,
			TLSCert: cfg.Server.TLSCert,
			TLSKey:  cfg.Server.TLSKey,
		},
		api.Dependencies{
			ServiceManager:  app.serviceManager,
			WorktreeManager: app.worktreeManager,
			WorkflowRunner:  app.workflowRunner,
			TerminalManager: app.terminalManager,
			LogManager:      app.logManager,
			TraceManager:    app.traceManager,
			CrashManager:    app.crashManager,
			EventBus:        app.eventBus,
			ClaudeManager:   app.claudeManager,
			CaseManager:     app.caseManager,
			VSCodeHandler:   app.vsCodeHandler,
			Shortcuts:       shortcuts,
			Notifications:   notifications,
			Links:           links,
			Version:         app.version,
		},
	)

	return nil
}

// Start starts all components.
func (app *App) Start(ctx context.Context) error {
	// Restore terminal sessions from saved state
	if saved := app.terminalManager.LoadSavedWindows(); len(saved) > 0 {
		worktrees, _ := app.worktreeManager.List()
		// Build map of session name → workdir from worktrees
		sessionWorkdirs := make(map[string]string)
		projectPrefix := terminal.ToTmuxSessionName(app.config.Project.Name)
		for _, wt := range worktrees {
			var sessionName string
			if wt.Name() == app.config.Project.Name || terminal.ToTmuxSessionName(wt.Name()) == projectPrefix {
				sessionName = projectPrefix
			} else {
				sessionName = projectPrefix + "-" + wt.Name()
			}
			sessionWorkdirs[sessionName] = wt.Path
		}

		for session, windowNames := range saved {
			workdir := sessionWorkdirs[session]
			if workdir == "" {
				log.Printf("Terminal restore: no workdir found for session %s, skipping", session)
				continue
			}
			windows := make([]terminal.WindowConfig, len(windowNames))
			for i, name := range windowNames {
				windows[i] = terminal.WindowConfig{Name: name}
			}
			if err := app.terminalManager.EnsureSession(ctx, session, workdir, windows); err != nil {
				log.Printf("Warning: failed to restore terminal session %s: %v", session, err)
			} else {
				log.Printf("Restored terminal session %s with %d windows", session, len(windows))
			}
		}
	}

	// Start services
	if err := app.serviceManager.StartAll(ctx); err != nil {
		log.Printf("Warning: failed to start some services: %v", err)
	}

	// Start log viewers
	if app.logManager != nil {
		if err := app.logManager.Start(ctx); err != nil {
			log.Printf("Warning: failed to start log viewers: %v", err)
		}
	}

	// Start proxy listeners
	if app.proxyManager != nil {
		if err := app.proxyManager.Start(ctx); err != nil {
			log.Printf("Warning: failed to start proxy listeners: %v", err)
		}
	}

	// Start API server in background
	go func() {
		log.Printf("Starting API server on %s:%d", app.config.Server.Host, app.config.Server.Port)
		if err := app.apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("API server error: %v", err)
		}
	}()

	return nil
}

// Run starts the app and blocks until shutdown.
func (app *App) Run(ctx context.Context) error {
	if err := app.Initialize(ctx); err != nil {
		return err
	}

	if err := app.Start(ctx); err != nil {
		return err
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received signal %v, shutting down...", sig)
	case <-ctx.Done():
		log.Printf("Context cancelled, shutting down...")
	case <-app.done:
		log.Printf("Shutdown requested...")
	}

	return app.Shutdown(context.Background())
}

// Shutdown gracefully shuts down all components.
func (app *App) Shutdown(ctx context.Context) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	log.Println("Shutting down...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Stop API server first to stop accepting new requests
	if app.apiServer != nil {
		if err := app.apiServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Error shutting down API server: %v", err)
		}
	}

	// Stop proxy listeners
	if app.proxyManager != nil {
		if err := app.proxyManager.Shutdown(shutdownCtx); err != nil {
			log.Printf("Error shutting down proxy listeners: %v", err)
		}
	}

	// Stop binary watcher
	if app.binaryWatcher != nil {
		app.binaryWatcher.Close()
	}

	// Stop workflow runner (cancels running workflows and stops cleanup goroutine)
	if app.workflowRunner != nil {
		app.workflowRunner.Close()
	}

	// Stop trace manager (stops cleanup goroutine)
	if app.traceManager != nil {
		app.traceManager.Close()
	}

	// Stop log viewers
	if app.logManager != nil {
		app.logManager.Stop()
	}

	// Stop Claude sessions
	if app.claudeManager != nil {
		app.claudeManager.Shutdown()
	}

	// Stop all services
	if app.serviceManager != nil {
		if err := app.serviceManager.StopAll(shutdownCtx); err != nil {
			log.Printf("Error stopping services: %v", err)
		}
	}

	// Stop VS Code handler
	if app.vsCodeHandler != nil {
		app.vsCodeHandler.Stop()
	}

	// Close event bus
	if app.eventBus != nil {
		app.eventBus.Close()
	}

	log.Println("Shutdown complete")
	return nil
}

// Stop signals the app to shut down. Safe to call multiple times.
func (app *App) Stop() {
	app.stopOnce.Do(func() {
		close(app.done)
	})
}

// serviceControllerAdapter adapts service.Manager to workflow.ServiceController.
type serviceControllerAdapter struct {
	mgr service.Manager
}

func (a *serviceControllerAdapter) StopServices(ctx context.Context, names []string) error {
	if names == nil {
		return a.mgr.StopAll(ctx)
	}
	for _, name := range names {
		if err := a.mgr.Stop(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

func (a *serviceControllerAdapter) StartAllServices(ctx context.Context) error {
	return a.mgr.StartAll(ctx)
}

func (a *serviceControllerAdapter) RestartAllServices(ctx context.Context) error {
	if err := a.mgr.StopAll(ctx); err != nil {
		return err
	}
	return a.mgr.StartAll(ctx)
}

func (a *serviceControllerAdapter) StopWatchedServices(ctx context.Context) error {
	return a.mgr.StopWatched(ctx)
}

func (a *serviceControllerAdapter) RestartWatchedServices(ctx context.Context) error {
	if err := a.mgr.StopWatched(ctx); err != nil {
		return err
	}
	return a.mgr.StartWatched(ctx)
}

func (a *serviceControllerAdapter) ClearAllLogs(ctx context.Context) error {
	for _, svc := range a.mgr.List() {
		a.mgr.ClearLogs(svc.Name)
	}
	return nil
}

// getCommandAsStrings converts a command interface to a string slice.
// Supports both string (shell-style) and array commands.
func getCommandAsStrings(cmd interface{}) []string {
	switch c := cmd.(type) {
	case string:
		if c == "" {
			return nil
		}
		// Use shell to execute string commands
		return []string{"sh", "-c", c}
	case []string:
		return c
	case []interface{}:
		result := make([]string, 0, len(c))
		for _, v := range c {
			switch s := v.(type) {
			case string:
				result = append(result, s)
			case float64:
				// JSON/HJSON numbers are float64
				result = append(result, fmt.Sprintf("%v", s))
			case int:
				result = append(result, fmt.Sprintf("%d", s))
			case bool:
				result = append(result, fmt.Sprintf("%v", s))
			default:
				// Skip nil and other unsupported types
				if v != nil {
					result = append(result, fmt.Sprintf("%v", v))
				}
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	default:
		return nil
	}
}

// getCommandsAsArray parses the commands field which is an array of commands.
// Each command must be an array of strings.
func getCommandsAsArray(cmds interface{}) [][]string {
	if cmds == nil {
		return nil
	}

	arr, ok := cmds.([]interface{})
	if !ok {
		return nil
	}

	result := make([][]string, 0, len(arr))
	for _, cmd := range arr {
		parsed := getCommandAsStrings(cmd)
		if len(parsed) > 0 {
			result = append(result, parsed)
		}
	}
	return result
}

// convertWorkflowInputs converts config.WorkflowInput to workflow.WorkflowInput.
func convertWorkflowInputs(inputs []config.WorkflowInput) []workflow.WorkflowInput {
	if len(inputs) == 0 {
		return nil
	}
	result := make([]workflow.WorkflowInput, len(inputs))
	for i, input := range inputs {
		result[i] = workflow.WorkflowInput{
			Name:          input.Name,
			Type:          input.Type,
			Label:         input.Label,
			Description:   input.Description,
			Placeholder:   input.Placeholder,
			Options:       input.Options,
			AllowedValues: input.AllowedValues,
			Pattern:       input.Pattern,
			Default:       input.Default,
			Required:      input.Required,
		}
	}
	return result
}

// serviceLogAdapter implements logs.ServiceLogProvider by delegating to service.Manager.
type serviceLogAdapter struct {
	mgr service.Manager
}

func (a *serviceLogAdapter) ServiceLogs(name string, n int) ([]string, error) {
	return a.mgr.Logs(name, n)
}

func (a *serviceLogAdapter) ServiceLogSize(name string) (int, error) {
	return a.mgr.LogSize(name)
}

// createServiceLogViewers creates svc:* log viewers from running services' in-memory
// log buffers. Only services with a parser configured (after applying LoggingDefaults)
// are included, since services without a parser can't participate in structured tracing.
// Returns the list of viewer names created.
func (app *App) createServiceLogViewers(cfg *config.Config) []string {
	provider := &serviceLogAdapter{mgr: app.serviceManager}
	var viewerNames []string

	for _, svcCfg := range cfg.Services {
		// Apply logging defaults to get merged config
		logging := svcCfg.Logging
		logging.ApplyDefaults(&cfg.LoggingDefaults)

		// Skip services without a parser — they can't participate in structured tracing
		if logging.Parser.Type == "" {
			continue
		}

		viewerName := "svc:" + svcCfg.Name

		// Build LogViewerConfig from the service's logging config
		viewerCfg := config.LogViewerConfig{
			Name:   viewerName,
			Parser: logging.Parser,
			Derive: logging.Derive,
			Layout: logging.Layout,
		}

		// Create source and viewer
		source := logs.NewServiceSource(svcCfg.Name, provider)
		viewer, err := logs.NewViewerWithSource(viewerCfg, source)
		if err != nil {
			log.Printf("Warning: failed to create service log viewer for %s: %v", svcCfg.Name, err)
			continue
		}

		app.logManager.AddViewer(viewer)
		viewerNames = append(viewerNames, viewerName)

		// Append config to cfg.LogViewers so trace manager's logViewerConfig map
		// picks up the parser.id field for two-pass ID expansion
		cfg.LogViewers = append(cfg.LogViewers, viewerCfg)
	}

	if len(viewerNames) > 0 {
		log.Printf("Created %d service log viewers: %v", len(viewerNames), viewerNames)
	}

	return viewerNames
}

// injectServicesTraceGroup ensures a "services" trace group exists that includes
// all svc:* viewers. If the group already exists in config, the service viewer
// names are appended; otherwise a new group is created.
func (app *App) injectServicesTraceGroup(cfg *config.Config, viewerNames []string) {
	if len(viewerNames) == 0 {
		return
	}

	// Check if a "services" group already exists
	for i := range cfg.TraceGroups {
		if cfg.TraceGroups[i].Name == "services" {
			// Append service viewer names (avoid duplicates)
			existing := make(map[string]bool)
			for _, name := range cfg.TraceGroups[i].LogViewers {
				existing[name] = true
			}
			for _, name := range viewerNames {
				if !existing[name] {
					cfg.TraceGroups[i].LogViewers = append(cfg.TraceGroups[i].LogViewers, name)
				}
			}
			log.Printf("Updated 'services' trace group with %d viewers", len(cfg.TraceGroups[i].LogViewers))
			return
		}
	}

	// Create a new "services" trace group
	cfg.TraceGroups = append(cfg.TraceGroups, config.TraceGroupConfig{
		Name:       "services",
		LogViewers: viewerNames,
	})
	log.Printf("Created 'services' trace group with %d viewers", len(viewerNames))
}
