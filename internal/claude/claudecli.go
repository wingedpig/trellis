// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CLIJSONLLine represents a single line in Claude CLI's session JSONL file.
type CLIJSONLLine struct {
	Type           string          `json:"type"`
	SessionID      string          `json:"sessionId"`
	UUID           string          `json:"uuid"`
	ParentUUID     string          `json:"parentUuid,omitempty"`
	Message        json.RawMessage `json:"message"`
	CWD            string          `json:"cwd"`
	GitBranch      string          `json:"gitBranch,omitempty"`
	Version        string          `json:"version"`
	Timestamp      string          `json:"timestamp"`
	IsSidechain    bool            `json:"isSidechain"`
	UserType       string          `json:"userType"`
	PermissionMode string          `json:"permissionMode,omitempty"`
}

// CLISessionsIndex represents the sessions-index.json file used by Claude CLI.
type CLISessionsIndex struct {
	Version int                    `json:"version"`
	Entries []CLISessionsIndexEntry `json:"entries"`
}

// CLISessionsIndexEntry is one entry in the sessions index.
type CLISessionsIndexEntry struct {
	SessionID   string `json:"sessionId"`
	FullPath    string `json:"fullPath"`
	FileMtime   int64  `json:"fileMtime"`
	FirstPrompt string `json:"firstPrompt,omitempty"`
	Summary     string `json:"summary,omitempty"`
	MessageCount int   `json:"messageCount"`
	Created     string `json:"created"`
	Modified    string `json:"modified"`
	GitBranch   string `json:"gitBranch,omitempty"`
	ProjectPath string `json:"projectPath,omitempty"`
	IsSidechain bool   `json:"isSidechain"`
}

// CLIProjectDir returns the path to Claude CLI's project-specific storage directory
// for the given project path. The CLI encodes the project path as the directory name
// under ~/.claude/projects/.
func CLIProjectDir(projectPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}

	// Claude CLI encodes the project path by replacing / and . with -
	// e.g., /Users/alice/src/myapp -> -Users-alice-src-myapp
	//       /Users/alice/src/groups.io -> -Users-alice-src-groups-io
	encoded := strings.NewReplacer("/", "-", ".", "-").Replace(projectPath)
	return filepath.Join(home, ".claude", "projects", encoded), nil
}

// WriteCLISession writes a transcript's messages as a Claude CLI JSONL session file
// and updates the sessions-index.json. Returns the new Claude CLI session ID.
func WriteCLISession(projectPath, workDir, gitBranch string, messages []Message) (string, error) {
	sessionID, err := WriteCLISessionFile(projectPath, workDir, gitBranch, messages)
	if err != nil {
		return "", err
	}

	// Update the sessions index
	projDir, _ := CLIProjectDir(projectPath)
	jsonlPath := filepath.Join(projDir, sessionID+".jsonl")
	if err := updateSessionsIndex(projDir, sessionID, jsonlPath, projectPath, gitBranch, messages); err != nil {
		// Non-fatal: the JSONL file is written, resume should still work
		fmt.Fprintf(os.Stderr, "claude: warning: failed to update sessions-index.json: %v\n", err)
	}

	return sessionID, nil
}

// WriteCLISessionFile writes a JSONL session file without updating the sessions index.
// Use this for rebuilding stale sessions where an index entry already exists.
func WriteCLISessionFile(projectPath, workDir, gitBranch string, messages []Message) (string, error) {
	projDir, err := CLIProjectDir(projectPath)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(projDir, 0755); err != nil {
		return "", fmt.Errorf("create project dir: %w", err)
	}

	cliSessionID := uuid.New().String()
	jsonlPath := filepath.Join(projDir, cliSessionID+".jsonl")

	// Convert messages to JSONL lines
	lines, err := messagesToJSONL(cliSessionID, workDir, gitBranch, messages)
	if err != nil {
		return "", fmt.Errorf("convert messages to JSONL: %w", err)
	}

	// Write the JSONL file
	f, err := os.Create(jsonlPath)
	if err != nil {
		return "", fmt.Errorf("create JSONL file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, line := range lines {
		if err := enc.Encode(line); err != nil {
			return "", fmt.Errorf("write JSONL line: %w", err)
		}
	}

	return cliSessionID, nil
}

// messagesToJSONL converts Trellis messages to Claude CLI JSONL lines.
func messagesToJSONL(sessionID, workDir, gitBranch string, messages []Message) ([]CLIJSONLLine, error) {
	lines := make([]CLIJSONLLine, 0, len(messages))
	var prevUUID string

	for _, msg := range messages {
		lineUUID := uuid.New().String()

		// Build the Messages API message object
		apiMsg := struct {
			Role    string         `json:"role"`
			Content []ContentBlock `json:"content"`
		}{
			Role:    msg.Role,
			Content: msg.Content,
		}
		msgJSON, err := json.Marshal(apiMsg)
		if err != nil {
			return nil, fmt.Errorf("marshal message: %w", err)
		}

		lineType := msg.Role // "user" or "assistant"

		line := CLIJSONLLine{
			Type:        lineType,
			SessionID:   sessionID,
			UUID:        lineUUID,
			ParentUUID:  prevUUID,
			Message:     json.RawMessage(msgJSON),
			CWD:         workDir,
			GitBranch:   gitBranch,
			Version:     "2.1.37",
			Timestamp:   msg.Timestamp.UTC().Format(time.RFC3339Nano),
			IsSidechain: false,
			UserType:    "external",
		}
		lines = append(lines, line)
		prevUUID = lineUUID
	}
	return lines, nil
}

// updateSessionsIndex reads, updates, and writes the sessions-index.json file.
func updateSessionsIndex(projDir, sessionID, jsonlPath, projectPath, gitBranch string, messages []Message) error {
	indexPath := filepath.Join(projDir, "sessions-index.json")

	// Read existing index
	var index CLISessionsIndex
	data, err := os.ReadFile(indexPath)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &index); err != nil {
			// If we can't parse it, start fresh rather than corrupting
			index = CLISessionsIndex{Version: 1}
		}
	} else {
		index = CLISessionsIndex{Version: 1}
	}

	// Derive firstPrompt and summary from the messages
	var firstPrompt string
	for _, msg := range messages {
		if msg.Role == "user" {
			for _, block := range msg.Content {
				if block.Type == "text" && block.Text != "" {
					firstPrompt = block.Text
					if len(firstPrompt) > 200 {
						firstPrompt = firstPrompt[:200]
					}
					break
				}
			}
			if firstPrompt != "" {
				break
			}
		}
	}

	now := time.Now()
	var created time.Time
	if len(messages) > 0 {
		created = messages[0].Timestamp
	} else {
		created = now
	}

	// Get the JSONL file's mtime
	fi, err := os.Stat(jsonlPath)
	var fileMtime int64
	if err == nil {
		fileMtime = fi.ModTime().UnixMilli()
	} else {
		fileMtime = now.UnixMilli()
	}

	entry := CLISessionsIndexEntry{
		SessionID:    sessionID,
		FullPath:     jsonlPath,
		FileMtime:    fileMtime,
		FirstPrompt:  firstPrompt,
		Summary:      firstPrompt,
		MessageCount: len(messages),
		Created:      created.UTC().Format(time.RFC3339Nano),
		Modified:     now.UTC().Format(time.RFC3339Nano),
		GitBranch:    gitBranch,
		ProjectPath:  projectPath,
		IsSidechain:  false,
	}

	index.Entries = append(index.Entries, entry)

	// Write atomically
	outData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sessions index: %w", err)
	}

	tmpPath := indexPath + ".tmp"
	if err := os.WriteFile(tmpPath, outData, 0644); err != nil {
		return fmt.Errorf("write temp sessions index: %w", err)
	}
	if err := os.Rename(tmpPath, indexPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename sessions index: %w", err)
	}

	return nil
}
