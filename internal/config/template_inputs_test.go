package config

import "testing"

func TestInputsPreservation(t *testing.T) {
	expander := NewTemplateExpander()
	ctx := &TemplateContext{
		Worktree: WorktreeTemplateData{Root: "/some/path"},
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple inputs pattern",
			input:    "{{ .Inputs.msgid }}",
			expected: "{{ .Inputs.msgid }}",
		},
		{
			name:     "mixed template",
			input:    "{{ .Worktree.Root }}/cmd --msgid={{ .Inputs.msgid }}",
			expected: "/some/path/cmd --msgid={{ .Inputs.msgid }}",
		},
		{
			name:     "multiple inputs",
			input:    "-msgid={{ .Inputs.msgid }} -date={{ .Inputs.date }}",
			expected: "-msgid={{ .Inputs.msgid }} -date={{ .Inputs.date }}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := expander.Expand(tt.input, ctx)
			if err != nil {
				t.Fatalf("Expand() error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expand() = %q, want %q", result, tt.expected)
			}
		})
	}
}
