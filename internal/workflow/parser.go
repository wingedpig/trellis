// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package workflow

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// GoCompilerParser parses Go compiler output.
type GoCompilerParser struct{}

func (p *GoCompilerParser) Name() string {
	return "go"
}

// Parse parses multiple lines of Go compiler output.
func (p *GoCompilerParser) Parse(output string) []ParsedLine {
	var result []ParsedLine
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if parsed := p.ParseLine(line); parsed != nil {
			result = append(result, *parsed)
		}
	}
	return result
}

// goErrorPattern matches: file:line:col: message or file:line: message
// Matches files with common extensions (.go, .qtpl, .s, .c, etc.)
// The file path part cannot contain spaces (to avoid matching log timestamps)
// Supports Windows paths with drive letters (e.g., C:\foo\bar.go)
var goErrorPattern = regexp.MustCompile(`^((?:[A-Za-z]:)?[^\s:]+\.[a-zA-Z]+):(\d+):(\d+)?:?\s*(.+)$`)

// qtcErrorPattern matches quicktemplate compiler errors:
// "identifier" at file "path/to/file.qtpl", line 90, pos 13
// The pattern can appear anywhere in the line (often embedded in longer error messages)
var qtcErrorPattern = regexp.MustCompile(`"([^"]*)" at file "([^"]+)", line (\d+), pos (\d+)`)

// ParseLine parses a single line of Go compiler output.
func (p *GoCompilerParser) ParseLine(line string) *ParsedLine {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}

	// Try standard Go compiler format first
	matches := goErrorPattern.FindStringSubmatch(line)
	if matches != nil {
		lineNum, _ := strconv.Atoi(matches[2])
		col := 0
		if matches[3] != "" {
			col, _ = strconv.Atoi(matches[3])
		}

		errType := "error"
		message := strings.TrimSpace(matches[4])
		if strings.Contains(strings.ToLower(message), "warning") {
			errType = "warning"
		}

		return &ParsedLine{
			Type:    errType,
			File:    matches[1],
			Line:    lineNum,
			Column:  col,
			Message: message,
		}
	}

	// Try quicktemplate compiler format
	matches = qtcErrorPattern.FindStringSubmatch(line)
	if matches != nil {
		lineNum, _ := strconv.Atoi(matches[3])
		col, _ := strconv.Atoi(matches[4])

		return &ParsedLine{
			Type:    "error",
			File:    matches[2],
			Line:    lineNum,
			Column:  col,
			Message: "undefined identifier: " + matches[1],
		}
	}

	return nil
}

// GoTestJSONParser parses `go test -json` output.
type GoTestJSONParser struct{}

func (p *GoTestJSONParser) Name() string {
	return "go_test_json"
}

// goTestEvent represents a single event from `go test -json`.
type goTestEvent struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

// Parse parses `go test -json` output.
func (p *GoTestJSONParser) Parse(output string) []ParsedLine {
	var result []ParsedLine
	// Track output per test for aggregating into final result
	testOutput := make(map[string][]string) // key: "package/test"

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event goTestEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		// Skip events without a test name (package-level events)
		if event.Test == "" {
			continue
		}

		key := event.Package + "/" + event.Test

		switch event.Action {
		case "output":
			testOutput[key] = append(testOutput[key], event.Output)
		case "pass":
			result = append(result, ParsedLine{
				Type:     "test_pass",
				Package:  event.Package,
				TestName: event.Test,
			})
		case "fail":
			rawOutput := strings.Join(testOutput[key], "")
			result = append(result, ParsedLine{
				Type:      "test_fail",
				Package:   event.Package,
				TestName:  event.Test,
				RawOutput: rawOutput,
			})
		case "skip":
			result = append(result, ParsedLine{
				Type:     "test_skip",
				Package:  event.Package,
				TestName: event.Test,
			})
		}
	}

	return result
}

// ParseLine is not applicable for JSON parser as it needs full context.
func (p *GoTestJSONParser) ParseLine(line string) *ParsedLine {
	return nil
}

// GenericParser parses generic file:line:message format.
type GenericParser struct{}

func (p *GenericParser) Name() string {
	return "generic"
}

// genericPattern matches: file:line:col: message or file:line: message
// Uses non-greedy matching to handle Windows paths (e.g., C:\foo\bar.go:10:5: message)
var genericPattern = regexp.MustCompile(`^((?:[A-Za-z]:)?[^:]+):(\d+):(\d+)?:?\s*(.+)$`)

// Parse parses multiple lines of generic output.
func (p *GenericParser) Parse(output string) []ParsedLine {
	var result []ParsedLine
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if parsed := p.ParseLine(line); parsed != nil {
			result = append(result, *parsed)
		}
	}
	return result
}

// ParseLine parses a single line of generic output.
func (p *GenericParser) ParseLine(line string) *ParsedLine {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	matches := genericPattern.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	lineNum, _ := strconv.Atoi(matches[2])
	col := 0
	if matches[3] != "" {
		col, _ = strconv.Atoi(matches[3])
	}

	errType := "error"
	message := strings.TrimSpace(matches[4])
	if strings.Contains(strings.ToLower(message), "warning") {
		errType = "warning"
	}

	return &ParsedLine{
		Type:    errType,
		File:    matches[1],
		Line:    lineNum,
		Column:  col,
		Message: message,
	}
}

// NoOpParser does no parsing.
type NoOpParser struct{}

func (p *NoOpParser) Name() string {
	return "none"
}

// Parse returns an empty slice.
func (p *NoOpParser) Parse(output string) []ParsedLine {
	return []ParsedLine{}
}

// ParseLine returns nil.
func (p *NoOpParser) ParseLine(line string) *ParsedLine {
	return nil
}

// HTMLParser assumes output is already HTML and should not be escaped.
// When this parser is used, the output is passed through directly without
// HTML escaping or link formatting.
type HTMLParser struct{}

func (p *HTMLParser) Name() string {
	return "html"
}

// Parse returns an empty slice (no structured parsing for HTML).
func (p *HTMLParser) Parse(output string) []ParsedLine {
	return []ParsedLine{}
}

// ParseLine returns nil.
func (p *HTMLParser) ParseLine(line string) *ParsedLine {
	return nil
}
