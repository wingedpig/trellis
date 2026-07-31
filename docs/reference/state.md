---
title: "State and Files"
weight: 25
---

# State and Files

Trellis stores runtime state in the following locations.

## Per-Worktree State

These directories are created relative to each worktree's root:

| Directory | Contents | Cleanup |
|-----------|----------|---------|
| `.trellis/crashes/` | Crash reports (JSON) | `trellis-ctl crash clear` |
| `traces/` | Trace reports (JSON) | `trellis-ctl trace-report -delete <name>` |

Crash reports are automatically cleaned up after 7 days or when the count exceeds 100 (configurable via `crashes.max_age` and `crashes.max_count`).

## tmux Sessions

Trellis creates tmux sessions named `<project>` for the main worktree and `<project>-<branch>` for additional worktrees. For example, if your project is `myapp` with worktrees on `main` and `feature`:

```bash
# List Trellis-created sessions
tmux list-sessions | grep myapp

# Kill a specific session
tmux kill-session -t myapp-feature

# Kill all project sessions
tmux kill-server  # Warning: kills ALL tmux sessions
```

## Temporary Files

Terminal WebSocket connections use named pipes in `/tmp/`:

```
/tmp/trellis-pipe-<session>-<window>-<timestamp>.fifo
```

These are cleaned up automatically when connections close.

## Full Reset

To completely reset Trellis state:

```bash
# Stop Trellis
# (Ctrl+C or kill the process)

# Remove per-worktree state
rm -rf .trellis/ traces/

# Kill tmux sessions for this project
tmux kill-session -t myproject
tmux kill-session -t myproject-feature  # etc.

# Restart Trellis
./trellis
```

## Related

- [Installation](/docs/install/) — Setting Trellis up
- [Configuration: crashes](/docs/reference/config/#crashes) — Crash retention settings
