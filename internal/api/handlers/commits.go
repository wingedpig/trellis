// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
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

// commitRequest is shared by the Commit and WrapUp endpoints. WrapUp-only
// fields (Traces, Links, RelatedSessions, Summary) are simply ignored on the
// intermediate-commit path.
type commitRequest struct {
	SessionID       string              `json:"session_id"`
	CaseID          string              `json:"case_id"`
	Title           string              `json:"title"`
	Kind            string              `json:"kind"`
	CommitMessage   string              `json:"commit_message"`
	Description     string              `json:"description"` // per-commit narrative beat (intermediate commits only)
	Files           []string            `json:"files"`
	Links           []cases.CaseLink    `json:"links"`
	Traces          []string            `json:"traces"`
	RelatedSessions []relatedSessionRef `json:"related_sessions,omitempty"`
	// Summary is the user-edited summary from the wrap-up modal. When
	// non-nil it is used verbatim (components are still normalized) and
	// server-side generation is skipped. When nil, the server generates
	// the summary as before.
	Summary *cases.CaseSummary `json:"summary,omitempty"`
}

// commitDeps bundles the things commitToCase needs from a handler.
type commitDeps struct {
	caseMgr     *cases.Manager
	traceMgr    *trace.Manager
	claudeMgr   *claude.Manager
	codexMgr    *codex.Manager
	worktreeMgr worktree.Manager
}

// agentAdapter abstracts the Claude-vs-Codex specifics of session save /
// refresh / trash within commitToCase.
type agentAdapter interface {
	name() string
	// saveInitialTranscript exports the active session and saves it to the case
	// (called when we create a new case as part of the commit).
	saveInitialTranscript(worktreePath, caseID, sessionID, title string) error
	// refreshAttached re-exports every transcript of this agent already
	// attached to the case (used at wrap-up to capture final state).
	refreshAttached(worktreePath string, c *cases.CaseJSON)
	// trashSession archives the live session post-commit.
	trashSession(sessionID string)
	// recentUserMessages returns up to n recent user-message previews from
	// the active session, used as input to the commit-message prompt.
	recentUserMessages(sessionID string, n int) []string
	// transcriptPreviews returns one-line previews for every transcript of
	// this agent attached to the case, used as input to the summary prompt.
	transcriptPreviews(c *cases.CaseJSON) []string
}

// claudeAdapter implements agentAdapter for Claude sessions.
type claudeAdapter struct{ m *claude.Manager }

func (a claudeAdapter) name() string { return "claude" }

func (a claudeAdapter) saveInitialTranscript(worktreePath, caseID, sessionID, title string) error {
	if a.m == nil || sessionID == "" {
		return nil
	}
	t, err := a.m.ExportSession(sessionID, "full")
	if err != nil {
		return err
	}
	// Caller (commitToCase) holds the caseMgr; we ask the runtime to do the
	// save via a callback-style: instead, we return the transcript here and
	// let the orchestrator save it. To keep the adapter narrow, we just
	// return nil — the orchestrator calls SaveTranscript directly when it
	// has access to the case manager. See commitToCase for the actual write.
	_ = t
	_ = title
	return nil
}

func (a claudeAdapter) refreshAttached(worktreePath string, c *cases.CaseJSON) {
	if a.m == nil {
		return
	}
	// We need the caseMgr to write, so refresh logic lives in commitToCase.
}

func (a claudeAdapter) trashSession(sessionID string) {
	if a.m == nil || sessionID == "" {
		return
	}
	_ = a.m.TrashSession(sessionID)
}

func (a claudeAdapter) recentUserMessages(sessionID string, n int) []string {
	if a.m == nil || sessionID == "" {
		return nil
	}
	t, err := a.m.ExportSession(sessionID, "summary")
	if err != nil {
		return nil
	}
	return lastUserClaudeMessages(t.Messages, n)
}

func (a claudeAdapter) transcriptPreviews(c *cases.CaseJSON) []string {
	out := make([]string, 0, len(c.Claude))
	for _, ref := range c.Claude {
		if ref.Preview != "" {
			out = append(out, "claude: "+ref.Title+" — "+ref.Preview)
		}
	}
	return out
}

func lastUserClaudeMessages(msgs []claude.Message, n int) []string {
	var out []string
	for i := len(msgs) - 1; i >= 0 && len(out) < n; i-- {
		m := msgs[i]
		if m.Role != "user" {
			continue
		}
		isToolResult := false
		for _, b := range m.Content {
			if b.Type == "tool_result" {
				isToolResult = true
				break
			}
		}
		if isToolResult {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				out = append(out, b.Text)
				break
			}
		}
	}
	return out
}

// codexAdapter implements agentAdapter for Codex sessions.
type codexAdapter struct{ m *codex.Manager }

func (a codexAdapter) name() string { return "codex" }

func (a codexAdapter) saveInitialTranscript(worktreePath, caseID, sessionID, title string) error {
	return nil // handled inline in commitToCase
}

func (a codexAdapter) refreshAttached(worktreePath string, c *cases.CaseJSON) {}

func (a codexAdapter) trashSession(sessionID string) {
	if a.m == nil || sessionID == "" {
		return
	}
	_ = a.m.TrashSession(sessionID)
}

func (a codexAdapter) recentUserMessages(sessionID string, n int) []string {
	if a.m == nil || sessionID == "" {
		return nil
	}
	t, err := a.m.ExportSession(sessionID, "summary")
	if err != nil {
		return nil
	}
	return lastUserCodexMessages(t.Messages, n)
}

func (a codexAdapter) transcriptPreviews(c *cases.CaseJSON) []string {
	out := make([]string, 0, len(c.Codex))
	for _, ref := range c.Codex {
		if ref.Preview != "" {
			out = append(out, "codex: "+ref.Title+" — "+ref.Preview)
		}
	}
	return out
}

func lastUserCodexMessages(msgs []codex.Message, n int) []string {
	var out []string
	for i := len(msgs) - 1; i >= 0 && len(out) < n; i-- {
		m := msgs[i]
		if m.Role != "user" {
			continue
		}
		for _, it := range m.Items {
			if strings.TrimSpace(it.Text) != "" {
				out = append(out, it.Text)
				break
			}
		}
	}
	return out
}

// commitToCase is the shared orchestrator for both intermediate Commit and
// Wrap-Up. When archive is true it behaves as wrap-up: archives the case
// directory, generates the summary, and includes the archived dir in the
// commit. When archive is false it appends an intermediate-commit entry to
// case.json's commits[] and leaves the case open.
func commitToCase(ctx context.Context, r *http.Request, agent agentAdapter, deps commitDeps, req commitRequest, archive bool) (resp map[string]string, statusCode int, errCode string, errMsg string) {
	worktreeName := mux.Vars(r)["worktree"]
	wt, ok := deps.worktreeMgr.GetByName(worktreeName)
	if !ok {
		return nil, http.StatusNotFound, ErrNotFound, "worktree not found"
	}
	if deps.caseMgr == nil {
		return nil, http.StatusServiceUnavailable, ErrInternalError, "case manager not configured"
	}
	if strings.TrimSpace(req.CommitMessage) == "" {
		return nil, http.StatusBadRequest, ErrBadRequest, "commit_message is required"
	}

	// Sanity: never let a user-selected path enter the live cases dir.
	livePrefix := deps.caseMgr.CasesRelDir() + "/"
	for _, f := range req.Files {
		if strings.HasPrefix(f, livePrefix) {
			return nil, http.StatusBadRequest, ErrBadRequest, "cannot stage paths inside the live cases directory: " + f
		}
		if strings.Contains(f, "..") {
			return nil, http.StatusBadRequest, ErrBadRequest, "invalid path: " + f
		}
	}

	// Step 1: load or create the case. Under the new lifecycle, all commits
	// in a worktree bind to the worktree's single open case. We honor an
	// explicit case_id if the client passed one, otherwise we look up the
	// worktree's open case before creating a new one.
	var caseID string
	if req.CaseID != "" {
		if _, err := deps.caseMgr.Get(wt.Path, req.CaseID); err != nil {
			return nil, http.StatusNotFound, ErrNotFound, fmt.Sprintf("case not found: %s", req.CaseID)
		}
		caseID = req.CaseID
	} else if existing, _ := deps.caseMgr.FirstOpenCase(wt.Path); existing != nil {
		caseID = existing.ID
	} else {
		if strings.TrimSpace(req.Title) == "" {
			return nil, http.StatusBadRequest, ErrBadRequest, "title is required when creating a new case"
		}
		kind := req.Kind
		if kind == "" {
			kind = "feature"
		}
		c, err := deps.caseMgr.Create(wt.Path, req.Title, kind, worktreeName, wt.Branch, wt.Commit)
		if err != nil {
			if err == cases.ErrOpenCaseExists {
				return nil, http.StatusConflict, ErrBadRequest, err.Error()
			}
			return nil, http.StatusInternalServerError, ErrInternalError, "create case: " + err.Error()
		}
		caseID = c.ID

		// Attach the active session's transcript to the newborn case.
		if req.SessionID != "" {
			saveActiveTranscript(agent, deps, wt.Path, caseID, req.SessionID, req.Title)
		}
	}

	// Step 2: refresh all transcripts attached to the case. We refresh BOTH
	// agents' transcripts (a wrap-up may be initiated from either agent and
	// the case may contain sessions from the other).
	if c, err := deps.caseMgr.Get(wt.Path, caseID); err == nil {
		refreshAllAttached(deps, wt.Path, c)
	}

	// Step 3: merge links.
	if len(req.Links) > 0 {
		if c, err := deps.caseMgr.Get(wt.Path, caseID); err == nil {
			merged := append([]cases.CaseLink{}, c.Links...)
			merged = append(merged, req.Links...)
			_ = deps.caseMgr.Update(wt.Path, caseID, cases.CaseUpdate{Links: merged})
		}
	}

	// Step 4: save selected traces (wrap-up only — intermediate commits do
	// not bundle traces).
	if archive && len(req.Traces) > 0 && deps.traceMgr != nil {
		for _, traceName := range req.Traces {
			report, err := deps.traceMgr.GetReport(traceName)
			if err != nil {
				continue
			}
			refID := uuid.New().String()[:8]
			_ = deps.caseMgr.SaveTrace(wt.Path, caseID, refID, report)
		}
	}

	// Step 4b: capture related sessions from the *other* agent (wrap-up
	// only).
	if archive {
		for _, rel := range req.RelatedSessions {
			captureRelatedSession(rel, wt.Path, caseID, deps.caseMgr, deps.claudeMgr, deps.codexMgr)
		}
	}

	// Step 5: write the case summary (wrap-up only). Two paths:
	//   - The client sent a user-curated summary in req.Summary — use it
	//     verbatim, normalizing components for chip consistency.
	//   - Otherwise, generate one synchronously with a timeout so it lands
	//     in the same commit as the archived case.
	// The diff is scoped to exactly the files the user picked — we don't
	// want the generator inferring anything from whatever happens to
	// already be in the index.
	if archive {
		var summary *cases.CaseSummary
		if req.Summary != nil && strings.TrimSpace(req.Summary.Synopsis) != "" {
			s := *req.Summary
			s.Components = genai.NormalizeComponents(s.Components)
			if s.GeneratedAt.IsZero() {
				s.GeneratedAt = time.Now()
			}
			summary = &s
		} else if c, err := deps.caseMgr.Get(wt.Path, caseID); err == nil {
			summary, _ = generateCaseSummary(ctx, deps, wt.Path, c, req.Files)
		}
		if summary != nil {
			_ = deps.caseMgr.SetSummary(wt.Path, caseID, summary)
		}
	}

	// Step 6 (wrap-up): archive the case directory before staging. Archive may
	// rename the directory (and the case ID) to dodge a collision with an
	// existing archived case, so adopt the returned ID for the steps below.
	if archive {
		archivedID, err := deps.caseMgr.Archive(wt.Path, caseID)
		if err != nil {
			return nil, http.StatusInternalServerError, ErrInternalError, "archive case: " + err.Error()
		}
		caseID = archivedID
	}

	// Step 7: git add user files + (if wrap-up) the archived case dir.
	gitArgs := []string{"-C", wt.Path, "add", "--"}
	if archive {
		gitArgs = append(gitArgs, filepath.Join(deps.caseMgr.ArchivedRelDir(), caseID))
	}
	for _, f := range req.Files {
		gitArgs = append(gitArgs, f)
	}
	if len(gitArgs) == 4 {
		// Nothing to add. Empty commit is an error case (intermediate
		// commits with zero files are useless). Roll back the archive if we
		// just did one and bail.
		if archive {
			_ = deps.caseMgr.Reopen(wt.Path, caseID)
		}
		return nil, http.StatusBadRequest, ErrBadRequest, "no files selected for commit"
	}
	if _, err := worktree.RunCommand(ctx, gitArgs...); err != nil {
		if archive {
			_ = deps.caseMgr.Reopen(wt.Path, caseID)
		}
		return nil, http.StatusInternalServerError, ErrInternalError, "git add: " + err.Error()
	}

	// Step 8: commit.
	commitOut, err := worktree.RunCommand(ctx, "-C", wt.Path, "commit", "-m", req.CommitMessage)
	if err != nil {
		if archive {
			_ = deps.caseMgr.Reopen(wt.Path, caseID)
		}
		return nil, http.StatusInternalServerError, ErrInternalError, "git commit: " + commitOut + " " + err.Error()
	}

	// Read SHA + short SHA + files-changed for the commit-timeline entry.
	fullSHA, _ := worktree.RunCommand(ctx, "-C", wt.Path, "rev-parse", "HEAD")
	fullSHA = strings.TrimSpace(fullSHA)
	shortSHA, _ := worktree.RunCommand(ctx, "-C", wt.Path, "rev-parse", "--short", "HEAD")
	shortSHA = strings.TrimSpace(shortSHA)

	// Step 9: for intermediate commits, append a CommitEntry to case.json.
	if !archive {
		filesOut, _ := worktree.RunCommand(ctx, "-C", wt.Path, "show", "--name-only", "--pretty=format:", "HEAD")
		var changed []string
		for _, line := range strings.Split(filesOut, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				changed = append(changed, line)
			}
		}
		_ = deps.caseMgr.AppendCommit(wt.Path, caseID, cases.CommitEntry{
			SHA:          fullSHA,
			ShortSHA:     shortSHA,
			CommittedAt:  time.Now(),
			Message:      req.CommitMessage,
			Description:  req.Description,
			FilesChanged: changed,
		})
	}

	// Step 10: trash the active session (wrap-up only — intermediate
	// commits keep the session alive so the user can keep working).
	if archive && req.SessionID != "" {
		agent.trashSession(req.SessionID)
	}

	return map[string]string{
		"case_id":     caseID,
		"commit_hash": shortSHA,
	}, http.StatusOK, "", ""
}

// saveActiveTranscript exports the live session for the active agent and
// attaches it to the case. Best-effort.
func saveActiveTranscript(agent agentAdapter, deps commitDeps, worktreePath, caseID, sessionID, title string) {
	switch agent.name() {
	case "claude":
		if deps.claudeMgr == nil {
			return
		}
		t, err := deps.claudeMgr.ExportSession(sessionID, "full")
		if err != nil {
			return
		}
		refID := uuid.New().String()[:8]
		if title == "" {
			title = t.Source.DisplayName
		}
		_ = deps.caseMgr.SaveTranscript(worktreePath, caseID, refID, title, sessionID, t)
		seedCasePlanFromClaudeSession(deps.caseMgr, deps.claudeMgr, worktreePath, caseID, sessionID)
	case "codex":
		if deps.codexMgr == nil {
			return
		}
		t, err := deps.codexMgr.ExportSession(sessionID, "full")
		if err != nil {
			return
		}
		refID := uuid.New().String()[:8]
		if title == "" {
			title = t.Source.DisplayName
		}
		_ = deps.caseMgr.SaveCodexTranscript(worktreePath, caseID, refID, title, sessionID, t)
	}
}

// refreshAllAttached re-exports every transcript (Claude AND Codex) attached
// to the case from its live source session. Sessions that have been deleted
// are silently skipped — the previously-saved snapshot stays in place.
func refreshAllAttached(deps commitDeps, worktreePath string, c *cases.CaseJSON) {
	if deps.claudeMgr != nil {
		for _, ref := range c.Claude {
			if ref.SourceSessionID == "" {
				continue
			}
			t, err := deps.claudeMgr.ExportSession(ref.SourceSessionID, "full")
			if err != nil {
				continue
			}
			_ = deps.caseMgr.UpdateTranscript(worktreePath, c.ID, ref.ID, t)
		}
	}
	if deps.codexMgr != nil {
		for _, ref := range c.Codex {
			if ref.SourceSessionID == "" {
				continue
			}
			t, err := deps.codexMgr.ExportSession(ref.SourceSessionID, "full")
			if err != nil {
				continue
			}
			_ = deps.caseMgr.UpdateCodexTranscript(worktreePath, c.ID, ref.ID, t)
		}
	}
}

// generateCaseSummary collects context from the case and calls genai. Returns
// nil on error or timeout — the caller treats absence as acceptable.
//
// `files` is the set of paths the user picked for the wrap-up commit. We diff
// exactly those files (not whatever happens to be staged) so the model sees
// the work that's about to be committed, nothing more, nothing less.
func generateCaseSummary(parent context.Context, deps commitDeps, worktreePath string, c *cases.CaseJSON, files []string) (*cases.CaseSummary, error) {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	notes, _ := deps.caseMgr.GetNotes(worktreePath, c.ID)
	diff := truncateDiff(diffForFiles(ctx, worktreePath, files), 16000)

	var transcriptSummaries []string
	for _, ref := range c.Claude {
		if ref.Preview != "" {
			transcriptSummaries = append(transcriptSummaries, "claude: "+ref.Title+" — "+ref.Preview)
		}
	}
	for _, ref := range c.Codex {
		if ref.Preview != "" {
			transcriptSummaries = append(transcriptSummaries, "codex: "+ref.Title+" — "+ref.Preview)
		}
	}

	var commitDescriptions []string
	for _, ce := range c.Commits {
		if strings.TrimSpace(ce.Description) != "" {
			commitDescriptions = append(commitDescriptions, ce.Description)
		}
	}

	traceSummaries := traceSummariesForCase(deps, worktreePath, c.ID)

	gen, err := genai.GenerateCaseSummary(ctx, genai.SummaryInput{
		CaseTitle:           c.Title,
		CaseKind:            c.Kind,
		CaseStatus:          c.Status,
		Notes:               notes,
		WrapUpDiff:          diff,
		TranscriptSummaries: transcriptSummaries,
		CommitDescriptions:  commitDescriptions,
		TraceSummaries:      traceSummaries,
		ChangedPaths:        changedPathsForCase(ctx, deps, worktreePath, c, files),
		Cwd:                 worktreePath,
	})
	if err != nil {
		return nil, err
	}
	return &cases.CaseSummary{
		Synopsis:    gen.Synopsis,
		Symptoms:    gen.Symptoms,
		RootCause:   gen.RootCause,
		Resolution:  gen.Resolution,
		Components:  gen.Components,
		GeneratedAt: gen.GeneratedAt,
		Model:       gen.Model,
	}, nil
}

// regenerateSummaryForCase rebuilds the case summary from whatever state is
// available (notes, commit descriptions, attached transcripts, linked
// traces). Used by RegenerateSummary on either open or archived cases.
// Unlike generateCaseSummary, this path does NOT include a staged diff —
// regeneration runs against the case's stored history, not the current
// working tree.
func regenerateSummaryForCase(parent context.Context, deps commitDeps, worktreePath string, c *cases.CaseJSON) (*cases.CaseSummary, error) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()

	notes, _ := deps.caseMgr.GetNotes(worktreePath, c.ID)

	var transcriptSummaries []string
	for _, ref := range c.Claude {
		if ref.Preview != "" {
			transcriptSummaries = append(transcriptSummaries, "claude: "+ref.Title+" — "+ref.Preview)
		}
	}
	for _, ref := range c.Codex {
		if ref.Preview != "" {
			transcriptSummaries = append(transcriptSummaries, "codex: "+ref.Title+" — "+ref.Preview)
		}
	}

	var commitDescriptions []string
	for _, ce := range c.Commits {
		if strings.TrimSpace(ce.Description) != "" {
			commitDescriptions = append(commitDescriptions, ce.Description)
		}
		if strings.TrimSpace(ce.Message) != "" {
			commitDescriptions = append(commitDescriptions, ce.Message)
		}
	}

	traceSummaries := traceSummariesForCase(deps, worktreePath, c.ID)

	gen, err := genai.GenerateCaseSummary(ctx, genai.SummaryInput{
		CaseTitle:           c.Title,
		CaseKind:            c.Kind,
		CaseStatus:          c.Status,
		Notes:               notes,
		WrapUpDiff:          "",
		TranscriptSummaries: transcriptSummaries,
		CommitDescriptions:  commitDescriptions,
		TraceSummaries:      traceSummaries,
		ChangedPaths:        changedPathsForCase(ctx, deps, worktreePath, c, nil),
		Cwd:                 worktreePath,
	})
	if err != nil {
		return nil, err
	}
	return &cases.CaseSummary{
		Synopsis:    gen.Synopsis,
		Symptoms:    gen.Symptoms,
		RootCause:   gen.RootCause,
		Resolution:  gen.Resolution,
		Components:  gen.Components,
		GeneratedAt: gen.GeneratedAt,
		Model:       gen.Model,
	}, nil
}

// changedPathsForCase gathers repo-relative paths a case touched, for
// deterministic component derivation (see genai.DeriveComponents). `extra` is
// any caller-supplied set (e.g. the wrap-up commit's selected files); the
// case's recorded intermediate commits are unioned in. When no `extra` is
// supplied (the regenerate path) the wrap-up commit — which isn't recorded in
// c.Commits — is located from git history and added too, so archived cases
// still yield components.
func changedPathsForCase(ctx context.Context, deps commitDeps, worktreePath string, c *cases.CaseJSON, extra []string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, p := range extra {
		add(p)
	}
	for _, ce := range c.Commits {
		for _, p := range ce.FilesChanged {
			add(p)
		}
	}
	if len(extra) == 0 && deps.caseMgr != nil && c.ID != "" {
		if wu := FindWrapUpCommit(ctx, worktreePath, deps.caseMgr.ArchivedRelDir(), c.ID); wu != nil {
			for _, p := range wu.FilesChanged {
				add(p)
			}
		}
	}
	return out
}

func traceSummariesForCase(deps commitDeps, worktreePath, caseID string) []string {
	if deps.caseMgr == nil {
		return nil
	}
	traces, err := deps.caseMgr.ListTraces(worktreePath, caseID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(traces))
	for _, t := range traces {
		out = append(out, fmt.Sprintf("%s (%s, %d entries)", t.Name, t.Group, t.EntryCount))
	}
	return out
}

// generateSummaryHTTP previews the case summary the wrap-up would generate.
// Used by the wrap-up modal so the user can review and prune the components
// before committing. The result is NOT persisted — the client
// is expected to send the (possibly edited) summary back with the wrap-up
// request, which writes it to case.json.
//
// Supports three resolution paths:
//   - body.case_id given → use that case (typical case detail wrap-up).
//   - no case_id but an open case exists → use that case.
//   - no case at all → synthesize an in-memory case from body.title /
//     body.kind. This covers the "Wrap Up creates a new case" flow where
//     the case directory doesn't exist on disk yet; the active session's
//     recent prompts are fed in as the only history the model has to work
//     from.
func generateSummaryHTTP(w http.ResponseWriter, r *http.Request, agent agentAdapter, deps commitDeps) {
	worktreeName := mux.Vars(r)["worktree"]
	wt, ok := deps.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}
	if deps.caseMgr == nil {
		WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "case manager not configured")
		return
	}

	var body struct {
		CaseID    string   `json:"case_id"`
		Files     []string `json:"files"`
		Title     string   `json:"title"`      // for the synthesize-new-case path
		Kind      string   `json:"kind"`       // for the synthesize-new-case path
		SessionID string   `json:"session_id"` // for the synthesize-new-case path
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Resolve the case: explicit ID, then worktree's open case, then a
	// synthetic in-memory case from title/kind. The third path only
	// activates when the user is wrapping up into a brand-new case.
	var c *cases.CaseJSON
	var synthetic bool
	if body.CaseID != "" {
		got, err := deps.caseMgr.Get(wt.Path, body.CaseID)
		if err != nil {
			WriteError(w, http.StatusNotFound, ErrNotFound, "case not found")
			return
		}
		c = got
	} else if open, _ := deps.caseMgr.FirstOpenCase(wt.Path); open != nil {
		c = open
	} else {
		title := strings.TrimSpace(body.Title)
		if title == "" {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "no open case to summarize and no title provided")
			return
		}
		kind := body.Kind
		if kind == "" {
			kind = "feature"
		}
		c = &cases.CaseJSON{
			Title:  title,
			Kind:   kind,
			Status: "open",
		}
		synthetic = true
	}

	// For the synthetic-case path the model has very thin context (no
	// case.json yet, no notes, no transcripts attached to a case, no
	// commit history). Inject the active session's recent prompts as
	// fake transcript previews on whichever agent's slot the session
	// belongs to, so the prompt's "claude:" / "codex:" labels remain
	// honest.
	if synthetic && body.SessionID != "" {
		for _, m := range agent.recentUserMessages(body.SessionID, 6) {
			switch agent.name() {
			case "codex":
				c.Codex = append(c.Codex, cases.CaseCodexRef{
					Title:   "active codex session",
					Preview: m,
				})
			default:
				c.Claude = append(c.Claude, cases.CaseClaudeRef{
					Title:   "active claude session",
					Preview: m,
				})
			}
		}
	}

	// Reuse the same context-collection logic as the wrap-up path. We pass
	// req.Files-equivalent for the diff scope so the preview describes the
	// same code that would actually be committed.
	summary, err := generateCaseSummary(r.Context(), deps, wt.Path, c, body.Files)
	if err != nil {
		WriteError(w, http.StatusBadGateway, ErrInternalError, "generate: "+err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, summary)
}

// WrapUpCommitInfo is the git data we surface on the case detail page for an
// archived case. The wrap-up commit is intentionally NOT recorded in
// case.json's commits[]; it's located on demand from git history as "the
// commit that added <archivedRelDir>/<caseID>/case.json".
type WrapUpCommitInfo struct {
	SHA          string
	ShortSHA     string
	CommittedAt  time.Time
	Message      string
	FilesChanged []string
}

// FindWrapUpCommit looks up the wrap-up commit for an archived case by
// finding the commit that introduced the archived case.json into git
// history. Searches across all refs so a case that's already been merged to
// main is still found. Returns nil (no error) when the commit can't be
// located — e.g. the case was archived manually without a wrap-up commit.
func FindWrapUpCommit(ctx context.Context, worktreePath, archivedRelDir, caseID string) *WrapUpCommitInfo {
	path := filepath.ToSlash(filepath.Join(archivedRelDir, caseID, "case.json"))

	// `--diff-filter=A` restricts to commits that ADDED the file. With the
	// live cases dir never being committed, the only commit that adds the
	// archived case.json is the wrap-up commit. `--all` scans every ref so
	// post-merge cases on main are still found from a feature branch's
	// worktree (and vice versa).
	out, err := worktree.RunCommand(ctx, "-C", worktreePath, "log", "-n", "1",
		"--diff-filter=A", "--all", "--pretty=format:%H", "--", path)
	if err != nil {
		return nil
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return nil
	}

	// Fetch SHA, short SHA, ISO commit date, and full subject+body in one
	// shot. Use a unit-separator so the body (which may contain newlines)
	// stays intact.
	meta, err := worktree.RunCommand(ctx, "-C", worktreePath, "show", "--no-patch",
		"--pretty=format:%H%x1f%h%x1f%cI%x1f%B", sha)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(meta, "\x1f", 4)
	if len(parts) < 4 {
		return nil
	}
	commitTime, _ := time.Parse(time.RFC3339, strings.TrimSpace(parts[2]))

	filesOut, _ := worktree.RunCommand(ctx, "-C", worktreePath, "show",
		"--name-only", "--pretty=format:", sha)
	var files []string
	for _, line := range strings.Split(filesOut, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}

	return &WrapUpCommitInfo{
		SHA:          strings.TrimSpace(parts[0]),
		ShortSHA:     strings.TrimSpace(parts[1]),
		CommittedAt:  commitTime,
		Message:      strings.TrimRight(parts[3], "\n"),
		FilesChanged: files,
	}
}

// diffForFiles builds a unified diff scoped to the supplied paths. For
// tracked paths it uses `git diff HEAD -- <path>`; for untracked paths it
// synthesizes a "new file" diff against /dev/null. This is what the
// commit-message / case-summary generators want: the diff reflects exactly
// the files the user picked, regardless of what is or isn't currently in
// the index. Returns "" when files is empty.
func diffForFiles(ctx context.Context, worktreePath string, files []string) string {
	if len(files) == 0 {
		return ""
	}

	var tracked, untracked []string
	for _, f := range files {
		if strings.TrimSpace(f) == "" || strings.Contains(f, "..") {
			continue
		}
		if _, err := worktree.RunCommand(ctx, "-C", worktreePath, "ls-files", "--error-unmatch", "--", f); err == nil {
			tracked = append(tracked, f)
		} else {
			untracked = append(untracked, f)
		}
	}

	var b strings.Builder
	if len(tracked) > 0 {
		args := append([]string{"-C", worktreePath, "diff", "HEAD", "--"}, tracked...)
		if d, err := worktree.RunCommand(ctx, args...); err == nil && d != "" {
			b.WriteString(d)
		}
	}
	// `git diff --no-index` exits with status 1 when the files differ, which
	// worktree.RunCommand reports as an error and turns the stdout into
	// stderr. We need stdout regardless, so run it directly here.
	for _, f := range untracked {
		cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "diff", "--no-index", "--", "/dev/null", f)
		out, _ := cmd.Output()
		if len(out) > 0 {
			b.Write(out)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// truncateDiff caps the diff at maxBytes, prefixing with file headers so the
// model still sees the surface area even when individual hunks are cut.
func truncateDiff(diff string, maxBytes int) string {
	if len(diff) <= maxBytes {
		return diff
	}
	// Keep file headers (`diff --git`) plus as much body as fits.
	lines := strings.Split(diff, "\n")
	var headers []string
	for _, ln := range lines {
		if strings.HasPrefix(ln, "diff --git") {
			headers = append(headers, ln)
		}
	}
	header := strings.Join(headers, "\n")
	if len(header) >= maxBytes {
		return header[:maxBytes] + "\n...(truncated)"
	}
	remaining := maxBytes - len(header) - len("\n...(truncated)\n\n")
	if remaining < 0 {
		remaining = 0
	}
	return header + "\n\n" + diff[:remaining] + "\n...(truncated)"
}

// generateCommitMessageHTTP handles POST /generate-commit-message. Shared by
// the Claude and Codex routes. The agent is selected by the URL prefix; the
// handler factories below pre-bind that.
func generateCommitMessageHTTP(w http.ResponseWriter, r *http.Request, agent agentAdapter, deps commitDeps) {
	worktreeName := mux.Vars(r)["worktree"]
	wt, ok := deps.worktreeMgr.GetByName(worktreeName)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "worktree not found")
		return
	}
	if deps.caseMgr == nil {
		WriteError(w, http.StatusServiceUnavailable, ErrInternalError, "case manager not configured")
		return
	}

	var body struct {
		SessionID string   `json:"session_id"`
		CaseID    string   `json:"case_id"`
		Title     string   `json:"title"`
		Kind      string   `json:"kind"`
		Files     []string `json:"files"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Resolve case context: explicit case_id first, then worktree's open
	// case, then the title/kind passed by the client for a not-yet-created
	// case.
	caseTitle := body.Title
	caseKind := body.Kind
	notes := ""
	if body.CaseID != "" {
		if c, err := deps.caseMgr.Get(wt.Path, body.CaseID); err == nil {
			caseTitle = c.Title
			caseKind = c.Kind
		}
	} else if c, _ := deps.caseMgr.FirstOpenCase(wt.Path); c != nil {
		caseTitle = c.Title
		caseKind = c.Kind
		notes, _ = deps.caseMgr.GetNotes(wt.Path, c.ID)
	}

	// Diff exactly the files the user has checked in the modal. We must NOT
	// fall back to `git diff --staged` or `git diff HEAD` — the staging
	// area can hold unrelated changes from prior workflows, and HEAD-vs-WT
	// includes files the user unchecked. Either would lead to a commit
	// message that describes the wrong work.
	if len(body.Files) == 0 {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "files is required: select at least one file to describe")
		return
	}
	diff := truncateDiff(diffForFiles(ctx, wt.Path, body.Files), 16000)
	if strings.TrimSpace(diff) == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "no diff to describe for the selected files")
		return
	}

	recent := agent.recentUserMessages(body.SessionID, 5)

	out, model, err := genai.GenerateCommitMessage(ctx, genai.CommitMessageInput{
		StagedDiff:     diff,
		CaseTitle:      caseTitle,
		CaseKind:       caseKind,
		Notes:          notes,
		RecentMessages: recent,
		Cwd:            wt.Path,
	})
	if err != nil {
		WriteError(w, http.StatusBadGateway, ErrInternalError, "generate: "+err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{
		"message":     out.Message,
		"description": out.Description,
		"model":       model,
	})
}
