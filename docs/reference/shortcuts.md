---
title: "Keyboard Shortcuts"
weight: 24
---

# Keyboard Shortcuts

Trellis provides keyboard shortcuts throughout the web interface for quick navigation and control.

## Global Shortcuts

These shortcuts work from any page in Trellis:

| Shortcut | Action |
|----------|--------|
| `Cmd/Ctrl + P` | Open navigation picker |
| `Cmd/Ctrl + /` | Open workflow picker (local terminals only) |
| `Cmd/Ctrl + Backspace` | Open history picker (recently visited screens) |
| `Cmd/Ctrl + H` | Show keyboard shortcut help |

## Navigation Picker (Cmd+P)

The navigation picker provides quick access to all destinations:

| Prefix | Type | Example |
|--------|------|---------|
| `/` | Pages | `/ Status`, `/ Worktrees`, `/ Trace`, `/ Events` |
| `@` | Local terminals | `@main - dev`, `@feature-auth - claude` |
| `!` | Remote terminals | `!admin(1)` |
| `#` | Services | `#api`, `#worker` |
| `~` | Log viewers | `~nginx-logs`, `~api-logs` |
| `>` | External links | `> Grafana`, `> Docs` |

Type to filter/search destinations. Press Enter to select, Escape to cancel.

## History Picker (Cmd+Backspace)

Quickly return to recently visited screens:

- Shows up to 50 recent entries
- Most recent first (the screen you just left appears at top)
- Current screen is excluded
- History is shared across all pages via session storage

Use arrow keys to navigate, Enter to select, Escape to cancel.

## Terminal Shortcuts

When focused on a terminal:

| Shortcut | Action |
|----------|--------|
| `Cmd/Ctrl + E` | Open VS Code editor for current worktree |
| `Ctrl + Escape` | Return from editor iframe to terminal (same-origin only) |
| `Shift + Enter` | Insert newline without executing command |

## Editor Toggle (Cmd+E)

Quickly switch between the terminal and VS Code:

- From terminal: Opens VS Code for the current worktree
- From editor: Returns to the terminal view
- Hidden for remote terminals (no associated worktree)

## Configuration

Customize terminal appearance in your config:

```hjson
{
  ui: {
    terminal: {
      font_family: "Monaco, monospace"
      font_size: 14
      cursor_blink: true
    }
  }
}
```
