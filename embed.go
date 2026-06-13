// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package trellis embeds repo-root assets that ship inside the binary.
package trellis

import _ "embed"

// SkillMD is the agent skill file teaching coding agents (Claude Code,
// Codex) how to drive trellis via trellis-ctl. It is installed into
// .claude/skills/trellis/SKILL.md in the repo and each worktree by
// internal/skill.
//
//go:embed SKILL.md
var SkillMD string
