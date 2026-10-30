// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package attachment stores files the user includes with an agent session turn.
//
// Each session keeps its uploads in a directory of its own, one file per
// upload named <8 hex>-<sanitized original name>. The stored name is the
// attachment's ID: it is unique, safe to put in a URL, and carries the
// original name, so no metadata needs to be kept beside the file.
package attachment

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MaxSize caps a single upload.
const MaxSize = 32 << 20

// MaxInlineImageSize caps the images sent to the model inline. Larger images
// are still offered by path.
const MaxInlineImageSize = 5 << 20

// File is an attachment as stored with a user message.
type File struct {
	ID   string `json:"id"`   // stored file name
	Name string `json:"name"` // original (sanitized) file name
	MIME string `json:"mime,omitempty"`
	Size int64  `json:"size"`
	Path string `json:"path"` // absolute path on disk
}

var imageMIMEs = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// IsImage reports whether the model can take the file as an image input.
func (f File) IsImage() bool { return imageMIMEs[f.MIME] }

// ErrTooLarge is returned by Save when the upload exceeds MaxSize.
var ErrTooLarge = errors.New("attachment too large")

// Save writes r to dir under a new unique name derived from name.
func Save(dir, name string, r io.Reader) (File, error) {
	if dir == "" {
		return File{}, errors.New("attachments are not available")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return File{}, err
	}
	var prefix [4]byte
	if _, err := rand.Read(prefix[:]); err != nil {
		return File{}, err
	}
	id := hex.EncodeToString(prefix[:]) + "-" + sanitize(name)
	path := filepath.Join(dir, id)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return File{}, err
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > MaxSize {
		err = ErrTooLarge
	}
	if err != nil {
		os.Remove(path)
		return File{}, err
	}
	return Load(dir, id)
}

// Load returns the attachment stored in dir under id.
func Load(dir, id string) (File, error) {
	path := Path(dir, id)
	if path == "" {
		return File{}, fmt.Errorf("invalid attachment %q", id)
	}
	info, err := os.Stat(path)
	if err != nil {
		return File{}, err
	}
	if !info.Mode().IsRegular() {
		return File{}, fmt.Errorf("invalid attachment %q", id)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return File{}, err
	}
	return File{
		ID:   id,
		Name: id[strings.IndexByte(id, '-')+1:],
		MIME: detectMIME(path),
		Size: info.Size(),
		Path: abs,
	}, nil
}

// Path returns where id is stored in dir, or "" when id is not a name Save
// could have produced (so a request can't reach outside dir).
func Path(dir, id string) string {
	if dir == "" || len(id) < 10 || id[8] != '-' || sanitize(id) != id {
		return ""
	}
	if _, err := hex.DecodeString(id[:8]); err != nil {
		return ""
	}
	return filepath.Join(dir, id)
}

// Note is the text appended to a prompt so the agent knows where the files
// are; images are sent inline as well, but the path lets the agent copy or
// inspect them.
func Note(files []File) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Attached files:")
	for _, f := range files {
		fmt.Fprintf(&b, "\n- %s (%s)", f.Path, humanSize(f.Size))
	}
	return b.String()
}

// WithNote joins a prompt and the note for its attachments.
func WithNote(prompt string, files []File) string {
	note := Note(files)
	switch {
	case note == "":
		return prompt
	case prompt == "":
		return note
	}
	return prompt + "\n\n" + note
}

// CopyDir hard-links (or, failing that, copies) every attachment from one
// session's directory to another's, so a fork still shows them.
func CopyDir(from, to string) {
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

// sanitize reduces name to a safe base name: path separators are dropped and
// anything outside [A-Za-z0-9._-] becomes '_'.
func sanitize(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := strings.TrimLeft(b.String(), ".")
	if len(s) > 100 {
		ext := filepath.Ext(s)
		if len(ext) > 10 {
			ext = ""
		}
		s = s[:100-len(ext)] + ext
	}
	if s == "" {
		s = "file"
	}
	return s
}

// detectMIME prefers the content's signature for the image types the model
// accepts, so a misnamed screenshot still goes inline, and falls back to the
// extension.
func detectMIME(path string) string {
	if f, err := os.Open(path); err == nil {
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		f.Close()
		if t := http.DetectContentType(head[:n]); imageMIMEs[t] {
			return t
		}
	}
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); t != "" {
		if i := strings.IndexByte(t, ';'); i >= 0 {
			t = t[:i]
		}
		if !imageMIMEs[t] {
			return t
		}
	}
	return "application/octet-stream"
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
