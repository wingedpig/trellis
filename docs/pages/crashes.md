---
title: "Crashes Page"
weight: 4
---

# Crashes Page

**URL:** `/crashes`

The Crashes page lists crash reports generated when services exit unexpectedly. Use it to investigate failures and debug issues.

## Crash Report List

When a service crashes, Trellis captures:

- **Name** — Unique crash report ID (timestamp-based)
- **Service** — Which service crashed
- **Trace ID** — If the crash output contained a trace ID, it's extracted and linked
- **Exit Code** — The process exit code (non-zero indicates error)
- **Error** — Summary of the error message or signal
- **Created** — When the crash occurred

Click on a crash report name to view the full details.

## Crash Report Details

The detail view (`/crashes/<id>`) shows:

- **Full error message** — Complete error output
- **Stack trace** — If available, the full stack trace
- **Last output** — The final lines of stdout/stderr before the crash
- **Environment** — Service configuration at the time of crash

## Managing Crash Reports

- **Delete** — Remove individual crash reports using the trash icon
- **Clear All** — Remove all crash reports at once

Crash reports are stored on disk in the `.trellis/crashes/` directory and persist across Trellis restarts.

## Trace ID Extraction

If your service logs include a trace ID or request ID when crashing, Trellis attempts to extract it. This allows you to:

1. See the trace ID in the crash list
2. Use the [Trace page](/docs/pages/trace/) to search for related log entries across all services

Configure the trace ID pattern in your service's crash settings if automatic extraction doesn't work.

## Related

- [Configuration: crashes](/docs/reference/config/#crashes) — Crash reporting configuration
- [trellis-ctl crashes](/docs/reference/trellis-ctl/#crashes) — CLI crash commands
