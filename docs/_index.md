---
title: "Documentation"
weight: 1
---

# Trellis Documentation

Trellis is an **AI-native development environment** — a local web app where Claude Code and Codex sessions work alongside your terminals, services, logs, and distributed traces, all scoped to the git worktree you're working in. Your AI sessions can read the same production logs, traces, and crash reports you can, so investigations start from real data — and finished work is captured as a case, committed to git alongside the fix.

**[Read the full overview](/docs/overview/)** to understand what Trellis does and how it works.

## At a Glance

- **Worktrees as environments** — Each git worktree gets its own terminals, running processes, and logs
- **Service supervision** — Run your app's components locally with automatic restart when binaries change
- **Terminal & tmux management** — Trellis creates and manages tmux sessions for you
- **Claude Code & Codex sessions** — AI-assisted development with per-worktree chat sessions for either agent, transcript management, and a per-session model picker (model family for Claude, model + reasoning effort for Codex)
- **Pair review & checklist runs** — Wire two AI sessions into an automated implementer/reviewer loop, or drive them through a multi-phase checklist one reviewed phase at a time
- **Session inbox** — A floating popup listing every live Claude and Codex session across worktrees, highlighting the ones waiting on you
- **Usage & cost tracking** — Daily, per-worktree, and per-session token usage and cost for Claude Code and Codex
- **Cases** — Track units of work (bugs, features, investigations) with notes, links, transcripts, and trace reports
- **Remote access** — SSH terminals and log streaming from staging/production in the same UI
- **Log search** — Tail, search, and correlate logs across local and remote systems
- **Reverse proxy** — Mirror production routing locally with path-based routing and optional Tailscale TLS
- **Keyboard-first** — Pickers and shortcuts for rapid navigation without touching the mouse

## Getting Started

1. [Install Trellis](/docs/install/) - Download and set up the binary
2. [Quickstart](/docs/quickstart/) - Configure your first project in 5 minutes

## Learn the Concepts

- [Overview](/docs/overview/) - Trellis explained in plain terms
- [Services](/docs/concepts/services/) - How Trellis manages your dev services
- [Worktrees](/docs/concepts/worktrees/) - Git worktree integration and environment isolation
- [Logging](/docs/concepts/logging/) - Log sources, parsers, and filtering

## Web Interface

- [Terminal Page](/docs/pages/terminal/) - Main view: terminals, service logs, log viewers, remote windows
- [AI Sessions (Claude & Codex)](/docs/pages/claude/) - AI-assisted development with Claude Code and OpenAI Codex
- [Pair Review & Checklist Runs](/docs/pages/pairing/) - Automated implementer/reviewer loops between two AI sessions
- [Status Page](/docs/pages/status/) - Monitor and control services
- [Worktrees Page](/docs/pages/worktrees/) - Manage git worktrees
- [Cases Page](/docs/pages/cases/) - Track work items with a commit timeline, generated summary, transcripts, and traces
- [Session Inbox](/docs/pages/inbox/) - Floating popup listing live Claude + Codex sessions across worktrees
- [Events Page](/docs/pages/events/) - View system event timeline
- [Crashes Page](/docs/pages/crashes/) - Review crash reports
- [Trace Page](/docs/pages/trace/) - Distributed tracing across log sources
- [Usage Page](/docs/pages/usage/) - Token usage and cost for Claude Code and Codex

## Reference

- [Configuration](/docs/reference/config/) - Complete config file reference
- [trellis-ctl](/docs/reference/trellis-ctl/) - Command-line interface
- [Keyboard Shortcuts](/docs/reference/shortcuts/) - Web interface navigation
- [Go Client Library](/docs/reference/client/) - Official Go client for the API
- [API](/api/) - REST and WebSocket API documentation
