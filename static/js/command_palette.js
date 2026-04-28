// Command palette: Shift+Cmd+P / Shift+Ctrl+P opens a searchable list of
// commands fetched from /api/v1/commands. Selected commands dispatch either
// a navigate or api action, with optional then-chaining and {data.Field}
// substitution from the api response.

(function() {
    var cmdById = {};

    function getWorktreeName() {
        if (typeof WORKTREE_NAME !== 'undefined') return WORKTREE_NAME;
        if (typeof CLAUDE_WORKTREE !== 'undefined') return CLAUDE_WORKTREE;
        var m = window.location.pathname.match(/^\/(worktree|claude)\/([^/]+)/);
        if (m) return decodeURIComponent(m[2]);
        return 'main';
    }

    function ensureOverlay() {
        if (document.getElementById('cmdPaletteOverlay')) return;
        var html =
            '<div id="cmdPaletteOverlay" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.4);z-index:1080;">' +
              '<div id="cmdPaletteBox" style="max-width:560px;margin:10vh auto 0;padding:0 12px;">' +
                '<select id="cmdPaletteSelect" class="form-select form-select-sm" style="width:100%">' +
                  '<option></option>' +
                '</select>' +
              '</div>' +
            '</div>';
        document.body.insertAdjacentHTML('beforeend', html);
        document.getElementById('cmdPaletteOverlay').addEventListener('mousedown', function(e) {
            if (e.target === this) closePalette();
        });
    }

    function closePalette() {
        var $sel = $('#cmdPaletteSelect');
        if ($sel.data('select2')) {
            $sel.off('select2:select select2:close');
            $sel.select2('destroy');
        }
        $('#cmdPaletteOverlay').hide();
    }

    function fillOptions(cmds) {
        var $sel = $('#cmdPaletteSelect').empty().append('<option></option>');
        for (var i = 0; i < cmds.length; i++) {
            $sel.append($('<option>').val(cmds[i].id).text(cmds[i].title));
        }
    }

    function interpolate(template, response) {
        if (!response || !response.data) return template;
        return template.replace(/\{data\.([A-Za-z0-9_]+)\}/g, function(_, key) {
            var v = response.data[key];
            return v == null ? '' : encodeURIComponent(v);
        });
    }

    var clientActions = {
        shortcuts: function() {
            var modal = document.getElementById('shortcutHelpModal');
            if (modal) new bootstrap.Modal(modal).show();
        },
        copyUrl: function() {
            if (navigator.clipboard) navigator.clipboard.writeText(window.location.href);
        },
    };

    function dispatch(action, lastResponse) {
        if (!action) return;
        if (action.type === 'navigate') {
            window.location.href = interpolate(action.url, lastResponse);
            return;
        }
        if (action.type === 'client') {
            var fn = clientActions[action.name];
            if (fn) fn();
            return;
        }
        if (action.type === 'api') {
            fetch(action.url, { method: action.method || 'GET' })
                .then(function(r) { return r.json(); })
                .then(function(body) {
                    if (action.then) dispatch(action.then, body);
                })
                .catch(function(err) {
                    console.error('Command failed:', err);
                });
        }
    }

    function onPick(e) {
        var id = e.params.data.id;
        var cmd = cmdById[id];
        closePalette();
        if (!cmd) return;
        if (cmd.confirm && !window.confirm(cmd.confirm)) return;
        if (typeof TrellisNav !== 'undefined' && TrellisNav.pushToHistory) {
            TrellisNav.pushToHistory(window.location.pathname);
        }
        dispatch(cmd.action);
    }

    function openPalette() {
        ensureOverlay();
        var wt = getWorktreeName();
        fetch('/api/v1/commands?worktree=' + encodeURIComponent(wt))
            .then(function(r) { return r.json(); })
            .then(function(resp) {
                var cmds = (resp && resp.data) || [];
                cmdById = {};
                for (var i = 0; i < cmds.length; i++) cmdById[cmds[i].id] = cmds[i];
                fillOptions(cmds);
                $('#cmdPaletteOverlay').show();
                var $sel = $('#cmdPaletteSelect').select2({
                    dropdownParent: $('#cmdPaletteBox'),
                    placeholder: 'Type a command…',
                    width: '100%',
                });
                $sel.on('select2:select', onPick);
                $sel.on('select2:close', function() { setTimeout(closePalette, 0); });
                $sel.select2('open');
            })
            .catch(function(err) {
                console.error('Command palette: fetch failed:', err);
            });
    }

    document.addEventListener('keydown', function(e) {
        if ((e.metaKey || e.ctrlKey) && e.shiftKey && (e.key === 'P' || e.key === 'p')) {
            e.preventDefault();
            openPalette();
        } else if (e.key === 'Escape' && $('#cmdPaletteOverlay').is(':visible')) {
            closePalette();
        }
    });
})();
