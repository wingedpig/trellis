---
title: "Worktrees Page"
weight: 1
---

# Worktrees Page

**URL:** `/` (also available at `/worktrees`)

The Worktrees page is the home page. It lets you manage git worktrees for your project. Each worktree provides an isolated working directory with its own branch, allowing you to work on multiple features or fixes simultaneously.

## Page Header

The page header shows the Trellis logo, a brief description of the project, the version number, and links to:

- **Documentation** — Opens the Trellis documentation site
- **GitHub** — Opens the Trellis GitHub repository
- **Email Group** — Opens the Trellis mailing list on Groups.io

## Worktree List

The page displays all worktrees with the following information:

- **Name** — The worktree directory name (derived from branch name)
- **Branch** — The git branch checked out in that worktree
- **Status indicators:**
  - **dirty** — The worktree has uncommitted changes to tracked files (untracked files are ignored)
  - **↑N / ↓N** — Commits ahead or behind the default branch (main/master)
  - **Detached** — The worktree is in detached HEAD state
- **Current** — Indicates which worktree Trellis is currently using

Status indicators (dirty, ahead/behind) load asynchronously after the page renders. The page displays immediately using cached worktree data, then fetches fresh status from the API (`GET /api/v1/worktrees`) and updates the badges in place.

## Switching Worktrees

Click the **Switch** button on any worktree to activate it. When you switch worktrees:

1. All running services are stopped
2. Trellis reconfigures for the new worktree's directory
3. Services are restarted in the new worktree
4. Any configured `pre_activate` hooks run

Switching worktrees changes where Trellis looks for binaries, logs, and other paths that use the `{{.Worktree}}` template variable.

## Creating Worktrees

Use the **Create New Worktree** form to create a new worktree:

1. Enter a branch name (e.g., `feature-x`, `bugfix-123`)
2. Trellis creates:
   - A new git branch from your default branch
   - A new worktree directory at `../<project>-<branch>`
3. Optionally check **Switch to new worktree** to immediately activate it

The branch name must start with a letter or number and can contain letters, numbers, hyphens, and underscores.

## Removing Worktrees

Click the **Remove** button to delete a worktree. You'll be asked whether to also delete the associated git branch.

**Note:** You cannot remove the currently active worktree or the main project worktree.

## Related

- [Worktrees Concept](/docs/concepts/worktrees/) — How Trellis uses worktrees
- [Configuration: worktrees](/docs/reference/config/#worktrees) — Worktree configuration options
