// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package worktree

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
)

func newTestBus() *events.MemoryEventBus {
	return events.NewMemoryEventBus(events.MemoryBusConfig{
		HistoryMaxEvents: 100,
		HistoryMaxAge:    time.Hour,
	})
}

func TestManager_New(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project", Commit: "abc", Branch: "main"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")
	assert.NotNil(t, mgr)
}

func TestManager_List(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
			{Path: "/project/feature", Commit: "def", Branch: "feature"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	worktrees, err := mgr.List()
	require.NoError(t, err)
	assert.Len(t, worktrees, 2)
	assert.Equal(t, "main", worktrees[0].Branch)
	assert.Equal(t, "feature", worktrees[1].Branch)
}

func TestManager_Active(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
			{Path: "/project/feature", Commit: "def", Branch: "feature"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	// Initially no active worktree
	active := mgr.Active()
	assert.Nil(t, active)

	// Activate one
	_, err := mgr.Activate(context.Background(), "main")
	require.NoError(t, err)

	active = mgr.Active()
	require.NotNil(t, active)
	assert.Equal(t, "main", active.Name())
}

func TestManager_Activate(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
			{Path: "/project/feature", Commit: "def", Branch: "feature"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	_, err := mgr.Activate(context.Background(), "feature")
	require.NoError(t, err)

	active := mgr.Active()
	require.NotNil(t, active)
	assert.Equal(t, "feature", active.Name())
}

func TestManager_Activate_NotFound(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	_, err := mgr.Activate(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestManager_Activate_EmitsEvent(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	var eventReceived atomic.Bool
	var activatedName string

	bus.Subscribe("worktree.activated", func(ctx context.Context, e events.Event) error {
		eventReceived.Store(true)
		if name, ok := e.Payload["name"].(string); ok {
			activatedName = name
		}
		return nil
	})

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	_, err := mgr.Activate(context.Background(), "main")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	assert.True(t, eventReceived.Load())
	assert.Equal(t, "main", activatedName)
}

func TestManager_Activate_SwitchWorktree(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	var deactivatedCount, activatedCount atomic.Int32

	bus.Subscribe(events.EventWorktreeDeactivating, func(ctx context.Context, e events.Event) error {
		deactivatedCount.Add(1)
		return nil
	})

	bus.Subscribe("worktree.activated", func(ctx context.Context, e events.Event) error {
		activatedCount.Add(1)
		return nil
	})

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
			{Path: "/project/feature", Commit: "def", Branch: "feature"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	// Activate main
	_, _ = mgr.Activate(context.Background(), "main")
	time.Sleep(50 * time.Millisecond)

	// Switch to feature
	_, _ = mgr.Activate(context.Background(), "feature")
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, int32(1), deactivatedCount.Load())
	assert.Equal(t, int32(2), activatedCount.Load())

	active := mgr.Active()
	assert.Equal(t, "feature", active.Name())
}

func TestManager_Refresh(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	worktrees, _ := mgr.List()
	assert.Len(t, worktrees, 1)

	// Add a worktree
	mock.worktrees = append(mock.worktrees, WorktreeInfo{
		Path: "/project/new", Commit: "ghi", Branch: "new-feature",
	})

	// Refresh
	err := mgr.Refresh()
	require.NoError(t, err)

	worktrees, _ = mgr.List()
	assert.Len(t, worktrees, 2)
}

func TestManager_GetByName(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
			{Path: "/project/feature", Commit: "def", Branch: "feature"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	wt, exists := mgr.GetByName("feature")
	require.True(t, exists)
	assert.Equal(t, "feature", wt.Branch)

	_, exists = mgr.GetByName("nonexistent")
	assert.False(t, exists)
}

func TestManager_GetByPath(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
			{Path: "/project/feature", Commit: "def", Branch: "feature"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	wt, exists := mgr.GetByPath("/project/feature")
	require.True(t, exists)
	assert.Equal(t, "feature", wt.Branch)

	_, exists = mgr.GetByPath("/nonexistent")
	assert.False(t, exists)
}

func TestManager_Count(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
			{Path: "/project/feature", Commit: "def", Branch: "feature"},
			{Path: "/project/hotfix", Commit: "ghi", Branch: "hotfix"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	assert.Equal(t, 3, mgr.Count())
}

func TestManager_WithLifecycleHooks(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	// Create a temp directory for the worktree path
	tempDir := t.TempDir()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: tempDir, Commit: "abc", Branch: "main"},
		},
	}

	cfg := config.WorktreeConfig{
		Lifecycle: config.LifecycleConfig{
			PreActivate: []config.HookConfig{
				{Name: "test-hook", Command: []string{"echo", "hello"}, Timeout: "5s"},
			},
		},
	}

	mgr := NewManager(mock, bus, cfg, "", "", "test-project")

	// Hooks run during Activate, using the worktree path as working dir
	_, err := mgr.Activate(context.Background(), "main")
	require.NoError(t, err)
}

func TestManager_BinariesPath(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
		},
	}

	cfg := config.WorktreeConfig{
		Binaries: config.BinariesConfig{
			Path: "{{.Worktree.Root}}/bin",
		},
	}

	mgr := NewManager(mock, bus, cfg, "", "", "test-project")
	_, _ = mgr.Activate(context.Background(), "main")

	binPath := mgr.BinariesPath()
	assert.Contains(t, binPath, "/bin")
}

func TestManager_Status(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
		},
		status: GitStatus{
			Clean:    false,
			Modified: []string{"file.go"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")
	_, _ = mgr.Activate(context.Background(), "main")

	status, err := mgr.Status()
	require.NoError(t, err)
	assert.False(t, status.Clean)
	assert.Contains(t, status.Modified, "file.go")
}

func TestManager_Status_NoActive(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	_, err := mgr.Status()
	assert.Error(t, err)
}

func TestManager_Concurrency(t *testing.T) {
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
			{Path: "/project/feature", Commit: "def", Branch: "feature"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	done := make(chan bool, 100)

	// Concurrent activations
	for i := 0; i < 20; i++ {
		go func(idx int) {
			name := "main"
			if idx%2 == 0 {
				name = "feature"
			}
			_, _ = mgr.Activate(context.Background(), name)
			done <- true
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 20; i++ {
		go func() {
			mgr.List()
			mgr.Active()
			mgr.Count()
			done <- true
		}()
	}

	for i := 0; i < 40; i++ {
		<-done
	}
}

func TestManager_ActivateVsRefresh_Concurrency(t *testing.T) {
	// This test specifically targets the race between Activate and Refresh
	// that can cause stale oldActive pointer issues.
	// Run with -race to detect data races.
	bus := newTestBus()
	defer bus.Close()

	mock := &MockGitExecutor{
		worktrees: []WorktreeInfo{
			{Path: "/project/main", Commit: "abc", Branch: "main"},
			{Path: "/project/feature", Commit: "def", Branch: "feature"},
			{Path: "/project/bugfix", Commit: "ghi", Branch: "bugfix"},
		},
	}

	mgr := NewManager(mock, bus, config.WorktreeConfig{}, "", "", "test-project")

	// Set initial active worktree
	_, err := mgr.Activate(context.Background(), "main")
	require.NoError(t, err)

	done := make(chan bool, 200)
	ctx := context.Background()

	// Run Activate and Refresh concurrently many times
	// This should trigger any race conditions with oldActive pointer
	for i := 0; i < 50; i++ {
		go func(idx int) {
			names := []string{"main", "feature", "bugfix"}
			name := names[idx%3]
			result, _ := mgr.Activate(ctx, name)
			// Verify result doesn't contain garbage data
			if result != nil {
				_ = result.Worktree.Name()
				_ = result.Worktree.Path
				_ = result.Worktree.Branch
			}
			done <- true
		}(i)

		go func() {
			_ = mgr.Refresh()
			done <- true
		}()
	}

	// Also interleave reads to stress test the locking
	for i := 0; i < 50; i++ {
		go func() {
			active := mgr.Active()
			if active != nil {
				// Access fields to catch races
				_ = active.Name()
				_ = active.Path
				_ = active.Branch
			}
			done <- true
		}()

		go func() {
			worktrees, _ := mgr.List()
			for _, wt := range worktrees {
				_ = wt.Name()
			}
			done <- true
		}()
	}

	for i := 0; i < 200; i++ {
		<-done
	}

	// Verify manager is still in valid state
	active := mgr.Active()
	assert.NotNil(t, active)
	assert.NotEmpty(t, active.Name())
}
