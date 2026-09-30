// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wingedpig/trellis/pkg/client"
)

// taskTimeout is the default HTTP timeout for task commands: start runs
// on_create hooks and waits for the agent to accept the prompt, and finish
// runs pre_merge/post_merge/pre_remove hooks (test suites, rebuilds).
const taskTimeout = 30 * time.Minute

func cmdTask(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl task <start|finish> [args]")
	}
	switch args[0] {
	case "start":
		return cmdTaskStart(args[1:])
	case "finish":
		return cmdTaskFinish(args[1:])
	default:
		return fmt.Errorf("unknown task subcommand: %s", args[0])
	}
}

// parseTaskFlags splits args into positionals and -flag values. valueFlags
// take an argument (as "-f v" or "-f=v"); boolFlags don't.
func parseTaskFlags(args []string, valueFlags, boolFlags []string) (pos []string, vals map[string]string, bools map[string]bool, err error) {
	vals = map[string]string{}
	bools = map[string]bool{}
	has := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		value, hasValue := "", false
		if j := strings.IndexByte(name, '='); j >= 0 {
			name, value, hasValue = name[:j], name[j+1:], true
		}
		switch {
		case has(valueFlags, name):
			if !hasValue {
				if i+1 >= len(args) {
					return nil, nil, nil, fmt.Errorf("flag -%s needs a value", name)
				}
				i++
				value = args[i]
			}
			vals[name] = value
		case has(boolFlags, name) && !hasValue:
			bools[name] = true
		default:
			return nil, nil, nil, fmt.Errorf("unknown flag: %s", a)
		}
	}
	return pos, vals, bools, nil
}

func cmdTaskStart(args []string) error {
	const usage = "usage: trellis-ctl task start <branch> [-agent claude|codex] [-prompt <text> | -prompt-file <path|->] [-model <m>] [-effort <e>] [-name <session name>] [-activate]"
	pos, vals, bools, err := parseTaskFlags(args,
		[]string{"agent", "prompt", "prompt-file", "model", "effort", "name"},
		[]string{"activate"})
	if err != nil {
		return fmt.Errorf("%v\n%s", err, usage)
	}
	if len(pos) != 1 {
		return fmt.Errorf(usage)
	}

	prompt := vals["prompt"]
	if path, ok := vals["prompt-file"]; ok {
		if prompt != "" {
			return fmt.Errorf("use -prompt or -prompt-file, not both")
		}
		var data []byte
		if path == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(path)
		}
		if err != nil {
			return fmt.Errorf("read prompt: %w", err)
		}
		prompt = string(data)
	}

	req := client.TaskStartRequest{
		Branch:      pos[0],
		Agent:       vals["agent"],
		Prompt:      prompt,
		Model:       vals["model"],
		Effort:      vals["effort"],
		SessionName: vals["name"],
		Activate:    bools["activate"],
	}
	if req.Agent == "" {
		req.Agent = "claude"
	}
	if !jsonOutput {
		fmt.Printf("Starting task %s with %s...\n", req.Branch, req.Agent)
	}
	res, err := apiClient.Tasks.Start(context.Background(), req)
	if err != nil {
		return err
	}
	if jsonOutput {
		printJSON(res)
		return nil
	}

	fmt.Printf("Worktree: %s (%s)\n", res.Worktree.Name(), res.Worktree.Path)
	fmt.Printf("Session:  %s %s\n", res.Agent, res.SessionID)
	fmt.Printf("Open:     %s%s\n", apiURL, res.SessionURL)
	switch {
	case res.PromptSent:
		fmt.Println("Prompt sent.")
	case prompt == "":
		fmt.Println("No prompt given; the session is waiting for input.")
	}
	for _, w := range res.Warnings {
		fmt.Printf("Warning: %s\n", w)
	}
	return nil
}

func cmdTaskFinish(args []string) error {
	const usage = "usage: trellis-ctl task finish [worktree] [-strategy ff|merge|squash] [-m <message>] [-keep-branch] [-keep-worktree]"
	pos, vals, bools, err := parseTaskFlags(args,
		[]string{"strategy", "m", "message"},
		[]string{"keep-branch", "keep-worktree"})
	if err != nil {
		return fmt.Errorf("%v\n%s", err, usage)
	}
	if len(pos) > 1 {
		return fmt.Errorf(usage)
	}

	ctx := context.Background()
	name := ""
	if len(pos) == 1 {
		name = pos[0]
	} else if name, err = currentWorktree(ctx); err != nil {
		return fmt.Errorf("%v; pass the worktree name\n%s", err, usage)
	}

	msg := vals["m"]
	if msg == "" {
		msg = vals["message"]
	}
	req := client.TaskFinishRequest{
		Worktree:     name,
		Strategy:     vals["strategy"],
		Message:      msg,
		KeepBranch:   bools["keep-branch"],
		KeepWorktree: bools["keep-worktree"],
	}
	if !jsonOutput {
		fmt.Printf("Finishing %s...\n", name)
	}
	res, err := apiClient.Tasks.Finish(ctx, req)
	if err != nil {
		var apiErr *client.APIError
		if !jsonOutput && errors.As(err, &apiErr) {
			printFinishErrorDetails(apiErr.Details)
		}
		return err
	}
	if jsonOutput {
		printJSON(res)
		return nil
	}

	m := res.Merge
	printHookResults("pre_merge", m.PreMergeHooks)
	if m.Merged {
		head := m.Head
		if len(head) > 12 {
			head = head[:12]
		}
		fmt.Printf("Merged %s into %s (%s, %d commit%s) → %s\n", m.Branch, m.Base, m.Strategy, m.Commits, plural(m.Commits), head)
	} else {
		fmt.Printf("Nothing to merge: %s has no commits beyond %s\n", m.Branch, m.Base)
	}
	printHookResults("post_merge", m.PostMergeHooks)
	if m.PostMergeError != "" {
		fmt.Printf("Warning: %s (the merge stands)\n", m.PostMergeError)
	}

	switch {
	case res.KeptWorktree:
		fmt.Printf("Kept worktree %s\n", m.Worktree)
	case res.CleanupDeferred:
		fmt.Printf("An agent session in %s is mid-turn; the worktree will be archived and removed once its sessions are idle.\n", m.Worktree)
	case res.Cleanup != nil:
		c := res.Cleanup
		if c.Activated != "" {
			fmt.Printf("Switched to %s\n", c.Activated)
		}
		if c.ArchivedSessions > 0 {
			fmt.Printf("Archived %d session%s\n", c.ArchivedSessions, plural(c.ArchivedSessions))
		}
		printHookResults("pre_remove", c.PreRemoveHooks)
		if c.Remove != nil {
			printHookResults("pre_remove", c.Remove.PreRemoveHooks)
		}
		if c.Error != "" {
			return fmt.Errorf("merged, but cleanup failed: %s", c.Error)
		}
		fmt.Printf("Removed worktree %s\n", m.Worktree)
		if c.Remove != nil {
			switch {
			case c.Remove.BranchDeleted:
				fmt.Printf("Deleted branch %s\n", m.Branch)
			case c.Remove.BranchError != "":
				fmt.Printf("Kept branch %s: %s\n", m.Branch, c.Remove.BranchError)
			}
		}
	}
	return nil
}

// currentWorktree resolves the Trellis worktree containing the working directory.
func currentWorktree(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git worktree")
	}
	top := resolvePath(strings.TrimSpace(string(out)))
	worktrees, err := apiClient.Worktrees.List(ctx)
	if err != nil {
		return "", err
	}
	for _, wt := range worktrees {
		if resolvePath(wt.Path) == top {
			return wt.Name(), nil
		}
	}
	return "", fmt.Errorf("%s is not a Trellis worktree", top)
}

func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func printHookResults(phase string, hooks []client.HookResult) {
	for _, h := range hooks {
		mark := "✓"
		if !h.Success {
			mark = "✗"
		}
		fmt.Printf("  %s %s: %s (%s)\n", mark, phase, h.Name, h.Duration.Round(100*time.Millisecond))
		if !h.Success && strings.TrimSpace(h.Output) != "" {
			fmt.Println(indent(strings.TrimRight(h.Output, "\n"), "      "))
		}
	}
}

// printFinishErrorDetails prints conflicts and hook output from a failed finish.
func printFinishErrorDetails(details map[string]interface{}) {
	if conflicts, ok := details["conflicts"].([]interface{}); ok && len(conflicts) > 0 {
		fmt.Println("Conflicting files:")
		for _, c := range conflicts {
			fmt.Printf("  %v\n", c)
		}
	}
	hooks, _ := details["hook_results"].([]interface{})
	for _, raw := range hooks {
		h, _ := raw.(map[string]interface{})
		if h == nil {
			continue
		}
		mark := "✓"
		if ok, _ := h["Success"].(bool); !ok {
			mark = "✗"
		}
		fmt.Printf("  %s pre_merge: %v\n", mark, h["Name"])
		if out, _ := h["Output"].(string); mark == "✗" && strings.TrimSpace(out) != "" {
			fmt.Println(indent(strings.TrimRight(out, "\n"), "      "))
		}
	}
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
