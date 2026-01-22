// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_CreateSession(t *testing.T) {
	mock := NewMockTmuxExecutor()
	mgr := NewManager(mock, TerminalConfig{
		DefaultShell: "/bin/bash",
	})

	windows := []WindowConfig{
		{Name: "dev", Command: "/bin/zsh"},
		{Name: "claude", Command: "claude"},
	}

	err := mgr.CreateSession(context.Background(), "main", "/project", windows)
	require.NoError(t, err)

	// Session should exist
	assert.True(t, mock.Sessions["main"])

	// Windows should be created
	assert.Len(t, mock.Windows["main"], 2)
}

func TestManager_CreateSession_WithDots(t *testing.T) {
	mock := NewMockTmuxExecutor()
	mgr := NewManager(mock, TerminalConfig{
		DefaultShell: "/bin/bash",
	})

	err := mgr.CreateSession(context.Background(), "groups.io", "/project", nil)
	require.NoError(t, err)

	// Session name should have dots replaced
	assert.True(t, mock.Sessions["groups_io"])
}

func TestManager_EnsureSession_AlreadyExists(t *testing.T) {
	mock := NewMockTmuxExecutor()
	mock.Sessions["main"] = true

	mgr := NewManager(mock, TerminalConfig{
		DefaultShell: "/bin/bash",
	})

	err := mgr.EnsureSession(context.Background(), "main", "/project", nil)
	require.NoError(t, err)

	// Should not have created a new session
	assert.Len(t, mock.Sessions, 1)
}

func TestManager_EnsureSession_NotExists(t *testing.T) {
	mock := NewMockTmuxExecutor()
	mgr := NewManager(mock, TerminalConfig{
		DefaultShell: "/bin/bash",
	})

	err := mgr.EnsureSession(context.Background(), "new-worktree", "/project", nil)
	require.NoError(t, err)

	// Should have created the session
	assert.True(t, mock.Sessions["new-worktree"])
}

func TestManager_KillSession(t *testing.T) {
	mock := NewMockTmuxExecutor()
	mock.Sessions["main"] = true

	mgr := NewManager(mock, TerminalConfig{})

	err := mgr.KillSession(context.Background(), "main")
	require.NoError(t, err)

	assert.False(t, mock.Sessions["main"])
}

func TestManager_ListSessions(t *testing.T) {
	mock := NewMockTmuxExecutor()

	mgr := NewManager(mock, TerminalConfig{})

	// Create sessions through the manager so they're tracked
	mgr.CreateSession(context.Background(), "main", "/project", nil)
	mgr.CreateSession(context.Background(), "feature", "/project/feature", nil)

	mock.Windows["main"] = []WindowInfo{{Name: "dev"}, {Name: "claude"}}
	mock.Windows["feature"] = []WindowInfo{{Name: "dev"}}

	sessions, err := mgr.ListSessions(context.Background())
	require.NoError(t, err)

	assert.Len(t, sessions, 2)
}

func TestManager_ListSessions_ProjectFiltering(t *testing.T) {
	mock := NewMockTmuxExecutor()

	// Manager with project name set - should filter by prefix
	mgr := NewManager(mock, TerminalConfig{
		ProjectName: "groups.io",
	})

	// Simulate multiple tmux sessions from different projects
	mock.Sessions["groups_io"] = true
	mock.Sessions["groups_io-feature"] = true
	mock.Sessions["trellis"] = true
	mock.Sessions["other-project"] = true

	mock.Windows["groups_io"] = []WindowInfo{{Name: "dev"}}
	mock.Windows["groups_io-feature"] = []WindowInfo{{Name: "dev"}}
	mock.Windows["trellis"] = []WindowInfo{{Name: "dev"}}
	mock.Windows["other-project"] = []WindowInfo{{Name: "dev"}}

	sessions, err := mgr.ListSessions(context.Background())
	require.NoError(t, err)

	// Should only return sessions matching the project prefix
	assert.Len(t, sessions, 2)

	sessionNames := make([]string, len(sessions))
	for i, s := range sessions {
		sessionNames[i] = s.Name
	}
	assert.Contains(t, sessionNames, "groups_io")
	assert.Contains(t, sessionNames, "groups_io-feature")
	assert.NotContains(t, sessionNames, "trellis")
	assert.NotContains(t, sessionNames, "other-project")
}

func TestManager_ListSessions_NoProjectFilter(t *testing.T) {
	mock := NewMockTmuxExecutor()

	// Manager with no project name - should return all sessions
	mgr := NewManager(mock, TerminalConfig{})

	mock.Sessions["groups_io"] = true
	mock.Sessions["trellis"] = true
	mock.Windows["groups_io"] = []WindowInfo{{Name: "dev"}}
	mock.Windows["trellis"] = []WindowInfo{{Name: "dev"}}

	sessions, err := mgr.ListSessions(context.Background())
	require.NoError(t, err)

	// Should return all sessions when no project filter
	assert.Len(t, sessions, 2)
}

func TestManager_GetScrollback(t *testing.T) {
	mock := NewMockTmuxExecutor()
	mock.Sessions["main"] = true
	mock.CapturePaneOut = []byte("some terminal output\nmore output\n")

	mgr := NewManager(mock, TerminalConfig{})

	scrollback, err := mgr.GetScrollback(context.Background(), "main", "dev")
	require.NoError(t, err)

	assert.Contains(t, string(scrollback), "some terminal output")
}

func TestManager_GetCursorPosition(t *testing.T) {
	mock := NewMockTmuxExecutor()
	mock.Sessions["main"] = true
	mock.CursorX = 10
	mock.CursorY = 5

	mgr := NewManager(mock, TerminalConfig{})

	x, y, err := mgr.GetCursorPosition(context.Background(), "main", "dev")
	require.NoError(t, err)

	assert.Equal(t, 10, x)
	assert.Equal(t, 5, y)
}

func TestManager_SendInput(t *testing.T) {
	mock := NewMockTmuxExecutor()
	mock.Sessions["main"] = true

	mgr := NewManager(mock, TerminalConfig{})

	err := mgr.SendInput(context.Background(), "main", "dev", []byte("hello"))
	require.NoError(t, err)
}

func TestManager_Resize(t *testing.T) {
	mock := NewMockTmuxExecutor()
	mock.Sessions["main"] = true

	mgr := NewManager(mock, TerminalConfig{})

	err := mgr.Resize(context.Background(), "main", "dev", 120, 40)
	require.NoError(t, err)
}

func TestManager_SessionNaming(t *testing.T) {
	tests := []struct {
		worktree string
		expected string
	}{
		{"main", "main"},
		{"feature-auth", "feature-auth"},
		{"groups.io", "groups_io"},
		{"my.project.dev", "my_project_dev"},
	}

	for _, tt := range tests {
		t.Run(tt.worktree, func(t *testing.T) {
			mock := NewMockTmuxExecutor()
			mgr := NewManager(mock, TerminalConfig{})

			err := mgr.CreateSession(context.Background(), tt.worktree, "/project", nil)
			require.NoError(t, err)

			assert.True(t, mock.Sessions[tt.expected])
		})
	}
}

func TestManager_WindowCreation(t *testing.T) {
	mock := NewMockTmuxExecutor()
	mgr := NewManager(mock, TerminalConfig{
		DefaultShell: "/bin/zsh",
	})

	windows := []WindowConfig{
		{Name: "dev", Command: "/bin/zsh"},
		{Name: "claude", Command: "claude"},
		{Name: "code"},
	}

	err := mgr.CreateSession(context.Background(), "main", "/project", windows)
	require.NoError(t, err)

	// All windows should be created
	assert.Len(t, mock.Windows["main"], 3)
}

func TestManager_Interface(t *testing.T) {
	// Verify manager implements interface
	var _ Manager = (*RealManager)(nil)
}
