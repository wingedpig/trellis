// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package pair

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/events"
)

// debounceWindow is the §5.3 idle debounce — we wait this long after a side
// transitions to needs_you before treating it as truly idle and capturing.
// 2s matches the spec.
const debounceWindow = 2 * time.Second

// PairRuntime is the in-memory wrapper around a Pair record. It owns the
// driver goroutine, the subscription to session.state_changed, and the
// channels used to feed commands and events into the state machine.
type PairRuntime struct {
	mu sync.Mutex

	pair   *Pair // the persisted record; mutated under mu and resaved
	store  *Store
	agents *Agents
	bus    events.EventBus

	ctx    context.Context
	cancel context.CancelFunc

	cmds   chan command
	events chan events.Event
	// relay carries debounce-timer wakeups onto the driver goroutine so that
	// capture+relay only ever runs there — never concurrently on the timer's
	// own goroutine — preventing double-relay and RoundCount races.
	relay chan struct{}

	subID events.SubscriptionID

	// awaitingSessionID is the session whose next needs_you transition the
	// driver will act on. Used to discriminate "expected activity" from
	// "user typed into the other session" (PAIRING_SPEC §7.5).
	awaitingSessionID string

	// prevState records the last observed coarse state per session id so
	// user-typed detection can require a genuine needs_you → running
	// transition rather than firing on every re-published running event
	// (e.g. the one we get back from our own relay's Send).
	prevState map[string]string

	// kickoffBaselineText is the implementer's last-assistant text at
	// pair-start in wait_for_next mode. Until the implementer produces a
	// genuinely new assistant message (i.e., captured text differs), the
	// driver suppresses the initial relay. Cleared after the first
	// successful round to leave subsequent rounds unconstrained.
	kickoffBaselineText string
	kickoffPending      bool

	debounceTimer *time.Timer

	// stopRequested guarantees Stop is never lost: even if the command
	// channel is full, the run loop checks this flag on every iteration.
	stopRequested bool
	stopReasonReq StopReason

	started bool
}

func newRuntime(p *Pair, store *Store, agents *Agents, bus events.EventBus) *PairRuntime {
	return &PairRuntime{
		pair:      p,
		store:     store,
		agents:    agents,
		bus:       bus,
		cmds:      make(chan command, 16),
		events:    make(chan events.Event, 64),
		relay:     make(chan struct{}, 1),
		prevState: make(map[string]string),
	}
}

// markStoppedOnLoad finalizes a runtime that was already stopped on disk
// at rehydration time. No driver is launched.
func (rt *PairRuntime) markStoppedOnLoad() {
	// nothing to do — pair record already reflects stopped state.
}

// Pair returns a deep-copy of the underlying record for external inspection.
func (rt *PairRuntime) Pair() Pair {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return *rt.pair
}

// PairPointer returns the live record. Callers must not mutate it; this is
// for internal serialization to the API.
func (rt *PairRuntime) PairPointer() *Pair {
	return rt.pair
}

// command is anything that perturbs the driver's state machine.
type command struct {
	kind        commandKind
	configPatch *Config
	changed     []string
	confirmAct  string // "send" | "skip" | "stop"
	editedText  string
	stopReason  StopReason
	done        chan struct{} // if non-nil, closed by the driver once the command has been applied
}

type commandKind int

const (
	cmdPause commandKind = iota
	cmdResume
	cmdStop
	cmdForceRelay
	cmdUpdateConfig
	cmdConfirm
)

// ----- public control surface -----

// commandAckTimeout bounds how long the synchronous control methods wait for
// the driver to apply a command before returning anyway.
const commandAckTimeout = 2 * time.Second

// Pause transitions the loop to paused. In-flight relays complete; no new
// relays scheduled (PAIRING_SPEC §7.3). Returns once applied (bounded).
func (rt *PairRuntime) Pause() {
	rt.sendWait(command{kind: cmdPause})
}

// Resume returns the loop to running from whatever step it was in.
// Returns once applied (bounded).
func (rt *PairRuntime) Resume() {
	rt.sendWait(command{kind: cmdResume})
}

// Stop terminates the loop with the given reason. Stop is guaranteed: the
// request is recorded under rt.mu before the command is sent, and the run
// loop checks the flag on every iteration, so a full command channel cannot
// drop it. Returns once applied (bounded).
func (rt *PairRuntime) Stop(reason StopReason) {
	if reason == "" {
		reason = StopReasonManual
	}
	rt.mu.Lock()
	stopped := rt.pair.State == StateStopped
	if !stopped {
		rt.stopRequested = true
		rt.stopReasonReq = reason
	}
	rt.mu.Unlock()
	if stopped {
		return
	}
	rt.sendWait(command{kind: cmdStop, stopReason: reason})
}

// ForceRelay skips the remaining debounce on the current await step.
// Fire-and-forget: the relay itself can be slow, so callers don't wait.
func (rt *PairRuntime) ForceRelay() {
	rt.send(command{kind: cmdForceRelay})
}

// UpdateConfig replaces the editable config fields. Per-field semantics live
// in handleConfigUpdate (PAIRING_SPEC §7.4). Returns once applied (bounded).
func (rt *PairRuntime) UpdateConfig(newCfg Config, changed []string) {
	patch := newCfg
	rt.sendWait(command{kind: cmdUpdateConfig, configPatch: &patch, changed: changed})
}

// ConfirmRelay supplies the user's decision when the loop is paused on a
// PendingConfirm step. action is one of "send", "skip", "stop".
// Returns once applied (bounded).
func (rt *PairRuntime) ConfirmRelay(action, editedText string) {
	rt.sendWait(command{kind: cmdConfirm, confirmAct: action, editedText: editedText})
}

func (rt *PairRuntime) send(c command) {
	rt.mu.Lock()
	stopped := rt.pair.State == StateStopped
	rt.mu.Unlock()
	if stopped {
		return
	}
	select {
	case rt.cmds <- c:
	default:
		log.Printf("pair %s: command channel full, dropping %v", rt.pair.ID, c.kind)
	}
}

// sendWait sends a command and waits (bounded by commandAckTimeout) for the
// driver to apply it, so HTTP handlers can return state that reflects the
// transition instead of sleeping and hoping. Returns true if the command was
// acked in time.
func (rt *PairRuntime) sendWait(c command) bool {
	rt.mu.Lock()
	stopped := rt.pair.State == StateStopped
	rt.mu.Unlock()
	if stopped {
		return true
	}
	c.done = make(chan struct{})
	timer := time.NewTimer(commandAckTimeout)
	defer timer.Stop()
	select {
	case rt.cmds <- c:
	case <-timer.C:
		log.Printf("pair %s: command channel full, dropping %v", rt.pair.ID, c.kind)
		return false
	}
	select {
	case <-c.done:
		return true
	case <-timer.C:
		log.Printf("pair %s: timed out waiting for %v to apply", rt.pair.ID, c.kind)
		return false
	}
}

// ----- driver lifecycle -----

// start spins up the driver goroutine for a freshly-created pair.
func (rt *PairRuntime) start(kickoff KickoffMode) {
	rt.mu.Lock()
	if rt.started {
		rt.mu.Unlock()
		return
	}
	rt.started = true
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	rt.pair.State = StateRunning
	rt.pair.Step = StepAwaitImplementer
	rt.awaitingSessionID = rt.pair.Implementer.SessionID
	implRef := rt.pair.Implementer
	rt.mu.Unlock()

	// In wait_for_next mode, snapshot the implementer's current last-
	// assistant text. The first capture will only proceed once the
	// implementer has produced something different — i.e., the user
	// typed a new prompt and got a new response.
	if kickoff == KickoffWaitForNext {
		baseline, _ := rt.agents.CaptureLastAssistantText(implRef)
		rt.mu.Lock()
		rt.kickoffBaselineText = baseline
		rt.kickoffPending = true
		rt.mu.Unlock()
	}

	_ = rt.persist()

	rt.subscribe()
	go rt.run(kickoff)
}

// resume restarts the driver from a rehydrated record.
func (rt *PairRuntime) resume() {
	rt.mu.Lock()
	if rt.started {
		rt.mu.Unlock()
		return
	}
	rt.started = true
	rt.ctx, rt.cancel = context.WithCancel(context.Background())

	// Determine who we're awaiting based on the persisted step. Default to
	// implementer if step is empty (e.g., crashed at pending).
	switch rt.pair.Step {
	case StepAwaitReviewer, StepRelayToImplementer:
		rt.awaitingSessionID = rt.pair.Reviewer.SessionID
	default:
		rt.awaitingSessionID = rt.pair.Implementer.SessionID
	}
	rt.mu.Unlock()

	rt.subscribe()
	// On resume we treat the run as if we just entered the await step. No
	// kickoff is needed; if the partner is already idle, the debounce will
	// fire on the next state event (or immediately via the periodic poll).
	go rt.run(KickoffWaitForNext)
}

// shutdown stops the driver without changing pair state. Used by registry
// shutdown on server stop. The persisted record remains in `running` or
// `paused` state so the next server start can resume.
func (rt *PairRuntime) shutdown() {
	rt.mu.Lock()
	if !rt.started {
		rt.mu.Unlock()
		return
	}
	rt.mu.Unlock()
	if rt.cancel != nil {
		rt.cancel()
	}
	rt.unsubscribe()
}

// subscribe registers the runtime's async event handler with the bus.
func (rt *PairRuntime) subscribe() {
	if rt.bus == nil {
		return
	}
	id, err := rt.bus.SubscribeAsync(events.EventSessionStateChanged, func(_ context.Context, ev events.Event) error {
		// Filter to events for the two paired sessions only.
		sid, _ := ev.Payload["session_id"].(string)
		if sid != rt.pair.Implementer.SessionID && sid != rt.pair.Reviewer.SessionID {
			return nil
		}
		select {
		case rt.events <- ev:
		default:
		}
		return nil
	}, 64)
	if err != nil {
		log.Printf("pair %s: subscribe failed: %v", rt.pair.ID, err)
		return
	}
	rt.subID = id
}

func (rt *PairRuntime) unsubscribe() {
	if rt.bus == nil || rt.subID == "" {
		return
	}
	_ = rt.bus.Unsubscribe(rt.subID)
	rt.subID = ""
}

// run is the driver's main loop. It blocks until the pair is stopped or the
// context is cancelled (server shutdown).
func (rt *PairRuntime) run(kickoff KickoffMode) {
	defer rt.unsubscribe()

	// Initial kickoff: use_current relays the implementer's most recent
	// assistant message immediately. wait_for_next intentionally does
	// nothing and waits for the next state transition (PAIRING_SPEC §4.2).
	if kickoff == KickoffUseCurrent {
		rt.maybeCaptureAndRelay()
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-rt.ctx.Done():
			return
		case c := <-rt.cmds:
			rt.handleCommand(c)
		case ev := <-rt.events:
			rt.handleStateEvent(ev)
		case <-rt.relay:
			// Debounce timer fired (signaled from its goroutine). Run the
			// capture+relay here on the single driver goroutine.
			rt.maybeCaptureAndRelay()
		case <-ticker.C:
			// Periodic self-check — catches the edge case where a needs_you
			// transition was missed (e.g., subscription buffer overrun) by
			// re-evaluating idleness directly from the manager.
			rt.maybeCaptureAndRelay()
		}
		rt.mu.Lock()
		stopped := rt.pair.State == StateStopped
		stopReq := rt.stopRequested
		stopReason := rt.stopReasonReq
		rt.mu.Unlock()
		if stopReq && !stopped {
			// Stop arrived while the command channel was full (the cmdStop
			// frame may have been dropped) — honor it here.
			rt.terminate(stopReason)
			return
		}
		if stopped {
			return
		}
	}
}

// handleCommand dispatches a single command and acks it when done.
func (rt *PairRuntime) handleCommand(c command) {
	if c.done != nil {
		defer close(c.done)
	}
	switch c.kind {
	case cmdPause:
		rt.transitionState(StatePaused, "manual")
	case cmdResume:
		rt.transitionState(StateRunning, "manual")
		// After resume, immediately try to capture so we don't sit on a
		// missed transition.
		rt.maybeCaptureAndRelay()
	case cmdStop:
		reason := c.stopReason
		if reason == "" {
			reason = StopReasonManual
		}
		rt.terminate(reason)
	case cmdForceRelay:
		rt.cancelDebounce()
		rt.maybeCaptureAndRelay()
	case cmdUpdateConfig:
		if c.configPatch != nil {
			rt.handleConfigUpdate(*c.configPatch, c.changed)
		}
	case cmdConfirm:
		rt.handleConfirm(c.confirmAct, c.editedText)
	}
}

// handleStateEvent reacts to a session.state_changed event from the bus.
func (rt *PairRuntime) handleStateEvent(ev events.Event) {
	sid, _ := ev.Payload["session_id"].(string)
	state, _ := ev.Payload["state"].(string)
	trashed, _ := ev.Payload["trashed"].(bool)

	if trashed {
		log.Printf("pair %s: session %s trashed", rt.pair.ID, sid)
		rt.terminate(StopReasonSessionTrashed)
		return
	}

	rt.mu.Lock()
	prev := rt.prevState[sid]
	rt.prevState[sid] = state
	awaiting := rt.awaitingSessionID
	lifecycle := rt.pair.State
	confirmMode := rt.pair.Step == StepConfirmRelay
	rt.mu.Unlock()

	log.Printf("pair %s: state event sid=%s state=%s prev=%s awaiting=%s lifecycle=%s",
		rt.pair.ID, sid, state, prev, awaiting, lifecycle)

	if lifecycle == StateStopped {
		return
	}

	// User-typed detection (PAIRING_SPEC §7.5): pause when a real
	// needs_you → running transition fires on a session other than the
	// one we're awaiting. The "real transition" requirement avoids
	// auto-pausing on re-published running events (e.g. the one we
	// receive back from our own relay's Send) and on the initial state
	// event for a freshly-observed session. We also skip when paused or
	// in confirm-relay mode (the user is intentionally driving).
	if state == events.SessionStateRunning && sid != awaiting &&
		lifecycle == StateRunning && !confirmMode &&
		prev == events.SessionStateNeedsYou {
		log.Printf("pair %s: auto-pause from user-typed detection", rt.pair.ID)
		rt.transitionState(StatePaused, "user_typed")
		return
	}

	if state != events.SessionStateNeedsYou {
		return
	}
	if sid != awaiting {
		return
	}
	if lifecycle != StateRunning {
		return
	}

	// Schedule a debounced capture-and-relay.
	log.Printf("pair %s: arming debounce for %s", rt.pair.ID, sid)
	rt.armDebounce()
}

// armDebounce starts (or restarts) the §5.3 debounce timer.
func (rt *PairRuntime) armDebounce() {
	rt.mu.Lock()
	if rt.debounceTimer != nil {
		rt.debounceTimer.Stop()
	}
	pairID := rt.pair.ID
	rt.debounceTimer = time.AfterFunc(debounceWindow, func() {
		log.Printf("pair %s: debounce fired, signaling driver", pairID)
		// Hand the wakeup to the driver goroutine rather than running
		// capture+relay on this timer goroutine — otherwise this and the
		// driver could relay the same turn concurrently (double-relay +
		// RoundCount race). Non-blocking: a pending signal already covers us.
		select {
		case rt.relay <- struct{}{}:
		default:
		}
	})
	rt.mu.Unlock()
}

// cancelDebounce stops any pending debounce timer.
func (rt *PairRuntime) cancelDebounce() {
	rt.mu.Lock()
	if rt.debounceTimer != nil {
		rt.debounceTimer.Stop()
		rt.debounceTimer = nil
	}
	rt.mu.Unlock()
}

// maybeCaptureAndRelay verifies the awaiting side really is idle and then
// either captures+relays (normal mode) or stages a PendingConfirm (confirm
// mode). No-op if state has moved on since the timer fired.
func (rt *PairRuntime) maybeCaptureAndRelay() {
	rt.mu.Lock()
	if rt.pair.State != StateRunning {
		log.Printf("pair %s: maybeCaptureAndRelay skip — state=%s", rt.pair.ID, rt.pair.State)
		rt.mu.Unlock()
		return
	}
	awaiting := rt.awaitingSessionID
	direction := rt.relayDirection()
	rt.mu.Unlock()

	if direction == "" {
		log.Printf("pair %s: maybeCaptureAndRelay skip — no direction", rt.pair.ID)
		return
	}

	// Determine source ref (the side we're capturing from) and target ref.
	srcRef, dstRef := rt.refsForDirection(direction)
	if srcRef.SessionID == "" || srcRef.SessionID != awaiting {
		log.Printf("pair %s: maybeCaptureAndRelay skip — src %s != awaiting %s",
			rt.pair.ID, srcRef.SessionID, awaiting)
		return
	}

	srcStatus := rt.agents.LookupStatus(srcRef)
	if !srcStatus.Exists {
		log.Printf("pair %s: source session %s missing → peer_error", rt.pair.ID, srcRef.SessionID)
		rt.terminate(StopReasonPeerError)
		return
	}
	if srcStatus.Trashed {
		log.Printf("pair %s: source session %s trashed", rt.pair.ID, srcRef.SessionID)
		rt.terminate(StopReasonSessionTrashed)
		return
	}
	if !srcStatus.Idle {
		// Not actually idle yet — leave the debounce machinery to re-arm
		// on the next state event.
		log.Printf("pair %s: source %s not idle yet, waiting", rt.pair.ID, srcRef.SessionID)
		return
	}

	text, err := rt.agents.CaptureLastAssistantText(srcRef)
	if err != nil {
		log.Printf("pair %s: capture from %s failed: %v", rt.pair.ID, srcRef.SessionID, err)
		return
	}
	if strings.TrimSpace(text) == "" {
		// §5.2 — turn produced no assistant text; treat as empty and
		// re-arm without relaying. The debounce will fire again on the
		// next needs_you transition.
		log.Printf("pair %s: capture from %s returned empty text; waiting for new turn",
			rt.pair.ID, srcRef.SessionID)
		return
	}

	// wait_for_next gate: while kickoffPending is set and we'd relay FROM
	// the implementer, suppress until the captured text differs from the
	// pair-start baseline. Without this, the 15s self-heal ticker would
	// happily relay the existing message even though the user explicitly
	// asked to wait for a fresh turn.
	rt.mu.Lock()
	pendingKickoff := rt.kickoffPending
	baseline := rt.kickoffBaselineText
	rt.mu.Unlock()
	if pendingKickoff && direction == "to_reviewer" && text == baseline {
		log.Printf("pair %s: wait_for_next gate — implementer text unchanged since pair start, holding",
			rt.pair.ID)
		return
	}

	log.Printf("pair %s: captured %d chars from %s, will relay %s",
		rt.pair.ID, len(text), srcRef.SessionID, direction)

	// In to_implementer direction, evaluate stop signal BEFORE relaying.
	// (Direction "to_implementer" means we captured from reviewer.)
	if direction == "to_implementer" {
		rt.mu.Lock()
		signal := rt.pair.Config.StopSignal
		rt.mu.Unlock()
		if MatchStopSignal(text, signal) {
			rt.terminate(StopReasonLGTM)
			return
		}
	}

	rt.mu.Lock()
	confirmBefore := rt.pair.Config.ConfirmBeforeRelay
	prefix := rt.relayPrefix(direction)
	rt.mu.Unlock()

	prepared := composeRelay(prefix, text)

	if confirmBefore {
		rt.mu.Lock()
		rt.pair.Step = StepConfirmRelay
		rt.pair.PendingConfirm = &PendingConfirm{
			Direction:      direction,
			PreparedText:   prepared,
			SourceCaptured: text,
		}
		rt.mu.Unlock()
		_ = rt.persist()
		rt.publish("pair.confirm_pending", map[string]interface{}{
			"pair_id":         rt.pair.ID,
			"direction":       direction,
			"prepared_text":   prepared,
			"source_captured": text,
		})
		return
	}

	rt.dispatchRelay(direction, srcRef, dstRef, text, prepared)
}

// dispatchRelay performs the actual Send and updates pair state. srcRef is
// kept in the signature for symmetry and future audit logging even though
// the dispatch itself only needs the destination.
//
// We update awaitingSessionID and Step **before** calling SendUserMessage.
// SendUserMessage publishes state=running for the destination side, and
// that event races our own dispatchRelay if we haven't yet flipped
// awaiting — handleStateEvent would see (state=running, sid=dst,
// awaiting=OLD_src, prev=needs_you) and auto-pause as "user typed". The
// pre-send update closes that race window; the destination side becomes
// awaiting BEFORE its running event can be processed elsewhere.
func (rt *PairRuntime) dispatchRelay(direction string, srcRef, dstRef AgentRef, sourceText, preparedText string) {
	_ = srcRef
	ctx, cancel := context.WithTimeout(rt.ctx, 30*time.Second)
	defer cancel()

	log.Printf("pair %s: dispatching %s to %s (%d chars)",
		rt.pair.ID, direction, dstRef.SessionID, len(preparedText))

	rt.mu.Lock()
	prevAwaiting := rt.awaitingSessionID
	prevStep := rt.pair.Step
	if direction == "to_reviewer" {
		rt.pair.Step = StepAwaitReviewer
		rt.awaitingSessionID = rt.pair.Reviewer.SessionID
	} else {
		rt.pair.Step = StepAwaitImplementer
		rt.awaitingSessionID = rt.pair.Implementer.SessionID
	}
	rt.mu.Unlock()

	if err := rt.agents.SendUserMessage(ctx, dstRef, preparedText); err != nil {
		log.Printf("pair %s: dispatch to %s failed: %v", rt.pair.ID, dstRef.SessionID, err)
		// Roll back the pre-send step/awaiting update so the audit log
		// matches reality.
		rt.mu.Lock()
		rt.awaitingSessionID = prevAwaiting
		rt.pair.Step = prevStep
		rt.mu.Unlock()
		rt.terminate(StopReasonPeerError)
		return
	}

	now := time.Now()
	rt.mu.Lock()
	rt.pair.RoundCount++
	rt.pair.Rounds = append(rt.pair.Rounds, Round{
		N:                 rt.pair.RoundCount,
		Direction:         direction,
		At:                now,
		SourceMessageText: sourceText,
		DeliveredText:     preparedText,
	})
	rt.pair.PendingConfirm = nil
	// Once a round has fired, the wait_for_next gate is satisfied. Clear
	// the baseline so subsequent rounds aren't reblocked on stale text.
	rt.kickoffPending = false
	rt.kickoffBaselineText = ""

	cap := rt.pair.Config.MaxRounds
	roundCount := rt.pair.RoundCount
	capHit := roundCount >= cap
	pairID := rt.pair.ID
	rt.mu.Unlock()

	_ = rt.persist()

	rt.publish("pair.round", map[string]interface{}{
		"pair_id":      pairID,
		"round_n":      roundCount,
		"direction":    direction,
		"delivered_at": now,
	})

	if capHit {
		rt.terminate(StopReasonMaxRounds)
	}
}

// relayDirection returns the direction implied by the current step.
func (rt *PairRuntime) relayDirection() string {
	switch rt.pair.Step {
	case StepAwaitImplementer:
		return "to_reviewer"
	case StepAwaitReviewer:
		return "to_implementer"
	}
	return ""
}

func (rt *PairRuntime) refsForDirection(direction string) (src, dst AgentRef) {
	if direction == "to_reviewer" {
		return rt.pair.Implementer, rt.pair.Reviewer
	}
	return rt.pair.Reviewer, rt.pair.Implementer
}

func (rt *PairRuntime) relayPrefix(direction string) string {
	if direction == "to_reviewer" {
		return rt.pair.Config.ReviewPrompt
	}
	return rt.pair.Config.FeedbackPrompt
}

// composeRelay assembles the outbound text — prefix on its own block, then
// a blank line, then the captured body. An empty prefix sends the body
// alone (PAIRING_SPEC §6.1).
func composeRelay(prefix, body string) string {
	prefix = strings.TrimRight(prefix, "\n")
	if prefix == "" {
		return body
	}
	return prefix + "\n\n" + body
}

// handleConfirm processes the user's edit-before-send decision.
func (rt *PairRuntime) handleConfirm(action, editedText string) {
	rt.mu.Lock()
	if rt.pair.Step != StepConfirmRelay || rt.pair.PendingConfirm == nil {
		rt.mu.Unlock()
		return
	}
	pending := *rt.pair.PendingConfirm
	rt.mu.Unlock()

	switch action {
	case "send":
		text := editedText
		if text == "" {
			text = pending.PreparedText
		}
		srcRef, dstRef := rt.refsForDirection(pending.Direction)
		rt.dispatchRelay(pending.Direction, srcRef, dstRef, pending.SourceCaptured, text)
	case "skip":
		rt.mu.Lock()
		rt.pair.PendingConfirm = nil
		// Restore the await step matching the direction we were going TO send.
		if pending.Direction == "to_reviewer" {
			rt.pair.Step = StepAwaitImplementer
			rt.awaitingSessionID = rt.pair.Implementer.SessionID
		} else {
			rt.pair.Step = StepAwaitReviewer
			rt.awaitingSessionID = rt.pair.Reviewer.SessionID
		}
		rt.mu.Unlock()
		_ = rt.persist()
	case "stop":
		rt.terminate(StopReasonManual)
	}
}

// handleConfigUpdate applies the per-field save semantics from §7.4.
func (rt *PairRuntime) handleConfigUpdate(newCfg Config, changedFields []string) {
	rt.mu.Lock()

	// If max_rounds was reduced to at-or-below current count, stop the loop
	// with max_rounds.
	maxRoundsReducedHit := newCfg.MaxRounds > 0 &&
		newCfg.MaxRounds <= rt.pair.RoundCount &&
		newCfg.MaxRounds != rt.pair.Config.MaxRounds

	// Track changed fields (computed if not supplied).
	if len(changedFields) == 0 {
		changedFields = diffConfigFields(rt.pair.Config, newCfg)
	}

	rt.pair.Config = newCfg
	rt.pair.ConfigHistory = append(rt.pair.ConfigHistory, ConfigChange{
		At:            time.Now(),
		ChangedFields: changedFields,
		By:            "user",
	})
	pairID := rt.pair.ID
	rt.mu.Unlock()

	_ = rt.persist()

	rt.publish("pair.config_changed", map[string]interface{}{
		"pair_id":        pairID,
		"changed_fields": changedFields,
		"new_config":     newCfg,
	})

	if maxRoundsReducedHit {
		rt.terminate(StopReasonMaxRounds)
	}
}

func diffConfigFields(a, b Config) []string {
	var out []string
	if a.ReviewPrompt != b.ReviewPrompt {
		out = append(out, "review_prompt")
	}
	if a.FeedbackPrompt != b.FeedbackPrompt {
		out = append(out, "feedback_prompt")
	}
	if a.StopSignal != b.StopSignal {
		out = append(out, "stop_signal")
	}
	if a.MaxRounds != b.MaxRounds {
		out = append(out, "max_rounds")
	}
	if a.ConfirmBeforeRelay != b.ConfirmBeforeRelay {
		out = append(out, "confirm_before_relay")
	}
	return out
}

// transitionState moves the lifecycle field and emits the corresponding
// event. trigger labels the cause for telemetry.
func (rt *PairRuntime) transitionState(s Lifecycle, trigger string) {
	rt.mu.Lock()
	if rt.pair.State == s {
		rt.mu.Unlock()
		return
	}
	rt.pair.State = s
	pairID := rt.pair.ID
	rt.mu.Unlock()
	_ = rt.persist()
	evt := "pair.paused"
	if s == StateRunning {
		evt = "pair.resumed"
	}
	rt.publish(evt, map[string]interface{}{
		"pair_id": pairID,
		"reason":  trigger,
	})
}

// terminate puts the pair in stopped state and frees its sessions. Idempotent.
func (rt *PairRuntime) terminate(reason StopReason) {
	rt.mu.Lock()
	if rt.pair.State == StateStopped {
		rt.mu.Unlock()
		return
	}
	now := time.Now()
	rt.pair.State = StateStopped
	rt.pair.StoppedAt = &now
	rt.pair.StopReason = reason
	rt.pair.Step = StepNone
	rt.pair.PendingConfirm = nil
	if rt.debounceTimer != nil {
		rt.debounceTimer.Stop()
		rt.debounceTimer = nil
	}
	pairID := rt.pair.ID
	rt.mu.Unlock()

	_ = rt.persist()
	rt.publish("pair.stopped", map[string]interface{}{
		"pair_id": pairID,
		"reason":  reason,
	})

	// Release session locks so the user can re-pair immediately.
	if reg := globalRegistryRef(); reg != nil {
		reg.freeSessions(rt.pair)
	}

	if rt.cancel != nil {
		rt.cancel()
	}
}

// persist saves the underlying record. Returns error for logging only — the
// driver continues either way (the in-memory state still progresses).
func (rt *PairRuntime) persist() error {
	rt.mu.Lock()
	cp := *rt.pair
	rt.mu.Unlock()
	if err := rt.store.Save(&cp); err != nil {
		log.Printf("pair %s: persist failed: %v", cp.ID, err)
		return err
	}
	return nil
}

// publish emits a pair.* event on the bus if one is configured.
func (rt *PairRuntime) publish(eventType string, payload map[string]interface{}) {
	if rt.bus == nil {
		return
	}
	// Tag with worktree from implementer side for routing parity with
	// other session-scoped events.
	wt := rt.pair.Implementer.Worktree
	_ = rt.bus.Publish(context.Background(), events.Event{
		Type:     eventType,
		Worktree: wt,
		Payload:  payload,
	})
}

// ----- registry back-reference -----
//
// The driver's terminate() needs to free the session locks held by the
// registry. Rather than threading a back-pointer through every method, we
// stash the registry instance in a package-level variable at construction
// time. There's only ever one Registry per process (it lives on the App).

var globalRegistry struct {
	mu sync.RWMutex
	r  *Registry
}

func setGlobalRegistry(r *Registry) {
	globalRegistry.mu.Lock()
	globalRegistry.r = r
	globalRegistry.mu.Unlock()
}

func globalRegistryRef() *Registry {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	return globalRegistry.r
}

// EnsureGlobalRegistry registers the registry for back-reference lookups.
// Call once per process after construction.
func EnsureGlobalRegistry(r *Registry) {
	setGlobalRegistry(r)
}

// ensure import not flagged in builds where some helpers go unused.
var _ = fmt.Errorf
