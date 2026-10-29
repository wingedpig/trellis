// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package agentmsg

import "testing"

func TestReplyPreview(t *testing.T) {
	cases := []struct {
		text string
		max  int
		want string
	}{
		{"Done.\n\nWant me to make the `~/.claude.json` change?\n\n", 200, "Want me to make the ~/.claude.json change?"},
		{"## Summary\n- **Ship it?**", 200, "Ship it?"},
		{"Here:\n```\ncode\n```", 200, "code"},
		{"abcdef", 3, "abc…"},
		{"", 10, ""},
	}
	for _, c := range cases {
		if got := ReplyPreview(c.text, c.max); got != c.want {
			t.Errorf("ReplyPreview(%q, %d) = %q, want %q", c.text, c.max, got, c.want)
		}
	}
}
