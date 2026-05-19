// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a fresh git repo at dir with one committed file, returns dir.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "keep.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write keep.go: %v", err)
	}
	run("add", "keep.go")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestDiffForFiles_scopedToSelection(t *testing.T) {
	dir := initRepo(t)

	// Modify a tracked file.
	if err := os.WriteFile(filepath.Join(dir, "keep.go"), []byte("package main\n\nfunc main() { println(\"hi\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create an untracked file the user WILL select.
	if err := os.WriteFile(filepath.Join(dir, "selected_new.go"), []byte("package main\n// selected new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create an untracked file the user will NOT select (this is the bug
	// case: the generated message must not see it).
	if err := os.WriteFile(filepath.Join(dir, "unselected_new.go"), []byte("package main\n// SHOULD NOT APPEAR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Also pre-stage an unrelated change to prove --staged isn't what we read.
	if err := os.WriteFile(filepath.Join(dir, "prestaged.go"), []byte("package main\n// pre-staged garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", dir, "add", "prestaged.go").Run()

	diff := diffForFiles(context.Background(), dir, []string{"keep.go", "selected_new.go"})

	if diff == "" {
		t.Fatal("diff is empty")
	}
	if !strings.Contains(diff, "keep.go") {
		t.Errorf("diff missing tracked file:\n%s", diff)
	}
	if !strings.Contains(diff, "selected_new.go") {
		t.Errorf("diff missing untracked selected file:\n%s", diff)
	}
	if !strings.Contains(diff, "println(\"hi\")") {
		t.Errorf("diff missing the new line of keep.go:\n%s", diff)
	}
	if !strings.Contains(diff, "selected new file") {
		t.Errorf("diff missing selected_new.go content:\n%s", diff)
	}
	if strings.Contains(diff, "SHOULD NOT APPEAR") {
		t.Errorf("diff leaked unselected untracked file content:\n%s", diff)
	}
	if strings.Contains(diff, "prestaged.go") {
		t.Errorf("diff leaked pre-staged unrelated file:\n%s", diff)
	}
}

func TestDiffForFiles_emptyInput(t *testing.T) {
	dir := initRepo(t)
	if got := diffForFiles(context.Background(), dir, nil); got != "" {
		t.Errorf("nil files: got %q, want empty", got)
	}
	if got := diffForFiles(context.Background(), dir, []string{}); got != "" {
		t.Errorf("empty files: got %q, want empty", got)
	}
}

func TestDiffForFiles_rejectsPathTraversal(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "keep.go"), []byte("package main\nfunc main() { _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := diffForFiles(context.Background(), dir, []string{"../etc/passwd", "keep.go"})
	if strings.Contains(diff, "../etc/passwd") {
		t.Errorf("path traversal not filtered:\n%s", diff)
	}
	if !strings.Contains(diff, "keep.go") {
		t.Errorf("valid path was dropped along with the bad one:\n%s", diff)
	}
}
