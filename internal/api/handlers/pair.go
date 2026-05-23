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
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/pair"
)

// PairHandler serves the paired-review-loop API (PAIRING_SPEC §9).
type PairHandler struct {
	reg *pair.Registry
	bus events.EventBus
}

// NewPairHandler constructs a handler bound to a registry and event bus.
func NewPairHandler(reg *pair.Registry, bus events.EventBus) *PairHandler {
	return &PairHandler{reg: reg, bus: bus}
}

// createReq is the body of POST /api/v1/pair.
type createReq struct {
	Implementer        pair.AgentRef `json:"implementer"`
	Reviewer           pair.AgentRef `json:"reviewer"`
	ReviewPrompt       string        `json:"review_prompt"`
	FeedbackPrompt     string        `json:"feedback_prompt"`
	StopSignal         string        `json:"stop_signal"`
	MaxRounds          int           `json:"max_rounds"`
	ConfirmBeforeRelay bool          `json:"confirm_before_relay"`
	Kickoff            string        `json:"kickoff"`
}

// Create handles POST /api/v1/pair.
func (h *PairHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}

	cfg := pair.Config{
		ReviewPrompt:       req.ReviewPrompt,
		FeedbackPrompt:     req.FeedbackPrompt,
		StopSignal:         req.StopSignal,
		MaxRounds:          req.MaxRounds,
		ConfirmBeforeRelay: req.ConfirmBeforeRelay,
	}
	if cfg.ReviewPrompt == "" {
		cfg.ReviewPrompt = pair.DefaultConfig().ReviewPrompt
	}
	if cfg.FeedbackPrompt == "" {
		cfg.FeedbackPrompt = pair.DefaultConfig().FeedbackPrompt
	}
	if cfg.StopSignal == "" {
		cfg.StopSignal = pair.DefaultConfig().StopSignal
	}
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = pair.DefaultConfig().MaxRounds
	}

	kickoff := pair.KickoffMode(req.Kickoff)
	if kickoff != pair.KickoffUseCurrent && kickoff != pair.KickoffWaitForNext {
		kickoff = pair.KickoffUseCurrent
	}

	rt, err := h.reg.Create(pair.CreateOptions{
		Implementer: req.Implementer,
		Reviewer:    req.Reviewer,
		Config:      cfg,
		Kickoff:     kickoff,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	WriteJSON(w, http.StatusCreated, rt.Pair())
}

// List handles GET /api/v1/pair.
func (h *PairHandler) List(w http.ResponseWriter, r *http.Request) {
	includeStopped := r.URL.Query().Get("include_stopped") == "true"
	runtimes := h.reg.List()
	out := make([]pair.Pair, 0, len(runtimes))
	for _, rt := range runtimes {
		p := rt.Pair()
		if !includeStopped && p.State == pair.StateStopped {
			continue
		}
		out = append(out, p)
	}
	WriteJSON(w, http.StatusOK, out)
}

// Get handles GET /api/v1/pair/{id}.
func (h *PairHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	rt := h.reg.Get(id)
	if rt == nil {
		http.Error(w, "pair not found", http.StatusNotFound)
		return
	}
	WriteJSON(w, http.StatusOK, rt.Pair())
}

// FindBySession handles GET /api/v1/pair/by-session/{session}.
// Returns the active pair containing the session, or 404 if none.
func (h *PairHandler) FindBySession(w http.ResponseWriter, r *http.Request) {
	sid := mux.Vars(r)["session"]
	rt := h.reg.BySession(sid)
	if rt == nil {
		http.Error(w, "no active pair for session", http.StatusNotFound)
		return
	}
	WriteJSON(w, http.StatusOK, rt.Pair())
}

// Pause handles POST /api/v1/pair/{id}/pause.
func (h *PairHandler) Pause(w http.ResponseWriter, r *http.Request) {
	rt := h.resolve(w, r)
	if rt == nil {
		return
	}
	rt.Pause()
	WriteJSON(w, http.StatusOK, rt.Pair())
}

// Resume handles POST /api/v1/pair/{id}/resume.
func (h *PairHandler) Resume(w http.ResponseWriter, r *http.Request) {
	rt := h.resolve(w, r)
	if rt == nil {
		return
	}
	rt.Resume()
	WriteJSON(w, http.StatusOK, rt.Pair())
}

// Stop handles POST /api/v1/pair/{id}/stop.
func (h *PairHandler) Stop(w http.ResponseWriter, r *http.Request) {
	rt := h.resolve(w, r)
	if rt == nil {
		return
	}
	rt.Stop(pair.StopReasonManual)
	// Brief wait so the returned state reflects the transition.
	time.Sleep(50 * time.Millisecond)
	WriteJSON(w, http.StatusOK, rt.Pair())
}

// ForceRelay handles POST /api/v1/pair/{id}/force-relay.
func (h *PairHandler) ForceRelay(w http.ResponseWriter, r *http.Request) {
	rt := h.resolve(w, r)
	if rt == nil {
		return
	}
	rt.ForceRelay()
	WriteJSON(w, http.StatusOK, rt.Pair())
}

// configReq is the body of POST /api/v1/pair/{id}/config.
type configReq struct {
	ReviewPrompt       *string `json:"review_prompt,omitempty"`
	FeedbackPrompt     *string `json:"feedback_prompt,omitempty"`
	StopSignal         *string `json:"stop_signal,omitempty"`
	MaxRounds          *int    `json:"max_rounds,omitempty"`
	ConfirmBeforeRelay *bool   `json:"confirm_before_relay,omitempty"`
}

// UpdateConfig handles POST /api/v1/pair/{id}/config. Accepts a partial
// patch — only the supplied fields are changed.
func (h *PairHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	rt := h.resolve(w, r)
	if rt == nil {
		return
	}
	var req configReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}

	current := rt.Pair().Config
	merged := current
	var changed []string
	if req.ReviewPrompt != nil {
		merged.ReviewPrompt = *req.ReviewPrompt
		changed = append(changed, "review_prompt")
	}
	if req.FeedbackPrompt != nil {
		merged.FeedbackPrompt = *req.FeedbackPrompt
		changed = append(changed, "feedback_prompt")
	}
	if req.StopSignal != nil {
		merged.StopSignal = *req.StopSignal
		changed = append(changed, "stop_signal")
	}
	if req.MaxRounds != nil {
		merged.MaxRounds = *req.MaxRounds
		changed = append(changed, "max_rounds")
	}
	if req.ConfirmBeforeRelay != nil {
		merged.ConfirmBeforeRelay = *req.ConfirmBeforeRelay
		changed = append(changed, "confirm_before_relay")
	}

	rt.UpdateConfig(merged, changed)
	// Tiny grace period so the returned record reflects the change.
	time.Sleep(20 * time.Millisecond)
	WriteJSON(w, http.StatusOK, rt.Pair())
}

// confirmReq is the body of POST /api/v1/pair/{id}/confirm.
type confirmReq struct {
	Action     string `json:"action"` // "send" | "skip" | "stop"
	EditedText string `json:"edited_text,omitempty"`
}

// Confirm handles POST /api/v1/pair/{id}/confirm.
func (h *PairHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	rt := h.resolve(w, r)
	if rt == nil {
		return
	}
	var req confirmReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	switch req.Action {
	case "send", "skip", "stop":
	default:
		http.Error(w, "action must be send/skip/stop", http.StatusBadRequest)
		return
	}
	rt.ConfirmRelay(req.Action, req.EditedText)
	time.Sleep(50 * time.Millisecond)
	WriteJSON(w, http.StatusOK, rt.Pair())
}

// Forget handles DELETE /api/v1/pair/{id}. Only valid for stopped pairs.
func (h *PairHandler) Forget(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.reg.Forget(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolve looks up the pair by id from the URL or returns an error response.
func (h *PairHandler) resolve(w http.ResponseWriter, r *http.Request) *pair.PairRuntime {
	id := mux.Vars(r)["id"]
	rt := h.reg.Get(id)
	if rt == nil {
		http.Error(w, "pair not found", http.StatusNotFound)
		return nil
	}
	return rt
}

// WebSocket streams pair.* events to clients. Optional query params:
//
//	?pair_id=...     filter to events for a specific pair
//	?session_id=...  filter to events for any pair containing that session
//
// No client-to-server messages are accepted; reads are drained for keepalive.
func (h *PairHandler) WebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	filterPair := r.URL.Query().Get("pair_id")
	filterSession := r.URL.Query().Get("session_id")

	pairIDsForSession := func() map[string]struct{} {
		if filterSession == "" {
			return nil
		}
		out := map[string]struct{}{}
		if rt := h.reg.BySession(filterSession); rt != nil {
			out[rt.Pair().ID] = struct{}{}
		}
		return out
	}
	sessFilterSet := pairIDsForSession()

	matches := func(ev events.Event) bool {
		if !strings.HasPrefix(ev.Type, "pair.") {
			return false
		}
		pid, _ := ev.Payload["pair_id"].(string)
		if filterPair != "" && pid != filterPair {
			return false
		}
		if filterSession != "" {
			if _, ok := sessFilterSet[pid]; !ok {
				// pair_id may not be in the set yet (e.g., pair.started arrives
				// for the session we're filtering on); re-resolve once.
				if rt := h.reg.BySession(filterSession); rt != nil && rt.Pair().ID == pid {
					sessFilterSet[pid] = struct{}{}
				} else {
					return false
				}
			}
		}
		return true
	}

	eventCh := make(chan events.Event, 64)
	done := make(chan struct{})
	subID, err := h.bus.SubscribeAsync("pair.*", func(_ context.Context, ev events.Event) error {
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

	// Reader loop just for close detection.
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
