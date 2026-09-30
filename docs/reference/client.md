---
title: "Go Client Library"
weight: 23
---

# Go Client Library

Trellis provides an official Go client library at `pkg/client` for programmatic access to the API. The library provides typed access to all endpoints and is used internally by `trellis-ctl`.

## Installation

```bash
go get github.com/wingedpig/trellis/pkg/client
```

## Basic Usage

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/wingedpig/trellis/pkg/client"
)

func main() {
    // Create a client (default Trellis port is 1234)
    c := client.New("http://localhost:1234")

    ctx := context.Background()

    // List all services
    services, err := c.Services.List(ctx)
    if err != nil {
        log.Fatal(err)
    }

    for _, svc := range services {
        fmt.Printf("%s: %s\n", svc.Name, svc.Status.State)
    }

    // Start a service
    svc, err := c.Services.Start(ctx, "backend")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Started %s (PID: %d)\n", svc.Name, svc.Status.PID)
}
```

## API Versioning

The client supports Stripe-style date-based API versioning. By default, the latest version is used. Pin to a specific version for stability:

```go
c := client.New("http://localhost:1234", client.WithVersion("2026-01-17"))
```

The version is sent via the `Trellis-Version` HTTP header on each request.

## Configuration Options

```go
import "time"

c := client.New("http://localhost:1234",
    client.WithVersion("2026-01-17"),      // Pin API version
    client.WithTimeout(60 * time.Second),  // Custom timeout (default: 30s)
    client.WithHTTPClient(customClient),   // Custom http.Client
)
```

## Available Sub-Clients

| Sub-Client | Description |
|------------|-------------|
| `c.Services` | Service management (list, get, start, stop, restart, logs) |
| `c.Worktrees` | Worktree operations (list, get, activate, remove) |
| `c.Workflows` | Workflow execution (list, get, run, status) |
| `c.Events` | Event log access (list with filters) |
| `c.Logs` | Log viewer operations (list viewers, get entries, history) |
| `c.Trace` | Distributed tracing (execute, list/get/delete reports, list groups) |
| `c.Crashes` | Crash history (list, get, newest, delete, clear) |
| `c.Notify` | Notifications (send) |
| `c.Tasks` | Task lifecycle (start a branch with an agent session; merge and clean up) |

## Service Operations

```go
// List all services
services, _ := c.Services.List(ctx)

// Get a specific service
svc, _ := c.Services.Get(ctx, "backend")

// Start/stop/restart
svc, _ := c.Services.Start(ctx, "backend")
svc, _ := c.Services.Stop(ctx, "backend")
svc, _ := c.Services.Restart(ctx, "backend")

// Get log buffer (raw JSON)
logs, _ := c.Services.Logs(ctx, "backend", 100)

// Clear log buffer
_ = c.Services.ClearLogs(ctx, "backend")
```

## Worktree Operations

```go
// List all worktrees
worktrees, _ := c.Worktrees.List(ctx)

// Get a specific worktree
wt, _ := c.Worktrees.Get(ctx, "feature-branch")

// Activate a worktree (switches active environment)
result, _ := c.Worktrees.Activate(ctx, "feature-branch")
fmt.Printf("Activated %s in %s\n", result.Worktree.Name(), result.Duration)

// Remove a worktree (runs pre_remove hooks first; a failing hook blocks removal)
_ = c.Worktrees.Remove(ctx, "old-branch", &client.RemoveOptions{
    DeleteBranch: true,  // Also delete the git branch
})
```

## Task Operations

`Tasks.Start` creates a branch and worktree and opens an agent session in it. `Tasks.Finish` merges the branch into the default branch and cleans up. They're the API behind [`trellis-ctl task`](/docs/reference/trellis-ctl/#task-commands). Both can run lifecycle hooks (tests, builds), so give the client a long timeout.

```go
c := client.New(url, client.WithTimeout(30*time.Minute))

// Start: Agent defaults to "claude"; Prompt is optional
res, err := c.Tasks.Start(ctx, client.TaskStartRequest{
    Branch: "fix-digest-crash",
    Agent:  "codex",
    Prompt: "Investigate the newest crash report and fix it",
})
// res.SessionURL is a path relative to the Trellis base URL,
// keyed by the worktree's short name: /codex/fix-digest-crash/<id>
fmt.Println(c.BaseURL() + res.SessionURL)

// Finish: rebase + fast-forward by default ("merge" or "squash" also accepted)
fin, err := c.Tasks.Finish(ctx, client.TaskFinishRequest{Worktree: "fix-digest-crash"})
if err != nil {
    if apiErr, ok := err.(*client.APIError); ok {
        // Details may carry "conflicts" and "hook_results" (failed pre_merge hooks)
        fmt.Println(apiErr.Message, apiErr.Details["conflicts"])
    }
    return err
}
switch {
case fin.CleanupDeferred:
    // An agent in the worktree was mid-turn; cleanup runs once it's idle
case fin.Cleanup != nil && fin.Cleanup.Error != "":
    // Merged, but cleanup failed (e.g. a pre_remove hook)
}
```

## Workflow Operations

```go
// List workflows
workflows, _ := c.Workflows.List(ctx)

// Run a workflow (returns immediately)
status, _ := c.Workflows.Run(ctx, "build", nil)

// Run in a specific worktree
status, _ := c.Workflows.Run(ctx, "build", &client.RunOptions{
    Worktree: "feature-branch",
})

// Poll for completion
for status.State == client.WorkflowStateRunning {
    time.Sleep(500 * time.Millisecond)
    status, _ = c.Workflows.Status(ctx, status.ID)
}

if status.Success {
    fmt.Println("Workflow completed successfully")
    fmt.Println(status.Output)
}

// Structured rollup of parsed output (nil without an output parser)
if s := status.Summary; s != nil {
    fmt.Printf("tests: %d passed, %d failed\n", s.TestsPassed, s.TestsFailed)
    for _, name := range s.FailedTests {
        fmt.Println("FAIL", name)
    }
}

// Cancel a running workflow (run ID, or workflow ID for its latest run)
status, _ = c.Workflows.Cancel(ctx, "build")
```

## Event Operations

```go
// List recent events
events, _ := c.Events.List(ctx, &client.ListOptions{
    Limit: 50,
})

// Filter by type and time range
events, _ := c.Events.List(ctx, &client.ListOptions{
    Types:    []string{"service.started", "service.stopped"},
    Since:    time.Now().Add(-1 * time.Hour),
    Worktree: "main",
})
```

## Log Viewer Operations

```go
// List configured log viewers
viewers, _ := c.Logs.List(ctx)

// Get entries from live buffer
entries, _ := c.Logs.GetEntries(ctx, "nginx", &client.LogEntriesOptions{
    Limit: 100,
    Since: time.Now().Add(-1 * time.Hour),
    Level: "error",
})

// Get historical entries (from rotated files)
entries, _ := c.Logs.GetHistoryEntries(ctx, "nginx", &client.LogEntriesOptions{
    Grep:   "connection refused",
    Before: 3,  // Context lines before match
    After:  3,  // Context lines after match
})
```

## Distributed Tracing

```go
// Execute a trace query
result, _ := c.Trace.Execute(ctx, &client.TraceRequest{
    ID:         "req-abc123",       // Correlation ID to search for
    Group:      "web",              // Trace group (set of log viewers)
    Start:      time.Now().Add(-1 * time.Hour),
    End:        time.Now(),
    ExpandByID: true,               // Two-pass search for related entries
})

// Poll for completion
for {
    report, _ := c.Trace.GetReport(ctx, result.Name)
    if report.Status == "completed" {
        fmt.Printf("Found %d entries\n", report.Summary.TotalEntries)
        for _, entry := range report.Entries {
            fmt.Printf("[%s] %s: %s\n", entry.Source, entry.Level, entry.Message)
        }
        break
    }
    time.Sleep(500 * time.Millisecond)
}

// List saved reports
reports, _ := c.Trace.ListReports(ctx)

// Delete a report
_ = c.Trace.DeleteReport(ctx, "trace-2026-01-17-abc123")

// List trace groups
groups, _ := c.Trace.ListGroups(ctx)
```

## Notifications

```go
// Send a notification
_, _ = c.Notify.Send(ctx, "Build complete!", client.NotifyDone)
_, _ = c.Notify.Send(ctx, "Waiting for input", client.NotifyBlocked)
_, _ = c.Notify.Send(ctx, "Build failed", client.NotifyError)
```

## Error Handling

API errors are returned as `*client.APIError`:

```go
svc, err := c.Services.Get(ctx, "unknown-service")
if err != nil {
    if apiErr, ok := err.(*client.APIError); ok {
        fmt.Printf("API error: %s - %s\n", apiErr.Code, apiErr.Message)
        // Common codes: "not_found", "invalid_request", "conflict"
    }
}
```

## Types Reference

Key types exported by the client library:

| Type | Description |
|------|-------------|
| `Service` | Service definition and status |
| `ServiceStatus` | Runtime state (State, PID, ExitCode, etc.) |
| `Worktree` | Git worktree info (Path, Branch, Commit, Dirty, etc.) |
| `Workflow` | Workflow definition (ID, Name, Command, etc.) |
| `WorkflowStatus` | Execution status (State, Success, Output, Summary, etc.) |
| `WorkflowSummary` | Structured rollup of parsed output (error/test counts, failed test names) |
| `Event` | Event log entry (Type, Timestamp, Payload) |
| `LogViewer` | Log viewer definition (Name, Description) |
| `LogEntry` | Parsed log entry (Timestamp, Level, Message, Fields) |
| `TraceRequest` | Trace query parameters |
| `TraceReport` | Complete trace results with entries |
| `TraceGroup` | Group of log viewers for tracing |
| `TaskStartRequest` / `TaskStartResult` | Task start input; created worktree, session ID and URL, warnings |
| `TaskFinishRequest` / `TaskFinishResult` | Task finish input; merge result, cleanup result, deferral flag |
| `HookResult` | Lifecycle hook outcome (Name, Success, Duration, Output) |

## Documentation

For complete API reference, see:
```bash
go doc github.com/wingedpig/trellis/pkg/client
```
