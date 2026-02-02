---
title: "Terminal Page"
weight: 0
---

# Terminal Page

**URL:** `/terminal`

The Terminal page is the main view in Trellis. It provides access to terminals, service logs, log viewers, and remote sessions—all switchable via the navigation picker.

## Navigation Picker

Press **Cmd+P** (or **Ctrl+P**) to open the navigation picker. Items are prefixed to indicate their type:

| Prefix | Type | Example |
|--------|------|---------|
| `@` | Local terminal | `@main - dev` |
| `#` | Service logs | `#api` |
| `~` | Log viewer | `~production-logs` |
| `!` | Remote window | `!admin` |

You can also access pages, links, and other items through the picker.

## History Picker

Press **Cmd+Backspace** to open the history picker, which shows your recently visited views in order. This lets you quickly toggle between two views.

---

## Local Terminals (@)

Local terminals connect to tmux windows in your project's tmux session. Each worktree has its own tmux session.

**Configuration:** Define default terminal windows in [`terminal.default_windows`](/docs/reference/config/#terminal). Configure tmux settings (history limit, default shell) in [`terminal.tmux`](/docs/reference/config/#terminal).

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

**Configuration:** Each log viewer is defined in the [`log_viewers`](/docs/reference/config/#log_viewers) array. Configure the source type (`file`, `ssh`, `command`), connection details, and logging options (parser, fields, history settings).

**Features:**
- Real-time streaming via WebSocket
- Structured log parsing
- Filtering (same syntax as service logs)
- Entry details panel
- Following/paused modes
- History search for past log entries

**History Search:**
Click the clock icon to search historical logs. Specify a time range and grep pattern to find past entries.

**Connection:** Log viewers use WebSocket connections. The connection is closed when you switch to a different view. When you return to the same log viewer, it reconnects and resumes streaming.

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
| Cmd/Ctrl+E | Toggle editor (VS Code) |
| Cmd/Ctrl+L | Jump to service log |
| Escape | Close picker / exit current mode |

See [Keyboard Shortcuts](/docs/reference/shortcuts/) for the complete list.
