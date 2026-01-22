---
title: "Tracing a Deadlock"
subtitle: "A real concurrency bug, fully analyzed and fixed."
date: 2026-01-20
draft: false
---

I was scanning the Groups.io anomalous error log in Trellis and noticed something I hadn't seen before: a commit that unexpectedly resulted in a rollback. Deadlocks I've seen. But not in this part of the code.

Deadlocks are tricky to debug. By definition, they involve at least two processes fighting over locks. You need to understand what both of them were doing, and when, to see where they collided.

I grabbed the trace_id and went into Claude Code:

> Use Trellis to trace smtpd.msgproc01-g2.16094.1768940835389216810 and figure out what happened. Notify me when you're done.

Then I switched to another worktree and kept working on a feature.

A few minutes later, a browser notification popped up. Claude had fetched the trace, followed the request through the code, and figured out the cause: a moderator had approved two pending messages at the same millisecond. Both transactions needed locks on the same group and the same subscription. PostgreSQL detected the deadlock and rolled one of them back.

I then asked Claude:

> Can you find the other message processing operation?

It ran a second trace, correlated the timestamps, and showed me both sides of the collision—which transaction got its locks first, which one lost, and what each was trying to do when they collided.

Then it proposed a fix: move the group lock earlier in the function so all transactions acquire locks in the same order. No more circular wait.

I told it to make the change. It did. I ran a build, committed the fix, merged it, and deleted the worktree.

Five minutes, start to finish. For most of that time, I was working on something else.

Without Trellis, I'd have figured it out eventually. I know deadlocks involve two processes; I would have looked for the second one. But I'd have been the one grepping through logs, reconstructing timelines, and piecing together lock acquisition order. It's the kind of thing that takes an hour if you're being careful.

Having Claude do the tracing—twice—and then explain exactly what it found is what turned a careful hour into five minutes.
