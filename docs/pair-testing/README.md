# Pair-loop manual test scenario

These three files give you a deterministic, scripted way to exercise the
paired-review-loop end-to-end without writing real code. Both sides follow
state-machine rules driven by the partner's most recent text, so a healthy
run always converges to LGTM in three rounds.

## What this exercises

- Basic relay flow (round 1 → round 2 → round 3)
- "Not LGTM" rejection (rounds 1 and 2 — the reviewer says "Not LGTM"
  in-line, which must **not** match the stop signal because LGTM is not on
  its own line)
- Stop-signal detection (round 3 — the reviewer puts `LGTM` on its own
  line and the loop must terminate)
- `wait_for_next` kickoff (the loop must not relay anything until you
  prompt the implementer after pair start)
- Auto-navigation between sessions on each round

## Prerequisites

Two live sessions in the same Trellis instance — one Claude, one Codex,
in any worktree. Either side can play either role; the canonical setup
is Claude as implementer, Codex as reviewer.

## Setup

If you're testing from within the trellis repo worktree, the sessions
can read these files directly. Otherwise, paste the file contents into
the session messages — same effect.

1. **Load the implementer rules** into the implementer session
   (Claude, by convention). Either:

   - **From inside the trellis repo worktree** (preferred — exercises
     the real Read tool):

     ```
     Read docs/pair-testing/implementer-mock.md and follow those rules
     exactly for the rest of this conversation. Reply only with the
     word "Ready" to confirm.
     ```

   - **From any other worktree**: paste the contents of
     `implementer-mock.md` into the session, prefixed with:

     ```
     Follow these rules exactly for the rest of this conversation. Reply
     only with the word "Ready" to confirm.

     ---
     <paste implementer-mock.md content here>
     ```

   The session should respond `Ready`.

2. **Load the reviewer rules** into the reviewer session (Codex, by
   convention) the same way, swapping `implementer-mock.md` for
   `reviewer-mock.md`.

3. **Create the pair.** From either session's page, click the 🔗 link
   button. In the modal:

   - Confirm "This session" role (use **Swap** if needed).
   - Pick the partner session.
   - Leave prompts and stop signal at defaults.
   - Kickoff: **Wait for implementer's next turn** (the default).
   - Click **Start pair**.

   The banner should appear in yellow on both pages. Nothing should
   happen yet — verify by waiting 30 seconds; if you see any `pair %s:
   captured` log lines, the wait-for-next gate is broken.

4. **Kick off the test** by typing into the implementer session:

   ```
   Begin the test.
   ```

## Expected behaviour

- **Round 1**: implementer first issues a Write tool call against
  `/tmp/trellis-pair-test-round1.txt`, which triggers a permission
  prompt. **While that prompt is pending, nothing should happen on the
  reviewer side** — no auto-navigation, no `pair %s: captured` log
  line. After you click Allow, the Write completes and the implementer
  says "Implemented addNumbers...". Page navigates to reviewer; reviewer
  responds with a "Not LGTM" complaint about negatives. Page navigates
  back to implementer.
- **Round 2**: implementer says "Updated addNumbers to handle negative
  numbers...". Reviewer complains about floats. "Not LGTM" again.
- **Round 3**: implementer says "Updated addNumbers to handle floats..."
  including both "negative" and "float". Reviewer says `LGTM` on its
  own line. Loop stops with reason `lgtm`. Banner disappears.

Total rounds: 3. Final state: stopped, reason lgtm.

## Permission-prompt check (baked into round 1)

The round 1 Write call exists specifically to verify the
permission-prompt fix — the bug where Claude asking for permission
was treated as "done" and triggered an unintended relay. Verify
explicitly:

1. Watch the implementer page after typing "Begin the test."
2. When the Allow/Deny prompt appears, wait 5+ seconds before clicking
   Allow.
3. The page should **not** auto-navigate during that wait, and no
   `pair %s: captured` log line should appear until after you click
   Allow and the scripted "Implemented addNumbers..." line is emitted.
4. If the page flips to the reviewer or a relay happens while the
   prompt is still pending, the bug has regressed.

## Failure modes worth specifically checking

- Confirm-before-each-relay mode: re-create the pair with the
  "Confirm before each relay" checkbox on. Each round should pause for
  your approval in a modal.
- Auto-pause: while the loop is mid-round, type a fresh message into
  the side that is NOT currently generating (i.e., the side waiting
  passively). The pair should auto-pause with reason `user_typed`. Verify
  by checking the banner shows "paused" or the trellis log shows
  `pair %s: auto-pause from user-typed detection`.
- Max-rounds: lower the max-rounds in the Settings dialog to 2 during
  round 2; the loop should stop with reason `max_rounds`.
- Server restart: stop trellis mid-loop while a turn is generating.
  Restart. The pair should resume; once the generating side finishes,
  the loop should continue.

## Notes

- Each model run still costs API tokens — the mock is "free" only of
  randomness, not of usage. Three rounds at ~1k tokens each is enough
  to verify the full state machine.
- The scripts are short on purpose; complex prose tends to leak through
  the model's planning and produce off-script responses.
