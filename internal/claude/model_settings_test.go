package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionModelAndPermissionSettingsPersist(t *testing.T) {
	m := newTestManager(t)
	s := m.CreateSession("main", t.TempDir(), "New")
	require.True(t, s.SkipPermissions())
	require.NoError(t, s.SetModelOption("opus", "max"))
	require.NoError(t, s.SetSkipPermissions(false))
	m.persistWG.Wait()

	reloaded := NewManager(m.stateDir)
	t.Cleanup(reloaded.Shutdown)
	saved := reloaded.GetSession(s.ID())
	require.NotNil(t, saved)
	require.False(t, saved.SkipPermissions(), "an explicit off must survive the new default")
	require.Equal(t, "opus", saved.ModelOverride())
	require.Equal(t, "max", saved.EffortOverride())

	require.Error(t, saved.SetModelOption("haiku", "high"))
	require.Error(t, saved.SetModelOption("opus", "ultra"))
	require.Error(t, saved.SetModelOption("", "max"))
	require.Equal(t, "opus", saved.ModelOverride())
	require.Equal(t, "max", saved.EffortOverride())
	require.NoError(t, saved.SetModel(""))
	require.Empty(t, saved.ModelOverride())
	require.Empty(t, saved.EffortOverride())
}

type modelControlWriter struct{ bytes.Buffer }

func (*modelControlWriter) Close() error { return nil }

func TestModelOnlySwitchIsLiveAndEffortRestartWaitsForIdle(t *testing.T) {
	writer := &modelControlWriter{}
	cancelled := false
	s := &Session{
		modelOverride: "opus", effortOverride: "high",
		stdin: writer, started: true, cancel: func() { cancelled = true },
	}
	require.NoError(t, s.SetModelOption("sonnet", "high"))
	require.False(t, cancelled)
	var request stdinControlRequest
	require.NoError(t, json.Unmarshal(writer.Bytes(), &request))
	require.Equal(t, "set_model", request.Request.Subtype)
	require.Equal(t, "sonnet", request.Request.Model)

	for _, busy := range []string{"turn", "background task", "permission"} {
		s.generating = busy == "turn"
		s.backgroundTasks = nil
		s.pendingControlRequests = nil
		if busy == "background task" {
			s.backgroundTasks = []BackgroundTask{{TaskID: "task"}}
		}
		if busy == "permission" {
			s.pendingControlRequests = []*StreamEvent{{RequestID: "permission"}}
		}
		require.Error(t, s.SetModelOption("opus", "max"), busy)
		require.False(t, cancelled, busy)
		require.Equal(t, "sonnet", s.ModelOverride())
		require.Equal(t, "high", s.EffortOverride())
	}
	s.pendingControlRequests = nil
	require.NoError(t, s.SetModelOption("opus", "max"))
	require.True(t, cancelled)
	require.False(t, s.started)
	require.Equal(t, "max", s.EffortOverride())
}

func TestClaudeSpawnAppliesEffortAndAutoApproval(t *testing.T) {
	binDir := t.TempDir()
	argsFile := filepath.Join(binDir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TRELLIS_TEST_ARGS_FILE\"\nwhile IFS= read -r line; do :; done\n"
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TRELLIS_TEST_ARGS_FILE", argsFile)
	m := newTestManager(t)
	s := m.CreateSession("main", binDir, "New")
	require.NoError(t, s.SetModelOption("opus", "max"))
	require.NoError(t, s.EnsureProcess(context.Background()))
	var args string
	require.Eventually(t, func() bool {
		data, _ := os.ReadFile(argsFile)
		args = string(data)
		return strings.Contains(args, "--effort\nmax\n")
	}, 2*time.Second, 10*time.Millisecond)
	require.Contains(t, args, "--model\nopus\n")
	require.Contains(t, args, "--permission-mode\nauto\n")
}
