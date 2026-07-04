// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package checklist

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/pair"
)

// debounceWindow matches the pair driver: wait this long after the implementer
// transitions to needs_you before treating it as truly idle.
const debounceWindow = 2 * time.Second

// selfHealInterval re-checks the implementer directly, catching a needs_you
// transition that was missed (e.g. subscription buffer overrun).
const selfHealInterval = 15 * time.Second

// commandAckTimeout bounds how long synchronous control methods wait for the
// driver to apply a command before returning anyway.
const commandAckTimeout = 2 * time.Second

// sendTimeout bounds a single SendUserMessage to the implementer.
const sendTimeout = 30 * time.Second

// RunRuntime is the in-memory wrapper around a Run record. It owns the driver
// goroutine and the subscriptions to session.state_changed (for advance/probe)
// and pair.stopped (for the active review pair).
type RunRuntime struct {
	mu sync.Mutex

	run     *Run
	store   *Store
	agents  *pair.Agents
	pairReg *pair.Registry
	reg     *Registry
	bus     events.EventBus

	ctx    context.Context
	cancel context.CancelFunc

	cmds   chan command
	events chan events.Event
	relay  chan struct{} // debounce-timer wakeups, handled on the driver goroutine

	sessionSub events.SubscriptionID
	pairSub    events.SubscriptionID

	// pausedInnerPair records that a run-level Pause paused the active review
	// pair, so Resume knows to resume it.
	pausedInnerPair bool

	debounceTimer *time.Timer

	// stopRequested guarantees Stop is never lost even if the command channel
	// is full: the run loop checks it on every iteration.
	stopRequested bool
	stopReasonReq StopReason

	started bool
}

func newRuntime(run *Run, store *Store, agents *pair.Agents, pairReg *pair.Registry, reg *Registry, bus events.EventBus) *RunRuntime {
	return &RunRuntime{
		run:     run,
		store:   store,
		agents:  agents,
		pairReg: pairReg,
		reg:     reg,
		bus:     bus,
		cmds:    make(chan command, 16),
		events:  make(chan events.Event, 64),
		relay:   make(chan struct{}, 1),
	}
}

// Run returns a deep-ish copy of the underlying record for external inspection.
func (c *RunRuntime) Run() Run {
	c.mu.Lock()
	defer c.mu.Unlock()
	return *c.run
}

// ----- commands -----

type commandKind int

const (
	cmdPause commandKind = iota
	cmdResume
	cmdStop
	cmdSkip
	cmdRetry
)

type command struct {
	kind       commandKind
	stopReason StopReason
	done       chan struct{}
}

// Pause suspends the run. If a review pair is active it is paused too.
func (c *RunRuntime) Pause() { c.sendWait(command{kind: cmdPause}) }

// Resume returns a paused run to running. When paused for non-convergence, this
// re-runs review on the current phase (equivalent to Retry).
func (c *RunRuntime) Resume() { c.sendWait(command{kind: cmdResume}) }

// Stop terminates the run. Guaranteed: recorded under mu before the command is
// sent, and re-checked every loop iteration.
func (c *RunRuntime) Stop(reason StopReason) {
	if reason == "" {
		reason = StopReasonManual
	}
	c.mu.Lock()
	stopped := c.run.State == StateStopped
	if !stopped {
		c.stopRequested = true
		c.stopReasonReq = reason
	}
	c.mu.Unlock()
	if stopped {
		return
	}
	c.sendWait(command{kind: cmdStop, stopReason: reason})
}

// Skip abandons the current phase and advances to the next.
func (c *RunRuntime) Skip() { c.sendWait(command{kind: cmdSkip}) }

// Retry re-runs the review loop on the implementer's current phase output.
func (c *RunRuntime) Retry() { c.sendWait(command{kind: cmdRetry}) }

func (c *RunRuntime) sendWait(cmd command) bool {
	c.mu.Lock()
	stopped := c.run.State == StateStopped
	c.mu.Unlock()
	if stopped {
		return true
	}
	cmd.done = make(chan struct{})
	timer := time.NewTimer(commandAckTimeout)
	defer timer.Stop()
	select {
	case c.cmds <- cmd:
	case <-timer.C:
		log.Printf("checklist %s: command channel full, dropping %v", c.run.ID, cmd.kind)
		return false
	}
	select {
	case <-cmd.done:
		return true
	case <-timer.C:
		log.Printf("checklist %s: timed out waiting for %v to apply", c.run.ID, cmd.kind)
		return false
	}
}

// ----- lifecycle -----

// start spins up the driver for a freshly created run.
func (c *RunRuntime) start() {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.run.State = StateRunning
	// Starting a run does NOT prompt the implementer. We begin in the probe
	// step, watching for the implementer's current/next turn to complete —
	// that turn is the first phase, which we then review. Only *subsequent*
	// phases are driven by the advance prompt (after each review converges).
	c.run.Step = StepProbe
	impl := c.run.Implementer
	c.mu.Unlock()

	// Snapshot the implementer's current last-assistant text so probe waits
	// for a genuinely new turn rather than reviewing whatever is already there
	// (mirrors the pair loop's wait_for_next kickoff).
	baseline, _ := c.agents.CaptureLastAssistantText(impl)
	c.mu.Lock()
	c.run.BaselineText = baseline
	c.mu.Unlock()

	_ = c.persist()
	c.subscribe()
	go c.loop(false)
}

// resume restarts the driver from a rehydrated record.
func (c *RunRuntime) resume() {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.mu.Unlock()

	c.subscribe()
	go c.loop(true)
}

// shutdown stops the driver without changing run state (server stop). The
// record stays running/paused so the next start resumes it.
func (c *RunRuntime) shutdown() {
	c.mu.Lock()
	started := c.started
	c.mu.Unlock()
	if !started {
		return
	}
	if c.cancel != nil {
		c.cancel()
	}
	c.unsubscribe()
}

func (c *RunRuntime) subscribe() {
	if c.bus == nil {
		return
	}
	// Implementer state changes drive advance/probe.
	sid, err := c.bus.SubscribeAsync(events.EventSessionStateChanged, func(_ context.Context, ev events.Event) error {
		if s, _ := ev.Payload["session_id"].(string); s != c.run.Implementer.SessionID {
			return nil
		}
		select {
		case c.events <- ev:
		default:
		}
		return nil
	}, 64)
	if err != nil {
		log.Printf("checklist %s: session subscribe failed: %v", c.run.ID, err)
	} else {
		c.sessionSub = sid
	}

	// pair.stopped tells us when the active review phase converged / capped.
	pid, err := c.bus.SubscribeAsync("pair.stopped", func(_ context.Context, ev events.Event) error {
		select {
		case c.events <- ev:
		default:
		}
		return nil
	}, 64)
	if err != nil {
		log.Printf("checklist %s: pair subscribe failed: %v", c.run.ID, err)
	} else {
		c.pairSub = pid
	}
}

func (c *RunRuntime) unsubscribe() {
	if c.bus == nil {
		return
	}
	if c.sessionSub != "" {
		_ = c.bus.Unsubscribe(c.sessionSub)
		c.sessionSub = ""
	}
	if c.pairSub != "" {
		_ = c.bus.Unsubscribe(c.pairSub)
		c.pairSub = ""
	}
}

// loop is the driver's main event loop.
func (c *RunRuntime) loop(resumed bool) {
	defer c.unsubscribe()

	if resumed {
		c.reconcile()
	} else {
		// Fresh start: we're already in the probe step (see start()). Kick the
		// probe once in case the implementer is already idle with a new turn.
		c.onImplementerIdle()
	}

	ticker := time.NewTicker(selfHealInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case cmd := <-c.cmds:
			c.handleCommand(cmd)
		case ev := <-c.events:
			c.handleEvent(ev)
		case <-c.relay:
			c.onImplementerIdle()
		case <-ticker.C:
			c.onImplementerIdle()
		}

		c.mu.Lock()
		stopped := c.run.State == StateStopped
		stopReq := c.stopRequested
		reason := c.stopReasonReq
		c.mu.Unlock()
		if stopReq && !stopped {
			c.doStop(reason)
			return
		}
		if stopped {
			return
		}
	}
}

func (c *RunRuntime) handleCommand(cmd command) {
	if cmd.done != nil {
		defer close(cmd.done)
	}
	switch cmd.kind {
	case cmdPause:
		c.handlePause()
	case cmdResume:
		c.handleResume()
	case cmdStop:
		reason := cmd.stopReason
		if reason == "" {
			reason = StopReasonManual
		}
		c.doStop(reason)
	case cmdSkip:
		c.handleSkip()
	case cmdRetry:
		c.handleRetry()
	}
}

func (c *RunRuntime) handleEvent(ev events.Event) {
	switch ev.Type {
	case events.EventSessionStateChanged:
		if sid, _ := ev.Payload["session_id"].(string); sid != c.run.Implementer.SessionID {
			return
		}
		if trashed, _ := ev.Payload["trashed"].(bool); trashed {
			c.terminate(StopReasonSessionTrashed)
			return
		}
		state, _ := ev.Payload["state"].(string)
		c.mu.Lock()
		step := c.run.Step
		running := c.run.State == StateRunning
		c.mu.Unlock()
		if running && (step == StepAdvance || step == StepProbe) && state == events.SessionStateNeedsYou {
			c.armDebounce()
		}
	case "pair.stopped":
		c.handlePairStopped(ev)
	}
}

// ----- advance / probe -----

func (c *RunRuntime) armDebounce() {
	c.mu.Lock()
	if c.debounceTimer != nil {
		c.debounceTimer.Stop()
	}
	c.debounceTimer = time.AfterFunc(debounceWindow, func() {
		select {
		case c.relay <- struct{}{}:
		default:
		}
	})
	c.mu.Unlock()
}

func (c *RunRuntime) stopTimer() {
	if c.debounceTimer != nil {
		c.debounceTimer.Stop()
		c.debounceTimer = nil
	}
}

// onImplementerIdle is the single entry point for "the implementer may be
// done"; it dispatches based on the current step.
func (c *RunRuntime) onImplementerIdle() {
	c.mu.Lock()
	step := c.run.Step
	running := c.run.State == StateRunning
	c.mu.Unlock()
	if !running {
		return
	}
	switch step {
	case StepAdvance:
		c.doAdvance()
	case StepProbe:
		c.doProbe()
	}
}

// enterAdvance moves to the advance step and immediately attempts it (the
// implementer is idle right after a phase converges).
func (c *RunRuntime) enterAdvance() {
	c.mu.Lock()
	if c.run.State != StateRunning {
		c.mu.Unlock()
		return
	}
	c.run.Step = StepAdvance
	c.run.CurrentPairID = ""
	c.mu.Unlock()
	_ = c.persist()
	c.doAdvance()
}

// doAdvance sends the "implement next phase" prompt once the implementer is
// idle, then moves to probe.
func (c *RunRuntime) doAdvance() {
	c.mu.Lock()
	if c.run.State != StateRunning || c.run.Step != StepAdvance {
		c.mu.Unlock()
		return
	}
	impl := c.run.Implementer
	prompt := c.run.Config.AdvancePrompt
	c.mu.Unlock()

	st := c.agents.LookupStatus(impl)
	if !st.Exists {
		c.terminate(StopReasonPeerError)
		return
	}
	if st.Trashed {
		c.terminate(StopReasonSessionTrashed)
		return
	}
	if !st.Idle {
		return // wait; a later state event or the self-heal tick retries
	}

	baseline, _ := c.agents.CaptureLastAssistantText(impl)

	ctx, cancel := context.WithTimeout(c.ctx, sendTimeout)
	defer cancel()
	if err := c.agents.SendUserMessage(ctx, impl, prompt); err != nil {
		log.Printf("checklist %s: advance send failed: %v", c.run.ID, err)
		c.terminate(StopReasonPeerError)
		return
	}

	c.mu.Lock()
	c.run.BaselineText = baseline
	c.run.Step = StepProbe
	c.mu.Unlock()
	_ = c.persist()
	log.Printf("checklist %s: advance sent, awaiting implementer reply", c.run.ID)
}

// doProbe inspects the implementer's reply to the advance prompt: completion
// signal → done; otherwise start a review pair on the phase work.
func (c *RunRuntime) doProbe() {
	c.mu.Lock()
	if c.run.State != StateRunning || c.run.Step != StepProbe {
		c.mu.Unlock()
		return
	}
	impl := c.run.Implementer
	baseline := c.run.BaselineText
	signal := c.run.Config.CompletionSignal
	c.mu.Unlock()

	st := c.agents.LookupStatus(impl)
	if !st.Exists {
		c.terminate(StopReasonPeerError)
		return
	}
	if st.Trashed {
		c.terminate(StopReasonSessionTrashed)
		return
	}
	if !st.Idle {
		return
	}

	text, err := c.agents.CaptureLastAssistantText(impl)
	if err != nil {
		log.Printf("checklist %s: probe capture failed: %v", c.run.ID, err)
		return
	}
	if strings.TrimSpace(text) == "" || text == baseline {
		// Implementer hasn't produced a new turn yet (its phase work, or its
		// reply to the advance prompt); keep waiting.
		return
	}

	if IsCompletionSignal(text, signal) {
		log.Printf("checklist %s: completion signal received, finishing", c.run.ID)
		c.complete()
		return
	}
	c.enterReview(text)
}

// ----- review -----

// enterReview spins up a pair to review the just-produced phase work.
func (c *RunRuntime) enterReview(phaseText string) {
	c.mu.Lock()
	impl := c.run.Implementer
	rev := c.run.Reviewer
	cfg := c.run.Config
	n := c.run.PhasesDone + 1
	c.mu.Unlock()

	rt, err := c.pairReg.Create(pair.CreateOptions{
		Implementer: impl,
		Reviewer:    rev,
		Owner:       "checklist",
		Config: pair.Config{
			ReviewPrompt:       cfg.ReviewPrompt,
			FeedbackPrompt:     cfg.FeedbackPrompt,
			StopSignal:         cfg.ReviewStopSignal,
			MaxRounds:          cfg.MaxRounds,
			ConfirmBeforeRelay: cfg.ConfirmBeforeRelay,
		},
		// The implementer's phase work is already its last message, so relay it
		// to the reviewer immediately.
		Kickoff: pair.KickoffUseCurrent,
	})
	if err != nil {
		log.Printf("checklist %s: could not start review pair: %v", c.run.ID, err)
		c.pause(PauseReasonReviewStartFailed)
		return
	}

	pid := rt.Pair().ID
	now := time.Now()
	c.mu.Lock()
	c.run.Step = StepReview
	c.run.CurrentPairID = pid
	c.run.Phases = append(c.run.Phases, PhaseRecord{
		N:         n,
		StartedAt: now,
		PairID:    pid,
		Status:    "running",
		Summary:   firstLine(phaseText),
	})
	c.mu.Unlock()
	_ = c.persist()
	c.publish("checklist.phase_started", map[string]interface{}{
		"run_id":  c.run.ID,
		"phase_n": n,
		"pair_id": pid,
	})
	log.Printf("checklist %s: phase %d under review (pair %s)", c.run.ID, n, pid)
}

func (c *RunRuntime) handlePairStopped(ev events.Event) {
	pid, _ := ev.Payload["pair_id"].(string)
	c.mu.Lock()
	cur := c.run.CurrentPairID
	step := c.run.Step
	c.mu.Unlock()
	if pid == "" || pid != cur || step != StepReview {
		return
	}
	c.reactToPairStop(pid, asStopReason(ev.Payload["reason"]))
}

// reactToPairStop applies the outcome of the current review pair. Shared by the
// live event path and by reconcile() on restart.
func (c *RunRuntime) reactToPairStop(pid string, reason pair.StopReason) {
	log.Printf("checklist %s: review pair %s stopped (%s)", c.run.ID, pid, reason)
	switch reason {
	case pair.StopReasonLGTM:
		c.finishPhase("converged")
		c.mu.Lock()
		c.run.PhasesDone++
		n := c.run.PhasesDone
		c.run.CurrentPairID = ""
		c.mu.Unlock()
		c.publish("checklist.phase_converged", map[string]interface{}{
			"run_id":  c.run.ID,
			"phase_n": n,
		})
		c.enterAdvance()
	case pair.StopReasonMaxRounds:
		c.finishPhase("not_converged")
		c.pause(PauseReasonPhaseNotConverged)
	case pair.StopReasonPeerError:
		c.finishPhase("error")
		c.terminate(StopReasonPeerError)
	case pair.StopReasonSessionTrashed:
		c.finishPhase("error")
		c.terminate(StopReasonSessionTrashed)
	default:
		// manual / user_typed / anything else: the phase pair was stopped out
		// from under us. Pause so the user decides retry / skip / stop.
		c.finishPhase("stopped")
		c.pause(PauseReasonPairStopped)
	}
}

// finishPhase closes out the most recent (running) phase record.
func (c *RunRuntime) finishPhase(status string) {
	c.mu.Lock()
	if n := len(c.run.Phases); n > 0 {
		last := &c.run.Phases[n-1]
		if last.Status == "running" {
			now := time.Now()
			last.EndedAt = &now
			last.Status = status
		}
	}
	c.mu.Unlock()
	_ = c.persist()
}

// ----- command handlers -----

func (c *RunRuntime) handlePause() {
	c.mu.Lock()
	if c.run.State != StateRunning {
		c.mu.Unlock()
		return
	}
	step := c.run.Step
	cur := c.run.CurrentPairID
	c.mu.Unlock()

	if step == StepReview && cur != "" {
		if prt := c.pairReg.Get(cur); prt != nil {
			prt.Pause()
			c.mu.Lock()
			c.pausedInnerPair = true
			c.mu.Unlock()
		}
	}
	c.transition(StatePaused, PauseReasonManual)
}

func (c *RunRuntime) handleResume() {
	c.mu.Lock()
	if c.run.State != StatePaused {
		c.mu.Unlock()
		return
	}
	reason := c.run.PausedReason
	step := c.run.Step
	cur := c.run.CurrentPairID
	pausedInner := c.pausedInnerPair
	c.mu.Unlock()

	// Resuming a non-convergence / dropped-pair pause means "try this phase
	// again" — there is no live pair to un-pause.
	if reason == PauseReasonPhaseNotConverged ||
		reason == PauseReasonPairStopped ||
		reason == PauseReasonReviewStartFailed {
		c.transition(StateRunning, PauseReasonManual)
		c.handleRetry()
		return
	}

	c.transition(StateRunning, PauseReasonManual)
	if step == StepReview && cur != "" && pausedInner {
		if prt := c.pairReg.Get(cur); prt != nil {
			prt.Resume()
		}
		c.mu.Lock()
		c.pausedInnerPair = false
		c.mu.Unlock()
	} else if step == StepAdvance || step == StepProbe {
		c.onImplementerIdle()
	}
}

func (c *RunRuntime) handleRetry() {
	c.mu.Lock()
	if c.run.State == StateStopped {
		c.mu.Unlock()
		return
	}
	if c.run.State == StatePaused {
		c.mu.Unlock()
		c.transition(StateRunning, PauseReasonManual)
	} else {
		c.mu.Unlock()
	}

	impl := c.run.Implementer
	c.mu.Lock()
	c.run.CurrentPairID = ""
	c.mu.Unlock()

	text, err := c.agents.CaptureLastAssistantText(impl)
	if err != nil || strings.TrimSpace(text) == "" {
		// Nothing to re-review; fall back to advancing.
		c.enterAdvance()
		return
	}
	c.enterReview(text)
}

func (c *RunRuntime) handleSkip() {
	c.mu.Lock()
	step := c.run.Step
	cur := c.run.CurrentPairID
	c.mu.Unlock()

	// Tear down any active review pair first. Its pair.stopped event will be a
	// no-op for us once CurrentPairID is cleared below.
	if step == StepReview && cur != "" {
		if prt := c.pairReg.Get(cur); prt != nil {
			prt.Stop(pair.StopReasonManual)
		}
	}
	c.finishPhase("skipped")

	c.mu.Lock()
	c.run.CurrentPairID = ""
	c.run.PhasesDone++ // count the skipped phase as advanced-past
	c.run.State = StateRunning
	c.run.PausedReason = ""
	c.pausedInnerPair = false
	c.mu.Unlock()
	_ = c.persist()
	c.enterAdvance()
}

// ----- terminal / state transitions -----

func (c *RunRuntime) transition(s Lifecycle, reason string) {
	c.mu.Lock()
	if c.run.State == s {
		c.mu.Unlock()
		return
	}
	c.run.State = s
	if s == StatePaused {
		c.run.PausedReason = reason
	} else {
		c.run.PausedReason = ""
	}
	c.mu.Unlock()
	_ = c.persist()
	evt := "checklist.paused"
	if s == StateRunning {
		evt = "checklist.resumed"
	}
	c.publish(evt, map[string]interface{}{
		"run_id": c.run.ID,
		"reason": reason,
	})
}

func (c *RunRuntime) pause(reason string) {
	c.transition(StatePaused, reason)
}

func (c *RunRuntime) complete() {
	c.finish(StopReasonCompleted)
}

func (c *RunRuntime) terminate(reason StopReason) {
	c.finish(reason)
}

// doStop stops any active review pair, then finishes the run.
func (c *RunRuntime) doStop(reason StopReason) {
	c.mu.Lock()
	step := c.run.Step
	cur := c.run.CurrentPairID
	c.mu.Unlock()
	if step == StepReview && cur != "" {
		if prt := c.pairReg.Get(cur); prt != nil {
			prt.Stop(pair.StopReasonManual)
		}
	}
	c.finish(reason)
}

// finish moves the run to stopped, frees its sessions, and cancels the driver.
// Idempotent.
func (c *RunRuntime) finish(reason StopReason) {
	c.mu.Lock()
	if c.run.State == StateStopped {
		c.mu.Unlock()
		return
	}
	now := time.Now()
	c.run.State = StateStopped
	c.run.StoppedAt = &now
	c.run.StopReason = reason
	c.run.Step = StepNone
	c.run.PausedReason = ""
	c.stopTimer()
	c.mu.Unlock()

	_ = c.persist()
	c.publish("checklist.stopped", map[string]interface{}{
		"run_id": c.run.ID,
		"reason": reason,
	})

	if c.reg != nil {
		c.reg.freeSessions(c.run)
	}
	if c.cancel != nil {
		c.cancel()
	}
}

// reconcile re-establishes the correct step after a restart.
func (c *RunRuntime) reconcile() {
	c.mu.Lock()
	step := c.run.Step
	state := c.run.State
	cur := c.run.CurrentPairID
	c.mu.Unlock()
	if state != StateRunning {
		return // paused runs wait for the user; stopped ones have no driver
	}
	switch step {
	case StepReview:
		if cur == "" {
			c.enterAdvance()
			return
		}
		prt := c.pairReg.Get(cur)
		if prt == nil {
			// The review pair didn't survive the restart. Treat as a dropped
			// pair and let the user decide.
			c.finishPhase("stopped")
			c.pause(PauseReasonPairStopped)
			return
		}
		if p := prt.Pair(); p.State == pair.StateStopped {
			c.reactToPairStop(cur, p.StopReason)
		}
		// else: pair still running — normal event handling will catch its stop.
	case StepAdvance, StepProbe:
		c.onImplementerIdle()
	default:
		c.enterAdvance()
	}
}

// ----- helpers -----

func (c *RunRuntime) persist() error {
	c.mu.Lock()
	cp := *c.run
	c.mu.Unlock()
	if err := c.store.Save(&cp); err != nil {
		log.Printf("checklist %s: persist failed: %v", cp.ID, err)
		return err
	}
	return nil
}

func (c *RunRuntime) publish(eventType string, payload map[string]interface{}) {
	if c.bus == nil {
		return
	}
	_ = c.bus.Publish(context.Background(), events.Event{
		Type:     eventType,
		Worktree: c.run.Implementer.Worktree,
		Payload:  payload,
	})
}

// asStopReason coerces an event payload value into a pair.StopReason. The
// in-memory bus preserves the concrete type, so pair.stopped carries a
// pair.StopReason (not a string), but we accept both defensively.
func asStopReason(v interface{}) pair.StopReason {
	switch x := v.(type) {
	case pair.StopReason:
		return x
	case string:
		return pair.StopReason(x)
	}
	return ""
}
