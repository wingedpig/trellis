// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tsBase(t *testing.T) *Config {
	dir := t.TempDir()
	return &Config{
		Version: "1.0",
		Project: ProjectConfig{Name: "test"},
		Server:  ServerConfig{Host: "127.0.0.1", Port: 8080},
		TailscaleNodes: []TailscaleNodeConfig{
			{Hostname: "ent", StateDir: filepath.Join(dir, "ent")},
		},
		Proxy: []ProxyListenerConfig{
			{Listen: ":443", Routes: []ProxyRouteConfig{{Upstream: "localhost:3000"}}},
			{Listen: ":443", TailscaleNode: "ent", Routes: []ProxyRouteConfig{{Upstream: "localhost:3000"}}},
		},
	}
}

func TestValidator_TailscaleNodes_Valid(t *testing.T) {
	// Host :443 and node :443 live in different bind namespaces.
	assert.NoError(t, NewValidator().Validate(tsBase(t)))
}

func TestValidator_TailscaleNodes_StructuralWarnings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	realDir := filepath.Join(home, "real")
	require.NoError(t, os.MkdirAll(realDir, 0o700))
	require.NoError(t, os.Symlink(realDir, filepath.Join(home, "link")))

	route := []ProxyRouteConfig{{Upstream: "localhost:3000"}}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"invalid hostname", func(c *Config) { c.TailscaleNodes[0].Hostname = "Ent_1" }, "invalid hostname"},
		{"duplicate hostname", func(c *Config) {
			c.TailscaleNodes = append(c.TailscaleNodes, TailscaleNodeConfig{Hostname: "ent", StateDir: home + "/x"})
		}, "duplicate tailscale node hostname"},
		{"state dir tilde vs absolute", func(c *Config) {
			c.TailscaleNodes = []TailscaleNodeConfig{{Hostname: "ent", StateDir: "~/s"}, {Hostname: "b", StateDir: home + "/s/"}}
			c.Proxy = append(c.Proxy, ProxyListenerConfig{Listen: ":443", TailscaleNode: "b", Routes: route})
		}, "also used by"},
		{"state dir symlinked parent", func(c *Config) {
			c.TailscaleNodes = []TailscaleNodeConfig{{Hostname: "ent", StateDir: home + "/link/n"}, {Hostname: "b", StateDir: realDir + "/n"}}
			c.Proxy = append(c.Proxy, ProxyListenerConfig{Listen: ":443", TailscaleNode: "b", Routes: route})
		}, "also used by"},
		{"state dir nested", func(c *Config) {
			c.TailscaleNodes = []TailscaleNodeConfig{{Hostname: "ent", StateDir: home + "/s"}, {Hostname: "b", StateDir: home + "/s/b"}}
			c.Proxy = append(c.Proxy, ProxyListenerConfig{Listen: ":443", TailscaleNode: "b", Routes: route})
		}, "nested"},
		{"undeclared node", func(c *Config) { c.Proxy[1].TailscaleNode = "other" }, "undeclared tailscale node 'other'"},
		{"tls_tailscale", func(c *Config) { c.Proxy[1].TLSTailscale = true }, "must be unset"},
		{"tls cert", func(c *Config) { c.Proxy[1].TLSCert, c.Proxy[1].TLSKey = "c", "k" }, "must be unset"},
		{"host part", func(c *Config) { c.Proxy[1].Listen = "100.64.0.1:443" }, "must not include a host"},
		{"no port", func(c *Config) { c.Proxy[1].Listen = "443" }, "bare port"},
		{"same node and port", func(c *Config) {
			c.Proxy = append(c.Proxy, ProxyListenerConfig{Listen: ":443", TailscaleNode: "ent", Routes: route})
		}, "conflicts with proxy[1].listen (both bind port 443 on tailscale node ent)"},
		{"same node, equivalent port spelling", func(c *Config) {
			c.Proxy = append(c.Proxy, ProxyListenerConfig{Listen: ":0443", TailscaleNode: "ent", Routes: route})
		}, "conflicts with proxy[1].listen (both bind port 443 on tailscale node ent)"},
		{"unreferenced node", func(c *Config) { c.Proxy = c.Proxy[:1] }, "tailscale node ent is declared but not used by any proxy listener"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tsBase(t)
			tt.mutate(cfg)
			err := NewValidator().Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestValidator_TailscaleNodes_DifferentPortsSameNode(t *testing.T) {
	cfg := tsBase(t)
	cfg.Proxy = append(cfg.Proxy, ProxyListenerConfig{Listen: ":1003", TailscaleNode: "ent", Routes: []ProxyRouteConfig{{Upstream: "localhost:3000"}}})
	assert.NoError(t, NewValidator().Validate(cfg))
}

func TestCanonicalStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resolvedHome, err := filepath.EvalSymlinks(home)
	require.NoError(t, err)

	got, err := CanonicalStateDir("~/.trellis/tsnet/ent")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(resolvedHome, ".trellis", "tsnet", "ent"), got)

	got2, err := CanonicalStateDir(home + "/.trellis//tsnet/ent/")
	require.NoError(t, err)
	assert.Equal(t, got, got2)

	assert.Equal(t, filepath.Join("~", ".trellis", "tsnet", "x"), TailscaleNodeStateDir(TailscaleNodeConfig{Hostname: "x"}))
}

func TestLoadTailscaleNodesHJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trellis.hjson")
	require.NoError(t, os.WriteFile(path, []byte(`{
  version: "1.0"
  project: { name: "t" }
  tailscale_nodes: [
    { hostname: "markbook-ent", state_dir: "~/.trellis/tsnet/markbook-ent", auth_key_env: "TRELLIS_TS_AUTHKEY", ephemeral: true }
  ]
  proxy: [
    { listen: ":443", tailscale_node: "markbook-ent", routes: [ { upstream: "localhost:3000" } ] }
  ]
}`), 0o600))
	cfg, err := NewLoader().Load(t.Context(), path)
	require.NoError(t, err)
	require.Len(t, cfg.TailscaleNodes, 1)
	n := cfg.TailscaleNodes[0]
	assert.Equal(t, "markbook-ent", n.Hostname)
	assert.Equal(t, "~/.trellis/tsnet/markbook-ent", n.StateDir)
	assert.Equal(t, "TRELLIS_TS_AUTHKEY", n.AuthKeyEnv)
	assert.True(t, n.Ephemeral)
	assert.Equal(t, "markbook-ent", cfg.Proxy[0].TailscaleNode)
}

func TestCheckTailscaleNodePortConflicts_EquivalentPorts(t *testing.T) {
	errs := CheckTailscaleNodePortConflicts([]ProxyListenerConfig{
		{Listen: ":443", TailscaleNode: "ent"},
		{Listen: ":0443", TailscaleNode: "ent"},
	})
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "both bind port 443")
}
