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

- **Needs you** — sessions waiting for something: a permission prompt (Claude), an approval request (Codex), or a turn that finished and is awaiting your next message.
- **Running** — sessions actively generating.

Each row carries:

- A coloured dot (yellow for "needs you", blue for "running").
- The session's display name and the worktree it lives in.
- A small `CLAUDE` or `CODEX` agent badge.
- A hide button (eye-with-slash) on hover.

Within each section, newer state transitions float to the top.

## Clicking a row

A click sends a `navigate` message over the inbox's WebSocket. The server forwards it to every main-window Trellis tab that's connected as `role=main` — those tabs then navigate themselves to `/{agent}/{worktree}/{session-id}`. The inbox window itself never leaves the popup.

If no main window is currently connected (e.g. you closed your last Trellis tab), the inbox falls back to opening the URL in a new `trellis-main` window.

Before navigating, each main tab pushes its current location onto the navigation history, so `Cmd+Backspace` (or your configured back binding) still works the way it does after any other navigation.

## Hide button

The eye-with-slash next to a row hides that row **until its state changes next**. Useful when you have a long-running build agent sitting in "running" that you don't want cluttering the view. As soon as the agent transitions (finishes, errors, asks for input), it pops back into the list.

Hides are stored in `localStorage` so they survive a popup reopen. They are automatically discarded when the recorded state no longer matches the live state.

## Live updates

The inbox subscribes to the `session.state_changed` event bus. Each event carries `{session_id, agent, worktree, display_name, state, trashed}`, and the row is updated (or removed) in place. The footer shows `live` when the WebSocket is open and `offline (reconnecting…)` while it's not — the client auto-reconnects every 3 seconds.

## State derivation

State is a coarse two-value field derived per agent:

- **Claude** — `running` while the session is generating *and* has no pending control request; otherwise `needs_you`.
- **Codex** — `running` while the session is generating *and* has no pending approvals; otherwise `needs_you`.

The aggregator merges both agents' session lists and tracks the timestamp of each session's most recent state transition, so the UI can sort by recency.

## API

The two endpoints behind the popup:

- `GET /api/v1/inbox/sessions` — initial merged list of `SessionRow{id, agent, worktree, display_name, state, last_state_change_at}` entries.
- `GET /api/v1/inbox/ws?role=inbox|main` — single WebSocket endpoint that serves two roles:
  - `role=inbox` — the popup connects this way. Receives `session.state_changed` events and can send `{type:"navigate", path:"..."}` commands.
  - `role=main` — every regular Trellis page connects this way (via `inbox_main_ws.js` loaded by the shared header). Receives `{type:"navigate", path:"..."}` commands and acts on them.

If the inbox sends a `navigate` and there's no `role=main` connection at all, the server replies with `{type:"navigate_failed", reason:"no_main_window", path:"..."}` and the popup opens a window directly.

## Related

- [Claude Page](/docs/pages/claude/) — what the inbox row points at for Claude sessions
- [Cases](/docs/pages/cases/) — sessions get exported into cases at wrap-up time
