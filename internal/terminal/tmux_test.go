// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToTmuxSessionName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"main", "main"},
		{"groups.io", "groups_io"},
		{"groups.io-feature", "groups_io-feature"},
		{"feature/auth", "feature/auth"},
		{"my.project.dev", "my_project_dev"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ToTmuxSessionName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToDisplayName(t *testing.T) {
	tests := []struct {
		session  string
		isMain   bool
		isRemote bool
		expected string
	}{
		{"main", true, false, "@main"},
		{"feature", false, false, "@feature"},
		{"admin(1)", false, true, "!admin(1)"},
		{"mail01-g2", false, true, "!mail01-g2"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := ToDisplayName(tt.session, tt.isMain, tt.isRemote)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// MockTmuxExecutor for testing
type MockTmuxExecutor struct {
	Sessions       map[string]bool
	Windows        map[string][]WindowInfo
	HasSessionErr  error
	NewSessionErr  error
	KillSessionErr error
	NewWindowErr   error
	CapturePaneOut []byte
	CapturePaneErr error
	CursorX        int
	CursorY        int
}

func NewMockTmuxExecutor() *MockTmuxExecutor {
	return &MockTmuxExecutor{
		Sessions: make(map[string]bool),
		Windows:  make(map[string][]WindowInfo),
	}
}

func (m *MockTmuxExecutor) HasSession(ctx context.Context, session string) bool {
	return m.Sessions[session]
}

func (m *MockTmuxExecutor) ListSessions(ctx context.Context) ([]string, error) {
	var sessions []string
	for session := range m.Sessions {
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (m *MockTmuxExecutor) NewSession(ctx context.Context, session, workdir, firstWindowName string) error {
	if m.NewSessionErr != nil {
		return m.NewSessionErr
	}
	m.Sessions[session] = true
	// Also record the first window
	if firstWindowName != "" {
		m.Windows[session] = append(m.Windows[session], WindowInfo{Name: firstWindowName, Index: 0})
	}
	return nil
}

func (m *MockTmuxExecutor) KillSession(ctx context.Context, session string) error {
	if m.KillSessionErr != nil {
		return m.KillSessionErr
	}
	delete(m.Sessions, session)
	return nil
}

func (m *MockTmuxExecutor) NewWindow(ctx context.Context, session, window, workdir string, command []string) error {
	if m.NewWindowErr != nil {
		return m.NewWindowErr
	}
	m.Windows[session] = append(m.Windows[session], WindowInfo{Name: window})
	return nil
}

func (m *MockTmuxExecutor) KillWindow(ctx context.Context, session, window string) error {
	return nil
}

func (m *MockTmuxExecutor) ListWindows(ctx context.Context, session string) ([]WindowInfo, error) {
	return m.Windows[session], nil
}

func (m *MockTmuxExecutor) CapturePane(ctx context.Context, target string, withHistory bool) ([]byte, error) {
	if m.CapturePaneErr != nil {
		return nil, m.CapturePaneErr
	}
	return m.CapturePaneOut, nil
}

func (m *MockTmuxExecutor) SendKeys(ctx context.Context, target string, keys string, literal bool) error {
	return nil
}

func (m *MockTmuxExecutor) SendText(ctx context.Context, target string, text string) error {
	return nil
}

func (m *MockTmuxExecutor) StartPipePane(ctx context.Context, target, pipePath string) error {
	return nil
}

func (m *MockTmuxExecutor) StopPipePane(ctx context.Context, target string) error {
	return nil
}

func (m *MockTmuxExecutor) ResizeWindow(ctx context.Context, target string, cols, rows int) error {
	return nil
}

func (m *MockTmuxExecutor) GetCursorPosition(ctx context.Context, target string) (x, y int, err error) {
	return m.CursorX, m.CursorY, nil
}

func (m *MockTmuxExecutor) SetEnvironment(ctx context.Context, session, name, value string) error {
	return nil
}

func (m *MockTmuxExecutor) SetOption(ctx context.Context, session, name, value string) error {
	return nil
}

func TestRealTmuxExecutor_HasSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	exec := NewRealTmuxExecutor()

	// Test with a session that almost certainly doesn't exist
	exists := exec.HasSession(context.Background(), "trellis_test_nonexistent_12345")
	assert.False(t, exists)
}

func TestRealTmuxExecutor_ParseWindowList(t *testing.T) {
	// Format matches ListWindows custom -F output: "INDEX: NAME[*]"
	output := `0: dev*
1: claude
2: shell
`

	windows := parseWindowList(output)
	require.Len(t, windows, 3)

	assert.Equal(t, 0, windows[0].Index)
	assert.Equal(t, "dev", windows[0].Name)
	assert.True(t, windows[0].Active)

	assert.Equal(t, 1, windows[1].Index)
	assert.Equal(t, "claude", windows[1].Name)
	assert.False(t, windows[1].Active)

	assert.Equal(t, 2, windows[2].Index)
	assert.Equal(t, "shell", windows[2].Name)
	assert.False(t, windows[2].Active)
}

func TestRealTmuxExecutor_ParseWindowList_NamesWithSpaces(t *testing.T) {
	// Format matches ListWindows custom -F output: "INDEX: NAME[*]"
	output := `0: my window*
1: build log
2: test runner
3: admin panel
`

	windows := parseWindowList(output)
	require.Len(t, windows, 4)

	assert.Equal(t, 0, windows[0].Index)
	assert.Equal(t, "my window", windows[0].Name)
	assert.True(t, windows[0].Active)

	assert.Equal(t, 1, windows[1].Index)
	assert.Equal(t, "build log", windows[1].Name)
	assert.False(t, windows[1].Active)

	assert.Equal(t, 2, windows[2].Index)
	assert.Equal(t, "test runner", windows[2].Name)
	assert.False(t, windows[2].Active)

	assert.Equal(t, 3, windows[3].Index)
	assert.Equal(t, "admin panel", windows[3].Name)
	assert.False(t, windows[3].Active)
}

func TestRealTmuxExecutor_ParseCursorPosition(t *testing.T) {
	tests := []struct {
		output   string
		expectedX int
		expectedY int
	}{
		{"5 10", 5, 10},
		{"0 0", 0, 0},
		{"80 24", 80, 24},
	}

	for _, tt := range tests {
		x, y := parseCursorPosition(tt.output)
		assert.Equal(t, tt.expectedX, x)
		assert.Equal(t, tt.expectedY, y)
	}
}
