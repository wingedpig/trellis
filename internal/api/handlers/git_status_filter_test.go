// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"reflect"
	"testing"
)

func TestFilterCasesPaths(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		live string
		want []string
	}{
		{
			name: "drops live cases directory entry",
			in:   []string{"trellis/cases/", "main.go"},
			live: "trellis/cases",
			want: []string{"main.go"},
		},
		{
			name: "drops live cases directory entry without trailing slash",
			in:   []string{"trellis/cases", "main.go"},
			live: "trellis/cases",
			want: []string{"main.go"},
		},
		{
			name: "drops individual files inside the live cases dir",
			in:   []string{"trellis/cases/2026-05-15__foo/case.json", "main.go", "trellis/cases/2026-05-15__foo/notes.md"},
			live: "trellis/cases",
			want: []string{"main.go"},
		},
		{
			name: "passes through unrelated cases-archived dir",
			in:   []string{"trellis/cases-archived/2026-05-01__bar/", "main.go"},
			live: "trellis/cases",
			want: []string{"trellis/cases-archived/2026-05-01__bar/", "main.go"},
		},
		{
			name: "passes through when live dir is empty",
			in:   []string{"trellis/cases/", "main.go"},
			live: "",
			want: []string{"trellis/cases/", "main.go"},
		},
		{
			name: "handles a configured trailing slash on the live dir",
			in:   []string{"trellis/cases/", "trellis/cases/x/case.json", "main.go"},
			live: "trellis/cases/",
			want: []string{"main.go"},
		},
		{
			name: "nil in → nil/empty out",
			in:   nil,
			live: "trellis/cases",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterCasesPaths(tc.in, tc.live)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("filterCasesPaths(%v, %q) = %v, want %v", tc.in, tc.live, got, tc.want)
			}
		})
	}
}
