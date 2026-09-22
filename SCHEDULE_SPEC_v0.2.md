# Scheduled Task Specification

**Version:** 0.2  
**Status:** Specification for Implementation  
**Related:** [PAIRING_SPEC.md](PAIRING_SPEC.md), [PHASE_LOOP_SPEC.md](PHASE_LOOP_SPEC.md)

---

## 1. Overview

A **scheduled task** is a durable, named Trellis object that starts a fresh
Claude or Codex session at a configured local time, sends one or more prompts,
waits for the work to finish, and records what happened.

Scheduled tasks are Trellis's third autonomous driver, after pairing and the
phased-checklist loop. They reuse the same basic architecture:

- a process-wide registry that owns persistence and scheduling;
- a per-run driver that controls an ordinary agent session;
- session-state events plus reconciliation polling for completion detection;
- durable records that explain every scheduled occurrence, including skips.

The defining difference is that nobody may be watching. The design therefore
prioritizes observable failures over optimistic automation:

- a scheduled occurrence is never silently lost;
- missed occurrences are recorded but never executed late;
- overlapping or capacity-blocked occurrences are skipped rather than queued;
- a clean agent turn is called `ran`, not `succeeded`, unless the agent reports
  an explicit result;
- blocked approval requests remain resumable while the same Trellis process is
  running;
- after a Trellis restart, nonterminal scheduled runs are interrupted rather
  than guessed-at or replayed;
- sessions are kept unless the agent explicitly requests another disposition.

This version deliberately implements **clock scheduling only**. Log-match,
event, API, and GitHub triggers are deferred to later versions.

---

## 2. Scope and Terminology

### 2.1 Task

A **task** is the durable configuration: its name, prompt file, schedule,
target worktree, agent, model, permission mode, and limits. It can create many
runs over its lifetime.

### 2.2 Trigger

A **trigger** describes when the task should run. The schema retains a trigger
array for future compatibility, but version 0.2 requires exactly one trigger
and that trigger must be of type `schedule`.

### 2.3 Occurrence

An **occurrence** is one nominal opportunity to run:

- an automatic schedule time;
- an explicit **Run now** request; or
- an aggregate record describing schedule times missed while Trellis was not
  running.

Each automatic occurrence has a deterministic identity so a timer, wake scan,
or restart cannot create duplicate run records for the same scheduled instant.

### 2.4 Run

A **run** is the persisted record produced for an occurrence. A run may start
an agent session, or it may terminate as `skipped` during preflight. Every
automatic occurrence produces a run record or is represented in one aggregate
`missed_while_down` record.

### 2.5 Scheduler ownership

A session created by a run is **scheduler-owned** until the run reaches a
terminal outcome or the user explicitly takes it over. While scheduler-owned,
ordinary user messages are rejected so they cannot interleave with later task
steps. Permission responses and cancellation remain available.

---

## 3. Task Storage and Prompt Files

### 3.1 Split storage

A task is stored in two places:

| Part | Location | Reason |
|------|----------|--------|
| Prompt | `trellis/schedules/<name>.md` in the target worktree | The substantive instructions belong in the repository, with diffs and history. |
| Configuration and runtime records | `.trellis/` in the Trellis state directory | Timing, enablement, history, and runtime checkpoints are live application state. |

There are no task declarations in `trellis.hjson`. The optional `schedules`
section contains only global scheduler settings:

```hjson
schedules: {
  dir: "trellis/schedules"       // relative to each target worktree root
  max_concurrent_runs: 2         // default 2, minimum 1
}
```

Defaults apply when the section is omitted.

### 3.2 Task record

Task configuration is stored in:

```text
.trellis/schedule-tasks/<task-id>.json
```

The task record is authoritative for:

- name and description;
- enabled and archived state;
- prompt path;
- schedule;
- target agent, worktree, model, and permission mode;
- timeout and retention limits.

The prompt file contains none of that metadata. In particular, it has no YAML
frontmatter.

Task names are kebab-case and unique across active and archived tasks. The
prompt path is normally derived from the name. Renaming a task atomically
renames the prompt file and updates the task record; it is refused when the
destination exists.

Every task update increments a monotonically increasing `revision`. API writes
use optimistic concurrency through an ETag or revision precondition so a stale
browser cannot overwrite a newer edit.

Changing the target worktree changes which repository copy of the relative
prompt path the task uses. Trellis does not move or delete the old worktree's
file. The editor reloads or creates the prompt in the new target and requires
the user to resolve any content difference before saving the target change.

### 3.3 Runtime record

Server-managed mutable state is stored separately in:

```text
.trellis/schedule-runtime/<task-id>.json
```

It contains scheduler checkpoints and derived status such as:

- the last instant through which the schedule was evaluated;
- the next calculated fire time;
- the latest run ID and outcome;
- whether a one-time occurrence has been consumed.

Normal task PATCH requests cannot supply or overwrite runtime fields. This
prevents a stale editor save from restoring old scheduler counters or
checkpoints.

### 3.4 Prompt file

The prompt file is plain Markdown:

```markdown
Review commits made since the previous business day.

Run the relevant tests and summarize any regressions. Do not change code.
```

The file is editable directly or through the task editor. The prompt API
returns a content ETag. Writes require `If-Match`; a mismatch returns a
conflict and the UI offers a diff rather than overwriting the newer file.

Bodies over **25 KiB of UTF-8 text** are rejected.

If the file is missing at run time, the occurrence becomes:

```text
skipped / prompt_missing
```

If it cannot be decoded or parsed into valid steps, it becomes:

```text
skipped / prompt_invalid
```

### 3.5 Prompt sequences

A prompt with no `## Step` headings is sent as one message.

An H2 heading outside a fenced code block begins a new step when its text is
`Step` or starts with `Step `, `Step:`, `Step -`, or `Step —`. Headings such as
`## Stepping through the code` do not split the prompt. For example:

```markdown
Before every step, keep changes limited to the current worktree.

## Step 1 — Test

Run the test suite and summarize failures.

## Step 2 — Analyze

Inspect each failure and explain whether it is a regression or a flaky test.
```

Content before the first step heading is a **preamble** prepended to every
step. Empty steps are invalid. The splitter used by the editor preview and the
runtime is the same implementation.

### 3.6 Immutable prompt snapshot

At the beginning of preflight, Trellis reads and parses the complete prompt
file once. The run stores:

- the task revision;
- prompt path and ETag;
- original prompt bytes and SHA-256;
- parsed preamble and step list;
- SHA-256 of each final message sent to the agent.

Edits made after the snapshot affect only later runs. A multi-step run never
changes instructions halfway through because its file was edited. The trigger,
target, model, permission mode, and timeout limits are snapshotted at the same
point; editing, pausing, or archiving the task does not rewrite an already
claimed run.

### 3.7 Run-control footer

Trellis appends a small, visible run-control footer to the final step. It tells
the agent that the session is scheduled and describes the result command:

```bash
trellis-ctl schedule result \
  --status succeeded \
  --summary "Completed the review" \
  --session archive
```

The footer states that:

- `--status` is `succeeded` or `failed`;
- `--summary` is a concise human-readable result;
- `--session` is optional and defaults to `keep`;
- the result command is to be called only after all requested work is complete;
- without a valid result command, a cleanly completed run is recorded as
  `ran` and its session is kept.

The footer is shown by prompt preview and stored as part of the sent-message
snapshot. It is not written into the repository Markdown file.

---

## 4. Schedule Trigger

### 4.1 Exactly one trigger

A task record carries a trigger array for forward compatibility:

```json
{
  "triggers": [
    {
      "id": "trg_01J...",
      "type": "schedule",
      "preset": "daily",
      "time": "09:00",
      "day_of_week": "",
      "cron": "",
      "timestamp": "",
      "timezone": "local"
    }
  ]
}
```

Version 0.2 validates that the array contains exactly one schedule trigger. The
UI exposes one **Schedule** section and no **Add trigger** control.

### 4.2 Presets

| Preset | Meaning |
|--------|---------|
| `manual` | No automatic fire. The task runs only through **Run now** or the run API. |
| `hourly` | Every hour at minute zero. |
| `daily` | Every day at `time`. Default `09:00`. |
| `weekdays` | Monday through Friday at `time`. |
| `weekly` | On `day_of_week` at `time`. |
| `cron` | Standard five-field cron expression in `cron`. |
| `once` | One absolute RFC3339 timestamp in `timestamp`. |

`time` is an `HH:MM` local wall-clock value. `timestamp` is separate so a
one-time instant is never confused with a recurring time of day.

The minimum recurring interval is one minute. Seconds are not supported.

### 4.3 Local timezone and daylight saving time

All recurring schedules use the Trellis server's local `time.Location`. The UI
shows the resolved zone name and the next five fire instants with UTC offsets.
Each run records both the local timestamp and UTC instant used.

Trellis uses `robfig/cron/v3` for candidate-time calculation and adds one
safety rule for autonomous work:

- a local wall-clock time that does not exist during a spring-forward gap does
  not produce an occurrence;
- a local wall-clock slot repeated during a fall-back transition runs only
  once, at its first occurrence. The second appearance has the same logical
  occurrence key and is ignored as a duplicate.

A `once` timestamp is an absolute RFC3339 instant and is not ambiguous.

### 4.4 Exact configured times

Trellis adds **no deterministic stagger or jitter**. A task configured for
09:00 is due at 09:00, subject only to ordinary operating-system scheduling
latency.

An already-running, recently reconciled scheduler treats a timer callback as
on time when it claims the occurrence within a fixed 30-second fire grace. The
grace absorbs normal timer and process latency; it is not used during startup
or wake reconciliation and never turns a missed occurrence into a catch-up
run.

Users who want to avoid collisions must configure different times. If several
tasks fire together and the global concurrency cap is reached, excess
occurrences are skipped as specified in §9.

### 4.5 Occurrence identity

For a recurring schedule, the automatic occurrence key is derived from:

```text
task ID + trigger ID + nominal local calendar slot + local zone name
```

Using the logical wall-clock slot rather than its UTC offset prevents a repeated
fall-back hour or backward wall-clock adjustment from running the same local
schedule slot twice. The run separately stores the actual resolved UTC instant.

For a `once` trigger, the key uses its absolute timestamp. The key is unique in
the run store, and claiming an already recorded occurrence is an idempotent
no-op. This also protects against duplicate timer callbacks, wake
reconciliation, and restart races.

Manual runs use a random occurrence ID and set `initiated_by: "manual"`.

### 4.6 Missed occurrences

Trellis never executes a scheduled occurrence late.

The registry maintains a durable `last_evaluated_at` checkpoint for every
enabled automatic task. It also runs a 30-second reconciliation tick to detect
sleep or large wall-clock jumps. On startup or wake, Trellis calculates the
schedule instants between the last durable checkpoint and the present time.

If one or more were missed, it writes one aggregate run record:

```text
skipped / missed_while_down
```

The record contains:

- `missed_count`;
- `missed_from` and `missed_through`;
- the task revision and trigger snapshot used for the calculation;
- `missed_count_truncated` if the fixed 10,000-occurrence enumeration cap
  prevented an exact count.

A pending timer is also classified as missed when wake detection shows that the
previous reconciliation tick was not recent, even if the process receives the
timer callback immediately after waking. The 30-second normal fire grace from
§4.4 applies only while Trellis has remained awake and responsive.

No catch-up session is created. Trellis waits for the next future occurrence.
When a task is newly enabled, resumed, restored, or materially rescheduled, its
checkpoint is reset to the current time so old schedule slots are not treated
as newly missed.

### 4.7 One-time tasks

A one-time timestamp is consumed exactly once. When its automatic scheduled
occurrence is claimed, Trellis disables the task whether the run starts or is
skipped. A manual **Run now** before the timestamp does not consume or disable
the future one-time occurrence.

If Trellis was down at the timestamp, startup records
`skipped / missed_while_down`, marks the one-time occurrence consumed, disables
the task, and notifies the user.

If preflight rejects the occurrence for any other reason—such as
`concurrency_cap`, `prompt_missing`, or `model_unavailable`—the task is still
disabled and the skipped one-time occurrence is prominently flagged.

The user may edit the timestamp or use **Run now** to try again. A past
one-time timestamp cannot be enabled through normal validation.

---

## 5. Target, Model, and Permissions

```json
{
  "target": {
    "agent": "claude",
    "worktree": "main",
    "model": "",
    "permission_mode": "prompt"
  }
}
```

### 5.1 Fixed worktrees only

Version 0.2 supports only an existing, named worktree. A scheduled run operates
against that checkout exactly as it exists at fire time, including:

- staged changes;
- modified or deleted tracked files;
- untracked files;
- locally generated files and warm build caches.

Trellis displays current Git status as advisory information but does not
require a clean worktree, including for `auto_approve` tasks.

Ephemeral worktrees, automatic branches, pushing, retention, and reaping are
out of scope for this version.

### 5.2 Agent and model

`agent` is `claude` or `codex`.

An empty `model` means “use the agent manager's current default.” A nonempty
value pins the task to that model.

Before starting a session, the agent manager resolves the requested model. If
a pinned model is unavailable, Trellis does not substitute another model. The
occurrence becomes:

```text
skipped / model_unavailable
```

The task remains enabled. Trellis notifies on the first such skip for a given
task revision and requested model; identical later skips remain in history but
do not repeatedly notify until the task changes or the model becomes available.
The task list and editor show that the pin is currently unavailable whenever
the agent manager can determine that before a fire.

Every started run stores both `requested_model` and `resolved_model`.

### 5.3 Permission modes

```text
permission_mode: prompt | auto_approve
```

`prompt` is the default and uses the agent manager's ordinary approval flow.

`auto_approve` uses Trellis's existing agent-specific automatic approval
behavior. The editor requires an explicit confirmation and explains that the
agent may read, edit, delete, and execute within the authority granted by the
underlying agent integration. The task list, task detail, session, and run
history all display an autonomous badge.

Every run stores:

- the requested generic permission mode;
- the resolved agent-specific permission policy;
- whether the target worktree was dirty at start.

The run-result operation is a narrow built-in capability. In `prompt` mode,
reporting the current run result must not require granting general shell or
filesystem authority: the agent integration should expose the scoped result
operation directly or allow only the exact token-scoped
`trellis-ctl schedule result` invocation. All other tool use follows the
selected permission mode.

### 5.4 Schedule self-modification

Scheduled tasks do not have a supported mechanism to pause, resume, archive,
restore, reschedule, retarget, or otherwise modify themselves or other tasks.
The run-control token described in §7 grants only permission to report the
current run result.

Official Trellis clients reject schedule-mutation commands when invoked from a
managed scheduled-session environment. The task prompt may recommend a future
configuration change, but a user must apply it.

`auto_approve` is still broad local authority, so this is a product and API
capability boundary—not a claim that an unrestricted same-machine shell is a
complete security sandbox.

---

## 6. Run Lifecycle

### 6.1 State machine

```text
                         preflight rejection
(trigger/manual request) ───────────────────────────────► done / skipped
          │
          ▼
      preflight
          │
          ▼
   starting_session ── startup error ───────────────────► done / failed
          │
          ▼
       running ── permission request ──► blocked
          ▲                                │
          └──── response forwarded after ──┘
                capacity is reacquired
          │
          ├─ all steps quiescent ─► evaluating ─► disposing ─► done
          │                                                  succeeded |
          │                                                  failed | ran
          ├─ active-time limit ─────────────────────────────► done / timed_out
          ├─ explicit takeover ─────────────────────────────► done / taken_over
          └─ cancellation ──────────────────────────────────► done / canceled

blocked ── explicit takeover ───────────────────────────► done / taken_over
blocked ── cancellation ────────────────────────────────► done / canceled

Any nonterminal state at Trellis startup ───────────────► done / interrupted
```

The persisted run `state` describes the current phase. `outcome` is populated
only when the run becomes terminal.

### 6.2 Claim before side effects

When a schedule is due, Trellis atomically claims its occurrence key and
creates a run record in `preflight` before creating a session or sending a
prompt. This guarantees that a process exit after the schedule fires still
leaves durable evidence.

For a one-time task, consuming and disabling the task occurs under the same
registry lock as occurrence claim. If separate atomic file writes are
interrupted, startup repair uses the occurrence record as the source of truth
and completes the disable operation without creating a second occurrence.

### 6.3 Preflight

Preflight checks are performed in this order:

1. For an automatic occurrence, the task is enabled and not archived. A manual
   **Run now** is allowed for a disabled task but not an archived task.
2. The target worktree exists and its root can be resolved.
3. The prompt file exists, is within the size limit, parses, and is snapshotted.
4. The requested agent binary is available.
5. The requested model resolves; a pinned unavailable model is rejected.
6. No prior run of the same task is active.
7. A global scheduled-run slot is available.

Checks 6 and 7 are performed under registry synchronization immediately before
session creation so concurrent fires cannot both pass.

A preflight rejection completes the run as `skipped` with one of the reasons
in §9. No session is created.

A failure after preflight has accepted the run—for example, session creation or
prompt dispatch failure—is `failed`, not `skipped`, because execution was
attempted.

### 6.4 Session creation

Each accepted run creates a fresh Claude or Codex session in the target
worktree. It does not reuse a prior scheduled or interactive session.

The session record includes origin metadata:

```text
origin: scheduled
schedule_task_id
schedule_run_id
schedule_task_name
scheduler_owned: true
```

The default display name is the task name plus the scheduled or manual start
time. The run stores the session ID before sending the first prompt. The agent manager
persists the origin metadata as part of session creation, so startup can
reconcile an orphaned session back to its run if Trellis exits between session
creation and the run-record update.

The driver then sends each snapshotted step in order, waiting for quiescence
between steps.

### 6.5 Quiescence and step completion

A step is complete when the agent session is stable for two seconds with all of
the following true:

- it is not generating;
- it has no pending permission or approval request;
- it has no agent-managed background work still running.

The scheduler uses `session.state_changed` events for responsiveness and
periodic status reconciliation to heal a missed event. The agent-control facade
must expose authoritative quiescence rather than inferring completion solely
from the last text or foreground process.

The driver records per-step start, quiescent, and duration timestamps and a
bounded final-response excerpt. It never reparses the prompt file between
steps.

### 6.6 Blocked approvals

When a scheduler-owned session requests permission in `prompt` mode:

1. the run transitions immediately to `blocked`;
2. the session remains alive and appears in the inbox as `needs_approval`;
3. the global active-run slot is released;
4. the task remains active for overlap purposes, so later occurrences of the
   same task are skipped;
5. step and run timeout clocks pause while blocked.

A blocked run does not expire automatically. The registry keeps a lightweight
event-driven monitor for it while the same Trellis process remains alive.

When the user approves or denies the request, the response is routed through
the scheduler. The existing run has priority in reacquiring a global slot. If
no slot is available yet, the response is recorded but not forwarded, and the
run remains visibly blocked with `waiting_for_capacity: true`. Once a slot is
available, Trellis forwards the response and the run returns to `running`.

Approval and denial are part of the managed run and do not constitute a user
takeover.

### 6.7 Explicit takeover

While a scheduled run is active, the ordinary message composer is disabled and
the session displays **Managed by scheduled task**. The server also rejects a
normal WebSocket `message` request with `takeover_required`; this is not only a
client-side restriction.

The user may select **Take over**. Trellis then:

- records terminal outcome `taken_over`;
- stops sending any remaining task steps;
- invalidates the run-result token;
- suppresses all automatic session disposition;
- transfers session ownership to the user;
- enables ordinary interactive messages;
- leaves the current agent turn or approval request intact.

A taken-over session that is still generating or waiting for approval counts
as an active prior run for the next occurrence of that task. Once it becomes
quiescent, later occurrences may start normally even though the interactive
session remains available.

### 6.8 Timeouts

Two limits bound active execution:

```text
step_timeout: 30m
run_timeout:  2h
```

Both use accumulated active execution time and pause while the run is blocked
on approval or waiting to reacquire capacity after a response.

Exceeding either limit:

- invokes an agent-specific hard cancellation that stops foreground and
  background work;
- records `timed_out` and whether the step or run limit fired;
- invalidates the result token;
- keeps the session;
- sends no later steps and performs no automatic disposition.

### 6.9 Cancellation

The user may cancel a `starting_session`, `running`, or `blocked` run. Trellis
hard-cancels agent work when present, records `canceled`, invalidates the result
token, keeps the session, and releases scheduler ownership.

Cancellation is separate from task pause or archive. Pausing or archiving a
task prevents future automatic fires but does not cancel a run already
claimed.

### 6.10 Restart behavior

On startup, every persisted run whose state is not terminal is changed to:

```text
done / interrupted
```

Trellis does not attempt to resume the run, resend a possibly delivered
prompt, continue later steps, evaluate an old result, or apply a pending
session disposition.

For an interrupted run:

- the session and worktree are retained;
- the run token is invalid;
- scheduler ownership is released so the session can be continued manually;
- the original task remains eligible for its next future occurrence;
- no catch-up run is created.

This policy intentionally favors avoiding duplicate side effects over
recovering unattended work after a server restart.

---

## 7. Completion and Outcome

### 7.1 Outcomes

Terminal outcomes are:

| Outcome | Meaning |
|---------|---------|
| `succeeded` | The agent explicitly reported success for this run and no later agent error invalidated it. |
| `failed` | The agent explicitly reported failure, the agent turn failed, or Trellis failed after accepting execution. |
| `ran` | All steps became quiescent without an operational error, but no explicit result was reported. |
| `skipped` | Preflight or schedule policy prevented session creation. |
| `timed_out` | A step or run exceeded its active execution limit. |
| `canceled` | A user canceled the active run. |
| `taken_over` | A user explicitly transferred the session from scheduler control to interactive control. |
| `interrupted` | Trellis restarted while the run was nonterminal. |

`ran` is deliberately neutral. A clean process and an idle session prove that
the turn ended, not that the requested work was accomplished.

### 7.2 Run-specific result command

The only affirmative completion protocol is:

```bash
trellis-ctl schedule result \
  --status succeeded|failed \
  --summary "human-readable summary" \
  [--session keep|archive|trash|delete]
```

`--status` is required. `--summary` is optional but strongly encouraged and is
limited to 4 KiB. `--session` defaults to `keep`.

The command reports against the current run using environment variables; the
agent does not choose an arbitrary run ID. A result is accepted only while:

- the run is scheduler-owned and nonterminal;
- the final step is active;
- the supplied token matches that run.

The first accepted result is authoritative. Repeating the identical request is
idempotent. A conflicting later request returns HTTP 409 and does not modify
the recorded result.

Reporting a result does not itself terminate the turn. The driver still waits
for the final step to become quiescent, then evaluates the run.

### 7.3 Outcome precedence

At evaluation, outcome is selected in this order:

1. A scheduler terminal condition already recorded—`skipped`, `timed_out`,
   `canceled`, `taken_over`, or `interrupted`—wins.
2. A session creation, dispatch, or final turn error produces `failed`.
3. A valid reported status of `failed` produces `failed`.
4. A valid reported status of `succeeded` produces `succeeded`.
5. Otherwise the outcome is `ran`.

A `succeeded` report followed by an agent or transport error is therefore
`failed`. Session-disposition failure is recorded separately and does not
rewrite the task outcome.

Each run stores `outcome_source`, such as `agent_result`, `agent_error`,
`preflight`, `scheduler_timeout`, or `clean_without_result`.

### 7.4 Run environment and authorization

A scheduled agent process receives:

```text
TRELLIS_API
TRELLIS_SESSION_ID
TRELLIS_WORKTREE
TRELLIS_SCHEDULE_MANAGED=1
TRELLIS_SCHEDULE_TASK_ID
TRELLIS_SCHEDULE_RUN_ID
TRELLIS_SCHEDULE_RUN_TOKEN
```

The run token is cryptographically random, scoped only to the result endpoint,
and valid only while the run is scheduler-owned and nonterminal. It is not a
general Trellis API credential. The server keeps only the material required to
validate it and invalidates it on completion, takeover, cancellation, timeout,
or restart.

`trellis-ctl schedule result` reads these variables and sends the result to:

```text
POST /api/v1/schedule/runs/{runID}/result
```

A call outside a managed scheduled session fails clearly rather than guessing
which run the caller intended.

---

## 8. Session Disposition

The agent may request what Trellis should do with the session as part of the
result command:

| Action | Effect |
|--------|--------|
| `keep` | Leave the session in the worktree's normal session list. |
| `archive` | Archive the session; hide it from normal lists while retaining searchable history. |
| `trash` | Move the session to the existing recoverable trash. |
| `delete` | Permanently delete the session and transcript. |

The default is always `keep`. It applies when:

- `--session` is omitted;
- no result command is submitted;
- the run is skipped, timed out, canceled, taken over, or interrupted;
- the agent or Trellis fails before a valid result is accepted.

A non-`keep` request is applied only after evaluation and only when:

- the session is quiescent;
- no approval request is pending;
- scheduler ownership has not been transferred;
- no human WebSocket viewer is currently attached to that session.

Internal scheduler subscriptions do not count as human viewers. The
agent-control facade therefore exposes `ActiveViewerCount`, not a generic
subscriber count.

If a viewer is present, Trellis keeps the session and records
`disposition_skipped_reason: "active_viewer"`; it does not later surprise the
user by archiving or deleting the session after the page closes.

If archive, trash, or delete fails, the task outcome remains unchanged. The
run records the disposition error, the session remains in its actual state,
and Trellis notifies the user.

The run record retains the session ID and requested/applied disposition even
when the session is later moved or deleted. A missing transcript is shown as
unavailable rather than breaking run history.

Once a kept or otherwise disposed session reaches terminal run state,
scheduler ownership ends. A kept session becomes an ordinary interactive
session and no **Take over** action is required.

---

## 9. Concurrency and Skip Records

### 9.1 Per-task overlap

Version 0.2 has one overlap policy: **skip**.

If a task occurrence arrives while a prior run of that task is active, the new
occurrence becomes:

```text
skipped / previous_run_active
```

No pending occurrence is retained. When the prior run later finishes, Trellis
waits for the next regularly scheduled time or an explicit **Run now**.

For overlap purposes, a prior run is active while it is in `preflight`,
`starting_session`, `running`, `blocked`, `evaluating`, or `disposing`. A run
that was taken over also blocks a new occurrence while its session is still
generating or waiting for approval.

### 9.2 Global concurrency

`schedules.max_concurrent_runs` defaults to two. It limits scheduler-owned runs
that currently hold an execution slot.

A run normally holds a slot from immediately before session creation through
evaluation and disposition. It releases the slot while blocked on approval and
reacquires one before the approval response is forwarded.

If a new occurrence reaches the dispatch gate while all slots are occupied, it
becomes:

```text
skipped / concurrency_cap
```

There is no global capacity queue and no later retry. An explicit manual run is
subject to the same cap.

### 9.3 Skip reasons

| Reason | Meaning |
|--------|---------|
| `previous_run_active` | An earlier run of the same task is still managed or its taken-over session is still busy. |
| `concurrency_cap` | The global scheduled-run limit was full at dispatch time. |
| `missed_while_down` | One or more automatic times passed while Trellis was not running or awake. |
| `worktree_missing` | The selected fixed worktree no longer exists. |
| `prompt_missing` | The repository prompt file is absent. |
| `prompt_invalid` | The prompt is too large, unreadable, or has an invalid step structure. |
| `agent_unavailable` | The configured Claude or Codex executable is unavailable. |
| `model_unavailable` | The task pinned a model the selected agent cannot use. |
| `task_disabled` | A stale timer lost a race with pause or automatic one-time disablement. |

Invalid schedule expressions and structurally invalid task records are rejected
when saved rather than becoming recurring skip reasons.

Every skip has its own persisted run record, except that several consecutive
missed schedule times are intentionally aggregated into one
`missed_while_down` record. Skip rows appear inline in run history with their
reason and relevant context.

---

## 10. Controls and Visibility

A scheduled task acts while nobody is watching, so its future behavior must be
inspectable before it is enabled.

### 10.1 Task editor

`/schedules/new` and `/schedules/{id}/edit` use the same full-page editor.

#### Identity

- unique kebab-case name;
- optional description;
- resolved prompt-file path.

Renaming shows that the repository file will be renamed and is rejected if the
destination already exists.

#### Prompt

The in-page editor reads and writes the Markdown prompt file. It includes:

- a live step count produced by the runtime splitter;
- a preview of the preamble and each step;
- the generated final-step run-control footer;
- an **Open in editor** action using Trellis's existing editor integration;
- ETag conflict detection and a diff when disk content changed since load.

The editor does not offer the interactive slash-command menu. Scheduled
instructions must be ordinary prose or invoke a repository skill or
`trellis-ctl` command explicitly.

#### Target

- agent: Claude or Codex;
- existing worktree picker;
- model: agent default or a pinned offered model;
- permission mode: `prompt` or `auto_approve`.

The target section shows current worktree Git status, but dirty status is
advisory. Selecting `auto_approve` opens an explicit confirmation describing
its authority and the fact that the fixed worktree may contain uncommitted
work. Creating or changing a task to `auto_approve` through any official API
client also requires a transient `acknowledge_auto_approve: true` field; the
acknowledgement is validated but is not part of the durable task definition.

#### Schedule

A single preset selector reveals only the fields required by that preset. The
editor shows:

- the server's resolved local timezone;
- the next five fire instants, without stagger;
- UTC offsets around daylight-saving transitions;
- a warning that missed fires are recorded and never run later;
- a one-time warning that the timestamp is consumed even if preflight skips.

For `manual`, the preview says **Manual only**.

#### Limits

The first release exposes:

```text
step timeout
run timeout
run-history retention
```

Overlap is fixed to skip and is explained but not configurable. The global
concurrency limit is a scheduler setting, not a per-task field.

#### Save actions

- **Save**;
- **Save and run now**.

Structural errors block save. Risk judgements—such as `auto_approve` against a
dirty fixed worktree—produce explicit warnings but remain allowed.

### 10.2 Dry run

**Dry run** performs the read-only portion of preflight and returns:

- resolved target and model;
- current worktree status;
- prompt path, ETag, byte count, and step count;
- every complete message that would be sent, including the run-control footer;
- current warnings and any read-only preflight failure.

It creates no run record, session, or side effect and does not claim a global
slot. It may be used while the task is disabled, but not after the task is
archived.

### 10.3 Task list

`/schedules` shows one row per non-archived task:

- name and description;
- schedule summary;
- target agent and worktree;
- requested model or **Agent default**;
- next fire;
- latest outcome;
- enabled toggle;
- an `auto_approve` badge when applicable.

Inline actions are **Run now** and **Pause / Resume**. Filters cover agent,
worktree, preset, enabled state, and latest outcome. A link opens archived
tasks.

There is no “unopened runs” or “unreviewed runs” heuristic in this version.

### 10.4 Task detail

`/schedules/{id}` shows:

- enabled or paused state;
- schedule and next five fire times;
- target, model, permission mode, and current worktree status;
- rendered prompt and detected steps;
- active run state, if any;
- complete run history, including skips;
- actions: **Run now**, **Dry run**, **Pause / Resume**, **Edit**, and
  **Archive task**.

Archiving is the only removal action in version 0.2.

### 10.5 Run history and detail

Run history is reverse chronological. Each row contains:

- scheduled time or manual initiation time;
- actual fire and start time;
- duration;
- state and outcome;
- skip reason when applicable;
- requested and resolved model;
- session ID and availability;
- explicit result summary;
- requested and applied session disposition.

The run detail is a timeline of occurrence claim, preflight, session creation,
each step, approval blocks and resumes, result report, evaluation, and
session disposition. It exposes the immutable task, trigger, prompt, target,
and permission snapshots used by that run.

`ran` is rendered neutrally with the explanation:

> Completed without an operational error, but the agent did not explicitly
> report whether the requested work succeeded.

### 10.6 Managed session UI

A scheduler-owned session displays:

- the originating task and run;
- **Managed by scheduled task** status;
- current step;
- approval controls when needed;
- **Cancel run**;
- **Take over**.

The ordinary composer remains disabled until takeover or terminal completion.
Approving or denying a tool request does not transfer ownership.

### 10.7 Inbox integration

A blocked scheduled session appears in the existing inbox as
`needs_approval`, tagged with its task and run. Opening it leads to the normal
session page with the scheduler ownership controls described above.

### 10.8 Archived tasks

Archived tasks appear under `/schedules/archived`. Archiving sets `enabled` to
false and:

- stops future automatic fires;
- removes the task from the normal list;
- preserves task configuration, runtime information, and run history;
- leaves the repository prompt file unchanged;
- leaves all prior sessions unchanged;
- does not cancel an already active run.

Restoring returns the task to the active list in a disabled state. The user
must explicitly resume it. Restoring resets missed-fire evaluation to the
restore time, so no archived interval is reported or executed.

---

## 11. Persistence and Restart

### 11.1 State layout

```text
.trellis/
  schedule-tasks/<task-id>.json
  schedule-runtime/<task-id>.json
  schedule-runs/<run-id>.json
```

All writes use the existing atomic temp-file-and-rename pattern. Registry locks
serialize occurrence claim, task overlap checks, one-time consumption, and
global slot allocation.

Run files are updated durably at phase boundaries. In particular:

- the occurrence and initial run record are persisted before external side
  effects;
- the session ID is persisted before the first prompt is sent;
- the result is persisted before evaluation;
- requested and applied disposition are persisted separately.

### 11.2 Task record

```json
{
  "schema": "trellis.schedule.task.v1",
  "id": "task_01J...",
  "revision": 7,
  "name": "nightly-review",
  "description": "Review recent commits and tests.",
  "enabled": true,
  "archived_at": null,
  "created_at": "2026-09-01T12:00:00-07:00",
  "updated_at": "2026-09-01T12:14:00-07:00",
  "prompt_path": "trellis/schedules/nightly-review.md",
  "triggers": [
    {
      "id": "trg_01J...",
      "type": "schedule",
      "preset": "daily",
      "time": "09:00",
      "day_of_week": "",
      "cron": "",
      "timestamp": "",
      "timezone": "local"
    }
  ],
  "target": {
    "agent": "claude",
    "worktree": "main",
    "model": "",
    "permission_mode": "prompt"
  },
  "limits": {
    "step_timeout_s": 1800,
    "run_timeout_s": 7200,
    "keep_runs": 50
  }
}
```

Fields removed from the original draft include worktree mode, base branch,
overlap selection, success strings, task-level teardown, log rules, and
unreviewed-session statistics.

### 11.3 Runtime record

```json
{
  "schema": "trellis.schedule.runtime.v1",
  "task_id": "task_01J...",
  "last_evaluated_at": "2026-09-01T12:15:00-07:00",
  "next_fire_at": "2026-09-02T09:00:00-07:00",
  "last_run_id": "run_01J...",
  "last_outcome": "ran",
  "one_time_consumed_at": null,
  "model_unavailable_notice_key": "",
  "updated_at": "2026-09-01T12:15:00-07:00"
}
```

`next_fire_at` is a cache and is recomputed when the task revision, local
location, or process state changes. `last_evaluated_at` is the durable boundary
used for missed-fire accounting.

### 11.4 Run record

```json
{
  "schema": "trellis.schedule.run.v1",
  "id": "run_01J...",
  "occurrence_key": "sha256:...",
  "task_id": "task_01J...",
  "task_name": "nightly-review",
  "task_revision": 7,
  "initiated_by": "schedule",
  "trigger": {
    "id": "trg_01J...",
    "type": "schedule",
    "preset": "daily",
    "time": "09:00",
    "timezone": "local"
  },
  "scheduled_for": "2026-09-02T09:00:00-07:00",
  "scheduled_for_utc": "2026-09-02T16:00:00Z",
  "fired_at": "2026-09-02T09:00:00.120-07:00",
  "started_at": "2026-09-02T09:00:00.310-07:00",
  "finished_at": "2026-09-02T09:12:31.000-07:00",
  "state": "done",
  "outcome": "succeeded",
  "outcome_source": "agent_result",
  "skip_reason": "",
  "missed": null,
  "prompt": {
    "path": "trellis/schedules/nightly-review.md",
    "etag": "sha256:...",
    "sha256": "...",
    "bytes": 1432,
    "body": "...",
    "preamble": "...",
    "steps": ["...", "..."]
  },
  "target": {
    "agent": "claude",
    "worktree": "main",
    "worktree_path": "/src/project",
    "worktree_dirty": true,
    "requested_model": "",
    "resolved_model": "agent-default-model",
    "permission_mode": "auto_approve",
    "resolved_permission_policy": "..."
  },
  "session": {
    "agent": "claude",
    "id": "...",
    "display_name": "nightly-review · 2026-09-02 09:00",
    "ownership": "released",
    "taken_over_at": null
  },
  "steps": [
    {
      "index": 0,
      "prompt_sha256": "...",
      "started_at": "...",
      "quiescent_at": "...",
      "active_duration_ms": 442000,
      "reply_excerpt": "..."
    }
  ],
  "result": {
    "status": "succeeded",
    "summary": "No regressions found.",
    "reported_at": "...",
    "requested_session_action": "archive"
  },
  "disposition": {
    "requested": "archive",
    "applied": "archive",
    "applied_at": "...",
    "skipped_reason": "",
    "error": ""
  },
  "timeout": null,
  "error": null
}
```

Skipped and interrupted runs omit fields that were never reached. A
`missed_while_down` run uses:

```json
{
  "missed": {
    "count": 4,
    "from": "2026-08-29T09:00:00-07:00",
    "through": "2026-09-01T09:00:00-07:00",
    "count_truncated": false
  }
}
```

### 11.5 Retention

`keep_runs` defaults to 50 per task. Pruning removes only terminal run records,
oldest first, and never removes the currently referenced active run. Archiving
a task does not immediately prune its existing history.

Session retention is independent. Removing an old run record does not delete a
session, and deleting a session does not delete its run record.

### 11.6 Startup sequence

The schedule registry starts after the agent managers and worktree manager are
ready. It then:

1. loads task, runtime, and retained run records;
2. validates task records and reports malformed entries visibly;
3. marks every nonterminal run `interrupted` and releases session ownership;
4. repairs one-time disablement from already claimed occurrence records;
5. reconciles scheduled-session origin metadata with run records, retaining any
   orphaned session and linking it to an interrupted run;
6. performs missed-fire accounting without starting catch-up work;
7. computes next fire times and arms timers.

Shutdown stops new occurrence claims before shutting down agent managers.
Orderly shutdown still causes any unfinished run to be observed as
`interrupted` on the next startup; no attempt is made to distinguish a clean
server restart from an unexpected one.

---

## 12. API

All endpoints use the existing `/api/v1` response envelope and are included in
`api/openapi.yaml` and the typed Go client under `pkg/client`.

### 12.1 Tasks

```text
GET    /api/v1/schedule                         list active tasks
GET    /api/v1/schedule/archived                list archived tasks
POST   /api/v1/schedule                         create task
GET    /api/v1/schedule/{id}                    task detail
PATCH  /api/v1/schedule/{id}                    update task (revision/ETag precondition)
POST   /api/v1/schedule/{id}/pause              disable automatic fires
POST   /api/v1/schedule/{id}/resume             enable automatic fires from now
POST   /api/v1/schedule/{id}/archive            archive task
POST   /api/v1/schedule/{id}/restore            restore task as disabled
POST   /api/v1/schedule/{id}/run                run now
POST   /api/v1/schedule/{id}/dry-run            resolve prompt and read-only preflight
GET    /api/v1/schedule/{id}/preview?count=5    next fire times
GET    /api/v1/schedule/{id}/runs               run history
```

There is no task DELETE endpoint in version 0.2.

`Run now` is allowed for a disabled non-archived task and creates a normal run
record. It is subject to target, prompt, overlap, model, agent, and global
concurrency checks.

### 12.2 Prompt file

```text
GET    /api/v1/schedule/{id}/prompt             read prompt, ETag, and parsed steps
PUT    /api/v1/schedule/{id}/prompt             write prompt with If-Match
```

The PUT endpoint validates size and step structure before the atomic write.
Task rename and prompt rename are handled by the task PATCH operation as one
registry transaction with repair information persisted before filesystem
changes.

### 12.3 Runs

```text
GET    /api/v1/schedule/runs/{runID}             run detail
POST   /api/v1/schedule/runs/{runID}/cancel      cancel managed run
POST   /api/v1/schedule/runs/{runID}/takeover    transfer session to user
POST   /api/v1/schedule/runs/{runID}/result      agent-scoped result report
```

The result endpoint requires the run token and accepts:

```json
{
  "status": "succeeded",
  "summary": "No regressions found.",
  "session": "archive"
}
```

It is idempotent for an identical payload and returns 409 for a conflicting
second result.

### 12.4 Live updates

```text
GET /api/v1/schedule/ws
```

The WebSocket carries task status, next-fire changes, run phase transitions,
step progress, block/resume state, result reports, and terminal outcomes. It
follows the existing pair and checklist subscription shape.

### 12.5 Representative errors

| Condition | HTTP result |
|-----------|-------------|
| Stale task revision or prompt ETag | `409 conflict` |
| Run now against archived task | `409 archived` |
| Message sent to scheduler-owned session | `409 takeover_required` |
| Takeover of terminal run | `409 run_terminal` |
| Result without valid run token | `401 invalid_run_token` |
| Result before final step | `409 result_not_allowed` |
| Unknown worktree, agent, or task | `404 not_found` |
| Auto-approve requested without acknowledgement | `400 acknowledgement_required` |
| Invalid cron, timestamp, prompt, or task body | `400 invalid_request` |

---

## 13. CLI

`trellis-ctl schedule` follows the existing JSON-capable CLI conventions:

```text
trellis-ctl schedule list [--archived]
trellis-ctl schedule show <name>
trellis-ctl schedule run <name> [--dry-run]
trellis-ctl schedule runs <name> [-n 20]
trellis-ctl schedule preview <name> [-n 5]
trellis-ctl schedule pause <name>
trellis-ctl schedule resume <name>
trellis-ctl schedule archive <name>
trellis-ctl schedule restore <name>
trellis-ctl schedule cancel <run-id>
trellis-ctl schedule takeover <run-id>
trellis-ctl schedule update <name> [schedule and target flags]
```

Human-facing mutation commands use the normal task revision checks. When
`TRELLIS_SCHEDULE_MANAGED=1` is present, official CLI mutation commands other
than `result` refuse to run and explain that managed sessions cannot modify
schedules.

### 13.1 Result command

Inside a scheduler-owned session:

```text
trellis-ctl schedule result \
  --status succeeded|failed \
  [--summary <text>] \
  [--session keep|archive|trash|delete]
```

The command takes no task or run name. It reads the current run ID and token
from the environment. `-json` returns the accepted immutable result record.

### 13.2 Run and history output

`runs` prints, at minimum:

```text
TIME  OUTCOME  DURATION  SKIP REASON  MODEL  SESSION  SUMMARY
```

This makes “why did the task not run?” answerable without opening the browser.
JSON output returns the complete typed records.

---

## 14. Events and Notifications

### 14.1 Events

Published on the existing event bus:

```text
schedule.task.created       {task_id, name}
schedule.task.updated       {task_id, revision}
schedule.task.paused        {task_id}
schedule.task.resumed       {task_id}
schedule.task.archived      {task_id}
schedule.task.restored      {task_id}
schedule.task.fired         {task_id, run_id, trigger_id, scheduled_for, initiated_by}
schedule.run.started        {task_id, run_id, session_id, agent, worktree}
schedule.run.step           {run_id, index, duration_ms}
schedule.run.blocked        {task_id, run_id, session_id}
schedule.run.resumed        {task_id, run_id, session_id}
schedule.run.result         {task_id, run_id, status, requested_session_action}
schedule.run.taken_over     {task_id, run_id, session_id}
schedule.run.skipped        {task_id, run_id, reason, missed_count}
schedule.run.interrupted    {task_id, run_id, session_id}
schedule.run.finished       {task_id, run_id, outcome, duration_ms}
schedule.run.disposition    {task_id, run_id, requested, applied, error}
```

Events describe persisted transitions; they are not the sole source of truth.
A missed event is repaired by reading registry and run state.

### 14.2 Notifications

Trellis sends a user notification for:

- a run blocked on approval;
- `failed` or `timed_out`;
- a skipped one-time occurrence;
- the first `model_unavailable` skip for a task revision/model pair;
- a session-disposition failure;
- a startup summary when one or more runs were marked `interrupted`.

Routine successful or neutral `ran` outcomes are silent. Recurring
`previous_run_active`, `concurrency_cap`, and `missed_while_down` skips remain
visible in history but do not generate a notification by default.

The run-specific result command does not automatically emit `notify.done` or
`notify.error`. A prompt may separately ask the agent to notify when that is
useful. General notifications are not used as evidence of schedule outcome.

---

## 15. Interaction With Other Trellis Features

### 15.1 Agent sessions

A scheduled session is an ordinary Claude or Codex session with additional
origin and ownership metadata. After terminal completion with disposition
`keep`, it behaves like any other interactive session.

While scheduler-owned:

- ordinary user messages require takeover;
- manual archive, trash, delete, reset, or move actions are rejected until the
  run is canceled, taken over, or terminal;
- approval responses remain available through the scheduler;
- session history and streaming use the existing agent UI.

If a session is later moved, trashed, restored, or deleted through ordinary
session controls, its run record remains intact and reflects current transcript
availability.

### 15.2 Worktrees

A task may target any currently discovered fixed worktree, whether or not that
worktree is the active service worktree. Switching Trellis's active worktree
does not move or cancel a scheduled session running elsewhere.

Removing a worktree is refused while it contains a scheduler-owned active run.
After all such runs are terminal, the worktree may be removed; tasks that still
target it remain configured and future occurrences become
`skipped / worktree_missing`.

The prompt is always read from the selected target worktree. The same task does
not follow the globally active branch.

### 15.3 Cases

A scheduled agent may create or update ordinary cases through supported
`trellis-ctl` commands. Cases require no special schedule record. A future
version may add a direct run-to-case evidence link.

### 15.4 Workflows, logs, traces, and crashes

Scheduled prompts may use the same `trellis-ctl` workflows, log queries, trace
commands, and crash reports available to an interactive agent. Their normal
validation and permission behavior applies.

A scheduled task does not acquire a special bypass around workflow input
validation, workflow confirmation, remote access, or other Trellis safeguards.
Prompts intended to run unattended must choose operations compatible with the
task's permission mode.

### 15.5 Pairing and checklist runs

A scheduled task creates one ordinary agent session. It does not directly start
a pair or phased-checklist run in this version.

Once the scheduled run is terminal or taken over, its session may be used by
other Trellis features under their ordinary rules.

### 15.6 Usage

Scheduled sessions are attributed to the selected worktree and appear in the
existing usage and cost views. The run record stores available usage totals at
completion when the agent manager provides them.

Version 0.2 does not impose task-level token or cost budgets.

### 15.7 Notifications

General `trellis-ctl notify` remains available to the agent and continues to
produce ordinary `notify.*` events. Notification attribution and schedule
outcome are separate concerns: only the run-specific result protocol determines
`succeeded` or explicitly reported `failed`.

---

## 16. Implementation Notes

### 16.1 Package layout

`internal/schedule/`:

```text
types.go        Task, Runtime, Run, Trigger, Target, Limits, enums
definition.go   prompt validation, fenced-code-aware step splitting, footer
cron.go         preset resolution, next-fire calculation, missed-fire scan
store.go        atomic task/runtime/run persistence and retention
registry.go     timers, occurrence claims, global slots, rehydration, archive
runtime.go      in-memory active-run and blocked-run supervision
driver.go       session creation, step loop, evaluation, disposition
result.go       run-token generation and result validation
```

There are intentionally no log-match, payload, fingerprint, storm-control, or
ephemeral-worktree packages in this version.

### 16.2 Scheduler timing model

The registry owns schedule calculation rather than handing execution directly
to the cron library. `robfig/cron/v3` is used to parse expressions and calculate
next times; Trellis performs the durable occurrence claim itself.

The registry maintains:

- one timer for the nearest next fire;
- a 30-second reconciliation ticker for wake and wall-clock-jump detection;
- a fake-clock interface used by all scheduler tests;
- a semaphore for `max_concurrent_runs`;
- a task-indexed map of active runtimes.

After each due-time scan, it persists runtime checkpoints only after all run or
aggregate missed records for that interval have been durably written.

No random or deterministic delay is introduced.

### 16.3 Extract the agent-control facade

The agent-agnostic facade currently used by pairing should move to
`internal/agentctl` and be extended for scheduled ownership and lifecycle.
Representative operations:

```go
type AgentRef struct {
    Agent     string
    SessionID string
    Worktree  string
}

type CreateSessionOptions struct {
    Agent          string
    Worktree       string
    WorkDir        string
    DisplayName    string
    Model          string
    PermissionMode string
    Origin         string
    Metadata       map[string]string
    Env            map[string]string
    Managed        bool
}

type SessionStatus struct {
    Generating        bool
    NeedsApproval     bool
    HasBackgroundWork bool
    LastTurnError     string
    ActiveViewers     int
}

func (a *Agents) CreateSession(ctx context.Context, opts CreateSessionOptions) (AgentRef, error)
func (a *Agents) ResolveModel(agent, requested string) (resolved string, err error)
func (a *Agents) ResolvePermissionPolicy(agent, mode string) (string, error)
func (a *Agents) SendUserMessage(ref AgentRef, prompt string) error
func (a *Agents) Status(ref AgentRef) (SessionStatus, error)
func (a *Agents) RespondToApproval(ref AgentRef, response ApprovalResponse) error
func (a *Agents) HardCancel(ref AgentRef) error
func (a *Agents) ArchiveSession(ref AgentRef) error
func (a *Agents) TrashSession(ref AgentRef) error
func (a *Agents) DeleteSession(ref AgentRef) error
func (a *Agents) TransferToUser(ref AgentRef) error
```

`SessionStatus` is the common source for quiescence. `ActiveViewers` counts
human session viewers, not internal subscribers.

Pairing and checklist call sites retain compatibility aliases during the move:

```go
package pair

type Agents = agentctl.Agents
type AgentRef = agentctl.AgentRef
```

### 16.4 Managed-session enforcement

Claude and Codex session managers persist origin metadata and enforce ownership
server-side.

While `Managed` is true:

- a normal user `message` is rejected with `takeover_required`;
- approval responses are routed through the schedule registry;
- reset, move, archive, trash, and delete are rejected;
- cancel and takeover remain available;
- result-token environment is supplied only to that session process.

On terminal completion, cancellation, takeover, timeout, or interruption, the
schedule registry clears managed ownership exactly once.

### 16.5 Result endpoint

The result token is generated before process launch and passed in the session's
environment. The endpoint validates:

- run ID and token;
- scheduler ownership;
- nonterminal state;
- final-step eligibility;
- status and disposition enums;
- summary size;
- first-write or identical-retry semantics.

The token is never returned by task or run-detail APIs and is redacted from
logs. Restart invalidates all outstanding tokens because all corresponding
runs become interrupted.

### 16.6 Completion supervision

The driver subscribes to session state changes and also polls at a modest
interval. A single helper shared with pairing implements the two-second stable
quiescence debounce.

Blocked runs stay registered with an event-only monitor. They do not consume an
active slot, but remain in the per-task active map. Approval forwarding first
reserves a slot and has priority over newly due occurrences.

### 16.7 Persistence and locking

The store mirrors existing Trellis JSON stores:

- write temporary file in the destination directory;
- fsync where existing state stores do so;
- rename atomically;
- never partially mutate a task record from browser input;
- keep config and runtime writes separate.

Occurrence-key uniqueness is enforced under the registry lock and verified by
the run store. One-time consumption has a startup repair path for a crash
between the run write and task/runtime write.

### 16.8 Application wiring

The schedule registry is initialized after worktree and agent managers, then
provided to API handlers and page handlers. Shutdown order is:

1. stop schedule timers and reject new occurrence claims;
2. persist runtime checkpoints;
3. stop other autonomous registries;
4. shut down agent managers.

Because version 0.2 always interrupts unfinished work after restart, shutdown
does not wait indefinitely for active scheduled sessions.

### 16.9 API and client surface

Schedule endpoints and types are added to:

- `internal/api/router.go`;
- `internal/api/handlers/schedule.go`;
- `api/openapi.yaml`;
- `pkg/client` as a typed `Schedules` sub-client;
- `cmd/trellis-ctl`.

The Go client exposes task CRUD-with-archive, prompt ETags, previews, manual
runs, history, cancellation, takeover, and the scoped result request.

The repository `SKILL.md`, generated documentation, and trellis.dev reference
material are updated with the human schedule commands and the managed-session
result protocol. The injected final-step footer remains authoritative for the
current run even when a repository carries an older skill file.

### 16.10 Frontend

Following existing per-feature conventions:

```text
views/schedules.qtpl
views/schedule_edit.qtpl
views/schedule_detail.qtpl
views/schedule_run.qtpl
static/js/schedule.js
static/css/schedule.css
```

Generated quicktemplate files are regenerated with the repository's standard
`qtc` command. Schedule tasks are added to the global navigation and command
palette.

The active agent-session view reuses existing approval, cancel, archive, trash,
and delete components, adding only managed-state, task-origin, and takeover
controls.

### 16.11 Dependency

Add:

```text
github.com/robfig/cron/v3
```

It is used for five-field parsing and next-time calculation, not as the durable
execution engine.

---

## 17. Tests

### 17.1 Schedule calculation

`internal/schedule/cron_test.go` covers:

- every preset;
- exact configured times with no stagger;
- the 30-second on-time fire grace for a continuously awake scheduler;
- startup and wake bypassing that grace and recording past slots as missed;
- five-field cron validation;
- minimum one-minute interval;
- local timezone previews;
- spring-forward nonexistent times;
- fall-back repeated local slots being claimed only once;
- occurrence-key determinism;
- backward wall-clock movement without duplicate claims.

### 17.2 Missed and one-time behavior

Using a fake clock:

- a daily task missed for four days creates one aggregate
  `missed_while_down` record and no session;
- waking before the next due time creates no catch-up run;
- pause, resume, restore, and material schedule edit reset the checkpoint to
  now;
- a one-time task fires once and disables itself;
- a manual run before a future one-time timestamp does not consume it;
- a one-time task skipped by concurrency, missing prompt, or unavailable model
  still disables itself;
- a one-time timestamp missed during downtime creates one skip, disables the
  task, and notifies;
- startup repair completes disablement after an injected crash between
  occurrence claim and task update.

### 17.3 Prompt definition

`internal/schedule/definition_test.go` covers:

- a plain one-step body;
- multiple `## Step` headings;
- preamble prepending;
- headings inside fenced code blocks not splitting steps;
- empty-step rejection;
- the 25 KiB limit;
- no YAML-frontmatter interpretation;
- final-step control-footer generation;
- immutable snapshot behavior after the source file changes;
- ETag conflict detection.

### 17.4 Registry and concurrency

`internal/schedule/registry_test.go` covers:

- every automatic occurrence leaves one run record;
- duplicate occurrence claims are idempotent;
- a prior running or blocked task produces
  `skipped / previous_run_active`;
- a busy taken-over session blocks until quiescent;
- a full global cap produces `skipped / concurrency_cap` immediately;
- no skipped occurrence is queued or retried;
- blocked runs release the global slot;
- an approved blocked run waits visibly and then reacquires capacity with
  priority;
- manual runs work while a task is disabled;
- archived tasks cannot run or dry-run;
- dirty fixed worktrees do not block either permission mode.

### 17.5 Driver lifecycle

`internal/schedule/driver_test.go` uses fake Claude and Codex managers to cover:

- session creation and session-ID persistence before prompt dispatch;
- one-step and multi-step prompt flow;
- authoritative quiescence including background work;
- clean completion without result becoming `ran`;
- explicit success and failure reports;
- an agent error overriding a prior success report;
- duplicate identical result calls and conflicting result calls;
- result rejection before the final step;
- immediate transition to `blocked` on approval request;
- approval and denial resuming the same run;
- timeout clocks pausing while blocked;
- step and run hard timeouts;
- cancellation;
- explicit takeover stopping later steps and suppressing disposition;
- normal completion releasing session ownership.

### 17.6 Restart

Restart tests persist a run in every nonterminal state:

```text
preflight
starting_session
running
blocked
evaluating
disposing
```

After registry rehydration each is terminal `interrupted`, receives no further
prompt, performs no disposition, retains its session reference, and releases
task and global locks. Outstanding result tokens are rejected.

### 17.7 Model and permissions

Tests cover:

- blank model resolving to the agent default;
- pinned available model recording requested and resolved values;
- pinned unavailable model producing a skip with no fallback;
- notification deduplication by task revision and requested model;
- `prompt` as default;
- the API acknowledgement requirement when selecting `auto_approve`;
- resolved agent-specific permission policy stored on the run;
- official schedule-mutation commands refusing managed-session environments.

### 17.8 Session disposition

Tests cover every requested action:

- omitted action defaults to keep;
- archive, trash, and delete after explicit result;
- no disposition after timeout, cancellation, takeover, or interruption;
- pending approval preventing disposition;
- active human viewer causing a permanent keep with recorded reason;
- disposition failure not changing the task outcome;
- run detail remaining valid after session deletion.

### 17.9 Persistence and API

Tests cover:

- atomic task, runtime, and run writes;
- task revision conflicts;
- prompt ETag conflicts;
- stale task PATCH not overwriting runtime state;
- task rename plus prompt-file rename and collision refusal;
- target-worktree changes retaining the old prompt file and requiring explicit
  resolution of the new target's prompt;
- archive preserving prompt, runs, and sessions;
- restore returning disabled and resetting missed-fire checkpoint;
- run retention pruning only terminal records;
- result-token authorization and redaction;
- server-side message rejection before takeover.

### 17.10 End-to-end

At minimum:

1. Create a disabled manual two-step task, dry-run it, run it manually, report
   success with `--session archive`, and find the transcript in archive search.
2. Run a prompt-mode task that requests permission, approve it from the inbox,
   and verify the same run resumes and finishes.
3. Start a multi-step task, take over during the first turn, and verify no later
   scheduled step or automatic disposition occurs.
4. Restart Trellis during a running task and verify the run becomes
   `interrupted`, the session is kept, and no prompt is resent.
5. Schedule three simultaneous tasks with a global cap of two and verify two
   start while the third records `concurrency_cap` without retry.

---

## 18. Non-Goals and Deferred Features

The following are explicitly outside version 0.2:

- log-match triggers, log payload templating, fingerprinting, coalescing, rate
  limits, and storm breakers;
- event-bus, API, GitHub, or cloud triggers;
- more than one trigger or more than one schedule per task;
- catch-up execution of missed schedule times;
- per-task overlap queues or global capacity queues;
- automatic schedule stagger or jitter;
- ephemeral worktrees, scheduled branches, automatic pushes, and worktree
  reaping;
- per-task saved permission allow-rules;
- scheduler self-modification by an agent;
- inference of success from final prose, completion strings, or `notify.*`
  events;
- deterministic post-run verification workflows;
- task-level token or cost budgets;
- unopened-run or unreviewed-run warnings;
- automatic deletion of task configuration, prompt files, run history, or
  sessions when a task is archived;
- a task that directly starts pairing or phased-checklist drivers;
- multi-agent scheduled runs;
- slash commands as scheduled prompts;
- remote or cloud execution while Trellis is not running on the target machine.

The task schema's trigger array and the shared agent-control facade preserve a
path to add these capabilities without weakening the deliberately conservative
first-release semantics.

---

## 19. First-Release Behavioral Summary

For implementation review, the governing rules are:

1. One task has one clock schedule and one fixed worktree.
2. Configured times are exact; Trellis adds no stagger.
3. Missed times are recorded and never executed late.
4. A prior active run or a full global cap causes an immediate visible skip.
5. There are no queues.
6. Prompt mode blocks and resumes after approval while the same Trellis process
   remains alive.
7. Ordinary user interaction requires explicit takeover.
8. A restart interrupts every nonterminal run and never resends work.
9. Both prompt and auto-approve permission modes are supported; dirty
   worktrees are allowed.
10. Agents cannot use the supported scheduled-run capability to modify task
    configuration.
11. Only `trellis-ctl schedule result` can affirmatively report success or
    failure; otherwise a clean completion is `ran`.
12. The result may request session keep, archive, trash, or delete; the default
    in every unspecified or abnormal case is keep.
13. A pinned unavailable model causes a visible skip with no fallback.
14. One-time tasks consume and disable themselves even when skipped.
15. Tasks are archived, not deleted, and all associated evidence is preserved.
16. No heuristic attempts to decide whether the user is reviewing the runs.

*End of specification.*
