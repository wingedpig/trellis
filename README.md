# Trellis

Trellis is a **local web app** that acts as a control panel for your development environment. It brings together terminals, tmux sessions, git worktrees, and logs—both local and remote—into one place, with fast, keyboard-driven navigation.

**[Documentation](https://trellis.dev/docs/)** | **[API Reference](https://trellis.dev/api/)**

## What Trellis Does

**On your machine:**
- Treat each git worktree as its own environment with isolated terminals, processes, and logs
- Run and supervise your app's services locally, with automatic restart when binaries change
- Create and manage tmux sessions without manual layout management
- Chat with Claude Code directly in the web UI, with per-worktree sessions and transcript management
- Track work items (bugs, features, investigations) with cases that link notes, transcripts, and trace reports
- Run reverse proxies with path-based routing and TLS support to mirror production routing locally

**Remote systems:**
- Open SSH terminals to staging/production from the same interface
- Tail and search remote logs, including rotated and compressed files
- Correlate requests across multiple hosts using trace IDs

**Navigation:**
- Keyboard-first web UI with pickers for rapid context switching
- Jump between worktrees, terminals, services, logs, Claude sessions, and workflows

## Quick Start

### Requirements

- **tmux** — Terminal multiplexer for session management
- **Go 1.25+** — For building from source

### Install

```bash
git clone https://github.com/wingedpig/trellis.git
cd trellis
make build
```

This creates two binaries in the repo root:
- `./trellis` — The main server
- `./trellis-ctl` — The CLI tool

> **Note:** If you modify `.qtpl` template files, you'll need [quicktemplate](https://github.com/valyala/quicktemplate): `go install github.com/valyala/quicktemplate/qtc@latest`

### Configure

Use `trellis init` to create a configuration file interactively:

```bash
cd your-project
./trellis init
```

Or create `trellis.hjson` manually in your project root:

```hjson
{
  project: {
    name: "myapp"
  }

  server: {
    port: 1234
  }

  services: [
    {
      name: "backend"
      command: "./bin/backend"
      watch_binary: "./bin/backend"
    }
  ]

  workflows: [
    {
      id: "build"
      name: "Build"
      command: ["make", "build"]
    }
  ]
}
```

### Run

```bash
./trellis
```

Open http://localhost:1234 in your browser.

### CLI

Use `trellis-ctl` to control Trellis from the command line:

```bash
./trellis-ctl status              # Check service status
./trellis-ctl logs backend        # View logs
./trellis-ctl workflow run build  # Run a workflow
./trellis-ctl crash newest        # View most recent crash
```

In Trellis-managed terminals, `TRELLIS_API` is set automatically. To use `trellis-ctl` without the `./` prefix, add it to your PATH or copy to `/usr/local/bin`.

## Key Features

### Automatic Restarts

When you recompile a watched binary, Trellis automatically restarts the service:

```bash
go build -o ./bin/backend ./cmd/backend
# Trellis detects the change and restarts the backend service
```

### Git Worktrees

Each worktree gets isolated services, terminals, and logs:

```bash
git worktree add ../myapp-feature -b feature-branch
./trellis-ctl worktree activate myapp-feature
```

### Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| `Cmd/Ctrl + P` | Navigation picker |
| `Cmd/Ctrl + /` | Workflow picker |
| `Cmd/Ctrl + Backspace` | History picker |
| `Cmd/Ctrl + H` | Show all shortcuts |

### Claude Code Integration

Each worktree can have multiple Claude Code chat sessions directly in the Trellis web UI. Sessions persist across restarts, and transcripts can be saved to cases or imported from previous exports.

Claude sessions appear in the navigation picker alongside terminals (`@main - Session 1` with a robot icon).

### Cases

Cases track units of work (bugs, features, investigations) within a worktree. Attach notes, evidence, Claude transcripts, and trace reports to build a durable record alongside your code.

### AI Assistant Skill File

Install the skill file for Claude Code or Codex:

```bash
# Claude Code
mkdir -p .claude/skills/trellis
cp /path/to/trellis/SKILL.md .claude/skills/trellis/SKILL.md

# Codex
cp /path/to/trellis/SKILL.md AGENTS.md
```

Crash reports provide AI assistants with the context they need to diagnose issues.

## Configuration

Trellis uses HJSON (JSON with comments). See the [configuration reference](https://trellis.dev/docs/reference/config/) for all options.

Key sections:
- `services` — Long-running processes to manage
- `workflows` — Builds, tests, and other commands
- `terminal` — Window configuration and remote terminals
- `log_viewers` — External log sources (SSH, Docker, K8s)
- `trace_groups` — Log viewers to search together for tracing
- `proxy` — Reverse proxy listeners with path-based routing
- `cases` — Work item storage configuration

## API

Trellis exposes a REST and WebSocket API on the configured port. See the [API documentation](https://trellis.dev/api/).

A Go client library is available:

```go
import "github.com/wingedpig/trellis/pkg/client"

c := client.New("http://localhost:1234")
services, _ := c.Services.List(ctx)
```

## Project Structure

```
cmd/
  trellis/          # Main server
  trellis-ctl/      # CLI tool
internal/
  api/              # HTTP handlers and routing
  app/              # Application initialization
  cases/            # Case (work item) management
  claude/           # Claude Code session management
  config/           # Configuration loading and validation
  crashes/          # Crash report management
  events/           # Event bus
  logs/             # Log viewing and parsing
  proxy/            # Reverse proxy with path-based routing
  service/          # Service lifecycle management
  terminal/         # Terminal/tmux integration
  trace/            # Distributed tracing
  watcher/          # Binary file watching
  workflow/         # Workflow execution and output parsing
  worktree/         # Git worktree discovery and switching
pkg/
  client/           # Go API client library
api/
  openapi.yaml      # API specification
views/              # Web UI templates (quicktemplate)
static/             # CSS and embedded static assets
docs/               # Documentation (Hugo site)
```

## License

Trellis is open source under the [Apache License 2.0](LICENSE).

Copyright 2026 Groups.io, Inc.
