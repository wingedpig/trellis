// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package pair implements ad-hoc paired review loops between two Trellis
// sessions. One session is the implementer; the other is the reviewer. The
// driver shuttles each side's latest assistant message to the other with
// configurable prompt prefixes until the reviewer emits the stop signal
// (default "LGTM" on its own line), the round cap is hit, or the user
// intervenes. See PAIRING_SPEC.md for the full specification.
package pair

import "time"

// Lifecycle is the high-level pair state.
type Lifecycle string

const (
	StatePending Lifecycle = "pending"
	StateRunning Lifecycle = "running"
	StatePaused  Lifecycle = "paused"
	StateStopped Lifecycle = "stopped"
)

// Step is the position within the running step-machine.
type Step string

const (
	StepNone               Step = ""
	StepAwaitImplementer   Step = "await_implementer"
	StepRelayToReviewer    Step = "relay_to_reviewer"
	StepAwaitReviewer      Step = "await_reviewer"
	StepRelayToImplementer Step = "relay_to_implementer"
	StepConfirmRelay       Step = "confirm_relay"
)

// StopReason explains why the loop terminated.
type StopReason string

const (
	StopReasonLGTM             StopReason = "lgtm"
	StopReasonMaxRounds        StopReason = "max_rounds"
	StopReasonManual           StopReason = "manual"
	StopReasonUserTyped        StopReason = "user_typed"
	StopReasonPeerError        StopReason = "peer_error"
	StopReasonSessionTrashed   StopReason = "session_trashed"
	StopReasonServerRestarted  StopReason = "server_restarted"
)

// KickoffMode controls how the first relay is scheduled when a pair starts.
type KickoffMode string

const (
	KickoffUseCurrent  KickoffMode = "use_current"
	KickoffWaitForNext KickoffMode = "wait_for_next"
)

// AgentRef identifies one side of a pair: which agent type, which worktree,
// which session.
type AgentRef struct {
	Agent     string `json:"agent"` // "claude" | "codex"
	Worktree  string `json:"worktree"`
	SessionID string `json:"session_id"`
}

// Config is the user-tunable behavior of the pair. Editable mid-loop via the
// Settings dialog (PAIRING_SPEC §7.4).
type Config struct {
	ReviewPrompt       string `json:"review_prompt"`
	FeedbackPrompt     string `json:"feedback_prompt"`
	StopSignal         string `json:"stop_signal"`
	MaxRounds          int    `json:"max_rounds"`
	ConfirmBeforeRelay bool   `json:"confirm_before_relay"`
}

// DefaultConfig returns the documented default config (PAIRING_SPEC §4.2).
func DefaultConfig() Config {
	return Config{
		ReviewPrompt:       "Review this. If it is good, reply with LGTM on its own line.",
		FeedbackPrompt:     "Feedback:",
		StopSignal:         "LGTM",
		MaxRounds:          10,
		ConfirmBeforeRelay: false,
	}
}

// ConfigChange records a mid-loop Settings save. Stored on the pair record
// so the audit trail shows when each field was last changed (PAIRING_SPEC §8.2).
type ConfigChange struct {
	At            time.Time `json:"at"`
	ChangedFields []string  `json:"changed_fields"`
	By            string    `json:"by,omitempty"` // typically "user"
}

// Round records one outbound relay.
type Round struct {
	N                  int       `json:"n"`
	Direction          string    `json:"direction"` // "to_reviewer" | "to_implementer"
	At                 time.Time `json:"at"`
	SourceMessageText  string    `json:"source_message_text,omitempty"`
	DeliveredText      string    `json:"delivered_text,omitempty"`
}

// PendingConfirm is non-nil when the loop is paused awaiting the user's
// edit-before-send decision (PAIRING_SPEC §7.5). It holds the prepared
// outbound text so the UI can render and edit it before approval.
type PendingConfirm struct {
	Direction      string `json:"direction"` // "to_reviewer" | "to_implementer"
	PreparedText   string `json:"prepared_text"`
	SourceCaptured string `json:"source_captured"` // captured message (without prefix)
}

// Pair is the persisted pair record (PAIRING_SPEC §8.2). The on-disk JSON
// representation is the authoritative source of truth; the in-memory
// PairRuntime is rebuilt from this on every server start.
type Pair struct {
	ID         string     `json:"id"`
	CreatedAt  time.Time  `json:"created_at"`
	StoppedAt  *time.Time `json:"stopped_at,omitempty"`
	StopReason StopReason `json:"stop_reason,omitempty"`

	// Owner identifies the feature that created and drives this pair, e.g.
	// "checklist" for a phased-checklist run's per-phase review pair. Empty
	// for user-created pairs. The UI uses this to suppress the standalone
	// pair banner when a higher-level banner already covers the state.
	Owner string `json:"owner,omitempty"`

	// Restart-resolvable flag: when a stopped pair could not have its
	// participant sessions reconstructed on rehydration.
	RestartUnresolvable bool `json:"restart_unresolvable,omitempty"`

	State      Lifecycle `json:"state"`
	Step       Step      `json:"step,omitempty"`
	RoundCount int       `json:"round_count"`

	LastPersistedAt time.Time `json:"last_persisted_at"`

	Implementer AgentRef `json:"implementer"`
	Reviewer    AgentRef `json:"reviewer"`

	Config        Config         `json:"config"`
	ConfigHistory []ConfigChange `json:"config_history,omitempty"`

	Rounds []Round `json:"rounds,omitempty"`

	PendingConfirm *PendingConfirm `json:"pending_confirm,omitempty"`
}
