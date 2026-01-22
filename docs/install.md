---
title: "Installation"
weight: 2
---

# Installing Trellis

Trellis is distributed as a single static binary with no external dependencies beyond tmux.

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

## Next Steps

Continue to [Quickstart](/docs/quickstart/) to configure your first project.
