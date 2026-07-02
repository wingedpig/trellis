// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package checklist

import "testing"

func TestIsCompletionSignal(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		signal string
		want   bool
	}{
		{"exact", "COMPLETED", "COMPLETED", true},
		{"case insensitive", "completed", "COMPLETED", true},
		{"surrounding whitespace", "  COMPLETED  \n", "COMPLETED", true},
		{"trailing blank lines", "COMPLETED\n\n", "COMPLETED", true},
		{"phase work then signal does NOT complete", "Implemented phase 3.\nCOMPLETED", "COMPLETED", false},
		{"signal inline does not match", "All COMPLETED now", "COMPLETED", false},
		{"period breaks equality", "COMPLETED.", "COMPLETED", false},
		{"empty signal never matches", "COMPLETED", "", false},
		{"empty text", "", "COMPLETED", false},
		{"different word", "DONE", "COMPLETED", false},
		{"custom signal", "ALL_PHASES_DONE", "ALL_PHASES_DONE", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCompletionSignal(tc.text, tc.signal); got != tc.want {
				t.Fatalf("IsCompletionSignal(%q, %q) = %v, want %v", tc.text, tc.signal, got, tc.want)
			}
		})
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("\n\n  hello world  \nsecond"); got != "hello world" {
		t.Fatalf("firstLine = %q", got)
	}
	if got := firstLine(""); got != "" {
		t.Fatalf("firstLine empty = %q", got)
	}
}
