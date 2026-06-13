// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	trellis "github.com/wingedpig/trellis"
)

// The embedded skill must carry the managed-by marker, or installed copies
// would be classified as user-owned and never refreshed after upgrades.
func TestEmbeddedSkillHasMarker(t *testing.T) {
	if !strings.Contains(trellis.SkillMD, Marker) {
		t.Fatalf("embedded SKILL.md is missing the %q marker", Marker)
	}
}

func TestInstall(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, RelPath)

	// Fresh install creates the file.
	changed, err := Install(dir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !changed {
		t.Fatal("expected fresh install to report changed")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read installed skill: %v", err)
	}
	if string(data) != trellis.SkillMD {
		t.Fatal("installed skill does not match embedded content")
	}

	// Re-install is a no-op.
	if changed, err = Install(dir); err != nil || changed {
		t.Fatalf("expected idempotent no-op, got changed=%v err=%v", changed, err)
	}

	// A drifted managed copy is refreshed.
	if err := os.WriteFile(target, []byte("stale "+Marker), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err = Install(dir); err != nil || !changed {
		t.Fatalf("expected managed copy refresh, got changed=%v err=%v", changed, err)
	}

	// A user-owned copy (no marker) is preserved.
	userContent := []byte("# my custom skill\n")
	if err := os.WriteFile(target, userContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err = Install(dir); err != nil || changed {
		t.Fatalf("expected user-owned copy to be preserved, got changed=%v err=%v", changed, err)
	}
	data, _ = os.ReadFile(target)
	if string(data) != string(userContent) {
		t.Fatal("user-owned skill was overwritten")
	}
}
