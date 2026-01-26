// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoCompilerParser_Parse(t *testing.T) {
	parser := &GoCompilerParser{}

	tests := []struct {
		name     string
		output   string
		expected []ParsedLine
	}{
		{
			name:   "single error",
			output: "./main.go:10:5: undefined: foo\n",
			expected: []ParsedLine{
				{Type: "error", File: "./main.go", Line: 10, Column: 5, Message: "undefined: foo"},
			},
		},
		{
			name: "multiple errors",
			output: `./main.go:10:5: undefined: foo
./pkg/handler.go:25:12: cannot use x (type int) as type string
`,
			expected: []ParsedLine{
				{Type: "error", File: "./main.go", Line: 10, Column: 5, Message: "undefined: foo"},
				{Type: "error", File: "./pkg/handler.go", Line: 25, Column: 12, Message: "cannot use x (type int) as type string"},
			},
		},
		{
			name:   "error without column",
			output: "./main.go:10: syntax error\n",
			expected: []ParsedLine{
				{Type: "error", File: "./main.go", Line: 10, Column: 0, Message: "syntax error"},
			},
		},
		{
			name:     "no errors",
			output:   "# github.com/example/pkg\n",
			expected: []ParsedLine{},
		},
		{
			name:   "warning",
			output: "./main.go:10:5: warning: unused variable x\n",
			expected: []ParsedLine{
				{Type: "warning", File: "./main.go", Line: 10, Column: 5, Message: "warning: unused variable x"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse(tt.output)
			require.Len(t, result, len(tt.expected))
			for i, exp := range tt.expected {
				assert.Equal(t, exp.Type, result[i].Type, "Type mismatch at index %d", i)
				assert.Equal(t, exp.File, result[i].File, "File mismatch at index %d", i)
				assert.Equal(t, exp.Line, result[i].Line, "Line mismatch at index %d", i)
				assert.Equal(t, exp.Column, result[i].Column, "Column mismatch at index %d", i)
				assert.Equal(t, exp.Message, result[i].Message, "Message mismatch at index %d", i)
			}
		})
	}
}

func TestGoCompilerParser_ParseLine(t *testing.T) {
	parser := &GoCompilerParser{}

	tests := []struct {
		line     string
		expected *ParsedLine
	}{
		{
			line: "./main.go:10:5: undefined: foo",
			expected: &ParsedLine{
				Type:    "error",
				File:    "./main.go",
				Line:    10,
				Column:  5,
				Message: "undefined: foo",
			},
		},
		{
			line:     "# github.com/example/pkg",
			expected: nil,
		},
		{
			line:     "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			result := parser.ParseLine(tt.line)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.expected.Type, result.Type)
				assert.Equal(t, tt.expected.File, result.File)
				assert.Equal(t, tt.expected.Line, result.Line)
				assert.Equal(t, tt.expected.Column, result.Column)
				assert.Equal(t, tt.expected.Message, result.Message)
			}
		})
	}
}

func TestGoTestJSONParser_Parse(t *testing.T) {
	parser := &GoTestJSONParser{}

	tests := []struct {
		name     string
		output   string
		expected []ParsedLine
	}{
		{
			name: "pass and fail",
			output: `{"Time":"2024-01-15T14:00:00Z","Action":"pass","Package":"github.com/example/pkg","Test":"TestFoo","Elapsed":0.1}
{"Time":"2024-01-15T14:00:01Z","Action":"fail","Package":"github.com/example/pkg","Test":"TestBar","Elapsed":0.2}
`,
			expected: []ParsedLine{
				{Type: "test_pass", Package: "github.com/example/pkg", TestName: "TestFoo"},
				{Type: "test_fail", Package: "github.com/example/pkg", TestName: "TestBar"},
			},
		},
		{
			name: "skip",
			output: `{"Time":"2024-01-15T14:00:00Z","Action":"skip","Package":"github.com/example/pkg","Test":"TestSkipped","Elapsed":0.001}
`,
			expected: []ParsedLine{
				{Type: "test_skip", Package: "github.com/example/pkg", TestName: "TestSkipped"},
			},
		},
		{
			name: "with output",
			output: `{"Time":"2024-01-15T14:00:00Z","Action":"output","Package":"github.com/example/pkg","Test":"TestFoo","Output":"    error_test.go:15: expected 1, got 2\n"}
{"Time":"2024-01-15T14:00:00Z","Action":"fail","Package":"github.com/example/pkg","Test":"TestFoo","Elapsed":0.1}
`,
			expected: []ParsedLine{
				{Type: "test_fail", Package: "github.com/example/pkg", TestName: "TestFoo", RawOutput: "    error_test.go:15: expected 1, got 2\n"},
			},
		},
		{
			name: "package level actions",
			output: `{"Time":"2024-01-15T14:00:00Z","Action":"start","Package":"github.com/example/pkg"}
{"Time":"2024-01-15T14:00:01Z","Action":"pass","Package":"github.com/example/pkg","Elapsed":1.0}
`,
			expected: []ParsedLine{}, // Package-level pass/fail without Test field are ignored
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse(tt.output)
			require.Len(t, result, len(tt.expected), "expected %d results, got %d", len(tt.expected), len(result))
			for i, exp := range tt.expected {
				assert.Equal(t, exp.Type, result[i].Type)
				assert.Equal(t, exp.Package, result[i].Package)
				assert.Equal(t, exp.TestName, result[i].TestName)
				if exp.RawOutput != "" {
					assert.Contains(t, result[i].RawOutput, "expected")
				}
			}
		})
	}
}

func TestGenericParser_Parse(t *testing.T) {
	parser := &GenericParser{}

	tests := []struct {
		name     string
		output   string
		expected []ParsedLine
	}{
		{
			name:   "file:line:message",
			output: "src/main.ts:10: error TS2304: Cannot find name 'foo'\n",
			expected: []ParsedLine{
				{Type: "error", File: "src/main.ts", Line: 10, Message: "error TS2304: Cannot find name 'foo'"},
			},
		},
		{
			name:   "file:line:column:message",
			output: "src/main.ts:10:5: error TS2304: Cannot find name 'foo'\n",
			expected: []ParsedLine{
				{Type: "error", File: "src/main.ts", Line: 10, Column: 5, Message: "error TS2304: Cannot find name 'foo'"},
			},
		},
		{
			name: "mixed formats",
			output: `src/main.ts:10: error TS2304: Cannot find name 'foo'
src/other.js:20:5: warning: unused variable
`,
			expected: []ParsedLine{
				{Type: "error", File: "src/main.ts", Line: 10, Message: "error TS2304: Cannot find name 'foo'"},
				{Type: "warning", File: "src/other.js", Line: 20, Column: 5, Message: "warning: unused variable"},
			},
		},
		{
			name:     "no matches",
			output:   "Building project...\nDone.\n",
			expected: []ParsedLine{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse(tt.output)
			require.Len(t, result, len(tt.expected))
			for i, exp := range tt.expected {
				assert.Equal(t, exp.File, result[i].File)
				assert.Equal(t, exp.Line, result[i].Line)
				assert.Equal(t, exp.Column, result[i].Column)
				assert.Contains(t, result[i].Message, exp.Message)
			}
		})
	}
}

func TestNoOpParser_Parse(t *testing.T) {
	parser := &NoOpParser{}

	output := "some output\nmore output\n"
	result := parser.Parse(output)

	assert.Empty(t, result)
	assert.Equal(t, "none", parser.Name())
}

func TestHTMLParser_Parse(t *testing.T) {
	parser := &HTMLParser{}

	output := "<div>some <b>html</b> output</div>\n"
	result := parser.Parse(output)

	assert.Empty(t, result)
	assert.Equal(t, "html", parser.Name())
}

func TestFormatOutput_HTMLParser(t *testing.T) {
	// Test that HTML parser passes output through unchanged
	htmlOutput := "<div class=\"error\">Error: <b>something went wrong</b></div>"
	result := FormatOutput(htmlOutput, nil, "html")
	assert.Equal(t, htmlOutput, result, "HTML output should pass through unchanged")

	// Test that other parsers escape HTML
	plainOutput := "<script>alert('xss')</script>"
	result = FormatOutput(plainOutput, nil, "go")
	assert.Contains(t, result, "&lt;script&gt;", "Non-HTML parsers should escape HTML")
	assert.NotContains(t, result, "<script>", "Non-HTML parsers should escape HTML")

	// Test with empty parser name (should escape)
	result = FormatOutput(plainOutput, nil, "")
	assert.Contains(t, result, "&lt;script&gt;", "Empty parser should escape HTML")
}

func TestParserRegistry_Get(t *testing.T) {
	registry := NewParserRegistry()

	tests := []struct {
		name     string
		expected string
	}{
		{"go", "go"},
		{"go_test_json", "go_test_json"},
		{"generic", "generic"},
		{"none", "none"},
		{"html", "html"},
		{"unknown", "none"}, // Falls back to none
		{"", "none"},        // Empty falls back to none
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := registry.Get(tt.name)
			require.NotNil(t, parser)
			assert.Equal(t, tt.expected, parser.Name())
		})
	}
}

func TestGoCompilerParser_RealWorldOutput(t *testing.T) {
	parser := &GoCompilerParser{}

	output := `# github.com/example/app/internal/handler
internal/handler/user.go:45:12: cannot use req.ID (variable of type string) as int value in argument to s.repo.GetByID
internal/handler/user.go:67:3: undefined: ctx
# github.com/example/app/cmd/server
cmd/server/main.go:23:2: imported and not used: "fmt"
`

	result := parser.Parse(output)
	require.Len(t, result, 3)

	assert.Equal(t, "internal/handler/user.go", result[0].File)
	assert.Equal(t, 45, result[0].Line)
	assert.Contains(t, result[0].Message, "cannot use req.ID")

	assert.Equal(t, "internal/handler/user.go", result[1].File)
	assert.Equal(t, 67, result[1].Line)
	assert.Contains(t, result[1].Message, "undefined: ctx")

	assert.Equal(t, "cmd/server/main.go", result[2].File)
	assert.Equal(t, 23, result[2].Line)
	assert.Contains(t, result[2].Message, "imported and not used")
}

func TestGoTestJSONParser_RealWorldOutput(t *testing.T) {
	parser := &GoTestJSONParser{}

	output := `{"Time":"2024-01-15T14:00:00.000Z","Action":"run","Package":"github.com/example/app/pkg","Test":"TestAdd"}
{"Time":"2024-01-15T14:00:00.001Z","Action":"output","Package":"github.com/example/app/pkg","Test":"TestAdd","Output":"=== RUN   TestAdd\n"}
{"Time":"2024-01-15T14:00:00.002Z","Action":"output","Package":"github.com/example/app/pkg","Test":"TestAdd","Output":"--- PASS: TestAdd (0.00s)\n"}
{"Time":"2024-01-15T14:00:00.003Z","Action":"pass","Package":"github.com/example/app/pkg","Test":"TestAdd","Elapsed":0.001}
{"Time":"2024-01-15T14:00:00.004Z","Action":"run","Package":"github.com/example/app/pkg","Test":"TestSubtract"}
{"Time":"2024-01-15T14:00:00.005Z","Action":"output","Package":"github.com/example/app/pkg","Test":"TestSubtract","Output":"=== RUN   TestSubtract\n"}
{"Time":"2024-01-15T14:00:00.006Z","Action":"output","Package":"github.com/example/app/pkg","Test":"TestSubtract","Output":"    math_test.go:20: expected 5, got 3\n"}
{"Time":"2024-01-15T14:00:00.007Z","Action":"output","Package":"github.com/example/app/pkg","Test":"TestSubtract","Output":"--- FAIL: TestSubtract (0.00s)\n"}
{"Time":"2024-01-15T14:00:00.008Z","Action":"fail","Package":"github.com/example/app/pkg","Test":"TestSubtract","Elapsed":0.001}
{"Time":"2024-01-15T14:00:00.009Z","Action":"pass","Package":"github.com/example/app/pkg","Elapsed":0.01}
`

	result := parser.Parse(output)
	require.Len(t, result, 2)

	assert.Equal(t, "test_pass", result[0].Type)
	assert.Equal(t, "TestAdd", result[0].TestName)

	assert.Equal(t, "test_fail", result[1].Type)
	assert.Equal(t, "TestSubtract", result[1].TestName)
	assert.Contains(t, result[1].RawOutput, "expected 5, got 3")
}

func TestFormatOutput_GoTestJSON(t *testing.T) {
	parser := &GoTestJSONParser{}

	output := `{"Time":"2024-01-15T14:00:00.000Z","Action":"pass","Package":"pkg","Test":"TestOne"}
{"Time":"2024-01-15T14:00:00.001Z","Action":"fail","Package":"pkg","Test":"TestTwo"}`

	parsedLines := parser.Parse(output)
	html := FormatOutput(output, parsedLines, "go_test_json")

	// Should NOT contain raw JSON
	assert.NotContains(t, html, `"Action"`)
	assert.NotContains(t, html, `"Package"`)

	// Should contain formatted test results
	assert.Contains(t, html, "FAIL")
	assert.Contains(t, html, "TestOne")
	assert.Contains(t, html, "TestTwo")
}

func TestFormatTestResults(t *testing.T) {
	parser := &GoTestJSONParser{}

	output := `{"Time":"2024-01-15T14:00:00.000Z","Action":"run","Package":"github.com/example/app/pkg","Test":"TestAdd"}
{"Time":"2024-01-15T14:00:00.001Z","Action":"output","Package":"github.com/example/app/pkg","Test":"TestAdd","Output":"=== RUN   TestAdd\n"}
{"Time":"2024-01-15T14:00:00.002Z","Action":"output","Package":"github.com/example/app/pkg","Test":"TestAdd","Output":"--- PASS: TestAdd (0.00s)\n"}
{"Time":"2024-01-15T14:00:00.003Z","Action":"pass","Package":"github.com/example/app/pkg","Test":"TestAdd","Elapsed":0.001}
{"Time":"2024-01-15T14:00:00.004Z","Action":"run","Package":"github.com/example/app/pkg","Test":"TestSubtract"}
{"Time":"2024-01-15T14:00:00.005Z","Action":"output","Package":"github.com/example/app/pkg","Test":"TestSubtract","Output":"=== RUN   TestSubtract\n"}
{"Time":"2024-01-15T14:00:00.006Z","Action":"output","Package":"github.com/example/app/pkg","Test":"TestSubtract","Output":"    math_test.go:20: expected 5, got 3\n"}
{"Time":"2024-01-15T14:00:00.007Z","Action":"output","Package":"github.com/example/app/pkg","Test":"TestSubtract","Output":"--- FAIL: TestSubtract (0.00s)\n"}
{"Time":"2024-01-15T14:00:00.008Z","Action":"fail","Package":"github.com/example/app/pkg","Test":"TestSubtract","Elapsed":0.001}
{"Time":"2024-01-15T14:00:00.009Z","Action":"pass","Package":"github.com/example/app/pkg","Elapsed":0.01}
`

	parsedLines := parser.Parse(output)
	html := FormatTestResults(parsedLines, output)

	t.Logf("Generated HTML:\n%s", html)

	// Should contain summary
	assert.Contains(t, html, "FAIL")
	assert.Contains(t, html, "1/2 tests passed")

	// Should contain failed test name
	assert.Contains(t, html, "TestSubtract")

	// Should contain passed test name
	assert.Contains(t, html, "TestAdd")

	// Should have clickable link for file:line
	assert.Contains(t, html, "math_test.go")
	assert.Contains(t, html, "openFileAtLine")
}

func TestExtractSkipReason(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected string
	}{
		{
			name: "short mode skip",
			output: `=== RUN   TestIntegration
    integration_test.go:15: skipping in short mode
--- SKIP: TestIntegration (0.00s)
`,
			expected: "skipping in short mode",
		},
		{
			name: "custom skip message",
			output: `=== RUN   TestDatabase
    db_test.go:42: database not available
--- SKIP: TestDatabase (0.00s)
`,
			expected: "database not available",
		},
		{
			name:     "no skip reason",
			output:   "--- SKIP: TestFoo (0.00s)\n",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSkipReason(tt.output)
			assert.Equal(t, tt.expected, result)
		})
	}
}
