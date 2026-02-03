// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/logs"
	"golang.org/x/sync/errgroup"
)

// Manager coordinates distributed trace execution across log viewers.
type Manager struct {
	mu              sync.RWMutex
	logManager      *logs.Manager
	traceGroups     []config.TraceGroupConfig
	logViewerConfig map[string]config.LogViewerConfig // Map of viewer name to config (for ID field access)
	reportsDir      string
	retention       time.Duration
	bus             events.EventBus
	storage         *Storage
	done            chan struct{} // signals shutdown to background goroutines
}

// NewManager creates a new trace manager.
func NewManager(logManager *logs.Manager, cfg *config.Config, bus events.EventBus) (*Manager, error) {
	// Parse max age duration
	retention := 7 * 24 * time.Hour // Default 7 days
	if cfg.Trace.MaxAge != "" {
		d, err := parseDuration(cfg.Trace.MaxAge)
		if err != nil {
			return nil, fmt.Errorf("invalid max_age duration: %w", err)
		}
		retention = d
	}

	reportsDir := cfg.Trace.ReportsDir
	if reportsDir == "" {
		reportsDir = "traces"
	}

	storage, err := NewStorage(reportsDir)
	if err != nil {
		return nil, fmt.Errorf("initializing trace storage: %w", err)
	}

	// Build log viewer config map for ID field access
	logViewerConfig := make(map[string]config.LogViewerConfig)
	for _, lvc := range cfg.LogViewers {
		logViewerConfig[lvc.Name] = lvc
	}

	m := &Manager{
		logManager:      logManager,
		traceGroups:     cfg.TraceGroups,
		logViewerConfig: logViewerConfig,
		reportsDir:      reportsDir,
		retention:       retention,
		bus:             bus,
		storage:         storage,
		done:            make(chan struct{}),
	}

	// Start background cleanup goroutine
	go m.cleanupLoop()

	return m, nil
}

// cleanupLoop periodically removes old trace reports.
func (m *Manager) cleanupLoop() {
	// Run cleanup immediately on startup
	if err := m.CleanupOldReports(); err != nil {
		log.Printf("Trace cleanup error: %v", err)
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			if err := m.CleanupOldReports(); err != nil {
				log.Printf("Trace cleanup error: %v", err)
			}
		}
	}
}

// Close shuts down the manager and stops background goroutines.
func (m *Manager) Close() error {
	close(m.done)
	return nil
}

// UpdateConfigs updates the trace configuration when switching worktrees.
// This updates the log viewer config map (used for ID field access) and trace groups.
func (m *Manager) UpdateConfigs(cfg *config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Update trace groups
	m.traceGroups = cfg.TraceGroups

	// Update reports dir (in case it uses worktree templates)
	newReportsDir := cfg.Trace.ReportsDir
	if newReportsDir == "" {
		newReportsDir = "traces"
	}
	if newReportsDir != m.reportsDir {
		m.reportsDir = newReportsDir
		if newStorage, err := NewStorage(m.reportsDir); err == nil {
			m.storage = newStorage
		} else {
			log.Printf("Warning: failed to update trace storage dir: %v", err)
		}
	}

	// Rebuild log viewer config map
	m.logViewerConfig = make(map[string]config.LogViewerConfig)
	for _, lvc := range cfg.LogViewers {
		m.logViewerConfig[lvc.Name] = lvc
	}

	log.Printf("Updated trace config with %d groups and %d log viewers", len(m.traceGroups), len(m.logViewerConfig))
}

// Execute starts a distributed trace search across all log viewers in a group.
// The search runs asynchronously and updates the report when complete.
func (m *Manager) Execute(ctx context.Context, req TraceRequest) (*ExecuteResult, error) {
	// Find the trace group and get storage reference while holding lock
	m.mu.RLock()
	group, err := m.findGroupLocked(req.Group)
	if err != nil {
		m.mu.RUnlock()
		return nil, err
	}
	viewerNames := group.LogViewers // copy before releasing lock
	storage := m.storage
	m.mu.RUnlock()

	// Generate report name if not provided
	reportName := req.Name
	if reportName == "" {
		reportName = fmt.Sprintf("%s-%s", req.TraceID, time.Now().Format("20060102-150405"))
	}

	// Sanitize name to match what storage will use for the filename
	reportName = sanitizeName(reportName)

	// Create initial report with "running" status
	createdAt := time.Now()
	report := &TraceReport{
		Version:   "1.0",
		Name:      reportName,
		TraceID:   req.TraceID,
		Group:     req.Group,
		Status:    "running",
		CreatedAt: createdAt,
		TimeRange: TimeRange{
			Start: req.Start,
			End:   req.End,
		},
		Summary: TraceSummary{
			BySource: make(map[string]int),
			ByLevel:  make(map[string]int),
		},
		Entries: []TraceEntry{},
	}

	// Save the initial report
	if _, err := storage.Save(report); err != nil {
		return nil, fmt.Errorf("saving initial report: %w", err)
	}

	// Emit trace.started event
	m.emitEvent("trace.started", map[string]any{
		"name":        reportName,
		"trace_id":    req.TraceID,
		"group":       req.Group,
		"log_viewers": viewerNames,
	})

	// Run the search asynchronously, passing the original creation time
	go m.executeAsync(reportName, req, viewerNames, createdAt)

	return &ExecuteResult{
		Name:   reportName,
		Status: "running",
	}, nil
}

// executeAsync runs the actual trace search and updates the report when done.
func (m *Manager) executeAsync(reportName string, req TraceRequest, viewerNames []string, createdAt time.Time) {
	startTime := time.Now()

	// Use a background context since we're running async
	ctx := context.Background()

	// Execute Pass 1: search for the trace pattern
	results, err := m.searchParallel(ctx, viewerNames, req.TraceID, req)
	if err != nil {
		m.updateReportFailed(reportName, req, createdAt, err)
		return
	}

	// Merge Pass 1 results
	pass1Entries := m.mergeResults(results)

	var entries []TraceEntry
	if req.ExpandByID && len(pass1Entries) > 0 {
		// Extract unique IDs from Pass 1 entries
		ids := m.extractIDs(pass1Entries)
		log.Printf("Trace: Pass 1 found %d entries, extracted %d unique IDs", len(pass1Entries), len(ids))

		if len(ids) > 0 {
			// Batch IDs to avoid command line length limits
			// Each batch creates a grep pattern; ~50 IDs keeps commands reasonable
			const maxIDsPerBatch = 50
			var allPass2Entries []TraceEntry

			for batchStart := 0; batchStart < len(ids); batchStart += maxIDsPerBatch {
				batchEnd := batchStart + maxIDsPerBatch
				if batchEnd > len(ids) {
					batchEnd = len(ids)
				}
				batchIDs := ids[batchStart:batchEnd]
				idPattern := m.buildIDPattern(batchIDs)

				log.Printf("Trace: Pass 2 batch %d-%d of %d IDs", batchStart, batchEnd, len(ids))

				// Execute Pass 2: search for this batch of IDs
				pass2Results, err := m.searchParallel(ctx, viewerNames, idPattern, req)
				if err != nil {
					log.Printf("Trace: Pass 2 batch failed: %v", err)
					continue // Skip this batch but try others
				}

				batchEntries := m.mergeResults(pass2Results)
				allPass2Entries = append(allPass2Entries, batchEntries...)
			}

			if len(allPass2Entries) > 0 {
				entries = m.deduplicateEntries(allPass2Entries)
				log.Printf("Trace: Pass 2 found %d entries, %d after dedup", len(allPass2Entries), len(entries))
			} else {
				// All batches failed, fall back to Pass 1
				log.Printf("Trace: Pass 2 found no entries, using Pass 1 results")
				entries = pass1Entries
			}
		} else {
			entries = pass1Entries
		}
	} else {
		entries = pass1Entries
	}

	// Build summary statistics
	summary := m.buildSummary(entries, time.Since(startTime))

	// Update the report with results (preserve original creation time)
	report := &TraceReport{
		Version:   "1.0",
		Name:      reportName,
		TraceID:   req.TraceID,
		Group:     req.Group,
		Status:    "completed",
		CreatedAt: createdAt,
		TimeRange: TimeRange{
			Start: req.Start,
			End:   req.End,
		},
		Summary: summary,
		Entries: entries,
	}

	// Save the completed report
	m.mu.RLock()
	storage := m.storage
	m.mu.RUnlock()
	reportPath, err := storage.Save(report)
	if err != nil {
		log.Printf("Trace: failed to save completed report %s: %v", reportName, err)
		m.emitEvent("trace.failed", map[string]any{
			"name":     reportName,
			"trace_id": req.TraceID,
			"group":    req.Group,
			"error":    fmt.Sprintf("failed to save report: %v", err),
		})
		return
	}

	// Emit trace.completed event
	m.emitEvent("trace.completed", map[string]any{
		"name":          reportName,
		"trace_id":      req.TraceID,
		"group":         req.Group,
		"total_entries": summary.TotalEntries,
		"duration_ms":   summary.DurationMS,
		"report_path":   reportPath,
	})

	log.Printf("Trace: completed %s with %d entries in %dms", reportName, summary.TotalEntries, summary.DurationMS)
}

// updateReportFailed updates a report to failed status.
func (m *Manager) updateReportFailed(reportName string, req TraceRequest, createdAt time.Time, err error) {
	log.Printf("Trace: failed %s: %v", reportName, err)

	report := &TraceReport{
		Version:   "1.0",
		Name:      reportName,
		TraceID:   req.TraceID,
		Group:     req.Group,
		Status:    "failed",
		CreatedAt: createdAt,
		TimeRange: TimeRange{
			Start: req.Start,
			End:   req.End,
		},
		Summary: TraceSummary{
			BySource: make(map[string]int),
			ByLevel:  make(map[string]int),
		},
		Entries: []TraceEntry{},
		Error:   err.Error(),
	}

	m.mu.RLock()
	storage := m.storage
	m.mu.RUnlock()
	if _, saveErr := storage.Save(report); saveErr != nil {
		log.Printf("Trace: failed to save failed report %s: %v", reportName, saveErr)
	}

	m.emitEvent("trace.failed", map[string]any{
		"name":     reportName,
		"trace_id": req.TraceID,
		"group":    req.Group,
		"error":    err.Error(),
	})
}

// searchParallel searches all log viewers in parallel for the given pattern.
func (m *Manager) searchParallel(ctx context.Context, viewerNames []string, pattern string, req TraceRequest) (map[string][]logs.LogEntry, error) {
	g, ctx := errgroup.WithContext(ctx)

	var mu sync.Mutex
	results := make(map[string][]logs.LogEntry)

	for _, viewerName := range viewerNames {
		viewerName := viewerName // capture loop variable
		g.Go(func() error {
			viewer, err := m.logManager.GetAndStart(viewerName)
			if err != nil {
				return fmt.Errorf("log viewer %s: %w", viewerName, err)
			}

			log.Printf("Trace: searching %s for %q", viewerName, pattern)
			entries, err := viewer.GetHistoricalEntries(
				ctx,
				req.Start,
				req.End,
				nil, // no filter
				0,   // no limit
				pattern,
				0, // no context before
				0, // no context after
			)
			if err != nil {
				return fmt.Errorf("%s: %w", viewerName, err)
			}

			log.Printf("Trace: %s returned %d entries", viewerName, len(entries))

			mu.Lock()
			results[viewerName] = entries
			mu.Unlock()

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}

// mergeResults merges results from all viewers and sorts by timestamp.
func (m *Manager) mergeResults(results map[string][]logs.LogEntry) []TraceEntry {
	var entries []TraceEntry

	for source, logEntries := range results {
		for _, entry := range logEntries {
			entries = append(entries, TraceEntryFromLogEntry(entry, source, false))
		}
	}

	// Sort by timestamp
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	return entries
}

// buildSummary creates summary statistics from entries.
func (m *Manager) buildSummary(entries []TraceEntry, duration time.Duration) TraceSummary {
	bySource := make(map[string]int)
	byLevel := make(map[string]int)

	for _, entry := range entries {
		bySource[entry.Source]++
		if entry.Level != "" {
			byLevel[entry.Level]++
		}
	}

	return TraceSummary{
		TotalEntries: len(entries),
		BySource:     bySource,
		ByLevel:      byLevel,
		DurationMS:   duration.Milliseconds(),
	}
}

// extractIDs extracts unique ID values from trace entries using each log viewer's configured ID field.
func (m *Manager) extractIDs(entries []TraceEntry) []string {
	// Take a snapshot of logViewerConfig under lock
	m.mu.RLock()
	logViewerConfig := m.logViewerConfig
	m.mu.RUnlock()

	idSet := make(map[string]struct{})

	for _, entry := range entries {
		// Get the log viewer config for this entry's source
		cfg, ok := logViewerConfig[entry.Source]
		if !ok || cfg.Parser.ID == "" {
			continue // No ID field configured for this viewer
		}

		// Look up the ID field value in the entry's fields
		if idValue, ok := entry.Fields[cfg.Parser.ID]; ok {
			if s, ok := idValue.(string); ok && s != "" {
				idSet[s] = struct{}{}
			}
		}
	}

	// Convert to slice
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	return ids
}

// buildIDPattern builds a regex alternation pattern for multiple IDs.
// Escapes special regex characters in each ID.
func (m *Manager) buildIDPattern(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	if len(ids) == 1 {
		return regexEscape(ids[0])
	}

	// Build alternation: (id1|id2|id3|...)
	var pattern string
	pattern = "("
	for i, id := range ids {
		if i > 0 {
			pattern += "|"
		}
		pattern += regexEscape(id)
	}
	pattern += ")"
	return pattern
}

// regexEscape escapes special regex characters in a string.
func regexEscape(s string) string {
	special := []string{"\\", ".", "+", "*", "?", "(", ")", "[", "]", "{", "}", "^", "$", "|"}
	result := s
	for _, ch := range special {
		result = replaceAll(result, ch, "\\"+ch)
	}
	return result
}

// replaceAll replaces all occurrences of old with new in s.
func replaceAll(s, old, new string) string {
	var result string
	for i := 0; i < len(s); {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}

// deduplicateEntries removes duplicate entries based on raw log line.
func (m *Manager) deduplicateEntries(entries []TraceEntry) []TraceEntry {
	seen := make(map[string]struct{})
	result := make([]TraceEntry, 0, len(entries))

	for _, entry := range entries {
		// Use combination of source and raw line as key for deduplication
		key := entry.Source + ":" + entry.Raw
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			result = append(result, entry)
		}
	}

	// Re-sort by timestamp after deduplication
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})

	return result
}

// GetReport retrieves a saved trace report by name.
func (m *Manager) GetReport(name string) (*TraceReport, error) {
	m.mu.RLock()
	storage := m.storage
	m.mu.RUnlock()
	return storage.Load(name)
}

// ListReports returns summaries of all saved reports.
func (m *Manager) ListReports() ([]ReportSummary, error) {
	m.mu.RLock()
	storage := m.storage
	m.mu.RUnlock()
	return storage.List()
}

// DeleteReport removes a saved report.
func (m *Manager) DeleteReport(name string) error {
	m.mu.RLock()
	storage := m.storage
	m.mu.RUnlock()
	return storage.Delete(name)
}

// GetGroups returns all configured trace groups.
func (m *Manager) GetGroups() []TraceGroup {
	m.mu.RLock()
	defer m.mu.RUnlock()
	groups := make([]TraceGroup, len(m.traceGroups))
	for i, cfg := range m.traceGroups {
		groups[i] = TraceGroup{
			Name:       cfg.Name,
			LogViewers: cfg.LogViewers,
		}
	}
	return groups
}

// CleanupOldReports removes reports older than the retention period.
func (m *Manager) CleanupOldReports() error {
	m.mu.RLock()
	storage := m.storage
	retention := m.retention
	m.mu.RUnlock()
	cutoff := time.Now().Add(-retention)
	return storage.DeleteOlderThan(cutoff)
}

// findGroup looks up a trace group by name.
func (m *Manager) findGroup(name string) (*config.TraceGroupConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.findGroupLocked(name)
}

// findGroupLocked looks up a trace group by name.
// Caller must hold m.mu.RLock() or m.mu.Lock().
func (m *Manager) findGroupLocked(name string) (*config.TraceGroupConfig, error) {
	for i := range m.traceGroups {
		if m.traceGroups[i].Name == name {
			return &m.traceGroups[i], nil
		}
	}
	return nil, fmt.Errorf("trace group not found: %s", name)
}

// emitEvent publishes a trace event.
func (m *Manager) emitEvent(eventType string, payload map[string]any) {
	if m.bus == nil {
		log.Printf("Trace: cannot emit %s event - event bus is nil", eventType)
		return
	}
	event := events.Event{
		Type:    eventType,
		Payload: payload,
	}
	if err := m.bus.Publish(context.Background(), event); err != nil {
		log.Printf("Failed to publish %s event: %v", eventType, err)
	}
}

// sanitizeName sanitizes a report name to be safe for use as a filename.
// This matches the sanitization done by Storage.reportPath.
func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "..", "_")
	return name
}

// parseDuration parses a duration string that may include days (e.g., "7d").
func parseDuration(s string) (time.Duration, error) {
	// Check for day suffix
	if len(s) > 1 && s[len(s)-1] == 'd' {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	return time.ParseDuration(s)
}
