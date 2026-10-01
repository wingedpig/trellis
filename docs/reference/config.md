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

  // Reverse proxy (mirrors production routing)
  proxy: [
    {
      listen: ":443"
      tls_tailscale: true
      routes: [
        { path_regexp: "^/api/.+", upstream: "localhost:3001" }
        { upstream: "localhost:3000" }
      ]
    }
  ]

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

  cases: {
    dir: "trellis/cases"
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

  log_viewer_settings: {
    idle_timeout: "5m"
    disconnect_grace: "30s"
    auto_pause_rate: 30
  }

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
  host: "100.80.99.38"    // Bind address (use 0.0.0.0 for all interfaces)
  port: 1234              // Server port
  tls_tailscale: true      // Automatic Tailscale HTTPS certificates
  // Or use a static certificate pair instead:
  // tls_cert: "path"
  // tls_key: "path"
  public_url: "https://mybox.tailnet.ts.net:1234"  // External URL and TLS SNI name
  allowed_origins: [      // Extra cross-origin browser origins permitted
    "https://review.example.com"
  ]
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `host` | `"127.0.0.1"` | Bind address. Use `"0.0.0.0"` to allow remote access. |
| `port` | `1234` | Server port |
| `tls_tailscale` | `false` | Fetch and automatically renew HTTPS certificates through the local Tailscale daemon. |
| `tls_cert` | (none) | Path to TLS certificate for HTTPS |
| `tls_key` | (none) | Path to TLS private key |
| `public_url` | (none) | External URL the UI is reachable at (e.g., behind a reverse proxy). Automatically permitted as a browser origin. |
| `allowed_origins` | `[]` | Additional cross-origin browser origins permitted to call the API and open WebSockets. Loopback (`localhost`, `127.0.0.1`, `::1`) is always allowed. |

When `host` is loopback-only the server runs in DNS-rebinding-safe mode: requests are rejected unless the Host header is loopback or appears in `allowed_origins`/`public_url`. Binding to `0.0.0.0` (or any non-loopback address) is treated as opt-in to wide network access — the Host gate is relaxed, but Origin-based CORS still blocks browser-driven cross-origin attacks. List your external hostname in `public_url` (or `allowed_origins`) so a browser loading the UI from that address gets an Origin match.

`tls_tailscale` and `tls_cert`/`tls_key` are mutually exclusive. With
`tls_tailscale: true`, Trellis obtains certificates on demand from the local
Tailscale daemon and renews them automatically; no certificate files are
needed. Clients must connect using the machine's `*.ts.net` hostname so the TLS
handshake includes the correct SNI name. Set `public_url` to that HTTPS URL when
`host` is an IP address or wildcard bind address.

### proxy

Configures reverse proxy listeners for routing requests to backend services. Useful for mirroring production routing (e.g., Caddy/nginx) in development. WebSocket upgrades are handled automatically.

```hjson
proxy: [
  {
    listen: ":1001"
    tls_tailscale: true
    routes: [
      { upstream: "localhost:1000" }
    ]
  }
  {
    listen: ":443"
    tls_tailscale: true
    routes: [
      { path_regexp: "askws", upstream: "localhost:3000" }
      { path_regexp: "^/g/.+/chatws", upstream: "localhost:3002" }
      { path_regexp: "^/api/.+", upstream: "localhost:3001" }
      { upstream: "localhost:3000" }
    ]
  }
]
```

**Proxy listener fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `listen` | yes | Address to bind (e.g., `":443"`, `"0.0.0.0:8080"`, `":1001"`) |
| `tls_tailscale` | no | Use Tailscale daemon for automatic TLS certificates. |
| `tls_cert` | no | Path to TLS certificate. Supports `~` expansion. |
| `tls_key` | no | Path to TLS private key. Supports `~` expansion. |
| `tailscale_node` | no | Bind this listener on an additional embedded Tailscale node (see [tailscale_nodes](#tailscale_nodes)) instead of host interfaces. |
| `routes` | yes | Ordered list of route rules. First match wins. |

`tls_tailscale` and `tls_cert`/`tls_key` are mutually exclusive. When `tls_tailscale` is true, certificates are fetched automatically from the local Tailscale daemon — no cert files needed. This matches Caddy's built-in Tailscale TLS behavior.

**Route fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `path_regexp` | no | Regex to match against request path. Omit for catch-all. |
| `upstream` | yes | Target address (`host:port`). `http://` prefix is optional. |

Routes are evaluated in order — the first matching route handles the request. A route without `path_regexp` matches all requests (catch-all). Place catch-all routes last.

Template variables (`{{.Worktree.*}}`) are supported in `listen` and `upstream` values.

### tailscale_nodes

`tls_tailscale` can only serve the host machine's own Tailscale name, because the local `tailscaled` issues certificates only for its own node. `tailscale_nodes` adds **extra Tailscale nodes embedded in the Trellis process** (via `tsnet`), each with its own hostname, tailnet IP, MagicDNS name and Let's Encrypt certificate. From any tailnet device, each node looks like a separate machine. Use this when you need a second HTTPS site on port 443 under a *different hostname*, for example because cookies, WebAuthn RP IDs or host-based routing ignore the port.

```hjson
tailscale_nodes: [
  {
    hostname: "markbook-ent"                        // becomes markbook-ent.<tailnet>.ts.net
    state_dir: "~/.trellis/tsnet/markbook-ent"      // optional; this is the default
    auth_key_env: "TRELLIS_TS_AUTHKEY"              // optional; only used for first registration
    ephemeral: false                                // optional
  }
]

proxy: [
  {
    listen: ":443"
    tailscale_node: "markbook-ent"
    routes: [
      { path_regexp: "^/api/v1/.*$", upstream: "localhost:3001" }
      { upstream: "localhost:3000" }
    ]
  }
]
```

**Node fields:**

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `hostname` | yes | — | Node name on the tailnet. A DNS label: `[a-z0-9-]`, 1–63 chars, no leading or trailing hyphen. Unique within `tailscale_nodes`. |
| `state_dir` | no | `~/.trellis/tsnet/<hostname>` | Persistent node state: keys, machine identity, certificate cache. Created with mode 0700. Must be exclusive (see below). |
| `auth_key_env` | no | (none) | Name of an environment variable holding a Tailscale auth key. The key itself never goes in the config and is never logged. Ignored once `state_dir` holds a logged-in node. Without it, a login URL is shown instead. |
| `ephemeral` | no | `false` | Ephemeral nodes are removed from the tailnet when they go offline. A dev hostname should normally be stable. |

**Listeners on a node.** When a proxy listener sets `tailscale_node`:

- It binds on the node's tailnet netstack only, never on host interfaces. `listen` must be a bare port (`":443"`, `":1003"`); a host part is an error.
- TLS is automatic, with a certificate for `<hostname>.<tailnet>.ts.net`. `tls_tailscale`, `tls_cert` and `tls_key` must be unset.
- Routes, `Host` preservation, `X-Forwarded-*` headers and WebSocket tunneling work as on host listeners. Upstreams are still dialed from the host, so `localhost:3000` is the Mac's loopback.
- Port conflicts are checked per node: `:443` on the host and `:443` on `markbook-ent` don't conflict, but two listeners on the same node and port do. Ports are compared as numbers, so `:443` and `:0443` count as the same port. Several listeners can share one node.
- Host listeners never wait for nodes. A node listener starts serving as soon as its node is ready, with no restart needed.

A node that no listener references is not started and never joins the tailnet (the config loader logs a warning).

**Structural errors abort startup.** Unlike most config warnings, these stop Trellis from starting: a missing, invalid or duplicate `hostname`; a duplicate or nested `state_dir`; a `tailscale_node` that isn't declared; `tailscale_node` combined with `tls_tailscale`/`tls_cert`/`tls_key`; a node `listen` with a host part or no port; and two listeners on the same node and port. Runtime problems (login, certificates, a held lock, key expiry) never abort startup. They show up in the node's status instead.

**State directory exclusivity.** Each node needs its own `state_dir`. Two servers sharing one would share a node identity and overwrite each other's state.

- Within one config, directories are compared after `~` expansion, making them absolute, cleaning them and resolving symlinks, so `~/x`, `/Users/me/x/` and a symlinked parent all count as the same directory. Equal or nested directories are an error.
- Across processes (a second Trellis instance, or another project using the default path with the same hostname), each node takes an exclusive lock on `<state_dir>/.trellis.lock` before starting. If another process holds it, that node goes to `failed` with an error naming the directory. Other nodes and listeners are unaffected. The lock is released automatically if the process dies.

**First run and login.**

1. Start Trellis. With no auth key, the node enters `needs-login` and Trellis logs a prominent line with a login URL:
   `tailscale node markbook-ent: NEEDS LOGIN — visit https://login.tailscale.com/a/… to add this node to your tailnet`.
   The same URL is shown by `trellis-ctl tailscale-nodes`.
2. Open the URL and approve the machine. If your tailnet requires device approval, the node shows `needs-approval` until you approve it in the admin console.
3. The node reaches `running` and its listeners start serving, with no restart.
4. The tailnet must have MagicDNS and HTTPS certificates enabled. If either is off, the node shows `error` with a pointer to the admin-console setting, and recovers on its own once you enable it.

Restarting Trellis brings the node back without logging in again, with the same FQDN. The first HTTPS request triggers certificate issuance, which takes a few seconds; after that the certificate is cached in `state_dir` and renewed automatically.

To skip the interactive login, create an auth key in the admin console (preferably one-off and non-reusable), then `export TRELLIS_TS_AUTHKEY=tskey-…` before starting Trellis. An untagged (user) key makes a user-owned node with the same ACL reachability as your machine, which is the recommended setup for dev. A tagged key gives a node with key expiry disabled by default, but it needs ACL rules granting access to the tag.

**Key expiry.** A user-owned node's key expires after the tailnet's expiry period (180 days by default). Trellis sets `key_expires_soon` and logs a daily warning in the last 14 days. On expiry the node drops to `needs-login` and a fresh login URL is surfaced. Listeners stay open and resume once you log in again. To avoid this, **disable key expiry for the node** in the admin console (Machines → … → Disable key expiry).

**Hostname mismatch.** If a stale machine with the same name is still registered (for example, after deleting `state_dir`), the tailnet assigns `markbook-ent-1` instead. The node still serves, but Trellis logs an error and sets `hostname_mismatch`. To fix it, remove the stale machine in the admin console and rename the new one.

**Node states** (as reported by `trellis-ctl tailscale-nodes` and `GET /api/v1/tailscale-nodes`): `starting`, `needs-login`, `needs-approval`, `running`, `stopped`, `error` (recoverable) and `failed` (terminal: lock held or start error; restart Trellis after fixing the cause).

Notes: the node's hostname appears in public Certificate Transparency logs, as your host's ts.net name already does. Funnel is never used; node listeners are reachable only from your tailnet. Tailscale nodes are supported on macOS and Linux.

**Manual verification checklist:**

1. First run: the login URL appears in the log and in `trellis-ctl tailscale-nodes`; after approval the node reaches `running` with no restart.
2. From another tailnet device, `curl -v https://markbook-ent.<tailnet>.ts.net/` shows a valid certificate, and the upstream sees the correct `Host`.
3. Restarting Trellis brings the node back without a login and with the same FQDN.
4. Starting a second Trellis on the same config leaves the node `failed` (lock held).
5. Deleting `state_dir` while the old machine is still registered produces `markbook-ent-1` and the mismatch error.

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
    pre_merge: [                    // Run in the branch's worktree before `task finish` merges it
      { name: "test", command: ["make", "test"], timeout: "10m" }
    ]
    post_merge: [                   // Run in the default branch's worktree after the merge
      { name: "build", command: ["make", "build"] }
    ]
    pre_remove: [                   // Run in a worktree before it is removed
      { name: "drop-db", command: ["./scripts/drop-dev-db.sh"] }
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

Each lifecycle hook is `{ name, command, timeout }` (timeout defaults to `5m`). Hooks in a phase run in order and stop at the first failure.

| Phase | Runs in | On failure |
|-------|---------|------------|
| `on_create` | the new worktree, once | logged; the worktree is still created |
| `pre_activate` | the worktree being activated | activation stops |
| `pre_merge` | the branch's worktree, after the rebase (for the `ff` strategy), before the merge | nothing is merged |
| `post_merge` | the default branch's worktree, after the merge | reported; the merge stands |
| `pre_remove` | the worktree being removed (by Remove or `task finish`) | the worktree is not removed |

Hooks get `TRELLIS_HOOK_PHASE`, `TRELLIS_WORKTREE_NAME`, `TRELLIS_WORKTREE_PATH`, and `TRELLIS_BRANCH` in their environment. `pre_merge` and `post_merge` also get `TRELLIS_MERGE_BRANCH`, `TRELLIS_MERGE_BASE`, `TRELLIS_MERGE_STRATEGY`, and `TRELLIS_MERGE_WORKTREE_PATH`, since `post_merge` runs outside the branch's worktree.

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
        // default: "2026-01-15"  // Optional, defaults to today
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

The datepicker defaults to today's date if no default is specified. Date values are passed as `YYYY-MM-DD` strings (e.g., `2026-01-15`).

### terminal

```hjson
terminal: {
  backend: "tmux"

  tmux: {
    history_limit: 50000
    shell: "/bin/sh"              // Default shell
  }

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
    mode: "live"                 // "live" (default) or "explore" — see Modes below
    timezone: "America/New_York" // IANA zone the remote host writes timestamps in
                                  // (inherits logging_defaults.timezone; see Timezones below)

    source: {
      type: "ssh"                 // "file", "ssh", "command", "docker", "kubernetes"
                                  // Note: "service" sources are auto-generated for services with parsers
      host: "web01.example.com"
      path: "/var/log/nginx"      // Log directory
      current: "access.log"       // Active log file name
      active_pattern: ""          // Alternative to current, for servers that cycle
                                  // through a set of filenames rather than writing
                                  // one stable file (ssh only; requires mode:
                                  // "explore"; mutually exclusive with current).
                                  // See Concepts › Logging, SSH Source
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
  idle_timeout: "5m"              // Stop viewers not accessed at all after this duration ("0" disables)
  disconnect_grace: "30s"         // Stop the tail this long after the last watcher disconnects ("0" disables)
  auto_pause_rate: 30             // Lines/sec that triggers UI auto-pause in the browser (0 disables)
}
```

**Modes:**

| Mode | Behavior |
|------|----------|
| `live` (default) | Opening the viewer starts tailing the source immediately and follows new entries. |
| `explore` | For high-volume logs (e.g. nginx access logs). Opening the viewer does not start the tail — the server loads a static snapshot of the ~200 most recent lines read directly from the end of the file (a byte-offset backward read), and the UI opens paused with search/scrollback as the primary workflow. A **Go live** button in the header starts the tail and switches to streaming. Scrolling up to page back through history, and history search, work the same as in `live` mode. |

`explore` mode requires a source that supports backward reads — `file` and `ssh`. For `docker`, `kubernetes`, and `command` sources, an `explore`-mode viewer falls back to starting the tail immediately but still opens paused, so the UI behaves the same even though the tail is already running underneath. `mode` is validated at config load; the only accepted values are `"live"`, `"explore"`, or unset.

**Log viewer defaults:**

| Field | Default | Description |
|-------|---------|-------------|
| `mode` | `"live"` | `"live"` or `"explore"` — see Modes above |
| `timezone` | trellis host's local zone | IANA name of the zone the remote host writes its log timestamps in; inherits `logging_defaults.timezone` — see Timezones below |
| `source.follow` | `true` | Follow log output in real-time |
| `source.since` | `"1h"` | How far back to start reading when connecting |
| `buffer.max_entries` | `10000` | Maximum entries to keep in memory |
| `log_viewer_settings.idle_timeout` | `"5m"` | Stop viewers that haven't been accessed at all in this long (`"0"` disables) |
| `log_viewer_settings.disconnect_grace` | `"30s"` | Stop the tail this long after the last watcher (WebSocket subscriber) disconnects — e.g. when you navigate away from the page. Reconnecting within the grace period keeps the tail warm. REST API polling also counts as activity and keeps the tail alive. (`"0"` disables) |
| `log_viewer_settings.auto_pause_rate` | `30` | Lines/sec above which the UI automatically drops out of following in the browser, to avoid rendering every line of a burst (`0` disables) |

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

### cases

```hjson
cases: {
  dir: "trellis/cases"        // Cases directory relative to worktree root
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `dir` | `"trellis/cases"` | Directory for case storage, relative to worktree root. Archived cases are stored in a sibling `-archived` directory (e.g., `trellis/cases-archived/`). |

### agent

```hjson
agent: {
  install_skill: true         // Install the trellis skill file for coding agents
  claude: {                   // Model new Claude sessions start with
    model: "opus"
    effort: "high"
  }
  codex: {                    // Model new Codex sessions start with
    model: "gpt-6-astra"
    effort: "xhigh"
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `install_skill` | `true` | Whether Trellis installs its skill file at `.claude/skills/trellis/SKILL.md` in the repo and each worktree (on startup and on worktree creation), teaching coding agents to use `trellis-ctl`. Installed copies carry a `managed-by: trellis` marker and are refreshed when the bundled skill changes; copies without the marker (user-edited) are never touched. |
| `claude.model` | `""` | Model alias new Claude sessions start with: `opus`, `sonnet`, `haiku`, or `fable`. Empty uses your Claude Code settings (the picker's **Default**). |
| `claude.effort` | `""` | Effort for `claude.model`: `low`, `medium`, `high`, `xhigh`, or `max`. Empty uses the model's default effort. Haiku takes no effort. |
| `codex.model` | `""` | Model new Codex sessions start with, e.g. `gpt-6-astra`. Empty uses the model from `~/.codex/config.toml`. |
| `codex.effort` | `""` | Reasoning effort for `codex.model`. The pair must match an entry in the [Codex model picker](/docs/pages/claude/#codex-model-picker); models offered only at their default effort take no effort. |

The `claude` and `codex` defaults apply only to sessions created after startup (including forks and imports); existing sessions keep their saved choice, and the footer picker can still change any session. Unknown model/effort pairs fail config validation.

### logging_defaults

```hjson
logging_defaults: {
  timezone: "America/New_York"  // Default production log timezone (IANA name)
                                // for every viewer that doesn't set its own
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

#### Timezones

Historical log search — scrollback paging and `/trace` — filters remote log files by a time window. For `ssh` sources, Trellis narrows the search by grepping the remote files for the hours/days in range, and it must express those in **the timezone the remote host writes its timestamps in**. Set that with `timezone` (an IANA name like `America/New_York` or `UTC`), per-viewer or as a `logging_defaults.timezone` default.

When `timezone` is unset, Trellis assumes the logs are in **the trellis host's own local timezone** — correct only when trellis runs in the same zone as the production hosts. If it doesn't (e.g. trellis on a UTC server, logs in US/Eastern), traces come back empty or with the wrong hours until you set `timezone`. The value also interprets log timestamps that carry no offset of their own; timestamps that already include an offset or `Z` are unaffected. Invalid IANA names are rejected at config load.

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
  log_terminal: "dev"             // Default terminal window for Cmd+L jump

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
