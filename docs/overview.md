---
title: "Overview"
weight: 2
---

# Trellis, in Plain Terms

Trellis is a **local web app** that acts as a control panel for your development environment. It brings together terminals, tmux sessions, git worktrees, and logs—both local and remote—into one place, with fast, keyboard-driven navigation.

---

## On Your Machine (Development Environment)

### Worktrees as First-Class Environments

Each git worktree represents a separate copy of your code. Trellis treats each worktree as its own environment, with its own terminals, running processes, and logs. Switching branches means switching environments, not re-wiring anything by hand.

### Terminal and tmux Management

Trellis creates and manages tmux sessions for you. It defines terminal windows and panes for shells, builds, tests, and diagnostics, so you don't have to manually manage tmux layouts.

### Running Your Application's Components Locally

Trellis runs and supervises the local instances of the services that make up your application—APIs, workers, daemons, etc.—as part of your development environment. When you rebuild a binary, Trellis detects the change and restarts the affected process automatically.

### Local Log Access

You can tail and search the logs produced by those locally running components directly from the Trellis UI.

---

## Remote Systems (Over SSH)

### Remote Terminals

Open terminals on remote machines (staging or production) over SSH directly inside the same web interface you use for local work.

### Tailing Remote Logs

Stream logs from remote hosts in real time, without maintaining separate SSH sessions.

### Grepping Remote Logs

Search through remote logs—including rotated and compressed logs—using standard Unix tools, but driven from a single UI.

### Multi-Host Log Correlation

Search for an ID across multiple remote machines at once, making it practical to follow a request as it moves through several services.

---

## Navigation and Speed

### Web UI, Keyboard-First

Trellis runs as a web app (typically on `localhost`) but is designed to be operated primarily from the keyboard.

### Pickers for Everything

A global picker lets you quickly jump between worktrees, terminals, running processes, logs, and workflows.

### Keyboard Shortcuts

Common actions—switching context, opening terminals, running commands—are bound to shortcuts so you rarely need to touch the mouse.
