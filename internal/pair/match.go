// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package pair

import "strings"

// MatchStopSignal reports whether body contains a line whose trimmed content
// equals signal (case-insensitive). This is the own-line match rule from
// PAIRING_SPEC §6.3: `LGTM with one nit` on a line does NOT match;
// `LGTM`, `lgtm.`... wait — punctuation matters here only insofar as it
// changes the trimmed content. `LGTM.` is *not* equal to `LGTM` after trim
// (TrimSpace only strips whitespace), so it would not match the default
// `LGTM` signal. Tests below pin this behaviour.
//
// An empty signal never matches.
func MatchStopSignal(body, signal string) bool {
	signal = strings.TrimSpace(signal)
	if signal == "" {
		return false
	}
	wantLower := strings.ToLower(signal)
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.ToLower(t) == wantLower {
			return true
		}
	}
	return false
}
