package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wingedpig/trellis/internal/codex"
)

// Persistence-restart test. Creates a session, runs one turn end-to-end, then
// re-creates the manager from the same state dir to verify messages persist.
func main() {
	stateDir := filepath.Join(os.TempDir(), "codex-restart-test")
	os.RemoveAll(stateDir)
	defer os.RemoveAll(stateDir)

	fmt.Println("=== run 1: create session, send turn ===")
	mgr1 := codex.NewManager(stateDir)
	sess := mgr1.CreateSession("test-wt", "/tmp", "smoke")
	sessID := sess.ID()
	fmt.Printf("session id=%s\n", sessID)

	sub := sess.Subscribe()
	go func() {
		for ev := range sub {
			if ev.Type == "turn_completed" {
				fmt.Println("[turn complete]")
			}
		}
	}()

	ctx := context.Background()
	if err := sess.EnsureProcess(ctx); err != nil {
		fmt.Println("EnsureProcess:", err)
		os.Exit(1)
	}
	if err := sess.Send(ctx, "Reply with just the word: pong"); err != nil {
		fmt.Println("Send:", err)
		os.Exit(1)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if !sess.IsGenerating() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Give async persistence a beat to flush
	time.Sleep(500 * time.Millisecond)

	msgs := sess.Messages()
	fmt.Printf("after run 1: %d messages\n", len(msgs))

	mgr1.Shutdown()

	// Inspect disk
	jsonl := filepath.Join(stateDir, "messages", sessID+".jsonl")
	st, err := os.Stat(jsonl)
	if err != nil {
		fmt.Printf("DISK: %s NOT FOUND (err=%v)\n", jsonl, err)
	} else {
		fmt.Printf("DISK: %s size=%d\n", jsonl, st.Size())
		data, _ := os.ReadFile(jsonl)
		fmt.Printf("--- FILE CONTENTS ---\n%s--- END ---\n", string(data))
	}

	fmt.Println("=== run 2: re-create manager from state dir ===")
	mgr2 := codex.NewManager(stateDir)
	defer mgr2.Shutdown()

	sess2 := mgr2.GetSession(sessID)
	if sess2 == nil {
		fmt.Println("BUG: session not loaded!")
		os.Exit(1)
	}
	msgs2 := sess2.Messages()
	fmt.Printf("after run 2: session id=%s, %d messages\n", sess2.ID(), len(msgs2))
	for i, m := range msgs2 {
		text := ""
		for _, it := range m.Items {
			text += it.Type + ":" + truncate(it.Text, 40) + " "
		}
		fmt.Printf("  [%d] role=%s items=%d text=%q\n", i, m.Role, len(m.Items), text)
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
