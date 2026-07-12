package codex

import (
	"encoding/json"
	"testing"
)

// The app-server sends reasoning text as summary/content arrays and command
// output as aggregatedOutput — never as the text/output fields Trellis
// persists. normalizeItem must fold them over.
func TestNormalizeItem(t *testing.T) {
	t.Run("reasoning summary folds into text", func(t *testing.T) {
		it := Item{
			ID:      "rs_1",
			Type:    "reasoning",
			Summary: []string{"**Scanning configs**\n\nLooking at loader.go", "**Next**"},
		}
		normalizeItem(&it)
		want := "**Scanning configs**\n\nLooking at loader.go\n\n**Next**"
		if it.Text != want {
			t.Errorf("Text = %q, want %q", it.Text, want)
		}
		if it.Summary != nil {
			t.Errorf("Summary not cleared: %v", it.Summary)
		}
	})

	t.Run("encrypted-only reasoning stays empty", func(t *testing.T) {
		it := Item{ID: "rs_2", Type: "reasoning"}
		normalizeItem(&it)
		if it.Text != "" {
			t.Errorf("Text = %q, want empty", it.Text)
		}
	})

	t.Run("aggregatedOutput overrides output", func(t *testing.T) {
		it := Item{ID: "exec-1", Type: "commandExecution", AggregatedOutput: "full output"}
		normalizeItem(&it)
		if it.Output != "full output" || it.AggregatedOutput != "" {
			t.Errorf("Output = %q, AggregatedOutput = %q", it.Output, it.AggregatedOutput)
		}
	})

	t.Run("wire fields parse", func(t *testing.T) {
		raw := `{"id":"exec-1","type":"commandExecution","status":"completed","command":"ls","aggregatedOutput":"a.txt\n","exitCode":0}`
		var it Item
		if err := json.Unmarshal([]byte(raw), &it); err != nil {
			t.Fatal(err)
		}
		normalizeItem(&it)
		if it.Output != "a.txt\n" {
			t.Errorf("Output = %q", it.Output)
		}
	})
}

// commitTurn drops encrypted-only (textless) reasoning items so history
// doesn't fill with empty collapsed panels, but they still count as turn
// activity for the silent-failure heuristic.
func TestCommitTurnFiltersEmptyReasoning(t *testing.T) {
	s := &Session{}
	empty := Item{ID: "rs_1", Type: "reasoning"}
	normalizeItem(&empty)
	s.recordItem(empty)
	s.recordItem(Item{ID: "msg_1", Type: "agentMessage", Text: "hello"})

	if !s.commitTurn("turn-1", "") {
		t.Fatal("commitTurn = false, want true")
	}
	if len(s.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(s.messages))
	}
	items := s.messages[0].Items
	if len(items) != 1 || items[0].ID != "msg_1" {
		t.Errorf("items = %+v, want only msg_1", items)
	}

	// A turn with only encrypted reasoning: still hadItems (no false
	// "silent failure"), but no empty message is appended.
	s2 := &Session{}
	s2.recordItem(Item{ID: "rs_2", Type: "reasoning"})
	if !s2.commitTurn("turn-2", "") {
		t.Fatal("reasoning-only turn: commitTurn = false, want true")
	}
	if len(s2.messages) != 0 {
		t.Errorf("reasoning-only turn appended %d messages, want 0", len(s2.messages))
	}
}

// Streamed reasoning deltas accumulate on the item, with paragraph breaks
// between summary sections (summaryPartAdded).
func TestReasoningDeltaAccumulation(t *testing.T) {
	s := &Session{}
	if s.appendReasoningBreak("rs_1") {
		t.Error("break before any text should be false")
	}
	s.appendItemText("rs_1", "reasoning", "**First**")
	if !s.appendReasoningBreak("rs_1") {
		t.Error("break after text should be true")
	}
	s.appendItemText("rs_1", "reasoning", "**Second**")

	it, ok := s.currentItems["rs_1"]
	if !ok {
		t.Fatal("item not recorded")
	}
	if want := "**First**\n\n**Second**"; it.Text != want {
		t.Errorf("Text = %q, want %q", it.Text, want)
	}
	if it.Type != "reasoning" {
		t.Errorf("Type = %q, want reasoning", it.Type)
	}
}
