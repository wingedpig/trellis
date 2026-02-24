---
title: "Concepts"
weight: 10
---

# Concepts

This section explains the core concepts behind Trellis.

## [Services](/docs/concepts/services/)

Services are long-running processes that Trellis manages. Learn about:
- Service lifecycle (start, stop, restart)
- Binary watching and automatic restarts
- Crash detection and reporting

## [Worktrees](/docs/concepts/worktrees/)

Trellis treats each git worktree as an isolated development environment. Learn about:
- Worktree discovery and management
- Environment isolation
- Switching between worktrees
- Template variables for worktree-aware configuration

## [Logging](/docs/concepts/logging/)

Trellis provides unified log viewing across multiple sources. Learn about:
- Log sources (files, SSH, Docker, Kubernetes)
- Log parsers (JSON, logfmt, regex)
- Filtering and searching
- Distributed tracing

## [Workflows](/docs/concepts/workflows/)

Workflows are parameterized commands you can run from the web UI or CLI. Learn about:
- Defining workflows with inputs
- Output parsers
- Service coordination
- Running from the web UI and CLI
