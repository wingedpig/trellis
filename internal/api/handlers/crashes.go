// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/crashes"
)

// CrashesHandler handles crash-related API requests.
type CrashesHandler struct {
	manager *crashes.Manager
}

// NewCrashesHandler creates a new crashes handler.
func NewCrashesHandler(mgr *crashes.Manager) *CrashesHandler {
	return &CrashesHandler{manager: mgr}
}

// List returns all crashes.
// GET /api/v1/crashes
func (h *CrashesHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"data": []interface{}{}})
		return
	}

	summaries, err := h.manager.List()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list crashes: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"data": summaries})
}

// Get returns a specific crash by ID.
// GET /api/v1/crashes/{id}
func (h *CrashesHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		respondError(w, http.StatusNotFound, "crashes not configured")
		return
	}

	vars := mux.Vars(r)
	id := vars["id"]

	crash, err := h.manager.Get(id)
	if err != nil {
		respondError(w, http.StatusNotFound, "crash not found: "+id)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"data": crash})
}

// Newest returns the most recent crash.
// GET /api/v1/crashes/newest
func (h *CrashesHandler) Newest(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"data": nil})
		return
	}

	crash, err := h.manager.Newest()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get newest crash: "+err.Error())
		return
	}

	if crash == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"data": nil})
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"data": crash})
}

// Delete removes a crash by ID.
// DELETE /api/v1/crashes/{id}
func (h *CrashesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		respondError(w, http.StatusNotFound, "crashes not configured")
		return
	}

	vars := mux.Vars(r)
	id := vars["id"]

	if err := h.manager.Delete(id); err != nil {
		respondError(w, http.StatusNotFound, "crash not found: "+id)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"message": "crash deleted"})
}

// Clear removes all crashes.
// DELETE /api/v1/crashes
func (h *CrashesHandler) Clear(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"message": "no crashes to clear"})
		return
	}

	if err := h.manager.Clear(); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to clear crashes: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"message": "all crashes cleared"})
}

// respondJSON writes a JSON response.
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// respondError writes a JSON error response.
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]interface{}{"error": message})
}
