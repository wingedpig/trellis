// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package genai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SummaryInput is the context fed to GenerateCaseSummary at wrap-up.
type SummaryInput struct {
	CaseTitle           string
	CaseKind            string   // "bug" / "feature" / "investigation" / "task"
	CaseStatus          string   // "open" / "resolved" / "wontfix"
	Notes               string   // case notes.md content
	WrapUpDiff          string   // truncated staged diff for the wrap-up commit
	TranscriptSummaries []string // brief summaries / preview lines for attached transcripts
	CommitDescriptions  []string // per-commit descriptions accumulated during the case
	TraceSummaries      []string // one-line summary per linked trace
	Cwd                 string
}

// SummaryOutput is the JSON shape we expect from the model.
type SummaryOutput struct {
	Synopsis   string   `json:"synopsis"`
	Symptoms   string   `json:"symptoms"`
	RootCause  string   `json:"root_cause"`
	Resolution string   `json:"resolution"`
	Components []string `json:"components"`
}

// GeneratedSummary is the full result including bookkeeping fields the caller
// will persist on case.json.
type GeneratedSummary struct {
	SummaryOutput
	GeneratedAt time.Time
	Model       string
}

const summaryPromptTemplate = `You are summarizing a completed development "case" — a unit of work that has been wrapped up. The summary will be stored alongside the case for later retrieval ("find the case where we fixed X") and should be optimized for full-text search.

Return your response as a single JSON object on its own with exactly these fields. Components MUST be an array of strings.
{
  "synopsis":    "<one human-readable line, ideally under 120 chars>",
  "symptoms":    "<the observable problem, error messages, or user-facing behavior — empty string if not applicable, e.g. a feature with no precipitating bug>",
  "root_cause":  "<what was actually wrong, if there was a wrong — empty string for feature work or investigations with no resolution>",
  "resolution":  "<what changed — the approach, not the full diff>",
  "components":  ["<short machine-friendly identifiers for the parts of the codebase touched>"]
}

Rules for "components":
  - Each entry is a SHORT identifier — a package path ("internal/cases"), a directory ("views"), a single-word subsystem ("wrap-up", "summary-generation"), or a service name. Lowercase, hyphens or slashes only.
  - NEVER an English phrase ("the case detail page", "wrap up workflow"). Convert prose to kebab-case identifiers.
  - Draw from real file paths, package names, or subsystem labels evident in the inputs — don't invent.
  - 1-6 entries. Omit anything ambiguous rather than padding.

Important: this case's KIND is %q and STATUS is %q. Calibrate accordingly — do NOT assume a feature is a "resolved bug" or that a wontfix has a "resolution". Empty strings are fine when a field doesn't apply.

Return only the JSON object — no surrounding prose, no markdown fence.

Case context:
%s

Wrap-up diff:
%s
`

// GenerateCaseSummary shells out to ` + "`claude -p`" + ` and returns a structured
// case summary suitable for searchability.
func GenerateCaseSummary(ctx context.Context, in SummaryInput) (*GeneratedSummary, error) {
	contextSection := buildSummaryContextSection(in)
	diff := in.WrapUpDiff
	if strings.TrimSpace(diff) == "" {
		diff = "(no diff)"
	}
	prompt := fmt.Sprintf(summaryPromptTemplate, in.CaseKind, in.CaseStatus, contextSection, diff)

	result, model, err := runClaude(ctx, prompt, in.Cwd)
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSON(result)
	var out SummaryOutput
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		return nil, fmt.Errorf("parse generated summary JSON: %w (got: %s)", err, truncate(result, 400))
	}
	if strings.TrimSpace(out.Synopsis) == "" {
		return nil, fmt.Errorf("generated summary has empty synopsis")
	}
	out.Components = NormalizeComponents(out.Components)
	return &GeneratedSummary{
		SummaryOutput: out,
		GeneratedAt:   time.Now(),
		Model:         model,
	}, nil
}

// NormalizeComponents cleans up the model's "components" array: trim, drop
// empty, lowercase, convert internal whitespace to hyphens (so a stray
// "case detail page" becomes "case-detail-page"), drop entries that still
// look like sentences after that, and dedupe. Idempotent.
func NormalizeComponents(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		v = collapseWhitespaceToHyphen(v)
		v = strings.Trim(v, "-")
		if v == "" {
			continue
		}
		// Drop anything that still has > 5 hyphen-separated parts — it
		// was probably a sentence the model couldn't help itself with.
		if strings.Count(v, "-") > 5 {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func collapseWhitespaceToHyphen(s string) string {
	// Replace any run of whitespace or underscores with a single hyphen.
	var b strings.Builder
	b.Grow(len(s))
	inSep := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '_' {
			if !inSep {
				b.WriteByte('-')
				inSep = true
			}
			continue
		}
		inSep = false
		b.WriteRune(r)
	}
	return b.String()
}

func buildSummaryContextSection(in SummaryInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- Title: %s\n", in.CaseTitle)
	if notes := strings.TrimSpace(in.Notes); notes != "" {
		fmt.Fprintf(&b, "- Notes:\n%s\n", indent(notes, "    "))
	}
	if len(in.CommitDescriptions) > 0 {
		b.WriteString("- Per-commit descriptions (chronological):\n")
		for _, d := range in.CommitDescriptions {
			fmt.Fprintf(&b, "    - %s\n", oneLine(d))
		}
	}
	if len(in.TranscriptSummaries) > 0 {
		b.WriteString("- Transcript previews:\n")
		for _, t := range in.TranscriptSummaries {
			fmt.Fprintf(&b, "    - %s\n", oneLine(t))
		}
	}
	if len(in.TraceSummaries) > 0 {
		b.WriteString("- Linked traces:\n")
		for _, t := range in.TraceSummaries {
			fmt.Fprintf(&b, "    - %s\n", oneLine(t))
		}
	}
	return b.String()
}
