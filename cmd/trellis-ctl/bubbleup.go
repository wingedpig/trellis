// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/wingedpig/trellis/cmd/trellis-ctl/logs"
	"github.com/wingedpig/trellis/pkg/client"
)

// cmdLogsBubbleUp implements `trellis-ctl logs bubbleup`: rank the field
// values that differ most between lines matching -where and the rest of the
// window.
func cmdLogsBubbleUp(args []string) error {
	opts := client.BubbleUpOptions{}
	var group, since, until string

	next := func(i *int, flag string) (string, error) {
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s requires a value", flag)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		var val string
		var err error
		switch arg {
		case "-where", "-viewer", "-group", "-baseline", "-since", "-until", "-fields", "-exclude", "-top":
			if val, err = next(&i, arg); err != nil {
				return err
			}
		case "-json":
			jsonOutput = true
			continue
		default:
			return fmt.Errorf("unknown argument: %s", arg)
		}
		switch arg {
		case "-where":
			opts.Selection = val
		case "-viewer":
			opts.Viewers = append(opts.Viewers, splitList(val)...)
		case "-group":
			group = val
		case "-baseline":
			opts.Baseline = val
		case "-since":
			since = val
		case "-until":
			until = val
		case "-fields":
			opts.Fields = splitList(val)
		case "-exclude":
			opts.Exclude = splitList(val)
		case "-top":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid value for -top: %s", val)
			}
			opts.Top = n
		}
	}

	if opts.Selection == "" {
		return fmt.Errorf("usage: trellis-ctl logs bubbleup -where <filter> (-viewer <name> | -group <trace group>) [-since 1h]")
	}

	ctx := context.Background()
	if group != "" {
		groups, err := apiClient.Trace.ListGroups(ctx)
		if err != nil {
			return fmt.Errorf("listing trace groups: %w", err)
		}
		found := false
		for _, g := range groups {
			if g.Name == group {
				opts.Viewers = append(opts.Viewers, g.LogViewers...)
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("trace group not found: %s", group)
		}
	}
	if len(opts.Viewers) == 0 {
		return fmt.Errorf("specify -viewer or -group")
	}

	if since == "" {
		since = "1h"
	}
	var err error
	if opts.Since, err = logs.ParseDuration(since); err != nil {
		return fmt.Errorf("invalid -since value: %w", err)
	}
	if until != "" {
		if opts.Until, err = logs.ParseDuration(until); err != nil {
			return fmt.Errorf("invalid -until value: %w", err)
		}
	}

	res, err := apiClient.Logs.BubbleUp(ctx, opts)
	if err != nil {
		return err
	}
	if jsonOutput {
		printJSON(res)
		return nil
	}
	printBubbleUp(res, opts.Selection)
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func printBubbleUp(res *client.BubbleUpResult, selection string) {
	r := res.Result
	fmt.Printf("Selection: %d lines (%s)   Baseline: %d lines   Window: %s–%s\n",
		r.SelectionCount, selection, r.BaselineCount,
		res.Start.Local().Format("Jan 2 15:04"), res.End.Local().Format("15:04"))

	var parts []string
	for _, v := range res.Viewers {
		p := fmt.Sprintf("%s (%d", v.Name, v.Lines)
		if v.Source == "buffer" {
			p += ", buffer only"
		}
		p += ")"
		if v.Error != "" {
			p += " [error: " + v.Error + "]"
		}
		parts = append(parts, p)
	}
	fmt.Printf("Viewers: %s\n\n", strings.Join(parts, ", "))

	if r.SelectionCount == 0 {
		fmt.Println("No lines matched the selection.")
		return
	}
	if len(r.Values) == 0 {
		fmt.Println("No field values differ meaningfully between selection and baseline.")
	} else {
		fieldW, valueW := len("FIELD"), len("VALUE")
		for _, v := range r.Values {
			fieldW = max(fieldW, len(v.Field))
			valueW = max(valueW, min(len(v.Value), 50))
		}
		fmt.Printf("%-*s  %-*s  %10s  %10s  %s\n", fieldW, "FIELD", valueW, "VALUE", "SELECTION", "BASELINE", "DIFF")
		for _, v := range r.Values {
			value := v.Value
			if len(value) > 50 {
				value = value[:49] + "…"
			}
			fmt.Printf("%-*s  %-*s  %9.1f%%  %9.1f%%  %s\n",
				fieldW, v.Field, valueW, value, v.SelectionPct, v.BaselinePct, diffBar(v.Diff))
		}
	}

	if len(r.HighCardinality) > 0 {
		fmt.Printf("\nSkipped (high-cardinality): %s\n", strings.Join(r.HighCardinality, ", "))
	}
	if len(r.Approximate) > 0 {
		fmt.Printf("Approximate baseline (too many distinct values): %s\n", strings.Join(r.Approximate, ", "))
	}
	// Suggest a drill-down on the strongest lead. The filter syntax splits on
	// spaces, so values containing one can't be added as a clause.
	for _, v := range r.Values {
		if v.Diff > 0 && !strings.ContainsAny(v.Value, " \"") {
			fmt.Printf("\nDrill down: -where '%s %s:%s'\n", selection, v.Field, v.Value)
			break
		}
	}
}

// diffBar renders the percentage-point difference as ▲/▼ markers (one per
// 25 points, at least one) followed by the signed value.
func diffBar(diff float64) string {
	mark := "▲"
	if diff < 0 {
		mark = "▼"
	}
	n := int(abs(diff)/25) + 1
	return fmt.Sprintf("%-4s %+.1f", strings.Repeat(mark, min(n, 4)), diff)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
