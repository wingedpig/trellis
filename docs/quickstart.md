---
title: "Quickstart"
weight: 4
---

# Quickstart

Get Trellis running with your project in 5 minutes.

## 1. Create a Configuration File

The easiest way to create a configuration file is with the interactive setup:

```bash
cd your-project
trellis init
```

This walks you through setting up your project with prompts for services, workflows, and log format. The generated file is fully commented.

Alternatively, create `trellis.hjson` manually in your project root:

```hjson
{
  // Project metadata
  project: {
    name: "myapp"
  }

  // HTTP server settings
  server: {
    port: 1234
    host: "127.0.0.1"
  }

  // Services to run
  services: [
    {
      name: "backend"
      command: "./bin/backend"
      watch_binary: "./bin/backend"
    }
    {
      name: "frontend"
      command: ["npm", "run", "dev"]
      work_dir: "./frontend"
    }
  ]

  // Workflows (builds, tests, etc.)
  workflows: [
    {
      id: "build"
      name: "Build"
      command: ["make", "build"]
    }
    {
      id: "test"
      name: "Run Tests"
      command: ["go", "test", "./..."]
    }
  ]
}
```

## 2. Start Trellis

```bash
trellis
```

Trellis will:
1. Load your configuration
2. Create tmux sessions for each worktree
3. Start your services
4. Begin watching for binary changes
5. Start the HTTP server

## 3. Open the Web Interface

Open http://localhost:1234 in your browser.

From here you can:
- View service status
- Access terminal windows
- Run workflows
- View logs

**Keyboard shortcuts** make navigation fast:
- `Cmd/Ctrl + P` - Open navigation picker (search for any terminal, service, or page)
- `Cmd/Ctrl + Backspace` - Open history picker (recently visited screens)
- `Cmd/Ctrl + H` - Show all shortcuts

## 4. Use the CLI

From your project directory (or any subdirectory), `trellis-ctl` finds
`trellis.hjson` by walking up and connects to the configured host/port.
In Trellis-managed terminals, `TRELLIS_API` is also set automatically and
takes precedence:

```bash
# Check service status
trellis-ctl status

# View logs
trellis-ctl logs backend

# Run a workflow
trellis-ctl workflow run build

# List worktrees
trellis-ctl worktree list
```

## 5. Automatic Restarts

When you recompile a binary that a service is watching, Trellis automatically restarts that service. The flow:

1. You run `go build -o ./bin/backend ./cmd/backend`
2. Trellis detects `./bin/backend` changed
3. The `backend` service is gracefully restarted
4. New logs stream to the UI

## 6. Open an AI Session

This is where Trellis stops being just a service manager. On startup, Trellis installed a skill file into your repo (`.claude/skills/trellis/SKILL.md`) that teaches Claude Code how to drive Trellis itself — checking service status, reading logs, running workflows, and tracing errors via `trellis-ctl`.

Open the navigation picker (`Cmd/Ctrl + P`), choose your worktree's home page (`@main - Home`), and click **New Session** under Claude Sessions. Then try something like:

> Check the backend service logs and summarize any errors from the last hour.

Claude runs `trellis-ctl` on its own — no copy-pasting logs into a chat window. When it's done investigating something real, **Wrap Up** captures the transcript and notes as a [case](/docs/pages/cases/), committed alongside the fix.

The same surfaces exist for OpenAI Codex sessions.

## What's Next

- [AI Sessions](/docs/pages/claude/) - Sessions, cost tracking, plans, commits, and wrap-up
- [Pair Review & Checklist Runs](/docs/pages/pairing/) - Two AI sessions reviewing each other's work
- [Services](/docs/concepts/services/) - Configure service lifecycle and restarts
- [Worktrees](/docs/concepts/worktrees/) - Use git worktrees for parallel development
- [Configuration Reference](/docs/reference/config/) - Full config file documentation
