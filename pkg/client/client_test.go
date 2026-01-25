// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockServer creates a test server that returns the given response.
func mockServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

// apiHandler creates a handler that returns a standard API response.
func apiHandler(data interface{}, statusCode int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)

		resp := map[string]interface{}{
			"data": data,
		}
		json.NewEncoder(w).Encode(resp)
	}
}

// apiErrorHandler creates a handler that returns an API error.
func apiErrorHandler(code, message string, statusCode int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)

		resp := map[string]interface{}{
			"error": map[string]string{
				"code":    code,
				"message": message,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}
}

func TestNew(t *testing.T) {
	c := New("http://localhost:8080")

	if c.BaseURL() != "http://localhost:8080" {
		t.Errorf("BaseURL() = %q, want %q", c.BaseURL(), "http://localhost:8080")
	}

	if c.Version() != LatestVersion {
		t.Errorf("Version() = %q, want %q", c.Version(), LatestVersion)
	}

	// Test sub-clients are initialized
	if c.Services == nil {
		t.Error("Services client is nil")
	}
	if c.Worktrees == nil {
		t.Error("Worktrees client is nil")
	}
	if c.Workflows == nil {
		t.Error("Workflows client is nil")
	}
	if c.Events == nil {
		t.Error("Events client is nil")
	}
	if c.Logs == nil {
		t.Error("Logs client is nil")
	}
	if c.Trace == nil {
		t.Error("Trace client is nil")
	}
	if c.Notify == nil {
		t.Error("Notify client is nil")
	}
}

func TestNewWithOptions(t *testing.T) {
	t.Run("WithVersion", func(t *testing.T) {
		c := New("http://localhost:8080", WithVersion("2026-01-01"))
		if c.Version() != "2026-01-01" {
			t.Errorf("Version() = %q, want %q", c.Version(), "2026-01-01")
		}
	})

	t.Run("WithTimeout", func(t *testing.T) {
		c := New("http://localhost:8080", WithTimeout(60*time.Second))
		// We can't directly check the timeout, but we verify it doesn't panic
		if c == nil {
			t.Error("Client is nil")
		}
	})

	t.Run("WithHTTPClient", func(t *testing.T) {
		customClient := &http.Client{Timeout: 10 * time.Second}
		c := New("http://localhost:8080", WithHTTPClient(customClient))
		if c == nil {
			t.Error("Client is nil")
		}
	})

	t.Run("trailing slash removed", func(t *testing.T) {
		c := New("http://localhost:8080/")
		if c.BaseURL() != "http://localhost:8080" {
			t.Errorf("BaseURL() = %q, want trailing slash removed", c.BaseURL())
		}
	})
}

func TestAPIError(t *testing.T) {
	err := &APIError{
		Code:    "not_found",
		Message: "Service not found",
	}

	expected := "not_found: Service not found"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}

	// Test without code
	err2 := &APIError{
		Message: "Something went wrong",
	}
	if err2.Error() != "Something went wrong" {
		t.Errorf("Error() = %q, want %q", err2.Error(), "Something went wrong")
	}
}

func TestVersionHeader(t *testing.T) {
	var receivedVersion string
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		receivedVersion = r.Header.Get("Trellis-Version")
		apiHandler([]Service{}, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL, WithVersion("2026-01-17"))
	_, _ = c.Services.List(context.Background())

	if receivedVersion != "2026-01-17" {
		t.Errorf("Trellis-Version header = %q, want %q", receivedVersion, "2026-01-17")
	}
}

func TestServiceClient_List(t *testing.T) {
	services := []Service{
		{
			Name:    "backend",
			Enabled: true,
			Status: ServiceStatus{
				State: ServiceStateRunning,
				PID:   1234,
			},
		},
		{
			Name:    "frontend",
			Enabled: true,
			Status: ServiceStatus{
				State: ServiceStateStopped,
			},
		},
	}

	server := mockServer(t, apiHandler(services, http.StatusOK))
	defer server.Close()

	c := New(server.URL)
	result, err := c.Services.List(context.Background())

	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(result) != 2 {
		t.Errorf("List() returned %d services, want 2", len(result))
	}

	if result[0].Name != "backend" {
		t.Errorf("result[0].Name = %q, want %q", result[0].Name, "backend")
	}

	if result[0].Status.State != ServiceStateRunning {
		t.Errorf("result[0].Status.State = %q, want %q", result[0].Status.State, ServiceStateRunning)
	}
}

func TestServiceClient_Get(t *testing.T) {
	service := Service{
		Name:    "backend",
		Enabled: true,
		Status: ServiceStatus{
			State: ServiceStateRunning,
			PID:   1234,
		},
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/services/backend" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(service, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Services.Get(context.Background(), "backend")

	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if result.Name != "backend" {
		t.Errorf("Name = %q, want %q", result.Name, "backend")
	}
}

func TestServiceClient_Start(t *testing.T) {
	service := Service{
		Name: "backend",
		Status: ServiceStatus{
			State: ServiceStateRunning,
			PID:   1234,
		},
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/services/backend/start" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(service, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Services.Start(context.Background(), "backend")

	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if result.Status.State != ServiceStateRunning {
		t.Errorf("State = %q, want %q", result.Status.State, ServiceStateRunning)
	}
}

func TestServiceClient_Stop(t *testing.T) {
	service := Service{
		Name: "backend",
		Status: ServiceStatus{
			State: ServiceStateStopped,
		},
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/services/backend/stop" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(service, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Services.Stop(context.Background(), "backend")

	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if result.Status.State != ServiceStateStopped {
		t.Errorf("State = %q, want %q", result.Status.State, ServiceStateStopped)
	}
}

func TestServiceClient_Restart(t *testing.T) {
	service := Service{
		Name: "backend",
		Status: ServiceStatus{
			State:        ServiceStateRunning,
			PID:          5678,
			RestartCount: 1,
		},
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/services/backend/restart" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(service, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Services.Restart(context.Background(), "backend")

	if err != nil {
		t.Fatalf("Restart() error = %v", err)
	}

	if result.Status.RestartCount != 1 {
		t.Errorf("RestartCount = %d, want 1", result.Status.RestartCount)
	}
}

func TestServiceClient_Logs(t *testing.T) {
	logData := map[string]interface{}{
		"service": "backend",
		"lines":   []string{"line1", "line2", "line3"},
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/services/backend/logs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("lines") != "100" {
			t.Errorf("lines param = %s, want 100", r.URL.Query().Get("lines"))
		}
		apiHandler(logData, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Services.Logs(context.Background(), "backend", 100)

	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}

	if len(result) == 0 {
		t.Error("Logs() returned empty result")
	}
}

func TestServiceClient_ClearLogs(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("Method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/services/backend/logs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(nil, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	err := c.Services.ClearLogs(context.Background(), "backend")

	if err != nil {
		t.Fatalf("ClearLogs() error = %v", err)
	}
}

func TestServiceClient_Error(t *testing.T) {
	server := mockServer(t, apiErrorHandler("not_found", "Service not found", http.StatusNotFound))
	defer server.Close()

	c := New(server.URL)
	_, err := c.Services.Get(context.Background(), "unknown")

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}

	if apiErr.Code != "not_found" {
		t.Errorf("Code = %q, want %q", apiErr.Code, "not_found")
	}
}

func TestWorktreeClient_List(t *testing.T) {
	worktrees := []Worktree{
		{
			Path:   "/home/user/project",
			Branch: "main",
			Active: true,
		},
		{
			Path:   "/home/user/project-feature",
			Branch: "feature",
			Active: false,
			Dirty:  true,
		},
	}

	server := mockServer(t, apiHandler(worktrees, http.StatusOK))
	defer server.Close()

	c := New(server.URL)
	result, err := c.Worktrees.List(context.Background())

	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(result) != 2 {
		t.Errorf("List() returned %d worktrees, want 2", len(result))
	}

	if result[0].Branch != "main" {
		t.Errorf("result[0].Branch = %q, want %q", result[0].Branch, "main")
	}

	if !result[0].Active {
		t.Error("result[0].Active = false, want true")
	}
}

func TestWorktreeClient_Get(t *testing.T) {
	worktree := Worktree{
		Path:   "/home/user/project-feature",
		Branch: "feature",
		Commit: "abc123",
		Dirty:  true,
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/worktrees/feature" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(worktree, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Worktrees.Get(context.Background(), "feature")

	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if result.Branch != "feature" {
		t.Errorf("Branch = %q, want %q", result.Branch, "feature")
	}
}

func TestWorktreeClient_Activate(t *testing.T) {
	activateResult := ActivateResult{
		Worktree: Worktree{
			Path:   "/home/user/project-feature",
			Branch: "feature",
			Active: true,
		},
		Duration: "2.5s",
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/worktrees/feature/activate" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(activateResult, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Worktrees.Activate(context.Background(), "feature")

	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	if !result.Worktree.Active {
		t.Error("Worktree.Active = false, want true")
	}

	if result.Duration != "2.5s" {
		t.Errorf("Duration = %q, want %q", result.Duration, "2.5s")
	}
}

func TestWorktreeClient_Remove(t *testing.T) {
	t.Run("without delete branch", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Errorf("Method = %s, want DELETE", r.Method)
			}
			if r.URL.Path != "/api/v1/worktrees/feature" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.URL.Query().Get("delete_branch") != "" {
				t.Error("delete_branch should not be set")
			}
			apiHandler(nil, http.StatusOK)(w, r)
		})
		defer server.Close()

		c := New(server.URL)
		err := c.Worktrees.Remove(context.Background(), "feature", nil)

		if err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
	})

	t.Run("with delete branch", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("delete_branch") != "1" {
				t.Errorf("delete_branch = %q, want %q", r.URL.Query().Get("delete_branch"), "1")
			}
			apiHandler(nil, http.StatusOK)(w, r)
		})
		defer server.Close()

		c := New(server.URL)
		err := c.Worktrees.Remove(context.Background(), "feature", &RemoveOptions{DeleteBranch: true})

		if err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
	})
}

func TestWorktree_Name(t *testing.T) {
	wt := Worktree{Path: "/home/user/project-feature"}
	if wt.Name() != "project-feature" {
		t.Errorf("Name() = %q, want %q", wt.Name(), "project-feature")
	}
}

func TestWorkflowClient_List(t *testing.T) {
	workflows := []Workflow{
		{
			ID:   "build",
			Name: "Build All",
		},
		{
			ID:      "test",
			Name:    "Run Tests",
			Confirm: true,
		},
	}

	server := mockServer(t, apiHandler(workflows, http.StatusOK))
	defer server.Close()

	c := New(server.URL)
	result, err := c.Workflows.List(context.Background())

	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(result) != 2 {
		t.Errorf("List() returned %d workflows, want 2", len(result))
	}

	if result[0].ID != "build" {
		t.Errorf("result[0].ID = %q, want %q", result[0].ID, "build")
	}
}

func TestWorkflowClient_Get(t *testing.T) {
	workflow := Workflow{
		ID:      "build",
		Name:    "Build All",
		Command: []string{"make", "build"},
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workflows/build" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(workflow, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Workflows.Get(context.Background(), "build")

	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if result.ID != "build" {
		t.Errorf("ID = %q, want %q", result.ID, "build")
	}
}

func TestWorkflowClient_Run(t *testing.T) {
	status := WorkflowStatus{
		ID:    "build-123",
		Name:  "Build All",
		State: WorkflowStateRunning,
	}

	t.Run("without options", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("Method = %s, want POST", r.Method)
			}
			if r.URL.Path != "/api/v1/workflows/build/run" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			apiHandler(status, http.StatusOK)(w, r)
		})
		defer server.Close()

		c := New(server.URL)
		result, err := c.Workflows.Run(context.Background(), "build", nil)

		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		if result.State != WorkflowStateRunning {
			t.Errorf("State = %q, want %q", result.State, WorkflowStateRunning)
		}
	})

	t.Run("with worktree option", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("worktree") != "feature" {
				t.Errorf("worktree = %q, want %q", r.URL.Query().Get("worktree"), "feature")
			}
			apiHandler(status, http.StatusOK)(w, r)
		})
		defer server.Close()

		c := New(server.URL)
		_, err := c.Workflows.Run(context.Background(), "build", &RunOptions{Worktree: "feature"})

		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	})
}

func TestWorkflowClient_Status(t *testing.T) {
	status := WorkflowStatus{
		ID:       "build-123",
		Name:     "Build All",
		State:    WorkflowStateSuccess,
		Success:  true,
		ExitCode: 0,
		Output:   "Build successful",
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workflows/build-123/status" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(status, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Workflows.Status(context.Background(), "build-123")

	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if !result.Success {
		t.Error("Success = false, want true")
	}

	if result.State != WorkflowStateSuccess {
		t.Errorf("State = %q, want %q", result.State, WorkflowStateSuccess)
	}
}

func TestEventClient_List(t *testing.T) {
	events := []Event{
		{
			ID:        "evt-1",
			Type:      "service.started",
			Timestamp: time.Now(),
			Worktree:  "main",
		},
		{
			ID:        "evt-2",
			Type:      "service.stopped",
			Timestamp: time.Now(),
			Worktree:  "main",
		},
	}

	t.Run("with limit", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("limit") != "50" {
				t.Errorf("limit = %q, want %q", r.URL.Query().Get("limit"), "50")
			}
			apiHandler(events, http.StatusOK)(w, r)
		})
		defer server.Close()

		c := New(server.URL)
		result, err := c.Events.List(context.Background(), &ListOptions{Limit: 50})

		if err != nil {
			t.Fatalf("List() error = %v", err)
		}

		if len(result) != 2 {
			t.Errorf("List() returned %d events, want 2", len(result))
		}
	})

	t.Run("with filters", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("worktree") != "main" {
				t.Errorf("worktree = %q, want %q", r.URL.Query().Get("worktree"), "main")
			}
			apiHandler(events, http.StatusOK)(w, r)
		})
		defer server.Close()

		c := New(server.URL)
		_, err := c.Events.List(context.Background(), &ListOptions{
			Worktree: "main",
			Types:    []string{"service.started"},
		})

		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
	})
}

func TestLogClient_List(t *testing.T) {
	viewers := []LogViewer{
		{Name: "nginx", Description: "Nginx access logs"},
		{Name: "postgres", Description: "PostgreSQL logs"},
	}

	server := mockServer(t, apiHandler(viewers, http.StatusOK))
	defer server.Close()

	c := New(server.URL)
	result, err := c.Logs.List(context.Background())

	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(result) != 2 {
		t.Errorf("List() returned %d viewers, want 2", len(result))
	}

	if result[0].Name != "nginx" {
		t.Errorf("result[0].Name = %q, want %q", result[0].Name, "nginx")
	}
}

func TestLogClient_GetEntries(t *testing.T) {
	entries := []LogEntry{
		{
			Timestamp: time.Now(),
			Level:     "INFO",
			Message:   "Request received",
		},
	}

	response := map[string]interface{}{
		"entries": entries,
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/logs/nginx/entries" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(response, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Logs.GetEntries(context.Background(), "nginx", &LogEntriesOptions{
		Limit: 100,
		Level: "INFO",
	})

	if err != nil {
		t.Fatalf("GetEntries() error = %v", err)
	}

	if len(result) != 1 {
		t.Errorf("GetEntries() returned %d entries, want 1", len(result))
	}
}

func TestLogClient_GetHistoryEntries(t *testing.T) {
	entries := []LogEntry{
		{
			Timestamp: time.Now().Add(-1 * time.Hour),
			Level:     "ERROR",
			Message:   "Connection failed",
		},
	}

	response := map[string]interface{}{
		"entries": entries,
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/logs/nginx/history" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(response, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Logs.GetHistoryEntries(context.Background(), "nginx", &LogEntriesOptions{
		Grep:   "connection",
		Before: 3,
		After:  3,
	})

	if err != nil {
		t.Fatalf("GetHistoryEntries() error = %v", err)
	}

	if len(result) != 1 {
		t.Errorf("GetHistoryEntries() returned %d entries, want 1", len(result))
	}
}

func TestLogClient_GetHistory(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/logs/nginx/history" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("lines") != "100" {
			t.Errorf("lines = %q, want %q", r.URL.Query().Get("lines"), "100")
		}
		apiHandler("log data here", http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	_, err := c.Logs.GetHistory(context.Background(), "nginx", 100)

	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
}

func TestTraceClient_Execute(t *testing.T) {
	result := TraceResult{
		Name:         "trace-123",
		Status:       "running",
		TotalEntries: 0,
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/trace" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(result, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	traceResult, err := c.Trace.Execute(context.Background(), &TraceRequest{
		ID:    "req-abc123",
		Group: "web",
		Start: time.Now().Add(-1 * time.Hour),
		End:   time.Now(),
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if traceResult.Name != "trace-123" {
		t.Errorf("Name = %q, want %q", traceResult.Name, "trace-123")
	}
}

func TestTraceClient_ListReports(t *testing.T) {
	reports := []TraceReportSummary{
		{
			Name:       "trace-123",
			TraceID:    "req-abc123",
			Group:      "web",
			EntryCount: 42,
		},
	}

	server := mockServer(t, apiHandler(reports, http.StatusOK))
	defer server.Close()

	c := New(server.URL)
	result, err := c.Trace.ListReports(context.Background())

	if err != nil {
		t.Fatalf("ListReports() error = %v", err)
	}

	if len(result) != 1 {
		t.Errorf("ListReports() returned %d reports, want 1", len(result))
	}
}

func TestTraceClient_GetReport(t *testing.T) {
	report := TraceReport{
		Name:    "trace-123",
		TraceID: "req-abc123",
		Group:   "web",
		Status:  "completed",
		Summary: TraceSummary{
			TotalEntries: 42,
		},
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/trace/reports/trace-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(report, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Trace.GetReport(context.Background(), "trace-123")

	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("Status = %q, want %q", result.Status, "completed")
	}
}

func TestTraceClient_DeleteReport(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("Method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/trace/reports/trace-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		apiHandler(nil, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	err := c.Trace.DeleteReport(context.Background(), "trace-123")

	if err != nil {
		t.Fatalf("DeleteReport() error = %v", err)
	}
}

func TestTraceClient_ListGroups(t *testing.T) {
	groups := []TraceGroup{
		{
			Name:       "web",
			LogViewers: []string{"nginx", "backend"},
		},
	}

	server := mockServer(t, apiHandler(map[string]interface{}{"groups": groups}, http.StatusOK))
	defer server.Close()

	c := New(server.URL)
	result, err := c.Trace.ListGroups(context.Background())

	if err != nil {
		t.Fatalf("ListGroups() error = %v", err)
	}

	if len(result) != 1 {
		t.Errorf("ListGroups() returned %d groups, want 1", len(result))
	}

	if result[0].Name != "web" {
		t.Errorf("result[0].Name = %q, want %q", result[0].Name, "web")
	}
}

func TestNotifyClient_Send(t *testing.T) {
	response := NotifyResponse{
		ID:        "notify-123",
		Type:      "done",
		Timestamp: "2026-01-17T10:00:00Z",
	}

	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/notify" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Verify request body
		var req NotifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if req.Message != "Build complete" {
			t.Errorf("Message = %q, want %q", req.Message, "Build complete")
		}
		if req.Type != "done" {
			t.Errorf("Type = %q, want %q", req.Type, "done")
		}

		apiHandler(response, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	result, err := c.Notify.Send(context.Background(), "Build complete", NotifyDone)

	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if result.ID != "notify-123" {
		t.Errorf("ID = %q, want %q", result.ID, "notify-123")
	}
}

func TestNotifyClient_SendTypes(t *testing.T) {
	tests := []struct {
		notifyType NotifyType
		expected   string
	}{
		{NotifyDone, "done"},
		{NotifyBlocked, "blocked"},
		{NotifyError, "error"},
	}

	for _, tt := range tests {
		t.Run(string(tt.notifyType), func(t *testing.T) {
			server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
				var req NotifyRequest
				json.NewDecoder(r.Body).Decode(&req)
				if req.Type != tt.expected {
					t.Errorf("Type = %q, want %q", req.Type, tt.expected)
				}
				apiHandler(NotifyResponse{}, http.StatusOK)(w, r)
			})
			defer server.Close()

			c := New(server.URL)
			_, err := c.Notify.Send(context.Background(), "test", tt.notifyType)
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
		})
	}
}

func TestContextCancellation(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		apiHandler([]Service{}, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := c.Services.List(ctx)
	if err == nil {
		t.Error("expected error due to cancelled context")
	}
}

// invalidJSONHandler returns a handler that sends invalid JSON.
func invalidJSONHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": invalid json}`))
	}
}

// invalidDataHandler returns a handler that sends valid JSON but invalid data type.
func invalidDataHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "not an object"}`))
	}
}

func TestServiceClient_InvalidJSON(t *testing.T) {
	server := mockServer(t, invalidJSONHandler())
	defer server.Close()

	c := New(server.URL)
	_, err := c.Services.List(context.Background())
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestWorktreeClient_InvalidJSON(t *testing.T) {
	server := mockServer(t, invalidJSONHandler())
	defer server.Close()

	c := New(server.URL)
	_, err := c.Worktrees.List(context.Background())
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestWorkflowClient_InvalidJSON(t *testing.T) {
	server := mockServer(t, invalidJSONHandler())
	defer server.Close()

	c := New(server.URL)
	_, err := c.Workflows.List(context.Background())
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestEventClient_InvalidJSON(t *testing.T) {
	server := mockServer(t, invalidJSONHandler())
	defer server.Close()

	c := New(server.URL)
	_, err := c.Events.List(context.Background(), nil)
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestLogClient_InvalidJSON(t *testing.T) {
	server := mockServer(t, invalidJSONHandler())
	defer server.Close()

	c := New(server.URL)

	_, err := c.Logs.List(context.Background())
	if err == nil {
		t.Error("expected error for invalid JSON response in List")
	}

	_, err = c.Logs.GetEntries(context.Background(), "test", nil)
	if err == nil {
		t.Error("expected error for invalid JSON response in GetEntries")
	}

	_, err = c.Logs.GetHistoryEntries(context.Background(), "test", nil)
	if err == nil {
		t.Error("expected error for invalid JSON response in GetHistoryEntries")
	}
}

func TestTraceClient_InvalidJSON(t *testing.T) {
	server := mockServer(t, invalidJSONHandler())
	defer server.Close()

	c := New(server.URL)

	_, err := c.Trace.Execute(context.Background(), &TraceRequest{ID: "test"})
	if err == nil {
		t.Error("expected error for invalid JSON response in Execute")
	}

	_, err = c.Trace.ListReports(context.Background())
	if err == nil {
		t.Error("expected error for invalid JSON response in ListReports")
	}

	_, err = c.Trace.GetReport(context.Background(), "test")
	if err == nil {
		t.Error("expected error for invalid JSON response in GetReport")
	}

	_, err = c.Trace.ListGroups(context.Background())
	if err == nil {
		t.Error("expected error for invalid JSON response in ListGroups")
	}
}

func TestNotifyClient_InvalidJSON(t *testing.T) {
	server := mockServer(t, invalidJSONHandler())
	defer server.Close()

	c := New(server.URL)
	_, err := c.Notify.Send(context.Background(), "test", NotifyDone)
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestLogClient_GetEntriesWithAllOptions(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("limit") != "100" {
			t.Errorf("expected limit=100, got %s", query.Get("limit"))
		}
		if query.Get("level") != "error" {
			t.Errorf("expected level=error, got %s", query.Get("level"))
		}
		if query.Get("grep") != "pattern" {
			t.Errorf("expected grep=pattern, got %s", query.Get("grep"))
		}
		if query.Get("after") == "" {
			t.Error("expected after parameter")
		}
		if query.Get("before") == "" {
			t.Error("expected before parameter")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"entries": []LogEntry{},
			},
		})
	})
	defer server.Close()

	c := New(server.URL)
	now := time.Now()
	_, err := c.Logs.GetEntries(context.Background(), "test", &LogEntriesOptions{
		Limit: 100,
		Since: now.Add(-1 * time.Hour),
		Until: now,
		Level: "error",
		Grep:  "pattern",
	})
	if err != nil {
		t.Fatalf("GetEntries() error = %v", err)
	}
}

func TestLogClient_GetHistoryEntriesWithAllOptions(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("limit") != "50" {
			t.Errorf("expected limit=50, got %s", query.Get("limit"))
		}
		if query.Get("grep") != "searchterm" {
			t.Errorf("expected grep=searchterm, got %s", query.Get("grep"))
		}
		if query.Get("before") != "5" {
			t.Errorf("expected before=5, got %s", query.Get("before"))
		}
		if query.Get("after") != "3" {
			t.Errorf("expected after=3, got %s", query.Get("after"))
		}
		if query.Get("start") == "" {
			t.Error("expected start parameter")
		}
		if query.Get("end") == "" {
			t.Error("expected end parameter")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"entries": []LogEntry{},
			},
		})
	})
	defer server.Close()

	c := New(server.URL)
	now := time.Now()
	_, err := c.Logs.GetHistoryEntries(context.Background(), "test", &LogEntriesOptions{
		Limit:  50,
		Since:  now.Add(-1 * time.Hour),
		Until:  now,
		Grep:   "searchterm",
		Before: 5,
		After:  3,
	})
	if err != nil {
		t.Fatalf("GetHistoryEntries() error = %v", err)
	}
}

func TestEventClient_ListWithAllOptions(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("worktree") != "feature" {
			t.Errorf("expected worktree=feature, got %s", query.Get("worktree"))
		}
		if query.Get("type") != "service.started" {
			t.Errorf("expected type=service.started, got %s", query.Get("type"))
		}
		if query.Get("since") == "" {
			t.Error("expected since parameter")
		}
		if query.Get("until") == "" {
			t.Error("expected until parameter")
		}

		apiHandler([]Event{}, http.StatusOK)(w, r)
	})
	defer server.Close()

	c := New(server.URL)
	now := time.Now()
	_, err := c.Events.List(context.Background(), &ListOptions{
		Limit:    10,
		Worktree: "feature",
		Types:    []string{"service.started"},
		Since:    now.Add(-1 * time.Hour),
		Until:    now,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
}
