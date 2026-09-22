# Scheduled Task Specification

**Version:** 0.1-draft
**Status:** Specification for Implementation
**Related:** [PAIRING_SPEC.md](PAIRING_SPEC.md), [PHASE_LOOP_SPEC.md](PHASE_LOOP_SPEC.md)

---

## 1. Overview

A **scheduled task** runs an agent prompt unattended, without a human present:
create a fresh Claude or Codex session, send it a prompt, wait for the turn to
finish, then optionally archive or delete the session.

Two things start one. A **schedule** (§4.1) — nightly, weekly, or manual. Or a
**log match** (§4.4) — a line appearing in a `log_viewer`, which turns a
production error into an investigation with the failing request's own logs
already in the prompt. The second is why §4.5 exists: anything driven by
production traffic must be built to survive production breaking.

It is the third autonomous driver in Trellis, after [pairing](PAIRING_SPEC.md)
and the [phased-checklist loop](PHASE_LOOP_SPEC.md), and it reuses their
skeleton: a `Registry` that owns persistence and rehydration, a per-run
runtime goroutine, idle detection off `session.state_changed`, and records that
survive a server restart.

Unlike pairing and checklist runs, a scheduled task is **not ad hoc**. It is a
durable, named object that outlives every run it creates, and it is the first
Trellis feature where nobody is watching when the work happens. That single
difference drives most of what follows: missed fires, stagger, teardown,
storm control, skip records, and the refusal to call anything "succeeded"
without evidence.

The design deliberately follows Claude Desktop's local scheduled tasks, which
solve the same problem on the same terrain — one machine, real files, a laptop
that sleeps. Where this spec diverges, §18 says why.

---

## 2. Anatomy of a Task

### 2.1 Split storage

A task is stored in two places, split by how often each part changes and who
should review it:

| Part | Lives in | Why |
|------|----------|-----|
| The prompt | `trellis/schedules/<name>.md` in the **target worktree** | It is the substance of the task. It deserves review, diffs, and history, and it can differ per branch. |
| Everything else | `.trellis/schedule-tasks/<id>.json` in the **state dir** | Schedule, target, teardown, enabled — twiddled constantly from the UI, and meaningless in a diff. |

This is the same cut Claude Desktop makes (prompt in a `SKILL.md`, schedule in
app state), and it resolves the config-vs-UI tension directly: the prompt is
versioned, the schedule is live. Editing the `.md` takes effect on the next
run with no restart. There is **no `schedules` section in `trellis.hjson`**.

The directory is configurable, mirroring `cases.dir`:

```hjson
schedules: {
  dir: "trellis/schedules"   // relative to worktree root
}
```

### 2.2 The task definition file

Markdown with YAML frontmatter. The body is the prompt.

```markdown
---
name: nightly-triage
description: Triage yesterday's crashes and open a case for anything new.
---

Look at the crashes Trellis recorded since yesterday (`trellis-ctl crash list`).

For each crash that does not already have a case, open one with
`trellis-ctl` and write a two-paragraph summary: what crashed, and the most
likely cause based on the stack and recent commits.

Do not change any code. When you are done, reply with a single line listing
the case IDs you opened, or NOTHING TO TRIAGE if there were none.
```

**Prompt sequences.** If the body contains `## Step` H2 headings, each section
is sent as a separate prompt, in order, waiting for idle between them. A body
with no `## Step` headings is a single prompt. This keeps the common case
plain and makes multi-turn tasks readable in a diff:

```markdown
## Step 1

Run the test suite and summarize any failures.

## Step 2

For each failure you found, open the relevant file and explain in one
paragraph whether it is a real regression or a flaky test.
```

Content preceding the first `## Step` heading is a preamble prepended to every
step. Bodies over 25 KB are rejected at save time.

**Which copy is read.** The file is read from the target worktree at fire
time, so a branch can carry its own version of the prompt. In `ephemeral`
worktree mode (§5.1) it is read from the freshly created worktree, which
starts from the base branch.

The file is editable in place or through the task editor (§11.1), which reads
and writes it over the API with an mtime precondition so a browser tab cannot
clobber an edit made in your editor.

If the file is missing at fire time, the run is recorded as
`skipped / prompt_missing`. It is never an error that silently does nothing.

### 2.3 The task record

`.trellis/schedule-tasks/<id>.json` — see §12.1 for the full shape.

---

## 3. Lifecycle of a Run

```
       (trigger fires)
              │
              ▼
        ┌──────────┐   preflight fails
        │ preflight├──────────────────► skipped
        └────┬─────┘
             │
             ▼
      ┌─────────────┐
      │ provisioning│   (ephemeral worktree, if configured)
      └──────┬──────┘
             ▼
      ┌─────────────┐
      │   session   │   CreateSession + first prompt
      └──────┬──────┘
             ▼
      ┌─────────────┐  stalls on approval   ┌─────────┐
      │   running   ├──────────────────────►│ blocked │
      │ (per step)  │                       └─────────┘
      └──────┬──────┘  step/run timeout ───► timed_out
             ▼
      ┌─────────────┐
      │  evaluating │   outcome: succeeded | failed | ran (§8.2)
      └──────┬──────┘
             ▼
      ┌─────────────┐
      │  teardown   │   keep | archive | trash | delete (§9)
      └──────┬──────┘
             ▼
           done
```

Every path — including every skip — produces a persisted `Run` record. A
trigger that fires and does nothing observable is the defining failure mode of
schedulers, and this spec's answer is that it cannot happen: there is always a
row, and the row always says why.

**Preflight** checks, in order:

1. Task is enabled and not paused.
2. Target worktree exists (or can be created, in `ephemeral` mode).
3. The prompt file exists and parses.
4. No prior run of this task is still active (unless `overlap: queue`).
5. Global concurrent-run cap not exceeded (§10).
6. The agent binary (`claude` / `codex`) is on `PATH`.

Any failure records `skipped` with the corresponding reason and stops.

---

## 4. Triggers

A task carries a list of triggers, not a single cron field. Only
`schedule` ships in this version; the list exists so event triggers (§4.4) do
not require a schema migration.

```json
"triggers": [
  { "type": "schedule", "preset": "daily", "at": "09:00", "cron": "", "tz": "local" }
]
```

### 4.1 Schedule trigger

Presets, matching the shape users actually reach for:

| Preset | Meaning |
|--------|---------|
| `manual` | No schedule. Runs only via **Run now**. |
| `hourly` | Every hour. |
| `daily` | Every day at `at` (default `09:00`). |
| `weekdays` | Mon–Fri at `at`. |
| `weekly` | On `dow` at `at`. |
| `cron` | Standard 5-field cron in `cron`. |
| `once` | A single run at the timestamp in `at`. Auto-disables after firing. |

`manual` is a first-class preset, not an absence of one. It makes the task
list double as a library of saved prompts, and it means every task is
runnable and debuggable from the moment it is created — before any schedule
is trusted.

All times are **local**, interpreted in the server's zone. Minimum interval is
**one minute**; Trellis runs on your machine, so the cloud services' one-hour
floor does not apply. The floor that does apply is the concurrency cap (§10).

### 4.2 Stagger

Each task gets a small, deterministic offset added to its fire time:

```
offset = hash(task.ID) mod min(5 minutes, interval / 4)
```

The offset is derived from the task ID, so it is stable for the life of the
task. Five tasks set to 09:00 do not spawn five agent processes in the same
second on a laptop. The UI shows the effective time (`09:00 +2m`) so the
offset is never a mystery.

### 4.3 Missed fires

Trellis will not be running at 3am if the machine slept, and a naive scheduler
either loses the run or stampedes a week of them on wake. The rule, adopted
from Claude Desktop:

> On startup, and on wake, check whether each enabled task missed any fire
> times **in the last 7 days**. If it did, start **exactly one** catch-up run
> for the **most recently missed** time, and discard everything older.

The discarded occurrences are recorded as a single `skipped` run with reason
`missed_while_down` and a `missed_count`, so the history shows the gap rather
than hiding it. A catch-up run is marked `catch_up: true` and notifies.

**Wake detection** needs no OS integration: the scheduler ticks every 30s and
treats a wall-clock jump of more than 3 ticks as a wake.

**This changes what prompts must say.** A task scheduled for 9am may run at
11pm. The task editor carries this warning inline, with the fix:

> Only review today's commits. If it is after 5pm, skip the review and just
> summarize what was missed.

### 4.4 Log-match trigger

A log-match trigger fires a task when a line in a configured `log_viewer`
matches a condition. The motivating case: the `errord-logs` viewer tails
`/var/groupsio/log/errord/` on `admin01-g2`; when a crash appears, a task
should investigate it with the crash's own data in hand.

```json
{
  "type": "log_match",
  "viewer": "errord-logs",
  "match": "level:error msg:~\"panic\"",
  "fingerprint": "{file}:{line}",
  "settle_s": 30,
  "context_lines": 20,
  "include_trace": true
}
```

**Matching reuses `logs.Filter`.** The `match` string is the *same filter
syntax the log viewer's filter box already accepts* (`field:value`,
`field:~"contains"`, `field:a,b` for OR, `-` for negation), parsed by the
existing `logs.ParseFilter`. This is deliberate and is the feature's best
debugging affordance: to write a rule, type the filter into the errord viewer,
watch what it selects against real traffic, and paste it into the task. There
is no second matching language to learn, and no rule that behaves differently
from what the viewer showed.

**Subscription, not a new hook.** `logs.Viewer` already exposes
`Subscribe(ch chan<- LogEntry)`, and `processLine` broadcasts every parsed,
derived entry. The schedule registry subscribes one channel per viewer that
has at least one active rule and evaluates rules on the entries. No change to
the log pipeline.

> Viewer broadcast is non-blocking and **drops** to a full subscriber channel.
> The match channel is therefore generously buffered, evaluation is a compiled
> filter over already-parsed fields, and dropped entries are counted and shown
> on the rule. A silently lossy matcher would be worse than no matcher.

**Matching is on parsed fields, not raw text.** The errord viewer parses JSON
with `level`, `trace_id`, `stack`, `file`, and `line`, and derives `file_line`
and `prog_host`. All of those are matchable and all are available to the
payload, which is what makes a useful fingerprint possible.

#### 4.4.1 What the task receives

The run's payload is assembled from the buffer at fire time:

| Piece | Source |
|-------|--------|
| The matched entry | Parsed fields + `Raw`. |
| Surrounding context | `context_lines` entries either side, via the viewer's existing `GetEntriesBefore` / `GetEntriesAfter`. |
| The full trace | When `include_trace` and the parser has an `id` field, every buffered entry sharing the matched entry's `trace_id` — the same correlation `crashes.filterEntriesByTraceID` already performs. |
| Aggregates | Match count in the window, distinct `prog_host` values, first and last timestamps, the fingerprint. |

For a `groups.io` crash, `include_trace` is the point of the feature: the
agent gets the whole request that died, not the one line that mentioned it.

#### 4.4.2 Referencing parsed fields in the prompt

A JSON log's fields are the reason the trigger is worth having, so the prompt
references them **by name**, not through a fixed set of scalars. Any field the
parser produced is addressable:

```markdown
The `{{.Fields.service.name}}` service panicked at {{.Fields.file}}:{{.Fields.line}},
{{.Count}} times since {{.FirstSeen}} across {{.Hosts}}.

Here is the stack:

{{.Fields.stack}}

Investigate using the full request trace in the log-match-payload block below.
Identify the most likely cause and open a case. Do not change any code.
```

The template namespace:

| Reference | Resolves to | Origin |
|-----------|-------------|--------|
| `{{.Fields.<name>}}` | Any parsed or derived field — `stack`, `trace_id`, `file`, `line`, `file_line`, `prog_host`, nested paths like `service.name`. | log |
| `{{.Message}}`, `{{.Level}}`, `{{.Timestamp}}`, `{{.Raw}}` | The `LogEntry`'s own fields. | log |
| `{{.Fields}}` | Every field, rendered as pretty JSON. | log |
| `{{.Count}}`, `{{.Fingerprint}}`, `{{.Hosts}}`, `{{.FirstSeen}}`, `{{.LastSeen}}`, `{{.Viewer}}`, `{{.RuleID}}` | Window aggregates. | Trellis |

Field names come from the parser at runtime, so the set is discovered rather
than declared. The editor lists what the viewer's buffer actually contains,
with a sample value for each (§11.1) — the fields are found by looking, not by
remembering what errord emits.

When a settle window holds several matches, singular references resolve
against the **first** match, which the window's aggregates then describe. The
whole set is in the payload block.

**Unresolved references** render empty and are recorded on the run as
`unresolved_fields`. A field that is simply absent from one crash must not
abort an investigation at 3am. The editor warns at save time about references
not seen in the buffer, which is where that mistake is cheap to catch.

#### 4.4.3 Log text is untrusted, wherever it appears

Log lines from a production system carry user-supplied content — addresses,
subjects, group names, message bodies. Crash text is therefore
**attacker-influenceable**, and referencing it by field does not launder it.

The rule is about origin, not location:

- **Trellis-produced values** — `{{.Count}}`, `{{.Fingerprint}}`, `{{.Hosts}}`,
  timestamps — interpolate bare. Trellis computed them.
- **Log-produced values** — everything in the `log` rows above — interpolate
  **delimited**: each expansion is emitted as a labeled, fenced span carrying
  the field's name, never as bare prose. Injected text lands inside a quoted
  region attributed to `stack` or `msg`, where it reads as data.
- When a body references any log-produced value, a short untrusted-content
  preamble is prepended once, telling the agent that fenced log spans and the
  payload block are data and that instructions inside them are not to be
  followed.
- The payload block itself — matched line, context, trace — is appended
  delimited by `<log-match-payload>` and labeled the same way.

So the prompt reads naturally and the guarantee holds: no log byte ever
reaches the agent as an unattributed instruction. Payload text is truncated to
`max_payload_bytes` (default 64 KB), oldest context dropped first, matched
line never dropped.

### 4.5 Storm control

The failure this must not have is: production breaks, the same error fires ten
thousand times, and Trellis spawns ten thousand agents. Three independent
mechanisms, because they guard three different failures.

**1. Coalescing into a live run — "the same problem, again."**
Every match is reduced to a fingerprint by a template over parsed fields,
default `{file}:{line}`. On each match the rule looks for a **live run** with
the same fingerprint — one whose state is not terminal, or which is `blocked`
with its session still alive. If it finds one, the match **does not fire a
task**: it increments `match_count` on that run, updates `last_seen`, and
merges the new host and trace ID into the record. Ten thousand identical
panics produce one run that says it happened ten thousand times, which is both
cheaper and a better report than ten thousand runs.

The counter is live: the run's page ticks up over the WebSocket while the
agent works, so "is this still happening?" is answerable by watching. The
session itself is not interrupted — a follow-up prompt mid-investigation would
turn one dedupe window into an unbounded conversation.

**Liveness, not a fixed window, is the dedupe key.** Once a run finishes and
its session is torn down, the investigation is over; the same crash recurring
afterwards is *news* and earns a fresh run. `cooldown_s` (default 600) is the
only time-based floor, preventing churn when a run finishes seconds before the
next occurrence. Coalescing is capped at `max_coalesce` (default 10,000) per
run so a long-`blocked` run cannot absorb a fingerprint forever; past the cap
the rule falls through to the rate cap like any other match.

Choosing the fingerprint is the main tuning knob and belongs in the UI with a
live preview: given the last hour of the viewer's buffer, show how many
distinct fingerprints the rule would have produced. A fingerprint that yields
one group per crash is right; one that yields a group per *request* is wrong,
and the preview shows that before the rule is enabled.

**2. Rate cap — "several different problems at once."**
Distinct fingerprints bypass dedupe, so a separate cap bounds them:
`max_fires_per_hour` (default 3) and `max_fires_per_day` (default 10), per
rule. Matches beyond the cap are recorded as one `skipped / rate_capped` run
carrying the suppressed count and the distinct fingerprints seen, so nothing
is invisible. A per-rule `cooldown_s` (default 600) additionally spaces fires.

**3. Storm breaker — "the system is on fire."**
If the match rate exceeds `storm_threshold` (default 100 matches/minute), the
rule **trips**: it stops evaluating, records one `skipped / storm` run with the
observed rate, and raises a single `notify.blocked`. When everything is
broken, the correct number of notifications is one.

A tripped rule stays tripped until the match rate stays below the threshold
for `storm_cooldown` (default 15 min), or the user resets it from the task
page. The trip and the reset are both in the run history, with the rate that
caused them. A tripped rule is shown in the task list with a distinct badge —
a silently disabled rule would be the worst outcome of all.

**Ordering.** settle → coalesce → cooldown → rate cap → storm breaker → global
`max_concurrent_runs` (§10). The breaker is checked on every match, not only
at fire time, so a storm short-circuits before any of the more expensive
checks run.

**The settle window** (`settle_s`, default 30s) is what makes the aggregates
meaningful: on first match the rule waits, collecting further matches, and
fires once with the batch. A prompt then opens with "47 occurrences across 3
hosts in the last 30 seconds" rather than a single line stripped of its
context. It also collapses the thundering-herd case at the source.

### 4.6 Future triggers

The trigger list is a list so these need no schema change. Event-bus triggers
(`{"type": "event", "pattern": "service.crashed"}`) are the obvious next one,
and would reuse §4.4.2's payload wrapping unchanged. API and GitHub triggers
are further out and less useful here than they are in a cloud product.

---

## 5. Target and Isolation

```json
"target": {
  "agent": "claude",
  "worktree": "main",
  "worktree_mode": "fixed",
  "model": "",
  "permission_mode": "prompt"
}
```

### 5.1 Worktree modes

| Mode | Behavior |
|------|----------|
| `fixed` | Run in the named worktree, against whatever state it is in — including uncommitted changes. |
| `ephemeral` | Create a fresh worktree per run, branch `sched/<task-name>/<short-run-id>`, from the configured base branch. Removed after the run per §5.2. |

`fixed` is the default because it is what people expect, and `ephemeral` is
the option that makes a task safe to leave running unattended — an agent
editing your live working tree at 3am is a different proposition from one
working on its own branch.

**This is a per-task setting with no special cases.** A log-match task is not
forced into an ephemeral worktree despite being triggered by production: an
investigation that only reads the repo is often better off in the worktree you
already have checked out, with its build cache warm. The editor surfaces the
choice with both trade-offs stated (§11.1) and leaves it to the task.

Ephemeral worktrees are created through the existing
`worktree.WorktreeManager.Create` and inherit the normal `on_create` lifecycle
hooks. `Create` currently runs `git worktree add -b <branch> <path>` with no
start-point, so it branches from the repo's current `HEAD`; honoring
`base_branch` requires the small extension in §17.4.

### 5.2 Ephemeral worktree retention

Ephemeral worktrees accumulate fast — a daily task on a two-week trip leaves
fourteen of them — so the reaper ships in the same change as the toggle, not
later.

After a run reaches a terminal state, its ephemeral worktree is removed **only
if all** of the following hold:

- the run is terminal (not `blocked`),
- the worktree has no uncommitted changes,
- the branch has no commits ahead of its base.

Otherwise the worktree is **kept** and the run record is annotated
`worktree_kept: "has work"`, surfaced in the run list. Work an agent did is
never silently deleted; only empty worktrees are reaped.

Independently, a per-task cap (`keep_worktrees`, default 3) removes the
oldest *reapable* worktrees beyond the cap.

---

## 6. Permissions

```
permission_mode: "prompt" | "auto_approve"
```

`auto_approve` maps to the existing per-session auto-approval: Claude spawns
with `--permission-mode bypassPermissions`; Codex passes `approvalPolicy:
"never"` with `sandboxPolicy: dangerFullAccess`. It is required for a task
that must complete without a human.

`prompt` leaves normal approval behavior in place. When a run stalls on an
approval, **it is not an error and the session is not killed**:

- the run transitions to `blocked`,
- teardown is skipped entirely,
- the session stays alive and appears in the inbox as `needs_approval`.

Trellis already has the right UI for a stalled unattended run — the inbox —
so a blocked run is simply a session waiting for you, which is what it is. A
`stall_timeout` (default 30 minutes) stops the driver waiting, but never
touches the session.

The UI marks `auto_approve` tasks with a warning badge, and every run record
stores the mode it ran under, so "what was this allowed to do?" is answerable
after the fact.

> **Known limitation.** Trellis's auto-approval is a single boolean per
> session; there are no per-task saved allow-rules. So there is no "run it
> once, approve each tool with *always allow*, and future runs are trained"
> path. A task is either fully supervised or fully autonomous. Narrowing this
> is out of scope here and noted in §16.

---

## 7. Prompts

### 7.1 Prompts are prose, not slash commands

The task editor does **not** offer the slash-command menu that the interactive
session composer does. Slash commands are largely handled by the CLI's own
input layer rather than the model, so a `/`-prefixed line in a scheduled
prompt is unreliable at best and inert at worst. A scheduled task must say what
it wants in plain language.

Where a task genuinely needs a repeatable procedure, the right vehicle is a
skill committed to the repository — the agent discovers it the normal way —
or a Trellis workflow invoked via `trellis-ctl workflow run`.

### 7.2 Self-contained prompts

Nobody is present to answer a clarifying question. The prompt must state what
to do, what is out of bounds, and what "done" looks like. The task editor
shows a short checklist to that effect, and `Run now` is one click away.

### 7.3 Self-modification

`trellis-ctl` is already installed into every session via the Trellis skill,
so a running task can adjust its own future:

```
trellis-ctl schedule pause nightly-triage
trellis-ctl schedule update nightly-triage --preset weekly
```

This lets a task back itself off when it finds nothing to do, or disable
itself after a migration completes.

---

## 8. Completion and Outcome

### 8.1 Idle detection

Identical to pairing (PAIRING_SPEC §5.3), reusing the same code: a step is
complete when the session is **not generating** and has **no pending
permission/approval request**, held stable for a 2s debounce.

Two timeouts bound a run: `step_timeout` (default 30 min) and `run_timeout`
(default 2 hours). Exceeding either transitions the run to `timed_out`,
interrupts the turn, and skips teardown so the transcript survives.

### 8.2 Outcome: `ran` is not `succeeded`

A session that starts and exits cleanly has proven only that the process
worked. It has not proven the task was accomplished. Reporting a green check
for that is the single most common way scheduled agents mislead their owners,
and this spec refuses it.

Outcome is determined in order:

1. `success_signal` is configured and the final assistant text matches it
   (same strict matching as `checklist.IsCompletionSignal`) → **`succeeded`**.
2. The session emitted `notify.done` during the run → **`succeeded`**.
3. The session emitted `notify.error` → **`failed`**.
4. The turn ended in an error → **`failed`**.
5. Otherwise → **`ran`**.

Rules 2 and 3 are **not implementable today**: `notify.*` events carry only a
`message`, with nothing identifying the session that raised them, so a run
cannot tell its own notification from any other. They depend on the change in
§17.4. Until that lands, outcome comes from rules 1, 4, and 5 — which is
sufficient, since `success_signal` covers the deliberate case and `ran` is an
honest answer for everything else.

`ran` renders as a neutral gray dot with the tooltip *"Completed without
error. Whether the task succeeded is unknown — open the run to see."* Only
`succeeded` is green, and only evidence produces it.

---

## 9. Teardown

```json
"teardown": { "on_success": "archive", "on_other": "keep" }
```

| Action | Effect |
|--------|--------|
| `keep` | Leave the session in the worktree's normal list. |
| `archive` | `ArchiveSession` — hidden from listings, kept forever, full-text searchable. |
| `trash` | `TrashSession` — recoverable. |
| `delete` | `DeleteSession` — permanent. |

**`archive` is the default for success.** Trellis's archive is already
searchable (`/archived-sessions`), which means an archived run's transcript
*is* its debug log, at no additional cost. `delete` is opt-in and confirmed in
the UI, because it destroys the only record of what the agent actually did.

Teardown is **skipped unconditionally** when any of the following hold, and
the reason is recorded on the run:

- the session is still generating,
- the session has a pending approval (run is `blocked`),
- a WebSocket client is currently subscribed to the session — somebody is
  looking at it.

The run record keeps the session ID forever regardless of teardown, so run
history links to the transcript even after archiving.

---

## 10. Concurrency and Skips

Two limits:

- **Per-task** `overlap`: `skip` (default) or `queue`. A task whose previous
  run is still active does not start a second one.
- **Global** `max_concurrent_runs` (default 2). Scheduled agent processes are
  expensive and this is one laptop.

Every non-fire is a `skipped` run record with a reason:

| Reason | Meaning |
|--------|---------|
| `previous_run_active` | This task's prior run had not finished. |
| `concurrency_cap` | Global cap reached. |
| `missed_while_down` | Trellis was not running (§4.3). Carries `missed_count`. |
| `worktree_missing` | Target worktree no longer exists. |
| `prompt_missing` | Definition file absent or unparseable. |
| `agent_unavailable` | `claude` / `codex` not on `PATH`. |
| `task_paused` | Fired while paused (only possible on a race). |
| `cooldown` | Log-match rule fired too recently (§4.5). |
| `rate_capped` | Log-match rule over its hourly/daily cap. Carries the suppressed count. |
| `storm` | Log-match rule tripped its storm breaker. Carries the observed rate. |

A repeat fingerprint that coalesces into a live run is **not** a skip: it
increments `match_count` on that run (§4.5).

The run list shows skips inline, dimmed, with the reason on hover.

---

## 11. Controls and Visibility

A scheduled task is configured once and then trusted to act while nobody is
watching, so the interface has a heavier burden than most: everything that
will happen at 3am must be inspectable at 3pm. The editor is therefore a
full page, not a modal, and every consequential field has a preview attached.

### 11.1 The task editor

`/schedules/new` and `/schedules/{id}/edit` render the same form.

**1 — Identity.** Name (kebab-case, unique, becomes the definition filename)
and description. Renaming a task renames its definition file and is refused if
the target exists.

**2 — Prompt.** An in-page editor over `trellis/schedules/<name>.md` (§2.2),
showing the resolved path and a reminder that it is a file in the worktree and
will show up in `git status`. Below the textarea:

- a live **steps detected** indicator, parsed with the same `## Step` splitter
  the runtime uses, so a heading typo is visible immediately;
- an **Open in editor** link through the existing VS Code handler, for people
  who would rather not type into a browser;
- **conflict detection** on save by mtime — if the file changed on disk since
  it was loaded, the editor says so and offers a diff instead of clobbering
  the newer version.

**3 — Field palette** *(log-match tasks)*. A panel beside the prompt listing
the fields **actually present in the selected viewer's buffer**, each with a
live sample value, sourced from the discovery endpoint (§13). Clicking one
inserts `{{.Fields.<name>}}` at the cursor. Aggregates (`{{.Count}}`,
`{{.Hosts}}`, …) are listed in a second group, visually separated because they
interpolate bare while log fields interpolate delimited (§4.4.3).

This panel is the answer to "what can I reference?" — the field set of a JSON
log is discovered by looking at traffic, never declared in advance. References
to names absent from the buffer are flagged at save time as a warning, not an
error: a field may be present only on the crash you are trying to catch.

**4 — Target.** Agent (Claude / Codex), worktree picker, model, and:

- **Worktree mode** — `fixed` and `ephemeral` as radio options with both
  trade-offs written out: `fixed` runs against your checked-out tree, warm
  build cache, uncommitted changes included; `ephemeral` branches per run and
  is reaped when empty. Choosing `ephemeral` reveals base-branch and
  `keep_worktrees`.
- **Permission posture** — `prompt` or `auto_approve`, the latter carrying an
  inline warning describing exactly what it grants, not a bare checkbox.

**5 — Triggers.** A repeatable list; **Add trigger** offers Schedule or Log
match. Each row is a card that can be removed independently.

*Schedule card:* preset selector (manual / hourly / daily / weekdays / weekly /
cron / once), with time, day, or cron inputs revealed per preset, and a **next
five fire times** list rendered live beside it — with stagger applied, in local
time, so both cron mistakes and the stagger offset are visible before saving.

*Log-match card:* viewer picker (populated from configured `log_viewers`),
match expression, fingerprint template, and the preview panel below.

**6 — The live preview panel.** The centerpiece of the log-match card, and the
reason a rule can be trusted before it is enabled. On every edit (debounced) it
replays the selected viewer's buffered window through the rule *as currently
configured* and reports:

- total matches, and the matched lines themselves in a scrollable list;
- **distinct fingerprints**, grouped, with per-group counts and a sample line —
  the single most important number, because it is the difference between one
  run per crash and one run per request;
- **how many runs would actually have fired**, after coalescing, cooldown, and
  the rate cap;
- whether the **storm breaker** would have tripped, and at what rate.

The match input carries the same autocomplete the log viewer's filter box uses,
since it is the same syntax and the same parser (§4.4).

**7 — Storm control.** Collapsed by default, showing a one-line summary of the
effective settings. Expanded, each field is labeled by the failure it guards
rather than by its variable name — *the same problem repeating*, *several
different problems at once*, *the system on fire* — because the three are easy
to confuse and are tuned for different reasons.

**8 — Teardown and limits.** Teardown on success and on anything else, with
`delete` requiring a confirmation and carrying a note that it destroys the only
record of the run. Step, run, and stall timeouts; overlap policy; retention.

**9 — Save.** *Save*, or *Save and run now*. Validation is inline and blocking
for structural errors (bad cron, unknown viewer, duplicate name, unparseable
match expression) and advisory for judgement calls (references to unseen
fields, a fingerprint that produced one group per line in the preview,
`auto_approve` with `fixed` worktree mode).

### 11.2 Task list

`/schedules` — one row per task: name, trigger summary ("daily 09:00 +2m" /
"errord-logs: level:error"), target, next fire or last match, last outcome,
and an enabled toggle. Badges mark `auto_approve`, a tripped breaker, and the
unopened-runs flag (§11.5). Inline **Run now** and **Pause**. Filter by agent,
worktree, trigger type, and state.

### 11.3 Task detail

`/schedules/{id}` — the task's live state and its history.

- **Header** — enabled/paused, last outcome, next fire, and the actions:
  **Run now**, **Dry run**, **Pause / Resume**, **Edit**, **Delete** (which
  archives the sessions the task created).
- **Trigger cards**, each showing runtime state rather than just
  configuration. A schedule card shows the next five fires. A log-match card
  shows matches in the last hour as a sparkline, the fingerprints currently
  live, dropped-entry count, fires used against the hourly and daily caps, and
  the breaker's state with a **Reset breaker** button when tripped.
- **Resolved prompt** — the definition file rendered with a real historical
  match substituted in, so what the agent will receive is visible without
  waiting for a crash.
- **Run history** — §11.4.

**Dry run** is the same resolution path: pick any line from the viewer's
buffer, and the page renders the complete prompt — preamble, delimited field
spans, payload block — exactly as it would be sent, without creating a session
or a worktree. Together with **Run now**, this is how a rule is debugged.

### 11.4 Run history and run detail

A reverse-chronological list of every run, **including skips**, dimmed with
their reason on hover. Each row: fire time, trigger, duration, outcome,
teardown action, and a link to the session. Log-match runs additionally show
their fingerprint and an occurrence count that **ticks up live** over the
WebSocket as matches coalesce into them (§4.5).

The run detail is a timeline — provisioning, session creation, each step with
its wall-clock duration, evaluation, teardown — so a hung run shows *which*
step hung. For log-match runs it also carries the match record: fingerprint,
first and last occurrence, hosts, trace IDs, payload size and whether it was
truncated, and any `unresolved_fields`.

### 11.5 Inbox integration

A `blocked` run's session appears in the inbox as `needs_approval`, like any
other stalled session. Scheduled sessions are tagged so the inbox can show
their origin.

Runs whose sessions are never opened are counted. At five consecutive
unopened runs the task is flagged in the UI and a `notify.blocked` event is
raised — a forgotten nightly job quietly consuming tokens is a real failure,
and the flag is deliberately softer than auto-pausing something you may be
relying on.

### 11.6 Events

Published on the existing bus, so `/events` and its history cover scheduling
with no new UI:

```
schedule.task.fired      {task_id, run_id, trigger, catch_up}
schedule.run.started     {task_id, run_id, session_id, worktree}
schedule.run.step        {run_id, step, index, duration_ms}
schedule.run.finished    {task_id, run_id, outcome, teardown}
schedule.run.skipped     {task_id, run_id, reason, missed_count}
schedule.run.blocked     {task_id, run_id, session_id}
schedule.rule.matched    {task_id, viewer, fingerprint, count, hosts}
schedule.rule.coalesced  {task_id, viewer, run_id, fingerprint, match_count}
schedule.rule.suppressed {task_id, viewer, reason, count}
schedule.rule.tripped    {task_id, viewer, rate_per_min}
schedule.rule.reset      {task_id, viewer, by}
```

`schedule.rule.matched` is published once per **settle window**, never per
line — a per-line event would reproduce the storm it exists to describe.

### 11.7 Notifications

A desktop notification fires on catch-up runs, `failed` outcomes, `blocked`
runs, and a tripped storm breaker, via the existing notification config.
Successful routine runs are silent by design, and a coalesced repeat is silent
by definition — the whole point is that it is not a new event.

### 11.8 Frontend implementation

Follows the existing per-feature convention: `views/schedules.qtpl`,
`views/schedule_edit.qtpl`, `views/schedule_detail.qtpl` (regenerated with
`qtc -file views/X.qtpl`), plus `static/js/schedule.js` and
`static/css/schedule.css`.

Reuse rather than rebuild:

- the filter-expression autocomplete from `static/js/logviewer.js`, since the
  match input takes the same syntax;
- the modal and confirmation patterns already used by `workflows.qtpl` and
  `claude.js`;
- `/api/v1/schedule/ws` for live task state, run progress, and coalesce
  counters, following the `pair.js` / `checklist.js` subscription shape;
- `spa.js` navigation and `command_palette.js` entries for tasks, so a task is
  reachable from the picker like every other Trellis object.

---

## 12. Persistence and Restart

State lives under `.trellis/`, alongside `pairs/` and `checklist-runs/`:
`schedule-tasks/<id>.json` and `schedule-runs/<id>.json`. Runs are retained
per task (`keep_runs`, default 50) and pruned oldest-first.

On startup the registry rehydrates tasks, marks any run left `running` as
`interrupted` (the CLI process did not survive the restart), and then applies
the missed-fire rule from §4.3.

### 12.1 Per-task record

```json
{
  "id": "...",
  "name": "nightly-triage",
  "description": "Triage yesterday's crashes.",
  "enabled": true,
  "created_at": "...",
  "updated_at": "...",
  "triggers": [
    { "type": "schedule", "preset": "daily", "at": "09:00", "cron": "", "tz": "local" },
    {
      "type": "log_match",
      "id": "rule_01",
      "viewer": "errord-logs",
      "match": "level:error msg:~\"panic\"",
      "fingerprint": "{file}:{line}",
      "settle_s": 30,
      "context_lines": 20,
      "include_trace": true,
      "max_payload_bytes": 65536,
      "cooldown_s": 600,
      "max_coalesce": 10000,
      "max_fires_per_hour": 3,
      "max_fires_per_day": 10,
      "storm_threshold_per_min": 100,
      "storm_cooldown_s": 900,
      "state": { "tripped_at": null, "fires_this_hour": 0, "dropped_entries": 0 }
    }
  ],
  "target": {
    "agent": "claude",
    "worktree": "main",
    "worktree_mode": "ephemeral",
    "base_branch": "main",
    "model": "",
    "permission_mode": "auto_approve"
  },
  "success_signal": "NOTHING TO TRIAGE",
  "teardown": { "on_success": "archive", "on_other": "keep" },
  "limits": {
    "step_timeout_s": 1800,
    "run_timeout_s": 7200,
    "stall_timeout_s": 1800,
    "overlap": "skip",
    "keep_runs": 50,
    "keep_worktrees": 3
  },
  "stats": {
    "last_run_id": "...",
    "last_outcome": "succeeded",
    "consecutive_unopened": 0
  }
}
```

### 12.2 Per-run record

```json
{
  "id": "...",
  "task_id": "...",
  "task_name": "nightly-triage",
  "trigger": "schedule",
  "catch_up": false,
  "scheduled_for": "2026-08-30T09:00:00-07:00",
  "fired_at": "2026-08-30T09:02:00-07:00",
  "finished_at": "2026-08-30T09:14:31-07:00",
  "state": "done",
  "outcome": "succeeded",
  "skip_reason": "",
  "missed_count": 0,
  "session": { "agent": "claude", "id": "...", "worktree": "sched-nightly-triage-a1b2" },
  "match": {
    "viewer": "errord-logs",
    "rule_id": "rule_01",
    "fingerprint": "api/handlers/message.go:412",
    "match_count": 47,
    "first_seen": "2026-08-30T09:01:58-07:00",
    "last_seen": "2026-08-30T14:22:03-07:00",
    "hosts": ["app01-g2", "app02-g2"],
    "trace_ids": ["..."],
    "dropped_entries": 0,
    "payload_bytes": 18422,
    "payload_truncated": false,
    "unresolved_fields": ["fields.user_id"]
  },
  "permission_mode": "auto_approve",
  "worktree_mode": "ephemeral",
  "worktree_kept": "",
  "steps": [
    { "index": 0, "prompt": "...", "started_at": "...", "ended_at": "...", "duration_ms": 742000, "reply_excerpt": "..." }
  ],
  "teardown": "archive",
  "teardown_skipped_reason": "",
  "error": ""
}
```

---

## 13. API

Mirrors the pair and checklist route shapes.

```
GET    /api/v1/schedule                     list tasks
POST   /api/v1/schedule                     create task
GET    /api/v1/schedule/ws                  live task + run updates
GET    /api/v1/schedule/{id}                task detail
PATCH  /api/v1/schedule/{id}                update task
DELETE /api/v1/schedule/{id}                delete task
POST   /api/v1/schedule/{id}/run            run now      {dry_run?: bool}
POST   /api/v1/schedule/{id}/pause          pause
POST   /api/v1/schedule/{id}/resume         resume
GET    /api/v1/schedule/{id}/preview        next N fire times
GET    /api/v1/schedule/{id}/runs           run history
GET    /api/v1/schedule/{id}/rules/{rid}/preview   fingerprint preview over the viewer buffer
POST   /api/v1/schedule/{id}/rules/{rid}/reset     clear a tripped storm breaker

GET    /api/v1/schedule/{id}/prompt         read the definition file (+ mtime)
PUT    /api/v1/schedule/{id}/prompt         write it (mtime precondition)
POST   /api/v1/schedule/preview             rule preview for an unsaved rule
GET    /api/v1/schedule/fields              fields seen in a viewer's buffer, with samples
POST   /api/v1/schedule/resolve             render a prompt against a chosen entry
GET    /api/v1/schedule/runs/{runID}        run detail
POST   /api/v1/schedule/runs/{runID}/cancel cancel an active run
```

---

## 14. CLI

`trellis-ctl schedule`, following the `workflow` verb shape:

```
trellis-ctl schedule list
trellis-ctl schedule show <name>
trellis-ctl schedule run <name> [--dry-run]
trellis-ctl schedule runs <name> [-n 20]
trellis-ctl schedule pause <name>
trellis-ctl schedule resume <name>
trellis-ctl schedule update <name> --preset weekly --at 09:00
trellis-ctl schedule rules <name>            # rule state: fires, drops, tripped
trellis-ctl schedule reset <name> <rule-id>  # clear a tripped storm breaker
```

`runs` prints outcome, duration, skip reason, and session ID per run, so the
question "why did my nightly task do nothing this morning?" is answerable from
a terminal.

`POST /api/v1/schedule/preview` and `/fields` take a rule body rather than a
task ID so the editor can preview a rule that has never been saved. That is
the common case: the rule is tuned against real buffered traffic *before* it
is allowed to fire anything.

---

## 15. Interaction With Other Features

**Cases.** A scheduled run may open cases via `trellis-ctl`; they are ordinary
cases. A future version may attach the run record to the case as evidence.

**Crashes.** `internal/crashes` records crashes from `service.crashed` events
raised by *locally managed services*, correlating entries by trace ID. A
log-match rule covers the case that subsystem cannot: a remote host's log
tailed over a network source, where Trellis manages no process and sees no
exit. The two are complementary, and §4.4.1's trace correlation deliberately
mirrors `crashes.filterEntriesByTraceID` so the payloads look alike. If event
triggers land (§4.6), `service.crashed` becomes the natural bridge between
them.

**Pairing / checklist.** A scheduled session is an ordinary session and may be
paired manually after the fact. A task that *starts* a pair or checklist run
is out of scope here (§16).

**Usage.** Scheduled sessions are attributed by working directory like any
other, so `/usage` already accounts for them. Ephemeral worktree paths must
map back to the parent project for this to hold.

**Session moves and trashing.** A run record holds a session ID, not a
pointer. If the session is trashed or moved, the run history shows it as
unavailable rather than breaking.

---

## 16. Non-Goals (this version)

- Cloud or remote execution. Tasks run on this machine, while Trellis runs.
- Event-bus, API, and GitHub triggers. Schedule (§4.1) and log-match (§4.4)
  triggers ship; the list is shaped for the rest (§4.6).
- Log-match rules against services' own logs (`services[].logging`) rather
  than `log_viewers`. Same pipeline, deferred only to keep the first version
  narrow.
- Per-task saved permission allow-rules (§6).
- Branching or conditional prompt sequences. Steps are linear.
- Slash commands in prompts (§7.1).
- A task that starts a pair or checklist run.
- Multi-agent scheduled runs (one session per run).

---

## 17. Implementation Notes

### 17.1 Package layout

`internal/schedule/`:

- `types.go` — `Task`, `Run`, `Trigger`, `Target`, `Teardown`, `Limits`,
  state/outcome/skip-reason enums, `DefaultLimits`.
- `definition.go` — parse and validate `<name>.md` (frontmatter + `## Step`
  splitting).
- `cron.go` — preset → schedule resolution, next-fire-time calculation,
  stagger, missed-fire detection.
- `store.go` — per-task and per-run JSON persistence (mirrors `pair.Store`).
- `registry.go` — process-wide registry; owns the ticker, the concurrency
  cap, validation, rehydrate, shutdown.
- `driver.go` — the per-run `RunRuntime` state machine.
- `worktrees.go` — ephemeral worktree provisioning and the reaper.
- `logmatch.go` — per-viewer subscription, rule evaluation via
  `logs.ParseFilter`, fingerprinting, settle window, dedupe, rate cap, and the
  storm breaker.
- `payload.go` — payload assembly (context, trace correlation, aggregates)
  and `<log-match-payload>` rendering with truncation.
- `render.go` — prompt template resolution: the `{{.Fields.*}}` namespace,
  bare-vs-delimited rendering by value origin (§4.4.3), the untrusted-content
  preamble, and `unresolved_fields` collection.

Wired in `internal/app/app.go` after the checklist registry and shut down
before it; routed in `internal/api/router.go`; handled in
`internal/api/handlers/schedule.go`; rendered by `views/schedules.qtpl`,
`views/schedule_edit.qtpl`, and `views/schedule_detail.qtpl` with
`static/js/schedule.js` and `static/css/schedule.css` (§11.8).

Adds one dependency: `github.com/robfig/cron/v3` for expression parsing and
next-time calculation.

### 17.2 Extract `pair.Agents` into `internal/agentctl`

`pair.Agents` is already the agent-agnostic control facade this feature needs
— `LookupStatus`, `CaptureLastAssistantText`, `SendUserMessage` — and it is
only in `internal/pair` for historical reasons. Move it to
`internal/agentctl` and extend it with the operations a scheduler needs and
the managers already implement:

```go
func (a *Agents) CreateSession(agent, worktree, workDir, displayName string) (string, error)
func (a *Agents) SetAutoApprove(ref AgentRef, on bool) error
func (a *Agents) ArchiveSession(ref AgentRef) error
func (a *Agents) TrashSession(ref AgentRef) error
func (a *Agents) DeleteSession(ref AgentRef) error
func (a *Agents) HasSubscribers(ref AgentRef) bool
```

Migrate without touching pair or checklist call sites by leaving aliases
behind:

```go
package pair

type Agents = agentctl.Agents
type AgentRef = agentctl.AgentRef
```

Three drivers then sit on one facade, and the next one is cheaper still.

### 17.3 Reuse over extension

- Idle detection, debounce, and the `session.state_changed` subscription come
  from `pair/driver.go` unchanged in behavior.
- Success-signal matching reuses `checklist.IsCompletionSignal`.
- Archive, trash, delete, and search are existing manager methods.
- Worktree creation and removal go through `worktree.WorktreeManager`.
- The stalled-run UI is the existing inbox.

### 17.4 Prerequisites in existing packages

Two small changes outside `internal/schedule` that this spec depends on. Both
are independently useful and should land first.

**Attributable notifications.** `internal/api/handlers/notify.go` publishes
`notify.*` events with a `message` payload and nothing else, and the agent
processes in `claude/manager.go` and `codex/manager.go` are spawned without a
custom `cmd.Env`, so a session has no way to identify itself to `trellis-ctl`.
Together these make §8.2's rules 2 and 3 impossible. The fix:

- Spawn agent processes with `TRELLIS_SESSION_ID` and `TRELLIS_WORKTREE` in
  the environment (alongside the `TRELLIS_API` that terminal windows already
  get via tmux).
- Have `trellis-ctl notify` forward them, and `NotifyRequest` accept them.
- Add `session_id` and `worktree` to the `notify.*` event payload.

This makes every notification attributable, not just scheduled ones — the
inbox and `/events` benefit immediately.

**Worktree start-point.** `worktree.WorktreeManager.Create` takes
`(ctx, branchName, switchTo)` and shells out to `git worktree add -b <branch>
<path>`, always branching from the repo's current `HEAD`. Ephemeral runs need
to branch from a named base regardless of what `HEAD` happens to be at 3am, so
`Create` needs an optional start-point threaded into that command.

Note also that `Create` sanitizes `/` to `-` for the directory name and
rejects collisions, so the `sched/<task>/<run-id>` branch lands in a worktree
directory named `<project>-sched-<task>-<run-id>`. That is fine, but the
reaper must match on the branch, not the directory name.

### 17.5 Tests

- `internal/schedule/cron_test.go` — preset resolution, DST boundaries,
  stagger determinism, missed-fire collapsing (a daily task down six days
  produces exactly one catch-up plus one skip record).
- `internal/schedule/definition_test.go` — frontmatter parsing, `## Step`
  splitting, preamble handling, size limit.
- `internal/schedule/driver_test.go` — fake agents and fake bus driving the
  run machine: success signal, `ran` fallback, step timeout, approval stall →
  `blocked` with teardown skipped, teardown guards.
- `internal/schedule/worktrees_test.go` — reaper keeps a worktree with
  commits, removes an empty one, honors `keep_worktrees`.
- `internal/schedule/logmatch_test.go` — filter reuse against real parsed
  entries; fingerprint grouping; settle-window batching; dedupe across the
  window; cooldown and rate cap producing exactly one suppressed record each;
  storm breaker tripping, staying tripped, and resetting. The load case is
  explicit: **10,000 identical matches in one second produce exactly one
  run**, and 10,000 distinct fingerprints produce at most
  `max_fires_per_hour`.
- Coalescing specifically: a repeat while the run is live increments its
  counter and creates no run; the same fingerprint after that run reaches a
  terminal state fires a **new** run once `cooldown_s` has elapsed; a
  `blocked` run with a live session still absorbs repeats up to
  `max_coalesce`.
- `internal/schedule/payload_test.go` — context and trace assembly, host
  aggregation, truncation order (context dropped before the matched line),
  and that no payload text escapes the `<log-match-payload>` block.
- `internal/schedule/render_test.go` — the field namespace including nested
  paths and absent fields; that Trellis-origin values render bare and
  log-origin values render delimited; that a log value containing a fake
  delimiter, a closing tag, or "ignore previous instructions" cannot escape
  its span; that the preamble is prepended exactly once and only when a log
  value is referenced.
- e2e: a `manual` task with a two-step prompt, run via **Run now**, ending in
  an archived session that the archive search can find.
- e2e: a log-match task against a `command`-source viewer fed synthetic
  crash lines, asserting one session with the trace in its payload.

---

## 18. Open Questions

1. **Should `queue` overlap actually ship?** `skip` covers the real cases and
   a queue that backs up behind a stalled run is its own failure mode.
   Shipping `skip`-only until something demands otherwise is defensible.
2. **Is `consecutive_unopened` the right unengagement signal?** It counts
   sessions never viewed, which misses a run you read via `trellis-ctl`.
   Practice will tell.
3. **Should ephemeral runs push their branch?** Currently they do not, so
   work lives only in the local worktree the reaper is instructed to keep.
   Auto-pushing `sched/*` branches would make the work durable and the reaper
   simpler, at the cost of remote noise.
4. **Is `{file}:{line}` the right default fingerprint?** It is right for the
   errord JSON parser, which supplies both. A viewer whose parser supplies
   neither falls back to the whole message, which dedupes almost nothing. The
   preview (§11.1) makes this visible, but a better fallback — first stack
   frame, or a normalized message with numbers stripped — may be worth having.
5. **How large a buffer window should the rule preview replay?** The viewer
   holds 50,000 entries for errord; replaying all of it on every keystroke is
   too slow, and replaying a minute of it tells you nothing about a rule meant
   to fire twice a week. The preview may need its own time-range control.
6. **Per-task model selection.** The record has a `model` field and the
   session API supports setting it, but a task pinned to a model will silently
   age. Worth a warning in the UI when the pinned model is no longer offered.
