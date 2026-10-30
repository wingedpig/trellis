// File attachments for the Claude and Codex composers.
//
// Files picked with the paperclip, dropped on the input area, or pasted into
// the textarea upload right away to the session's attachments endpoint. The
// returned IDs ride along with the next "message" over the WebSocket; the
// server hands the agent each file's path (and images inline).
(function() {
    'use strict';

    function attachmentURL(agent, sessionId, id) {
        return '/api/v1/' + agent + '/sessions/' + encodeURIComponent(sessionId) +
            '/attachments/' + encodeURIComponent(id);
    }

    function humanSize(n) {
        if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB';
        if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + ' KB';
        return n + ' B';
    }

    function isImage(mime) {
        return /^image\/(png|jpeg|gif|webp)$/.test(mime || '');
    }

    // renderList builds the attachment strip shown inside a user bubble.
    function renderList(agent, sessionId, files) {
        var list = document.createElement('div');
        list.className = 'agent-attach-list';
        files.forEach(function(f) {
            var url = attachmentURL(agent, sessionId, f.id);
            var a = document.createElement('a');
            a.href = url;
            a.title = f.name + ' (' + humanSize(f.size) + ')';
            if (isImage(f.mime)) {
                a.className = 'agent-attach-thumb';
                a.target = '_blank';
                a.rel = 'noopener';
                var img = document.createElement('img');
                img.src = url;
                img.alt = f.name;
                img.loading = 'lazy';
                a.appendChild(img);
            } else {
                a.className = 'agent-attach-file';
                a.setAttribute('download', f.name);
                var icon = document.createElement('i');
                icon.className = 'fa-solid fa-file';
                var name = document.createElement('span');
                name.textContent = f.name;
                a.appendChild(icon);
                a.appendChild(name);
            }
            list.appendChild(a);
        });
        return list;
    }

    // create wires one composer. opts: agent ('claude' | 'codex'), sessionId,
    // inputEl (textarea), dropEl (input area), button (paperclip), listEl
    // (where pending chips go).
    function create(opts) {
        var pending = []; // { file?, chip, status: 'uploading'|'done'|'error', data? }
        var fileInput = document.createElement('input');
        fileInput.type = 'file';
        fileInput.multiple = true;
        fileInput.hidden = true;
        opts.dropEl.appendChild(fileInput);

        function refresh() {
            opts.listEl.style.display = pending.length ? '' : 'none';
        }

        function remove(entry) {
            var i = pending.indexOf(entry);
            if (i >= 0) pending.splice(i, 1);
            entry.chip.remove();
            if (entry.preview) URL.revokeObjectURL(entry.preview);
            refresh();
        }

        function addFile(file) {
            var entry = { status: 'uploading' };
            var chip = document.createElement('div');
            chip.className = 'agent-attach-chip uploading';
            if (isImage(file.type)) {
                entry.preview = URL.createObjectURL(file);
                var img = document.createElement('img');
                img.src = entry.preview;
                img.alt = '';
                chip.appendChild(img);
            } else {
                var icon = document.createElement('i');
                icon.className = 'fa-solid fa-file';
                chip.appendChild(icon);
            }
            var label = document.createElement('span');
            label.className = 'agent-attach-name';
            label.textContent = file.name || 'pasted file';
            chip.appendChild(label);
            var status = document.createElement('span');
            status.className = 'agent-attach-status';
            status.textContent = 'uploading…';
            chip.appendChild(status);
            var x = document.createElement('button');
            x.type = 'button';
            x.className = 'agent-attach-remove';
            x.title = 'Remove';
            x.innerHTML = '<i class="fa-solid fa-xmark"></i>';
            x.addEventListener('click', function() { remove(entry); });
            chip.appendChild(x);
            entry.chip = chip;
            pending.push(entry);
            opts.listEl.appendChild(chip);
            refresh();

            var form = new FormData();
            form.append('file', file, file.name || 'pasted');
            fetch('/api/v1/' + opts.agent + '/sessions/' + encodeURIComponent(opts.sessionId) + '/attachments', {
                method: 'POST',
                body: form
            })
                .then(function(r) {
                    return r.json().then(function(body) {
                        if (!r.ok) throw new Error((body && body.error && body.error.message) || r.statusText);
                        return body.data;
                    });
                })
                .then(function(data) {
                    entry.status = 'done';
                    entry.data = data;
                    chip.classList.remove('uploading');
                    status.textContent = humanSize(data.size);
                })
                .catch(function(err) {
                    entry.status = 'error';
                    chip.classList.remove('uploading');
                    chip.classList.add('error');
                    status.textContent = err.message || 'upload failed';
                });
        }

        function addFiles(files) {
            for (var i = 0; i < files.length; i++) addFile(files[i]);
        }

        opts.button.addEventListener('click', function() { fileInput.click(); });
        fileInput.addEventListener('change', function() {
            addFiles(fileInput.files);
            fileInput.value = '';
        });

        opts.inputEl.addEventListener('paste', function(e) {
            var files = e.clipboardData && e.clipboardData.files;
            if (!files || !files.length) return;
            e.preventDefault();
            addFiles(files);
        });

        var dragDepth = 0;
        function hasFiles(e) {
            return e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types, 'Files') >= 0;
        }
        opts.dropEl.addEventListener('dragenter', function(e) {
            if (!hasFiles(e)) return;
            e.preventDefault();
            dragDepth++;
            opts.dropEl.classList.add('agent-attach-dragover');
        });
        opts.dropEl.addEventListener('dragover', function(e) {
            if (hasFiles(e)) e.preventDefault();
        });
        opts.dropEl.addEventListener('dragleave', function(e) {
            if (!hasFiles(e)) return;
            if (--dragDepth <= 0) {
                dragDepth = 0;
                opts.dropEl.classList.remove('agent-attach-dragover');
            }
        });
        opts.dropEl.addEventListener('drop', function(e) {
            if (!hasFiles(e)) return;
            e.preventDefault();
            dragDepth = 0;
            opts.dropEl.classList.remove('agent-attach-dragover');
            addFiles(e.dataTransfer.files);
        });

        refresh();

        return {
            // ready is false while an upload is in flight or one has failed
            // (the user removes it), so a send never silently drops a file.
            ready: function() {
                return pending.every(function(p) { return p.status === 'done'; });
            },
            // files are the uploaded attachments (failed uploads are left out).
            files: function() {
                return pending.filter(function(p) { return p.status === 'done'; })
                    .map(function(p) { return p.data; });
            },
            clear: function() {
                pending.slice().forEach(remove);
            }
        };
    }

    window.TrellisAttachments = { create: create, renderList: renderList };
})();
