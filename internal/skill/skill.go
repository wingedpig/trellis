// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package skill installs the embedded trellis skill file into project
// checkouts so coding agents (Claude Code, Codex) discover trellis-ctl.
package skill

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	trellis "github.com/wingedpig/trellis"
)

// Marker identifies a skill file as trellis-managed. Files without the
// marker are treated as user-owned and never overwritten.
const Marker = "managed-by: trellis"

// RelPath is where the skill is installed relative to a repo or worktree root.
var RelPath = filepath.Join(".claude", "skills", "trellis", "SKILL.md")

// Install writes the embedded skill file into dir. It creates the file when
// missing and refreshes trellis-managed copies that have drifted from the
// embedded version (e.g. after a trellis upgrade). User-owned files are left
// untouched. Returns true when the file was created or updated.
func Install(dir string) (bool, error) {
	target := filepath.Join(dir, RelPath)
	embedded := []byte(trellis.SkillMD)

	existing, err := os.ReadFile(target)
	switch {
	case err == nil && bytes.Equal(existing, embedded):
		return false, nil
	case err == nil && !bytes.Contains(existing, []byte(Marker)):
		// User-owned copy; leave it alone.
		return false, nil
	case err != nil && !os.IsNotExist(err):
		return false, fmt.Errorf("read %s: %w", target, err)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, fmt.Errorf("create skill dir: %w", err)
	}
	if err := os.WriteFile(target, embedded, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", target, err)
	}
	return true, nil
}
