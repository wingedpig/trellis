// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/wingedpig/trellis/internal/checklist"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/pair"
)

// ChecklistHandler serves the phased-checklist outer-loop API
// (PHASE_LOOP_SPEC §8).
type ChecklistHandler struct {
	upgraderHolder
	reg *checklist.Registry
	bus events.EventBus
}

// NewChecklistHandler constructs a handler bound to a registry and event bus.
func NewChecklistHandler(reg *checklist.Registry, bus events.EventBus) *ChecklistHandler {
	return &ChecklistHandler{reg: reg, bus: bus}
}

// checklistCreateReq is the body of POST /api/v1/checklist.
type checklistCreateReq struct {
	Implementer        pair.AgentRef `json:"implementer"`
	Reviewer           pair.AgentRef `json:"reviewer"`
	AdvancePrompt      string        `json:"advance_prompt"`
	CompletionSignal   string        `json:"completion_signal"`
	ReviewPrompt       string        `json:"review_prompt"`
	FeedbackPrompt     string        `json:"feedback_prompt"`
	ReviewStopSignal   string        `json:"review_stop_signal"`
	MaxRounds          int           `json:"max_rounds"`
	ConfirmBeforeRelay bool          `json:"confirm_before_relay"`
}

// Create handles POST /api/v1/checklist.
func (h *ChecklistHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req checklistCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}

	cfg := checklist.Config{
		AdvancePrompt:      req.AdvancePrompt,
		CompletionSignal:   req.CompletionSignal,
		ReviewPrompt:       req.ReviewPrompt,
		FeedbackPrompt:     req.FeedbackPrompt,
		ReviewStopSignal:   req.ReviewStopSignal,
		MaxRounds:          req.MaxRounds,
		ConfirmBeforeRelay: req.ConfirmBeforeRelay,
	}
	// Registry.Create fills any remaining empty fields from DefaultConfig.

	rt, err := h.reg.Create(checklist.CreateOptions{
		Implementer: req.Implementer,
		Reviewer:    req.Reviewer,
		Config:      cfg,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	WriteJSON(w, http.StatusCreated, rt.Run())
}

// List handles GET /api/v1/checklist.
func (h *ChecklistHandler) List(w http.ResponseWriter, r *http.Request) {
	includeStopped := r.URL.Query().Get("include_stopped") == "true"
	runtimes := h.reg.List()
	out := make([]checklist.Run, 0, len(runtimes))
	for _, rt := range runtimes {
		run := rt.Run()
		if !includeStopped && run.State == checklist.StateStopped {
			continue
		}
		out = append(out, run)
	}
	WriteJSON(w, http.StatusOK, out)
}

// Get handles GET /api/v1/checklist/{id}.
func (h *ChecklistHandler) Get(w http.ResponseWriter, r *http.Request) {
	rt := h.resolve(w, r)
	if rt == nil {
		return
	}
	WriteJSON(w, http.StatusOK, rt.Run())
}

// FindBySession handles GET /api/v1/checklist/by-session/{session}.
func (h *ChecklistHandler) FindBySession(w http.ResponseWriter, r *http.Request) {
	sid := mux.Vars(r)["session"]
	rt := h.reg.BySession(sid)
	if rt == nil {
		http.Error(w, "no active run for session", http.StatusNotFound)
		return
	}
	WriteJSON(w, http.StatusOK, rt.Run())
}

// Pause handles POST /api/v1/checklist/{id}/pause.
func (h *ChecklistHandler) Pause(w http.ResponseWriter, r *http.Request) {
	if rt := h.resolve(w, r); rt != nil {
		rt.Pause()
		WriteJSON(w, http.StatusOK, rt.Run())
	}
}

// Resume handles POST /api/v1/checklist/{id}/resume.
func (h *ChecklistHandler) Resume(w http.ResponseWriter, r *http.Request) {
	if rt := h.resolve(w, r); rt != nil {
		rt.Resume()
		WriteJSON(w, http.StatusOK, rt.Run())
	}
}

// Stop handles POST /api/v1/checklist/{id}/stop.
func (h *ChecklistHandler) Stop(w http.ResponseWriter, r *http.Request) {
	if rt := h.resolve(w, r); rt != nil {
		rt.Stop(checklist.StopReasonManual)
		WriteJSON(w, http.StatusOK, rt.Run())
	}
}

// Skip handles POST /api/v1/checklist/{id}/skip.
func (h *ChecklistHandler) Skip(w http.ResponseWriter, r *http.Request) {
	if rt := h.resolve(w, r); rt != nil {
		rt.Skip()
		WriteJSON(w, http.StatusOK, rt.Run())
	}
}

// Retry handles POST /api/v1/checklist/{id}/retry.
func (h *ChecklistHandler) Retry(w http.ResponseWriter, r *http.Request) {
	if rt := h.resolve(w, r); rt != nil {
		rt.Retry()
		WriteJSON(w, http.StatusOK, rt.Run())
	}
}

// Forget handles DELETE /api/v1/checklist/{id}. Only valid for stopped runs.
func (h *ChecklistHandler) Forget(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.reg.Forget(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ChecklistHandler) resolve(w http.ResponseWriter, r *http.Request) *checklist.RunRuntime {
	id := mux.Vars(r)["id"]
	rt := h.reg.Get(id)
	if rt == nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return nil
	}
	return rt
}

// WebSocket streams checklist.* events to clients. Optional query params:
//
//	?run_id=...      filter to a specific run
//	?session_id=...  filter to the run containing that session
func (h *ChecklistHandler) WebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.ws().Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	filterRun := r.URL.Query().Get("run_id")
	filterSession := r.URL.Query().Get("session_id")
	if filterRun == "" && filterSession != "" {
		if rt := h.reg.BySession(filterSession); rt != nil {
			filterRun = rt.Run().ID
		}
	}

	matches := func(ev events.Event) bool {
		if !strings.HasPrefix(ev.Type, "checklist.") {
			return false
		}
		if filterRun == "" {
			return true
		}
		rid, _ := ev.Payload["run_id"].(string)
		return rid == filterRun
	}

	eventCh := make(chan events.Event, 64)
	done := make(chan struct{})
	subID, err := h.bus.SubscribeAsync("checklist.*", func(_ context.Context, ev events.Event) error {
		if !matches(ev) {
			return nil
		}
		select {
		case eventCh <- ev:
		case <-done:
		default:
		}
		return nil
	}, 64)
	if err != nil {
		return
	}
	defer h.bus.Unsubscribe(subID)

	var writeMu sync.Mutex
	writeJSON := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, data)
	}

	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	pingTicker := time.NewTicker(54 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case ev := <-eventCh:
			if err := writeJSON(ev); err != nil {
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
