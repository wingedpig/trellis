// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/wingedpig/trellis/internal/events"
)

// NotifyHandler handles notification API requests.
type NotifyHandler struct {
	bus events.EventBus
}

// NewNotifyHandler creates a new notify handler.
func NewNotifyHandler(bus events.EventBus) *NotifyHandler {
	return &NotifyHandler{bus: bus}
}

// NotifyRequest is the request body for the notify endpoint.
type NotifyRequest struct {
	Message string `json:"message"`
	Type    string `json:"type"` // done, blocked, error
}

// NotifyResponse is the response from the notify endpoint.
type NotifyResponse struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
}

// Notify emits a notification event.
func (h *NotifyHandler) Notify(w http.ResponseWriter, r *http.Request) {
	var req NotifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON")
		return
	}

	if req.Message == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "message is required")
		return
	}

	// Default to "done"
	eventType := events.EventNotifyDone
	switch req.Type {
	case "blocked":
		eventType = events.EventNotifyBlocked
	case "error":
		eventType = events.EventNotifyError
	case "", "done":
		eventType = events.EventNotifyDone
	default:
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "type must be done, blocked, or error")
		return
	}

	event := events.Event{
		Type: eventType,
		Payload: map[string]interface{}{
			"message": req.Message,
		},
	}

	if err := h.bus.Publish(context.Background(), event); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, NotifyResponse{
		ID:        event.ID,
		Type:      event.Type,
		Timestamp: event.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
	})
}
