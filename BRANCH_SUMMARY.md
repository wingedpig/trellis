# Branch Summary: `nodefaultwindows` vs `main`

## Overview

This branch makes several major changes to the Trellis development environment manager:

1. **Removes `default_windows`** — Terminal windows are no longer pre-created from config. Instead, they are created on demand from a new worktree home page.
2. **Adds Claude Code integration** — A full chat UI for Claude Code sessions, managed per-worktree with WebSocket streaming.
3. **Adds worktree home page** — A new `/worktree/{name}` page that serves as the hub for each worktree, listing its Claude sessions and terminal windows with create/delete/rename controls.
4. **Redesigns the home page** — The `/` route now shows the worktrees list (with project branding) instead of redirecting to a terminal.
5. **Enhances the navigation picker** — Adds worktree home pages, Claude sessions, and per-type FontAwesome icons to the picker dropdown.

---

## Detailed Changes

### 1. Remove `default_windows` config and auto-creation

**Files:** `internal/config/schema.go`, `internal/config/validator.go`, `internal/config/template.go`, `internal/terminal/types.go`, `internal/app/app.go`, `cmd/trellis/main.go`

- Removed `DefaultWindows []WindowConfig` from `TerminalConfig` in both the config schema and the terminal types.
- Removed `WindowConfig` struct (had `Name` and `Command` fields).
- Removed validator logic for `terminal.default_windows[].name`.
- Removed the startup code in `app.go` that iterated over all worktrees and called `EnsureSession()` with default windows.
- Removed the `worktree.created` event subscriber that auto-created terminal sessions for new worktrees.
- Removed `DefaultWindow` from `api.Dependencies` (was used for `/terminal` redirect target).
- Updated `generateConfig()` in `main.go` to remove the `default_windows` section from generated config and update the comment to say terminals are created on demand.

### 2. Template expansion refactor

**Files:** `internal/config/template.go`

- Simplified the `.Inputs` template preservation logic. Previously used placeholder-based substitution to preserve `{{.Inputs.X}}` references during config expansion. Now uses a regex (`inputsOrControlPattern`) to detect whether a value contains only `.Inputs`-related templates (including control flow like `{{if .Inputs.id}}...{{end}}`). If so, the entire string is returned unexpanded. This avoids bugs where control-flow blocks spanning multiple `{{ }}` directives couldn't be safely partial-expanded.

### 3. Claude Code session manager

**New files:** `internal/claude/manager.go`, `internal/claude/store.go`

- `Manager` manages Claude Code sessions across worktrees. Sessions are identified by UUID and indexed by worktree name.
- Each `Session` holds a long-running `claude` CLI process (started with `--output-format stream-json`, `--input-format stream-json`, `--permission-prompt-tool stdio`, `--include-partial-messages`).
- Sessions support: create, delete, rename, list (by worktree or all), get by ID, get-or-create (backwards compat).
- Messages are streamed via NDJSON from claude's stdout. The `readLoop` parses `system`, `assistant`, `result`, `stream_event`, and `control_request` events.
- `stream_event` inner events (`message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`) are accumulated into content blocks for message history.
- Token usage tracking sums `input_tokens + cache_creation_input_tokens + cache_read_input_tokens` for accurate context window usage reporting.
- Fan-out pub/sub: multiple WebSocket clients can subscribe to a session's event stream.
- Persistence: session metadata saved to `{stateDir}/sessions.json`, per-session messages saved to `{stateDir}/messages/{id}.json`. Process state is not persisted (sessions resume via `--resume {claudeSID}`).
- `store.go` handles JSON file I/O for `SessionRecord` and `Message` types with atomic writes (write to temp, rename).

### 4. Claude Code API handlers

**New file:** `internal/api/handlers/claude.go`

- `ClaudeHandler` with methods:
  - `WebSocket` — WebSocket connection for a specific session (by session ID). Sends history + pending messages on connect, then streams events. Handles `message`, `permission_response`, `cancel`, and `reset` client messages.
  - `WebSocketByWorktree` — Backwards-compat WebSocket using first/default session for a worktree.
  - `ListSessions` — GET sessions for a worktree.
  - `CreateSessionAPI` — POST to create a new session (optional `display_name`).
  - `RenameSessionAPI` — PATCH to rename a session's display name.
  - `DeleteSessionAPI` — DELETE a session (kills process, removes messages file).

### 5. Terminal handler additions

**File:** `internal/api/handlers/terminal.go`

- Added `CreateWindow` handler — POST to create a new tmux window for a worktree. Auto-names as "shell", "shell-2", etc. if no name provided. Optionally runs a command.
- Added `RenameWindow` handler — PATCH to rename a tmux window via `tmux rename-window`.
- Added `DeleteWindow` handler — DELETE to kill a tmux window via `tmux kill-window`.
- `TerminalHandler` now takes a `worktree.Manager` to resolve worktree paths and build tmux session names.
- Added `worktreeToSession()` helper that maps worktree names to tmux session names using the project name prefix.

### 6. API routes

**File:** `internal/api/router.go`

- Added routes in `NewRouterWithTerminalHandler`:
  - `POST /api/v1/terminal/{worktree}/windows` — create window
  - `PATCH /api/v1/terminal/{worktree}/windows/{window}` — rename window
  - `DELETE /api/v1/terminal/{worktree}/windows/{window}` — delete window
  - `GET /api/v1/claude/{worktree}/sessions` — list Claude sessions
  - `POST /api/v1/claude/{worktree}/sessions` — create Claude session
  - `PATCH /api/v1/claude/sessions/{session}` — rename Claude session
  - `DELETE /api/v1/claude/sessions/{session}` — delete Claude session
  - `GET /api/v1/claude/sessions/{session}/ws` — Claude WebSocket
  - `GET /api/v1/claude/{worktree}/ws` — Claude WebSocket (worktree-level compat)
- Added page routes:
  - `GET /worktree/{name}` — worktree home page
  - `GET /claude/{worktree}/{session}` — Claude chat page
  - `GET /claude/{worktree}` — redirect to first session
- Changed `GET /` from terminal redirect to `Home` (worktrees page).
- Updated `NewServer` to pass `WorktreeManager` to `NewTerminalHandler`.

### 7. Navigation handler enhancements

**File:** `internal/api/handlers/nav.go`

- `NavHandler` now takes `*claude.Manager` and `worktree.Manager`.
- Response includes `claudeSessions []NavClaudeSession` and `worktrees []NavWorktree`.
- `NavClaudeSession` has URL, display name, worktree, session ID, and running status.
- `NavWorktree` has URL, display name, and worktree name.
- Added `worktreeToDisplayName()` to map worktree directory names to display names ("main" for the project root, branch suffix for others).

### 8. Page handler additions

**File:** `internal/api/handlers/pages.go`

- Added `Home()` handler — renders worktrees page (with project branding/version).
- Added `WorktreeHome()` handler — renders the worktree home page with Claude sessions, terminal windows, and tmux session info.
- Added `ClaudePage()` handler — renders the Claude chat page for a specific session.
- Added `ClaudeRedirect()` handler — redirects `/claude/{worktree}` to first session or creates one.
- `Terminal()` now redirects to `/worktree/{name}` instead of `/terminal/local/{name}/{defaultWindow}`.
- `Worktrees()` no longer calls `worktrees.Refresh()` synchronously (status is loaded async via JS).
- Added `version` field passed through to the worktrees page template.

### 9. Worktree home page UI

**New files:** `views/worktree_home.qtpl`, `views/worktree_home.qtpl.go`, `static/css/worktree_home.css`

- Displays worktree name and branch.
- Lists Claude sessions with links to `/claude/{worktree}/{session}`, showing Running/Generating badges. Each has pencil (rename) and trash (delete) buttons.
- Lists terminal windows with links to `/terminal/local/{worktree}/{window}`. Each has pencil (rename) and trash (delete) buttons.
- "New Session" and "New Terminal" buttons open Bootstrap modals for creation.
- Rename modal is shared between Claude sessions and terminals. Opens with current name pre-filled, submits PATCH to the appropriate API endpoint.
- All JS functions: `createClaudeSession`, `deleteClaudeSession`, `renameClaudeSession`, `createTerminal`, `deleteTerminal`, `renameTerminal`.

### 10. Claude Code chat UI

**New files:** `views/claude.qtpl`, `views/claude.qtpl.go`, `static/css/claude.css`, `static/js/claude.js`

- Full chat interface for a Claude Code session.
- WebSocket connection to `/api/v1/claude/sessions/{session}/ws`.
- Renders message history (user and assistant messages) with Markdown support.
- Streams assistant responses in real-time, rendering text and tool_use/tool_result blocks.
- Permission prompt handling — when Claude requests tool permissions, shows an inline prompt with Allow/Deny buttons.
- Input area with send button and Shift+Enter for newlines.
- Cancel button to abort generation.
- Reset button to clear conversation and start fresh.
- Context window usage bar showing input tokens vs 200K limit.
- CSS styling for chat bubbles, code blocks, tool use visualization, and permission prompts.

### 11. Worktrees page redesign

**File:** `views/worktrees.qtpl`

- Added project branding header (logo, version, links to docs/GitHub/email group).
- Changed heading from h2 "Worktrees" to h5.
- Added `data-worktree-path` attribute to each worktree item for async status targeting.
- Added async status refresh: after page load, fetches `GET /api/v1/worktrees` and updates dirty/ahead/behind badges in place (avoids slow synchronous `git status` calls blocking page render).
- Wrapped status badges in a `.wt-status` span for targeted DOM updates.

### 12. Navigation picker enhancements

**File:** `views/header.qtpl`

- Picker items now grouped by worktree: home page, then local terminals, then Claude sessions for each worktree.
- Added `data-isClaude`, `data-isWorktree` dataset attributes for item type identification.
- `formatNavOption()` now prepends FontAwesome icons based on item type:
  - Claude sessions: `fa-robot`
  - Terminals: `fa-terminal`
  - Worktree home: `fa-folder-tree`
  - Log viewers: `fa-file-lines`
  - Links: `fa-arrow-up-right-from-square`
  - Pages: `fa-compass`
  - Services: colored status dot (unchanged)
- Added `templateSelection: formatNavOption` to all Select2 init calls so icons show in both dropdown and selected display.
- Updated `formatTerminalUrl()` to handle `/worktree/` and `/claude/` URLs for history display.
- Changed `/Worktrees` nav page to `/Home`.

### 13. Terminal page picker integration

**File:** `views/terminal.qtpl`

- Added `fetchNavExtras()` function that merges worktree home pages and Claude sessions into the terminal-mode picker after loading terminal sessions.
- Worktree home pages are inserted before their worktree's terminals; Claude sessions are inserted after.
- Conditional rendering: workflow picker only shown for local/output views, VS Code button only for local views.

### 14. Workflow picker

**New file:** `static/js/workflow_picker.js`

- Standalone workflow dropdown component (extracted or newly added). Fetches workflows from API and populates a select element. Handles workflow execution with optional inputs modal.

### 15. App lifecycle changes

**File:** `internal/app/app.go`

- Added `claudeManager *claude.Manager` field.
- Initialize Claude manager with state dir at `{configDir}/.trellis/claude`.
- Added `claudeManager.Shutdown()` in app shutdown sequence.
- Pass `ClaudeManager` and `Version` to API dependencies.
- Removed all `DefaultWindow`-related code.

### 16. Documentation updates

**Files:** `TRELLIS_SPEC.md`, `docs/pages/terminal.md`, `docs/pages/_index.md`, `docs/pages/worktrees.md`, `docs/concepts/worktrees.md`, `docs/quickstart.md`, `docs/reference/config.md`

- Spec: Added Terminal CRUD and Claude Sessions API endpoints. Updated navigation picker table with icon column and Claude sessions. Updated window management to mention rename.
- Terminal page docs: Updated picker table with icons and Claude sessions.
- Pages index: Added Claude sessions to terminal page description.
- Worktrees concept: Mentioned rename capability.
- Removed references to `default_windows` from quickstart and config reference.
- Updated worktrees page docs to reflect new home page role with project branding.

### 17. Dependency changes

**Files:** `go.mod`, `go.sum`

- Added `github.com/google/uuid` (for Claude session IDs).
- Various indirect dependency updates.

---

## New API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/terminal/{worktree}/windows` | Create terminal window |
| PATCH | `/api/v1/terminal/{worktree}/windows/{window}` | Rename terminal window |
| DELETE | `/api/v1/terminal/{worktree}/windows/{window}` | Delete terminal window |
| GET | `/api/v1/claude/{worktree}/sessions` | List Claude sessions |
| POST | `/api/v1/claude/{worktree}/sessions` | Create Claude session |
| PATCH | `/api/v1/claude/sessions/{session}` | Rename Claude session |
| DELETE | `/api/v1/claude/sessions/{session}` | Delete Claude session |
| GET | `/api/v1/claude/sessions/{session}/ws` | Claude WebSocket |
| GET | `/api/v1/claude/{worktree}/ws` | Claude WebSocket (worktree-level) |

## New Page Routes

| Path | Description |
|------|-------------|
| `/` | Home page (worktrees list with project branding) |
| `/worktree/{name}` | Worktree home page (Claude sessions + terminals) |
| `/claude/{worktree}/{session}` | Claude Code chat page |
| `/claude/{worktree}` | Redirect to first/new Claude session |

## New Files

| File | Purpose |
|------|---------|
| `internal/claude/manager.go` | Claude session manager and session types |
| `internal/claude/store.go` | JSON persistence for sessions and messages |
| `internal/api/handlers/claude.go` | Claude API handlers (WebSocket, CRUD) |
| `views/worktree_home.qtpl` | Worktree home page template |
| `views/claude.qtpl` | Claude chat page template |
| `static/css/claude.css` | Claude chat UI styles |
| `static/css/worktree_home.css` | Worktree home page styles |
| `static/js/claude.js` | Claude chat WebSocket client and UI logic |
| `static/js/workflow_picker.js` | Workflow dropdown component |

## Config Changes

- Removed `terminal.default_windows` array and `WindowConfig` struct.
- No new config fields added (Claude sessions are managed at runtime, not configured).
