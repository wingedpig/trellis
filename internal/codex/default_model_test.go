// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package codex

import "testing"

func TestCreateSessionUsesDefaultModel(t *testing.T) {
	m := NewManager("")
	before := m.CreateSession("wt", t.TempDir(), "")
	m.SetDefaultModel("gpt-6-sol", "high")
	s := m.CreateSession("wt", t.TempDir(), "")

	if model, effort := s.ModelOverride(); model != "gpt-6-sol" || effort != "high" {
		t.Fatalf("new session model = %q/%q, want gpt-6-sol/high", model, effort)
	}
	if model, _ := before.ModelOverride(); model != "" {
		t.Fatalf("existing session model changed to %q", model)
	}
}
