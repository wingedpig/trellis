// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// CaseClient reads cases: the per-worktree records of past work (summary,
// commits, notes, agent transcripts) that Trellis archives into the repo.
//
// Access this client through [Client.Cases]:
//
//	hits, err := client.Cases.Search(ctx, "main", client.CaseSearchOptions{Query: "rate limiter"})
type CaseClient struct {
	c *Client
}

// CaseSummary is the structured summary generated when a case is wrapped up.
type CaseSummary struct {
	Synopsis    string    `json:"synopsis"`
	Symptoms    string    `json:"symptoms"`
	RootCause   string    `json:"root_cause"`
	Resolution  string    `json:"resolution"`
	GeneratedAt time.Time `json:"generated_at"`
}

// CaseSearchResult is one archived case matching a search.
type CaseSearchResult struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Kind      string       `json:"kind"`
	Status    string       `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	Worktree  string       `json:"worktree"`
	Score     int          `json:"score"`
	Snippet   string       `json:"snippet,omitempty"`
	Summary   *CaseSummary `json:"summary,omitempty"`
}

// CaseSearchOptions filters [CaseClient.Search].
type CaseSearchOptions struct {
	// Query is free text matched against titles, summaries, commit
	// descriptions, and notes. Empty lists every archived case.
	Query string
	// Kind is bug, feature, investigation, or task.
	Kind string
	// From and To bound the case creation date (YYYY-MM-DD or RFC3339).
	From, To string
	// Transcripts also matches the first prompt of each attached transcript.
	Transcripts bool
}

// CaseCommit is an intermediate commit recorded on a case.
type CaseCommit struct {
	SHA          string    `json:"sha"`
	ShortSHA     string    `json:"short_sha"`
	CommittedAt  time.Time `json:"committed_at"`
	Message      string    `json:"message"`
	Description  string    `json:"description"`
	FilesChanged []string  `json:"files_changed,omitempty"`
	// SessionID is "<agent>:<session-id>" when an agent session made the
	// commit itself (recorded from its Trellis-Session trailer).
	SessionID string `json:"session_id,omitempty"`
}

// CaseTranscriptRef is an agent transcript attached to a case.
type CaseTranscriptRef struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	MessageCount int    `json:"message_count"`
	Preview      string `json:"preview,omitempty"`
}

// CaseLink is a URL attached to a case.
type CaseLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Case is a case manifest.
type Case struct {
	ID         string              `json:"id"`
	Title      string              `json:"title"`
	Kind       string              `json:"kind"`
	Status     string              `json:"status"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
	Links      []CaseLink          `json:"links,omitempty"`
	Claude     []CaseTranscriptRef `json:"claude,omitempty"`
	Codex      []CaseTranscriptRef `json:"codex,omitempty"`
	Commits    []CaseCommit        `json:"commits,omitempty"`
	Summary    *CaseSummary        `json:"summary,omitempty"`
	Redactions map[string]int      `json:"redactions,omitempty"`
}

// CaseProvenance explains where a line of code or a commit came from.
type CaseProvenance struct {
	File              string            `json:"file,omitempty"`
	Line              int               `json:"line,omitempty"`
	LineText          string            `json:"line_text,omitempty"`
	Commit            *ProvenanceCommit `json:"commit,omitempty"`
	CaseID            string            `json:"case_id,omitempty"`
	CaseTitle         string            `json:"case_title,omitempty"`
	Archived          bool              `json:"archived,omitempty"`
	Summary           *CaseSummary      `json:"summary,omitempty"`
	CommitDescription string            `json:"commit_description,omitempty"`
	// Session is the agent session that made the commit ("<agent>:<id>"),
	// from its Trellis-Session trailer.
	Session string `json:"session,omitempty"`
	// LinkedBy is how the case was found: trailer, session, case-tag,
	// wrap-up, case-commits, transcript, or open-case.
	LinkedBy string           `json:"linked_by,omitempty"`
	Turns    []ProvenanceTurn `json:"turns,omitempty"`
}

// ProvenanceCommit identifies the commit behind a line.
type ProvenanceCommit struct {
	SHA         string    `json:"sha"`
	ShortSHA    string    `json:"short_sha"`
	Author      string    `json:"author"`
	Date        time.Time `json:"date"`
	Subject     string    `json:"subject"`
	Message     string    `json:"message"`
	Uncommitted bool      `json:"uncommitted,omitempty"`
}

// ProvenanceTurn is an agent edit to the file and the prompt behind it.
type ProvenanceTurn struct {
	CaseID          string    `json:"case_id"`
	Agent           string    `json:"agent"`
	TranscriptID    string    `json:"transcript_id"`
	TranscriptTitle string    `json:"transcript_title,omitempty"`
	Session         string    `json:"session,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
	Tool            string    `json:"tool"`
	Prompt          string    `json:"prompt,omitempty"`
	Note            string    `json:"note,omitempty"`
	MatchedLine     bool      `json:"matched_line"`
}

// Search finds archived cases in a worktree, best match first.
func (c *CaseClient) Search(ctx context.Context, worktree string, opts CaseSearchOptions) ([]CaseSearchResult, error) {
	q := url.Values{}
	if opts.Query != "" {
		q.Set("q", opts.Query)
	}
	if opts.Kind != "" {
		q.Set("kind", opts.Kind)
	}
	if opts.From != "" {
		q.Set("from", opts.From)
	}
	if opts.To != "" {
		q.Set("to", opts.To)
	}
	if opts.Transcripts {
		q.Set("include_transcripts", "1")
	}
	path := "/api/v1/cases/" + url.PathEscape(worktree) + "/archived/search"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	data, err := c.c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var results []CaseSearchResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("failed to parse search results: %w", err)
	}
	return results, nil
}

// Get returns a case (open or archived) by ID.
func (c *CaseClient) Get(ctx context.Context, worktree, id string) (*Case, error) {
	data, err := c.c.get(ctx, "/api/v1/cases/"+url.PathEscape(worktree)+"/"+url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	var cs Case
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("failed to parse case: %w", err)
	}
	return &cs, nil
}

// Notes returns a case's notes.md.
func (c *CaseClient) Notes(ctx context.Context, worktree, id string) (string, error) {
	// notes is served as raw markdown, not the JSON envelope.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.c.baseURL+"/api/v1/cases/"+url.PathEscape(worktree)+"/"+url.PathEscape(id)+"/notes", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Trellis-Version", c.c.version)
	resp, err := c.c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		_, err := c.c.parseResponse(resp)
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

// Explain reports the provenance of file:line (a path relative to the
// worktree root): the commit that last changed it, that commit's case, and
// the agent turns that wrote it.
func (c *CaseClient) Explain(ctx context.Context, worktree, file string, line int) (*CaseProvenance, error) {
	q := url.Values{"file": {file}, "line": {strconv.Itoa(line)}}
	return c.explain(ctx, worktree, q)
}

// ExplainCommit reports the case behind a commit.
func (c *CaseClient) ExplainCommit(ctx context.Context, worktree, commit string) (*CaseProvenance, error) {
	return c.explain(ctx, worktree, url.Values{"commit": {commit}})
}

func (c *CaseClient) explain(ctx context.Context, worktree string, q url.Values) (*CaseProvenance, error) {
	data, err := c.c.get(ctx, "/api/v1/cases/"+url.PathEscape(worktree)+"/explain?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var p CaseProvenance
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed to parse provenance: %w", err)
	}
	return &p, nil
}
