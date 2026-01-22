// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/trace"
)

// Error code for trace-related errors.
const ErrTraceError = "TRACE_ERROR"

// TraceHandler handles distributed trace API requests.
type TraceHandler struct {
	manager *trace.Manager
}

// NewTraceHandler creates a new trace handler.
func NewTraceHandler(manager *trace.Manager) *TraceHandler {
	return &TraceHandler{manager: manager}
}

// executeRequest is the JSON request body for executing a trace.
type executeRequest struct {
	ID         string `json:"id"`
	Group      string `json:"group"`
	Start      string `json:"start"`
	End        string `json:"end"`
	Name       string `json:"name"`
	ExpandByID *bool  `json:"expand_by_id"` // nil defaults to true
}

// Execute runs a distributed trace search.
// POST /api/v1/trace
func (h *TraceHandler) Execute(w http.ResponseWriter, r *http.Request) {
	var req executeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid request body")
		return
	}

	// Validate required fields
	if req.ID == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "id is required")
		return
	}
	if req.Group == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "group is required")
		return
	}

	// Parse time range
	start, err := time.Parse(time.RFC3339, req.Start)
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid start time (expected RFC3339)")
		return
	}

	// End time defaults to now if not provided
	var end time.Time
	if req.End == "" || req.End == "now" {
		end = time.Now()
	} else {
		end, err = time.Parse(time.RFC3339, req.End)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid end time (expected RFC3339)")
			return
		}
	}

	// ExpandByID defaults to true if not specified
	expandByID := true
	if req.ExpandByID != nil {
		expandByID = *req.ExpandByID
	}

	// Build trace request
	traceReq := trace.TraceRequest{
		TraceID:    req.ID,
		Group:      req.Group,
		Name:       req.Name,
		Start:      start,
		End:        end,
		ExpandByID: expandByID,
	}

	// Execute the trace
	result, err := h.manager.Execute(r.Context(), traceReq)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrTraceError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, result)
}

// GetReport retrieves a saved trace report.
// GET /api/v1/trace/reports/{name}
func (h *TraceHandler) GetReport(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	report, err := h.manager.GetReport(name)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, report)
}

// ListReports returns summaries of all saved reports.
// GET /api/v1/trace/reports
func (h *TraceHandler) ListReports(w http.ResponseWriter, r *http.Request) {
	reports, err := h.manager.ListReports()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrTraceError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"reports": reports,
	})
}

// DeleteReport removes a saved trace report.
// DELETE /api/v1/trace/reports/{name}
func (h *TraceHandler) DeleteReport(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if err := h.manager.DeleteReport(name); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"deleted": name,
	})
}

// ListGroups returns all configured trace groups.
// GET /api/v1/trace/groups
func (h *TraceHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups := h.manager.GetGroups()
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"groups": groups,
	})
}
