// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deletion that is already staged is in neither the working tree nor the
// index, and `git add` exits 128 on it; it must be skipped, while unstaged
// deletions, modifications, and new files are still added.
func TestStageablePathsSkipsStagedDeletions(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	write := func(name string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644))
	}
	git("init", "-q")
	write("staged-rm")
	write("unstaged-rm")
	write("modified")
	git("add", ".")
	git("commit", "-qm", "init")

	git("rm", "-q", "staged-rm")
	require.NoError(t, os.Remove(filepath.Join(dir, "unstaged-rm")))
	write("modified")
	write("new")

	files := []string{"staged-rm", "unstaged-rm", "modified", "new"}
	got := stageablePaths(context.Background(), dir, files)
	assert.Equal(t, []string{"unstaged-rm", "modified", "new"}, got)
	git(append([]string{"add", "--"}, got...)...)
	git("commit", "-qm", "all four")
}
