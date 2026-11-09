// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"fmt"
	"regexp"
	"testing"
	"time"
)

func TestNumericValue(t *testing.T) {
	tests := []struct {
		in   any
		want float64
		ok   bool
	}{
		{float64(12.5), 12.5, true},
		{int64(7), 7, true},
		{"42", 42, true},
		{"1.5s", 1500, true},
		{"250ms", 250, true},
		{"fast", 0, false},
		{nil, 0, false},
	}
	for _, tt := range tests {
		got, ok := NumericValue(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("NumericValue(%v) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestFieldValueNested(t *testing.T) {
	e := LogEntry{Message: "m", Level: LevelError, Fields: map[string]any{"http": map[string]any{"status": 503}}}
	if v, ok := FieldValue(e, "http.status"); !ok || v != 503 {
		t.Errorf("http.status = %v, %v", v, ok)
	}
	if v, ok := FieldValue(e, "level"); !ok || v != "error" {
		t.Errorf("level = %v, %v", v, ok)
	}
	if _, ok := FieldValue(e, "http.missing"); ok {
		t.Error("http.missing found")
	}
}

func TestStatsPercentilesByGroup(t *testing.T) {
	where, _ := ParseFilter("level:info")
	s := NewStats(StatsOptions{Field: "duration", By: []string{"route"}, Where: where})
	for i := 1; i <= 100; i++ {
		s.Add(LogEntry{Level: LevelInfo, Fields: map[string]any{"route": "/a", "duration": float64(i)}})
	}
	for i := 1; i <= 10; i++ {
		s.Add(LogEntry{Level: LevelInfo, Fields: map[string]any{"route": "/b", "duration": fmt.Sprintf("%dms", i*10)}})
	}
	s.Add(LogEntry{Level: LevelInfo, Fields: map[string]any{"route": "/a"}})                            // missing
	s.Add(LogEntry{Level: LevelError, Fields: map[string]any{"route": "/a", "duration": float64(1e6)}}) // filtered out
	s.Add(LogEntry{Level: LevelInfo, Fields: map[string]any{"duration": float64(5)}})                   // no route

	res := s.Result()
	if res.Matched != 112 || res.Missing != 1 {
		t.Errorf("matched=%d missing=%d, want 112, 1", res.Matched, res.Missing)
	}
	if len(res.Groups) != 3 {
		t.Fatalf("groups = %+v", res.Groups)
	}
	a := res.Groups[0]
	if a.Key[0] != "/a" || a.Count != 100 || a.P50 != 50 || a.P90 != 90 || a.P99 != 99 || a.Min != 1 || a.Max != 100 || a.Mean != 50.5 {
		t.Errorf("/a = %+v", a)
	}
	b := res.Groups[1]
	if b.Key[0] != "/b" || b.Count != 10 || b.P50 != 50 || b.Max != 100 {
		t.Errorf("/b = %+v", b)
	}
	if res.Groups[2].Key[0] != "(none)" {
		t.Errorf("third group = %+v, want (none)", res.Groups[2])
	}
}

func TestStatsSamplingAndOverflow(t *testing.T) {
	s := NewStats(StatsOptions{Field: "d", By: []string{"k"}, MaxSamples: 100, MaxGroups: 2})
	for i := range 10000 {
		s.Add(LogEntry{Fields: map[string]any{"k": "x", "d": float64(i % 1000)}})
	}
	s.Add(LogEntry{Fields: map[string]any{"k": "y", "d": float64(1)}})
	s.Add(LogEntry{Fields: map[string]any{"k": "z", "d": float64(2)}})
	res := s.Result()
	x := res.Groups[0]
	if !x.Approximate || x.Count != 10000 || x.Max != 999 || x.Min != 0 {
		t.Errorf("x = %+v, want exact count/min/max with approximate percentiles", x)
	}
	if x.P50 < 300 || x.P50 > 700 {
		t.Errorf("sampled p50 = %v, want near 500", x.P50)
	}
	if !res.Overflow {
		t.Error("third group should overflow into (other)")
	}
}

func TestRequestsSummary(t *testing.T) {
	base := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	db, _ := ParseNamedPattern("db=SELECT|UPDATE")
	retry, _ := ParseNamedPattern("retry")
	r := NewRequests(RequestsOptions{
		IDField: "rid",
		Start:   regexp.MustCompile(`STRT .*path=/api/`),
		End:     regexp.MustCompile(`END`),
		Counts:  []NamedPattern{db, retry},
	})
	entry := func(ms int, rid, raw string, fields map[string]any) LogEntry {
		f := map[string]any{"rid": rid}
		for k, v := range fields {
			f[k] = v
		}
		return LogEntry{Timestamp: at(ms), Raw: raw, Message: raw, Fields: f, Source: "web"}
	}
	// Lines arrive out of order across viewers: the END for r1 first.
	r.Add(entry(120, "r1", "END status=200", map[string]any{"status": 200}))
	r.Add(entry(0, "r1", "STRT GET path=/api/x", map[string]any{"user": "u1"}))
	r.Add(entry(10, "r1", "SELECT 1", nil))
	r.Add(entry(20, "r1", "UPDATE t", nil))
	r.Add(entry(30, "r1", "retry", nil))
	r.Add(entry(5, "r2", "STRT GET path=/api/y", nil)) // no END
	r.Add(entry(6, "r2", "SELECT 2", nil))
	r.Add(entry(7, "r3", "STRT GET path=/static/z", nil)) // start doesn't match
	r.Add(entry(8, "r3", "SELECT 3", nil))
	r.Add(LogEntry{Timestamp: at(9), Raw: "STRT path=/api/ no id"})

	res := r.Result(0)
	if res.Total != 2 || res.WithEnd != 1 {
		t.Fatalf("total=%d with_end=%d, want 2, 1", res.Total, res.WithEnd)
	}
	q := res.Requests[0]
	if q.ID != "r1" || q.DurationMS == nil || *q.DurationMS != 120 || q.StartFields["user"] != "u1" || q.EndFields["status"] != 200 {
		t.Errorf("r1 = %+v", q)
	}
	if q.Counts["db"] != 2 || q.Counts["retry"] != 1 {
		t.Errorf("r1 counts = %v", q.Counts)
	}
	if res.Requests[1].ID != "r2" || res.Requests[1].End != nil || res.Requests[1].Counts["db"] != 1 {
		t.Errorf("r2 = %+v", res.Requests[1])
	}
	if res.Counts["db"] != 3 || res.Counts["retry"] != 1 {
		t.Errorf("totals = %v (r3 must not count)", res.Counts)
	}
	if got := r.Result(1); len(got.Requests) != 1 || got.Total != 2 {
		t.Errorf("limit 1: %d requests, total %d", len(got.Requests), got.Total)
	}

	pre := regexp.MustCompile(r.Prefilter())
	for _, l := range []string{"STRT GET path=/api/x", "END", "SELECT 1", "retry"} {
		if !pre.MatchString(l) {
			t.Errorf("prefilter %q drops %q", r.Prefilter(), l)
		}
	}
}

func TestRequestsIDPattern(t *testing.T) {
	r := NewRequests(RequestsOptions{
		IDPattern: regexp.MustCompile(`trace=(\S+)`),
		Start:     regexp.MustCompile(`STRT`),
	})
	r.Add(LogEntry{Raw: "STRT trace=web.1.2"})
	r.Add(LogEntry{Raw: "STRT no trace here"})
	res := r.Result(0)
	if res.Total != 1 || res.Requests[0].ID != "web.1.2" {
		t.Errorf("res = %+v", res)
	}
}
