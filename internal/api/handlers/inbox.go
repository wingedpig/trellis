// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/inbox"
)

// InboxHandler serves the floating session-inbox window.
//
// The inbox runs in a separate browser window. It connects to one WebSocket
// as role=inbox and receives session.state_changed events. When the user
// clicks a row, the inbox client sends a "navigate" message; this handler
// forwards it to every connected role=main client (regular trellis tabs) so
// the main window does the actual navigation.
type InboxHandler struct {
	upgraderHolder
	agg *inbox.Aggregator
	bus events.EventBus

	mu    sync.Mutex
	mains map[*mainClient]struct{}
}

// NewInboxHandler creates a new InboxHandler.
func NewInboxHandler(agg *inbox.Aggregator, bus events.EventBus) *InboxHandler {
	return &InboxHandler{
		agg:   agg,
		bus:   bus,
		mains: make(map[*mainClient]struct{}),
	}
}

// mainClient holds the per-connection send channel for a role=main WS.
type mainClient struct {
	send chan []byte
}

// inboxClientMsg is what the inbox window sends to the server.
type inboxClientMsg struct {
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
}

// inboxServerMsg is what the server sends to either window.
type inboxServerMsg struct {
	Type   string          `json:"type"`
	Event  json.RawMessage `json:"event,omitempty"`
	Path   string          `json:"path,omitempty"`
	Reason string          `json:"reason,omitempty"`
}

// Sessions returns the current merged inbox rows (initial load).
func (h *InboxHandler) Sessions(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, h.agg.List())
}

// WebSocket handles both role=inbox and role=main connections.
func (h *InboxHandler) WebSocket(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	switch role {
	case "inbox":
		h.serveInbox(w, r)
	case "main":
		h.serveMain(w, r)
	default:
		http.Error(w, "role must be 'inbox' or 'main'", http.StatusBadRequest)
	}
}

// serveInbox forwards session.state_changed events to the inbox window and
// fans incoming navigate commands out to all connected main-window clients.
func (h *InboxHandler) serveInbox(w http.ResponseWriter, r *http.Request) {
	conn, err := h.ws().Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var writeMu sync.Mutex
	writeJSON := func(msg inboxServerMsg) error {
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, data)
	}

	eventCh := make(chan events.Event, 256)
	done := make(chan struct{})
	subID, err := h.bus.SubscribeAsync(events.EventSessionStateChanged, func(_ context.Context, ev events.Event) error {
		select {
		case eventCh <- ev:
		case <-done:
		default:
		}
		return nil
	}, 256)
	if err != nil {
		_ = writeJSON(inboxServerMsg{Type: "error", Reason: err.Error()})
		return
	}
	defer h.bus.Unsubscribe(subID)

	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	readCh := make(chan inboxClientMsg, 4)
	go func() {
		defer close(done)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg inboxClientMsg
			if json.Unmarshal(raw, &msg) == nil {
				readCh <- msg
			}
		}
	}()

	pingTicker := time.NewTicker(54 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case ev := <-eventCh:
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if err := writeJSON(inboxServerMsg{Type: "state_changed", Event: payload}); err != nil {
				return
			}
		case msg := <-readCh:
			if msg.Type == "navigate" && msg.Path != "" {
				h.dispatchNavigate(msg.Path, writeJSON)
			}
		case <-pingTicker.C:
			writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			writeMu.Unlock()
			if err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// dispatchNavigate fans a navigate command out to every registered main
// window. If none are connected, replies to the inbox with navigate_failed.
func (h *InboxHandler) dispatchNavigate(path string, writeJSON func(inboxServerMsg) error) {
	data, err := json.Marshal(inboxServerMsg{Type: "navigate", Path: path})
	if err != nil {
		return
	}
	h.mu.Lock()
	clients := make([]*mainClient, 0, len(h.mains))
	for c := range h.mains {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	delivered := 0
	for _, c := range clients {
		select {
		case c.send <- data:
			delivered++
		default:
		}
	}
	if delivered == 0 {
		_ = writeJSON(inboxServerMsg{Type: "navigate_failed", Reason: "no_main_window", Path: path})
	}
}

// serveMain holds a WS connection from a regular trellis page so the server
// can push navigate commands to it. Reads are drained for close detection
// and ping/pong; the page never sends application messages over this socket.
func (h *InboxHandler) serveMain(w http.ResponseWriter, r *http.Request) {
	conn, err := h.ws().Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	mc := &mainClient{send: make(chan []byte, 16)}
	h.mu.Lock()
	h.mains[mc] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.mains, mc)
		h.mu.Unlock()
	}()

	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	var writeMu sync.Mutex
	pingTicker := time.NewTicker(54 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case data := <-mc.send:
			writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := conn.WriteMessage(websocket.TextMessage, data)
			writeMu.Unlock()
			if err != nil {
				return
			}
		case <-pingTicker.C:
			writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			writeMu.Unlock()
			if err != nil {
				return
			}
		case <-done:
			return
		}
	}
}
