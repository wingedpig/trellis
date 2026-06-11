// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package genai

import (
	"reflect"
	"testing"
)

func TestNormalizeComponents(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "package-style paths preserved",
			in:   []string{"internal/cases", "internal/api/handlers", "views"},
			want: []string{"internal/cases", "internal/api/handlers", "views"},
		},
		{
			name: "prose entries get kebab-cased",
			in:   []string{"Case detail page", "wrap up workflow"},
			want: []string{"case-detail-page", "wrap-up-workflow"},
		},
		{
			name: "underscores become hyphens",
			in:   []string{"summary_generation"},
			want: []string{"summary-generation"},
		},
		{
			name: "trim, drop empty",
			in:   []string{"  views  ", "", "   "},
			want: []string{"views"},
		},
		{
			name: "dedup is case-insensitive",
			in:   []string{"Cases", "cases", "CASES"},
			want: []string{"cases"},
		},
		{
			name: "drops sentence-like entries (>5 hyphens after normalization)",
			in:   []string{"this is a long english sentence we should drop"},
			want: nil,
		},
		{
			name: "5-hyphen identifier kept (boundary, > rule)",
			in:   []string{"a-b-c-d-e-f"},
			want: []string{"a-b-c-d-e-f"},
		},
		{
			name: "6-hyphen identifier dropped",
			in:   []string{"a-b-c-d-e-f-g"},
			want: nil,
		},
		{
			name: "leading/trailing hyphens trimmed",
			in:   []string{"-views-"},
			want: []string{"views"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeComponents(tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("NormalizeComponents(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalize_idempotent(t *testing.T) {
	comps := []string{"Case detail page", "internal/cases", "internal_cases"}
	once := NormalizeComponents(comps)
	twice := NormalizeComponents(once)
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("not idempotent: once=%v twice=%v", once, twice)
	}
}
