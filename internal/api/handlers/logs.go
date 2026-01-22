// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/wingedpig/trellis/internal/logs"
)

// isConnectionClosed checks if an error indicates a normal connection close
// (broken pipe, connection reset, etc.) that shouldn't be logged as an error.
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "use of closed network connection")
}

// Error code for log-related errors.
const ErrLogViewerError = "LOG_VIEWER_ERROR"

// LogHandler handles log viewer API requests.
type LogHandler struct {
	manager *logs.Manager
}

// NewLogHandler creates a new log handler.
func NewLogHandler(manager *logs.Manager) *LogHandler {
	return &LogHandler{manager: manager}
}

// List returns all log viewers and their status.
func (h *LogHandler) List(w http.ResponseWriter, r *http.Request) {
	statuses := h.manager.ListStatus()
	WriteJSON(w, http.StatusOK, statuses)
}

// Get returns a single log viewer's status.
func (h *LogHandler) Get(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	viewer, ok := h.manager.Get(name)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "log viewer not found")
		return
	}

	WriteJSON(w, http.StatusOK, viewer.Status())
}

// GetEntries returns filtered log entries.
func (h *LogHandler) GetEntries(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	viewer, err := h.manager.GetAndStart(name)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	// Parse query parameters
	query := r.URL.Query()
	filterStr := query.Get("filter")
	limitStr := query.Get("limit")
	beforeStr := query.Get("before")
	afterStr := query.Get("after")

	// Parse filter
	var filter *logs.Filter
	if filterStr != "" {
		var err error
		filter, err = logs.ParseFilter(filterStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid filter: "+err.Error())
			return
		}
	}

	// Parse limit
	limit := 1000 // Default limit
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Parse time range
	var before, after time.Time
	if beforeStr != "" {
		t, err := time.Parse(time.RFC3339, beforeStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid 'before' timestamp: expected RFC3339 format")
			return
		}
		before = t
	}
	if afterStr != "" {
		t, err := time.Parse(time.RFC3339, afterStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid 'after' timestamp: expected RFC3339 format")
			return
		}
		after = t
	}

	// Get entries
	var entries []logs.LogEntry
	if !before.IsZero() || !after.IsZero() {
		if after.IsZero() {
			after = time.Time{}
		}
		if before.IsZero() {
			before = time.Now()
		}

		if filter != nil {
			// When filtering with time range, get all entries in range first,
			// then filter, then apply limit to ensure we return up to limit matches
			entries = viewer.GetEntriesRange(after, before, 0) // 0 = no limit
			var filtered []logs.LogEntry
			for _, e := range entries {
				if filter.Match(e) {
					filtered = append(filtered, e)
					if limit > 0 && len(filtered) >= limit {
						break
					}
				}
			}
			entries = filtered
		} else {
			// No filter, apply limit directly
			entries = viewer.GetEntriesRange(after, before, limit)
		}
	} else {
		entries = viewer.GetEntries(filter, limit)
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"entries":  entries,
		"count":    len(entries),
		"sequence": viewer.CurrentSequence(),
	})
}

// GetHistory returns historical log entries from rotated files.
func (h *LogHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	viewer, err := h.manager.GetAndStart(name)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	// Parse query parameters
	query := r.URL.Query()
	startStr := query.Get("start")
	endStr := query.Get("end")
	filterStr := query.Get("filter")
	limitStr := query.Get("limit")
	grepStr := query.Get("grep")
	beforeStr := query.Get("before")
	afterStr := query.Get("after")

	// Parse time range (required)
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid start time")
		return
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid end time")
		return
	}

	// Parse filter
	var filter *logs.Filter
	if filterStr != "" {
		filter, err = logs.ParseFilter(filterStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid filter: "+err.Error())
			return
		}
	}

	// Parse limit
	limit := 10000 // Higher default for historical queries
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Parse context lines (grep -B/-A)
	var grepBefore, grepAfter int
	if beforeStr != "" {
		grepBefore, _ = strconv.Atoi(beforeStr)
	}
	if afterStr != "" {
		grepAfter, _ = strconv.Atoi(afterStr)
	}

	// Get historical entries
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	entries, err := viewer.GetHistoricalEntries(ctx, start, end, filter, limit, grepStr, grepBefore, grepAfter)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrLogViewerError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"count":   len(entries),
		"start":   start,
		"end":     end,
	})
}

// ListRotatedFiles returns available rotated log files.
func (h *LogHandler) ListRotatedFiles(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	viewer, err := h.manager.GetAndStart(name)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	files, err := viewer.ListRotatedFiles(ctx)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrLogViewerError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, files)
}

// Stream handles WebSocket connections for streaming log entries.
func (h *LogHandler) Stream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	viewer, err := h.manager.GetAndStart(name)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Log stream: upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Parse initial filter from query params
	filterStr := r.URL.Query().Get("filter")
	var filter *logs.Filter
	var filterMu sync.RWMutex
	var paused atomic.Bool
	if filterStr != "" {
		var err error
		filter, err = logs.ParseFilter(filterStr)
		if err != nil {
			// Send error but continue with no filter
			conn.WriteJSON(map[string]interface{}{
				"type":  "error",
				"error": "invalid filter: " + err.Error(),
			})
		}
	}

	// Channel for receiving entries
	entryCh := make(chan logs.LogEntry, 1000)
	viewer.Subscribe(entryCh)
	defer viewer.Unsubscribe(entryCh)

	// Send connection status
	conn.WriteJSON(map[string]interface{}{
		"type":      "status",
		"viewer":    name,
		"connected": true,
		"sequence":  viewer.CurrentSequence(),
	})

	// Send initial buffered entries (small batch, more loaded on scroll)
	initialEntries := viewer.GetEntries(filter, 100)
	if len(initialEntries) > 0 {
		conn.WriteJSON(map[string]interface{}{
			"type":    "entries",
			"entries": initialEntries,
		})
	}

	// Mutex for WebSocket writes
	var writeMu sync.Mutex

	// Set up ping/pong
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Done channel
	done := make(chan struct{})

	// Ping goroutine
	go func() {
		ticker := time.NewTicker(54 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				writeMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				writeMu.Unlock()
				if err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Read goroutine for client messages and disconnect detection
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				// Normal close - don't log as error
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) &&
					!websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
					// Only log unexpected errors (not normal disconnects or broken pipes)
					if !isConnectionClosed(err) {
						log.Printf("Log stream %s: read error: %v", name, err)
					}
				}
				close(done)
				return
			}

			// Parse client message
			var clientMsg struct {
				Type      string `json:"type"`
				Query     string `json:"query"`
				SeekTS    string `json:"timestamp"`
				BeforeSeq uint64 `json:"before_seq"`
				Limit     int    `json:"limit"`
			}
			if err := json.Unmarshal(msg, &clientMsg); err != nil {
				continue
			}

			switch clientMsg.Type {
			case "filter":
				// Update filter
				filterMu.Lock()
				if clientMsg.Query != "" {
					newFilter, err := logs.ParseFilter(clientMsg.Query)
					if err != nil {
						filterMu.Unlock()
						// Send error back to client
						writeMu.Lock()
						conn.WriteJSON(map[string]interface{}{
							"type":  "error",
							"error": "invalid filter: " + err.Error(),
						})
						writeMu.Unlock()
					} else {
						filter = newFilter
						filterMu.Unlock()
					}
				} else {
					filter = nil
					filterMu.Unlock()
				}
			case "pause":
				// Client wants to pause streaming
				paused.Store(true)
			case "resume":
				// Client wants to resume streaming
				paused.Store(false)
			case "load_more":
				// Client wants older entries (scrolled to top)
				limit := clientMsg.Limit
				if limit <= 0 || limit > 100 {
					limit = 100
				}
				olderEntries := viewer.GetEntriesBefore(clientMsg.BeforeSeq, limit)
				if len(olderEntries) > 0 {
					writeMu.Lock()
					conn.WriteJSON(map[string]interface{}{
						"type":    "older_entries",
						"entries": olderEntries,
					})
					writeMu.Unlock()
				} else {
					// Signal no more entries available
					writeMu.Lock()
					conn.WriteJSON(map[string]interface{}{
						"type":     "older_entries",
						"entries":  []logs.LogEntry{},
						"no_more":  true,
					})
					writeMu.Unlock()
				}
			}
		}
	}()

	// Main loop - stream entries to client
	for {
		select {
		case entry, ok := <-entryCh:
			if !ok {
				log.Printf("Log stream %s: entry channel closed", name)
				return
			}

			// Skip sending if paused (entries are dropped while paused)
			if paused.Load() {
				continue
			}

			// Apply filter
			filterMu.RLock()
			currentFilter := filter
			filterMu.RUnlock()
			if currentFilter != nil && !currentFilter.Match(entry) {
				continue
			}

			writeMu.Lock()
			err := conn.WriteJSON(map[string]interface{}{
				"type":  "entry",
				"entry": entry,
			})
			writeMu.Unlock()

			if err != nil {
				log.Printf("Log stream: write failed: %v", err)
				return
			}

		case <-done:
			return
		}
	}
}

// StreamSSE streams log entries via Server-Sent Events for CLI consumption.
func (h *LogHandler) StreamSSE(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	viewer, err := h.manager.GetAndStart(name)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "streaming not supported")
		return
	}

	// Subscribe to entries
	entryCh := make(chan logs.LogEntry, 1000)
	viewer.Subscribe(entryCh)
	defer viewer.Unsubscribe(entryCh)

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"viewer\":%q}\n\n", name)
	flusher.Flush()

	// Set up keepalive ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Stream entries
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Send keepalive comment
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case entry, ok := <-entryCh:
			if !ok {
				return
			}
			// Send entry as JSON
			data, _ := json.Marshal(entry)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
