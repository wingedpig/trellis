// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveToolImages(t *testing.T) {
	dir := t.TempDir()
	s := &Session{id: "s1", imagesDir: filepath.Join(dir, "s1")}

	png := []byte("\x89PNG fake")
	b64 := base64.StdEncoding.EncodeToString(png)
	msg := `{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"toolu_01Abc","content":[
			{"type":"text","text":"took a screenshot"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + b64 + `"}},
			{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"` + b64 + `"}}
		]},
		{"type":"tool_result","tool_use_id":"toolu_02Def","content":"plain text result"},
		{"type":"tool_result","tool_use_id":"../escape","content":[
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + b64 + `"}}
		]},
		{"type":"tool_result","tool_use_id":"toolu_03Ghi","content":[
			{"type":"image","source":{"type":"base64","media_type":"image/svg+xml","data":"` + b64 + `"}}
		]}
	]}`
	s.saveToolImages([]byte(msg))
	s.persistQ.Wait()

	images := s.ToolImages()
	if len(images) != 1 {
		t.Fatalf("ToolImages = %v, want only toolu_01Abc", images)
	}
	got := images["toolu_01Abc"]
	if len(got) != 2 || got[0] != "toolu_01Abc-0.png" || got[1] != "toolu_01Abc-1.jpg" {
		t.Fatalf("toolu_01Abc images = %v", got)
	}
	data, err := os.ReadFile(s.ToolImagePath(got[0]))
	if err != nil || string(data) != string(png) {
		t.Fatalf("saved image = %q, %v; want the decoded bytes", data, err)
	}

	for _, name := range []string{"../s2/toolu_01Abc-0.png", "toolu_01Abc-0.svg", "noext", "-0.png", "a.b-0.png"} {
		if p := s.ToolImagePath(name); p != "" {
			t.Errorf("ToolImagePath(%q) = %q, want rejected", name, p)
		}
	}
}

func TestSaveToolImagesWithoutDir(t *testing.T) {
	s := &Session{id: "s1"}
	s.saveToolImages([]byte(`{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}`))
	s.persistQ.Wait()
	if images := s.ToolImages(); images != nil {
		t.Fatalf("ToolImages = %v, want nil when the session has no images dir", images)
	}
}
