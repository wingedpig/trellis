# Trellis — Code Review Findings

Working document. Generated 2026-06-03 from a full-repo review (`internal/*`, `cmd/*`,
`pkg/client`, `views/*.qtpl`, `static/js`). Check items off as they're fixed.

**Threat model this review is calibrated to:** no user auth *by design* — Trellis is a
local control panel whose only security boundary is loopback binding + CORS/Host/
WebSocket-origin checks. "Endpoint has no auth" is therefore not a finding; **bypasses of
that boundary are.**

Severity legend: 🔴 High (fix first) · 🟡 Medium · 🟢 Low / hardening.
Items marked ✅verified were confirmed against source during review; others are
high-confidence from focused sub-reviews with cited line numbers — confirm before fixing.

---

## 🔴 High — network exposure & XSS (do these first)

- [x] **1. Non-loopback bind opens an unauthenticated, command-executing control plane** ✅verified — **DONE (per decision: warnings-only). Hardened `permitAnyHost` to wildcard-only (specific-IP binds keep DNS-rebinding protection) + loud unauthenticated/no-TLS startup warnings. Remaining unauth-network-access is an accepted risk of the no-auth-by-design model; rely on Tailscale/LAN trust.**
  - `internal/app/app.go:757` · `internal/api/middleware/cors.go:50-58`
  - `permitAnyHost := !loopbackBind`. Binding to `0.0.0.0`, a LAN IP, or a Tailscale IP
    (encouraged by README / `trellis init`) sets `PermitAnyHost`, which **disables the
    DNS-rebinding Host check**. CORS only validates Origin `if origin != ""`, so any client
    sending **no `Origin` header** (curl, scripts, other hosts on the tailnet) reaches:
    `POST /api/v1/workflows/{id}/run` (runs configured shell commands),
    `POST /api/v1/worktrees` (lifecycle hooks), `POST /api/v1/terminal/{worktree}/windows`
    (spawns processes), service start/stop/restart → **unauthenticated remote code execution**.
    No TLS requirement or warning for non-loopback binds either (`app.go:758-765`).
  - **Fix:** require same-origin (`Origin`/`Sec-Fetch-Site`) or a CSRF token / shared secret
    on all mutating requests regardless of `PermitAnyHost`; refuse-or-warn on non-loopback
    bind without TLS; add a security note to the `init`-generated `host: "0.0.0.0"` comment.

- [x] **2. Stored XSS — AI transcripts & case notes rendered as raw HTML** ✅verified — **DONE**
  - `static/js/claude.js:1001` (+ :958, :1138, :1466, :1485, :2383, :590, :56)
  - `static/js/codex.js:610` (+ :769, :783, :824, :847)
  - `views/case_detail.qtpl:396`
  - All pipe model-generated text through `marked.parse()` → `innerHTML`. Library is
    **marked 12.0.1** (no built-in sanitizer); **DOMPurify is absent from the tree**.
    Anything the agent emits or echoes from a file/branch/repo it reads (`<img src=x
    onerror=…>`) executes with full same-origin access to the control panel and re-fires
    on every reopen of the persisted session.
  - **Fix:** `DOMPurify.sanitize(marked.parse(text))` at every sink in both JS files and
    the case-notes render. (Log viewer + server templates already escape correctly.)

- [x] **3. Path traversal — `cases`/`crashes` managers never sanitize IDs** ✅verified — **DONE**
  - `internal/cases/manager.go:128,144,262,346,441,…` · `internal/crashes/manager.go:386,407`
  - Every method joins `caseID`/`id` into a path verbatim, no `filepath.Base`/`..` guard.
    URL routes are saved by gorilla/mux path-cleaning, but **`internal/api/handlers/commits.go:31,251`
    reads `case_id` from the JSON body**, bypassing mux, and feeds it to `Get` then writes
    (`AppendCommit`, `SaveTrace`, `Update`, `SetSummary`). Confirmed `caseID="../../../x"`
    escapes the cases dir for read and write. `crashes` `Get`/`Delete` share the router-only
    protection (`Delete` = arbitrary `*.json` removal if reached off-URL).
  - **Fix:** validate at the top of every manager method — reject `filepath.Base(id) != id`
    or any `/`, `\`, `..` (mirror the existing `AttachEvidence` filename check).

---

## 🔴 High — process stability (panics / leaks in the running server)

- [x] **4. Double-close panic on log subscribers during worktree switch** ✅verified — **DONE**
  - `internal/service/logs.go:119-123` vs `:128-135`
  - `Unsubscribe` does `delete` then **unconditionally `close(ch)`**; `CloseAllSubscribers`
    (called on every worktree switch via `UpdateConfigs`) closes them all. Open SSE
    log-stream tab + worktree switch → deferred `Unsubscribe` closes an already-closed
    channel → **panic, whole process down.**
  - **Fix:** `if _, ok := b.subscribers[ch]; ok { delete(...); close(ch) }`.

- [x] **5. Duplicate CLI subprocess spawn (TOCTOU) → orphaned processes/pipes** ✅verified — **DONE**
  - `internal/claude/manager.go:936-991` · `internal/codex/manager.go:474`
  - `ensureProcess` checks `s.started` under lock, **releases the lock**, `cmd.Start()`,
    then re-locks to set `started=true`. Two concurrent callers (two tabs; connect racing
    send — WS handler calls this on every connect) both spawn; the second overwrites
    `s.cmd`/`s.cancel`, so the first is never killed or reaped — orphan process + leaked pipes.
  - **Fix:** hold the lock across spawn, or add a `starting` guard set before releasing.

- [x] **6. Pair driver double-relay / `RoundCount` data race** — **DONE (verified with `-race`)**
  - `internal/pair/driver.go:407-409, 427-540, 610`
  - `maybeCaptureAndRelay` runs from both the driver goroutine and the debounce
    `time.AfterFunc` goroutine, releasing `rt.mu` between the state check and `dispatchRelay`.
    Two can interleave → same turn relayed twice + `RoundCount` race, breaking the
    one-relay-per-hop rule and the `max_rounds` cost cap (PAIRING_SPEC §5/§6.2).
  - **Fix:** funnel relay work onto the single driver goroutine (debounce/ticker only
    signal), or hold a relay mutex across capture→dispatch and re-check state under it.

- [x] **7. SPA: agent pages never tear down WebSocket/timers on eviction → duplicate WS + leak** — **DONE**
  - `static/js/claude.js:2817-2838` · `static/js/codex.js:1218`
  - Neither page listens for `trellis:page-evicted` (which `spa.js` documents as the teardown
    hook); claude.js also adds document/window `visibilitychange`/`pageshow` listeners that
    are never removed. After visiting several sessions, every prior closure stays alive and on
    any tab focus all call `forceReconnect()` → duplicate WebSockets, duplicated server-side
    subscribers, unbounded listener/memory leak, 3s reconnect timer on detached DOM.
  - **Fix:** on `trellis:page-evicted`, `ws.close()` + `clearTimeout` + `removeEventListener`
    using named handlers.

- [~] **8. Event bus runs synchronous subscribers inline & serially; publish-under-lock** — **PARTIAL: `binary.changed` now async; `worktree.activated` left sync (changing it alters activate-response timing); publish-under-lock is latent only (no sync session-state subscribers exist)**
  - `internal/events/memory.go:131-152` · `internal/claude/manager.go:845` · `internal/codex/manager.go:367`
  - `Publish` invokes sync handlers in the publisher's goroutine in a loop; the app registers
    multi-second sync handlers (`worktree.activated` = stop-all → re-expand → start-all). One
    slow handler stalls delivery to all later subscribers. Compounding: `publishStateLocked`
    calls `bus.Publish` **while holding `s.mu`** — latent self-deadlock if any sync subscriber
    calls back into the session (only saved today by all subscribers being async).
  - **Fix:** snapshot payload and publish after releasing `s.mu`; make long app handlers
    `SubscribeAsync`, or dispatch sync handlers without head-of-line blocking.

---

## 🟡 Medium — correctness / robustness

- [x] **9. `cmd.Wait()` races stdout/stderr pipe readers (no WaitGroup before Wait)** — **DONE (readersDone WaitGroup drained before `cmd.Wait()`, mirrors workflow runner)**
  - `internal/service/process.go:338` — drops final crash/panic lines → **corrupts crash
    analysis** (`CrashAnalyzer` consumes exactly those lines). Workflow runner does it
    correctly (`wg.Wait()` before `cmd.Wait()`); mirror that here.

- [x] **10. `ExpandConfig` aliases `LogViewers`/`TraceGroups`; app mutates shared config & grows it** — **DONE (deep-copy both in `ExpandConfig`; `Initialize` now mutates the expanded config, not `originalConfig`; `createServiceLogViewers` replaces-by-name instead of appending dupes)**
  - `internal/config/template.go:111` · `internal/app/app.go:1168,1196`
  - `expanded := *cfg` reassigns Services/Workflows/Proxy but **not** `LogViewers`/`TraceGroups`,
    so they alias the original; `createServiceLogViewers` appends with no dedup on every
    worktree switch → viewer list grows each switch.
  - **Fix:** deep-copy `LogViewers` + `TraceGroups` (and each `TraceGroup.LogViewers`) in
    `ExpandConfig`; make `createServiceLogViewers` idempotent.

- [x] **11. Streaming log sources leak zombie + FD on cancel; >1 MB line aborts the stream** — **DONE (shared `sourceBase.forwardLines` with ReadString+truncate; `cmd.Wait()` now runs on every exit path; docker/k8s `ReadRange` kill+Wait on cancel and k8s early-stop)**
  - `internal/logs/source_{ssh,command,docker,k8s}.go` — ctx-cancel path skips `cmd.Wait()`
    (zombie + pipe FD leak per stopped/idle-timed-out/reloaded viewer); `bufio.Scanner` with
    1 MB cap returns `ErrTooLong` and stops the tail on one oversized log line. `FileSource`
    handles both correctly — make these consistent (deferred Wait; `ReadString('\n')` + truncate).

- [x] **12. `restartCount` never decays — supervisor permanently gives up** — **DONE (counter resets in `handleExit` when the exiting process had ≥1 min uptime)**
  - `internal/service/manager.go:712` — counter resets only on manual restart / worktree
    update. A `policy: always` service that crashes `maxRestarts` times over its whole
    lifetime (even with hours of health between) stays dead.
  - **Fix:** reset the counter after a restarted process stays up past a threshold
    (sliding-window crash-loop detection).

- [x] **13. Workflow child processes leak grandchildren on cancel/timeout** — **DONE (`Setpgid` + `cmd.Cancel` kills `-pgid` with SIGKILL)**
  - `internal/workflow/runner.go:398` — no `Setpgid`, so SIGKILL hits only the direct child.
    `make`/`go test`/script descendants leak. Mirror the service path (Setpgid + kill `-pgid`).

- [x] **14. Codex dispatches each notification on its own goroutine — out-of-order turn/item** — **DONE (single ordered worker drains a buffered notify queue; server-requests stay per-frame)**
  - `internal/codex/jsonrpc.go:140` — breaks the ordered stream protocol; intermittent
    dropped/misattributed assistant content. Serialize notification handling on one worker
    (keep blocking server-requests/approvals on their own goroutines).

- [x] **15. `Cancel()` uses `context.Background()` (no timeout) for `turn/interrupt`** — **DONE (3s bounded context; `shutdownProcess()` runs regardless)**
  - `internal/codex/manager.go:704` — "Stop" on a wedged turn can hang the HTTP handler.
    Use a short bounded context; proceed to `shutdownProcess()` regardless.

- [x] **16. Watcher: fsnotify-on-file goes stale after first atomic rename; debounce goroutines escape `Close()`** — **DONE (parent-dir watches with per-dir refcount + name filter; `Debouncer.Stop` is terminal and waits for in-flight callbacks; two-rename regression test added; bonus: not-yet-built binaries in existing dirs are now watchable)**
  - `internal/watcher/binary.go:60,141` — watch follows the old inode after `go build` +
    rename, so auto-restart works only for the first rebuild; `time.AfterFunc` callbacks
    aren't tracked by the WaitGroup, so `Close()` can return before a `binary.changed` publish
    runs (publish-after-close race). Watch the parent dir + filter by name; track callbacks in
    the WaitGroup; bail if `closed`. (A single-rename test masks this — add a two-rename test.)

- [x] **17. Trace report name collision + non-atomic `Save`** — **DONE (Execute reserves the name in `activeTraces` under lock, rejects an active duplicate; `Save` is tmp+rename)**
  - `internal/trace/manager.go:217,245` · `internal/trace/storage.go` — same `reportName`
    clobbers another trace's cancel func (orphaned uncancellable goroutine) and races the
    report file; `Save` is plain `os.WriteFile` (no tmp+rename, unlike the other stores).
    Reject/uniquify an active name; make `Save` atomic.

- [x] **18. Pair control commands silently dropped on full channel; sleep-then-read race** — **DONE (Stop records a flag under `rt.mu` checked every loop iteration — never droppable; Pause/Resume/Stop/UpdateConfig/Confirm use per-command ack with 2s bound; handler sleeps removed; ForceRelay stays fire-and-forget)**
  - `internal/pair/driver.go:160` · `internal/api/handlers/pair.go:154` — non-blocking send
    drops on full buffer (incl. `Stop` → loop keeps relaying against user intent); handlers
    `time.Sleep` then read state. Make `Stop` guaranteed (flag under `rt.mu` or unbuffered
    signal); replace sleep with a synchronous per-command ack.

- [x] **19. Argument injection: branch/window names reach `git`/`tmux` unvalidated** — **DONE (new `internal/validate.Name` applied to branch create, WS session/window params, Create/Rename/DeleteWindow; Rename/Delete now resolve the worktree via `GetByName` like Create)**
  - `internal/worktree/manager.go:391,397` · `internal/api/handlers/terminal.go:382,747,768`
  - argv form blocks *shell* injection, but a leading-`-` name is parsed as a flag (arg
    injection); contained today only by git/tmux's own option parsing. Also
    `RenameWindow`/`DeleteWindow` don't resolve the worktree via `GetByName` like
    `CreateWindow` does. **Fix:** shared name validator (`^[A-Za-z0-9][A-Za-z0-9._/-]*$`,
    reject leading `-`) applied before building args; validate the `{worktree}` var.

- [x] **20. `/debug/pprof/` mounted on `http.DefaultServeMux`** — **DONE (wrapped in `loopbackOnly` middleware — non-loopback peers get 403 regardless of bind address)**
  - `internal/api/router.go:13,500` — heap/goroutine/**cmdline** disclosure + cheap CPU-DoS
    when `PermitAnyHost`. Mount only on loopback / behind an explicit flag, not blanket.

---

## 🟢 Low — hardening & hygiene

- [x] **21.** No request-body size limits or server timeouts — unbounded `json.NewDecoder(r.Body)`,
  evidence-upload `io.ReadAll`, no `ReadTimeout`/`MaxHeaderBytes` (`router.go:539`, `cases.go:285`).
  Add `http.MaxBytesReader` + server timeouts; stream evidence to disk. — **DONE (global
  `middleware.BodyLimit(64MB)`; `ReadHeaderTimeout`/`IdleTimeout`/`MaxHeaderBytes` on the server —
  no blanket Read/WriteTimeout because SSE/WebSocket streams would be severed; evidence now
  streams to disk via `io.Copy`)**
- [x] **22.** `genai` buffers entire subprocess stdout/stderr into `bytes.Buffer`, no cap
  (`internal/genai/genai.go:49`). Wrap in a size-limited writer. — **DONE (`limitedBuffer`,
  8MB stdout / 1MB stderr)**
- [x] **23.** `claude readLoop` logs every raw NDJSON event line — tool output/secrets leak +
  log bloat (`internal/claude/manager.go:1019`). Gate behind debug + truncate. — **DONE
  (gated behind `TRELLIS_DEBUG_EVENTS=1`, truncated to 2KB)**
- [x] **24.** Client library doesn't `url.PathEscape` path params (only `trace.go` does) —
  `svc:foo`/`feature/x` names mis-route (`pkg/client/{services,worktrees,logs,workflows}.go`).
  — **DONE (all path params in services/worktrees/logs/workflows/crashes now PathEscape'd)**
- [x] **25.** `loadMessages` `break`s on first bad JSONL line — one corrupt interior line
  silently truncates the rest of the conversation on reload (`claude/store.go:91`, `codex/store.go:90`).
  Only tolerate failure on the genuine last line. — **DONE (interior corrupt lines are skipped
  with a warning; only an unterminated final line is silently tolerated)**
- [x] **26.** `javascript:` URLs not blocked in case links; custom shortcut `keys` inserted as
  raw HTML (operator-controlled) (`views/case_detail.qtpl:450`, `static/js/shortcut_help.js:58`).
  — **DONE (`safeURL` scheme allowlist http/https/mailto/relative; shortcut keys built as DOM
  nodes with `textContent`)**
- [x] **27.** Config validation gaps: server `tls_cert`/`tls_key` not validated both-or-neither
  (silent HTTP downgrade); no cross-listener port-conflict check (`internal/config/validator.go:81`).
  — **DONE (both-or-neither rule + host-aware port-conflict check across server/proxy listeners,
  wildcard hosts conflict with everything; templated listens skipped; tests added)**
- [x] **28.** `NewRouter` vs `NewRouterWithTerminalHandler` duplicate ~150 lines of routing;
  only the latter is used in prod. Collapse to one to avoid drift (`internal/api/router.go`).
  — **DONE (`NewRouter` is now a thin wrapper that creates a terminal handler and delegates)**
- [x] **29.** Repo hygiene: built `trellis` (17 MB) and `trellis-ctl` (11 MB) binaries are **not
  in `.gitignore`** — a `git add .` would commit them. Add `/trellis` and `/trellis-ctl`. — **DONE**

---

## Verified-correct (do not re-litigate)

CORS/Host/origin/WebSocket-upgrader design (no bypass on loopback); argv-form exec
everywhere (no shell injection); log viewer + server templates escape correctly (`{%s%}` +
HTML-safe JSON — no server-side XSS); WebSocket single-writer discipline across ~8 handlers;
service process-group kills + SIGTERM→SIGKILL escalation; atomic tmp+rename in cases/pair/
inbox stores; worktree slash→dash + `project-` prefix (worktree *path* traversal impossible);
trace `{name:.+}` sanitizer.

---

## Suggested order

1. **#1 (network exposure)** and **#2 (XSS)** — these turn a safe local tool into remote RCE
   the moment the operator binds non-loopback (as the docs recommend).
2. **#3 (path traversal)**, then the panic/leak set **#4, #5, #8, #7**.
3. Batch the Medium correctness items (#9–#20).
4. Low/hygiene (#21–#29) as cleanup.
