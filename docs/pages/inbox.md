---
title: "Session Inbox"
weight: 4
---

# Session Inbox

**URL:** `/inbox` (opened as a small popup window, not a regular tab)

The session inbox is a chromeless floating window that lists every active Claude and Codex session across every worktree, with a live state badge. Click a row and the foreground Trellis window jumps to that session — without leaving whatever screen you were on to scroll through worktrees.

![The session inbox: stalled, errored, and waiting sessions on top, running sessions with live activity below](/docs/images/session-inbox.png)

## Opening the inbox

Click the inbox icon in the top-right of any Trellis page header, or press **Cmd/Ctrl + I**. The first invocation opens the popup; subsequent invocations reuse the same window.

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

Clicking a row makes your main Trellis window — not the popup — jump to that session. The popup stays where it is, so you can keep triaging. If you have no Trellis tab open at all, a new one is opened for you.

The jump is recorded in navigation history, so `Cmd+Backspace` (or your configured back binding) takes you back the same way it does after any other navigation.

## Hide button

The eye-with-slash next to a row hides that row **until its state changes next**. Useful when you have a long-running build agent sitting in "running" that you don't want cluttering the view. As soon as the agent transitions (finishes, errors, asks for input), it pops back into the list. Hides survive closing and reopening the popup.

## Live updates

Rows update in real time: state badges flip the moment a session starts or stops needing you, and the activity line refreshes at each tool/step boundary without reordering the list. The footer shows `live` while the connection is up and `offline (reconnecting…)` while it's not — the popup reconnects automatically. Time-in-state labels tick over client-side every 30 seconds with no extra server traffic.

## Under the hood

Mechanics you don't need day-to-day:

- **State model** — `state` is a coarse two-value field derived per agent: Claude is `running` while generating with no pending control request, Codex while generating with no pending approvals; otherwise `needs_you`. `state` drives sorting and transition detection — it flips only on a real `running ↔ needs_you` change. A finer `reason` field (`running`, `awaiting_input`, `needs_approval`, `error`) refines presentation only and never reorders the list. The aggregator merges both agents' session lists and timestamps each session's most recent transition so the UI can sort by recency.
- **Event streams** — the popup subscribes to `session.state_changed` (fired only on coarse `running ↔ needs_you` transitions, carrying `{session_id, agent, worktree, display_name, state, reason, unread, trashed, archived}` — a row leaves the inbox when either `trashed` or `archived` is true) and `session.activity` (a lighter stream carrying `{session_id, activity}`, fired at tool/step boundaries — not per token — and only when the description changes).
- **Navigation** — `GET /api/v1/inbox/ws?role=inbox|main` is a single WebSocket endpoint serving two roles: the popup connects as `role=inbox`; every regular Trellis page connects as `role=main` (via `inbox_main_ws.js` in the shared header). A row click sends `{type:"navigate", path:"..."}`, which the server forwards to every main-window connection. If no main window is connected, the server replies `{type:"navigate_failed", reason:"no_main_window"}` and the popup opens a new `trellis-main` window directly.
- **Initial list** — `GET /api/v1/inbox/sessions` returns the merged list of `SessionRow{id, agent, worktree, display_name, state, reason, activity, unread, last_state_change_at}` entries.
- **Hides** — stored in `localStorage` with the state they were hidden in; a hide is discarded as soon as the recorded state no longer matches the live one.

## Related

- [Claude Page](/docs/pages/claude/) — what the inbox row points at for Claude sessions
- [Cases](/docs/pages/cases/) — sessions get exported into cases at wrap-up time
