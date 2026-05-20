// Trellis Session Inbox — popup window client.
//
// Lists all active claude+codex sessions across worktrees with a real-time
// state badge (running / needs-you). Clicking a row sends a "navigate"
// command over the WebSocket; the server forwards it to all main-window
// trellis tabs, which then navigate themselves.

(function() {
    "use strict";

    var INBOX_API = "/api/v1/inbox/sessions";
    var WS_URL = (location.protocol === "https:" ? "wss:" : "ws:") +
        "//" + location.host + "/api/v1/inbox/ws?role=inbox";

    // sessionId -> row data
    var rows = new Map();
    var ws = null;
    var reconnectTimer = null;

    // Hidden sessions: sessionId -> state-at-hide-time. Persists across
    // popup reopens. A session is unhidden as soon as its state differs
    // from the stored value (either via a live state-changed event or on
    // initial load when the popup is reopened after the change).
    var HIDDEN_KEY = "trellis-inbox-hidden";
    var hidden = {};
    try { hidden = JSON.parse(localStorage.getItem(HIDDEN_KEY) || "{}"); }
    catch (e) { hidden = {}; }

    function saveHidden() {
        try { localStorage.setItem(HIDDEN_KEY, JSON.stringify(hidden)); }
        catch (e) { /* quota / privacy mode — non-fatal */ }
    }

    function reconcileHidden() {
        // Drop hides whose session is gone or whose state has changed since
        // we hid it. Caller must invoke after rows is up to date.
        var changed = false;
        Object.keys(hidden).forEach(function(id) {
            var r = rows.get(id);
            if (!r || r.state !== hidden[id]) {
                delete hidden[id];
                changed = true;
            }
        });
        if (changed) saveHidden();
    }

    function rowURL(r) {
        return "/" + r.agent + "/" + encodeURIComponent(r.worktree) +
            "/" + encodeURIComponent(r.id);
    }

    function setStatus(text, cls) {
        var el = document.getElementById("inbox-status");
        if (!el) return;
        el.textContent = text;
        el.className = "inbox-status " + (cls || "");
    }

    function render() {
        var needsYou = [];
        var running = [];
        rows.forEach(function(r) {
            if (r.trashed) return;
            if (hidden[r.id]) return;
            if (r.state === "needs_you") {
                needsYou.push(r);
            } else {
                running.push(r);
            }
        });
        var bySort = function(a, b) {
            // Newest transition on top.
            var ta = a.last_state_change_at ? Date.parse(a.last_state_change_at) : 0;
            var tb = b.last_state_change_at ? Date.parse(b.last_state_change_at) : 0;
            return tb - ta;
        };
        needsYou.sort(bySort);
        running.sort(bySort);

        renderSection("needs-you-list", "needs-you-empty", needsYou, "needs-you");
        renderSection("running-list", "running-empty", running, "running");
    }

    function renderSection(listID, emptyID, items, cls) {
        var list = document.getElementById(listID);
        var empty = document.getElementById(emptyID);
        if (!list || !empty) return;
        list.textContent = "";
        if (items.length === 0) {
            empty.style.display = "";
            return;
        }
        empty.style.display = "none";
        items.forEach(function(r) {
            var a = document.createElement("a");
            a.className = "inbox-row " + cls + (r.unread ? " unread" : "");
            a.href = rowURL(r);
            a.dataset.sessionId = r.id;
            a.title = r.display_name + " — " + r.worktree;

            var dot = document.createElement("span");
            dot.className = "inbox-dot";
            a.appendChild(dot);

            if (r.unread) {
                var u = document.createElement("span");
                u.className = "inbox-unread";
                u.title = "Unread — transitioned to needs-you while you weren't watching";
                a.appendChild(u);
            }

            var text = document.createElement("div");
            text.className = "inbox-text";
            var name = document.createElement("div");
            name.className = "inbox-name";
            name.textContent = r.display_name || r.id.slice(0, 8);
            var sub = document.createElement("div");
            sub.className = "inbox-sub";
            sub.textContent = r.worktree;
            text.appendChild(name);
            text.appendChild(sub);
            a.appendChild(text);

            var agent = document.createElement("span");
            agent.className = "inbox-agent";
            agent.textContent = r.agent;
            a.appendChild(agent);

            var hideBtn = document.createElement("button");
            hideBtn.className = "inbox-hide";
            hideBtn.type = "button";
            hideBtn.title = "Hide until next state change";
            hideBtn.innerHTML = '<i class="fa-solid fa-eye-slash"></i>';
            hideBtn.addEventListener("click", function(e) {
                e.preventDefault();
                e.stopPropagation();
                hideRow(r.id);
            });
            a.appendChild(hideBtn);

            a.addEventListener("click", function(e) {
                e.preventDefault();
                sendNavigate(a.href);
            });

            list.appendChild(a);
        });
    }

    function hideRow(id) {
        var r = rows.get(id);
        if (!r) return;
        hidden[id] = r.state;
        saveHidden();
        render();
    }

    function sendNavigate(path) {
        if (!ws || ws.readyState !== WebSocket.OPEN) {
            // No live socket — open a window directly as a fallback.
            window.open(path, "trellis-main");
            return;
        }
        ws.send(JSON.stringify({type: "navigate", path: pathOnly(path)}));
    }

    // pathOnly strips host/scheme so we hand the server a same-origin path.
    function pathOnly(p) {
        try {
            var u = new URL(p, location.origin);
            return u.pathname + u.search + u.hash;
        } catch (e) {
            return p;
        }
    }

    function initialLoad() {
        fetch(INBOX_API).then(function(r) {
            return r.json();
        }).then(function(data) {
            rows.clear();
            var list = data.data || data; // tolerate either {data:[]} or [] shape
            if (Array.isArray(list)) {
                list.forEach(function(r) { rows.set(r.id, r); });
            }
            reconcileHidden();
            render();
        }).catch(function(err) {
            console.warn("inbox: initial load failed", err);
        });
    }

    function applyEvent(ev) {
        if (!ev || !ev.payload) return;
        var p = ev.payload;
        var id = p.session_id;
        if (!id) return;
        var existing = rows.get(id) || {};
        var newState = p.state || existing.state;
        var stateChanged = existing.state !== undefined && existing.state !== newState;
        var merged = {
            id: id,
            agent: p.agent || existing.agent,
            worktree: p.worktree !== undefined ? p.worktree : existing.worktree,
            display_name: p.display_name || existing.display_name,
            state: newState,
            unread: !!p.unread,
            trashed: !!p.trashed,
            // Only bump on a real running↔needs-you transition. Unread-only
            // updates (e.g. cleared by viewing) must not reorder rows.
            last_state_change_at: (existing.state === undefined || stateChanged)
                ? (ev.timestamp || new Date().toISOString())
                : existing.last_state_change_at
        };
        if (merged.trashed) {
            rows.delete(id);
        } else {
            rows.set(id, merged);
        }
        // Drop any hide entry for this session — the spec is that hides clear
        // on state change. (Unread-only updates re-emit but don't transition;
        // we leave hides alone for those, since stateChanged covers it.)
        if (stateChanged && hidden[id] !== undefined) {
            delete hidden[id];
            saveHidden();
        }
        render();
    }

    function connect() {
        clearTimeout(reconnectTimer);
        setStatus("connecting…", "");
        ws = new WebSocket(WS_URL);

        ws.onopen = function() {
            setStatus("live", "connected");
            // Re-fetch on (re)connect so state is fresh.
            initialLoad();
        };

        ws.onmessage = function(e) {
            var msg;
            try { msg = JSON.parse(e.data); } catch (err) { return; }
            if (msg.type === "state_changed") {
                applyEvent(msg.event);
            } else if (msg.type === "navigate_failed") {
                // No main window connected — open one directly.
                window.open(msg.path, "trellis-main");
            }
        };

        ws.onclose = function() {
            ws = null;
            setStatus("offline (reconnecting…)", "disconnected");
            reconnectTimer = setTimeout(connect, 3000);
        };

        ws.onerror = function() {
            // onclose will fire next — handle reconnect there.
        };
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", function() {
            initialLoad();
            connect();
        });
    } else {
        initialLoad();
        connect();
    }
})();
