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

// inputsPlaceholderPrefix is used to temporarily preserve .Inputs references during config expansion.
const inputsPlaceholderPrefix = "\x00TRELLIS_INPUTS_"

// Expand expands template variables in a string value.
// Templates referencing .Inputs are preserved (they are expanded at runtime).
func (e *TemplateExpander) Expand(value string, ctx *TemplateContext) (string, error) {
	if !strings.Contains(value, "{{") {
		return value, nil
	}

	// Temporarily replace .Inputs references with placeholders to preserve them
	// They will be expanded at runtime when the workflow is executed
	preserved := value
	hasInputs := strings.Contains(value, ".Inputs")
	if hasInputs {
		// Use a regex to find and replace {{ ... .Inputs ... }} patterns
		inputsPattern := regexp.MustCompile(`\{\{[^}]*\.Inputs\.[^}]*\}\}`)
		matches := inputsPattern.FindAllString(value, -1)
		for i, match := range matches {
			placeholder := fmt.Sprintf("%s%d\x00", inputsPlaceholderPrefix, i)
			preserved = strings.Replace(preserved, match, placeholder, 1)
		}
	}

	// If nothing left to expand, restore and return
	if !strings.Contains(preserved, "{{") {
		// Restore .Inputs patterns
		if hasInputs {
			inputsPattern := regexp.MustCompile(`\{\{[^}]*\.Inputs\.[^}]*\}\}`)
			matches := inputsPattern.FindAllString(value, -1)
			for i, match := range matches {
				placeholder := fmt.Sprintf("%s%d\x00", inputsPlaceholderPrefix, i)
				preserved = strings.Replace(preserved, placeholder, match, 1)
			}
		}
		return preserved, nil
	}

	tmpl, err := template.New("").Funcs(e.funcMap).Parse(preserved)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", err
	}

	result := buf.String()

	// Restore .Inputs patterns
	if hasInputs {
		inputsPattern := regexp.MustCompile(`\{\{[^}]*\.Inputs\.[^}]*\}\}`)
		matches := inputsPattern.FindAllString(value, -1)
		for i, match := range matches {
			placeholder := fmt.Sprintf("%s%d\x00", inputsPlaceholderPrefix, i)
			result = strings.Replace(result, placeholder, match, 1)
		}
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

	return &expanded, nil
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
