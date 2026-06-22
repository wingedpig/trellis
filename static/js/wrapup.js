// Copyright (c) 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Shared modal for Commit and Wrap Up flows.
//
// Both flows use the same DOM (the #wrapUpModal subtree defined inline by the
// Claude / Codex / case-detail pages). The `mode` parameter controls which
// sections are visible and which API endpoint the confirm button hits:
//   - mode: 'commit'  → POST /api/v1/{agent}/{worktree}/commit
//   - mode: 'wrapup'  → POST /api/v1/{agent}/{worktree}/wrap-up
//
// The trace/related-session/links sections are hidden in commit mode — those
// are wrap-up-only concerns.
//
// Requires globals set by the including page:
//   WRAPUP_WORKTREE         — worktree name (string)
//   WRAPUP_SESSION_ID       — session ID (string or null)
//   WRAPUP_SESSION_NAME     — session display name (string), used as the
//                              preferred title when creating a new case
//   WRAPUP_SESSION_CREATED  — ISO 8601 timestamp of session creation (string)
//   WRAPUP_CASE             — {id, title, kind} or null
//   WRAPUP_AGENT            — 'claude' or 'codex' (defaults to 'claude')
//   WRAPUP_WORKTREE_NAME_HUMANIZED — optional humanized worktree name for
//                              prefilling the title when no case exists
//   WRAPUP_IS_DEFAULT_BRANCH — whether the worktree is on the repo's default
//                              branch (main/master); see _newCaseTitlePrefill

window.toggleAllCheckboxes = window.toggleAllCheckboxes || function(containerId, checked) {
    var container = document.getElementById(containerId);
    if (!container) return;
    var boxes = container.querySelectorAll('input[type="checkbox"]');
    boxes.forEach(function(cb) { cb.checked = !!checked; });
};

// Internal: state remembered between modal open and confirm.
var _modalState = {
    mode: 'wrapup',           // 'commit' | 'wrapup'
    generatedDescription: '', // last per-commit case description from the model
    generationInflight: false,
    userTouchedMessage: false,
    // Wrap-up only: the full summary returned by /generate-summary. Stored
    // so we can ship it back on confirm with the user's chip edits applied.
    // null when no summary has been generated (or generation failed).
    generatedSummary: null,
    summaryInflight: false,
    components: [],
    componentsLoaded: false,  // true once chips are painted (deriver or fallback)
};

function _agent() {
    return (typeof WRAPUP_AGENT !== 'undefined' && WRAPUP_AGENT) ? WRAPUP_AGENT : 'claude';
}

function _apiBase() {
    return '/api/v1/' + _agent() + '/' + encodeURIComponent(WRAPUP_WORKTREE);
}

// Public entry point.
function showCommitModal(mode) {
    _modalState.mode = (mode === 'commit') ? 'commit' : 'wrapup';
    _modalState.generatedDescription = '';
    _modalState.generationInflight = false;
    _modalState.userTouchedMessage = false;
    _modalState.generatedSummary = null;
    _modalState.summaryInflight = false;
    _modalState.components = [];
    _modalState.componentsLoaded = false;

    var modal = document.getElementById('wrapUpModal');
    if (!modal) return;

    // Mode-driven label and button text.
    var isCommit = _modalState.mode === 'commit';
    var titleEl = modal.querySelector('.modal-title');
    if (titleEl) {
        titleEl.innerHTML = isCommit
            ? '<i class="fa-solid fa-code-commit"></i> Commit'
            : '<i class="fa-solid fa-flag-checkered"></i> Wrap Up';
    }
    var confirmBtn = document.getElementById('wrapUpConfirmBtn');
    if (confirmBtn) {
        confirmBtn.innerHTML = isCommit
            ? '<i class="fa-solid fa-code-commit"></i> Commit'
            : '<i class="fa-solid fa-flag-checkered"></i> Wrap Up';
        confirmBtn.disabled = false;
    }

    // Reset feedback panes.
    document.getElementById('wrapUpError').style.display = 'none';
    document.getElementById('wrapUpSuccess').style.display = 'none';
    var linksEl = document.getElementById('wrapUpLinks');
    if (linksEl) linksEl.innerHTML = '';
    document.getElementById('wrapUpFileList').innerHTML = '<div class="text-muted">Loading...</div>';
    var msgEl = document.getElementById('wrapUpCommitMsg');
    msgEl.value = '';
    msgEl.placeholder = 'Generating commit message…';

    // Track whether the user has typed in the message field — once they do,
    // we never overwrite their text with generated output.
    if (!msgEl._touchHookInstalled) {
        msgEl._touchHookInstalled = true;
        msgEl.addEventListener('input', function() { _modalState.userTouchedMessage = true; });
    }

    // Install the Regenerate button next to the message field (idempotent).
    _ensureRegenerateButton();

    // Hide wrap-up-only sections in commit mode. (Tags section starts hidden
    // and is only shown once /generate-summary returns a result.)
    var hideForCommit = ['wrapUpTraceSection', 'wrapUpRelatedSection', 'wrapUpLinksSection', 'wrapUpTagsSection'];
    hideForCommit.forEach(function(id) {
        var el = document.getElementById(id);
        if (el) el.style.display = 'none';
    });

    var existingCase = (typeof WRAPUP_CASE !== 'undefined' && WRAPUP_CASE) ? WRAPUP_CASE : null;

    var promises = [];

    promises.push(
        fetch(_apiBase() + '/git-status')
            .then(function(r) { return r.json(); })
            .then(function(d) { return d.data || d; })
    );

    if (WRAPUP_SESSION_ID && !existingCase) {
        promises.push(
            fetch(_apiBase() + '/session-case?session_id=' + encodeURIComponent(WRAPUP_SESSION_ID))
                .then(function(r) { return r.ok ? r.json().then(function(d) { return d.data || d; }) : null; })
                .catch(function() { return null; })
        );
    } else {
        promises.push(Promise.resolve(null));
    }

    // Trace reports and related-agent sessions are wrap-up-only.
    if (isCommit) {
        promises.push(Promise.resolve([]));
        promises.push(Promise.resolve([]));
    } else {
        promises.push(
            fetch(_apiBase() + '/trace-reports')
                .then(function(r) { return r.json(); })
                .then(function(d) { return (d.data || d).reports || []; })
                .catch(function() { return []; })
        );
        var thisAgent = _agent();
        var otherAgent = thisAgent === 'codex' ? 'claude' : 'codex';
        promises.push(
            fetch('/api/v1/' + otherAgent + '/' + encodeURIComponent(WRAPUP_WORKTREE) + '/sessions')
                .then(function(r) { return r.ok ? r.json() : null; })
                .then(function(d) { return d ? (d.data || d) : []; })
                .catch(function() { return []; })
        );
    }

    Promise.all(promises).then(function(results) {
        var status = results[0];
        var linkedCase = results[1];
        var traceReports = results[2];
        var relatedSessions = results[3] || [];

        if (linkedCase) existingCase = linkedCase;

        var caseInfoEl = document.getElementById('wrapUpCaseInfo');
        var newCaseEl = document.getElementById('wrapUpNewCase');
        if (existingCase) {
            caseInfoEl.style.display = '';
            newCaseEl.style.display = 'none';
            document.getElementById('wrapUpCaseId').textContent = existingCase.case_id || existingCase.id;
            document.getElementById('wrapUpCaseTitle').textContent = existingCase.title;
            document.getElementById('wrapUpCaseKind').textContent = existingCase.kind;
            modal.dataset.caseId = existingCase.case_id || existingCase.id;
            modal.dataset.isNewCase = 'false';
        } else {
            caseInfoEl.style.display = 'none';
            newCaseEl.style.display = '';
            document.getElementById('wrapUpNewTitle').value = _newCaseTitlePrefill();
            // Commit mode defaults to 'feature'; wrap-up keeps 'task' for
            // legacy compatibility (was 'task' before this change).
            document.getElementById('wrapUpNewKind').value = isCommit ? 'feature' : 'task';
            modal.dataset.caseId = '';
            modal.dataset.isNewCase = 'true';
        }

        renderWrapUpFiles(status);

        var sessionCreated = (typeof WRAPUP_SESSION_CREATED !== 'undefined' && WRAPUP_SESSION_CREATED) ? WRAPUP_SESSION_CREATED : null;
        renderWrapUpTraces(traceReports, sessionCreated);
        var otherAgent = _agent() === 'codex' ? 'claude' : 'codex';
        renderWrapUpRelatedSessions(relatedSessions, otherAgent);

        // Kick off generation. The result only lands if the user hasn't
        // started typing in the message field by the time it arrives.
        _generateMessage();

        // Wrap-up only: also pre-generate the case summary so the user can
        // review and prune the tags before committing.
        if (!isCommit) {
            _generateSummary();
            // For the new-case path the title input starts empty, so the
            // initial summary call asks the user to enter a title.
            // Re-fire generation when they blur the title field so the
            // chips show up without an explicit click.
            _wireNewCaseTitleBlur();
        }

        var bsModal = new bootstrap.Modal(modal);
        bsModal.show();
    }).catch(function(err) {
        alert('Failed to load modal data: ' + err);
    });
}

// Back-compat shim: pages that haven't migrated still call showWrapUpModal().
function showWrapUpModal() { showCommitModal('wrapup'); }

// _newCaseTitlePrefill chooses the default title for a brand-new case.
//
//   1. A renamed session wins — its display name is the most descriptive
//      thing we have. Auto-assigned "Session N" names carry no information,
//      so they don't count as "renamed".
//   2. Otherwise the humanized worktree name is usually a good default
//      (e.g. "fix-login-bug" → "Fix Login Bug")…
//   3. …except on the default branch, where it degrades to "Main"/"Master".
//      There we fall back to the session name even if it's just "Session N",
//      since that's still more useful than the branch name.
function _newCaseTitlePrefill() {
    var sessionName = (typeof WRAPUP_SESSION_NAME !== 'undefined' && WRAPUP_SESSION_NAME)
        ? WRAPUP_SESSION_NAME.trim() : '';
    var worktreeTitle = (typeof WRAPUP_WORKTREE_NAME_HUMANIZED !== 'undefined' && WRAPUP_WORKTREE_NAME_HUMANIZED)
        ? WRAPUP_WORKTREE_NAME_HUMANIZED : '';
    var isDefaultBranch = (typeof WRAPUP_IS_DEFAULT_BRANCH !== 'undefined') && WRAPUP_IS_DEFAULT_BRANCH;

    var renamed = sessionName && !/^Session \d+$/.test(sessionName);
    if (renamed) return sessionName;
    if (isDefaultBranch) return sessionName || worktreeTitle;
    return worktreeTitle || sessionName;
}

function _ensureRegenerateButton() {
    var msgEl = document.getElementById('wrapUpCommitMsg');
    if (!msgEl) return;
    var existing = document.getElementById('wrapUpRegenerateBtn');
    if (existing) return;
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.id = 'wrapUpRegenerateBtn';
    btn.className = 'btn btn-outline-secondary btn-sm mt-1';
    btn.innerHTML = '<i class="fa-solid fa-arrows-rotate"></i> Regenerate';
    btn.onclick = function() {
        _modalState.userTouchedMessage = false;
        msgEl.value = '';
        msgEl.placeholder = 'Generating commit message…';
        _generateMessage(true);
    };
    msgEl.parentNode.appendChild(btn);
}

function _generateMessage(force) {
    if (_modalState.generationInflight) return;

    // Pin generation to exactly the files the user has checked. This must
    // match the set we'll pass to /commit or /wrap-up — otherwise the
    // generated message describes work that won't be in the commit.
    var msgEl = document.getElementById('wrapUpCommitMsg');
    var modal = document.getElementById('wrapUpModal');
    var files = [];
    document.querySelectorAll('#wrapUpFileList .form-check-input:checked').forEach(function(cb) {
        files.push(cb.value);
    });
    if (files.length === 0) {
        msgEl.placeholder = 'Select at least one file to generate a message.';
        return;
    }
    _modalState.generationInflight = true;

    var body = { files: files };
    if (typeof WRAPUP_SESSION_ID !== 'undefined' && WRAPUP_SESSION_ID) body.session_id = WRAPUP_SESSION_ID;
    if (modal.dataset.isNewCase === 'true') {
        body.title = (document.getElementById('wrapUpNewTitle').value || '').trim();
        body.kind = document.getElementById('wrapUpNewKind').value;
    } else {
        body.case_id = modal.dataset.caseId;
    }

    fetch(_apiBase() + '/generate-commit-message', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(body)
    })
    .then(function(r) { return r.json().then(function(d) { return {ok: r.ok, data: d}; }); })
    .then(function(result) {
        _modalState.generationInflight = false;
        if (!result.ok) {
            msgEl.placeholder = 'Generation failed — type your message.';
            return;
        }
        var d = result.data.data || result.data;
        if (force || !_modalState.userTouchedMessage) {
            msgEl.value = d.message || '';
            _modalState.userTouchedMessage = false;
            _modalState.generatedDescription = d.description || '';
        }
    })
    .catch(function() {
        _modalState.generationInflight = false;
        msgEl.placeholder = 'Generation failed — type your message.';
    });
}

// _wireNewCaseTitleBlur ensures the title input on the new-case form
// triggers summary generation when the user finishes editing it. Idempotent
// — multiple modal opens reuse the same listener.
function _wireNewCaseTitleBlur() {
    var titleEl = document.getElementById('wrapUpNewTitle');
    if (!titleEl || titleEl._blurHookInstalled) return;
    titleEl._blurHookInstalled = true;
    titleEl.addEventListener('blur', function() {
        var modal = document.getElementById('wrapUpModal');
        if (!modal || modal.dataset.isNewCase !== 'true') return;
        if (_modalState.mode !== 'wrapup') return;
        if (_modalState.summaryInflight) return;
        // Only fire if we don't yet have a generated summary, OR if the
        // user is changing the title for a fresh attempt after a prior
        // "Enter a title" prompt.
        if (_modalState.generatedSummary) return;
        if (!titleEl.value.trim()) return;
        _generateSummary();
    });
}

// _generateSummary pre-generates the case summary so the user can review
// and prune the generated components before confirming wrap-up.
// The selected files are passed through so the model's diff input matches
// what will actually be committed.
function _generateSummary() {
    if (_modalState.summaryInflight) return;
    var modal = document.getElementById('wrapUpModal');
    var statusEl = document.getElementById('wrapUpTagsStatus');
    var section = document.getElementById('wrapUpTagsSection');

    // The set of files is sent purely to scope the diff input. After a
    // round of intermediate Commits the working tree will often be clean
    // — that's fine, the summary still has notes / transcripts / commit
    // descriptions to work from. Pass whatever is checked (may be empty).
    var files = [];
    document.querySelectorAll('#wrapUpFileList .form-check-input:checked').forEach(function(cb) {
        files.push(cb.value);
    });

    _modalState.summaryInflight = true;
    if (section) section.style.display = '';
    if (statusEl) statusEl.textContent = 'Generating…';
    _modalState.components = [];
    _modalState.componentsLoaded = false;
    _renderChips();

    var body = { files: files };
    if (modal.dataset.isNewCase === 'true') {
        // Wrap-up is creating the case in this same submit. The server
        // synthesizes a CaseJSON in memory from title/kind plus the
        // active session's recent prompts.
        body.title = (document.getElementById('wrapUpNewTitle').value || '').trim();
        body.kind = document.getElementById('wrapUpNewKind').value;
        if (typeof WRAPUP_SESSION_ID !== 'undefined' && WRAPUP_SESSION_ID) {
            body.session_id = WRAPUP_SESSION_ID;
        }
    } else {
        body.case_id = modal.dataset.caseId;
    }

    // Paint the component chips immediately from the deterministic deriver —
    // no LLM, so this returns in milliseconds, independent of the prose
    // summary (which needs the model and, for new cases, a title).
    _deriveComponents(body);

    // The prose summary needs a title for the new-case path; defer it until
    // one is entered. Chips are already painting regardless.
    if (modal.dataset.isNewCase === 'true' && !body.title) {
        _modalState.summaryInflight = false;
        if (statusEl) statusEl.textContent = '';
        return;
    }

    fetch(_apiBase() + '/generate-summary', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(body)
    })
    .then(function(r) { return r.json().then(function(d) { return {ok: r.ok, data: d}; }); })
    .then(function(result) {
        _modalState.summaryInflight = false;
        if (!result.ok) {
            // generate-summary only supplies the prose fields now; the chips
            // are already painted by the deriver and the server regenerates
            // the summary at commit time, so fail quietly here.
            if (statusEl) statusEl.textContent = '';
            return;
        }
        var s = result.data.data || result.data;
        _modalState.generatedSummary = s;
        // Backstop: if the fast deriver hasn't populated chips (e.g. it
        // errored), use the identical deterministic components carried in
        // this response. Never clobber chips the user may have edited.
        if (!_modalState.componentsLoaded && s.components) {
            _modalState.components = s.components.slice();
            _modalState.componentsLoaded = true;
            _renderChips();
        }
        if (statusEl) statusEl.textContent = '';
    })
    .catch(function() {
        _modalState.summaryInflight = false;
        if (statusEl) statusEl.textContent = '';
    });
}

// _deriveComponents paints the component chips from the deterministic
// server-side deriver — no LLM, so it returns near-instantly. Runs
// independently of the prose summary and fails silently (generate-summary's
// response carries the same deterministic components as a backstop).
function _deriveComponents(body) {
    fetch(_apiBase() + '/derive-components', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({ case_id: body.case_id, files: body.files })
    })
    .then(function(r) { return r.ok ? r.json() : null; })
    .then(function(d) {
        if (!d) return;
        // Don't clobber chips the user may already have edited.
        if (_modalState.componentsLoaded) return;
        _modalState.components = (d.components || []).slice();
        _modalState.componentsLoaded = true;
        _renderChips();
    })
    .catch(function() { /* silent — generate-summary backstops */ });
}

// _renderChips paints the components list from _modalState.
// Each chip's × removes it from the in-memory list.
function _renderChips() {
    var compEl = document.getElementById('wrapUpComponentsList');
    if (compEl) compEl.innerHTML = _chipHTML('component', _modalState.components) || '<span class="text-muted small">(none)</span>';
}

function _chipHTML(kind, values) {
    return values.map(function(v, i) {
        return '<span class="wrap-up-chip ' + kind + '">' +
            escapeWrapUpHtml(v) +
            '<button type="button" class="wrap-up-chip-remove" ' +
            'aria-label="Remove" onclick="removeWrapUpChip(\'' + kind + '\', ' + i + ')">' +
            '×</button></span>';
    }).join('');
}

// Exposed globally so the chip buttons can find it.
window.removeWrapUpChip = function(kind, index) {
    var arr = _modalState.components;
    if (index < 0 || index >= arr.length) return;
    arr.splice(index, 1);
    _renderChips();
};

function renderWrapUpFiles(status) {
    var el = document.getElementById('wrapUpFileList');
    if (status.clean) {
        el.innerHTML = '<div class="text-muted">Working tree is clean - no files to commit.</div>';
        return;
    }

    var html = '';
    var categories = [
        {files: status.modified || [], label: 'Modified', color: 'text-warning', icon: 'M'},
        {files: status.added || [], label: 'Added', color: 'text-success', icon: 'A'},
        {files: status.deleted || [], label: 'Deleted', color: 'text-danger', icon: 'D'},
        {files: status.renamed || [], label: 'Renamed', color: 'text-info', icon: 'R'},
        {files: status.untracked || [], label: 'Untracked', color: 'text-muted', icon: '?'}
    ];

    categories.forEach(function(cat) {
        cat.files.forEach(function(f) {
            var id = 'wrapup-file-' + btoa(f).replace(/[^a-zA-Z0-9]/g, '');
            html += '<div class="form-check">' +
                '<input class="form-check-input" type="checkbox" id="' + id + '" value="' + escapeWrapUpAttr(f) + '" checked>' +
                '<label class="form-check-label" for="' + id + '">' +
                '<span class="' + cat.color + ' fw-bold me-1">' + cat.icon + '</span>' +
                escapeWrapUpHtml(f) +
                '</label></div>';
        });
    });

    el.innerHTML = html || '<div class="text-muted">No changes detected.</div>';
}

function renderWrapUpTraces(reports, sessionCreatedAt) {
    var section = document.getElementById('wrapUpTraceSection');
    var el = document.getElementById('wrapUpTraceList');
    if (!section || !el) return;
    if (_modalState.mode === 'commit') {
        section.style.display = 'none';
        return;
    }

    var completed = reports.filter(function(r) { return r.status === 'completed'; });
    if (completed.length === 0) {
        section.style.display = 'none';
        el.innerHTML = '';
        return;
    }

    section.style.display = '';
    var sessionTime = sessionCreatedAt ? new Date(sessionCreatedAt).getTime() : 0;

    var html = '';
    completed.forEach(function(r) {
        var id = 'wrapup-trace-' + btoa(r.name).replace(/[^a-zA-Z0-9]/g, '');
        var createdAt = new Date(r.created_at);
        var checked = sessionTime && createdAt.getTime() >= sessionTime ? ' checked' : '';
        var timeStr = createdAt.toLocaleString();
        var detail = escapeWrapUpHtml(r.group) + ' · ' + r.entry_count + ' entries · ' + timeStr;
        html += '<div class="form-check">' +
            '<input class="form-check-input" type="checkbox" id="' + id + '" value="' + escapeWrapUpAttr(r.name) + '"' + checked + '>' +
            '<label class="form-check-label" for="' + id + '">' +
            '<i class="fa-solid fa-magnifying-glass text-info me-1"></i>' +
            escapeWrapUpHtml(r.name) +
            '<small class="text-muted ms-2">' + detail + '</small>' +
            '</label></div>';
    });

    el.innerHTML = html;
}

function renderWrapUpRelatedSessions(sessions, otherAgent) {
    var section = document.getElementById('wrapUpRelatedSection');
    var el = document.getElementById('wrapUpRelatedList');
    var label = document.getElementById('wrapUpRelatedLabel');
    if (!section || !el) return;
    if (_modalState.mode === 'commit') {
        section.style.display = 'none';
        return;
    }

    if (!sessions || sessions.length === 0) {
        section.style.display = 'none';
        el.innerHTML = '';
        return;
    }
    section.style.display = '';
    if (label) {
        var pretty = otherAgent === 'codex' ? 'Codex' : 'Claude';
        label.textContent = 'Related ' + pretty + ' sessions to archive';
    }

    var ownID = (typeof WRAPUP_SESSION_ID !== 'undefined' && WRAPUP_SESSION_ID) ? WRAPUP_SESSION_ID : '';

    var html = '';
    sessions.forEach(function(s) {
        if (!s || !s.id || s.id === ownID) return;
        var id = 'wrapup-related-' + btoa(s.id).replace(/[^a-zA-Z0-9]/g, '');
        var lastInput = s.last_user_input ? new Date(s.last_user_input).toLocaleString() : '(no activity)';
        var displayName = s.display_name || '(unnamed)';
        html += '<div class="form-check">' +
            '<input class="form-check-input" type="checkbox" id="' + id + '" value="' + escapeWrapUpAttr(s.id) +
                '" data-agent="' + escapeWrapUpAttr(otherAgent) + '">' +
            '<label class="form-check-label" for="' + id + '">' +
            '<i class="fa-solid fa-message me-1"></i>' +
            escapeWrapUpHtml(displayName) +
            '<small class="text-muted ms-2">' + escapeWrapUpHtml(lastInput) + '</small>' +
            '</label></div>';
    });

    if (!html) {
        section.style.display = 'none';
        el.innerHTML = '';
        return;
    }
    el.innerHTML = html;
}

function slugify(s) {
    s = s.toLowerCase();
    s = s.replace(/[\/_.]/g, '-');
    s = s.replace(/ /g, '-');
    s = s.replace(/[^a-z0-9-]+/g, '');
    s = s.replace(/-+/g, '-');
    s = s.replace(/^-+|-+$/g, '');
    return s;
}

// updateWrapUpCommitMsg is kept for back-compat with templates that wire
// oninput handlers to it on the title field. It now only seeds an initial
// fallback message when generation hasn't produced one yet.
function updateWrapUpCommitMsg() {
    var modal = document.getElementById('wrapUpModal');
    var msgEl = document.getElementById('wrapUpCommitMsg');
    if (_modalState.userTouchedMessage) return;
    if (msgEl.value && msgEl.value.trim() !== '') return;
    // Don't overwrite a generation-in-flight placeholder.
}

function addWrapUpLink() {
    var container = document.getElementById('wrapUpLinks');
    var row = document.createElement('div');
    row.className = 'input-group input-group-sm mb-2';
    row.innerHTML = '<input type="text" class="form-control" placeholder="Title" data-link-title>' +
        '<input type="url" class="form-control" placeholder="https://..." data-link-url>' +
        '<button class="btn btn-outline-danger" type="button" onclick="this.parentElement.remove()"><i class="fa-solid fa-xmark"></i></button>';
    container.appendChild(row);
    row.querySelector('[data-link-title]').focus();
}

function wrapUpConfirm() {
    var modal = document.getElementById('wrapUpModal');
    var btn = document.getElementById('wrapUpConfirmBtn');
    var errEl = document.getElementById('wrapUpError');
    var successEl = document.getElementById('wrapUpSuccess');
    errEl.style.display = 'none';
    successEl.style.display = 'none';

    var files = [];
    document.querySelectorAll('#wrapUpFileList .form-check-input:checked').forEach(function(cb) {
        files.push(cb.value);
    });

    var links = [];
    document.querySelectorAll('#wrapUpLinks .input-group').forEach(function(row) {
        var t = row.querySelector('[data-link-title]').value.trim();
        var u = row.querySelector('[data-link-url]').value.trim();
        if (t && u) links.push({title: t, url: u});
    });

    var traces = [];
    document.querySelectorAll('#wrapUpTraceList .form-check-input:checked').forEach(function(cb) {
        traces.push(cb.value);
    });

    var relatedSessions = [];
    document.querySelectorAll('#wrapUpRelatedList .form-check-input:checked').forEach(function(cb) {
        relatedSessions.push({ agent: cb.dataset.agent, session_id: cb.value });
    });

    var commitMsg = document.getElementById('wrapUpCommitMsg').value.trim();
    if (!commitMsg) {
        errEl.textContent = 'Commit message is required.';
        errEl.style.display = '';
        return;
    }

    var isCommit = _modalState.mode === 'commit';
    var body = {
        commit_message: commitMsg,
        files: files,
        description: _modalState.generatedDescription || '',
    };
    if (!isCommit) {
        body.links = links;
        body.traces = traces;
        body.related_sessions = relatedSessions;
        // Ship the user-edited summary. The server normalizes and writes
        // it as-is, skipping a second generation pass — so the chips the
        // user just curated are exactly what lands in case.json.
        if (_modalState.generatedSummary) {
            body.summary = Object.assign({}, _modalState.generatedSummary, {
                components: _modalState.components.slice(),
            });
        }
    }

    if (modal.dataset.isNewCase === 'true') {
        var title = (document.getElementById('wrapUpNewTitle').value || '').trim();
        if (!title) {
            errEl.textContent = 'Title is required for a new case.';
            errEl.style.display = '';
            return;
        }
        body.title = title;
        body.kind = document.getElementById('wrapUpNewKind').value;
        if (typeof WRAPUP_SESSION_ID !== 'undefined' && WRAPUP_SESSION_ID) {
            body.session_id = WRAPUP_SESSION_ID;
        }
    } else {
        body.case_id = modal.dataset.caseId;
        if (typeof WRAPUP_SESSION_ID !== 'undefined' && WRAPUP_SESSION_ID) {
            body.session_id = WRAPUP_SESSION_ID;
        }
    }

    btn.disabled = true;
    btn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> ' + (isCommit ? 'Committing…' : 'Wrapping up…');

    var endpoint = isCommit ? '/commit' : '/wrap-up';
    fetch(_apiBase() + endpoint, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(body)
    })
    .then(function(r) { return r.json().then(function(d) { return {ok: r.ok, data: d}; }); })
    .then(function(result) {
        if (!result.ok) {
            var msg = (result.data.error && result.data.error.message) || (isCommit ? 'Commit failed' : 'Wrap-up failed');
            throw new Error(msg);
        }
        var d = result.data.data || result.data;
        if (isCommit) {
            successEl.innerHTML = '<i class="fa-solid fa-circle-check me-1"></i> Committed <code>' +
                escapeWrapUpHtml(d.commit_hash) + '</code> to case <strong>' +
                escapeWrapUpHtml(d.case_id) + '</strong>';
        } else {
            successEl.innerHTML = '<i class="fa-solid fa-circle-check me-1"></i> Done! Case <strong>' +
                escapeWrapUpHtml(d.case_id) + '</strong> archived, commit <code>' +
                escapeWrapUpHtml(d.commit_hash) + '</code>';
        }
        successEl.style.display = '';
        btn.innerHTML = '<i class="fa-solid fa-check"></i> Done';
        setTimeout(function() {
            if (isCommit) {
                // Stay on the page; the session continues. Just close the
                // modal and refresh status so the file list is up-to-date.
                var bs = bootstrap.Modal.getInstance(modal);
                if (bs) bs.hide();
            } else {
                window.location.href = '/worktree/' + encodeURIComponent(WRAPUP_WORKTREE);
            }
        }, 1500);
    })
    .catch(function(err) {
        errEl.textContent = err.message || String(err);
        errEl.style.display = '';
        btn.disabled = false;
        btn.innerHTML = isCommit
            ? '<i class="fa-solid fa-code-commit"></i> Commit'
            : '<i class="fa-solid fa-flag-checkered"></i> Wrap Up';
    });
}

function escapeWrapUpHtml(s) {
    var d = document.createElement('div');
    d.textContent = s;
    return d.innerHTML;
}

function escapeWrapUpAttr(s) {
    var d = document.createElement('div');
    d.textContent = s;
    return d.innerHTML.replace(/"/g, '&quot;');
}
