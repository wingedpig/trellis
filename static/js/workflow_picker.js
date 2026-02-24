// Workflow picker for navbar - shared across worktree home and Claude pages.
// Adds a workflow <select> to the navbar, loads workflows from the API,
// and handles Cmd/Ctrl+/ shortcut.
// Runs workflows by POSTing to the API then navigating to the terminal output view.

(function() {
    // Detect current worktree name from page globals or URL
    function getWorktreeName() {
        // Worktree home page sets WORKTREE_NAME, Claude page sets CLAUDE_WORKTREE
        if (typeof WORKTREE_NAME !== 'undefined') return WORKTREE_NAME;
        if (typeof CLAUDE_WORKTREE !== 'undefined') return CLAUDE_WORKTREE;
        // Fallback: parse from URL (/worktree/{name} or /claude/{name}/...)
        var path = window.location.pathname;
        var m = path.match(/^\/(worktree|claude)\/([^/]+)/);
        if (m) return decodeURIComponent(m[2]);
        return 'main';
    }

    function initWorkflowPicker() {
        var navPickerContainer = document.querySelector('.navbar .d-flex.align-items-center.gap-2.ms-3');
        if (!navPickerContainer || document.getElementById('workflowSelect')) return;

        var wfSelect = document.createElement('select');
        wfSelect.id = 'workflowSelect';
        wfSelect.className = 'form-select form-select-sm';
        wfSelect.style.width = '160px';
        wfSelect.style.background = 'var(--trellis-input-bg)';
        wfSelect.style.borderColor = 'var(--trellis-input-border)';
        wfSelect.style.color = 'var(--bs-body-color)';
        var defaultOpt = document.createElement('option');
        defaultOpt.value = '';
        defaultOpt.textContent = 'Workflow...';
        wfSelect.appendChild(defaultOpt);
        navPickerContainer.appendChild(wfSelect);

        fetch('/api/v1/workflows')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                if (data.data && Array.isArray(data.data)) {
                    data.data.sort(function(a, b) { return (a.Name || '').localeCompare(b.Name || ''); });
                    for (var i = 0; i < data.data.length; i++) {
                        var wf = data.data[i];
                        if (wf.ID && wf.ID.startsWith('_')) continue;
                        var opt = document.createElement('option');
                        opt.value = wf.ID;
                        opt.textContent = wf.Name;
                        opt.dataset.confirm = wf.Confirm || false;
                        opt.dataset.confirmMessage = wf.ConfirmMessage || '';
                        wfSelect.appendChild(opt);
                    }
                }
            });

        wfSelect.addEventListener('change', function() {
            if (this.value) {
                var opt = this.options[this.selectedIndex];
                var needsConfirm = opt.dataset.confirm === 'true';
                var confirmMsg = opt.dataset.confirmMessage || '';
                var id = this.value;
                this.value = '';
                if (needsConfirm) {
                    var msg = confirmMsg || 'Are you sure you want to run this workflow?';
                    workflowPickerConfirm(msg, 'Run Workflow').then(function(confirmed) {
                        if (confirmed) workflowPickerExecute(id);
                    });
                } else {
                    workflowPickerExecute(id);
                }
            }
        });

        // Cmd/Ctrl+/ to open workflow picker
        document.addEventListener('keydown', function(e) {
            if ((e.metaKey || e.ctrlKey) && e.key === '/') {
                e.preventDefault();
                var sel = document.getElementById('workflowSelect');
                if (sel) {
                    sel.focus();
                    if (sel.showPicker) sel.showPicker();
                }
            }
        });
    }

    // Confirm modal - creates one if not present
    function ensureConfirmModal() {
        if (document.getElementById('wfConfirmModal')) return;
        var html = '<div class="modal fade" id="wfConfirmModal" tabindex="-1">' +
            '<div class="modal-dialog"><div class="modal-content">' +
            '<div class="modal-header"><h5 class="modal-title" id="wfConfirmModalTitle">Confirm</h5>' +
            '<button type="button" class="btn-close" data-bs-dismiss="modal"></button></div>' +
            '<div class="modal-body"><p id="wfConfirmModalMessage"></p></div>' +
            '<div class="modal-footer"><button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Cancel</button>' +
            '<button type="button" class="btn btn-primary" id="wfConfirmModalYes">Yes, Run</button></div>' +
            '</div></div></div>';
        document.body.insertAdjacentHTML('beforeend', html);
    }

    function workflowPickerConfirm(message, title) {
        ensureConfirmModal();
        return new Promise(function(resolve) {
            document.getElementById('wfConfirmModalTitle').textContent = title || 'Confirm';
            document.getElementById('wfConfirmModalMessage').textContent = message;
            var modal = new bootstrap.Modal(document.getElementById('wfConfirmModal'));
            var yesBtn = document.getElementById('wfConfirmModalYes');
            var modalEl = document.getElementById('wfConfirmModal');
            var newYesBtn = yesBtn.cloneNode(true);
            yesBtn.parentNode.replaceChild(newYesBtn, yesBtn);
            newYesBtn.onclick = function() { modal.hide(); resolve(true); };
            modalEl.addEventListener('hidden.bs.modal', function() { resolve(false); }, { once: true });
            modal.show();
            modalEl.addEventListener('shown.bs.modal', function() { newYesBtn.focus(); }, { once: true });
        });
    }

    function workflowPickerExecute(id) {
        var worktree = getWorktreeName();
        // Save current page to nav history before navigating away
        if (typeof TrellisNav !== 'undefined' && TrellisNav.pushToHistory) {
            TrellisNav.pushToHistory(window.location.pathname);
        }
        // Fire the workflow, get the run ID, then navigate to the terminal output view
        fetch('/api/v1/workflows/' + id + '/run?worktree=' + encodeURIComponent(worktree), { method: 'POST' })
            .then(function(r) { return r.json(); })
            .then(function(data) {
                var runID = (data.data && data.data.ID) ? data.data.ID : '';
                var url = '/terminal/output/' + encodeURIComponent(worktree) +
                    '?run=' + encodeURIComponent(runID) +
                    '&workflow=' + encodeURIComponent(id);
                window.location.href = url;
            })
            .catch(function() {
                window.location.href = '/terminal/output/' + encodeURIComponent(worktree);
            });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initWorkflowPicker);
    } else {
        initWorkflowPicker();
    }
})();
