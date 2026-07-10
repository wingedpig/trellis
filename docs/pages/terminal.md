---
title: "Terminal Page"
weight: 0
---

# Terminal Page

**URL:** `/terminal`

The Terminal page is the main view in Trellis. It provides access to terminals, service logs, log viewers, and remote sessions—all switchable via the navigation picker.

## Navigation Picker

Press **Cmd+P** (or **Ctrl+P**) to open the navigation picker. Items are prefixed to indicate their type, and each type has a distinct icon in the dropdown:

| Prefix | Icon | Type | Example |
|--------|------|------|---------|
| `@` | terminal | Local terminal | `@main - dev` |
| `@` | robot | Claude session | `@main - Session 1` |
| `@` | folder-tree | Worktree home | `@main - Home` |
| `#` | status dot | Service logs | `#api` |
| `~` | file-lines | Log viewer | `~production-logs` |
| `!` | terminal | Remote window | `!admin` |

Claude sessions and local terminals both use the `@` prefix but are visually distinguished by their icons. You can also access pages, links, and other items through the picker.

## History Picker

Press **Cmd+Backspace** to open the history picker, which shows your recently visited views in order. This lets you quickly toggle between two views.

## Links Panel

Press **Cmd/Ctrl+K** to open a standalone panel listing every configured link on its own — the same `terminal.links` entries the navigation picker interleaves with terminals and services, but shown together so you can scan and click them without filtering the picker. Click a link to open it; each link reuses a named browser tab, so re-opening the same link focuses its existing tab instead of piling up duplicates.

The shortcut is active only when links are configured (otherwise `Cmd/Ctrl+K` falls through to the browser). The panel also appears as **Open links panel** in the Commands &amp; Shortcuts menu (`Cmd/Ctrl+H`). Define links under `terminal.links` in your config:

```hjson
terminal: {
  links: [
    { name: "Grafana", url: "https://grafana.example.com" }
    { name: "Docs", url: "https://docs.example.com" }
  ]
}
```

---

## Local Terminals (@)

Local terminals connect to tmux windows in your project's tmux session. Each worktree has its own tmux session.

**Configuration:** Terminals are created on demand from the worktree home page. Configure tmux settings (history limit, default shell) in [`terminal.tmux`](/docs/reference/config/#terminal).

**Features:**
- Full terminal emulation via xterm.js
- Automatic tmux session creation
- Multiple windows per worktree
- Copy/paste support
- Keyboard shortcuts passed through to tmux

**Connection:** Terminals use WebSocket connections that automatically reconnect if interrupted. Terminals stay connected even when viewing other items—switching back is instant.

---

## Service Logs (#)

Service logs show the stdout/stderr output from your configured services.

**Configuration:** Each service log corresponds to an entry in the [`services`](/docs/reference/config/#services) array. Service logging behavior is controlled by the `logging` field within each service definition (parser type, field extraction, etc.).

**Features:**
- Real-time log streaming
- Structured log parsing (JSON, logfmt, etc.)
- Filtering by level, field values, or text patterns
- Entry details panel with field inspection
- Following mode (auto-scroll) or manual scroll

**Filtering syntax:**
- `level:error` — Filter by log level
- `msg:~"timeout"` — Filter messages containing "timeout"
- `field:value` — Filter by any parsed field
- Multiple terms are AND'd together

**Connection:** Service logs use HTTP polling (1 second interval). Polling stops when you switch to a different view, and resumes when you return.

---

## Log Viewers (~)

Log viewers stream logs from configured sources—local files, remote SSH connections, or custom commands.

**Configuration:** Each log viewer is defined in the [`log_viewers`](/docs/reference/config/#log_viewers) array. Configure the source type (`file`, `ssh`, `command`), connection details, and logging options (parser, fields, history settings). The `mode` field controls whether the viewer opens tailing (`live`, the default) or paused with a static snapshot for search/scrollback (`explore`) — see [Viewer Modes](/docs/concepts/logging/#viewer-modes-live-vs-explore).

**Features:**
- Real-time streaming via WebSocket
- Structured log parsing
- Filtering (same syntax as service logs)
- Entry details panel
- Following/paused modes, with automatic pausing on high-volume streams
- History search for past log entries

**Live vs. Explore:** `live` viewers (the default) start tailing the source as soon as you open them and follow new entries. `explore` viewers — meant for high-volume logs like nginx access logs — open paused with the last ~200 lines loaded from the end of the file, so search and scrollback are the primary workflow. Click **Go live** in the header to start the tail and switch to streaming.

**Auto-pause:** If a followed viewer streams faster than the configured `auto_pause_rate` (default 30 lines/sec), it automatically drops out of following and shows a "High volume (≈N lines/s) — following paused" banner. New lines keep being counted while paused; click the **+N new lines** indicator or the **Following** button to resume.

**Pausing is lossless:** whenever a viewer is paused — by scrolling up, by auto-pause, or in `explore` mode before going live — the server stops sending individual log lines and instead sends a small stats update every couple of seconds (count of missed lines and current rate). Resuming replays the missed lines from the server's in-memory buffer (up to 2000 entries); if more were missed, the newest 2000 are shown with a "N lines skipped while paused" divider, and history search can still find the rest. Entries otherwise stream in small batches (roughly every 150ms) rather than one message per line, and the view keeps about the last 4000 rows in the browser, trimming the oldest as new ones arrive; scrolling up past what's rendered reloads older rows from the server's buffer.

**History Search:**
Click the clock icon to search historical logs. Specify a time range and grep pattern to find past entries.

**Connection:** Log viewers use WebSocket connections. The connection is closed when you switch to a different view. When you return to the same log viewer, it reconnects and resumes streaming. The underlying tail keeps running briefly after the last viewer disconnects (`disconnect_grace`, default 30s) so quickly switching back and forth doesn't restart it; after the grace period with no watchers, the tail is stopped.

---

## Remote Windows (!)

Remote windows provide SSH terminal access to remote servers configured in your `trellis.hjson`.

**Configuration:** Each remote window is defined in the [`terminal.remote_windows`](/docs/reference/config/#terminal) array. Specify the `name`, SSH `host`, and optional `command` to run on connection.

**Features:**
- Full terminal emulation
- SSH connection management
- Same keyboard shortcuts as local terminals

**Connection:** Remote windows use WebSocket connections with automatic reconnection, similar to local terminals.

---

## Other Picker Items

The navigation picker also includes:

- **Output** (`@worktree - output`) — Workflow execution output
- **Editor** (`@worktree - editor`) — Opens VS Code for the worktree
- **Pages** — Links to /worktrees, /status, /events, /crashes, /trace
- **Links** — Configured external URLs (open in new window/tab)

## Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| Cmd/Ctrl+P | Open navigation picker |
| Cmd/Ctrl+Backspace | Open history picker |
| Cmd/Ctrl+K | Open links panel (when links are configured) |
| Cmd/Ctrl+E | Toggle editor (VS Code) |
| Cmd/Ctrl+L | Jump to service log |
| Escape | Close picker / exit current mode |

See [Keyboard Shortcuts](/docs/reference/shortcuts/) for the complete list.
