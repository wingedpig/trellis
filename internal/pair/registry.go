// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package pair

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wingedpig/trellis/internal/events"
)

// Registry is the process-wide collection of pairs. It owns persistence
// (via Store), per-pair drivers, and the indexes used to enforce the
// "a session may belong to at most one active pair" rule (PAIRING_SPEC §2).
type Registry struct {
	mu     sync.RWMutex
	pairs  map[string]*PairRuntime // pair id -> runtime
	bySess map[string]string       // session id -> pair id (active only)

	store  *Store
	agents *Agents
	bus    events.EventBus
}

// NewRegistry creates a registry. The store may be a no-op store from
// NewStore(""); the bus may be nil in tests, in which case event publication
// is a no-op.
func NewRegistry(store *Store, agents *Agents, bus events.EventBus) *Registry {
	return &Registry{
		pairs:  make(map[string]*PairRuntime),
		bySess: make(map[string]string),
		store:  store,
		agents: agents,
		bus:    bus,
	}
}

// CreateOptions configures a new pair.
type CreateOptions struct {
	Implementer AgentRef
	Reviewer    AgentRef
	Config      Config
	Kickoff     KickoffMode
}

// Create validates the options, persists the pair, registers it, and starts
// its driver. Returns the active runtime.
func (r *Registry) Create(opts CreateOptions) (*PairRuntime, error) {
	if err := r.validateCreate(opts); err != nil {
		return nil, err
	}

	cfg := opts.Config
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = DefaultConfig().MaxRounds
	}
	if cfg.StopSignal == "" {
		cfg.StopSignal = DefaultConfig().StopSignal
	}
	// ReviewPrompt / FeedbackPrompt may legitimately be empty (§6.1).

	now := time.Now()
	p := &Pair{
		ID:          uuid.New().String(),
		CreatedAt:   now,
		State:       StatePending,
		Implementer: opts.Implementer,
		Reviewer:    opts.Reviewer,
		Config:      cfg,
	}

	rt := newRuntime(p, r.store, r.agents, r.bus)

	r.mu.Lock()
	r.pairs[p.ID] = rt
	r.bySess[opts.Implementer.SessionID] = p.ID
	r.bySess[opts.Reviewer.SessionID] = p.ID
	r.mu.Unlock()

	// Persist initial pending state, then kick off.
	if err := r.store.Save(p); err != nil {
		log.Printf("pair: failed to persist new pair %s: %v", p.ID, err)
	}

	rt.publish("pair.started", map[string]interface{}{
		"pair_id":     p.ID,
		"implementer": p.Implementer,
		"reviewer":    p.Reviewer,
		"config":      p.Config,
	})

	rt.start(opts.Kickoff)
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
	if _, ok := r.bySess[opts.Implementer.SessionID]; ok {
		r.mu.RUnlock()
		return fmt.Errorf("implementer session is already in an active pair")
	}
	if _, ok := r.bySess[opts.Reviewer.SessionID]; ok {
		r.mu.RUnlock()
		return fmt.Errorf("reviewer session is already in an active pair")
	}
	r.mu.RUnlock()

	for _, ref := range []AgentRef{opts.Implementer, opts.Reviewer} {
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

// Get returns the runtime for a pair, or nil if unknown.
func (r *Registry) Get(id string) *PairRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pairs[id]
}

// BySession returns the runtime a session currently belongs to, or nil.
func (r *Registry) BySession(sessionID string) *PairRuntime {
	r.mu.RLock()
	pid := r.bySess[sessionID]
	r.mu.RUnlock()
	if pid == "" {
		return nil
	}
	return r.Get(pid)
}

// List returns a snapshot of every pair currently in the registry, including
// stopped ones that haven't been discarded.
func (r *Registry) List() []*PairRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*PairRuntime, 0, len(r.pairs))
	for _, rt := range r.pairs {
		out = append(out, rt)
	}
	return out
}

// Rehydrate is called once on server start. It loads every persisted pair
// from disk and either resumes its driver (running/paused) or marks it
// stopped with peer_error/server_restarted as appropriate (PAIRING_SPEC §8.1).
func (r *Registry) Rehydrate() {
	if r.store == nil {
		return
	}
	pairs, err := r.store.LoadAll()
	if err != nil {
		log.Printf("pair: rehydrate load failed: %v", err)
		return
	}
	for _, p := range pairs {
		// Already-stopped records are kept in memory for audit/inspection but
		// have no active driver.
		if p.StoppedAt != nil || p.State == StateStopped {
			rt := newRuntime(p, r.store, r.agents, r.bus)
			rt.markStoppedOnLoad()
			r.mu.Lock()
			r.pairs[p.ID] = rt
			r.mu.Unlock()
			continue
		}

		// Active pair — verify both sessions still resolve.
		implStatus := r.agents.LookupStatus(p.Implementer)
		revStatus := r.agents.LookupStatus(p.Reviewer)
		if !implStatus.Exists || implStatus.Trashed || !revStatus.Exists || revStatus.Trashed {
			now := time.Now()
			p.State = StateStopped
			p.StoppedAt = &now
			p.StopReason = StopReasonPeerError
			p.RestartUnresolvable = true
			if err := r.store.Save(p); err != nil {
				log.Printf("pair: rehydrate save (unresolvable) failed for %s: %v", p.ID, err)
			}
			rt := newRuntime(p, r.store, r.agents, r.bus)
			rt.markStoppedOnLoad()
			r.mu.Lock()
			r.pairs[p.ID] = rt
			r.mu.Unlock()
			continue
		}

		rt := newRuntime(p, r.store, r.agents, r.bus)
		r.mu.Lock()
		r.pairs[p.ID] = rt
		r.bySess[p.Implementer.SessionID] = p.ID
		r.bySess[p.Reviewer.SessionID] = p.ID
		r.mu.Unlock()
		rt.resume()
	}
	if n := len(pairs); n > 0 {
		log.Printf("pair: rehydrated %d pair records", n)
	}
}

// Shutdown stops every active driver. Records remain on disk.
func (r *Registry) Shutdown(ctx context.Context) {
	r.mu.RLock()
	runtimes := make([]*PairRuntime, 0, len(r.pairs))
	for _, rt := range r.pairs {
		runtimes = append(runtimes, rt)
	}
	r.mu.RUnlock()
	for _, rt := range runtimes {
		rt.shutdown()
	}
}

// freeSessions removes the session-id → pair-id mappings for a pair once it
// is no longer active. Called by the driver when transitioning to stopped.
func (r *Registry) freeSessions(p *Pair) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bySess[p.Implementer.SessionID] == p.ID {
		delete(r.bySess, p.Implementer.SessionID)
	}
	if r.bySess[p.Reviewer.SessionID] == p.ID {
		delete(r.bySess, p.Reviewer.SessionID)
	}
}

// Forget removes a pair record from both memory and disk. Only valid for
// stopped pairs.
func (r *Registry) Forget(id string) error {
	r.mu.Lock()
	rt, ok := r.pairs[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("pair not found")
	}
	if rt.pair.State != StateStopped {
		r.mu.Unlock()
		return fmt.Errorf("cannot forget an active pair; stop it first")
	}
	delete(r.pairs, id)
	r.mu.Unlock()
	return r.store.Delete(id)
}
