---
title: "Web Interface"
weight: 15
---

# Web Interface Pages

Trellis provides a web interface for managing your development environment.

## [Terminal](/docs/pages/terminal/)

The main view—access local terminals (@), Claude sessions (@), service logs (#), log viewers (~), and remote windows (!) through the navigation picker.

## [Claude](/docs/pages/claude/)

AI-assisted development with Claude Code. Create per-worktree chat sessions, save transcripts to cases, make intermediate commits or wrap up a case in one click, and continue previous conversations.

## [Status](/docs/pages/status/)

Monitor all configured services—see which are running or stopped, start/stop individual services, or control all services at once.

## [Worktrees](/docs/pages/worktrees/) (Home Page)

The home page (`/`). Manage git worktrees—view status, create new worktrees, switch between them, and remove old ones. Includes links to documentation, GitHub, and the mailing list.

## [Events](/docs/pages/events/)

View a timeline of system events including service starts, crashes, worktree switches, and workflow executions.

## [Crashes](/docs/pages/crashes/)

Review crash reports when services exit unexpectedly. View stack traces, exit codes, and related trace IDs.

## [Cases](/docs/pages/cases/)

The durable record of a worktree's effort — one open case per worktree, created lazily on first commit. Tracks notes, evidence, transcripts, traces, a per-commit timeline, and a generated searchable summary written at wrap-up.

## [Session Inbox](/docs/pages/inbox/)

A chromeless popup that lists every live Claude and Codex session across worktrees with a real-time "running / needs you" badge. Click a row and the foreground Trellis window jumps to that session.

## [Trace](/docs/pages/trace/)

Execute distributed traces across multiple log sources to correlate events by trace ID or request ID.

## [Usage](/docs/pages/usage/)

Token usage and cost for Claude Code and Codex, computed from the agents' local transcript files — daily totals, per-worktree attribution, and the most expensive sessions. A header badge shows today's spend on every page.
