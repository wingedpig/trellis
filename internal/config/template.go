// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// TemplateExpander handles Go text/template variable expansion in config values.
type TemplateExpander struct {
	funcMap template.FuncMap
}

// NewTemplateExpander creates a new template expander with built-in functions.
func NewTemplateExpander() *TemplateExpander {
	return &TemplateExpander{
		funcMap: template.FuncMap{
			"slugify": Slugify,
			"replace": Replace,
			"upper":   strings.ToUpper,
			"lower":   strings.ToLower,
			"default": Default,
			"quote":   Quote,
		},
	}
}

// inputsOrControlPattern matches template actions that reference .Inputs or are control-flow (if/else/end/range/with).
var inputsOrControlPattern = regexp.MustCompile(`\{\{-?\s*(?:(?:if|else if|with|range)\s+[^}]*\.Inputs\.[^}]*|\.Inputs\.[^}]*|end|else)\s*-?\}\}`)

// controlFlowPattern matches control-flow template directives (if/else/end/range/with).
var controlFlowPattern = regexp.MustCompile(`\{\{-?\s*(?:if|else if|else|end|range|with)\b`)

// inputsActionPattern matches standalone {{ .Inputs.xxx }} actions (no control flow).
var inputsActionPattern = regexp.MustCompile(`\{\{-?\s*\.Inputs\.\w+\s*-?\}\}`)

// Expand expands template variables in a string value.
// Templates referencing .Inputs are preserved (they are expanded at runtime).
func (e *TemplateExpander) Expand(value string, ctx *TemplateContext) (string, error) {
	if !strings.Contains(value, "{{") {
		return value, nil
	}

	// If the value contains .Inputs references, it's a runtime template
	// (e.g. workflow commands with {{if .Inputs.id}}...{{end}}).
	// We can't selectively extract individual {{ }} blocks because
	// control-flow directives like {{if}}/{{end}} span multiple blocks.
	// Instead, skip expansion entirely if the only templates are .Inputs references.
	if strings.Contains(value, ".Inputs") {
		// Check if there are any non-.Inputs template actions to expand.
		// Remove all {{ }} blocks that reference .Inputs or are control flow (if/else/end/range/with).
		remaining := inputsOrControlPattern.ReplaceAllString(value, "")
		if !strings.Contains(remaining, "{{") {
			// Everything is .Inputs-related — preserve the whole string
			return value, nil
		}
		// There's a mix of .Inputs and other templates. If control-flow
		// blocks are present we can't safely partial-expand.
		if controlFlowPattern.MatchString(value) {
			return value, nil
		}
		// Simple mix: replace .Inputs actions with placeholders, expand,
		// then restore.
		return e.expandWithInputsPlaceholders(value, ctx)
	}

	tmpl, err := template.New("").Funcs(e.funcMap).Parse(value)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// expandWithInputsPlaceholders handles mixed templates containing both .Inputs
// references and other expandable actions. It replaces .Inputs actions with
// unique placeholders, expands the rest, then restores the originals.
func (e *TemplateExpander) expandWithInputsPlaceholders(value string, ctx *TemplateContext) (string, error) {
	// Collect all .Inputs actions and replace with placeholders
	var placeholders []string
	i := 0
	replaced := inputsActionPattern.ReplaceAllStringFunc(value, func(match string) string {
		ph := fmt.Sprintf("__TRELLIS_INPUTS_%d__", i)
		i++
		placeholders = append(placeholders, match)
		return ph
	})

	// Expand the remaining template actions
	tmpl, err := template.New("").Funcs(e.funcMap).Parse(replaced)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", err
	}

	// Restore .Inputs placeholders
	result := buf.String()
	for j, original := range placeholders {
		ph := fmt.Sprintf("__TRELLIS_INPUTS_%d__", j)
		result = strings.Replace(result, ph, original, 1)
	}

	return result, nil
}

// ExpandConfig expands all template variables in the config.
// This creates a deep copy with expanded values.
func (e *TemplateExpander) ExpandConfig(cfg *Config, ctx *TemplateContext) (*Config, error) {
	// Create a copy of the config
	expanded := *cfg

	// Expand worktree binaries path
	if expanded.Worktree.Binaries.Path != "" {
		path, err := e.Expand(expanded.Worktree.Binaries.Path, ctx)
		if err != nil {
			return nil, err
		}
		expanded.Worktree.Binaries.Path = path
	}

	// Expand crashes directory
	if expanded.Crashes.ReportsDir != "" {
		path, err := e.Expand(expanded.Crashes.ReportsDir, ctx)
		if err != nil {
			return nil, err
		}
		expanded.Crashes.ReportsDir = path
	}

	// Expand service configs
	expandedServices := make([]ServiceConfig, len(cfg.Services))
	for i, svc := range cfg.Services {
		expandedSvc, err := e.expandService(svc, ctx)
		if err != nil {
			return nil, err
		}
		expandedServices[i] = expandedSvc
	}
	expanded.Services = expandedServices

	// Expand workflow configs
	expandedWorkflows := make([]WorkflowConfig, len(cfg.Workflows))
	for i, wf := range cfg.Workflows {
		expandedWf, err := e.expandWorkflow(wf, ctx)
		if err != nil {
			return nil, err
		}
		expandedWorkflows[i] = expandedWf
	}
	expanded.Workflows = expandedWorkflows

	// Expand proxy configs
	if len(cfg.Proxy) > 0 {
		expandedProxy := make([]ProxyListenerConfig, len(cfg.Proxy))
		for i, listener := range cfg.Proxy {
			expandedListener, err := e.expandProxyListener(listener, ctx)
			if err != nil {
				return nil, err
			}
			expandedProxy[i] = expandedListener
		}
		expanded.Proxy = expandedProxy
	}

	return &expanded, nil
}

// expandProxyListener expands template variables in a proxy listener config.
func (e *TemplateExpander) expandProxyListener(listener ProxyListenerConfig, ctx *TemplateContext) (ProxyListenerConfig, error) {
	expanded := listener

	if listener.Listen != "" {
		v, err := e.Expand(listener.Listen, ctx)
		if err != nil {
			return expanded, err
		}
		expanded.Listen = v
	}

	if len(listener.Routes) > 0 {
		expandedRoutes := make([]ProxyRouteConfig, len(listener.Routes))
		for i, route := range listener.Routes {
			expandedRoute := route
			if route.Upstream != "" {
				v, err := e.Expand(route.Upstream, ctx)
				if err != nil {
					return expanded, err
				}
				expandedRoute.Upstream = v
			}
			expandedRoutes[i] = expandedRoute
		}
		expanded.Routes = expandedRoutes
	}

	return expanded, nil
}

// expandService expands template variables in a service config.
func (e *TemplateExpander) expandService(svc ServiceConfig, ctx *TemplateContext) (ServiceConfig, error) {
	// Create service-specific context
	svcCtx := &TemplateContext{
		Worktree: ctx.Worktree,
		Project:  ctx.Project,
		Service:  &ServiceTemplateData{Name: svc.Name},
	}

	expanded := svc

	// Expand command (string or array)
	switch cmd := svc.Command.(type) {
	case string:
		expandedCmd, err := e.Expand(cmd, svcCtx)
		if err != nil {
			return expanded, err
		}
		expanded.Command = expandedCmd
	case []interface{}:
		expandedCmd := make([]string, len(cmd))
		for i, v := range cmd {
			str, ok := v.(string)
			if !ok {
				expandedCmd[i] = ""
				continue
			}
			exp, err := e.Expand(str, svcCtx)
			if err != nil {
				return expanded, err
			}
			expandedCmd[i] = exp
		}
		expanded.Command = expandedCmd
	case []string:
		expandedCmd := make([]string, len(cmd))
		for i, str := range cmd {
			exp, err := e.Expand(str, svcCtx)
			if err != nil {
				return expanded, err
			}
			expandedCmd[i] = exp
		}
		expanded.Command = expandedCmd
	}

	// Expand args
	if len(svc.Args) > 0 {
		expandedArgs := make([]string, len(svc.Args))
		for i, arg := range svc.Args {
			exp, err := e.Expand(arg, svcCtx)
			if err != nil {
				return expanded, err
			}
			expandedArgs[i] = exp
		}
		expanded.Args = expandedArgs
	}

	// Expand watch_binary
	if svc.WatchBinary != "" {
		wb, err := e.Expand(svc.WatchBinary, svcCtx)
		if err != nil {
			return expanded, err
		}
		expanded.WatchBinary = wb
	}

	// Expand debug
	if svc.Debug != "" {
		dbg, err := e.Expand(svc.Debug, svcCtx)
		if err != nil {
			return expanded, err
		}
		expanded.Debug = dbg
	}

	// Expand work_dir (default to worktree root if not set)
	if svc.WorkDir != "" {
		wd, err := e.Expand(svc.WorkDir, svcCtx)
		if err != nil {
			return expanded, err
		}
		expanded.WorkDir = wd
	} else if ctx.Worktree.Root != "" {
		// Default to worktree root if work_dir is not specified
		expanded.WorkDir = ctx.Worktree.Root
	}

	// Expand environment variable values
	if len(svc.Env) > 0 {
		expandedEnv := make(map[string]string, len(svc.Env))
		for k, v := range svc.Env {
			expVal, err := e.Expand(v, svcCtx)
			if err != nil {
				return expanded, err
			}
			expandedEnv[k] = expVal
		}
		expanded.Env = expandedEnv
	}

	return expanded, nil
}

// expandWorkflow expands template variables in a workflow config.
func (e *TemplateExpander) expandWorkflow(wf WorkflowConfig, ctx *TemplateContext) (WorkflowConfig, error) {
	expanded := wf

	// Expand command (string or array)
	switch cmd := wf.Command.(type) {
	case string:
		expandedCmd, err := e.Expand(cmd, ctx)
		if err != nil {
			return expanded, err
		}
		expanded.Command = expandedCmd
	case []interface{}:
		expandedCmd := make([]string, len(cmd))
		for i, v := range cmd {
			str, ok := v.(string)
			if !ok {
				expandedCmd[i] = ""
				continue
			}
			exp, err := e.Expand(str, ctx)
			if err != nil {
				return expanded, err
			}
			expandedCmd[i] = exp
		}
		expanded.Command = expandedCmd
	case []string:
		expandedCmd := make([]string, len(cmd))
		for i, str := range cmd {
			exp, err := e.Expand(str, ctx)
			if err != nil {
				return expanded, err
			}
			expandedCmd[i] = exp
		}
		expanded.Command = expandedCmd
	}

	// Expand commands (array of arrays)
	switch cmds := wf.Commands.(type) {
	case []interface{}:
		expandedCmds := make([]interface{}, len(cmds))
		for i, cmdAny := range cmds {
			switch cmd := cmdAny.(type) {
			case []interface{}:
				expandedCmd := make([]string, len(cmd))
				for j, v := range cmd {
					str, ok := v.(string)
					if !ok {
						expandedCmd[j] = ""
						continue
					}
					exp, err := e.Expand(str, ctx)
					if err != nil {
						return expanded, err
					}
					expandedCmd[j] = exp
				}
				expandedCmds[i] = expandedCmd
			case []string:
				expandedCmd := make([]string, len(cmd))
				for j, str := range cmd {
					exp, err := e.Expand(str, ctx)
					if err != nil {
						return expanded, err
					}
					expandedCmd[j] = exp
				}
				expandedCmds[i] = expandedCmd
			default:
				expandedCmds[i] = cmdAny
			}
		}
		expanded.Commands = expandedCmds
	}

	return expanded, nil
}

// Slugify converts a string to a URL-friendly slug.
func Slugify(s string) string {
	// Convert to lowercase
	s = strings.ToLower(s)

	// Replace common separators with hyphens
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, " ", "-")

	// Remove any character that isn't alphanumeric or hyphen
	reg := regexp.MustCompile(`[^a-z0-9-]+`)
	s = reg.ReplaceAllString(s, "")

	// Replace multiple hyphens with single hyphen
	reg = regexp.MustCompile(`-+`)
	s = reg.ReplaceAllString(s, "-")

	// Trim leading/trailing hyphens
	s = strings.Trim(s, "-")

	return s
}

// Replace replaces all occurrences of old with new in s.
func Replace(old, new, s string) string {
	return strings.ReplaceAll(s, old, new)
}

// Default returns the value if non-empty, otherwise the default.
func Default(defaultVal, value string) string {
	if value == "" {
		return defaultVal
	}
	return value
}

// Quote adds shell-safe quotes around a string.
func Quote(s string) string {
	// Escape existing quotes and wrap in quotes
	escaped := strings.ReplaceAll(s, `"`, `\"`)
	return `"` + escaped + `"`
}
