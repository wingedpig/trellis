---
title: "Trace Page"
weight: 5
---

# Trace Page

**URL:** `/trace`

The Trace page lets you execute distributed traces across multiple log sources. Use it to correlate events by trace ID, request ID, or any other pattern that appears in your logs.

## Starting a Trace

To execute a trace:

1. **Trace ID** — Enter the pattern to search for (e.g., `req-abc123`, a UUID, or any grep pattern)
2. **Trace Group** — Select which group of log viewers to search
3. **Time Range** — Specify when to search:
   - **Range mode**: Enter start and end times (e.g., `1h` to `now`, or `6:00am` to `7:00am`)
   - **Day mode**: Select a specific date to search that entire day
4. **Report Name** — Optional custom name (auto-generated if empty)
5. Click **Execute**

## Trace Groups

Trace groups define which log sources to search together. Configure them in your `trellis.hjson`:

```hjson
trace_groups: [
  {
    name: "backend"
    log_viewers: ["api-logs", "worker-logs", "database-logs"]
  }
  {
    name: "all"
    log_viewers: ["api-logs", "worker-logs", "frontend-logs"]
  }
]
```

Click the **Groups** button in the header to see configured groups and their log viewers.

## Time Formats

The start and end time fields accept flexible formats:

- **Relative**: `1h`, `30m`, `2d` (hours, minutes, days ago)
- **Clock time**: `6:00am`, `14:30`, `9pm`
- **Special**: `now` (current time)

## Trace Reports

After executing a trace, a report is generated showing:

- **All matching log entries** from all log viewers in the group
- **Sorted chronologically** across sources
- **Source column** showing which log viewer each entry came from
- **Entry details panel** for inspecting individual entries

Reports are saved and listed in the **Trace Reports** table. Click a report name to view it, or delete old reports you no longer need.

## Report Features

When viewing a trace report:

- **Filter** — Search within the results
- **Timestamp toggle** — Switch between absolute and relative timestamps
- **Entry details** — Click any row to see all fields with copy buttons
- **Delete** — Remove the report when done

## Expand by ID

When **Expand by ID** is checked, Trellis searches for additional log entries that share extracted IDs (like request IDs or correlation IDs) from the initial results. This helps find related entries that may not contain the original trace pattern.

## Related

- [Configuration: trace_groups](/docs/reference/config/#trace_groups) — Trace group configuration
- [Configuration: log_viewers](/docs/reference/config/#log_viewers) — Log viewer configuration
- [trellis-ctl trace](/docs/reference/trellis-ctl/#trace) — CLI trace commands
