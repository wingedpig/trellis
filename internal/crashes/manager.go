// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package crashes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/logs"
	"github.com/wingedpig/trellis/internal/service"
	"github.com/wingedpig/trellis/internal/worktree"
)

const crashReportVersion = "1.0"

// Config holds configuration for crash storage.
type Config struct {
	ReportsDir string        // Directory to store crash files
	MaxAge     time.Duration // Max age of crashes to keep
	MaxCount   int           // Max number of crashes to keep
}

// Manager handles crash capture and storage.
type Manager struct {
	mu              sync.RWMutex
	config          Config
	serviceManager  service.Manager
	worktreeManager worktree.Manager
	eventBus        events.EventBus
	defaultIDField  string            // Default field name for trace IDs
	serviceIDFields map[string]string // Per-service ID field overrides
	stackField      string            // Field name containing stack trace
}

// NewManager creates a new crash manager.
func NewManager(cfg Config, svcMgr service.Manager, wtMgr worktree.Manager, bus events.EventBus, defaultIDField string, serviceIDFields map[string]string, stackField string) (*Manager, error) {
	// Set defaults
	if cfg.ReportsDir == "" {
		cfg.ReportsDir = ".trellis/crashes"
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 7 * 24 * time.Hour // 7 days
	}
	if cfg.MaxCount == 0 {
		cfg.MaxCount = 100
	}

	// Ensure directory exists
	if err := os.MkdirAll(cfg.ReportsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create crashes directory: %w", err)
	}

	if serviceIDFields == nil {
		serviceIDFields = make(map[string]string)
	}

	return &Manager{
		config:          cfg,
		serviceManager:  svcMgr,
		worktreeManager: wtMgr,
		eventBus:        bus,
		defaultIDField:  defaultIDField,
		serviceIDFields: serviceIDFields,
		stackField:      stackField,
	}, nil
}

// Subscribe subscribes to crash events from the event bus.
func (m *Manager) Subscribe() error {
	if m.eventBus == nil {
		return nil
	}

	_, err := m.eventBus.Subscribe(events.EventServiceCrashed, func(ctx context.Context, e events.Event) error {
		m.handleCrashEvent(e)
		return nil
	})
	return err
}

// handleCrashEvent processes a service.crashed event.
func (m *Manager) handleCrashEvent(e events.Event) {
	serviceName, ok := e.Payload["service"].(string)
	if !ok {
		return
	}

	// Build crash record
	crash := Crash{
		Version:   crashReportVersion,
		ID:        generateCrashID(),
		Service:   serviceName,
		Timestamp: e.Timestamp,
		Trigger:   "service.crashed",
	}

	// Extract exit code and error
	if exitCode, ok := e.Payload["exitCode"].(int); ok {
		crash.ExitCode = exitCode
	}
	if reason, ok := e.Payload["reason"].(string); ok {
		crash.Error = reason
	}

	// Get worktree info
	if m.worktreeManager != nil {
		if active := m.worktreeManager.Active(); active != nil {
			crash.Worktree = WorktreeInfo{
				Name:   active.Name(),
				Branch: active.Branch,
				Path:   active.Path,
			}
		}
	}

	// Collect all recent parsed logs from services
	allLogs := m.collectParsedLogs()

	// Extract stack trace from the last log entry of crashed service
	if m.stackField != "" {
		if svcLogs, ok := allLogs[serviceName]; ok && len(svcLogs) > 0 {
			// Scan backwards for a log entry with the stack field
			for i := len(svcLogs) - 1; i >= 0; i-- {
				if stackTrace := getFieldAsString(svcLogs[i].Fields, m.stackField); stackTrace != "" {
					if crash.Error != "" {
						crash.Error = crash.Error + "\n\n" + stackTrace
					} else {
						crash.Error = stackTrace
					}
					break
				}
			}
		}
	}

	// Find request trace ID by scanning backwards through crashed service's logs
	crash.TraceID = m.findRequestTraceIDFromEntries(allLogs, serviceName)

	// Filter and convert logs to entries
	var entries []CrashEntry
	if crash.TraceID != "" {
		entries = m.filterEntriesByTraceID(allLogs, crash.TraceID)
	} else {
		// No trace ID found - include entries from crashed service only
		if svcLogs, ok := allLogs[serviceName]; ok {
			for _, entry := range svcLogs {
				entries = append(entries, logEntryToCrashEntry(entry, serviceName))
			}
		}
	}

	// Sort entries by timestamp
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	crash.Entries = entries
	crash.Summary = m.buildSummary(entries)

	// Save crash
	if err := m.Save(crash); err != nil {
		// Log error but don't fail
		fmt.Fprintf(os.Stderr, "Failed to save crash: %v\n", err)
	}

	// Cleanup old crashes
	m.cleanup()
}

// collectParsedLogs collects recent parsed logs from all services.
func (m *Manager) collectParsedLogs() map[string][]*logs.LogEntry {
	result := make(map[string][]*logs.LogEntry)
	if m.serviceManager == nil {
		return result
	}

	for _, svc := range m.serviceManager.List() {
		entries, err := m.serviceManager.ParsedLogs(svc.Name, 500)
		if err != nil {
			continue
		}
		if len(entries) > 0 {
			result[svc.Name] = entries
		}
	}
	return result
}

// filterEntriesByTraceID filters parsed log entries to only include those matching the trace ID.
func (m *Manager) filterEntriesByTraceID(allLogs map[string][]*logs.LogEntry, traceID string) []CrashEntry {
	var result []CrashEntry

	for serviceName, entries := range allLogs {
		idField := m.getIDFieldForService(serviceName)

		for _, entry := range entries {
			// Check if entry has the trace ID in its fields
			if entryID := getFieldAsString(entry.Fields, idField); entryID == traceID {
				result = append(result, logEntryToCrashEntry(entry, serviceName))
			}
		}
	}

	return result
}

// findRequestTraceIDFromEntries scans service logs to find the request trace ID.
func (m *Manager) findRequestTraceIDFromEntries(allLogs map[string][]*logs.LogEntry, crashedService string) string {
	entries, ok := allLogs[crashedService]
	if !ok || len(entries) == 0 {
		return ""
	}

	idField := m.getIDFieldForService(crashedService)

	// Get the trace ID from the last log entry (the crash line)
	lastEntry := entries[len(entries)-1]
	crashTraceID := getFieldAsString(lastEntry.Fields, idField)

	// Scan backwards looking for a trace ID
	for i := len(entries) - 2; i >= 0; i-- {
		lineTraceID := getFieldAsString(entries[i].Fields, idField)
		if lineTraceID != "" {
			// If crash line had no ID, return the first ID we find
			// If crash line had an ID, return the first DIFFERENT ID we find
			if crashTraceID == "" || lineTraceID != crashTraceID {
				return lineTraceID
			}
		}
	}

	// If no different ID found (or no IDs at all), return whatever the crash line had
	return crashTraceID
}

// logEntryToCrashEntry converts a logs.LogEntry to a CrashEntry.
func logEntryToCrashEntry(entry *logs.LogEntry, source string) CrashEntry {
	return CrashEntry{
		Timestamp: entry.Timestamp,
		Source:    source,
		Level:     string(entry.Level),
		Message:   entry.Message,
		Fields:    entry.Fields,
		Raw:       entry.Raw,
	}
}

// getFieldAsString extracts a field value as a string.
func getFieldAsString(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	if val, ok := fields[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}

// buildSummary builds summary statistics from crash entries.
func (m *Manager) buildSummary(entries []CrashEntry) CrashStats {
	summary := CrashStats{
		TotalEntries: len(entries),
		BySource:     make(map[string]int),
		ByLevel:      make(map[string]int),
	}

	for _, e := range entries {
		summary.BySource[e.Source]++
		if e.Level != "" {
			summary.ByLevel[e.Level]++
		}
	}

	return summary
}

// getIDFieldForService returns the ID field name for a specific service.
// Uses per-service override if available, otherwise falls back to default.
func (m *Manager) getIDFieldForService(serviceName string) string {
	if idField, ok := m.serviceIDFields[serviceName]; ok && idField != "" {
		return idField
	}
	if m.defaultIDField != "" {
		return m.defaultIDField
	}
	return "id" // fallback default
}

// extractTraceID extracts a trace ID from a raw log line (used for tests).
func extractTraceID(logLine, idField string) string {
	// Try JSON parsing first
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(logLine), &logEntry); err == nil {
		if id, ok := logEntry[idField].(string); ok {
			return id
		}
	}

	// Fall back to regex for non-JSON logs
	pattern := regexp.MustCompile(fmt.Sprintf(`"%s"\s*:\s*"([^"]+)"`, regexp.QuoteMeta(idField)))
	if matches := pattern.FindStringSubmatch(logLine); len(matches) >= 2 {
		return matches[1]
	}

	return ""
}

// generateCrashID generates a unique crash ID based on timestamp.
func generateCrashID() string {
	return time.Now().Format("20060102-150405.000")
}

// Save saves a crash to disk.
func (m *Manager) Save(crash Crash) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filename := filepath.Join(m.config.ReportsDir, crash.ID+".json")
	data, err := json.MarshalIndent(crash, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal crash: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write crash file: %w", err)
	}

	return nil
}

// List returns all crashes, sorted by timestamp (newest first).
func (m *Manager) List() ([]CrashSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries, err := os.ReadDir(m.config.ReportsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read crashes directory: %w", err)
	}

	var summaries []CrashSummary
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		crash, err := m.loadCrash(entry.Name())
		if err != nil {
			continue
		}

		summaries = append(summaries, CrashSummary{
			ID:        crash.ID,
			Service:   crash.Service,
			Timestamp: crash.Timestamp,
			TraceID:   crash.TraceID,
			ExitCode:  crash.ExitCode,
			Error:     crash.Error,
		})
	}

	// Sort by timestamp descending (newest first)
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Timestamp.After(summaries[j].Timestamp)
	})

	return summaries, nil
}

// Get retrieves a specific crash by ID.
func (m *Manager) Get(id string) (*Crash, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.loadCrash(id + ".json")
}

// Newest returns the most recent crash.
func (m *Manager) Newest() (*Crash, error) {
	summaries, err := m.List()
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return nil, nil
	}

	return m.Get(summaries[0].ID)
}

// Delete removes a crash by ID.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filename := filepath.Join(m.config.ReportsDir, id+".json")
	if err := os.Remove(filename); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("crash not found: %s", id)
		}
		return fmt.Errorf("failed to delete crash: %w", err)
	}
	return nil
}

// Clear removes all crashes.
func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.config.ReportsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read crashes directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		os.Remove(filepath.Join(m.config.ReportsDir, entry.Name()))
	}

	return nil
}

// loadCrash loads a crash from disk.
func (m *Manager) loadCrash(filename string) (*Crash, error) {
	data, err := os.ReadFile(filepath.Join(m.config.ReportsDir, filename))
	if err != nil {
		return nil, fmt.Errorf("failed to read crash file: %w", err)
	}

	var crash Crash
	if err := json.Unmarshal(data, &crash); err != nil {
		return nil, fmt.Errorf("failed to unmarshal crash: %w", err)
	}

	return &crash, nil
}

// cleanup removes old crashes based on age and count limits.
func (m *Manager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.config.ReportsDir)
	if err != nil {
		return
	}

	type crashFile struct {
		name      string
		timestamp time.Time
	}

	var files []crashFile
	cutoff := time.Now().Add(-m.config.MaxAge)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Parse timestamp from filename (format: 20060102-150405.000.json)
		idPart := strings.TrimSuffix(entry.Name(), ".json")
		ts, err := time.ParseInLocation("20060102-150405.000", idPart, time.Local)
		if err != nil {
			continue
		}

		// Remove if too old
		if ts.Before(cutoff) {
			os.Remove(filepath.Join(m.config.ReportsDir, entry.Name()))
			continue
		}

		files = append(files, crashFile{name: entry.Name(), timestamp: ts})
	}

	// Sort by timestamp descending
	sort.Slice(files, func(i, j int) bool {
		return files[i].timestamp.After(files[j].timestamp)
	})

	// Remove excess files
	if len(files) > m.config.MaxCount {
		for _, f := range files[m.config.MaxCount:] {
			os.Remove(filepath.Join(m.config.ReportsDir, f.name))
		}
	}
}

// UpdateConfig updates the manager configuration (e.g., after worktree switch).
func (m *Manager) UpdateConfig(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.ReportsDir != "" {
		m.config.ReportsDir = cfg.ReportsDir
		os.MkdirAll(m.config.ReportsDir, 0755)
	}
	if cfg.MaxAge > 0 {
		m.config.MaxAge = cfg.MaxAge
	}
	if cfg.MaxCount > 0 {
		m.config.MaxCount = cfg.MaxCount
	}
}

// UpdateServiceIDFields updates the per-service ID field mappings.
func (m *Manager) UpdateServiceIDFields(serviceIDFields map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if serviceIDFields == nil {
		m.serviceIDFields = make(map[string]string)
	} else {
		m.serviceIDFields = serviceIDFields
	}
}
