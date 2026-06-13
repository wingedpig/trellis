---
title: "Usage Page"
weight: 12
---

# Usage Page

**URL:** `/usage`

The Usage page shows token usage and cost for your AI coding agents — both Claude Code and OpenAI Codex — computed from the transcript files the agent CLIs write locally. No API keys or network calls are involved; Trellis reads the same on-disk data that tools like [ccusage](https://github.com/ccusage/ccusage) use.

Open it from the navigation picker (`Cmd+P`, type `/Usage`), the command palette (`Shift+Cmd+P`, "View: Token usage & costs"), or by clicking the cost badge in the header.

## Data Sources

| Agent | Source | Location |
|-------|--------|----------|
| Claude Code | Transcript JSONL files | `~/.claude/projects/` and `~/.config/claude/projects/` (honors `CLAUDE_CONFIG_DIR`) |
| Codex CLI | Rollout JSONL files | `~/.codex/sessions/` (honors `CODEX_HOME`) |

Costs are computed from token counts at current API list prices, including cache-read and cache-write rates. If you're on a subscription plan (Claude Max, ChatGPT Pro), the dollar figures are *what the usage would have cost* at API prices — useful for judging how much value you're getting from the subscription.

Files are parsed once and cached by modification time, so refreshes are fast even with months of history.

## Summary Cards

- **Today** — Total cost and tokens across all projects on this machine, with a Claude/Codex split when both have usage
- **Last N days** — Period total (7, 30, or 90 days, selectable)
- **Daily average** — Average cost over days that had usage
- **API calls** — Total number of model calls in the period

## Daily Usage

One row per calendar day across **all** Claude Code and Codex usage on the machine (not just this project): models used, input/output tokens, cache read/write tokens, and cost. Model badges (`opus-4-8`, `gpt-5.5`, ...) show which models drove the spend.

## By Worktree

Usage attributed to **this project's** worktrees, matched by the working directory recorded in each transcript. Worktree names link to the worktree home page.

## Top Sessions by Cost

The most expensive agent sessions in this project for the period, with an agent badge (claude/codex), the models used, and last activity time. Capped at the top 50 by cost.

## Header Cost Badge

Every Trellis page shows today's total agent spend in the header (e.g. `$12.40 today`), refreshed every 5 minutes. Click it to open the Usage page. The badge is hidden when there's no spend yet today.

## Per-Session Cost

Live cost also appears outside this page:

- **Claude chat footer** — The session's accumulated cost displays next to the context usage (e.g. `$1.23 · 45K / 1M tokens (4%)`); hover for a token and cost breakdown
- **Worktree home page** — Each Claude session row shows a cost badge

## Retention

Claude Code prunes transcripts after about 30 days by default, which bounds the history this page can show. To keep more, raise `cleanupPeriodDays` in your Claude Code `settings.json`.
