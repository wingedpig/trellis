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

// Turn ids are only meaningful on the thread that owns them. The fork starts
// a fresh thread, so the moved messages lose theirs; the source loses the ids
// of a turn the split cut through (here it has no thread, so all of them).
func TestForkSessionClearsForeignTurnIDs(t *testing.T) {
	u, a := agentmsg.RoleUser, agentmsg.RoleAssistant
	m := NewManager("")
	src := m.CreateSession("wt", t.TempDir(), "src")
	src.mu.Lock()
	src.messages = []Message{
		forkMsg(u, "q0", ""),
		forkMsg(a, "a1", "t1"),
		forkMsg(u, "q2", ""),
		forkMsg(a, "a3a", "t2"),
		forkMsg(a, "a3b", "t2"),
	}
	src.mu.Unlock()

	fork, err := m.ForkSession(src.ID(), 4, "fork")
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	for _, msgs := range [][]Message{src.Messages(), fork.Messages()} {
		for _, msg := range msgs {
			if msg.TurnID != "" {
				t.Errorf("message %q kept turn id %q", msg.Items[0].Text, msg.TurnID)
			}
		}
	}
}

func TestClearTurnIDsAfter(t *testing.T) {
	u, a := agentmsg.RoleUser, agentmsg.RoleAssistant
	msgs := []Message{
		forkMsg(u, "q0", ""),
		forkMsg(a, "a1", "t1"),
		forkMsg(u, "q2", ""),
		forkMsg(a, "a3a", "t2"), // the turn the split cut through
	}
	clearTurnIDsAfter(msgs, "t1")
	if msgs[1].TurnID != "t1" || msgs[3].TurnID != "" {
		t.Errorf("turn ids = %q, %q; want t1 kept and t2 cleared", msgs[1].TurnID, msgs[3].TurnID)
	}
}

func TestForkSessionPersistsLaterAppends(t *testing.T) {
	u, a := agentmsg.RoleUser, agentmsg.RoleAssistant
	for i := 0; i < 50; i++ {
		m := NewManager(t.TempDir())
		src := m.CreateSession("wt", t.TempDir(), "src")
		src.mu.Lock()
		src.messages = []Message{forkMsg(u, "q0", ""), forkMsg(a, "a1", ""), forkMsg(u, "q2", "")}
		src.persistAllMessages()
		src.mu.Unlock()

		if _, err := m.ForkSession(src.ID(), 2, "fork"); err != nil {
			t.Fatalf("ForkSession: %v", err)
		}
		next := forkMsg(u, "after", "")
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
		if len(got) != 3 || got[2].Items[0].Text != "after" {
			texts := make([]string, len(got))
			for j, g := range got {
				texts[j] = g.Items[0].Text
			}
			t.Fatalf("iteration %d: messages on disk = %v, want [q0 a1 after]", i, texts)
		}
	}
}
