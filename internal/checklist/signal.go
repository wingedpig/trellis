// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package checklist

import "strings"

// IsCompletionSignal reports whether text is the implementer's "no phases
// left" reply. Stricter than pair.MatchStopSignal: the reply must consist of a
// *single* non-empty line equal to signal (case-insensitive, whitespace
// trimmed). Requiring the sentinel to stand alone prevents a final phase that
// also produced work in the same turn from being mistaken for completion and
// skipping its review (PHASE_LOOP_SPEC §5.2).
//
// An empty signal never matches.
func IsCompletionSignal(text, signal string) bool {
	signal = strings.TrimSpace(signal)
	if signal == "" {
		return false
	}
	var lines []string
	for _, ln := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			lines = append(lines, t)
		}
	}
	return len(lines) == 1 && strings.EqualFold(lines[0], signal)
}

// firstLine returns the first non-empty trimmed line of s, capped for display.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			if len(t) > 200 {
				return t[:200] + "…"
			}
			return t
		}
	}
	return ""
}
