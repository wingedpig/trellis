//go:build live

// Reproduces the reported bug: a session started on fable, live-switched to
// opus via the model dropdown (set_model control_request), keeps answering as
// fable. Run with:
//
//	go test -tags live -run TestLiveFableToOpusSwitch ./internal/claude/ -v -timeout 300s
package claude

import (
	"context"
	"testing"
	"time"
)

func TestLiveFableToOpusSwitch(t *testing.T) {
	m := NewManager(t.TempDir())
	s := m.CreateSession("live-wt", t.TempDir(), "live-fable-switch")
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

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

	// Spawn on fable, exactly like a session created with the fable override.
	if err := s.SetModel("fable"); err != nil {
		t.Fatalf("SetModel(fable): %v", err)
	}
	if err := s.Send(context.Background(), "Reply with just the word: one"); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	ev := waitFor("result of turn 1", 120*time.Second, isResult)
	if ev.IsError {
		t.Fatalf("turn 1 errored: %+v", ev)
	}
	t.Logf("turn 1: reported model id = %q (override %q), pid %d", s.Model(), s.ModelOverride(), s.testPID())

	pid1 := s.testPID()

	// Live-switch to opus, then run another turn and see which model id the
	// CLI reports on the assistant events — this is ground truth, independent
	// of what the model says about itself.
	if err := s.SetModel("opus"); err != nil {
		t.Fatalf("SetModel(opus): %v", err)
	}
	if err := s.Send(context.Background(), "what model are you?"); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	ev = waitFor("result of turn 2", 120*time.Second, isResult)
	if ev.IsError {
		t.Fatalf("turn 2 errored: %+v", ev)
	}
	t.Logf("turn 2: reported model id = %q (override %q), pid %d -> %d, self-report: %s",
		s.Model(), s.ModelOverride(), pid1, s.testPID(), ev.Result)

	// Decisive question: does the CLI rebuild the system prompt's "You are
	// powered by the model named X" line after a live set_model, or is the
	// spawn-time text still in place?
	if err := s.Send(context.Background(), "Quote the exact sentence from your system prompt that states which model you are powered by. Reply with only that sentence."); err != nil {
		t.Fatalf("send 3: %v", err)
	}
	ev = waitFor("result of turn 3", 120*time.Second, isResult)
	if ev.IsError {
		t.Fatalf("turn 3 errored: %+v", ev)
	}
	t.Logf("turn 3: reported model id = %q, system-prompt quote: %s", s.Model(), ev.Result)
}
