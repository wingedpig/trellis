---
title: "Quickstart"
weight: 3
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

In any Trellis-managed terminal, `TRELLIS_API` is automatically set:

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

## What's Next

- [Services](/docs/concepts/services/) - Configure service lifecycle and restarts
- [Worktrees](/docs/concepts/worktrees/) - Use git worktrees for parallel development
- [Configuration Reference](/docs/reference/config/) - Full config file documentation
