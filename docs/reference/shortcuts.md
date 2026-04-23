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
| `Cmd/Ctrl + H` | Open commands & shortcuts menu |

## Commands &amp; Shortcuts Menu

Click the keyboard icon in the top nav (or press `Cmd/Ctrl + H`) to open a tappable menu listing every action — useful on touch devices where modifier-key shortcuts aren't practical. Each row runs its action when tapped and dismisses the menu.

The menu lists:

- **Open navigation picker** — same as `Cmd/Ctrl + P`
- **Open history picker** — same as `Cmd/Ctrl + Backspace`. If no history has been recorded yet in the current tab session, an alert says so.
- On the terminal page additionally: **Open workflow picker** (when a workflow selector is visible) and **Toggle Terminal / Code view** (when a local worktree is active)
- **Custom shortcuts** configured for the current worktree (each appears with the assigned key combo as a label)

Custom shortcuts invoked from the menu run the same handler as the keyboard path, so the target screen is resolved and navigated to identically.

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

## Claude Shortcuts

When on the Claude chat page:

| Shortcut | Action |
|----------|--------|
| `Enter` | Send message |
| `Shift + Enter` | Insert newline without sending |
| `Escape` | Stop/cancel current response |

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
