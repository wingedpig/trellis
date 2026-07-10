// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/logs"
)

// TestStreamColdStartDeliversBacklog reproduces the "navigated to a cold log
// viewer and saw no lines" bug: a WebSocket connection that itself triggers
// the lazy viewer start must receive the tail's backlog as streamed entries.
func TestStreamColdStartDeliversBacklog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf(`{"time":"2026-07-09T12:00:%02d Z","level":"info","msg":"line %d"}`, i%60, i))
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := logs.NewManager(nil, config.LogViewerSettings{})
	if err := manager.Initialize([]config.LogViewerConfig{{
		Name:   "cold-test",
		Source: config.LogSourceConfig{Type: "file", Path: logPath},
		Parser: config.LogParserConfig{Type: "json", Timestamp: "time", Level: "level", Message: "msg"},
	}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	h := NewLogHandler(manager)
	h.SetUpgrader(&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }})

	r := mux.NewRouter()
	r.HandleFunc("/api/v1/logs/{name}/stream", h.Stream)
	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/logs/cold-test/stream"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Mimic the browser: send resume once connected.
	if err := conn.WriteJSON(map[string]any{"type": "resume", "after_seq": 0}); err != nil {
		t.Fatal(err)
	}

	// Collect frames for up to 5 seconds; the tail's backlog must arrive as
	// streamed entries even though the buffer was empty at connect time.
	received := 0
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for received == 0 {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break // deadline reached or connection closed
		}
		var frame struct {
			Type    string          `json:"type"`
			Entries json.RawMessage `json:"entries"`
		}
		if err := json.Unmarshal(msg, &frame); err != nil {
			continue
		}
		if frame.Type == "entries" && len(frame.Entries) > 0 && string(frame.Entries) != "null" {
			var entries []map[string]any
			if err := json.Unmarshal(frame.Entries, &entries); err == nil {
				received += len(entries)
			}
		}
	}

	if received == 0 {
		t.Fatalf("cold-start stream delivered no entries within 5s (backlog was %d lines)", len(lines))
	}
	t.Logf("received %d entries from cold start", received)
}
