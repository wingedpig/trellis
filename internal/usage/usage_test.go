// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package usage

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTranscript(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	var data []byte
	for _, l := range lines {
		data = append(data, []byte(l+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func assistantLine(msgID, reqID, model, cwd, sessionID, ts string, input, output, cache5m, cache1h, cacheRead int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"cwd":%q,"sessionId":%q,"requestId":%q,"message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
		ts, cwd, sessionID, reqID, msgID, model, input, output, cache5m+cache1h, cacheRead, cache5m, cache1h)
}

func TestParseAndDedup(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "-tmp-proj")
	ts := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)

	line := assistantLine("msg_1", "req_1", "claude-opus-4-8", "/tmp/proj", "sess1", ts, 1000, 1000, 1000, 1000, 1000)
	writeTranscript(t, projDir, "sess1.jsonl",
		line,
		line, // duplicate (streamed content blocks repeat usage) — must dedup
		`{"type":"user","timestamp":"`+ts+`","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","timestamp":"`+ts+`","message":{"id":"msg_2","model":"<synthetic>","usage":{"input_tokens":5,"output_tokens":5}}}`,
	)

	m := NewManagerWithDirs([]string{root}, nil)
	entries := m.Entries(time.Time{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after dedup, got %d", len(entries))
	}

	e := entries[0]
	// Opus 4.8: $5/MTok in, $25/MTok out; cache read 0.1×, 5m write 1.25×, 1h write 2×.
	want := 5.0/1e6*(1000+1.25*1000+2*1000+0.1*1000) + 25.0/1e6*1000
	if math.Abs(e.CostUSD-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", e.CostUSD, want)
	}
	if e.SessionID != "sess1" || e.Cwd != "/tmp/proj" || e.Model != "claude-opus-4-8" {
		t.Errorf("unexpected entry metadata: %+v", e)
	}
}

func TestPrecomputedCostWins(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	line := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"cwd":"/x","sessionId":"s","requestId":"r","costUSD":1.5,"message":{"id":"m","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":10}}}`, ts)
	writeTranscript(t, filepath.Join(root, "-x"), "s.jsonl", line)

	m := NewManagerWithDirs([]string{root}, nil)
	entries := m.Entries(time.Time{})
	if len(entries) != 1 || entries[0].CostUSD != 1.5 {
		t.Fatalf("expected precomputed cost 1.5, got %+v", entries)
	}
}

func TestReportAttribution(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	ts1 := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	ts2 := now.Add(-1 * time.Hour).Format(time.RFC3339Nano)

	writeTranscript(t, filepath.Join(root, "-wt1"), "a.jsonl",
		assistantLine("m1", "r1", "claude-opus-4-8", "/work/wt1", "sa", ts1, 100, 100, 0, 0, 0),
		assistantLine("m2", "r2", "claude-sonnet-4-6", "/work/wt1/sub", "sa", ts2, 100, 100, 0, 0, 0),
	)
	writeTranscript(t, filepath.Join(root, "-other"), "b.jsonl",
		assistantLine("m3", "r3", "claude-opus-4-8", "/elsewhere", "sb", ts2, 100, 100, 0, 0, 0),
	)

	m := NewManagerWithDirs([]string{root}, nil)
	rep := m.Report(7, map[string]string{"/work/wt1": "wt1"})

	if rep.Total.Calls != 3 {
		t.Errorf("total calls = %d, want 3", rep.Total.Calls)
	}
	if len(rep.Worktrees) != 1 || rep.Worktrees[0].Worktree != "wt1" || rep.Worktrees[0].Calls != 2 {
		t.Errorf("worktree attribution wrong: %+v", rep.Worktrees)
	}
	if len(rep.Sessions) != 1 || rep.Sessions[0].SessionID != "sa" || len(rep.Sessions[0].Models) != 2 {
		t.Errorf("session aggregation wrong: %+v", rep.Sessions)
	}
	if len(rep.Daily) == 0 {
		t.Error("expected at least one daily bucket")
	}
}

func TestFileCacheInvalidation(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "-p")
	ts := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	writeTranscript(t, projDir, "s.jsonl",
		assistantLine("m1", "r1", "claude-opus-4-8", "/p", "s", ts, 100, 100, 0, 0, 0))

	m := NewManagerWithDirs([]string{root}, nil)
	if got := len(m.Entries(time.Time{})); got != 1 {
		t.Fatalf("first scan: %d entries, want 1", got)
	}

	// Append a second entry; size change must invalidate the cache.
	writeTranscript(t, projDir, "s.jsonl",
		assistantLine("m1", "r1", "claude-opus-4-8", "/p", "s", ts, 100, 100, 0, 0, 0),
		assistantLine("m2", "r2", "claude-opus-4-8", "/p", "s", ts, 100, 100, 0, 0, 0))
	if got := len(m.Entries(time.Time{})); got != 2 {
		t.Fatalf("second scan: %d entries, want 2", got)
	}
}

func TestPricingFallbacks(t *testing.T) {
	cases := []struct {
		model string
		in    float64
	}{
		{"claude-fable-5", 10},
		{"claude-opus-4-8", 5},
		{"claude-opus-4-1", 15},
		{"claude-sonnet-4-6", 3},
		{"claude-haiku-4-5-20251001", 1},
		{"claude-3-5-haiku-20241022", 0.8},
	}
	for _, c := range cases {
		p, ok := pricingFor(c.model)
		if !ok || p.InputPerMTok != c.in {
			t.Errorf("pricingFor(%s) = %+v ok=%v, want input %v", c.model, p, ok, c.in)
		}
	}
	if _, ok := pricingFor("gpt-4"); ok {
		t.Error("expected unknown model to have no pricing")
	}
}

func codexTokenLine(ts string, totalIn, totalCached, totalOut, lastIn, lastCached, lastOut int) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"total_tokens":%d},"last_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"total_tokens":%d},"model_context_window":258400}}}`,
		ts, totalIn, totalCached, totalOut, totalIn+totalOut, lastIn, lastCached, lastOut, lastIn+lastOut)
}

func TestParseCodexRollout(t *testing.T) {
	root := t.TempDir()
	dayDir := filepath.Join(root, "2026", "06", "10")
	ts := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)

	writeTranscript(t, dayDir, "rollout-x.jsonl",
		`{"timestamp":"`+ts+`","type":"session_meta","payload":{"id":"codex-sess-1","cwd":"/work/wt1"}}`,
		`{"timestamp":"`+ts+`","type":"turn_context","payload":{"turn_id":"t1","cwd":"/work/wt1","model":"gpt-5.5"}}`,
		// First event: totals == last (fresh thread).
		codexTokenLine(ts, 10000, 8000, 500, 10000, 8000, 500),
		// Second event: cumulative grows; delta = 20000 in / 9000 cached / 1000 out.
		codexTokenLine(ts, 30000, 17000, 1500, 20000, 9000, 1000),
	)

	m := NewManagerWithDirs(nil, []string{root})
	entries := m.Entries(time.Time{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}

	e := entries[0]
	if e.Agent != AgentCodex || e.Model != "gpt-5.5" || e.SessionID != "codex-sess-1" || e.Cwd != "/work/wt1" {
		t.Errorf("unexpected metadata: %+v", e)
	}
	// Uncached input = 10000-8000; cached billed at $0.50/MTok for gpt-5.5.
	if e.Input != 2000 || e.CacheRead != 8000 || e.Output != 500 {
		t.Errorf("unexpected tokens: %+v", e)
	}
	want := 5.0/1e6*2000 + 0.5/1e6*8000 + 30.0/1e6*500
	if math.Abs(e.CostUSD-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", e.CostUSD, want)
	}

	e2 := entries[1]
	if e2.Input != 11000 || e2.CacheRead != 9000 || e2.Output != 1000 {
		t.Errorf("unexpected delta tokens: %+v", e2)
	}
}

func TestCodexResumeNoDoubleCount(t *testing.T) {
	root := t.TempDir()
	dayDir := filepath.Join(root, "2026", "06", "10")
	ts := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)

	// Resumed thread: first event's cumulative total includes prior-file
	// usage (50000 in), but last_token_usage is just this call (1000 in).
	writeTranscript(t, dayDir, "rollout-resume.jsonl",
		`{"timestamp":"`+ts+`","type":"session_meta","payload":{"id":"s2","cwd":"/w"}}`,
		codexTokenLine(ts, 50000, 40000, 5000, 1000, 800, 100),
	)

	m := NewManagerWithDirs(nil, []string{root})
	entries := m.Entries(time.Time{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if e := entries[0]; e.Input != 200 || e.CacheRead != 800 || e.Output != 100 {
		t.Errorf("expected last_token_usage for first event, got %+v", e)
	}
}
