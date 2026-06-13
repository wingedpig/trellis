---
title: "Session Inbox"
weight: 11
---

# Session Inbox

**URL:** `/inbox` (opened as a small popup window, not a regular tab)

The session inbox is a chromeless floating window that lists every active Claude and Codex session across every worktree, with a live state badge. Click a row and the foreground Trellis window jumps to that session — without leaving whatever screen you were on to scroll through worktrees.

## Opening the inbox

Click the inbox icon in the top-right of any Trellis page header, or press **Cmd/Ctrl + I**. The first invocation opens the popup; subsequent invocations reuse the same window (`window.open(..., "trellis-inbox", ...)`).

The popup is sized to roughly 420×720 — small enough to dock alongside an editor or browser, large enough to hold a useful list.

## What it shows

Two stacked sections:

- **Needs you** — sessions waiting for something: a permission prompt (Claude), an approval request (Codex), a turn that finished and is awaiting your next message, or a turn that ended in an error.
- **Running** — sessions actively generating.

Each row carries:

- A **status indicator** that reflects the row's reason:
  - a blue dot while **running**,
  - a yellow dot while **awaiting your input**,
  - a pulsing red hand when the agent is **stalled on a permission/approval prompt**,
  - a red warning triangle when the **last turn errored**.
- The session's display name.
- A second line showing the worktree and — while running — a **live activity description** of what the agent is doing right now (`Running go test`, `Editing schema.go`, `Thinking…`). It updates in place at each tool/step boundary.
- A **time-in-state** label (`4m`, `2h`) showing how long the session has sat in its current state.
- A small `CLAUDE` or `CODEX` agent badge.
- A hide button (eye-with-slash) on hover.

Within **Running**, newer state transitions float to the top. Within **Needs you**, the most urgent reason comes first — stalled approvals, then errors, then turns merely awaiting input — with ties broken by most-recent transition.

## Clicking a row

A click sends a `navigate` message over the inbox's WebSocket. The server forwards it to every main-window Trellis tab that's connected as `role=main` — those tabs then navigate themselves to `/{agent}/{worktree}/{session-id}`. The inbox window itself never leaves the popup.

If no main window is currently connected (e.g. you closed your last Trellis tab), the inbox falls back to opening the URL in a new `trellis-main` window.

Before navigating, each main tab pushes its current location onto the navigation history, so `Cmd+Backspace` (or your configured back binding) still works the way it does after any other navigation.

## Hide button

The eye-with-slash next to a row hides that row **until its state changes next**. Useful when you have a long-running build agent sitting in "running" that you don't want cluttering the view. As soon as the agent transitions (finishes, errors, asks for input), it pops back into the list.

Hides are stored in `localStorage` so they survive a popup reopen. They are automatically discarded when the recorded state no longer matches the live state.

## Live updates

The inbox subscribes to two event streams:

- `session.state_changed` — fired only on coarse `running ↔ needs_you` transitions. Each event carries `{session_id, agent, worktree, display_name, state, reason, unread, trashed}`, and the row is updated (or removed) in place.
- `session.activity` — a lighter stream carrying `{session_id, activity}`, fired at tool/step boundaries (not on every token) and only when the description changes. It refreshes a row's activity line **in place without reordering** the list.

The footer shows `live` when the WebSocket is open and `offline (reconnecting…)` while it's not — the client auto-reconnects every 3 seconds. Time-in-state labels are refreshed client-side on a 30-second tick, with no extra server traffic.

## State derivation

`state` stays a coarse two-value field, derived per agent:

- **Claude** — `running` while the session is generating *and* has no pending control request; otherwise `needs_you`.
- **Codex** — `running` while the session is generating *and* has no pending approvals; otherwise `needs_you`.

`state` is what drives sorting and transition detection — it flips only on a real `running ↔ needs_you` change. A finer **`reason`** field refines it for presentation only, and never reorders the list:

| `reason` | meaning |
|----------|---------|
| `running` | a turn is actively in flight |
| `awaiting_input` | the turn finished cleanly; your move |
| `needs_approval` | stalled on a permission/approval prompt |
| `error` | the last turn ended in an error |

The aggregator merges both agents' session lists and tracks the timestamp of each session's most recent state transition, so the UI can sort by recency.

## API

The two endpoints behind the popup:

- `GET /api/v1/inbox/sessions` — initial merged list of `SessionRow{id, agent, worktree, display_name, state, reason, activity, unread, last_state_change_at}` entries.
- `GET /api/v1/inbox/ws?role=inbox|main` — single WebSocket endpoint that serves two roles:
  - `role=inbox` — the popup connects this way. Receives `session.state_changed` events (forwarded as `{type:"state_changed"}`) and `session.activity` events (forwarded as `{type:"activity"}`), and can send `{type:"navigate", path:"..."}` commands.
  - `role=main` — every regular Trellis page connects this way (via `inbox_main_ws.js` loaded by the shared header). Receives `{type:"navigate", path:"..."}` commands and acts on them.

If the inbox sends a `navigate` and there's no `role=main` connection at all, the server replies with `{type:"navigate_failed", reason:"no_main_window", path:"..."}` and the popup opens a window directly.

## Related

- [Claude Page](/docs/pages/claude/) — what the inbox row points at for Claude sessions
- [Cases](/docs/pages/cases/) — sessions get exported into cases at wrap-up time
