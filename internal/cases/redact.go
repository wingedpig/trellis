// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package cases

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// redactRule masks one kind of secret. When group is non-zero only that
// submatch is replaced, so the surrounding key name or URL stays readable.
type redactRule struct {
	kind  string
	re    *regexp.Regexp
	group int
}

// Archived cases are committed to the repo, and agent transcripts routinely
// carry secrets echoed by tool output (`cat .env`, config dumps, curl -v).
// Every token class below excludes `"` and `\`, so masking a match inside a
// JSON string never breaks the surrounding encoding — the private key rule
// consumes `\n` escapes as whole units for the same reason.
var builtinRedactRules = []redactRule{
	{kind: "private-key", re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?:[A-Za-z0-9+/=\s]|\\+[nrt])*(?:-----END [A-Z0-9 ]*PRIVATE KEY-----)?`)},
	{kind: "aws-access-key", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{kind: "aws-secret-key", re: regexp.MustCompile(`(?i)aws_?secret_?access_?key\\?["']?\s*[:=]\s*\\?["']?([A-Za-z0-9/+=]{40})\b`), group: 1},
	{kind: "github-token", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,})\b`)},
	{kind: "slack-token", re: regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{kind: "slack-webhook", re: regexp.MustCompile(`https://hooks\.slack\.com/services/[A-Za-z0-9/_-]+`)},
	{kind: "stripe-key", re: regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`)},
	{kind: "stripe-webhook-secret", re: regexp.MustCompile(`\bwhsec_[A-Za-z0-9+/=]{16,}`)},
	{kind: "anthropic-key", re: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{kind: "openai-key", re: regexp.MustCompile(`\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_-]{32,}`)},
	{kind: "google-api-key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{kind: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{kind: "url-password", re: regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@"'\\]+:([^\s@/"'\\]{3,})@`), group: 1},
	// key = "value" assignments. The value must be a quoted literal so code
	// like `password := req.Password` is left alone.
	{kind: "assigned-secret", re: regexp.MustCompile(`(?i)\b(?:[a-z0-9]+[_-])?(?:password|passwd|secret|token|api[_-]?key|apikey|private[_-]?key)\\?["']?\s*(?::=|[:=])\s*\\?["']([^\s"'\\]{8,})\\?["']`), group: 1},
}

// redactText masks secrets in s. It returns the masked text and the number
// of matches per rule kind (nil when nothing matched).
func redactText(s string, extra []*regexp.Regexp) (string, map[string]int) {
	var counts map[string]int
	apply := func(r redactRule) {
		locs := r.re.FindAllStringSubmatchIndex(s, -1)
		if len(locs) == 0 {
			return
		}
		mask := "[REDACTED:" + r.kind + "]"
		var b strings.Builder
		last := 0
		n := 0
		for _, loc := range locs {
			start, end := loc[2*r.group], loc[2*r.group+1]
			// Skip text that is already masked, so re-archiving a reopened
			// case doesn't count a custom pattern's earlier hits again.
			if start < 0 || start < last || s[start:end] == mask {
				continue
			}
			b.WriteString(s[last:start])
			b.WriteString(mask)
			last = end
			n++
		}
		if n == 0 {
			return
		}
		b.WriteString(s[last:])
		s = b.String()
		if counts == nil {
			counts = map[string]int{}
		}
		counts[r.kind] += n
	}
	for _, r := range builtinRedactRules {
		apply(r)
	}
	for _, re := range extra {
		g := 0
		if re.NumSubexp() > 0 {
			g = 1
		}
		apply(redactRule{kind: "custom", re: re, group: g})
	}
	return s, counts
}

// redactDir masks secrets in every text file under dir, rewriting only the
// files that change. Binary files (evidence screenshots and the like) are
// skipped. Returns the combined per-kind counts.
func redactDir(dir string, extra []*regexp.Regexp) (map[string]int, error) {
	total := map[string]int{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return nil
		}
		out, counts := redactText(string(data), extra)
		if counts == nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(out), info.Mode().Perm()); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return err
		}
		for k, n := range counts {
			total[k] += n
		}
		return nil
	})
	return total, err
}

// formatRedactions renders counts as "kind×n, kind×n" in a stable order.
func formatRedactions(counts map[string]int) string {
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = k + "×" + strconv.Itoa(counts[k])
	}
	return strings.Join(parts, ", ")
}
