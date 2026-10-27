// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"math"
	"sort"
	"strings"
)

// BubbleUp compares a selection of log entries (e.g. errors) against a
// baseline (by default, every other entry in the same window) and ranks the
// field values whose share differs most between the two. It answers "what is
// different about these lines?" without the caller knowing which field to
// look at.
//
// Entries are counted as they stream in; only per-field value counts are kept,
// so memory is bounded by MaxDistinct rather than by the number of lines.
type BubbleUp struct {
	opts BubbleUpOptions

	selection int
	baseline  int

	fields  map[string]*bubbleField
	only    map[string]bool
	exclude map[string]bool
}

// BubbleUpOptions configures a BubbleUp analysis.
type BubbleUpOptions struct {
	// Selection picks the entries being investigated. Required.
	Selection *Filter
	// Baseline optionally narrows the comparison set. Entries matching the
	// selection are never counted in the baseline.
	Baseline *Filter
	// Fields restricts the analysis to these fields (empty = all fields).
	Fields []string
	// Exclude lists fields to ignore in addition to the built-in ones.
	Exclude []string
	// MaxDistinct caps the number of distinct values tracked per field.
	// Values first seen after the cap are not tracked in the baseline, and
	// the field is reported as approximate. Default 50000.
	MaxDistinct int
	// Top is the number of ranked values to return. Default 20.
	Top int
	// PerField caps how many values of a single field appear in the ranking,
	// so one field can't crowd out the rest. Default 3.
	PerField int
}

// BubbleUpValue is one ranked field value.
type BubbleUpValue struct {
	Field          string  `json:"field"`
	Value          string  `json:"value"`
	SelectionCount int     `json:"selection_count"`
	BaselineCount  int     `json:"baseline_count"`
	SelectionPct   float64 `json:"selection_pct"`
	BaselinePct    float64 `json:"baseline_pct"`
	// Diff is SelectionPct - BaselinePct: positive when the value is
	// overrepresented in the selection.
	Diff float64 `json:"diff"`
}

// BubbleUpResult is the outcome of a BubbleUp analysis.
type BubbleUpResult struct {
	SelectionCount int             `json:"selection_count"`
	BaselineCount  int             `json:"baseline_count"`
	Values         []BubbleUpValue `json:"values"`
	// HighCardinality lists fields skipped because their values are close to
	// unique per line (trace IDs, timestamps, emails).
	HighCardinality []string `json:"high_cardinality,omitempty"`
	// Approximate lists fields that hit MaxDistinct; their baseline counts
	// may undercount values first seen after the cap.
	Approximate []string `json:"approximate,omitempty"`
}

type bubbleField struct {
	sel, base   map[string]int
	selPresent  int
	basePresent int
	full        bool // hit MaxDistinct for new baseline values
}

const (
	bubbleMaxValueLen = 200
	// A field is "high cardinality" when at least this fraction of the lines
	// carrying it have a distinct value...
	bubbleHighCardRatio = 0.5
	// ...and it has been seen on at least this many lines (below this, a
	// handful of distinct values says nothing about cardinality).
	bubbleHighCardMinLines = 20
)

// builtinBubbleExclude holds fields that never carry signal for a comparison.
var builtinBubbleExclude = []string{"time", "timestamp", "ts", "@timestamp", "raw"}

// NewBubbleUp creates a BubbleUp accumulator.
func NewBubbleUp(opts BubbleUpOptions) *BubbleUp {
	if opts.MaxDistinct <= 0 {
		opts.MaxDistinct = 50000
	}
	if opts.Top <= 0 {
		opts.Top = 20
	}
	if opts.PerField <= 0 {
		opts.PerField = 3
	}
	b := &BubbleUp{
		opts:    opts,
		fields:  make(map[string]*bubbleField),
		exclude: make(map[string]bool),
	}
	for _, f := range builtinBubbleExclude {
		b.exclude[f] = true
	}
	for _, f := range opts.Exclude {
		b.exclude[f] = true
	}
	// A field the selection or baseline filters on is trivially different
	// (level:error ranks level=error at 100%), so leave it out.
	for _, f := range []*Filter{opts.Selection, opts.Baseline} {
		for _, name := range f.FieldNames() {
			b.exclude[name] = true
		}
	}
	if len(opts.Fields) > 0 {
		b.only = make(map[string]bool, len(opts.Fields))
		for _, f := range opts.Fields {
			b.only[f] = true
		}
	}
	return b
}

// Add counts one entry. Entries matching neither the selection nor the
// baseline are ignored.
func (b *BubbleUp) Add(entry LogEntry) {
	inSelection := b.opts.Selection != nil && b.opts.Selection.Match(entry)
	if !inSelection && b.opts.Baseline != nil && !b.opts.Baseline.Match(entry) {
		return
	}
	if inSelection {
		b.selection++
	} else {
		b.baseline++
	}

	_, hasLevel := entry.Fields["level"]
	if !hasLevel && entry.Level != "" {
		b.count("level", string(entry.Level), inSelection)
	}
	b.addFields("", entry.Fields, inSelection)
}

func (b *BubbleUp) addFields(prefix string, fields map[string]any, inSelection bool) {
	for k, v := range fields {
		name := k
		if prefix != "" {
			name = prefix + "." + k
		}
		if nested, ok := v.(map[string]any); ok {
			b.addFields(name, nested, inSelection)
			continue
		}
		s := stringValue(v)
		if s == "" {
			continue
		}
		b.count(name, s, inSelection)
	}
}

func (b *BubbleUp) count(name, value string, inSelection bool) {
	if b.exclude[name] || (b.only != nil && !b.only[name]) {
		return
	}
	if len(value) > bubbleMaxValueLen {
		value = value[:bubbleMaxValueLen] + "…"
	}
	f := b.fields[name]
	if f == nil {
		f = &bubbleField{sel: make(map[string]int), base: make(map[string]int)}
		b.fields[name] = f
	}
	if inSelection {
		f.selPresent++
		// Selection values are always tracked: they are what the ranking is
		// about, and the selection is normally the small side.
		if _, ok := f.sel[value]; ok || len(f.sel) < b.opts.MaxDistinct {
			f.sel[value]++
		} else {
			f.full = true
		}
		return
	}
	f.basePresent++
	if _, ok := f.base[value]; ok {
		f.base[value]++
	} else if len(f.base) < b.opts.MaxDistinct {
		f.base[value] = 1
	} else {
		f.full = true
	}
}

// Result ranks the field values seen so far.
func (b *BubbleUp) Result() BubbleUpResult {
	res := BubbleUpResult{
		SelectionCount: b.selection,
		BaselineCount:  b.baseline,
		Values:         []BubbleUpValue{},
	}
	if b.selection == 0 {
		return res
	}

	var candidates []BubbleUpValue
	names := make([]string, 0, len(b.fields))
	for name := range b.fields {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		f := b.fields[name]
		// Judge cardinality on the baseline when it is big enough: a small
		// selection looks near-unique for almost any field.
		distinct, lines := len(f.base), f.basePresent
		if lines < bubbleHighCardMinLines {
			distinct, lines = len(f.sel), f.selPresent
		}
		if highCardinality(distinct, lines) {
			res.HighCardinality = append(res.HighCardinality, name)
			continue
		}
		if f.full {
			res.Approximate = append(res.Approximate, name)
		}

		seen := make(map[string]bool, len(f.sel))
		add := func(value string) {
			if seen[value] {
				return
			}
			seen[value] = true
			v := BubbleUpValue{
				Field:          name,
				Value:          value,
				SelectionCount: f.sel[value],
				BaselineCount:  f.base[value],
				SelectionPct:   pct(f.sel[value], b.selection),
				BaselinePct:    pct(f.base[value], b.baseline),
			}
			v.Diff = v.SelectionPct - v.BaselinePct
			candidates = append(candidates, v)
		}
		for value, n := range f.sel {
			// A single matching line is noise unless the selection is tiny.
			if n < 2 && b.selection >= 2 {
				continue
			}
			add(value)
		}
		// Underrepresented values: common in the baseline, rare in the
		// selection. Only worth considering when they could plausibly rank.
		for value, n := range f.base {
			if pct(n, b.baseline) >= 5 {
				add(value)
			}
		}
	}

	// Overrepresented values come first, strongest first: they are the leads.
	// Underrepresented ones ("normal lines are absent from the errors") follow,
	// most negative first, and only fill remaining slots.
	sort.Slice(candidates, func(i, j int) bool {
		ci, cj := candidates[i], candidates[j]
		if (ci.Diff > 0) != (cj.Diff > 0) {
			return ci.Diff > 0
		}
		if di, dj := math.Abs(ci.Diff), math.Abs(cj.Diff); di != dj {
			return di > dj
		}
		if ci.Field != cj.Field {
			return ci.Field < cj.Field
		}
		return ci.Value < cj.Value
	})

	perField := make(map[string]int)
	for _, c := range candidates {
		if len(res.Values) >= b.opts.Top {
			break
		}
		// Ignore values whose share barely moves; they are not "different".
		if math.Abs(c.Diff) < 1 {
			continue
		}
		if perField[c.Field] >= b.opts.PerField {
			continue
		}
		perField[c.Field]++
		res.Values = append(res.Values, c)
	}
	return res
}

// highCardinality reports whether a field's values are close to unique per
// line, which makes per-value comparison meaningless.
func highCardinality(distinct, lines int) bool {
	return lines >= bubbleHighCardMinLines && float64(distinct) >= bubbleHighCardRatio*float64(lines)
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(n)*1000/float64(total)) / 10
}

// ParseFieldList splits a comma-separated field list, dropping blanks.
func ParseFieldList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
