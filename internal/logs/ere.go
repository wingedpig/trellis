// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// grepPatternError reports a -grep pattern that cannot be run. It is a
// distinct type so the API layer can answer 400 instead of 500.
type grepPatternError struct{ msg string }

func (e *grepPatternError) Error() string { return e.msg }

// IsGrepPatternError reports whether err is a rejected grep pattern.
func IsGrepPatternError(err error) bool {
	_, ok := err.(*grepPatternError)
	return ok
}

// CompileGrep compiles a user grep pattern. Patterns use Go regexp (RE2)
// syntax everywhere, including (?i) for case-insensitive matching; an invalid
// pattern is an error rather than a silent literal or empty search.
func CompileGrep(pattern string) (*regexp.Regexp, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, &grepPatternError{fmt.Sprintf("invalid grep pattern %q: %v", pattern, err)}
	}
	return re, nil
}

// translateToERE rewrites a Go regexp as a POSIX extended regular expression
// for a remote `grep -E`. The translation never matches fewer lines than the
// Go pattern (non-greedy quantifiers become greedy, which matches the same
// lines), so the remote grep is a safe prefilter and the viewer re-checks
// every line with the real Go regexp. Case-insensitive parts are spelled out
// as bracket pairs ([Tt][Aa]…), so no grep -i is needed and a pattern that is
// only partly case-insensitive still works.
//
// ok is false when the pattern uses something ERE cannot express (e.g. a
// character class over a non-ASCII range); callers then skip the remote user
// grep and rely on the Go-side filter.
func translateToERE(pattern string) (ere string, ok bool) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return "", false
	}
	var b strings.Builder
	if !writeERE(&b, re, true) {
		return "", false
	}
	return b.String(), true
}

// ereMeta are the characters that must be escaped outside brackets.
const ereMeta = `.[]()*+?{}|^$\`

func writeERE(b *strings.Builder, re *syntax.Regexp, top bool) bool {
	switch re.Op {
	case syntax.OpEmptyMatch:
		return true
	case syntax.OpLiteral:
		fold := re.Flags&syntax.FoldCase != 0
		for _, r := range re.Rune {
			if !writeLiteralRune(b, r, fold) {
				return false
			}
		}
		return true
	case syntax.OpCharClass:
		return writeClass(b, re.Rune)
	case syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		b.WriteByte('.')
		return true
	case syntax.OpBeginLine, syntax.OpBeginText:
		b.WriteByte('^')
		return true
	case syntax.OpEndLine, syntax.OpEndText:
		b.WriteByte('$')
		return true
	case syntax.OpWordBoundary:
		b.WriteString(`\b`)
		return true
	case syntax.OpNoWordBoundary:
		b.WriteString(`\B`)
		return true
	case syntax.OpCapture:
		b.WriteByte('(')
		if !writeERE(b, re.Sub[0], true) {
			return false
		}
		b.WriteByte(')')
		return true
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		if !writeAtom(b, re.Sub[0]) {
			return false
		}
		switch re.Op {
		case syntax.OpStar:
			b.WriteByte('*')
		case syntax.OpPlus:
			b.WriteByte('+')
		case syntax.OpQuest:
			b.WriteByte('?')
		default:
			b.WriteByte('{')
			b.WriteString(strconv.Itoa(re.Min))
			if re.Max != re.Min {
				b.WriteByte(',')
				if re.Max >= 0 {
					b.WriteString(strconv.Itoa(re.Max))
				}
			}
			b.WriteByte('}')
		}
		return true
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if sub.Op == syntax.OpAlternate {
				if !writeGroup(b, sub) {
					return false
				}
			} else if !writeERE(b, sub, false) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		if !top {
			return writeGroup(b, re)
		}
		for i, sub := range re.Sub {
			if i > 0 {
				b.WriteByte('|')
			}
			if !writeERE(b, sub, false) {
				return false
			}
		}
		return true
	}
	// OpNoMatch and anything newer than this translator.
	return false
}

func writeGroup(b *strings.Builder, re *syntax.Regexp) bool {
	b.WriteByte('(')
	if !writeERE(b, re, true) {
		return false
	}
	b.WriteByte(')')
	return true
}

// writeAtom writes re so a following quantifier applies to all of it.
func writeAtom(b *strings.Builder, re *syntax.Regexp) bool {
	single := false
	switch re.Op {
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL, syntax.OpCapture:
		single = true
	case syntax.OpLiteral:
		// One ASCII rune renders as a single atom ("a", "\." or "[Aa]").
		single = len(re.Rune) == 1 && re.Rune[0] < utf8.RuneSelf
	}
	if single {
		return writeERE(b, re, false)
	}
	return writeGroup(b, re)
}

func writeLiteralRune(b *strings.Builder, r rune, fold bool) bool {
	if fold && r < utf8.RuneSelf && unicode.IsLetter(r) {
		lo, up := unicode.ToLower(r), unicode.ToUpper(r)
		if lo != up {
			// Go's folding also pairs k with the Kelvin sign and s with the
			// long s; keep those as alternatives so nothing is dropped.
			var extra []rune
			for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
				if f >= utf8.RuneSelf {
					extra = append(extra, f)
				}
			}
			if len(extra) > 0 {
				b.WriteByte('(')
			}
			b.WriteByte('[')
			b.WriteRune(up)
			b.WriteRune(lo)
			b.WriteByte(']')
			for _, f := range extra {
				b.WriteByte('|')
				b.WriteRune(f)
			}
			if len(extra) > 0 {
				b.WriteByte(')')
			}
			return true
		}
	}
	if r < utf8.RuneSelf {
		if r == '\n' || r == 0 {
			return false // never inside a line / can't be passed to grep
		}
		if strings.ContainsRune(ereMeta, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
		return true
	}
	// Non-ASCII literals are written as their UTF-8 bytes, which match
	// byte-for-byte in any grep locale. Case folding of non-ASCII letters is
	// left to the Go-side filter: widen to "any character" so the prefilter
	// never drops a line the Go pattern would keep.
	if fold && unicode.SimpleFold(r) != r {
		b.WriteString(`(.|`)
		b.WriteRune(r)
		b.WriteByte(')')
		return true
	}
	b.WriteRune(r)
	return true
}

// writeClass renders a Go character class (sorted lo/hi pairs) as a POSIX
// bracket expression. Classes that reach past ASCII are only expressible as
// negations of an ASCII set ([^…]); anything else is reported untranslatable.
func writeClass(b *strings.Builder, ranges []rune) bool {
	if len(ranges) == 0 {
		return false // matches nothing
	}
	maxRune := ranges[len(ranges)-1]
	if maxRune < utf8.RuneSelf {
		return writeBracket(b, ranges, false)
	}
	if maxRune == unicode.MaxRune {
		// Negated class: render its complement, which must be pure ASCII.
		var comp []rune
		next := rune(0)
		for i := 0; i < len(ranges); i += 2 {
			if ranges[i] > next {
				comp = append(comp, next, ranges[i]-1)
			}
			next = ranges[i+1] + 1
		}
		if len(comp) == 0 {
			b.WriteByte('.')
			return true
		}
		if comp[len(comp)-1] >= utf8.RuneSelf {
			return false
		}
		return writeBracket(b, comp, true)
	}
	return false
}

func writeBracket(b *strings.Builder, ranges []rune, negate bool) bool {
	// In a POSIX bracket ']' must come first, '-' last, and '^' anywhere but
	// first; backslash is literal. Pull those three out and place them.
	var hasClose, hasDash, hasCaret bool
	var body strings.Builder
	for i := 0; i < len(ranges); i += 2 {
		lo, hi := ranges[i], ranges[i+1]
		// Control characters are written as their raw bytes, except NUL
		// (which can't be passed to grep) and newline (which never occurs
		// inside a line). Leaving them out of a positive class is safe only
		// for newline; leaving anything out of a negated class only widens it.
		for ; lo <= hi && lo < 0x20; lo++ {
			switch {
			case lo != 0 && lo != '\n':
				body.WriteByte(byte(lo))
			case negate, lo == '\n':
			default:
				return false
			}
		}
		if lo > hi {
			continue
		}
		start := lo
		for r := lo; r <= hi+1; r++ {
			if r <= hi {
				switch r {
				case ']':
					hasClose = true
				case '-':
					hasDash = true
				case '^':
					hasCaret = true
				default:
					continue
				}
			}
			if start <= r-1 {
				writeRange(&body, start, r-1)
			}
			start = r + 1
		}
	}
	if !negate && body.Len() == 0 && !hasClose && !hasDash {
		if !hasCaret {
			return false // nothing left to match
		}
		b.WriteString(`\^`) // "[^]" would read as an open negation
		return true
	}
	b.WriteByte('[')
	if negate {
		b.WriteByte('^')
	}
	if hasClose {
		b.WriteByte(']')
	}
	b.WriteString(body.String())
	if hasCaret {
		b.WriteByte('^')
	}
	if hasDash {
		b.WriteByte('-')
	}
	b.WriteByte(']')
	return true
}

func writeRange(b *strings.Builder, lo, hi rune) {
	switch {
	case lo == hi:
		b.WriteRune(lo)
	case hi == lo+1:
		b.WriteRune(lo)
		b.WriteRune(hi)
	default:
		b.WriteRune(lo)
		b.WriteByte('-')
		b.WriteRune(hi)
	}
}
