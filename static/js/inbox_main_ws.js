// Main-window listener for navigate commands forwarded from the floating
// session inbox window. Every trellis page opens a WebSocket as role=main
// to /api/v1/inbox/ws. When the user clicks a row in the inbox, the server
// forwards a {type:"navigate", path:"..."} message here; we push the current
// page onto TrellisNav's history (so Cmd-Backspace behaves as if the user
// navigated normally) and then send the browser to the new path.

(function() {
    "use strict";

    var WS_URL = (location.protocol === "https:" ? "wss:" : "ws:") +
        "//" + location.host + "/api/v1/inbox/ws?role=main";

    var ws = null;
    var reconnectTimer = null;

    function connect() {
        clearTimeout(reconnectTimer);
        try {
            ws = new WebSocket(WS_URL);
        } catch (e) {
            reconnectTimer = setTimeout(connect, 3000);
            return;
        }

        ws.onmessage = function(e) {
            var msg;
            try { msg = JSON.parse(e.data); } catch (err) { return; }
            if (msg.type === "navigate" && msg.path) {
                try {
                    if (window.TrellisNav && typeof window.TrellisNav.pushToHistory === "function") {
                        window.TrellisNav.pushToHistory(window.location.pathname);
                    }
                } catch (err) { /* non-fatal */ }
                window.location.href = msg.path;
            }
        };

        ws.onclose = function() {
            ws = null;
            reconnectTimer = setTimeout(connect, 3000);
        };

        ws.onerror = function() {
            // onclose will follow.
        };
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", connect);
    } else {
        connect();
    }
})();
