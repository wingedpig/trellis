// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package cases manages durable case objects within Trellis worktrees.
package cases

import (
	"time"
)

// CaseSchema is the schema identifier for case.json files.
const CaseSchema = "trellis.case.v1"

// CaseJSON is the full manifest stored as case.json in each case directory.
type CaseJSON struct {
	Schema    string          `json:"schema"`
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Kind      string          `json:"kind"`                // "bug", "feature", "investigation", "task"
	Status    string          `json:"status"`              // "open", "resolved", "wontfix"
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Worktree  CaseWorktree    `json:"worktree"`
	Links     []CaseLink      `json:"links,omitempty"`
	Evidence  []CaseEvidence  `json:"evidence,omitempty"`
	Claude    []CaseClaudeRef `json:"claude,omitempty"`
	Codex     []CaseCodexRef  `json:"codex,omitempty"`
	// Commits records intermediate (non-wrap-up) commits made against this case
	// during its active life. The wrap-up commit is intentionally not present
	// here — it is locatable from git history.
	Commits []CommitEntry `json:"commits,omitempty"`
	// Summary is a generated, structured summary written at wrap-up. Nil until
	// generated; may be edited or regenerated later from the case detail page.
	Summary *CaseSummary `json:"summary,omitempty"`
}

// CommitEntry is one intermediate commit made against the case.
type CommitEntry struct {
	SHA          string    `json:"sha"`
	ShortSHA     string    `json:"short_sha"`
	CommittedAt  time.Time `json:"committed_at"`
	Message      string    `json:"message"`     // full commit message
	Description  string    `json:"description"` // per-commit narrative beat
	FilesChanged []string  `json:"files_changed,omitempty"`
}

// CaseSummary is the structured generated summary attached to a case at wrap-up.
type CaseSummary struct {
	Synopsis    string    `json:"synopsis"`
	Symptoms    string    `json:"symptoms"`
	RootCause   string    `json:"root_cause"`
	Resolution  string    `json:"resolution"`
	Components  []string  `json:"components,omitempty"`
	GeneratedAt time.Time `json:"generated_at"`
	Model       string    `json:"model,omitempty"`
}

// CaseWorktree records which worktree/branch the case was created in.
type CaseWorktree struct {
	Name       string `json:"name"`
	Branch     string `json:"branch"`
	BaseCommit string `json:"base_commit,omitempty"`
}

// CaseLink is a reference URL associated with a case.
type CaseLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// CaseEvidence is a snapshot file attached to a case.
type CaseEvidence struct {
	Title    string   `json:"title"`
	Filename string   `json:"filename"`
	Format   string   `json:"format"` // "png", "txt", "log", "json", etc.
	Tags     []string `json:"tags,omitempty"`
	AddedAt  time.Time `json:"added_at"`
}

// CaseClaudeRef references a Claude transcript stored in the case.
type CaseClaudeRef struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Filename        string    `json:"filename"`
	ExportedAt      time.Time `json:"exported_at"`
	MessageCount    int       `json:"message_count"`
	Preview         string    `json:"preview,omitempty"`
	SourceSessionID string    `json:"source_session_id,omitempty"`
	// CurrentMessageCount is populated at render time, not persisted.
	// 0 = not checked, -1 = session deleted, >0 = live message count.
	CurrentMessageCount int `json:"-"`
}

// CaseCodexRef references a Codex transcript stored in the case.
// Symmetric to CaseClaudeRef; codex transcripts live under codex_transcripts/
// to avoid filename collisions with claude transcripts in the same case.
type CaseCodexRef struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Filename        string    `json:"filename"`
	ExportedAt      time.Time `json:"exported_at"`
	MessageCount    int       `json:"message_count"`
	Preview         string    `json:"preview,omitempty"`
	SourceSessionID string    `json:"source_session_id,omitempty"`
	// CurrentMessageCount is populated at render time, not persisted.
	// 0 = not checked, -1 = session deleted, >0 = live message count.
	CurrentMessageCount int `json:"-"`
}

// CaseTraceSummary is a lightweight summary of a saved trace report, used in list views.
type CaseTraceSummary struct {
	ID         string    `json:"id"`
	Filename   string    `json:"filename"`
	Name       string    `json:"name"`
	TraceID    string    `json:"trace_id"`
	Group      string    `json:"group"`
	EntryCount int       `json:"entry_count"`
	SavedAt    time.Time `json:"saved_at"`
}

// CaseExportRef is a lightweight reference for export/import scenarios.
type CaseExportRef struct {
	CaseID    string `json:"case_id"`
	Worktree  string `json:"worktree"`
	Title     string `json:"title"`
}

// CaseInfo is a lightweight summary used in list views.
type CaseInfo struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Worktree  string    `json:"worktree"` // worktree name only
}

// CaseUpdate holds partial update fields. Nil/empty values are not applied.
type CaseUpdate struct {
	Title  *string    `json:"title,omitempty"`
	Kind   *string    `json:"kind,omitempty"`
	Status *string    `json:"status,omitempty"`
	Links  []CaseLink `json:"links,omitempty"`
	Notes  *string    `json:"notes,omitempty"` // If set, overwrites notes.md
	Plan   *string    `json:"plan,omitempty"`  // If set, overwrites plan.md
}

// SummaryUpdate holds per-field summary edits. Nil values are not applied;
// empty-string values clear the corresponding field.
type SummaryUpdate struct {
	Synopsis   *string  `json:"synopsis,omitempty"`
	Symptoms   *string  `json:"symptoms,omitempty"`
	RootCause  *string  `json:"root_cause,omitempty"`
	Resolution *string  `json:"resolution,omitempty"`
	Components []string `json:"components,omitempty"`
}
