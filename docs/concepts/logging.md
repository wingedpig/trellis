---
title: "Logging"
weight: 13
---

# Logging

Trellis provides unified log viewing across multiple sources with parsing, filtering, and distributed tracing.

## Log Sources

### Service Logs

Every service automatically captures stdout/stderr in a ring buffer:

```bash
trellis-ctl logs backend
trellis-ctl logs backend -f  # Follow mode
```

### Log Viewers

For external log sources, configure log viewers:

```hjson
{
  log_viewers: [
    {
      name: "nginx-logs"
      source: {
        type: "ssh"
        host: "web01.example.com"
        path: "/var/log/nginx/access.log"
      }
      parser: {
        type: "json"
        timestamp: "time"
        level: "status"
        message: "request"
      }
    }
  ]
}
```

## Source Types

### Service Source (Automatic)

Services with `logging.parser` configured automatically get a log viewer (`svc:<name>`) that reads from the service's in-memory ring buffer. These are created at startup and require no manual configuration. See [Distributed Tracing](#service-tracing-dev-environment) for usage.

### File Source
```hjson
source: {
  type: "file"
  path: "/var/log/app.log"
  follow: true
}
```

### SSH Source
```hjson
source: {
  type: "ssh"
  host: "server.example.com"
  path: "/var/log/app"
  current: "current.log"
  rotated_pattern: "*.log.*"
}
```

### Command Source
```hjson
source: {
  type: "command"
  command: ["journalctl", "-f", "-u", "myapp", "-o", "json"]
}
```

### Docker Source
```hjson
source: {
  type: "docker"
  container: "my-container"
  follow: true
}
```

### Kubernetes Source
```hjson
source: {
  type: "kubernetes"
  namespace: "default"
  pod: "my-pod"
  container: "app"
  follow: true
}
```

## Viewer Modes: Live vs. Explore

Each log viewer opens in one of two modes, set via `mode` in its `log_viewers` entry:

```hjson
log_viewers: [
  {
    name: "nginx-access"
    mode: "explore"
    source: {
      type: "file"
      path: "/var/log/nginx"
      current: "access.log"
    }
  }
]
```

- **`live`** (default): opens tailing the source and following new entries — the existing behavior.
- **`explore`**: for high-volume logs (nginx access logs and similar) where tailing every line isn't useful. Opening the viewer does not start the tail. Instead the server reads a static snapshot of the ~200 most recent lines directly from the end of the file (a byte-offset backward read), and the UI opens paused, with search and scrollback as the primary workflow. A **Go live** button in the header starts the tail and switches to streaming. Scrolling up to page back through history, and history search, work the same as in `live` mode.

`explore` mode requires a source that supports backward reads — `file` and `ssh`. For `docker`, `kubernetes`, and `command` sources (which stream rather than expose a seekable byte offset), an `explore`-mode viewer falls back to starting the tail immediately but still opens paused, so the UI behaves consistently even though the tail is already running underneath.

### Pausing and Auto-Pause

Whichever mode a viewer is in, pausing is lossless and cheap: while a connection is paused (scrolled up, auto-paused, or in `explore` mode before going live) the server stops shipping individual log lines and instead sends a small stats frame every couple of seconds (missed-line count and current rate). On resume, the server replays the missed lines from an in-memory ring buffer of up to 2000 entries; if more were missed, the newest 2000 are shown with a "N lines skipped while paused" divider, and history search can still locate the rest.

Followed viewers also auto-pause under load: if entries arrive faster than `log_viewer_settings.auto_pause_rate` (default 30 lines/sec), the UI drops out of following and shows a "High volume — following paused" banner rather than trying to render every line. A viewer that isn't accessed at all — no active watchers and no polling — is stopped after `log_viewer_settings.idle_timeout` (default 5m); one whose last watcher just disconnected is stopped sooner, after `log_viewer_settings.disconnect_grace` (default 30s). See [Configuration Reference](/docs/reference/config/#log_viewers) for the `mode`, `disconnect_grace`, and `auto_pause_rate` settings.

## Parsers

### JSON Parser
```hjson
parser: {
  type: "json"
  timestamp: "ts"
  level: "level"
  message: "msg"
}
```

### Logfmt Parser
```hjson
parser: {
  type: "logfmt"
  timestamp: "time"
  level: "level"
  message: "msg"
}
```

### Regex Parser
```hjson
parser: {
  type: "regex"
  pattern: "^\\[(?P<timestamp>[^\\]]+)\\] (?P<level>\\w+): (?P<message>.*)$"
  timestamp_format: "2006-01-02 15:04:05"
}
```

## Filtering

### CLI Filtering
```bash
# By level
trellis-ctl logs backend -level error
trellis-ctl logs backend -level warn,error

# By time
trellis-ctl logs backend -since 1h
trellis-ctl logs backend -since 6:00am -until 7:00am

# By pattern
trellis-ctl logs backend -grep "connection"
trellis-ctl logs backend -grep "panic|fatal"

# By field
trellis-ctl logs backend -field host=prod1

# Context lines (like grep -B/-A/-C)
trellis-ctl logs backend -grep "error" -B 5 -A 10
```

### Web UI Filter Syntax

**Service Log Filters:**

| Syntax | Description | Example |
|--------|-------------|---------|
| `field:value` | Field contains value | `level:error` |
| `field:~regex` | Regex match on field | `msg:~timeout.*` |
| `text` | Full text search | `timeout` |

**Log Viewer Filters:**

| Syntax | Description | Example |
|--------|-------------|---------|
| `level:value` | Level contains value | `level:error` |
| `-level:value` | Exclude exact level | `-level:debug` |
| `msg:~text` | Message contains text | `msg:~timeout` |
| `"quoted"` | Message contains text | `"error"` |
| `field:value` | Field contains value | `host:prod1` |
| `text` | Full text search | `timeout` |

Multiple terms are AND-ed together. All matching is case-insensitive.

**Trace Report Filters:**

Trace reports support all the log viewer filter syntax above, plus:

| Syntax | Description | Example |
|--------|-------------|---------|
| `trace:text` | Show all entries sharing a trace ID with entries matching `text` | `trace:/api/users` |

The `trace:` prefix performs a three-pass filter: first it finds entries matching the search text, then collects their trace ID values, and finally shows all entries that share any of those trace IDs. This is useful for seeing the full request context across services when you know part of a request (e.g., a URL path or error message).

## Distributed Tracing

Search for a trace ID across multiple log sources:

```bash
trellis-ctl trace abc123 api-flow -since 1h
```

### Service Tracing (Dev Environment)

When services have `logging.parser` configured (directly or via `logging_defaults`), Trellis automatically creates a `services` trace group that searches all service log buffers:

```bash
# Search across all dev service logs
trellis-ctl trace "req-123" services -since 1h

# View available trace groups (includes auto-generated "services" group)
trellis-ctl trace-report -groups
```

This works with zero configuration — service log viewers (`svc:api`, `svc:worker`, etc.) are created automatically from each service's in-memory ring buffer. Two-pass ID expansion works if the service parser has an `id` field configured.

### Configure Trace Groups

For production log sources, configure trace groups explicitly:

```hjson
{
  trace_groups: [
    {
      name: "api-flow"
      log_viewers: ["nginx-logs", "api-logs", "db-logs"]
    }
  ]
}
```

### View Trace Reports

```bash
trellis-ctl trace-report -list
trellis-ctl trace-report debug-session-1
```

## Service Log Parsing

Configure parsing for service logs:

```hjson
{
  services: [
    {
      name: "api"
      command: "./bin/api"
      logging: {
        parser: {
          type: "json"
          timestamp: "ts"
          level: "level"
          message: "msg"
          id: "request_id"
          file: "source"      // Enables "Open in Editor" from log entries
          line: "lineno"
        }
      }
    }
  ]
}
```

With a parser configured, service logs appear in the table-based log viewer UI with filtering and field display.

## Logging Defaults

Set defaults for all services and log viewers:

```hjson
{
  logging_defaults: {
    parser: {
      type: "json"
      timestamp: "ts"
      level: "level"
      message: "msg"
      id: "request_id"
      stack: "stack"
      file: "source"
      line: "lineno"
    }
    derive: {
      short_time: { from: "timestamp", op: "timefmt", args: { format: "15:04:05" } }
    }
    layout: [
      { field: "short_time", min_width: 8 }
      { field: "level", min_width: 5 }
      { field: "message" }
    ]
  }
}
```

Individual services and log viewers can override these defaults.
