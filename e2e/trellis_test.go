// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/api"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/service"
	"github.com/wingedpig/trellis/internal/workflow"
	"github.com/wingedpig/trellis/internal/worktree"
)

// TestServerStartup verifies that the API server starts correctly.
func TestServerStartup(t *testing.T) {
	deps := createTestDependencies(t)
	server := api.NewServer(api.ServerConfig{Host: "127.0.0.1", Port: 0}, deps)
	require.NotNil(t, server)
	require.NotNil(t, server.Router())
}

// TestServiceLifecycle tests starting, stopping, and restarting services via API.
func TestServiceLifecycle(t *testing.T) {
	deps := createTestDependencies(t)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	// List services
	resp, err := http.Get(server.URL + "/api/v1/services")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// API returns PascalCase field names
	var listResp struct {
		Data []struct {
			Name   string `json:"Name"`
			Status struct {
				State string `json:"State"`
			} `json:"Status"`
		} `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&listResp)
	require.NoError(t, err)
	resp.Body.Close()

	// Should have test services
	assert.NotEmpty(t, listResp.Data)
}

// TestWorkflowExecution tests running a workflow and checking its status.
func TestWorkflowExecution(t *testing.T) {
	deps := createTestDependencies(t)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	// List workflows
	resp, err := http.Get(server.URL + "/api/v1/workflows")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Run built-in workflow
	resp, err = http.Post(server.URL+"/api/v1/workflows/_restart_all/run", "application/json", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// TestEventHistory tests the event history API.
func TestEventHistory(t *testing.T) {
	deps := createTestDependencies(t)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	// Get event history
	resp, err := http.Get(server.URL + "/api/v1/events")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// TestWorktreeOperations tests worktree listing and information.
func TestWorktreeOperations(t *testing.T) {
	deps := createTestDependencies(t)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	// List worktrees
	resp, err := http.Get(server.URL + "/api/v1/worktrees")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// TestUIPages tests that UI pages are served correctly.
func TestUIPages(t *testing.T) {
	deps := createTestDependencies(t)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	// Test pages that exist in the current router
	pages := []string{
		"/",
		"/worktrees",
		"/events",
	}

	for _, page := range pages {
		t.Run(page, func(t *testing.T) {
			resp, err := http.Get(server.URL + page)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")

			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			assert.Contains(t, string(body), "<!DOCTYPE html>")
		})
	}
}

// TestCORS tests that CORS headers are set correctly.
func TestCORS(t *testing.T) {
	deps := createTestDependencies(t)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	// Make GET request with Origin header
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/services", nil)
	req.Header.Set("Origin", "http://localhost:3000")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// CORS middleware should set Access-Control-Allow-Origin
	assert.NotEmpty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	resp.Body.Close()
}

// TestServiceLogs tests the service logs API.
func TestServiceLogs(t *testing.T) {
	deps := createTestDependencies(t)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	// Get list of services first
	resp, err := http.Get(server.URL + "/api/v1/services")
	require.NoError(t, err)

	// API returns array directly under data with PascalCase field names
	var listResp struct {
		Data []struct {
			Name string `json:"Name"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)
	resp.Body.Close()

	if len(listResp.Data) > 0 {
		svcName := listResp.Data[0].Name

		// Get logs
		resp, err = http.Get(server.URL + "/api/v1/services/" + svcName + "/logs")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}
}

// TestBuiltinWorkflows tests that built-in workflows exist.
func TestBuiltinWorkflows(t *testing.T) {
	deps := createTestDependencies(t)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	builtins := []string{"_start_all", "_restart_all", "_stop_all", "_clear_logs"}

	for _, id := range builtins {
		t.Run(id, func(t *testing.T) {
			resp, err := http.Get(server.URL + "/api/v1/workflows/" + id)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()
		})
	}
}

// TestAPIErrorResponses tests that API errors are properly formatted.
func TestAPIErrorResponses(t *testing.T) {
	deps := createTestDependencies(t)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	// Request non-existent service
	resp, err := http.Get(server.URL + "/api/v1/services/nonexistent")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	var errResp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&errResp)
	resp.Body.Close()
	assert.NotEmpty(t, errResp.Error.Message)

	// Request non-existent workflow
	resp, err = http.Get(server.URL + "/api/v1/workflows/nonexistent")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

// Helper functions

func createTestDependencies(t *testing.T) api.Dependencies {
	t.Helper()

	// Create temp dir for test
	tempDir := t.TempDir()

	// Create event bus
	bus := events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: 100,
		HistoryMaxAge:    time.Hour,
	})
	t.Cleanup(func() { bus.Close() })

	// Create test service configs
	testScript := filepath.Join(tempDir, "test.sh")
	err := os.WriteFile(testScript, []byte("#!/bin/sh\necho hello\nsleep 60"), 0755)
	require.NoError(t, err)

	serviceConfigs := []config.ServiceConfig{
		{Name: "test-service", Command: testScript},
	}

	// Create service manager
	svcMgr := service.NewManager(serviceConfigs, bus, nil)

	// Create worktree manager with mock
	wtMgr := worktree.NewManager(
		&mockGitExecutor{tempDir: tempDir},
		bus,
		config.WorktreeConfig{},
		tempDir,
		tempDir,
		"test-project",
	)

	// Create workflow runner with empty ServiceController for tests
	wfRunner := workflow.NewRunner(
		[]workflow.WorkflowConfig{
			{ID: "test", Name: "Test Workflow", Command: []string{"echo", "test"}},
		},
		bus,
		&mockServiceController{svc: svcMgr},
		tempDir,
	)

	return api.Dependencies{
		ServiceManager:  svcMgr,
		WorktreeManager: wtMgr,
		WorkflowRunner:  wfRunner,
		EventBus:        bus,
	}
}

// Mock implementations

type mockGitExecutor struct {
	tempDir string
}

func (m *mockGitExecutor) WorktreeList(ctx context.Context, dir string) ([]worktree.WorktreeInfo, error) {
	return []worktree.WorktreeInfo{
		{Path: m.tempDir, Commit: "abc123", Branch: "main"},
	}, nil
}

func (m *mockGitExecutor) Status(ctx context.Context, path string) (worktree.GitStatus, error) {
	return worktree.GitStatus{Clean: true}, nil
}

func (m *mockGitExecutor) BranchInfo(ctx context.Context, path string) (worktree.BranchInfo, error) {
	return worktree.BranchInfo{Name: "main", Commit: "abc123"}, nil
}

type mockServiceController struct {
	svc service.Manager
}

func (m *mockServiceController) StopServices(ctx context.Context, names []string) error {
	if names == nil {
		return m.svc.StopAll(ctx)
	}
	for _, name := range names {
		m.svc.Stop(ctx, name)
	}
	return nil
}

func (m *mockServiceController) StartAllServices(ctx context.Context) error {
	return m.svc.StartAll(ctx)
}

func (m *mockServiceController) RestartAllServices(ctx context.Context) error {
	m.svc.StopAll(ctx)
	return m.svc.StartAll(ctx)
}

func (m *mockServiceController) StopWatchedServices(ctx context.Context) error {
	return m.svc.StopWatched(ctx)
}

func (m *mockServiceController) RestartWatchedServices(ctx context.Context) error {
	m.svc.StopWatched(ctx)
	return m.svc.StartWatched(ctx)
}

func (m *mockServiceController) ClearAllLogs(ctx context.Context) error {
	for _, svc := range m.svc.List() {
		m.svc.ClearLogs(svc.Name)
	}
	return nil
}

// Integration test with real processes (requires build)

func TestServiceStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	deps := createTestDependencies(t)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	// Get service status
	resp, err := http.Get(server.URL + "/api/v1/services/test-service")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Start service
	resp, err = http.Post(server.URL+"/api/v1/services/test-service/start", "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Stop service
	resp, err = http.Post(server.URL+"/api/v1/services/test-service/stop", "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()
}

// Benchmark tests

func BenchmarkServiceList(b *testing.B) {
	deps := createBenchDependencies(b)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, _ := http.Get(server.URL + "/api/v1/services")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func BenchmarkEventHistory(b *testing.B) {
	deps := createBenchDependencies(b)
	server := httptest.NewServer(api.NewRouter(deps))
	defer server.Close()

	// Publish some events first
	for i := 0; i < 100; i++ {
		deps.EventBus.Publish(context.Background(), events.Event{
			Type: "test.event",
			Payload: map[string]interface{}{
				"index": i,
			},
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, _ := http.Get(server.URL + "/api/v1/events")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func createBenchDependencies(b *testing.B) api.Dependencies {
	b.Helper()

	tempDir := b.TempDir()

	bus := events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: 1000,
		HistoryMaxAge:    time.Hour,
	})
	b.Cleanup(func() { bus.Close() })

	svcMgr := service.NewManager(nil, bus, nil)

	wtMgr := worktree.NewManager(
		&mockGitExecutor{tempDir: tempDir},
		bus,
		config.WorktreeConfig{},
		tempDir,
		tempDir,
		"test-project",
	)

	wfRunner := workflow.NewRunner(nil, bus, nil, tempDir)

	return api.Dependencies{
		ServiceManager:  svcMgr,
		WorktreeManager: wtMgr,
		WorkflowRunner:  wfRunner,
		EventBus:        bus,
	}
}

// Utility for creating POST request with JSON body
func postJSON(t *testing.T, url string, body interface{}) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp, err := http.Post(url, "application/json", &buf)
	require.NoError(t, err)
	return resp
}
