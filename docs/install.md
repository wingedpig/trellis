---
title: "Installation"
weight: 2
---

# Installing Trellis

Trellis consists of two static binaries with no external dependencies beyond tmux:
- **trellis** — The main server (web UI, API, service management)
- **trellis-ctl** — The command-line tool for interacting with the server

## Requirements

- **tmux** (required) - Terminal multiplexer for session management
- **Go 1.25+** (for building from source)

## Install from Source

```bash
# Clone the repository
git clone https://github.com/wingedpig/trellis.git
cd trellis

# Build both binaries
make build

# This creates:
#   trellis       - The main server
#   trellis-ctl   - The CLI tool
```

## Install tmux

Trellis requires tmux for terminal management.

**macOS:**
```bash
brew install tmux
```

**Ubuntu/Debian:**
```bash
sudo apt install tmux
```

**Fedora/RHEL:**
```bash
sudo dnf install tmux
```

## Verify Installation

```bash
# Check trellis version
./trellis -v

# Check tmux is available
tmux -V
```

## Optional: Install Globally

Move the binaries to a location in your PATH:

```bash
sudo mv trellis trellis-ctl /usr/local/bin/
```

Or add the Trellis directory to your PATH:

```bash
export PATH="$PATH:/path/to/trellis"
```

## AI Assistant Integration

Trellis includes a skill file that teaches AI coding assistants (Claude Code, Codex, etc.) how to use `trellis-ctl` effectively.

### Claude Code

Copy the skill file to your project's Claude skills directory:

```bash
mkdir -p .claude/skills/trellis
cp /path/to/trellis/SKILL.md .claude/skills/trellis/SKILL.md
```

Or create a symlink to always use the latest version:

```bash
mkdir -p .claude/skills/trellis
ln -s /path/to/trellis/SKILL.md .claude/skills/trellis/SKILL.md
```

### Codex

Codex uses `AGENTS.md` files placed in your repository. Copy the Trellis skill content to your project root:

```bash
cp /path/to/trellis/SKILL.md AGENTS.md
```

Or append to an existing `AGENTS.md`:

```bash
cat /path/to/trellis/SKILL.md >> AGENTS.md
```

Codex discovers `AGENTS.md` files hierarchically from the Git root down to your current directory, concatenating them. See the [Codex AGENTS.md documentation](https://developers.openai.com/codex/guides/agents-md) for details.

### What the Skill Provides

The skill file teaches AI assistants to:
- Check service status and view logs
- Filter logs by time, level, and pattern
- Run workflows and switch worktrees
- Debug crashes using crash reports
- Investigate production errors with distributed tracing
- Send notifications when tasks complete

## Initialize a Project

After installation, use `trellis init` to create a configuration file:

```bash
cd your-project
trellis init
```

This interactive command walks you through:
- Project name
- Server port
- Services to manage
- Build workflow
- Log format

The generated `trellis.hjson` is fully commented to help you understand and customize all options.

For manual configuration or to see what options are available, see the [Configuration Reference](/docs/reference/config/).

## Troubleshooting

### tmux not found

Trellis requires tmux for terminal management. Install it using your package manager:

```bash
# macOS
brew install tmux

# Ubuntu/Debian
sudo apt install tmux

# Fedora/RHEL
sudo dnf install tmux
```

### Port already in use

If port 1234 (or your configured port) is in use:

```bash
# Find what's using the port
lsof -i :1234

# Use a different port
./trellis -port 8080
```

Or change the port in your `trellis.hjson`:

```hjson
server: {
  port: 8080
}
```

### Permission denied for log files

When using file-based log viewers, ensure Trellis has read access to the log files:

```bash
# Check permissions
ls -la /var/log/myapp/

# Add user to appropriate group (example for syslog group)
sudo usermod -a -G adm $USER
```

### SSH key prompts for remote logs

Remote log viewers and terminals use SSH. To avoid password prompts:

1. Ensure your SSH key is added to ssh-agent: `ssh-add ~/.ssh/id_rsa`
2. Verify you can connect without prompts: `ssh hostname`
3. Check `~/.ssh/config` for proper host configuration

### Docker socket access denied

For Docker log viewers, your user needs access to the Docker socket:

```bash
# Add user to docker group
sudo usermod -a -G docker $USER

# Log out and back in, then verify
docker ps
```

### kubectl context issues

For Kubernetes log viewers, ensure your context is set correctly:

```bash
# List contexts
kubectl config get-contexts

# Switch context
kubectl config use-context my-cluster

# Test access
kubectl get pods -n my-namespace
```

### Services not restarting on binary change

1. Verify `watch_binary` path matches the actual binary location
2. Check the `watch.debounce` setting isn't too high
3. Ensure `watching: true` (the default) isn't set to `false`

## State and Files

Trellis stores runtime state in the following locations:

### Per-Worktree State

These directories are created relative to each worktree's root:

| Directory | Contents | Cleanup |
|-----------|----------|---------|
| `.trellis/crashes/` | Crash reports (JSON) | `trellis-ctl crash clear` |
| `traces/` | Trace reports (JSON) | `trellis-ctl trace-report -delete <name>` |

Crash reports are automatically cleaned up after 7 days or when the count exceeds 100 (configurable via `crashes.max_age` and `crashes.max_count`).

### tmux Sessions

Trellis creates tmux sessions named `<project>` for the main worktree and `<project>-<branch>` for additional worktrees. For example, if your project is `myapp` with worktrees on `main` and `feature`:

```bash
# List Trellis-created sessions
tmux list-sessions | grep myapp

# Kill a specific session
tmux kill-session -t myapp-feature

# Kill all project sessions
tmux kill-server  # Warning: kills ALL tmux sessions
```

### Temporary Files

Terminal WebSocket connections use named pipes in `/tmp/`:

```
/tmp/trellis-pipe-<session>-<window>-<timestamp>.fifo
```

These are cleaned up automatically when connections close.

### Full Reset

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

## Next Steps

Continue to [Quickstart](/docs/quickstart/) to configure your first project.
