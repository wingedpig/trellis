// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/template"
	"time"
)

// Formatter formats log entries for output.
type Formatter struct {
	opts     OutputOptions
	template *template.Template
	writer   io.Writer
}

// NewFormatter creates a new Formatter with the given options.
func NewFormatter(w io.Writer, opts OutputOptions) (*Formatter, error) {
	f := &Formatter{
		opts:   opts,
		writer: w,
	}

	// Parse template if needed
	if opts.Format == FormatTemplate && opts.Template != "" {
		tmpl, err := template.New("log").Parse(opts.Template)
		if err != nil {
			return nil, fmt.Errorf("invalid template: %w", err)
		}
		f.template = tmpl
	}

	return f, nil
}

// FormatEntry formats a single log entry.
func (f *Formatter) FormatEntry(entry *LogEntry) error {
	switch f.opts.Format {
	case FormatPlain:
		return f.formatPlain(entry)
	case FormatJSON:
		// JSON format outputs as array, so individual entries are handled differently
		return fmt.Errorf("use FormatEntries for JSON array format")
	case FormatJSONL:
		return f.formatJSONL(entry)
	case FormatCSV:
		return f.formatCSV(entry)
	case FormatRaw:
		return f.formatRaw(entry)
	case FormatTemplate:
		return f.formatTemplate(entry)
	default:
		return f.formatPlain(entry)
	}
}

// FormatEntries formats multiple log entries.
func (f *Formatter) FormatEntries(entries []LogEntry) error {
	switch f.opts.Format {
	case FormatJSON:
		return f.formatJSONArray(entries)
	case FormatCSV:
		return f.formatCSVWithHeader(entries)
	default:
		for _, entry := range entries {
			if err := f.FormatEntry(&entry); err != nil {
				return err
			}
		}
		return nil
	}
}

func (f *Formatter) formatPlain(entry *LogEntry) error {
	// Format: TIMESTAMP LEVEL MESSAGE
	ts := entry.Timestamp.Format("2006-01-02 15:04:05.000")
	level := strings.ToUpper(entry.Level)
	if level == "" {
		level = "INFO"
	}
	_, err := fmt.Fprintf(f.writer, "%s %-5s %s\n", ts, level, entry.Message)
	return err
}

func (f *Formatter) formatJSONL(entry *LogEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f.writer, "%s\n", data)
	return err
}

func (f *Formatter) formatJSONArray(entries []LogEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f.writer, "%s\n", data)
	return err
}

func (f *Formatter) formatCSV(entry *LogEntry) error {
	w := csv.NewWriter(f.writer)
	record := []string{
		entry.Timestamp.Format(time.RFC3339),
		entry.Level,
		entry.Message,
		entry.Source,
		entry.Raw,
	}
	if err := w.Write(record); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func (f *Formatter) formatCSVWithHeader(entries []LogEntry) error {
	w := csv.NewWriter(f.writer)

	// Write header
	header := []string{"timestamp", "level", "message", "source", "raw"}
	if err := w.Write(header); err != nil {
		return err
	}

	// Write entries
	for _, entry := range entries {
		record := []string{
			entry.Timestamp.Format(time.RFC3339),
			entry.Level,
			entry.Message,
			entry.Source,
			entry.Raw,
		}
		if err := w.Write(record); err != nil {
			return err
		}
	}

	w.Flush()
	return w.Error()
}

func (f *Formatter) formatRaw(entry *LogEntry) error {
	_, err := fmt.Fprintln(f.writer, entry.Raw)
	return err
}

func (f *Formatter) formatTemplate(entry *LogEntry) error {
	if f.template == nil {
		return fmt.Errorf("no template configured")
	}

	// Create template data with easy access to fields
	data := map[string]interface{}{
		"timestamp": entry.Timestamp.Format("2006-01-02 15:04:05.000"),
		"level":     entry.Level,
		"message":   entry.Message,
		"raw":       entry.Raw,
		"source":    entry.Source,
		"fields":    entry.Fields,
	}

	var buf bytes.Buffer
	if err := f.template.Execute(&buf, data); err != nil {
		return err
	}

	_, err := fmt.Fprintln(f.writer, buf.String())
	return err
}

// CalculateStats calculates statistics from a set of log entries.
func CalculateStats(entries []LogEntry, duration time.Duration) *LogStats {
	stats := &LogStats{
		TotalEntries: len(entries),
		Duration:     duration,
		LevelCounts:  make(map[string]int),
	}

	if len(entries) == 0 {
		return stats
	}

	// Count entries per minute
	if duration > 0 {
		stats.EntriesPerMin = float64(len(entries)) / duration.Minutes()
	}

	// Count by level
	errorCount := 0
	warnCount := 0
	errorMessages := make(map[string]int)

	for _, entry := range entries {
		level := strings.ToUpper(entry.Level)
		if level == "" {
			level = "INFO"
		}
		stats.LevelCounts[level]++

		switch level {
		case "ERROR", "ERR", "FATAL", "CRITICAL":
			errorCount++
			// Track error messages for top errors
			msg := truncateMessage(entry.Message, 50)
			errorMessages[msg]++
		case "WARN", "WARNING":
			warnCount++
		}
	}

	// Calculate rates
	if len(entries) > 0 {
		stats.ErrorRate = float64(errorCount) / float64(len(entries)) * 100
		stats.WarnRate = float64(warnCount) / float64(len(entries)) * 100
	}

	// Get top errors
	stats.TopErrors = getTopErrors(errorMessages, 5)

	return stats
}

func truncateMessage(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	return msg[:maxLen] + "..."
}

func getTopErrors(errorMessages map[string]int, limit int) []ErrorCount {
	var errors []ErrorCount
	for msg, count := range errorMessages {
		errors = append(errors, ErrorCount{Message: msg, Count: count})
	}

	// Sort by count descending
	sort.Slice(errors, func(i, j int) bool {
		return errors[i].Count > errors[j].Count
	})

	if len(errors) > limit {
		errors = errors[:limit]
	}
	return errors
}

// FormatStats formats log statistics for display.
func FormatStats(w io.Writer, stats *LogStats, serviceName string) {
	fmt.Fprintf(w, "Log Statistics for '%s'", serviceName)
	if stats.Duration > 0 {
		fmt.Fprintf(w, " (last %s)", formatDuration(stats.Duration))
	}
	fmt.Fprintln(w, ":")
	fmt.Fprintf(w, "  Total entries:    %d\n", stats.TotalEntries)

	if stats.TotalEntries > 0 {
		fmt.Fprintf(w, "  Error rate:       %.1f%% (%d errors)\n",
			stats.ErrorRate, stats.LevelCounts["ERROR"]+stats.LevelCounts["ERR"]+stats.LevelCounts["FATAL"]+stats.LevelCounts["CRITICAL"])
		fmt.Fprintf(w, "  Warn rate:        %.1f%% (%d warnings)\n",
			stats.WarnRate, stats.LevelCounts["WARN"]+stats.LevelCounts["WARNING"])

		if stats.EntriesPerMin > 0 {
			fmt.Fprintf(w, "  Avg rate:         %.0f entries/min\n", stats.EntriesPerMin)
		}

		fmt.Fprintln(w, "\n  Level distribution:")
		// Sort levels for consistent output
		levels := []string{"TRACE", "DEBUG", "INFO", "WARN", "WARNING", "ERROR", "ERR", "FATAL", "CRITICAL"}
		for _, level := range levels {
			if count, ok := stats.LevelCounts[level]; ok && count > 0 {
				pct := float64(count) / float64(stats.TotalEntries) * 100
				fmt.Fprintf(w, "    %-7s %d (%.1f%%)\n", level+":", count, pct)
			}
		}

		if len(stats.TopErrors) > 0 {
			fmt.Fprintln(w, "\n  Top errors:")
			for _, e := range stats.TopErrors {
				fmt.Fprintf(w, "    %q (%d occurrences)\n", e.Message, e.Count)
			}
		}
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	}
	return fmt.Sprintf("%.1fd", d.Hours()/24)
}
