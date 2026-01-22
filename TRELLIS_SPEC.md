# Trellis Specification

**Version:** 1.0-draft
**Status:** Specification for Implementation

---

## Table of Contents

1. [Overview](#1-overview)
2. [Architecture](#2-architecture)
3. [Configuration Format](#3-configuration-format)
4. [Service Management](#4-service-management)
5. [Binary Watching](#5-binary-watching)
6. [Event Bus](#6-event-bus)
7. [Worktree Management](#7-worktree-management)
8. [Crash Reports](#8-crash-reports)
9. [Terminal System](#9-terminal-system)
10. [Log Viewers](#10-log-viewers)
11. [Distributed Tracing](#11-distributed-tracing)
12. [Web Interface](#12-web-interface)
13. [API](#13-api)
14. [Workflows](#14-workflows)
15. [Observability](#15-observability)
16. [Security](#16-security)
17. [Implementation Guide](#17-implementation-guide)
18. [CLI Tool (trellis-ctl)](#18-cli-tool-trellis-ctl)

---

## 1. Overview

### 1.1 What is Trellis?

Trellis is a development environment orchestrator that manages microservice ecosystems during local development. It provides:

- **Service orchestration**: Start, stop, restart, and monitor multiple services
- **Automatic restarts**: Watch binaries and restart services on changes
- **Crash history**: Persistent crash records with full context for debugging
- **Worktree support**: Parallel development across git worktrees
- **Unified terminal**: Browser-based terminal access to all services
- **Event-driven architecture**: Extensible event bus for integrations

### 1.2 Design Principles

1. **Configuration over code**: All behavior defined in HJSON config files
2. **Worktree-scoped**: Each worktree is an isolated development context
3. **Crash visibility**: Persistent crash history with full debugging context
4. **Event-driven**: All state changes emit events for extensibility
5. **Fail-open**: Individual failures don't cascade

### 1.3 Key Concepts

| Concept | Description |
|---------|-------------|
| **Project** | A Trellis-managed codebase with a `trellis.hjson` config |
| **Worktree** | A git worktree representing an isolated development branch |
| **Service** | A long-running process managed by Trellis |
| **Workflow** | A user-triggered action (build, test, deploy, etc.) |
| **Event** | An immutable record of something that happened |
| **Crash** | A recorded service failure with full debugging context |

---

## 2. Architecture

### 2.1 Component Diagram

```
                              ┌─────────────┐
                              │ trellis-ctl │ (CLI client)
                              └──────┬──────┘
                                     │ HTTP API
                                     ▼
┌─────────────────────────────────────────────────────────────────┐
│                        Trellis Server                           │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │   Config    │  │   Event     │  │    Service Manager      │  │
│  │   Loader    │──│    Bus      │──│  (start/stop/restart)   │  │
│  └─────────────┘  └──────┬──────┘  └─────────────────────────┘  │
│                          │                                       │
│  ┌─────────────┐  ┌──────┴──────┐  ┌─────────────────────────┐  │
│  │  Worktree   │  │  Subscribers │  │    Binary Watcher       │  │
│  │  Manager    │  │  (context,   │  │  (restarts on change)   │  │
│  └─────────────┘  │   UI, logs)  │  └─────────────────────────┘  │
│                   └─────────────┘                                │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │  Terminal   │  │  Workflow   │  │    Crash                │  │
│  │  Manager    │  │  Runner     │  │    Manager              │  │
│  └─────────────┘  └─────────────┘  └─────────────────────────┘  │
├─────────────────────────────────────────────────────────────────┤
│                         HTTP Server                              │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐ │
│  │  Web UI  │  │   API    │  │WebSocket │  │  Static Files    │ │
│  └──────────┘  └──────────┘  └──────────┘  └──────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 Data Flow

```
Binary Change ──► Watcher ──► Event Bus ──► Restart Service
                                   │
                                   ├──► Record Crash (if crashed)
                                   ├──► Push to UI
                                   └──► Log/Metrics
```

### 2.3 Process Model

- **Trellis server**: Single long-running process
- **Services**: Child processes managed by Trellis
- **Terminals**: tmux sessions, one per worktree
- **trellis-ctl**: Standalone CLI that communicates with server via HTTP API

---

## 3. Configuration Format

Trellis uses HJSON (Human JSON) for configuration. HJSON allows comments, unquoted keys, trailing commas, and multiline strings.

### 3.1 Configuration File Location

Trellis looks for configuration in this order:
1. Path specified via `--config` flag
2. `./trellis.hjson` in current directory
3. `./trellis.json` in current directory

### 3.2 Root Configuration Schema

```hjson
{
  // Schema version for compatibility checking
  version: "1.0"

  // Project metadata
  project: {
    name: "my-project"
    description: "My microservices project"
  }

  // Trellis server configuration
  server: {
    port: 1000
    host: "127.0.0.1"

    // Optional: Enable HTTPS (see section 15.2 for certificate generation)
    // tls_cert: "~/.trellis/cert.pem"
    // tls_key: "~/.trellis/key.pem"
  }

  // Worktree configuration
  worktree: {
    // ... see section 7
  }

  // Service definitions
  services: [
    // ... see section 4
  ]

  // Workflow definitions
  workflows: [
    // ... see section 12
  ]

  // Crash history configuration
  crashes: {
    // ... see section 8
  }

  // Terminal configuration
  terminal: {
    // ... see section 9
  }

  // Event bus configuration
  events: {
    // ... see section 6
  }

  // UI configuration
  ui: {
    // ... see section 10
  }

  // Global watch configuration (binary watching)
  watch: {
    // ... see section 5
  }

  // Logging configuration
  logging: {
    // ... see section 13
  }
}
```

### 3.3 Template Variables

Configuration values can use template variables:

| Variable | Description | Example |
|----------|-------------|---------|
| `{{.Worktree.Root}}` | Worktree root path | `/home/user/project` |
| `{{.Worktree.Name}}` | Worktree name | `feature-auth` |
| `{{.Worktree.Branch}}` | Git branch name | `feature/auth` |
| `{{.Worktree.Binaries}}` | Expanded value of `worktree.binaries.path` | `/home/user/bin/myproject` |
| `{{.Project.Root}}` | Main project root | `/home/user/project` |
| `{{.Project.Name}}` | Project name | `my-project` |
| `{{.Service.Name}}` | Current service name | `api` |

Templates use Go's `text/template` syntax with additional functions:

```hjson
{
  command: "{{.Worktree.Root}}/bin/{{.Service.Name}}"
  args: ["-config", "{{.Worktree.Root}}/config/{{.Service.Name}}_conf.json"]
}
```

---

## 4. Service Management

### 4.1 Service Definition Schema

```hjson
{
  services: [
    {
      // Required: unique identifier
      name: "api"

      // Required: command to run
      // Can be a string or array:
      //   String: Split on whitespace (no shell quoting/escaping honored),
      //           templates expanded first.
      //           "{{.Worktree.Root}}/bin/api -config /etc/api.json"
      //   Array:  Each element is a separate argument, templates expanded per-element
      //           ["{{.Worktree.Root}}/bin/api", "-config", "/etc/api.json"]
      // Use array form when arguments contain spaces or special characters.
      // For shell features (pipes, redirects), use: ["sh", "-c", "command | other"]
      //   Note: When using sh -c, set watch_binary explicitly since the first
      //   token would be "sh" which is not the binary you want to watch.
      // See section 14.2 for template escaping rules.
      command: "{{.Worktree.Root}}/bin/api"

      // Optional: command arguments (appended to command if command is string)
      // Ignored if command is already an array.
      args: ["-config", "/etc/api/config.json"]

      // Optional: restart policy
      restart: {
        policy: "on_failure"  // "always", "on_failure", "never"
      }

      // Optional: disable watching this service's binary (default: true)
      watching: false

      // Optional: override which binary to watch (default: extracted from command)
      watch_binary: "{{.Worktree.Binaries}}/actual-binary"

      // Optional: additional files to watch for changes (e.g., config files)
      // When any of these files change, the service is restarted
      watch_files: [
        "{{.Worktree.Root}}/config/api.yaml",
        "{{.Worktree.Root}}/config/database.yaml"
      ]

      // Optional: debug wrapper command (prepended to command)
      // When set, service runs under debugger
      debug: "dlv exec --headless --listen=:2345 --api-version=2 --accept-multiclient --"

      // Optional: logging configuration for structured log viewing
      // When parser is configured, service logs show in a table format
      // with filtering, column display, and entry expansion (like log_viewers).
      // See section 10 for detailed parser/display options.
      logging: {
        buffer_size: 50000     // Lines to keep in memory (default: 50000)

        // Parser configuration (enables structured log display)
        // If not specified, inherits from logging_defaults.parser
        parser: {
          type: "json"         // "json", "logfmt", or "none"
          timestamp: "time"    // Required: field containing timestamp
          // level: "level"    // Optional: enables row color coding
          // id: "trace_id"    // Optional: field for trace ID (used by crashes and trace)
        }

        // Derived fields - computed from parsed fields
        derive: {
          ts_short: {
            from: "time"
            op: "timefmt"
            args: { format: "15:04:05.000" }
          }
        }

        // Layout configuration - columns to display (in order)
        layout: [
          { field: "ts_short", max_width: 12, timestamp: true }
          { field: "level", max_width: 6 }
          { field: "host", max_width: 15 }
          { field: "msg" }
        ]
      }

      // Optional: disable service (skip during start)
      enabled: true
    }
  ]
}
```

### 4.2 Service Lifecycle States

```
               ┌──────────────────────┐
               │                      │
               ▼
┌─────────┐         ┌─────────┐         ┌─────────┐
│ Stopped │────────►│ Running │────────►│ Crashed │
└─────────┘         └─────────┘         └────┬────┘
     ▲                   ▲                   │
     └───────────────────┴───────────────────┘
```

| State | Description |
|-------|-------------|
| `stopped` | Not running (initial state, or intentionally stopped) |
| `running` | Process is running |
| `crashed` | Exited unexpectedly (may auto-restart based on restart policy) |

### 4.3 Service Commands

Trellis provides these service operations:

| Operation | Description |
|-----------|-------------|
| `start` | Start a stopped service |
| `stop` | Stop a running service (SIGTERM, then SIGKILL) |
| `restart` | Stop then start |
| `logs` | Stream service logs |
| `status` | Get current state |

---

## 5. Binary Watching

Trellis watches compiled binaries and restarts services when they change. Builds are external (user, IDE, or `mage`/`make`). This approach is simple, works with any build system, and integrates naturally with IDE build-on-save workflows.

Trellis watches the service binary and restarts when it changes:

```hjson
{
  services: [
    {
      name: "api"
      // Binary path - Trellis watches this file automatically
      command: ["{{.Worktree.Root}}/bin/api", "-config", "/etc/api/config.json"]
      // watching: true is the default, no need to specify
    }
    {
      name: "postgres"
      command: ["/opt/homebrew/bin/postgres", "-D", "/usr/local/var/postgres"]
      watching: false  // External binary, don't watch
    }
  ]
}
```

**How it works:**

1. Trellis extracts the binary path from `command`
2. Uses fsnotify to watch the binary's parent directory
3. On write/create/rename event for the binary, waits for debounce period
4. Kills the running process (SIGTERM)
5. Starts the process with new binary
6. Emits `service.restarted` event

**Note:** Watching the directory (not the file) ensures atomic renames are detected. Many build tools replace binaries via rename (e.g., `mv new_binary binary`), which wouldn't trigger a write event on the file itself.

**Binary path resolution:**

- If `watch_binary` is set, use that path directly
- Otherwise, extract from `command` (ignoring `debug` wrapper):
  - If `command` is a string, the first whitespace-delimited token is the binary
  - If `command` is an array, the first element is the binary
- Templates are expanded before path extraction
- Set `watching: false` to disable watching for external binaries

**Additional file watching:**

In addition to the binary, services can watch config files or other resources via `watch_files`. When any watched file changes, the service is restarted. This is useful for:
- Configuration files that the service reads on startup
- Resource files that require a restart to reload
- Any file whose modification should trigger a service restart

**Note:** The `debug` field prepends a wrapper (e.g., `dlv exec`) to the command at runtime, but binary resolution always uses the original `command` value. This ensures the service binary is watched, not the debugger.

**Global watch configuration:**

```hjson
{
  watch: {
    // Global debounce for all binary watches (default: "100ms")
    debounce: "500ms"
  }
}
```

**Duration format:** All duration values use Go duration syntax: a sequence of decimal numbers with unit suffixes. Valid units: `ns`, `us`/`µs`, `ms`, `s`, `m`, `h`. Examples: `500ms`, `1.5s`, `10m`, `1h30m`.

---

## 6. Event Bus

### 6.1 Event Bus Configuration

```hjson
{
  events: {
    // Event history retention
    history: {
      max_events: 10000
      max_age: "1h"
    }

    // Webhook subscriptions
    webhooks: [
      {
        id: "slack-alerts"
        url: "https://slack.example.com/webhook"
        events: ["service.crashed", "workflow.finished"]
      }
    ]
  }
}
```

### 6.2 Event Envelope

All events have this structure:

```json
{
  "id": "01H9X5K2P3Q4R5S6T7U8V9W0",
  "version": "1.0",
  "type": "service.crashed",
  "timestamp": "2024-01-15T14:32:01.123Z",
  "worktree": "feature-auth",
  "payload": { /* type-specific */ }
}
```

### 6.3 Event Types

#### Service Events

| Type | Payload | When |
|------|---------|------|
| `service.started` | `{service, pid}` | Process started |
| `service.crashed` | `{service, exit_code, reason, excerpt, log_buffer_id}` | Unexpected exit |
| `service.stopped` | `{service, reason}` | Intentional stop |
| `service.restarted` | `{service, trigger}` | Service restarted |

**`service.restarted` trigger values:**
- `binary_change` — Binary file was modified (file watcher)
- `manual` — User requested restart via UI/API
- `worktree_switch` — Worktree switch restarted all services
- `crash` — Automatic restart after crash (if restart policy enabled)

#### Worktree Events

| Type | Payload | When |
|------|---------|------|
| `worktree.deactivating` | `{worktree, branch, path}` | About to switch away from worktree |
| `worktree.activated` | `{worktree, branch, path, previous?}` | Worktree switched |
| `worktree.created` | `{worktree, branch, path, based_on}` | New worktree created |
| `worktree.deleted` | `{worktree, branch}` | Worktree removed |
| `worktree.hook.started` | `{worktree, hook_name, phase, command}` | Lifecycle hook starting |
| `worktree.hook.finished` | `{worktree, hook_name, phase, success, duration, output?}` | Lifecycle hook completed |

#### Workflow Events

| Type | Payload | When |
|------|---------|------|
| `workflow.started` | `{workflow_id, name, trigger}` | Workflow started |
| `workflow.finished` | `{workflow_id, name, success, duration}` | Workflow complete |

#### Binary Events

| Type | Payload | When |
|------|---------|------|
| `binary.changed` | `{service, path}` | Watched binary file was modified |

#### Notification Events

External tools (like AI assistants) can emit notification events to alert users:

| Type | Payload | When |
|------|---------|------|
| `notify.done` | `{message}` | Task completed successfully |
| `notify.blocked` | `{message}` | Waiting for user input |
| `notify.error` | `{message}` | Something failed |

These events are emitted via the `/api/v1/notify` endpoint or `trellis-ctl notify` command. Users can subscribe to `notify.*` to receive all notification events.

### 6.4 Crash Reason Derivation

When a service crashes, derive the `reason` field:

**Algorithm:**
1. Scan buffer backward for `panic:` line → use it (truncate to 120 chars)
2. Else scan for `fatal:` or `runtime error:` → use it
3. Else find last line matching `/(error|failed|refused|timeout|undefined:|cannot)/i` → use it
4. Else return `"exit <code>"`

**Excerpt extraction:**
- Find the panic/fatal/error line
- Include up to 3 non-blank lines immediately before it
- Include the error line itself
- Truncate each line to 200 chars
- Strip timestamps/prefixes if consistently formatted

### 6.5 Event Subscription API

```go
type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(pattern string, handler EventHandler) (SubscriptionID, error)
    SubscribeAsync(pattern string, handler EventHandler, bufferSize int) (SubscriptionID, error)
    Unsubscribe(id SubscriptionID) error
    History(filter EventFilter) ([]Event, error)
    Close() error
}
```

Pattern matching:
- `service.crashed` — exact match
- `service.*` — all service events
- `*` — all events
- `*.finished` — all finish events

---

## 7. Worktree Management

### 7.1 Worktree Configuration

```hjson
{
  worktree: {
    // Directory to run git worktree discovery (defaults to config file directory)
    repo_dir: "/path/to/main/worktree"

    // Directory where new worktrees are created (defaults to parent of repo_dir)
    create_dir: "/path/to/worktrees"

    // Worktrees are discovered via `git worktree list`
    discovery: {
      mode: "git"
    }

    // Binary location strategy
    binaries: {
      // Where binaries are built for each worktree
      path: "{{.Worktree.Root}}/bin"

      // Or use a shared location with worktree prefix
      // path: "/home/user/bin/{{.Worktree.Name}}"
    }

    // Lifecycle hooks for worktree operations
    // Commands run in the TARGET worktree's root directory
    lifecycle: {
      // Run once when a worktree is first created
      // Use for: one-time setup, database initialization, config generation
      on_create: [
        {
          name: "setup-env"
          command: ["cp", ".env.example", ".env"]
          timeout: "30s"
        }
        {
          name: "init-db"
          command: ["./scripts/init-dev-db.sh"]
          timeout: "2m"
        }
      ]

      // Run before activating a worktree (after stopping old services)
      // Use for: dependency install, build, setup
      pre_activate: [
        {
          name: "install-deps"
          command: ["npm", "install"]
          timeout: "5m"
        }
        {
          name: "build"
          command: ["mage", "build"]
          timeout: "10m"
        }
      ]
    }
  }
}
```

### 7.2 Worktree Discovery

Trellis uses `git worktree list` to discover worktrees. The command runs in the `repo_dir` directory (defaults to the config file's directory).

```bash
$ git worktree list
/home/user/project         abc1234 [main]
/home/user/project-feature def5678 [feature/auth]
```

When creating new worktrees, they are placed in `create_dir` (defaults to parent of `repo_dir`) with the naming pattern `{project.name}-{branch}`.

**Example:** With `project.name: "myapp"` and `create_dir: "/home/user/worktrees"`, creating a branch `feature-auth` produces `/home/user/worktrees/myapp-feature-auth`.

### 7.3 Worktree State

Each worktree tracks:

| Field | Description |
|-------|-------------|
| `name` | Worktree identifier (directory name) |
| `path` | Absolute path to worktree |
| `branch` | Current git branch |
| `head` | Current commit SHA |
| `dirty` | `bool` — Whether working tree has uncommitted changes |
| `ahead` | `int` — Commits ahead of default branch (main/master) |
| `behind` | `int` — Commits behind default branch (main/master) |

### 7.4 Worktree Switching

When switching worktrees, Trellis stops all services, runs lifecycle hooks, then restarts services using the new worktree's binaries.

```
┌─────────────────────────────────────────────────────────────────┐
│                    WORKTREE SWITCH SEQUENCE                      │
├─────────────────────────────────────────────────────────────────┤
│  OLD WORKTREE                                                    │
│  1. Emit `worktree.deactivating` event                          │
│  2. Stop all services                                           │
├─────────────────────────────────────────────────────────────────┤
│  SWITCH                                                          │
│  3. Update active worktree reference                            │
│  4. Re-expand all template variables ({{.Worktree.*}})          │
├─────────────────────────────────────────────────────────────────┤
│  NEW WORKTREE                                                    │
│  5. Run `lifecycle.pre_activate` hooks (install, build, etc.)   │
│  6. Emit `worktree.activated` event                             │
│  7. Start services (using new worktree's binaries)              │
└─────────────────────────────────────────────────────────────────┘
```

Since service commands use `{{.Worktree.Binaries}}`, switching worktrees automatically uses the correct binaries for each worktree.

**Note:** All worktree tmux sessions exist simultaneously and persist across worktree switches. Switching worktrees only affects which binaries the services run—it does not switch or restart tmux sessions. The terminal UI shows all sessions (all worktrees + remote), and you can view any terminal regardless of which worktree is "active".

### 7.5 Worktree Creation

When creating a new worktree, Trellis runs `on_create` hooks once, then proceeds with activation:

```
┌─────────────────────────────────────────────────────────────────┐
│                    WORKTREE CREATE SEQUENCE                      │
├─────────────────────────────────────────────────────────────────┤
│  1. Run `git worktree add` to create the worktree               │
│  2. Emit `worktree.created` event                               │
│  3. Run `lifecycle.on_create` hooks (one-time setup)            │
│  4. Proceed with normal activation (pre_activate, etc.)         │
└─────────────────────────────────────────────────────────────────┘
```

**`on_create` vs `pre_activate`:**

| Hook | When it runs | Use case |
|------|--------------|----------|
| `on_create` | Once, when worktree is first created | Database init, .env setup, one-time config |
| `pre_activate` | Every time worktree is activated/switched to | Dependency install, build, migrations |

**Lifecycle hook execution:**

- Hooks run in array order, sequentially
- Working directory is the worktree root
- Hook output is captured and emitted as events
- If a hook fails, it is logged but the operation continues

**Example configuration for a Go project:**

```hjson
{
  worktree: {
    binaries: {
      path: "/home/user/bin/{{.Project.Name}}-{{.Worktree.Name}}"
    }
    lifecycle: {
      pre_activate: [
        { name: "go-mod", command: ["go", "mod", "download"], timeout: "2m" }
        { name: "build", command: ["mage", "build"], timeout: "5m" }
      ]
    }
  }
}
```

**Example for a Node.js project:**

```hjson
{
  worktree: {
    lifecycle: {
      pre_activate: [
        { name: "npm-install", command: ["npm", "ci"], timeout: "5m" }
        { name: "build", command: ["npm", "run", "build"], timeout: "3m" }
      ]
    }
  }
}
```

**Example with both on_create and pre_activate:**

```hjson
{
  worktree: {
    lifecycle: {
      // One-time setup when worktree is created
      on_create: [
        { name: "copy-env", command: ["cp", ".env.example", ".env"], timeout: "10s" }
        { name: "init-db", command: ["./scripts/create-dev-database.sh"], timeout: "1m" }
        { name: "seed-data", command: ["./scripts/seed-test-data.sh"], timeout: "2m" }
      ]
      // Run on every activation (including after create)
      pre_activate: [
        { name: "deps", command: ["go", "mod", "download"], timeout: "2m" }
        { name: "build", command: ["mage", "build"], timeout: "5m" }
        { name: "migrate", command: ["./scripts/migrate.sh"], timeout: "1m" }
      ]
    }
  }
}
```

---

## 8. Crash Reports

### 8.1 Overview

Trellis maintains a persistent crash history that survives server restarts:

- Automatically records crashes when services fail
- Stores complete context including service logs, worktree state, and trace IDs
- Persists crashes as JSON files in `.trellis/crashes/`
- Provides API, CLI, and web UI access

### 8.2 Crash Reports Configuration

```hjson
{
  crashes: {
    // Directory to store crash files (default: .trellis/crashes)
    reports_dir: ".trellis/crashes"

    // Maximum age of crashes to keep (default: 7d)
    // Supports: h (hours), d (days), w (weeks)
    max_age: "7d"

    // Maximum number of crashes to keep (default: 100)
    max_count: 100
  }
}
```

### 8.3 Crash Data Structure

Each crash record contains:

| Field | Description |
|-------|-------------|
| `id` | Unique identifier (timestamp-based: `20060102-150405.000`) |
| `service` | Name of the crashed service |
| `timestamp` | When the crash occurred |
| `trace_id` | Request trace ID that caused the crash (if found) |
| `exit_code` | Process exit code |
| `error` | Error message from the crash |
| `worktree` | Worktree context (name, branch, path) |
| `service_logs` | Log buffers from all services at crash time |
| `trigger` | What triggered the crash (signal, error, etc.) |

### 8.4 Trace ID Extraction

When a service crashes, Trellis attempts to find the trace ID of the request that caused the crash. This is non-trivial because the final log line (the crash/panic line) typically has its own trace ID that belongs to the error logging system, not the original request.

Trellis uses backwards scanning to find the actual request:
1. Extract the trace ID from the last log line (the crash line)
2. Scan backwards through earlier log lines
3. Return the first trace ID that differs from the crash line's ID

This works because the crash line's trace ID is generated when logging the error, while earlier lines have the original request's trace ID. If all lines have the same ID (or only the crash line has an ID), that ID is used as a fallback.

### 8.5 Crash Reports API

**List all crashes:**
```
GET /api/v1/crashes
```

Response:
```json
{
  "data": [
    {
      "id": "20260115-143201.234",
      "service": "api",
      "timestamp": "2026-01-15T14:32:01.234Z",
      "trace_id": "req-abc123",
      "exit_code": 2,
      "error": "panic: nil pointer dereference"
    }
  ]
}
```

**Get specific crash:**
```
GET /api/v1/crashes/{id}
```

Returns full crash record including service logs.

**Get newest crash:**
```
GET /api/v1/crashes/newest
```

Returns the most recent crash with full details.

**Delete a crash:**
```
DELETE /api/v1/crashes/{id}
```

**Clear all crashes:**
```
DELETE /api/v1/crashes
```

### 8.6 CLI Commands

```bash
# List all crashes
trellis-ctl crash list

# Show the most recent crash with full details
trellis-ctl crash newest

# Show a specific crash
trellis-ctl crash <id>

# Delete a crash
trellis-ctl crash delete <id>

# Clear all crashes
trellis-ctl crash clear
```

### 8.7 Web UI

The crashes page is accessible at `/crashes` and shows:
- List of all crashes sorted by timestamp (newest first)
- Service name, exit code, error message, and trace ID for each crash
- Click on a crash to view full details including service logs
- Delete individual crashes or clear all

---

## 9. Terminal System

### 9.1 Terminal Configuration

```hjson
{
  terminal: {
    // Terminal multiplexer backend
    backend: "tmux"  // currently only tmux supported

    // Tmux configuration
    tmux: {
      // Scrollback buffer size
      history_limit: 50000

      // Default shell
      shell: "/bin/zsh"
    }

    // Default windows per worktree (local tmux)
    default_windows: [
      { name: "dev", command: "/bin/zsh" }
      { name: "claude", command: "claude" }
    ]

    // Remote windows - global terminals (SSH, not local tmux)
    // Two formats supported:
    //   1. Full command: { name: "...", command: ["ssh", "-t", "host", "..."] }
    //   2. SSH+tmux shorthand: { name: "...", ssh_host: "host", tmux_session: "session" }
    remote_windows: [
      // Full command format - use for screen, custom commands, etc.
      { name: "mail01-g2", command: ["ssh", "-t", "mail01-g2", "screen", "-dR", "runner1"] }
      // SSH+tmux shorthand - automatically builds command with mouse mode enabled
      { name: "admin(1)", ssh_host: "admin01-g2", tmux_session: "admin1" }
      { name: "admin(2)", ssh_host: "admin01-g2", tmux_session: "admin2" }
    ]

    // VS Code server configuration (for embedded editor)
    vscode: {
      // Path to code-server binary (default: search PATH)
      binary: "code-server"
      // Internal port for code-server (Trellis proxies to this)
      port: 8443
      // Path to VS Code user data directory (settings, keybindings, etc.)
      // Useful for sharing settings across machines via a dotfiles repo
      user_data_dir: "~/dotfiles/vscode"
    }

    // Keyboard shortcuts to jump to specific views
    // Window MUST start with a prefix character indicating the target type:
    //   ~name             = log viewer (e.g., ~nginx, ~auth-server)
    //   #name             = service logs (e.g., #api, #worker)
    //   @worktree - win   = local terminal (e.g., @main - dev, @feature - claude)
    //   !name             = remote window (e.g., !admin, !production)
    shortcuts: [
      { key: "cmd+l", window: "~nginx" }            // Jump to nginx log viewer
      { key: "cmd+1", window: "@main - dev" }       // Jump to dev terminal in main worktree
      { key: "cmd+2", window: "@main - claude" }    // Jump to claude terminal in main worktree
      { key: "cmd+3", window: "#api" }              // Jump to api service logs
      { key: "cmd+4", window: "!admin" }            // Jump to admin remote window
    ]

    // Links - external URLs accessible from terminal picker (open in new browser tabs)
    links: [
      { name: "Grafana", url: "http://localhost:3000/" }
      { name: "GitHub", url: "https://github.com/myorg/myrepo" }
      { name: "Admin", url: "http://admin-server:8080/" }
    ]
  }
}
```

### 9.2 Naming Convention

**Tmux session names** (internal):
- Use the worktree directory name with periods replaced by underscores (tmux compatibility)
- Examples: `groups_io` (from `groups.io`), `groups_io-demovideos` (from `groups.io-demovideos`)

**UI display names:**

| Type | Prefix | Tmux Session | Display Name |
|------|--------|--------------|--------------|
| Main worktree | `@` | `groups_io` | `@main` |
| Other worktree | `@` | `groups_io-demovideos` | `@demovideos` |
| Remote | `!` | (none) | `!admin(1)` |

**Window names** come from `default_windows[].name` and `remote_windows[].name`.

The terminal selector shows: `{display_name} - {window_name}`
- `@main - dev`
- `@main - claude`
- `@demovideos - claude`
- `!admin(1)`

### 9.3 Window Management

Each worktree tmux session has windows defined by `default_windows`:

```
Session: groups_io
├── Window 0: dev (zsh)
└── Window 1: claude (claude CLI)
```

Windows can be:
- Created on session start
- Created on demand via API
- Created by user in tmux

### 9.4 Remote Windows

Remote windows are global terminals that persist across worktree switches. They are not local tmux sessions—Trellis spawns the command directly (typically SSH) and streams I/O to xterm.js.

**Configuration formats:**

Remote windows support two configuration formats:

1. **Full command** - For arbitrary commands (screen, custom scripts, etc.):
   ```hjson
   { name: "server1", command: ["ssh", "-t", "host", "screen -dR session1"] }
   ```

2. **SSH+tmux shorthand** - For SSH connections to remote tmux sessions:
   ```hjson
   { name: "server1", ssh_host: "host", tmux_session: "session1" }
   ```

The shorthand automatically builds the command:
```
ssh -t <ssh_host> 'tmux new -A -s <tmux_session> \; set -g status off \; set -g mouse on'
```

This includes:
- `new -A` — Create session or attach if exists
- `set -g status off` — Hide tmux status bar (Trellis provides its own UI)
- `set -g mouse on` — Enable mouse mode for scrolling support in xterm.js

**Display naming:**

| Type | Prefix | Example |
|------|--------|---------|
| Worktree windows | `@` | `@main`, `@feature-auth` |
| Remote windows | `!` | `!admin(1)`, `!mail01-g2` |

**Behavior differences:**

| Aspect | Worktree Windows | Remote Windows |
|--------|------------------|----------------|
| Lifetime | Per-worktree, recreated on switch | Global, persist across switches |
| Backend | Local tmux | Direct command (SSH, etc.) |
| Multiple windows | Yes (`default_windows` array) | One per entry |
| Scrollback | tmux buffer | xterm.js buffer |

### 9.5 Terminal Web Interface

Trellis streams terminals to the browser via WebSocket:

**For worktree windows:**
1. Client connects to `/api/v1/terminal/ws?session=...&window=...`
2. Server attaches to tmux pane
3. Server streams pane output to client (xterm.js)
4. Client sends keystrokes back to server
5. Server sends keystrokes to tmux pane

**For remote windows:**
1. Client connects to `/api/v1/terminal/ws?remote=...`
2. Server spawns the remote command (e.g., SSH) if not already running
3. Server streams command stdout to client
4. Client sends keystrokes to command stdin
5. On disconnect, command continues running

**Remote window lifecycle:**
- Each remote window name maps to at most one running process
- Reconnecting to the same remote name reattaches to the existing process
- If the process has exited, a new one is spawned on connect
- Scrollback is preserved across reconnections

Features:
- Full scrollback capture on connect
- Real-time output streaming
- Input forwarding
- Window resize handling
- Multiple concurrent viewers
- Auto-reconnect for remote windows

### 9.6 VS Code Integration

Trellis provides an integrated VS Code experience using [code-server](https://github.com/coder/code-server). The editor is accessible from the terminal picker as an "editor" option for each worktree.

**Architecture:**

```
┌─────────────────────────────────────────────────────────────┐
│  Machine (local or remote)                                  │
│                                                             │
│   ┌─────────────┐         ┌─────────────┐                  │
│   │   Trellis   │ proxy   │ code-server │                  │
│   │ (port 1000) │ ──────▶ │ (port 8443) │                  │
│   └─────────────┘         └─────────────┘                  │
│         │                        │                          │
│         │    /vscode/*           │                          │
│         └────────────────────────┘                          │
│                                                             │
└─────────────────────────────────────────────────────────────┘
           │
           ▼ (network / Tailscale)
┌─────────────────────────────────────────────────────────────┐
│  Browser                                                    │
│  - Trellis dashboard: http://host:1000/                    │
│  - VS Code editor:    http://host:1000/vscode/             │
└─────────────────────────────────────────────────────────────┘
```

**How it works:**

1. Trellis starts code-server as a managed subprocess on startup (if configured)
2. code-server listens on internal port (default 8443, not exposed)
3. Trellis proxies `/vscode/*` requests to code-server
4. Browser accesses VS Code through Trellis (single auth, single URL)
5. Workspace is set to `{{.Worktree.Root}}`
6. On worktree switch, workspace switches automatically

**Configuration:**

```hjson
{
  terminal: {
    vscode: {
      binary: "code-server"  // Path to code-server binary
      port: 8443             // Internal port (proxied by Trellis)
    }
  }
}
```

**Benefits:**

| Feature | How |
|---------|-----|
| Single URL | All access through Trellis (host:1000) |
| Single auth | No separate code-server password |
| Worktree-aware | Workspace switches with worktree |
| Error links | `vscode://` links open in integrated VS Code |
| Works remotely | Same experience whether Trellis is local or remote |

**API routes:**

| Route | Description |
|-------|-------------|
| `GET /vscode/` | VS Code UI (proxied to code-server) |
| `GET /vscode/*` | All code-server assets and WebSocket |

**Settings Sync via Dotfiles:**

code-server doesn't support Microsoft's proprietary Settings Sync. Instead, use a dotfiles repository to share settings across machines:

1. Create a dotfiles repo with your VS Code settings:
   ```bash
   mkdir -p ~/dotfiles/vscode/User
   cp ~/.config/Code/User/settings.json ~/dotfiles/vscode/User/
   cp ~/.config/Code/User/keybindings.json ~/dotfiles/vscode/User/
   ```

2. Clone the repo on any machine running Trellis

3. Configure Trellis to use it:
   ```hjson
   terminal: {
     vscode: {
       user_data_dir: "~/dotfiles/vscode"
     }
   }
   ```

The `~` is automatically expanded to the home directory, so this works identically on local and remote Trellis installations. When you update settings locally, commit and push, then pull on remote machines to sync.

**Note:** code-server must be installed separately. If vscode is configured and code-server is not found, Trellis will log a warning. See https://coder.com/docs/code-server/install

### 9.7 Links

Links provide quick access to external URLs from the terminal picker dropdown. They appear with a `>` prefix in the picker (e.g., `> Grafana`).

**Configuration:**

```hjson
{
  terminal: {
    links: [
      {
        // Required: display name in picker
        name: "Grafana"
        // Required: URL to open
        url: "http://localhost:3000/"
      }
      {
        name: "GitHub"
        url: "https://github.com/myorg/myrepo"
      }
    ]
  }
}
```

**Behavior:**

- Selecting a link opens it in a new browser tab
- Clicking the same link again reuses the existing tab (navigates and focuses it)
- Each link gets its own tab (based on link name)
- If the tab was manually closed, a new tab is opened
- Selecting a link does not affect Cmd/Ctrl+Backspace navigation history

**Tab reuse:**

Trellis stores references to opened link windows. When you click a link:

1. If a tab for that link is already open → navigate to URL and focus the tab
2. If the tab was closed → open a new tab
3. Different links open in different tabs

This allows quick switching between Trellis and external dashboards without accumulating duplicate tabs.

### 9.8 Environment Variables

Trellis automatically sets environment variables in tmux sessions to enable integration with tools like `trellis-ctl`:

| Variable | Description | Example |
|----------|-------------|---------|
| `TRELLIS_API` | Base URL of the Trellis HTTP API | `http://localhost:8080` |

The `TRELLIS_API` variable is set when:
- A new tmux session is created for a worktree
- An existing session is attached (updated if URL changed)

This enables CLI tools and scripts running in Trellis-managed terminals to discover and interact with the Trellis API without manual configuration.

---

### 9.9 Logging Defaults

The `logging_defaults` section provides shared default configurations for `parser`, `derive`, and `layout` that apply to all log viewers and service logging unless overridden:

```hjson
{
  logging_defaults: {
    // Default parser configuration
    parser: {
      type: "json"
      timestamp: "time"
      level: "level"
      id: "trace_id"      // Field for trace ID extraction (used by crashes and trace)
      stack: "stack"      // Field containing stack trace (included in crash reports)
    }

    // Default derived fields
    derive: {
      ts_short: {
        from: "time"
        op: "timefmt"
        args: { format: "15:04:05.000" }
      }
      file_line: {
        op: "fmt"
        args: { template: "{basename(file)}:{line}" }
      }
    }

    // Default layout
    layout: [
      { field: "ts_short", max_width: 12, timestamp: true }
      { field: "level", max_width: 6 }
      { field: "msg" }
    ]
  }
}
```

**Merge behavior:**

| Config Section | Behavior |
|---------------|----------|
| `parser` | Default fields used only if not specified locally (per-field merge) |
| `derive` | Default derives merged in; local definitions take precedence |
| `layout` | Default used only if local layout is empty (all-or-nothing) |

**Example: Minimal log viewer using defaults:**

```hjson
{
  logging_defaults: {
    parser: { type: "json", timestamp: "time" }
    derive: {
      ts_short: { from: "time", op: "timefmt", args: { format: "15:04:05.000" } }
    }
    layout: [
      { field: "ts_short", max_width: 12, timestamp: true }
      { field: "msg" }
    ]
  }

  log_viewers: [
    {
      // Only need to specify name and source; parser/derive/layout come from defaults
      name: "api-logs"
      source: { type: "ssh", host: "api01", path: "/var/log/api/", current: "app.log" }
    }
    {
      name: "admin-logs"
      source: { type: "ssh", host: "admin01", path: "/var/log/admin/", current: "app.log" }
      // Override layout for this viewer only
      layout: [
        { field: "ts_short", max_width: 12, timestamp: true }
        { field: "level", max_width: 6 }
        { field: "request_id", max_width: 20 }
        { field: "msg" }
      ]
    }
  ]

  services: [
    {
      name: "backend"
      command: "./bin/backend"
      // logging.parser/derive/layout use logging_defaults if not specified
    }
    {
      name: "legacy-api"
      command: "./bin/legacy"
      // Override parser.id for this service (uses different trace ID field)
      logging: {
        parser: {
          id: "request_id"   // This service uses "request_id" instead of "trace_id"
        }
      }
    }
  ]
}
```

---

## 10. Log Viewers

Log Viewers provide structured, searchable log viewing with support for remote sources, multiple parsers, and historical access to rotated/compressed logs.

### 10.1 Log Viewer Configuration

```hjson
{
  log_viewers: [
    {
      // Unique identifier for this log viewer
      name: "admin-logs"

      // Where logs come from
      source: {
        // Source type: "ssh", "file", "command", "docker", "kubernetes"
        type: "ssh"

        // SSH source options
        host: "admin01-g2"           // SSH host (uses ~/.ssh/config)
        path: "/var/log/app/"        // Directory containing logs
        current: "current"           // Name of the active log file
        rotated_pattern: "@*.zst"    // Glob pattern for rotated logs (optional)

        // File source options (for local files)
        // path: "/var/log/myapp/app.log"

        // Command source options (for arbitrary commands)
        // command: ["journalctl", "-f", "-u", "myapp"]

        // Docker source options
        // container: "my-container"
        // follow: true

        // Kubernetes source options
        // namespace: "default"
        // pod: "my-pod"
        // container: "app"
      }

      // How to parse log lines into structured entries
      parser: {
        // Parser type: "json", "logfmt", "regex", "syslog", "none"
        type: "json"

        // Field mappings (for json/logfmt parsers)
        timestamp: "time"            // Required: field containing timestamp (used for sorting/filtering)
        // level: "level"            // Optional: field containing log level (enables row color coding)
        // message: "msg"            // Optional: field containing message (not needed with layout system)

        // Timestamp format (Go time format, default: RFC3339)
        timestamp_format: "2006-01-02T15:04:05.000Z"

        // For regex parser: named capture groups
        // pattern: "^(?P<timestamp>\\S+) (?P<level>\\w+) (?P<message>.*)$"

        // Optional: Field name containing request/session ID for trace expansion
        // When set, trace "Expand by ID" will extract IDs from this field
        // and search for related entries sharing the same ID
        id: "request_id"
      }

      // Derived fields - computed from parsed fields
      derive: {
        // Combine file basename and line number
        file_line: {
          op: "fmt"
          args: { template: "{basename(file)}:{line}" }
        }
        // Short timestamp format
        ts_short: {
          from: "time"
          op: "timefmt"
          args: { format: "15:04:05.000" }  // Go time layout
        }
        // Combine program and host
        prog_host: {
          op: "fmt"
          args: { template: "{prog}@{host}" }
        }
      }

      // Layout configuration - columns to display (in order)
      //
      // Column width behavior:
      //   min_width > 0: Creates a reservation - column always takes at least this much space
      //   min_width = 0 (or omitted): No reservation - column shrinks to fit content
      //   max_width > 0: Caps column width - content truncated with ellipsis if exceeded
      //   max_width = 0 (or omitted): No limit - column fills available space
      //
      // Fixed-width column: set min_width = max_width (e.g., timestamp, level)
      // Non-fixed-width column: omit min_width, set max_width to cap space
      layout: [
        {
          field: "ts_short"          // Use derived field
          min_width: 12              // Fixed-width: reserve exactly 12 chars
          max_width: 12
          timestamp: true            // Enable timestamp toggle (absolute/relative)
        }
        {
          field: "level"
          min_width: 6               // Fixed-width: reserve exactly 6 chars
          max_width: 6
        }
        {
          // kvpairs column type - displays key=value pairs from fields
          type: "kvpairs"
          keys: ["request_id", "user_id", "action"]  // Fields to display (in order)
          max_pairs: 3               // Max pairs to show (0 = all)
          max_width: 40              // Non-fixed: shrinks to content, caps at 40
        }
        {
          field: "msg"               // No width constraints - fills remaining space
        }
      ]

      // Buffer configuration
      buffer: {
        max_entries: 100000          // Max entries to keep in memory
        persist: false               // Persist buffer across restarts
      }
    }
  ]
}
```

### 10.2 Source Types

#### SSH Source

Connects to a remote host via SSH to tail logs:

```hjson
source: {
  type: "ssh"
  host: "server01"                   // SSH host (from ~/.ssh/config or hostname)
  path: "/var/log/app/"              // Log directory
  current: "current"                 // Active log file name
  rotated_pattern: "@*.zst"          // Pattern for rotated logs
  decompress: "zstd -dc"             // Command to decompress (auto-detected for .zst, .gz)
}
```

For live tailing, Trellis runs: `ssh <host> tail -F <path>/<current>`

For historical access, Trellis lists matching files and decompresses on demand.

**Server-side filtering optimization**: When querying historical logs with `-grep` and time filters (`-since`/`-until`), the server executes grep remotely on the SSH host rather than transferring entire files. This is essential for large log files. Context flags (`-B`/`-A`/`-C`) are also handled server-side:

```bash
# This runs grep remotely, not locally:
trellis-ctl logs -viewer api-logs -grep "error" -B 5 -since 6:00am -until 7:00am
# Server executes: ssh host "grep 'timestamp-pattern' file | grep -B 5 'error'"
```

#### File Source

Watches a local file:

```hjson
source: {
  type: "file"
  path: "/var/log/myapp/app.log"
  rotated_pattern: "/var/log/myapp/app.log.*"
}
```

#### Command Source

Runs an arbitrary command and parses its stdout:

```hjson
source: {
  type: "command"
  command: ["journalctl", "-f", "-u", "myapp", "-o", "json"]
}
```

#### Docker Source

Tails Docker container logs:

```hjson
source: {
  type: "docker"
  container: "my-container"          // Container name or ID
  follow: true                       // Follow log output
  since: "1h"                        // How far back to start (optional)
}
```

#### Kubernetes Source

Tails Kubernetes pod logs:

```hjson
source: {
  type: "kubernetes"
  namespace: "default"
  pod: "my-pod"                      // Pod name (supports wildcards)
  container: "app"                   // Container name (optional)
  follow: true
  since: "1h"
}
```

### 10.3 Parser Types

#### JSON Parser

Each line is a JSON object:

```hjson
parser: {
  type: "json"
  timestamp: "ts"                    // JSON field for timestamp
  level: "level"                     // JSON field for level
  message: "msg"                     // JSON field for message
  // All other fields become metadata
}
```

Input: `{"ts":"2024-01-15T10:30:00Z","level":"error","msg":"Connection failed","host":"db01"}`

#### Logfmt Parser

Key-value pairs:

```hjson
parser: {
  type: "logfmt"
  timestamp: "time"
  level: "level"
  message: "msg"
}
```

Input: `time=2024-01-15T10:30:00Z level=error msg="Connection failed" host=db01`

#### Regex Parser

Named capture groups:

```hjson
parser: {
  type: "regex"
  pattern: "^\\[(?P<timestamp>[^\\]]+)\\] (?P<level>\\w+): (?P<message>.*)$"
  timestamp_format: "2006-01-02 15:04:05"
}
```

Input: `[2024-01-15 10:30:00] ERROR: Connection failed`

#### Syslog Parser

RFC 3164 or RFC 5424 syslog format:

```hjson
parser: {
  type: "syslog"
  format: "rfc5424"                  // or "rfc3164"
}
```

#### None Parser

No parsing, each line becomes a raw entry:

```hjson
parser: {
  type: "none"
}
```

### 10.4 Log Entry Structure

All parsers produce normalized `LogEntry` objects:

```go
type LogEntry struct {
    Timestamp time.Time            // Parsed timestamp (or receive time)
    Level     string               // info, warn, error, debug, trace, fatal
    Message   string               // Main log message
    Fields    map[string]any       // Additional structured fields
    Raw       string               // Original unparsed line
    Source    string               // Log viewer name
    Offset    int64                // Position in source (for historical access)
}
```

### 10.5 Filter Syntax

The log viewer supports a query syntax for filtering:

| Syntax | Description | Example |
|--------|-------------|---------|
| `field:value` | Exact match | `level:error` |
| `field:val1,val2` | OR match | `level:error,warn` |
| `field:~"text"` | Contains | `msg:~"connection"` |
| `field:>"value"` | Greater than | `duration:>100ms` |
| `field:<"value"` | Less than | `status:<500` |
| `field:>=`, `field:<=` | Comparison | `status:>=400` |
| `-field:value` | Exclude | `-level:debug` |
| `term1 term2` | AND (space) | `level:error host:db01` |
| `"quoted text"` | Message contains | `"connection refused"` |

**Time filters:**

| Syntax | Description |
|--------|-------------|
| `ts:>-5m` | Last 5 minutes |
| `ts:>-1h` | Last hour |
| `ts:>"2024-01-15T10:00:00Z"` | After specific time |
| `ts:<"2024-01-15T11:00:00Z"` | Before specific time |

**Examples:**

```
level:error                              # All errors
level:error,warn                         # Errors and warnings
level:error host:db01                    # Errors from db01
msg:~"timeout" -level:debug              # Timeouts, excluding debug
ts:>-5m level:error                      # Errors in last 5 minutes
request_id:abc123                        # Specific request
duration:>500ms level:error              # Slow errors
```

### 10.6 Historical Access

When `rotated_pattern` is configured, the log viewer can access historical logs:

**File discovery:**
- SSH source: `ssh <host> ls -1t <path>/<pattern>`
- File source: `ls -1t <pattern>`

**Decompression:**
- `.zst` files: `zstd -dc`
- `.gz` files: `gzip -dc`
- `.bz2` files: `bzip2 -dc`
- `.xz` files: `xz -dc`

**Time-based navigation:**

The UI provides:
1. Time range picker (start/end timestamps)
2. "Jump to time" input
3. Seamless scrolling across file boundaries

**Implementation:**

When the user scrolls into a time range not in the buffer:
1. Identify which rotated file covers that time range
2. Decompress and parse the relevant portion
3. Merge into the display buffer
4. Maintain a sliding window to limit memory usage

### 10.7 Log Viewer Web Interface

**Naming in the picker:**

Log viewers appear in a unified picker alongside terminals. Prefixes distinguish types:

| Prefix | Type | Example |
|--------|------|---------|
| `@` | Worktree terminal | `@main - dev` |
| `!` | Remote terminal | `!admin(1)` |
| `#` | Service logs | `#api` |
| `~` | Log viewer | `~admin-logs` |

**Note:** Services with `logging.parser` configured display logs using the same table-based UI as log viewers, with filtering, column display, and entry expansion. Services without parser config show raw log output.

#### Mode 1: Live Tail

Optimized for streaming and reading sequences. Chronological order (oldest → newest, newest at bottom).

**Behavior:**
- On page open: auto-scroll to bottom, enter "Following" mode
- Following mode: viewport stays pinned to bottom as new lines stream in
- Scroll up: pause following (do not yank viewport back), show "Jump to latest" button
- New lines while paused: show counter badge (e.g., "+127 new lines")

**UI mockup (following):**

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ [~admin-logs ▼]                                          [Following ●]     │
├─────────────────────────────────────────────────────────────────────────────┤
│ 10:30:43.050  INFO   Starting health check                        db01     │
│ 10:30:44.100  ERROR  Connection timeout                           db02     │
│ 10:30:44.892  WARN   Retry attempt 3/5                            db01     │
│ 10:30:45.123  ERROR  Connection refused                           db01     │
│ 10:30:45.456  INFO   Retrying connection...                       db01     │
│ 10:30:45.789  INFO   Connected successfully                       db01  ←  │
└─────────────────────────────────────────────────────────────────────────────┘
```

**UI mockup (paused, scrolled up):**

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ [~admin-logs ▼]                                 [Paused] [+47 new lines ↓] │
├─────────────────────────────────────────────────────────────────────────────┤
│ 10:28:12.100  INFO   Request received                             api      │
│ 10:28:12.234  DEBUG  Parsing payload                              api      │
│ 10:28:12.300  INFO   Calling database                             api      │
│ 10:28:12.892  WARN   Slow query: 450ms                            db01     │
│ 10:28:13.001  INFO   Response sent                                api      │
│ ...                                                                         │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Mode 2: Search/Explore

Optimized for investigation. Streaming is paused.

**Behavior:**
- Activated by: clicking filter bar, pressing Ctrl+F/Cmd+F, or clicking "Search" button
- Filter by level, component, request-id, or any field
- Full-text search with highlighting
- Jump to specific time or line number
- Infinite scroll upward: load older chunks on demand

**UI mockup:**

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ [~admin-logs ▼]  🔍 [level:error request_id:abc123            ] [× Clear]  │
├─────────────────────────────────────────────────────────────────────────────┤
│                         ▲ Load earlier (10:00 - 10:28)                      │
├─────────────────────────────────────────────────────────────────────────────┤
│ 10:28:44.100  ERROR  Connection timeout          db02   request_id=abc123  │
│ 10:28:45.123  ERROR  Connection refused          db01   request_id=abc123  │
│ 10:30:12.456  ERROR  Max retries exceeded        api    request_id=abc123  │
├─────────────────────────────────────────────────────────────────────────────┤
│ Showing 3 of 3 matches                                    [Resume tail →]  │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### UX Details

**Follow state:**

| State | Indicator | Behavior |
|-------|-----------|----------|
| Following | Green "Following ●" badge | Pinned to bottom, auto-scrolls |
| Paused | "Paused" badge + "+N new lines ↓" button | Viewport frozen, counter updates |
| Search | Filter bar active, "Resume tail →" button | Streaming paused, filtered view |

**Text rendering:**
- Monospace font throughout
- Preserved whitespace (no collapsing)
- Stable line wrapping (lines don't reflow on update)
- Selection not disrupted by incoming updates (auto-pause on selection)

**Timestamps:**
- Always visible in left column
- Toggle between absolute (`10:30:45.123`) and relative (`2m ago`)
- Click timestamp to copy or jump

**Performance:**
- Virtualized scrolling: only render visible rows (cap DOM nodes)
- Buffer limit: keep last N lines in memory (configurable, default 100k)
- Older history: fetch on demand when scrolling up
- Batched rendering: throttle DOM updates during high-volume streaming

**Interactions:**

| Action | Result |
|--------|--------|
| Click row | Expand to show all fields + raw JSON |
| Scroll up | Pause following, show "Jump to latest" |
| Click "Jump to latest" | Resume following, scroll to bottom |
| Ctrl+F / Cmd+F | Enter search mode, focus filter bar |
| Escape | Clear filter, exit search mode |
| Select text | Pause updates until selection cleared |
| Click timestamp | Toggle absolute/relative (or copy) |
| Click history button (🕐) | Open History Search modal |

#### History Search Modal

For searching historical logs with time ranges and grep context (like `grep -B/-A/-C`):

**UI mockup:**

```
┌─────────────────────────────────────────────────────────────────┐
│ 🕐 Search Log History                                      [×] │
├─────────────────────────────────────────────────────────────────┤
│ Time Range                                                      │
│ ┌─────────────────────┐  ┌─────────────────────┐               │
│ │ From: 1h            │  │ To: now             │               │
│ └─────────────────────┘  └─────────────────────┘               │
│ Quick: [1h] [4h] [today] [yesterday]                           │
│                                                                 │
│ Search Pattern (grep)                                           │
│ ┌─────────────────────────────────────────────────────────────┐│
│ │ error|timeout|panic                                         ││
│ └─────────────────────────────────────────────────────────────┘│
│                                                                 │
│ Context Lines                                                   │
│ ┌──────────┐ ┌──────────┐ ┌──────────┐                         │
│ │ -B: 5    │ │ -A: 10   │ │ -C: 0    │                         │
│ └──────────┘ └──────────┘ └──────────┘                         │
│ Like grep: -B before, -A after, -C both                        │
├─────────────────────────────────────────────────────────────────┤
│                                    [Cancel] [🔍 Search]         │
└─────────────────────────────────────────────────────────────────┘
```

**Time input formats:**
- Duration: `1h`, `30m`, `2d`
- Clock time: `6:30am`, `14:00`
- ISO date: `2024-01-15T10:00:00Z`
- Special: `now`

**Behavior:**
- Opens via clock-rotate-left icon button in log viewer header
- Fetches from `/api/v1/logs/{name}/history` with grep and context params
- Switches to "History" mode (gray indicator), pauses live streaming
- Click mode button to return to live streaming

**Server-side optimization:** For SSH sources, grep with context flags runs remotely on the server rather than transferring large files locally.

### 10.8 Log Viewer API

#### List Log Viewers

```
GET /api/v1/logs
```

Returns configured log viewers and their status.

#### Get Log Entries

```
GET /api/v1/logs/:name/entries
  ?filter=level:error
  &limit=100
  &before=<timestamp>
  &after=<timestamp>
```

Returns filtered log entries.

#### Stream Log Entries

```
WebSocket /api/v1/logs/:name/stream
  ?filter=level:error
```

Streams new entries matching the filter.

**Messages (server → client):**

```json
{"type": "entry", "entry": {"timestamp": "...", "level": "error", ...}}
{"type": "entries", "entries": [...]}  // Bulk historical load
{"type": "status", "connected": true, "source": "admin-logs"}
```

**Messages (client → server):**

```json
{"type": "filter", "query": "level:error"}
{"type": "seek", "timestamp": "2024-01-15T10:30:00Z"}
{"type": "pause"}
{"type": "resume"}
```

#### Get Historical Range

```
GET /api/v1/logs/:name/history
  ?start=2024-01-15T00:00:00Z
  &end=2024-01-15T12:00:00Z
  &filter=level:error
```

Returns entries from rotated logs in the time range.

### 10.9 Events

Log viewers emit events:

| Event | Payload | Description |
|-------|---------|-------------|
| `log.connected` | `{viewer, source}` | Source connection established |
| `log.disconnected` | `{viewer, source, error}` | Source connection lost |
| `log.error` | `{viewer, error}` | Error parsing or fetching logs |

### 10.10 Implementation Notes

**Backend components:**

```
internal/logs/
├── source.go          # LogSource interface
├── source_ssh.go      # SSH source implementation
├── source_file.go     # Local file source
├── source_command.go  # Command source
├── source_docker.go   # Docker source
├── source_k8s.go      # Kubernetes source
├── parser.go          # LogParser interface
├── parser_json.go     # JSON parser
├── parser_logfmt.go   # Logfmt parser
├── parser_regex.go    # Regex parser
├── parser_syslog.go   # Syslog parser
├── entry.go           # LogEntry type
├── filter.go          # Filter parsing and evaluation
├── buffer.go          # Ring buffer for entries
├── viewer.go          # LogViewer coordinator
└── manager.go         # Manages all log viewers
```

**Performance considerations:**

- Virtual scrolling in frontend (only render visible rows)
- Ring buffer with configurable max entries
- Lazy loading of historical data
- Compression for WebSocket bulk transfers
- Index rotated files by time range for fast seeks

---

## 11. Distributed Tracing

Distributed tracing allows searching for a trace ID (or any pattern) across multiple log viewers and combining the results into a unified, time-sorted report.

### 11.1 Configuration

```hjson
{
  // Trace configuration
  trace: {
    // Directory where trace reports are saved
    reports_dir: "traces"

    // Auto-cleanup old reports (duration format)
    max_age: "7d"
  }

  // Named groups of log viewers for tracing
  trace_groups: [
    {
      name: "api-flow"
      log_viewers: ["nginx-logs", "api-logs", "db-logs", "cache-logs"]
    }
    {
      name: "auth-flow"
      log_viewers: ["nginx-logs", "auth-logs", "db-logs"]
    }
  ]
}
```

### 11.2 CLI Commands

#### Execute a Trace

```bash
trellis-ctl trace <id> <group> [options]
```

**Arguments:**
- `<id>` - The trace ID or grep pattern to search for
- `<group>` - Name of the trace group to search

**Options:**
- `-since <time>` - Start time (see time formats below)
- `-until <time>` - End time (default: now)
- `-name <name>` - Report name (default: auto-generated)
- `-no-expand-by-id` - Disable ID expansion (see section 11.6)

**Time Input Formats:**
- Duration: `1h`, `30m`, `2d` (relative to now)
- Clock time: `6:00am`, `14:30` (today)
- Date: `2025-01-10` (inclusive - see below)
- Date + time: `2025-01-10 6:00am`, `2025-01-10 14:30`
- Keywords: `today`, `yesterday`, `now`

**Important: Date ranges are inclusive.** When using a date without a time:
- For start time: the date means the **start** of that day (00:00:00)
- For end time: the date means the **end** of that day (23:59:59)

To search a **single day**, use the same date for both `-since` and `-until`:
```bash
trellis-ctl trace abc123 api-flow -since 2025-01-10 -until 2025-01-10
```

**Examples:**
```bash
trellis-ctl trace abc123 api-flow -since 1h
trellis-ctl trace "user-456" auth-flow -since 6:00am -until 7:00am
trellis-ctl trace abc123 api-flow -name "debug-session-1"
trellis-ctl trace abc123 api-flow -since 2025-01-10 -until 2025-01-10  # Single day
trellis-ctl trace abc123 api-flow -since yesterday -until yesterday    # Yesterday only
trellis-ctl trace abc123 api-flow -since 1h -no-expand-by-id           # Skip ID expansion
```

#### View Trace Reports

```bash
# Get a specific report
trellis-ctl trace-report <name>
trellis-ctl trace-report debug-session-1 -json

# List all reports
trellis-ctl trace-report -list

# List configured trace groups
trellis-ctl trace-report -groups

# Delete a report
trellis-ctl trace-report -delete <name>
```

### 11.3 API Endpoints

#### Execute Trace

Starts an asynchronous trace search. Returns immediately with "running" status. Poll the report endpoint to check completion status.

```
POST /api/v1/trace
{
  "id": "abc123",
  "group": "api-flow",
  "start": "2024-01-15T10:00:00Z",
  "end": "2024-01-15T11:00:00Z",
  "name": "debug-session-1",
  "expand_by_id": true
}
```

**Parameters:**
- `id` (required) - Trace ID or grep pattern to search for
- `group` (required) - Name of the trace group to search
- `start` (required) - Start time in RFC3339 format
- `end` (optional) - End time in RFC3339 format (default: now)
- `name` (optional) - Report name (auto-generated if not provided)
- `expand_by_id` (optional) - Enable ID expansion two-pass search (default: true)

```
Response:
{
  "data": {
    "name": "debug-session-1",
    "status": "running"
  }
}
```

Poll `GET /api/v1/trace/reports/{name}` to check when the trace completes. The report's `status` field will be "running", "completed", or "failed".

#### Get Trace Report

```
GET /api/v1/trace/reports/{name}

Response:
{
  "data": {
    "version": "1.0",
    "name": "debug-session-1",
    "trace_id": "abc123",
    "group": "api-flow",
    "status": "completed",
    "created_at": "2024-01-15T11:30:00Z",
    "time_range": {
      "start": "2024-01-15T10:00:00Z",
      "end": "2024-01-15T11:00:00Z"
    },
    "summary": {
      "total_entries": 47,
      "by_source": {"nginx-logs": 12, "api-logs": 25, "db-logs": 10},
      "by_level": {"INFO": 40, "ERROR": 5, "WARN": 2},
      "duration_ms": 1523
    },
    "entries": [
      {
        "timestamp": "2024-01-15T10:30:00.123Z",
        "source": "nginx-logs",
        "level": "INFO",
        "message": "Request abc123 received from 10.0.0.1",
        "fields": {"host": "api.example.com"},
        "raw": "...",
        "is_context": false
      }
    ],
    "error": ""
  }
}
```

**Status values:**
- `running` - Trace is still executing
- `completed` - Trace finished successfully
- `failed` - Trace failed (check `error` field for details)

#### List Trace Reports

```
GET /api/v1/trace/reports

Response:
{
  "data": {
    "reports": [
      {"name": "debug-session-1", "trace_id": "abc123", "group": "api-flow", "status": "completed", "created_at": "...", "entry_count": 47},
      {"name": "trace-xyz-20240115", "trace_id": "xyz789", "group": "auth-flow", "status": "running", "created_at": "...", "entry_count": 0}
    ]
  }
}
```

#### List Trace Groups

```
GET /api/v1/trace/groups

Response:
{
  "data": {
    "groups": [
      {"name": "api-flow", "log_viewers": ["nginx-logs", "api-logs", "db-logs"]}
    ]
  }
}
```

#### Delete Trace Report

```
DELETE /api/v1/trace/reports/{name}
```

### 11.4 Events

Trace operations emit events for monitoring and notifications:

| Event | Payload |
|-------|---------|
| `trace.started` | `{name, trace_id, group, log_viewers}` |
| `trace.completed` | `{name, trace_id, group, total_entries, duration_ms, report_path}` |
| `trace.failed` | `{name, trace_id, group, error}` |

Users can subscribe to `trace.completed` in their notification config to be notified when traces finish.

### 11.5 Implementation Details

**Parallel Execution:**

Trace searches all log viewers in the group in parallel using `errgroup` for optimal performance. Results are collected, merged by timestamp, and saved as a JSON report.

**Search Method:**

Traces use the same `GetHistoricalEntries()` method as log viewer history searches, which performs server-side grep filtering with optional context lines (-B/-A).

**Report Storage:**

Reports are saved as JSON files in the configured `reports_dir`. Old reports are automatically cleaned up based on the `max_age` setting.

### 11.6 ID Expansion

When `expand_by_id` is enabled (default), trace performs a two-pass search to capture related log entries:

**Pass 1:** Search for the trace pattern across all log viewers in the group.

**Pass 2:** Extract unique IDs from Pass 1 results (using each log viewer's `id` field), then search for all those IDs.

This captures related log entries that share the same request/session ID even if they don't contain the original trace pattern.

**Configuration:**

Each log viewer's parser can optionally specify an `id` field indicating which parsed field contains the entry's ID:

```hjson
log_viewers: [
  {
    name: "api-logs"
    source: { ... }
    parser: {
      type: "json"
      timestamp: "ts"
      level: "level"
      message: "msg"
      id: "request_id"  // Field containing request ID
    }
  }
]
```

Log viewers without `parser.id` configured are searched in Pass 1 only; their results go directly to the final report.

**Example:**

Trace for "user-login-error" with `expand_by_id: true`:
1. Pass 1 finds 3 entries in `api-logs` (parser.id: "request_id") with IDs: ["req-001", "req-002"]
2. Pass 2 searches for "req-001" and "req-002" across all log viewers
3. Final results include all entries from Pass 2 (which includes Pass 1 matches) plus entries from log viewers without `parser.id` configured

**Disabling:**

To disable ID expansion and perform a simple single-pass search:
- CLI: `trellis-ctl trace abc123 api-flow -no-expand-by-id`
- API: `"expand_by_id": false` in request body
- Web UI: Uncheck "Expand by ID" checkbox

---

## 12. Web Interface

### 12.1 UI Configuration

```hjson
{
  ui: {
    // Theme: "light", "dark", or "auto" (follow system)
    theme: "auto"

    // Terminal settings
    terminal: {
      font_family: "Monaco, monospace"
      font_size: 14
      cursor_blink: true
    }

    // Notification settings
    notifications: {
      enabled: true
      // Events that trigger browser notifications
      events: ["service.crashed", "workflow.finished"]
      // Only notify on failures (for workflow events)
      failures_only: true
      // Play sound on notification
      sound: false
    }

    // External editor integration
    editor: {
      // If set, generate vscode-remote:// URLs for clicking error links.
      // This is the SSH hostname used to connect to the machine running Trellis.
      // Example: "devbox.example.com" or "myserver"
      // When unset (local Trellis), uses vscode://file URLs instead.
      remote_host: "devbox.example.com"
    }
  }
}
```

### 12.2 UI Routes

| Route | Description |
|-------|-------------|
| `/` | Redirects to default terminal window |
| `/status` | Service status overview with start/stop controls |
| `/worktrees` | Worktree list and switcher |
| `/trace` | Distributed tracing interface |
| `/trace/report/{name}` | View a specific trace report |
| `/events` | Event stream viewer |
| `/terminal/local/{worktree}/{window}` | Local terminal window (e.g., `/terminal/local/main/dev`) |
| `/terminal/remote/{name}` | Remote terminal window (e.g., `/terminal/remote/admin(1)`) |
| `/terminal/service/{name}` | Service log viewer (e.g., `/terminal/service/api`) |
| `/terminal/logviewer/{name}` | Log viewer (e.g., `/terminal/logviewer/nginx-logs`) |
| `/terminal/editor/{worktree}` | VS Code editor for worktree (e.g., `/terminal/editor/main`) |
| `/terminal/output/{worktree}` | Workflow output viewer (e.g., `/terminal/output/main`) |
| `/services/{name}` | Legacy service detail page |

**URL structure:**

The terminal URLs use a consistent `/{type}/{identifier}` pattern:

- **Local terminals**: `/terminal/local/{worktree}/{window}` - The worktree name is `main` for the main worktree, or the worktree directory name for feature branches. Window names come from `terminal.default_windows[].name`.

- **Remote terminals**: `/terminal/remote/{name}` - The name comes from `terminal.remote_windows[].name`.

- **Service logs**: `/terminal/service/{name}` - The name is the service name from `services[].name`. When the service has `logging.parser` configured, displays structured logs in a table with filtering; otherwise shows raw log output.

- **Editor**: `/terminal/editor/{worktree}` - Opens VS Code for the specified worktree.

- **Workflow output**: `/terminal/output/{worktree}` - Shows workflow execution results for the specified worktree.

**Legacy redirect**: Old URLs like `/terminal/{session}/{window}` are automatically redirected to the new format.

### 12.3 Navigation Picker

All navigation in Trellis is done through a unified picker dropdown in the navbar. The picker provides quick access to all destinations using keyboard shortcuts and a searchable dropdown.

**Picker contents (in order):**

| Prefix | Type | Example |
|--------|------|---------|
| `/` | Pages | `/ Status`, `/ Worktrees`, `/ Trace`, `/ Events` |
| `@` | Local terminals | `@main - dev`, `@feature-auth - claude` |
| `!` | Remote terminals | `!admin(1)` |
| `#` | Services | `#api - service`, `#worker - service` |
| `~` | Log viewers | `~nginx-logs - logs`, `~api-logs - logs` |
| `>` | External links | `> Grafana`, `> Docs` |

**Opening the picker:**
- Click the dropdown in the navbar
- Press `Cmd/Ctrl + P` to open and focus the picker
- Type to filter/search destinations

### 12.4 Keyboard Shortcuts

Keyboard shortcuts available in the web interface:

| Shortcut | Action |
|----------|--------|
| `Cmd/Ctrl + P` | Open navigation picker |
| `Cmd/Ctrl + Backspace` | Open history picker (recently visited screens) |
| `Cmd/Ctrl + E` | Toggle between terminal and VS Code editor |
| `Cmd/Ctrl + H` | Show keyboard shortcut help |
| `Ctrl + Escape` | Return from editor iframe to terminal (same-origin only) |

**History Picker (Cmd+Backspace):**

The history picker shows recently visited screens for quick back-navigation:

- **Cross-page history**: Navigation history is shared across all pages via session storage
- **Most recent first**: The screen you just left appears at the top
- **Up to 50 entries**: History is limited to prevent unbounded growth
- **Current screen excluded**: The screen you're currently viewing is not shown

Press Enter to select the highlighted item, use arrow keys to navigate, or Escape to cancel.

**Note:** Links open in separate browser tabs and do not affect the navigation history within Trellis.

### 11.3.1 Editor Integration

Build errors and test failures display clickable links that open the file in VS Code at the error location.

**URL generation:**

| `editor.remote_host` | URL format |
|---------------------|------------|
| Unset (local) | `vscode://file/path/to/file:line:column` |
| Set to hostname | `vscode://vscode-remote/ssh-remote+hostname/path/to/file:line:column` |

For remote Trellis, set `remote_host` to the SSH hostname you use to connect:

```hjson
{
  ui: {
    editor: {
      remote_host: "devbox.example.com"
    }
  }
}
```

**Note:** Remote links require VS Code with the Remote-SSH extension installed. The hostname must match your SSH config.

### 11.4 Dashboard Components

```
┌─────────────────────────────────────────────────────────────┐
│  Trellis - myproject (feature/auth)              [Worktree▼]│
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Services                              Recent Events        │
│  ┌─────────────────────────────┐      ┌──────────────────┐ │
│  │ ● api         running       │      │ 14:32 api crash  │ │
│  │ ● worker      running       │      │ 14:30 build ok   │ │
│  │ ○ scheduler   stopped       │      │ 14:28 test fail  │ │
│  │ ✗ mailer      crashed       │      │ 14:15 worker ok  │ │
│  └─────────────────────────────┘      └──────────────────┘ │
│                                                             │
│  Terminals: [dev] [claude] [admin(1)] [admin(2)]           │
│                                                             │
│  Recent: Build All ✓ 14:30 | Run Tests ✗ 14:28             │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

Clicking a terminal button opens a dedicated browser window:

```
┌─────────────────────────────────────────────────────────────┐
│  [Terminal: claude ▼]                      [Open Code]      │
├─────────────────────────────────────────────────────────────┤
│  $ claude                                                   │
│  Claude Code v1.0                                           │
│  > _                                                        │
│                                                             │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

- **Terminal dropdown**: Switch between terminals without leaving the window
- **Open Code button**: Opens VS Code for the current worktree (hidden for remote terminals)
  - Uses `vscode://` or `vscode-remote://` URL based on `editor.remote_host` config

Terminal windows use xterm.js and connect via WebSocket (see section 9.4).

### 11.5 Real-time Updates

The UI uses WebSocket for real-time updates:

- Service status changes
- Workflow progress
- Log streaming
- Event notifications

Connection: `ws://localhost:1000/api/v1/events/ws`

Message format:
```json
{
  "type": "event",
  "event": { /* Event object (see section 6.2) */ }
}
```

### 11.6 Notifications

Browser notifications for critical events (configured in `ui.notifications`, see section 10.1):

| Event | Notification |
|-------|--------------|
| `service.crashed` | "Service {name} crashed: {reason}" |
| `workflow.finished` (failure) | "Workflow {name} failed" |

Notifications require browser permission. Trellis will prompt on first visit.

---

## 12. API

### 12.1 REST API

All endpoints return JSON. Prefix: `/api/v1`

#### Services

```
GET    /api/v1/services                    # List all services
GET    /api/v1/services/:name              # Get service details
POST   /api/v1/services/:name/start        # Start service
POST   /api/v1/services/:name/stop         # Stop service
POST   /api/v1/services/:name/restart      # Restart service
GET    /api/v1/services/:name/logs         # Get log buffer
DELETE /api/v1/services/:name/logs         # Clear log buffer
```

#### Worktrees

```
GET    /api/v1/worktrees                   # List worktrees
GET    /api/v1/worktrees/:name             # Get worktree details
POST   /api/v1/worktrees/:name/activate    # Switch to worktree
```

#### Workflows

```
GET    /api/v1/workflows                   # List workflows
POST   /api/v1/workflows/:id/run           # Run workflow (?worktree=<name> optional)
GET    /api/v1/workflows/:runID/status     # Workflow run status
GET    /api/v1/workflows/:runID/stream     # WebSocket: stream workflow output
```

#### Events

```
GET    /api/v1/events                      # Event history (with filters)
```

#### Notifications

```
POST   /api/v1/notify                      # Emit a notification event
```

Request body:
```json
{
  "message": "Task completed",
  "type": "done"
}
```

- `message` (required): Human-readable notification text
- `type` (optional): One of `done` (default), `blocked`, `error`

Response:
```json
{
  "id": "evt_abc123",
  "type": "notify.done",
  "timestamp": "2024-01-15T14:32:01Z"
}
```

#### Crash Reports

```
GET    /api/v1/crashes                     # List all crashes
GET    /api/v1/crashes/newest              # Get most recent crash
GET    /api/v1/crashes/{id}                # Get specific crash
DELETE /api/v1/crashes/{id}                # Delete a crash
DELETE /api/v1/crashes                     # Clear all crashes
```

#### Terminal

```
GET    /api/v1/terminal/sessions           # List tmux sessions
```

### 12.2 WebSocket API

#### Terminal Stream

For worktree terminals:
```
WS /api/v1/terminal/ws?session=X&window=Y
```

For remote terminals:
```
WS /api/v1/terminal/ws?remote=X
```

Where `X` is the remote window name from `terminal.remote_windows[].name`.

Messages:
- Server → Client: `{"type": "output", "data": "base64..."}`
- Client → Server: `{"type": "input", "data": "base64..."}`
- Client → Server: `{"type": "resize", "cols": 80, "rows": 24}`

#### Event Stream
```
WS /api/v1/events/ws
```

Messages:
- Server → Client: `{"type": "event", "event": {...}}`
- Client → Server: `{"type": "subscribe", "patterns": ["service.*"]}`

### 12.3 Response Format

Success:
```json
{
  "data": { /* response data */ },
  "meta": {
    "timestamp": "2024-01-15T14:32:01Z"
  }
}
```

Error:
```json
{
  "error": {
    "code": "SERVICE_NOT_FOUND",
    "message": "Service 'foo' not found",
    "details": {}
  }
}
```

### 12.4 Go Client Library

Trellis provides an official Go client library at `pkg/client` for programmatic access to the API. The library provides typed access to all endpoints and is used internally by `trellis-ctl`.

#### Installation

```bash
go get github.com/wingedpig/trellis/pkg/client
```

#### Basic Usage

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/wingedpig/trellis/pkg/client"
)

func main() {
    // Create a client
    c := client.New("http://localhost:8080")

    ctx := context.Background()

    // List all services
    services, err := c.Services.List(ctx)
    if err != nil {
        log.Fatal(err)
    }

    for _, svc := range services {
        fmt.Printf("%s: %s\n", svc.Name, svc.Status.State)
    }

    // Start a service
    svc, err := c.Services.Start(ctx, "backend")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Started %s (PID: %d)\n", svc.Name, svc.Status.PID)
}
```

#### API Versioning

The client supports Stripe-style date-based API versioning. By default, the latest version is used. Pin to a specific version for stability:

```go
c := client.New("http://localhost:8080", client.WithVersion("2026-01-17"))
```

The version is sent via the `Trellis-Version` HTTP header on each request.

#### Configuration Options

```go
c := client.New("http://localhost:8080",
    client.WithVersion("2026-01-17"),      // Pin API version
    client.WithTimeout(60 * time.Second),  // Custom timeout (default: 30s)
    client.WithHTTPClient(customClient),   // Custom http.Client
)
```

#### Available Sub-Clients

| Sub-Client | Description |
|------------|-------------|
| `c.Services` | Service management (list, get, start, stop, restart, logs) |
| `c.Worktrees` | Worktree operations (list, get, activate, remove) |
| `c.Workflows` | Workflow execution (list, get, run, status) |
| `c.Events` | Event log access (list with filters) |
| `c.Logs` | Log viewer operations (list viewers, get entries, history) |
| `c.Trace` | Distributed tracing (execute, list/get/delete reports, list groups) |
| `c.Notify` | Notifications (send) |

#### Service Operations

```go
// List all services
services, _ := c.Services.List(ctx)

// Get a specific service
svc, _ := c.Services.Get(ctx, "backend")

// Start/stop/restart
svc, _ := c.Services.Start(ctx, "backend")
svc, _ := c.Services.Stop(ctx, "backend")
svc, _ := c.Services.Restart(ctx, "backend")

// Get log buffer (raw JSON)
logs, _ := c.Services.Logs(ctx, "backend", 100)

// Clear log buffer
_ = c.Services.ClearLogs(ctx, "backend")
```

#### Worktree Operations

```go
// List all worktrees
worktrees, _ := c.Worktrees.List(ctx)

// Get a specific worktree
wt, _ := c.Worktrees.Get(ctx, "feature-branch")

// Activate a worktree (switches active environment)
result, _ := c.Worktrees.Activate(ctx, "feature-branch")
fmt.Printf("Activated %s in %s\n", result.Worktree.Name(), result.Duration)

// Remove a worktree
_ = c.Worktrees.Remove(ctx, "old-branch", &client.RemoveOptions{
    DeleteBranch: true,  // Also delete the git branch
})
```

#### Workflow Operations

```go
// List workflows
workflows, _ := c.Workflows.List(ctx)

// Run a workflow (returns immediately)
status, _ := c.Workflows.Run(ctx, "build", nil)

// Run in a specific worktree
status, _ := c.Workflows.Run(ctx, "build", &client.RunOptions{
    Worktree: "feature-branch",
})

// Poll for completion
for status.State == client.WorkflowStateRunning {
    time.Sleep(500 * time.Millisecond)
    status, _ = c.Workflows.Status(ctx, status.ID)
}

if status.Success {
    fmt.Println("Workflow completed successfully")
    fmt.Println(status.Output)
}
```

#### Event Operations

```go
// List recent events
events, _ := c.Events.List(ctx, &client.ListOptions{
    Limit: 50,
})

// Filter by type and time range
events, _ := c.Events.List(ctx, &client.ListOptions{
    Types:    []string{"service.started", "service.stopped"},
    Since:    time.Now().Add(-1 * time.Hour),
    Worktree: "main",
})
```

#### Log Viewer Operations

```go
// List configured log viewers
viewers, _ := c.Logs.List(ctx)

// Get entries from live buffer
entries, _ := c.Logs.GetEntries(ctx, "nginx", &client.LogEntriesOptions{
    Limit: 100,
    Since: time.Now().Add(-1 * time.Hour),
    Level: "error",
})

// Get historical entries (from rotated files)
entries, _ := c.Logs.GetHistoryEntries(ctx, "nginx", &client.LogEntriesOptions{
    Grep:   "connection refused",
    Before: 3,  // Context lines before match
    After:  3,  // Context lines after match
})
```

#### Distributed Tracing

```go
// Execute a trace query
result, _ := c.Trace.Execute(ctx, &client.TraceRequest{
    ID:         "req-abc123",       // Correlation ID to search for
    Group:      "web",              // Trace group (set of log viewers)
    Start:      time.Now().Add(-1 * time.Hour),
    End:        time.Now(),
    ExpandByID: true,               // Two-pass search for related entries
})

// Poll for completion
for {
    report, _ := c.Trace.GetReport(ctx, result.Name)
    if report.Status == "completed" {
        fmt.Printf("Found %d entries\n", report.Summary.TotalEntries)
        for _, entry := range report.Entries {
            fmt.Printf("[%s] %s: %s\n", entry.Source, entry.Level, entry.Message)
        }
        break
    }
    time.Sleep(500 * time.Millisecond)
}

// List saved reports
reports, _ := c.Trace.ListReports(ctx)

// Delete a report
_ = c.Trace.DeleteReport(ctx, "trace-2026-01-17-abc123")

// List trace groups
groups, _ := c.Trace.ListGroups(ctx)
```

#### Notifications

```go
// Send a notification
_, _ = c.Notify.Send(ctx, "Build complete!", client.NotifyDone)
_, _ = c.Notify.Send(ctx, "Waiting for input", client.NotifyBlocked)
_, _ = c.Notify.Send(ctx, "Build failed", client.NotifyError)
```

#### Error Handling

API errors are returned as `*client.APIError`:

```go
svc, err := c.Services.Get(ctx, "unknown-service")
if err != nil {
    if apiErr, ok := err.(*client.APIError); ok {
        fmt.Printf("API error: %s - %s\n", apiErr.Code, apiErr.Message)
        // Common codes: "not_found", "invalid_request", "conflict"
    }
}
```

#### Types Reference

Key types exported by the client library:

| Type | Description |
|------|-------------|
| `Service` | Service definition and status |
| `ServiceStatus` | Runtime state (State, PID, ExitCode, etc.) |
| `Worktree` | Git worktree info (Path, Branch, Commit, Dirty, etc.) |
| `Workflow` | Workflow definition (ID, Name, Command, etc.) |
| `WorkflowStatus` | Execution status (State, Success, Output, etc.) |
| `Event` | Event log entry (Type, Timestamp, Payload) |
| `LogViewer` | Log viewer definition (Name, Description) |
| `LogEntry` | Parsed log entry (Timestamp, Level, Message, Fields) |
| `TraceRequest` | Trace query parameters |
| `TraceReport` | Complete trace results with entries |
| `TraceGroup` | Group of log viewers for tracing |

See the package documentation (`go doc github.com/wingedpig/trellis/pkg/client`) for complete API reference.

---

## 13. Workflows

### 13.1 Workflow Definition

Workflows are user-triggered actions:

```hjson
{
  workflows: [
    {
      // Required: unique identifier
      id: "test-full"

      // Required: display name
      name: "Run All Tests"

      // Command to run (use either command OR commands, not both)
      // Must be an array where each element is a separate argument.
      // Templates ({{.Worktree.*}}) are expanded before execution.
      // For shell features, use: ["sh", "-c", "command | other"]
      command: ["go", "test", "-json", "-count=1", "./..."]

      // OR: Multiple commands to run in sequence
      // Each command is an array of arguments
      // Commands execute sequentially; if one fails, subsequent commands are skipped
      // Output shows headers like "=== Command 1/3: ... ===" between commands
      commands: [
        ["make", "clean"],
        ["make", "build"],
        ["make", "test"]
      ]

      // Optional: timeout (default: no timeout)
      timeout: "10m"

      // Optional: output parser (default: "none")
      output_parser: "go_test_json"

      // Optional: require user confirmation before running (default: false)
      confirm: false

      // Optional: custom confirmation message
      confirm_message: "Are you sure?"

      // Optional: specific services that must be stopped before running
      requires_stopped: []

      // Optional: stop/restart watched services (default: false)
      // When true: stops services with watching=true → runs workflow → restarts them (if successful)
      // Services with watching: false (e.g., databases) are NOT stopped/restarted
      restart_services: false
    }
  ]
}
```

**Example workflows:**

```hjson
{
  workflows: [
    {
      id: "test"
      name: "Run Tests"
      command: ["go", "test", "-json", "-count=1", "./..."]
      output_parser: "go_test_json"
      timeout: "10m"
    }
    {
      id: "db-reset"
      name: "Reset Database"
      command: ["{{.Worktree.Root}}/bin/dbutil", "ALL", "reset"]
      confirm: true
      confirm_message: "This will delete all data. Continue?"
      requires_stopped: ["api", "worker"]
      restart_services: true
    }
    {
      id: "build"
      name: "Build All"
      command: ["make", "build"]
      output_parser: "go"
    }
    {
      id: "full-rebuild"
      name: "Full Rebuild"
      // Multiple commands run in sequence
      commands: [
        ["make", "clean"],
        ["make", "generate"],
        ["make", "build"]
      ]
      output_parser: "go"
      restart_services: true
    }
  ]
}
```

### 13.2 Output Parsers

| Parser | Input Format | Extracts |
|--------|--------------|----------|
| `go` | Go compiler output | File, line, column, message |
| `go_test_json` | `go test -json` | Package, test, pass/fail, output |
| `typescript` | TypeScript compiler | File, line, column, message |
| `jest` | Jest JSON output | Test suites, tests, failures |
| `generic` | Line-based | Patterns matching `file:line: message` |
| `html` | Raw HTML | None (output passed through as-is, no escaping) |
| `none` | Raw output | No parsing |

**Note:** The `html` parser is useful when a workflow command outputs pre-formatted HTML. The output is displayed directly without HTML escaping or link formatting. Use with caution - only use with trusted commands.

### 13.3 Workflow Execution and Streaming

Workflows execute asynchronously and stream output in real-time via WebSocket:

**Worktree context:**

Workflows execute in the **viewed worktree's directory**, not necessarily the active worktree. This allows running builds and tests in a specific worktree while viewing its terminal, even if a different worktree is "active" (running services).

- **Terminal page**: Workflows run in the worktree being viewed (passed via `?worktree=` query parameter)
- **Other pages** (dashboard, status, workflows): Workflows run in the active worktree (no worktree parameter)

The working directory is set to the worktree root (e.g., `/home/user/project` for main, `/home/user/project-feature` for a worktree).

**Execution model:**
1. If `confirm: true`, UI shows confirmation dialog and waits for user approval
2. If `restart_services: true`, UI calls `_stop_watched` to stop watched services first
3. Client calls `POST /api/v1/workflows/:id/run?worktree=<name>` (worktree parameter optional)
4. Server resolves worktree name to path (or uses active worktree if not specified)
5. Server stops any `requires_stopped` services (if specified)
6. Server returns immediately with a `runID`
7. Client opens WebSocket to `GET /api/v1/workflows/:runID/stream`
8. Workflow executes in background (in the specified worktree's directory):
   - For single command: runs the command, streaming stdout/stderr
   - For multiple commands: runs each command sequentially with headers (e.g., `=== Command 1/3: make clean ===`)
   - If any command fails, subsequent commands are skipped
9. Server streams output line-by-line over WebSocket as it arrives
10. When workflow completes, server sends final status and closes connection
11. If `restart_services: true` and workflow succeeded, server restarts watched services

**Note:** Services with `watching: false` (e.g., databases, external tools) are never stopped/restarted by `restart_services`. Only services with `watching: true` (default) are affected.

**WebSocket streaming:**

The client connects to `/api/v1/workflows/:runID/stream` to receive real-time output:

**Output message (sent for each line):**
```json
{
  "type": "output",
  "line": "Compiling main.go...\n"
}
```

**Done message (sent when workflow completes):**
```json
{
  "type": "done",
  "status": {
    "ID": "build-1234567890",
    "Name": "Build All",
    "State": "success",
    "Success": true,
    "Output": "...",
    "OutputHTML": "...",
    "Duration": "2.5s"
  }
}
```

**Output fields in final status:**

| Field | Description |
|-------|-------------|
| `Output` | Raw text output |
| `OutputHTML` | HTML-formatted output with clickable file links |
| `ParsedLines` | Structured parsing results |

**Link generation:**

The HTML formatter recognizes these patterns and generates clickable links:
- Go compiler errors: `file.go:10:5: error message`
- QTC errors: `qtc: ... at file "path", line N, pos M`

Links call `openFileAtLine()` which opens the file in VS Code at the specified location.

**Fallback polling:**

For clients that don't support WebSocket, `GET /api/v1/workflows/:runID/status` can be polled (250ms recommended):

```json
{
  "data": {
    "ID": "build-1234567890",
    "Name": "Build All",
    "State": "running",
    "Output": "Compiling...\nmain.go:10:5: undefined: foo\n",
    "OutputHTML": "Compiling...<br><a href=\"#\" onclick=\"openFileAtLine('main.go:10:5')\">main.go:10:5</a>: undefined: foo<br>"
  }
}
```

### 13.4 Built-in Workflows

Trellis provides these built-in workflows (can be overridden):

| ID | Name | Description |
|----|------|-------------|
| `_restart_all` | Restart All Services | Stop and start all services |
| `_stop_all` | Stop All Services | Stop all running services |
| `_stop_watched` | Stop Watched Services | Stop services with `watching: true` only |
| `_clear_logs` | Clear Logs | Clear all service log buffers |

---

## 14. Observability

### 14.1 Logging

Trellis logs to stderr with structured JSON:

```json
{
  "timestamp": "2024-01-15T14:32:01.123Z",
  "level": "info",
  "message": "Service started",
  "service": "api",
  "pid": 12345,
  "worktree": "main"
}
```

Log levels: `debug`, `info`, `warn`, `error`

Configuration:
```hjson
{
  logging: {
    level: "info"
    format: "json"  // or "text"
  }
}
```

---

## 15. Security

### 15.1 Network Binding

By default, Trellis binds to `127.0.0.1` only:

```hjson
{
  server: {
    host: "127.0.0.1"  // localhost only
    port: 1000
  }
}
```

### 15.2 TLS/HTTPS

Trellis supports HTTPS for secure remote access. This is required when accessing Trellis over a network (e.g., via Tailscale) because HTTP triggers browser security warnings that disable clipboard access, service workers, and other features.

**Configuration:**

```hjson
{
  server: {
    host: "0.0.0.0"  // Allow remote access
    port: 1000
    tls_cert: "~/.trellis/cert.pem"
    tls_key: "~/.trellis/key.pem"
  }
}
```

Both `tls_cert` and `tls_key` must be set, and both files must exist. Paths support `~` expansion.

**Generating certificates:**

Use **mkcert** for local development (creates locally-trusted certificates):

```bash
# Install mkcert and set up local CA (one-time setup)
brew install mkcert
mkcert -install

# Generate certificate for your hostnames
mkdir -p ~/.trellis
mkcert -cert-file ~/.trellis/cert.pem -key-file ~/.trellis/key.pem \
  localhost 127.0.0.1 ::1 \
  $(hostname) \
  your-machine.tailnet.ts.net  # Add your Tailscale hostname if needed
```

For **Tailscale** access, you can also use Tailscale's built-in cert provisioning:

```bash
# Generates certs signed by Tailscale's CA (trusted by your tailnet devices)
tailscale cert --cert-file ~/.trellis/cert.pem --key-file ~/.trellis/key.pem \
  your-machine.tailnet.ts.net
```

**Startup logging:**

```
# HTTP (no TLS configured)
API server listening on http://0.0.0.0:1000

# HTTPS (TLS configured)
API server listening on https://0.0.0.0:1000 (TLS enabled)
```

### 15.3 Command Execution

Trellis executes commands defined in configuration. Security considerations:

- Configuration files should be trusted (checked into git)
- Commands are executed directly via `exec`, **not through a shell**
- User-supplied input (API) is never interpolated into commands

**Command execution modes:**

| Format | Execution | Template Expansion |
|--------|-----------|-------------------|
| String: `"./bin/api -p 8080"` | Split on whitespace, then exec | Before splitting |
| Array: `["./bin/api", "-p", "8080"]` | Exec directly (no splitting) | Per-element |

**Important:** Since commands are not executed through a shell:
- Shell features (pipes, redirects, `&&`) require explicit `sh -c` wrapper
- The `quote` function is **only useful inside `sh -c` commands**
- For direct exec, spaces in paths require array form, not quoting

**Template variable safety:**

| Source | Trust Level | Notes |
|--------|-------------|-------|
| Config file values | Trusted | Author controls content |
| `{{.Worktree.Root}}` | Trusted | Filesystem path from git |
| `{{.Worktree.Branch}}` | Semi-trusted | May contain spaces/special chars |

**Handling special characters:**

```hjson
// WRONG: quote does nothing useful here (direct exec, not shell)
command: "{{.Worktree.Root | quote}}/bin/api"

// CORRECT: Use array form for paths that might have spaces
command: ["{{.Worktree.Root}}/bin/api", "-config", "{{.Worktree.Root}}/config/api.json"]

// CORRECT: Use quote only inside sh -c
command: ["sh", "-c", "cd {{.Worktree.Root | quote}} && make build"]

// CORRECT: Use slugify to sanitize branch names for safe use anywhere
command: ["./deploy.sh", "{{.Worktree.Branch | slugify}}"]
```

### 15.4 File System Access

Trellis operates within:
- Worktree directories
- Configured paths only

It does not:
- Serve arbitrary files
- Execute commands outside configured scope
- Write outside configured directories

---

## 16. Implementation Guide

### 16.1 Technology Stack

Recommended implementation:

| Component | Technology |
|-----------|------------|
| Language | Go |
| HTTP Router | gorilla/mux or chi |
| WebSocket | gorilla/websocket |
| File Watching | fsnotify |
| Template Engine | quicktemplate |
| Config Parsing | hjson-go |
| Terminal | tmux (via exec) |
| CSS Framework | Bootstrap 5 |
| Icons | Font Awesome (free) |
| Terminal UI | xterm.js |

### 16.2 Package Structure

```
trellis/
├── cmd/
│   └── trellis/
│       └── main.go           # Entry point
├── internal/
│   ├── config/
│   │   ├── loader.go         # HJSON loading
│   │   ├── schema.go         # Config structs
│   │   └── template.go       # Variable expansion
│   ├── service/
│   │   ├── manager.go        # Service lifecycle
│   │   ├── process.go        # Process management
│   │   ├── health.go         # Ready checks
│   │   └── logs.go           # Ring buffer
│   ├── watcher/
│   │   └── binary.go         # Binary file watching
│   ├── events/
│   │   ├── bus.go            # Event bus interface
│   │   ├── memory.go         # In-process transport
│   │   ├── types.go          # Event definitions
│   │   └── history.go        # Event retention
│   ├── worktree/
│   │   ├── manager.go        # Worktree discovery
│   │   └── git.go            # Git operations
│   ├── crashes/
│   │   ├── types.go          # Crash data structures
│   │   └── manager.go        # Crash storage and cleanup
│   ├── terminal/
│   │   ├── tmux.go           # Tmux operations
│   │   └── stream.go         # WebSocket streaming
│   ├── workflow/
│   │   ├── runner.go         # Workflow execution
│   │   └── parser.go         # Output parsing (go, go_test_json, etc.)
│   ├── api/
│   │   ├── routes.go         # API endpoints
│   │   └── handlers.go       # Request handlers
│   └── ui/
│       └── server.go         # Static file serving
├── views/
│   ├── header.qtpl           # Common header (Bootstrap 5, Font Awesome)
│   ├── dashboard.qtpl        # Dashboard view
│   ├── services.qtpl         # Services list
│   ├── terminal.qtpl         # Terminal view
│   ├── worktrees.qtpl        # Worktree switcher
│   ├── workflows.qtpl        # Workflow runner
│   └── crashes.qtpl          # Crash history view
├── static/
│   ├── css/
│   │   └── xterm.css         # xterm.js styles
│   └── js/
│       └── xterm.js          # xterm.js terminal
├── docs/
│   └── spec.md               # This document
├── examples/
│   ├── go-project/
│   │   └── trellis.hjson
│   └── node-project/
│       └── trellis.hjson
├── go.mod
├── go.sum
└── README.md
```

### 16.2.1 Web Templates

Web pages use [quicktemplate](https://github.com/valyala/quicktemplate) for type-safe, compiled templates.

**Note:** Config file template variables (e.g., `{{.Worktree.Binaries}}`, `{{.Service.Name}}`) use Go's `text/template` syntax. Quicktemplate is only for HTML views.

**Common header template (`header.qtpl`):**

```qtpl
{% package views %}

{% import "trellis/internal/config" %}

{% code
type BaseData struct {
    Config    *config.Config
    Worktree  string
    Branch    string
}
%}

{% func (d *BaseData) Header(title string) %}
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Trellis | {%s title %}</title>

    <!-- Bootstrap 5 -->
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css"
          rel="stylesheet">

    <!-- Font Awesome (free) -->
    <link href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css"
          rel="stylesheet">
</head>
<body>
<nav class="navbar navbar-expand-lg navbar-dark bg-dark">
    <div class="container-fluid">
        <a class="navbar-brand" href="/">Trellis</a>
        <ul class="navbar-nav">
            <li class="nav-item">
                <a class="nav-link" href="/worktrees">
                    <i class="fa-solid fa-code-branch"></i> {%s d.Worktree %}
                </a>
            </li>
        </ul>
    </div>
</nav>
{% endfunc %}

{% func (d *BaseData) Footer() %}
<script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js"></script>
</body>
</html>
{% endfunc %}
```

**Dashboard template (`dashboard.qtpl`):**

```qtpl
{% package views %}

{% code
type DashboardData struct {
    BaseData
    Services  []ServiceStatus
    Events    []Event
    Workflows []WorkflowStatus
}
%}

{% func (d *DashboardData) Page() %}
{%= d.Header("Dashboard") %}

<div class="container-fluid mt-3">
    <div class="row">
        <!-- Services -->
        <div class="col-md-6">
            <div class="card">
                <div class="card-header">
                    <i class="fa-solid fa-server"></i> Services
                </div>
                <ul class="list-group list-group-flush">
                    {% for _, svc := range d.Services %}
                    <li class="list-group-item d-flex justify-content-between">
                        <span>
                            {% if svc.State == "running" %}
                            <i class="fa-solid fa-circle text-success"></i>
                            {% elseif svc.State == "crashed" %}
                            <i class="fa-solid fa-circle text-danger"></i>
                            {% else %}
                            <i class="fa-regular fa-circle text-muted"></i>
                            {% endif %}
                            {%s svc.Name %}
                        </span>
                        <span class="text-muted">{%s svc.State %}</span>
                    </li>
                    {% endfor %}
                </ul>
            </div>
        </div>

        <!-- Recent Events -->
        <div class="col-md-6">
            <div class="card">
                <div class="card-header">
                    <i class="fa-solid fa-clock-rotate-left"></i> Recent Events
                </div>
                <ul class="list-group list-group-flush">
                    {% for _, evt := range d.Events %}
                    <li class="list-group-item">
                        <small class="text-muted">{%s evt.Timestamp.Format("15:04") %}</small>
                        {%s evt.Type %}
                    </li>
                    {% endfor %}
                </ul>
            </div>
        </div>
    </div>

    <!-- Terminals -->
    <div class="row mt-3">
        <div class="col-12">
            <div class="btn-group" role="group">
                {% for _, term := range d.Config.Terminal.DefaultWindows %}
                <a href="/terminal/local/{%s d.Worktree %}/{%s term.Name %}"
                   class="btn btn-outline-secondary" target="_blank">
                    <i class="fa-solid fa-terminal"></i> {%s term.Name %}
                </a>
                {% endfor %}
            </div>
        </div>
    </div>
</div>

{%= d.Footer() %}
{% endfunc %}
```

**Terminal template (`terminal.qtpl`):**

```qtpl
{% package views %}

{% code
type TerminalData struct {
    BaseData
    Session   string
    Window    string
    Windows   []WindowInfo
    IsRemote  bool
}
%}

{% func (d *TerminalData) Page() %}
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Terminal: {%s d.Window %}</title>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css" rel="stylesheet">
    <link href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css" rel="stylesheet">
    <link href="/static/css/xterm.css" rel="stylesheet">
    <style>
        body { background: #1a1a1a; margin: 0; padding: 20px; }
        .controls { margin-bottom: 15px; display: flex; gap: 10px; }
        #terminal { height: calc(100vh - 80px); }
    </style>
</head>
<body>

<div class="controls">
    <select id="windowSelect" class="form-select" style="width: 300px; background: #2d2d2d; color: #fff; border-color: #444;">
        {% for _, w := range d.Windows %}
        <option value="{%s w.Session %}/{%s w.Name %}"
                {% if w.Name == d.Window %}selected{% endif %}>
            {%s w.DisplayName %}
        </option>
        {% endfor %}
    </select>

    {% if !d.IsRemote %}
    <button id="openCodeBtn" class="btn btn-secondary" onclick="openCode()">
        <i class="fa-solid fa-code"></i> Open Code
    </button>
    {% endif %}
</div>

<div id="terminal"></div>

<script src="/static/js/xterm.js"></script>
<script>
    const term = new Terminal({ cursorBlink: true, fontSize: 14 });
    term.open(document.getElementById('terminal'));

    const ws = new WebSocket(
        'ws://' + location.host + '/api/v1/terminal/ws?session={%s d.Session %}&window={%s d.Window %}'
    );

    ws.onmessage = (e) => term.write(e.data);
    term.onData((data) => ws.send(data));

    document.getElementById('windowSelect').onchange = function() {
        // Navigate to selected terminal (simplified - actual implementation parses value)
        window.location.href = '/terminal/local/' + this.value;
    };

    function openCode() {
        fetch('/api/v1/editor/open?session={%s d.Session %}')
            .then(r => r.json())
            .then(data => window.location.href = data.url);
    }

    // Cmd/Ctrl+E to open code
    document.addEventListener('keydown', (e) => {
        if ((e.metaKey || e.ctrlKey) && e.key === 'e' && !{%v d.IsRemote %}) {
            e.preventDefault();
            openCode();
        }
    });
</script>
</body>
</html>
{% endfunc %}
```

**Generating templates:**

```bash
# Install quicktemplate compiler
go install github.com/valyala/quicktemplate/qtc@latest

# Generate Go code from templates
qtc -dir=views/
```

### 16.2.2 Terminal Implementation

This section documents the production-tested terminal WebSocket implementation. Getting terminal streaming right requires careful handling of tmux pipe-pane, UTF-8 validation, reconnection, and keyboard input.

**Required JavaScript libraries:**
- [xterm.js](https://github.com/xtermjs/xterm.js) (v5.5.0+) - Terminal emulator
- [xterm-addon-fit](https://github.com/xtermjs/xterm.js/tree/master/addons/addon-fit) - Auto-resize
- [ReconnectingWebSocket](https://github.com/joewalnes/reconnecting-websocket) - Auto-reconnect
- [Select2](https://select2.org/) - Enhanced dropdown for terminal selector (searchable, keyboard navigation)

**Client-side JavaScript:**

```javascript
// Terminal state management
let terminals = {};  // Map of "session|window" -> {term, fitAddon, ws, container}
let currentTerminalKey = null;

function getOrCreateTerminal(terminalKey) {
    if (terminals[terminalKey]) {
        return terminals[terminalKey];
    }

    // Create container for this terminal
    const container = document.createElement('div');
    container.id = 'terminal-' + terminalKey.replace(/[^a-zA-Z0-9]/g, '-');
    container.className = 'terminal-container';
    container.style.display = 'none';
    document.getElementById('terminal-wrapper').appendChild(container);

    // Create xterm instance
    // Note: xterm.js renders the cursor, but tmux controls its position
    // via cursor control sequences sent through the pipe-pane stream
    const term = new Terminal({
        scrollback: 10000,
        fontSize: 13,
        fontFamily: '"JetBrains Mono", Monaco, monospace',
        theme: {
            background: '#000000',
            foreground: '#d4d4d4'
        },
        cursorBlink: true,
        cursorStyle: 'block',
        convertEol: true
    });

    const fitAddon = new FitAddon.FitAddon();
    term.loadAddon(fitAddon);
    term.open(container);
    fitAddon.fit();
    term.scrollToBottom();

    terminals[terminalKey] = {
        term: term,
        fitAddon: fitAddon,
        ws: null,
        container: container
    };

    return terminals[terminalKey];
}

// Handle window resize for all terminals
window.addEventListener('resize', () => {
    for (const key in terminals) {
        const termData = terminals[key];
        termData.fitAddon.fit();
        termData.term.scrollToBottom();

        // Send resize event to WebSocket if connected
        if (termData.ws && termData.ws.readyState === WebSocket.OPEN) {
            termData.ws.send(JSON.stringify({
                type: 'resize',
                rows: termData.term.rows,
                cols: termData.term.cols
            }));
        }
    }
});

function connectWebSocket(termData, session, windowName) {
    // If already connected, don't reconnect
    if (termData.ws && termData.ws.readyState === WebSocket.OPEN) {
        return;
    }

    // Close existing connection if any
    if (termData.ws) {
        termData.ws.close();
        termData.ws = null;
    }

    const term = termData.term;
    const fitAddon = termData.fitAddon;

    // Use wss:// for HTTPS, ws:// for HTTP
    const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = wsProtocol + '//' + window.location.host +
                  '/api/v1/terminal/ws?session=' + session + '&window=' + windowName;

    // Use ReconnectingWebSocket for automatic reconnection
    termData.ws = new ReconnectingWebSocket(wsUrl);
    termData.ws.maxReconnectAttempts = 10;
    termData.ws.reconnectInterval = 1000;      // Start at 1 second
    termData.ws.maxReconnectInterval = 30000;  // Max 30 seconds
    termData.ws.reconnectDecay = 1.5;          // Exponential backoff

    const ws = termData.ws;

    ws.onopen = () => {
        term.write('\r\n\x1b[32mConnected to ' + session + ':' + windowName + '\x1b[0m\r\n');
        term.scrollToBottom();

        // Send terminal dimensions on connect to restore proper sizing
        fitAddon.fit();
        ws.send(JSON.stringify({type: 'resize', rows: term.rows, cols: term.cols}));
    };

    ws.onmessage = (event) => {
        // Server sends validated UTF-8 text messages
        if (typeof event.data === 'string') {
            term.write(event.data);
        }
    };

    ws.onerror = (error) => {
        console.error('WebSocket error:', error);
    };

    ws.onconnecting = () => {
        term.write('\r\n\x1b[33mReconnecting...\x1b[0m\r\n');
        term.scrollToBottom();
    };

    ws.onclose = (event) => {
        if (!ws || ws.readyState === WebSocket.CLOSED) {
            term.write('\r\n\x1b[31mDisconnected from session\x1b[0m\r\n');
            term.scrollToBottom();
        }
    };

    // Dispose old handlers properly
    if (termData.dataHandler) {
        termData.dataHandler.dispose();
    }
    if (termData.resizeHandler) {
        termData.resizeHandler.dispose();
    }

    // Track if Shift is held for Shift+Enter handling
    if (typeof window.shiftHeld === 'undefined') {
        window.shiftHeld = false;
    }

    // Send terminal input to WebSocket as JSON
    termData.dataHandler = term.onData((data) => {
        // Shift+Enter sends newline without executing command
        if (data === '\r' && window.shiftHeld) {
            if (ws && ws.readyState === WebSocket.OPEN) {
                ws.send(JSON.stringify({type: 'input', data: '\n'}));
            }
            return;
        }

        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({type: 'input', data: data}));
        }
    });

    // Send terminal resize events
    termData.resizeHandler = term.onResize((size) => {
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({type: 'resize', rows: size.rows, cols: size.cols}));
        }
    });

    // Focus terminal after connection
    setTimeout(() => {
        term.focus();
    }, 100);
}

// Keyboard shortcuts (use capture phase to intercept before terminal)
document.addEventListener('keydown', (event) => {
    if (event.shiftKey) {
        window.shiftHeld = true;
    }

    // Cmd/Ctrl+P to open terminal dropdown (Select2)
    if ((event.metaKey || event.ctrlKey) && event.key === 'p') {
        event.preventDefault();
        $('#terminalSelect').select2('open');
        return;
    }

    // Cmd/Ctrl+E to open code editor (non-production only)
    if ((event.metaKey || event.ctrlKey) && event.key === 'e') {
        event.preventDefault();
        openCode();
        return;
    }

    // Cmd/Ctrl+Backspace to go back to previous terminal
    if ((event.metaKey || event.ctrlKey) && event.key === 'Backspace') {
        event.preventDefault();
        event.stopPropagation();
        if (window.previousTerminalKey) {
            switchTerminal(window.previousTerminalKey);
        }
        return;
    }
}, true);

document.addEventListener('keyup', (event) => {
    if (!event.shiftKey) {
        window.shiftHeld = false;
    }
});

// Initialize Select2 for terminal selector
$('#terminalSelect').select2({
    placeholder: 'Select a terminal...',
    allowClear: true,
    width: '300px'
});

// Cmd+P opens the Select2 dropdown
// (in keydown handler above, use: $('#terminalSelect').select2('open'))
```

**Server-side Go WebSocket handler:**

```go
import (
    "bufio"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "os/exec"
    "strconv"
    "strings"
    "sync"
    "time"

    "github.com/gorilla/websocket"
)

// WebSocket upgrader
var upgrader = websocket.Upgrader{
    ReadBufferSize:  8192,
    WriteBufferSize: 8192,
    CheckOrigin: func(r *http.Request) bool {
        return true // Allow localhost during development
    },
}

// Mutex to serialize tmux send-keys commands
var tmuxSendMutex sync.Mutex
var lastSendTime time.Time

// terminalWebSocket handles the WebSocket connection for terminal streaming
func terminalWebSocket(w http.ResponseWriter, r *http.Request) {
    session := r.URL.Query().Get("session")
    window := r.URL.Query().Get("window")
    if session == "" || window == "" {
        http.Error(w, "session and window parameters required", http.StatusBadRequest)
        return
    }

    // Upgrade to WebSocket
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Printf("WebSocket upgrade failed: %s", err)
        return
    }
    defer conn.Close()

    // Configure keepalive with ping/pong
    const pongWait = 60 * time.Second
    const pingPeriod = (pongWait * 9) / 10
    conn.SetReadDeadline(time.Now().Add(pongWait))
    conn.SetPongHandler(func(string) error {
        conn.SetReadDeadline(time.Now().Add(pongWait))
        return nil
    })

    // Start ping ticker goroutine
    pingTicker := time.NewTicker(pingPeriod)
    defer pingTicker.Stop()
    stopPing := make(chan bool, 1)
    defer func() {
        select {
        case stopPing <- true:
        default:
        }
    }()
    go func() {
        for {
            select {
            case <-pingTicker.C:
                if err := conn.WriteControl(websocket.PingMessage, []byte{},
                    time.Now().Add(10*time.Second)); err != nil {
                    return
                }
            case <-stopPing:
                return
            }
        }
    }()

    // Target for tmux commands
    target := fmt.Sprintf("%s:%s", session, window)

    // Check if session exists
    checkCmd := exec.Command("tmux", "has-session", "-t", session)
    if err := checkCmd.Run(); err != nil {
        conn.WriteMessage(websocket.TextMessage,
            []byte(fmt.Sprintf("Session %s does not exist\r\n", session)))
        return
    }

    // Check if window exists, create if needed
    checkWindowCmd := exec.Command("tmux", "list-windows", "-t", session,
        "-F", "#{window_name}")
    windowsOutput, _ := checkWindowCmd.Output()
    windowExists := false
    for _, w := range strings.Split(strings.TrimSpace(string(windowsOutput)), "\n") {
        if w == window {
            windowExists = true
            break
        }
    }

    if !windowExists {
        // Get working directory from first window
        getCwdCmd := exec.Command("tmux", "display-message", "-t", session+":0",
            "-p", "#{pane_current_path}")
        cwdOutput, _ := getCwdCmd.Output()
        cwd := strings.TrimSpace(string(cwdOutput))
        if cwd == "" {
            cwd = "~"
        }

        // Create the window
        createWindowCmd := exec.Command("tmux", "new-window", "-t", session,
            "-n", window, "-c", cwd)
        createWindowCmd.Env = append(os.Environ(), "TMUX=")
        if err := createWindowCmd.Run(); err != nil {
            conn.WriteMessage(websocket.TextMessage,
                []byte(fmt.Sprintf("Failed to create window %s: %v\r\n", window, err)))
            return
        }
    }

    // Send initial pane content with ANSI colors and full scrollback
    captureCmd := exec.Command("tmux", "capture-pane", "-t", target,
        "-p",      // print to stdout
        "-e",      // preserve ANSI escape sequences
        "-S", "-", // capture entire scrollback history
    )
    output, err := captureCmd.CombinedOutput()
    if err != nil {
        conn.WriteMessage(websocket.TextMessage,
            []byte(fmt.Sprintf("Failed to capture pane: %v\r\n", err)))
        return
    }

    if len(output) > 0 {
        // Validate UTF-8 to avoid protocol errors
        validOutput := strings.ToValidUTF8(string(output), "")
        conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
        conn.WriteMessage(websocket.TextMessage, []byte(validOutput))
    }

    // Get and send cursor position
    cursorCmd := exec.Command("tmux", "display-message", "-t", target,
        "-p", "#{cursor_x} #{cursor_y}")
    cursorOutput, err := cursorCmd.Output()
    if err == nil {
        var cursorX, cursorY int
        fmt.Sscanf(string(cursorOutput), "%d %d", &cursorX, &cursorY)
        // Position cursor (tmux 0-indexed, ANSI 1-indexed)
        cursorSeq := fmt.Sprintf("\x1b[%d;%dH", cursorY, cursorX+1)
        conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
        conn.WriteMessage(websocket.TextMessage, []byte(cursorSeq))
    }

    // Use pipe-pane to stream output with proper escape sequences
    pipeName := fmt.Sprintf("/tmp/tmux-pipe-%s-%s.fifo", session, window)

    // Remove any existing pipe
    exec.Command("rm", "-f", pipeName).Run()
    exec.Command("tmux", "pipe-pane", "-t", target, "").Run()

    // Create named pipe
    if err := exec.Command("mkfifo", pipeName).Run(); err != nil {
        log.Printf("Failed to create pipe: %s", err)
        return
    }
    defer exec.Command("rm", "-f", pipeName).Run()
    defer exec.Command("tmux", "pipe-pane", "-t", target, "").Run()

    // Start pipe-pane
    pipeCmd := fmt.Sprintf("cat >> %s", pipeName)
    if err := exec.Command("tmux", "pipe-pane", "-t", target, "-o", pipeCmd).Run(); err != nil {
        log.Printf("Failed to start pipe-pane: %s", err)
        return
    }

    // Read from pipe in goroutine
    stopReader := make(chan bool)
    go func() {
        file, err := os.Open(pipeName)
        if err != nil {
            log.Printf("Failed to open pipe: %s", err)
            return
        }
        defer file.Close()

        reader := bufio.NewReader(file)
        buf := make([]byte, 4096)

        for {
            select {
            case <-stopReader:
                return
            default:
                n, err := reader.Read(buf)
                if err != nil {
                    if err != io.EOF {
                        log.Printf("Error reading from pipe: %s", err)
                    }
                    return
                }
                if n > 0 {
                    // Validate UTF-8 and send
                    validUTF8 := strings.ToValidUTF8(string(buf[:n]), "")
                    conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
                    if err := conn.WriteMessage(websocket.TextMessage,
                        []byte(validUTF8)); err != nil {
                        return
                    }
                }
            }
        }
    }()

    // Read from WebSocket and send to tmux
    for {
        conn.SetReadDeadline(time.Now().Add(pongWait))
        messageType, message, err := conn.ReadMessage()
        if err != nil {
            break
        }

        if messageType == websocket.TextMessage {
            var msg struct {
                Type string `json:"type"`
                Data string `json:"data"`
                Rows int    `json:"rows"`
                Cols int    `json:"cols"`
            }

            if err := json.Unmarshal(message, &msg); err != nil {
                continue
            }

            switch msg.Type {
            case "input":
                data := msg.Data

                // Strip bracketed paste mode sequences if present
                if strings.HasPrefix(data, "\x1b[200~") &&
                   strings.HasSuffix(data, "\x1b[201~") {
                    data = strings.TrimPrefix(data, "\x1b[200~")
                    data = strings.TrimSuffix(data, "\x1b[201~")
                }

                // Rate limit to prevent overwhelming tmux
                tmuxSendMutex.Lock()
                if time.Since(lastSendTime) < 1*time.Millisecond {
                    time.Sleep(1 * time.Millisecond)
                }

                var err error
                if data == "\r" {
                    // Enter key - execute command
                    cmd := exec.Command("tmux", "send-keys", "-t", target, "Enter")
                    err = cmd.Run()
                } else if data == "\n" {
                    // Shift+Enter - add newline without executing
                    cmd := exec.Command("tmux", "send-keys", "-t", target, "-l", data)
                    err = cmd.Run()
                } else {
                    // All other input - use paste-buffer for special chars
                    cmd := exec.Command("tmux", "load-buffer", "-")
                    cmd.Stdin = strings.NewReader(data)
                    if err = cmd.Run(); err == nil {
                        cmd = exec.Command("tmux", "paste-buffer", "-d", "-t", target)
                        err = cmd.Run()
                    }
                }

                lastSendTime = time.Now()
                tmuxSendMutex.Unlock()

                if err != nil {
                    log.Printf("Failed to send keys to tmux: %s", err)
                }

            case "resize":
                cmd := exec.Command("tmux", "resize-window", "-t", target,
                    "-x", strconv.Itoa(msg.Cols), "-y", strconv.Itoa(msg.Rows))
                cmd.Run()
            }
        }
    }

    close(stopReader)
}
```

**Key implementation notes:**

1. **UTF-8 validation**: Always validate output with `strings.ToValidUTF8()` before sending to WebSocket to avoid protocol errors with binary data in terminal output.

2. **tmux pipe-pane**: Uses named pipes (FIFO) to stream terminal output. The `-o` flag enables output mode for capturing escape sequences correctly.

3. **Input handling**: Uses `load-buffer`/`paste-buffer` for regular input (handles special characters like semicolons correctly) and `send-keys` for Enter/newline.

4. **Rate limiting**: A mutex with minimum 1ms delay prevents overwhelming tmux with rapid keystrokes.

5. **Ping/pong keepalive**: Essential for detecting stale connections. Uses 60-second timeout with 54-second ping interval.

6. **ReconnectingWebSocket**: Provides automatic reconnection with exponential backoff (1s → 30s max).

7. **Cursor restoration**: After sending scrollback, send cursor position using ANSI escape sequence to restore proper cursor location.

### 16.3 Core Interfaces

```go
// Service Manager
type ServiceManager interface {
    Start(ctx context.Context, name string) error
    Stop(ctx context.Context, name string) error
    Restart(ctx context.Context, name string) error
    Status(name string) ServiceStatus
    Logs(name string, lines int) []string
    List() []ServiceInfo
}

// Event Bus
type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(pattern string, handler EventHandler) (SubscriptionID, error)
    SubscribeAsync(pattern string, handler EventHandler, bufferSize int) (SubscriptionID, error)
    Unsubscribe(id SubscriptionID) error
    History(filter EventFilter) ([]Event, error)
    Close() error
}

// Worktree Manager
type WorktreeManager interface {
    List() []WorktreeInfo
    Active() *WorktreeInfo
    Activate(name string) error
    Create(name, branch string) error
    Delete(name string) error
}

// Crash Manager
type CrashManager interface {
    List() ([]CrashSummary, error)
    Get(id string) (*Crash, error)
    Newest() (*Crash, error)
    Delete(id string) error
    Clear() error
}
```

### 16.3.1 Command Line Options

The `trellis` server binary accepts the following command-line flags:

```bash
trellis [options]

Options:
  -config, -c <path>   Path to config file (default: auto-detect)
  -host <host>         HTTP server host (overrides config)
  -port <port>         HTTP server port (overrides config)
  -worktree, -w <name> Worktree to activate on startup (name or branch)
  -version, -v         Show version and exit
  -debug               Enable debug mode
```

**Config auto-detection:**

When `-config` is not specified, Trellis searches for a configuration file in the following order:
1. `trellis.hjson` in the current directory
2. `trellis.json` in the current directory

**Examples:**

```bash
# Start with auto-detected config
trellis

# Start with specific config file
trellis -config /path/to/trellis.hjson

# Override port from config
trellis -port 9000

# Start with specific worktree active
trellis -w feature-branch

# Show version
trellis -v
```

### 16.4 Startup Sequence

1. Parse command-line flags
2. Load configuration file(s)
3. Expand template variables
4. **Validate dependencies** - If vscode is configured, verify code-server binary exists (check configured path or PATH). Log warning if missing.
5. Initialize event bus
6. Register built-in event subscribers
7. Discover worktrees
8. Set active worktree
9. Initialize terminal (tmux sessions)
10. Initialize service manager
11. Start file watchers
12. Start services (including code-server if configured)
13. Start HTTP server

### 16.5 Shutdown Sequence

1. Receive shutdown signal (SIGTERM, SIGINT)
2. Stop accepting new HTTP requests
3. Stop file watchers
4. Stop all services (parallel, with timeout)
5. Close event bus (flush pending)
6. Close tmux sessions (optional, configurable)
7. Exit

### 16.6 Example Configuration

Complete example for a Go microservices project:

```hjson
{
  version: "1.0"

  project: {
    name: "myapp"
    description: "My microservices application"
  }

  server: {
    port: 1000
    host: "127.0.0.1"
  }

  worktree: {
    discovery: {
      mode: "git"
    }
    binaries: {
      path: "{{.Worktree.Root}}/bin"
    }
  }

  services: [
    // Infrastructure
    {
      name: "postgres"
      command: "postgres"
      args: ["-D", "/usr/local/var/postgres"]
      watching: false  // External binary
      restart: { policy: "always" }
    }
    // Application
    {
      name: "api"
      command: "{{.Worktree.Root}}/bin/api"
      args: ["-config", "{{.Worktree.Root}}/config/api.json"]
      debug: "dlv exec --headless --listen=:2345 --api-version=2 --accept-multiclient --"
    }
    {
      name: "worker"
      command: "{{.Worktree.Root}}/bin/worker"
    }
  ]

  workflows: [
    {
      id: "test"
      name: "Run Tests"
      command: ["go", "test", "-json", "-count=1", "./..."]
      output_parser: "go_test_json"
      timeout: "10m"
    }
    {
      id: "test-coverage"
      name: "Test with Coverage"
      // Shell operators (&&, |, >) require sh -c wrapper
      command: ["sh", "-c", "go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out"]
      timeout: "10m"
    }
    {
      id: "db-reset"
      name: "Reset Database"
      command: ["./scripts/reset-db.sh"]
      confirm: true
      requires_stopped: ["api", "worker"]
    }
    {
      id: "db-migrate"
      name: "Run Migrations"
      command: ["./bin/migrate", "up"]
    }
  ]

  crashes: {
    reports_dir: ".trellis/crashes"
    max_age: "7d"
    max_count: 100
  }

  terminal: {
    backend: "tmux"
    tmux: {
      history_limit: 50000
    }
    default_windows: [
      { name: "shell", command: "/bin/zsh" }
      { name: "claude", command: "claude" }
    ]
    vscode: {
      binary: "code-server"
      port: 8443
      user_data_dir: "~/dotfiles/vscode"  // Share settings via dotfiles repo
    }
    links: [
      { name: "Grafana", url: "http://localhost:3000/" }
      { name: "Admin", url: "http://admin.local:8080/" }
      { name: "GitHub", url: "https://github.com/myorg/myapp" }
    ]
  }

  events: {
    history: {
      max_events: 10000
      max_age: "1h"
    }
  }

}
```

---

## 17. CLI Tool (trellis-ctl)

`trellis-ctl` is a command-line tool for controlling a running Trellis instance. It communicates with the Trellis HTTP API and is designed for use in Trellis-managed terminal sessions where the `TRELLIS_API` environment variable is automatically set.

### 17.1 Installation

The CLI is built alongside the main Trellis binary:

```bash
make build
# Creates: trellis, trellis-ctl
```

Or build directly:

```bash
go build -o trellis-ctl ./cmd/trellis-ctl
```

### 17.2 Configuration

| Environment Variable | Description | Default |
|---------------------|-------------|---------|
| `TRELLIS_API` | Base URL of Trellis API | `http://localhost:8080` |

In Trellis-managed tmux sessions, `TRELLIS_API` is set automatically.

### 17.3 Global Flags

| Flag | Description |
|------|-------------|
| `-json` | Output in JSON format (machine-readable) |

The `-json` flag can be placed anywhere in the command:

```bash
trellis-ctl -json status
trellis-ctl status -json
trellis-ctl workflow -json list
```

### 17.4 Commands

#### Service Commands

```bash
# List all services with status
trellis-ctl status

# Get detailed status for a specific service
trellis-ctl status <service>

# Control services
trellis-ctl start <service>
trellis-ctl stop <service>
trellis-ctl restart <service>
```

#### Log Viewer Commands

The `logs` command provides access to both service logs and configured log viewers with powerful filtering and output options.

**Service vs Log Viewer behavior:**
- **Service logs** are raw lines from the service's stdout/stderr. If the service has `logging.parser` configured (see [Service Definition Schema](#41-service-definition-schema)), `trellis-ctl` parses each line using that format, enabling filtering by level, field, and time. Without a parser config, logs are treated as raw text (only `-grep` filtering works).
- **Log viewers** stream pre-parsed JSON entries from remote sources (SSH, files, commands). All filtering options are always available.

```bash
# Service logs (basic)
trellis-ctl logs <service>              # Last 100 lines
trellis-ctl logs <service> -n 50        # Last 50 lines
trellis-ctl logs <service> -f           # Follow/stream mode (Ctrl+C to stop)

# Log viewer logs
trellis-ctl logs -viewer <name>         # Access a configured log viewer
trellis-ctl logs -list                  # List available log viewers

# Time-based filtering
trellis-ctl logs <service> -since 1h    # Logs from last hour
trellis-ctl logs <service> -since 30m   # Logs from last 30 minutes
trellis-ctl logs <service> -since 2024-01-15T10:00:00Z  # Since specific time
trellis-ctl logs <service> -since 6:30am               # Since clock time (today)
trellis-ctl logs <service> -since 14:00                # 24-hour format
trellis-ctl logs <service> -since 6:00am -until 7:00am # Time range

# Level filtering
trellis-ctl logs <service> -level error        # Only errors
trellis-ctl logs <service> -level warn,error   # Warnings and errors
trellis-ctl logs <service> -level info+        # Info and above

# Text/pattern filtering
trellis-ctl logs <service> -grep "pattern"         # Regex search in message
trellis-ctl logs <service> -grep "panic|fatal"     # Multiple patterns
trellis-ctl logs <service> -field host=prod1       # Filter by parsed field
trellis-ctl logs <service> -field status=500       # Numeric field match

# Context lines around grep matches (like grep -B/-A/-C)
trellis-ctl logs <service> -grep "error" -B 5      # 5 lines before each match
trellis-ctl logs <service> -grep "error" -A 10     # 10 lines after each match
trellis-ctl logs <service> -grep "error" -C 3      # 3 lines before and after
trellis-ctl logs -viewer api-logs -grep "started" -B 10 -since 6:00am -until 7:00am

# Output formats
trellis-ctl logs <service> -json                   # JSON array output
trellis-ctl logs <service> -jsonl                  # JSON Lines (one per line)
trellis-ctl logs <service> -csv                    # CSV format
trellis-ctl logs <service> -raw                    # Original unparsed lines
trellis-ctl logs <service> -format "{{.timestamp}} [{{.level}}] {{.message}}"

# Combining filters
trellis-ctl logs backend -level error -since 1h -json
trellis-ctl logs -viewer api-logs -grep "user_id=123" -f

# Browser integration
trellis-ctl logs <service> -open                   # Open in browser log viewer
trellis-ctl logs <service> -url                    # Print URL to log viewer

# Management
trellis-ctl logs <service> -clear                  # Clear log buffer
trellis-ctl logs <service> -stats                  # Show log statistics
```

**Log statistics output:**
```
Log Statistics for 'backend' (last 1h):
  Total entries:    12,847
  Error rate:       0.3% (42 errors)
  Warn rate:        1.2% (156 warnings)
  Avg rate:         214 entries/min

  Level distribution:
    DEBUG:  2,103 (16.4%)
    INFO:   10,546 (82.1%)
    WARN:   156 (1.2%)
    ERROR:  42 (0.3%)

  Top errors:
    "connection timeout" (18 occurrences)
    "rate limit exceeded" (12 occurrences)
    "invalid token" (8 occurrences)
```

**Custom format templates:**

The `-format` flag supports Go template syntax with access to parsed log fields:

| Field | Description |
|-------|-------------|
| `{{.timestamp}}` | Parsed timestamp |
| `{{.level}}` | Log level |
| `{{.message}}` | Log message |
| `{{.raw}}` | Original raw line |
| `{{.source}}` | Log source (service name or file) |
| `{{.fields.<name>}}` | Custom parsed field |

Example:
```bash
# Compact format
trellis-ctl logs backend -format "{{.timestamp}} {{.message}}"

# Include custom fields
trellis-ctl logs backend -format "{{.fields.request_id}} {{.level}}: {{.message}}"

# Tab-separated for further processing
trellis-ctl logs backend -format "{{.timestamp}}\t{{.level}}\t{{.message}}"
```

#### Workflow Commands

```bash
# List all workflows
trellis-ctl workflow list

# Run a workflow (waits for completion)
trellis-ctl workflow run <id>

# Check workflow status
trellis-ctl workflow status <id>
```

#### Worktree Commands

```bash
# List all worktrees
trellis-ctl worktree list

# Activate a worktree (stops services, switches, restarts)
trellis-ctl worktree activate <name>
```

#### Event Commands

```bash
# Show recent events
trellis-ctl events            # Last 50 events
trellis-ctl events -n 20      # Last 20 events
```

#### Crash Commands

```bash
# List all crashes
trellis-ctl crash list

# Show most recent crash with full details
trellis-ctl crash newest

# Show specific crash by ID
trellis-ctl crash <id>

# Delete a specific crash
trellis-ctl crash delete <id>

# Clear all crashes
trellis-ctl crash clear
```

#### Notify Command

Send notification events to alert the user (designed for AI assistants and external tools):

```bash
# Task completed (default type)
trellis-ctl notify "Refactoring complete"

# Waiting for user input
trellis-ctl notify "Need database credentials" -type blocked

# Something failed
trellis-ctl notify "Build failed with 3 errors" -type error
```

**Notification types:**
| Type | Use Case |
|------|----------|
| `done` | Task completed successfully, user can review results |
| `blocked` | Stuck and need user input to continue |
| `error` | Something failed that needs attention |

Users can subscribe to these events via WebSocket (`/api/v1/events/ws?pattern=notify.*`) or view them with `trellis-ctl events`.

#### Other Commands

```bash
trellis-ctl version    # Show version
trellis-ctl help       # Show help
```

### 17.5 Output Formats

**Status output** (table format):
```
SERVICE              STATE      PID      RESTARTS   ERROR
backend              running    12345    0
frontend             running    12346    0
worker               crashed    -        3          exit code 1
```

**Service detail** (JSON):
```json
{
  "name": "backend",
  "status": {
    "state": "running",
    "pid": 12345,
    "exit_code": 0,
    "started_at": "2024-01-15T10:30:00Z",
    "restart_count": 0
  },
  "enabled": true
}
```

**Logs output** (plain text):
```
2024/01/15 10:30:00 Server starting on :8080
2024/01/15 10:30:01 Connected to database
```

**Worktree list output** (table format):
```
NAME                 BRANCH               ACTIVE   PATH
--------------------------------------------------------------------------------
myproject            main                 *        /Users/dev/src/myproject
myproject-feature    feature              /Users/dev/src/myproject-feature
```

**Worktree list** (JSON with `-json` flag):
```json
{
  "worktrees": [
    {
      "Path": "/Users/dev/src/myproject",
      "Branch": "main",
      "Commit": "abc123",
      "Detached": false,
      "IsBare": false,
      "Dirty": false,
      "Ahead": 0,
      "Behind": 0,
      "Active": true
    },
    {
      "Path": "/Users/dev/src/myproject-feature",
      "Branch": "feature",
      "Commit": "def456",
      "Detached": false,
      "IsBare": false,
      "Dirty": true,
      "Ahead": 5,
      "Behind": 12,
      "Active": false
    }
  ]
}
```

### 17.6 CLI Integration Patterns

`trellis-ctl` is designed for automation and scripting. Common patterns:

**After making code changes:**
```bash
# Check if service auto-restarted
trellis-ctl status

# If crashed, check crash history
trellis-ctl crash newest
```

**Debugging a crash:**
```bash
# View most recent crash with full context
trellis-ctl crash newest

# View crash history
trellis-ctl crash list

# View specific crash
trellis-ctl crash <crash-id>
```

**Running builds:**
```bash
# Run a build workflow
trellis-ctl workflow run build

# Check if services restarted
trellis-ctl status
```

### 17.7 Claude Code Skills

A skill file can be placed at `.claude/skills/trellis.md` to teach Claude Code how to use `trellis-ctl`:

```markdown
---
name: trellis
description: Control the Trellis development environment
---

Use `trellis-ctl` to interact with Trellis. The `TRELLIS_API`
environment variable is automatically set.

## Commands
- `trellis-ctl status` - Check service status
- `trellis-ctl logs <service>` - View logs
- `trellis-ctl restart <service>` - Restart after fixes
- `trellis-ctl workflow run build` - Run builds
```

---

## Appendix A: HJSON Syntax Reference

HJSON is JSON for humans. Key differences from JSON:

```hjson
{
  // Comments are allowed
  # Hash comments too

  // Unquoted keys
  name: "value"

  // Unquoted strings (if no special chars)
  simple: hello world

  // Multiline strings
  description: '''
    This is a
    multiline string
  '''

  // Trailing commas allowed
  array: [
    1,
    2,
    3,
  ]

  // Optional root braces
}
```

---

## Appendix B: Template Function Reference

| Function | Description | Example |
|----------|-------------|---------|
| `slugify` | Convert to slug | `{{.Worktree.Branch \| slugify}}` → `feature-auth` |
| `replace` | String replace | `{{.Name \| replace "-" "_"}}` |
| `upper` | Uppercase | `{{.Name \| upper}}` |
| `lower` | Lowercase | `{{.Name \| lower}}` |
| `default` | Default value | `{{.Port \| default 8080}}` |
| `now` | Current time | `{{now.Format "2006-01-02"}}` |
| `quote` | Shell quote | `{{.Path \| quote}}` |

---

## Appendix C: Migration from Docker Compose

| Docker Compose | Trellis |
|----------------|---------|
| `services:` | `services:` |
| `image:` | `command:` (run binary directly) |
| `build:` | `workflows:` (build externally, Trellis watches binaries) |
| `environment:` | (set in shell before running Trellis) |
| `ports:` | (services bind directly) |
| `volumes:` | (use worktree paths) |
| `healthcheck:` | (not used; services manage their own health) |
| `restart:` | `restart:` |

---

## Appendix D: Glossary

| Term | Definition |
|------|------------|
| **Crash** | A recorded service failure with full debugging context (logs, worktree, trace ID) |
| **Event** | Immutable record of a state change in the system |
| **Ring Buffer** | Fixed-size circular buffer for log retention |
| **trellis-ctl** | Command-line tool for controlling a running Trellis instance |
| **Worktree** | Git worktree representing an isolated development context |
| **Workflow** | User-triggered action like build, test, or deploy |

---

*End of Specification*
