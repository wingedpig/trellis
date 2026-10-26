// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package cases

import (
	"context"
	"slices"
	"strings"
	"time"
)

// sessionCommitLookback bounds how far before a case's creation SessionCommits
// looks: an agent session usually commits shortly before its first Trellis
// Commit creates the case.
const sessionCommitLookback = 30 * 24 * time.Hour

// SessionKey is how sessions are named in Trellis-Session trailers and
// CommitEntry.SessionID: "<agent>:<session-id>".
func SessionKey(agent, sessionID string) string {
	return agent + ":" + sessionID
}

// AttachedSessions returns the session keys of every transcript on the case.
func (c *CaseJSON) AttachedSessions() map[string]bool {
	out := map[string]bool{}
	for _, r := range c.Claude {
		if r.SourceSessionID != "" {
			out[SessionKey("claude", r.SourceSessionID)] = true
		}
	}
	for _, r := range c.Codex {
		if r.SourceSessionID != "" {
			out[SessionKey("codex", r.SourceSessionID)] = true
		}
	}
	return out
}

// SessionCommits finds commits on the worktree's HEAD that agent sessions
// made themselves for an open case: commits whose Trellis-Session trailer
// names a session attached to the case (or one of extraSessions), or whose
// Trellis-Case trailer names the case. Commits already recorded on the case
// are skipped, including rebased copies (same session and message).
//
// unattached lists the sessions behind the returned commits that have no
// transcript on the case yet, so the caller can attach them.
func (m *Manager) SessionCommits(ctx context.Context, worktreePath, caseID string, extraSessions ...string) (entries []CommitEntry, unattached []string, err error) {
	c, err := m.Get(worktreePath, caseID)
	if err != nil {
		return nil, nil, err
	}
	attached := c.AttachedSessions()
	want := map[string]bool{}
	for k := range attached {
		want[k] = true
	}
	for _, k := range extraSessions {
		want[k] = true
	}
	recorded := map[string]bool{}
	for _, ce := range c.Commits {
		recorded[ce.SHA] = true
		if ce.SessionID != "" {
			recorded[ce.SessionID+"\x00"+ce.Message] = true
		}
	}

	since := c.CreatedAt.Add(-sessionCommitLookback).Format(time.RFC3339)
	out, err := gitOut(ctx, worktreePath, "log", "--since="+since,
		"--format=%H%x1f%h%x1f%cI%x1f%(trailers:key="+SessionTrailer+",valueonly,unfold,separator=%x20)%x1f%(trailers:key="+CommitTrailer+",valueonly,unfold,separator=%x20)%x1e",
		"HEAD")
	if err != nil {
		return nil, nil, nil // no commits yet, or not a repo: nothing to record
	}
	newSessions := map[string]bool{}
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.Split(strings.TrimSpace(rec), "\x1f")
		if len(f) != 5 || recorded[f[0]] {
			continue
		}
		// Made while a different case was open (a session can outlive a
		// case): that commit belongs to the other case.
		if strings.TrimSpace(f[4]) != "" && !containsField(f[4], caseID) {
			continue
		}
		session := ""
		for _, s := range strings.Fields(f[3]) {
			if want[s] || f[4] != "" {
				session = s
				break
			}
		}
		if session == "" {
			continue
		}
		msg, err := gitOut(ctx, worktreePath, "log", "-1", "--format=%B", f[0])
		if err != nil {
			continue
		}
		msg = strings.TrimSpace(msg)
		if recorded[session+"\x00"+msg] {
			continue
		}
		at, _ := time.Parse(time.RFC3339, f[2])
		var files []string
		if names, err := gitOut(ctx, worktreePath, "show", "--name-only", "--format=", f[0]); err == nil {
			for _, n := range strings.Split(names, "\n") {
				if n = strings.TrimSpace(n); n != "" {
					files = append(files, n)
				}
			}
		}
		entries = append(entries, CommitEntry{
			SHA: f[0], ShortSHA: f[1], CommittedAt: at, Message: msg,
			FilesChanged: files, SessionID: session,
		})
		if !attached[session] && !newSessions[session] {
			newSessions[session] = true
			unattached = append(unattached, session)
		}
	}
	// git log lists newest first; the case timeline is oldest first.
	slices.Reverse(entries)
	return entries, unattached, nil
}

func containsField(s, v string) bool {
	for _, f := range strings.Fields(s) {
		if f == v {
			return true
		}
	}
	return false
}
