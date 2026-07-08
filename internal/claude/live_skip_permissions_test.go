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

// TestLiveToggleOnRestartsAndResumes exercises turning auto-approve ON for a
// session that was started with prompts ON (a process NOT spawned bypass-capable).
// The toggle must restart the process so the respawn comes up with
// --dangerously-skip-permissions — and, critically, the restart must not lose
// the conversation even when the CLI's own transcript is absent (a young session
// it never flushed to disk). We delete the CLI session file to force that case:
// a naive --resume aborts the next turn with "No conversation found"; the fix
// rebuilds the resume target from the messages Trellis holds, so the next turn
// both runs auto-approved AND still remembers the earlier turn.
func TestLiveToggleOnRestartsAndResumes(t *testing.T) {
	m := NewManager(t.TempDir())
	work := t.TempDir()
	s := m.CreateSession("live-wt", work, "live-toggle-on")
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

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
	// Real turn results only — never the synthetic process_restarted /
	// process_exited results a kill can fan out between turns.
	isTurnResult := func(ev StreamEvent) bool {
		return ev.Type == "result" && ev.Subtype != "process_restarted" && ev.Subtype != "process_exited"
	}

	if err := s.SetModel("haiku"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}

	// Turn 1 runs with prompts ON (no flag): a pure-text reply so nothing
	// prompts, but it establishes a transcript and a claudeSID.
	if err := s.Send(context.Background(), "Remember this secret word for later: bluebird. Reply with exactly: ok"); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	ev := waitFor("result of turn 1", 120*time.Second, isTurnResult)
	if ev.IsError {
		t.Fatalf("turn 1 errored: %+v", ev)
	}
	if promptCount != 0 {
		t.Fatalf("turn 1 raised %d prompts, want 0", promptCount)
	}
	pid1 := s.testPID()
	if pid1 == 0 {
		t.Fatal("no process after turn 1")
	}
	s.mu.Lock()
	sid := s.claudeSID
	s.mu.Unlock()
	t.Logf("turn 1 ok: pid %d, claudeSID %s", pid1, sid)

	// Simulate a young session whose transcript the CLI never flushed: drop the
	// JSONL so --resume has no on-disk target. ensureResumeTarget must rebuild
	// it from Trellis's own messages on the toggle respawn.
	if sid != "" {
		projDir, err := CLIProjectDir(work)
		if err != nil {
			t.Fatalf("CLIProjectDir: %v", err)
		}
		if err := os.Remove(filepath.Join(projDir, sid+".jsonl")); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove CLI session file: %v", err)
		}
	}

	// Toggle auto-approve ON. The running process was not spawned bypass-capable,
	// so this must restart it (kill now, respawn with the flag on the next Send)
	// rather than send a set_permission_mode the CLI would reject.
	if err := s.SetSkipPermissions(true); err != nil {
		t.Fatalf("SetSkipPermissions(true): %v", err)
	}
	if pid := s.testPID(); pid != 0 {
		t.Fatalf("toggle on did not kill the prompts-on process (pid %d)", pid)
	}

	// Turn 2: the respawn must come up bypass-capable (command runs, zero
	// prompts) AND resume turn 1's history (recalls the secret word) even though
	// the CLI's transcript is gone.
	promptCount = 0
	marker := filepath.Join(work, "toggle-on-ran.txt")
	if err := s.Send(context.Background(), "Run this exact bash command: touch "+marker+" — then tell me the secret word I asked you to remember."); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	ev = waitFor("result of turn 2", 120*time.Second, isTurnResult)
	if ev.IsError {
		t.Fatalf("turn 2 errored (resume/bypass broken): %+v", ev)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker not created — auto-approve did not take effect after restart: %v", err)
	}
	if promptCount != 0 {
		t.Fatalf("turn 2 raised %d prompts with auto-approve on, want 0", promptCount)
	}
	if !strings.Contains(strings.ToLower(ev.Result), "bluebird") {
		t.Fatalf("turn 2 did not resume turn 1's history (no secret word): %s", ev.Result)
	}
	pid2 := s.testPID()
	if pid2 == 0 || pid2 == pid1 {
		t.Fatalf("expected a fresh process after the toggle restart: pid1=%d pid2=%d", pid1, pid2)
	}
	t.Logf("turn 2 ok: ran auto-approved, resumed history across restart, pid %d -> %d", pid1, pid2)

	s.Cancel()
	m.Shutdown()
}
