// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Structural checks for tailscale_nodes and the listeners that reference
// them. They are shared by the validator (which only warns) and by
// tsnode.NewRegistry / proxy.NewManager (which abort startup on any of them).

var tailscaleHostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateTailscaleHostname checks that h is a lowercase DNS label.
func ValidateTailscaleHostname(h string) error {
	if h == "" {
		return errors.New("is required")
	}
	if !tailscaleHostnameRe.MatchString(h) {
		return fmt.Errorf("invalid hostname %q: must be a DNS label ([a-z0-9-], 1-63 chars, no leading or trailing hyphen)", h)
	}
	return nil
}

// TailscaleNodeStateDir returns the configured state directory for a node,
// or the default ~/.trellis/tsnet/<hostname>. The result is not canonical.
func TailscaleNodeStateDir(n TailscaleNodeConfig) string {
	if n.StateDir != "" {
		return n.StateDir
	}
	return filepath.Join("~", ".trellis", "tsnet", n.Hostname)
}

// CanonicalStateDir canonicalizes a state directory: ~ expansion, Abs, Clean,
// then EvalSymlinks. When the directory doesn't exist yet, symlinks are
// resolved on its longest existing ancestor and the rest is appended.
func CanonicalStateDir(dir string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		dir = filepath.Join(home, dir[1:])
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)

	existing, rest := abs, ""
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
}

// pathWithin reports whether a equals b or lies inside it.
func pathWithin(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasPrefix(a, strings.TrimSuffix(b, string(filepath.Separator))+string(filepath.Separator))
}

// CheckTailscaleNodes returns structural errors in the node declarations:
// missing, invalid or duplicate hostnames, and duplicate or nested state
// directories. canonical maps each valid hostname to its canonical state_dir.
func CheckTailscaleNodes(nodes []TailscaleNodeConfig) (canonical map[string]string, errs []FieldError) {
	canonical = make(map[string]string)
	seen := make(map[string]bool)
	type dirEntry struct{ dir, field string }
	var dirs []dirEntry

	for i, n := range nodes {
		prefix := fmt.Sprintf("tailscale_nodes[%d]", i)
		if err := ValidateTailscaleHostname(n.Hostname); err != nil {
			errs = append(errs, FieldError{prefix + ".hostname", err.Error()})
			continue
		}
		if seen[n.Hostname] {
			errs = append(errs, FieldError{prefix + ".hostname", fmt.Sprintf("duplicate tailscale node hostname '%s'", n.Hostname)})
			continue
		}
		seen[n.Hostname] = true
		dir, err := CanonicalStateDir(TailscaleNodeStateDir(n))
		if err != nil {
			errs = append(errs, FieldError{prefix + ".state_dir", fmt.Sprintf("cannot resolve %q: %v", TailscaleNodeStateDir(n), err)})
			continue
		}
		canonical[n.Hostname] = dir
		field := prefix + ".state_dir"
		for _, d := range dirs {
			switch {
			case d.dir == dir:
				errs = append(errs, FieldError{field, fmt.Sprintf("state directory %s is also used by %s; each node needs its own", dir, d.field)})
			case pathWithin(dir, d.dir) || pathWithin(d.dir, dir):
				errs = append(errs, FieldError{field, fmt.Sprintf("state directory %s is nested with %s (%s)", dir, d.field, d.dir)})
			}
		}
		dirs = append(dirs, dirEntry{dir, field})
	}
	return canonical, errs
}

// TailscaleNodeListenPort parses the listen value of a listener bound on a
// Tailscale node. It must be a bare port form (":443"); a host part is an
// error because the node has exactly one address set.
func TailscaleNodeListenPort(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("listen %q must be a bare port like \":443\" when tailscale_node is set", listen)
	}
	if host != "" {
		return "", fmt.Errorf("listen %q must not include a host when tailscale_node is set; use \":%s\"", listen, port)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("listen %q has an invalid port", listen)
	}
	// Normalize so equivalent spellings (":443", ":0443") compare equal.
	return strconv.Itoa(n), nil
}

// CheckProxyTailscaleNodes returns structural errors in proxy listeners that
// set tailscale_node: undeclared nodes, TLS options that conflict with the
// node's automatic TLS, and listen values with a host part or no port.
// Port conflicts are checked separately by CheckTailscaleNodePortConflicts.
func CheckProxyTailscaleNodes(listeners []ProxyListenerConfig, declared map[string]bool) []FieldError {
	var errs []FieldError
	for i, l := range listeners {
		if l.TailscaleNode == "" {
			continue
		}
		prefix := fmt.Sprintf("proxy[%d]", i)
		if !declared[l.TailscaleNode] {
			errs = append(errs, FieldError{prefix + ".tailscale_node", fmt.Sprintf("references undeclared tailscale node '%s'", l.TailscaleNode)})
		}
		if l.TLSTailscale || l.TLSCert != "" || l.TLSKey != "" {
			errs = append(errs, FieldError{prefix, "tailscale_node provides TLS automatically; tls_tailscale, tls_cert and tls_key must be unset"})
		}
		// Templated listen values can't be checked until expansion.
		if strings.Contains(l.Listen, "{{") {
			continue
		}
		if _, err := TailscaleNodeListenPort(l.Listen); err != nil {
			errs = append(errs, FieldError{prefix + ".listen", err.Error()})
		}
	}
	return errs
}

// CheckTailscaleNodePortConflicts returns an error for each listener that
// binds the same port on the same Tailscale node as an earlier listener.
func CheckTailscaleNodePortConflicts(listeners []ProxyListenerConfig) []FieldError {
	var errs []FieldError
	seen := make(map[string]string) // "node\x00port" -> field
	for i, l := range listeners {
		if l.TailscaleNode == "" {
			continue
		}
		port, err := TailscaleNodeListenPort(l.Listen)
		if err != nil {
			continue
		}
		field := fmt.Sprintf("proxy[%d].listen", i)
		key := l.TailscaleNode + "\x00" + port
		if other, ok := seen[key]; ok {
			errs = append(errs, FieldError{field, fmt.Sprintf("listen address conflicts with %s (both bind port %s on tailscale node %s)", other, port, l.TailscaleNode)})
			continue
		}
		seen[key] = field
	}
	return errs
}

// ReferencedTailscaleNodes returns the distinct node hostnames referenced by
// proxy listeners, in first-reference order.
func ReferencedTailscaleNodes(listeners []ProxyListenerConfig) []string {
	var out []string
	seen := make(map[string]bool)
	for _, l := range listeners {
		if l.TailscaleNode != "" && !seen[l.TailscaleNode] {
			seen[l.TailscaleNode] = true
			out = append(out, l.TailscaleNode)
		}
	}
	return out
}

// FieldErrorsToError joins field errors into a single error, or nil.
func FieldErrorsToError(errs []FieldError) error {
	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{Errors: errs}
}
