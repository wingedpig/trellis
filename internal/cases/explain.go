// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package cases

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wingedpig/trellis/internal/claude"
	"github.com/wingedpig/trellis/internal/codex"
)

// Provenance answers "why is this code here": the commit that last touched a
// line, the case that commit belongs to, and the agent turns that wrote it.
type Provenance struct {
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// LineText is the current content of File:Line.
	LineText string `json:"line_text,omitempty"`
	// Commit is nil when the line has uncommitted changes.
	Commit *ProvenanceCommit `json:"commit,omitempty"`

	CaseID    string       `json:"case_id,omitempty"`
	CaseTitle string       `json:"case_title,omitempty"`
	Archived  bool         `json:"archived,omitempty"`
	Summary   *CaseSummary `json:"summary,omitempty"`
	// CommitDescription is the case's narrative note for this commit, when
	// the commit was recorded as an intermediate commit.
	CommitDescription string `json:"commit_description,omitempty"`
	// Session is the agent session that made the commit, from its
	// Trellis-Session trailer ("<agent>:<session-id>").
	Session string `json:"session,omitempty"`
	// LinkedBy says how the case was found: "trailer" (Trellis-Case
	// trailer), "session" (the case holds the transcript of the session
	// named by the Trellis-Session trailer), "case-tag" (legacy "[case: id]"
	// in the message), "wrap-up"
	// (the commit archived the case), "case-commits" (listed in case.json),
	// "transcript" (no recorded link; an agent turn in the case wrote this
	// line), or "open-case" (the line is uncommitted).
	LinkedBy string `json:"linked_by,omitempty"`

	Turns []ProvenanceTurn `json:"turns,omitempty"`
}

// ProvenanceCommit identifies a commit.
type ProvenanceCommit struct {
	SHA         string    `json:"sha"`
	ShortSHA    string    `json:"short_sha"`
	Author      string    `json:"author"`
	Date        time.Time `json:"date"`
	Subject     string    `json:"subject"`
	Message     string    `json:"message"`
	Uncommitted bool      `json:"uncommitted,omitempty"`
}

// ProvenanceTurn is one agent edit to the file, with the prompt that led to it.
type ProvenanceTurn struct {
	CaseID          string `json:"case_id"`
	Agent           string `json:"agent"` // "claude" | "codex"
	TranscriptID    string `json:"transcript_id"`
	TranscriptTitle string `json:"transcript_title,omitempty"`
	// Session is the transcript's source session ("<agent>:<session-id>").
	Session   string    `json:"session,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Tool      string    `json:"tool"`
	// Prompt is the user message that started the turn.
	Prompt string `json:"prompt,omitempty"`
	// Note is the agent's most recent explanation before the edit.
	Note string `json:"note,omitempty"`
	// MatchedLine is true when the edit's new text contains the line;
	// false means the turn edited the file but not visibly this line.
	MatchedLine bool `json:"matched_line"`
}

const (
	maxProvenanceTurns = 3
	provenanceTextLen  = 800
	// A case's transcripts are only searched when the commit falls within
	// the case's lifetime, padded by this much on each side.
	caseWindowPad = 36 * time.Hour
)

var legacyCaseTag = regexp.MustCompile(`\[case:\s*([^\]\s]+)\s*\]`)

// Explain resolves the provenance of file:line (repo-relative) or, when file
// is empty, of commit. Uncommitted lines are matched against the open case.
func (m *Manager) Explain(ctx context.Context, worktreePath, file string, line int, commit string) (*Provenance, error) {
	p := &Provenance{File: filepath.ToSlash(file), Line: line}
	sha := commit
	if file != "" {
		if line < 1 {
			return nil, fmt.Errorf("line must be >= 1")
		}
		var err error
		sha, p.LineText, err = blameLine(ctx, worktreePath, file, line)
		if err != nil {
			return nil, err
		}
	}
	if sha == "" {
		return nil, fmt.Errorf("file or commit is required")
	}

	var candidates []string
	if strings.Trim(sha, "0") == "" {
		p.Commit = &ProvenanceCommit{Uncommitted: true}
		if open, _ := m.FirstOpenCase(worktreePath); open != nil {
			candidates = append(candidates, open.ID)
			p.LinkedBy = "open-case"
		}
	} else {
		c, err := commitInfo(ctx, worktreePath, sha)
		if err != nil {
			return nil, err
		}
		p.Commit = c
		var sessions []string
		candidates, p.LinkedBy, sessions = m.linkedCases(ctx, worktreePath, c)
		if len(sessions) > 0 {
			p.Session = sessions[0]
		}
	}

	// Search the linked cases' transcripts for the edit.
	if p.File != "" {
		for _, id := range candidates {
			if cj, err := m.Get(worktreePath, id); err == nil {
				p.Turns = append(p.Turns, m.findEdits(worktreePath, cj, p.File, p.LineText)...)
			}
		}
		// The session that made the commit wrote it; other sessions' edits
		// to the same file are noise when that one's turn is found.
		if p.Session != "" {
			var own []ProvenanceTurn
			for _, t := range p.Turns {
				if t.Session == p.Session {
					own = append(own, t)
				}
			}
			if len(own) > 0 {
				p.Turns = own
			}
		}
	}

	// No recorded link: commits made outside Trellis (an agent's own `git
	// commit`, a terminal) still came from some session. Look for a turn
	// that wrote this exact line in any case alive around the commit date.
	if len(candidates) == 0 && p.File != "" && significant(p.LineText) && !p.Commit.Uncommitted {
		for _, cj := range m.casesAround(worktreePath, p.Commit.Date) {
			for _, t := range m.findEdits(worktreePath, cj, p.File, p.LineText) {
				if t.MatchedLine {
					p.Turns = append(p.Turns, t)
				}
			}
		}
		if len(p.Turns) > 0 {
			sortTurns(p.Turns)
			candidates = []string{p.Turns[0].CaseID}
			p.LinkedBy = "transcript"
		}
	}

	sortTurns(p.Turns)
	if len(p.Turns) > maxProvenanceTurns {
		p.Turns = p.Turns[:maxProvenanceTurns]
	}

	if len(candidates) > 0 {
		id := candidates[0]
		// Prefer the case whose transcript actually wrote the line (a
		// wrap-up commit can archive more than one case).
		if len(p.Turns) > 0 {
			id = p.Turns[0].CaseID
		}
		if cj, err := m.Get(worktreePath, id); err == nil {
			p.CaseID, p.CaseTitle, p.Summary = cj.ID, cj.Title, cj.Summary
			p.Archived = m.IsArchived(worktreePath, cj.ID)
			if p.Commit != nil {
				for _, ce := range cj.Commits {
					if ce.SHA == p.Commit.SHA {
						p.CommitDescription = ce.Description
					}
				}
			}
		}
	}
	return p, nil
}

// linkedCases returns the IDs of cases the commit records a link to, most
// specific first, how the first was found, and the sessions named by the
// commit's Trellis-Session trailers.
func (m *Manager) linkedCases(ctx context.Context, worktreePath string, c *ProvenanceCommit) ([]string, string, []string) {
	var ids []string
	how := ""
	add := func(id, by string) {
		if validID(id) != nil {
			return
		}
		for _, x := range ids {
			if x == id {
				return
			}
		}
		if _, err := m.Get(worktreePath, id); err != nil {
			return
		}
		if how == "" {
			how = by
		}
		ids = append(ids, id)
	}

	if out, err := gitOut(ctx, worktreePath, "log", "-1", "--format=%(trailers:key="+CommitTrailer+",valueonly,unfold)", c.SHA); err == nil {
		for _, v := range strings.Fields(out) {
			add(v, "trailer")
		}
	}
	var sessions []string
	if out, err := gitOut(ctx, worktreePath, "log", "-1", "--format=%(trailers:key="+SessionTrailer+",valueonly,unfold)", c.SHA); err == nil {
		sessions = strings.Fields(out)
	}
	if len(sessions) > 0 {
		for _, dir := range []string{m.casesDir(worktreePath), m.archivedDir(worktreePath)} {
			matches, _ := filepath.Glob(filepath.Join(dir, "*", "case.json"))
			for _, path := range matches {
				cj, err := loadCase(path)
				if err != nil {
					continue
				}
				attached := cj.AttachedSessions()
				for _, s := range sessions {
					if attached[s] {
						add(cj.ID, "session")
					}
				}
			}
		}
	}
	for _, mt := range legacyCaseTag.FindAllStringSubmatch(c.Message, -1) {
		add(mt[1], "case-tag")
	}
	// The wrap-up commit is the one that added the archived case.json.
	if out, err := gitOut(ctx, worktreePath, "show", "--name-only", "--diff-filter=A", "--format=", c.SHA, "--", m.ArchivedRelDir()); err == nil {
		prefix := filepath.ToSlash(m.ArchivedRelDir()) + "/"
		for _, f := range strings.Split(out, "\n") {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(f), prefix); ok {
				if id, ok := strings.CutSuffix(rest, "/case.json"); ok {
					add(id, "wrap-up")
				}
			}
		}
	}
	// Intermediate commits recorded on the case (SHAs don't survive a
	// rebase, which is why the trailer exists).
	for _, dir := range []string{m.casesDir(worktreePath), m.archivedDir(worktreePath)} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*", "case.json"))
		for _, path := range matches {
			cj, err := loadCase(path)
			if err != nil {
				continue
			}
			for _, ce := range cj.Commits {
				if ce.SHA == c.SHA {
					add(cj.ID, "case-commits")
				}
			}
		}
	}
	return ids, how, sessions
}

// casesAround returns open and archived cases whose lifetime (padded)
// contains t.
func (m *Manager) casesAround(worktreePath string, t time.Time) []*CaseJSON {
	var out []*CaseJSON
	for _, dir := range []string{m.casesDir(worktreePath), m.archivedDir(worktreePath)} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*", "case.json"))
		for _, path := range matches {
			cj, err := loadCase(path)
			if err != nil {
				continue
			}
			if t.Before(cj.CreatedAt.Add(-caseWindowPad)) || t.After(cj.UpdatedAt.Add(caseWindowPad)) {
				continue
			}
			out = append(out, cj)
		}
	}
	return out
}

// findEdits scans a case's transcripts for agent edits to file. When
// lineText is significant, turns whose new text contains it are marked
// MatchedLine.
func (m *Manager) findEdits(worktreePath string, cj *CaseJSON, file, lineText string) []ProvenanceTurn {
	needle := strings.TrimSpace(lineText)
	if !significant(needle) {
		needle = ""
	}
	var out []ProvenanceTurn
	for _, ref := range cj.Claude {
		t, err := m.GetTranscript(worktreePath, cj.ID, ref.ID)
		if err != nil {
			continue
		}
		for _, turn := range claudeEdits(t.Messages, file, needle) {
			turn.CaseID, turn.Agent, turn.TranscriptID, turn.TranscriptTitle = cj.ID, "claude", ref.ID, ref.Title
			if ref.SourceSessionID != "" {
				turn.Session = SessionKey("claude", ref.SourceSessionID)
			}
			out = append(out, turn)
		}
	}
	for _, ref := range cj.Codex {
		t, err := m.GetCodexTranscript(worktreePath, cj.ID, ref.ID)
		if err != nil {
			continue
		}
		for _, turn := range codexEdits(t.Messages, file, needle) {
			turn.CaseID, turn.Agent, turn.TranscriptID, turn.TranscriptTitle = cj.ID, "codex", ref.ID, ref.Title
			if ref.SourceSessionID != "" {
				turn.Session = SessionKey("codex", ref.SourceSessionID)
			}
			out = append(out, turn)
		}
	}
	// When the line was found, the turns that merely touched the file are noise.
	matched := out[:0:0]
	for _, t := range out {
		if t.MatchedLine {
			matched = append(matched, t)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return out
}

// claudeEditInput covers the file-writing tools' inputs.
type claudeEditInput struct {
	FilePath  string `json:"file_path"`
	NewString string `json:"new_string"`
	Content   string `json:"content"`
	Edits     []struct {
		NewString string `json:"new_string"`
	} `json:"edits"`
}

func claudeEdits(msgs []claude.Message, file, needle string) []ProvenanceTurn {
	var out []ProvenanceTurn
	prompt, note := "", ""
	for _, msg := range msgs {
		if msg.Role == "user" {
			if text := userText(msg); text != "" {
				prompt, note = text, ""
			}
			continue
		}
		for _, b := range msg.Content {
			switch {
			case b.Type == "text" && strings.TrimSpace(b.Text) != "":
				note = b.Text
			case b.Type == "tool_use" && b.Name == "Bash":
				var in struct {
					Command string `json:"command"`
				}
				if json.Unmarshal(b.Input, &in) == nil && shellWrites(in.Command, file, needle) {
					out = append(out, ProvenanceTurn{
						Timestamp:   msg.Timestamp,
						Tool:        "Bash",
						Prompt:      clip(prompt),
						Note:        clip(note),
						MatchedLine: true,
					})
				}
			case b.Type == "tool_use" && (b.Name == "Edit" || b.Name == "MultiEdit" || b.Name == "Write"):
				var in claudeEditInput
				if json.Unmarshal(b.Input, &in) != nil || !samePath(in.FilePath, file) {
					continue
				}
				written := in.NewString + "\n" + in.Content
				for _, e := range in.Edits {
					written += "\n" + e.NewString
				}
				out = append(out, ProvenanceTurn{
					Timestamp:   msg.Timestamp,
					Tool:        b.Name,
					Prompt:      clip(prompt),
					Note:        clip(note),
					MatchedLine: needle != "" && strings.Contains(written, needle),
				})
			}
		}
	}
	return out
}

// userText returns a user message's typed text, or "" for tool results.
func userText(msg claude.Message) string {
	for _, b := range msg.Content {
		if b.Type == "tool_result" {
			return ""
		}
	}
	for _, b := range msg.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return ""
}

func codexEdits(msgs []codex.Message, file, needle string) []ProvenanceTurn {
	var out []ProvenanceTurn
	prompt, note := "", ""
	for _, msg := range msgs {
		if msg.Role == "user" {
			for _, it := range msg.Items {
				if strings.TrimSpace(it.Text) != "" {
					prompt, note = it.Text, ""
					break
				}
			}
			continue
		}
		for _, it := range msg.Items {
			switch it.Type {
			case "agentMessage":
				if strings.TrimSpace(it.Text) != "" {
					note = it.Text
				}
			case "commandExecution":
				if shellWrites(commandText(it.Command), file, needle) {
					out = append(out, ProvenanceTurn{
						Timestamp:   msg.Timestamp,
						Tool:        "commandExecution",
						Prompt:      clip(prompt),
						Note:        clip(note),
						MatchedLine: true,
					})
				}
			case "fileChange":
				for path, diff := range it.FileDiffs() {
					if !samePath(path, file) {
						continue
					}
					out = append(out, ProvenanceTurn{
						Timestamp:   msg.Timestamp,
						Tool:        "fileChange",
						Prompt:      clip(prompt),
						Note:        clip(note),
						MatchedLine: needle != "" && diffAdds(diff, needle),
					})
				}
			}
		}
	}
	return out
}

// shellWrites reports whether a shell command plausibly wrote the line:
// agents often edit with sed, a heredoc, or an inline script instead of an
// edit tool. Commands that only name the file (grep, sed -n) are too common
// to count, so the command must contain the line's text as well.
func shellWrites(cmd, file, needle string) bool {
	return needle != "" && strings.Contains(cmd, needle) &&
		(strings.Contains(cmd, file) || strings.Contains(cmd, filepath.Base(file)))
}

// commandText flattens a Codex command (argv array or string).
func commandText(raw json.RawMessage) string {
	var argv []string
	if json.Unmarshal(raw, &argv) == nil {
		return strings.Join(argv, " ")
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// diffAdds reports whether needle appears on an added line of a unified diff,
// or anywhere in a diff with no hunks (Codex sends a new file's full content).
func diffAdds(diff, needle string) bool {
	if !strings.Contains(diff, "\n@@") && !strings.HasPrefix(diff, "@@") {
		return strings.Contains(diff, needle)
	}
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") && strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

// samePath matches a transcript's path (usually absolute, possibly in a
// different worktree) against a repo-relative path.
func samePath(transcriptPath, rel string) bool {
	tp := filepath.ToSlash(transcriptPath)
	return tp == rel || strings.HasSuffix(tp, "/"+rel)
}

// significant rejects lines too generic to identify an edit ("}", "return
// nil", blank).
func significant(s string) bool {
	s = strings.TrimSpace(s)
	letters := 0
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			letters++
		}
	}
	return letters >= 8
}

func sortTurns(ts []ProvenanceTurn) {
	sort.SliceStable(ts, func(i, j int) bool {
		if ts[i].MatchedLine != ts[j].MatchedLine {
			return ts[i].MatchedLine
		}
		return ts[i].Timestamp.After(ts[j].Timestamp)
	})
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= provenanceTextLen {
		return s
	}
	return s[:provenanceTextLen] + "…"
}

// blameLine returns the commit that last changed file:line (all zeros when
// uncommitted) and the line's current text.
func blameLine(ctx context.Context, dir, file string, line int) (string, string, error) {
	l := strconv.Itoa(line)
	out, err := gitOut(ctx, dir, "blame", "--porcelain", "-L", l+","+l, "--", file)
	if err != nil {
		return "", "", fmt.Errorf("git blame %s:%d: %s", file, line, strings.TrimSpace(out))
	}
	lines := strings.Split(out, "\n")
	sha := strings.Fields(lines[0])[0]
	for _, ln := range lines[1:] {
		if text, ok := strings.CutPrefix(ln, "\t"); ok {
			return sha, text, nil
		}
	}
	return sha, "", nil
}

func commitInfo(ctx context.Context, dir, rev string) (*ProvenanceCommit, error) {
	out, err := gitOut(ctx, dir, "show", "-s", "--format=%H%x1f%h%x1f%an%x1f%cI%x1f%s%x1f%B", rev+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("unknown commit %q", rev)
	}
	parts := strings.SplitN(out, "\x1f", 6)
	if len(parts) < 6 {
		return nil, fmt.Errorf("unexpected git show output for %q", rev)
	}
	date, _ := time.Parse(time.RFC3339, parts[3])
	return &ProvenanceCommit{
		SHA:      parts[0],
		ShortSHA: parts[1],
		Author:   parts[2],
		Date:     date,
		Subject:  parts[4],
		Message:  strings.TrimRight(parts[5], "\n"),
	}, nil
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(ee.Stderr), err
		}
		return "", err
	}
	return string(out), nil
}
