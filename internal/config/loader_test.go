// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoader_Load_ValidConfig(t *testing.T) {
	configContent := `{
		version: "1.0"
		project: {
			name: "test-project"
			description: "A test project"
		}
		server: {
			port: 8080
			host: "127.0.0.1"
		}
		services: [
			{
				name: "api"
				command: "./bin/api"
				args: ["-port", "8080"]
			}
		]
	}`

	cfg := loadFromString(t, configContent)

	assert.Equal(t, "1.0", cfg.Version)
	assert.Equal(t, "test-project", cfg.Project.Name)
	assert.Equal(t, "A test project", cfg.Project.Description)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "127.0.0.1", cfg.Server.Host)
	require.Len(t, cfg.Services, 1)
	assert.Equal(t, "api", cfg.Services[0].Name)
}

func TestLoader_Load_HJSONFeatures(t *testing.T) {
	// Test HJSON-specific features: comments, unquoted keys, trailing commas
	configContent := `{
		// This is a comment
		version: "1.0"

		# Hash comment
		project: {
			name: test-project
			description: '''
				Multi-line
				description
			'''
		}

		server: {
			port: 8080,
			host: 127.0.0.1,
		}

		services: [
			{
				name: api
				command: ./bin/api
			},
		]
	}`

	cfg := loadFromString(t, configContent)

	assert.Equal(t, "1.0", cfg.Version)
	assert.Equal(t, "test-project", cfg.Project.Name)
	assert.Contains(t, cfg.Project.Description, "Multi-line")
	assert.Equal(t, 8080, cfg.Server.Port)
}

func TestLoader_Load_AllSections(t *testing.T) {
	configContent := `{
		version: "1.0"

		project: {
			name: "full-project"
		}

		server: {
			port: 1000
			host: "0.0.0.0"
		}

		worktree: {
			discovery: { mode: "git" }
			binaries: { path: "{{.Worktree.Root}}/bin" }
			lifecycle: {
				pre_activate: [
					{ name: "build", command: ["make", "build"], timeout: "5m" }
				]
			}
		}

		services: [
			{
				name: "api"
				command: "./bin/api"
				restart: { policy: "on_failure" }
				watching: true
			}
			{
				name: "postgres"
				command: ["postgres", "-D", "/var/lib/postgres"]
				watching: false
				enabled: true
			}
		]

		workflows: [
			{
				id: "test"
				name: "Run Tests"
				command: "go test ./..."
				timeout: "10m"
				output_parser: "go_test_json"
			}
			{
				id: "db-reset"
				name: "Reset Database"
				command: ["./scripts/reset-db.sh"]
				confirm: true
				confirm_message: "This will delete all data. Continue?"
				requires_stopped: ["api", "worker"]
				restart_services: true
			}
		]

		crashes: {
			reports_dir: ".trellis/crashes"
			max_age: "7d"
			max_count: 100
		}

		terminal: {
			backend: "tmux"
			tmux: {
				history_limit: 50000
				shell: "/bin/zsh"
			}
			remote_windows: [
				{ name: "admin", command: ["ssh", "-t", "admin01", "screen", "-dR"] }
			]
			vscode: {
				binary: "code-server"
				port: 8443
			}
		}

		events: {
			history: {
				max_events: 10000
				max_age: "1h"
			}
			webhooks: [
				{
					id: "slack"
					url: "https://hooks.slack.com/xxx"
					events: ["service.crashed"]
				}
			]
		}

		watch: {
			debounce: "500ms"
		}

		logging: {
			level: "info"
			format: "json"
		}

		ui: {
			theme: "dark"
			terminal: {
				font_family: "Monaco"
				font_size: 14
				cursor_blink: true
			}
			notifications: {
				enabled: true
				events: ["service.crashed", "workflow.finished"]
				failures_only: true
				sound: false
			}
			editor: {
				remote_host: "devbox.example.com"
			}
			log_terminal: "admin"
		}
	}`

	cfg := loadFromString(t, configContent)

	// Worktree
	assert.Equal(t, "git", cfg.Worktree.Discovery.Mode)
	assert.Equal(t, "{{.Worktree.Root}}/bin", cfg.Worktree.Binaries.Path)
	require.Len(t, cfg.Worktree.Lifecycle.PreActivate, 1)
	assert.Equal(t, "build", cfg.Worktree.Lifecycle.PreActivate[0].Name)

	// Services
	require.Len(t, cfg.Services, 2)
	assert.Equal(t, "on_failure", cfg.Services[0].Restart.Policy)
	assert.True(t, cfg.Services[0].IsWatching())
	assert.False(t, cfg.Services[1].IsWatching())

	// Workflows
	require.Len(t, cfg.Workflows, 2)
	assert.Equal(t, "go_test_json", cfg.Workflows[0].OutputParser)
	assert.True(t, cfg.Workflows[1].Confirm)
	assert.Equal(t, []string{"api", "worker"}, cfg.Workflows[1].RequiresStopped)

	// Crashes
	assert.Equal(t, ".trellis/crashes", cfg.Crashes.ReportsDir)
	assert.Equal(t, "7d", cfg.Crashes.MaxAge)
	assert.Equal(t, 100, cfg.Crashes.MaxCount)

	// Terminal
	assert.Equal(t, "tmux", cfg.Terminal.Backend)
	assert.Equal(t, 50000, cfg.Terminal.Tmux.HistoryLimit)
	require.Len(t, cfg.Terminal.RemoteWindows, 1)
	require.NotNil(t, cfg.Terminal.VSCode)
	assert.Equal(t, 8443, cfg.Terminal.VSCode.Port)

	// Events
	assert.Equal(t, 10000, cfg.Events.History.MaxEvents)
	require.Len(t, cfg.Events.Webhooks, 1)

	// Watch
	assert.Equal(t, "500ms", cfg.Watch.Debounce)

	// Logging
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)

	// UI
	assert.Equal(t, "dark", cfg.UI.Theme)
	assert.Equal(t, 14, cfg.UI.Terminal.FontSize)
	assert.True(t, cfg.UI.Notifications.Enabled)
	assert.Equal(t, "devbox.example.com", cfg.UI.Editor.RemoteHost)
}

func TestLoader_Load_ServiceCommand_String(t *testing.T) {
	configContent := `{
		version: "1.0"
		services: [
			{
				name: "api"
				command: "./bin/api -config /etc/api.json"
			}
		]
	}`

	cfg := loadFromString(t, configContent)

	require.Len(t, cfg.Services, 1)
	cmd := cfg.Services[0].GetCommand()
	assert.Equal(t, []string{"./bin/api", "-config", "/etc/api.json"}, cmd)
}

func TestLoader_Load_ServiceCommand_Array(t *testing.T) {
	configContent := `{
		version: "1.0"
		services: [
			{
				name: "api"
				command: ["./bin/api", "-config", "/etc/api.json"]
			}
		]
	}`

	cfg := loadFromString(t, configContent)

	require.Len(t, cfg.Services, 1)
	cmd := cfg.Services[0].GetCommand()
	assert.Equal(t, []string{"./bin/api", "-config", "/etc/api.json"}, cmd)
}

func TestLoader_Load_Defaults(t *testing.T) {
	configContent := `{
		version: "1.0"
		project: { name: "test" }
	}`

	loader := NewLoader()
	cfg, err := loader.LoadWithDefaults(context.Background(), writeTestConfig(t, configContent))
	require.NoError(t, err)

	// Check defaults are applied
	assert.Equal(t, 1234, cfg.Server.Port)
	assert.Equal(t, "127.0.0.1", cfg.Server.Host)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
}

func TestLoader_Load_FileNotFound(t *testing.T) {
	loader := NewLoader()
	_, err := loader.Load(context.Background(), "/nonexistent/path/config.hjson")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read config")
}

func TestLoader_Load_InvalidHJSON(t *testing.T) {
	configContent := `{
		version: "1.0"
		invalid json here {{{
	}`

	loader := NewLoader()
	path := writeTestConfig(t, configContent)
	_, err := loader.Load(context.Background(), path)
	assert.Error(t, err)
}

func TestLoader_Load_ConfigPaths(t *testing.T) {
	dir := t.TempDir()

	// Create trellis.hjson
	hjsonPath := filepath.Join(dir, "trellis.hjson")
	require.NoError(t, os.WriteFile(hjsonPath, []byte(`{version: "1.0", project: {name: "hjson"}}`), 0644))

	// Create trellis.json
	jsonPath := filepath.Join(dir, "trellis.json")
	require.NoError(t, os.WriteFile(jsonPath, []byte(`{"version": "1.0", "project": {"name": "json"}}`), 0644))

	loader := NewLoader()

	// Explicit path takes precedence
	cfg, err := loader.Load(context.Background(), hjsonPath)
	require.NoError(t, err)
	assert.Equal(t, "hjson", cfg.Project.Name)

	// Can also load JSON
	cfg, err = loader.Load(context.Background(), jsonPath)
	require.NoError(t, err)
	assert.Equal(t, "json", cfg.Project.Name)
}

func TestLoader_FindConfig(t *testing.T) {
	dir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer os.Chdir(originalWd)
	os.Chdir(dir)

	loader := NewLoader()

	// No config file exists
	_, err := loader.FindConfig()
	assert.Error(t, err)

	// Create trellis.hjson
	require.NoError(t, os.WriteFile(filepath.Join(dir, "trellis.hjson"), []byte(`{}`), 0644))
	path, err := loader.FindConfig()
	require.NoError(t, err)
	assert.Contains(t, path, "trellis.hjson")

	// Remove hjson, create json - json should be found
	os.Remove(filepath.Join(dir, "trellis.hjson"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "trellis.json"), []byte(`{}`), 0644))
	path, err = loader.FindConfig()
	require.NoError(t, err)
	assert.Contains(t, path, "trellis.json")
}

func TestLoader_Load_ProxyConfig(t *testing.T) {
	configContent := `{
		version: "1.0"
		project: { name: "test" }
		proxy: [
			{
				listen: ":1001"
				routes: [
					{ upstream: "localhost:1000" }
				]
			}
			{
				listen: ":443"
				tls_tailscale: true
				routes: [
					{ path_regexp: "askws", upstream: "localhost:3000" }
					{ path_regexp: "^/api/.+", upstream: "localhost:3001" }
					{ upstream: "localhost:3000" }
				]
			}
		]
	}`

	cfg := loadFromString(t, configContent)

	require.Len(t, cfg.Proxy, 2)

	// First listener
	assert.Equal(t, ":1001", cfg.Proxy[0].Listen)
	assert.False(t, cfg.Proxy[0].TLSTailscale)
	require.Len(t, cfg.Proxy[0].Routes, 1)
	assert.Equal(t, "", cfg.Proxy[0].Routes[0].PathRegexp)
	assert.Equal(t, "localhost:1000", cfg.Proxy[0].Routes[0].Upstream)

	// Second listener
	assert.Equal(t, ":443", cfg.Proxy[1].Listen)
	assert.True(t, cfg.Proxy[1].TLSTailscale)
	require.Len(t, cfg.Proxy[1].Routes, 3)
	assert.Equal(t, "askws", cfg.Proxy[1].Routes[0].PathRegexp)
	assert.Equal(t, "localhost:3000", cfg.Proxy[1].Routes[0].Upstream)
	assert.Equal(t, "^/api/.+", cfg.Proxy[1].Routes[1].PathRegexp)
	assert.Equal(t, "localhost:3001", cfg.Proxy[1].Routes[1].Upstream)
	assert.Equal(t, "", cfg.Proxy[1].Routes[2].PathRegexp)
	assert.Equal(t, "localhost:3000", cfg.Proxy[1].Routes[2].Upstream)
}

func TestLoader_Load_ProxyConfig_WithTLSCertKey(t *testing.T) {
	configContent := `{
		version: "1.0"
		project: { name: "test" }
		proxy: [
			{
				listen: ":443"
				tls_cert: "~/.trellis/cert.pem"
				tls_key: "~/.trellis/key.pem"
				routes: [
					{ upstream: "localhost:3000" }
				]
			}
		]
	}`

	cfg := loadFromString(t, configContent)

	require.Len(t, cfg.Proxy, 1)
	assert.Equal(t, "~/.trellis/cert.pem", cfg.Proxy[0].TLSCert)
	assert.Equal(t, "~/.trellis/key.pem", cfg.Proxy[0].TLSKey)
	assert.False(t, cfg.Proxy[0].TLSTailscale)
}

func TestServiceConfig_IsWatching_Defaults(t *testing.T) {
	tests := []struct {
		name     string
		watching *bool
		expected bool
	}{
		{"nil defaults to true", nil, true},
		{"explicit true", boolPtr(true), true},
		{"explicit false", boolPtr(false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := ServiceConfig{Watching: tt.watching}
			assert.Equal(t, tt.expected, svc.IsWatching())
		})
	}
}

func TestServiceConfig_IsEnabled_Defaults(t *testing.T) {
	tests := []struct {
		name     string
		enabled  *bool
		expected bool
	}{
		{"nil defaults to true", nil, true},
		{"explicit true", boolPtr(true), true},
		{"explicit false", boolPtr(false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := ServiceConfig{Enabled: tt.enabled}
			assert.Equal(t, tt.expected, svc.IsEnabled())
		})
	}
}

func TestServiceConfig_GetBinaryPath(t *testing.T) {
	tests := []struct {
		name        string
		svc         ServiceConfig
		expected    string
	}{
		{
			name: "watch_binary takes precedence",
			svc: ServiceConfig{
				Command:     "./bin/api",
				WatchBinary: "./bin/actual-api",
			},
			expected: "./bin/actual-api",
		},
		{
			name: "extract from string command",
			svc: ServiceConfig{
				Command: "./bin/api -port 8080",
			},
			expected: "./bin/api",
		},
		{
			name: "extract from array command",
			svc: ServiceConfig{
				Command: []string{"./bin/worker", "-queue", "default"},
			},
			expected: "./bin/worker",
		},
		{
			name: "empty command",
			svc: ServiceConfig{
				Command: "",
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.svc.GetBinaryPath())
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		def      string
		expected string
	}{
		{"500ms", "100ms", "500ms"},
		{"1m", "100ms", "1m"},
		{"", "100ms", "100ms"},
		{"invalid", "100ms", "100ms"},
		{"1h30m", "100ms", "1h30m"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			defDur := mustParseDuration(tt.def)
			result := ParseDuration(tt.input, defDur)
			assert.Equal(t, mustParseDuration(tt.expected), result)
		})
	}
}

// Helper functions

func loadFromString(t *testing.T, content string) *Config {
	t.Helper()
	path := writeTestConfig(t, content)
	loader := NewLoader()
	cfg, err := loader.Load(context.Background(), path)
	require.NoError(t, err)
	return cfg
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "trellis.hjson")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

func boolPtr(b bool) *bool {
	return &b
}

func mustParseDuration(s string) time.Duration {
	dur, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return dur
}
