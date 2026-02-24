// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/cases"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/trace"
	"github.com/wingedpig/trellis/internal/worktree"
)

// CaseHandler handles case API requests.
type CaseHandler struct {
	caseMgr     *cases.Manager
	claudeMgr   *claude.Manager
	traceMgr    *trace.Manager
	worktreeMgr worktree.Manager
}

// NewCaseHandler creates a new case handler.
func NewCaseHandler(caseMgr *cases.Manager, claudeMgr *claude.Manager, traceMgr *trace.Manager, worktreeMgr worktree.Manager) *CaseHandler {
	return &CaseHandler{
		caseMgr:     caseMgr,
		claudeMgr:   claudeMgr,
		traceMgr:    traceMgr,
		worktreeMgr: worktreeMgr,
	}
}

// resolveWorktree looks up worktree info from the URL parameter.
func (h *CaseHandler) resolveWorktree(r *http.Request) (worktree.WorktreeInfo, bool) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]
	return h.worktreeMgr.GetByName(worktreeName)
}

// List returns open cases for a worktree.
func (h *CaseHandler) List(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	caseList, err := h.caseMgr.List(wt.Path)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}
	if caseList == nil {
		caseList = []cases.CaseInfo{}
	}
	WriteJSON(w, http.StatusOK, caseList)
}

// ListArchived returns archived cases for a worktree.
func (h *CaseHandler) ListArchived(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	caseList, err := h.caseMgr.ListArchived(wt.Path)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}
	if caseList == nil {
		caseList = []cases.CaseInfo{}
	}
	WriteJSON(w, http.StatusOK, caseList)
}

// Create creates a new case.
func (h *CaseHandler) Create(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]

	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	var body struct {
		Title string `json:"title"`
		Kind  string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON")
		return
	}
	if body.Title == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "title is required")
		return
	}
	if body.Kind == "" {
		body.Kind = "task"
	}

	c, err := h.caseMgr.Create(wt.Path, body.Title, body.Kind, worktreeName, wt.Branch, wt.Commit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, c)
}

// Get returns the full case manifest.
func (h *CaseHandler) Get(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]

	c, err := h.caseMgr.Get(wt.Path, caseID)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, c)
}

// GetNotes returns the notes.md content for a case.
func (h *CaseHandler) GetNotes(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]

	notes, err := h.caseMgr.GetNotes(wt.Path, caseID)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(notes))
}

// Update applies partial updates to a case.
func (h *CaseHandler) Update(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]

	var updates cases.CaseUpdate
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON")
		return
	}

	if err := h.caseMgr.Update(wt.Path, caseID, updates); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Delete permanently removes a case.
func (h *CaseHandler) Delete(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]

	if err := h.caseMgr.Delete(wt.Path, caseID); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Archive moves a case to the archived directory.
// Before archiving, stale transcripts are auto-updated from their live sessions.
func (h *CaseHandler) Archive(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]

	// Auto-update stale transcripts before archiving
	if h.claudeMgr != nil {
		if c, err := h.caseMgr.Get(wt.Path, caseID); err == nil {
			for _, ref := range c.Claude {
				if ref.SourceSessionID == "" {
					continue
				}
				transcript, err := h.claudeMgr.ExportSession(ref.SourceSessionID, "full")
				if err != nil {
					continue // session deleted, archive with existing snapshot
				}
				if transcript.Stats.MessageCount != ref.MessageCount {
					_ = h.caseMgr.UpdateTranscript(wt.Path, caseID, ref.ID, transcript)
				}
			}
		}
	}

	if err := h.caseMgr.Archive(wt.Path, caseID); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Reopen moves a case back from the archived directory.
func (h *CaseHandler) Reopen(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]

	if err := h.caseMgr.Reopen(wt.Path, caseID); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// AttachEvidence handles multipart upload of evidence files.
func (h *CaseHandler) AttachEvidence(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]

	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB max
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "file field required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "read file: "+err.Error())
		return
	}

	title := r.FormValue("title")
	if title == "" {
		title = header.Filename
	}

	ext := filepath.Ext(header.Filename)
	if ext != "" {
		ext = ext[1:] // strip leading dot
	}

	tags := r.Form["tags"]

	ev := cases.CaseEvidence{
		Title:    title,
		Filename: header.Filename,
		Format:   ext,
		Tags:     tags,
		AddedAt:  time.Now(),
	}

	if err := h.caseMgr.AttachEvidence(wt.Path, caseID, ev, data); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, ev)
}

// SaveTranscript exports a Claude session transcript and saves it to a case.
func (h *CaseHandler) SaveTranscript(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]

	var body struct {
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON")
		return
	}
	if body.SessionID == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "session_id is required")
		return
	}

	if h.claudeMgr == nil {
		WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "Claude integration is not configured")
		return
	}

	// Export the transcript from the Claude session
	transcript, err := h.claudeMgr.ExportSession(body.SessionID, "full")
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, fmt.Sprintf("claude session not found: %s", body.SessionID))
		return
	}

	if body.Title == "" {
		body.Title = transcript.Source.DisplayName
	}

	refID := uuid.New().String()[:8]
	if err := h.caseMgr.SaveTranscript(wt.Path, caseID, refID, body.Title, body.SessionID, transcript); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]string{
		"claude_ref_id": refID,
		"title":         body.Title,
	})
}

// UpdateTranscript re-exports a Claude session transcript and overwrites the saved copy.
func (h *CaseHandler) UpdateTranscript(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]
	claudeID := vars["claude_id"]

	if h.claudeMgr == nil {
		WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "Claude integration is not configured")
		return
	}

	// Load the case to find the ref's SourceSessionID
	c, err := h.caseMgr.Get(wt.Path, caseID)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	var sourceSessionID string
	for _, ref := range c.Claude {
		if ref.ID == claudeID {
			sourceSessionID = ref.SourceSessionID
			break
		}
	}
	if sourceSessionID == "" {
		WriteError(w, http.StatusNotFound, ErrNotFound, "transcript ref not found or has no source session")
		return
	}

	transcript, err := h.claudeMgr.ExportSession(sourceSessionID, "full")
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, fmt.Sprintf("source session not found: %s", sourceSessionID))
		return
	}

	if err := h.caseMgr.UpdateTranscript(wt.Path, caseID, claudeID, transcript); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"claude_ref_id": claudeID,
		"message_count": transcript.Stats.MessageCount,
	})
}

// SaveTrace fetches a trace report and saves the full data to a case.
func (h *CaseHandler) SaveTrace(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]

	var body struct {
		ReportName string `json:"report_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON")
		return
	}
	if body.ReportName == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "report_name is required")
		return
	}

	if h.traceMgr == nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, "trace manager not configured")
		return
	}

	report, err := h.traceMgr.GetReport(body.ReportName)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, fmt.Sprintf("trace report not found: %s", body.ReportName))
		return
	}

	refID := uuid.New().String()[:8]
	if err := h.caseMgr.SaveTrace(wt.Path, caseID, refID, report); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]string{
		"trace_ref_id": refID,
		"report_name":  body.ReportName,
	})
}

// DeleteTrace removes a trace reference from a case.
func (h *CaseHandler) DeleteTrace(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	vars := mux.Vars(r)
	caseID := vars["id"]
	traceID := vars["trace_id"]

	if err := h.caseMgr.DeleteTrace(wt.Path, caseID, traceID); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ContinueTranscript reads a transcript from a case and imports it as a new Claude session.
func (h *CaseHandler) ContinueTranscript(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]
	caseID := vars["id"]
	claudeID := vars["claude_id"]

	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	if h.claudeMgr == nil {
		WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "Claude integration is not configured")
		return
	}

	transcript, err := h.caseMgr.GetTranscript(wt.Path, caseID, claudeID)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	session, err := h.claudeMgr.ImportSession(worktreeName, wt.Path, wt.Branch, transcript)
	if err != nil {
		if _, ok := err.(*claude.TranscriptError); ok {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, session.Info())
}
