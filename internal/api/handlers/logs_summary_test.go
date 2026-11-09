// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/logs"
)

// summaryFixture writes a file viewer of request logs: for each request i a
// STRT line, i%3 SQL lines, and an END line carrying its duration.
func summaryFixture(t *testing.T) (*LogHandler, time.Time) {
	t.Helper()
	dir := t.TempDir()
	base := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	var lines []string
	add := func(ts time.Time, msg, rid string, extra string) {
		lines = append(lines, fmt.Sprintf(`{"time":%q,"level":"info","msg":%q,"rid":%q%s}`,
			ts.Format(time.RFC3339Nano), msg, rid, extra))
	}
	for i := range 20 {
		start := base.Add(time.Duration(i) * time.Second)
		route := "/api/a"
		if i%2 == 1 {
			route = "/api/b"
		}
		rid := fmt.Sprintf("r%02d", i)
		add(start, "STRT GET "+route, rid, fmt.Sprintf(`,"route":%q`, route))
		for j := range i % 3 {
			add(start.Add(time.Duration(j+1)*time.Millisecond), "SELECT x", rid, "")
		}
		dur := (i + 1) * 10
		add(start.Add(time.Duration(dur)*time.Millisecond), "END", rid, fmt.Sprintf(`,"route":%q,"duration":"%dms","status":200`, route, dur))
	}
	path := filepath.Join(dir, "web.log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := logs.NewManager(nil, config.LogViewerSettings{})
	if err := manager.Initialize([]config.LogViewerConfig{{
		Name:   "web",
		Source: config.LogSourceConfig{Type: "file", Path: path, Current: "web.log"},
		Parser: config.LogParserConfig{Type: "json", Timestamp: "time", Level: "level", Message: "msg"},
	}}); err != nil {
		t.Fatal(err)
	}
	return NewLogHandler(manager), base
}

func TestLogStatsEndpoint(t *testing.T) {
	h, base := summaryFixture(t)
	q := url.Values{
		"viewers": {"web"},
		"field":   {"duration"},
		"by":      {"route"},
		"grep":    {"END"},
		"start":   {base.Add(-time.Minute).Format(time.RFC3339)},
		"end":     {base.Add(time.Hour).Format(time.RFC3339)},
	}
	rec := httptest.NewRecorder()
	h.LogStats(rec, httptest.NewRequest(http.MethodGet, "/api/v1/logs/stats?"+q.Encode(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Result   logs.StatsResult `json:"result"`
			Complete bool             `json:"complete"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	r := resp.Data.Result
	if !resp.Data.Complete || r.Matched != 20 || r.Missing != 0 || len(r.Groups) != 2 {
		t.Fatalf("result = %+v complete=%v", r, resp.Data.Complete)
	}
	// /api/a holds requests 0,2,...,18: durations 10,30,...,190 ms.
	for _, g := range r.Groups {
		if g.Key[0] == "/api/a" && (g.Count != 10 || g.P50 != 90 || g.P90 != 170 || g.P99 != 190 || g.Min != 10) {
			t.Errorf("/api/a = %+v", g)
		}
	}

	rec = httptest.NewRecorder()
	h.LogStats(rec, httptest.NewRequest(http.MethodGet, "/api/v1/logs/stats?viewers=web", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing field: status %d, want 400", rec.Code)
	}
}

func TestLogRequestsEndpoint(t *testing.T) {
	h, base := summaryFixture(t)
	q := url.Values{
		"viewers":     {"web"},
		"id_field":    {"rid"},
		"start_match": {"STRT GET /api/b"},
		"end_match":   {"END"},
		"count":       {"sql=SELECT"},
		"start":       {base.Add(-time.Minute).Format(time.RFC3339)},
		"end":         {base.Add(time.Hour).Format(time.RFC3339)},
	}
	rec := httptest.NewRecorder()
	h.LogRequests(rec, httptest.NewRequest(http.MethodGet, "/api/v1/logs/requests?"+q.Encode(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Result   logs.RequestsResult `json:"result"`
			Complete bool                `json:"complete"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	r := resp.Data.Result
	if r.Total != 10 || r.WithEnd != 10 {
		t.Fatalf("total=%d with_end=%d, want 10/10", r.Total, r.WithEnd)
	}
	first := r.Requests[0]
	if first.ID != "r01" || first.DurationMS == nil || *first.DurationMS != 20 || first.Counts["sql"] != 1 || first.EndFields["status"] != float64(200) {
		t.Errorf("first = %+v", first)
	}
	// Odd requests 1..19 have i%3 SQL lines: 1,0,2,1,0,2,1,0,2,1 = 10.
	if r.Counts["sql"] != 10 {
		t.Errorf("sql total = %d, want 10", r.Counts["sql"])
	}

	rec = httptest.NewRecorder()
	h.LogRequests(rec, httptest.NewRequest(http.MethodGet, "/api/v1/logs/requests?viewers=web&start_match=STRT", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing id: status %d, want 400", rec.Code)
	}
}

func TestHistoryUnsupportedViewer(t *testing.T) {
	manager := logs.NewManager(nil, config.LogViewerSettings{})
	if err := manager.Initialize([]config.LogViewerConfig{{
		Name:   "cmd",
		Source: config.LogSourceConfig{Type: "command", Command: []string{"true"}},
		Parser: config.LogParserConfig{Type: "none"},
	}}); err != nil {
		t.Fatal(err)
	}
	h := NewLogHandler(manager)
	q := url.Values{
		"start": {time.Now().Add(-time.Hour).Format(time.RFC3339)},
		"end":   {time.Now().Format(time.RFC3339)},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/cmd/history?"+q.Encode(), nil)
	req = mux.SetURLVars(req, map[string]string{"name": "cmd"})
	rec := httptest.NewRecorder()
	h.GetHistory(rec, req)
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), ErrHistoryUnsupported) {
		t.Errorf("status %d body %s; want 501 %s", rec.Code, rec.Body.String(), ErrHistoryUnsupported)
	}
}
