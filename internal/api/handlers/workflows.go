// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/wingedpig/trellis/internal/workflow"
	"github.com/wingedpig/trellis/internal/worktree"
)

// WorkflowHandler handles workflow-related API requests.
type WorkflowHandler struct {
	runner      workflow.Runner
	worktreeMgr worktree.Manager
}

// NewWorkflowHandler creates a new workflow handler.
func NewWorkflowHandler(runner workflow.Runner, worktreeMgr worktree.Manager) *WorkflowHandler {
	return &WorkflowHandler{runner: runner, worktreeMgr: worktreeMgr}
}

// List returns all workflows.
func (h *WorkflowHandler) List(w http.ResponseWriter, r *http.Request) {
	workflows := h.runner.List()
	WriteJSON(w, http.StatusOK, workflows)
}

// Get returns a single workflow by ID.
func (h *WorkflowHandler) Get(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	wf, ok := h.runner.Get(id)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "workflow not found")
		return
	}

	WriteJSON(w, http.StatusOK, wf)
}

// Run executes a workflow.
func (h *WorkflowHandler) Run(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	// Get worktree from query parameter - workflows run in the specified worktree's directory
	worktreeName := r.URL.Query().Get("worktree")

	opts := workflow.RunOptions{}

	// If worktree specified, resolve to path
	if worktreeName != "" && h.worktreeMgr != nil {
		wt, found := h.worktreeMgr.GetByName(worktreeName)
		if !found {
			WriteError(w, http.StatusBadRequest, ErrWorkflowError, "worktree not found: "+worktreeName)
			return
		}
		opts.WorkingDir = wt.Path
	}

	// Use background context - workflows (especially service start/stop) should outlive the HTTP request
	status, err := h.runner.RunWithOptions(context.Background(), id, opts)
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrWorkflowError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, status)
}

// Status returns the status of a workflow run.
func (h *WorkflowHandler) Status(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	// First try as a run ID
	status, ok := h.runner.Status(id)
	if !ok {
		// If not found as run ID, check if workflow exists
		_, wfOk := h.runner.Get(id)
		if !wfOk {
			WriteError(w, http.StatusNotFound, ErrNotFound, "workflow or run not found")
			return
		}
		// Workflow exists but no active run (use PascalCase to match WorkflowStatus struct)
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"ID":    id,
			"State": "idle",
		})
		return
	}

	WriteJSON(w, http.StatusOK, status)
}

// Stream handles WebSocket connections for streaming workflow output.
func (h *WorkflowHandler) Stream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	runID := vars["runID"]

	// Check if the run exists
	_, ok := h.runner.Status(runID)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "workflow run not found")
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Workflow stream: upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Create channel for output updates
	outputCh := make(chan workflow.OutputUpdate, 100)

	// Subscribe to output updates
	if err := h.runner.Subscribe(runID, outputCh); err != nil {
		log.Printf("Workflow stream: subscribe failed: %v", err)
		conn.WriteJSON(map[string]interface{}{
			"type":  "error",
			"error": err.Error(),
		})
		return
	}
	defer h.runner.Unsubscribe(runID, outputCh)

	// Mutex to protect concurrent WebSocket writes (gorilla/websocket requires single writer)
	var writeMu sync.Mutex

	// Set up ping/pong for keepalive
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Start ping goroutine
	done := make(chan struct{})
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

	// Start read goroutine to detect client disconnect
	go func() {
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				close(done)
				return
			}
		}
	}()

	// Send output updates to client
	for {
		select {
		case update, ok := <-outputCh:
			if !ok {
				return
			}

			msg := map[string]interface{}{
				"type": "output",
			}

			if update.Done {
				msg["type"] = "done"
				msg["status"] = update.Status
			} else {
				msg["line"] = update.Line
			}

			writeMu.Lock()
			err := conn.WriteJSON(msg)
			writeMu.Unlock()
			if err != nil {
				log.Printf("Workflow stream: write failed: %v", err)
				return
			}

			if update.Done {
				return
			}

		case <-done:
			return
		}
	}
}
