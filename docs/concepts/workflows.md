---
title: "Workflows"
weight: 14
---

# Workflows

Workflows are parameterized commands you can run from the Trellis web UI or CLI. They turn common tasks — builds, deploys, database operations, test runs — into repeatable actions with input dialogs, confirmation prompts, and structured output.

## Defining a Workflow

Define workflows in your `trellis.hjson`:

```hjson
{
  workflows: [
    {
      id: "build"
      name: "Build All"
      command: ["make", "build"]
    }
  ]
}
```

Every workflow needs an `id` (used in the CLI and URLs), a `name` (shown in the UI), and either `command` or `commands`.

### Single vs. Multi-Command

Use `command` for a single command:

```hjson
{
  id: "test"
  name: "Run Tests"
  command: ["go", "test", "-json", "-count=1", "./..."]
}
```

Use `commands` to run multiple commands sequentially. If any command fails, the remaining commands are skipped:

```hjson
{
  id: "db-reset"
  name: "Reset Database"
  commands: [
    ["./bin/dbutil", "reset"]
    ["./bin/dbutil", "seed"]
  ]
}
```

### Template Variables

Commands support Go template variables for worktree-aware paths:

```hjson
{
  id: "build"
  name: "Build"
  command: ["make", "-C", "{{.Worktree.Root}}", "build"]
}
```

See [Template Variables](/docs/reference/config/#template-variables) for the full list.

## Inputs

Workflows can prompt the user for input before execution. Inputs appear as a dialog in the web UI or as `--flag=value` arguments in the CLI.

### Input Types

**text** — Free-form text entry:

```hjson
{
  name: "version"
  type: "text"
  label: "Version Tag"
  placeholder: "e.g., v1.2.3"
  pattern: "^v[0-9]+\\.[0-9]+\\.[0-9]+$"
  required: true
}
```

**select** — Dropdown with predefined options:

```hjson
{
  name: "environment"
  type: "select"
  label: "Target Environment"
  options: ["staging", "production"]
  default: "staging"
  required: true
}
```

**checkbox** — Boolean toggle:

```hjson
{
  name: "dry_run"
  type: "checkbox"
  label: "Dry run (don't actually deploy)"
  default: false
}
```

**datepicker** — Date selector (defaults to today if no default specified):

```hjson
{
  name: "deploy_date"
  type: "datepicker"
  label: "Deploy Date"
}
```

### Using Inputs in Commands

Reference input values in commands and confirmation messages with `{{ .Inputs.<name> }}`:

```hjson
{
  id: "deploy"
  name: "Deploy"
  inputs: [
    { name: "env", type: "select", options: ["staging", "prod"], required: true }
    { name: "dry_run", type: "checkbox", label: "Dry run", default: false }
  ]
  command: [
    "./deploy.sh"
    "--env={{ .Inputs.env }}"
    "{{ if .Inputs.dry_run }}--dry-run{{ end }}"
  ]
}
```

### Validation

Text inputs support two validation mechanisms:

| Field | Description |
|-------|-------------|
| `pattern` | Regex that the value must match |
| `allowed_values` | Whitelist of acceptable values |

Invalid inputs are rejected before the workflow runs, both in the web UI and CLI.

## Confirmation

Require the user to confirm before a workflow runs:

```hjson
{
  id: "db-reset"
  name: "Reset Database"
  confirm: true
  confirm_message: "This will delete all data. Continue?"
  commands: [
    ["./bin/dbutil", "reset"]
    ["./bin/dbutil", "seed"]
  ]
}
```

The `confirm_message` field supports templates, so you can include input values:

```hjson
confirm_message: "Deploy to {{ .Inputs.environment }}?"
```

## Output Parsers

The `output_parser` field controls how Trellis displays workflow output:

| Parser | Description |
|--------|-------------|
| `go` | Parses Go compiler output. File:line references become clickable links to your editor. |
| `go_test_json` | Parses `go test -json` output. Shows pass/fail/skip status per test with timing. |
| `generic` | Line-by-line output with exit code summary. The default if no parser is specified. |
| `html` | Renders the output as HTML in the browser. Useful for formatted reports. |
| `none` | Suppresses output display entirely. |

Example with `go_test_json`:

```hjson
{
  id: "test"
  name: "Run Tests"
  command: ["go", "test", "-json", "-count=1", "./..."]
  output_parser: "go_test_json"
  timeout: "10m"
}
```

## Service Coordination

Workflows can interact with services:

**`requires_stopped`** — Stop specific services before the workflow runs, then restart them afterward:

```hjson
{
  id: "migrate"
  name: "Run Migrations"
  command: ["./bin/migrate", "up"]
  requires_stopped: ["api", "worker"]
}
```

**`restart_services`** — Restart all watched services after the workflow completes (useful for build workflows):

```hjson
{
  id: "build"
  name: "Build All"
  command: ["make", "build"]
  restart_services: true
}
```

## Timeouts

Set a maximum duration with the `timeout` field. If the workflow exceeds the timeout, the process is killed:

```hjson
{
  id: "test"
  name: "Run Tests"
  command: ["go", "test", "./..."]
  timeout: "10m"
}
```

Values use Go duration syntax: `"30s"`, `"5m"`, `"1h"`.

## Running Workflows

### Web UI

Open the workflow picker from the navbar or press **Cmd/Ctrl + /**. Select a workflow to run it. If the workflow has inputs, a dialog prompts you to fill them in. Output is displayed in a dedicated view with the configured parser.

### CLI

```bash
# List available workflows
trellis-ctl workflow list

# See a workflow's inputs and validation rules
trellis-ctl workflow describe deploy

# Run a workflow (waits for completion)
trellis-ctl workflow run build

# Run with inputs
trellis-ctl workflow run deploy --environment=staging --dry_run=true

# Check status of a running workflow (pass the run ID, not workflow ID)
trellis-ctl workflow status <run-id>
```

## Examples

### Build Workflow

A simple build that restarts services afterward:

```hjson
{
  id: "build"
  name: "Build All"
  command: ["make", "build"]
  output_parser: "go"
  timeout: "10m"
  restart_services: true
}
```

### Deploy Workflow

Inputs, confirmation, and service coordination:

```hjson
{
  id: "deploy"
  name: "Deploy"
  inputs: [
    { name: "environment", type: "select", label: "Environment", options: ["staging", "production"], default: "staging", required: true }
    { name: "deploy_date", type: "datepicker", label: "Deploy Date" }
    { name: "dry_run", type: "checkbox", label: "Dry run", default: false }
  ]
  confirm: true
  confirm_message: "Deploy to {{ .Inputs.environment }}?"
  command: ["./deploy.sh", "--env={{ .Inputs.environment }}", "--date={{ .Inputs.deploy_date }}", "{{ if .Inputs.dry_run }}--dry-run{{ end }}"]
  requires_stopped: ["api", "worker"]
  timeout: "15m"
}
```

### Database Query Workflow

Text inputs with HTML output:

```hjson
{
  id: "db-fetch"
  name: "DB Fetch"
  description: "Fetch a database row by table and ID"
  inputs: [
    { name: "table", type: "text", label: "Table", placeholder: "e.g., users", required: true }
    { name: "id", type: "text", label: "Row ID", placeholder: "e.g., 12345", pattern: "^[0-9]+$", required: true }
  ]
  command: ["./bin/dbutil", "fetch", "--table={{ .Inputs.table }}", "--id={{ .Inputs.id }}", "--format=html"]
  output_parser: "html"
}
```
