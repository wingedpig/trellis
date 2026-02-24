// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package cases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/trace"
)

// Manager manages case objects. It is stateless — all operations take a worktreePath.
type Manager struct {
	casesRelDir string // Relative directory within worktree (e.g., "trellis/cases")
}

// NewManager creates a new case manager with the given relative directory.
func NewManager(casesRelDir string) *Manager {
	return &Manager{casesRelDir: casesRelDir}
}

// CasesRelDir returns the relative directory within a worktree for open cases.
func (m *Manager) CasesRelDir() string {
	return m.casesRelDir
}

// ArchivedRelDir returns the relative directory within a worktree for archived cases.
func (m *Manager) ArchivedRelDir() string {
	return m.casesRelDir + "-archived"
}

// FindCaseBySession scans open cases for one with a transcript linked to the given session.
func (m *Manager) FindCaseBySession(worktreePath, sessionID string) *CaseJSON {
	dir := m.casesDir(worktreePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "case.json")
		c, err := loadCase(path)
		if err != nil {
			continue
		}
		for _, ref := range c.Claude {
			if ref.SourceSessionID == sessionID {
				return c
			}
		}
	}
	return nil
}

// casesDir returns the absolute path to the cases directory for a worktree.
func (m *Manager) casesDir(worktreePath string) string {
	return filepath.Join(worktreePath, m.casesRelDir)
}

// archivedDir returns the absolute path to the archived cases directory for a worktree.
func (m *Manager) archivedDir(worktreePath string) string {
	return filepath.Join(worktreePath, m.casesRelDir+"-archived")
}

// List returns open cases sorted by created_at descending.
func (m *Manager) List(worktreePath string) ([]CaseInfo, error) {
	cases, err := scanCases(m.casesDir(worktreePath))
	if err != nil {
		return nil, err
	}
	sort.Slice(cases, func(i, j int) bool {
		return cases[i].CreatedAt.After(cases[j].CreatedAt)
	})
	return cases, nil
}

// ListArchived returns archived cases sorted by created_at descending.
func (m *Manager) ListArchived(worktreePath string) ([]CaseInfo, error) {
	cases, err := scanCases(m.archivedDir(worktreePath))
	if err != nil {
		return nil, err
	}
	sort.Slice(cases, func(i, j int) bool {
		return cases[i].CreatedAt.After(cases[j].CreatedAt)
	})
	return cases, nil
}

// Get returns the full case manifest.
func (m *Manager) Get(worktreePath, caseID string) (*CaseJSON, error) {
	path := filepath.Join(m.casesDir(worktreePath), caseID, "case.json")
	c, err := loadCase(path)
	if err != nil {
		// Also check archived
		archivedPath := filepath.Join(m.archivedDir(worktreePath), caseID, "case.json")
		c, err2 := loadCase(archivedPath)
		if err2 != nil {
			return nil, fmt.Errorf("case not found: %s", caseID)
		}
		return c, nil
	}
	return c, nil
}

// GetNotes reads the notes.md file for a case.
func (m *Manager) GetNotes(worktreePath, caseID string) (string, error) {
	path := filepath.Join(m.casesDir(worktreePath), caseID, "notes.md")
	data, err := os.ReadFile(path)
	if err != nil {
		// Check archived
		path = filepath.Join(m.archivedDir(worktreePath), caseID, "notes.md")
		data, err = os.ReadFile(path)
		if err != nil {
			return "", nil // No notes yet
		}
	}
	return string(data), nil
}

// Create creates a new case directory with case.json and notes.md.
func (m *Manager) Create(worktreePath, title, kind, worktreeName, branch, baseCommit string) (*CaseJSON, error) {
	now := time.Now()
	datePrefix := now.Format("2006-01-02")
	slug := config.Slugify(title)
	if slug == "" {
		slug = "untitled"
	}
	id := datePrefix + "__" + slug

	// Handle collisions
	dir := m.casesDir(worktreePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cases dir: %w", err)
	}

	candidateID := id
	for suffix := 2; ; suffix++ {
		casePath := filepath.Join(dir, candidateID)
		if _, err := os.Stat(casePath); os.IsNotExist(err) {
			break
		}
		candidateID = id + "-" + strconv.Itoa(suffix)
	}
	id = candidateID

	caseDir := filepath.Join(dir, id)
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create case dir: %w", err)
	}

	c := &CaseJSON{
		Schema:    CaseSchema,
		ID:        id,
		Title:     title,
		Kind:      kind,
		Status:    "open",
		CreatedAt: now,
		UpdatedAt: now,
		Worktree: CaseWorktree{
			Name:       worktreeName,
			Branch:     branch,
			BaseCommit: baseCommit,
		},
	}

	if err := saveCase(filepath.Join(caseDir, "case.json"), c); err != nil {
		return nil, err
	}

	// Create initial notes.md
	notes := fmt.Sprintf("# %s\n\n", title)
	if err := os.WriteFile(filepath.Join(caseDir, "notes.md"), []byte(notes), 0o644); err != nil {
		return nil, fmt.Errorf("create notes.md: %w", err)
	}

	return c, nil
}

// Update applies partial updates to a case.
func (m *Manager) Update(worktreePath, caseID string, updates CaseUpdate) error {
	path := filepath.Join(m.casesDir(worktreePath), caseID, "case.json")
	c, err := loadCase(path)
	if err != nil {
		return fmt.Errorf("case not found: %s", caseID)
	}

	if updates.Title != nil {
		c.Title = *updates.Title
	}
	if updates.Kind != nil {
		c.Kind = *updates.Kind
	}
	if updates.Status != nil {
		c.Status = *updates.Status
	}
	if updates.Links != nil {
		c.Links = updates.Links
	}

	if err := saveCase(path, c); err != nil {
		return err
	}

	// Update notes if provided
	if updates.Notes != nil {
		notesPath := filepath.Join(m.casesDir(worktreePath), caseID, "notes.md")
		if err := os.WriteFile(notesPath, []byte(*updates.Notes), 0o644); err != nil {
			return fmt.Errorf("write notes.md: %w", err)
		}
	}

	return nil
}

// Archive moves a case from the active cases directory to the archived directory.
func (m *Manager) Archive(worktreePath, caseID string) error {
	src := filepath.Join(m.casesDir(worktreePath), caseID)
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return fmt.Errorf("case not found: %s", caseID)
	}

	archDir := m.archivedDir(worktreePath)
	if err := os.MkdirAll(archDir, 0o755); err != nil {
		return fmt.Errorf("create archived dir: %w", err)
	}

	dst := filepath.Join(archDir, caseID)
	return os.Rename(src, dst)
}

// Reopen moves a case from the archived directory back to the active cases directory.
func (m *Manager) Reopen(worktreePath, caseID string) error {
	src := filepath.Join(m.archivedDir(worktreePath), caseID)
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return fmt.Errorf("archived case not found: %s", caseID)
	}

	casesDir := m.casesDir(worktreePath)
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		return fmt.Errorf("create cases dir: %w", err)
	}

	dst := filepath.Join(casesDir, caseID)
	return os.Rename(src, dst)
}

// Delete permanently removes a case directory.
func (m *Manager) Delete(worktreePath, caseID string) error {
	// Try active first
	path := filepath.Join(m.casesDir(worktreePath), caseID)
	if _, err := os.Stat(path); err == nil {
		return os.RemoveAll(path)
	}
	// Try archived
	path = filepath.Join(m.archivedDir(worktreePath), caseID)
	if _, err := os.Stat(path); err == nil {
		return os.RemoveAll(path)
	}
	return fmt.Errorf("case not found: %s", caseID)
}

// AttachEvidence adds an evidence file to a case.
func (m *Manager) AttachEvidence(worktreePath, caseID string, ev CaseEvidence, data []byte) error {
	caseDir := filepath.Join(m.casesDir(worktreePath), caseID)
	casePath := filepath.Join(caseDir, "case.json")
	c, err := loadCase(casePath)
	if err != nil {
		return fmt.Errorf("case not found: %s", caseID)
	}

	// Create evidence directory
	evidenceDir := filepath.Join(caseDir, "evidence")
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return fmt.Errorf("create evidence dir: %w", err)
	}

	// Write evidence file
	filePath := filepath.Join(evidenceDir, ev.Filename)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return fmt.Errorf("write evidence file: %w", err)
	}

	// Update case.json
	ev.AddedAt = time.Now()
	c.Evidence = append(c.Evidence, ev)
	return saveCase(casePath, c)
}

// SaveTranscript writes a Claude transcript to the case and updates case.json.
// Uses the v2 split format: JSONL messages + JSON metadata sidecar.
func (m *Manager) SaveTranscript(worktreePath, caseID, claudeRefID, title, sourceSessionID string, transcript *claude.Transcript) error {
	caseDir := filepath.Join(m.casesDir(worktreePath), caseID)
	casePath := filepath.Join(caseDir, "case.json")
	c, err := loadCase(casePath)
	if err != nil {
		return fmt.Errorf("case not found: %s", caseID)
	}

	// Write split transcript files
	transcriptsDir := filepath.Join(caseDir, "transcripts")
	jsonlPath := filepath.Join(transcriptsDir, claudeRefID+".jsonl")
	metaPath := filepath.Join(transcriptsDir, claudeRefID+".json")
	if err := claude.WriteTranscriptSplit(jsonlPath, metaPath, transcript); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}

	// Update case.json
	ref := CaseClaudeRef{
		ID:              claudeRefID,
		Title:           title,
		Filename:        claudeRefID + ".jsonl",
		ExportedAt:      time.Now(),
		MessageCount:    transcript.Stats.MessageCount,
		Preview:         claude.FirstUserPreview(transcript.Messages, 200),
		SourceSessionID: sourceSessionID,
	}
	c.Claude = append(c.Claude, ref)
	return saveCase(casePath, c)
}

// UpdateTranscript overwrites an existing transcript in the case with fresh data.
// If the existing transcript is v1 format (.json), it upgrades to v2 split format.
func (m *Manager) UpdateTranscript(worktreePath, caseID, refID string, transcript *claude.Transcript) error {
	caseDir := filepath.Join(m.casesDir(worktreePath), caseID)
	casePath := filepath.Join(caseDir, "case.json")
	c, err := loadCase(casePath)
	if err != nil {
		return fmt.Errorf("case not found: %s", caseID)
	}

	// Find the ref
	found := false
	for i := range c.Claude {
		if c.Claude[i].ID == refID {
			transcriptsDir := filepath.Join(caseDir, "transcripts")
			jsonlPath := filepath.Join(transcriptsDir, refID+".jsonl")
			metaPath := filepath.Join(transcriptsDir, refID+".json")

			// If upgrading from v1 (.json monolith), the old .json will be
			// overwritten by the new metadata sidecar at the same path.
			if err := claude.WriteTranscriptSplit(jsonlPath, metaPath, transcript); err != nil {
				return fmt.Errorf("write transcript: %w", err)
			}

			// Update ref metadata; upgrade filename to .jsonl
			c.Claude[i].Filename = refID + ".jsonl"
			c.Claude[i].ExportedAt = time.Now()
			c.Claude[i].MessageCount = transcript.Stats.MessageCount
			c.Claude[i].Preview = claude.FirstUserPreview(transcript.Messages, 200)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("transcript ref not found: %s", refID)
	}

	return saveCase(casePath, c)
}

// SaveTrace writes a full trace report as a standalone JSON file in the traces/ subdirectory.
func (m *Manager) SaveTrace(worktreePath, caseID, traceRefID string, report *trace.TraceReport) error {
	caseDir := filepath.Join(m.casesDir(worktreePath), caseID)
	casePath := filepath.Join(caseDir, "case.json")
	if _, err := loadCase(casePath); err != nil {
		return fmt.Errorf("case not found: %s", caseID)
	}

	tracesDir := filepath.Join(caseDir, "traces")
	if err := os.MkdirAll(tracesDir, 0o755); err != nil {
		return fmt.Errorf("create traces dir: %w", err)
	}

	filename := traceRefID + ".json"
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal trace report: %w", err)
	}
	filePath := filepath.Join(tracesDir, filename)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return fmt.Errorf("write trace report: %w", err)
	}

	return nil
}

// ListTraces scans the traces/ subdirectory and returns lightweight summaries.
func (m *Manager) ListTraces(worktreePath, caseID string) ([]CaseTraceSummary, error) {
	tracesDir := m.tracesDir(worktreePath, caseID)
	if tracesDir == "" {
		return nil, nil
	}

	matches, err := filepath.Glob(filepath.Join(tracesDir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("glob traces: %w", err)
	}

	var summaries []CaseTraceSummary
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			continue
		}
		var report trace.TraceReport
		if err := json.Unmarshal(data, &report); err != nil {
			continue
		}
		base := filepath.Base(match)
		id := base[:len(base)-len(".json")]
		summaries = append(summaries, CaseTraceSummary{
			ID:         id,
			Filename:   base,
			Name:       report.Name,
			TraceID:    report.TraceID,
			Group:      report.Group,
			EntryCount: report.Summary.TotalEntries,
			SavedAt:    report.CreatedAt,
		})
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].SavedAt.After(summaries[j].SavedAt)
	})

	return summaries, nil
}

// GetTrace reads a full trace report from the traces/ subdirectory.
func (m *Manager) GetTrace(worktreePath, caseID, traceRefID string) (*trace.TraceReport, error) {
	tracesDir := m.tracesDir(worktreePath, caseID)
	if tracesDir == "" {
		return nil, fmt.Errorf("trace not found: %s", traceRefID)
	}

	filePath := filepath.Join(tracesDir, traceRefID+".json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("trace not found: %s", traceRefID)
	}

	var report trace.TraceReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse trace report: %w", err)
	}
	return &report, nil
}

// DeleteTrace removes a trace report file from the traces/ subdirectory.
func (m *Manager) DeleteTrace(worktreePath, caseID, traceRefID string) error {
	tracesDir := m.tracesDir(worktreePath, caseID)
	if tracesDir == "" {
		return fmt.Errorf("trace not found: %s", traceRefID)
	}

	filePath := filepath.Join(tracesDir, traceRefID+".json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("trace not found: %s", traceRefID)
	}
	return os.Remove(filePath)
}

// tracesDir returns the traces directory for a case, checking active then archived.
func (m *Manager) tracesDir(worktreePath, caseID string) string {
	dir := filepath.Join(m.casesDir(worktreePath), caseID, "traces")
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	dir = filepath.Join(m.archivedDir(worktreePath), caseID, "traces")
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	return ""
}

// GetTranscript reads a transcript from a case.
// Probes for v2 split format (.jsonl + .json sidecar) first, then falls back to v1 (.json monolith).
func (m *Manager) GetTranscript(worktreePath, caseID, claudeRefID string) (*claude.Transcript, error) {
	dirs := []string{
		filepath.Join(m.casesDir(worktreePath), caseID, "transcripts"),
		filepath.Join(m.archivedDir(worktreePath), caseID, "transcripts"),
	}

	for _, dir := range dirs {
		// Try v2 split format first
		jsonlPath := filepath.Join(dir, claudeRefID+".jsonl")
		if _, err := os.Stat(jsonlPath); err == nil {
			metaPath := filepath.Join(dir, claudeRefID+".json")
			return claude.ReadTranscriptSplit(jsonlPath, metaPath)
		}

		// Fall back to v1 monolith format
		jsonPath := filepath.Join(dir, claudeRefID+".json")
		data, err := os.ReadFile(jsonPath)
		if err == nil {
			return claude.ParseTranscript(data)
		}
	}

	return nil, fmt.Errorf("transcript not found: %s", claudeRefID)
}

// BackfillPreviews fills in empty Preview fields on CaseClaudeRef entries
// by reading the transcript files.
func (m *Manager) BackfillPreviews(worktreePath, caseID string, refs []CaseClaudeRef) {
	for i := range refs {
		if refs[i].Preview != "" {
			continue
		}
		t, err := m.GetTranscript(worktreePath, caseID, refs[i].ID)
		if err != nil {
			continue
		}
		refs[i].Preview = claude.FirstUserPreview(t.Messages, 200)
	}
}
