// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowStore_LoadEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewWindowStore(filepath.Join(dir, "windows.json"))

	data, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("expected empty map, got %v", data)
	}
}

func TestWindowStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewWindowStore(filepath.Join(dir, "windows.json"))

	want := WindowsData{
		"groups_io":         {"dev", "shell", "claude"},
		"groups_io-feature": {"dev", "shell-2"},
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	for session, windows := range want {
		gotWindows, ok := got[session]
		if !ok {
			t.Errorf("session %q not found in loaded data", session)
			continue
		}
		if len(gotWindows) != len(windows) {
			t.Errorf("session %q: got %d windows, want %d", session, len(gotWindows), len(windows))
			continue
		}
		for i, w := range windows {
			if gotWindows[i] != w {
				t.Errorf("session %q window %d: got %q, want %q", session, i, gotWindows[i], w)
			}
		}
	}
}

func TestWindowStore_SaveCreatesDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "dir")
	store := NewWindowStore(filepath.Join(nested, "windows.json"))

	data := WindowsData{"test": {"win1"}}
	if err := store.Save(data); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filepath.Join(nested, "windows.json")); err != nil {
		t.Fatalf("file does not exist after Save: %v", err)
	}
}

func TestWindowStore_LoadCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "windows.json")
	os.WriteFile(fp, []byte("not json"), 0644)

	store := NewWindowStore(fp)
	_, err := store.Load()
	if err == nil {
		t.Fatal("expected error for corrupted file")
	}
}

func TestWindowStore_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "windows.json")
	store := NewWindowStore(fp)

	// Save initial data
	if err := store.Save(WindowsData{"s1": {"w1"}}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Save new data
	if err := store.Save(WindowsData{"s1": {"w1", "w2"}}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Verify no .tmp file left behind
	if _, err := os.Stat(fp + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should not exist after successful save")
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(got["s1"]) != 2 {
		t.Errorf("expected 2 windows, got %d", len(got["s1"]))
	}
}
