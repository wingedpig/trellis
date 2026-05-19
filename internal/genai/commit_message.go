// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package genai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CommitMessageInput is the context fed to GenerateCommitMessage.
type CommitMessageInput struct {
	StagedDiff     string   // truncated `git diff --staged` output
	CaseTitle      string   // empty for first-commit-without-case scenarios
	CaseKind       string   // "bug", "feature", "investigation", "task"
	Notes          string   // case notes.md content, may be empty
	RecentMessages []string // recent session activity (user inputs, assistant titles)
	Cwd            string   // worktree path — used as the subprocess cwd
}

// CommitMessageOutput is the structured response: a freeform commit message
// and a 1-2 sentence per-commit case description (the narrative beat, not
// the code change).
type CommitMessageOutput struct {
	Message     string `json:"message"`
	Description string `json:"description"`
}

const commitMessagePromptTemplate = `You are helping a developer write a commit message and a short per-commit "case description" for one intermediate commit on a longer effort.

A "case" is the durable record of a worktree's effort — it has a title, a kind, and notes. This commit is one step within that case.

Return your response as a single JSON object on its own with exactly these fields:
{
  "message": "<the commit message — freeform, no enforced style, present-tense imperative is fine but not required>",
  "description": "<one to two sentences, plain prose, describing what this commit accomplishes relative to the case's overall arc. Different from the message: the message describes the code change, this describes the narrative beat.>"
}

Return only the JSON object — no surrounding prose, no markdown fence.

Context:
%s

Staged diff:
%s
`

// GenerateCommitMessage shells out to ` + "`claude -p`" + ` and returns a
// structured commit-message-plus-description pair. Returns the model name
// the response was generated with as the second value.
func GenerateCommitMessage(ctx context.Context, in CommitMessageInput) (*CommitMessageOutput, string, error) {
	contextSection := buildCommitContextSection(in)
	diff := in.StagedDiff
	if strings.TrimSpace(diff) == "" {
		return nil, "", fmt.Errorf("staged diff is empty — nothing to describe")
	}

	prompt := fmt.Sprintf(commitMessagePromptTemplate, contextSection, diff)

	result, model, err := runClaude(ctx, prompt, in.Cwd)
	if err != nil {
		return nil, "", err
	}

	jsonStr := extractJSON(result)
	var out CommitMessageOutput
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		return nil, model, fmt.Errorf("parse generated JSON: %w (got: %s)", err, truncate(result, 400))
	}
	if strings.TrimSpace(out.Message) == "" {
		return nil, model, fmt.Errorf("generated message is empty")
	}
	return &out, model, nil
}

func buildCommitContextSection(in CommitMessageInput) string {
	var b strings.Builder
	if in.CaseTitle != "" {
		fmt.Fprintf(&b, "- Case title: %s\n", in.CaseTitle)
	} else {
		b.WriteString("- Case title: (no case yet — this may be the first commit creating one)\n")
	}
	if in.CaseKind != "" {
		fmt.Fprintf(&b, "- Case kind: %s\n", in.CaseKind)
	}
	if notes := strings.TrimSpace(in.Notes); notes != "" {
		fmt.Fprintf(&b, "- Notes:\n%s\n", indent(notes, "    "))
	}
	if len(in.RecentMessages) > 0 {
		b.WriteString("- Recent session activity (most recent first):\n")
		for _, m := range in.RecentMessages {
			fmt.Fprintf(&b, "    - %s\n", oneLine(m))
		}
	}
	return b.String()
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}
