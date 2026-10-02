package codex

import (
	"testing"

	"github.com/wingedpig/trellis/internal/agentmsg"
)

func forkMsg(role, text, turn string) Message {
	return Message{Role: role, Items: []Item{{Type: "text", Text: text}}, TurnID: turn}
}

func TestLastKeptTurnID(t *testing.T) {
	u, a := agentmsg.RoleUser, agentmsg.RoleAssistant
	msgs := []Message{
		forkMsg(u, "q0", ""),
		forkMsg(a, "a1", "t1"),
		forkMsg(u, "q2", ""),
		forkMsg(a, "a3a", "t2"), // a turn split by an approval prompt
		forkMsg(a, "a3b", "t2"),
	}
	cases := []struct {
		split int
		want  string
	}{
		{split: 1, want: ""},   // only the first prompt kept
		{split: 2, want: "t1"}, // fork on a user message
		{split: 3, want: "t1"}, // fork on t2's first answer
		{split: 4, want: "t1"}, // fork mid-t2: t2 is cut, so not kept
	}
	for _, tc := range cases {
		if got := lastKeptTurnID(msgs[:tc.split], msgs[tc.split:]); got != tc.want {
			t.Errorf("split %d: got %q, want %q", tc.split, got, tc.want)
		}
	}
}

func TestForkSessionSplits(t *testing.T) {
	u, a := agentmsg.RoleUser, agentmsg.RoleAssistant
	m := NewManager("")
	src := m.CreateSession("wt", t.TempDir(), "src")
	src.mu.Lock()
	src.messages = []Message{
		forkMsg(u, "q0", ""),
		forkMsg(a, "a1", "t1"),
		forkMsg(u, "q2", ""),
		forkMsg(a, "a3", "t2"),
	}
	src.mu.Unlock()

	for _, idx := range []int{0, 4, -1} {
		if _, err := m.ForkSession(src.ID(), idx, "fork"); err == nil {
			t.Errorf("fork at %d: expected error", idx)
		}
	}

	fork, err := m.ForkSession(src.ID(), 1, "fork")
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	texts := func(msgs []Message) []string {
		out := make([]string, len(msgs))
		for i, m := range msgs {
			out[i] = m.Items[0].Text
		}
		return out
	}
	if got := texts(src.Messages()); len(got) != 1 || got[0] != "q0" {
		t.Errorf("source messages = %v, want [q0]", got)
	}
	if got := texts(fork.Messages()); len(got) != 3 || got[0] != "a1" || got[2] != "a3" {
		t.Errorf("fork messages = %v, want [a1 q2 a3]", got)
	}
	fork.mu.Lock()
	tid := fork.threadID
	fork.mu.Unlock()
	if tid != "" {
		t.Errorf("fork threadID = %q, want a fresh thread", tid)
	}
}
