// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/claude"
)

func TestFileAPI(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "docs", "spec.md"), "# Spec\n")
	write(filepath.Join(root, "bin.dat"), "\xff\xfe\x00")
	write(filepath.Join(outside, "secret.md"), "secret")
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}

	cm := claude.NewManager(t.TempDir())
	s := cm.CreateSession("main", root, "docs")
	h := NewClaudeHandler(cm, nil, nil, nil, nil, nil)

	get := func(path string) (int, map[string]interface{}) {
		req := httptest.NewRequest(http.MethodGet, "/file?path="+url.QueryEscape(path), nil)
		req = mux.SetURLVars(req, map[string]string{"session": s.ID()})
		rec := httptest.NewRecorder()
		h.FileAPI(rec, req)
		var body map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	for _, p := range []string{"docs/spec.md", filepath.Join(root, "docs", "spec.md")} {
		code, body := get(p)
		data, _ := body["data"].(map[string]interface{})
		if code != http.StatusOK || data["content"] != "# Spec\n" || data["path"] != "docs/spec.md" {
			t.Errorf("get(%q) = %d %v, want the file", p, code, body)
		}
	}

	for _, p := range []string{
		"../" + filepath.Base(outside) + "/secret.md",
		filepath.Join(outside, "secret.md"),
		"link.md",
		"bin.dat",
		"docs",
		"missing.md",
		"",
	} {
		if code, _ := get(p); code == http.StatusOK {
			t.Errorf("get(%q) = 200, want refused", p)
		}
	}
}

func TestFilesExistAPI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "spec.md"), []byte("# Spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cm := claude.NewManager(t.TempDir())
	s := cm.CreateSession("main", root, "docs")
	h := NewClaudeHandler(cm, nil, nil, nil, nil, nil)

	paths := []string{
		"spec.md",
		filepath.Join(root, "spec.md"),
		"renamed.md",
		filepath.Join(root, "gone.md"),
		"../outside.md",
	}
	reqBody, _ := json.Marshal(map[string]interface{}{"paths": paths})
	req := httptest.NewRequest(http.MethodPost, "/files/exist", strings.NewReader(string(reqBody)))
	req = mux.SetURLVars(req, map[string]string{"session": s.ID()})
	rec := httptest.NewRecorder()
	h.FilesExistAPI(rec, req)

	var body struct {
		Data struct {
			Missing []string `json:"missing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("FilesExistAPI = %d %s", rec.Code, rec.Body.String())
	}
	want := paths[2:]
	if !reflect.DeepEqual(body.Data.Missing, want) {
		t.Errorf("missing = %v, want %v", body.Data.Missing, want)
	}
}
