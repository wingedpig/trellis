package codex

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoApprovalDefaultsOnAndPreservesSavedOff(t *testing.T) {
	m := newTestManager(t)
	s := m.CreateSession("main", t.TempDir(), "New")
	require.True(t, s.SkipPermissions())
	require.NoError(t, s.SetSkipPermissions(false))
	m.persistWG.Wait()

	reloaded := NewManager(m.stateDir)
	t.Cleanup(reloaded.Shutdown)
	saved := reloaded.GetSession(s.ID())
	require.NotNil(t, saved)
	require.False(t, saved.SkipPermissions())
	require.True(t, reloaded.CreateSession("main", t.TempDir(), "Another").SkipPermissions())
}
