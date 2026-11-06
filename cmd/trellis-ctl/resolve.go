// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"

	"github.com/wingedpig/trellis/internal/config"
)

// apiSource says where apiURL came from, for error messages.
var apiSource string

// apiSourceKind classifies apiSource, to pick the fix to suggest.
var apiSourceKind string // "env", "config", "running", "default"

// resolveAPIURL picks the API URL, in this order:
//  1. TRELLIS_API (set in Trellis terminals and agent sessions)
//  2. -config flag / TRELLIS_CONFIG
//  3. trellis.hjson found by walking up from the cwd
//  4. the Trellis server running on this machine (~/.trellis/run)
//  5. http://localhost:1234
func resolveAPIURL(configPath string) {
	if env := os.Getenv("TRELLIS_API"); env != "" {
		apiURL = strings.TrimSuffix(env, "/")
		apiSource = "the TRELLIS_API environment variable"
		apiSourceKind = "env"
		return
	}
	if configPath == "" {
		configPath = os.Getenv("TRELLIS_CONFIG")
	}
	url, path, err := apiURLFromConfig(configPath)
	if err == nil {
		apiURL = url
		apiSource = path
		apiSourceKind = "config"
		return
	}
	if configPath != "" {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	if servers := config.LiveServers(); len(servers) > 0 {
		s := servers[0]
		apiURL = strings.TrimSuffix(s.URL, "/")
		apiSource = fmt.Sprintf("the running Trellis server for %s", s.Config)
		apiSourceKind = "running"
		if len(servers) > 1 {
			fmt.Fprintf(os.Stderr, "Note: %d Trellis servers are running; using %s (%s). Pass -config or set TRELLIS_API to choose another.\n",
				len(servers), apiURL, s.Config)
		}
		return
	}
	apiURL = defaultAPIURL
	apiSource = "the built-in default"
	apiSourceKind = "default"
}

// apiURLFromConfig resolves the API URL from a Trellis config file. If path
// is empty, walks up from the current directory looking for trellis.hjson /
// trellis.json. Returns the URL and the config file it came from.
func apiURLFromConfig(path string) (string, string, error) {
	loader := config.NewLoader()
	if path == "" {
		found, err := loader.FindConfigUp("")
		if err != nil {
			return "", "", err
		}
		path = found
	}

	// The loader logs config warnings (unknown keys and such) meant for the
	// server operator; here they would only clutter every command's output.
	log.SetOutput(io.Discard)
	cfg, err := loader.LoadWithDefaults(context.Background(), path)
	log.SetOutput(os.Stderr)
	if err != nil {
		return "", "", fmt.Errorf("load %s: %w", path, err)
	}
	return cfg.APIBaseURL(), path, nil
}

// connectionHint explains a failure to reach the server: where the URL came
// from and how to point trellis-ctl elsewhere. Empty for other errors.
func connectionHint(err error) string {
	var opErr *net.OpError
	var dnsErr *net.DNSError
	if !errors.As(err, &opErr) && !errors.As(err, &dnsErr) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Could not reach Trellis at %s (address from %s).\n", apiURL, apiSource)
	if apiSourceKind == "default" {
		b.WriteString("No TRELLIS_API is set, no trellis.hjson was found above this directory, " +
			"and no running Trellis server is recorded in ~/.trellis/run (servers record themselves there when they start).\n")
	}
	for _, s := range config.LiveServers() {
		if strings.TrimSuffix(s.URL, "/") != apiURL {
			fmt.Fprintf(&b, "A Trellis server is running at %s (config %s).\n", s.URL, s.Config)
		}
	}
	switch apiSourceKind {
	case "env":
		b.WriteString("Fix: correct TRELLIS_API, or unset it to use the config file or running server.")
	case "config":
		b.WriteString("Fix: start Trellis, or if it listens elsewhere set server.public_url in " + apiSource +
			", or set TRELLIS_API to the server's URL.")
	default:
		b.WriteString("Fix: set TRELLIS_API to the server's URL (e.g. export TRELLIS_API=https://host:1234), " +
			"or pass -config /path/to/trellis.hjson.")
	}
	return b.String()
}
