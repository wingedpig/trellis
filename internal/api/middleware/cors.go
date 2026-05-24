// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// CORSConfig configures the CORS middleware.
type CORSConfig struct {
	// AllowedOrigins is the explicit allow-list of full origins
	// (scheme://host[:port]) permitted to talk to the API and open WebSockets.
	// Loopback origins (localhost/127.0.0.1/::1) are always implicitly allowed
	// regardless of this list — see IsAllowedOrigin / IsAllowedHost.
	AllowedOrigins []string

	// PermitAnyHost disables the Host-header allow-list check (the
	// DNS-rebinding gate). Set this when the server is bound to a wildcard or
	// non-loopback address: the operator has deliberately opted into wide
	// network access, every host pointed at this machine can already reach
	// the server, and we can't enumerate every hostname clients might use.
	// Origin-based CORS still applies, so browser-driven cross-origin attacks
	// remain blocked.
	PermitAnyHost bool
}

// CORS returns middleware that enforces a same-origin policy on the server.
//
// Two checks run before any handler executes:
//
//  1. The Host header must point at a hostname this server is reachable under:
//     loopback names (localhost/127.0.0.1/::1) or a host that appears in
//     cfg.AllowedOrigins. This defeats DNS-rebinding attacks where the
//     attacker's domain resolves to 127.0.0.1 — both Origin and Host would
//     then be the attacker's hostname, which fails the allow-list check.
//
//  2. If an Origin header is present (any browser-initiated request), it must
//     match — scheme and all — either a loopback origin whose host equals the
//     request's Host, or an entry in cfg.AllowedOrigins. Scheme is part of
//     the match so http://example.com is not considered same-origin for
//     https://example.com.
//
// Requests with no Origin header (non-browser clients such as trellis-ctl)
// still pass the Host check, which is what blocks DNS-rebinding-style abuse
// against endpoints that don't require CORS.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowed := normalizeOrigins(cfg.AllowedOrigins)
	permitAnyHost := cfg.PermitAnyHost
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !permitAnyHost && !IsAllowedHost(r, allowed) {
				http.Error(w, "host not allowed", http.StatusForbidden)
				return
			}

			origin := r.Header.Get("Origin")
			if origin != "" {
				ok := IsAllowedOrigin(r, origin, allowed)
				if !ok && permitAnyHost {
					// Wildcard / non-loopback bind: trust same-origin via
					// the Host header. The operator has opted into wide
					// network access, so a browser loading the UI at the
					// same host:port it then calls back is treated as
					// same-origin. Scheme is checked against r.TLS so an
					// http page can't claim to be the https server.
					ok = IsSameOriginRequest(r, origin)
				}
				if !ok {
					w.Header().Set("Vary", "Origin")
					http.Error(w, "origin not allowed", http.StatusForbidden)
					return
				}
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// IsAllowedOrigin reports whether the given Origin header value is permitted
// for the request r. The match is exact on scheme+host (no host-substring
// matches and no scheme downgrade), with one narrow shortcut: a loopback
// origin (localhost, 127.0.0.1, ::1) is allowed when the request's Host
// header has the same host and the schemes match. The shortcut is safe
// against DNS rebinding because the loopback names cannot be redirected to
// a different origin.
//
// `allowed` is the normalized form produced by normalizeOrigins (lowercased,
// trailing slashes stripped, full scheme://host strings).
func IsAllowedOrigin(r *http.Request, origin string, allowed []string) bool {
	if origin == "" {
		return false
	}
	o, err := url.Parse(origin)
	if err != nil || o.Host == "" || o.Scheme == "" {
		return false
	}
	normalized := strings.ToLower(o.Scheme + "://" + o.Host)
	for _, a := range allowed {
		if a == normalized {
			return true
		}
	}
	if isLoopbackHostport(o.Host) &&
		strings.EqualFold(strings.TrimSpace(r.Host), o.Host) &&
		strings.EqualFold(o.Scheme, requestScheme(r)) {
		return true
	}
	return false
}

// IsSameOriginRequest reports whether the Origin header represents the same
// origin as the request itself: same scheme (derived from r.TLS) and same
// host[:port] as the Host header. Callers should only consult this when the
// server has opted out of the Host-header allow-list (PermitAnyHost) — it's
// the "wildcard bind" same-origin path, where the operator has accepted
// that any reachable hostname can drive the server.
//
// Scheme is taken from r.TLS rather than X-Forwarded-Proto to avoid trusting
// arbitrary proxy headers; behind a TLS-terminating proxy, configure
// `public_url`/`allowed_origins` explicitly.
func IsSameOriginRequest(r *http.Request, origin string) bool {
	o, err := url.Parse(origin)
	if err != nil || o.Host == "" || o.Scheme == "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(r.Host), o.Host) {
		return false
	}
	return strings.EqualFold(o.Scheme, requestScheme(r))
}

// requestScheme returns the scheme the client is using to reach this server,
// based on the actual transport (r.TLS) rather than any proxy header.
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// IsAllowedHost reports whether the request's Host header points at a
// hostname this server should accept. Loopback names are always allowed;
// other hosts must appear in the allow-list (matched on host[:port],
// ignoring scheme). This is the DNS-rebinding gate: an attacker hostname
// that resolves to 127.0.0.1 produces Host: <attacker>, which is rejected
// unless the operator has explicitly added it.
func IsAllowedHost(r *http.Request, allowed []string) bool {
	host := strings.ToLower(strings.TrimSpace(r.Host))
	if host == "" {
		// Required by HTTP/1.1 — refuse if missing.
		return false
	}
	if isLoopbackHostport(host) {
		return true
	}
	for _, a := range allowed {
		u, err := url.Parse(a)
		if err != nil {
			continue
		}
		if strings.EqualFold(u.Host, host) {
			return true
		}
	}
	return false
}

// IsLoopbackHost reports whether the given hostname or "host:port" value
// refers to a loopback address. Used by configuration code to decide whether
// the server is bound loopback-only (strict Host gate) or wide (relaxed).
func IsLoopbackHost(host string) bool { return isLoopbackHostport(host) }

// IsWildcardBindHost reports whether the value names a wildcard bind
// address ("0.0.0.0", "::", "[::]", or empty after defaults). These cannot
// appear in a real Host header, so we never add them to the allow-list.
func IsWildcardBindHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.Trim(h, "[]")
	switch h {
	case "", "0.0.0.0", "::":
		return true
	}
	return false
}

// isLoopbackHostport reports whether a "host" or "host:port" value refers
// to a loopback name. Loopback names cannot be DNS-rebound to a different
// destination, so we trust them as authoritative same-origin authority.
func isLoopbackHostport(host string) bool {
	bare, _, err := net.SplitHostPort(host)
	if err != nil {
		bare = host
	}
	bare = strings.ToLower(strings.Trim(bare, "[]"))
	switch bare {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	if ip := net.ParseIP(bare); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// NormalizeOrigins is the exported form of normalizeOrigins, for callers that
// need to feed an allow-list to IsAllowedOrigin / IsAllowedHost directly
// (such as the WebSocket upgrader factory).
func NormalizeOrigins(origins []string) []string { return normalizeOrigins(origins) }

// normalizeOrigins lowercases and trims a list of origin URLs, dropping empty
// entries. Callers should pass full origins such as "https://example.com".
// The returned slice is in the form "scheme://host" (lowercased, no trailing
// slash, no path), suitable for exact comparison by IsAllowedOrigin.
func normalizeOrigins(origins []string) []string {
	if len(origins) == 0 {
		return nil
	}
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		u, err := url.Parse(o)
		if err != nil || u.Scheme == "" || u.Host == "" {
			continue
		}
		out = append(out, strings.ToLower(u.Scheme+"://"+u.Host))
	}
	return out
}
