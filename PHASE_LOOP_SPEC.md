# Phased-Checklist Loop Specification

**Version:** 0.1-draft
**Status:** Backend implemented (`internal/checklist/`); UI pending
**Depends on:** [PAIRING_SPEC.md](PAIRING_SPEC.md)

---

## 1. Overview

The Phased-Checklist Loop ("checklist run") automates working through a
multi-phase checklist: an *implementer* session implements one phase, an inner
[paired review loop](PAIRING_SPEC.md) reviews that phase until the reviewer
converges, and then the run advances to the next phase — repeating until the
implementer signals there are no phases left.

It is the **outer loop** that PAIRING_SPEC §11 reserved as a separate feature
("Phase-checklist driver … built on top of pair loops"). It reuses the pair
primitive unchanged: each phase is one ordinary `internal/pair` loop.

The checklist itself is a **file the implementer reads and tracks** (e.g.
`CHECKLIST.md` with `## Phase N` headings). The driver is checklist-agnostic —
it never parses phases. It only pumps a generic "implement the next phase"
prompt and watches for a completion sentinel.

Like pairing, a run is **ad hoc** — created on the fly between two live
sessions, with no config-file declaration. Either side may be a Claude or Codex
session, in any worktree.

---

## 2. Roles and Constitution

A run has exactly two participants, fixed for its whole life:

- **Implementer** — reads the checklist file, implements one phase per advance,
  and replies with the completion signal when no phases remain.
- **Reviewer** — reviews each phase via the inner pair loop.

Both sessions are **stateful for the entire run** (same CLI processes
throughout), so the implementer carries context between phases and the reviewer
can catch a later phase breaking an earlier one.

Constraints (enforced in `Registry.validateCreate`):

- The two sessions must be distinct.
- Neither may already be in an active checklist run, nor in an active
  standalone pair (the run needs both free to create per-phase pairs).
- Neither may be trashed.

---

## 3. Lifecycle

| State | Meaning |
|-------|---------|
| `pending` | Created, driver not yet started. Transient. |
| `running` | Driver is advancing, probing, or a phase is under review. |
| `paused` | Suspended; awaiting a user decision. Carries a `paused_reason`. |
| `stopped` | Terminated; record is read-only. |

Stop reasons (`stop_reason`, set when entering `stopped`):

- `completed` — the implementer emitted the completion signal (§5.3).
- `manual` — the user stopped the run.
- `peer_error` — the implementer crashed / became unusable.
- `session_trashed` — a participant session was trashed.
- `server_restarted` — a participant could not be reconstructed on restart.

Pause reasons (`paused_reason`, advisory; drive the banner's offered actions):

- `manual` — user paused.
- `phase_not_converged` — a phase hit its round cap without converging (§6).
- `pair_stopped` — the review pair was stopped out from under the run.
- `review_start_failed` — the review pair could not be created.

---

## 4. Configuration

Collected in the create-run modal; defaults from `checklist.DefaultConfig()`.

| Field | Default | Meaning |
|-------|---------|---------|
| `advance_prompt` | *(see below)* | Sent to the implementer to drive each phase. |
| `completion_signal` | `COMPLETED` | The implementer's "no phases left" sentinel. |
| `review_prompt` | *(see below)* | Passed to each phase's pair as its review prompt. |
| `feedback_prompt` | `Feedback:` | Passed to each phase's pair as its feedback prompt. May be empty. |
| `review_stop_signal` | `LGTM` | The **reviewer's** convergence word (the pair's stop signal). Distinct from `completion_signal`. |
| `max_rounds` | `10` | Per-phase round cap (passed to each pair). |
| `confirm_before_relay` | `false` | Passed through to each pair. |

Default `advance_prompt`:

> Implement the next phase from the checklist. Make only the changes for that
> one phase, then stop and wait for review. If there are no phases left to
> implement, reply with exactly COMPLETED on its own line and nothing else.

Default `review_prompt`:

> Review the implementer's work on the current phase. If it fully and correctly
> satisfies that phase, reply with LGTM on its own line. Otherwise, give
> specific, actionable feedback.

> **Note:** if you change `completion_signal`, update `advance_prompt` to name
> the new word. If you change `review_stop_signal`, update `review_prompt`
> likewise.

---

## 5. The Loop

### 5.1 Step machine

```
   start ─────┐
              ▼
ADVANCE ──▶ PROBE ──▶ (completion signal?) ──yes──▶ COMPLETE
   ▲          │
   │          └──no──▶ REVIEW (inner pair loop) ──LGTM──┐
   └──────────────────────────────────────────────────┘
```

The driver (`internal/checklist/driver.go`) is a single goroutine owning all
state, mirroring the pair driver: commands arrive on a channel, session and
pair events on another, and a debounce timer signals via a `relay` channel.

**Starting a run does not prompt the implementer.** The run begins directly in
`probe` (not `advance`): it snapshots the implementer's current last-assistant
text as the `baseline` and waits — the implementer's current/next turn becomes
the *first* phase, which is reviewed as-is. This lets the user (or the
implementer's existing instructions) initiate the first phase; the loop only
sends `advance_prompt` for *subsequent* phases, after each review converges.
(This mirrors the pair loop's `wait_for_next` kickoff. The `baseline` is
persisted so the wait survives a restart.)

- **`advance`** — entered only on a phase *transition* (after a review
  converges). Once the implementer is idle, capture its current last-assistant
  text as the `baseline`, send `advance_prompt`, move to `probe`.
- **`probe`** — wait for the implementer to go idle with a reply that differs
  from `baseline`, then evaluate it (§5.3): completion signal → `COMPLETE`;
  otherwise → `review`.
- **`review`** — `pair.Registry.Create` a pair on the same two sessions with
  `kickoff: use_current` (the phase work is already the implementer's last
  message, so it relays to the reviewer as round 1). Wait for `pair.stopped`
  (§6).
- **`COMPLETE`** — `stopped(completed)`.

### 5.2 Idle detection

The driver subscribes to the same `session.state_changed` bus the inbox and
pair drivers use, filtered to the implementer. A `needs_you` transition arms a
**2-second debounce** (matching pairing); when it fires, the driver verifies
idleness directly via the pair `Agents.LookupStatus` before acting. A 15-second
self-heal ticker re-checks in case a transition was missed. During `review` the
driver ignores implementer state changes — the inner pair owns that turn.

### 5.3 Completion detection

The completion signal is checked **only at `probe`**, on the implementer's reply
to `advance_prompt`. It is stricter than the pair's stop-signal rule
(`IsCompletionSignal`): the reply must be a **single non-empty line equal to the
signal** (case-insensitive, trimmed). Requiring the sentinel to stand alone
prevents a final phase that also produced work in the same turn from being
mistaken for completion and skipping its review.

Because the signal is only evaluated at `probe`, it can never false-positive
during `review`: the implementer only ever sees `feedback_prompt` there, never
"implement the next phase".

### 5.4 Why no free-before-publish race

Between one phase's pair stopping and the next phase's pair being created, the
run always runs a full `advance` + implementer-work + `probe` cycle. By the time
`review` calls `pair.Create` again, the previous pair's `terminate()` has long
since freed the two sessions, so re-pairing them never trips the
"already in an active pair" check. No change to the pair package is required.

---

## 6. Convergence and Non-Convergence

When the review pair stops, the driver reacts by `stop_reason`:

| Pair stop reason | Run action |
|------------------|------------|
| `lgtm` | Phase `converged`; `phases_done++`; back to `advance`. |
| `max_rounds` | Phase `not_converged`; **auto-pause** (`phase_not_converged`). |
| `peer_error` | Stop run (`peer_error`). |
| `session_trashed` | Stop run (`session_trashed`). |
| `manual` / other | Phase `stopped`; pause (`pair_stopped`). |

Auto-pause on non-convergence keeps the human in charge. From the paused state
the user chooses (§7):

- **Retry** — re-run review on the implementer's current output (new pair,
  fresh round counter). *Resume* on a `phase_not_converged` / `pair_stopped` /
  `review_start_failed` pause is treated as Retry.
- **Skip** — mark the phase `skipped`, `phases_done++`, advance.
- **Stop** — end the run.

Retrying a phase appends a second `PhaseRecord` with the same `N`, so the audit
trail shows every attempt.

---

## 7. Controls

Run-level controls, exposed via the API (§8) and a run banner:

- **Pause / Resume** — suspend the run. If a phase is under review, Pause also
  pauses the inner pair; Resume resumes it. (Resume on a non-convergence pause
  means Retry — there is no live pair to un-pause.)
- **Stop** — terminate (`manual`). Stops any active review pair first.
- **Skip** — abandon the current phase, advance to the next.
- **Retry** — re-review the current phase's output.

Run-level pause is distinct from a phase-pair pause. If the user types directly
into a paired session mid-phase, the inner pair auto-pauses (PAIRING_SPEC §7.6)
while the run stays `running` and simply waits — the phase resumes when the pair
does.

---

## 8. API

All under `/api/v1/checklist` (`internal/api/handlers/checklist.go`):

- `POST /api/v1/checklist` — create a run. Body:

  ```json
  {
    "implementer": { "agent": "claude", "worktree": "main", "session_id": "..." },
    "reviewer":    { "agent": "codex",  "worktree": "main", "session_id": "..." },
    "advance_prompt": "...",
    "completion_signal": "COMPLETED",
    "review_prompt": "...",
    "feedback_prompt": "Feedback:",
    "review_stop_signal": "LGTM",
    "max_rounds": 10,
    "confirm_before_relay": false
  }
  ```

  Empty fields are filled from `DefaultConfig`. Validates distinct/free/untrashed
  sessions. Returns the run record.

- `GET /api/v1/checklist` — list active runs (`?include_stopped=true` to include
  finished ones).
- `GET /api/v1/checklist/{id}` — full run record, including phase history.
- `GET /api/v1/checklist/by-session/{session}` — the active run for a session.
- `POST /api/v1/checklist/{id}/pause`
- `POST /api/v1/checklist/{id}/resume`
- `POST /api/v1/checklist/{id}/stop`
- `POST /api/v1/checklist/{id}/skip`
- `POST /api/v1/checklist/{id}/retry`
- `DELETE /api/v1/checklist/{id}` — discard a stopped run.
- `GET /api/v1/checklist/ws` — WebSocket emitting `checklist.*` events.
  Optional `?run_id=...` or `?session_id=...`.

Events published on the bus: `checklist.started`, `checklist.phase_started`,
`checklist.phase_converged`, `checklist.paused`, `checklist.resumed`,
`checklist.stopped`.

---

## 9. Persistence and Restart

Active run state lives in an in-process registry (`internal/checklist/`) backed
by per-run JSON files under `~/.trellis/checklist-runs/{run-id}.json`. **The
disk record is authoritative**; the in-memory runtime is rebuilt from disk on
every server start. Every state-affecting step is persisted atomically
(write-then-rename) before the in-memory state advances.

On server start, the checklist registry rehydrates **after** the pair registry
(so each active review pair already exists):

- A `running` run resumes from its persisted step (`reconcile()`):
  - `review` with a live pair → re-attach and wait; if the pair already stopped
    while the server was down, apply its outcome immediately; if the pair didn't
    survive, pause (`pair_stopped`).
  - `advance` / `probe` → re-check the implementer and proceed.
- A `paused` run stays paused; the user resumes it.
- A run whose participant sessions can't be reconstructed is stopped
  (`server_restarted`, `restart_unresolvable: true`).

A record is removed from disk only when the user discards a stopped run
(`DELETE`).

### 9.1 Per-run record

```json
{
  "id": "...",
  "created_at": "...",
  "stopped_at": "...",
  "stop_reason": "completed",
  "state": "running",
  "step": "review",
  "paused_reason": "",
  "phases_done": 2,
  "implementer": { "agent": "claude", "worktree": "main", "session_id": "..." },
  "reviewer":    { "agent": "codex",  "worktree": "main", "session_id": "..." },
  "config": { "advance_prompt": "...", "completion_signal": "COMPLETED", "...": "..." },
  "current_pair_id": "...",
  "phases": [
    { "n": 1, "status": "converged", "pair_id": "...", "started_at": "...", "ended_at": "..." },
    { "n": 2, "status": "converged", "pair_id": "...", "...": "..." },
    { "n": 3, "status": "running",   "pair_id": "...", "summary": "..." }
  ]
}
```

---

## 10. Implementation Notes

### 10.1 Package layout

`internal/checklist/`:

- `types.go` — `Run`, `Config`, `PhaseRecord`, lifecycle/step/stop/pause enums,
  `DefaultConfig`.
- `signal.go` — `IsCompletionSignal` (strict sentinel match) and `firstLine`.
- `store.go` — per-run JSON persistence (mirrors `pair.Store`).
- `registry.go` — process-wide registry; holds the `*pair.Registry` and
  `*pair.Agents`; validation, rehydrate, shutdown, forget.
- `driver.go` — the per-run `RunRuntime` step machine.

Wired in `internal/app/app.go` (init after the pair registry; shutdown before
it), routed in `internal/api/router.go`, handled in
`internal/api/handlers/checklist.go`.

### 10.2 Reuse over extension

The pair primitive is used **unchanged**. Each phase is a real, independently
inspectable pair record; the run links them via `phases[].pair_id`. The
alternative — widening `pair.Pair` with phase fields — was rejected: it would
entangle the tested single-loop primitive, collapse per-phase audit records, and
complicate rehydration.

### 10.3 Dispatch through existing managers

Advance/probe use the pair package's `Agents.SendUserMessage`,
`Agents.CaptureLastAssistantText`, and `Agents.LookupStatus`. The driver never
touches CLI processes, transcripts, or worktrees directly — it orchestrates.

---

## 11. Non-Goals (this version)

- **Config-file declared runs** — runs are ad hoc only.
- **Driver-parsed checklists** — the implementer owns and tracks the checklist
  file; the driver is phase-agnostic.
- **Advancing on non-convergence** — a capped phase always pauses for the user;
  it never auto-advances over unreviewed work.
- **Fresh reviewer per phase** — both sessions stay stateful for the whole run.
- **Multi-reviewer fan-out / more than two participants.**

---

## 12. Open Questions

1. **Should a completed run archive into a case**, linking every phase's pair
   record (cf. PAIRING_SPEC §10.1)? Likely yes; out of scope here.
2. **Should the advance prompt auto-append the completion-signal instruction**
   using the configured `completion_signal`, so the two can't drift? Currently
   the default prompt hard-codes `COMPLETED` and the operator keeps them in sync.
3. **Should Skip count toward `phases_done`?** It currently does (the number
   reflects "phases advanced past", not "phases converged"). Revisit if the
   banner needs a converged-only count.
