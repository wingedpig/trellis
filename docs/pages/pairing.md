---
title: "Pair Review & Checklist Runs"
weight: 8
---

# Pair Review & Checklist Runs

Trellis can wire two AI sessions together so they review each other's work without you relaying messages by hand. There are two levels:

- **Paired review loop** — one implementer session and one reviewer session iterate on a single piece of work until the reviewer approves it.
- **Checklist run** — an outer loop that drives a pair through a multi-phase plan: the implementer completes one phase, a paired review loop reviews it, and the run advances to the next phase until the whole checklist is done.

Both are ad hoc: you create them on the fly between any two live sessions — Claude or Codex, in any combination, in the same or different worktrees. No configuration file changes are needed.

## Paired Review Loop

### What it does

A pair has two fixed roles:

- **Implementer** — produces or revises the work, and reacts to reviewer feedback.
- **Reviewer** — critiques each iteration and signals convergence by replying with a stop signal (default `LGTM`) on a line of its own.

Once running, the loop watches the implementer session. When it finishes a turn and goes idle, the loop captures its last assistant message, prefixes it with your review prompt, and sends it to the reviewer. The reviewer's critique is prefixed with your feedback prompt and relayed back. This repeats until the reviewer emits the stop signal, the round cap is hit, or you intervene.

### Starting a pair

On a Claude or Codex session page, open the **⋮ More actions** menu next to the input box and choose **Pair for review**. The modal asks for:

| Field | Default | Meaning |
|-------|---------|---------|
| Role / Swap | This session = Implementer | Which side the current session plays. **Swap** flips the roles. |
| Partner session | — | Any other live session (Claude or Codex, any worktree). |
| Review prompt | `Review this. If it is good, reply with LGTM on its own line.` | Prefixed to each implementer→reviewer relay. |
| Feedback prompt | `Feedback:` | Prefixed to each reviewer→implementer relay. May be empty. |
| Stop signal | `LGTM` | Ends the loop when the reviewer puts it on a line by itself (case-insensitive). `LGTM with one nit:` does **not** match — context around a signal-only line is fine. |
| Max rounds | 10 | Cap on relays before the loop stops unconverged. |
| Kickoff | Wait for implementer's next turn | Or **Use implementer's current last message** to relay the existing last message immediately as round 1. |
| Confirm before each relay | off | Review and edit every outbound message before it is sent (see below). |

Your settings are remembered as defaults for the next pair.

### The pair banner

While a session is in an active pair, an amber banner appears at the top of its page: partner, your role, round count, and the current step (waiting for a side, relaying, paused). Controls:

- **Pause / Resume** — suspend and resume the loop.
- **Stop** — end the loop (the record is kept for audit).
- **Force relay** — capture and relay the current message immediately instead of waiting for idle detection.
- **Settings** — edit prompts, stop signal, round cap, and confirm-mode mid-loop; changes apply on the next relay. Partner and roles are fixed.
- **Review pending relay…** — appears in confirm mode when a relay is waiting for you; opens an editor where you can edit, send, or skip the message, or stop the loop.

When a relay lands, the browser follows the action: if the receiving session is a different one, Trellis navigates to it so you're always watching the side that is generating.

### Staying in control

- **Typing into a paired session pauses the loop.** If you send a message directly to either side mid-loop, the pair auto-pauses so your conversation and the loop don't interleave. Resume it from the banner when you're done.
- A pair stops automatically if a participant session errors out or is trashed.
- Active pairs survive Trellis restarts — state is persisted after every step and rehydrated on startup.

## Checklist Runs

### What it does

A checklist run automates working through a phased plan — for example a `CHECKLIST.md` or a spec with `## Phase N` sections. The run is checklist-agnostic: **the implementer owns and tracks the checklist file**; Trellis never parses it. The run just pumps the loop:

1. The implementer implements one phase.
2. An inner paired review loop reviews that phase until the reviewer approves it (`LGTM`).
3. The run sends the advance prompt ("implement the next phase…") and the cycle repeats.
4. When no phases remain, the implementer replies with the completion signal (default `COMPLETED`) alone on a line, and the run stops as completed.

Both sessions keep their full context for the whole run — the implementer carries knowledge between phases, and the reviewer can catch a later phase breaking an earlier one.

### Starting a run

Ask the implementer session to work from a checklist file (or write one first — a numbered/phased plan checked into the worktree works well). Then open the **⋮ More actions** menu and choose **Start checklist run**. The modal collects:

| Field | Default | Meaning |
|-------|---------|---------|
| Role / Swap, Partner | This session = Implementer | Same as pairing. |
| Advance prompt | `Implement the next phase from the checklist…` | Sent to the implementer to start each phase after the first. Must tell it to reply with the completion signal when nothing is left. |
| Completion signal | `COMPLETED` | The implementer's "no phases left" sentinel — matched only as a reply that is exactly this one line. |
| Review prompt | `Review the implementer's work on the current phase…` | Used by each phase's review pair. |
| Feedback prompt | `Feedback:` | Used by each phase's review pair. |
| Review stop signal | `LGTM` | The **reviewer's** approval word — distinct from the completion signal. |
| Max rounds per phase | 10 | If a phase's review hits this cap without approval, the run pauses for you. |

If you change the completion signal or review stop signal, update the corresponding prompt to name the new word — the prompts are sent verbatim.

**Starting a run does not prompt the implementer.** The implementer's current (or next) turn is treated as the first phase, so you kick off phase 1 yourself — typically by sending "implement the first phase of docs/my-checklist.md" — and the run takes over from there.

### The run banner

Sessions in a run show a blue banner: partner, your role, the current phase number, its status (implementing, under review, starting next phase), and how many phases are done. Controls:

- **Pause / Resume** — suspend the run (pausing mid-review also pauses the inner pair).
- **Skip phase** — abandon the current phase and advance.
- **Retry phase** — re-run review on the implementer's current output with a fresh round counter (offered while paused).
- **Stop** — end the run.

During a phase's review the checklist banner reports the state; the inner pair's banner only appears when it needs your attention (paused, or waiting on a relay confirmation).

### When a phase doesn't converge

If a phase's review hits the round cap without an approval, the run **pauses instead of advancing** — it never moves past unreviewed work. From the paused banner you choose: Retry, Skip, or Stop. Every attempt is recorded in the run's phase history for audit.

Runs persist to disk after every step and are rehydrated on server restart: a running phase re-attaches to its review pair, and a paused run stays paused until you resume it.

## API

Pairing lives under `/api/v1/pair` and checklist runs under `/api/v1/checklist` — creation, listing, per-id control actions (`pause`, `resume`, `stop`, and for runs `skip` / `retry`), and read-only WebSockets for live updates. The full specifications, including the persisted record formats, are in [PAIRING_SPEC.md](https://github.com/wingedpig/trellis/blob/main/PAIRING_SPEC.md) and [PHASE_LOOP_SPEC.md](https://github.com/wingedpig/trellis/blob/main/PHASE_LOOP_SPEC.md) in the repository.

## Related

- [Claude Page](/docs/pages/claude/) — the session interface both features build on
- [Session Inbox](/docs/pages/inbox/) — watch every session's live state while a loop runs
