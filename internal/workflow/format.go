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
// For other parsers, output is HTML-escaped with clickable file links.
func FormatOutput(output string, parsedLines []ParsedLine, parserName string) string {
	if parserName == "html" {
		return output
	}
	return FormatOutputHTML(output, parsedLines)
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
