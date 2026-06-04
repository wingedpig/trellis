// Shared "Commands & Shortcuts" dialog.
//
// One source of truth for the modal HTML, styles, and list-building logic.
// Pages contribute entries via TrellisShortcuts.register({...}); the dialog
// renders them in registration order. The universal entries (nav picker,
// history picker, session inbox, plus any per-worktree custom shortcuts) are
// registered here so every page that loads this script gets them
// automatically. Page-specific entries (terminal/code toggle, workflow
// picker, etc.) are registered from the relevant page script.

(function() {
    var MODAL_ID = 'shortcutHelpModal';
    var LIST_ID  = 'shortcutHelpList';

    // Each entry: { id?, label, keys, run, when, source? }
    // - when() is consulted at show-time; entries whose when() returns false
    //   are omitted. Default: always show.
    // - source ('builtin' | 'custom') is used internally for ordering and to
    //   let TrellisShortcuts.replaceCustom() refresh just the custom set.
    var entries = [];

    function ensureModal() {
        if (document.getElementById(MODAL_ID)) return;
        var modal = document.createElement('div');
        modal.className = 'modal fade shortcut-help-modal';
        modal.id = MODAL_ID;
        modal.tabIndex = -1;
        modal.innerHTML =
            '<div class="modal-dialog">' +
              '<div class="modal-content">' +
                '<div class="modal-header">' +
                  '<h5 class="modal-title"><i class="fa-solid fa-keyboard"></i> Commands &amp; Shortcuts</h5>' +
                  '<button type="button" class="btn-close" data-bs-dismiss="modal"></button>' +
                '</div>' +
                '<div class="modal-body p-0">' +
                  '<div class="list-group list-group-flush" id="' + LIST_ID + '"></div>' +
                '</div>' +
              '</div>' +
            '</div>';
        document.body.appendChild(modal);
    }

    function populate() {
        var list = document.getElementById(LIST_ID);
        if (!list) return;
        list.innerHTML = '';
        entries.forEach(function(item) {
            if (typeof item.when === 'function' && !item.when()) return;
            var btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'shortcut-action';
            var labelEl = document.createElement('span');
            labelEl.className = 'shortcut-action-label';
            labelEl.textContent = item.label || '';
            var keysEl = document.createElement('span');
            keysEl.className = 'shortcut-action-keys';
            if (item.keys) {
                // Build with DOM nodes, not innerHTML — custom shortcut keys
                // come from config and must not be injectable as raw HTML.
                String(item.keys).split(' + ').forEach(function(k, idx) {
                    if (idx > 0) keysEl.appendChild(document.createTextNode(' + '));
                    var kbd = document.createElement('kbd');
                    kbd.textContent = k;
                    keysEl.appendChild(kbd);
                });
            }
            btn.appendChild(labelEl);
            btn.appendChild(keysEl);
            btn.addEventListener('click', function() {
                // Run the action AFTER the modal has fully closed so focus
                // doesn't get yanked back into the dismissing modal (this
                // would otherwise prevent Select2-style pickers from opening).
                var modalEl = document.getElementById(MODAL_ID);
                var modal = modalEl && bootstrap.Modal.getInstance(modalEl);
                if (modalEl) {
                    modalEl.addEventListener('hidden.bs.modal', function handler() {
                        modalEl.removeEventListener('hidden.bs.modal', handler);
                        try { if (typeof item.run === 'function') item.run(); }
                        catch (err) { console.error('shortcut action failed:', err); }
                    });
                }
                if (modal) modal.hide();
                else try { if (typeof item.run === 'function') item.run(); } catch (err) {}
            });
            list.appendChild(btn);
        });
    }

    function show() {
        ensureModal();
        populate();
        new bootstrap.Modal(document.getElementById(MODAL_ID)).show();
    }

    // Register an entry. `id` is optional; if provided, re-registering the
    // same id replaces the previous entry (handy for page scripts that may
    // load more than once during SPA navigation).
    function register(item) {
        if (!item) return;
        if (item.id) {
            for (var i = 0; i < entries.length; i++) {
                if (entries[i].id === item.id) { entries[i] = item; return; }
            }
        }
        entries.push(item);
    }

    // Replace all entries with source==='custom'. Called when per-worktree
    // shortcuts are loaded from the nav API so they refresh in place rather
    // than accumulating duplicates.
    function replaceCustom(items) {
        entries = entries.filter(function(e) { return e.source !== 'custom'; });
        (items || []).forEach(function(it) {
            it.source = 'custom';
            entries.push(it);
        });
    }

    // Built-in universal entries. These match the keyboard shortcuts wired
    // up in header.qtpl / terminal.qtpl; the keys strings here are display-
    // only — the actual key handling lives in the page scripts.
    register({
        id: 'nav-picker',
        source: 'builtin',
        label: 'Open navigation picker',
        keys: 'Cmd/Ctrl + P',
        run: function() {
            if (window.TrellisNav && TrellisNav.openPicker) { TrellisNav.openPicker(); return; }
            // Fallback for terminal page (which uses select2 directly).
            if (window.jQuery && jQuery('#navSelect').length) {
                jQuery('#navSelect').val(window.location.pathname).trigger('change.select2');
                jQuery('#navSelect').select2('open');
            }
        },
    });
    register({
        id: 'history-picker',
        source: 'builtin',
        label: 'Open history picker',
        keys: 'Cmd/Ctrl + Backspace',
        run: function() {
            if (!window.TrellisNav || !TrellisNav.openHistoryPicker) return;
            var hist = TrellisNav.getHistory ? TrellisNav.getHistory() : [];
            if (!hist.length) { alert('No navigation history yet.'); return; }
            TrellisNav.openHistoryPicker();
        },
    });
    register({
        id: 'session-inbox',
        source: 'builtin',
        label: 'Open session inbox',
        keys: 'Cmd/Ctrl + I',
        run: function() {
            window.open('/inbox', 'trellis-inbox', 'popup=yes,width=420,height=720');
        },
    });

    // Refresh custom shortcuts from the nav controller whenever they
    // change. The nav controller fires this event after fetching shortcuts
    // from the API so the dialog stays current without polling.
    function syncCustomFromNav() {
        if (!window.TrellisNav || !TrellisNav.getCustomShortcuts) return;
        var raw = TrellisNav.getCustomShortcuts() || [];
        var items = raw.map(function(sc) {
            return {
                label: sc.window || sc.key,
                keys:  sc.key || '',
                run:   function() {
                    if (!(window.TrellisNav && TrellisNav.handleCustomShortcut)) return;
                    var combo = TrellisNav.parseKeyCombo ? TrellisNav.parseKeyCombo(sc.key || '') : { key: '' };
                    var fakeEvent = {
                        metaKey:  !!combo.meta,
                        ctrlKey:  !!combo.ctrl,
                        shiftKey: !!combo.shift,
                        altKey:   !!combo.alt,
                        key:      combo.key,
                        preventDefault: function(){},
                    };
                    // If neither meta nor ctrl was specified, default to one
                    // so matchesKeyCombo's metaOrCtrl branch fires.
                    if (!fakeEvent.metaKey && !fakeEvent.ctrlKey) fakeEvent.metaKey = true;
                    TrellisNav.handleCustomShortcut(fakeEvent);
                },
            };
        });
        replaceCustom(items);
    }
    document.addEventListener('trellis-custom-shortcuts-changed', syncCustomFromNav);
    // Also pick up any that were already present when this script loaded.
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', syncCustomFromNav);
    } else {
        syncCustomFromNav();
    }

    window.TrellisShortcuts = {
        register: register,
        replaceCustom: replaceCustom,
        show: show,
    };
    // Back-compat: callers (command_palette.js, header keyboard handler,
    // terminal keyboard handler) all invoke `showShortcutHelp()` or look up
    // `#shortcutHelpModal` directly. Point both at the shared dialog.
    window.showShortcutHelp = show;
})();
