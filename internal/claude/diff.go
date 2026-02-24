// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// enrichEditBlock adds pre-rendered diff HTML to an Edit tool_use block.
// It reads the file before the edit is applied and generates a unified-style
// HTML diff table with line numbers and context lines.
func enrichEditBlock(block *ContentBlock, workDir string) {
	if block.Type != "tool_use" || block.Name != "Edit" {
		return
	}
	if len(block.Input) == 0 {
		return
	}

	var input editInput
	if err := json.Unmarshal(block.Input, &input); err != nil {
		return
	}

	block.DiffHTML = generateDiffHTML(workDir, input)
}

// resolvePath resolves a file path relative to workDir, handling ~/ prefix.
func resolvePath(filePath, workDir string) string {
	if strings.HasPrefix(filePath, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, filePath[2:])
		}
	} else if !filepath.IsAbs(filePath) {
		return filepath.Join(workDir, filePath)
	}
	return filePath
}

// isBinaryData checks if data contains null bytes in the first 8KB.
func isBinaryData(data []byte) bool {
	checkLen := len(data)
	if checkLen > 8192 {
		checkLen = 8192
	}
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// generateDiffHTML produces an HTML diff table for an Edit operation.
// Returns "" if the file cannot be read or the old_string is not found.
func generateDiffHTML(workDir string, input editInput) string {
	if input.FilePath == "" {
		return ""
	}

	path := resolvePath(input.FilePath, workDir)

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	// Guard: file too large
	if len(data) > 1024*1024 {
		return ""
	}

	// Guard: binary file
	if isBinaryData(data) {
		return ""
	}

	content := string(data)

	// Guard: diff too large
	if len(input.OldString)+len(input.NewString) > 50*1024 {
		return ""
	}

	// Handle pure insertion (empty old_string)
	if input.OldString == "" {
		return generateInsertionDiffHTML(input.FilePath, content, input.NewString)
	}

	// Find old_string in file
	idx := strings.Index(content, input.OldString)
	if idx < 0 {
		return ""
	}

	// Count newlines before match to find 1-based start line
	startLine := strings.Count(content[:idx], "\n") + 1

	// Split file into lines
	fileLines := strings.Split(content, "\n")

	// Split old and new strings into lines
	oldLines := strings.Split(input.OldString, "\n")
	newLines := strings.Split(input.NewString, "\n")

	// Calculate context region (3 lines before and after)
	contextBefore := 3
	contextAfter := 3
	regionStart := startLine - contextBefore
	if regionStart < 1 {
		regionStart = 1
	}
	regionEnd := startLine + len(oldLines) - 1 + contextAfter
	if regionEnd > len(fileLines) {
		regionEnd = len(fileLines)
	}

	// Count additions and deletions
	addCount := len(newLines)
	delCount := len(oldLines)
	// For pure deletion (empty new_string), newLines will be [""] which is 1 line
	if input.NewString == "" {
		addCount = 0
	}

	// Build HTML
	var b strings.Builder
	b.WriteString(`<div class="claude-diff">`)

	// Header
	displayPath := input.FilePath
	b.WriteString(`<div class="claude-diff-header">`)
	b.WriteString(`<span class="claude-diff-file">`)
	b.WriteString(html.EscapeString(displayPath))
	b.WriteString(`</span>`)
	b.WriteString(`<span class="claude-diff-stats">`)
	if addCount > 0 {
		b.WriteString(fmt.Sprintf(`<span class="claude-diff-stat-add">+%d</span> `, addCount))
	}
	if delCount > 0 {
		b.WriteString(fmt.Sprintf(`<span class="claude-diff-stat-del">-%d</span>`, delCount))
	}
	b.WriteString(`</span>`)
	b.WriteString(`</div>`)

	// Table
	b.WriteString(`<table class="claude-diff-table">`)

	oldLineNum := regionStart
	newLineNum := regionStart

	// Context lines before
	for i := regionStart; i < startLine; i++ {
		line := ""
		if i-1 < len(fileLines) {
			line = fileLines[i-1]
		}
		b.WriteString(fmt.Sprintf(`<tr class="claude-diff-ctx"><td class="claude-diff-ln">%d</td><td class="claude-diff-ln">%d</td><td class="claude-diff-code"> %s</td></tr>`,
			oldLineNum, newLineNum, html.EscapeString(line)))
		oldLineNum++
		newLineNum++
	}

	// Deleted lines (old)
	for _, line := range oldLines {
		b.WriteString(fmt.Sprintf(`<tr class="claude-diff-del"><td class="claude-diff-ln">%d</td><td class="claude-diff-ln"></td><td class="claude-diff-code">-%s</td></tr>`,
			oldLineNum, html.EscapeString(line)))
		oldLineNum++
	}

	// Added lines (new)
	if input.NewString != "" {
		for _, line := range newLines {
			b.WriteString(fmt.Sprintf(`<tr class="claude-diff-add"><td class="claude-diff-ln"></td><td class="claude-diff-ln">%d</td><td class="claude-diff-code">+%s</td></tr>`,
				newLineNum, html.EscapeString(line)))
			newLineNum++
		}
	}

	// Context lines after
	afterStart := startLine + len(oldLines)
	for i := afterStart; i <= regionEnd; i++ {
		line := ""
		if i-1 < len(fileLines) {
			line = fileLines[i-1]
		}
		b.WriteString(fmt.Sprintf(`<tr class="claude-diff-ctx"><td class="claude-diff-ln">%d</td><td class="claude-diff-ln">%d</td><td class="claude-diff-code"> %s</td></tr>`,
			oldLineNum, newLineNum, html.EscapeString(line)))
		oldLineNum++
		newLineNum++
	}

	b.WriteString(`</table>`)
	b.WriteString(`</div>`)

	return b.String()
}

// generateInsertionDiffHTML handles the case where old_string is empty (pure insertion).
// The new content is inserted at the beginning of the file.
func generateInsertionDiffHTML(filePath, content, newString string) string {
	newLines := strings.Split(newString, "\n")
	fileLines := strings.Split(content, "\n")

	// Show first few context lines after insertion point
	contextAfter := 3
	contextEnd := contextAfter
	if contextEnd > len(fileLines) {
		contextEnd = len(fileLines)
	}

	var b strings.Builder
	b.WriteString(`<div class="claude-diff">`)

	// Header
	b.WriteString(`<div class="claude-diff-header">`)
	b.WriteString(`<span class="claude-diff-file">`)
	b.WriteString(html.EscapeString(filePath))
	b.WriteString(`</span>`)
	b.WriteString(`<span class="claude-diff-stats">`)
	b.WriteString(fmt.Sprintf(`<span class="claude-diff-stat-add">+%d</span>`, len(newLines)))
	b.WriteString(`</span>`)
	b.WriteString(`</div>`)

	// Table
	b.WriteString(`<table class="claude-diff-table">`)

	newLineNum := 1
	for _, line := range newLines {
		b.WriteString(fmt.Sprintf(`<tr class="claude-diff-add"><td class="claude-diff-ln"></td><td class="claude-diff-ln">%d</td><td class="claude-diff-code">+%s</td></tr>`,
			newLineNum, html.EscapeString(line)))
		newLineNum++
	}

	// Context after
	for i := 0; i < contextEnd; i++ {
		b.WriteString(fmt.Sprintf(`<tr class="claude-diff-ctx"><td class="claude-diff-ln">%d</td><td class="claude-diff-ln">%d</td><td class="claude-diff-code"> %s</td></tr>`,
			i+1, newLineNum, html.EscapeString(fileLines[i])))
		newLineNum++
	}

	b.WriteString(`</table>`)
	b.WriteString(`</div>`)

	return b.String()
}

// enrichWriteBlock adds pre-rendered diff HTML to a Write tool_use block.
// For new files it shows the first 50 lines as additions.
// For existing files it computes an LCS-based line diff.
func enrichWriteBlock(block *ContentBlock, workDir string) {
	if block.Type != "tool_use" || block.Name != "Write" {
		return
	}
	if len(block.Input) == 0 {
		return
	}

	var input writeInput
	if err := json.Unmarshal(block.Input, &input); err != nil {
		return
	}

	block.DiffHTML = generateWriteDiffHTML(workDir, input)
}

// generateWriteDiffHTML produces diff HTML for a Write operation.
func generateWriteDiffHTML(workDir string, input writeInput) string {
	if input.FilePath == "" {
		return ""
	}

	// Guard: new content too large
	if len(input.Content) > 50*1024 {
		return ""
	}

	path := resolvePath(input.FilePath, workDir)

	data, err := os.ReadFile(path)
	if err != nil {
		// File doesn't exist — show as new file
		return generateNewFileDiffHTML(input.FilePath, input.Content)
	}

	// Guard: existing file too large
	if len(data) > 1024*1024 {
		return ""
	}

	// Guard: binary file
	if isBinaryData(data) {
		return ""
	}

	oldContent := string(data)

	// No changes
	if oldContent == input.Content {
		return ""
	}

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(input.Content, "\n")

	// For large files, show summary only
	if len(oldLines) > 500 || len(newLines) > 500 {
		return generateLargeFileSummaryHTML(input.FilePath, oldLines, newLines)
	}

	// Compute LCS-based diff
	edits := computeLineDiff(oldLines, newLines)
	return generateFullWriteDiffHTML(input.FilePath, edits)
}

// generateNewFileDiffHTML shows the first 50 lines of a new file as additions.
func generateNewFileDiffHTML(filePath, content string) string {
	lines := strings.Split(content, "\n")
	maxLines := 50
	truncated := len(lines) > maxLines
	showLines := lines
	if truncated {
		showLines = lines[:maxLines]
	}

	var b strings.Builder
	b.WriteString(`<div class="claude-diff">`)

	// Header
	b.WriteString(`<div class="claude-diff-header">`)
	b.WriteString(`<span class="claude-diff-file">`)
	b.WriteString(html.EscapeString(filePath))
	b.WriteString(` <em>(new file)</em>`)
	b.WriteString(`</span>`)
	b.WriteString(`<span class="claude-diff-stats">`)
	fmt.Fprintf(&b, `<span class="claude-diff-stat-add">+%d</span>`, len(lines))
	b.WriteString(`</span>`)
	b.WriteString(`</div>`)

	// Table
	b.WriteString(`<table class="claude-diff-table">`)

	for i, line := range showLines {
		fmt.Fprintf(&b, `<tr class="claude-diff-add"><td class="claude-diff-ln"></td><td class="claude-diff-ln">%d</td><td class="claude-diff-code">+%s</td></tr>`,
			i+1, html.EscapeString(line))
	}

	if truncated {
		fmt.Fprintf(&b, `<tr class="claude-diff-ctx"><td class="claude-diff-ln"></td><td class="claude-diff-ln"></td><td class="claude-diff-code">... +%d more lines</td></tr>`,
			len(lines)-maxLines)
	}

	b.WriteString(`</table>`)
	b.WriteString(`</div>`)

	return b.String()
}

// generateLargeFileSummaryHTML shows a stats-only summary for large file diffs.
func generateLargeFileSummaryHTML(filePath string, oldLines, newLines []string) string {
	// Quick count: lines unique to old/new
	addCount := 0
	delCount := 0
	oldSet := make(map[string]int, len(oldLines))
	for _, l := range oldLines {
		oldSet[l]++
	}
	newSet := make(map[string]int, len(newLines))
	for _, l := range newLines {
		newSet[l]++
	}
	for l, c := range newSet {
		oc := oldSet[l]
		if c > oc {
			addCount += c - oc
		}
	}
	for l, c := range oldSet {
		nc := newSet[l]
		if c > nc {
			delCount += c - nc
		}
	}

	var b strings.Builder
	b.WriteString(`<div class="claude-diff">`)
	b.WriteString(`<div class="claude-diff-header">`)
	b.WriteString(`<span class="claude-diff-file">`)
	b.WriteString(html.EscapeString(filePath))
	b.WriteString(`</span>`)
	b.WriteString(`<span class="claude-diff-stats">`)
	if addCount > 0 {
		fmt.Fprintf(&b, `<span class="claude-diff-stat-add">+%d</span> `, addCount)
	}
	if delCount > 0 {
		fmt.Fprintf(&b, `<span class="claude-diff-stat-del">-%d</span>`, delCount)
	}
	fmt.Fprintf(&b, ` <span style="opacity:0.6">(%d → %d lines, diff too large for line-by-line view)</span>`, len(oldLines), len(newLines))
	b.WriteString(`</span>`)
	b.WriteString(`</div>`)
	b.WriteString(`</div>`)

	return b.String()
}

// diffOp represents a line diff operation.
type diffOp int

const (
	diffKeep   diffOp = iota
	diffDelete
	diffInsert
)

// diffLine represents a single line in the diff output.
type diffLine struct {
	Op      diffOp
	OldLine int // 1-based line number in old file (0 for inserts)
	NewLine int // 1-based line number in new file (0 for deletes)
	Text    string
}

// computeLineDiff computes an LCS-based diff between old and new lines.
// Returns a sequence of diffLine entries.
func computeLineDiff(oldLines, newLines []string) []diffLine {
	m := len(oldLines)
	n := len(newLines)

	// Build DP table for LCS length
	dp := make([][]int, m+1)
	for i := 0; i <= m; i++ {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Backtrack to produce edit script
	var result []diffLine
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			result = append(result, diffLine{Op: diffKeep, OldLine: i, NewLine: j, Text: oldLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			result = append(result, diffLine{Op: diffInsert, NewLine: j, Text: newLines[j-1]})
			j--
		} else {
			result = append(result, diffLine{Op: diffDelete, OldLine: i, Text: oldLines[i-1]})
			i--
		}
	}

	// Reverse to get forward order
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}

	return result
}

// generateFullWriteDiffHTML renders an LCS-based diff with hunks and context lines.
func generateFullWriteDiffHTML(filePath string, edits []diffLine) string {
	contextLines := 3

	// Find changed regions (hunks)
	type hunk struct {
		start, end int // indices into edits
	}
	var hunks []hunk

	for i := 0; i < len(edits); i++ {
		if edits[i].Op == diffKeep {
			continue
		}
		// Start of a change region
		start := i - contextLines
		if start < 0 {
			start = 0
		}
		// Find end of contiguous changes (with context merging)
		end := i
		for end < len(edits) {
			if edits[end].Op != diffKeep {
				end++
				continue
			}
			// Count consecutive keep lines
			keepRun := 0
			for k := end; k < len(edits) && edits[k].Op == diffKeep; k++ {
				keepRun++
			}
			if keepRun <= 2*contextLines {
				// Merge with next change
				end += keepRun
				continue
			}
			break
		}
		// Extend end by context
		end += contextLines
		if end > len(edits) {
			end = len(edits)
		}
		hunks = append(hunks, hunk{start, end})
		i = end - 1
	}

	if len(hunks) == 0 {
		return ""
	}

	// Count total adds and deletes
	addCount := 0
	delCount := 0
	for _, e := range edits {
		switch e.Op {
		case diffInsert:
			addCount++
		case diffDelete:
			delCount++
		}
	}

	var b strings.Builder
	b.WriteString(`<div class="claude-diff">`)

	// Header
	b.WriteString(`<div class="claude-diff-header">`)
	b.WriteString(`<span class="claude-diff-file">`)
	b.WriteString(html.EscapeString(filePath))
	b.WriteString(`</span>`)
	b.WriteString(`<span class="claude-diff-stats">`)
	if addCount > 0 {
		fmt.Fprintf(&b, `<span class="claude-diff-stat-add">+%d</span> `, addCount)
	}
	if delCount > 0 {
		fmt.Fprintf(&b, `<span class="claude-diff-stat-del">-%d</span>`, delCount)
	}
	b.WriteString(`</span>`)
	b.WriteString(`</div>`)

	// Table
	b.WriteString(`<table class="claude-diff-table">`)

	for hi, h := range hunks {
		if hi > 0 {
			b.WriteString(`<tr class="claude-diff-ctx"><td class="claude-diff-ln"></td><td class="claude-diff-ln"></td><td class="claude-diff-code" style="opacity:0.5">...</td></tr>`)
		}
		for i := h.start; i < h.end; i++ {
			e := edits[i]
			switch e.Op {
			case diffKeep:
				fmt.Fprintf(&b, `<tr class="claude-diff-ctx"><td class="claude-diff-ln">%d</td><td class="claude-diff-ln">%d</td><td class="claude-diff-code"> %s</td></tr>`,
					e.OldLine, e.NewLine, html.EscapeString(e.Text))
			case diffDelete:
				fmt.Fprintf(&b, `<tr class="claude-diff-del"><td class="claude-diff-ln">%d</td><td class="claude-diff-ln"></td><td class="claude-diff-code">-%s</td></tr>`,
					e.OldLine, html.EscapeString(e.Text))
			case diffInsert:
				fmt.Fprintf(&b, `<tr class="claude-diff-add"><td class="claude-diff-ln"></td><td class="claude-diff-ln">%d</td><td class="claude-diff-code">+%s</td></tr>`,
					e.NewLine, html.EscapeString(e.Text))
			}
		}
	}

	b.WriteString(`</table>`)
	b.WriteString(`</div>`)

	return b.String()
}
