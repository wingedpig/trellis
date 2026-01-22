---
title: "Worktrees"
weight: 12
---

# Worktrees

Trellis treats each git worktree as a first-class development environment with isolated services, logs, and terminal sessions.

## What are Worktrees?

Git worktrees allow you to have multiple working directories from a single repository. Each worktree can have a different branch checked out:

```
~/src/myapp/          # main branch
~/src/myapp-feature/  # feature branch (worktree)
~/src/myapp-hotfix/   # hotfix branch (worktree)
```

Trellis extends this by giving each worktree its own:
- Service instances (binaries from that worktree's build)
- Terminal sessions
- Log buffers
- Workflow execution context

## Worktree Discovery

Trellis automatically discovers worktrees:

```hjson
{
  worktree: {
    discovery: {
      mode: "git"  // Use git worktree list
    }
  }
}
```

Discovery runs at startup.

## Active Worktree

One worktree is "active" at a time. The active worktree:
- Has its services running
- Is shown by default in the UI

Workflows run in the context of the currently viewed worktree, not necessarily the active one.

Switch worktrees with:
```bash
trellis-ctl worktree activate feature-branch
```

This:
1. Stops services in the current worktree
2. Switches to the new worktree
3. Starts services with binaries from the new worktree

## Worktree-Aware Configuration

Use template variables to make your config worktree-aware:

```hjson
{
  services: [
    {
      name: "api"
      command: "{{.Worktree.Root}}/bin/api"
      watch_binary: "{{.Worktree.Binaries}}/api"
      env: {
        CONFIG_PATH: "{{.Worktree.Root}}/config"
      }
    }
  ]
}
```

### Available Variables

| Variable | Description |
|----------|-------------|
| `{{.Worktree.Root}}` | Worktree root directory |
| `{{.Worktree.Branch}}` | Current branch name |
| `{{.Worktree.Binaries}}` | Configured binary path |
| `{{.Worktree.Name}}` | Worktree name (directory name) |

### Binary Path Configuration

Configure where binaries are located:

```hjson
{
  worktree: {
    binaries: {
      path: "{{.Worktree.Root}}/bin"
    }
  }
}
```

## Terminal Sessions

Each worktree gets its own tmux session with configured windows:

```hjson
{
  terminal: {
    default_windows: [
      { name: "shell" }
      { name: "claude", command: "claude" }
      { name: "logs", command: "tail -f logs/app.log" }
    ]
  }
}
```

Each worktree has its own tmux session.

## Parallel Development

With worktrees, you can:

1. **Work on multiple features simultaneously** - Each worktree has its own services running its own binaries

2. **Quick bug fixes** - Create a worktree from main, fix the bug, deploy, delete the worktree

3. **Code review** - Check out a PR in a worktree, run the services, test it

4. **AI-assisted development** - Have Claude working in one worktree while you work in another

## Worktree Events

| Event | Description |
|-------|-------------|
| `worktree.deactivating` | About to switch away from a worktree |
| `worktree.activated` | Switched to a worktree |
| `worktree.created` | New worktree discovered |
| `worktree.deleted` | Worktree removed |
| `worktree.hook.started` | Lifecycle hook started |
| `worktree.hook.finished` | Lifecycle hook completed |

## Creating Worktrees

Create worktrees with git:

```bash
# Create worktree with new branch
git worktree add ../myapp-feature -b feature-branch

# Create worktree from existing branch
git worktree add ../myapp-hotfix hotfix-branch
```

Trellis will discover the new worktree automatically.

## Removing Worktrees

```bash
# Remove the worktree directory
git worktree remove ../myapp-feature

# Or delete and prune
rm -rf ../myapp-feature
git worktree prune
```
