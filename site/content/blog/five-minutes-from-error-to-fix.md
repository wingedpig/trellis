---
title: "Five Minutes From Error to Fix"
subtitle: "A real example of how I use Trellis to debug and fix production issues."
date: 2026-01-17
draft: false
---

I was checking production logs in Trellis and noticed an error: "group not found."

Not urgent. But wrong.

I grabbed the trace_id and went into Claude Code. I didn't dig through logs or reconstruct anything myself. I just said:

> Around 6 hours ago, we saw a "group not found" error in production on the web. Use Trellis to trace web.app01-g2.2838584.1768732203818824464 and figure out what happened. Notify me when you're done.

Then I went and did something else.

Claude knows how to use Trellis. I have a skill file that teaches it trellis-ctl, so it fetched the trace on its own, followed the request through the code, and figured out what was going on.

When it was done, it notified me.

The cause was simple: a crawler was hitting a legacy route we don't link to anymore. Nothing was broken. The route was just noise.

I told Claude to remove it.

It made the change. I ran a build workflow in Trellis, committed the fix, switched back to my main worktree, merged it, and deleted the worktree used for the fix.

Five minutes, start to finish. For most of that time, I wasn't even paying attention.

Before Trellis, this would have meant figuring out which production machine logged the error, SSHing into it, grepping through logs to reconstruct the request, then pasting that into Claude or stepping through it myself. Not hard—but annoying enough that it often gets deferred.

That friction matters. It's the difference between noticing a problem and fixing it.

That's what Trellis is for.
