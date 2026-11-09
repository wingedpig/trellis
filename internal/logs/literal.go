// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"regexp/syntax"
	"unicode/utf8"
)

// Literal prefilter limits: literals shorter than minLiteralLen filter too
// little to be worth a pass, and more than maxLiterals alternatives stop
// being cheap for grep -F.
const (
	minLiteralLen = 3
	maxLiterals   = 16
)

// RequiredLiterals returns fixed strings at least one of which appears in
// every line the Go regexp pattern matches, so `grep -F` on them can drop
// lines before the full pattern runs. foldCase reports that they must be
// matched case-insensitively. ok is false when no useful set exists (the
// pattern can match without any fixed text, or only short text).
//
// The set does not reject a line the pattern accepts. Case-folded literals
// are only offered when ASCII, where byte-wise grep -F -i under LC_ALL=C folds
// as Go does — with one exception accepted on purpose: Go's (?i) also lets
// "k" match the Kelvin sign (U+212A) and "s" match long s (U+017F), which a
// C-locale grep -i does not.
func RequiredLiterals(pattern string) (lits []string, foldCase, ok bool) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, false, false
	}
	set := required(re.Simplify())
	if set == nil || len(set.lits) == 0 || len(set.lits) > maxLiterals {
		return nil, false, false
	}
	for _, l := range set.lits {
		if utf8.RuneCountInString(l) < minLiteralLen {
			return nil, false, false
		}
	}
	return set.lits, set.fold, true
}

// litSet is a set of alternatives, one of which every match contains.
type litSet struct {
	lits []string
	fold bool
}

// score ranks a set by how selective it is: its shortest literal, with a
// small penalty per extra alternative.
func (s *litSet) score() int {
	if s == nil || len(s.lits) == 0 {
		return -1
	}
	shortest := len(s.lits[0])
	for _, l := range s.lits[1:] {
		shortest = min(shortest, len(l))
	}
	return shortest*4 - len(s.lits)
}

func better(a, b *litSet) *litSet {
	if b.score() > a.score() {
		return b
	}
	return a
}

// literalOf returns a literal node's text, or ok=false when it can't be used
// (case-folded non-ASCII).
func literalOf(re *syntax.Regexp) (text string, fold, ok bool) {
	fold = re.Flags&syntax.FoldCase != 0
	for _, r := range re.Rune {
		if fold && r >= utf8.RuneSelf {
			return "", false, false
		}
	}
	return string(re.Rune), fold, true
}

// required computes the literal set for re, or nil when there is none.
func required(re *syntax.Regexp) *litSet {
	switch re.Op {
	case syntax.OpLiteral:
		text, fold, ok := literalOf(re)
		if !ok || text == "" {
			return nil
		}
		return &litSet{lits: []string{text}, fold: fold}

	case syntax.OpCapture:
		return required(re.Sub[0])

	case syntax.OpPlus:
		return required(re.Sub[0])

	case syntax.OpRepeat:
		if re.Min >= 1 {
			return required(re.Sub[0])
		}
		return nil

	case syntax.OpConcat:
		// Adjacent literals form one longer run ("abc" "def" -> "abcdef");
		// any other node ends the run but may carry a requirement of its
		// own. Keep whichever candidate is most selective.
		var best *litSet
		var run []rune
		runFold := false
		flush := func() {
			if len(run) > 0 {
				best = better(best, &litSet{lits: []string{string(run)}, fold: runFold})
			}
			run, runFold = nil, false
		}
		for _, sub := range re.Sub {
			if sub.Op == syntax.OpLiteral {
				text, fold, ok := literalOf(sub)
				if !ok {
					flush()
					continue
				}
				if len(run) > 0 && fold != runFold {
					flush()
				}
				run = append(run, []rune(text)...)
				runFold = fold
				continue
			}
			flush()
			best = better(best, required(sub))
		}
		flush()
		return best

	case syntax.OpAlternate:
		// Every branch must contribute, or a line could match through a
		// branch with no fixed text.
		out := &litSet{}
		for _, sub := range re.Sub {
			s := required(sub)
			if s == nil {
				return nil
			}
			out.lits = append(out.lits, s.lits...)
			out.fold = out.fold || s.fold
			if len(out.lits) > maxLiterals {
				return nil
			}
		}
		return out
	}
	return nil
}
