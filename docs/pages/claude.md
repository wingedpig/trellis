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
