// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package genai

import "testing"

func TestExtractJSON_handlesProseAndFences(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`{"a":1}`, `{"a":1}`},
		{"Here is the JSON:\n```json\n{\"a\":1}\n```\n", `{"a":1}`},
		{`prose before {"nested":{"x":"}"}} prose after`, `{"nested":{"x":"}"}}`},
		{"no json here", "no json here"},
		{`{"escaped":"a \" b"}`, `{"escaped":"a \" b"}`},
	}
	for _, tc := range cases {
		got := extractJSON(tc.in)
		if got != tc.want {
			t.Errorf("extractJSON(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short: %q", got)
	}
	if got := truncate("abcdefghij", 5); got != "abcde...(truncated)" {
		t.Errorf("truncate long: %q", got)
	}
}
