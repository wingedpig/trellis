// Copyright (c) 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Wrap-up modal logic shared between Claude chat page and case detail page.
// Requires globals set by the including page:
//   WRAPUP_WORKTREE  — worktree name (string)
//   WRAPUP_SESSION_ID — session ID (string or null)
//   WRAPUP_SESSION_CREATED — ISO 8601 timestamp of session creation (string)
//   WRAPUP_CASE      — {id, title, kind} or null

function showWrapUpModal() {
    var modal = document.getElementById('wrapUpModal');
    if (!modal) return;

    // Reset state
    document.getElementById('wrapUpError').style.display = 'none';
    document.getElementById('wrapUpSuccess').style.display = 'none';
    document.getElementById('wrapUpConfirmBtn').disabled = false;
    document.getElementById('wrapUpConfirmBtn').innerHTML = '<i class="fa-solid fa-flag-checkered"></i> Wrap Up';
    document.getElementById('wrapUpLinks').innerHTML = '';
    document.getElementById('wrapUpFileList').innerHTML = '<div class="text-muted">Loading...</div>';
    document.getElementById('wrapUpCommitMsg').value = '';

    // Detect existing case
    var existingCase = typeof WRAPUP_CASE !== 'undefined' ? WRAPUP_CASE : null;

    var promises = [];

    // Fetch git status
    promises.push(
        fetch('/api/v1/claude/' + encodeURIComponent(WRAPUP_WORKTREE) + '/git-status')
        .then(function(r) { return r.json(); })
        .then(function(d) { return d.data || d; })
    );

    // If we have a session ID and no existing case, check for linked case
    if (WRAPUP_SESSION_ID && !existingCase) {
        promises.push(
            fetch('/api/v1/claude/' + encodeURIComponent(WRAPUP_WORKTREE) + '/session-case?session_id=' + encodeURIComponent(WRAPUP_SESSION_ID))
            .then(function(r) {
                if (!r.ok) return null;
                return r.json().then(function(d) { return d.data || d; });
            })
            .catch(function() { return null; })
        );
    } else {
        promises.push(Promise.resolve(null));
    }

    // Fetch trace reports
    promises.push(
        fetch('/api/v1/claude/' + encodeURIComponent(WRAPUP_WORKTREE) + '/trace-reports')
        .then(function(r) { return r.json(); })
        .then(function(d) { return (d.data || d).reports || []; })
        .catch(function() { return []; })
    );

    Promise.all(promises).then(function(results) {
        var status = results[0];
        var linkedCase = results[1];
        var traceReports = results[2];

        if (linkedCase) {
            existingCase = linkedCase;
        }

        // Populate case info section
        var caseInfoEl = document.getElementById('wrapUpCaseInfo');
        var newCaseEl = document.getElementById('wrapUpNewCase');
        if (existingCase) {
            caseInfoEl.style.display = '';
            newCaseEl.style.display = 'none';
            document.getElementById('wrapUpCaseId').textContent = existingCase.case_id || existingCase.id;
            document.getElementById('wrapUpCaseTitle').textContent = existingCase.title;
            document.getElementById('wrapUpCaseKind').textContent = existingCase.kind;
            // Store for submit
            modal.dataset.caseId = existingCase.case_id || existingCase.id;
            modal.dataset.isNewCase = 'false';
        } else {
            caseInfoEl.style.display = 'none';
            newCaseEl.style.display = '';
            document.getElementById('wrapUpNewTitle').value = '';
            document.getElementById('wrapUpNewKind').value = 'task';
            modal.dataset.caseId = '';
            modal.dataset.isNewCase = 'true';
        }

        // Render files
        renderWrapUpFiles(status);

        // Render traces
        var sessionCreated = (typeof WRAPUP_SESSION_CREATED !== 'undefined' && WRAPUP_SESSION_CREATED) ? WRAPUP_SESSION_CREATED : null;
        renderWrapUpTraces(traceReports, sessionCreated);

        // Pre-fill commit message
        updateWrapUpCommitMsg();

        var bsModal = new bootstrap.Modal(modal);
        bsModal.show();
    }).catch(function(err) {
        alert('Failed to load wrap-up data: ' + err);
    });
}

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

    // Filter to only completed traces
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
        var detail = escapeWrapUpHtml(r.group) + ' \u00b7 ' + r.entry_count + ' entries \u00b7 ' + timeStr;
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

function slugify(s) {
    s = s.toLowerCase();
    s = s.replace(/[\/_.]/g, '-');
    s = s.replace(/ /g, '-');
    s = s.replace(/[^a-z0-9-]+/g, '');
    s = s.replace(/-+/g, '-');
    s = s.replace(/^-+|-+$/g, '');
    return s;
}

function updateWrapUpCommitMsg() {
    var modal = document.getElementById('wrapUpModal');
    var msgEl = document.getElementById('wrapUpCommitMsg');
    var title, caseId;

    if (modal.dataset.isNewCase === 'true') {
        title = (document.getElementById('wrapUpNewTitle').value || '').trim();
        if (!title) {
            msgEl.value = '';
            return;
        }
        var today = new Date().toISOString().slice(0, 10);
        caseId = today + '__' + slugify(title);
    } else {
        caseId = modal.dataset.caseId;
        title = document.getElementById('wrapUpCaseTitle').textContent;
    }

    msgEl.value = title + ' [case: ' + caseId + ']';
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

    // Gather selected files
    var files = [];
    document.querySelectorAll('#wrapUpFileList .form-check-input:checked').forEach(function(cb) {
        files.push(cb.value);
    });

    // Gather links
    var links = [];
    document.querySelectorAll('#wrapUpLinks .input-group').forEach(function(row) {
        var t = row.querySelector('[data-link-title]').value.trim();
        var u = row.querySelector('[data-link-url]').value.trim();
        if (t && u) links.push({title: t, url: u});
    });

    // Gather selected traces
    var traces = [];
    document.querySelectorAll('#wrapUpTraceList .form-check-input:checked').forEach(function(cb) {
        traces.push(cb.value);
    });

    var commitMsg = document.getElementById('wrapUpCommitMsg').value.trim();
    if (!commitMsg) {
        errEl.textContent = 'Commit message is required.';
        errEl.style.display = '';
        return;
    }

    var body = {
        commit_message: commitMsg,
        files: files,
        links: links,
        traces: traces
    };

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
    }

    btn.disabled = true;
    btn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Wrapping up...';

    fetch('/api/v1/claude/' + encodeURIComponent(WRAPUP_WORKTREE) + '/wrap-up', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(body)
    })
    .then(function(r) {
        return r.json().then(function(d) { return {ok: r.ok, data: d}; });
    })
    .then(function(result) {
        if (!result.ok) {
            var msg = (result.data.error && result.data.error.message) || 'Wrap-up failed';
            throw new Error(msg);
        }
        var d = result.data.data || result.data;
        successEl.innerHTML = '<i class="fa-solid fa-circle-check me-1"></i> Done! Case <strong>' +
            escapeWrapUpHtml(d.case_id) + '</strong> archived, commit <code>' +
            escapeWrapUpHtml(d.commit_hash) + '</code>';
        successEl.style.display = '';
        btn.innerHTML = '<i class="fa-solid fa-check"></i> Done';
        // Redirect after a short delay
        setTimeout(function() {
            window.location.href = '/worktree/' + encodeURIComponent(WRAPUP_WORKTREE);
        }, 1500);
    })
    .catch(function(err) {
        errEl.textContent = err.message || String(err);
        errEl.style.display = '';
        btn.disabled = false;
        btn.innerHTML = '<i class="fa-solid fa-flag-checkered"></i> Wrap Up';
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
