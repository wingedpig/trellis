// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidator_Validate_ValidConfig(t *testing.T) {
	cfg := &Config{
		Version: "1.0",
		Project: ProjectConfig{
			Name: "test-project",
		},
		Server: ServerConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Services: []ServiceConfig{
			{
				Name:    "api",
				Command: "./bin/api",
			},
		},
	}

	validator := NewValidator()
	err := validator.Validate(cfg)
	assert.NoError(t, err)
}

func TestValidator_Validate_RequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *Config
		errContains string
	}{
		{
			name: "missing version",
			cfg: &Config{
				Project: ProjectConfig{Name: "test"},
			},
			errContains: "version",
		},
		{
			name: "missing project name",
			cfg: &Config{
				Version: "1.0",
				Project: ProjectConfig{},
			},
			errContains: "project.name",
		},
	}

	validator := NewValidator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.Validate(tt.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestValidator_Validate_ServiceConfig(t *testing.T) {
	tests := []struct {
		name        string
		service     ServiceConfig
		errContains string
	}{
		{
			name: "missing service name",
			service: ServiceConfig{
				Command: "./bin/api",
			},
			errContains: "name",
		},
		{
			name: "missing service command",
			service: ServiceConfig{
				Name: "api",
			},
			errContains: "command",
		},
		{
			name: "invalid restart policy",
			service: ServiceConfig{
				Name:    "api",
				Command: "./bin/api",
				Restart: RestartConfig{Policy: "invalid"},
			},
			errContains: "restart.policy",
		},
	}

	validator := NewValidator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Version: "1.0",
				Project: ProjectConfig{Name: "test"},
				Services: []ServiceConfig{tt.service},
			}
			err := validator.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestValidator_Validate_DuplicateServiceNames(t *testing.T) {
	cfg := &Config{
		Version: "1.0",
		Project: ProjectConfig{Name: "test"},
		Services: []ServiceConfig{
			{Name: "api", Command: "./bin/api"},
			{Name: "api", Command: "./bin/api-2"},
		},
	}

	validator := NewValidator()
	err := validator.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestValidator_Validate_WorkflowConfig(t *testing.T) {
	tests := []struct {
		name        string
		workflow    WorkflowConfig
		errContains string
	}{
		{
			name: "missing workflow id",
			workflow: WorkflowConfig{
				Name:    "Test",
				Command: "go test ./...",
			},
			errContains: "id",
		},
		{
			name: "missing workflow name",
			workflow: WorkflowConfig{
				ID:      "test",
				Command: "go test ./...",
			},
			errContains: "name",
		},
		{
			name: "missing workflow command",
			workflow: WorkflowConfig{
				ID:   "test",
				Name: "Test",
			},
			errContains: "command",
		},
		{
			name: "invalid output parser",
			workflow: WorkflowConfig{
				ID:           "test",
				Name:         "Test",
				Command:      "go test ./...",
				OutputParser: "invalid_parser",
			},
			errContains: "output_parser",
		},
	}

	validator := NewValidator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Version:   "1.0",
				Project:   ProjectConfig{Name: "test"},
				Workflows: []WorkflowConfig{tt.workflow},
			}
			err := validator.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestValidator_Validate_DuplicateWorkflowIDs(t *testing.T) {
	cfg := &Config{
		Version: "1.0",
		Project: ProjectConfig{Name: "test"},
		Workflows: []WorkflowConfig{
			{ID: "test", Name: "Test 1", Command: "go test"},
			{ID: "test", Name: "Test 2", Command: "go test -v"},
		},
	}

	validator := NewValidator()
	err := validator.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestValidator_Validate_ServerConfig(t *testing.T) {
	tests := []struct {
		name        string
		server      ServerConfig
		errContains string
	}{
		{
			name: "port out of range (negative)",
			server: ServerConfig{
				Port: -1,
				Host: "127.0.0.1",
			},
			errContains: "port",
		},
		{
			name: "port out of range (too high)",
			server: ServerConfig{
				Port: 70000,
				Host: "127.0.0.1",
			},
			errContains: "port",
		},
	}

	validator := NewValidator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Version: "1.0",
				Project: ProjectConfig{Name: "test"},
				Server:  tt.server,
			}
			err := validator.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestValidator_Validate_TerminalConfig(t *testing.T) {
	tests := []struct {
		name        string
		terminal    TerminalConfig
		errContains string
	}{
		{
			name: "invalid backend",
			terminal: TerminalConfig{
				Backend: "invalid",
			},
			errContains: "backend",
		},
		{
			name: "missing window name",
			terminal: TerminalConfig{
				Backend: "tmux",
				DefaultWindows: []WindowConfig{
					{Command: "/bin/zsh"},
				},
			},
			errContains: "window",
		},
	}

	validator := NewValidator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Version:  "1.0",
				Project:  ProjectConfig{Name: "test"},
				Terminal: tt.terminal,
			}
			err := validator.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestValidator_Validate_LoggingConfig(t *testing.T) {
	tests := []struct {
		name        string
		logging     LoggingConfig
		errContains string
	}{
		{
			name: "invalid log level",
			logging: LoggingConfig{
				Level: "invalid",
			},
			errContains: "level",
		},
		{
			name: "invalid log format",
			logging: LoggingConfig{
				Level:  "info",
				Format: "invalid",
			},
			errContains: "format",
		},
	}

	validator := NewValidator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Version: "1.0",
				Project: ProjectConfig{Name: "test"},
				Logging: tt.logging,
			}
			err := validator.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestValidator_Validate_UIConfig(t *testing.T) {
	tests := []struct {
		name        string
		ui          UIConfig
		errContains string
	}{
		{
			name: "invalid theme",
			ui: UIConfig{
				Theme: "invalid",
			},
			errContains: "theme",
		},
	}

	validator := NewValidator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Version: "1.0",
				Project: ProjectConfig{Name: "test"},
				UI:      tt.ui,
			}
			err := validator.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestValidator_Validate_ValidOutputParsers(t *testing.T) {
	validParsers := []string{"go", "go_test_json", "generic", "none", "html", ""}

	validator := NewValidator()
	for _, parser := range validParsers {
		t.Run(parser, func(t *testing.T) {
			cfg := &Config{
				Version: "1.0",
				Project: ProjectConfig{Name: "test"},
				Workflows: []WorkflowConfig{
					{ID: "test", Name: "Test", Command: []string{"go", "test"}, OutputParser: parser},
				},
			}
			err := validator.Validate(cfg)
			assert.NoError(t, err)
		})
	}
}

func TestValidator_Validate_ValidRestartPolicies(t *testing.T) {
	validPolicies := []string{"always", "on_failure", "never", ""}

	validator := NewValidator()
	for _, policy := range validPolicies {
		t.Run(policy, func(t *testing.T) {
			cfg := &Config{
				Version: "1.0",
				Project: ProjectConfig{Name: "test"},
				Services: []ServiceConfig{
					{Name: "api", Command: "./api", Restart: RestartConfig{Policy: policy}},
				},
			}
			err := validator.Validate(cfg)
			assert.NoError(t, err)
		})
	}
}

func TestValidator_Validate_RequiresStopped_UnknownService(t *testing.T) {
	cfg := &Config{
		Version: "1.0",
		Project: ProjectConfig{Name: "test"},
		Services: []ServiceConfig{
			{Name: "api", Command: "./api"},
		},
		Workflows: []WorkflowConfig{
			{
				ID:              "test",
				Name:            "Test",
				Command:         "go test",
				RequiresStopped: []string{"api", "unknown-service"},
			},
		},
	}

	validator := NewValidator()
	err := validator.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown-service")
}

func TestValidator_Validate_DurationFormats(t *testing.T) {
	tests := []struct {
		name      string
		duration  string
		wantError bool
	}{
		{"valid ms", "500ms", false},
		{"valid seconds", "30s", false},
		{"valid minutes", "5m", false},
		{"valid hours", "1h", false},
		{"valid combined", "1h30m", false},
		{"invalid format", "5minutes", true},
		{"negative", "-5s", true},
	}

	validator := NewValidator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Version: "1.0",
				Project: ProjectConfig{Name: "test"},
				Watch: WatchConfig{
					Debounce: tt.duration,
				},
			}
			err := validator.Validate(cfg)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidator_Validate_ProxyConfig(t *testing.T) {
	tests := []struct {
		name        string
		proxy       []ProxyListenerConfig
		errContains string
	}{
		{
			name: "missing listen",
			proxy: []ProxyListenerConfig{
				{Routes: []ProxyRouteConfig{{Upstream: "localhost:3000"}}},
			},
			errContains: "listen",
		},
		{
			name: "missing routes",
			proxy: []ProxyListenerConfig{
				{Listen: ":443"},
			},
			errContains: "routes",
		},
		{
			name: "missing upstream",
			proxy: []ProxyListenerConfig{
				{Listen: ":443", Routes: []ProxyRouteConfig{{PathRegexp: "^/api"}}},
			},
			errContains: "upstream",
		},
		{
			name: "invalid path_regexp",
			proxy: []ProxyListenerConfig{
				{Listen: ":443", Routes: []ProxyRouteConfig{{PathRegexp: "[invalid", Upstream: "localhost:3000"}}},
			},
			errContains: "path_regexp",
		},
		{
			name: "tls_cert without tls_key",
			proxy: []ProxyListenerConfig{
				{Listen: ":443", TLSCert: "/path/cert.pem", Routes: []ProxyRouteConfig{{Upstream: "localhost:3000"}}},
			},
			errContains: "tls_cert and tls_key must be specified together",
		},
		{
			name: "tls_tailscale with tls_cert",
			proxy: []ProxyListenerConfig{
				{Listen: ":443", TLSTailscale: true, TLSCert: "/path/cert.pem", TLSKey: "/path/key.pem", Routes: []ProxyRouteConfig{{Upstream: "localhost:3000"}}},
			},
			errContains: "mutually exclusive",
		},
	}

	validator := NewValidator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Version: "1.0",
				Project: ProjectConfig{Name: "test"},
				Proxy:   tt.proxy,
			}
			err := validator.Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestValidator_Validate_ProxyConfig_Valid(t *testing.T) {
	tests := []struct {
		name  string
		proxy []ProxyListenerConfig
	}{
		{
			name: "basic proxy",
			proxy: []ProxyListenerConfig{
				{Listen: ":443", Routes: []ProxyRouteConfig{{Upstream: "localhost:3000"}}},
			},
		},
		{
			name: "with path_regexp",
			proxy: []ProxyListenerConfig{
				{Listen: ":443", Routes: []ProxyRouteConfig{
					{PathRegexp: "^/api/.+", Upstream: "localhost:3001"},
					{Upstream: "localhost:3000"},
				}},
			},
		},
		{
			name: "with tls_tailscale",
			proxy: []ProxyListenerConfig{
				{Listen: ":443", TLSTailscale: true, Routes: []ProxyRouteConfig{{Upstream: "localhost:3000"}}},
			},
		},
		{
			name: "with tls cert/key",
			proxy: []ProxyListenerConfig{
				{Listen: ":443", TLSCert: "/path/cert.pem", TLSKey: "/path/key.pem", Routes: []ProxyRouteConfig{{Upstream: "localhost:3000"}}},
			},
		},
		{
			name: "multiple listeners",
			proxy: []ProxyListenerConfig{
				{Listen: ":1001", Routes: []ProxyRouteConfig{{Upstream: "localhost:1000"}}},
				{Listen: ":443", TLSTailscale: true, Routes: []ProxyRouteConfig{
					{PathRegexp: "^/api/.+", Upstream: "localhost:3001"},
					{Upstream: "localhost:3000"},
				}},
			},
		},
		{
			name:  "no proxy configured",
			proxy: nil,
		},
	}

	validator := NewValidator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Version: "1.0",
				Project: ProjectConfig{Name: "test"},
				Proxy:   tt.proxy,
			}
			err := validator.Validate(cfg)
			assert.NoError(t, err)
		})
	}
}

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{
		Errors: []FieldError{
			{Field: "version", Message: "is required"},
			{Field: "project.name", Message: "is required"},
		},
	}

	errStr := err.Error()
	assert.Contains(t, errStr, "version")
	assert.Contains(t, errStr, "project.name")
}

func TestValidationError_IsEmpty(t *testing.T) {
	err := &ValidationError{}
	assert.True(t, err.IsEmpty())

	err.Errors = append(err.Errors, FieldError{Field: "test", Message: "error"})
	assert.False(t, err.IsEmpty())
}
