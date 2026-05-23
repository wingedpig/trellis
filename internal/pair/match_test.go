// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package pair

import "testing"

func TestMatchStopSignal(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		signal string
		want   bool
	}{
		// Positive matches: signal appears as its own line.
		{"plain line", "LGTM", "LGTM", true},
		{"lower case", "lgtm", "LGTM", true},
		{"surrounded by prose", "Nice work.\n\nLGTM\n", "LGTM", true},
		{"leading whitespace", "  LGTM", "LGTM", true},
		{"trailing whitespace", "LGTM   ", "LGTM", true},
		{"both whitespace", "\t LGTM\t ", "LGTM", true},

		// Negative matches: signal mixed into another line.
		{"with nit", "LGTM with one nit: see below", "LGTM", false},
		{"prefixed", "ALGTM", "LGTM", false},
		{"suffixed", "LGTMaybe", "LGTM", false},
		{"trailing period", "LGTM.", "LGTM", false},
		{"trailing punct", "LGTM!", "LGTM", false},
		{"parens", "(LGTM)", "LGTM", false},

		// No signal anywhere.
		{"empty body", "", "LGTM", false},
		{"prose only", "Looks good but a few comments...", "LGTM", false},

		// Empty signal never matches.
		{"empty signal", "anything", "", false},

		// Multi-paragraph: own line in the middle is fine.
		{"middle line", "Comment 1.\nLGTM\nbut also fix this", "LGTM", true},

		// Signal with internal whitespace (rare but should still need own-line).
		{"two-word signal own line", "looks good\nSHIP IT\nthanks", "SHIP IT", true},
		{"two-word signal mid line", "I think we should SHIP IT today", "SHIP IT", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchStopSignal(tc.body, tc.signal)
			if got != tc.want {
				t.Errorf("MatchStopSignal(%q, %q) = %v; want %v", tc.body, tc.signal, got, tc.want)
			}
		})
	}
}
