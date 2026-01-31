// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package config handles HJSON configuration loading and template expansion.
package config

import (
	"strings"
	"time"
)

// Config is the root configuration structure for Trellis.
type Config struct {
	Version    string            `json:"version"`
	Project    ProjectConfig     `json:"project"`
	Server     ServerConfig      `json:"server"`
	Worktree   WorktreeConfig    `json:"worktree"`
	Services   []ServiceConfig   `json:"services"`
	Workflows  []WorkflowConfig  `json:"workflows"`
	Terminal   TerminalConfig    `json:"terminal"`
	Events     EventsConfig      `json:"events"`
	Watch      WatchConfig       `json:"watch"`
	Logging    LoggingConfig     `json:"logging"`
	UI         UIConfig          `json:"ui"`
	LoggingDefaults   LoggingDefaultsConfig `json:"logging_defaults"`
	LogViewers        []LogViewerConfig     `json:"log_viewers"`
	LogViewerSettings LogViewerSettings     `json:"log_viewer_settings"`
	Trace             TraceConfig           `json:"trace"`
	TraceGroups       []TraceGroupConfig    `json:"trace_groups"`
	Crashes           CrashesConfig         `json:"crashes"`
}

// LoggingDefaultsConfig provides default parser, derive, and layout settings
// for log_viewers and services.logging that don't specify their own.
type LoggingDefaultsConfig struct {
	Parser LogParserConfig          `json:"parser"`
	Derive map[string]DeriveConfig  `json:"derive"`
	Layout []LayoutColumnConfig     `json:"layout"`
}

// LogViewerSettings configures log viewer behavior.
type LogViewerSettings struct {
	IdleTimeout string `json:"idle_timeout"` // Duration after which idle viewers are stopped (e.g., "5m", "0" to disable)
}

// ProjectConfig contains project metadata.
type ProjectConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ServerConfig configures the HTTP server.
type ServerConfig struct {
	Port    int    `json:"port"`
	Host    string `json:"host"`
	TLSCert string `json:"tls_cert"` // Path to TLS certificate file (enables HTTPS if both cert and key set)
	TLSKey  string `json:"tls_key"`  // Path to TLS private key file
}

// WorktreeConfig configures worktree management.
type WorktreeConfig struct {
	RepoDir   string           `json:"repo_dir"`   // Directory for git worktree discovery (defaults to config file dir)
	CreateDir string           `json:"create_dir"` // Directory where new worktrees are created (defaults to parent of repo_dir)
	Discovery DiscoveryConfig  `json:"discovery"`
	Binaries  BinariesConfig   `json:"binaries"`
	Lifecycle LifecycleConfig  `json:"lifecycle"`
}

// DiscoveryConfig configures worktree discovery.
type DiscoveryConfig struct {
	Mode string `json:"mode"` // "git"
}

// BinariesConfig configures binary locations.
type BinariesConfig struct {
	Path string `json:"path"`
}

// LifecycleConfig configures worktree lifecycle hooks.
type LifecycleConfig struct {
	OnCreate    []HookConfig `json:"on_create"`    // Run once when worktree is first created
	PreActivate []HookConfig `json:"pre_activate"` // Run before each activation
}

// HookConfig defines a lifecycle hook command.
type HookConfig struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
	Timeout string   `json:"timeout"`
}

// ServiceConfig defines a managed service.
type ServiceConfig struct {
	Name          string               `json:"name"`
	Command       interface{}          `json:"command"` // string or []string
	Args          []string             `json:"args"`
	WorkDir       string               `json:"work_dir"`
	Env           map[string]string    `json:"env"`
	Restart       RestartConfig        `json:"restart"`
	RestartPolicy string               `json:"restart_policy"` // "always", "on-failure", "never"
	RestartDelay  string               `json:"restart_delay"`
	MaxRestarts   int                  `json:"max_restarts"`
	StopSignal    string               `json:"stop_signal"`
	StopTimeout   string               `json:"stop_timeout"`
	Watching      *bool                `json:"watching"`
	WatchBinary   string               `json:"watch_binary"`
	WatchFiles    []string             `json:"watch_files"`
	Debug         string               `json:"debug"`
	Logging       ServiceLoggingConfig `json:"logging"`
	LogBufferSize int                  `json:"log_buffer_size"`
	Enabled       *bool                `json:"enabled"`
	Disabled      *bool                `json:"disabled"`
	DependsOn     []string             `json:"depends_on"`
}

// RestartConfig configures restart behavior.
type RestartConfig struct {
	Policy string `json:"policy"` // "always", "on_failure", "never"
}

// ServiceLoggingConfig configures per-service logging.
type ServiceLoggingConfig struct {
	BufferSize int                        `json:"buffer_size"`
	Parser     LogParserConfig            `json:"parser"`
	Derive     map[string]DeriveConfig    `json:"derive"` // Derived fields computed from parsed fields
	Layout     []LayoutColumnConfig       `json:"layout"` // Columns to display (in order)
}

// WorkflowInput defines a parameter that prompts the user before execution.
type WorkflowInput struct {
	Name          string   `json:"name"`           // Variable name for templates
	Type          string   `json:"type"`           // "text", "select", "checkbox", "datepicker"
	Label         string   `json:"label"`          // Display label
	Description   string   `json:"description"`    // Description for CLI help
	Placeholder   string   `json:"placeholder"`    // Placeholder for text inputs
	Options       []string `json:"options"`        // Options for select type
	AllowedValues []string `json:"allowed_values"` // Whitelist of allowed values (for validation)
	Pattern       string   `json:"pattern"`        // Regex pattern for validation
	Default       any      `json:"default"`        // Default value
	Required      bool     `json:"required"`       // Whether required
}

// WorkflowConfig defines a workflow action.
type WorkflowConfig struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description"` // Description for CLI help
	Command         interface{}     `json:"command"`     // string or []string (single command, for backwards compat)
	Commands        interface{}     `json:"commands"`    // array of commands to run in sequence
	Timeout         string          `json:"timeout"`
	OutputParser    string          `json:"output_parser"`
	Confirm         bool            `json:"confirm"`
	ConfirmMessage  string          `json:"confirm_message"`
	RequiresStopped []string        `json:"requires_stopped"`
	RestartServices bool            `json:"restart_services"`
	Inputs          []WorkflowInput `json:"inputs"` // Input parameters to prompt user for
}

// CrashesConfig configures crash history storage.
type CrashesConfig struct {
	ReportsDir string `json:"reports_dir"` // Directory to store crash files (default: .trellis/crashes)
	MaxAge     string `json:"max_age"`     // Max age of crashes to keep (default: 7d)
	MaxCount   int    `json:"max_count"`   // Max number of crashes to keep (default: 100)
}

// TerminalConfig configures the terminal system.
type TerminalConfig struct {
	Backend        string               `json:"backend"` // "tmux"
	Tmux           TmuxConfig           `json:"tmux"`
	DefaultWindows []WindowConfig       `json:"default_windows"`
	RemoteWindows  []RemoteWindowConfig `json:"remote_windows"`
	VSCode         *VSCodeConfig        `json:"vscode"`
	Shortcuts      []ShortcutConfig     `json:"shortcuts"`
	Links          []LinkConfig         `json:"links"`
}

// LinkConfig defines a link that appears in the terminal picker.
// Links open in new browser tabs when selected.
type LinkConfig struct {
	Name string `json:"name"` // Display name in picker
	URL  string `json:"url"`  // URL to open in new browser tab
}

// ShortcutConfig defines a keyboard shortcut to a terminal window.
// Window must start with a prefix character indicating the target type:
//   ~name           = log viewer (e.g., ~nginx)
//   #name           = service (e.g., #api)
//   @worktree - win = local terminal (e.g., @main - dev)
//   !name           = remote window (e.g., !admin)
type ShortcutConfig struct {
	Key    string `json:"key"`    // Key combo like "cmd+l", "ctrl+shift+1"
	Window string `json:"window"` // Prefixed target: ~log, #service, @worktree - window, !remote
}

// TmuxConfig configures tmux settings.
type TmuxConfig struct {
	HistoryLimit int    `json:"history_limit"`
	Shell        string `json:"shell"`
}

// WindowConfig defines a terminal window.
type WindowConfig struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// RemoteWindowConfig defines a remote terminal window.
// Either Command or (SSHHost + TmuxSession) must be set.
type RemoteWindowConfig struct {
	Name        string   `json:"name"`
	Command     []string `json:"command"`      // Full command (mutually exclusive with ssh_host/tmux_session)
	SSHHost     string   `json:"ssh_host"`     // SSH host for tmux connection
	TmuxSession string   `json:"tmux_session"` // Tmux session name on remote host
}

// GetCommand returns the command to execute for this remote window.
// If SSHHost and TmuxSession are set, builds the ssh+tmux command automatically.
func (r *RemoteWindowConfig) GetCommand() []string {
	if len(r.Command) > 0 {
		return r.Command
	}
	if r.SSHHost != "" && r.TmuxSession != "" {
		// Build: ssh -t <host> 'tmux new -A -s <session> \; set -g status off'
		tmuxCmd := "tmux new -A -s " + r.TmuxSession + " \\; set -g status off"
		return []string{"ssh", "-t", r.SSHHost, tmuxCmd}
	}
	return nil
}

// VSCodeConfig configures VS Code integration.
type VSCodeConfig struct {
	Binary      string `json:"binary"`
	Port        int    `json:"port"`
	UserDataDir string `json:"user_data_dir"` // Path to VS Code user data (settings, keybindings, etc.)
}

// EventsConfig configures the event system.
type EventsConfig struct {
	History  HistoryConfig   `json:"history"`
	Webhooks []WebhookConfig `json:"webhooks"`
}

// HistoryConfig configures event history retention.
type HistoryConfig struct {
	MaxEvents int    `json:"max_events"`
	MaxAge    string `json:"max_age"`
}

// WebhookConfig defines an event webhook.
type WebhookConfig struct {
	ID     string   `json:"id"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// WatchConfig configures file watching.
type WatchConfig struct {
	Debounce string `json:"debounce"`
}

// LoggingConfig configures application logging.
type LoggingConfig struct {
	Level  string `json:"level"`  // "debug", "info", "warn", "error"
	Format string `json:"format"` // "json", "text"
}

// UIConfig configures the web interface.
type UIConfig struct {
	Theme         string              `json:"theme"` // "light", "dark", "auto"
	Terminal      UITerminalConfig    `json:"terminal"`
	Notifications NotificationConfig  `json:"notifications"`
	Editor        EditorConfig        `json:"editor"`
	LogTerminal   string              `json:"log_terminal"`
}

// UITerminalConfig configures terminal appearance.
type UITerminalConfig struct {
	FontFamily  string `json:"font_family"`
	FontSize    int    `json:"font_size"`
	CursorBlink bool   `json:"cursor_blink"`
}

// NotificationConfig configures browser notifications.
type NotificationConfig struct {
	Enabled      bool     `json:"enabled"`
	Events       []string `json:"events"`
	FailuresOnly bool     `json:"failures_only"`
	Sound        bool     `json:"sound"`
}

// EditorConfig configures external editor integration.
type EditorConfig struct {
	RemoteHost string `json:"remote_host"`
}

// TemplateContext provides data for template expansion.
type TemplateContext struct {
	Worktree WorktreeTemplateData
	Project  ProjectTemplateData
	Service  *ServiceTemplateData
	Inputs   map[string]any // User-supplied workflow input values
}

// WorktreeTemplateData provides worktree data for templates.
type WorktreeTemplateData struct {
	Root     string
	Name     string
	Branch   string
	Binaries string
}

// ProjectTemplateData provides project data for templates.
type ProjectTemplateData struct {
	Root string
	Name string
}

// ServiceTemplateData provides service data for templates.
type ServiceTemplateData struct {
	Name string
}

// ParseDuration parses a duration string, returning a default if empty.
func ParseDuration(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}

// IsWatching returns whether the service should be watched for binary changes.
func (s *ServiceConfig) IsWatching() bool {
	if s.Watching == nil {
		return true // Default to true
	}
	return *s.Watching
}

// IsEnabled returns whether the service is enabled.
// Checks both enabled and disabled fields for backwards compatibility.
func (s *ServiceConfig) IsEnabled() bool {
	// Check disabled first (explicit disable takes precedence)
	if s.Disabled != nil && *s.Disabled {
		return false
	}
	// Then check enabled
	if s.Enabled != nil {
		return *s.Enabled
	}
	return true // Default to true
}

// GetRestartPolicy returns the normalized restart policy.
// Handles both restart.policy and restart_policy fields, and normalizes
// on_failure/on-failure to "on-failure" for consistency.
func (s *ServiceConfig) GetRestartPolicy() string {
	// Check nested restart.policy first
	policy := s.Restart.Policy
	// Fall back to top-level restart_policy
	if policy == "" {
		policy = s.RestartPolicy
	}
	// Normalize: convert on_failure to on-failure
	if policy == "on_failure" {
		return "on-failure"
	}
	return policy
}

// GetCommand returns the command as a slice of strings.
func (s *ServiceConfig) GetCommand() []string {
	switch cmd := s.Command.(type) {
	case string:
		// Split on whitespace
		return splitCommand(cmd)
	case []interface{}:
		result := make([]string, 0, len(cmd))
		for _, v := range cmd {
			if str, ok := v.(string); ok {
				result = append(result, str)
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	case []string:
		return cmd
	default:
		return nil
	}
}

// GetBinaryPath returns the path to the binary to watch.
func (s *ServiceConfig) GetBinaryPath() string {
	if s.WatchBinary != "" {
		return s.WatchBinary
	}
	cmd := s.GetCommand()
	if len(cmd) > 0 {
		return cmd[0]
	}
	return ""
}

// splitCommand splits a command string on whitespace, respecting quoted strings.
// Supports both single and double quotes.
func splitCommand(cmd string) []string {
	var result []string
	var current strings.Builder
	var inQuote rune
	var escape bool

	for _, r := range cmd {
		if escape {
			current.WriteRune(r)
			escape = false
			continue
		}

		if r == '\\' && inQuote != '\'' {
			escape = true
			continue
		}

		if inQuote != 0 {
			if r == inQuote {
				inQuote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}

		if r == '"' || r == '\'' {
			inQuote = r
			continue
		}

		if r == ' ' || r == '\t' {
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
			continue
		}

		current.WriteRune(r)
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}
	return result
}

// LogViewerConfig defines a log viewer configuration.
type LogViewerConfig struct {
	Name    string                     `json:"name"`
	Source  LogSourceConfig            `json:"source"`
	Parser  LogParserConfig            `json:"parser"`
	Derive  map[string]DeriveConfig    `json:"derive"` // Derived fields computed from parsed fields
	Layout  []LayoutColumnConfig       `json:"layout"` // Columns to display (in order)
	Buffer  LogBufferConfig            `json:"buffer"`
}

// LogSourceConfig defines where logs come from.
type LogSourceConfig struct {
	Type           string   `json:"type"`            // "ssh", "file", "command", "docker", "kubernetes"
	Host           string   `json:"host"`            // SSH host
	Path           string   `json:"path"`            // Log directory or file path
	Current        string   `json:"current"`         // Active log file name (for SSH)
	RotatedPattern string   `json:"rotated_pattern"` // Glob pattern for rotated logs
	Decompress     string   `json:"decompress"`      // Command to decompress
	Command        []string `json:"command"`         // Command source: command to run
	Container      string   `json:"container"`       // Docker/K8s container name
	Namespace      string   `json:"namespace"`       // Kubernetes namespace
	Pod            string   `json:"pod"`             // Kubernetes pod name
	Follow         *bool    `json:"follow"`          // Follow log output
	Since          string   `json:"since"`           // How far back to start
}

// IsFollow returns whether to follow log output.
func (s *LogSourceConfig) IsFollow() bool {
	if s.Follow == nil {
		return true // Default to true
	}
	return *s.Follow
}

// LogParserConfig defines how to parse log lines.
type LogParserConfig struct {
	Type            string `json:"type"`             // "json", "logfmt", "regex", "syslog", "none"
	Timestamp       string `json:"timestamp"`        // Field name for timestamp
	Level           string `json:"level"`            // Field name for level
	Message         string `json:"message"`          // Field name for message
	TimestampFormat string `json:"timestamp_format"` // Go time format
	Pattern         string `json:"pattern"`          // Regex pattern (for regex parser)
	ID              string `json:"id,omitempty"`     // Field name containing entry ID for trace expansion
	Stack           string `json:"stack,omitempty"`  // Field name containing stack trace for crash reports
}

// DeriveConfig defines a derived field computed from parsed fields.
type DeriveConfig struct {
	From string                 `json:"from,omitempty"` // Source field name (for single-field ops like timefmt)
	Op   string                 `json:"op"`             // Operation: "fmt", "timefmt"
	Args map[string]interface{} `json:"args"`           // Operation-specific arguments
}

// LayoutColumnConfig defines a column in the log display layout.
type LayoutColumnConfig struct {
	Field     string   `json:"field,omitempty"`     // Field name (parsed or derived)
	Type      string   `json:"type,omitempty"`      // Column type: "" (default) or "kvpairs"
	Keys      []string `json:"keys,omitempty"`      // For kvpairs: ordered list of field keys to display
	MaxPairs  int      `json:"max_pairs,omitempty"` // For kvpairs: max pairs to show (0 = all)
	MinWidth  int      `json:"min_width,omitempty"` // Minimum width in characters
	MaxWidth  int      `json:"max_width,omitempty"` // Maximum width in characters (0 = fill)
	Align     string   `json:"align,omitempty"`     // "left", "right", "center" (default: left)
	Optional  bool     `json:"optional,omitempty"`  // Hide column if field is missing
	Timestamp bool     `json:"timestamp,omitempty"` // Use timestamp formatting (respects toggle)
}

// GetColumns returns column names from the layout configuration.
func (c *LogViewerConfig) GetColumns() []string {
	if len(c.Layout) == 0 {
		return nil
	}
	cols := make([]string, len(c.Layout))
	for i, col := range c.Layout {
		cols[i] = col.Field
	}
	return cols
}

// GetColumnWidths returns column widths (max_width) from the layout configuration.
func (c *LogViewerConfig) GetColumnWidths() map[string]int {
	if len(c.Layout) == 0 {
		return nil
	}
	widths := make(map[string]int)
	for _, col := range c.Layout {
		if col.MaxWidth > 0 {
			widths[col.Field] = col.MaxWidth
		}
	}
	return widths
}

// ApplyDefaults fills in missing parser, derive, and layout from defaults.
func (c *LogViewerConfig) ApplyDefaults(defaults *LoggingDefaultsConfig) {
	if defaults == nil {
		return
	}
	// Apply parser defaults (only if parser type not set)
	if c.Parser.Type == "" {
		c.Parser = mergeParserConfig(c.Parser, defaults.Parser)
	}
	// Apply derive defaults (merge, config takes precedence)
	if len(defaults.Derive) > 0 {
		if c.Derive == nil {
			c.Derive = make(map[string]DeriveConfig)
		}
		for k, v := range defaults.Derive {
			if _, exists := c.Derive[k]; !exists {
				c.Derive[k] = v
			}
		}
	}
	// Apply layout defaults (only if not specified)
	if len(c.Layout) == 0 && len(defaults.Layout) > 0 {
		c.Layout = defaults.Layout
	}
}

// GetColumns returns column names from the layout configuration.
func (c *ServiceLoggingConfig) GetColumns() []string {
	if len(c.Layout) == 0 {
		return nil
	}
	cols := make([]string, len(c.Layout))
	for i, col := range c.Layout {
		cols[i] = col.Field
	}
	return cols
}

// GetColumnWidths returns column widths (max_width) from the layout configuration.
func (c *ServiceLoggingConfig) GetColumnWidths() map[string]int {
	if len(c.Layout) == 0 {
		return nil
	}
	widths := make(map[string]int)
	for _, col := range c.Layout {
		if col.MaxWidth > 0 {
			widths[col.Field] = col.MaxWidth
		}
	}
	return widths
}

// ApplyDefaults fills in missing parser, derive, and layout from defaults.
func (c *ServiceLoggingConfig) ApplyDefaults(defaults *LoggingDefaultsConfig) {
	if defaults == nil {
		return
	}
	// Apply parser defaults (only if parser type not set)
	if c.Parser.Type == "" {
		c.Parser = mergeParserConfig(c.Parser, defaults.Parser)
	}
	// Apply derive defaults (merge, config takes precedence)
	if len(defaults.Derive) > 0 {
		if c.Derive == nil {
			c.Derive = make(map[string]DeriveConfig)
		}
		for k, v := range defaults.Derive {
			if _, exists := c.Derive[k]; !exists {
				c.Derive[k] = v
			}
		}
	}
	// Apply layout defaults (only if not specified)
	if len(c.Layout) == 0 && len(defaults.Layout) > 0 {
		c.Layout = defaults.Layout
	}
}

// mergeParserConfig merges two parser configs, with cfg taking precedence over defaults.
func mergeParserConfig(cfg, defaults LogParserConfig) LogParserConfig {
	if cfg.Type == "" {
		cfg.Type = defaults.Type
	}
	if cfg.Timestamp == "" {
		cfg.Timestamp = defaults.Timestamp
	}
	if cfg.Level == "" {
		cfg.Level = defaults.Level
	}
	if cfg.Message == "" {
		cfg.Message = defaults.Message
	}
	if cfg.TimestampFormat == "" {
		cfg.TimestampFormat = defaults.TimestampFormat
	}
	if cfg.Pattern == "" {
		cfg.Pattern = defaults.Pattern
	}
	if cfg.ID == "" {
		cfg.ID = defaults.ID
	}
	return cfg
}

// LogBufferConfig defines buffer settings.
type LogBufferConfig struct {
	MaxEntries int  `json:"max_entries"` // Max entries in memory
	Persist    bool `json:"persist"`     // Persist across restarts
}

// TraceConfig configures distributed tracing.
type TraceConfig struct {
	ReportsDir string `json:"reports_dir"` // Directory for trace reports
	MaxAge     string `json:"max_age"`     // How long to keep reports (e.g., "7d")
}

// TraceGroupConfig defines a named group of log viewers for tracing.
type TraceGroupConfig struct {
	Name       string   `json:"name"`        // Unique name for this trace group
	LogViewers []string `json:"log_viewers"` // Log viewer names to search
}

// BuildServiceIDFields builds a map of service name to ID field for trace ID extraction.
// Each service's logging config is merged with defaults to determine the ID field.
func BuildServiceIDFields(services []ServiceConfig, defaults *LoggingDefaultsConfig) map[string]string {
	result := make(map[string]string)
	for _, svc := range services {
		// Apply defaults to get the merged config
		logging := svc.Logging
		logging.ApplyDefaults(defaults)
		// Only include if the service has a non-empty ID field
		if logging.Parser.ID != "" {
			result[svc.Name] = logging.Parser.ID
		}
	}
	return result
}
