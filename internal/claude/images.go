// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Tool results (including the screenshots browser tools return) are not part
// of a session's stored messages. Their images are saved beside them instead,
// one file per image named <tool_use_id>-<n><ext>, so the session page can
// show them after a reload.

var toolImageExts = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// saveToolImages writes the images carried by the tool_result blocks of a
// "user" stream event's message. Writes go through the persist queue so a
// large screenshot never stalls the stream reader.
func (s *Session) saveToolImages(message json.RawMessage) {
	if s.imagesDir == "" {
		return
	}
	var msg struct {
		Content []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
		} `json:"content"`
	}
	if json.Unmarshal(message, &msg) != nil {
		return
	}
	type image struct {
		name string
		data []byte
	}
	var images []image
	for _, block := range msg.Content {
		if block.Type != "tool_result" || !validToolUseID(block.ToolUseID) {
			continue
		}
		var parts []struct {
			Type   string `json:"type"`
			Source struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type"`
				Data      string `json:"data"`
			} `json:"source"`
		}
		// A plain-string result is not an array; nothing to save.
		if json.Unmarshal(block.Content, &parts) != nil {
			continue
		}
		n := 0
		for _, p := range parts {
			ext, ok := toolImageExts[p.Source.MediaType]
			if p.Type != "image" || p.Source.Type != "base64" || !ok {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(p.Source.Data)
			if err != nil {
				continue
			}
			images = append(images, image{fmt.Sprintf("%s-%d%s", block.ToolUseID, n, ext), data})
			n++
		}
	}
	if len(images) == 0 {
		return
	}
	dir, id := s.imagesDir, s.id
	s.persistQ.Submit(func() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("claude: failed to create images dir for session %s: %v", id, err)
			return
		}
		for _, img := range images {
			if err := os.WriteFile(filepath.Join(dir, img.name), img.data, 0o644); err != nil {
				log.Printf("claude: failed to save tool image for session %s: %v", id, err)
			}
		}
	})
}

// validToolUseID reports whether id is safe to use in a file name. Tool use
// IDs look like "toolu_01AbC…"; the "-" check keeps the name parseable.
func validToolUseID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// ToolImages maps each tool_use_id to the file names of the images saved for
// its result, in the order the tool returned them.
func (s *Session) ToolImages() map[string][]string {
	if s.imagesDir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.imagesDir)
	if err != nil {
		return nil
	}
	images := make(map[string][]string)
	for _, e := range entries {
		name := e.Name()
		dash := strings.LastIndexByte(name, '-')
		if e.IsDir() || dash <= 0 {
			continue
		}
		id := name[:dash]
		images[id] = append(images[id], name)
	}
	return images
}

// ToolImagePath returns the path of the saved tool image with the given file
// name, or "" when name could not be one.
func (s *Session) ToolImagePath(name string) string {
	dash := strings.LastIndexByte(name, '-')
	if s.imagesDir == "" || dash <= 0 || !validToolUseID(name[:dash]) {
		return ""
	}
	ext := filepath.Ext(name)
	known := false
	for _, e := range toolImageExts {
		known = known || e == ext
	}
	if !known {
		return ""
	}
	return filepath.Join(s.imagesDir, name)
}

// copyToolImages hard-links (or, failing that, copies) every saved tool image
// from one session to another, so a fork shows the same screenshots.
func copyToolImages(from, to string) {
	if from == "" || to == "" {
		return
	}
	entries, err := os.ReadDir(from)
	if err != nil || len(entries) == 0 {
		return
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		return
	}
	for _, e := range entries {
		src, dst := filepath.Join(from, e.Name()), filepath.Join(to, e.Name())
		if os.Link(src, dst) == nil {
			continue
		}
		if data, err := os.ReadFile(src); err == nil {
			os.WriteFile(dst, data, 0o644)
		}
	}
}
