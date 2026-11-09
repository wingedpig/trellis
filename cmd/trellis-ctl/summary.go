// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wingedpig/trellis/cmd/trellis-ctl/logs"
	"github.com/wingedpig/trellis/pkg/client"
)

// summaryFlags are the flags `logs stats` and `logs requests` share.
type summaryFlags struct {
	viewers      []string
	group        string
	since, until string
}

// parse handles a shared flag at args[*i]. It reports whether the flag was
// one of the shared ones.
func (f *summaryFlags) parse(args []string, i *int) (bool, error) {
	arg := args[*i]
	switch arg {
	case "-viewer", "-group", "-since", "-until":
	case "-json":
		jsonOutput = true
		return true, nil
	default:
		return false, nil
	}
	if *i+1 >= len(args) {
		return true, fmt.Errorf("%s requires a value", arg)
	}
	*i++
	val := args[*i]
	switch arg {
	case "-viewer":
		f.viewers = append(f.viewers, splitList(val)...)
	case "-group":
		f.group = val
	case "-since":
		f.since = val
	case "-until":
		f.until = val
	}
	return true, nil
}

// resolve returns the viewers and window the flags name. The window
// defaults to the last hour.
func (f *summaryFlags) resolve(ctx context.Context) (viewers []string, since, until time.Time, err error) {
	viewers = f.viewers
	if f.group != "" {
		gv, err := resolveTraceGroup(ctx, f.group)
		if err != nil {
			return nil, since, until, err
		}
		viewers = append(viewers, gv...)
	}
	if len(viewers) == 0 {
		return nil, since, until, fmt.Errorf("specify -viewer or -group")
	}
	if f.since == "" {
		f.since = "1h"
	}
	if since, err = logs.ParseDuration(f.since); err != nil {
		return nil, since, until, fmt.Errorf("invalid -since value: %w", err)
	}
	if f.until != "" {
		if until, err = logs.ParseUntil(f.until); err != nil {
			return nil, since, until, fmt.Errorf("invalid -until value: %w", err)
		}
	}
	return viewers, since, until, nil
}

// flagValue returns the value following the flag at args[*i].
func flagValue(args []string, i *int) (string, error) {
	if *i+1 >= len(args) {
		return "", fmt.Errorf("%s requires a value", args[*i])
	}
	*i++
	return args[*i], nil
}

// incompleteViewers turns viewer read errors into a *partialResultError, so
// the command prints what it has and then exits non-zero.
func incompleteViewers(complete bool, viewers []client.BubbleUpViewer) error {
	if complete {
		return nil
	}
	var problems []string
	for _, v := range viewers {
		if v.Error != "" {
			problems = append(problems, fmt.Sprintf("%s: read stopped early after %d lines: %s", v.Name, v.Lines, v.Error))
		}
	}
	if len(problems) == 0 {
		problems = []string{"a viewer's read stopped early"}
	}
	problems = append(problems, "the numbers above cover only part of the window")
	return &partialResultError{problems: problems}
}

// cmdLogsStats implements `trellis-ctl logs stats`: count, mean and
// percentiles of a numeric field, grouped by other fields.
func cmdLogsStats(args []string) error {
	var f summaryFlags
	opts := client.LogStatsOptions{Timeout: historyTimeout}
	for i := 0; i < len(args); i++ {
		if ok, err := f.parse(args, &i); err != nil {
			return err
		} else if ok {
			continue
		}
		arg := args[i]
		switch arg {
		case "-h", "-help", "--help":
			printLogsUsage()
			return nil
		case "-field", "-by", "-where", "-grep", "-top":
		default:
			return fmt.Errorf("unknown argument: %s (see trellis-ctl logs -h)", arg)
		}
		val, err := flagValue(args, &i)
		if err != nil {
			return err
		}
		switch arg {
		case "-field":
			opts.Field = val
		case "-by":
			opts.By = append(opts.By, splitList(val)...)
		case "-where":
			opts.Where = val
		case "-grep":
			opts.Grep = val
		case "-top":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid value for -top: %s", val)
			}
			opts.Top = n
		}
	}
	if opts.Field == "" {
		return fmt.Errorf("usage: trellis-ctl logs stats -field <name> [-by <a,b>] (-viewer <name> | -group <trace group>) [-since 1h]")
	}

	ctx := context.Background()
	var err error
	if opts.Viewers, opts.Since, opts.Until, err = f.resolve(ctx); err != nil {
		return err
	}
	res, err := apiClient.Logs.Stats(ctx, opts)
	if err != nil {
		return err
	}
	if jsonOutput {
		printJSON(res)
	} else {
		printLogStats(res)
	}
	return incompleteViewers(res.Complete, res.Viewers)
}

func printLogStats(res *client.LogStatsResult) {
	r := res.Result
	fmt.Printf("Field: %s   Matched: %d lines (%d without a numeric %s)   Window: %s – %s\n\n",
		r.Field, r.Matched, r.Missing, r.Field,
		res.Start.Local().Format("Jan 2 15:04:05"), res.End.Local().Format("Jan 2 15:04:05"))
	if len(r.Groups) == 0 {
		fmt.Println("No lines carried a numeric value for the field.")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	var header []string
	for _, b := range r.By {
		header = append(header, strings.ToUpper(b))
	}
	header = append(header, "COUNT", "MEAN", "P50", "P90", "P99", "MIN", "MAX")
	fmt.Fprintln(tw, strings.Join(header, "\t")+"\t")
	approx := false
	for _, g := range r.Groups {
		var cols []string
		if len(r.By) > 0 {
			cols = append(cols, padKey(g.Key, len(r.By))...)
		} else if len(g.Key) > 0 {
			cols = append(cols, g.Key...)
		}
		mark := ""
		if g.Approximate {
			mark, approx = "~", true
		}
		cols = append(cols, strconv.Itoa(g.Count), num(g.Mean),
			mark+num(g.P50), mark+num(g.P90), mark+num(g.P99), num(g.Min), num(g.Max))
		fmt.Fprintln(tw, strings.Join(cols, "\t")+"\t")
	}
	tw.Flush()
	if res.TotalGroups > len(r.Groups) {
		fmt.Printf("\nShowing the %d largest of %d groups; raise -top to see more.\n", len(r.Groups), res.TotalGroups)
	}
	if r.Overflow {
		fmt.Println("Too many distinct groups: later ones are pooled into (other).")
	}
	if approx {
		fmt.Println("~ percentiles estimated from a sample (count, mean, min and max are exact).")
	}
}

func padKey(key []string, n int) []string {
	out := make([]string, n)
	copy(out, key)
	return out
}

// num formats a number with at most three decimals and no trailing zeros.
func num(f float64) string {
	s := strconv.FormatFloat(f, 'f', 3, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// cmdLogsRequests implements `trellis-ctl logs requests`: one summary per
// request whose start line matches.
func cmdLogsRequests(args []string) error {
	var f summaryFlags
	opts := client.LogRequestsOptions{Timeout: historyTimeout}
	var show []string
	for i := 0; i < len(args); i++ {
		if ok, err := f.parse(args, &i); err != nil {
			return err
		} else if ok {
			continue
		}
		arg := args[i]
		switch arg {
		case "-h", "-help", "--help":
			printLogsUsage()
			return nil
		case "-id-field", "-id-pattern", "-start-match", "-end-match", "-count", "-show", "-n":
		default:
			return fmt.Errorf("unknown argument: %s (see trellis-ctl logs -h)", arg)
		}
		val, err := flagValue(args, &i)
		if err != nil {
			return err
		}
		switch arg {
		case "-id-field":
			opts.IDField = val
		case "-id-pattern":
			opts.IDPattern = val
		case "-start-match":
			opts.StartMatch = val
		case "-end-match":
			opts.EndMatch = val
		case "-count":
			opts.Counts = append(opts.Counts, val)
		case "-show":
			show = append(show, splitList(val)...)
		case "-n":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid value for -n: %s", val)
			}
			opts.Limit = n
		}
	}
	if opts.StartMatch == "" || (opts.IDField == "" && opts.IDPattern == "") {
		return fmt.Errorf("usage: trellis-ctl logs requests (-id-field <name> | -id-pattern <regexp>) -start-match <regexp> [-end-match <regexp>] [-count name=<regexp>]... (-viewer <name> | -group <trace group>) [-since 1h]")
	}

	ctx := context.Background()
	var err error
	if opts.Viewers, opts.Since, opts.Until, err = f.resolve(ctx); err != nil {
		return err
	}
	res, err := apiClient.Logs.Requests(ctx, opts)
	if err != nil {
		return err
	}
	if jsonOutput {
		printJSON(res)
	} else {
		printLogRequests(res, opts, show)
	}
	return incompleteViewers(res.Complete, res.Viewers)
}

func printLogRequests(res *client.LogRequestsResult, opts client.LogRequestsOptions, show []string) {
	r := res.Result
	var countNames []string
	for _, c := range opts.Counts {
		name := c
		if i := strings.Index(c, "="); i > 0 {
			name = c[:i]
		}
		countNames = append(countNames, name)
	}

	if len(r.Requests) == 0 {
		fmt.Println("No request start lines matched.")
	} else {
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		header := []string{"START", "ID", "DURATION"}
		for _, s := range show {
			header = append(header, strings.ToUpper(s))
		}
		for _, n := range countNames {
			header = append(header, strings.ToUpper(n))
		}
		fmt.Fprintln(tw, strings.Join(header, "\t"))
		for _, q := range r.Requests {
			dur := "-"
			if q.DurationMS != nil {
				dur = num(*q.DurationMS) + "ms"
			}
			cols := []string{q.Start.Local().Format("Jan 2 15:04:05.000"), q.ID, dur}
			for _, s := range show {
				cols = append(cols, requestField(q, s))
			}
			for _, n := range countNames {
				cols = append(cols, strconv.Itoa(q.Counts[n]))
			}
			fmt.Fprintln(tw, strings.Join(cols, "\t"))
		}
		tw.Flush()
	}

	fmt.Printf("\n%d requests started in %s – %s", r.Total,
		res.Start.Local().Format("Jan 2 15:04:05"), res.End.Local().Format("Jan 2 15:04:05"))
	if opts.EndMatch != "" {
		fmt.Printf(", %d with an end line", r.WithEnd)
	}
	fmt.Println()
	if len(r.Counts) > 0 {
		names := make([]string, 0, len(r.Counts))
		for n := range r.Counts {
			names = append(names, n)
		}
		sort.Strings(names)
		var parts []string
		for _, n := range names {
			parts = append(parts, fmt.Sprintf("%s=%d", n, r.Counts[n]))
		}
		fmt.Printf("Totals: %s\n", strings.Join(parts, " "))
	}
	if r.Total > len(r.Requests) {
		fmt.Printf("Showing the first %d; raise -n or use -json for all of them.\n", len(r.Requests))
	}
	if r.Truncated {
		fmt.Println("Warning: too many distinct request IDs; some were dropped. Narrow -since/-until.")
	}
	if len(show) == 0 && len(r.Requests) > 0 {
		fmt.Println("Add -show <field,...> for start/end line fields as columns, or -json for everything.")
	}
}

// requestField returns a field for a -show column: from the end line when it
// has it (end lines carry outcomes like status), else from the start line.
// "start.x" and "end.x" pick a side explicitly.
func requestField(q client.LogRequestSummary, name string) string {
	look := func(m map[string]interface{}, key string) (string, bool) {
		v, ok := m[key]
		if !ok {
			return "", false
		}
		return fmt.Sprint(v), true
	}
	switch {
	case strings.HasPrefix(name, "start."):
		v, _ := look(q.StartFields, strings.TrimPrefix(name, "start."))
		return v
	case strings.HasPrefix(name, "end."):
		v, _ := look(q.EndFields, strings.TrimPrefix(name, "end."))
		return v
	}
	if v, ok := look(q.EndFields, name); ok {
		return v
	}
	v, _ := look(q.StartFields, name)
	return v
}
