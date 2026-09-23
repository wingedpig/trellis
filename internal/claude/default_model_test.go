// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import "testing"

func TestCreateSessionUsesDefaultModel(t *testing.T) {
	m := NewManager("")
	before := m.CreateSession("wt", t.TempDir(), "")
	m.SetDefaultModel("sonnet", "high")
	s := m.CreateSession("wt", t.TempDir(), "")

	if got, eff := s.ModelOverride(), s.EffortOverride(); got != "sonnet" || eff != "high" {
		t.Fatalf("new session model = %q/%q, want sonnet/high", got, eff)
	}
	if got := before.ModelOverride(); got != "" {
		t.Fatalf("existing session model changed to %q", got)
	}
}
