// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hjson/hjson-go/v4"
)

// APIBaseURL returns the URL clients (trellis-ctl, terminals via TRELLIS_API)
// should use to reach this Trellis instance.
//
// Resolution order:
//  1. Server.PublicURL when set (use verbatim, trailing slash trimmed).
//  2. Otherwise build "<scheme>://<host>:<port>" where scheme is "https" if
//     both TLS cert and key are set, and bind-all addresses ("", "0.0.0.0",
//     "::", "[::]") are rewritten to "127.0.0.1".
//
// PublicURL is needed when bind address differs from the address clients
// should use — for example, when binding to a Tailscale IP whose certificate
// is issued for the machine's Tailscale hostname rather than the IP.
func (c *Config) APIBaseURL() string {
	if u := strings.TrimRight(c.Server.PublicURL, "/"); u != "" {
		return u
	}
	host := c.Server.Host
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	scheme := "http"
	if c.Server.TLSCert != "" && c.Server.TLSKey != "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, c.Server.Port)
}

// Loader handles configuration file loading.
type Loader struct{}

// NewLoader creates a new config loader.
func NewLoader() *Loader {
	return &Loader{}
}

// Load reads and parses the configuration from the given path.
func (l *Loader) Load(ctx context.Context, path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Parse HJSON to intermediate map
	var raw map[string]interface{}
	if err := hjson.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse hjson: %w", err)
	}

	// Convert to JSON and unmarshal to struct (for type safety)
	jsonData, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("convert to json: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(jsonData, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &cfg, nil
}

// LoadWithDefaults loads config with default values applied.
func (l *Loader) LoadWithDefaults(ctx context.Context, path string) (*Config, error) {
	cfg, err := l.Load(ctx, path)
	if err != nil {
		return nil, err
	}

	applyDefaults(cfg)
	return cfg, nil
}

// FindConfig searches for a config file in the current directory.
// It looks for trellis.hjson first, then trellis.json.
func (l *Loader) FindConfig() (string, error) {
	candidates := []string{
		"trellis.hjson",
		"trellis.json",
	}

	for _, name := range candidates {
		path := filepath.Join(".", name)
		if _, err := os.Stat(path); err == nil {
			abs, err := filepath.Abs(path)
			if err != nil {
				return path, nil
			}
			return abs, nil
		}
	}

	return "", fmt.Errorf("config file not found (looked for trellis.hjson, trellis.json)")
}

// FindConfigUp searches for a config file starting at startDir and walking up
// the directory tree until one is found or the filesystem root is reached.
// It looks for trellis.hjson first, then trellis.json at each level.
// If startDir is empty, the current working directory is used.
func (l *Loader) FindConfigUp(startDir string) (string, error) {
	candidates := []string{
		"trellis.hjson",
		"trellis.json",
	}

	dir := startDir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve start directory: %w", err)
	}

	for {
		for _, name := range candidates {
			path := filepath.Join(abs, name)
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}

	return "", fmt.Errorf("config file not found in %s or any parent directory (looked for trellis.hjson, trellis.json)", startDir)
}

// applyDefaults sets default values for missing config fields.
func applyDefaults(cfg *Config) {
	// Server defaults
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 1234
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}

	// Logging defaults
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}

	// Terminal defaults
	if cfg.Terminal.Backend == "" {
		cfg.Terminal.Backend = "tmux"
	}
	if cfg.Terminal.Tmux.HistoryLimit == 0 {
		cfg.Terminal.Tmux.HistoryLimit = 50000
	}
	if cfg.Terminal.Tmux.Shell == "" {
		cfg.Terminal.Tmux.Shell = "/bin/sh"
	}

	// Watch defaults
	if cfg.Watch.Debounce == "" {
		cfg.Watch.Debounce = "100ms"
	}

	// Events defaults
	if cfg.Events.History.MaxEvents == 0 {
		cfg.Events.History.MaxEvents = 10000
	}
	if cfg.Events.History.MaxAge == "" {
		cfg.Events.History.MaxAge = "1h"
	}

	// UI defaults
	if cfg.UI.Theme == "" {
		cfg.UI.Theme = "auto"
	}
	if cfg.UI.Terminal.FontSize == 0 {
		cfg.UI.Terminal.FontSize = 14
	}
	if cfg.UI.Terminal.FontFamily == "" {
		cfg.UI.Terminal.FontFamily = "Monaco, monospace"
	}

	// Log viewer settings defaults
	if cfg.LogViewerSettings.IdleTimeout == "" {
		cfg.LogViewerSettings.IdleTimeout = "5m" // Default 5 minute idle timeout
	}

	// Service defaults
	for i := range cfg.Services {
		if cfg.Services[i].Logging.BufferSize == 0 {
			cfg.Services[i].Logging.BufferSize = 50000
		}
		// Apply logging_defaults to services.logging
		cfg.Services[i].Logging.ApplyDefaults(&cfg.LoggingDefaults)
	}

	// Apply logging_defaults to log_viewers
	for i := range cfg.LogViewers {
		cfg.LogViewers[i].ApplyDefaults(&cfg.LoggingDefaults)
	}

	// Worktree defaults
	if cfg.Worktree.Discovery.Mode == "" {
		cfg.Worktree.Discovery.Mode = "git"
	}

	// Trace defaults
	if cfg.Trace.ReportsDir == "" {
		cfg.Trace.ReportsDir = "traces"
	}
	if cfg.Trace.MaxAge == "" {
		cfg.Trace.MaxAge = "7d"
	}
}
