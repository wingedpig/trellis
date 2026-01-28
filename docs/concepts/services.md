---
title: "Services"
weight: 11
---

# Services

Services are long-running processes that Trellis manages throughout your development session.

## Service Definition

Define services in your `trellis.hjson`:

```hjson
{
  services: [
    {
      // Required: unique name
      name: "backend"

      // Command to run (string or array)
      command: "./bin/backend"
      // or: command: ["./bin/backend", "-port", "8080"]

      // Working directory (default: worktree root)
      work_dir: "{{.Worktree.Root}}/services/backend"

      // Environment variables
      env: {
        DB_HOST: "localhost"
        DEBUG: "true"
      }

      // Binary to watch for changes (auto-restart on change)
      watch_binary: "{{.Worktree.Binaries}}/backend"

      // Additional files to watch (configs, etc.)
      watch_files: [
        "config/backend.yaml"
        "config/database.yaml"
      ]

      // Enable/disable the service (default: enabled)
      enabled: true
      // disabled: true  // Alternative way to disable

      // Include in binary watching (default: true)
      // Set to false for external services like databases
      watching: true
    }
  ]
}
```

## Service Lifecycle

### States

| State | Description |
|-------|-------------|
| `stopped` | Not running |
| `starting` | Process started |
| `running` | Running |
| `stopping` | Graceful shutdown in progress |
| `crashed` | Exited unexpectedly |

### State Transitions

```
stopped → starting → running → stopping → stopped
              ↓           ↓
           crashed     crashed
```

## Binary Watching

When you configure `watch_binary`, Trellis monitors that file for changes:

1. You recompile: `go build -o ./bin/backend ./cmd/backend`
2. Trellis detects the binary changed
3. The service is gracefully stopped (SIGTERM)
4. After a short delay, the service restarts
5. A `binary.changed` event is emitted

**Configuration:**
```hjson
{
  watch: {
    debounce: "100ms"  // Wait for rapid changes to settle (default)
  }
}
```

## Restart Policies

Control what happens when a service exits:

```hjson
{
  services: [
    {
      name: "worker"
      command: "./bin/worker"
      restart_policy: "on-failure"  // "always", "on-failure", "never"
      max_restarts: 3               // Give up after N attempts
      restart_delay: "1s"           // Wait between restarts
    }
  ]
}
```

Alternatively, use a nested `restart` block with just the policy:

```hjson
{
  services: [
    {
      name: "worker"
      command: "./bin/worker"
      restart: {
        policy: "on-failure"
      }
      max_restarts: 3
      restart_delay: "1s"
    }
  ]
}
```

## Crash Reports

When a service crashes, Trellis captures:
- Recent log lines (context before the crash)
- Exit code
- Stack trace (if configured)
- Timestamp and worktree context

View crashes with:
```bash
trellis-ctl crash newest
trellis-ctl crash list
```

## Service Log Tracing

Services with `logging.parser` configured (directly or via `logging_defaults`) are automatically registered as trace-searchable log sources. Trellis creates `svc:<name>` log viewers backed by each service's in-memory ring buffer and collects them into a `services` trace group:

```bash
# Search all service log buffers for a trace ID
trellis-ctl trace "req-123" services -since 1h

# View available trace groups
trellis-ctl trace-report -groups
```

This enables distributed tracing across dev services with zero configuration. Two-pass ID expansion works when the service parser includes an `id` field (e.g., `id: "request_id"`).

## Service Events

Services emit events throughout their lifecycle:

| Event | Description |
|-------|-------------|
| `service.started` | Service started running |
| `service.stopped` | Service stopped |
| `service.crashed` | Service exited unexpectedly |
| `service.restarted` | Service was restarted |
| `binary.changed` | Watched binary was modified |
