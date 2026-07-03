//go:build live

// Live end-to-end verification against the real claude CLI. Costs real API
// tokens; run explicitly with:
//
//	go test -tags live -run TestLive ./internal/claude/ -v -timeout 300s
package claude

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func (s *Session) testPID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		return s.cmd.Process.Pid
	}
	return 0
}

func TestLiveInterruptAndSetModel(t *testing.T) {
	m := NewManager(t.TempDir())
	s := m.CreateSession("live-wt", t.TempDir(), "live-test")
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

	// waitFor drains ch until pred matches or the timeout elapses.
	waitFor := func(what string, timeout time.Duration, pred func(StreamEvent) bool) *StreamEvent {
		deadline := time.After(timeout)
		for {
			select {
			case ev := <-ch:
				if pred(ev) {
					return &ev
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
				return nil
			}
		}
	}
	isResult := func(ev StreamEvent) bool { return ev.Type == "result" }

	// 1. The pair-dispatch bug: spawn the process through a short-lived ctx
	// that is canceled the moment Send returns — exactly what
	// pair/checklist dispatch does. The process must survive and the turn
	// must complete.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := s.Send(ctx, "Reply with just the word: one"); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	cancel()
	ev := waitFor("result of turn 1", 90*time.Second, isResult)
	if ev.IsError {
		t.Fatalf("turn 1 errored: %+v", ev)
	}
	pid1 := s.testPID()
	if pid1 == 0 {
		t.Fatal("process not alive after caller ctx canceled")
	}
	t.Logf("turn 1 ok on pid %d (spawn ctx canceled immediately)", pid1)

	// 2. Mid-turn interrupt: process must survive, turn must end via a
	// result event, and the session must not be flagged as errored.
	if err := s.Send(context.Background(), "Count from 1 to 2000, one number per line, plain text, no tools, no commentary."); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	waitFor("streaming to start", 90*time.Second, func(ev StreamEvent) bool { return ev.Type == "stream_event" })
	if !s.Interrupt() {
		t.Fatal("Interrupt returned false with a turn in flight")
	}
	ev = waitFor("result of interrupted turn", 30*time.Second, isResult)
	t.Logf("interrupted turn result: subtype=%q is_error=%v", ev.Subtype, ev.IsError)
	if s.IsGenerating() {
		t.Fatal("still generating after interrupt result")
	}
	if s.Reason() == "error" {
		t.Fatal("interrupted turn surfaced as error state")
	}
	if pid := s.testPID(); pid != pid1 {
		t.Fatalf("process changed after interrupt: %d -> %d", pid1, pid)
	}
	t.Logf("interrupt ok, process %d still alive", pid1)

	// 3. Live model switch: same process, next turn runs on the new model.
	if err := s.SetModel("haiku"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if err := s.Send(context.Background(), "Reply with just the word: two"); err != nil {
		t.Fatalf("send 3: %v", err)
	}
	ev = waitFor("result of turn 3", 90*time.Second, isResult)
	if ev.IsError {
		t.Fatalf("turn 3 errored: %+v", ev)
	}
	if pid := s.testPID(); pid != pid1 {
		t.Fatalf("process restarted by SetModel: %d -> %d", pid1, pid)
	}
	if model := s.Model(); !strings.Contains(model, "haiku") {
		t.Fatalf("model after live switch = %q, want haiku", model)
	}
	t.Logf("live set_model ok: now on %s, same pid %d", s.Model(), pid1)

	m.Shutdown()
}

// TestLiveBackgroundAgentPermissions replays the incident where background
// agents blocked forever on permission prompts: concurrent prompts must all
// stay pending (not overwrite each other), must survive the main turn's
// result event, and answering each must unblock its agent.
func TestLiveBackgroundAgentPermissions(t *testing.T) {
	m := NewManager(t.TempDir())
	workDir := t.TempDir()
	outDir := t.TempDir() // outside workDir → Bash writes here need permission
	s := m.CreateSession("live-wt", workDir, "live-perm-test")
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

	prompt := "Spawn TWO subagents via the Agent/Task tool with run_in_background=true, both in one message. " +
		"Agent A must run exactly this bash command: echo A > " + outDir + "/a.txt -- and report done. " +
		"Agent B must run exactly this bash command: echo B > " + outDir + "/b.txt -- and report done. " +
		"Do not wait for them; end your turn immediately after spawning both."
	if err := s.Send(context.Background(), prompt); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Collect until we have both permission prompts and the main turn's end,
	// in whatever order they arrive.
	var reqs []StreamEvent
	gotResult := false
	deadline := time.After(180 * time.Second)
	for len(reqs) < 2 || !gotResult {
		select {
		case ev := <-ch:
			switch ev.Type {
			case "control_request":
				reqs = append(reqs, ev)
			case "result":
				gotResult = true
			}
		case <-deadline:
			t.Fatalf("timed out: %d control_requests, result=%v", len(reqs), gotResult)
		}
	}
	if n := len(s.PendingControlRequests()); n != 2 {
		t.Fatalf("pending prompts after result = %d, want 2 (prompts must survive turn end)", n)
	}
	t.Logf("2 prompts pending after main turn ended — incident scenario reproduced")

	// Answer both the way the WS handler does.
	for _, req := range reqs {
		var inner struct {
			Input json.RawMessage `json:"input"`
		}
		_ = json.Unmarshal(req.Request, &inner)
		resp := map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": req.RequestID,
				"response":   map[string]any{"behavior": "allow", "updatedInput": inner.Input},
			},
		}
		data, _ := json.Marshal(resp)
		s.ClearPendingControlRequest(req.RequestID)
		if err := s.WriteStdinRaw(data); err != nil {
			t.Fatalf("permission response: %v", err)
		}
	}
	if n := len(s.PendingControlRequests()); n != 0 {
		t.Fatalf("pending prompts after answering = %d, want 0", n)
	}

	// Both agents should now complete their writes.
	fileDeadline := time.Now().Add(120 * time.Second)
	for {
		_, errA := os.Stat(outDir + "/a.txt")
		_, errB := os.Stat(outDir + "/b.txt")
		if errA == nil && errB == nil {
			break
		}
		if time.Now().After(fileDeadline) {
			t.Fatalf("agents never completed: a.txt err=%v, b.txt err=%v", errA, errB)
		}
		time.Sleep(2 * time.Second)
	}
	t.Log("both background agents completed after prompts were answered")

	m.Shutdown()
}
