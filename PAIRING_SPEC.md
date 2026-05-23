# Paired Review Loop Specification

**Version:** 0.1-draft
**Status:** Specification for Implementation

---

## 1. Overview

The Paired Review Loop ("pairing") automates a workflow Trellis users perform
manually today: an *implementer* session produces work, a *reviewer* session
critiques it, the critique is fed back to the implementer, and the cycle
repeats until the reviewer is satisfied or the user steps in.

Pairing is **ad hoc** — created on the fly from the web interface between any
two live sessions, with no config-file declaration. A pair lasts as long as the
loop is running; when it stops, the pair record is retained for audit but no
longer affects either session.

The feature is provider-agnostic: either side can be a Claude session or a
Codex session, in any combination, in the same or different worktrees.

---

## 2. Roles and Pair Constitution

A pair has exactly two roles:

- **Implementer** — produces or revises the artifact. Receives reviewer
  feedback as a normal user message and reacts to it.
- **Reviewer** — receives the implementer's latest output and critiques it.
  Signals convergence by emitting a configurable stop signal (default `LGTM`).

Constraints:

- The two sessions must be distinct (same session cannot self-pair).
- A session may participate in at most one **active** pair at a time. Once a
  pair stops, both sessions are free to enter new pairs.
- Sessions in the trash may not be paired.
- Either session may be Claude or Codex; either may be in any worktree.

---

## 3. Lifecycle

A pair moves through these states:

| State | Meaning |
|-------|---------|
| `pending` | Created but not yet kicked off (e.g. waiting for implementer's first turn to complete). |
| `running` | Loop is actively waiting on a side, generating, or relaying. |
| `paused` | Loop is suspended; no relays will fire until resumed. |
| `stopped` | Loop has terminated; record is read-only. |

Stop reasons (recorded on the pair when it enters `stopped`):

- `lgtm` — reviewer's message matched the stop signal.
- `max_rounds` — round cap reached without convergence.
- `manual` — user clicked Stop.
- `user_typed` — user sent a message directly into a paired session (see §7).
- `peer_error` — implementer or reviewer crashed, hit an unrecoverable
  permission denial, or otherwise became unusable.
- `session_trashed` — one of the paired sessions was trashed.

---

## 4. Creating a Pair

### 4.1 Entry points

A pair can be created from two places in the UI:

1. **Session page action.** On `/claude/{w}/{s}` and `/codex/{w}/{s}`, a new
   **Pair for Review** button (placed next to Save to Case / Commit / Wrap Up)
   opens the Pair modal pre-seeded with the current session as implementer.
2. **Inbox row action.** In the session inbox popup, each row gains a "link"
   icon on hover. Clicking it opens the Pair modal pre-seeded with that row's
   session as implementer.

There is no global "create pair from scratch" entry; you always start from one
side. This keeps the picker shorter and the action discoverable in context.

### 4.2 Pair modal

The modal collects:

- **This session** (read-only) — the entry-point session, labeled with its
  role. A swap arrow lets the user flip roles before submitting.
- **Partner session** — a searchable picker listing all eligible sessions
  (live, not the same session, not currently in an active pair). Each entry
  shows the session display name, worktree, and `CLAUDE`/`CODEX` agent badge,
  identical to inbox row formatting.
- **Review prompt** — text field, prefixes the implementer's last message when
  relayed to the reviewer. Default: `Review this. If it is good, reply with LGTM on its own line.`
- **Feedback prompt** — text field, prefixes the reviewer's last message when
  relayed to the implementer. Default: `Feedback:`
- **Stop signal** — string field, default `LGTM`. Matched per §6.3.
- **Max rounds** — integer, default `10`. A "round" is one implementer→reviewer
  hop plus one reviewer→implementer hop; the loop stops after at most this
  many hops in either direction.
- **Kickoff** — radio:
  - *Use implementer's current last message* (default if the implementer's
    most recent transcript entry is an assistant message that is not already
    a previously relayed reviewer message).
  - *Wait for the implementer's next turn* (default otherwise; also chosen
    when the implementer is currently generating or has no assistant message
    yet).
- **Confirm before each relay** — checkbox, default off. When on, every relay
  pauses for explicit user approval (with an edit-before-send affordance)
  before being sent. See §7.5.

The modal remembers the last-used review prompt, feedback prompt, stop signal,
and max rounds in `localStorage` per browser, so repeat usage doesn't require
retyping.

### 4.3 Submission

On submit, the server validates the constraints in §2, creates a pair record,
and:

- Returns the new pair's id and initial state.
- If kickoff is *Use implementer's current last message* and the implementer
  is idle, the loop immediately schedules the first implementer→reviewer
  relay.
- Otherwise, the pair enters `pending` and the loop arms a listener for the
  implementer's next `needs_you` transition.

---

## 5. The Loop

### 5.1 Step machine

Once `running`, the loop alternates between two steps:

- **`await_implementer`** — wait for the implementer session to enter
  `needs_you` and remain there for the debounce period (§5.3). Then capture
  the implementer's latest assistant message (§5.4) and transition to
  **`relay_to_reviewer`**.
- **`relay_to_reviewer`** — prefix the captured message with the review
  prompt and send it as a user message to the reviewer. Increment the round
  counter. Transition to **`await_reviewer`**.
- **`await_reviewer`** — wait for the reviewer session to enter `needs_you`
  and debounce. Then capture the reviewer's latest assistant message,
  evaluate the stop signal (§6.3): if matched, stop with reason `lgtm`.
  Otherwise transition to **`relay_to_implementer`**.
- **`relay_to_implementer`** — prefix with the feedback prompt and send to
  the implementer. Transition to **`await_implementer`**.

The round cap is checked after each successful relay; if hit, stop with
reason `max_rounds`.

### 5.2 What "latest assistant message" means

The loop captures the final assistant text block of the most recent turn,
with tool-call records, scratchpad/thinking blocks, and Trellis-injected
metadata stripped. For Claude this is the last `assistant` event whose
content is plain text; for Codex it is the last `agent_message` in the
JSON-RPC transcript. Both providers' transcript packages already expose
this — pairing uses those getters, not raw event streams.

If a turn produced zero assistant text (e.g., Claude only made tool calls
and the turn ended without a user-visible message), the loop treats the
turn as empty and re-arms `await_implementer` / `await_reviewer` without
relaying. This avoids no-op relays that confuse the partner.

### 5.3 Idle detection and debounce

The loop subscribes to the existing `session.state_changed` event bus that
the inbox already consumes. A side is considered "ready to relay" when it
has been in `needs_you` continuously for **2 seconds**. The debounce
prevents firing on transient idle blips during long tool-using turns.

If the side flips back to `running` during the debounce window, the timer
resets.

### 5.4 First relay

When the loop is kicked off in *Use implementer's current last message*
mode, the loop skips the initial `await_implementer` debounce and proceeds
straight to `relay_to_reviewer`. The kickoff is treated as round 1's
implementer→reviewer hop; the round counter starts at 1 on first relay.

### 5.5 Cross-worktree safety

Pair messages are delivered through the same per-session input pathway as
user-typed messages. The loop driver does not touch worktree filesystems
directly; if a session's underlying worktree is removed or its CLI crashes,
the next send fails and the loop stops with `peer_error`.

---

## 6. Configuration and Matching Rules

### 6.1 Prompts

The review and feedback prompts are inserted **verbatim** as the first line
(or lines) of the relayed user message, followed by a blank line, followed
by the captured assistant message. No formatting wrappers, no quoting, no
markdown fences are added. Users that want quoting can include it in the
prompt template themselves.

Both prompts may be the empty string. An empty prompt sends the captured
message with no prefix at all.

### 6.2 Max rounds

Counted as outbound relays in either direction. A pair with `max_rounds=10`
will fire at most 10 relays before stopping with `max_rounds`. The default
of 10 is a balance between giving the loop room to converge and bounding
runaway cost.

### 6.3 Stop-signal matching

The stop signal is matched against the reviewer's captured assistant
message text, after tool-call and scratchpad stripping but before any
prefix is added.

Rules:

- The signal must appear as **a line whose content, after trimming
  leading and trailing whitespace, equals the signal**.
- Match is case-insensitive (`LGTM` matches `lgtm`).
- No partial or in-line matches. `LGTM with one nit:` on a line does
  **not** match; the reviewer must put `LGTM` on a line by itself.
- Surrounding lines — other paragraphs, code fences, prose nits — are
  ignored. The presence of a signal-only line alone triggers convergence.

The own-line rule lets the reviewer attach optional context above or
below the signal without ambiguity about whether the loop stopped, and
matches how models naturally format a verdict when prompted with the
default review prompt.

---

## 7. Live Control and Visibility

### 7.1 Pair banner

While a session is part of an active pair, its session page (Claude or
Codex) shows a fixed banner above the message area:

```
🔗 Paired with @main - Session 3 (Codex)  ·  Reviewer
   Round 4 / 10  ·  waiting for partner
   [Pause] [Stop] [Force relay] [Settings] [Edit before send: off]
```

The banner shows:

- Partner session name and agent.
- Current side's role.
- Round count and step (`waiting for partner`, `relaying`, `awaiting
  this side`, `paused`).
- Action buttons (§7.3).
- Edit-before-send toggle (§7.4).

When the pair stops, the banner remains for a few seconds with the stop
reason and a dismiss button, then hides.

### 7.2 Relay markers in transcripts

Each relayed user message appears in the receiving session's transcript
with a visible badge: `relayed from <partner session> · round N`. The
badge clicks through to the partner session at the source message.

This makes paired transcripts self-documenting and lets the user
distinguish hand-typed prompts from auto-relayed ones in the conversation
history — both at runtime and later when the case is archived.

### 7.3 Controls

The banner exposes:

- **Pause** — transitions the loop to `paused`. The currently in-flight
  relay (if any) is allowed to complete; no new relays are scheduled.
- **Resume** — only shown while paused. Returns the loop to `running`
  from whatever step it was in.
- **Stop** — terminates the loop with reason `manual`. Confirmation
  required only if the loop is in the middle of generating output.
- **Force relay** — only enabled when a side has entered `needs_you` but
  the debounce timer hasn't fired yet. Skips the remaining debounce and
  triggers the relay immediately. Useful when the user knows the side is
  truly done and is impatient.
- **Settings** — opens the Settings dialog (§7.4). Available in any
  state, including `paused` and during in-flight relays.

### 7.4 Settings dialog

The Settings button reopens a modal showing the pair's current
configuration. The dialog uses the same field layout as the Pair-create
modal (§4.2), with two differences:

- Partner session and role assignment are read-only. Changing those
  requires stopping the pair and creating a new one.
- All other fields — review prompt, feedback prompt, stop signal,
  max rounds, confirm-before-relay — are editable and apply on the
  **next** relay.

Per-field save semantics:

- **Review / feedback prompt** — replaces the template used for
  subsequent relays. Already-delivered relays are not retroactively
  altered.
- **Stop signal** — replaces immediately. The new signal is **not**
  retroactively evaluated against the previously-captured reviewer
  message; it takes effect on the next reviewer turn.
- **Max rounds** — if the new cap is **at or below** the current round
  count, the loop stops immediately with reason `max_rounds`. Otherwise
  it simply extends or tightens the remaining budget.
- **Confirm before relay** — toggle. If turned on while a relay is
  already in flight, that relay completes uninterrupted; subsequent
  relays go through confirm mode (§7.5).

Each save writes through to the persisted pair record (§8.2) before
acknowledging to the client, and emits a `pair.config_changed` event
carrying the new config. The banner and any open Settings dialogs in
other tabs update in place.

The dialog can be reopened any number of times over the life of the
pair, and remains available after the pair is stopped — in that case
all fields are read-only and the dialog functions purely as an audit
view of the final config.

### 7.5 Edit-before-send (confirm mode)

When **Confirm before each relay** is on, the loop pauses before each
relay and shows the prepared message in a modal:

- Prefix and captured message are shown together as the final outgoing
  text.
- User can edit any of it inline.
- **Send** dispatches the (possibly edited) message and resumes the loop.
- **Skip** discards this relay and re-arms the same `await_*` step (i.e.,
  waits for the same side to produce another turn). Useful when the
  captured turn is irrelevant (e.g., the implementer just asked a
  clarifying question).
- **Stop loop** terminates with reason `manual`.

Confirm mode can be toggled mid-loop from the banner; it takes effect on
the next relay.

### 7.6 User intervention

If the user types and sends a message directly into either paired session,
the loop **auto-pauses** and surfaces a banner notice:

> Paused — you sent a message into this session.
> [Resume] [Stop loop]

This keeps the human always-in-charge. The user can resume the loop after
their detour, or stop it if the manual intervention has taken the work in
a different direction.

If instead the user wants to permanently leave the loop, Stop yields
`manual`. The auto-pause-on-user-input behavior is non-optional; there is
no way to configure the loop to ignore user messages.

### 7.7 Inbox integration

Inbox rows for sessions that are part of an active pair show a small chain
icon next to their existing dot. Hovering reveals `paired with <partner>`.
This makes paired state visible at-a-glance from any Trellis page.

---

## 8. Persistence and Records

### 8.1 In-flight state and restart

Active pair state lives in an in-process registry (`internal/pair/`)
backed by per-pair JSON files on disk (§8.2). **The disk record is the
authoritative source of truth**; the in-memory registry is rebuilt from
disk on every server start. On every relay, settings change, pause,
resume, or stop, the corresponding record is updated atomically
(write-then-rename) **before** the in-memory state advances. This makes
the loop crash-safe — if the server dies between a successful relay and
the next decision, the record reflects the most recent committed step.

On server start, every pair whose `stopped_at` is empty is rehydrated:

- A pair last recorded as `running` resumes from its persisted step
  (`await_implementer`, `await_reviewer`, etc.) with its round count and
  settings intact. The driver re-arms the `session.state_changed`
  listener; if the partner side has already transitioned to `needs_you`
  while the server was down, the listener fires on the first state event
  after startup and the loop proceeds normally.
- A pair last recorded as `paused` stays paused. The user resumes
  manually from the banner.
- A pair whose participant session cannot be reconstructed on startup
  (its worktree was removed, or the session was permanently deleted) is
  stopped with reason `peer_error` and a flag
  `restart_unresolvable: true` on the record so the user can see what
  happened.

A pair record is removed from disk only when the user explicitly
discards a stopped pair, or when it is archived into a case (§10.1).

### 8.2 Per-pair record

A pair persists a small JSON record under
`~/.trellis/pairs/{pair-id}.json` with:

```
{
  "id": "...",
  "created_at": "...",
  "stopped_at": "...",
  "stop_reason": "lgtm",
  "state": "running",
  "step": "await_implementer",
  "round_count": 4,
  "last_persisted_at": "...",
  "implementer": { "agent": "claude", "worktree": "main", "session": "..." },
  "reviewer":    { "agent": "codex",  "worktree": "main", "session": "..." },
  "config": {
    "review_prompt": "...",
    "feedback_prompt": "...",
    "stop_signal": "LGTM",
    "max_rounds": 10,
    "confirm_before_relay": false
  },
  "config_history": [
    { "at": "...", "changed_fields": ["stop_signal"], "by": "user" },
    ...
  ],
  "rounds": [
    { "n": 1, "direction": "to_reviewer",    "at": "...", "source_message_id": "...", "delivered_message_id": "..." },
    { "n": 1, "direction": "to_implementer", "at": "...", "source_message_id": "...", "delivered_message_id": "..." },
    ...
  ]
}
```

- `state` mirrors the lifecycle enum from §3 (`pending`, `running`,
  `paused`, `stopped`).
- `step` is the current step-machine position (§5.1); only meaningful
  when `state` is `running` or `paused`. Empty for `pending` and
  `stopped`.
- `round_count` is the count used for the §6.2 cap and the banner
  display.
- `config_history` records each Settings dialog save (§7.4) so the audit
  trail shows when the prompt or stop signal was changed mid-loop. Each
  entry lists the fields that changed, not the values themselves — the
  full historical value can be reconstructed by walking back through the
  events (§8.3).

The record is created on pair creation and updated transactionally on
every state-affecting event. It survives restart (§8.1) and can be
linked from cases (§10.1).

### 8.3 Events

The pair driver publishes on the event bus:

- `pair.started` — `{pair_id, implementer, reviewer, config}`
- `pair.round` — `{pair_id, round_n, direction, source_message_id, delivered_message_id}`
- `pair.paused` / `pair.resumed` — `{pair_id, reason}`
- `pair.config_changed` — `{pair_id, changed_fields, new_config}`
- `pair.stopped` — `{pair_id, reason}`

These drive the banner, inbox indicators, and the live WebSocket pair view.

---

## 9. API

All under `/api/v1/pair`:

- `POST /api/v1/pair` — create a pair. Body:

  ```
  {
    "implementer": { "agent": "claude", "worktree": "main", "session_id": "..." },
    "reviewer":    { "agent": "codex",  "worktree": "main", "session_id": "..." },
    "review_prompt": "Review this. If it is good, reply with LGTM.",
    "feedback_prompt": "Feedback:",
    "stop_signal": "LGTM",
    "max_rounds": 10,
    "confirm_before_relay": false,
    "kickoff": "use_current" | "wait_for_next"
  }
  ```

  Returns the pair record. Validates: distinct sessions, neither already
  paired, neither trashed.

- `GET /api/v1/pair` — list active pairs (lightweight summary records).

- `GET /api/v1/pair/{id}` — full pair record including round history.

- `POST /api/v1/pair/{id}/pause`
- `POST /api/v1/pair/{id}/resume`
- `POST /api/v1/pair/{id}/stop`
- `POST /api/v1/pair/{id}/force-relay` — skip current debounce.
- `POST /api/v1/pair/{id}/confirm` — when in confirm mode, body
  `{action: "send"|"skip"|"stop", edited_text?: "..."}`.

- `GET /api/v1/pair/ws` — WebSocket emitting `pair.*` events for clients
  rendering the banner / inbox indicators. Optionally scoped by
  `?pair_id=...` or `?session_id=...`.

---

## 10. Interaction With Other Features

### 10.1 Cases

When either paired session is wrapped up (see [Cases](docs/pages/cases.md)),
the pair record id is included in the related-sessions list. If the partner
session is selected as a related session in the wrap-up modal, the pair
record is copied into the case directory under `pairs/{pair-id}.json` so
the full review history is archived alongside the transcripts.

### 10.2 Trashing a session

Trashing either paired session stops the loop with reason
`session_trashed`. The pair record is retained.

### 10.3 Forking

Forking a session that is currently in an active pair clones the
transcript but does **not** clone the pair membership; the fork starts
unpaired. The original session remains paired.

### 10.4 Moving a session to a new worktree

The move flow stops any active pair on that session with reason
`session_trashed` (since the underlying CLI process is restarted as part of
the move). The user can re-pair after the move completes.

---

## 11. Non-Goals (this version)

- **Config-file declared pairs** — pairs are ad hoc only.
- **Phase-checklist driver** — the multi-phase outer loop (Pattern 2 in
  brainstorming notes) is a separate feature built on top of pair loops.
- **Stateless reviewer / fresh session per round** — both sessions remain
  stateful for the duration of the loop. Users who want stateless review
  can stop the pair, reset the reviewer, and start a new pair.
- **Multi-reviewer fan-out** — exactly two participants.
- **Cross-Trellis pairs** — both sessions must live in the same Trellis
  instance.

---

## 12. Implementation Notes

### 12.1 Package layout

A new `internal/pair/` package containing:

- `pair.go` — `Pair` struct, state machine, validation.
- `driver.go` — the per-pair goroutine that runs the step machine,
  subscribes to `session.state_changed`, and dispatches relays through
  the existing `internal/claude` and `internal/codex` manager APIs.
- `registry.go` — process-wide registry of active pairs, indexed by id
  and by participating session id.
- `store.go` — persistence of pair records to `~/.trellis/pairs/`.
- `api.go` — HTTP handlers under `/api/v1/pair`.

### 12.2 Dispatch through existing managers

Pair relays go through whatever method the Claude/Codex managers already
expose for "send user message into session". The loop driver is **not**
allowed to touch CLI processes, transcripts, or worktrees directly — it
only orchestrates.

### 12.3 Idle subscription

Pair drivers subscribe to the same `session.state_changed` event bus the
inbox uses. No new transport. The driver filters by participant session
id and applies the 2s debounce in-process.

### 12.4 Frontend

- `static/js/pair_banner.js` — banner controller, attached on Claude and
  Codex session pages when `pair_id` is present in initial page data or
  arrives via `pair.started`.
- `static/js/pair_modal.js` — the create-pair modal.
- `static/js/pair_confirm_modal.js` — the edit-before-send modal.
- Inbox row chain indicator added to existing `inbox.js`.

### 12.5 Tests

- `internal/pair/pair_test.go` — state machine transitions,
  stop-signal matching including edge cases (word boundaries, casing,
  "LGTM with nit").
- `internal/pair/driver_test.go` — fake session managers + fake
  event bus driving the step machine through full loops; cover stop
  reasons, debounce, user-intervention auto-pause, max-rounds, kickoff
  modes.
- e2e: a real Claude×Codex pair running a 3-round loop on a trivial
  artifact.

---

## 13. Open Questions

1. **Should `Force relay` be available when not paired-but-impatient?**
   I.e., a "yes the session is really idle, relay now" button could be
   useful even outside paired loops. Out of scope here.
2. **Should the round counter survive `Pause` / `Resume`?** Spec assumes
   yes — pause/resume does not reset rounds. Confirm with practice.
3. **Should the user be able to swap roles mid-loop?** E.g., flip
   implementer ↔ reviewer without stopping. Spec says no (stop and
   re-pair). Revisit if there's demand.
