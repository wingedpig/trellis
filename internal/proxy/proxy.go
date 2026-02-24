// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package proxy implements reverse proxy listeners with path-based routing.
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tailscale/tscert"
	"github.com/wingedpig/trellis/internal/config"
)

// Manager manages multiple proxy listeners.
type Manager struct {
	listeners []*Listener
	mu        sync.Mutex
}

// Listener represents a single proxy listener with routes.
type Listener struct {
	addr   string
	server *http.Server
	routes []route
}

// route is a compiled proxy route.
type route struct {
	pattern  *regexp.Regexp // nil means catch-all
	upstream *url.URL
	proxy    *httputil.ReverseProxy
}

// NewManager creates a new proxy manager from config.
// Compiles regexes and sets up reverse proxies. Returns an error if any
// regex is invalid (fail fast at startup).
func NewManager(configs []config.ProxyListenerConfig) (*Manager, error) {
	m := &Manager{}

	for i, cfg := range configs {
		listener, err := newListener(cfg)
		if err != nil {
			return nil, fmt.Errorf("proxy[%d]: %w", i, err)
		}
		m.listeners = append(m.listeners, listener)
	}

	return m, nil
}

func newListener(cfg config.ProxyListenerConfig) (*Listener, error) {
	l := &Listener{
		addr: cfg.Listen,
	}

	for j, routeCfg := range cfg.Routes {
		r, err := newRoute(routeCfg)
		if err != nil {
			return nil, fmt.Errorf("routes[%d]: %w", j, err)
		}
		l.routes = append(l.routes, r)
	}

	handler := http.HandlerFunc(l.serveHTTP)

	l.server = &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Configure TLS
	if cfg.TLSTailscale {
		// Use Tailscale daemon for automatic TLS certificates
		l.server.TLSConfig = &tls.Config{
			GetCertificate: tscert.GetCertificate,
		}
	} else if cfg.TLSCert != "" && cfg.TLSKey != "" {
		certPath := expandPath(cfg.TLSCert)
		keyPath := expandPath(cfg.TLSKey)

		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load TLS cert/key: %w", err)
		}
		l.server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
	}

	return l, nil
}

func newRoute(cfg config.ProxyRouteConfig) (route, error) {
	r := route{}

	// Compile path regex if specified
	if cfg.PathRegexp != "" {
		re, err := regexp.Compile(cfg.PathRegexp)
		if err != nil {
			return r, fmt.Errorf("invalid path_regexp %q: %w", cfg.PathRegexp, err)
		}
		r.pattern = re
	}

	// Parse upstream address
	upstream := cfg.Upstream
	if !strings.Contains(upstream, "://") {
		upstream = "http://" + upstream
	}
	u, err := url.Parse(upstream)
	if err != nil {
		return r, fmt.Errorf("invalid upstream %q: %w", cfg.Upstream, err)
	}
	r.upstream = u

	// Create reverse proxy with a transport configured for proxying
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.FlushInterval = -1 // Immediate flushing for streaming
	proxy.Transport = &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// Custom director to preserve original Host and add forwarding headers
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origHost := req.Host
		originalDirector(req)
		// Preserve the original client Host header (like Caddy/Nginx default behavior)
		// The default director sets req.Host to the upstream, but backends typically
		// need the original host for virtual hosting and request validation.
		req.Host = origHost
		// Standard forwarding headers
		if req.Header.Get("X-Forwarded-Host") == "" {
			req.Header.Set("X-Forwarded-Host", origHost)
		}
		if req.Header.Get("X-Forwarded-Proto") == "" {
			if req.TLS != nil {
				req.Header.Set("X-Forwarded-Proto", "https")
			} else {
				req.Header.Set("X-Forwarded-Proto", "http")
			}
		}
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		// Client disconnected — not a proxy error, don't log or write a response
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Printf("Proxy error [%s %s -> %s]: %v", req.Method, req.URL.Path, u.Host, err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

	r.proxy = proxy
	return r, nil
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseWriter optional interfaces (Flusher, Hijacker) pass through.
func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

// serveHTTP routes the request to the first matching route.
func (l *Listener) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// Check for WebSocket upgrade
	if isWebSocket(r) {
		l.serveWebSocket(w, r)
		return
	}

	start := time.Now()
	for _, route := range l.routes {
		if route.pattern == nil || route.pattern.MatchString(r.URL.Path) {
			rec := &statusRecorder{ResponseWriter: w, statusCode: 200}
			route.proxy.ServeHTTP(rec, r)
			elapsed := time.Since(start)
			// Log slow requests and server errors, but not client cancellations
			if (elapsed >= 5*time.Second || rec.statusCode >= 500) && r.Context().Err() == nil {
				log.Printf("Proxy: %s %s -> %s [%d] (%s)", r.Method, r.URL.Path, route.upstream.Host, rec.statusCode, elapsed.Round(time.Millisecond))
			}
			return
		}
	}

	// No route matched (shouldn't happen if config has a catch-all)
	http.Error(w, "No matching route", http.StatusBadGateway)
}

// serveWebSocket handles WebSocket upgrade requests by tunneling the
// connection to the matched upstream.
func (l *Listener) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	// Find matching route
	var target *url.URL
	for _, route := range l.routes {
		if route.pattern == nil || route.pattern.MatchString(r.URL.Path) {
			target = route.upstream
			break
		}
	}
	if target == nil {
		http.Error(w, "No matching route", http.StatusBadGateway)
		return
	}

	// Dial upstream
	targetAddr := target.Host
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	upstreamConn, err := dialer.Dial("tcp", targetAddr)
	if err != nil {
		log.Printf("WebSocket proxy: failed to connect to %s: %v", targetAddr, err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	// Hijack the client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstreamConn.Close()
		http.Error(w, "WebSocket hijack not supported", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		upstreamConn.Close()
		log.Printf("WebSocket proxy: hijack failed: %v", err)
		return
	}

	// Write the original HTTP request to the upstream connection (preserving Upgrade headers)
	if err := r.Write(upstreamConn); err != nil {
		clientConn.Close()
		upstreamConn.Close()
		log.Printf("WebSocket proxy: failed to write request to upstream: %v", err)
		return
	}

	// Bidirectional copy — when one direction closes, shut down the other
	// so neither side hangs on a dead connection.
	var wg sync.WaitGroup
	wg.Add(2)

	// upstream -> client
	go func() {
		defer wg.Done()
		io.Copy(clientConn, upstreamConn)
		// Upstream closed or errored; signal client that no more data is coming
		if tc, ok := clientConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		} else {
			clientConn.Close()
		}
	}()

	// client -> upstream (flush any buffered data first)
	go func() {
		defer wg.Done()
		if clientBuf.Reader.Buffered() > 0 {
			buffered := make([]byte, clientBuf.Reader.Buffered())
			clientBuf.Read(buffered)
			upstreamConn.Write(buffered)
		}
		io.Copy(upstreamConn, clientConn)
		// Client closed or errored; signal upstream that no more data is coming
		if tc, ok := upstreamConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		} else {
			upstreamConn.Close()
		}
	}()

	wg.Wait()
	clientConn.Close()
	upstreamConn.Close()
}

// isWebSocket returns true if the request is a WebSocket upgrade request.
func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// Start starts all proxy listeners. Each listener runs in its own goroutine.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, l := range m.listeners {
		listener := l // capture for goroutine
		go func() {
			var err error
			if listener.server.TLSConfig != nil {
				log.Printf("Proxy listener starting on %s (TLS)", listener.addr)
				// TLS certs are already loaded in TLSConfig, use empty paths
				err = listener.server.ListenAndServeTLS("", "")
			} else {
				log.Printf("Proxy listener starting on %s", listener.addr)
				err = listener.server.ListenAndServe()
			}
			if err != nil && err != http.ErrServerClosed {
				log.Printf("Proxy listener %s error: %v", listener.addr, err)
			}
		}()
	}

	return nil
}

// Shutdown gracefully shuts down all proxy listeners.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for _, l := range m.listeners {
		if err := l.server.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down proxy listener %s: %v", l.addr, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// expandPath expands ~ to home directory.
func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}
