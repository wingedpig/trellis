---
title: "trellis-ctl"
weight: 22
---

# trellis-ctl Reference

`trellis-ctl` is the command-line tool for controlling a running Trellis instance.

## Installation

```bash
# Build with Trellis
make build

# Or build directly
go build -o trellis-ctl ./cmd/trellis-ctl
```

## Configuration

`trellis-ctl` resolves the API URL in this order (highest precedence first):

1. `TRELLIS_API` environment variable — set automatically in Trellis-managed
   tmux sessions, so commands run inside Trellis terminals always reach the
   right instance.
2. `-config <path>` flag or `TRELLIS_CONFIG` environment variable — load the
   specified `trellis.hjson` and use its `server.public_url` if set; otherwise
   build the URL from `server.host`, `server.port`, and the configured TLS mode
   (`server.tls_tailscale` or `server.tls_cert` / `server.tls_key`).
3. `trellis.hjson` (or `trellis.json`) auto-discovered by walking up from the
   current directory, resolved the same way.
4. `http://localhost:1234` fallback if no config or env override is found.

`server.public_url`, when set, is used verbatim — useful when the bind
address differs from the address clients should connect to (e.g., binding
to a Tailscale IP whose certificate is issued for the machine's Tailscale
hostname rather than the IP). Example:

```hjson
server: {
  host: "100.80.99.38"                            // bind address
  port: 1234
  tls_cert: "..."
  tls_key: "..."
  public_url: "https://mybox.tail-net.ts.net:1234"  // address clients use
}
```

When `public_url` is not set: if `server.host` is `0.0.0.0` or `::`,
`trellis-ctl` connects to `127.0.0.1` instead, and when `tls_tailscale` or both
`tls_cert` and `tls_key` are set the scheme becomes `https`. Because Tailscale
TLS requires the machine's `*.ts.net` SNI hostname, set `public_url` when using
`tls_tailscale` with an IP or wildcard bind address.

The same resolution is used by the Trellis daemon to compute the
`TRELLIS_API` value injected into Trellis-managed tmux sessions, so the CLI
inside terminals reaches the instance via the correct URL automatically.

| Environment Variable | Description |
|---------------------|-------------|
| `TRELLIS_API` | Base URL of Trellis API; overrides config-based discovery. |
| `TRELLIS_CONFIG` | Path to `trellis.hjson`; used when `-config` is not given. |

## Global Flags

| Flag | Description |
|------|-------------|
| `-json` | Output in JSON format |
| `-config <path>` | Path to `trellis.hjson` to read connection info from. |

The `-json` flag works with any command:
```bash
trellis-ctl -json status
trellis-ctl status -json
```

## Commands

### Service Commands

```bash
# List all services
trellis-ctl status

# Get specific service status
trellis-ctl status <service>

# Control services
trellis-ctl start <service>
trellis-ctl stop <service>
trellis-ctl restart <service>
```

**Example output:**
```
SERVICE              STATE      PID      RESTARTS   ERROR
backend              running    12345    0
frontend             running    12346    0
worker               crashed    -        3          exit code 1
```

### Log Commands

```bash
# Basic log viewing
trellis-ctl logs <service>              # Last 100 lines
trellis-ctl logs <service> -n 50        # Last 50 lines
trellis-ctl logs <service> -f           # Follow mode

# Log viewers
trellis-ctl logs -viewer <name>         # View log viewer
trellis-ctl logs -list                  # List available viewers

# Time filtering
trellis-ctl logs <service> -since 1h
trellis-ctl logs <service> -since 30m
trellis-ctl logs <service> -since 6:30am
trellis-ctl logs <service> -since 6:00am -until 7:00am

# Level filtering
trellis-ctl logs <service> -level error
trellis-ctl logs <service> -level warn,error
trellis-ctl logs <service> -level info+

# Pattern filtering
trellis-ctl logs <service> -grep "pattern"
trellis-ctl logs <service> -grep "panic|fatal"
trellis-ctl logs <service> -field host=prod1

# Context lines
trellis-ctl logs <service> -grep "error" -B 5      # 5 lines before
trellis-ctl logs <service> -grep "error" -A 10     # 10 lines after
trellis-ctl logs <service> -grep "error" -C 3      # 3 lines both

# Output formats
trellis-ctl logs <service> -json
trellis-ctl logs <service> -jsonl
trellis-ctl logs <service> -csv
trellis-ctl logs <service> -raw
trellis-ctl logs <service> -format "{{.timestamp}} [{{.level}}] {{.message}}"

# Management
trellis-ctl logs <service> -clear
trellis-ctl logs <service> -stats
```

#### BubbleUp: what's different about these lines?

`logs bubbleup` compares the lines matching a selection against every other line in the same time window and ranks the field values whose share differs most. Use it to turn "there are errors" into a lead ("90% of errors are on app02 and come from attach.go:212") before reading individual lines.

```bash
trellis-ctl logs bubbleup -where 'level:error' -group webpost -since 1h
trellis-ctl logs bubbleup -where 'status:>=500' -viewer nginx-access01-logs -since 30m
trellis-ctl logs bubbleup -where 'level:error host.name:app02-g2' -group webpost   # drill down
trellis-ctl logs bubbleup -where 'level:error' -baseline 'service.name:web' -viewer web01-logs,web02-logs
trellis-ctl -json logs bubbleup -where 'level:error' -group webpost             # for agents
```

```
Selection: 412 lines (level:error)   Baseline: 1284330 lines   Window: Oct 27 13:05–14:05
Viewers: web01-logs (601233), web02-logs (683509)

FIELD      VALUE                          SELECTION    BASELINE  DIFF
error      s3: RequestTimeout                 74.0%        0.0%  ▲▲▲  +74.0
file_line  messageserverd/attach.go:212       81.0%        7.2%  ▲▲▲  +73.8
host.name  app02-g2                           79.0%       50.0%  ▲▲   +29.0

Skipped (high-cardinality): email, msg, trace_id
```

| Flag | Description |
|------|-------------|
| `-where <filter>` | Selection, in log filter syntax (`level:error`, `status:>=500`, `host.name:app02-g2`). Required. |
| `-viewer <a,b>` | Log viewer(s) to read; repeatable or comma-separated |
| `-group <name>` | Read every viewer in a trace group |
| `-baseline <filter>` | Narrow the comparison set (default: all lines not in the selection) |
| `-since`, `-until` | Window (default: the last hour) |
| `-fields <a,b>` / `-exclude <a,b>` | Restrict or skip fields |
| `-top <n>` | Number of values shown (default 20, at most 3 per field) |

Lines come from each viewer's history (rotated files plus the current file), falling back to the live buffer for sources without history. Fields that are close to unique per line (trace IDs, timestamps, emails) are skipped automatically, as are fields named in `-where`/`-baseline`. Per-entity paths such as `/g/<group>/compose` will look high-cardinality; normalize them with a [`replace` derive](config.md#derived-fields) first.

### Workflow Commands

```bash
# List workflows
trellis-ctl workflow list

# Describe a workflow (show inputs and validation)
trellis-ctl workflow describe <workflow-id>

# Run a workflow (waits for completion)
trellis-ctl workflow run <workflow-id>

# Run a workflow with inputs
trellis-ctl workflow run <workflow-id> --input1=value1 --input2=value2

# Check status of a running workflow (uses run ID, not workflow ID)
# The run ID is returned by "workflow run" when using -json
trellis-ctl workflow status <run-id>

# Cancel a running workflow. Accepts a run ID, or a workflow ID to cancel
# that workflow's most recent in-flight run.
trellis-ctl workflow cancel <id>
```

**Structured results:** When a workflow has an `output_parser` configured (e.g. `go_compiler`, `go_test_json`), completed runs include a `Summary` rollup in the `-json` status output — error/warning counts, test pass/fail/skip counts, and the names of failing tests:

```json
"Summary": {
  "Errors": 0,
  "Warnings": 0,
  "TestsPassed": 41,
  "TestsFailed": 2,
  "TestsSkipped": 1,
  "FailedTests": ["github.com/acme/pkg.TestFoo", "github.com/acme/pkg.TestBar"],
  "FirstError": ""
}
```

`Summary` is `null` for workflows without an output parser. The human-readable `workflow run` output prints the same rollup as a `Summary:` line plus a `FAIL` line per failing test.

**Output streams:** Without `-json`, `workflow run` writes the workflow's stdout to stdout, and its stderr, the progress line, the `Summary:` rollup, and the final ✓/✗ line to stderr, so a workflow that prints JSON can be piped straight into `jq`. With `-json`, completed runs carry `Stdout` and `Stderr` alongside the interleaved `Output`, plus `Result` — the stdout parsed as JSON — when the stdout is a single JSON value:

```bash
trellis-ctl workflow run db-fetch --model=User --id=12345 | jq .Email
trellis-ctl -json workflow run db-fetch --model=User --id=12345 | jq .Result.Email
```

**Example: Discovering and running a workflow with inputs**

```bash
# See available workflows
$ trellis-ctl workflow list
ID              NAME              DESCRIPTION
find-email      Find Email        Search for an email by message ID or database ID
db-fetch        DB Fetch          Fetch a database row by table and ID

# Get details about a workflow's inputs
$ trellis-ctl workflow describe find-email
Workflow: find-email
Name: Find Email
Description: Search for an email by message ID or database ID

Inputs:
  --msgid    Email message ID (optional)
             Pattern: ^[a-zA-Z0-9._@<>-]+$

  --id       Database ID (optional)
             Pattern: ^[0-9]+$

  --date     Date to search (required)
             Type: datepicker (YYYY-MM-DD format)

# Run with validated inputs
$ trellis-ctl workflow run find-email --date=2024-01-15 --id=12345
{"email": "...", ...}
```

**Input validation:** When `pattern` or `allowed_values` are configured for an input, the server validates inputs before execution. Invalid inputs return an error without running the workflow.

### Worktree Commands

```bash
# List worktrees
trellis-ctl worktree list

# Activate a worktree
trellis-ctl worktree activate <name>
```

**Example output:**
```
NAME                 BRANCH               ACTIVE   STATUS               PATH
myproject            main                 *        ready                /Users/dev/src/myproject
myproject-feature    feature                       ready                /Users/dev/src/myproject-feature
```

### Task Commands

`task start` and `task finish` bracket a unit of work: a branch in its own worktree with an agent session, then a merge back into the default branch and cleanup.

```bash
# Create worktree + branch fix-digest-crash and start a Claude session in it
trellis-ctl task start fix-digest-crash -prompt "Investigate the newest crash report and fix it"

# Use Codex instead, with a model override and the prompt read from a file
trellis-ctl task start fix-digest-crash -agent codex -model gpt-6-astra -effort high -prompt-file prompt.md

# Merge the worktree containing the current directory, then clean up
trellis-ctl task finish

# Name the worktree, squash, and keep the branch
trellis-ctl task finish fix-digest-crash -strategy squash -m "Fix digest crash" -keep-branch
```

**`task start <branch>`** creates the branch and worktree (running `on_create` hooks), opens a session, and sends the first message. The session appears on the worktree's home page and in the navigation picker as `@<branch>`, like one created from the UI. The command prints its URL.

| Flag | Description |
|------|-------------|
| `-agent claude\|codex` | Agent to start (default `claude`) |
| `-prompt <text>` / `-prompt-file <path\|->` | First message; `-` reads stdin. Without one the session waits for input |
| `-model <m>`, `-effort <e>` | Model option (as offered in the session's model picker); validated before anything is created |
| `-name <name>` | Session display name |
| `-activate` | Switch to the new worktree (restarts services) |

**`task finish [worktree]`** defaults to the worktree containing the current directory. It:

1. Refuses if the worktree has uncommitted changes (untracked files included, since they would be deleted), if the default branch's worktree has uncommitted changes to tracked files, or if the worktree has a case that isn't wrapped up
2. With the default `ff` strategy, rebases the branch onto the default branch. On conflict, it aborts the rebase, lists the conflicting files, and leaves everything as it was
3. Runs `pre_merge` hooks in the worktree; a failure stops here
4. Merges: fast-forward (`ff`), merge commit (`merge`), or squash (`squash`); then runs `post_merge` hooks in the default branch's worktree
5. Switches to the default branch's worktree if the finished one was active, archives its Claude and Codex sessions (still searchable), runs `pre_remove` hooks, removes the worktree, and deletes the branch. For `ff` and `merge`, `git branch -d` is used, so git refuses to delete a branch that isn't merged

If an agent session in the worktree is mid-turn (typically because the agent ran `task finish` itself), the merge happens immediately and step 5 waits until every session there is idle.

| Flag | Description |
|------|-------------|
| `-strategy ff\|merge\|squash` | Default `ff` (rebase, then fast-forward) |
| `-m <message>` | Merge or squash commit message (default: derived from the branch). A squash commit keeps the trailers of the commits it replaces, such as `Trellis-Case` and `Co-Authored-By` |
| `-keep-branch` | Don't delete the branch |
| `-keep-worktree` | Merge only; leave the worktree and its sessions |

A branch with no commits beyond the default branch is not merged; the worktree is still cleaned up. `task` commands use a 30-minute HTTP timeout by default so long hooks can finish.

### Session Commands

`session start` opens a Claude or Codex session in a worktree that already exists, without creating a branch or running hooks.

```bash
# Start a Claude session in the worktree containing the current directory
trellis-ctl session start

# Start a Codex session in another worktree with a first message
trellis-ctl session start fix-digest-crash -agent codex -prompt "Review the last commit"
```

**`session start [worktree]`** takes a worktree name, branch, or `main`, and defaults to the worktree containing the current directory. Like `task start`, the session appears on the worktree's home page and in the navigation picker, and the command prints its URL. It takes the same `-agent`, `-prompt`, `-prompt-file`, `-model`, `-effort`, and `-name` flags as `task start` (but not `-activate`). It uses a 5-minute HTTP timeout by default.

### Case Commands

`case` reads the record of past work. Cases are archived into the repo at wrap-up, so these see every case merged into the worktree's branch. All take `-worktree <name>` (default: the worktree containing the current directory) and the global `-json` flag.

```bash
# Search archived cases (title, generated summary, commit descriptions, notes)
trellis-ctl case search rate limiter
trellis-ctl case search -kind bug -since 2026-06-01 -n 20 webhook

# Read a case: summary, commits, attached transcripts, links, notes
trellis-ctl case show 2026-05-13__stripe-api-fix

# Why is this line here? Commit, case, and the agent turn that wrote it
trellis-ctl case why pkg/limits/limiter.go:42
# The case behind a commit
trellis-ctl case why 9c1494d6a2
```

**`case search <query>`** ranks matches with the generated summary weighted highest. Flags: `-kind` (bug, feature, investigation, task), `-since` / `-until` (creation date, `YYYY-MM-DD`), `-n` (default 10), `-transcripts` (also match each transcript's first prompt).

**`case why <file>:<line>`** blames the line, then finds its case through, in order: the commit's `Trellis-Case` trailer, its `Trellis-Session` trailer (added to agents' own commits by the [commit hook](/docs/pages/cases/#agent-commits); it also narrows the search to that session's transcript), a legacy `[case: <id>]` tag in the message, the wrap-up commit that archived the case, or the case's recorded commit list. It then searches that case's transcripts for the edit that wrote the line (Edit/Write tool calls, Codex file changes, or a shell command containing the line) and prints the prompt that led to it and the agent's explanation. For commits with no case link, such as an agent running `git commit` itself without the commit hook, it looks for a transcript that wrote the exact line in any case active around the commit date. An uncommitted line is matched against the worktree's open case. The output's `linked by` field says which route found the case.

### Event Commands

```bash
# Show recent events
trellis-ctl events            # Last 50
trellis-ctl events -n 20      # Last 20
```

### Crash Commands

```bash
# List crashes
trellis-ctl crash list

# Show most recent crash
trellis-ctl crash newest

# Show specific crash
trellis-ctl crash <id>

# Delete a crash
trellis-ctl crash delete <id>

# Clear all crashes
trellis-ctl crash clear
```

### Tailscale Node Commands

```bash
# Show embedded Tailscale nodes (see tailscale_nodes in the config reference)
trellis-ctl tailscale-nodes

# All fields, including key expiry and certificate details
trellis-ctl tailscale-nodes -json
```

The table lists each node's hostname, state, FQDN and tailnet IPs. Below each node it shows, where relevant, the login URL (while in `needs-login`), a hostname-mismatch or key-expiry warning, certificate status, the last error and the state directory. Only nodes referenced by a proxy listener are listed. The command reads `GET /api/v1/tailscale-nodes`.

### Trace Commands

```bash
# Execute a trace
trellis-ctl trace <id> <group> [options]

# Options
-since <time>         # Start time (1h, 30m, 6:00am, 2026-01-10)
-until <time>         # End time (default: now)
-name <name>          # Report name
-no-expand-by-id      # Disable ID expansion

# Examples
trellis-ctl trace abc123 api-flow -since 1h
trellis-ctl trace "user-456" auth-flow -since 6:00am -until 7:00am
trellis-ctl trace abc123 api-flow -since 2026-01-10 -until 2026-01-10

# Trace across dev service logs (auto-generated group)
trellis-ctl trace "req-123" services -since 1h

# View reports
trellis-ctl trace-report -list
trellis-ctl trace-report <name>
trellis-ctl trace-report <name> -json
trellis-ctl trace-report -groups              # Shows auto-generated "services" group
trellis-ctl trace-report -delete <name>
```

### Notify Command

Send notifications to alert users:

```bash
# Task completed (default)
trellis-ctl notify "Refactoring complete"

# Waiting for input
trellis-ctl notify "Need database credentials" -type blocked

# Error occurred
trellis-ctl notify "Build failed with 3 errors" -type error
```

| Type | Use Case |
|------|----------|
| `done` | Task completed, user can review |
| `blocked` | Need user input to continue |
| `error` | Something failed |

### Other Commands

```bash
trellis-ctl version    # Show version
trellis-ctl help       # Show help
```

## Common Patterns

### After Code Changes
```bash
# Check if services restarted
trellis-ctl status

# If crashed, view the crash
trellis-ctl crash newest
```

### Debugging a Crash
```bash
# View most recent crash
trellis-ctl crash newest

# View crash history
trellis-ctl crash list

# View specific crash
trellis-ctl crash <id>
```

### Running Builds
```bash
# Run build workflow
trellis-ctl workflow run build

# Check services restarted
trellis-ctl status
```

### Searching Logs
```bash
# Find errors in last hour
trellis-ctl logs backend -level error -since 1h

# Search for pattern with context
trellis-ctl logs backend -grep "timeout" -C 5

# Export as JSON for analysis
trellis-ctl logs backend -since 1h -json > logs.json
```

## Claude Code Integration

Trellis installs its skill file automatically: on startup it writes `.claude/skills/trellis/SKILL.md` into the repo and every worktree (and into new worktrees as they're created), so Claude Code discovers trellis-ctl without any setup. Installed copies carry a `managed-by: trellis` marker and are refreshed when the bundled skill changes after an upgrade; remove the marker line to take ownership of a copy, or disable installation entirely:

```hjson
agent: {
  install_skill: false
}
```

The skill teaches Claude to check service status, read and filter logs, run validation workflows (and read their structured `Summary` results), inspect crashes, and run distributed traces. See [Installation](/docs/install/#ai-assistant-integration) for manual setup (including Codex's `AGENTS.md`).
