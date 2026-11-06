// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"regexp"
	"strings"
	"testing"
)

func TestTranslateToERE(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`tal01@ca\.org`, `tal01@ca\.org`},
		{`(?i)tal01@ca\.org`, `[Tt][Aa][Ll]01@[Cc][Aa]\.[Oo][Rr][Gg]`},
		{`(?i)x`, `[Xx]`},
		{`(?i)k`, "([Kk]|K)"},
		{`\d+`, `[0-9]+`},
		{`\d{3,5}`, `[0-9]{3,5}`},
		{`a{2,}`, `a{2,}`},
		{`(?:ab)+`, `(ab)+`},
		{`(foo|bar)baz`, `(foo|bar)baz`},
		{`^start|end$`, `^start|end$`},
		{`a.*?b`, `a.*b`},
		{`[^"]+`, `[^"]+`},
		{`\w+`, `[0-9A-Z_a-z]+`},
		{`\s`, "[\t\f\r ]"},
		{`[]a^-]`, `[]a^-]`},
		{`\^`, `\^`},
		{`\bword\b`, `\bword\b`},
		{`trace=web\.app01-g2\.837569\.1793982657140344307`, `trace=web\.app01-g2\.837569\.1793982657140344307`},
	}
	for _, tt := range tests {
		got, ok := translateToERE(tt.in)
		if !ok {
			t.Errorf("translateToERE(%q) not translatable", tt.in)
			continue
		}
		if got != tt.want {
			t.Errorf("translateToERE(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTranslateToEREUntranslatable(t *testing.T) {
	for _, in := range []string{`\p{Greek}`, `[α-ω]`, `\x00`, `[\x00-\x05]`} {
		if got, ok := translateToERE(in); ok {
			t.Errorf("translateToERE(%q) = %q, want untranslatable", in, got)
		}
	}
}

// The translation is a prefilter: every line the Go pattern matches must also
// match the ERE, or the remote grep would drop it before the Go check.
func TestTranslateToERENeverNarrower(t *testing.T) {
	lines := []string{
		`{"msg":"delivered","to":"TAL01@ca.org"}`,
		`{"msg":"delivered","to":"tal01@CA.ORG"}`,
		`{"msg":"delivered","to":"tal01@caXorg"}`,
		`status=500 path=/api/v1/x`,
		`status=200 path=/`,
		`id=1793982657140344307 trace_id=web.app01-g2.837569.1793982657140344307`,
		`[warn] a-b ^caret ]bracket`,
		`naïve café Straße`,
		"crlf\r",
	}
	patterns := []string{
		`(?i)tal01@ca\.org`, `tal01@ca\.org`, `status=5\d\d`, `\d{19}`,
		`(?i)(warn|error)`, `[]^-]`, `a.*?b`, `\w+@\w+`, `(?i)café`, `(?i)STRASSE|straße`,
		`path=/\s*$`, `[^a-z ]+`, `crlf\s*$`,
	}
	for _, p := range patterns {
		ere, ok := translateToERE(p)
		if !ok {
			t.Errorf("%q: not translatable", p)
			continue
		}
		posix, err := regexp.CompilePOSIX(strings.NewReplacer(`\b`, ``).Replace(ere))
		if err != nil {
			t.Errorf("%q -> %q: not valid POSIX ERE: %v", p, ere, err)
			continue
		}
		goRe := regexp.MustCompile(p)
		for _, line := range lines {
			if goRe.MatchString(line) && !posix.MatchString(line) {
				t.Errorf("%q -> %q drops line %q", p, ere, line)
			}
		}
	}
}

func TestCompileGrepRejectsInvalid(t *testing.T) {
	_, err := CompileGrep(`(unclosed`)
	if err == nil || !IsGrepPatternError(err) {
		t.Fatalf("CompileGrep(invalid) err = %v, want grep pattern error", err)
	}
}
