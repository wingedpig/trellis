// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateExpander_Expand_WorktreeVariables(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Worktree: WorktreeTemplateData{
			Root:     "/home/user/project",
			Name:     "feature-auth",
			Branch:   "feature/auth",
			Binaries: "/home/user/bin/project-feature-auth",
		},
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "worktree root",
			input:    "{{.Worktree.Root}}/bin/api",
			expected: "/home/user/project/bin/api",
		},
		{
			name:     "worktree name",
			input:    "prefix-{{.Worktree.Name}}-suffix",
			expected: "prefix-feature-auth-suffix",
		},
		{
			name:     "worktree branch",
			input:    "branch: {{.Worktree.Branch}}",
			expected: "branch: feature/auth",
		},
		{
			name:     "worktree binaries",
			input:    "{{.Worktree.Binaries}}/api",
			expected: "/home/user/bin/project-feature-auth/api",
		},
		{
			name:     "multiple variables",
			input:    "{{.Worktree.Root}}/config/{{.Worktree.Name}}.json",
			expected: "/home/user/project/config/feature-auth.json",
		},
		{
			name:     "no template",
			input:    "plain string",
			expected: "plain string",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := expander.Expand(tt.input, ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTemplateExpander_Expand_ProjectVariables(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Project: ProjectTemplateData{
			Root: "/home/user/main-project",
			Name: "my-project",
		},
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "project root",
			input:    "{{.Project.Root}}/shared",
			expected: "/home/user/main-project/shared",
		},
		{
			name:     "project name",
			input:    "{{.Project.Name}}-service",
			expected: "my-project-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := expander.Expand(tt.input, ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTemplateExpander_Expand_ServiceVariables(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Worktree: WorktreeTemplateData{
			Root: "/project",
		},
		Service: &ServiceTemplateData{
			Name: "api-server",
		},
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "service name",
			input:    "{{.Worktree.Root}}/bin/{{.Service.Name}}",
			expected: "/project/bin/api-server",
		},
		{
			name:     "service name in config path",
			input:    "/etc/{{.Service.Name}}/config.json",
			expected: "/etc/api-server/config.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := expander.Expand(tt.input, ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTemplateExpander_Expand_NilServiceContext(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Worktree: WorktreeTemplateData{
			Root: "/project",
		},
		Service: nil, // No service context
	}

	// Should work for non-service templates
	result, err := expander.Expand("{{.Worktree.Root}}/bin", ctx)
	require.NoError(t, err)
	assert.Equal(t, "/project/bin", result)
}

func TestTemplateExpander_Expand_TemplateFunctions(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Worktree: WorktreeTemplateData{
			Root:   "/home/user/project",
			Name:   "feature-auth",
			Branch: "feature/authentication",
		},
		Project: ProjectTemplateData{
			Name: "my-project",
		},
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "slugify function",
			input:    "{{.Worktree.Branch | slugify}}",
			expected: "feature-authentication",
		},
		{
			name:     "slugify with special chars",
			input:    `{{.Worktree.Branch | slugify}}`,
			expected: "feature-authentication",
		},
		{
			name:     "upper function",
			input:    "{{.Worktree.Name | upper}}",
			expected: "FEATURE-AUTH",
		},
		{
			name:     "lower function",
			input:    "{{.Project.Name | lower}}",
			expected: "my-project",
		},
		{
			name:     "replace function",
			input:    `{{.Worktree.Name | replace "-" "_"}}`,
			expected: "feature_auth",
		},
		{
			name:     "default function with value",
			input:    `{{.Worktree.Name | default "fallback"}}`,
			expected: "feature-auth",
		},
		{
			name:     "quote function",
			input:    `{{.Worktree.Root | quote}}`,
			expected: `"/home/user/project"`,
		},
		{
			name:     "chained functions",
			input:    "{{.Worktree.Branch | slugify | upper}}",
			expected: "FEATURE-AUTHENTICATION",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := expander.Expand(tt.input, ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTemplateExpander_Expand_DefaultFunction_EmptyValue(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Worktree: WorktreeTemplateData{
			Root: "",
			Name: "",
		},
	}

	result, err := expander.Expand(`{{.Worktree.Name | default "fallback"}}`, ctx)
	require.NoError(t, err)
	assert.Equal(t, "fallback", result)
}

func TestTemplateExpander_Expand_Errors(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{}

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "invalid syntax",
			input: "{{.Worktree.Root",
		},
		{
			name:  "unknown function",
			input: "{{.Worktree.Root | unknownFunc}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := expander.Expand(tt.input, ctx)
			assert.Error(t, err)
		})
	}
}

func TestTemplateExpander_ExpandConfig(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Worktree: WorktreeTemplateData{
			Root:     "/home/user/project",
			Name:     "main",
			Binaries: "/home/user/bin",
		},
		Project: ProjectTemplateData{
			Name: "myapp",
		},
	}

	cfg := &Config{
		Worktree: WorktreeConfig{
			Binaries: BinariesConfig{
				Path: "{{.Worktree.Root}}/bin",
			},
		},
		Services: []ServiceConfig{
			{
				Name:    "api",
				Command: "{{.Worktree.Binaries}}/api",
				Args:    []string{"-config", "{{.Worktree.Root}}/config/api.json"},
			},
			{
				Name:    "worker",
				Command: []string{"{{.Worktree.Binaries}}/worker", "-queue", "default"},
			},
		},
		Crashes: CrashesConfig{
			ReportsDir: "{{.Worktree.Root}}/.trellis/crashes",
		},
	}

	expanded, err := expander.ExpandConfig(cfg, ctx)
	require.NoError(t, err)

	// Verify worktree binaries path expanded
	assert.Equal(t, "/home/user/project/bin", expanded.Worktree.Binaries.Path)

	// Verify service command expanded (string form)
	assert.Equal(t, "/home/user/bin/api", expanded.Services[0].Command)

	// Verify service args expanded
	assert.Equal(t, []string{"-config", "/home/user/project/config/api.json"}, expanded.Services[0].Args)

	// Verify crashes dir expanded
	assert.Equal(t, "/home/user/project/.trellis/crashes", expanded.Crashes.ReportsDir)
}

func TestTemplateExpander_ExpandConfig_ServiceContext(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Worktree: WorktreeTemplateData{
			Root: "/project",
		},
	}

	cfg := &Config{
		Services: []ServiceConfig{
			{
				Name:    "api",
				Command: "{{.Worktree.Root}}/bin/{{.Service.Name}}",
			},
			{
				Name:    "worker",
				Command: "{{.Worktree.Root}}/bin/{{.Service.Name}}",
			},
		},
	}

	expanded, err := expander.ExpandConfig(cfg, ctx)
	require.NoError(t, err)

	// Each service should have its own name substituted
	assert.Equal(t, "/project/bin/api", expanded.Services[0].Command)
	assert.Equal(t, "/project/bin/worker", expanded.Services[1].Command)
}

func TestTemplateExpander_ExpandConfig_PreservesNonTemplates(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Worktree: WorktreeTemplateData{
			Root: "/project",
		},
	}

	cfg := &Config{
		Version: "1.0",
		Project: ProjectConfig{
			Name:        "test-project",
			Description: "A test project",
		},
		Server: ServerConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
	}

	expanded, err := expander.ExpandConfig(cfg, ctx)
	require.NoError(t, err)

	assert.Equal(t, "1.0", expanded.Version)
	assert.Equal(t, "test-project", expanded.Project.Name)
	assert.Equal(t, 8080, expanded.Server.Port)
	assert.Equal(t, "127.0.0.1", expanded.Server.Host)
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"feature/auth", "feature-auth"},
		{"Feature/Auth", "feature-auth"},
		{"feature_auth", "feature-auth"},
		{"feature auth", "feature-auth"},
		{"feature--auth", "feature-auth"},
		{"  feature  auth  ", "feature-auth"},
		{"feature/auth/login", "feature-auth-login"},
		{"UPPERCASE", "uppercase"},
		{"with.dots.here", "with-dots-here"},
		{"special!@#chars", "specialchars"},
		{"", ""},
		{"-leading-trailing-", "leading-trailing"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := Slugify(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", `"simple"`},
		{"/path/to/file", `"/path/to/file"`},
		{`path with "quotes"`, `"path with \"quotes\""`},
		{"path with spaces", `"path with spaces"`},
		{"", `""`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := Quote(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTemplateExpander_ExpandConfig_WorkflowCommands(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Worktree: WorktreeTemplateData{
			Root:     "/home/user/project",
			Binaries: "/home/user/bin",
		},
	}

	cfg := &Config{
		Workflows: []WorkflowConfig{
			{
				ID:   "single",
				Name: "Single Command",
				// Single command with template
				Command: []interface{}{"{{.Worktree.Binaries}}/tool", "arg1"},
			},
			{
				ID:   "multi",
				Name: "Multiple Commands",
				// Multiple commands with templates
				Commands: []interface{}{
					[]interface{}{"{{.Worktree.Binaries}}/clean"},
					[]interface{}{"{{.Worktree.Binaries}}/build", "-o", "{{.Worktree.Root}}/output"},
					[]interface{}{"{{.Worktree.Binaries}}/test"},
				},
			},
		},
	}

	expanded, err := expander.ExpandConfig(cfg, ctx)
	require.NoError(t, err)

	// Verify single command expanded
	assert.Equal(t, []string{"/home/user/bin/tool", "arg1"}, expanded.Workflows[0].Command)

	// Verify multi commands expanded
	expectedCommands := []interface{}{
		[]string{"/home/user/bin/clean"},
		[]string{"/home/user/bin/build", "-o", "/home/user/project/output"},
		[]string{"/home/user/bin/test"},
	}
	assert.Equal(t, expectedCommands, expanded.Workflows[1].Commands)
}
