---
title: "Events Page"
weight: 3
---

# Events Page

**URL:** `/events`

The Events page displays a chronological timeline of system events. Use it to understand what has happened in your development environment.

## Event Types

Events are color-coded by type:

| Event Type | Color | Description |
|------------|-------|-------------|
| `service.started` | Green | A service started successfully |
| `service.restarted` | Green | A service was restarted (binary changed or manual restart) |
| `service.stopped` | Gray | A service was stopped |
| `service.crashed` | Red | A service exited unexpectedly |
| `workflow.started` | Gray | A workflow began execution |
| `workflow.finished` | Blue | A workflow completed |
| `worktree.activated` | Blue | The active worktree was changed |

## Event Details

Each event shows:

- **Time** — When the event occurred
- **Type** — The event type (with color-coded badge)
- **Worktree** — Which worktree the event occurred in
- **Details** — Additional context (service name, exit code, duration, etc.)

## Event Retention

Events are kept in memory and reset when Trellis restarts. The page shows the most recent events first (scrolled to bottom).

## Real-Time Updates

Click **Refresh** to reload the event list. For real-time event streaming, use the WebSocket API or subscribe via the event bus.

## Related

- [API: Events](/api/#tag/Events) — Event API endpoints
- [Go Client: Events](/docs/reference/client/#events) — Programmatic event access
