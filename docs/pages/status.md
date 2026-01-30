---
title: "Status Page"
weight: 2
---

# Status Page

**URL:** `/status`

The Status page provides an overview of all configured services and their current state. Use it to monitor, start, and stop services.

## Service List

Services are organized into two collapsible sections:

### Stopped Services

Shows services that are not currently running. This section is expanded by default so you can quickly see what needs attention.

For each stopped service:
- **Name** — The service name from your configuration
- **Start** button — Start this individual service

### Running Services

Shows services that are currently running.

For each running service:
- **Name** — The service name
- **PID** — The process ID
- **Uptime** — How long the service has been running
- **Stop** button — Stop this individual service
- **Restart** button — Stop and restart the service

## Bulk Actions

The header provides buttons to control all services at once:

- **Start All** — Start all stopped services
- **Stop All** — Stop all running services
- **Refresh** — Reload the current status from the server

## Auto-Refresh

The page automatically refreshes service status periodically to keep the display current.

## Related

- [Services Concept](/docs/concepts/services/) — How Trellis manages services
- [Configuration: services](/docs/reference/config/#services) — Service configuration options
