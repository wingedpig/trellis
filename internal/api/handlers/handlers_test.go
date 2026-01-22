// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/logs"
	"github.com/wingedpig/trellis/internal/service"
	"github.com/wingedpig/trellis/internal/terminal"
	"github.com/wingedpig/trellis/internal/workflow"
	"github.com/wingedpig/trellis/internal/worktree"
)

// Mock implementations

type mockServiceManager struct {
	services map[string]service.ServiceInfo
}

func newMockServiceManager() *mockServiceManager {
	return &mockServiceManager{
		services: map[string]service.ServiceInfo{
			"api": {Name: "api", Status: service.ServiceStatus{State: service.StatusRunning}, Enabled: true},
			"db":  {Name: "db", Status: service.ServiceStatus{State: service.StatusStopped}, Enabled: true},
		},
	}
}

func (m *mockServiceManager) Start(ctx context.Context, name string) error {
	if _, ok := m.services[name]; !ok {
		return &serviceNotFoundError{name: name}
	}
	svc := m.services[name]
	svc.Status.State = service.StatusRunning
	m.services[name] = svc
	return nil
}

func (m *mockServiceManager) Stop(ctx context.Context, name string) error {
	if _, ok := m.services[name]; !ok {
		return &serviceNotFoundError{name: name}
	}
	svc := m.services[name]
	svc.Status.State = service.StatusStopped
	m.services[name] = svc
	return nil
}

func (m *mockServiceManager) Restart(ctx context.Context, name string, trigger service.RestartTrigger) error {
	return m.Start(ctx, name)
}

func (m *mockServiceManager) Status(name string) (service.ServiceStatus, error) {
	if svc, ok := m.services[name]; ok {
		return svc.Status, nil
	}
	return service.ServiceStatus{}, &serviceNotFoundError{name: name}
}

func (m *mockServiceManager) Logs(name string, lines int) ([]string, error) {
	if _, ok := m.services[name]; !ok {
		return nil, &serviceNotFoundError{name: name}
	}
	return []string{"log line 1", "log line 2"}, nil
}

func (m *mockServiceManager) ClearLogs(name string) error {
	if _, ok := m.services[name]; !ok {
		return &serviceNotFoundError{name: name}
	}
	return nil
}

func (m *mockServiceManager) List() []service.ServiceInfo {
	result := make([]service.ServiceInfo, 0, len(m.services))
	for _, svc := range m.services {
		result = append(result, svc)
	}
	return result
}

func (m *mockServiceManager) StartAll(ctx context.Context) error    { return nil }
func (m *mockServiceManager) StopAll(ctx context.Context) error     { return nil }
func (m *mockServiceManager) StartWatched(ctx context.Context) error { return nil }
func (m *mockServiceManager) StopWatched(ctx context.Context) error  { return nil }

func (m *mockServiceManager) GetService(name string) (service.ServiceInfo, bool) {
	svc, ok := m.services[name]
	return svc, ok
}

func (m *mockServiceManager) UpdateConfigs(configs []config.ServiceConfig) {}

func (m *mockServiceManager) SubscribeLogs(name string) (chan service.LogLine, error) {
	if _, ok := m.services[name]; !ok {
		return nil, &serviceNotFoundError{name: name}
	}
	return make(chan service.LogLine, 100), nil
}

func (m *mockServiceManager) UnsubscribeLogs(name string, ch chan service.LogLine) {}

func (m *mockServiceManager) ParsedLogs(name string, lines int) ([]*logs.LogEntry, error) {
	if _, ok := m.services[name]; !ok {
		return nil, &serviceNotFoundError{name: name}
	}
	return nil, nil
}

func (m *mockServiceManager) HasParser(name string) bool {
	return false
}

type serviceNotFoundError struct {
	name string
}

func (e *serviceNotFoundError) Error() string {
	return "service not found: " + e.name
}

type mockWorktreeManager struct {
	worktrees []worktree.WorktreeInfo
	active    *worktree.WorktreeInfo
}

func newMockWorktreeManager() *mockWorktreeManager {
	wts := []worktree.WorktreeInfo{
		{Path: "/project/main", Branch: "main"},
		{Path: "/project/feature", Branch: "feature-x"},
	}
	return &mockWorktreeManager{
		worktrees: wts,
		active:    &wts[0],
	}
}

func (m *mockWorktreeManager) List() ([]worktree.WorktreeInfo, error) {
	return m.worktrees, nil
}

func (m *mockWorktreeManager) Active() *worktree.WorktreeInfo {
	return m.active
}

func (m *mockWorktreeManager) SetActive(name string) error {
	for i := range m.worktrees {
		if m.worktrees[i].Name() == name || m.worktrees[i].Branch == name {
			m.active = &m.worktrees[i]
			return nil
		}
	}
	return &worktreeNotFoundError{name: name}
}

func (m *mockWorktreeManager) Activate(ctx context.Context, name string) (*worktree.ActivateResult, error) {
	if err := m.SetActive(name); err != nil {
		return nil, err
	}
	wt, _ := m.GetByName(name)
	return &worktree.ActivateResult{
		Worktree: wt,
		Duration: "100ms",
	}, nil
}

func (m *mockWorktreeManager) Refresh() error { return nil }

func (m *mockWorktreeManager) GetByName(name string) (worktree.WorktreeInfo, bool) {
	for _, wt := range m.worktrees {
		if wt.Name() == name || wt.Branch == name {
			return wt, true
		}
	}
	return worktree.WorktreeInfo{}, false
}

func (m *mockWorktreeManager) GetByPath(path string) (worktree.WorktreeInfo, bool) {
	for _, wt := range m.worktrees {
		if wt.Path == path {
			return wt, true
		}
	}
	return worktree.WorktreeInfo{}, false
}

func (m *mockWorktreeManager) Count() int {
	return len(m.worktrees)
}

func (m *mockWorktreeManager) Status() (worktree.GitStatus, error) {
	return worktree.GitStatus{Clean: true}, nil
}

func (m *mockWorktreeManager) BinariesPath() string {
	return "/project/bin"
}

func (m *mockWorktreeManager) ProjectName() string {
	return "test-project"
}

func (m *mockWorktreeManager) Create(ctx context.Context, branchName string, switchTo bool) error {
	wt := worktree.WorktreeInfo{
		Path:   "/project/test-project-" + branchName,
		Branch: branchName,
	}
	m.worktrees = append(m.worktrees, wt)
	if switchTo {
		m.active = &m.worktrees[len(m.worktrees)-1]
	}
	return nil
}

func (m *mockWorktreeManager) Remove(ctx context.Context, name string, deleteBranch bool) error {
	for i, wt := range m.worktrees {
		if wt.Name() == name || wt.Branch == name {
			if m.active != nil && m.active.Path == wt.Path {
				return fmt.Errorf("cannot remove active worktree")
			}
			m.worktrees = append(m.worktrees[:i], m.worktrees[i+1:]...)
			return nil
		}
	}
	return &worktreeNotFoundError{name: name}
}

type worktreeNotFoundError struct {
	name string
}

func (e *worktreeNotFoundError) Error() string {
	return "worktree not found: " + e.name
}

type mockWorkflowRunner struct {
	workflows map[string]workflow.WorkflowConfig
}

func newMockWorkflowRunner() *mockWorkflowRunner {
	return &mockWorkflowRunner{
		workflows: map[string]workflow.WorkflowConfig{
			"build": {ID: "build", Name: "Build", Command: []string{"make", "build"}},
			"test":  {ID: "test", Name: "Test", Command: []string{"make", "test"}},
		},
	}
}

func (m *mockWorkflowRunner) Run(ctx context.Context, id string) (*workflow.WorkflowStatus, error) {
	return m.RunWithOptions(ctx, id, workflow.RunOptions{})
}

func (m *mockWorkflowRunner) RunWithOptions(ctx context.Context, id string, opts workflow.RunOptions) (*workflow.WorkflowStatus, error) {
	if _, ok := m.workflows[id]; !ok {
		return nil, &workflowNotFoundError{id: id}
	}
	return &workflow.WorkflowStatus{
		ID:      id + "-123",
		Name:    m.workflows[id].Name,
		State:   workflow.StateSuccess,
		Success: true,
	}, nil
}

func (m *mockWorkflowRunner) Status(runID string) (*workflow.WorkflowStatus, bool) {
	return nil, false
}

func (m *mockWorkflowRunner) List() []workflow.WorkflowConfig {
	result := make([]workflow.WorkflowConfig, 0, len(m.workflows))
	for _, wf := range m.workflows {
		result = append(result, wf)
	}
	return result
}

func (m *mockWorkflowRunner) Get(id string) (workflow.WorkflowConfig, bool) {
	wf, ok := m.workflows[id]
	return wf, ok
}

func (m *mockWorkflowRunner) Cancel(runID string) error {
	return nil
}

func (m *mockWorkflowRunner) Subscribe(runID string, ch chan<- workflow.OutputUpdate) error {
	return nil
}

func (m *mockWorkflowRunner) Unsubscribe(runID string, ch chan<- workflow.OutputUpdate) {
}

func (m *mockWorkflowRunner) Close() error {
	return nil
}

func (m *mockWorkflowRunner) UpdateConfig(workflows []workflow.WorkflowConfig, worktree string) {
}

type workflowNotFoundError struct {
	id string
}

func (e *workflowNotFoundError) Error() string {
	return "workflow not found: " + e.id
}

type mockEventBus struct {
	events          []events.Event
	defaultWorktree string
}

func newMockEventBus() *mockEventBus {
	return &mockEventBus{
		events: []events.Event{
			{ID: "1", Type: "service.started", Timestamp: time.Now()},
			{ID: "2", Type: "service.stopped", Timestamp: time.Now()},
		},
	}
}

func (m *mockEventBus) SetDefaultWorktree(worktree string) {
	m.defaultWorktree = worktree
}

func (m *mockEventBus) Publish(ctx context.Context, event events.Event) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockEventBus) Subscribe(pattern string, handler events.EventHandler) (events.SubscriptionID, error) {
	return "sub-1", nil
}

func (m *mockEventBus) SubscribeAsync(pattern string, handler events.EventHandler, bufferSize int) (events.SubscriptionID, error) {
	return "sub-1", nil
}

func (m *mockEventBus) Unsubscribe(id events.SubscriptionID) error {
	return nil
}

func (m *mockEventBus) History(filter events.EventFilter) ([]events.Event, error) {
	return m.events, nil
}

func (m *mockEventBus) Close() error {
	return nil
}

type mockTerminalManager struct {
	sessions []terminal.SessionInfo
}

func newMockTerminalManager() *mockTerminalManager {
	return &mockTerminalManager{
		sessions: []terminal.SessionInfo{
			{Name: "main", Windows: []terminal.WindowInfo{{Name: "dev"}}},
		},
	}
}

func (m *mockTerminalManager) CreateSession(ctx context.Context, worktree, workdir string, windows []terminal.WindowConfig) error {
	return nil
}

func (m *mockTerminalManager) EnsureSession(ctx context.Context, worktree, workdir string, windows []terminal.WindowConfig) error {
	return nil
}

func (m *mockTerminalManager) KillSession(ctx context.Context, worktree string) error {
	return nil
}

func (m *mockTerminalManager) AttachReader(ctx context.Context, session, window string) (io.ReadCloser, error) {
	return nil, nil
}

func (m *mockTerminalManager) SendInput(ctx context.Context, session, window string, data []byte) error {
	return nil
}

func (m *mockTerminalManager) Resize(ctx context.Context, session, window string, cols, rows int) error {
	return nil
}

func (m *mockTerminalManager) ListSessions(ctx context.Context) ([]terminal.SessionInfo, error) {
	return m.sessions, nil
}

func (m *mockTerminalManager) GetScrollback(ctx context.Context, session, window string) ([]byte, error) {
	return []byte("scrollback content"), nil
}

func (m *mockTerminalManager) GetCursorPosition(ctx context.Context, session, window string) (int, int, error) {
	return 0, 0, nil
}

func (m *mockTerminalManager) GetRemoteWindow(name string) *terminal.RemoteWindowConfig {
	return nil
}

// Tests

func TestServiceHandler_List(t *testing.T) {
	handler := NewServiceHandler(newMockServiceManager())

	req := httptest.NewRequest("GET", "/api/v1/services", nil)
	rec := httptest.NewRecorder()

	handler.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.NotNil(t, resp.Data)
}

func TestServiceHandler_Get(t *testing.T) {
	handler := NewServiceHandler(newMockServiceManager())

	req := httptest.NewRequest("GET", "/api/v1/services/api", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "api"})
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServiceHandler_Get_NotFound(t *testing.T) {
	handler := NewServiceHandler(newMockServiceManager())

	req := httptest.NewRequest("GET", "/api/v1/services/unknown", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "unknown"})
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServiceHandler_Start(t *testing.T) {
	handler := NewServiceHandler(newMockServiceManager())

	req := httptest.NewRequest("POST", "/api/v1/services/db/start", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "db"})
	rec := httptest.NewRecorder()

	handler.Start(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServiceHandler_Stop(t *testing.T) {
	handler := NewServiceHandler(newMockServiceManager())

	req := httptest.NewRequest("POST", "/api/v1/services/api/stop", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "api"})
	rec := httptest.NewRecorder()

	handler.Stop(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServiceHandler_Restart(t *testing.T) {
	handler := NewServiceHandler(newMockServiceManager())

	req := httptest.NewRequest("POST", "/api/v1/services/api/restart", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "api"})
	rec := httptest.NewRecorder()

	handler.Restart(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServiceHandler_Logs(t *testing.T) {
	handler := NewServiceHandler(newMockServiceManager())

	req := httptest.NewRequest("GET", "/api/v1/services/api/logs", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "api"})
	rec := httptest.NewRecorder()

	handler.Logs(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServiceHandler_Logs_WithLines(t *testing.T) {
	handler := NewServiceHandler(newMockServiceManager())

	req := httptest.NewRequest("GET", "/api/v1/services/api/logs?lines=50", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "api"})
	rec := httptest.NewRecorder()

	handler.Logs(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestWorktreeHandler_List(t *testing.T) {
	handler := NewWorktreeHandler(newMockWorktreeManager())

	req := httptest.NewRequest("GET", "/api/v1/worktrees", nil)
	rec := httptest.NewRecorder()

	handler.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify response structure
	var resp Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	data, ok := resp.Data.(map[string]interface{})
	require.True(t, ok)

	// Should have worktrees array
	worktrees, ok := data["worktrees"].([]interface{})
	require.True(t, ok)
	assert.Len(t, worktrees, 2)

	// Should NOT have standalone "active" field anymore
	_, hasActive := data["active"]
	assert.False(t, hasActive, "response should not have standalone 'active' field")

	// Each worktree should have an Active field
	for i, wt := range worktrees {
		wtMap, ok := wt.(map[string]interface{})
		require.True(t, ok)

		// Verify Active field exists
		active, hasActiveField := wtMap["Active"]
		require.True(t, hasActiveField, "worktree should have Active field")

		// First worktree should be active (based on mock)
		if i == 0 {
			assert.Equal(t, true, active, "first worktree should be active")
		} else {
			assert.Equal(t, false, active, "other worktrees should not be active")
		}
	}
}

func TestWorktreeHandler_Get(t *testing.T) {
	handler := NewWorktreeHandler(newMockWorktreeManager())

	req := httptest.NewRequest("GET", "/api/v1/worktrees/main", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "main"})
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify response structure
	var resp Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	data, ok := resp.Data.(map[string]interface{})
	require.True(t, ok)

	// Should have worktree object with embedded Active field
	wt, ok := data["worktree"].(map[string]interface{})
	require.True(t, ok)

	// Verify Active field is embedded in worktree object
	active, hasActiveField := wt["Active"]
	require.True(t, hasActiveField, "worktree should have Active field")
	assert.Equal(t, true, active, "main worktree should be active")

	// Verify other worktree fields
	assert.Equal(t, "main", wt["Branch"])
	assert.Equal(t, "/project/main", wt["Path"])
}

func TestWorktreeHandler_Get_Inactive(t *testing.T) {
	handler := NewWorktreeHandler(newMockWorktreeManager())

	req := httptest.NewRequest("GET", "/api/v1/worktrees/feature", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "feature"})
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify response structure
	var resp Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	data, ok := resp.Data.(map[string]interface{})
	require.True(t, ok)

	wt, ok := data["worktree"].(map[string]interface{})
	require.True(t, ok)

	// Feature worktree should not be active
	active, hasActiveField := wt["Active"]
	require.True(t, hasActiveField, "worktree should have Active field")
	assert.Equal(t, false, active, "feature worktree should not be active")
}

func TestWorktreeHandler_Get_NotFound(t *testing.T) {
	handler := NewWorktreeHandler(newMockWorktreeManager())

	req := httptest.NewRequest("GET", "/api/v1/worktrees/unknown", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "unknown"})
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestWorktreeHandler_Activate(t *testing.T) {
	handler := NewWorktreeHandler(newMockWorktreeManager())

	req := httptest.NewRequest("POST", "/api/v1/worktrees/feature/activate", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "feature"})
	rec := httptest.NewRecorder()

	handler.Activate(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify response structure
	var resp Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	data, ok := resp.Data.(map[string]interface{})
	require.True(t, ok)

	// Should have worktree object with embedded Active field
	wt, ok := data["worktree"].(map[string]interface{})
	require.True(t, ok)

	// Activated worktree should have Active=true embedded
	active, hasActiveField := wt["Active"]
	require.True(t, hasActiveField, "worktree should have Active field")
	assert.Equal(t, true, active, "activated worktree should be active")

	// Should have duration
	_, hasDuration := data["duration"]
	assert.True(t, hasDuration, "response should have duration field")

	// Should NOT have standalone "active" boolean anymore
	_, hasStandaloneActive := data["active"]
	assert.False(t, hasStandaloneActive, "response should not have standalone 'active' field")
}

func TestWorktreeHandler_Activate_NotFound(t *testing.T) {
	handler := NewWorktreeHandler(newMockWorktreeManager())

	req := httptest.NewRequest("POST", "/api/v1/worktrees/unknown/activate", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "unknown"})
	rec := httptest.NewRecorder()

	handler.Activate(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWorktreeHandler_Info(t *testing.T) {
	handler := NewWorktreeHandler(newMockWorktreeManager())

	req := httptest.NewRequest("GET", "/api/v1/worktrees/info", nil)
	rec := httptest.NewRecorder()

	handler.Info(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify response structure
	var resp Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	data, ok := resp.Data.(map[string]interface{})
	require.True(t, ok)

	// Should have worktrees array with Active field
	worktrees, ok := data["worktrees"].([]interface{})
	require.True(t, ok)
	assert.Len(t, worktrees, 2)

	// Should NOT have standalone "active" field
	_, hasActive := data["active"]
	assert.False(t, hasActive, "response should not have standalone 'active' field")

	// Should have project_name and binaries_dir
	_, hasProjectName := data["project_name"]
	assert.True(t, hasProjectName, "response should have project_name")
	_, hasBinariesDir := data["binaries_dir"]
	assert.True(t, hasBinariesDir, "response should have binaries_dir")

	// First worktree should have Active=true
	wtMap, ok := worktrees[0].(map[string]interface{})
	require.True(t, ok)
	active, hasActiveField := wtMap["Active"]
	require.True(t, hasActiveField, "worktree should have Active field")
	assert.Equal(t, true, active, "first worktree should be active")
}

func TestWorktreeResponse_AllFields(t *testing.T) {
	// Test that WorktreeResponse includes all expected fields
	handler := NewWorktreeHandler(newMockWorktreeManager())

	req := httptest.NewRequest("GET", "/api/v1/worktrees/main", nil)
	req = mux.SetURLVars(req, map[string]string{"name": "main"})
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	var resp Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	data, ok := resp.Data.(map[string]interface{})
	require.True(t, ok)

	wt, ok := data["worktree"].(map[string]interface{})
	require.True(t, ok)

	// Verify all expected fields are present
	expectedFields := []string{"Path", "Branch", "Commit", "Detached", "IsBare", "Dirty", "Ahead", "Behind", "Active"}
	for _, field := range expectedFields {
		_, hasField := wt[field]
		assert.True(t, hasField, "worktree should have %s field", field)
	}
}

func TestWorkflowHandler_List(t *testing.T) {
	handler := NewWorkflowHandler(newMockWorkflowRunner(), nil)

	req := httptest.NewRequest("GET", "/api/v1/workflows", nil)
	rec := httptest.NewRecorder()

	handler.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestWorkflowHandler_Get(t *testing.T) {
	handler := NewWorkflowHandler(newMockWorkflowRunner(), nil)

	req := httptest.NewRequest("GET", "/api/v1/workflows/build", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "build"})
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestWorkflowHandler_Get_NotFound(t *testing.T) {
	handler := NewWorkflowHandler(newMockWorkflowRunner(), nil)

	req := httptest.NewRequest("GET", "/api/v1/workflows/unknown", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "unknown"})
	rec := httptest.NewRecorder()

	handler.Get(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestWorkflowHandler_Run(t *testing.T) {
	handler := NewWorkflowHandler(newMockWorkflowRunner(), nil)

	req := httptest.NewRequest("POST", "/api/v1/workflows/build/run", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "build"})
	rec := httptest.NewRecorder()

	handler.Run(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestWorkflowHandler_Status(t *testing.T) {
	handler := NewWorkflowHandler(newMockWorkflowRunner(), nil)

	req := httptest.NewRequest("GET", "/api/v1/workflows/build/status", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "build"})
	rec := httptest.NewRecorder()

	handler.Status(rec, req)

	// Workflow exists but no active run
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestEventHandler_History(t *testing.T) {
	handler := NewEventHandler(newMockEventBus())

	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	rec := httptest.NewRecorder()

	handler.History(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestEventHandler_History_WithFilters(t *testing.T) {
	handler := NewEventHandler(newMockEventBus())

	req := httptest.NewRequest("GET", "/api/v1/events?type=service.started&limit=10", nil)
	rec := httptest.NewRecorder()

	handler.History(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestTerminalHandler_ListSessions(t *testing.T) {
	handler := NewTerminalHandler(newMockTerminalManager())

	req := httptest.NewRequest("GET", "/api/v1/terminal/sessions", nil)
	rec := httptest.NewRecorder()

	handler.ListSessions(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteJSON(rec, http.StatusOK, map[string]string{"key": "value"})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.NotNil(t, resp.Data)
	assert.NotNil(t, resp.Meta)
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteError(rec, http.StatusNotFound, ErrNotFound, "resource not found")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, ErrNotFound, resp.Error.Code)
	assert.Equal(t, "resource not found", resp.Error.Message)
}

func TestWriteErrorWithDetails(t *testing.T) {
	rec := httptest.NewRecorder()

	details := map[string]interface{}{
		"field": "name",
		"value": "test",
	}
	WriteErrorWithDetails(rec, http.StatusBadRequest, ErrBadRequest, "validation failed", details)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.NotNil(t, resp.Error)
	assert.NotNil(t, resp.Error.Details)
}
