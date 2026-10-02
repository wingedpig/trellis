// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func forkTestMessages() []Message {
	text := func(role, t string) Message {
		return Message{Role: role, Content: []ContentBlock{{Type: "text", Text: t}}}
	}
	return []Message{
		text("user", "q0"),      // 0
		text("assistant", "a1"), // 1
		text("user", "q2"),      // 2
		text("assistant", "a3"), // 3
		text("user", "q4"),      // 4
		text("assistant", "a5"), // 5
	}
}

func messageTexts(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content[0].Text
	}
	return out
}

// cliTranscriptTexts reads back the text of every line in a CLI JSONL file.
func cliTranscriptTexts(t *testing.T, workDir, sid string) []string {
	t.Helper()
	projDir, err := CLIProjectDir(workDir)
	if err != nil {
		t.Fatalf("CLIProjectDir: %v", err)
	}
	f, err := os.Open(filepath.Join(projDir, sid+".jsonl"))
	if err != nil {
		t.Fatalf("open CLI transcript: %v", err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var line struct {
			Message struct {
				Content []ContentBlock `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("parse CLI line: %v", err)
		}
		out = append(out, line.Message.Content[0].Text)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read CLI transcript: %v", err)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestForkSessionSplits(t *testing.T) {
	cases := []struct {
		name     string
		index    int
		wantHead []string
		wantTail []string
		wantCLI  []string // new session's CLI transcript
	}{
		{
			name:     "user message starts the new session",
			index:    2,
			wantHead: []string{"q0", "a1"},
			wantTail: []string{"q2", "a3", "q4", "a5"},
			wantCLI:  []string{"q2", "a3", "q4", "a5"},
		},
		{
			name:     "assistant message starts the new session",
			index:    3,
			wantHead: []string{"q0", "a1", "q2"},
			wantTail: []string{"a3", "q4", "a5"},
			wantCLI:  []string{"(This conversation was split off from an earlier session.)", "a3", "q4", "a5"},
		},
		{
			name:     "last message",
			index:    5,
			wantHead: []string{"q0", "a1", "q2", "a3", "q4"},
			wantTail: []string{"a5"},
			wantCLI:  []string{"(This conversation was split off from an earlier session.)", "a5"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			work := t.TempDir()
			m := NewManager("")
			src := m.CreateSession("wt", work, "src")
			src.mu.Lock()
			src.messages = forkTestMessages()
			src.claudeSID = "old-sid"
			src.mu.Unlock()

			fork, err := m.ForkSession(src.ID(), tc.index, "fork")
			if err != nil {
				t.Fatalf("ForkSession: %v", err)
			}
			if got := messageTexts(src.Messages()); !equalStrings(got, tc.wantHead) {
				t.Errorf("source messages = %v, want %v", got, tc.wantHead)
			}
			if got := messageTexts(fork.Messages()); !equalStrings(got, tc.wantTail) {
				t.Errorf("fork messages = %v, want %v", got, tc.wantTail)
			}

			src.mu.Lock()
			srcSID := src.claudeSID
			src.mu.Unlock()
			if srcSID == "old-sid" {
				t.Fatalf("source still resumes the old CLI session")
			}
			if got := cliTranscriptTexts(t, work, srcSID); !equalStrings(got, tc.wantHead) {
				t.Errorf("source CLI transcript = %v, want %v", got, tc.wantHead)
			}
			fork.mu.Lock()
			forkSID := fork.claudeSID
			fork.mu.Unlock()
			if got := cliTranscriptTexts(t, work, forkSID); !equalStrings(got, tc.wantCLI) {
				t.Errorf("fork CLI transcript = %v, want %v", got, tc.wantCLI)
			}
		})
	}
}

func TestForkSessionRejects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	work := t.TempDir()
	m := NewManager("")
	src := m.CreateSession("wt", work, "src")
	src.mu.Lock()
	src.messages = forkTestMessages()
	src.mu.Unlock()

	for _, idx := range []int{0, 6, -1} {
		if _, err := m.ForkSession(src.ID(), idx, "fork"); err == nil {
			t.Errorf("fork at %d: expected error", idx)
		}
	}

	src.mu.Lock()
	src.generating = true
	src.mu.Unlock()
	if _, err := m.ForkSession(src.ID(), 2, "fork"); err == nil {
		t.Errorf("fork while generating: expected error")
	}
	if got := len(src.Messages()); got != 6 {
		t.Errorf("rejected forks changed the source: %d messages", got)
	}
}

func TestCLILeadInForAssistantStart(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "a"}}},
	}
	got := cliLeadIn(msgs)
	if len(got) != 2 || got[0].Role != "user" || got[1].Role != "assistant" {
		t.Fatalf("cliLeadIn did not prepend a user turn: %+v", got)
	}
}

// A fork that opens with an assistant message keeps its placeholder user turn
// only in the CLI transcript. Splitting it again must give the source side a
// transcript that still opens with a user turn.
func TestForkSessionResplitKeepsLeadIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	work := t.TempDir()
	m := NewManager("")
	src := m.CreateSession("wt", work, "src")
	src.mu.Lock()
	src.messages = forkTestMessages()
	src.mu.Unlock()

	first, err := m.ForkSession(src.ID(), 3, "first") // first = [a3 q4 a5]
	if err != nil {
		t.Fatalf("first fork: %v", err)
	}
	if _, err := m.ForkSession(first.ID(), 1, "second"); err != nil { // first keeps [a3]
		t.Fatalf("second fork: %v", err)
	}
	first.mu.Lock()
	sid := first.claudeSID
	first.mu.Unlock()
	want := []string{"(This conversation was split off from an earlier session.)", "a3"}
	if got := cliTranscriptTexts(t, work, sid); !equalStrings(got, want) {
		t.Errorf("re-split source CLI transcript = %v, want %v", got, want)
	}
}

// The source's messages file is rewritten on fork; a message appended right
// after must survive on disk rather than being clobbered by the rewrite.
func TestForkSessionPersistsLaterAppends(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for i := 0; i < 50; i++ {
		work := t.TempDir()
		m := NewManager(t.TempDir())
		src := m.CreateSession("wt", work, "src")
		src.mu.Lock()
		src.messages = forkTestMessages()
		src.persistAllMessages()
		src.mu.Unlock()

		if _, err := m.ForkSession(src.ID(), 2, "fork"); err != nil {
			t.Fatalf("ForkSession: %v", err)
		}
		next := Message{Role: "user", Content: []ContentBlock{{Type: "text", Text: "after"}}}
		src.mu.Lock()
		src.messages = append(src.messages, next)
		src.persistMessage(next)
		file := src.messagesFile
		src.mu.Unlock()
		m.Shutdown()

		got, err := loadMessages(file)
		if err != nil {
			t.Fatalf("loadMessages: %v", err)
		}
		want := []string{"q0", "a1", "after"}
		if texts := messageTexts(got); !equalStrings(texts, want) {
			t.Fatalf("iteration %d: messages on disk = %v, want %v", i, texts, want)
		}
	}
}
