---
title: "Claude Page"
weight: 7
---

# Claude Page

**URL:** `/claude/{worktree}/{session}`

The Claude page provides an integrated Claude Code chat interface within Trellis. Each worktree can have multiple Claude sessions, allowing you to work with AI assistance in the context of your development environment.

## Accessing Claude

- **From the worktree home page** (`/worktree/{name}`) — The Claude Sessions section lists all sessions for that worktree with buttons to create new sessions
- **From the navigation picker** (`Cmd+P`) — Claude sessions appear with the `@` prefix and a robot icon (e.g., `@main - Session 1`)
- **Direct URL** — `/claude/{worktree}/{session}` opens a specific session

## Session Management

### Creating Sessions

Click **New Session** on the worktree home page. You can optionally provide a display name; if left blank, sessions are auto-named (Session 1, Session 2, ...).

### Renaming Sessions

Click the pencil icon next to a session on the worktree home page to rename it.

### Trashing Sessions

Click the trash icon next to a session on the worktree home page. This moves the session to trash — the process is stopped but the session data is preserved.

### Forking a Session

While viewing a Claude chat, hover over any completed message and click the branch icon (next to the copy icon) to fork the session at that point.

The fork modal prompts for a name and creates a new session in the same worktree containing the first N messages of the conversation, where N is the index (inclusive) of the message you clicked. The Claude CLI's JSONL resume file is rewritten for the new session, so when you send the next message the process resumes from exactly that point. The original session is untouched.

Use this when you want to explore an alternate path from a particular decision point without losing the existing conversation — typical pattern: fork off the last user message, then retry a different approach in the new session.

### Moving Sessions to a New Worktree

Click the move icon (arrow leaving a box) next to a session on the worktree home page to move the session — and optionally some of the source worktree's uncommitted files — into a fresh git worktree.

The move modal:

1. **Branch name** — Supply a branch name for the new worktree. A fresh worktree is created via the same flow as the regular "New Worktree" action (the branch must not already exist; `/` in names is converted to `-` for the worktree directory).
2. **Files** — Lists the source worktree's modified, added, renamed, and untracked files as checkboxes. All are checked by default; uncheck any you want to leave behind. Directories and symlinks are not supported.
3. **Move** — Submits to the server, which:
   - Creates the new worktree
   - Copies the selected files into the new worktree (preserving mode and relative paths)
   - Reverts the source worktree — tracked files via `git checkout --`, untracked files via delete
   - Stops the running Claude process and rebinds the session to the new worktree; the process restarts in the new working directory on the next message
   - Emits a `claude.session.moved` event

After completion, you're redirected to the new worktree's home page. If any source files could not be reverted, the session move itself still succeeds and the per-file errors are shown.

### Show Trash

Click **Show Trash** on the worktree home page to view trashed sessions. Each trashed session has:

- **Restore** — Move the session back to the active list
- **Permanent Delete** — Permanently remove the session and its message history (requires confirmation)

Trashed sessions are automatically purged after 7 days on server startup.

## Chat Interface

The Claude page provides a chat interface with:

- **Message area** — Shows the conversation history with syntax-highlighted code blocks
- **Input area** — Text input for sending messages to Claude
- **Send button** — Submit your message
- **Cancel button** — Stop Claude's current response (appears while generating)
- **Reset button** — Start a new conversation within the same session
- **Save to Case button** — Save the session transcript to a case
- **Commit button** — Make an intermediate commit against the worktree's open case (see [Commit](#commit-intermediate))
- **Wrap Up button** — Archive the case and commit in one step (see [Wrap Up](#wrap-up))

### Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| Enter | Send message |
| Shift+Enter | Insert newline without sending |
| Escape | Stop/cancel current response |

### Context Usage and Session Cost

The footer shows the session's accumulated API cost and current context window usage, e.g. `$1.23 · 45K / 1M tokens (4%)`. The context window size is model-aware — 1M tokens for Opus 4.6+/Fable/Sonnet 4.6+, 200K for Haiku and older models — and the readout turns amber at 50% and red at 70%. Hover it for a breakdown of input, cache-read, and cache-write tokens plus the model and session cost.

Cost accumulates across the whole session, including process restarts and `--resume`, and persists with the session. It is computed by the Claude CLI itself (API list prices — informational if you're on a subscription plan). Each session's cost also appears as a badge in the worktree home page session list, and machine-wide totals live on the [Usage page](/docs/pages/usage/).

## Transcript Import/Export

### Importing Transcripts

Click **Import Transcript** on the worktree home page to load a previously exported transcript JSON file. This creates a new session with the imported conversation history.

### Saving to Cases

Click the briefcase button in the Claude chat interface to save the current transcript to a case:

1. Select an existing case or create a new one
2. Optionally provide a transcript title
3. The transcript is saved to the case's `transcripts/` directory

Saved transcripts can be continued from the case detail page using the **Continue** button, which imports the transcript into a new Claude session.

## Commit (intermediate)

Click the **Commit** button to make an intermediate commit against the worktree's open case (creating the case if it's the worktree's first commit). The case stays open and the session keeps going — use this to ship shippable pieces of work over the life of the case.

The Commit modal:

1. **Auto-detects the worktree's open case.** If one exists, it's shown read-only at the top. If none, the modal shows new-case fields: title (prefilled from a humanized version of the worktree name) and kind (default: `feature`).
2. **Lists changed files** as checkboxes (all checked by default). Paths inside the live cases directory are rejected — only your selected files are staged.
3. **Generates a draft commit message** by calling `claude -p` when the modal opens. Inputs include the staged diff, case manifest, `notes.md`, and the last few user messages from this session. The draft populates the textarea unless you've started typing; **Regenerate** is always available. The model also emits a short per-commit case description that is stored on the resulting commit entry.

On confirm, the server:

- Resolves or creates the case.
- Snapshots the session's transcript onto the case (if it was just created).
- Refreshes every transcript already attached to the case from its live source.
- `git add`s the selected files and `git commit`s your message.
- Appends a `CommitEntry` to `case.json` with the SHA, date, message, generated description, and files changed.

## Wrap Up

Click the **Wrap Up** button when the work is done. Wrap Up runs the same workflow as Commit with one extra flag: the case directory is archived and bundled into the commit, and the session is trashed.

The Wrap Up modal adds (on top of the Commit modal):

- **Optional links** to attach to the case before archiving.
- **Traces to include** — saved trace reports from the session.
- **Related sessions to archive** — sessions from the *other* agent (Codex if this is Claude) that you want captured into the same case in one shot.

On confirm, the server:

1. Runs all the Commit steps above.
2. Merges new links into the case.
3. Saves any selected traces. Captures any selected related sessions (transcript saved, session trashed).
4. **Generates the case summary** via `claude -p` synchronously, with a timeout. The summary lands in the same commit as the archived case so there is no follow-up amend.
5. Moves the case directory from `cases/` to `cases-archived/`.
6. `git add`s the selected files **plus** the archived case directory.
7. `git commit`s. Trashes the active session.

If anything between the archive and commit fails, the archive is rolled back so the case returns to its pre-wrap-up state.

After completion, you're redirected to the worktree home page.

## Codex parity

Everything described above also exists on the Codex page (`/codex/{worktree}/{session}`). The shared modal in `static/js/wrapup.js` and the shared `commitToCase` server orchestrator are agent-agnostic; the only differences are the Save-to-Case button label and which transcript directory the snapshot lands in (`codex_transcripts/` instead of `transcripts/`).

## Related

- [Cases](/docs/pages/cases/) — Case lifecycle, commits timeline, generated summaries
- [Terminal Page](/docs/pages/terminal/) — Claude sessions in the navigation picker
