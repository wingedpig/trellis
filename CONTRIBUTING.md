# Contributing to Trellis

Thank you for your interest in contributing to Trellis.

Trellis is an open-source project maintained by **Groups.io, Inc.** Contributions of all kinds—bug reports, documentation improvements, and code—are welcome.

By participating in this project, you agree to follow the guidelines below.

---

## License and Contributions

Trellis is licensed under the **Apache License, Version 2.0**.

By submitting a contribution (code, documentation, or other material), you agree that:

- Your contribution is licensed under the Apache License 2.0.
- You have the right to submit the contribution.
- You grant Groups.io, Inc. the rights described in the Apache License, including the patent license.

No separate Contributor License Agreement (CLA) is required at this time.

---

## How to Contribute

### Reporting Bugs
- Use GitHub Issues.
- Include:
  - What you expected to happen
  - What actually happened
  - Steps to reproduce
  - Relevant logs or configuration snippets (redact secrets)

### Suggesting Features
- Open a GitHub Issue describing:
  - The problem you’re trying to solve
  - Why existing functionality is insufficient
  - Any prior art or similar tools

Design-heavy features may be discussed before implementation.

---

## Development Guidelines

### Code Style
- Go code should be formatted with `gofmt`.
- Follow standard Go conventions.
- Prefer clarity over cleverness.

### Scope
Trellis intentionally avoids:
- Heavy abstractions
- Magic configuration behavior
- Implicit side effects

Changes should align with Trellis’s design principles:
- Explicit configuration
- Worktree-scoped behavior
- Predictable process lifecycle
- Debuggability over automation

---

## Commit Messages

Use clear, descriptive commit messages. Examples:

- `service: fix restart race when binary changes`
- `logs: support context lines for grep`
- `docs: clarify worktree lifecycle hooks`

---

## Review Process

- All changes are reviewed by a maintainer.
- You may be asked to revise or simplify a change.
- Large or architectural changes should start with an issue or discussion.

---

## Security Issues

Do **not** report security vulnerabilities via public GitHub issues.

Instead, contact: security@groups.io

---

## Code of Conduct

This project follows a simple rule:

**Be respectful and professional.**

Harassment, abuse, or disruptive behavior will not be tolerated.

