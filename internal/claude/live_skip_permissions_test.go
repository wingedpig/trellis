//go:build live

// End-to-end verification of the auto-approve (skip permissions) toggle:
//
//  1. With the toggle ON, commands run without any can_use_tool prompt.
//  2. Still with the toggle ON, ssh remains impossible — the user-level
//     settings.json deny rules / PreToolUse hook fire even in
//     bypassPermissions mode.
//  3. Toggling OFF is a live switch (same pid) and prompts come back.
//
// Run with:
//
//	go test -tags live -run TestLiveSkipPermissions ./internal/claude/ -v -timeout 300s
package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveSkipPermissions(t *testing.T) {
	m := NewManager(t.TempDir())
	work := t.TempDir()
	s := m.CreateSession("live-wt", work, "live-skip-perms")
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

	// Track every can_use_tool permission prompt that arrives.
	var promptCount int
	waitFor := func(what string, timeout time.Duration, pred func(StreamEvent) bool) *StreamEvent {
		deadline := time.After(timeout)
		for {
			select {
			case ev := <-ch:
				if ev.Type == "control_request" {
					promptCount++
				}
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

	if err := s.SetModel("haiku"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if err := s.SetSkipPermissions(true); err != nil {
		t.Fatalf("SetSkipPermissions(true): %v", err)
	}

	// 1. Auto-approve on: the command must run with zero prompts.
	marker := filepath.Join(work, "skip-perms-ok.txt")
	if err := s.Send(context.Background(), "Run this exact bash command: touch "+marker+" — then reply done."); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	ev := waitFor("result of turn 1", 120*time.Second, isResult)
	if ev.IsError {
		t.Fatalf("turn 1 errored: %+v", ev)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker file not created — command did not run: %v", err)
	}
	if promptCount != 0 {
		t.Fatalf("got %d permission prompts with auto-approve on, want 0", promptCount)
	}
	pid1 := s.testPID()
	t.Logf("turn 1 ok: command ran, no prompts, pid %d", pid1)

	// 2. Still bypassing: ssh must be blocked by the settings.json guardrails.
	if err := s.Send(context.Background(), "Run this exact bash command and paste its full output: ssh -V"); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	ev = waitFor("result of turn 2", 120*time.Second, isResult)
	if ev.IsError {
		t.Fatalf("turn 2 errored: %+v", ev)
	}
	if strings.Contains(ev.Result, "OpenSSH") {
		t.Fatalf("ssh executed under bypass! result: %s", ev.Result)
	}
	if promptCount != 0 {
		t.Fatalf("ssh raised a prompt instead of a silent deny (%d prompts)", promptCount)
	}
	t.Logf("turn 2 ok: ssh blocked while bypassing. reply: %.120s", ev.Result)

	// 3. Toggle off live: same process, and prompts come back.
	if err := s.SetSkipPermissions(false); err != nil {
		t.Fatalf("SetSkipPermissions(false): %v", err)
	}
	if pid := s.testPID(); pid != pid1 {
		t.Fatalf("toggle off restarted the process: %d -> %d", pid1, pid)
	}
	marker2 := filepath.Join(work, "should-need-prompt.txt")
	if err := s.Send(context.Background(), "Run this exact bash command: touch "+marker2+" — then reply done."); err != nil {
		t.Fatalf("send 3: %v", err)
	}
	waitFor("permission prompt after toggle off", 120*time.Second, func(ev StreamEvent) bool {
		return ev.Type == "control_request"
	})
	if _, err := os.Stat(marker2); err == nil {
		t.Fatal("command ran without approval after toggling auto-approve off")
	}
	t.Logf("turn 3 ok: prompt returned after live toggle off, same pid %d", pid1)

	s.Cancel()
	m.Shutdown()
}
