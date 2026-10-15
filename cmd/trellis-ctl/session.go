// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/wingedpig/trellis/pkg/client"
)

// sessionTimeout is the default HTTP timeout for session commands: start
// waits up to 90s for the agent to accept the prompt.
const sessionTimeout = 5 * time.Minute

func cmdSession(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: trellis-ctl session start [worktree] [args]")
	}
	switch args[0] {
	case "start":
		return cmdSessionStart(args[1:])
	default:
		return fmt.Errorf("unknown session subcommand: %s", args[0])
	}
}

func cmdSessionStart(args []string) error {
	const usage = "usage: trellis-ctl session start [worktree] [-agent claude|codex] [-prompt <text> | -prompt-file <path|->] [-model <m>] [-effort <e>] [-name <session name>]"
	pos, vals, _, err := parseTaskFlags(args,
		[]string{"agent", "prompt", "prompt-file", "model", "effort", "name"},
		nil)
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

	prompt, err := readPrompt(vals)
	if err != nil {
		return err
	}

	req := client.SessionStartRequest{
		Worktree:    name,
		Agent:       vals["agent"],
		Prompt:      prompt,
		Model:       vals["model"],
		Effort:      vals["effort"],
		SessionName: vals["name"],
	}
	if req.Agent == "" {
		req.Agent = "claude"
	}
	if !jsonOutput {
		fmt.Printf("Starting %s session in %s...\n", req.Agent, name)
	}
	res, err := apiClient.Tasks.StartSession(ctx, req)
	if err != nil {
		return err
	}
	if jsonOutput {
		printJSON(res)
		return nil
	}
	printSessionStart(res, prompt)
	return nil
}
