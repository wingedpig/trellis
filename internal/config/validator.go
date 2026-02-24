// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Validator validates configuration against schema rules.
type Validator struct{}

// NewValidator creates a new config validator.
func NewValidator() *Validator {
	return &Validator{}
}

// ValidationError contains multiple validation failures.
type ValidationError struct {
	Errors []FieldError
}

// FieldError represents a single field validation error.
type FieldError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	var msgs []string
	for _, fe := range e.Errors {
		msgs = append(msgs, fmt.Sprintf("%s: %s", fe.Field, fe.Message))
	}
	return strings.Join(msgs, "; ")
}

// IsEmpty returns true if there are no validation errors.
func (e *ValidationError) IsEmpty() bool {
	return len(e.Errors) == 0
}

// Add adds a field error.
func (e *ValidationError) Add(field, message string) {
	e.Errors = append(e.Errors, FieldError{Field: field, Message: message})
}

// Validate checks configuration validity.
func (v *Validator) Validate(cfg *Config) error {
	errs := &ValidationError{}

	v.validateRequired(cfg, errs)
	v.validateServer(cfg, errs)
	v.validateServices(cfg, errs)
	v.validateWorkflows(cfg, errs)
	v.validateTerminal(cfg, errs)
	v.validateLogging(cfg, errs)
	v.validateUI(cfg, errs)
	v.validateDurations(cfg, errs)
	v.validateCrossReferences(cfg, errs)
	v.validateTraceGroups(cfg, errs)
	v.validateProxy(cfg, errs)

	if errs.IsEmpty() {
		return nil
	}
	return errs
}

func (v *Validator) validateRequired(cfg *Config, errs *ValidationError) {
	if cfg.Version == "" {
		errs.Add("version", "is required")
	}
	if cfg.Project.Name == "" {
		errs.Add("project.name", "is required")
	}
}

func (v *Validator) validateServer(cfg *Config, errs *ValidationError) {
	if cfg.Server.Port != 0 {
		if cfg.Server.Port < 0 || cfg.Server.Port > 65535 {
			errs.Add("server.port", "must be between 0 and 65535")
		}
	}
}

func (v *Validator) validateServices(cfg *Config, errs *ValidationError) {
	seenNames := make(map[string]bool)

	for i, svc := range cfg.Services {
		prefix := fmt.Sprintf("services[%d]", i)

		if svc.Name == "" {
			errs.Add(prefix+".name", "is required")
		} else if seenNames[svc.Name] {
			errs.Add(prefix+".name", fmt.Sprintf("duplicate service name '%s'", svc.Name))
		} else {
			seenNames[svc.Name] = true
		}

		if svc.Command == nil || svc.Command == "" {
			errs.Add(prefix+".command", "is required")
		}

		// Validate restart policy (can be in restart.policy or restart_policy)
		restartPolicy := svc.Restart.Policy
		if restartPolicy == "" {
			restartPolicy = svc.RestartPolicy
		}
		if restartPolicy != "" {
			validPolicies := map[string]bool{
				"always":     true,
				"on_failure": true,
				"on-failure": true, // Allow both underscore and hyphen
				"never":      true,
			}
			if !validPolicies[restartPolicy] {
				field := prefix + ".restart.policy"
				if svc.Restart.Policy == "" {
					field = prefix + ".restart_policy"
				}
				errs.Add(field, fmt.Sprintf("invalid policy '%s', must be one of: always, on_failure, on-failure, never", restartPolicy))
			}
		}
	}
}

func (v *Validator) validateWorkflows(cfg *Config, errs *ValidationError) {
	seenIDs := make(map[string]bool)
	validParsers := map[string]bool{
		"":             true,
		"go":           true,
		"go_test_json": true,
		"generic":      true,
		"none":         true,
		"html":         true,
	}

	for i, wf := range cfg.Workflows {
		prefix := fmt.Sprintf("workflows[%d]", i)

		if wf.ID == "" {
			errs.Add(prefix+".id", "is required")
		} else if seenIDs[wf.ID] {
			errs.Add(prefix+".id", fmt.Sprintf("duplicate workflow id '%s'", wf.ID))
		} else {
			seenIDs[wf.ID] = true
		}

		if wf.Name == "" {
			errs.Add(prefix+".name", "is required")
		}

		// Either command or commands must be set
		hasCommand := false
		if wf.Command != nil {
			switch cmd := wf.Command.(type) {
			case []interface{}:
				hasCommand = len(cmd) > 0
			case []string:
				hasCommand = len(cmd) > 0
			case string:
				hasCommand = cmd != ""
			}
		}
		hasCommands := false
		if arr, ok := wf.Commands.([]interface{}); ok {
			hasCommands = len(arr) > 0
		}
		if !hasCommand && !hasCommands {
			errs.Add(prefix+".command", "either command or commands is required")
		}

		if !validParsers[wf.OutputParser] {
			errs.Add(prefix+".output_parser", fmt.Sprintf("invalid parser '%s', must be one of: go, go_test_json, generic, none, html", wf.OutputParser))
		}
	}
}

func (v *Validator) validateTerminal(cfg *Config, errs *ValidationError) {
	if cfg.Terminal.Backend != "" && cfg.Terminal.Backend != "tmux" {
		errs.Add("terminal.backend", "must be 'tmux' (only supported backend)")
	}
}

func (v *Validator) validateLogging(cfg *Config, errs *ValidationError) {
	if cfg.Logging.Level != "" {
		validLevels := map[string]bool{
			"debug": true,
			"info":  true,
			"warn":  true,
			"error": true,
		}
		if !validLevels[cfg.Logging.Level] {
			errs.Add("logging.level", fmt.Sprintf("invalid level '%s', must be one of: debug, info, warn, error", cfg.Logging.Level))
		}
	}

	if cfg.Logging.Format != "" {
		validFormats := map[string]bool{
			"json": true,
			"text": true,
		}
		if !validFormats[cfg.Logging.Format] {
			errs.Add("logging.format", fmt.Sprintf("invalid format '%s', must be one of: json, text", cfg.Logging.Format))
		}
	}
}

func (v *Validator) validateUI(cfg *Config, errs *ValidationError) {
	if cfg.UI.Theme != "" {
		validThemes := map[string]bool{
			"light": true,
			"dark":  true,
			"auto":  true,
		}
		if !validThemes[cfg.UI.Theme] {
			errs.Add("ui.theme", fmt.Sprintf("invalid theme '%s', must be one of: light, dark, auto", cfg.UI.Theme))
		}
	}
}

func (v *Validator) validateDurations(cfg *Config, errs *ValidationError) {
	// Validate watch debounce
	if cfg.Watch.Debounce != "" {
		d, err := time.ParseDuration(cfg.Watch.Debounce)
		if err != nil {
			errs.Add("watch.debounce", fmt.Sprintf("invalid duration format: %s", err))
		} else if d < 0 {
			errs.Add("watch.debounce", "must be positive")
		}
	}

	// Validate event history max_age
	if cfg.Events.History.MaxAge != "" {
		d, err := time.ParseDuration(cfg.Events.History.MaxAge)
		if err != nil {
			errs.Add("events.history.max_age", fmt.Sprintf("invalid duration format: %s", err))
		} else if d < 0 {
			errs.Add("events.history.max_age", "must be positive")
		}
	}

	// Validate workflow timeouts
	for i, wf := range cfg.Workflows {
		if wf.Timeout != "" {
			d, err := time.ParseDuration(wf.Timeout)
			if err != nil {
				errs.Add(fmt.Sprintf("workflows[%d].timeout", i), fmt.Sprintf("invalid duration format: %s", err))
			} else if d < 0 {
				errs.Add(fmt.Sprintf("workflows[%d].timeout", i), "must be positive")
			}
		}
	}

	// Validate lifecycle hook timeouts
	for i, hook := range cfg.Worktree.Lifecycle.OnCreate {
		if hook.Timeout != "" {
			d, err := time.ParseDuration(hook.Timeout)
			if err != nil {
				errs.Add(fmt.Sprintf("worktree.lifecycle.on_create[%d].timeout", i), fmt.Sprintf("invalid duration format: %s", err))
			} else if d < 0 {
				errs.Add(fmt.Sprintf("worktree.lifecycle.on_create[%d].timeout", i), "must be positive")
			}
		}
	}
	for i, hook := range cfg.Worktree.Lifecycle.PreActivate {
		if hook.Timeout != "" {
			d, err := time.ParseDuration(hook.Timeout)
			if err != nil {
				errs.Add(fmt.Sprintf("worktree.lifecycle.pre_activate[%d].timeout", i), fmt.Sprintf("invalid duration format: %s", err))
			} else if d < 0 {
				errs.Add(fmt.Sprintf("worktree.lifecycle.pre_activate[%d].timeout", i), "must be positive")
			}
		}
	}
}

func (v *Validator) validateCrossReferences(cfg *Config, errs *ValidationError) {
	// Build set of service names
	serviceNames := make(map[string]bool)
	for _, svc := range cfg.Services {
		serviceNames[svc.Name] = true
	}

	// Validate requires_stopped references valid services
	for i, wf := range cfg.Workflows {
		for _, svcName := range wf.RequiresStopped {
			if !serviceNames[svcName] {
				errs.Add(fmt.Sprintf("workflows[%d].requires_stopped", i),
					fmt.Sprintf("references unknown service '%s'", svcName))
			}
		}
	}
}

func (v *Validator) validateTraceGroups(cfg *Config, errs *ValidationError) {
	// Build set of log viewer names
	logViewerNames := make(map[string]bool)
	for _, lv := range cfg.LogViewers {
		logViewerNames[lv.Name] = true
	}

	// Track seen trace group names for uniqueness check
	seenNames := make(map[string]bool)

	for i, tg := range cfg.TraceGroups {
		prefix := fmt.Sprintf("trace_groups[%d]", i)

		// Validate name is present and unique
		if tg.Name == "" {
			errs.Add(prefix+".name", "is required")
		} else if seenNames[tg.Name] {
			errs.Add(prefix+".name", fmt.Sprintf("duplicate trace group name '%s'", tg.Name))
		} else {
			seenNames[tg.Name] = true
		}

		// Validate log_viewers is not empty
		if len(tg.LogViewers) == 0 {
			errs.Add(prefix+".log_viewers", "must have at least one log viewer")
		}

		// Validate each log viewer reference exists
		for j, lvName := range tg.LogViewers {
			if !logViewerNames[lvName] {
				errs.Add(fmt.Sprintf("%s.log_viewers[%d]", prefix, j),
					fmt.Sprintf("references unknown log viewer '%s'", lvName))
			}
		}
	}

	// Validate trace max_age duration if specified
	if cfg.Trace.MaxAge != "" {
		d, err := parseDurationWithDays(cfg.Trace.MaxAge)
		if err != nil {
			errs.Add("trace.max_age", fmt.Sprintf("invalid duration format: %s", err))
		} else if d < 0 {
			errs.Add("trace.max_age", "must be positive")
		}
	}
}

func (v *Validator) validateProxy(cfg *Config, errs *ValidationError) {
	for i, listener := range cfg.Proxy {
		prefix := fmt.Sprintf("proxy[%d]", i)

		if listener.Listen == "" {
			errs.Add(prefix+".listen", "is required")
		}
		if len(listener.Routes) == 0 {
			errs.Add(prefix+".routes", "must have at least one route")
		}

		// Validate TLS: tls_tailscale and tls_cert/tls_key are mutually exclusive
		hasCertKey := listener.TLSCert != "" || listener.TLSKey != ""
		if listener.TLSTailscale && hasCertKey {
			errs.Add(prefix, "tls_tailscale and tls_cert/tls_key are mutually exclusive")
		}
		// If using cert/key, both must be specified
		if !listener.TLSTailscale && (listener.TLSCert == "") != (listener.TLSKey == "") {
			errs.Add(prefix, "both tls_cert and tls_key must be specified together")
		}

		for j, route := range listener.Routes {
			routePrefix := fmt.Sprintf("%s.routes[%d]", prefix, j)
			if route.Upstream == "" {
				errs.Add(routePrefix+".upstream", "is required")
			}
			if route.PathRegexp != "" {
				if _, err := regexp.Compile(route.PathRegexp); err != nil {
					errs.Add(routePrefix+".path_regexp", fmt.Sprintf("invalid regex: %s", err))
				}
			}
		}
	}
}

// parseDurationWithDays parses a duration string that may include days (e.g., "7d").
func parseDurationWithDays(s string) (time.Duration, error) {
	if len(s) > 1 && s[len(s)-1] == 'd' {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	return time.ParseDuration(s)
}
