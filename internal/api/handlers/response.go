// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"encoding/json"
	"net/http"
	"time"
)

// Response is the standard API response wrapper.
type Response struct {
	Data  interface{} `json:"data,omitempty"`
	Error *ErrorInfo  `json:"error,omitempty"`
	Meta  *MetaInfo   `json:"meta,omitempty"`
}

// ErrorInfo contains error details.
type ErrorInfo struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// MetaInfo contains response metadata.
type MetaInfo struct {
	Timestamp time.Time `json:"timestamp"`
}

// Common error codes
const (
	ErrNotFound       = "NOT_FOUND"
	ErrBadRequest     = "BAD_REQUEST"
	ErrInternalError  = "INTERNAL_ERROR"
	ErrConflict       = "CONFLICT"
	ErrServiceError   = "SERVICE_ERROR"
	ErrWorktreeError  = "WORKTREE_ERROR"
	ErrWorkflowError  = "WORKFLOW_ERROR"
	ErrTerminalError  = "TERMINAL_ERROR"
)

// WriteJSON writes a JSON response.
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	resp := Response{
		Data: data,
		Meta: &MetaInfo{Timestamp: time.Now()},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// WriteError writes an error response.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	resp := Response{
		Error: &ErrorInfo{
			Code:    code,
			Message: message,
		},
		Meta: &MetaInfo{Timestamp: time.Now()},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// WriteErrorWithDetails writes an error response with details.
func WriteErrorWithDetails(w http.ResponseWriter, status int, code, message string, details map[string]interface{}) {
	resp := Response{
		Error: &ErrorInfo{
			Code:    code,
			Message: message,
			Details: details,
		},
		Meta: &MetaInfo{Timestamp: time.Now()},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
