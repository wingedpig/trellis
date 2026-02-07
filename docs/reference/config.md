---
title: "Configuration"
weight: 21
---

# Configuration Reference

Trellis is configured via an HJSON file (JSON with comments and relaxed syntax).

## Config File Location

Trellis searches for configuration in this order:
1. Path specified with `-config` flag
2. `trellis.hjson` in current directory
3. `trellis.json` in current directory

## Complete Example

```hjson
{
  version: "1.0"

  project: {
    name: "myapp"
    description: "My Application Development Environment"
  }

  server: {
    port: 1234
    host: "127.0.0.1"
  }

  // Worktree configuration
  worktree: {
    discovery: {
      mode: "git"
    }
    repo_dir: "/Users/dev/src/myapp"
    create_dir: "/Users/dev/src"
    binaries: {
      path: "/Users/dev/bin/{{if .Worktree.Name}}{{.Worktree.Name}}{{else}}myapp{{end}}"
    }
    lifecycle: {
      on_create: [
        { name: "npm-install", command: ["npm", "install"], timeout: "5m" }
        { name: "build", command: ["make", "build"], timeout: "10m" }
      ]
    }
  }

  watch: {
    debounce: "500ms"
  }

  terminal: {
    backend: "tmux"
    tmux: {
      history_limit: 50000
      shell: "/bin/zsh"
    }
    default_windows: [
      { name: "shell" }
      { name: "claude" }
      { name: "dev" }
    ]
    shortcuts: [
      { key: "cmd+l", window: "~prod-logs" }
    ]
    remote_windows: [
      { name: "prod (1)", ssh_host: "prod01", tmux_session: "main" }
      { name: "prod (2)", ssh_host: "prod02", tmux_session: "main" }
      { name: "db01", command: ["ssh", "-t", "db01", "screen", "-dR", "db"] }
    ]
    links: [
      { name: "admin", url: "http://localhost:8080/" }
      { name: "docs", url: "https://docs.example.com/" }
    ]
    vscode: {
      binary: "code-server"
      port: 8443
    }
  }

  crashes: {
    reports_dir: ".trellis/crashes"
    max_age: "7d"
    max_count: 100
  }

  trace: {
    reports_dir: "traces"
    max_age: "7d"
  }

  logging_defaults: {
    parser: {
      type: "json"
      timestamp: "time"
      level: "level"
      id: "trace_id"
      stack: "stack"
    }
    derive: {
      ts_short: {
        from: "time"
        op: "timefmt"
        args: { format: "15:04:05.000" }
      }
      file_line: {
        op: "fmt"
        args: { template: "{file}:{line}" }
      }
    }
    layout: [
      { field: "ts_short", min_width: 12, max_width: 12, timestamp: true }
      { field: "level", min_width: 5 }
      { field: "file_line", max_width: 40 }
      { field: "msg", max_width: 80 }
    ]
  }

  trace_groups: [
    {
      name: "web"
      log_viewers: ["web01-logs", "web02-logs"]
    }
    {
      name: "api"
      log_viewers: ["api01-logs", "api02-logs"]
    }
  ]

  log_viewers: [
    {
      name: "prod-logs"
      source: {
        type: "ssh"
        host: "prod01"
        path: "/var/log/myapp/"
        current: "current"
        rotated_pattern: "@*s"
        decompress: "zstd -dc"
      }
    }
    {
      name: "web01-logs"
      source: {
        type: "ssh"
        host: "web01"
        path: "/var/log/web/"
        current: "current"
        rotated_pattern: "@*s"
        decompress: "zstd -dc"
      }
    }
    {
      name: "web02-logs"
      source: {
        type: "ssh"
        host: "web02"
        path: "/var/log/web/"
        current: "current"
        rotated_pattern: "@*s"
        decompress: "zstd -dc"
      }
    }
    {
      name: "api01-logs"
      source: {
        type: "ssh"
        host: "api01"
        path: "/var/log/api/"
        current: "current"
        rotated_pattern: "@*s"
        decompress: "zstd -dc"
      }
    }
    {
      name: "api02-logs"
      source: {
        type: "ssh"
        host: "api02"
        path: "/var/log/api/"
        current: "current"
        rotated_pattern: "@*s"
        decompress: "zstd -dc"
      }
    }
  ]

  services: [
    // Infrastructure (external binaries)
    {
      name: "redis"
      command: ["redis-server"]
      watching: false
    }

    // Core services
    {
      name: "api"
      command: ["{{.Worktree.Binaries}}/api", "/etc/myapp/api.json"]
    }
    {
      name: "web"
      command: ["{{.Worktree.Binaries}}/web", "/etc/myapp/web.json"]
    }
    {
      name: "worker"
      command: ["{{.Worktree.Binaries}}/worker", "/etc/myapp/worker.json"]
    }
  ]

  workflows: [
    {
      id: "test"
      name: "Run All Tests"
      command: ["go", "test", "-json", "-count=1", "./..."]
      output_parser: "go_test_json"
      timeout: "10m"
    }
    {
      id: "build"
      name: "Build All"
      command: ["make", "build"]
      timeout: "10m"
      output_parser: "go"
    }
    {
      id: "db-reset"
      name: "Reset Database"
      commands: [
        ["./bin/dbutil", "reset"]
        ["./bin/dbutil", "seed"]
      ]
      confirm: true
      confirm_message: "This will delete all data. Continue?"
      restart_services: true
    }
    {
      id: "deploy"
      name: "Deploy"
      inputs: [
        { name: "environment", type: "select", label: "Environment", options: ["staging", "production"], default: "staging", required: true }
        { name: "deploy_date", type: "datepicker", label: "Deploy Date" }
        { name: "dry_run", type: "checkbox", label: "Dry run", default: false }
      ]
      confirm: true
      confirm_message: "Deploy to {{ .Inputs.environment }}?"
      command: ["./deploy.sh", "--env={{ .Inputs.environment }}", "--date={{ .Inputs.deploy_date }}", "{{ if .Inputs.dry_run }}--dry-run{{ end }}"]
    }
  ]

  ui: {
    theme: "auto"
    notifications: {
      enabled: true
      events: ["service.crashed", "workflow.finished", "notify.done", "notify.error"]
      failures_only: false
    }
  }
}
```

## Section Reference

### project

```hjson
project: {
  name: "myapp"           // Project name (shown in UI)
  description: "..."      // Optional description
}
```

### server

```hjson
server: {
  host: "127.0.0.1"       // Bind address (use 0.0.0.0 for remote)
  port: 1234              // HTTP port
  tls_cert: "path"        // TLS certificate (for HTTPS)
  tls_key: "path"         // TLS private key
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `host` | `"127.0.0.1"` | Bind address. Use `"0.0.0.0"` to allow remote access. |
| `port` | `1234` | HTTP server port |
| `tls_cert` | (none) | Path to TLS certificate for HTTPS |
| `tls_key` | (none) | Path to TLS private key |

### worktree

```hjson
worktree: {
  repo_dir: "."                     // Directory for git worktree discovery
  create_dir: ".."                  // Directory where new worktrees are created
  discovery: {
    mode: "git"                     // Discovery mode
  }
  binaries: {
    path: "{{.Worktree.Root}}/bin"  // Where binaries are built
  }
  lifecycle: {
    on_create: [                    // Run once when worktree is created
      { name: "setup", command: ["make", "setup"], timeout: "5m" }
    ]
    pre_activate: [                 // Run before each activation
      { name: "build", command: ["make", "build"], timeout: "2m" }
    ]
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `repo_dir` | `"."` (current directory) | Root directory for git worktree discovery |
| `create_dir` | `".."` (parent directory) | Directory where new worktrees are created |
| `discovery.mode` | `"git"` | Discovery mode. Currently only `"git"` is supported. |
| `binaries.path` | `"{{.Worktree.Root}}/bin"` | Path to compiled binaries |

### watch

```hjson
watch: {
  debounce: "100ms"       // Wait for rapid file changes to settle
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `debounce` | `"100ms"` | Time to wait for rapid file changes to settle before triggering a restart |

### logging

Configures Trellis application logging (not service logs):

```hjson
logging: {
  level: "info"           // "debug", "info", "warn", "error"
  format: "json"          // "json", "text"
}
```

### services

```hjson
services: [
  {
    // Required
    name: "service-name"
    command: "./bin/app"  // String or array

    // Optional
    args: ["-port", "8080"]       // Arguments (if command is string)
    work_dir: "{{.Worktree.Root}}" // Working directory
    env: { KEY: "value" }         // Environment variables
    watch_binary: "path"          // Binary to watch for restarts
    watch_files: ["config.yaml"]  // Additional files to watch
    enabled: true                 // Enable/disable the service
    watching: true                // Include in binary watching
    depends_on: ["postgres"]      // Services that must start first

    // Restart policy (top-level fields)
    restart_policy: "on-failure"  // "always", "on-failure", "never"
    max_restarts: 3               // Give up after N attempts
    restart_delay: "1s"           // Wait between restarts

    // Or use nested restart block for policy only
    restart: {
      policy: "on-failure"
    }

    // Graceful shutdown
    stop_signal: "SIGTERM"        // Signal to send (default: SIGTERM)
    stop_timeout: "10s"           // Wait before SIGKILL

    // Log buffer size (default: 1000)
    log_buffer_size: 10000

    // Log parsing and display
    logging: {
      parser: {
        type: "json"
        timestamp: "ts"
        level: "level"
        message: "msg"
        id: "request_id"
        stack: "stack"
      }
      // Derived fields computed from parsed fields
      derive: {
        short_time: { from: "timestamp", op: "timefmt", args: { format: "15:04:05" } }
      }
      // Column layout (overrides logging_defaults)
      layout: [
        { field: "short_time", min_width: 8 }
        { field: "level", min_width: 5 }
        { field: "message", max_width: 0 }
      ]
    }
  }
]
```

### workflows

```hjson
workflows: [
  {
    // Required
    id: "workflow-id"
    name: "Workflow Name"
    description: "Description for CLI help"  // Shown in trellis-ctl workflow list/describe
    command: ["make", "build"]    // Single command

    // Or multiple commands (run sequentially)
    commands: [
      ["make", "clean"],
      ["make", "build"]
    ]

    // Optional
    timeout: "10m"
    output_parser: "go"           // "go", "go_test_json", "generic", "html", "none"
    confirm: false                // Require confirmation
    confirm_message: "Are you sure?"
    requires_stopped: ["api"]     // Services to stop first
    restart_services: false       // Restart watched services after

    // Input parameters (prompts user before execution)
    inputs: [
      {
        name: "environment"       // Variable name for templates
        type: "select"            // "text", "select", "checkbox", or "datepicker"
        label: "Target Environment"
        description: "Target deployment environment"
        options: ["staging", "production"]
        default: "staging"
        required: true
      }
      {
        name: "version"
        type: "text"
        label: "Version Tag"
        description: "Semantic version tag"
        placeholder: "e.g., v1.2.3"
        pattern: "^v[0-9]+\\.[0-9]+\\.[0-9]+$"  // Validation pattern
      }
      {
        name: "deploy_date"
        type: "datepicker"
        label: "Deploy Date"
        description: "Scheduled deployment date"
        // default: "2024-01-15"  // Optional, defaults to today
      }
      {
        name: "dry_run"
        type: "checkbox"
        label: "Dry run (don't actually deploy)"
        description: "Preview changes without applying"
        default: false
      }
    ]
  }
]
```

#### Workflow Inputs

Workflows can define input parameters that prompt the user with a dialog before execution:

| Input Type | Description | Fields |
|------------|-------------|--------|
| `text` | Free-form text input | `placeholder`, `default`, `required`, `description`, `pattern`, `allowed_values` |
| `select` | Dropdown with predefined options | `options` (array), `default`, `required`, `description` |
| `checkbox` | Boolean toggle | `default` (bool), `description` |
| `datepicker` | Date selector | `default` (YYYY-MM-DD string), `required`, `description` |

**Validation fields** (for CLI/automation safety):

| Field | Description |
|-------|-------------|
| `description` | Description shown in `trellis-ctl workflow describe` output |
| `pattern` | Regex pattern that values must match |
| `allowed_values` | Whitelist of allowed values (rejects anything else) |

Input values are available in command and confirm_message templates via `{{ .Inputs.name }}`:

```hjson
{
  id: "deploy"
  name: "Deploy"
  inputs: [
    { name: "env", type: "select", options: ["staging", "prod"], required: true }
    { name: "scheduled_date", type: "datepicker", label: "Scheduled Date" }
    { name: "dry_run", type: "checkbox", label: "Dry run", default: false }
  ]
  confirm: true
  confirm_message: "Deploy to {{ .Inputs.env }} on {{ .Inputs.scheduled_date }}?"
  command: ["./deploy.sh", "--env={{ .Inputs.env }}", "--date={{ .Inputs.scheduled_date }}", "{{ if .Inputs.dry_run }}--dry-run{{ end }}"]
}
```

The datepicker defaults to today's date if no default is specified. Date values are passed as `YYYY-MM-DD` strings (e.g., `2024-01-15`).

### terminal

```hjson
terminal: {
  backend: "tmux"

  tmux: {
    history_limit: 50000
    shell: "/bin/sh"              // Default shell
  }

  default_windows: [
    { name: "shell" }
    { name: "claude", command: "claude" }
  ]

  remote_windows: [
    // Option 1: SSH host + tmux session (auto-builds command)
    {
      name: "admin(1)"
      ssh_host: "admin.example.com"
      tmux_session: "main"
    }
    // Option 2: Explicit command
    {
      name: "prod"
      command: ["ssh", "-t", "prod.example.com", "tmux", "attach"]
    }
  ]

  // Custom keyboard shortcuts to terminals
  shortcuts: [
    { key: "cmd+1", window: "#api" }           // Jump to service
    { key: "cmd+2", window: "~nginx-logs" }    // Jump to log viewer
    { key: "cmd+3", window: "!admin(1)" }      // Jump to remote
  ]

  vscode: {
    binary: "code-server"
    port: 8443
    user_data_dir: "~/.config/code-server"
  }

  links: [
    { name: "Grafana", url: "http://localhost:3000/" }
  ]
}
```

### log_viewers

```hjson
log_viewers: [
  {
    name: "nginx-logs"

    source: {
      type: "ssh"                 // "file", "ssh", "command", "docker", "kubernetes"
                                  // Note: "service" sources are auto-generated for services with parsers
      host: "web01.example.com"
      path: "/var/log/nginx"      // Log directory
      current: "access.log"       // Active log file name
      rotated_pattern: "access.log.*"  // Pattern for rotated logs
      decompress: "zcat"          // Command to decompress rotated files
      follow: true                // Follow log output (default: true)
      since: "1h"                 // How far back to start
    }

    parser: {
      type: "json"                // "json", "logfmt", "regex", "syslog", "none"
      timestamp: "time"
      level: "status"
      message: "request"
      id: "request_id"
    }

    // Derived fields computed from parsed fields
    derive: {
      short_time: { from: "timestamp", op: "timefmt", args: { format: "15:04:05" } }
    }

    // Column layout (overrides logging_defaults)
    layout: [
      { field: "short_time", min_width: 8 }
      { field: "level", min_width: 5 }
      { field: "message", max_width: 0 }
    ]

    buffer: {
      max_entries: 10000          // Max entries in memory
    }
  }
]

// Global log viewer settings
log_viewer_settings: {
  idle_timeout: "5m"              // Stop idle viewers after this duration
}
```

**Log viewer defaults:**

| Field | Default | Description |
|-------|---------|-------------|
| `source.follow` | `true` | Follow log output in real-time |
| `source.since` | `"1h"` | How far back to start reading when connecting |
| `buffer.max_entries` | `10000` | Maximum entries to keep in memory |
| `log_viewer_settings.idle_timeout` | `"5m"` | Stop idle viewers after this duration |

```hjson
```

### trace

```hjson
trace: {
  reports_dir: "traces"
  max_age: "7d"
}

trace_groups: [
  {
    name: "api-flow"
    log_viewers: ["nginx-logs", "api-logs", "db-logs"]
  }
]
```

**Auto-generated `services` group:** When services have `logging.parser` configured (directly or via `logging_defaults`), Trellis automatically creates `svc:*` log viewers and a `services` trace group. Use `trellis-ctl trace <id> services -since 1h` to search across dev service logs with no additional configuration. If you define a `services` trace group in config, the auto-generated viewers are appended to it.

### crashes

```hjson
crashes: {
  reports_dir: ".trellis/crashes"
  max_age: "7d"
  max_count: 100
}
```

### logging_defaults

```hjson
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
  // Derived fields computed from parsed fields
  derive: {
    short_time: { from: "timestamp", op: "timefmt", args: { format: "15:04:05" } }
  }
  // Default column layout
  layout: [
    { field: "short_time", min_width: 8 }
    { field: "level", min_width: 5 }
    { field: "message", max_width: 0 }  // 0 = fill remaining
  ]
}
```

### events

```hjson
events: {
  // Event history settings
  history: {
    max_events: 10000           // Maximum events to keep in memory
    max_age: "1h"               // Maximum age of events to keep
  }

  // Webhooks to notify on events
  webhooks: [
    {
      id: "slack"
      url: "https://hooks.slack.com/services/..."
      events: ["service.crashed", "workflow.finished"]
    }
  ]
}
```

### ui

```hjson
ui: {
  theme: "auto"                   // "light", "dark", "auto"

  terminal: {
    font_family: "Monaco, monospace"
    font_size: 14
    cursor_blink: true
  }

  notifications: {
    enabled: true
    events: ["service.crashed", "workflow.finished"]
    failures_only: true
    sound: false
  }

  editor: {
    // For remote Trellis, enables vscode-remote:// URLs
    remote_host: "devbox.example.com"
  }
}
```

## Template Variables

| Variable | Description |
|----------|-------------|
| `{{.Project.Name}}` | Project name from config |
| `{{.Project.Root}}` | Project root directory |
| `{{.Worktree.Root}}` | Worktree root directory |
| `{{.Worktree.Branch}}` | Current branch name |
| `{{.Worktree.Binaries}}` | Configured binaries path |
| `{{.Worktree.Name}}` | Worktree directory name |
| `{{.Service.Name}}` | Current service name |
| `{{.Inputs.<name>}}` | Workflow input value (in workflow commands/confirm_message only) |

## Template Functions

| Function | Description | Example |
|----------|-------------|---------|
| `slugify` | Convert to slug | `{{.Branch \| slugify}}` |
| `replace` | String replace | `{{.Name \| replace "-" "_"}}` |
| `upper` | Uppercase | `{{.Name \| upper}}` |
| `lower` | Lowercase | `{{.Name \| lower}}` |
| `default` | Default value | `{{.Port \| default 8080}}` |
| `quote` | Shell quote | `{{.Path \| quote}}` |
