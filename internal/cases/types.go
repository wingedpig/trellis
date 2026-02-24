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
	Schema    string         `json:"schema"`
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Kind      string         `json:"kind"`                // "bug", "feature", "investigation", "task"
	Status    string         `json:"status"`              // "open", "resolved", "wontfix"
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Worktree  CaseWorktree   `json:"worktree"`
	Links     []CaseLink     `json:"links,omitempty"`
	Evidence  []CaseEvidence `json:"evidence,omitempty"`
	Claude    []CaseClaudeRef `json:"claude,omitempty"`
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
	Title  *string     `json:"title,omitempty"`
	Kind   *string     `json:"kind,omitempty"`
	Status *string     `json:"status,omitempty"`
	Links  []CaseLink  `json:"links,omitempty"`
	Notes  *string     `json:"notes,omitempty"` // If set, overwrites notes.md
}
