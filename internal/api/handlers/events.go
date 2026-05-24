// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wingedpig/trellis/internal/api/middleware"
	"github.com/wingedpig/trellis/internal/events"
)

// NewUpgrader builds a *websocket.Upgrader whose CheckOrigin enforces a
// per-router policy: the Origin header must match (scheme and all) either a
// loopback origin whose host equals the request's Host header or an explicit
// entry in cfg.AllowedOrigins. The Host header is also re-validated as
// DNS-rebinding defense (unless cfg.PermitAnyHost is set, mirroring the
// CORS middleware); the CORS middleware ahead of this would normally have
// caught a bad Host, but the WS upgrader keeps a belt-and-braces check in
// case ordering or middleware composition changes.
//
// The policy is captured by the closure, so two routers can carry different
// policies without stepping on each other.
func NewUpgrader(cfg middleware.CORSConfig) *websocket.Upgrader {
	normalized := middleware.NormalizeOrigins(cfg.AllowedOrigins)
	permitAnyHost := cfg.PermitAnyHost
	return &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			if !permitAnyHost && !middleware.IsAllowedHost(r, normalized) {
				log.Printf("WebSocket upgrade rejected: host %q not in allow-list", r.Host)
				return false
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				return false
			}
			if middleware.IsAllowedOrigin(r, origin, normalized) {
				return true
			}
			if permitAnyHost && middleware.IsSameOriginRequest(r, origin) {
				return true
			}
			log.Printf("WebSocket upgrade rejected: origin %q does not match host %q", origin, r.Host)
			return false
		},
	}
}

// defaultUpgrader is the fallback used by handlers that were constructed
// without an explicit upgrader (e.g. unit tests that exercise non-WS methods).
// It fails closed: every CheckOrigin returns false, so attempting an upgrade
// without a configured policy produces a 403 rather than a wide-open accept.
var defaultUpgrader = &websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return false },
}

// upgraderHolder is embedded by handlers that need to perform WebSocket
// upgrades. Router constructors call SetUpgrader once with a per-router
// upgrader; if SetUpgrader is never called, WebSocket methods use
// defaultUpgrader (fail-closed).
type upgraderHolder struct {
	upgrader *websocket.Upgrader
}

// SetUpgrader attaches a *websocket.Upgrader to the handler. Safe to call
// once at router construction time.
func (h *upgraderHolder) SetUpgrader(u *websocket.Upgrader) { h.upgrader = u }

func (h *upgraderHolder) ws() *websocket.Upgrader {
	if h.upgrader != nil {
		return h.upgrader
	}
	return defaultUpgrader
}

// EventHandler handles event-related API requests.
type EventHandler struct {
	upgraderHolder
	bus events.EventBus
}

// NewEventHandler creates a new event handler.
func NewEventHandler(bus events.EventBus) *EventHandler {
	return &EventHandler{bus: bus}
}

// History returns the event history.
func (h *EventHandler) History(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	filter := events.EventFilter{}

	// Parse type filter
	if types := query["type"]; len(types) > 0 {
		filter.Types = types
	}

	// Parse worktree filter
	if wt := query.Get("worktree"); wt != "" {
		filter.Worktree = wt
	}

	// Parse limit
	if limitStr := query.Get("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			filter.Limit = n
		}
	}

	// Parse since
	if sinceStr := query.Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			filter.Since = t
		}
	}

	// Parse until
	if untilStr := query.Get("until"); untilStr != "" {
		if t, err := time.Parse(time.RFC3339, untilStr); err == nil {
			filter.Until = t
		}
	}

	eventList, err := h.bus.History(filter)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, eventList)
}

// WebSocket handles the WebSocket connection for real-time events.
func (h *EventHandler) WebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.ws().Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Get pattern from query
	pattern := r.URL.Query().Get("pattern")
	if pattern == "" {
		pattern = "*" // All events
	}

	// Create channel for events
	eventCh := make(chan events.Event, 100)
	done := make(chan struct{})

	// Subscribe to events
	subID, err := h.bus.SubscribeAsync(pattern, func(_ context.Context, event events.Event) error {
		select {
		case eventCh <- event:
		case <-done:
		default:
			// Drop if buffer full
		}
		return nil
	}, 100)

	if err != nil {
		conn.WriteJSON(map[string]string{"error": err.Error()})
		return
	}
	defer h.bus.Unsubscribe(subID)

	// Set up ping/pong
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Start ping ticker
	pingTicker := time.NewTicker(54 * time.Second)
	defer pingTicker.Stop()

	// Read goroutine (for close detection)
	go func() {
		defer close(done)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	// Write loop
	for {
		select {
		case event := <-eventCh:
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		case <-pingTicker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}
