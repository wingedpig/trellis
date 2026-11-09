// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/logs"
)

// summaryRequest holds the parameters every multi-viewer summary shares.
type summaryRequest struct {
	viewers    []*logs.Viewer
	start, end time.Time
	timeout    time.Duration
}

// parseSummaryRequest reads viewers, start, end (default: the last hour) and
// timeout. It writes the error response itself and returns ok=false on a bad
// request.
func (h *LogHandler) parseSummaryRequest(w http.ResponseWriter, r *http.Request) (summaryRequest, bool) {
	query := r.URL.Query()
	var sr summaryRequest

	names := logs.ParseFieldList(query.Get("viewers"))
	if len(names) == 0 {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "viewers is required")
		return sr, false
	}
	for _, name := range names {
		viewer, ok := h.manager.Get(name)
		if !ok {
			WriteError(w, http.StatusNotFound, ErrNotFound, "log viewer not found: "+name)
			return sr, false
		}
		viewer.Touch()
		sr.viewers = append(sr.viewers, viewer)
	}

	var err error
	sr.end = time.Now()
	if s := query.Get("end"); s != "" {
		if sr.end, err = time.Parse(time.RFC3339, s); err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid end time")
			return sr, false
		}
	}
	sr.start = sr.end.Add(-time.Hour)
	if s := query.Get("start"); s != "" {
		if sr.start, err = time.Parse(time.RFC3339, s); err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid start time")
			return sr, false
		}
	}
	if !sr.start.Before(sr.end) {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "start must be before end")
		return sr, false
	}

	sr.timeout = defaultHistoryTimeout
	if s := query.Get("timeout"); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			sr.timeout = min(d, maxHistoryTimeout)
		}
	}
	return sr, true
}

// readAll feeds every viewer's entries in the window (narrowed by grep, when
// set) to add, reading the viewers concurrently. add is called under a lock.
// complete is false when any viewer's read stopped early.
func (sr summaryRequest) readAll(ctx context.Context, grep *regexp.Regexp, add func(logs.LogEntry)) (viewers []bubbleUpViewer, complete bool) {
	ctx, cancel := context.WithTimeout(ctx, sr.timeout)
	defer cancel()

	var mu sync.Mutex
	locked := func(e logs.LogEntry) {
		mu.Lock()
		add(e)
		mu.Unlock()
	}
	viewers = make([]bubbleUpViewer, len(sr.viewers))
	var wg sync.WaitGroup
	for i, v := range sr.viewers {
		wg.Go(func() {
			viewers[i] = bubbleUpRead(ctx, v, sr.start, sr.end, grep, sr.timeout, locked)
		})
	}
	wg.Wait()

	complete = true
	for _, v := range viewers {
		if v.Error != "" {
			complete = false
		}
	}
	return viewers, complete
}

// optionalGrep compiles an optional pattern parameter.
func optionalGrep(w http.ResponseWriter, name, pattern string) (*regexp.Regexp, bool) {
	if pattern == "" {
		return nil, true
	}
	re, err := logs.CompileGrep(pattern)
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, name+": "+err.Error())
		return nil, false
	}
	return re, true
}

// LogStats summarizes a numeric field — count, sum, mean, min, max, p50, p90,
// p99 — grouped by other fields, across one or more viewers.
// GET /api/v1/logs/stats?viewers=a,b&field=duration&by=route&start=...&end=...
//
// Optional: by (comma-separated fields), where (filter), grep (regexp that
// narrows the lines read; much faster than where on remote history), top
// (groups returned, default 50), timeout.
func (h *LogHandler) LogStats(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	sr, ok := h.parseSummaryRequest(w, r)
	if !ok {
		return
	}
	field := query.Get("field")
	if field == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "field is required")
		return
	}
	opts := logs.StatsOptions{Field: field, By: logs.ParseFieldList(query.Get("by"))}
	if s := query.Get("where"); s != "" {
		f, err := logs.ParseFilter(s)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid where: "+err.Error())
			return
		}
		opts.Where = f
	}
	grep, ok := optionalGrep(w, "grep", query.Get("grep"))
	if !ok {
		return
	}
	top := 50
	if n, err := strconv.Atoi(query.Get("top")); err == nil && n > 0 {
		top = n
	}

	stats := logs.NewStats(opts)
	viewers, complete := sr.readAll(r.Context(), grep, stats.Add)
	res := stats.Result()
	totalGroups := len(res.Groups)
	if len(res.Groups) > top {
		res.Groups = res.Groups[:top]
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"result":       res,
		"total_groups": totalGroups,
		"viewers":      viewers,
		"complete":     complete,
		"start":        sr.start,
		"end":          sr.end,
	})
}

// LogRequests summarizes requests: for every request whose start line
// matches, the fields of its start and end lines, its duration, and how many
// of its lines matched each named pattern.
// GET /api/v1/logs/requests?viewers=a&id_field=trace_id&start_match=STRT&end_match=END&count=db=SELECT&start=...&end=...
//
// The request ID comes from id_field (a field name) or id_pattern (a regexp
// over the raw line; its first capture group). count is repeatable, as
// name=regexp. Optional: limit (requests returned, default 1000; totals cover
// all of them), timeout.
func (h *LogHandler) LogRequests(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	sr, ok := h.parseSummaryRequest(w, r)
	if !ok {
		return
	}

	opts := logs.RequestsOptions{IDField: query.Get("id_field")}
	if opts.IDField == "" && query.Get("id_pattern") == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "id_field or id_pattern is required")
		return
	}
	if opts.IDField == "" {
		if opts.IDPattern, ok = optionalGrep(w, "id_pattern", query.Get("id_pattern")); !ok {
			return
		}
	}
	if query.Get("start_match") == "" {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "start_match is required")
		return
	}
	if opts.Start, ok = optionalGrep(w, "start_match", query.Get("start_match")); !ok {
		return
	}
	if opts.End, ok = optionalGrep(w, "end_match", query.Get("end_match")); !ok {
		return
	}
	for _, c := range query["count"] {
		np, err := logs.ParseNamedPattern(c)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "count: "+err.Error())
			return
		}
		opts.Counts = append(opts.Counts, np)
	}
	limit := 1000
	if n, err := strconv.Atoi(query.Get("limit")); err == nil && n >= 0 {
		limit = n
	}

	reqs := logs.NewRequests(opts)
	// Only start, end and counted lines matter: read just those.
	prefilter, err := logs.CompileGrep(reqs.Prefilter())
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, err.Error())
		return
	}
	viewers, complete := sr.readAll(r.Context(), prefilter, reqs.Add)

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"result":   reqs.Result(limit),
		"viewers":  viewers,
		"complete": complete,
		"start":    sr.start,
		"end":      sr.end,
	})
}
