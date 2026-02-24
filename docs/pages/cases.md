---
title: "Cases Page"
weight: 6
---

# Cases

Cases are durable, repo-committed objects that capture a unit of work within a Trellis worktree. They appear in two places: the worktree home page and the case detail page.

## Worktree Home — Cases Section

The worktree home page (`/worktree/{name}`) shows a list of open cases with:

- **Title** — Case name, linked to the detail page
- **Kind badge** — Color-coded: bug (red), feature (blue), investigation (purple), task (gray)
- **Created date** — When the case was created
- **Archive button** — Move the case to the archived directory

Additional controls:

- **New Case** — Opens a modal to create a case with a title and kind (bug, feature, investigation, task)
- **Show Archived** — Toggle to load and display archived cases with a "Reopen" button on each

## Case Detail Page

**URL:** `/case/{worktree}/{id}`

The detail page shows the full case record:

### Header

- Case title
- Kind badge (bug, feature, investigation, task)
- Status badge (open, resolved, wontfix)
- Created and updated timestamps

### Actions

- **Back** — Return to the worktree home page
- **Wrap Up** — Archive the case and commit in one step (see [Wrap Up](#wrap-up) below). Only shown for non-archived cases.
- **Archive / Reopen** — Move to archived or restore from archived
- **Delete** — Permanently remove the case directory

### Links

External references (URLs) associated with the case. Always visible, even when empty.

- **Add** — Opens an inline form to add a new link (title + URL). The link is saved immediately via the API.
- **Delete (X)** — Each link has a remove button that deletes it after confirmation-free PATCH to the API.

Links are managed entirely in the browser and persisted by PATCHing the full links array to the cases API.

### Notes

Markdown content from `notes.md`, rendered in the browser using marked.js.

### Evidence

List of attached evidence files with:

- Title and format badge
- Tags as info badges
- Date added

### Transcripts

Claude Code transcripts saved to the case. Each shows:

- Title and message count
- Export date
- **Continue** button — Imports the transcript into a new Claude session for continued work

### Traces

Trace reports saved to the case from the [Trace page](/docs/pages/trace/). Each shows:

- Report name, linked to a read-only trace viewer within the case
- Trace ID, group, and entry count
- Save date
- **Delete (X)** button — Removes the saved trace from the case

Traces are saved as full report data (not references), so they remain viewable even if the original trace report is deleted.

## Trace Page — Save to Case

The Trace report page includes a "Save to Case" button that:

1. Fetches worktrees from the navigation API
2. On worktree selection, fetches open cases for that worktree
3. Shows a modal to pick an existing case or create a new one
4. Saves the full trace report data to the chosen case

## Wrap Up

The "Wrap Up" button collapses several manual steps (create case, save transcripts, add links, archive, git commit) into a single modal interaction. It is available in two places:

1. **Claude chat page** — Wraps up the current session. If the session is already linked to an open case, that case is used; otherwise, a new case is created.
2. **Case detail page** — Wraps up an existing case. The case info is read-only.

The Wrap Up modal shows:

- **Case info** — Read-only for existing cases, or editable title/kind for new cases
- **Git status** — Checkboxes for all changed files (modified, added, deleted, renamed, untracked), all checked by default
- **Commit message** — Pre-filled as `<title> [case: <case-id>]`, freely editable
- **Links** — Optional title/URL pairs to attach to the case

On confirmation, the server:

1. Creates a new case (if needed) and saves the session transcript
2. Updates ALL transcripts linked to the case with the latest messages from live sessions
3. Merges any new links into the case
4. Archives the case (moves from `cases/` to `cases-archived/`)
5. Runs `git add` on selected files plus the archived case directory
6. Runs `git commit` with the user's commit message

The archive happens BEFORE the commit, so the committed state includes the archived case directory.

## Claude Page — Save to Case

The Claude session page (`/claude/{worktree}/{session}`) includes a "Save to Case" button that:

1. Fetches open cases for the current worktree
2. Shows a modal to pick an existing case or create a new one
3. Exports the session transcript and attaches it to the chosen case

## File Structure

Cases are stored as directories under the configured `cases.dir` (default: `trellis/cases`):

```
trellis/cases/
  20260210__fix-login-crash/
    case.json           # Machine-readable manifest
    notes.md            # Human narrative
    evidence/           # Attached files
    transcripts/        # Claude transcript exports
    traces/             # Saved trace reports
```

Archived cases are moved to `trellis/cases-archived/`.

## Related

- [Configuration: cases](/docs/reference/config/#cases) — Cases configuration
