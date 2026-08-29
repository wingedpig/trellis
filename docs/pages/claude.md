---
title: "AI Sessions (Claude & Codex)"
weight: 2
---

# AI Sessions (Claude & Codex)

**URL:** `/claude/{worktree}/{session}` and `/codex/{worktree}/{session}`

Trellis provides an integrated chat interface for both Claude Code and OpenAI Codex. Each worktree can have multiple sessions of either agent, allowing you to work with AI assistance in the context of your development environment.

This page describes the Claude interface; nearly everything applies to Codex sessions too — see [Codex parity](#codex-parity) for the short list of differences.

![A Claude session that traced a production crash and wrote the fix](/docs/images/claude-session.png)

## Accessing Claude

- **From the worktree home page** (`/worktree/{name}`) — The Claude Sessions section lists all sessions for that worktree with buttons to create new sessions
- **From the navigation picker** (`Cmd+P`) — Claude sessions appear with the `@` prefix and a robot icon (e.g., `@main - Session 1`)
- **Direct URL** — `/claude/{worktree}/{session}` opens a specific session

## Session Management

### Creating Sessions

Click **New Session** on the worktree home page. You can optionally provide a display name; if left blank, sessions are auto-named (Session 1, Session 2, ...).

![A worktree home page with its Claude sessions, terminals, and case](/docs/images/worktree-home.png)

### Renaming Sessions

Click the pencil icon next to a session on the worktree home page to rename it.

### Trashing Sessions

Click the trash icon next to a session on the worktree home page. This moves the session to trash — the process is stopped but the session data is preserved.

### Archiving Sessions

Click the box icon next to a session on the worktree home page, or **Archive session** in the chat page's ⋮ menu. Archiving stops the process and takes the session out of the worktree listing, but keeps it indefinitely and searchable — the archive is for finished work you may want to look up later, where the trash is for work you are done with.

A session is in exactly one place: active, archived, or trashed. Archiving a session clears it from the trash and vice versa, and unlike the trash the archive is never auto-purged.

### Forking a Session

While viewing a Claude chat, hover over any completed message and click the branch icon (next to the copy icon) to fork the session at that point.

The fork modal prompts for a name and creates a new session in the same worktree containing everything up to and including the message you clicked. Send your next message in the new session and the conversation resumes from exactly that point. The original session is untouched.

Use this when you want to explore an alternate path from a particular decision point without losing the existing conversation — typical pattern: fork off the last user message, then retry a different approach in the new session.

### Moving Sessions to a New Worktree

Click the move icon (arrow leaving a box) next to a session on the worktree home page to move the session — and optionally some of the source worktree's uncommitted files — into a fresh git worktree.

The move modal:

1. **Branch name** — Supply a branch name for the new worktree. A fresh worktree is created via the same flow as the regular "New Worktree" action (the branch must not already exist; `/` in names is converted to `-` for the worktree directory).
2. **Files** — Lists the source worktree's modified, added, renamed, and untracked files as checkboxes. All are checked by default; uncheck any you want to leave behind. Directories and symlinks are not supported.
3. **Move** — Creates the worktree, moves the selected files into it, reverts them in the source worktree, and rebinds the session. The Claude process restarts in the new directory on your next message.

After completion, you're redirected to the new worktree's home page. If any source files could not be reverted, the session move itself still succeeds and the per-file errors are shown.

### Show Trash

Click **Show Trash** on the worktree home page to view trashed sessions. Each trashed session has:

- **Restore** — Move the session back to the active list
- **Permanent Delete** — Permanently remove the session and its message history (requires confirmation)

Trashed sessions are automatically purged after 7 days on server startup.

### Archived Sessions Page

**Archived** on the worktree home page (or `/Archived sessions` in the nav picker) opens the archive at `/archived-sessions`. It lists archived Claude and Codex sessions together and searches across them:

- **Search box** — Free-text search over session names and the conversation itself. Every word you type has to appear somewhere in a session for it to match, so adding words narrows the results. Results are ranked: a hit in the session's name outranks one in your own messages, which outranks one in Claude's replies.
- **Worktree / agent filters** — The worktree list includes worktrees that no longer exist, since removing a worktree doesn't remove sessions archived from it.
- **Date range** — Bounds on when the session was created.
- **Also search tool calls and output** — Off by default. Tool calls, command output, and diffs are the bulk of a transcript and the noisiest part of it; turn this on when you're looking for a command or a filename rather than something someone said.
- **Sort** — Relevance (default), date archived, last activity, name, or worktree.

Each result links to the session's chat page, and carries:

- **Unarchive** — Puts the session back in its worktree's normal listing
- **Move to trash** — Sends it to the trash, where the usual 7-day purge applies

The page opens filtered when you arrive from a worktree (`?worktree=` and `?agent=`); clear the filters to search the whole archive.

## Chat Interface

The Claude page provides a chat interface with:

- **Message area** — Shows the conversation history with syntax-highlighted code blocks
- **Input area** — Text input for sending messages to Claude
- **Send button** — Submit your message
- **Cancel button** — Stop Claude's current response (appears while generating)
- **⋮ More actions menu** — A drop-up next to the input box collecting the session actions:
  - **New conversation** — Start a fresh conversation within the same session
  - **View plan** — View and edit the session's captured plan (appears once a plan exists, see [Plan Artifacts](#plan-artifacts))
  - **Save to case** — Save the session transcript to a case
  - **Commit** — Make an intermediate commit against the worktree's open case (see [Commit](#commit-intermediate))
  - **Wrap up** — Archive the case and commit in one step (see [Wrap Up](#wrap-up))
  - **Pair for review** — Wire this session to another for an automated review loop (see [Pair Review & Checklist Runs](/docs/pages/pairing/))
  - **Start checklist run** — Drive this session and a reviewer through a multi-phase checklist (see [Pair Review & Checklist Runs](/docs/pages/pairing/))

### Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| Enter | Send message |
| Shift+Enter | Insert newline without sending |
| Escape | Stop/cancel current response |

### Context Usage and Session Cost

The footer shows the session's accumulated API cost and current context window usage, e.g. `$1.23 · 45K / 1M tokens (4%)`. The context window size is model-aware — 1M tokens for Opus 4.6+/Fable/Sonnet 4.6+, 200K for Haiku and older models — and the readout turns amber at 50% and red at 70%. Hover it for a breakdown of input, cache-read, and cache-write tokens plus the model and session cost.

Cost accumulates across the whole session, including process restarts and `--resume`, and persists with the session. It is computed by the Claude CLI itself (API list prices — informational if you're on a subscription plan). Each session's cost also appears as a badge in the worktree home page session list, and machine-wide totals live on the [Usage page](/docs/pages/usage/).

### Model Picker

A model dropdown in the footer (Default, Opus, Sonnet, Haiku, Fable) forces the session onto a model family. Switching is applied live to the running Claude process — no restart, so background tasks and pending permission prompts survive — and takes effect from your next message. The choice persists with the session and is re-applied (via `--model`) whenever the process respawns.

Two things to know:

- **Default** means no forced model: the session runs whatever your Claude Code settings (`~/.claude/settings.json` → `model`) resolve to. A fresh session starts on Default — if new sessions keep coming up on a model you don't expect, that settings default is what's choosing it. The picker reflects the forced model, or the model actually observed on the session's responses when no override is set.
- After a live switch the model may still *introduce itself* by the old name if asked — its identity line was written into the system prompt when the process started. The switch is real regardless: every subsequent response is generated (and billed) by the model you picked, which is what the picker and the [Usage page](/docs/pages/usage/) report.

### Auto-Approve (Skip Permissions)

An **auto-approve** checkbox in the footer switches the session into Claude Code's `auto` mode: tool calls stop raising permission prompts and run immediately, with a background safety classifier vetting each action (and silently declining risky ones). This is deliberately *not* `bypassPermissions` — the classifier and the guardrails below stay in force, so it is safer than skipping the checks entirely. The label turns amber while it's on, and the setting persists with the session.

Toggling is live where the CLI allows it: turning auto-approve **off** never restarts the process (background tasks survive, and any prompts that were already pending still need answers). Turning it **on** restarts the process once if it wasn't started in auto mode — the conversation resumes automatically on your next message.

**Pair this with hard guardrails.** `permissions.deny` rules in `~/.claude/settings.json` are enforced by the CLI in *every* mode, including auto, and a denied call is refused silently without prompting. Use them (plus a `PreToolUse` hook, which also catches indirect invocations like `sh -c "ssh …"`) to make classes of commands — e.g. anything SSH-shaped when your production hosts are one passwordless hop away — impossible regardless of what the agent decides to run:

```json
{
  "permissions": {
    "deny": ["Bash(ssh)", "Bash(ssh:*)", "Bash(scp:*)", "Bash(sftp:*)", "Bash(tailscale ssh:*)"]
  },
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "~/.claude/hooks/block-ssh.sh" }] }
    ]
  }
}
```

The same toggle exists on the Codex page. There it sends approval policy `never` plus a danger-full-access sandbox on each turn; turning it off restarts the app-server so your configured policies reapply. The Codex guardrail equivalent is an execpolicy rules file — `prefix_rule(pattern = ["ssh"], decision = "forbidden", …)` in `~/.codex/rules/default.rules` — which Codex enforces even when approvals and the sandbox are fully bypassed.

## Transcript Import/Export

### Importing Transcripts

Click **Import Transcript** on the worktree home page to load a previously exported transcript JSON file. This creates a new session with the imported conversation history.

### Saving to Cases

Click the briefcase button in the Claude chat interface to save the current transcript to a case:

1. Select an existing case or create a new one
2. Optionally provide a transcript title
3. The transcript is saved to the case's `transcripts/` directory

Saved transcripts can be continued from the case detail page using the **Continue** button, which imports the transcript into a new Claude session.

## Plan Artifacts

When Claude works in plan mode and calls `ExitPlanMode`, Trellis captures the plan as a durable, versioned artifact attached to the session — the plan text comes from the tool call itself, or from the markdown plan file Claude wrote just before presenting it.

- **Viewing** — A clipboard button appears in the chat toolbar once the session has a plan. It opens a modal showing the latest version rendered as Markdown, with a version badge.
- **Editing** — Click **Edit** in the modal to revise the plan. Edits don't overwrite history; each save appends a new version (marked as user-edited).
- **Storage** — Plan history persists per session and survives restarts.
- **Cases** — When a session with a plan is saved to a case (via Save to Case, Commit, or Wrap Up), the latest plan is copied into the case as `plan.md`. An existing case plan is never overwritten, so case-level edits stick. See [Cases](/docs/pages/cases/).

## Commit (intermediate)

Click the **Commit** button to make an intermediate commit against the worktree's open case (creating the case if it's the worktree's first commit). The case stays open and the session keeps going — use this to ship shippable pieces of work over the life of the case.

The Commit modal:

1. **Auto-detects the worktree's open case.** If one exists, it's shown read-only at the top. If none, the modal shows new-case fields: title (prefilled from a humanized version of the worktree name) and kind (default: `feature`).
2. **Lists changed files** as checkboxes (all checked by default). Paths inside the live cases directory are rejected — only your selected files are staged.
3. **Generates a draft commit message** using your existing Claude Code setup — no separate API key. The draft describes exactly the files you've checked; uncheck a file and hit **Regenerate** and the new draft covers only what's left. The draft populates the textarea unless you've started typing.

On confirm, Trellis creates the case if needed, snapshots this session's transcript onto it, refreshes any transcripts already attached, commits your selected files, and records the commit on the case's timeline. The session stays alive and the case stays open — keep working.

## Wrap Up

Click the **Wrap Up** button when the work is done. Wrap Up runs the same workflow as Commit with one extra flag: the case directory is archived and bundled into the commit, and the session is archived.

The Wrap Up modal adds (on top of the Commit modal):

- **Optional links** to attach to the case before archiving.
- **Traces to include** — saved trace reports from the session.
- **Related sessions to archive** — sessions from the *other* agent (Codex if this is Claude) that you want captured into the same case in one shot.

![The Wrap Up modal: changed files, a linked trace, and a generated commit message](/docs/images/wrapup-modal.png)

On confirm, Trellis runs everything Commit does, saves the extras you selected, generates the case's searchable summary, and archives the case directory into the same commit as your code — so the whole record of the work lands in git in one step. The session is archived when done (see [Archived Sessions Page](#archived-sessions-page)), and you're redirected to the worktree home page.

If anything fails partway through, the archive is rolled back so the case returns to its pre-wrap-up state.

## Codex parity

Everything described above also exists on the Codex page (`/codex/{worktree}/{session}`), including [Auto-Approve](#auto-approve-skip-permissions), with two exceptions: [Plan Artifacts](#plan-artifacts) rely on Claude Code's plan mode, and the [Model Picker](#model-picker) is Claude-only. Codex transcripts saved to a case land in the case's `codex_transcripts/` directory instead of `transcripts/`.

## Under the hood

Mechanics you don't need day-to-day, collected here for the curious:

- **Forking** — The Claude CLI's JSONL resume file is rewritten for the new session, so the process resumes from exactly the fork point on the next message.
- **Moving a session** — The server creates the new worktree, copies the selected files (preserving mode and relative paths), reverts the source worktree — tracked files via `git checkout --`, untracked files via delete — then stops the running Claude process and rebinds the session. A `claude.session.moved` event is emitted.
- **Plans** — Plan history is stored per session in `.trellis/claude/plans/<session-id>.json` and exposed at `GET/PUT /api/v1/claude/sessions/{session}/plan`.
- **Commit** — On confirm the server resolves or creates the case, snapshots the session's transcript (if the case was just created), refreshes every attached transcript from its live source, `git add`s the selected files, `git commit`s, and appends a `CommitEntry` to `case.json` with the SHA, date, message, generated description, and files changed. The draft message comes from `claude -p`; inputs include the diff of your checked files (the staging area is not consulted), the case manifest, `notes.md`, and the last few user messages from the session.
- **Wrap Up** — Runs the Commit steps, merges new links, saves selected traces, captures selected related sessions (transcript saved, session archived), generates the case summary via `claude -p` synchronously (with a timeout) so it lands in the same commit, moves the case directory from `cases/` to `cases-archived/`, `git add`s the selected files plus the archived directory, commits, and archives the active session. A failure between archive and commit rolls the archive back.
- **Codex** — The wrap-up modal (`static/js/wrapup.js`) and the server-side `commitToCase` orchestrator are agent-agnostic; the only per-agent differences are the Save-to-Case button label and the transcript directory.

## Related

- [Pair Review & Checklist Runs](/docs/pages/pairing/) — Automated implementer/reviewer loops between two sessions
- [Cases](/docs/pages/cases/) — Case lifecycle, commits timeline, generated summaries
- [Terminal Page](/docs/pages/terminal/) — Claude sessions in the navigation picker
