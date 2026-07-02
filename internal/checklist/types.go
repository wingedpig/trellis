// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package checklist implements the phased-checklist outer loop: a driver that
// repeatedly prompts an implementer session to "implement the next phase",
// runs a paired review loop (internal/pair) over that phase's work until the
// reviewer converges, and then advances — until the implementer signals that
// no phases remain (the completion signal, default "COMPLETED").
//
// The checklist itself is a file the implementer reads and tracks; the driver
// is checklist-agnostic. It never parses phases. See PHASE_LOOP_SPEC.md for
// the full specification.
package checklist

import (
	"time"

	"github.com/wingedpig/trellis/internal/pair"
)

// Lifecycle is the high-level run state.
type Lifecycle string

const (
	StatePending Lifecycle = "pending"
	StateRunning Lifecycle = "running"
	StatePaused  Lifecycle = "paused"
	StateStopped Lifecycle = "stopped"
)

// Step is the position within the running outer step-machine.
type Step string

const (
	StepNone    Step = ""
	StepAdvance Step = "advance" // sending "implement next phase" to the implementer
	StepProbe   Step = "probe"   // waiting on the implementer's reply; sentinel-checking it
	StepReview  Step = "review"  // an inner pair loop is reviewing the current phase
)

// StopReason explains why the run terminated (only set when State==stopped).
type StopReason string

const (
	StopReasonCompleted       StopReason = "completed"        // implementer emitted the completion signal
	StopReasonManual          StopReason = "manual"           // user stopped the run
	StopReasonPeerError       StopReason = "peer_error"       // a session crashed / became unusable
	StopReasonSessionTrashed  StopReason = "session_trashed"  // a participant session was trashed
	StopReasonServerRestarted StopReason = "server_restarted" // could not rehydrate on restart
)

// Pause reasons live on PausedReason while State==paused. They are advisory
// (they drive the banner's offered actions) and never affect stop accounting.
const (
	PauseReasonManual            = "manual"
	PauseReasonPhaseNotConverged = "phase_not_converged" // a phase hit max_rounds without converging
	PauseReasonPairStopped       = "pair_stopped"        // the review pair was stopped out from under the run
	PauseReasonReviewStartFailed = "review_start_failed" // could not create the review pair
)

// Config is the user-tunable behavior of a run.
type Config struct {
	// AdvancePrompt is sent to the implementer each time the run advances. It
	// must instruct the implementer to implement the next phase and to reply
	// with the completion signal (on its own line, nothing else) when there
	// are no phases left.
	AdvancePrompt string `json:"advance_prompt"`

	// CompletionSignal is the implementer's "no phases left" sentinel. Matched
	// as the sole non-empty line of the implementer's advance reply
	// (case-insensitive). Default "COMPLETED".
	CompletionSignal string `json:"completion_signal"`

	// ReviewPrompt / FeedbackPrompt / ReviewStopSignal / MaxRounds /
	// ConfirmBeforeRelay are passed straight through to the per-phase pair
	// (internal/pair.Config). ReviewStopSignal is the *reviewer's* convergence
	// word (default "LGTM"); do not confuse it with CompletionSignal.
	ReviewPrompt       string `json:"review_prompt"`
	FeedbackPrompt     string `json:"feedback_prompt"`
	ReviewStopSignal   string `json:"review_stop_signal"`
	MaxRounds          int    `json:"max_rounds"`
	ConfirmBeforeRelay bool   `json:"confirm_before_relay"`
}

// DefaultConfig returns the documented defaults (PHASE_LOOP_SPEC §4).
func DefaultConfig() Config {
	return Config{
		AdvancePrompt: "Implement the next phase from the checklist. Make only the changes for that one " +
			"phase, then stop and wait for review. If there are no phases left to implement, reply with " +
			"exactly COMPLETED on its own line and nothing else.",
		CompletionSignal: "COMPLETED",
		ReviewPrompt: "Review the implementer's work on the current phase. If it fully and correctly " +
			"satisfies that phase, reply with LGTM on its own line. Otherwise, give specific, actionable feedback.",
		FeedbackPrompt:     "Feedback:",
		ReviewStopSignal:   "LGTM",
		MaxRounds:          10,
		ConfirmBeforeRelay: false,
	}
}

// PhaseRecord is one attempt at one phase. A phase that is retried after a
// non-convergence pause produces a second record with the same N.
type PhaseRecord struct {
	N         int        `json:"n"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	PairID    string     `json:"pair_id"`           // the review pair for this attempt
	Status    string     `json:"status"`            // running | converged | not_converged | skipped | stopped | error
	Summary   string     `json:"summary,omitempty"` // first line of the implementer's phase output, for display
}

// Run is the persisted outer-loop record. The on-disk JSON is the
// authoritative source of truth; the in-memory RunRuntime is rebuilt from it
// on every server start (PHASE_LOOP_SPEC §7).
type Run struct {
	ID         string     `json:"id"`
	CreatedAt  time.Time  `json:"created_at"`
	StoppedAt  *time.Time `json:"stopped_at,omitempty"`
	StopReason StopReason `json:"stop_reason,omitempty"`

	// RestartUnresolvable flags a run stopped on rehydration because its
	// participant sessions could not be reconstructed.
	RestartUnresolvable bool `json:"restart_unresolvable,omitempty"`

	State        Lifecycle `json:"state"`
	Step         Step      `json:"step,omitempty"`
	PausedReason string    `json:"paused_reason,omitempty"`
	PhasesDone   int       `json:"phases_done"`

	LastPersistedAt time.Time `json:"last_persisted_at"`

	Implementer pair.AgentRef `json:"implementer"`
	Reviewer    pair.AgentRef `json:"reviewer"`

	Config Config `json:"config"`

	// CurrentPairID is the active review pair when Step==review; empty
	// otherwise.
	CurrentPairID string `json:"current_pair_id,omitempty"`

	// BaselineText is the implementer's last-assistant text captured at the
	// start of the current probe wait (run start, or just before an advance
	// prompt). Probe only acts once the implementer's text differs from this,
	// so it never reviews a stale turn. Persisted so a probe survives restart.
	BaselineText string `json:"baseline_text,omitempty"`

	Phases []PhaseRecord `json:"phases,omitempty"`
}
