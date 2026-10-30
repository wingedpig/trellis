// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/wingedpig/trellis/internal/attachment"
)

// attachmentStore is the part of a Claude or Codex session that keeps the
// files a user includes with a message.
type attachmentStore interface {
	SaveAttachment(name string, r io.Reader) (attachment.File, error)
	LoadAttachments(ids []string) ([]attachment.File, error)
}

// uploadAttachment stores the multipart "file" field of r and responds with
// the saved attachment. The client sends its ID with the next message.
func uploadAttachment(w http.ResponseWriter, r *http.Request, store attachmentStore) {
	mr, err := r.MultipartReader()
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "multipart form required")
		return
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "file field required")
			return
		}
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid multipart form")
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}
		f, err := store.SaveAttachment(part.FileName(), part)
		part.Close()
		switch {
		case errors.Is(err, attachment.ErrTooLarge):
			WriteError(w, http.StatusRequestEntityTooLarge, ErrBadRequest,
				fmt.Sprintf("file is larger than %d MB", attachment.MaxSize>>20))
		case err != nil:
			WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		default:
			WriteJSON(w, http.StatusOK, f)
		}
		return
	}
}

// serveAttachment returns a stored attachment. Only images are shown inline;
// anything else is a download, so an uploaded HTML or SVG file can't run
// script on the trellis origin.
func serveAttachment(w http.ResponseWriter, r *http.Request, store attachmentStore, id string) {
	files, err := store.LoadAttachments([]string{id})
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, "attachment not found")
		return
	}
	f := files[0]
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if f.IsImage() {
		w.Header().Set("Content-Type", f.MIME)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	}
	// IDs are unique per upload, so the bytes never change.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, f.Path)
}

