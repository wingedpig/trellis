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

| Environment Variable | Description | Default |
|---------------------|-------------|---------|
| `TRELLIS_API` | Base URL of Trellis API | `http://localhost:1234` |

In Trellis-managed tmux sessions, `TRELLIS_API` is set automatically.

## Global Flags

| Flag | Description |
|------|-------------|
| `-json` | Output in JSON format |

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
             Type: date (YYYY-MM-DD)

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

### Trace Commands

```bash
# Execute a trace
trellis-ctl trace <id> <group> [options]

# Options
-since <time>         # Start time (1h, 30m, 6:00am, 2025-01-10)
-until <time>         # End time (default: now)
-name <name>          # Report name
-no-expand-by-id      # Disable ID expansion

# Examples
trellis-ctl trace abc123 api-flow -since 1h
trellis-ctl trace "user-456" auth-flow -since 6:00am -until 7:00am
trellis-ctl trace abc123 api-flow -since 2025-01-10 -until 2025-01-10

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

Create `.claude/skills/trellis/SKILL.md` to teach Claude about trellis-ctl:

```markdown
---
name: trellis
description: Control the Trellis development environment
---

Use `trellis-ctl` to interact with Trellis. The `TRELLIS_API`
environment variable is automatically set.

## Commands
- `trellis-ctl status` - Check service status
- `trellis-ctl logs <service>` - View logs
- `trellis-ctl restart <service>` - Restart after fixes
- `trellis-ctl workflow run build` - Run builds
- `trellis-ctl crash newest` - View crash details
```
