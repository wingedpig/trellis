# Scheduled Task Specification

**Version:** 0.3

**Status:** Specification for Implementation

**Supersedes:** [SCHEDULE_SPEC_v0.2.md](SCHEDULE_SPEC_v0.2.md)

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
- after a Trellis restart, unfinished execution is interrupted rather than
  guessed-at or replayed; a durably committed outcome is preserved;
- duplicate-prevention records survive run-history pruning;
- permission waits release capacity and pause limits only when all agent work
  is authoritatively suspended;
- sessions are kept unless the agent explicitly requests another disposition,
  and operational failures always suppress automatic disposition.

This version deliberately implements **clock scheduling only**. Log-match,
event, API, and GitHub triggers are deferred to later versions.

Version 0.3 resolves five implementation gaps in version 0.2: durable occurrence
accounting independent of history retention, approval waits with concurrent
background work, restart during session disposition, failure before the final
step, and ownership enforcement across all execution controls. These changes
do not expand the trigger or worktree scope.

---

## 2. Scope and Terminology

### 2.1 Task

A **task** is the durable configuration: its name, prompt file, schedule,
target worktree, agent, model, permission mode, and limits. It can create many
runs over its lifetime.

### 2.2 Trigger

A **trigger** describes when the task should run. The schema retains a trigger
array for future compatibility, but version 0.3 requires exactly one trigger
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
automatic occurrence initially produces a run record or is represented in one
aggregate `missed_while_down` record. History retention may later remove the
detailed run, but never the accounting evidence needed to prevent replay.

### 2.5 Scheduler ownership

A session created by a run is **scheduler-owned** until finalization finishes
or the user explicitly takes it over. Execution outcome and ownership are
separate: a completed run retains ownership briefly while applying its recorded
session disposition. While scheduler-owned, ordinary messages and changes to
execution controls are rejected. Approval responses and cancellation are
available while execution remains unfinished.

### 2.6 Execution completion and finalization

**Execution completion** is the durable commit of the run's outcome. Once
committed, that outcome cannot be rewritten by restart or a disposition error.
**Finalization** applies or suppresses session disposition, releases ownership
and capacity, and changes the run state to `done`.

A run in `disposing` has a committed outcome but is not yet fully finalized.
A run in `done` is terminal for lifecycle and retention purposes, subject to
the busy taken-over session exception in §9.1.

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
- whether a one-time occurrence has been consumed;
- the current timer generation, used to reject stale callbacks.

Compact occurrence receipts are stored separately from runtime caches and
retained history (§11.2). They survive pruning, pause, archive, and restart.

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
target, model, permission mode, and timeout limits come from the task snapshot
captured at claim. Editing, pausing, or archiving the task does not rewrite an
already claimed run.

### 3.7 Run-control footer

Trellis appends a small, visible run-control footer to **every** step. It
identifies the current step and total step count, states that the session is
scheduled, and describes the result command:

```bash
trellis-ctl schedule result \
  --status succeeded \
  --summary "Completed the review" \
  --session archive
```

The footer states that:

- `--status failed` may be reported during any active step when the task cannot
  be completed; accepting it prevents dispatch of all remaining steps;
- `--status succeeded` is accepted only during the final step, after all
  requested work is complete;
- reporting a result does not stop current work; Trellis still waits for
  quiescence, subject to the execution limits;
- `--summary` is a concise human-readable result;
- `--session` is optional and defaults to `keep`;
- without a valid result command, a cleanly completed run is recorded as
  `ran` and its session is kept.

The footer is shown by prompt preview and stored in each sent-message snapshot.
It is not written into the repository Markdown file.

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

Version 0.3 validates that the array contains exactly one schedule trigger. The
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
task ID + trigger ID + nominal local calendar slot + local zone identity
```

The zone identity is the resolved location identity, not its seasonal UTC
offset or abbreviation. The trigger ID is stable across edits to its schedule.
A material schedule edit invalidates old timers but does not erase previously
accounted slots for that trigger.

Each ambiguous local slot is resolved to its **first** UTC occurrence before
claim or missed-range lookup. Both appearances during fall-back therefore
identify the same slot and canonical instant. This applies even when the first
appearance was skipped, represented by a missed aggregate, or pruned from
detailed history. The run separately records the actual fire instant.

For a `once` trigger, the key uses the task ID, trigger ID, and absolute
timestamp. Manual runs use a random occurrence ID and set
`initiated_by: "manual"`.

Occurrence receipts and missed-range receipts enforce uniqueness independently
of retained run records (§11.2). An already accounted occurrence is an
idempotent no-op: it creates neither a session nor another skip row. Pruning
history, resetting a checkpoint, or moving the wall clock backward never
clears this protection.

Timers carry a generation that is incremented on enablement changes or material
schedule changes. A callback must revalidate that generation and its due slot
under the registry lock before claim. An obsolete callback is discarded; it
does not claim a slot from the replacement schedule. The occurrence key itself
does not include this timer generation.

### 4.6 Missed occurrences

Trellis never executes a scheduled occurrence late.

The registry maintains a durable `last_evaluated_at` checkpoint for every
enabled automatic task. It also runs a 30-second reconciliation tick to detect
sleep or large wall-clock jumps. On startup or wake, Trellis calculates the
schedule instants in the interval `(last_evaluated_at, scan_through]`, using a
fixed scan boundary and trigger snapshot.

Already accounted occurrences are excluded from the missed count. If one or
more unaccounted instants were missed, Trellis writes one aggregate run record:

```text
skipped / missed_while_down
```

The record contains:

- `missed_count`;
- `missed_from` and `missed_through`;
- the evaluated interval, including its exclusive start and inclusive end;
- the task revision and trigger snapshot used for the calculation;
- `missed_count_truncated` if the fixed 10,000-candidate enumeration cap
  prevented an exact count.

An exact count uses the first and last missed occurrence for the displayed
range. When truncated, the count is a lower bound, `missed_through` is the scan
boundary rather than a claimed exact last occurrence, and the UI explicitly
labels the count and range accordingly. The accounting receipt covers the
**entire evaluated interval**, including its unenumerated tail. Applying the
stored trigger snapshot to that interval determines coverage; truncation never
makes the tail eligible to execute later.

The aggregate has a durable receipt containing a fixed run ID, interval,
trigger snapshot, count, and initial run data. Startup repairs this receipt and
its checkpoint before starting another scan. A crash after the aggregate write
but before checkpoint advancement reuses the same receipt and run ID, rather
than emitting an overlapping aggregate (§11.2).

A pending timer is also classified as missed when wake detection shows that the
previous reconciliation tick was not recent, even if the process receives the
timer callback immediately after waking. The 30-second normal fire grace from
§4.4 applies only while Trellis has remained awake and responsive.

No catch-up session is created. Trellis waits for the next future occurrence.
When a task is newly enabled, resumed, restored, or materially rescheduled, its
checkpoint is reset to the current time so old schedule slots are not treated
as newly missed. These changes first finish any already committed accounting
transaction, invalidate old timers, and preserve all existing receipts.

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

Version 0.3 supports only an existing, named worktree. A scheduled run operates
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

Official Trellis clients reject schedule-mutation commands while the current
session is scheduler-owned, resolving server-side ownership as described in
§13 rather than relying solely on a possibly stale environment marker. The task
prompt may recommend a future configuration change, but a user must apply it.

`auto_approve` is still broad local authority, so this is a product and API
capability boundary—not a claim that an unrestricted same-machine shell is a
complete security sandbox.

---

## 6. Run Lifecycle

### 6.1 State machine

```text
(trigger/manual request)
          │
          ▼
      preflight ── rejection ──────────────────────────► done / skipped
          │
          ▼
   starting_session ── startup error ──────────────────► done / failed
          │
          ▼
       running ◄──── approvals resolved ────── blocked
          │         permission request ────────► │
          │                                      │
          ├── all steps quiescent ────────────────┤
          ├── reported failure, then quiescent ───┤
          └── any step operational error ────────┤
                                                 ▼
                                             evaluating
                                                 │
                                   durable outcome commit
                                                 ▼
                                             disposing
                                                 │
                                                 ▼
                                               done

Before outcome commit:
  active-time limit ──────────────────────────────────► done / timed_out
  explicit takeover ──────────────────────────────────► done / taken_over
  cancellation ───────────────────────────────────────► done / canceled
  restart ────────────────────────────────────────────► done / interrupted

After outcome commit:
  restart ── reconcile/suppress disposition ───────────► done / same outcome
```

The persisted `state` describes the lifecycle phase. `outcome` is absent until
execution completion is durably committed. Evaluation commits the outcome,
`execution_finished_at`, the requested disposition, and the transition to
`disposing` in one atomic run write **before any disposition side effect**.
The committed outcome is immutable. `done` adds `finished_at` after
finalization. Paths with no disposition may commit directly to `done`.

`blocked` describes pending approval or capacity-gated approval responses; it
does not imply that all foreground or background execution has stopped. Slot
ownership and active-time accounting are explicit orthogonal fields (§6.6).

### 6.2 Claim before side effects

When a schedule is due, Trellis atomically claims its occurrence key through
the accounting store, then creates the associated run record in `preflight`
before creating a session or sending a prompt. The durable receipt reserves
the run ID and contains enough initial data to repair a missing run file.
Both writes must complete before execution is allowed (§11.2).

For a one-time task, consuming and disabling the task occurs under the same
registry lock as occurrence claim. If separate atomic file writes are
interrupted, startup uses the receipt as the source of truth and completes
disablement without creating a second occurrence. The claimed run continues
preflight using its captured eligibility; its own one-time disablement does
not reject it as `task_disabled`.

Task configuration is captured at claim; prompt bytes are added during
preflight. Later task edits cannot change the claimed run. A storage failure
prevents subsequent side effects and checkpoint advancement, is reported
visibly, and leaves repairable accounting state.

### 6.3 Preflight

Preflight checks are performed in this order:

1. Validate eligibility at claim: an automatic occurrence requires an enabled,
   non-archived task and a current timer generation. A manual **Run now** is
   allowed for a disabled task but not an archived task. Later pause, archive,
   or one-time consumption does not revoke an accepted claim.
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
schedule_receipt_id
schedule_occurrence_key
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
- it has no pending permission or approval request or queued approval response;
- it has no agent-managed background work still running.

The scheduler uses `session.state_changed` events for responsiveness and
periodic status reconciliation to heal a missed event. The agent-control facade
must expose authoritative quiescence rather than inferring completion solely
from the last text or foreground process. Status and completion/error events
identify the dispatched turn so a stale event cannot complete the next step.

The driver records per-step start, quiescent, and active-duration timestamps,
turn identity, operational error, and a bounded final-response excerpt. It
never reparses the prompt file between steps.

An operational error in **any** step is latched on the run, prevents all later
step dispatch, and produces `failed`. This includes creation, dispatch,
transport, and agent-turn failures. Later status resets cannot erase the error.
The driver hard-cancels any remaining agent work before releasing ownership.
An individual tool's nonzero exit is not automatically a turn failure; the
agent may handle that result as part of its task.

An accepted explicit `failed` result also prevents all later step dispatch.
The current step is allowed to become quiescent under the normal limits, then
the driver evaluates the run. Otherwise, after a clean step, the next step is
sent only if no result, error, cancellation, takeover, or outcome commit has
made it ineligible. The final check is serialized with those transitions.

### 6.6 Blocked approvals

An approval request makes the run visibly `blocked` immediately, and the
session appears in the inbox as `needs_approval`. The run stays active for
per-task overlap. Approval state alone does **not** release its execution slot
or pause its step/run clocks: another tool or background agent may still work.

The manager exposes `execution_suspended` only when it can authoritatively
guarantee that **all** foreground and agent-managed background execution is
stopped and cannot resume without the scheduler's capacity gate. A pending
approval count, a foreground `Generating` flag, or an empty recent event stream
is insufficient proof. If the integration cannot provide that guarantee, the
run retains its slot and its active-time clocks continue. The UI explains
whether approval is pending while execution is active or fully suspended.

Only after confirmed full suspension does the registry release the global slot
and pause both clocks. A fully suspended approval wait has no automatic expiry.
A partially blocked run can still reach an active-time timeout.

Several requests may be pending concurrently. Each has a stable request ID and
its own state: `pending`, `response_recorded`, `forwarded`, or `withdrawn`.
Approvals and denials use the same capacity rules:

1. Validate the request ID and persist the user's response before forwarding.
   An identical response retry is idempotent; a conflicting response is 409.
2. If the run already holds a slot, forward the response through its manager.
   Other requests may remain pending, so its visible state may remain blocked.
3. If the slot was released, retain the response with
   `waiting_for_capacity: true`. Responses ready to resume existing runs have
   priority over newly due occurrences, in FIFO order of readiness.
4. Reserve exactly one slot, resume active-time accounting, and authorize the
   manager to resume execution before forwarding any queued response. Multiple
   responses for the same run share its one slot.
5. Return to `running` when no unresolved requests or queued responses remain;
   otherwise retain the blocked presentation with accurate execution state.

The manager must also gate a resume caused by request withdrawal or another
agent event after it has reported full suspension. It cannot let work restart
outside the scheduler's slot reservation. Integrations without such a gate
must use the conservative slot-retaining behavior above.

Withdrawn requests invalidate any unforwarded response. Cancellation, takeover,
or restart also prevents queued scheduler responses from being forwarded; a
takeover leaves live agent requests available for the user to answer anew.
Loss of the agent process while blocked is an operational failure.

Blocked runs receive periodic status reconciliation as well as events. A
dropped event must not strand a run, queued response, or occupied slot. Approval
and denial are part of the managed run and do not constitute takeover.

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

Takeover is accepted only before execution outcome commit and after a session
exists. It is serialized with step dispatch, response forwarding, and outcome
commit. Any queued, unforwarded approval responses are invalidated.

A taken-over session that is still generating, waiting for approval, or running
agent-managed background work counts as an active prior run for that task.
The registry keeps monitoring it until its first authoritative, two-second
quiescence and durably records `overlap_released_at`. Later interactive turns
do not reacquire the old task's overlap lock. This monitoring and the run record
survive restart and cannot be removed by history pruning while still needed.

### 6.8 Timeouts

Two limits bound active execution:

```text
step_timeout: 30m
run_timeout:  2h
```

Both use accumulated execution time, rather than CPU usage. The run clock
starts when the execution slot is reserved for session creation; a step clock
starts on dispatch. The clocks continue through ordinary I/O, debounce waits,
and partially blocked execution. They pause only during the authoritative full
suspension described in §6.6, including a fully suspended wait to reacquire
capacity. The run clock ends at execution outcome commit; disposition has its
own bounded manager operation and cannot consume the task's execution budget.

Exceeding either limit:

- invokes an agent-specific hard cancellation that stops foreground and
  background work;
- records `timed_out` and whether the step or run limit fired;
- invalidates the result token;
- keeps the session;
- sends no later steps and performs no automatic disposition.

### 6.9 Cancellation

The user may cancel any run whose execution outcome has not been committed,
including `preflight`, `starting_session`, `running`, `blocked`, or
`evaluating`. Trellis prevents later dispatch and response forwarding,
hard-cancels agent work when present, records `canceled`, invalidates the result
token, keeps the session, and releases scheduler ownership. If session creation
is in flight, its eventual session is retained and canceled without receiving
a prompt. No execution slot is released while uncanceled work is still active.

Cancellation and takeover race with outcome commit under the same run
synchronization. Once the outcome is committed, both return
`409 execution_completed`; they cannot rewrite an outcome or race a committed
disposition operation. Takeover before session creation returns
`409 session_not_started`.

Cancellation is separate from task pause or archive. Pausing or archiving a
task prevents future automatic fires but does not cancel a run already claimed.

### 6.10 Restart behavior

Startup distinguishes unfinished execution from unfinished finalization:

| Persisted condition | Startup action |
|---------------------|----------------|
| No committed execution outcome | Record `done / interrupted`; keep any session and send no further prompts. |
| Committed outcome with incomplete disposition/finalization | Preserve that outcome; reconcile or suppress disposition as described in §8, then finalize. |
| Fully finalized run | Preserve it, including any busy taken-over session's overlap monitor. |

Trellis never resends a possibly delivered prompt, continues later steps,
forwards a queued approval response, or evaluates an uncommitted old result.
All outstanding run-result tokens are invalidated.

For interrupted execution:

- the session and worktree are retained;
- scheduler ownership is released so the session can be continued manually;
- the original task remains eligible for its next future occurrence;
- no catch-up run is created.

No disposition may begin before the outcome commit, so an interrupted execution
cannot already have deleted its session through scheduler disposition. A run
whose execution finished and committed before a crash remains `succeeded`,
`failed`, or `ran`, even if cleanup was unfinished. Startup reports uncertain
cleanup explicitly and never repeats an uncertain irreversible action.

This policy favors avoiding duplicate side effects over recovering unattended
execution after a server restart.

---

## 7. Completion and Outcome

### 7.1 Outcomes

Committed execution outcomes are:

| Outcome | Meaning |
|---------|---------|
| `succeeded` | The agent explicitly reported success for this run and no later agent error invalidated it. |
| `failed` | The agent explicitly reported failure from any step, any agent turn failed, or Trellis failed after accepting execution. |
| `ran` | All steps became quiescent without an operational error, but no explicit result was reported. |
| `skipped` | Preflight or schedule policy prevented session creation. |
| `timed_out` | A step or run exceeded its active execution limit. |
| `canceled` | A user canceled the active run. |
| `taken_over` | A user explicitly transferred the session from scheduler control to interactive control. |
| `interrupted` | Trellis restarted before execution outcome commit. |

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

- the run is scheduler-owned and has no committed execution outcome;
- a dispatched step is active;
- for `succeeded`, that step is the final step; `failed` is allowed in any step;
- the supplied token matches that run.

The first accepted result is authoritative. Repeating the identical request is
idempotent. A conflicting later request returns HTTP 409 and does not modify
the recorded result.

Reporting a result does not itself terminate the turn. An accepted `failed`
result immediately makes later steps ineligible. The driver waits for the
current step to become quiescent, then evaluates the run. If a timeout,
cancellation, takeover, or operational error intervenes before outcome commit,
the precedence rules below apply.

### 7.3 Outcome precedence

At evaluation, outcome is selected in this order:

1. A scheduler terminal condition already recorded—`skipped`, `timed_out`,
   `canceled`, `taken_over`, or `interrupted`—wins.
2. A latched session creation, dispatch, transport, or **any-step** agent-turn
   error produces `failed`.
3. A valid reported status of `failed` produces `failed`.
4. A valid reported status of `succeeded` produces `succeeded`.
5. Otherwise the outcome is `ran`.

A `succeeded` report followed by an agent or transport error is therefore
`failed`. Session-disposition failure is recorded separately and does not
rewrite the task outcome.

Each run stores `outcome_source`, such as `agent_result`, `agent_error`,
`preflight`, `scheduler_timeout`, or `clean_without_result`. Any operational
error forces session disposition to `keep`, even when an earlier valid result
requested archive, trash, or delete. An explicit agent-reported failure without
an operational error may still request a disposition.

These rules select the outcome once, at durable execution completion. No later
cleanup failure, restart, or user action changes it.

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
and valid only while the run is scheduler-owned and execution is unfinished.
It is not a general Trellis API credential. The server keeps only the material
required to validate it and invalidates it at execution outcome commit, takeover,
cancellation, timeout, or restart. It is already invalid during disposition.

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

### 8.1 Eligibility

The default is always `keep`. It applies when:

- `--session` is omitted or no result command is submitted;
- the outcome is `skipped`, `timed_out`, `canceled`, `taken_over`, or `interrupted`;
- **any** operational error occurs before execution outcome commit, including
  one occurring after a valid result report.

An explicit agent-reported `failed` result without an operational error may
request archive, trash, or delete. The original request is retained even when
policy overrides it with `keep`, together with the suppression reason.

A non-`keep` request is applied only after the durable outcome commit and when:

- the session is quiescent, with no pending approval or queued response;
- scheduler ownership has not been transferred;
- no human WebSocket viewer is currently attached to that session.

Internal scheduler subscriptions do not count as human viewers. The manager
checks ownership, viewers, and quiescence atomically with acquiring its
disposition operation guard. A viewer attaching before the operation is
reserved suppresses it. A viewer arriving after reservation sees the operation
in progress and, if necessary, the final transcript availability.

If a viewer prevents disposition, Trellis keeps the session and records
`skipped_reason: "active_viewer"`. It does not retry after the page closes.

### 8.2 Durable operation boundary

Evaluation atomically persists the outcome, `execution_finished_at`, requested
action, and `disposition.status: "pending"` before entering `disposing`.
The result token is invalidated at that commit. Scheduler ownership and its
execution slot remain reserved until finalization, preventing concurrent
session mutation while the disposition decision is applied.

Before calling a non-`keep` manager operation, Trellis durably records
`status: "attempting"` and a stable operation ID. Afterward it records the
applied action or error. Disposition operations have a bounded deadline; an
operation with uncertain completion is `unknown` and must not be retried
automatically. Ownership is not released until the manager guarantees that the
operation can no longer mutate the session; if that guarantee cannot be
established, the run remains visibly finalizing and notifies the user.

The statuses are `pending`, `attempting`, `applied`, `skipped`, `failed`, and
`unknown`. A normal keep records `applied: "keep"`. A suppressed request records
`status: "skipped"`, `applied: "keep"`, and its reason. A confirmed failed
operation records its error and actual session state; it never assumes that a
partially completed operation rolled back.

### 8.3 Restart during disposition

Startup preserves the committed execution outcome and performs read-only
reconciliation against the agent manager's persisted session/transcript state:

- `pending`: no side effect was allowed to start. Suppress the request with
  `skipped_reason: "restart_before_disposition"` and keep the session.
- `attempting`: the side effect may already have completed. If authoritative
  state establishes the requested effect, record it as applied with
  `reconciled_after_restart: true`. Otherwise record `unknown`, the observed
  state, and an explanatory error. Do not repeat the operation.
- `applied`, `skipped`, or `failed`: preserve the recorded result and reconcile
  current transcript availability.
- `unknown`: preserve the uncertainty, with updated observed state if available.

For example, deletion followed by a crash before its completion write leaves
the run's committed `succeeded` outcome intact. A confirmed absent session and
transcript can establish applied deletion. An unavailable or corrupt manager
store cannot establish it and must be reported as unknown. Startup cannot
promise that an already deleted session will be retained.

After reconciliation and after old manager operations are known to have
stopped, finalize the run and release ownership. No session disposition is
newly initiated during startup. Errors and uncertain results notify the user
without rewriting the execution outcome.

### 8.4 History and ownership release

The run retains the session ID and requested/applied disposition even when the
session is later moved or deleted. A missing transcript is shown as unavailable
rather than breaking run history.

After finalization, a kept session becomes an ordinary interactive session and
requires no takeover. Its model, permissions, messages, and other controls are
again governed by the ordinary session rules.

---

## 9. Concurrency and Skip Records

### 9.1 Per-task overlap

Version 0.3 has one overlap policy: **skip**.

If a task occurrence arrives while a prior run of that task is active, the new
occurrence becomes:

```text
skipped / previous_run_active
```

No pending occurrence is retained. When the prior run later finishes, Trellis
waits for the next regularly scheduled time or an explicit **Run now**.

For overlap purposes, a prior run is active while it is in `preflight`,
`starting_session`, `running`, `blocked`, `evaluating`, or `disposing`. A run
that was taken over also blocks a new occurrence until its session first
becomes authoritatively quiescent, including all background work and approval
requests. Retention cannot remove a run whose overlap monitor is still needed.

### 9.2 Global concurrency

`schedules.max_concurrent_runs` defaults to two. It limits scheduler-owned runs
that currently hold an execution slot.

A run normally holds a slot from immediately before session creation through
evaluation and disposition. It releases the slot during an approval wait only
after full execution suspension is guaranteed (§6.6), and reserves one before
any execution can resume. Partially blocked runs retain their slots. Multiple
pending approvals on a run never reserve multiple slots.

If a new occurrence reaches the dispatch gate while all slots are occupied, it
becomes:

```text
skipped / concurrency_cap
```

There is no queue for new occurrences and no later retry. An explicit manual
run is subject to the same cap. Capacity-gated responses for already existing
runs are retained as specified in §6.6; this is not a queue of new occurrences.

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
| `task_disabled` | A claim request failed its enabled-state eligibility check; no previously accepted claim is revoked. |

Invalid schedule expressions and structurally invalid task records are rejected
when saved rather than becoming recurring skip reasons.

Every new skipped occurrence has its own persisted run record, except that
several consecutive missed schedule times are intentionally aggregated into one
`missed_while_down` record. Skip rows appear inline in run history with their
reason and relevant context, subject to configured history retention. Duplicate
or obsolete timer callbacks do not themselves create new occurrences or rows.

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
- the generated step-specific run-control footer on every message;
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

Archiving is the only removal action in version 0.3.

### 10.5 Run history and detail

Run history is reverse chronological. Each row contains:

- scheduled time or manual initiation time;
- actual fire and start time;
- wall-clock duration and accumulated active execution time;
- state and committed outcome, including a distinct **Finalizing** state;
- skip reason when applicable;
- requested and resolved model;
- session ID and availability;
- explicit result summary;
- requested and applied session disposition, including unknown cleanup status.

The run detail is a timeline of occurrence claim, preflight, session creation,
each step, individually identified approval requests and responses, execution
suspension and resumption, result report, outcome commit, and session disposition. It exposes the immutable task, trigger, prompt, target,
and permission snapshots used by that run.

`ran` is rendered neutrally with the explanation:

> Completed without an operational error, but the agent did not explicitly
> report whether the requested work succeeded.

### 10.6 Managed session UI

A scheduler-owned session displays:

- the originating task and run;
- **Managed by scheduled task** status;
- current step;
- approval controls with each request's response/withdrawal state;
- whether other work is active or all execution is suspended;
- `waiting_for_capacity` when recorded responses are not yet forwarded;
- **Cancel run** and **Take over** while execution is unfinished;
- **Finalizing** and disposition status after outcome commit.

The ordinary composer remains disabled until takeover or finalization. Model,
permission, reset, move, archive, trash, delete, and pair/checklist attachment
controls also require ownership release. Approving or denying a tool request
does not transfer ownership.

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

### 11.1 State layout and durability

```text
.trellis/
  schedule-tasks/<task-id>.json
  schedule-runtime/<task-id>.json
  schedule-occurrences/<task-id>/<receipt-id>.json
  schedule-runs/<run-id>.json
```

All state writes use a temporary file in the destination directory, flush and
fsync that file, rename atomically, and sync the containing directory before
reporting the commit complete. This requirement is stronger than merely using
write-and-rename in an existing store. If the platform cannot provide the
required durability operation, startup must report that limitation instead of
silently claiming the same guarantee. Filesystem or hardware behavior outside
its documented persistence guarantees is not covered.

Registry locks serialize receipt creation, task eligibility, overlap checks,
one-time consumption, checkpoint advancement, and slot allocation. Per-run
synchronization serializes message dispatch eligibility, response forwarding,
outcome commit, cancellation, and takeover. No external side effect is allowed
to cross a failed persistence boundary.

### 11.2 Occurrence accounting independent of history

An occurrence receipt is durable, compact accounting data, not user-visible
run history. Each automatic or manual claim has a receipt reserving its run ID.
A missed aggregate has one receipt covering a fixed interval and schedule
snapshot. Receipts contain no transcript or prompt body and are **not** subject
to `keep_runs`.

A representative single-occurrence receipt is:

```json
{
  "schema": "trellis.schedule.receipt.v1",
  "id": "occ_01J...",
  "kind": "occurrence",
  "task_id": "task_01J...",
  "occurrence_key": "sha256:...",
  "run_id": "run_01J...",
  "initiated_by": "schedule",
  "task_revision": 7,
  "trigger_id": "trg_01J...",
  "nominal_local_slot": "2026-11-01T01:00",
  "zone_identity": "America/Los_Angeles",
  "canonical_scheduled_for_utc": "2026-11-01T08:00:00Z",
  "claimed_at": "2026-11-01T08:00:00.120Z",
  "consumes_once": false,
  "history_pruned": false,
  "initial_run": { "state": "preflight", "task_snapshot": {} }
}
```

`initial_run` contains the complete minimum data needed to reconstruct the
claimed run, including its task/trigger/target/limit snapshot. The abbreviated
object above omits those fields for readability. Manual receipts do not consume
one-time schedules. A receipt for a one-time missed occurrence does.

Claim and recovery follow this order:

1. Under registry synchronization, check the receipt index for a matching key
   or covering missed interval. If found, claim is an idempotent no-op.
2. Durably create a receipt with a fixed run ID and initial run data.
3. Durably create that run's `preflight` record. Only then may normal execution
   proceed. An execution-claim receipt without a run file is repaired on startup
   using its same run ID and marked interrupted without starting execution.
   A missed-range receipt instead repairs its original `done / skipped` aggregate.
4. Advance runtime checkpoints only after all accounting and initial run writes
   for the evaluated interval have completed.

A missed-range receipt additionally stores the exclusive interval start,
inclusive scan boundary, immutable trigger/zone snapshot, exact or lower-bound
missed count, and initial `done / skipped` aggregate run. Its receipt ID and run
ID are fixed before either file is written. It covers schedule slots selected
by that snapshot whose canonical instants are in the interval; lookup still
normalizes repeated local slots to their first occurrence. Individually
accounted slots are excluded from the aggregate count. For truncated scans,
the receipt covers the entire interval, not only the enumerated candidates.

Recovery repairs missing initial run files, completes one-time consumption,
and applies unfinished aggregate checkpoint updates **before another scan can
choose new boundaries**. Checkpoints advance only across fully accounted
intervals, never by taking the maximum timestamp of arbitrary receipts. Thus a
crash between receipt, run, and checkpoint writes cannot replay work or count
the same missed interval again. Task edits and lifecycle changes serialize with
this repair and preserve its stored snapshot.

Before pruning a run, the store durably marks its receipt `history_pruned: true`.
Only then may it remove the run file. A crash between these operations leaves a
harmless extra history file that pruning may finish removing. Recovery never
recreates a run whose receipt records intentional pruning, and a retained
session linked to that receipt is not treated as an orphaned interrupted run.

Receipts survive archive, restore, and history pruning. Version 0.3 does not
garbage-collect them or expose a receipt-deletion API. They grow with accounted
occurrences, but store only claim metadata. A future compaction must preserve
duplicate prevention, missed-range coverage, intentional-pruning information,
and session linkage before it may remove individual receipts.

### 11.3 Task record

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

The first release still omits worktree modes, base branches, overlap selection,
success strings, task-level teardown, log rules, and review-engagement heuristics.

### 11.4 Runtime record

```json
{
  "schema": "trellis.schedule.runtime.v1",
  "task_id": "task_01J...",
  "timer_generation": 3,
  "last_evaluated_at": "2026-09-01T12:15:00-07:00",
  "next_fire_at": "2026-09-02T09:00:00-07:00",
  "last_run_id": "run_01J...",
  "last_outcome": "ran",
  "one_time_consumed_at": null,
  "model_unavailable_notice_key": "",
  "updated_at": "2026-09-01T12:15:00-07:00"
}
```

`next_fire_at` is a cache, recomputed when task, local location, or process state
changes. `last_evaluated_at` is a durable evaluation boundary backed by receipt
coverage. A timer-generation change does not invalidate occurrence receipts.

### 11.5 Run record

Representative fields for a finalized run are:

```json
{
  "schema": "trellis.schedule.run.v1",
  "id": "run_01J...",
  "receipt_id": "occ_01J...",
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
  "execution_finished_at": "2026-09-02T09:12:30.000-07:00",
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
    "permission_mode": "prompt",
    "resolved_permission_policy": "..."
  },
  "limits": { "step_timeout_s": 1800, "run_timeout_s": 7200 },
  "session": {
    "agent": "claude",
    "id": "...",
    "display_name": "nightly-review · 2026-09-02 09:00",
    "ownership": "released",
    "taken_over_at": null,
    "overlap_released_at": null
  },
  "execution": {
    "holds_slot": false,
    "execution_suspended": false,
    "waiting_for_capacity": false,
    "active_duration_ms": 749800
  },
  "approvals": [],
  "steps": [
    {
      "index": 0,
      "turn_id": "...",
      "sent_message": "...including this step's control footer...",
      "prompt_sha256": "...",
      "started_at": "...",
      "quiescent_at": "...",
      "active_duration_ms": 442000,
      "reply_excerpt": "...",
      "operational_error": null
    }
  ],
  "result": {
    "status": "succeeded",
    "summary": "No regressions found.",
    "step_index": 1,
    "reported_at": "...",
    "requested_session_action": "archive"
  },
  "disposition": {
    "operation_id": "disp_01J...",
    "status": "applied",
    "requested": "archive",
    "applied": "archive",
    "attempted_at": "2026-09-02T09:12:30.100-07:00",
    "applied_at": "2026-09-02T09:12:31.000-07:00",
    "reconciled_after_restart": false,
    "observed_session_state": "archived",
    "skipped_reason": "",
    "error": ""
  },
  "timeout": null,
  "error": null
}
```

The `steps` example is abbreviated; real records retain every dispatched step.
An approval entry stores request ID, state, arrival time, response payload and
recorded/forwarded times, and any withdrawal reason. These entries and execution
suspension/resumption transitions are persisted before dependent side effects.
No response is replayed after restart.

Skipped and interrupted runs omit fields never reached. A missed aggregate
uses:

```json
{
  "missed": {
    "count": 4,
    "from": "2026-08-29T09:00:00-07:00",
    "through": "2026-09-01T09:00:00-07:00",
    "interval_start_exclusive": "2026-08-28T12:00:00-07:00",
    "interval_end_inclusive": "2026-09-01T12:00:00-07:00",
    "count_truncated": false
  }
}
```

### 11.6 Retention

`keep_runs` defaults to 50 per task. Pruning removes only finalized `done` run
records, oldest first. It never removes an active run, an unfinished
finalization, or a taken-over run whose overlap monitor has not been released.
Archiving a task does not immediately prune history. The receipt is marked
intentionally pruned before run-file removal (§11.2).

Occurrence receipts, missed-range coverage, and one-time consumption are
independent of this limit. Detailed history may expire without allowing an old
slot to run again or causing a retained session to be misclassified as orphaned.

Session retention is independent. Removing an old run record does not delete a
session, and deleting a session does not delete its run record. Links to pruned
history explain that the detail expired rather than implying interrupted work.

### 11.7 Startup sequence

The registry starts after agent and worktree managers are ready, but before
scheduled sessions accept mutation requests. It then:

1. Loads tasks, runtime records, occurrence receipts, and retained run records;
   malformed entries and incomplete accounting coverage are reported visibly.
2. Repairs receipt/run creation and intentional pruning, completes one-time
   disablement, and repairs committed aggregate checkpoints. Repairs never
   create agent sessions or send prompts.
3. Marks runs without a committed execution outcome `interrupted`, invalidates
   all old result tokens, and prevents forwarding of old queued responses.
4. Preserves committed outcomes and reconciles unfinished disposition according
   to §8. Releases ownership after manager operations have stopped.
5. Reconciles session origin metadata with receipts and runs. Intentionally
   pruned history remains pruned. A genuinely unaccounted scheduled session is
   retained, visibly reported, and linked to an interrupted recovery record.
6. Restores overlap monitoring for busy taken-over sessions.
7. Performs missed-fire accounting, computes future fire times, and arms timers.

Accounting corruption that prevents reliable duplicate detection disables
automatic dispatch for the affected task until repaired; it cannot be ignored
as an empty ledger. Ordinary task/run APIs expose the diagnostic.

Shutdown stops new claims before shutting down agent managers. It does not
wait indefinitely for active sessions. On next startup, unfinished execution
is interrupted; previously committed outcomes are preserved, regardless of
whether shutdown was orderly.

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

There is no task DELETE endpoint in version 0.3.

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
step progress, per-request approvals, suspension/capacity state, result reports,
committed outcomes, disposition uncertainty, and finalization. It
follows the existing pair and checklist subscription shape.

### 12.5 Representative errors

| Condition | HTTP result |
|-----------|-------------|
| Stale task revision or prompt ETag | `409 conflict` |
| Run now against archived task | `409 archived` |
| Message sent to scheduler-owned session | `409 takeover_required` |
| Cancel or takeover after execution outcome commit | `409 execution_completed` |
| Takeover before session creation | `409 session_not_started` |
| Model, permission, lifecycle change, or driver attachment while managed | `409 takeover_required` |
| Conflicting response to an approval request | `409 approval_response_conflict` |
| Response to a withdrawn approval request | `409 approval_request_withdrawn` |
| Result without valid run token | `401 invalid_run_token` |
| Success result before final step, or result with no active step | `409 result_not_allowed` |
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

Human-facing mutation commands use the normal task revision checks. When a
scheduled-session environment is detected, official CLI clients resolve the
session's **current server-side ownership** before permitting mutations.
While it is scheduler-owned, mutation commands other than `result` refuse with
an explanation. A failed ownership lookup is not proof that ownership ended.

After ownership release, a kept process may still carry its old environment
variables; that stale marker alone must not restrict a now-interactive session.
The server rejects its old result token, and any subsequently launched process
omits managed-run credentials. The CLI does not need to mutate an existing
process's environment to recognize ownership release.

### 13.1 Result command

Inside a scheduler-owned session:

```text
trellis-ctl schedule result \
  --status succeeded|failed \
  [--summary <text>] \
  [--session keep|archive|trash|delete]
```

The command takes no task or run name. `failed` may be reported in any active
step; `succeeded` requires the final step. It reads the current run ID and token
from the environment. `-json` returns the accepted immutable result record.

### 13.2 Run and history output

`runs` prints, at minimum:

```text
TIME  STATE  OUTCOME  ACTIVE TIME  SKIP REASON  MODEL  SESSION  SUMMARY
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
schedule.run.blocked        {task_id, run_id, session_id, pending_request_ids, execution_suspended, holds_slot}
schedule.run.approval       {run_id, request_id, response_state, waiting_for_capacity}
schedule.run.suspended      {task_id, run_id, active_duration_ms}
schedule.run.resumed        {task_id, run_id, session_id, holds_slot}
schedule.run.result         {task_id, run_id, status, requested_session_action}
schedule.run.taken_over     {task_id, run_id, session_id}
schedule.run.skipped        {task_id, run_id, reason, missed_count}
schedule.run.interrupted    {task_id, run_id, session_id}
schedule.run.outcome        {task_id, run_id, outcome, outcome_source, execution_finished_at}
schedule.run.finished       {task_id, run_id, outcome, duration_ms, active_duration_ms}
schedule.run.disposition    {task_id, run_id, requested, applied, status, reconciled_after_restart, error}
schedule.task.accounting_error {task_id, message}
```

Events describe persisted transitions; they are not the sole source of truth.
A missed event is repaired by reading registry and run state.

### 14.2 Notifications

Trellis sends a user notification for:

- a run blocked on approval;
- `failed` or `timed_out`;
- a skipped one-time occurrence;
- the first `model_unavailable` skip for a task revision/model pair;
- a session-disposition failure or uncertain disposition;
- an accounting error that prevents safe automatic dispatch;
- a startup summary when one or more runs were marked `interrupted`.

Blocked notifications are deduplicated for an unresolved approval episode;
individual requests remain visible in the session. A committed failure can
notify immediately without waiting for finalization. Startup reconciliation of
an already committed outcome does not reannounce it as a new execution.

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
origin and ownership metadata. After finalization with disposition `keep`, it
behaves like any other interactive session.

While scheduler-owned:

- ordinary messages, model/reasoning-effort changes, permission-mode changes,
  and saved permission-policy changes require takeover;
- manual archive, trash, delete, reset, move, and attachment to a pair or
  checklist run require ownership release;
- approval responses pass through the scheduler's per-request and capacity
  gates; the ordinary stop/cancel control routes to scheduled-run cancellation;
- session history and streaming remain available through the existing UI.

These checks apply in session managers and autonomous-driver attachment paths,
not just page handlers. Trusted scheduler operations use an internal ownership
handle (§16.4) so ordinary message or lifecycle APIs cannot impersonate them.

If a session is later moved, trashed, restored, or deleted through ordinary
controls, retained run history remains intact and reflects current transcript
availability. Pruned history remains identifiable through its receipt.

### 15.2 Worktrees

A task may target any currently discovered fixed worktree, whether or not that
worktree is the active service worktree. Switching Trellis's active worktree
does not move or cancel a scheduled session running elsewhere.

Removing a worktree is refused while it contains a scheduler-owned run,
including one finalizing a committed outcome. Once ownership is released, the
ordinary worktree-removal safeguards apply. Tasks that still target a removed
worktree remain configured and future occurrences become
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

The pair and checklist registries must reject attachment of a scheduler-owned
session before reserving it or sending any prompt. An ordinary relay/send path
must also reject such ownership, so attachment validation is not the sole gate.
Once scheduled finalization or takeover releases ownership, the session may be
used by other features under their ordinary rules.

### 15.6 Usage

Scheduled sessions are attributed to the selected worktree and appear in the
existing usage and cost views. The run record stores available usage totals at
completion when the agent manager provides them.

Version 0.3 does not impose task-level token or cost budgets.

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
types.go        Task, Runtime, Run, Receipt, Trigger, Target, Limits, enums
definition.go   prompt validation, fenced-code-aware step splitting, footers
cron.go         preset resolution, canonical slots, next-fire and missed scans
accounting.go   occurrence/missed-range receipts, coverage index, crash repair
store.go        durable task/runtime/run persistence and coordinated retention
registry.go     timer generations, claims, slots, rehydration, archive
runtime.go      active and blocked supervision, request and overlap tracking
driver.go       session creation, step loop, latched errors, outcome commit
disposition.go  guarded operations, status reconciliation, finalization
result.go       run-token generation and step-sensitive result validation
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
- a task-indexed map of active runtimes, including busy taken-over sessions;
- an occurrence and missed-range index built from unpruned accounting receipts;
- a FIFO of ready approval responses for existing fully suspended runs.

After each due-time scan, it persists checkpoints only after all receipts and
initial run or aggregate records for that interval have been durably written.
Timer generations are revalidated under the same synchronization used for task
schedule changes. Checkpoint repair precedes all new scans.

No random or deterministic delay is introduced.

### 16.3 Extract and establish the agent-control contract

Move the facade currently used by pairing to `internal/agentctl`, retaining
compatibility aliases and existing method signatures for pair/checklist call
sites. Establish the stronger scheduler contract for both Claude and Codex
before building the automatic timing path. The existing facade's idle check
is insufficient because it considers generation and pending approvals without
authoritative background-work tracking.

Representative types and operations are:

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
    TurnID               string
    Generating           bool
    PendingApprovalIDs   []string
    HasBackgroundWork    bool
    BackgroundStateKnown bool
    ExecutionSuspended   bool
    LastTurnError        *TurnError
    ActiveViewers        int
}

// ManagedHandle is an opaque in-process capability, never accepted from an API
// caller or serialized as a client credential. It identifies the current owner.
func (a *Agents) CreateManagedSession(ctx context.Context, opts CreateSessionOptions) (AgentRef, ManagedHandle, error)
func (a *Agents) ResolveModel(agent, requested string) (string, error)
func (a *Agents) ResolvePermissionPolicy(agent, mode string) (string, error)
func (a *Agents) Status(ref AgentRef) (SessionStatus, error)
func (a *Agents) SendManagedMessage(ctx context.Context, ref AgentRef, owner ManagedHandle, prompt string) (TurnRef, error)
func (a *Agents) TrySuspendExecution(ctx context.Context, ref AgentRef, owner ManagedHandle) (bool, error)
func (a *Agents) ResumeExecution(ctx context.Context, ref AgentRef, owner ManagedHandle) error
func (a *Agents) RespondToApproval(ctx context.Context, ref AgentRef, owner ManagedHandle, response ApprovalResponse) error
func (a *Agents) HardCancel(ctx context.Context, ref AgentRef, owner ManagedHandle) error
func (a *Agents) ApplyDisposition(ctx context.Context, ref AgentRef, owner ManagedHandle, operation DispositionOperation) (DispositionResult, error)
func (a *Agents) TransferToUser(ref AgentRef, owner ManagedHandle) error
```

`TrySuspendExecution` returns false if the integration cannot establish the
full suspension/resume gate in §6.6. The scheduler then retains its slot and
clocks. A true result guarantees that all execution remains suspended until
`ResumeExecution`, including spontaneous approval withdrawal or other agent
events. Partial suspension is never reported as true.

Status, turn completion, and operational errors must be authoritative for the
dispatched turn. Managers expose unknown background state as uncertainty,
never as quiescence. Hard cancellation must establish that agent-managed work
has stopped before resources are released; failures remain visible and cannot
be converted to successful cancellation by clearing a flag.

The shared quiescence helper checks foreground work, background work, approvals,
queued responses, and a two-second stable interval. Existing pair/checklist
users retain source compatibility while adopting the stronger status helper:

```go
package pair

type Agents = agentctl.Agents
type AgentRef = agentctl.AgentRef
```

`SessionStatus.ActiveViewers` counts human viewers, not internal subscriptions.
`ApplyDisposition` checks viewers, ownership, and quiescence under a manager
operation guard and reports actual or uncertain completion. Startup has a
read-only session-state inspection path independent of obsolete owner handles.

### 16.4 Managed-session enforcement

Claude and Codex managers persist origin metadata and the current owner kind,
run ID, and ownership generation. Internal managed handles are valid only for
that current owner/generation. Transfer or release invalidates them; they cannot
be reused to send a late step, forward a response, or mutate an interactive
session. Result tokens are separately invalidated at execution completion.

While scheduler-owned, ordinary operations reject:

- user messages and agent-driver relays;
- changes to model, reasoning effort, permission mode, or persistent permission
  rules, apart from a scoped approval response for the current request;
- reset, move, archive, trash, and delete;
- pair or checklist attachment.

Initial model and permission settings are applied atomically during managed
session creation. The scheduler's own message, cancellation, approval, and
disposition operations use its internal handle. No browser, CLI, or public API
can bypass ownership by supplying an `internal`, `force`, or `managed` flag.
Pair/checklist registries validate ownership before attachment, and the session
manager checks it again on mutation.

Normal stop/cancel and approval UI paths route through the schedule registry.
Cancel and takeover remain available until execution outcome commit. During
finalization they return `execution_completed`, and the UI shows disposition
progress. On finalization, cancellation, takeover, timeout, or interrupted
execution, ownership release is idempotent and drains or invalidates outstanding
scheduler control calls before ordinary mutations are allowed. Cancellation and
timeout additionally require agent work to stop. Takeover intentionally preserves
the current agent turn while preventing any later scheduler control operation.

Only the scheduled process receives its run token. Kept or taken-over processes
may retain stale environment strings, so official clients consult current
server ownership (§13); new process launches omit old managed-run credentials.
This remains a product/API capability boundary, not a same-machine sandbox.

### 16.5 Result endpoint

The result token is generated before process launch and passed in the session's
environment. The endpoint validates:

- run ID and token;
- scheduler ownership;
- unfinished execution and a currently dispatched step;
- final-step eligibility for success, any active step for failure;
- status and disposition enums;
- summary size;
- first-write or identical-retry semantics.

The token is never returned by task or run-detail APIs and is redacted from
logs. Execution outcome commit invalidates the token before disposition;
restart invalidates every old token regardless of the run's committed outcome.

### 16.6 Completion supervision

All active, blocked, and busy taken-over sessions receive event-driven updates
and modest periodic reconciliation. Events may be missed and are not the sole
source of truth. A shared helper implements the two-second stable quiescence
debounce using turn identity and complete manager status.

An any-step error is latched independently of the manager's current-turn status.
An accepted failure report disables later steps before acknowledging the result
request. Outcome commit, takeover, cancellation, and final dispatch eligibility
are serialized. An obsolete owner handle cannot perform a late operation.

Blocked presentation is separate from suspension and capacity ownership. The
registry tracks each request and durable response, checks for withdrawals, and
reserves one slot before any resume from full suspension. Existing ready runs
have priority over new occurrences. Reconciliation heals missed process-exit,
approval, background-work, and quiescence events.

### 16.7 Persistence and locking

The schedule store implements the explicit durability protocol in §11.1 rather
than assuming every existing JSON store already supplies it. It separates:

- task configuration, guarded by user revision preconditions;
- runtime checkpoints and derived status;
- occurrence/missed-range receipts that cannot be pruned with history;
- detailed run records, including committed outcome and disposition status.

Receipt/run/checkpoint recovery completes before timers or scheduled-session
mutations are enabled. Intentional-pruning markers precede deletion. Outcome
commit precedes disposition intent execution, and `attempting` precedes the
manager operation. These boundaries need failure injection tests, including
failures between separate successful file writes. Locks alone do not provide
crash atomicity across files.

### 16.8 Application wiring

The schedule registry is initialized after worktree and agent managers, then
provided to API handlers and page handlers. Shutdown order is:

1. stop schedule timers and reject new occurrence claims;
2. persist runtime checkpoints;
3. stop other autonomous registries;
4. shut down agent managers.

Shutdown does not wait indefinitely for active scheduled sessions. On restart,
unfinished execution is interrupted, committed outcomes are preserved, and
uncertain disposition is reconciled without replay. Manager readiness for
reconciliation is distinct from exposing mutation endpoints to clients.

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
result protocol. The injected step-specific footer remains authoritative for the
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

The active agent-session view reuses existing approval and lifecycle components,
adding task origin, takeover, per-request response state, execution suspension,
capacity waiting, and finalization status. Model, permission, and driver
attachment controls follow the same server ownership rules as the composer.

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

Using the shared fake clock, cover:

- every preset and five-field validation with minimum one-minute recurrence;
- exact times without stagger;
- the 30-second grace only for a continuously awake, responsive scheduler;
- startup and wake recording past slots as missed despite that grace;
- local timezone previews and spring-forward nonexistent slots;
- fall-back canonicalization to the first occurrence, for execution and misses;
- occurrence-key determinism and backward wall-clock movement without replay;
- obsolete timer generations after pause, resume, or material rescheduling.

### 17.2 Durable occurrence accounting and missed runs

Cover:

- a daily task down four days creating one aggregate and no session;
- duplicate timer claims and retries of one manual claim's persistence using the
  same receipt and run ID; separate Run now requests remain distinct occurrences;
- an every-minute task with `keep_runs: 50` crossing a repeated hour without
  duplicate runs after the first appearance's detailed records are pruned;
- a first appearance represented by a missed aggregate suppressing its repeated
  fall-back appearance;
- a backward clock change after history pruning preserving duplicate protection;
- existing claims excluded from missed counts;
- failure after receipt creation but before initial run creation repairing that
  same run without starting a session;
- failure after aggregate creation but before checkpoint advancement repairing
  the same interval without another overlapping aggregate;
- truncated enumeration covering the entire interval and displaying a lower
  bound rather than an exact count or exact last occurrence;
- pause, resume, restore, and material edits resetting checkpoints without
  clearing receipts or abandoning a committed accounting transaction;
- receipt or coverage corruption preventing automatic dispatch visibly.

### 17.3 One-time behavior

Cover:

- firing once, consuming the timestamp, and disabling the task;
- the claimed run remaining eligible despite its own automatic disablement;
- manual execution before a future timestamp not consuming it;
- a concurrency, overlap, prompt, agent, or model skip still consuming it;
- a missed one-time timestamp producing one skip, disablement, and notification;
- crash repair between receipt, run, task, and runtime writes;
- editing to a new future timestamp while retaining old occurrence protection.

### 17.4 Prompt definition and results

Cover:

- plain and multi-step bodies, preamble prepending, fenced-code handling,
  empty-step rejection, size limit, and no frontmatter interpretation;
- preview/runtime agreement on every step's generated control footer;
- immutable task and prompt snapshots after edits;
- success accepted only on the final active step;
- explicit failure accepted on any active step and preventing later dispatch;
- results rejected before dispatch, after outcome commit, and with invalid tokens;
- identical retries and conflicting second results;
- a failed-result report followed by a still-busy turn waiting under the normal
  limits instead of dispatching another step or finalizing prematurely.

### 17.5 Registry, capacity, and approvals

Cover:

- one run per new automatic occurrence, overlap skips, and capacity skips;
- manual execution of a disabled task and rejection for archived tasks;
- dirty worktrees allowed in either permission mode;
- no queue or retry of skipped new occurrences;
- a fully suspended approval wait releasing its slot and pausing clocks;
- one background agent blocked while another continues, retaining the slot and
  continuing both clocks;
- unsupported suspension guarantees conservatively retaining the slot;
- two or more outstanding requests and partial approval/denial;
- durable per-request responses, identical retries, and conflicting responses;
- ready existing runs reacquiring capacity before new occurrences, FIFO among
  ready runs, with only one slot reserved per run;
- request withdrawal, stale responses, and automatic-resume gating;
- a dropped event healed by reconciliation, including process death while blocked;
- takeover/cancel invalidating queued responses before forwarding.

### 17.6 Driver lifecycle and ownership

Cover:

- receipt and session-ID persistence before prompt dispatch;
- one-step and multi-step execution, including background work and stale turn
  completion/error events;
- clean completion becoming `ran` without a result;
- operational error on the first or middle step stopping all later steps;
- a manager's error reset not erasing the run's latched error;
- an operational error overriding prior success and forcing disposition keep;
- active-time accounting through partial blocks, debounce, and full suspension;
- hard timeout/cancel waiting for manager work to stop before releasing resources;
- cancellation during preflight or session creation preventing first dispatch;
- takeover racing step dispatch, approval forwarding, and outcome commit;
- model, reasoning-effort, permission, lifecycle, and message changes rejected
  through every ordinary API while managed;
- pair/checklist attachment and relay rejection for a managed session;
- stale internal ownership handles rejected after transfer/release;
- a busy taken-over session, including background-only work, blocking until its
  first stable quiescence, with monitoring retained across pruning and restart;
- kept and taken-over sessions regaining ordinary controls even if their process
  environment still contains the old managed marker.

### 17.7 Restart and disposition

Inject a restart at each relevant durable boundary:

- `preflight`, `starting_session`, `running`, `blocked`, and `evaluating` without
  outcome commit become interrupted, retain sessions, and send no prompt or response;
- an accepted but uncommitted result is not evaluated after restart;
- a committed outcome in `disposing` is preserved;
- a pending, unattempted disposition is suppressed on restart;
- deletion/archive/trash completed before the completion write is reconciled
  from authoritative state without repeating the action;
- an indeterminate side effect becomes `unknown`, not assumed keep or success;
- a completed run remains completed if restart interrupts only ownership release;
- all old result tokens and ownership handles are invalidated.

Also cover each disposition action, explicit failure requesting disposition,
operational failure overriding disposition, an active viewer suppressing it,
viewer/operation reservation races, bounded operation failure or uncertainty,
and run detail after transcript deletion. Cancellation and takeover after
outcome commit must return `execution_completed` without changing the outcome.

### 17.8 Persistence, retention, and API

Cover:

- durable file-write boundaries, task revisions, and prompt ETags;
- stale task PATCH not restoring runtime fields;
- task/prompt rename transaction repair and collision refusal;
- worktree changes retaining the old prompt and resolving destination content;
- archive preserving prompts, receipts, retained history, and sessions;
- restore returning disabled without resetting duplicate protection;
- pruning only finalized runs whose overlap monitor is released;
- marking intentional pruning before removal and finishing that removal after
  an injected crash;
- retained sessions with pruned run history not generating interrupted recovery
  records on every startup;
- result-token authorization/redaction and current-ownership client checks;
- API/UI distinction between committed outcome and incomplete finalization.

Model and permission validation tests additionally cover blank-model resolution,
available pins recording requested and resolved models, unavailable pins skipping
without fallback, notification deduplication and reset on renewed availability,
prompt mode as the default, the auto-approve acknowledgement requirement, and
the resolved agent-specific permission policy in each run snapshot. Official
schedule-mutation clients must reject currently managed sessions and permit
ordinary operations after verified ownership release.

### 17.9 Agent integration contracts

Fakes exercise scheduler policy, but cannot establish what the real manager
reports or stops. For **both** Claude and Codex, add adapter contract tests
using representative protocol events for:

- turn identity and any-step operational errors;
- background-work tracking and unknown-state handling;
- multiple approval requests and withdrawals;
- full suspension/resume gating, or an explicit conservative unsupported result;
- hard cancellation and ownership enforcement;
- scoped result reporting in prompt mode without general shell authority;
- persisted session origin and disposition state after restart.

The first implementation milestone is a manual two-step run through each real
integration, including an approval response and ownership release. Add automatic
timers only after that contract works. Live checks may be opt-in when credentials
or agent binaries are required, and must be reported separately from fake tests.

### 17.10 End-to-end

At minimum:

1. Create a disabled manual two-step task, dry-run it, run it, report success
   with archive, and find the transcript in archive search.
2. Run a prompt-mode task that requests permission, approve it from the inbox,
   and verify the same run resumes with correct slot and time accounting.
3. Report failure on the first step and verify no later message is sent.
4. Take over during the first turn and verify full ordinary controls return,
   remaining steps stop, and background work still blocks the task until idle.
5. Restart during unfinished execution and verify interruption, session retention,
   and no prompt or approval response replay.
6. Restart after outcome commit during disposition and verify outcome preservation
   and no repeated delete/archive/trash operation.
7. Schedule three simultaneous tasks with cap two and verify two start and the
   third records a capacity skip without retry.
8. Exercise fall-back with an injected clock and history pruning; verify one
   logical occurrence per slot and no orphan-history reconstruction.

---

## 18. Non-Goals and Deferred Features

The following are explicitly outside version 0.3:

- log-match triggers, log payload templating, fingerprinting, coalescing, rate
  limits, and storm breakers;
- event-bus, API, GitHub, or cloud triggers;
- more than one trigger or more than one schedule per task;
- catch-up execution of missed schedule times;
- per-task overlap queues or global queues of new occurrences (recorded
  responses waiting to resume an existing run remain supported);
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
4. Durable occurrence accounting survives history pruning, restart, schedule
   edits, repeated local hours, and backward wall-clock movement.
5. A prior active run or a full global cap causes an immediate visible skip;
   new occurrences are never queued or retried.
6. Approval requests are resumable in the same process. Capacity and execution
   limits pause only when all agent work is authoritatively suspended behind
   the scheduler's resume gate.
7. Ordinary messages, model/permission changes, lifecycle controls, and other
   driver attachment require ownership release. Approval responses use the
   scheduler; takeover releases ownership without stopping the current turn.
8. Restart interrupts execution without a committed outcome and never resends
   work. A durably committed outcome survives incomplete disposition.
9. Both prompt and auto-approve permission modes are supported; dirty fixed
   worktrees are allowed.
10. Agents cannot use the supported scheduled-run capability to modify task
    configuration.
11. Only the scoped result protocol affirmatively reports task success or failure.
    Failure may be reported in any active step; success requires the final step.
    A clean completion without a result is `ran`.
12. Any-step operational error or explicit failure stops later step dispatch.
    Operational errors remain latched and suppress automatic disposition.
13. Agent-requested keep, archive, trash, or delete is evaluated after execution.
    The outcome is committed before side effects; uncertain disposition is
    reported and never automatically replayed after restart.
14. A pinned unavailable model produces a visible skip with no fallback.
15. One-time tasks consume and disable themselves even when skipped.
16. Tasks are archived, not deleted. Prompts, sessions, accounting receipts,
    and history still within retention are preserved by archive.
17. No heuristic attempts to decide whether the user is reviewing the runs.

*End of specification.*
