// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package checklist

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/pair"
)

// Registry is the process-wide collection of checklist runs. It owns
// persistence (via Store), per-run drivers, and the index enforcing "a session
// may belong to at most one active run" (PHASE_LOOP_SPEC §2). It also holds the
// pair registry, since each phase spins up a real pair.
type Registry struct {
	mu     sync.RWMutex
	runs   map[string]*RunRuntime // run id -> runtime
	bySess map[string]string      // session id -> run id (active only)

	store   *Store
	agents  *pair.Agents
	pairReg *pair.Registry
	bus     events.EventBus
}

// NewRegistry creates a registry. store may be a no-op store from NewStore("");
// bus may be nil in tests. pairReg must be non-nil to actually run phases.
func NewRegistry(store *Store, agents *pair.Agents, pairReg *pair.Registry, bus events.EventBus) *Registry {
	return &Registry{
		runs:    make(map[string]*RunRuntime),
		bySess:  make(map[string]string),
		store:   store,
		agents:  agents,
		pairReg: pairReg,
		bus:     bus,
	}
}

// CreateOptions configures a new run.
type CreateOptions struct {
	Implementer pair.AgentRef
	Reviewer    pair.AgentRef
	Config      Config
}

// Create validates the options, persists the run, registers it, and starts its
// driver. Returns the active runtime.
func (r *Registry) Create(opts CreateOptions) (*RunRuntime, error) {
	if err := r.validateCreate(opts); err != nil {
		return nil, err
	}

	cfg := opts.Config
	def := DefaultConfig()
	if cfg.AdvancePrompt == "" {
		cfg.AdvancePrompt = def.AdvancePrompt
	}
	if cfg.CompletionSignal == "" {
		cfg.CompletionSignal = def.CompletionSignal
	}
	if cfg.ReviewPrompt == "" {
		cfg.ReviewPrompt = def.ReviewPrompt
	}
	if cfg.ReviewStopSignal == "" {
		cfg.ReviewStopSignal = def.ReviewStopSignal
	}
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = def.MaxRounds
	}
	// FeedbackPrompt may legitimately be empty (pair §6.1).

	now := time.Now()
	run := &Run{
		ID:          uuid.New().String(),
		CreatedAt:   now,
		State:       StatePending,
		Implementer: opts.Implementer,
		Reviewer:    opts.Reviewer,
		Config:      cfg,
	}

	rt := newRuntime(run, r.store, r.agents, r.pairReg, r, r.bus)

	r.mu.Lock()
	r.runs[run.ID] = rt
	r.bySess[opts.Implementer.SessionID] = run.ID
	r.bySess[opts.Reviewer.SessionID] = run.ID
	r.mu.Unlock()

	if err := r.store.Save(run); err != nil {
		log.Printf("checklist: failed to persist new run %s: %v", run.ID, err)
	}

	rt.publish("checklist.started", map[string]interface{}{
		"run_id":      run.ID,
		"implementer": run.Implementer,
		"reviewer":    run.Reviewer,
		"config":      run.Config,
	})

	rt.start()
	return rt, nil
}

func (r *Registry) validateCreate(opts CreateOptions) error {
	if opts.Implementer.SessionID == "" || opts.Reviewer.SessionID == "" {
		return fmt.Errorf("both implementer and reviewer session ids are required")
	}
	if opts.Implementer.SessionID == opts.Reviewer.SessionID {
		return fmt.Errorf("a session cannot pair with itself")
	}

	r.mu.RLock()
	_, implBusy := r.bySess[opts.Implementer.SessionID]
	_, revBusy := r.bySess[opts.Reviewer.SessionID]
	r.mu.RUnlock()
	if implBusy {
		return fmt.Errorf("implementer session is already in an active checklist run")
	}
	if revBusy {
		return fmt.Errorf("reviewer session is already in an active checklist run")
	}

	// Neither session may already be in a standalone pair — the run needs both
	// free so it can create per-phase pairs on them.
	if r.pairReg != nil {
		if r.pairReg.BySession(opts.Implementer.SessionID) != nil {
			return fmt.Errorf("implementer session is already in an active pair")
		}
		if r.pairReg.BySession(opts.Reviewer.SessionID) != nil {
			return fmt.Errorf("reviewer session is already in an active pair")
		}
	}

	for _, ref := range []pair.AgentRef{opts.Implementer, opts.Reviewer} {
		st := r.agents.LookupStatus(ref)
		if !st.Exists {
			return fmt.Errorf("%s session %s does not exist", ref.Agent, ref.SessionID)
		}
		if st.Trashed {
			return fmt.Errorf("%s session %s is in trash", ref.Agent, ref.SessionID)
		}
	}
	return nil
}

// Get returns the runtime for a run, or nil if unknown.
func (r *Registry) Get(id string) *RunRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runs[id]
}

// BySession returns the run a session currently belongs to, or nil.
func (r *Registry) BySession(sessionID string) *RunRuntime {
	r.mu.RLock()
	rid := r.bySess[sessionID]
	r.mu.RUnlock()
	if rid == "" {
		return nil
	}
	return r.Get(rid)
}

// List returns a snapshot of every run in the registry, including stopped ones
// that haven't been discarded.
func (r *Registry) List() []*RunRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RunRuntime, 0, len(r.runs))
	for _, rt := range r.runs {
		out = append(out, rt)
	}
	return out
}

// Rehydrate is called once on server start, AFTER the pair registry has
// rehydrated (so per-phase pairs already exist). It loads every persisted run
// and either resumes its driver or marks it stopped (PHASE_LOOP_SPEC §7).
func (r *Registry) Rehydrate() {
	if r.store == nil {
		return
	}
	runs, err := r.store.LoadAll()
	if err != nil {
		log.Printf("checklist: rehydrate load failed: %v", err)
		return
	}
	for _, run := range runs {
		// Already-stopped records are kept in memory for audit only.
		if run.StoppedAt != nil || run.State == StateStopped {
			rt := newRuntime(run, r.store, r.agents, r.pairReg, r, r.bus)
			r.mu.Lock()
			r.runs[run.ID] = rt
			r.mu.Unlock()
			continue
		}

		// Active run — verify both sessions still resolve.
		implStatus := r.agents.LookupStatus(run.Implementer)
		revStatus := r.agents.LookupStatus(run.Reviewer)
		if !implStatus.Exists || implStatus.Trashed || !revStatus.Exists || revStatus.Trashed {
			now := time.Now()
			run.State = StateStopped
			run.StoppedAt = &now
			run.StopReason = StopReasonServerRestarted
			run.RestartUnresolvable = true
			if err := r.store.Save(run); err != nil {
				log.Printf("checklist: rehydrate save (unresolvable) failed for %s: %v", run.ID, err)
			}
			rt := newRuntime(run, r.store, r.agents, r.pairReg, r, r.bus)
			r.mu.Lock()
			r.runs[run.ID] = rt
			r.mu.Unlock()
			continue
		}

		rt := newRuntime(run, r.store, r.agents, r.pairReg, r, r.bus)
		r.mu.Lock()
		r.runs[run.ID] = rt
		r.bySess[run.Implementer.SessionID] = run.ID
		r.bySess[run.Reviewer.SessionID] = run.ID
		r.mu.Unlock()
		rt.resume()
	}
	if n := len(runs); n > 0 {
		log.Printf("checklist: rehydrated %d run records", n)
	}
}

// Shutdown stops every active driver. Records remain on disk.
func (r *Registry) Shutdown(ctx context.Context) {
	r.mu.RLock()
	runtimes := make([]*RunRuntime, 0, len(r.runs))
	for _, rt := range r.runs {
		runtimes = append(runtimes, rt)
	}
	r.mu.RUnlock()
	for _, rt := range runtimes {
		rt.shutdown()
	}
}

// freeSessions removes the session-id → run-id mappings once a run is no longer
// active. Called by the driver when transitioning to stopped.
func (r *Registry) freeSessions(run *Run) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bySess[run.Implementer.SessionID] == run.ID {
		delete(r.bySess, run.Implementer.SessionID)
	}
	if r.bySess[run.Reviewer.SessionID] == run.ID {
		delete(r.bySess, run.Reviewer.SessionID)
	}
}

// Forget removes a run record from memory and disk. Only valid for stopped runs.
func (r *Registry) Forget(id string) error {
	r.mu.Lock()
	rt, ok := r.runs[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("run not found")
	}
	if rt.run.State != StateStopped {
		r.mu.Unlock()
		return fmt.Errorf("cannot forget an active run; stop it first")
	}
	delete(r.runs, id)
	r.mu.Unlock()
	return r.store.Delete(id)
}
