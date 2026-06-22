// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package genai

import (
	"reflect"
	"testing"
)

func TestComponentForPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// Go: scaffolding stripped, two-segment package path kept.
		{"internal/api/handlers/commits.go", "api/handlers"},
		{"internal/genai/summary.go", "genai"},
		{"cmd/trellis/main.go", "trellis"},
		// Top-level dirs that aren't scaffolding.
		{"views/case_detail.qtpl", "views"},
		{"static/js/wrapup.js", "static/js"},
		// Monorepo containers: the package/app name is the component.
		{"packages/auth/src/index.ts", "auth"},
		{"apps/web/src/components/Button.tsx", "web"},
		// JVM-ish nesting: scaffolding run stripped, package prefix kept.
		{"src/main/java/com/foo/Bar.java", "com/foo"},
		// Root-level files and dependency/build paths yield nothing.
		{"README.md", ""},
		{"go.mod", ""},
		{"node_modules/react/index.js", ""},
		{"vendor/foo/bar.go", ""},
		{"frontend/dist/bundle.js", ""},
		// Windows separators and dot-prefixes normalize.
		{"internal\\cases\\manager.go", "cases"},
		{"./views/inbox.qtpl", "views"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := componentForPath(tc.path); got != tc.want {
			t.Errorf("componentForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestDeriveComponents(t *testing.T) {
	// Ranked by frequency (api/handlers touched twice), then alphabetical.
	got := DeriveComponents([]string{
		"internal/api/handlers/commits.go",
		"internal/api/handlers/cases.go",
		"internal/genai/summary.go",
		"views/case_detail.qtpl",
		"README.md", // dropped
	})
	want := []string{"api/handlers", "genai", "views"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DeriveComponents = %v, want %v", got, want)
	}
}

func TestDeriveComponentsCaps(t *testing.T) {
	paths := []string{
		"a/x.go", "b/x.go", "c/x.go", "d/x.go",
		"e/x.go", "f/x.go", "g/x.go", "h/x.go",
	}
	if got := DeriveComponents(paths); len(got) != maxDerivedComponents {
		t.Errorf("DeriveComponents returned %d components, want cap of %d", len(got), maxDerivedComponents)
	}
}

func TestDeriveComponentsDedupes(t *testing.T) {
	// "my_pkg" and "my-pkg" collapse to the same identifier after normalization.
	got := DeriveComponents([]string{"my_pkg/a.go", "my-pkg/b.go"})
	want := []string{"my-pkg"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DeriveComponents = %v, want %v", got, want)
	}
}
