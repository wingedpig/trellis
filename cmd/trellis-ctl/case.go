// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wingedpig/trellis/pkg/client"
)

func cmdCase(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl case <search|show|why> [args]")
	}
	switch args[0] {
	case "search":
		return cmdCaseSearch(args[1:])
	case "show":
		return cmdCaseShow(args[1:])
	case "why":
		return cmdCaseWhy(args[1:])
	default:
		return fmt.Errorf("unknown case subcommand: %s", args[0])
	}
}

// caseWorktree returns -worktree, or the worktree containing the cwd.
func caseWorktree(ctx context.Context, vals map[string]string, usage string) (string, error) {
	if wt := vals["worktree"]; wt != "" {
		return wt, nil
	}
	wt, err := currentWorktree(ctx)
	if err != nil {
		return "", fmt.Errorf("%v; pass -worktree\n%s", err, usage)
	}
	return wt, nil
}

func cmdCaseSearch(args []string) error {
	const usage = "usage: trellis-ctl case search <query> [-kind bug|feature|investigation|task] [-since <date>] [-until <date>] [-n N] [-transcripts] [-worktree <name>]"
	pos, vals, bools, err := parseTaskFlags(args,
		[]string{"kind", "since", "until", "n", "worktree"},
		[]string{"transcripts"})
	if err != nil {
		return fmt.Errorf("%v\n%s", err, usage)
	}
	limit := 10
	if v, ok := vals["n"]; ok {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 {
			return fmt.Errorf("-n must be a positive number\n%s", usage)
		}
	}
	ctx := context.Background()
	wt, err := caseWorktree(ctx, vals, usage)
	if err != nil {
		return err
	}
	results, err := apiClient.Cases.Search(ctx, wt, client.CaseSearchOptions{
		Query:       strings.Join(pos, " "),
		Kind:        vals["kind"],
		From:        vals["since"],
		To:          vals["until"],
		Transcripts: bools["transcripts"],
	})
	if err != nil {
		return err
	}
	more := 0
	if len(results) > limit {
		more = len(results) - limit
		results = results[:limit]
	}
	if jsonOutput {
		printJSON(results)
		return nil
	}
	if len(results) == 0 {
		fmt.Println("No matching cases.")
		return nil
	}
	for i, r := range results {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s  [%s]  %s\n", r.ID, r.Kind, r.Title)
		if r.Summary != nil && r.Summary.Synopsis != "" {
			fmt.Println(indent(r.Summary.Synopsis, "  "))
		}
		if r.Snippet != "" {
			fmt.Println(indent("match: "+firstLine(r.Snippet), "  "))
		}
	}
	if more > 0 {
		fmt.Printf("\n%d more; raise -n or narrow the query.\n", more)
	}
	fmt.Println("\nRead one with: trellis-ctl case show <id>")
	return nil
}

func cmdCaseShow(args []string) error {
	const usage = "usage: trellis-ctl case show <id> [-worktree <name>]"
	pos, vals, _, err := parseTaskFlags(args, []string{"worktree"}, nil)
	if err != nil || len(pos) != 1 {
		return fmt.Errorf(usage)
	}
	ctx := context.Background()
	wt, err := caseWorktree(ctx, vals, usage)
	if err != nil {
		return err
	}
	c, err := apiClient.Cases.Get(ctx, wt, pos[0])
	if err != nil {
		return err
	}
	notes, _ := apiClient.Cases.Notes(ctx, wt, c.ID)
	if jsonOutput {
		printJSON(struct {
			*client.Case
			Notes string `json:"notes,omitempty"`
		}{c, notes})
		return nil
	}

	fmt.Printf("%s — %s\n", c.ID, c.Title)
	fmt.Printf("Kind: %s  Status: %s  Created: %s  Updated: %s\n", c.Kind, c.Status,
		c.CreatedAt.Format("2006-01-02"), c.UpdatedAt.Format("2006-01-02"))
	if s := c.Summary; s != nil {
		for _, f := range [][2]string{{"Synopsis", s.Synopsis}, {"Symptoms", s.Symptoms}, {"Root cause", s.RootCause}, {"Resolution", s.Resolution}} {
			if strings.TrimSpace(f[1]) != "" {
				fmt.Printf("\n%s:\n%s\n", f[0], indent(strings.TrimSpace(f[1]), "  "))
			}
		}
	}
	if len(c.Commits) > 0 {
		fmt.Println("\nCommits:")
		for _, ce := range c.Commits {
			desc := ce.Description
			if desc == "" {
				desc = firstLine(ce.Message)
			}
			if ce.SessionID != "" {
				desc += "  (committed by " + ce.SessionID + ")"
			}
			fmt.Printf("  %s %s  %s\n", ce.ShortSHA, ce.CommittedAt.Format("2006-01-02"), desc)
		}
	}
	if len(c.Claude)+len(c.Codex) > 0 {
		fmt.Println("\nTranscripts:")
		for _, group := range []struct {
			agent string
			refs  []client.CaseTranscriptRef
		}{{"claude", c.Claude}, {"codex", c.Codex}} {
			for _, r := range group.refs {
				fmt.Printf("  %s %s  %q (%d messages)\n", group.agent, r.ID, r.Title, r.MessageCount)
				if r.Preview != "" {
					fmt.Println(indent(firstLine(r.Preview), "    "))
				}
			}
		}
	}
	if len(c.Links) > 0 {
		fmt.Println("\nLinks:")
		for _, l := range c.Links {
			fmt.Printf("  %s  %s\n", l.Title, l.URL)
		}
	}
	if strings.TrimSpace(notes) != "" {
		fmt.Printf("\nNotes:\n%s\n", indent(strings.TrimSpace(notes), "  "))
	}
	if len(c.Redactions) > 0 {
		var parts []string
		for k, n := range c.Redactions {
			parts = append(parts, fmt.Sprintf("%s×%d", k, n))
		}
		fmt.Printf("\nSecrets redacted on archive: %s\n", strings.Join(parts, ", "))
	}
	return nil
}

func cmdCaseWhy(args []string) error {
	const usage = "usage: trellis-ctl case why <file>:<line> | <commit> [-worktree <name>]"
	pos, vals, _, err := parseTaskFlags(args, []string{"worktree"}, nil)
	if err != nil || len(pos) != 1 {
		return fmt.Errorf(usage)
	}
	ctx := context.Background()
	wt, err := caseWorktree(ctx, vals, usage)
	if err != nil {
		return err
	}

	var p *client.CaseProvenance
	if file, line, ok := splitFileLine(pos[0]); ok {
		rel, err := repoRelative(ctx, file)
		if err != nil {
			return err
		}
		p, err = apiClient.Cases.Explain(ctx, wt, rel, line)
		if err != nil {
			return err
		}
	} else {
		p, err = apiClient.Cases.ExplainCommit(ctx, wt, pos[0])
		if err != nil {
			return err
		}
	}
	if jsonOutput {
		printJSON(p)
		return nil
	}
	printProvenance(p)
	return nil
}

// splitFileLine parses "path:123".
func splitFileLine(s string) (string, int, bool) {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n < 1 {
		return "", 0, false
	}
	return s[:i], n, true
}

// repoRelative turns a path given relative to the cwd (or absolute) into one
// relative to the git worktree root, which is what the server blames.
func repoRelative(ctx context.Context, file string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git worktree")
	}
	top := resolvePath(strings.TrimSpace(string(out)))
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	abs = filepath.Join(resolvePath(filepath.Dir(abs)), filepath.Base(abs))
	rel, err := filepath.Rel(top, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the worktree %s", file, top)
	}
	return filepath.ToSlash(rel), nil
}

func printProvenance(p *client.CaseProvenance) {
	if p.File != "" {
		fmt.Printf("%s:%d  %s\n", p.File, p.Line, strings.TrimSpace(p.LineText))
	}
	if c := p.Commit; c != nil {
		if c.Uncommitted {
			fmt.Println("Commit:  (uncommitted)")
		} else {
			fmt.Printf("Commit:  %s  %s  %s  %s\n", c.ShortSHA, c.Date.Format("2006-01-02"), c.Author, c.Subject)
		}
	}
	if p.CaseID == "" {
		fmt.Println("Case:    none found — the commit has no case link and no saved transcript wrote this line.")
		return
	}
	state := "open"
	if p.Archived {
		state = "archived"
	}
	fmt.Printf("Case:    %s — %s (%s; linked by %s)\n", p.CaseID, p.CaseTitle, state, p.LinkedBy)
	if p.Session != "" {
		fmt.Printf("Session: %s\n", p.Session)
	}
	if p.Summary != nil && p.Summary.Synopsis != "" {
		fmt.Println(indent(p.Summary.Synopsis, "         "))
	}
	if p.CommitDescription != "" {
		fmt.Printf("Commit note: %s\n", p.CommitDescription)
	}
	for _, t := range p.Turns {
		match := "wrote this line"
		if !t.MatchedLine {
			match = "edited this file"
		}
		fmt.Printf("\n%s session %s %q — %s via %s, %s\n", t.Agent, t.TranscriptID, t.TranscriptTitle, match, t.Tool, t.Timestamp.Format("2006-01-02 15:04"))
		if t.Prompt != "" {
			fmt.Printf("  Prompt: %s\n", strings.ReplaceAll(t.Prompt, "\n", "\n          "))
		}
		if t.Note != "" {
			fmt.Printf("  Agent:  %s\n", strings.ReplaceAll(t.Note, "\n", "\n          "))
		}
	}
	fmt.Printf("\nMore: trellis-ctl case show %s\n", p.CaseID)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
