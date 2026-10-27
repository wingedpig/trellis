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

	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/logs"
)

// TestBubbleUpAcrossViewers reads two file viewers over a time window and
// checks that the value concentrated in the error lines ranks first.
func TestBubbleUpAcrossViewers(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 27, 13, 0, 0, 0, time.UTC)

	var cfgs []config.LogViewerConfig
	for v, host := range []string{"app01-g2", "app02-g2"} {
		var lines []string
		for i := range 200 {
			ts := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
			level, file := "info", "web/handlers.go:88"
			// Errors only on app02, all from one source line.
			if host == "app02-g2" && i%5 == 0 {
				level, file = "error", "messageserverd/attach.go:212"
			}
			lines = append(lines, fmt.Sprintf(`{"time":%q,"level":%q,"msg":"m","host.name":%q,"file_line":%q,"trace_id":"t%d-%d"}`,
				ts, level, host, file, v, i))
		}
		path := filepath.Join(dir, host+".log")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cfgs = append(cfgs, config.LogViewerConfig{
			Name:   host,
			Source: config.LogSourceConfig{Type: "file", Path: path, Current: host + ".log"},
			Parser: config.LogParserConfig{Type: "json", Timestamp: "time", Level: "level", Message: "msg"},
		})
	}

	manager := logs.NewManager(nil, config.LogViewerSettings{})
	if err := manager.Initialize(cfgs); err != nil {
		t.Fatal(err)
	}
	h := NewLogHandler(manager)

	q := url.Values{
		"viewers":   {"app01-g2,app02-g2"},
		"selection": {"level:error"},
		"start":     {base.Add(-time.Minute).Format(time.RFC3339)},
		"end":       {base.Add(time.Hour).Format(time.RFC3339)},
	}
	rec := httptest.NewRecorder()
	h.BubbleUp(rec, httptest.NewRequest(http.MethodGet, "/api/v1/logs/bubbleup?"+q.Encode(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data struct {
			Result  logs.BubbleUpResult `json:"result"`
			Viewers []bubbleUpViewer    `json:"viewers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	res := resp.Data.Result
	if res.SelectionCount != 40 || res.BaselineCount != 360 {
		t.Fatalf("counts = %d/%d, want 40/360 (viewers: %+v)", res.SelectionCount, res.BaselineCount, resp.Data.Viewers)
	}
	if len(res.Values) == 0 {
		t.Fatal("no ranked values")
	}
	top := res.Values[0]
	if top.Diff <= 0 || (top.Value != "messageserverd/attach.go:212" && top.Value != "app02-g2") {
		t.Errorf("top value = %+v, want attach.go:212 or app02-g2", top)
	}
	for _, v := range res.Values {
		if v.Field == "level" || v.Field == "trace_id" || v.Field == "time" {
			t.Errorf("field %q should not be ranked", v.Field)
		}
	}
}

func TestBubbleUpValidation(t *testing.T) {
	h := NewLogHandler(logs.NewManager(nil, config.LogViewerSettings{}))
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"selection=level:error", http.StatusBadRequest},
		{"viewers=x", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		h.BubbleUp(rec, httptest.NewRequest(http.MethodGet, "/api/v1/logs/bubbleup?"+tc.query, nil))
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.query, rec.Code, tc.want)
		}
	}
}
