// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/cmd/trellis-ctl/logs"
	"github.com/wingedpig/trellis/pkg/client"
)

// Window searched around the start time embedded in a trace ID, when no
// -since/-until is given. Requests start at the embedded time; the tail covers
// their slower downstream work and clock skew between hosts.
const (
	traceWindowBefore = time.Minute
	traceWindowAfter  = 5 * time.Minute
)

// traceIDTime extracts a request start time embedded in a trace ID. It
// recognizes Unix epoch components in seconds, milliseconds, microseconds or
// nanoseconds (e.g. "web.app01-g2.837569.1793982657140344307") and UUIDv7.
// Only times between 2000 and a day from now count, so ordinary numeric
// components (PIDs, counters) aren't mistaken for one.
func traceIDTime(id string) (time.Time, bool) {
	if t, ok := uuidV7Time(id); ok {
		return t, true
	}
	tokens := strings.FieldsFunc(id, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	})
	lo := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	hi := time.Now().Add(24 * time.Hour)
	// Prefer the last component: IDs typically end with the timestamp.
	for i := len(tokens) - 1; i >= 0; i-- {
		tok := tokens[i]
		if len(tok) < 10 || len(tok) > 19 {
			continue
		}
		n, err := strconv.ParseInt(tok, 10, 64)
		if err != nil {
			continue
		}
		var t time.Time
		switch len(tok) {
		case 10:
			t = time.Unix(n, 0)
		case 13:
			t = time.UnixMilli(n)
		case 16:
			t = time.UnixMicro(n)
		case 19:
			t = time.Unix(0, n)
		default:
			continue
		}
		if t.After(lo) && t.Before(hi) {
			return t, true
		}
	}
	return time.Time{}, false
}

var uuidRe = regexp.MustCompile(`(?i)\b([0-9a-f]{8})-([0-9a-f]{4})-7[0-9a-f]{3}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

func uuidV7Time(id string) (time.Time, bool) {
	m := uuidRe.FindStringSubmatch(id)
	if m == nil {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(m[1]+m[2], 16, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}

// resolveTraceGroup returns the log viewers in a trace group.
func resolveTraceGroup(ctx context.Context, group string) ([]string, error) {
	groups, err := apiClient.Trace.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing trace groups: %w", err)
	}
	var names []string
	for _, g := range groups {
		if g.Name == group {
			return g.LogViewers, nil
		}
		names = append(names, g.Name)
	}
	return nil, fmt.Errorf("trace group not found: %s (have: %s)", group, strings.Join(names, ", "))
}

// viewerQuery is one log viewer read.
type viewerQuery struct {
	limit         int
	since, until  time.Time
	grep          string
	before, after int
}

// fetchViewers reads several log viewers concurrently and merges the results
// by timestamp. -n applies per viewer. Truncation notes go to stderr; a viewer
// that fails is reported and skipped unless every viewer fails.
func fetchViewers(names []string, q viewerQuery) ([]logs.LogEntry, error) {
	if len(names) == 1 {
		entries, note, err := fetchLogViewerEntries(names[0], q)
		if note != "" {
			fmt.Fprintln(os.Stderr, "Note: "+note)
		}
		return entries, err
	}

	type result struct {
		entries []logs.LogEntry
		note    string
		err     error
	}
	results := make([]result, len(names))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			e, note, err := fetchLogViewerEntries(name, q)
			results[i] = result{e, note, err}
		}()
	}
	wg.Wait()

	var all []logs.LogEntry
	failed := 0
	var lastErr error
	for i, r := range results {
		if r.err != nil {
			failed++
			lastErr = r.err
			fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", names[i], r.err)
			continue
		}
		if r.note != "" {
			fmt.Fprintln(os.Stderr, "Note: "+r.note)
		}
		all = append(all, r.entries...)
	}
	if failed == len(names) {
		return nil, lastErr
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Timestamp.Before(all[j].Timestamp) })
	return all, nil
}

// fetchLogViewerEntries reads one viewer: its history files when a start time
// is given, plus its live buffer. note is non-empty when -n cut the result
// short, saying where.
//
// A search (grep) returns the first matches from the start of the window —
// history and the buffer both scan forward and stop at the limit — so the
// merged result is cut at the earliest point either source stopped, which
// keeps it free of gaps. A plain read keeps the newest lines.
func fetchLogViewerEntries(name string, q viewerQuery) (entries []logs.LogEntry, note string, err error) {
	ctx := context.Background()
	limit := q.limit
	search := q.grep != ""

	var all []logs.LogEntry
	var cutoff time.Time // search results are complete up to here

	if !q.since.IsZero() {
		end := q.until
		if end.IsZero() {
			end = time.Now()
		}
		hist, err := apiClient.Logs.GetHistoryEntries(ctx, name, &client.LogEntriesOptions{
			Limit: limit, Since: q.since, Until: end,
			Grep: q.grep, Before: q.before, After: q.after,
		})
		switch {
		case err != nil && isPatternError(err):
			return nil, "", err
		case err != nil:
			// History may be unavailable (no rotated files, unsupported
			// source); fall back to the live buffer.
			fmt.Fprintf(os.Stderr, "Warning: history not available for %s: %v\n", name, err)
		default:
			if limit > 0 && len(hist) >= limit {
				cutoff = hist[len(hist)-1].Timestamp
			}
			all = append(all, convertClientLogEntries(hist)...)
		}
	}

	buf, err := apiClient.Logs.GetEntries(ctx, name, &client.LogEntriesOptions{
		Limit: limit, Since: q.since, Until: q.until,
		Grep: q.grep, Before: q.before, After: q.after,
	})
	if err != nil {
		return nil, "", err
	}
	bufTruncated := limit > 0 && len(buf) >= limit
	if search && bufTruncated {
		if t := buf[len(buf)-1].Timestamp; cutoff.IsZero() || t.Before(cutoff) {
			cutoff = t
		}
	}
	all = append(all, convertClientLogEntries(buf)...)

	all = dedupeLogEntries(all)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Timestamp.Before(all[j].Timestamp) })

	if search && !cutoff.IsZero() {
		n := sort.Search(len(all), func(i int) bool { return all[i].Timestamp.After(cutoff) })
		all = all[:n]
		if len(all) > limit {
			all = all[:limit]
		}
		windowEnd := "now"
		if !q.until.IsZero() {
			windowEnd = q.until.Local().Format("Jan 2 15:04:05")
		}
		note = fmt.Sprintf("%s: stopped at the -n limit of %d matches; results end at %s, so matches between then and %s are not shown. Raise -n or narrow -since/-until.",
			name, limit, all[len(all)-1].Timestamp.Local().Format("Jan 2 15:04:05.000"), windowEnd)
		return all, note, nil
	}

	if limit > 0 && len(all) > limit {
		total := len(all)
		all = all[len(all)-limit:]
		if search {
			note = fmt.Sprintf("%s: showing the last %d of %d matches. Raise -n to see them all.", name, limit, total)
		}
	} else if bufTruncated && search {
		note = fmt.Sprintf("%s: stopped at the -n limit of %d matches; there may be more. Raise -n or add -since.", name, limit)
	}
	return all, note, nil
}

// isPatternError reports whether the server rejected the request itself —
// typically a grep pattern it can't run — which must stop the command rather
// than degrade to a buffer-only read.
func isPatternError(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "BAD_REQUEST"
}
