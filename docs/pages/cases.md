---
title: "Cases Page"
weight: 6
---

# Cases

A **case** is the durable record of a worktree's effort — one logical unit of work that accumulates notes, transcripts, evidence, traces, a commit timeline, and a generated summary over its lifetime. When the work is done, the case is wrapped up: the directory is archived and the final state is committed to git alongside the code.

Cases are created lazily — usually on the first commit a worktree makes. Worktrees that never commit (throwaway experiments, abandoned work) never get a case.

## Lifecycle invariants

- **One open case per worktree.** Creating a second open case is refused. If you have a second unrelated effort, make a new worktree.
- **All commits in a worktree bind to its open case.** Both Claude and Codex sessions in the same worktree write their commits to the same case without prompting.
- **The case ID is immutable.** The title is freely editable; the `id` directory name and any `[case: <id>]` references in commit messages are permanent.
- **Wrap Up is Commit + Archive.** Same workflow, with a flag that tells the server to also move the case directory into the archived path and bundle it into the commit.

## Worktree home — Cases section

The worktree home page (`/worktree/{name}`) shows the worktree's single open case (if any) and:

- **Archived cases** — Link to the per-worktree archived-cases browser.
- **New Case** — Only available when no open case exists.
- **Archive button** on the case row — Moves the case to the archived directory without committing.

## Archived cases page

**URL:** `/worktree/{name}/archived-cases`

A per-worktree browser for finished work, designed to be a usable memory system for half-remembered cases.

- **Full-text search** across title, summary fields (synopsis, symptoms, root cause, resolution), keywords, components, commit descriptions, and `notes.md`. Keyword and component matches rank highest.
- **Filters:** kind (bug/feature/investigation/task), date range, "has linked traces" toggle.
- **Optional transcript scan** — A separate checkbox extends the search into attached transcript previews. Slow path; off by default.
- **Sort:** date (default), kind, duration, worktree of origin.

Results show the synopsis, component/keyword chips from the generated summary, and the first matching snippet for context.

## Case detail page

**URL:** `/case/{worktree}/{id}`

### Header

- **Title** with an inline edit button. The ID is shown as an immutable monospace tag.
- Kind and status badges, created/updated timestamps.
- Action bar: Back, Wrap Up (non-archived), Archive / Reopen, Delete.

### Summary (generated)

When a case has been wrapped up, the page shows the structured summary written by the `claude -p` generator at wrap-up time:

| Field | Use |
|-------|-----|
| Synopsis | One human-readable line. |
| Symptoms | The observable problem (empty for non-bug work). |
| Root cause | What was actually wrong (empty for features / wontfix). |
| Resolution | What changed — approach, not the diff. |
| Components | Service / package / subsystem names touched. |
| Keywords | Explicit search terms: error codes, function names, libraries. |

Each field is individually editable. The **Regenerate Summary** button re-runs generation, confirming first if the existing summary may have been hand-edited.

### Commits

A reverse-chronological timeline of the **intermediate** commits made against the case during its active life. Each row shows:

- Short SHA
- Date
- First line of the git commit message
- The per-commit case description (the "narrative beat", distinct from the commit message)

The wrap-up commit is intentionally not in this list — it is locatable from git history.

### Links

External references (URLs). Add inline with title + URL; remove with the × button.

### Notes

Markdown content from `notes.md`, rendered in the browser. Edit in-place with the pencil button.

### Evidence

Attached files with format badges and tags.

### Transcripts

Saved Claude and Codex transcripts. Each row shows:

- Title and message count
- Whether the live source session has more recent messages (and an **Update** button to refresh the stored copy)
- **Continue** — Imports the transcript into a new session for continued work

### Traces

Linked trace reports — each linked to a read-only viewer within the case. Saved as full report data, so they remain viewable even if the original report is later deleted.

## Commit and Wrap Up

The **Commit** and **Wrap Up** buttons on the Claude and Codex session pages share a single modal and a single server-side orchestrator.

### Commit (intermediate)

Click **Commit** on the Claude or Codex session page to make an intermediate commit against the worktree's open case (creating the case if it's the worktree's first commit).

The modal:

- Auto-detects the worktree's open case. If none, shows a small title + kind form prefilled from a humanized version of the worktree name.
- Lists changed files as checkboxes (all checked by default).
- Generates a draft commit message via `claude -p` when it opens. The draft populates the textarea unless you've started typing; a **Regenerate** button is always available. The model also emits a short per-commit case description that is stored on the resulting commit entry.

On confirm:

1. Resolve / create the case.
2. Snapshot the active session's transcript onto the case (if the case was just created).
3. Refresh every transcript already attached to the case.
4. `git add` the selected files, `git commit` with your message.
5. Append a `CommitEntry` to `case.json` with SHA, date, message, generated description, and files changed.

The session stays alive and the case stays open — keep working.

### Wrap Up

Click **Wrap Up** when the work is done. Same orchestrator as Commit, but with `archive: true`:

1. All the Commit steps above.
2. Merge any new links.
3. Save any selected traces. Capture any selected related sessions from the other agent.
4. **Generate the case summary** synchronously (with a timeout) so it lands in the same commit as the archived case.
5. Move the case directory from `cases/` to `cases-archived/`.
6. `git add` the selected files **plus** the archived case directory.
7. `git commit`. Trash the active session.

If any step between archive and commit fails, the archive is rolled back so the case returns to its pre-wrap-up state.

## Generated commit messages and summaries

Generation uses your existing Claude Code authentication — there is no separate API key to configure. Trellis shells out to `claude -p --output-format json` for two distinct purposes:

- **Commit message + per-commit description** when the Commit / Wrap Up modal opens. The diff fed to the model is built from exactly the files you've checked in the modal — uncheck a file and Regenerate, and the new draft describes only what's left selected. The staging area is not consulted at all. Other inputs: the case manifest, `notes.md`, and the last few user messages from the session.
- **Case summary** at wrap-up. The wrap-up diff is scoped the same way (your selected files only). Other inputs: attached transcripts, `notes.md`, the per-commit descriptions accumulated during the case, linked trace summaries, and the case `kind`/`status`.

Failures degrade gracefully: an empty textarea (you type the message), or a missing `summary{}` block that you can regenerate from the case detail page.

## File structure

Cases are stored as directories under the configured `cases.dir` (default: `trellis/cases`):

```
trellis/cases/
  20260514__ach-payments-stripe/
    case.json              # Manifest including commits[] and summary{}
    notes.md               # Human narrative
    evidence/              # Attached files
    transcripts/           # Saved Claude transcripts
    codex_transcripts/     # Saved Codex transcripts
    traces/                # Saved trace reports
```

At wrap-up, the directory moves to `trellis/cases-archived/` and is included in the same commit as the user-selected files.

## Related

- [Configuration: cases](/docs/reference/config/#cases) — Cases configuration
- [Claude Page](/docs/pages/claude/) — Where Commit and Wrap Up are initiated from
