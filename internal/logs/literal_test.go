// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestRequiredLiterals(t *testing.T) {
	tests := []struct {
		pattern string
		want    []string
		fold    bool
		ok      bool
	}{
		{`STRT`, []string{"STRT"}, false, true},
		{`STRT.*path=/api/`, []string{"path=/api/"}, false, true},
		{`web\.app01-g2\.837569`, []string{"web.app01-g2.837569"}, false, true},
		{`(?i)timeout`, []string{"TIMEOUT"}, true, true}, // Go stores folded literals upper-cased
		{`(STRT|END) req`, []string{" req"}, false, true},
		{`(?:STRT)|(?:END)|(?:SELECT)`, []string{"STRT", "END", "SELECT"}, false, true},
		{`status=5\d\d`, []string{"status=5"}, false, true},
		{`(abc)+xyz`, []string{"abc"}, false, true},
		{`ab`, nil, false, false},        // too short
		{`.*`, nil, false, false},        // no fixed text
		{`foo|.*`, nil, false, false},    // a branch with no fixed text
		{`(?i)café`, nil, false, false},  // non-ASCII fold
		{`x(abc)?yz`, nil, false, false}, // optional group, short runs
		{`[`, nil, false, false},         // invalid
	}
	for _, tt := range tests {
		got, fold, ok := RequiredLiterals(tt.pattern)
		if ok != tt.ok || fold != tt.fold || !slices.Equal(got, tt.want) {
			t.Errorf("RequiredLiterals(%q) = %q, fold=%v, ok=%v; want %q, %v, %v", tt.pattern, got, fold, ok, tt.want, tt.fold, tt.ok)
		}
	}
}

// TestRequiredLiteralsNeverRejectAMatch checks the prefilter's contract on
// sample lines: every line the pattern matches contains one of the literals.
func TestRequiredLiteralsNeverRejectAMatch(t *testing.T) {
	lines := []string{
		"2026-11-08T06:00:01Z STRT GET path=/api/v1/foo id=1",
		"2026-11-08T06:00:02Z END status=503 id=1 dur=12ms",
		"2026-11-08T06:00:02Z SELECT * FROM t",
		"connection TIMEOUT after 30s",
		"nothing here",
	}
	for _, pattern := range []string{`STRT.*path=/api/`, `(?i)timeout`, `status=5\d\d`, `(?:STRT)|(?:END)|(?:SELECT)`} {
		re := regexp.MustCompile(pattern)
		lits, fold, ok := RequiredLiterals(pattern)
		if !ok {
			t.Fatalf("%q: no literals", pattern)
		}
		for _, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			hay := line
			if fold {
				hay = strings.ToLower(line)
			}
			if !slices.ContainsFunc(lits, func(l string) bool {
				if fold {
					l = strings.ToLower(l)
				}
				return strings.Contains(hay, l)
			}) {
				t.Errorf("%q matches %q but no literal in %q does", pattern, line, lits)
			}
		}
	}
}
