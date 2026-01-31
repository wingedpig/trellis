// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// trellis-ctl is a command-line tool for controlling a running Trellis instance.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wingedpig/trellis/cmd/trellis-ctl/logs"
	"github.com/wingedpig/trellis/pkg/client"
)

var (
	version    = "0.90"
	apiURL     = "http://localhost:1234"
	jsonOutput = false

	// API client instance
	apiClient *client.Client
)

func main() {
	// Check for TRELLIS_API environment variable
	if env := os.Getenv("TRELLIS_API"); env != "" {
		apiURL = strings.TrimSuffix(env, "/")
	}

	// Parse global flags and filter them out
	var filteredArgs []string
	for _, arg := range os.Args[1:] {
		if arg == "-json" {
			jsonOutput = true
		} else {
			filteredArgs = append(filteredArgs, arg)
		}
	}

	// Initialize API client
	apiClient = client.New(apiURL)

	if len(filteredArgs) < 1 {
		printUsage()
		os.Exit(1)
	}

	cmd := filteredArgs[0]
	args := filteredArgs[1:]

	var err error
	switch cmd {
	case "status":
		err = cmdStatus(args)
	case "logs":
		err = cmdLogs(args)
	case "start":
		err = cmdStart(args)
	case "stop":
		err = cmdStop(args)
	case "restart":
		err = cmdRestart(args)
	case "workflow":
		err = cmdWorkflow(args)
	case "worktree":
		err = cmdWorktree(args)
	case "events":
		err = cmdEvents(args)
	case "trace":
		err = cmdTrace(args)
	case "trace-report":
		err = cmdTraceReport(args)
	case "notify":
		err = cmdNotify(args)
	case "crash":
		err = cmdCrash(args)
	case "version", "-v", "--version":
		fmt.Printf("trellis-ctl %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`trellis-ctl - Control a running Trellis instance

Usage:
  trellis-ctl [-json] <command> [arguments]

Global Flags:
  -json          Output in JSON format

Environment:
  TRELLIS_API    Base URL of Trellis API (default: http://localhost:1234)

Commands:
  status [service]         Show status of all services or a specific service
  start <service>          Start a service
  stop <service>           Stop a service
  restart <service>        Restart a service

  logs <service> [options] Show logs for a service
    -n N                   Number of lines (default: 100)
    -f                     Stream logs in real-time
    -since <duration>      Show logs since (e.g., 1h, 30m, 6:30am, 2024-01-15T10:00:00Z)
    -until <duration>      Show logs until (e.g., 1h, 30m, 7:00am, 2024-01-15T11:00:00Z)
    -level <levels>        Filter by level (error, warn,error, info+)
    -grep <pattern>        Filter by regex pattern
    -B N                   Show N lines before each grep match
    -A N                   Show N lines after each grep match
    -C N                   Show N lines before and after each grep match
    -field <key=value>     Filter by field value (can repeat)
    -json                  Output as JSON array
    -jsonl                 Output as JSON Lines
    -csv                   Output as CSV
    -raw                   Output original raw lines
    -format <template>     Custom Go template format
    -viewer <name>         Access a log viewer instead of service
    -list                  List available log viewers
    -stats                 Show log statistics
    -clear                 Clear log buffer
    -open                  Open in browser
    -url                   Print browser URL

  workflow list            List all workflows
  workflow run <id>        Run a workflow (waits for completion)
  workflow status <id>     Get workflow status

  worktree list            List all worktrees
  worktree activate <name> Activate a worktree

  events [-n N]            Show recent events (default: 50)

  trace <id> <group> [options]  Run a distributed trace search
    -since <duration>      Start time (e.g., 1h, 30m, 6:30am)
    -until <duration>      End time (default: now)
    -name <name>           Report name (default: auto-generated)
    -no-expand-by-id       Disable ID expansion (two-pass search)

  trace-report <name>      Get a saved trace report
  trace-report -list       List all trace reports
  trace-report -groups     List configured trace groups
  trace-report -delete <name>  Delete a trace report

  notify <message> [options]  Send a notification event
    -type <type>           Type: done (default), blocked, error

  crash list               List all crashes
  crash newest             Get the most recent crash
  crash <id>               Get a specific crash by ID
  crash delete <id>        Delete a crash by ID
  crash clear              Clear all crashes

  version                  Show version
  help                     Show this help`)
}

// printJSON outputs any value as formatted JSON
func printJSON(v interface{}) {
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}

func cmdStatus(args []string) error {
	ctx := context.Background()

	if len(args) > 0 {
		// Specific service
		name := args[0]
		svc, err := apiClient.Services.Get(ctx, name)
		if err != nil {
			return err
		}

		if jsonOutput {
			printJSON(svc)
			return nil
		}

		// Print as formatted table (single service)
		fmt.Printf("%-20s %-10s %-8s %-10s %s\n", "SERVICE", "STATE", "PID", "RESTARTS", "ERROR")
		fmt.Println(strings.Repeat("-", 70))
		pid := "-"
		if svc.Status.PID > 0 {
			pid = strconv.Itoa(svc.Status.PID)
		}
		errMsg := svc.Status.Error
		if errMsg == "" {
			errMsg = "-"
		}
		fmt.Printf("%-20s %-10s %-8s %-10d %s\n", svc.Name, svc.Status.State, pid, svc.Status.RestartCount, errMsg)
		return nil
	}

	// All services
	services, err := apiClient.Services.List(ctx)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(services)
		return nil
	}

	// Print as formatted table
	fmt.Printf("%-20s %-10s %-8s %-10s %s\n", "SERVICE", "STATE", "PID", "RESTARTS", "ERROR")
	fmt.Println(strings.Repeat("-", 70))
	for _, svc := range services {
		pid := "-"
		if svc.Status.PID > 0 {
			pid = strconv.Itoa(svc.Status.PID)
		}
		errMsg := svc.Status.Error
		if len(errMsg) > 20 {
			errMsg = errMsg[:20] + "..."
		}
		fmt.Printf("%-20s %-10s %-8s %-10d %s\n",
			svc.Name,
			svc.Status.State,
			pid,
			svc.Status.RestartCount,
			errMsg,
		)
	}

	return nil
}

// logsConfig holds parsed command-line options for the logs command
type logsConfig struct {
	service      string
	viewer       string
	lines        int
	follow       bool
	since        string
	until        string
	level        string
	grep         string
	field        []string
	outputFormat string
	template     string
	list         bool
	clear        bool
	stats        bool
	openBrowser  bool
	showURL      bool
	before       int // -B: lines before match
	after        int // -A: lines after match
}

func parseLogsArgs(args []string) (*logsConfig, error) {
	cfg := &logsConfig{
		lines: 100,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-n" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("invalid value for -n: %s", args[i])
			}
			cfg.lines = n
		case arg == "-f":
			cfg.follow = true
		case arg == "-since" && i+1 < len(args):
			i++
			cfg.since = args[i]
		case arg == "-until" && i+1 < len(args):
			i++
			cfg.until = args[i]
		case arg == "-level" && i+1 < len(args):
			i++
			cfg.level = args[i]
		case arg == "-grep" && i+1 < len(args):
			i++
			cfg.grep = args[i]
		case arg == "-field" && i+1 < len(args):
			i++
			cfg.field = append(cfg.field, args[i])
		case arg == "-format" && i+1 < len(args):
			i++
			cfg.template = args[i]
		case arg == "-json":
			cfg.outputFormat = "json"
		case arg == "-jsonl":
			cfg.outputFormat = "jsonl"
		case arg == "-csv":
			cfg.outputFormat = "csv"
		case arg == "-raw":
			cfg.outputFormat = "raw"
		case arg == "-viewer" && i+1 < len(args):
			i++
			cfg.viewer = args[i]
		case arg == "-list":
			cfg.list = true
		case arg == "-clear":
			cfg.clear = true
		case arg == "-stats":
			cfg.stats = true
		case arg == "-open":
			cfg.openBrowser = true
		case arg == "-url":
			cfg.showURL = true
		case arg == "-B" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid value for -B: %s", args[i])
			}
			cfg.before = n
		case arg == "-A" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid value for -A: %s", args[i])
			}
			cfg.after = n
		case arg == "-C" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid value for -C: %s", args[i])
			}
			cfg.before = n
			cfg.after = n
		case !strings.HasPrefix(arg, "-"):
			if cfg.service == "" {
				cfg.service = arg
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}
		}
	}

	return cfg, nil
}

func cmdLogs(args []string) error {
	cfg, err := parseLogsArgs(args)
	if err != nil {
		return err
	}

	// Handle --list
	if cfg.list {
		return cmdLogsListViewers()
	}

	// Need either service or viewer
	if cfg.service == "" && cfg.viewer == "" {
		return fmt.Errorf("usage: trellis-ctl logs <service> [options] or trellis-ctl logs -viewer <name> [options]")
	}

	name := cfg.service
	isViewer := cfg.viewer != ""
	if isViewer {
		name = cfg.viewer
	}

	// Handle --url
	if cfg.showURL {
		if isViewer {
			fmt.Printf("%s/terminal/logviewer/%s\n", apiURL, name)
		} else {
			fmt.Printf("%s/terminal/#%s\n", apiURL, name)
		}
		return nil
	}

	// Handle --open
	if cfg.openBrowser {
		var url string
		if isViewer {
			url = fmt.Sprintf("%s/terminal/logviewer/%s", apiURL, name)
		} else {
			url = fmt.Sprintf("%s/terminal/#%s", apiURL, name)
		}
		return openBrowser(url)
	}

	// Handle --clear
	if cfg.clear {
		if isViewer {
			return fmt.Errorf("--clear is not supported for log viewers")
		}
		ctx := context.Background()
		err := apiClient.Services.ClearLogs(ctx, name)
		if err != nil {
			return err
		}
		fmt.Printf("Cleared logs for %s\n", name)
		return nil
	}

	// Build filter options
	filterOpts := logs.FilterOptions{
		MinLevel: logs.LevelUnset,
		Before:   cfg.before,
		After:    cfg.after,
	}

	if cfg.since != "" {
		since, err := logs.ParseDuration(cfg.since)
		if err != nil {
			return fmt.Errorf("invalid --since value: %w", err)
		}
		filterOpts.Since = since
	}

	if cfg.until != "" {
		until, err := logs.ParseDuration(cfg.until)
		if err != nil {
			return fmt.Errorf("invalid --until value: %w", err)
		}
		filterOpts.Until = until
	}

	if cfg.level != "" {
		levels, minLevel, err := logs.ParseLevelFilter(cfg.level)
		if err != nil {
			return fmt.Errorf("invalid --level value: %w", err)
		}
		filterOpts.Levels = levels
		filterOpts.MinLevel = minLevel
	}

	if cfg.grep != "" {
		filterOpts.GrepPattern = cfg.grep
	}

	if len(cfg.field) > 0 {
		filterOpts.FieldFilters = make(map[string]string)
		for _, f := range cfg.field {
			field, value, err := logs.ParseFieldFilter(f)
			if err != nil {
				return fmt.Errorf("invalid --field value: %w", err)
			}
			filterOpts.FieldFilters[field] = value
		}
	}

	// Build output options
	outputOpts := logs.OutputOptions{}
	if cfg.template != "" {
		outputOpts.Format = logs.FormatTemplate
		outputOpts.Template = cfg.template
	} else if cfg.outputFormat != "" {
		format, err := logs.ParseOutputFormat(cfg.outputFormat)
		if err != nil {
			return err
		}
		outputOpts.Format = format
	}

	// Handle follow mode
	if cfg.follow {
		// JSON array format doesn't work with streaming - use JSONL instead
		// Also check the global -json flag
		if outputOpts.Format == logs.FormatJSON || jsonOutput {
			outputOpts.Format = logs.FormatJSONL
		}
		return cmdLogsFollow(name, isViewer, filterOpts, outputOpts)
	}

	// Fetch logs
	var entries []logs.LogEntry
	if isViewer {
		// For log viewers, pass time range, grep pattern, and context to server
		entries, err = fetchLogViewerEntries(name, cfg.lines, filterOpts.Since, filterOpts.Until, cfg.grep, cfg.before, cfg.after)
	} else {
		entries, err = fetchServiceLogs(name, cfg.lines)
	}
	if err != nil {
		return err
	}

	// Apply client-side filters (level, field filters - grep is done server-side for viewers)
	// Clear grep from filterOpts for viewers since server already filtered
	clientFilterOpts := filterOpts
	if isViewer && cfg.grep != "" {
		clientFilterOpts.GrepPattern = ""
		clientFilterOpts.Before = 0
		clientFilterOpts.After = 0
	}
	filtered, err := logs.FilterEntries(entries, clientFilterOpts)
	if err != nil {
		return err
	}

	// Handle --stats
	if cfg.stats {
		duration := time.Hour // Default to last hour
		if cfg.since != "" {
			since, _ := logs.ParseDuration(cfg.since)
			duration = time.Since(since)
		}
		stats := logs.CalculateStats(filtered, duration)
		logs.FormatStats(os.Stdout, stats, name)
		return nil
	}

	// Global JSON output flag takes precedence
	if jsonOutput && cfg.outputFormat == "" {
		printJSON(filtered)
		return nil
	}

	// Format and output
	formatter, err := logs.NewFormatter(os.Stdout, outputOpts)
	if err != nil {
		return err
	}

	return formatter.FormatEntries(filtered)
}

func cmdLogsListViewers() error {
	ctx := context.Background()
	viewers, err := apiClient.Logs.List(ctx)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(viewers)
		return nil
	}

	if len(viewers) == 0 {
		fmt.Println("No log viewers configured")
		return nil
	}

	fmt.Printf("%-20s %s\n", "NAME", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 60))
	for _, v := range viewers {
		fmt.Printf("%-20s %s\n", v.Name, v.Description)
	}
	return nil
}

func cmdLogsFollow(name string, isViewer bool, filterOpts logs.FilterOptions, outputOpts logs.OutputOptions) error {
	// Follow mode uses Server-Sent Events for real-time streaming
	filter, err := logs.NewFilter(filterOpts)
	if err != nil {
		return err
	}

	formatter, err := logs.NewFormatter(os.Stdout, outputOpts)
	if err != nil {
		return err
	}

	// Build SSE endpoint URL
	var endpoint string
	if isViewer {
		endpoint = fmt.Sprintf("/api/v1/logs/%s/stream/sse", name)
	} else {
		endpoint = fmt.Sprintf("/api/v1/services/%s/logs/stream", name)
	}

	return streamSSE(endpoint, name, isViewer, filter, formatter)
}

func streamSSE(endpoint, name string, isViewer bool, filter *logs.Filter, formatter *logs.Formatter) error {
	url := apiURL + endpoint

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	// Use a client without timeout for streaming
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to SSE stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("SSE stream error: %s - %s", resp.Status, string(body))
	}

	// Get parser config for service logs
	var parserCfg logs.ParserConfig
	if !isViewer {
		ctx := context.Background()
		parserCfg = getServiceParserConfig(ctx, name)
	}

	// Read SSE events
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("SSE stream closed")
			}
			return fmt.Errorf("SSE read error: %w", err)
		}

		line = strings.TrimSpace(line)

		// Skip empty lines and comments (keepalives)
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Skip event type lines
		if strings.HasPrefix(line, "event:") {
			continue
		}

		// Parse data lines
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)

			var entry logs.LogEntry
			if isViewer {
				// Log viewer sends full LogEntry objects
				if err := json.Unmarshal([]byte(data), &entry); err != nil {
					continue // Skip malformed entries
				}
			} else {
				// Service logs send {line, sequence} objects
				var logLine struct {
					Line     string `json:"line"`
					Sequence int64  `json:"sequence"`
				}
				if err := json.Unmarshal([]byte(data), &logLine); err != nil {
					continue // Skip malformed entries
				}
				entry = logs.ParseLogLine(logLine.Line, parserCfg, name)
			}

			if filter.Match(&entry) {
				if err := formatter.FormatEntry(&entry); err != nil {
					return err
				}
			}
		}
	}
}

func fetchServiceLogs(name string, lines int) ([]logs.LogEntry, error) {
	ctx := context.Background()

	// First, fetch service info to get parser config
	parserCfg := getServiceParserConfig(ctx, name)

	// Fetch log lines
	data, err := apiClient.Services.Logs(ctx, name, lines)
	if err != nil {
		return nil, err
	}

	var result struct {
		Service string   `json:"service"`
		Lines   []string `json:"lines"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse logs: %w", err)
	}

	// Parse log lines using the parser config
	entries := make([]logs.LogEntry, 0, len(result.Lines))
	for _, line := range result.Lines {
		if line == "" {
			continue
		}
		entry := logs.ParseLogLine(line, parserCfg, name)
		entries = append(entries, entry)
	}
	return entries, nil
}

// getServiceParserConfig fetches the parser configuration for a service.
func getServiceParserConfig(ctx context.Context, name string) logs.ParserConfig {
	svc, err := apiClient.Services.Get(ctx, name)
	if err != nil {
		return logs.ParserConfig{} // No parsing if we can't get config
	}

	return logs.ParserConfig{
		Type:           svc.ParserType,
		TimestampField: svc.TimestampField,
		LevelField:     svc.LevelField,
		MessageField:   svc.MessageField,
	}
}

func fetchLogViewerEntries(name string, limit int, since, until time.Time, grep string, before, after int) ([]logs.LogEntry, error) {
	ctx := context.Background()
	var allEntries []logs.LogEntry

	// If a time range is specified, query historical entries first
	// The server's ReadRange already filters files by ModTime to avoid scanning unnecessary files
	if !since.IsZero() {
		end := until
		if end.IsZero() {
			end = time.Now()
		}

		histOpts := &client.LogEntriesOptions{
			Limit:  limit,
			Since:  since,
			Until:  end,
			Grep:   grep,
			Before: before,
			After:  after,
		}

		histEntries, err := apiClient.Logs.GetHistoryEntries(ctx, name, histOpts)
		if err != nil {
			// History might not be available (no rotated files), continue with live buffer
			if !strings.Contains(err.Error(), "404") {
				return nil, err
			}
		} else {
			allEntries = append(allEntries, convertClientLogEntries(histEntries)...)
		}
	}

	// Also query live buffer
	bufferOpts := &client.LogEntriesOptions{
		Limit: limit,
		Since: since,
		Until: until,
	}

	bufferEntries, err := apiClient.Logs.GetEntries(ctx, name, bufferOpts)
	if err != nil {
		return nil, err
	}

	// Merge entries from history and live buffer
	allEntries = append(allEntries, convertClientLogEntries(bufferEntries)...)

	// Dedupe by timestamp+message (history and buffer might overlap)
	allEntries = dedupeLogEntries(allEntries)

	// Sort by timestamp (ascending)
	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].Timestamp.Before(allEntries[j].Timestamp)
	})

	// Apply limit after merge - take the LAST N entries (newest)
	if limit > 0 && len(allEntries) > limit {
		allEntries = allEntries[len(allEntries)-limit:]
	}

	return allEntries, nil
}

// convertClientLogEntries converts client.LogEntry slice to logs.LogEntry slice.
func convertClientLogEntries(entries []client.LogEntry) []logs.LogEntry {
	result := make([]logs.LogEntry, len(entries))
	for i, e := range entries {
		result[i] = logs.LogEntry{
			Timestamp: e.Timestamp,
			Source:    e.Source,
			Level:     e.Level,
			Message:   e.Message,
			Fields:    e.Fields,
			Raw:       e.Raw,
		}
	}
	return result
}

// dedupeLogEntries removes duplicate entries based on source and raw log line.
// Using the raw log line as the key ensures that repeated identical log lines
// are preserved (they would have different positions/content), while true duplicates
// (same entry appearing in both history and buffer) are removed.
func dedupeLogEntries(entries []logs.LogEntry) []logs.LogEntry {
	seen := make(map[string]bool)
	var result []logs.LogEntry

	for _, e := range entries {
		// Create a key from source + raw line
		// Raw is the original log line which is unique per entry
		key := e.Source + "|" + e.Raw
		if !seen[key] {
			seen[key] = true
			result = append(result, e)
		}
	}

	return result
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform")
	}
	return cmd.Start()
}

func cmdStart(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl start <service>")
	}

	ctx := context.Background()
	name := args[0]
	svc, err := apiClient.Services.Start(ctx, name)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(svc)
		return nil
	}

	fmt.Printf("Started %s (state: %s)\n", svc.Name, svc.Status.State)
	return nil
}

func cmdStop(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl stop <service>")
	}

	ctx := context.Background()
	name := args[0]
	svc, err := apiClient.Services.Stop(ctx, name)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(svc)
		return nil
	}

	fmt.Printf("Stopped %s (state: %s)\n", svc.Name, svc.Status.State)
	return nil
}

func cmdRestart(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl restart <service>")
	}

	ctx := context.Background()
	name := args[0]
	svc, err := apiClient.Services.Restart(ctx, name)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(svc)
		return nil
	}

	fmt.Printf("Restarted %s (state: %s)\n", svc.Name, svc.Status.State)
	return nil
}

func cmdWorkflow(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl workflow <list|describe|run|status> [args]")
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "list":
		return cmdWorkflowList()
	case "describe":
		return cmdWorkflowDescribe(subargs)
	case "run":
		return cmdWorkflowRun(subargs)
	case "status":
		return cmdWorkflowStatus(subargs)
	default:
		return fmt.Errorf("unknown workflow subcommand: %s", subcmd)
	}
}

func cmdWorkflowList() error {
	ctx := context.Background()
	workflows, err := apiClient.Workflows.List(ctx)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(workflows)
		return nil
	}

	fmt.Printf("%-20s %-30s %s\n", "ID", "NAME", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 80))
	for _, wf := range workflows {
		desc := wf.Description
		if len(desc) > 30 {
			desc = desc[:27] + "..."
		}
		fmt.Printf("%-20s %-30s %s\n", wf.ID, wf.Name, desc)
	}

	return nil
}

func cmdWorkflowDescribe(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl workflow describe <id>")
	}

	ctx := context.Background()
	id := args[0]

	wf, err := apiClient.Workflows.Get(ctx, id)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(wf)
		return nil
	}

	fmt.Printf("Workflow: %s\n", wf.ID)
	fmt.Printf("Name: %s\n", wf.Name)
	if wf.Description != "" {
		fmt.Printf("Description: %s\n", wf.Description)
	}

	if len(wf.Inputs) > 0 {
		fmt.Println("\nInputs:")
		for _, input := range wf.Inputs {
			// Build the flag name and requirement indicator
			required := "(optional)"
			if input.Required {
				required = "(required)"
			}

			// Get display name
			displayName := input.Name
			if input.Label != "" {
				displayName = input.Label
			}

			fmt.Printf("  --%s    %s %s\n", input.Name, displayName, required)

			// Description
			if input.Description != "" {
				fmt.Printf("             %s\n", input.Description)
			}

			// Type-specific info
			switch input.Type {
			case "select":
				if len(input.Options) > 0 {
					fmt.Printf("             Options: %s\n", strings.Join(input.Options, ", "))
				}
			case "datepicker":
				fmt.Printf("             Type: date (YYYY-MM-DD)\n")
			case "checkbox":
				fmt.Printf("             Type: boolean\n")
			}

			// Validation constraints
			if len(input.AllowedValues) > 0 {
				fmt.Printf("             Allowed: %s\n", strings.Join(input.AllowedValues, ", "))
			}
			if input.Pattern != "" {
				fmt.Printf("             Pattern: %s\n", input.Pattern)
			}

			// Default value
			if input.Default != nil {
				fmt.Printf("             Default: %v\n", input.Default)
			}

			fmt.Println()
		}
	}

	return nil
}

func cmdWorkflowRun(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl workflow run <id> [--input=value ...]")
	}

	ctx := context.Background()
	id := args[0]

	// Parse --name=value inputs from remaining args
	inputs := make(map[string]any)
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "--") {
			// Remove the -- prefix
			arg = strings.TrimPrefix(arg, "--")
			// Split on first =
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				inputs[parts[0]] = parts[1]
			} else {
				// Flag without value, treat as boolean true
				inputs[parts[0]] = true
			}
		}
	}

	if !jsonOutput {
		fmt.Printf("Running workflow: %s\n", id)
	}

	opts := &client.RunOptions{}
	if len(inputs) > 0 {
		opts.Inputs = inputs
	}

	status, err := apiClient.Workflows.Run(ctx, id, opts)
	if err != nil {
		return err
	}

	// Poll for completion if running (with timeout and backoff)
	const maxPollTime = 30 * time.Minute
	const maxPollInterval = 5 * time.Second
	pollStart := time.Now()
	pollInterval := 500 * time.Millisecond

	for status.State == "running" || status.State == "pending" {
		if time.Since(pollStart) > maxPollTime {
			return fmt.Errorf("timeout waiting for workflow completion after %v", maxPollTime)
		}

		time.Sleep(pollInterval)
		// Exponential backoff: double interval up to max
		if pollInterval < maxPollInterval {
			pollInterval = pollInterval * 2
			if pollInterval > maxPollInterval {
				pollInterval = maxPollInterval
			}
		}

		status, err = apiClient.Workflows.Status(ctx, status.ID)
		if err != nil {
			return err
		}
	}

	if jsonOutput {
		printJSON(status)
		if !status.Success {
			return fmt.Errorf("workflow failed")
		}
		return nil
	}

	// Print output
	if status.Output != "" {
		fmt.Println(status.Output)
	}

	// Print result
	duration := status.Duration.Round(time.Millisecond).String()
	if status.Success {
		fmt.Printf("\n✓ Workflow completed successfully (%s)\n", duration)
	} else {
		fmt.Printf("\n✗ Workflow failed (%s)\n", duration)
		if status.Error != "" {
			fmt.Printf("Error: %s\n", status.Error)
		}
		return fmt.Errorf("workflow failed")
	}

	return nil
}

func cmdWorkflowStatus(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl workflow status <id>")
	}

	ctx := context.Background()
	id := args[0]
	status, err := apiClient.Workflows.Status(ctx, id)
	if err != nil {
		return err
	}

	// Always output JSON for status (it's the natural format)
	printJSON(status)
	return nil
}

func cmdWorktree(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl worktree <list|activate> [args]")
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "list":
		return cmdWorktreeList()
	case "activate":
		return cmdWorktreeActivate(subargs)
	default:
		return fmt.Errorf("unknown worktree subcommand: %s", subcmd)
	}
}

func cmdWorktreeList() error {
	ctx := context.Background()
	worktrees, err := apiClient.Worktrees.List(ctx)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(worktrees)
		return nil
	}

	fmt.Printf("%-20s %-20s %-8s %-20s %s\n", "NAME", "BRANCH", "ACTIVE", "STATUS", "PATH")
	fmt.Println(strings.Repeat("-", 100))
	for _, wt := range worktrees {
		active := ""
		if wt.Active {
			active = "*"
		}
		status := formatWorktreeStatus(wt)
		fmt.Printf("%-20s %-20s %-8s %-20s %s\n", wt.Name(), wt.Branch, active, status, wt.Path)
	}

	return nil
}

// formatWorktreeStatus builds a compact status string for a worktree.
func formatWorktreeStatus(wt client.Worktree) string {
	var parts []string
	if wt.Dirty {
		parts = append(parts, "dirty")
	}
	if wt.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", wt.Ahead))
	}
	if wt.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", wt.Behind))
	}
	if wt.Detached {
		parts = append(parts, "detached")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func cmdWorktreeActivate(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl worktree activate <name>")
	}

	ctx := context.Background()
	name := args[0]
	if !jsonOutput {
		fmt.Printf("Activating worktree: %s\n", name)
	}

	result, err := apiClient.Worktrees.Activate(ctx, name)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(result)
		return nil
	}

	fmt.Printf("Activated %s (%s) in %s\n", result.Worktree.Name(), result.Worktree.Branch, result.Duration)
	return nil
}

func cmdEvents(args []string) error {
	limit := 50

	// Parse -n flag
	for i := 0; i < len(args); i++ {
		if args[i] == "-n" && i+1 < len(args) {
			n, err := strconv.Atoi(args[i+1])
			if err == nil && n > 0 {
				limit = n
			}
			i++
		}
	}

	ctx := context.Background()
	events, err := apiClient.Events.List(ctx, &client.ListOptions{Limit: limit})
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(events)
		return nil
	}

	fmt.Printf("%-25s %-25s %-15s %s\n", "TIME", "TYPE", "WORKTREE", "DETAILS")
	fmt.Println(strings.Repeat("-", 100))
	for _, evt := range events {
		details := ""
		if len(evt.Payload) > 0 {
			parts := []string{}
			for k, v := range evt.Payload {
				parts = append(parts, fmt.Sprintf("%s=%v", k, v))
			}
			details = strings.Join(parts, " ")
		}
		fmt.Printf("%-25s %-25s %-15s %s\n",
			evt.Timestamp.Format("2006-01-02 15:04:05"),
			evt.Type,
			evt.Worktree,
			details,
		)
	}

	return nil
}

func cmdNotify(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl notify <message> [-type done|blocked|error]")
	}

	message := args[0]
	notifyType := client.NotifyDone

	// Parse flags
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-type", "-t", "--type":
			if i+1 < len(args) {
				switch args[i+1] {
				case "done":
					notifyType = client.NotifyDone
				case "blocked":
					notifyType = client.NotifyBlocked
				case "error":
					notifyType = client.NotifyError
				default:
					return fmt.Errorf("invalid type: %s (must be done, blocked, or error)", args[i+1])
				}
				i++
			}
		}
	}

	ctx := context.Background()
	_, err := apiClient.Notify.Send(ctx, message, notifyType)
	if err != nil {
		return err
	}

	if !jsonOutput {
		fmt.Println("Notification sent")
	}

	return nil
}

// traceConfig holds parsed command-line options for the trace command
type traceConfig struct {
	traceID    string
	group      string
	since      string
	until      string
	name       string
	expandByID bool
}

func parseTraceArgs(args []string) (*traceConfig, error) {
	cfg := &traceConfig{
		expandByID: true, // Default to true
	}

	positional := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-since" && i+1 < len(args):
			i++
			cfg.since = args[i]
		case arg == "-until" && i+1 < len(args):
			i++
			cfg.until = args[i]
		case arg == "-name" && i+1 < len(args):
			i++
			cfg.name = args[i]
		case arg == "-expand-by-id":
			cfg.expandByID = true
		case arg == "-no-expand-by-id":
			cfg.expandByID = false
		case !strings.HasPrefix(arg, "-"):
			switch positional {
			case 0:
				cfg.traceID = arg
			case 1:
				cfg.group = arg
			}
			positional++
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}
		}
	}

	return cfg, nil
}

func cmdTrace(args []string) error {
	cfg, err := parseTraceArgs(args)
	if err != nil {
		return err
	}

	if cfg.traceID == "" || cfg.group == "" {
		return fmt.Errorf("usage: trellis-ctl trace <id> <group> [options]")
	}

	ctx := context.Background()

	// Parse time range
	var since, until time.Time

	if cfg.since != "" {
		since, err = logs.ParseDuration(cfg.since)
		if err != nil {
			return fmt.Errorf("invalid --since value: %w", err)
		}
	} else {
		// Default to 1 hour ago
		since = time.Now().Add(-1 * time.Hour)
	}

	if cfg.until != "" {
		until, err = logs.ParseDuration(cfg.until)
		if err != nil {
			return fmt.Errorf("invalid --until value: %w", err)
		}
	} else {
		until = time.Now()
	}

	// Build request
	req := &client.TraceRequest{
		ID:         cfg.traceID,
		Group:      cfg.group,
		Start:      since,
		End:        until,
		Name:       cfg.name,
		ExpandByID: cfg.expandByID,
	}

	if !jsonOutput {
		fmt.Printf("Searching for %q in group %q (%s to %s)...\n",
			cfg.traceID, cfg.group,
			since.Format("15:04:05"),
			until.Format("15:04:05"))
	}

	// Start the trace (returns immediately with "running" status)
	result, err := apiClient.Trace.Execute(ctx, req)
	if err != nil {
		return err
	}

	// If the API returned an error immediately
	if result.Status == "failed" {
		if jsonOutput {
			printJSON(result)
			return nil
		}
		fmt.Printf("\nTrace failed: %s\n", result.Error)
		return fmt.Errorf("trace failed")
	}

	// Poll for completion
	reportName := result.Name
	for {
		// Fetch the report to check status
		report, err := apiClient.Trace.GetReport(ctx, reportName)
		if err != nil {
			return fmt.Errorf("failed to check trace status: %w", err)
		}

		if report.Status == "completed" {
			if jsonOutput {
				printJSON(report)
				return nil
			}
			fmt.Printf("\nTrace completed: %s\n", report.Name)
			fmt.Printf("  Total entries: %d\n", report.Summary.TotalEntries)
			fmt.Printf("  Duration: %dms\n", report.Summary.DurationMS)
			if len(report.Summary.BySource) > 0 {
				fmt.Println("  By source:")
				for source, count := range report.Summary.BySource {
					fmt.Printf("    %s: %d\n", source, count)
				}
			}
			fmt.Printf("\nView report: trellis-ctl trace-report %s\n", report.Name)
			return nil
		} else if report.Status == "failed" {
			if jsonOutput {
				printJSON(report)
				return nil
			}
			fmt.Printf("\nTrace failed: %s\n", report.Error)
			return fmt.Errorf("trace failed")
		}

		// Still running - wait and poll again
		time.Sleep(500 * time.Millisecond)
	}
}

func cmdTraceReport(args []string) error {
	// Check for flags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-list":
			return cmdTraceReportList()
		case "-groups":
			return cmdTraceGroups()
		case "-delete":
			if i+1 >= len(args) {
				return fmt.Errorf("-delete requires a report name")
			}
			return cmdTraceReportDelete(args[i+1])
		}
	}

	// Get specific report
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl trace-report <name> or trace-report -list")
	}

	ctx := context.Background()
	name := args[0]
	report, err := apiClient.Trace.GetReport(ctx, name)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(report)
		return nil
	}

	// Print report header
	fmt.Printf("Trace Report: %s\n", report.Name)
	fmt.Printf("  Trace ID: %s\n", report.TraceID)
	fmt.Printf("  Group: %s\n", report.Group)
	fmt.Printf("  Created: %s\n", report.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Time Range: %s to %s\n",
		report.TimeRange.Start.Format("15:04:05"),
		report.TimeRange.End.Format("15:04:05"))
	fmt.Printf("  Total Entries: %d\n", report.Summary.TotalEntries)
	fmt.Println()

	// Print entries
	fmt.Printf("%-25s %-15s %-8s %s\n", "TIMESTAMP", "SOURCE", "LEVEL", "MESSAGE")
	fmt.Println(strings.Repeat("-", 100))

	for _, entry := range report.Entries {
		level := entry.Level
		if level == "" {
			level = "-"
		}
		msg := entry.Message
		if len(msg) > 50 {
			msg = msg[:50] + "..."
		}
		prefix := " "
		if entry.IsContext {
			prefix = "-"
		}
		fmt.Printf("%s%-24s %-15s %-8s %s\n",
			prefix,
			entry.Timestamp.Format("2006-01-02 15:04:05.000"),
			entry.Source,
			level,
			msg,
		)
	}

	return nil
}

func cmdTraceReportList() error {
	ctx := context.Background()
	reports, err := apiClient.Trace.ListReports(ctx)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(reports)
		return nil
	}

	if len(reports) == 0 {
		fmt.Println("No trace reports found")
		return nil
	}

	fmt.Printf("%-30s %-20s %-15s %-8s %s\n", "NAME", "TRACE ID", "GROUP", "ENTRIES", "CREATED")
	fmt.Println(strings.Repeat("-", 100))
	for _, r := range reports {
		traceID := r.TraceID
		if len(traceID) > 18 {
			traceID = traceID[:18] + ".."
		}
		fmt.Printf("%-30s %-20s %-15s %-8d %s\n",
			r.Name,
			traceID,
			r.Group,
			r.EntryCount,
			r.CreatedAt.Format("2006-01-02 15:04"),
		)
	}

	return nil
}

func cmdTraceReportDelete(name string) error {
	ctx := context.Background()
	err := apiClient.Trace.DeleteReport(ctx, name)
	if err != nil {
		return err
	}

	if !jsonOutput {
		fmt.Printf("Deleted trace report: %s\n", name)
	}

	return nil
}

func cmdTraceGroups() error {
	ctx := context.Background()
	groups, err := apiClient.Trace.ListGroups(ctx)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(groups)
		return nil
	}

	if len(groups) == 0 {
		fmt.Println("No trace groups configured")
		return nil
	}

	fmt.Printf("%-20s %s\n", "GROUP", "LOG VIEWERS")
	fmt.Println(strings.Repeat("-", 60))
	for _, g := range groups {
		fmt.Printf("%-20s %s\n", g.Name, strings.Join(g.LogViewers, ", "))
	}

	return nil
}

func cmdCrash(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl crash <list|newest|delete|clear|id>")
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "list":
		return cmdCrashList()
	case "newest":
		return cmdCrashNewest()
	case "delete":
		return cmdCrashDelete(subargs)
	case "clear":
		return cmdCrashClear()
	default:
		// Treat as crash ID
		return cmdCrashGet(subcmd)
	}
}

func cmdCrashList() error {
	ctx := context.Background()
	crashes, err := apiClient.Crashes.List(ctx)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(crashes)
		return nil
	}

	if len(crashes) == 0 {
		fmt.Println("No crashes recorded")
		return nil
	}

	fmt.Printf("%-25s %-15s %-20s %-8s %s\n", "ID", "SERVICE", "TRACE ID", "EXIT", "ERROR")
	fmt.Println(strings.Repeat("-", 100))
	for _, c := range crashes {
		traceID := c.TraceID
		if len(traceID) > 18 {
			traceID = traceID[:18] + ".."
		}
		errMsg := c.Error
		if len(errMsg) > 30 {
			errMsg = errMsg[:30] + "..."
		}
		fmt.Printf("%-25s %-15s %-20s %-8d %s\n",
			c.ID,
			c.Service,
			traceID,
			c.ExitCode,
			errMsg,
		)
	}

	return nil
}

func cmdCrashNewest() error {
	ctx := context.Background()
	crash, err := apiClient.Crashes.Newest(ctx)
	if err != nil {
		return err
	}

	if crash == nil {
		if jsonOutput {
			printJSON(nil)
			return nil
		}
		fmt.Println("No crashes recorded")
		return nil
	}

	if jsonOutput {
		printJSON(crash)
		return nil
	}

	printCrashDetail(crash)
	return nil
}

func cmdCrashGet(id string) error {
	ctx := context.Background()
	crash, err := apiClient.Crashes.Get(ctx, id)
	if err != nil {
		return err
	}

	if jsonOutput {
		printJSON(crash)
		return nil
	}

	printCrashDetail(crash)
	return nil
}

func cmdCrashDelete(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl crash delete <id>")
	}

	ctx := context.Background()
	id := args[0]
	if err := apiClient.Crashes.Delete(ctx, id); err != nil {
		return err
	}

	if !jsonOutput {
		fmt.Printf("Deleted crash: %s\n", id)
	}

	return nil
}

func cmdCrashClear() error {
	ctx := context.Background()
	if err := apiClient.Crashes.Clear(ctx); err != nil {
		return err
	}

	if !jsonOutput {
		fmt.Println("Cleared all crashes")
	}

	return nil
}

func printCrashDetail(crash *client.Crash) {
	fmt.Printf("Crash: %s\n", crash.ID)
	fmt.Printf("  Service: %s\n", crash.Service)
	fmt.Printf("  Timestamp: %s\n", crash.Timestamp.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Exit Code: %d\n", crash.ExitCode)
	if crash.Error != "" {
		fmt.Printf("  Error: %s\n", crash.Error)
	}
	if crash.TraceID != "" {
		fmt.Printf("  Trace ID: %s\n", crash.TraceID)
	}
	if crash.Worktree.Name != "" {
		fmt.Printf("  Worktree: %s (%s)\n", crash.Worktree.Name, crash.Worktree.Branch)
	}
	fmt.Println()

	// Print summary statistics
	if crash.Summary.TotalEntries > 0 {
		fmt.Printf("Summary: %d entries\n", crash.Summary.TotalEntries)
		if len(crash.Summary.BySource) > 0 {
			fmt.Print("  By source: ")
			first := true
			for svc, count := range crash.Summary.BySource {
				if !first {
					fmt.Print(", ")
				}
				fmt.Printf("%s=%d", svc, count)
				first = false
			}
			fmt.Println()
		}
		fmt.Println()
	}

	// Print log entries (last 20)
	if len(crash.Entries) > 0 {
		fmt.Println("Log Entries:")
		start := len(crash.Entries) - 20
		if start < 0 {
			start = 0
		}
		for _, entry := range crash.Entries[start:] {
			ts := entry.Timestamp.Format("15:04:05.000")
			level := entry.Level
			if level == "" {
				level = "---"
			}
			fmt.Printf("  %s [%-5s] %s: %s\n", ts, level, entry.Source, entry.Message)
		}
	}
}
