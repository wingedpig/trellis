// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/logs"
)

// bubbleUpViewer reports where one viewer's lines came from.
type bubbleUpViewer struct {
	Name   string `json:"name"`
	Lines  int    `json:"lines"`
	Source string `json:"source"` // "history" or "buffer"
	Error  string `json:"error,omitempty"`
}

// BubbleUp compares the entries matching a selection filter against the rest
// of the same time window, across one or more viewers, and ranks the field
// values that differ most.
// GET /api/v1/logs/bubbleup?viewers=a,b&selection=level:error&start=...&end=...
//
// Optional: baseline (filter narrowing the comparison set), fields, exclude
// (comma-separated field names), top, per_field, timeout.
func (h *LogHandler) BubbleUp(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	names := logs.ParseFieldList(query.Get("viewers"))
	if len(names) == 0 {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "viewers is required")
		return
	}
	viewers := make([]*logs.Viewer, 0, len(names))
	for _, name := range names {
		viewer, ok := h.manager.Get(name)
		if !ok {
			WriteError(w, http.StatusNotFound, ErrNotFound, "log viewer not found: "+name)
			return
		}
		viewer.Touch()
		viewers = append(viewers, viewer)
	}

	selectionStr := query.Get("selection")
	if selectionStr == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "selection is required")
		return
	}
	selection, err := logs.ParseFilter(selectionStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid selection: "+err.Error())
		return
	}
	var baseline *logs.Filter
	if s := query.Get("baseline"); s != "" {
		if baseline, err = logs.ParseFilter(s); err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid baseline: "+err.Error())
			return
		}
	}

	end := time.Now()
	if s := query.Get("end"); s != "" {
		if end, err = time.Parse(time.RFC3339, s); err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid end time")
			return
		}
	}
	start := end.Add(-time.Hour)
	if s := query.Get("start"); s != "" {
		if start, err = time.Parse(time.RFC3339, s); err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid start time")
			return
		}
	}
	if !start.Before(end) {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "start must be before end")
		return
	}

	opts := logs.BubbleUpOptions{
		Selection: selection,
		Baseline:  baseline,
		Fields:    logs.ParseFieldList(query.Get("fields")),
		Exclude:   logs.ParseFieldList(query.Get("exclude")),
	}
	opts.Top, _ = strconv.Atoi(query.Get("top"))
	opts.PerField, _ = strconv.Atoi(query.Get("per_field"))
	for _, v := range viewers {
		// The parser's timestamp field is unique per line; never compare it.
		if ts := v.Config().Parser.Timestamp; ts != "" {
			opts.Exclude = append(opts.Exclude, ts)
		}
	}

	timeout := defaultHistoryTimeout
	if s := query.Get("timeout"); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			timeout = min(d, maxHistoryTimeout)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	bu := logs.NewBubbleUp(opts)
	var mu sync.Mutex
	add := func(e logs.LogEntry) {
		mu.Lock()
		bu.Add(e)
		mu.Unlock()
	}

	summaries := make([]bubbleUpViewer, len(viewers))
	var wg sync.WaitGroup
	for i, v := range viewers {
		wg.Go(func() {
			summaries[i] = bubbleUpRead(ctx, v, start, end, add)
		})
	}
	wg.Wait()

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"result":  bu.Result(),
		"viewers": summaries,
		"start":   start,
		"end":     end,
	})
}

// bubbleUpRead feeds one viewer's entries for [start, end] into add. It reads
// history (rotated files plus the current file) and falls back to the
// in-memory buffer for sources without historical access.
func bubbleUpRead(ctx context.Context, v *logs.Viewer, start, end time.Time, add func(logs.LogEntry)) bubbleUpViewer {
	s := bubbleUpViewer{Name: v.Name(), Source: "history"}
	err := v.StreamHistoricalEntries(ctx, start, end, nil, 0, "", 0, 0, func(e logs.LogEntry) error {
		s.Lines++
		add(e)
		return nil
	})
	if s.Lines > 0 || ctx.Err() != nil {
		if err != nil {
			// Partial read: keep what we counted, but say so.
			s.Error = err.Error()
		}
		return s
	}
	// Nothing from history — either the source has no historical access
	// (error) or none configured (e.g. a file source without "current"
	// returns no lines and no error). The live buffer is the best we have.
	s.Source = "buffer"
	for _, e := range v.GetEntriesRange(start, end, 0) {
		s.Lines++
		add(e)
	}
	return s
}
