// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package workflow

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// FormatOutput formats workflow output based on the parser type.
// For HTML parser, output is passed through unchanged.
// For go_test_json parser, test results are rendered as a summary.
// For other parsers, output is HTML-escaped with clickable file links.
func FormatOutput(output string, parsedLines []ParsedLine, parserName string) string {
	if parserName == "html" {
		return output
	}
	if parserName == "go_test_json" {
		// During streaming, parsedLines is nil - don't show raw JSON
		if len(parsedLines) == 0 {
			return "<span class=\"text-muted\">Running tests...</span>"
		}
		return FormatTestResults(parsedLines, output)
	}
	return FormatOutputHTML(output, parsedLines)
}

// FormatTestResults formats go test -json parsed results into readable HTML.
func FormatTestResults(parsedLines []ParsedLine, rawOutput string) string {
	if len(parsedLines) == 0 {
		// No parsed lines - fall back to showing raw output
		return FormatOutputHTML(rawOutput, nil)
	}

	var sb strings.Builder

	// Count results
	var passed, failed, skipped int
	var failures []ParsedLine
	for _, line := range parsedLines {
		switch line.Type {
		case "test_pass":
			passed++
		case "test_fail":
			failed++
			failures = append(failures, line)
		case "test_skip":
			skipped++
		}
	}

	// Summary header
	total := passed + failed + skipped
	if failed > 0 {
		sb.WriteString(fmt.Sprintf("<span class=\"text-danger\"><strong>FAIL</strong></span> - %d/%d tests passed", passed, total))
	} else {
		sb.WriteString(fmt.Sprintf("<span class=\"text-success\"><strong>PASS</strong></span> - %d/%d tests passed", passed, total))
	}
	if skipped > 0 {
		sb.WriteString(fmt.Sprintf(", %d skipped", skipped))
	}
	sb.WriteString("<br><br>")

	// Show failures with details (expanded by default)
	if len(failures) > 0 {
		sb.WriteString("<details open><summary style=\"cursor: pointer;\"><strong>Failed Tests</strong> <span class=\"text-muted\">(")
		sb.WriteString(fmt.Sprintf("%d", failed))
		sb.WriteString(")</span></summary><div style=\"margin-left: 1em; margin-top: 0.5em;\">")
		for _, f := range failures {
			sb.WriteString(fmt.Sprintf("<span class=\"text-danger\">✗</span> %s/%s<br>",
				html.EscapeString(f.Package), html.EscapeString(f.TestName)))
			if f.RawOutput != "" {
				// Format the test output, making file:line references clickable
				formattedOutput := formatTestOutput(f.RawOutput)
				sb.WriteString("<pre style=\"margin-left: 1em; margin-top: 0.5em; margin-bottom: 1em;\">")
				sb.WriteString(formattedOutput)
				sb.WriteString("</pre>")
			}
		}
		sb.WriteString("</div></details>")
	}

	// Show passed tests (collapsed by default if there are failures)
	if passed > 0 {
		if failed > 0 {
			sb.WriteString("<details><summary style=\"cursor: pointer;\"><strong>Passed Tests</strong> <span class=\"text-muted\">(")
		} else {
			sb.WriteString("<details open><summary style=\"cursor: pointer;\"><strong>Passed Tests</strong> <span class=\"text-muted\">(")
		}
		sb.WriteString(fmt.Sprintf("%d", passed))
		sb.WriteString(")</span></summary><div style=\"margin-left: 1em; margin-top: 0.5em;\">")
		for _, line := range parsedLines {
			if line.Type == "test_pass" {
				sb.WriteString(fmt.Sprintf("<span class=\"text-success\">✓</span> %s/%s<br>",
					html.EscapeString(line.Package), html.EscapeString(line.TestName)))
			}
		}
		sb.WriteString("</div></details>")
	}

	// Show skipped tests (collapsed by default)
	if skipped > 0 {
		sb.WriteString("<details><summary style=\"cursor: pointer;\"><strong>Skipped Tests</strong> <span class=\"text-muted\">(")
		sb.WriteString(fmt.Sprintf("%d", skipped))
		sb.WriteString(")</span></summary><div style=\"margin-left: 1em; margin-top: 0.5em;\">")
		for _, line := range parsedLines {
			if line.Type == "test_skip" {
				sb.WriteString(fmt.Sprintf("<span class=\"text-warning\">○</span> %s/%s",
					html.EscapeString(line.Package), html.EscapeString(line.TestName)))
				// Extract skip reason from output
				if reason := extractSkipReason(line.RawOutput); reason != "" {
					sb.WriteString(fmt.Sprintf(" - <span class=\"text-muted\">%s</span>", html.EscapeString(reason)))
				}
				sb.WriteString("<br>")
			}
		}
		sb.WriteString("</div></details>")
	}

	return sb.String()
}

// formatTestOutput formats test failure output, making file:line references clickable.
func formatTestOutput(output string) string {
	lines := strings.Split(output, "\n")
	var result []string

	// Pattern for file:line:col or file:line references
	fileLinePattern := regexp.MustCompile(`(\S+\.go):(\d+)(?::(\d+))?`)

	for _, line := range lines {
		// Skip empty lines and redundant markers
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" {
			continue
		}

		// Make file:line references clickable
		escaped := html.EscapeString(line)
		escaped = fileLinePattern.ReplaceAllStringFunc(escaped, func(match string) string {
			// The match is already escaped, need to unescape for processing
			unescaped := html.UnescapeString(match)
			parts := fileLinePattern.FindStringSubmatch(unescaped)
			if parts == nil {
				return match
			}
			file := parts[1]
			lineNum := parts[2]
			col := "0"
			if len(parts) > 3 && parts[3] != "" {
				col = parts[3]
			}
			return fmt.Sprintf("<a href=\"#\" class=\"text-danger\" onclick=\"openFileAtLine('%s', %s, %s); return false;\">%s</a>",
				html.EscapeString(file), lineNum, col, match)
		})

		result = append(result, escaped)
	}

	return strings.Join(result, "\n")
}

// extractSkipReason extracts the skip reason from test output.
// Go test output for skipped tests looks like:
//
//	=== RUN   TestFoo
//	    foo_test.go:10: skipping in short mode
//	--- SKIP: TestFoo (0.00s)
func extractSkipReason(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip the === RUN and --- SKIP lines
		if strings.HasPrefix(trimmed, "===") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		// Look for lines with file:line: prefix (the actual skip message)
		if idx := strings.Index(trimmed, ".go:"); idx != -1 {
			// Find the colon after line number
			rest := trimmed[idx+4:]
			if colonIdx := strings.Index(rest, ": "); colonIdx != -1 {
				reason := strings.TrimSpace(rest[colonIdx+2:])
				if reason != "" {
					return reason
				}
			}
		}
	}
	return ""
}

// FormatOutputHTML converts raw output to HTML with clickable file links.
// This replicates the logic from the runner's /recompile endpoint.
func FormatOutputHTML(output string, parsedLines []ParsedLine) string {
	if output == "" {
		return ""
	}

	// Convert escaped characters to actual characters (e.g., \t to tab, \n to newline)
	// This handles cases where tools output literal escape sequences
	output = strings.ReplaceAll(output, "\\t", "\t")
	output = strings.ReplaceAll(output, "\\n", "\n")

	lines := strings.Split(output, "\n")

	// First pass: handle Go compiler errors (file:line:col format)
	// Pattern: after a line starting with "# ", look for file:line:col patterns
	foundLine := false
	for i, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			foundLine = true
			continue
		}
		if len(line) > 0 && (line[0] == '\t' || line[0] == ' ') {
			continue
		}
		// error lines contain colons, other lines don't
		if !strings.Contains(line, ":") {
			foundLine = false
			continue
		}
		if !foundLine {
			continue
		}
		// cmd/web/web.go:2109:36: undefined: pendingMsgView
		end := strings.Index(line, " ")
		if end <= 0 {
			continue
		}
		// file:line:col is everything before the first space, minus the trailing colon
		fileLineCol := line[:end-1]
		href := fmt.Sprintf("<a href=\"#\" class=\"text-danger\" onclick=\"openFileAtLine('%s'); return false;\">%s</a>",
			html.EscapeString(fileLineCol), html.EscapeString(fileLineCol))
		lines[i] = href + html.EscapeString(line[end-1:])
	}

	// Second pass: handle qtc errors
	// qtc: 2020/09/27 10:04:35 error when parsing file "...": ... "end" at file "...", line 51, pos 54, ...
	qtcFieldRegex := regexp.MustCompile(`[^\s"']+|"([^"]*)"|'([^']*)`)
	for i, line := range lines {
		if len(line) < 6 || line[:5] != "qtc: " {
			continue
		}

		fields := qtcFieldRegex.FindAllString(line, -1)
		if len(fields) < 8 || fields[3] != "error" {
			continue
		}

		// Find "line" and "pos" keywords
		var lineNum, posNum int
		for j := range fields {
			if fields[j] == "line" && j+1 < len(fields) {
				fmt.Sscanf(fields[j+1], "%d,", &lineNum)
			}
			if fields[j] == "pos" && j+1 < len(fields) {
				fmt.Sscanf(fields[j+1], "%d,", &posNum)
			}
		}

		// fields[7] should be the file path in quotes like "cmd/web/file.qtpl"
		if len(fields) > 7 && len(fields[7]) > 2 {
			filePath := fields[7][1 : len(fields[7])-1] // strip quotes
			href := fmt.Sprintf("<a href=\"#\" class=\"text-danger\" onclick=\"openFileAtLine('%s', %d, %d); return false;\">%s</a>",
				html.EscapeString(filePath), lineNum, posNum, html.EscapeString(filePath))
			fields[7] = href

			// Replace standalone colons with ":\n " for better readability
			for j := range fields {
				if fields[j] == ":" {
					fields[j] = ":<br> "
				}
			}

			lines[i] = strings.Join(fields, " ")
		}
	}

	// Escape any lines that weren't already processed, then join and convert newlines to <br>
	for i, line := range lines {
		// Skip lines that already have HTML (contain href)
		if !strings.Contains(line, "href=") {
			lines[i] = html.EscapeString(line)
		}
	}

	result := strings.Join(lines, "\n")
	result = strings.ReplaceAll(result, "\n", "<br>")

	// Convert tabs to non-breaking spaces for proper display
	result = strings.ReplaceAll(result, "\t", "&nbsp;&nbsp;&nbsp;&nbsp;")

	return result
}
