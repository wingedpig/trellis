// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/cases"
	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
	"github.com/wingedpig/trellis/internal/genai"
	"github.com/wingedpig/trellis/internal/trace"
	"github.com/wingedpig/trellis/internal/worktree"
)

// CaseHandler handles case API requests.
type CaseHandler struct {
	caseMgr     *cases.Manager
	claudeMgr   *claude.Manager
	codexMgr    *codex.Manager
	traceMgr    *trace.Manager
	worktreeMgr worktree.Manager
}

// NewCaseHandler creates a new case handler.
func NewCaseHandler(caseMgr *cases.Manager, claudeMgr *claude.Manager, codexMgr *codex.Manager, traceMgr *trace.Manager, worktreeMgr worktree.Manager) *CaseHandler {
	return &CaseHandler{
		caseMgr:     caseMgr,
		claudeMgr:   claudeMgr,
		codexMgr:    codexMgr,
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
		if err == cases.ErrOpenCaseExists {
			WriteError(w, http.StatusConflict, ErrBadRequest, err.Error())
			return
		}
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

	if err := h.caseMgr.AttachEvidence(wt.Path, caseID, ev, file); err != nil {
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

// SaveCodexTranscript exports a Codex session transcript and saves it to a case.
func (h *CaseHandler) SaveCodexTranscript(w http.ResponseWriter, r *http.Request) {
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
	if h.codexMgr == nil {
		WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "Codex integration is not configured")
		return
	}

	transcript, err := h.codexMgr.ExportSession(body.SessionID, "full")
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, fmt.Sprintf("codex session not found: %s", body.SessionID))
		return
	}
	if body.Title == "" {
		body.Title = transcript.Source.DisplayName
	}

	refID := uuid.New().String()[:8]
	if err := h.caseMgr.SaveCodexTranscript(wt.Path, caseID, refID, body.Title, body.SessionID, transcript); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]string{
		"codex_ref_id": refID,
		"title":        body.Title,
	})
}

// UpdateCodexTranscript re-exports a Codex session transcript and overwrites the saved copy.
func (h *CaseHandler) UpdateCodexTranscript(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}
	vars := mux.Vars(r)
	caseID := vars["id"]
	codexID := vars["codex_id"]

	if h.codexMgr == nil {
		WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "Codex integration is not configured")
		return
	}

	c, err := h.caseMgr.Get(wt.Path, caseID)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	var sourceSessionID string
	for _, ref := range c.Codex {
		if ref.ID == codexID {
			sourceSessionID = ref.SourceSessionID
			break
		}
	}
	if sourceSessionID == "" {
		WriteError(w, http.StatusNotFound, ErrNotFound, "codex transcript ref not found or has no source session")
		return
	}
	transcript, err := h.codexMgr.ExportSession(sourceSessionID, "full")
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, fmt.Sprintf("source session not found: %s", sourceSessionID))
		return
	}
	if err := h.caseMgr.UpdateCodexTranscript(wt.Path, caseID, codexID, transcript); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"codex_ref_id":  codexID,
		"message_count": transcript.Stats.MessageCount,
	})
}

// ContinueCodexTranscript reads a codex transcript from a case and imports it as a new Codex session.
func (h *CaseHandler) ContinueCodexTranscript(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	worktreeName := vars["worktree"]
	caseID := vars["id"]
	codexID := vars["codex_id"]

	wt, ok := h.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}
	if h.codexMgr == nil {
		WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "Codex integration is not configured")
		return
	}

	transcript, err := h.caseMgr.GetCodexTranscript(wt.Path, caseID, codexID)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	session, err := h.codexMgr.ImportSession(worktreeName, wt.Path, transcript)
	if err != nil {
		if _, ok := err.(*codex.TranscriptError); ok {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, session.Info())
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

// SearchResult is one match returned by SearchArchived.
type SearchResult struct {
	cases.CaseInfo
	Score   int               `json:"score"`
	Snippet string            `json:"snippet,omitempty"` // first matching line for display
	Summary *cases.CaseSummary `json:"summary,omitempty"` // included so the page can render summary chips
}

// SearchArchived runs a full-text + filter search over archived cases.
//
// Query params:
//   q                    - free-text query (matched against title, summary
//                           fields, commit descriptions, notes.md; opt-in
//                           transcript scan when include_transcripts=1)
//   kind                 - bug|feature|investigation|task
//   from, to             - ISO date or RFC3339; bounds on created_at
//   has_traces           - "1" to require at least one linked trace
//   include_transcripts  - "1" to scan transcript .jsonl files (slow)
//   sort                 - date (default) | kind | duration | worktree
func (h *CaseHandler) SearchArchived(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	kindFilter := r.URL.Query().Get("kind")
	fromRaw := r.URL.Query().Get("from")
	toRaw := r.URL.Query().Get("to")
	hasTracesFilter := r.URL.Query().Get("has_traces") == "1"
	includeTranscripts := r.URL.Query().Get("include_transcripts") == "1"
	sortKey := r.URL.Query().Get("sort")
	if sortKey == "" {
		sortKey = "date"
	}

	var fromTime, toTime time.Time
	if fromRaw != "" {
		if t, err := parseFlexTime(fromRaw); err == nil {
			fromTime = t
		}
	}
	if toRaw != "" {
		if t, err := parseFlexTime(toRaw); err == nil {
			// Inclusive: bump to end of day if the input was a bare date.
			if len(toRaw) == 10 {
				t = t.Add(24*time.Hour - time.Nanosecond)
			}
			toTime = t
		}
	}

	infos, err := h.caseMgr.ListArchived(wt.Path)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, err.Error())
		return
	}

	qLower := strings.ToLower(q)
	terms := splitTerms(qLower)
	results := make([]SearchResult, 0, len(infos))
	for _, info := range infos {
		if kindFilter != "" && info.Kind != kindFilter {
			continue
		}
		if !fromTime.IsZero() && info.CreatedAt.Before(fromTime) {
			continue
		}
		if !toTime.IsZero() && info.CreatedAt.After(toTime) {
			continue
		}

		full, err := h.caseMgr.Get(wt.Path, info.ID)
		if err != nil {
			continue
		}

		if hasTracesFilter {
			traces, _ := h.caseMgr.ListTraces(wt.Path, info.ID)
			if len(traces) == 0 {
				continue
			}
		}

		// Build a haystack and score it.
		score, snippet := scoreCase(full, terms, qLower, h.caseMgr, wt.Path, includeTranscripts)
		if len(terms) > 0 && score == 0 {
			continue
		}

		results = append(results, SearchResult{
			CaseInfo: info,
			Score:    score,
			Snippet:  snippet,
			Summary:  full.Summary,
		})
	}

	// Sort
	switch sortKey {
	case "kind":
		sortByScoreThen(results, func(a, b SearchResult) bool { return a.Kind < b.Kind })
	case "duration":
		sortByScoreThen(results, func(a, b SearchResult) bool {
			return a.UpdatedAt.Sub(a.CreatedAt) > b.UpdatedAt.Sub(b.CreatedAt)
		})
	case "worktree":
		sortByScoreThen(results, func(a, b SearchResult) bool { return a.Worktree < b.Worktree })
	default: // date
		sortByScoreThen(results, func(a, b SearchResult) bool { return a.CreatedAt.After(b.CreatedAt) })
	}

	WriteJSON(w, http.StatusOK, results)
}

func parseFlexTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

func splitTerms(q string) []string {
	if q == "" {
		return nil
	}
	fields := strings.Fields(q)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

// scoreCase produces a relevance score and a short snippet for display.
// Component matches rank highest, title next, notes / commit
// descriptions lowest. With an empty term list every case scores 0 and the
// caller treats that as "include unconditionally" (browse mode, not search
// mode).
func scoreCase(c *cases.CaseJSON, terms []string, qLower string, mgr *cases.Manager, worktreePath string, includeTranscripts bool) (int, string) {
	if len(terms) == 0 {
		return 0, ""
	}

	score := 0
	var firstHit string

	hit := func(weight int, text, where string) {
		if text == "" {
			return
		}
		low := strings.ToLower(text)
		for _, t := range terms {
			if strings.Contains(low, t) {
				score += weight
				if firstHit == "" {
					firstHit = where + ": " + text
				}
			}
		}
	}

	// Highest-weight: explicit component fields.
	if c.Summary != nil {
		for _, comp := range c.Summary.Components {
			hit(8, comp, "component")
		}
		hit(6, c.Summary.Synopsis, "synopsis")
		hit(4, c.Summary.Symptoms, "symptoms")
		hit(4, c.Summary.RootCause, "root cause")
		hit(4, c.Summary.Resolution, "resolution")
	}
	hit(5, c.Title, "title")

	// Commit descriptions and messages.
	for _, ce := range c.Commits {
		hit(2, ce.Description, "commit")
		hit(1, firstLineString(ce.Message), "commit msg")
	}

	// notes.md
	notes, _ := mgr.GetNotes(worktreePath, c.ID)
	if notes != "" {
		hit(2, notes, "notes")
	}

	// Transcripts are opt-in: high volume, low signal-to-noise.
	if includeTranscripts {
		for _, ref := range c.Claude {
			hit(1, ref.Preview, "claude")
		}
		for _, ref := range c.Codex {
			hit(1, ref.Preview, "codex")
		}
	}

	return score, truncForSnippet(firstHit, 240)
}

func firstLineString(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func truncForSnippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// sortByScoreThen sorts results by score (high first), breaking ties using
// the supplied secondary comparator.
func sortByScoreThen(results []SearchResult, secondary func(a, b SearchResult) bool) {
	// stable sort to make secondary order matter.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0; j-- {
			a, b := results[j-1], results[j]
			swap := false
			if a.Score != b.Score {
				swap = a.Score < b.Score
			} else {
				swap = secondary(b, a)
			}
			if !swap {
				break
			}
			results[j-1], results[j] = b, a
		}
	}
}

// UpdateSummary applies partial edits to the generated summary fields.
// Works on both open and archived cases.
func (h *CaseHandler) UpdateSummary(w http.ResponseWriter, r *http.Request) {
	wt, ok := h.resolveWorktree(r)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}
	vars := mux.Vars(r)
	caseID := vars["id"]

	var upd cases.SummaryUpdate
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}
	// Apply the same normalization to hand-edits as we do to generated
	// summaries, so the chip set stays consistent regardless of source.
	if upd.Components != nil {
		upd.Components = genai.NormalizeComponents(upd.Components)
	}
	if err := h.caseMgr.UpdateSummary(wt.Path, caseID, upd); err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RegenerateSummary re-runs the case summary generator and overwrites the
// stored summary. Works on archived cases too (writes back into the
// archived directory). The caller is expected to confirm the overwrite if a
// summary already exists with hand edits.
func (h *CaseHandler) RegenerateSummary(w http.ResponseWriter, r *http.Request) {
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

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	deps := commitDeps{
		caseMgr:     h.caseMgr,
		traceMgr:    h.traceMgr,
		claudeMgr:   h.claudeMgr,
		codexMgr:    h.codexMgr,
		worktreeMgr: h.worktreeMgr,
	}
	summary, err := regenerateSummaryForCase(ctx, deps, wt.Path, c)
	if err != nil {
		WriteError(w, http.StatusBadGateway, ErrInternalError, "regenerate: "+err.Error())
		return
	}
	if err := h.caseMgr.ReplaceSummary(wt.Path, caseID, summary); err != nil {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "save summary: "+err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, summary)
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
