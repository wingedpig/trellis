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
- **Wrap Up button** — Archive a case and commit in one step (see below)

### Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| Enter | Send message |
| Shift+Enter | Insert newline without sending |
| Escape | Stop/cancel current response |

### Context Usage

The footer shows current context window usage, helping you track how much of Claude's context is being used by the conversation.

## Transcript Import/Export

### Importing Transcripts

Click **Import Transcript** on the worktree home page to load a previously exported transcript JSON file. This creates a new session with the imported conversation history.

### Saving to Cases

Click the briefcase button in the Claude chat interface to save the current transcript to a case:

1. Select an existing case or create a new one
2. Optionally provide a transcript title
3. The transcript is saved to the case's `transcripts/` directory

Saved transcripts can be continued from the case detail page using the **Continue** button, which imports the transcript into a new Claude session.

## Wrap Up

Click the flag-checkered button in the Claude chat interface to wrap up a session — creating/archiving a case and committing in one step. This replaces the manual workflow of creating a case, saving the transcript, adding links, archiving, and making a git commit.

The Wrap Up modal:

1. **Auto-detects linked case** — If the session already has a transcript saved to an open case, that case is pre-selected (read-only). Otherwise, you fill in a title and kind to create a new case.
2. **Shows git status** — Lists all modified, added, deleted, renamed, and untracked files as checkboxes. All are checked by default; uncheck files you don't want to commit.
3. **Pre-fills commit message** — Automatically generates `<title> [case: <case-id>]`. The case ID is deterministic (`YYYY-MM-DD__slugified-title`), so the predicted ID is used for new cases. You can freely edit the message.
4. **Optional links** — Add title/URL pairs to attach to the case before archiving.
5. **Wrap Up** — Submits to the server, which:
   - Creates a new case (if needed) and saves the session transcript
   - Updates ALL transcripts linked to the case with the latest messages
   - Merges any new links into the case
   - Archives the case
   - Runs `git add` on selected files plus the archived case directory
   - Runs `git commit` with your message

After completion, you're redirected to the worktree home page.

## Related

- [Cases](/docs/pages/cases/) — Saving transcripts to cases
- [Terminal Page](/docs/pages/terminal/) — Claude sessions in the navigation picker
