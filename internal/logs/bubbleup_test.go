// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"fmt"
	"testing"

	"github.com/wingedpig/trellis/internal/config"
)

func mustFilter(t *testing.T, q string) *Filter {
	t.Helper()
	f, err := ParseFilter(q)
	if err != nil {
		t.Fatalf("ParseFilter(%q): %v", q, err)
	}
	return f
}

func entry(level LogLevel, fields map[string]any) LogEntry {
	return LogEntry{Level: level, Fields: fields}
}

func findValue(res BubbleUpResult, field, value string) (BubbleUpValue, bool) {
	for _, v := range res.Values {
		if v.Field == field && v.Value == value {
			return v, true
		}
	}
	return BubbleUpValue{}, false
}

// webpostEntries models the groups.io scenario: errors cluster on app02 and
// one source line, while normal traffic is spread evenly.
func webpostEntries() []LogEntry {
	var out []LogEntry
	for i := 0; i < 1000; i++ {
		host := "app01-g2"
		if i%2 == 0 {
			host = "app02-g2"
		}
		out = append(out, entry(LevelInfo, map[string]any{
			"host.name": host,
			"file_line": "web/handlers.go:88",
			"trace_id":  fmt.Sprintf("t%d", i),
			"time":      fmt.Sprintf("2026-10-27T13:%02d:00Z", i%60),
		}))
	}
	for i := 0; i < 100; i++ {
		host := "app02-g2"
		if i%10 == 0 {
			host = "app01-g2"
		}
		out = append(out, entry(LevelError, map[string]any{
			"host.name": host,
			"file_line": "messageserverd/attach.go:212",
			"trace_id":  fmt.Sprintf("e%d", i),
			"time":      fmt.Sprintf("2026-10-27T13:%02d:30Z", i%60),
			"error":     "s3: RequestTimeout",
		}))
	}
	return out
}

func TestBubbleUpRanksOverrepresentedValues(t *testing.T) {
	bu := NewBubbleUp(BubbleUpOptions{Selection: mustFilter(t, "level:error")})
	for _, e := range webpostEntries() {
		bu.Add(e)
	}
	res := bu.Result()

	if res.SelectionCount != 100 || res.BaselineCount != 1000 {
		t.Fatalf("counts = %d/%d, want 100/1000", res.SelectionCount, res.BaselineCount)
	}

	v, ok := findValue(res, "file_line", "messageserverd/attach.go:212")
	if !ok {
		t.Fatalf("attach.go:212 not ranked: %+v", res.Values)
	}
	if v.SelectionPct != 100 || v.BaselinePct != 0 || v.Diff != 100 {
		t.Errorf("attach.go:212 = %+v, want 100%% vs 0%%", v)
	}

	v, ok = findValue(res, "host.name", "app02-g2")
	if !ok {
		t.Fatalf("app02-g2 not ranked: %+v", res.Values)
	}
	if v.SelectionPct != 90 || v.BaselinePct != 50 {
		t.Errorf("app02-g2 = %.1f%% vs %.1f%%, want 90%% vs 50%%", v.SelectionPct, v.BaselinePct)
	}

	// Underrepresented baseline value shows up with a negative diff.
	v, ok = findValue(res, "file_line", "web/handlers.go:88")
	if !ok || v.Diff >= 0 {
		t.Errorf("handlers.go:88 should rank as underrepresented, got %+v (found=%v)", v, ok)
	}

	// Overrepresented values lead, strongest first; underrepresented follow.
	if res.Values[0].Diff != 100 {
		t.Errorf("top value = %+v, want a +100 value", res.Values[0])
	}
	seenNegative := false
	for i, v := range res.Values {
		if v.Diff < 0 {
			seenNegative = true
		} else if seenNegative {
			t.Errorf("positive value %+v ranked after a negative one", v)
		}
		if i > 0 && v.Diff > 0 && v.Diff > res.Values[i-1].Diff {
			t.Errorf("positive values not in descending order at %d: %+v", i, res.Values)
		}
	}
}

func TestBubbleUpSkipsHighCardinalityAndTimestamps(t *testing.T) {
	bu := NewBubbleUp(BubbleUpOptions{Selection: mustFilter(t, "level:error")})
	for _, e := range webpostEntries() {
		bu.Add(e)
	}
	res := bu.Result()

	for _, v := range res.Values {
		if v.Field == "trace_id" || v.Field == "time" {
			t.Errorf("unexpected ranked field %q", v.Field)
		}
	}
	found := false
	for _, f := range res.HighCardinality {
		if f == "trace_id" {
			found = true
		}
	}
	if !found {
		t.Errorf("trace_id not reported as high-cardinality: %v", res.HighCardinality)
	}
}

func TestBubbleUpBaselineFilterAndFieldRestriction(t *testing.T) {
	bu := NewBubbleUp(BubbleUpOptions{
		Selection: mustFilter(t, "level:error"),
		Baseline:  mustFilter(t, "host.name:app02-g2"),
		Fields:    []string{"host.name"},
	})
	for _, e := range webpostEntries() {
		bu.Add(e)
	}
	res := bu.Result()

	if res.BaselineCount != 500 {
		t.Errorf("baseline = %d, want 500 (app02 non-errors only)", res.BaselineCount)
	}
	for _, v := range res.Values {
		if v.Field != "host.name" {
			t.Errorf("field %q ranked despite Fields restriction", v.Field)
		}
	}
}

func TestBubbleUpPerFieldCapAndNestedFields(t *testing.T) {
	bu := NewBubbleUp(BubbleUpOptions{Selection: mustFilter(t, "level:error"), PerField: 1})
	for i := 0; i < 40; i++ {
		bu.Add(entry(LevelError, map[string]any{
			"group": map[string]any{"plan": "premium"},
			"code":  fmt.Sprintf("c%d", i%2),
		}))
		bu.Add(entry(LevelInfo, map[string]any{
			"group": map[string]any{"plan": "free"},
			"code":  "c9",
		}))
	}
	res := bu.Result()

	if _, ok := findValue(res, "group.plan", "premium"); !ok {
		t.Errorf("nested field not flattened to group.plan: %+v", res.Values)
	}
	perField := map[string]int{}
	for _, v := range res.Values {
		perField[v.Field]++
	}
	for f, n := range perField {
		if n > 1 {
			t.Errorf("field %s has %d values, PerField is 1", f, n)
		}
	}
}

func TestBubbleUpEmptySelection(t *testing.T) {
	bu := NewBubbleUp(BubbleUpOptions{Selection: mustFilter(t, "level:fatal")})
	for _, e := range webpostEntries() {
		bu.Add(e)
	}
	res := bu.Result()
	if res.SelectionCount != 0 || len(res.Values) != 0 {
		t.Errorf("empty selection should yield no values, got %+v", res)
	}
}

func TestBubbleUpMaxDistinctMarksApproximate(t *testing.T) {
	bu := NewBubbleUp(BubbleUpOptions{Selection: mustFilter(t, "level:error"), MaxDistinct: 5})
	for i := 0; i < 10; i++ {
		bu.Add(entry(LevelInfo, map[string]any{"group.id": fmt.Sprintf("g%d", i%8)}))
	}
	bu.Add(entry(LevelError, map[string]any{"group.id": "g1"}))
	res := bu.Result()
	if len(res.Approximate) != 1 || res.Approximate[0] != "group.id" {
		t.Errorf("approximate = %v, want [group.id]", res.Approximate)
	}
}

func TestDeriveReplace(t *testing.T) {
	d := NewDeriver(map[string]config.DeriveConfig{
		"route": {From: "url", Op: "replace", Args: map[string]any{
			"pattern": "^/g/[^/]+/", "replacement": "/g/*/",
		}},
		"url": {From: "url", Op: "replace", Args: map[string]any{
			"pattern": `/\d+`, "replacement": "/:id",
		}},
		"bad": {From: "url", Op: "replace", Args: map[string]any{"pattern": "("}},
	})
	e := LogEntry{Fields: map[string]any{"url": "/g/beta-testers/topic/12345"}}
	d.Apply(&e)

	if got := e.Fields["route"]; got != "/g/*/topic/12345" && got != "/g/*/topic/:id" {
		t.Errorf("route = %v", got)
	}
	if got := e.Fields["url"]; got != "/g/beta-testers/topic/:id" {
		t.Errorf("in-place url = %v, want /g/beta-testers/topic/:id", got)
	}
	if got := e.Fields["bad"]; got != "/g/beta-testers/topic/12345" && got != "/g/beta-testers/topic/:id" {
		t.Errorf("invalid pattern should pass the value through, got %v", got)
	}
}
