// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
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

func newGrepTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "mail.log")
	now := time.Now().UTC()
	var lines []string
	for i, to := range []string{"TAL01@ca.org", "tal01@caXorg", "other@ca.org"} {
		lines = append(lines, fmt.Sprintf(`{"time":%q,"level":"info","msg":"delivered","to":%q}`,
			now.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339Nano), to))
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := logs.NewManager(nil, config.LogViewerSettings{})
	if err := manager.Initialize([]config.LogViewerConfig{{
		Name:   "mail",
		Source: config.LogSourceConfig{Type: "file", Path: logPath},
		Parser: config.LogParserConfig{Type: "json", Timestamp: "time", Level: "level", Message: "msg"},
	}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Stop() })

	h := NewLogHandler(manager)
	r := mux.NewRouter()
	r.HandleFunc("/api/v1/logs/{name}/entries", h.GetEntries)
	r.HandleFunc("/api/v1/logs/{name}/history", h.GetHistory)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestLogGrepRejectsInvalidPattern(t *testing.T) {
	srv := newGrepTestServer(t)
	now := time.Now()
	for _, path := range []string{
		"/api/v1/logs/mail/entries?grep=" + url.QueryEscape("(?i"),
		"/api/v1/logs/mail/history?grep=" + url.QueryEscape("(?i") +
			"&start=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)) +
			"&end=" + url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339)),
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", path, resp.StatusCode)
		}
	}
}

func TestLogEntriesGrepIsRegexpOverRawLine(t *testing.T) {
	srv := newGrepTestServer(t)
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(srv.URL + "/api/v1/logs/mail/entries?grep=" + url.QueryEscape(`(?i)tal01@ca\.org`))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Data struct {
				Entries []logs.LogEntry `json:"entries"`
			} `json:"data"`
		}
		json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if n := len(body.Data.Entries); n == 1 {
			if to := body.Data.Entries[0].Fields["to"]; to != "TAL01@ca.org" {
				t.Fatalf("matched %v, want TAL01@ca.org", to)
			}
			return
		} else if n > 1 {
			t.Fatalf("got %d entries, want 1", n)
		}
		if time.Now().After(deadline) {
			t.Fatal("tail never buffered the matching line")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
