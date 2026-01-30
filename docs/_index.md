---
title: "Documentation"
weight: 1
---

# Trellis Documentation

Trellis is a **local web app** that acts as a control panel for your development environment. It brings together terminals, tmux sessions, git worktrees, and logs—both local and remote—into one place, with fast, keyboard-driven navigation.

**[Read the full overview](/docs/overview/)** to understand what Trellis does and how it works.

## At a Glance

- **Worktrees as environments** — Each git worktree gets its own terminals, running processes, and logs
- **Service supervision** — Run your app's components locally with automatic restart when binaries change
- **Terminal & tmux management** — Trellis creates and manages tmux sessions for you
- **Remote access** — SSH terminals and log streaming from staging/production in the same UI
- **Log search** — Tail, search, and correlate logs across local and remote systems
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
- [Status Page](/docs/pages/status/) - Monitor and control services
- [Worktrees Page](/docs/pages/worktrees/) - Manage git worktrees
- [Events Page](/docs/pages/events/) - View system event timeline
- [Crashes Page](/docs/pages/crashes/) - Review crash reports
- [Trace Page](/docs/pages/trace/) - Distributed tracing across log sources

## Reference

- [Configuration](/docs/reference/config/) - Complete config file reference
- [trellis-ctl](/docs/reference/trellis-ctl/) - Command-line interface
- [Keyboard Shortcuts](/docs/reference/shortcuts/) - Web interface navigation
- [Go Client Library](/docs/reference/client/) - Official Go client for the API
- [API](/api/) - REST and WebSocket API documentation
