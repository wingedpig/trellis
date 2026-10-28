// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/hjson/hjson-go/v4"
)

// APIBaseURL returns the URL clients (trellis-ctl, terminals via TRELLIS_API)
// should use to reach this Trellis instance.
//
// Resolution order:
//  1. Server.PublicURL when set (use verbatim, trailing slash trimmed).
//  2. Otherwise build "<scheme>://<host>:<port>" where scheme is "https" if
//     Tailscale TLS or both TLS cert and key are set, and bind-all addresses
//     ("", "0.0.0.0", "::", "[::]") are rewritten to "127.0.0.1".
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
	if c.Server.TLSTailscale || (c.Server.TLSCert != "" && c.Server.TLSKey != "") {
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
	cfg, _, err := l.load(path)
	return cfg, err
}

// load is Load that also returns the parsed HJSON, for unknown-key checks.
func (l *Loader) load(path string) (*Config, map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read config: %w", err)
	}

	// Parse HJSON to intermediate map
	var raw map[string]interface{}
	if err := hjson.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("parse hjson: %w", err)
	}

	// Convert to JSON and unmarshal to struct (for type safety)
	jsonData, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("convert to json: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(jsonData, &cfg); err != nil {
		return nil, nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &cfg, raw, nil
}

// LoadWithDefaults loads config with default values applied. The config is
// also passed through the schema validator so invalid ports, duplicate names,
// bad duration strings, broken cross-references, and similar mistakes show
// up at startup instead of surfacing as obscure runtime failures.
//
// Validation errors are reported as warnings rather than aborting startup:
// an existing config with a benign typo (e.g. a misspelled log_viewer name
// referenced from trace_groups) should not block the whole server. The
// invalid items will still fail at the point of use, but with their normal
// runtime error messages. Keys the schema does not know are warned about the
// same way, since they are otherwise dropped without a trace.
func (l *Loader) LoadWithDefaults(ctx context.Context, path string) (*Config, error) {
	cfg, raw, err := l.load(path)
	if err != nil {
		return nil, err
	}

	for _, w := range UnknownKeys(raw) {
		log.Printf("config warning: %s", w)
	}

	applyDefaults(cfg)

	if err := NewValidator().Validate(cfg); err != nil {
		var verr *ValidationError
		if errors.As(err, &verr) {
			for _, fe := range verr.Errors {
				log.Printf("config warning: %s: %s", fe.Field, fe.Message)
			}
		} else {
			log.Printf("config warning: %v", err)
		}
	}

	return cfg, nil
}

// UnknownKeys returns a warning for each key in raw (parsed HJSON) that no
// Config field reads, sorted by path. HJSON has no dotted-key syntax, so
// "cases.commit_hook: true" is one top-level key named "cases.commit_hook";
// those get a hint showing the nested form.
func UnknownKeys(raw map[string]interface{}) []string {
	var warnings []string
	unknownKeys(raw, reflect.TypeOf(Config{}), "", &warnings)
	sort.Strings(warnings)
	return warnings
}

func unknownKeys(v interface{}, t reflect.Type, path string, warnings *[]string) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]interface{})
		if !ok {
			return
		}
		for key, val := range m {
			keyPath := key
			if path != "" {
				keyPath = path + "." + key
			}
			ft, ok := jsonField(t, key)
			if !ok {
				*warnings = append(*warnings, unknownKeyWarning(t, key, keyPath))
				continue
			}
			unknownKeys(val, ft, keyPath, warnings)
		}
	case reflect.Slice, reflect.Array:
		items, ok := v.([]interface{})
		if !ok {
			return
		}
		for i, item := range items {
			unknownKeys(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i), warnings)
		}
	case reflect.Map:
		m, ok := v.(map[string]interface{})
		if !ok {
			return
		}
		for key, val := range m {
			unknownKeys(val, t.Elem(), path+"."+key, warnings)
		}
	}
}

// jsonField returns the type of t's field that encoding/json would decode
// key into. Like encoding/json, the match ignores case.
func jsonField(t reflect.Type, key string) (reflect.Type, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if strings.EqualFold(name, key) {
			return f.Type, true
		}
	}
	return nil, false
}

func unknownKeyWarning(t reflect.Type, key, keyPath string) string {
	if head, rest, ok := strings.Cut(key, "."); ok {
		if _, known := jsonField(t, head); known {
			parts := strings.Split(rest, ".")
			nested := strings.Join(parts, ": { ") + ": ..." + strings.Repeat(" }", len(parts)-1)
			return fmt.Sprintf("%s: unknown key (HJSON has no dotted keys; write %s: { %s })", keyPath, head, nested)
		}
	}
	return fmt.Sprintf("%s: unknown key", keyPath)
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
	// Schema version. The validator requires this, but `trellis init` and
	// hand-written README examples have historically omitted it, so default
	// rather than fail at startup. Bump alongside schema-breaking changes.
	if cfg.Version == "" {
		cfg.Version = "1.0"
	}

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
