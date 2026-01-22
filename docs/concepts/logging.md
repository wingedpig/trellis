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

## Distributed Tracing

Search for a trace ID across multiple log sources:

```bash
trellis-ctl trace abc123 api-flow -since 1h
```

### Configure Trace Groups

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
