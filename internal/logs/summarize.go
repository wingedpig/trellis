// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FieldValue looks up a named value in an entry: "message"/"msg", "level",
// a top-level field, or a dotted path into nested fields ("http.status").
func FieldValue(e LogEntry, name string) (any, bool) {
	switch name {
	case "message", "msg":
		return e.Message, e.Message != ""
	case "level":
		if v, ok := e.Fields["level"]; ok {
			return v, true
		}
		return string(e.Level), e.Level != ""
	}
	if v, ok := e.Fields[name]; ok {
		return v, true
	}
	if !strings.Contains(name, ".") {
		return nil, false
	}
	var cur any = e.Fields
	for _, part := range strings.Split(name, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// NumericValue converts a field value to a number. Numbers and numeric
// strings are taken as they are; Go duration strings ("1.5s", "250ms") are
// converted to milliseconds, so a duration field reads the same whether it
// was logged as a number of ms or as a duration.
func NumericValue(v any) (float64, bool) {
	switch tv := v.(type) {
	case float64:
		return tv, true
	case float32:
		return float64(tv), true
	case int:
		return float64(tv), true
	case int64:
		return float64(tv), true
	case int32:
		return float64(tv), true
	case uint64:
		return float64(tv), true
	case json.Number:
		f, err := tv.Float64()
		return f, err == nil
	case string:
		s := strings.TrimSpace(tv)
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, true
		}
		if d, err := time.ParseDuration(s); err == nil {
			return float64(d) / float64(time.Millisecond), true
		}
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Numeric stats

// StatsOptions configures a Stats aggregation.
type StatsOptions struct {
	// Field is the numeric field to summarize. Required.
	Field string
	// By lists fields whose values group the results (empty = one group).
	By []string
	// Where optionally restricts which entries are counted.
	Where *Filter
	// MaxGroups caps distinct groups; entries for groups first seen after
	// the cap are pooled into one "(other)" group. Default 10000.
	MaxGroups int
	// MaxSamples caps the values kept per group for percentiles; past it a
	// uniform reservoir sample is kept and the percentiles are approximate.
	// Count, sum, mean, min and max stay exact. Default 200000.
	MaxSamples int
}

// Stats computes count, mean, min, max and percentiles of a numeric field,
// grouped by the values of other fields.
type Stats struct {
	opts    StatsOptions
	groups  map[string]*statsGroup
	matched int // entries passing Where
	missing int // matched entries without a numeric Field
}

type statsGroup struct {
	key     []string
	count   int
	sum     float64
	min     float64
	max     float64
	samples []float64
	sampled bool
}

// StatsGroup is one group's summary. Values are in the field's own unit
// (milliseconds for duration strings).
type StatsGroup struct {
	Key   []string `json:"key,omitempty"`
	Count int      `json:"count"`
	Sum   float64  `json:"sum"`
	Mean  float64  `json:"mean"`
	Min   float64  `json:"min"`
	Max   float64  `json:"max"`
	P50   float64  `json:"p50"`
	P90   float64  `json:"p90"`
	P99   float64  `json:"p99"`
	// Approximate is set when percentiles come from a sample.
	Approximate bool `json:"approximate,omitempty"`
}

// StatsResult is the outcome of a Stats aggregation. Groups are sorted by
// count, largest first.
type StatsResult struct {
	Field   string       `json:"field"`
	By      []string     `json:"by,omitempty"`
	Matched int          `json:"matched"`
	Missing int          `json:"missing"`
	Groups  []StatsGroup `json:"groups"`
	// Overflow is set when more than MaxGroups groups were seen and the rest
	// were pooled into "(other)".
	Overflow bool `json:"overflow,omitempty"`
}

const otherGroup = "(other)"

// NewStats returns an empty Stats aggregation.
func NewStats(opts StatsOptions) *Stats {
	if opts.MaxGroups <= 0 {
		opts.MaxGroups = 10000
	}
	if opts.MaxSamples <= 0 {
		opts.MaxSamples = 200000
	}
	return &Stats{opts: opts, groups: make(map[string]*statsGroup)}
}

// Add counts one entry.
func (s *Stats) Add(e LogEntry) {
	if s.opts.Where != nil && !s.opts.Where.Match(e) {
		return
	}
	s.matched++
	raw, ok := FieldValue(e, s.opts.Field)
	if !ok {
		s.missing++
		return
	}
	v, ok := NumericValue(raw)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		s.missing++
		return
	}

	key := make([]string, len(s.opts.By))
	for i, f := range s.opts.By {
		if fv, ok := FieldValue(e, f); ok {
			key[i] = stringValue(fv)
		}
		if key[i] == "" {
			key[i] = "(none)"
		}
	}
	id := strings.Join(key, "\x00")
	g := s.groups[id]
	if g == nil {
		if len(s.groups) >= s.opts.MaxGroups {
			id = otherGroup
			if g = s.groups[id]; g == nil {
				g = &statsGroup{key: []string{otherGroup}, min: v, max: v}
				s.groups[id] = g
			}
		} else {
			g = &statsGroup{key: key, min: v, max: v}
			s.groups[id] = g
		}
	}
	g.add(v, s.opts.MaxSamples)
}

func (g *statsGroup) add(v float64, maxSamples int) {
	g.count++
	g.sum += v
	g.min = min(g.min, v)
	g.max = max(g.max, v)
	if len(g.samples) < maxSamples {
		g.samples = append(g.samples, v)
		return
	}
	// Reservoir sampling (Algorithm R): every value seen so far has an equal
	// chance of being in the sample.
	g.sampled = true
	if j := rand.IntN(g.count); j < maxSamples {
		g.samples[j] = v
	}
}

// Result returns the per-group summaries.
func (s *Stats) Result() StatsResult {
	res := StatsResult{Field: s.opts.Field, By: s.opts.By, Matched: s.matched, Missing: s.missing, Groups: []StatsGroup{}}
	for id, g := range s.groups {
		if id == otherGroup {
			res.Overflow = true
		}
		sort.Float64s(g.samples)
		sg := StatsGroup{
			Count: g.count, Sum: g.sum, Mean: g.sum / float64(g.count),
			Min: g.min, Max: g.max,
			P50: percentile(g.samples, 50), P90: percentile(g.samples, 90), P99: percentile(g.samples, 99),
			Approximate: g.sampled,
		}
		if len(s.opts.By) > 0 || id == otherGroup {
			sg.Key = g.key
		}
		res.Groups = append(res.Groups, sg)
	}
	sort.SliceStable(res.Groups, func(i, j int) bool {
		if res.Groups[i].Count != res.Groups[j].Count {
			return res.Groups[i].Count > res.Groups[j].Count
		}
		return strings.Join(res.Groups[i].Key, "\x00") < strings.Join(res.Groups[j].Key, "\x00")
	})
	return res
}

// percentile returns the nearest-rank p-th percentile of sorted values.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	return sorted[max(rank, 1)-1]
}

// ---------------------------------------------------------------------------
// Per-request summary

// NamedPattern is a regexp counted per request under a name.
type NamedPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

// ParseNamedPattern parses "name=regexp". Without "name=", the pattern itself
// is the name.
func ParseNamedPattern(s string) (NamedPattern, error) {
	name, pat := s, s
	if i := strings.Index(s, "="); i > 0 {
		name, pat = s[:i], s[i+1:]
	}
	re, err := CompileGrep(pat)
	if err != nil {
		return NamedPattern{}, fmt.Errorf("invalid pattern %q: %w", pat, err)
	}
	return NamedPattern{Name: name, Pattern: re}, nil
}

// RequestsOptions configures a request summary.
type RequestsOptions struct {
	// IDField names the field holding the request ID. Either IDField or
	// IDPattern is required.
	IDField string
	// IDPattern extracts the request ID from the raw line: its first
	// capture group, or the whole match when it has none.
	IDPattern *regexp.Regexp
	// Start matches a request's start line; only requests whose start line
	// was seen in the window are reported. Required.
	Start *regexp.Regexp
	// End optionally matches a request's end line.
	End *regexp.Regexp
	// Counts are patterns counted over each request's lines.
	Counts []NamedPattern
	// MaxRequests caps the request IDs tracked at once. Default 500000.
	MaxRequests int
}

// Requests builds one summary per request: the fields of its start and end
// lines and how many of its lines matched each named pattern.
type Requests struct {
	opts      RequestsOptions
	reqs      map[string]*requestState
	truncated bool
}

type requestState struct {
	started  bool
	start    time.Time
	end      time.Time
	startF   map[string]any
	endF     map[string]any
	startMsg string
	endMsg   string
	ended    bool
	lines    int
	counts   map[string]int
	sources  map[string]bool
}

// RequestSummary is one request's summary.
type RequestSummary struct {
	ID          string         `json:"id"`
	Start       time.Time      `json:"start"`
	End         *time.Time     `json:"end,omitempty"`
	DurationMS  *float64       `json:"duration_ms,omitempty"`
	StartMsg    string         `json:"start_message,omitempty"`
	EndMsg      string         `json:"end_message,omitempty"`
	StartFields map[string]any `json:"start_fields,omitempty"`
	EndFields   map[string]any `json:"end_fields,omitempty"`
	Lines       int            `json:"lines"`
	Counts      map[string]int `json:"counts,omitempty"`
	Sources     []string       `json:"sources,omitempty"`
}

// RequestsResult is the outcome of a request summary, requests ordered by
// start time.
type RequestsResult struct {
	Requests []RequestSummary `json:"requests"`
	// Total is the number of requests whose start line matched; Requests
	// holds at most the limit passed to Result.
	Total   int            `json:"total"`
	WithEnd int            `json:"with_end"`
	Counts  map[string]int `json:"counts,omitempty"` // summed over Total
	// Truncated is set when MaxRequests was reached and later request IDs
	// were dropped.
	Truncated bool `json:"truncated,omitempty"`
}

// NewRequests returns an empty request summary.
func NewRequests(opts RequestsOptions) *Requests {
	if opts.MaxRequests <= 0 {
		opts.MaxRequests = 500000
	}
	return &Requests{opts: opts, reqs: make(map[string]*requestState)}
}

// Prefilter returns a Go regexp matching every line the summary can use (a
// start, end or counted line), so a source can drop the rest before they are
// parsed. Lines are still matched against the individual patterns.
func (r *Requests) Prefilter() string {
	parts := []string{r.opts.Start.String()}
	if r.opts.End != nil {
		parts = append(parts, r.opts.End.String())
	}
	for _, c := range r.opts.Counts {
		parts = append(parts, c.Pattern.String())
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(?:" + strings.Join(parts, ")|(?:") + ")"
}

func (r *Requests) id(e LogEntry, line string) string {
	if r.opts.IDField != "" {
		v, ok := FieldValue(e, r.opts.IDField)
		if !ok {
			return ""
		}
		return stringValue(v)
	}
	m := r.opts.IDPattern.FindStringSubmatch(line)
	switch {
	case m == nil:
		return ""
	case len(m) > 1:
		return m[1]
	default:
		return m[0]
	}
}

// Add counts one entry.
func (r *Requests) Add(e LogEntry) {
	line := e.Raw
	if line == "" {
		line = e.Message
	}
	id := r.id(e, line)
	if id == "" {
		return
	}
	isStart := r.opts.Start.MatchString(line)
	isEnd := r.opts.End != nil && r.opts.End.MatchString(line)
	var counted []string
	for _, c := range r.opts.Counts {
		if c.Pattern.MatchString(line) {
			counted = append(counted, c.Name)
		}
	}
	if !isStart && !isEnd && len(counted) == 0 {
		return
	}

	st := r.reqs[id]
	if st == nil {
		if len(r.reqs) >= r.opts.MaxRequests {
			r.truncated = true
			return
		}
		st = &requestState{counts: map[string]int{}, sources: map[string]bool{}}
		r.reqs[id] = st
	}
	st.lines++
	if e.Source != "" {
		st.sources[e.Source] = true
	}
	// Viewers stream concurrently, so lines arrive out of order: keep the
	// earliest start line and the latest end line.
	if isStart && (!st.started || e.Timestamp.Before(st.start)) {
		st.started = true
		st.start = e.Timestamp
		st.startF = e.Fields
		st.startMsg = e.Message
	}
	if isEnd && (!st.ended || e.Timestamp.After(st.end)) {
		st.ended = true
		st.end = e.Timestamp
		st.endF = e.Fields
		st.endMsg = e.Message
	}
	for _, name := range counted {
		st.counts[name]++
	}
}

// Result returns the requests whose start line was seen, ordered by start
// time, at most limit of them (0 = all).
func (r *Requests) Result(limit int) RequestsResult {
	res := RequestsResult{Requests: []RequestSummary{}, Truncated: r.truncated}
	if len(r.opts.Counts) > 0 {
		res.Counts = map[string]int{}
	}
	for id, st := range r.reqs {
		if !st.started {
			continue
		}
		res.Total++
		s := RequestSummary{
			ID: id, Start: st.start, StartMsg: st.startMsg, StartFields: st.startF,
			Lines: st.lines,
		}
		if st.ended {
			end := st.end
			d := float64(end.Sub(st.start)) / float64(time.Millisecond)
			s.End, s.DurationMS = &end, &d
			s.EndMsg, s.EndFields = st.endMsg, st.endF
			res.WithEnd++
		}
		if len(r.opts.Counts) > 0 {
			s.Counts = map[string]int{}
			for _, c := range r.opts.Counts {
				s.Counts[c.Name] = st.counts[c.Name]
				res.Counts[c.Name] += st.counts[c.Name]
			}
		}
		for src := range st.sources {
			s.Sources = append(s.Sources, src)
		}
		sort.Strings(s.Sources)
		res.Requests = append(res.Requests, s)
	}
	sort.SliceStable(res.Requests, func(i, j int) bool {
		a, b := res.Requests[i], res.Requests[j]
		if !a.Start.Equal(b.Start) {
			return a.Start.Before(b.Start)
		}
		return a.ID < b.ID
	})
	if limit > 0 && len(res.Requests) > limit {
		res.Requests = res.Requests[:limit]
	}
	return res
}
