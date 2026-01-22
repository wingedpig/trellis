// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/service"
)

// ServiceHandler handles service-related API requests.
type ServiceHandler struct {
	mgr service.Manager
}

// NewServiceHandler creates a new service handler.
func NewServiceHandler(mgr service.Manager) *ServiceHandler {
	return &ServiceHandler{mgr: mgr}
}

// List returns all services.
func (h *ServiceHandler) List(w http.ResponseWriter, r *http.Request) {
	services := h.mgr.List()
	WriteJSON(w, http.StatusOK, services)
}

// Get returns a single service by name.
func (h *ServiceHandler) Get(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	svc, ok := h.mgr.GetService(name)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "service not found")
		return
	}

	WriteJSON(w, http.StatusOK, svc)
}

// Start starts a service.
func (h *ServiceHandler) Start(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Use background context - service should outlive the HTTP request
	if err := h.mgr.Start(context.Background(), name); err != nil {
		WriteError(w, http.StatusBadRequest, ErrServiceError, err.Error())
		return
	}

	svc, _ := h.mgr.GetService(name)
	WriteJSON(w, http.StatusOK, svc)
}

// Stop stops a service.
func (h *ServiceHandler) Stop(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Use background context - stop should complete even if request is cancelled
	if err := h.mgr.Stop(context.Background(), name); err != nil {
		WriteError(w, http.StatusBadRequest, ErrServiceError, err.Error())
		return
	}

	svc, _ := h.mgr.GetService(name)
	WriteJSON(w, http.StatusOK, svc)
}

// Restart restarts a service.
func (h *ServiceHandler) Restart(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Use background context - service should outlive the HTTP request
	if err := h.mgr.Restart(context.Background(), name, service.RestartManual); err != nil {
		WriteError(w, http.StatusBadRequest, ErrServiceError, err.Error())
		return
	}

	svc, _ := h.mgr.GetService(name)
	WriteJSON(w, http.StatusOK, svc)
}

// Logs returns the logs for a service.
func (h *ServiceHandler) Logs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Parse lines parameter
	lines := 100 // default
	if linesStr := r.URL.Query().Get("lines"); linesStr != "" {
		if n, err := strconv.Atoi(linesStr); err == nil && n > 0 {
			lines = n
		}
	}

	// Check if service has a parser configured
	if h.mgr.HasParser(name) {
		// Return parsed entries
		entries, err := h.mgr.ParsedLogs(name, lines)
		if err != nil {
			WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
			return
		}

		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"service": name,
			"entries": entries,
		})
		return
	}

	// No parser - return raw lines
	logs, err := h.mgr.Logs(name, lines)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"service": name,
		"lines":   logs,
	})
}

// ClearLogs clears the logs for a service.
func (h *ServiceHandler) ClearLogs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if err := h.mgr.ClearLogs(name); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"service": name,
		"cleared": true,
	})
}

// StreamLogs streams service logs via Server-Sent Events.
func (h *ServiceHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Subscribe to log updates
	ch, err := h.mgr.SubscribeLogs(name)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	defer h.mgr.UnsubscribeLogs(name, ch)

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

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"service\":%q}\n\n", name)
	flusher.Flush()

	// Set up keepalive ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Stream log lines
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Send keepalive comment
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case line, ok := <-ch:
			if !ok {
				return
			}
			// Send log line as JSON (include parsed entry if available)
			resp := map[string]interface{}{
				"line":     line.Line,
				"sequence": line.Sequence,
			}
			if line.Entry != nil {
				resp["entry"] = line.Entry
			}
			data, _ := json.Marshal(resp)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
