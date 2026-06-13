// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PlanVersion is one captured or edited version of a session's plan.
// Plans are captured automatically when Claude calls ExitPlanMode and can
// be revised by the user afterwards; every revision appends a new version.
type PlanVersion struct {
	Version   int       `json:"version"`
	Source    string    `json:"source"` // "agent" (ExitPlanMode) or "user" (manual edit)
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Plans returns a copy of the session's plan history, oldest first.
func (s *Session) Plans() []PlanVersion {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PlanVersion, len(s.plans))
	copy(out, s.plans)
	return out
}

// LatestPlan returns the most recent plan version.
func (s *Session) LatestPlan() (PlanVersion, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.plans) == 0 {
		return PlanVersion{}, false
	}
	return s.plans[len(s.plans)-1], true
}

// UpdatePlan appends a user-edited plan version and persists it.
func (s *Session) UpdatePlan(content string) PlanVersion {
	return s.appendPlan(content, "user")
}

// maybeCapturePlan captures a plan artifact when an ExitPlanMode tool_use
// block completes. The plan text comes from the block's "plan" input when
// present; otherwise from the most recent Write of a markdown file (claude
// writes the plan to a .md file before calling ExitPlanMode). Mirrors the
// plan-content heuristic in claude.js.
func (s *Session) maybeCapturePlan(block ContentBlock) {
	if block.Type != "tool_use" || block.Name != "ExitPlanMode" {
		return
	}
	content := ""
	if len(block.Input) > 0 {
		var in struct {
			Plan string `json:"plan"`
		}
		if json.Unmarshal(block.Input, &in) == nil {
			content = in.Plan
		}
	}
	if content == "" {
		content = s.findPlanFromWrites()
	}
	if content == "" {
		return
	}

	s.mu.Lock()
	identical := len(s.plans) > 0 && s.plans[len(s.plans)-1].Content == content
	s.mu.Unlock()
	if identical {
		return
	}
	s.appendPlan(content, "agent")
}

// appendPlan adds a plan version, persists the history, and notifies
// subscribers via a plan_captured stream event.
func (s *Session) appendPlan(content, source string) PlanVersion {
	s.mu.Lock()
	pv := PlanVersion{
		Version:   len(s.plans) + 1,
		Source:    source,
		Content:   content,
		CreatedAt: time.Now(),
	}
	s.plans = append(s.plans, pv)
	plansCopy := make([]PlanVersion, len(s.plans))
	copy(plansCopy, s.plans)
	file := s.plansFile
	s.mu.Unlock()

	if file != "" {
		if err := savePlans(file, plansCopy); err != nil {
			log.Printf("claude [%s]: failed to persist plan: %v", s.id, err)
		}
	}

	if payload, err := json.Marshal(pv); err == nil {
		s.fanOut(StreamEvent{Type: "plan_captured", Message: json.RawMessage(payload)})
	}
	return pv
}

// findPlanFromWrites searches backward through the in-progress blocks and
// the message history for the latest Write tool_use targeting a .md file
// and returns its content.
func (s *Session) findPlanFromWrites() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := planFromBlocks(s.currentBlocks); c != "" {
		return c
	}
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].Role != "assistant" {
			continue
		}
		if c := planFromBlocks(s.messages[i].Content); c != "" {
			return c
		}
	}
	return ""
}

func planFromBlocks(blocks []ContentBlock) string {
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		if b.Type != "tool_use" || b.Name != "Write" || len(b.Input) == 0 {
			continue
		}
		var in struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}
		if json.Unmarshal(b.Input, &in) != nil {
			continue
		}
		if in.Content != "" && strings.HasSuffix(strings.ToLower(in.FilePath), ".md") {
			return in.Content
		}
	}
	return ""
}

// loadPlans reads a session's plan history from disk.
func loadPlans(filePath string) ([]PlanVersion, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read plans file: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var plans []PlanVersion
	if err := json.Unmarshal(data, &plans); err != nil {
		return nil, fmt.Errorf("parse plans file: %w", err)
	}
	return plans, nil
}

// savePlans writes a session's plan history to disk atomically.
func savePlans(filePath string, plans []PlanVersion) error {
	data, err := json.MarshalIndent(plans, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plans: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("create plans dir: %w", err)
	}
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temp plans file: %w", err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename plans file: %w", err)
	}
	return nil
}
