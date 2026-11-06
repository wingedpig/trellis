// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wingedpig/trellis/pkg/client"
)

func TestTraceIDTime(t *testing.T) {
	want := time.Unix(0, 1793982657140344307)
	tests := []struct {
		id   string
		want time.Time
		ok   bool
	}{
		{"web.app01-g2.837569.1793982657140344307", want, true},
		{"req-1793982657140", time.UnixMilli(1793982657140), true},
		{"1793982657", time.Unix(1793982657, 0), true},
		{"019a5a9c-2f40-7cc3-8000-0123456789ab", time.UnixMilli(0x019a5a9c2f40), true},
		{"web.app01-g2.837569.42", time.Time{}, false},
		{"abcdef", time.Time{}, false},
		{"9999999999999999999", time.Time{}, false}, // far future
	}
	for _, tt := range tests {
		got, ok := traceIDTime(tt.id)
		if ok != tt.ok || !got.Equal(tt.want) {
			t.Errorf("traceIDTime(%q) = %v, %v; want %v, %v", tt.id, got, ok, tt.want, tt.ok)
		}
	}
}

// fakeViewerServer serves /history and /entries for one viewer from canned
// entries, honoring limit the way the real server does (first N in order).
func fakeViewerServer(t *testing.T, hist, buf []client.LogEntry) *httptest.Server {
	t.Helper()
	first := func(es []client.LogEntry, r *http.Request) []client.LogEntry {
		var n int
		fmt.Sscan(r.URL.Query().Get("limit"), &n)
		if n > 0 && len(es) > n {
			return es[:n]
		}
		return es
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/history"):
			w.Header().Set("Content-Type", "application/x-ndjson")
			enc := json.NewEncoder(w)
			for _, e := range first(hist, r) {
				enc.Encode(e)
			}
		case strings.HasSuffix(r.URL.Path, "/entries"):
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"entries": first(buf, r)}})
		default:
			http.NotFound(w, r)
		}
	}))
}

func entriesAt(base time.Time, secs ...int) []client.LogEntry {
	var out []client.LogEntry
	for _, s := range secs {
		ts := base.Add(time.Duration(s) * time.Second)
		out = append(out, client.LogEntry{Timestamp: ts, Source: "karld01", Message: fmt.Sprintf("m%d", s), Raw: fmt.Sprintf("raw %d", s)})
	}
	return out
}

func TestFetchLogViewerEntriesSearchTruncation(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	// History holds matches at 1..5, the live buffer 4..8. With -n 3 each
	// source stops after its first three; the merged result must end where
	// history stopped (3), not show 3 + buffer's later matches with a gap.
	srv := fakeViewerServer(t, entriesAt(base, 1, 2, 3, 4, 5), entriesAt(base, 4, 5, 6, 7, 8))
	defer srv.Close()
	apiClient = client.New(srv.URL)

	got, note, err := fetchLogViewerEntries("karld01", viewerQuery{limit: 3, since: base, grep: "tal01"})
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, e := range got {
		msgs = append(msgs, e.Message)
	}
	if strings.Join(msgs, ",") != "m1,m2,m3" {
		t.Errorf("entries = %v, want m1,m2,m3", msgs)
	}
	if !strings.Contains(note, "-n limit of 3") {
		t.Errorf("note = %q, want a truncation note", note)
	}
}

func TestFetchLogViewerEntriesNoNoteWhenComplete(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	srv := fakeViewerServer(t, entriesAt(base, 1, 2), entriesAt(base, 2, 3))
	defer srv.Close()
	apiClient = client.New(srv.URL)

	got, note, err := fetchLogViewerEntries("karld01", viewerQuery{limit: 10, since: base, grep: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || note != "" {
		t.Errorf("got %d entries, note %q; want 3 and no note", len(got), note)
	}
}

func TestFetchLogViewerEntriesBufferOnlySearchNote(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	srv := fakeViewerServer(t, nil, entriesAt(base, 1, 2, 3, 4))
	defer srv.Close()
	apiClient = client.New(srv.URL)

	got, note, err := fetchLogViewerEntries("karld01", viewerQuery{limit: 2, grep: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.Contains(note, "-n limit of 2") {
		t.Errorf("got %d entries, note %q; want 2 with a truncation note", len(got), note)
	}
}
