// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package attachment

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A minimal PNG signature is enough for http.DetectContentType.
var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	f, err := Save(dir, "../../etc/my report (final).pdf", strings.NewReader("%PDF-1.4 hello"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "my_report__final_.pdf" {
		t.Errorf("Name = %q", f.Name)
	}
	if filepath.Dir(f.Path) != dir || !filepath.IsAbs(f.Path) {
		t.Errorf("Path = %q, want inside %q", f.Path, dir)
	}
	if f.MIME != "application/pdf" || f.IsImage() {
		t.Errorf("MIME = %q", f.MIME)
	}
	if f.Size != int64(len("%PDF-1.4 hello")) {
		t.Errorf("Size = %d", f.Size)
	}
	got, err := Load(dir, f.ID)
	if err != nil || got != f {
		t.Errorf("Load = %+v, %v; want %+v", got, err, f)
	}
}

func TestImageDetection(t *testing.T) {
	dir := t.TempDir()
	// A screenshot pasted without an extension is still an image.
	f, err := Save(dir, "image", bytes.NewReader(pngHeader))
	if err != nil {
		t.Fatal(err)
	}
	if !f.IsImage() || f.MIME != "image/png" {
		t.Errorf("MIME = %q, want image/png", f.MIME)
	}
	// A file named like an image that isn't one must not go to the model as one.
	f, err = Save(dir, "fake.png", strings.NewReader("not an image"))
	if err != nil {
		t.Fatal(err)
	}
	if f.IsImage() {
		t.Errorf("fake.png detected as %q", f.MIME)
	}
}

func TestSaveTooLarge(t *testing.T) {
	dir := t.TempDir()
	_, err := Save(dir, "big.bin", bytes.NewReader(make([]byte, MaxSize+1)))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("oversized upload left %d files behind", len(entries))
	}
}

func TestPathRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"", "../secret", "deadbeef-../x", "zzzzzzzz-file", "deadbeef", "deadbeef-a/b"} {
		if p := Path(dir, id); p != "" {
			t.Errorf("Path(%q) = %q, want rejected", id, p)
		}
	}
	if p := Path(dir, "deadbeef-ok.txt"); p != filepath.Join(dir, "deadbeef-ok.txt") {
		t.Errorf("valid id rejected: %q", p)
	}
}

func TestWithNote(t *testing.T) {
	files := []File{{Path: "/s/a.txt", Size: 10}, {Path: "/s/b.png", Size: 2048}}
	want := "look\n\nAttached files:\n- /s/a.txt (10 bytes)\n- /s/b.png (2.0 KB)"
	if got := WithNote("look", files); got != want {
		t.Errorf("WithNote = %q", got)
	}
	if got := WithNote("", files[:1]); got != "Attached files:\n- /s/a.txt (10 bytes)" {
		t.Errorf("WithNote empty prompt = %q", got)
	}
	if got := WithNote("hi", nil); got != "hi" {
		t.Errorf("WithNote no files = %q", got)
	}
}
