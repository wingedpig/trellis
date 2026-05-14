// Codex Chat Interface
//
// Consumes the WebSocket protocol exposed by internal/api/handlers/codex.go.
// The wire model mirrors Claude's chat handler but the events come from
// the Codex app-server's JSON-RPC protocol (thread/turn/item).
(function () {
    'use strict';

    // ---------- DOM ----------
    const messagesEl = document.getElementById('codex-messages');
    const inputEl = document.getElementById('codex-input');
    const sendBtn = document.getElementById('codex-send-btn');
    const cancelBtn = document.getElementById('codex-cancel-btn');

    // ---------- State ----------
    let ws = null;
    let reconnectTimer = null;
    let generating = false;

    // Per-turn streaming accumulator. Items are rendered as they arrive and
    // updated in place by id; the streaming agent_message_delta events
    // append to the matching item's text.
    //
    // turnEl    — the wrapper <div> for the current assistant turn
    // itemEls   — Map<itemId, { kind, root, body, raw, parsedJSON }>
    // workingEl — the spinner shown while a turn is in flight
    let turnEl = null;
    let itemEls = new Map();
    let workingEl = null;
    let lastTurnID = null;

    // Most recent thread-level token usage (from `thread/tokenUsage/updated`).
    let tokenUsage = null;
    // Codex doesn't broadcast its current model's context window in the
    // app-server protocol, so we use a conservative default the user can
    // override later if we expose it in config.
    const CODEX_CONTEXT_WINDOW_DEFAULT = 200000;

    // Cross-agent quote: raw text per assistant message, indexed by turnEl,
    // used by the "copy as quote" button.
    const turnPlaintext = new WeakMap();

    // ---------- marked / hljs setup ----------
    if (typeof marked !== 'undefined') {
        marked.setOptions({
            highlight: function (code, lang) {
                // Only highlight when we know the language. highlightAuto is
                // tens of ms per block and made history re-renders block the
                // main thread; un-tagged fences stay as plain escaped text.
                if (typeof hljs !== 'undefined' && lang && hljs.getLanguage(lang)) {
                    return hljs.highlight(code, { language: lang }).value;
                }
                return escapeHtml(code);
            },
            breaks: false,
            gfm: true,
        });
    }

    window.addEventListener('trellis-theme-change', function (e) {
        const theme = e.detail.theme;
        const lt = document.getElementById('hljs-light-codex');
        const dk = document.getElementById('hljs-dark-codex');
        if (lt) lt.disabled = theme === 'dark';
        if (dk) dk.disabled = theme !== 'dark';
    });

    // ---------- Helpers ----------
    function escapeHtml(s) {
        if (s == null) return '';
        return String(s)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    // Scroll the messages container, not the page. .codex-messages has its
    // own overflow-y so the chat scrolls inside the chat container — same
    // shape as Claude's chat.
    function scrollToBottom() {
        requestAnimationFrame(function () {
            messagesEl.scrollTop = messagesEl.scrollHeight;
        });
    }

    function addCopyButtonsToCode(container) {
        if (!container) return;
        const blocks = container.querySelectorAll('pre');
        blocks.forEach(function (pre) {
            if (pre.querySelector('.codex-copy-code')) return;
            const btn = document.createElement('button');
            btn.className = 'codex-copy-code';
            btn.title = 'Copy';
            btn.innerHTML = '<i class="fa-solid fa-copy"></i>';
            btn.addEventListener('click', function () {
                const code = pre.innerText;
                navigator.clipboard.writeText(code).then(function () {
                    btn.innerHTML = '<i class="fa-solid fa-check"></i>';
                    setTimeout(function () {
                        btn.innerHTML = '<i class="fa-solid fa-copy"></i>';
                    }, 1200);
                });
            });
            pre.style.position = 'relative';
            pre.appendChild(btn);
        });
    }

    function renderMarkdown(text) {
        if (!text) return '';
        if (typeof marked === 'undefined') return escapeHtml(text);
        try {
            return marked.parse(text);
        } catch (e) {
            return escapeHtml(text);
        }
    }

    // Build a plain-text rendering of an item, used for the cross-agent
    // copy-as-quote feature. Mirrors internal/agentmsg's RenderPlain.
    function itemPlain(item) {
        if (!item) return '';
        switch (item.type) {
            case 'agentMessage':
                return item.text || '';
            case 'reasoning':
                return '_(reasoning)_ ' + (item.text || '');
            case 'commandExecution':
                var cmd = item.command || '';
                if (typeof cmd === 'object') cmd = JSON.stringify(cmd);
                var s = '[command: ' + cmd + ']';
                if (item.output) s += '\n```\n' + item.output + '\n```';
                return s;
            case 'fileChange':
                return '[file change: ' + (item.path || '?') + ']';
            case 'plan':
                return '[plan]\n' + (item.text || '');
            default:
                return '[' + item.type + '] ' + (item.text || '');
        }
    }

    // ---------- WebSocket ----------
    function connect() {
        if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
            return;
        }
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        let url;
        if (typeof window.CODEX_SESSION !== 'undefined' && window.CODEX_SESSION) {
            url = proto + '//' + location.host + '/api/v1/codex/sessions/' + encodeURIComponent(window.CODEX_SESSION) + '/ws';
        } else {
            url = proto + '//' + location.host + '/api/v1/codex/' + encodeURIComponent(window.CODEX_WORKTREE) + '/ws';
        }
        ws = new WebSocket(url);
        ws.onopen = function () { clearTimeout(reconnectTimer); };
        ws.onmessage = function (e) {
            if (typeof e.data !== 'string' || !e.data) return;
            try {
                // Temporary perf instrumentation — remove once history-render
                // cold-load slowness is resolved.
                const isHistory = e.data.length > 1000 && e.data.indexOf('"type":"history"') !== -1;
                const t0 = isHistory ? performance.now() : 0;
                const parsed = JSON.parse(e.data);
                const t1 = isHistory ? performance.now() : 0;
                handleServerMessage(parsed);
                if (isHistory) {
                    const t2 = performance.now();
                    console.log('[codex perf] history payload=' + (e.data.length / 1024).toFixed(1) + 'KB' +
                        ' messages=' + (parsed.messages ? parsed.messages.length : 0) +
                        ' JSON.parse=' + (t1 - t0).toFixed(0) + 'ms' +
                        ' handle=' + (t2 - t1).toFixed(0) + 'ms');
                    // Measure gap from JS completion to next event-loop tick
                    // (catches GC pauses and browser internal work) and to
                    // next paint.
                    setTimeout(function () {
                        console.log('[codex perf] post-handle event-loop gap=' +
                            (performance.now() - t2).toFixed(0) + 'ms');
                    }, 0);
                    requestAnimationFrame(function () {
                        requestAnimationFrame(function () {
                            console.log('[codex perf] post-handle paint gap=' +
                                (performance.now() - t2).toFixed(0) + 'ms');
                        });
                    });
                }
            } catch (err) {
                console.error('codex: bad WS message', err, e.data);
            }
        };
        ws.onclose = function () {
            ws = null;
            reconnectTimer = setTimeout(connect, 3000);
        };
        ws.onerror = function () { /* onclose will follow */ };
    }

    function sendWS(msg) {
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify(msg));
        }
    }

    // ---------- Server messages ----------
    function handleServerMessage(msg) {
        switch (msg.type) {
            case 'history':
                renderHistory(msg.messages || [], !!msg.generating);
                if (msg.generating) setGenerating(true);
                else requestAnimationFrame(function () { inputEl.focus(); });
                if (msg.token_usage) {
                    tokenUsage = msg.token_usage;
                    renderContextUsage();
                }
                break;
            case 'stream':
                handleStreamEvent(msg.event);
                break;
            case 'done':
                finishTurn();
                setGenerating(false);
                break;
            case 'status':
                setGenerating(msg.generating || false);
                break;
            case 'error':
                showError(msg.message || 'Unknown error');
                setGenerating(false);
                break;
        }
    }

    function handleStreamEvent(ev) {
        if (!ev) return;
        switch (ev.type) {
            case 'thread_started':
                // Informational. We don't currently surface threadId in UI.
                break;
            case 'turn_started':
                lastTurnID = ev.turn_id || null;
                ensureTurn();
                showWorking();
                break;
            case 'item_started':
                ensureTurn();
                if (ev.item) renderItem(ev.item, false);
                hideWorking();
                break;
            case 'item_completed':
                ensureTurn();
                if (ev.item) renderItem(ev.item, true);
                break;
            case 'agent_message_delta':
                appendAgentDelta(ev.item_id, ev.delta || '');
                break;
            case 'command_output_delta':
                appendCommandOutput(ev.item_id, ev.delta || '', ev.stream || 'stdout');
                break;
            case 'turn_completed':
                finishTurn();
                break;
            case 'turn_failed':
                showError(ev.error || 'Turn failed');
                finishTurn();
                break;
            case 'approval_request':
                showApprovalPrompt(ev);
                break;
            case 'diff_updated':
                // No-op for v1; pass-through info.
                break;
            case 'plan_updated':
                // No-op for v1.
                break;
            case 'token_usage':
                if (ev.params) {
                    const parsed = safeParseJSON(ev.params);
                    const total = parsed && parsed.tokenUsage && parsed.tokenUsage.total;
                    if (total) {
                        tokenUsage = {
                            total_tokens: total.totalTokens || 0,
                            input_tokens: total.inputTokens || 0,
                            cached_input_tokens: total.cachedInputTokens || 0,
                            output_tokens: total.outputTokens || 0,
                            reasoning_output_tokens: total.reasoningOutputTokens || 0,
                        };
                        renderContextUsage();
                    }
                }
                break;
            default:
                // Pass-through informational events (token usage,
                // rateLimits/updated, etc.). Not rendered.
                break;
        }
    }

    // ---------- History rendering ----------
    function renderHistory(messages, isGenerating) {
        messagesEl.innerHTML = '';
        if (!messages.length) {
            showEmptyState();
            return;
        }
        for (let i = 0; i < messages.length; i++) {
            const m = messages[i];
            if (m.role === 'user') {
                renderUserMessage(m, i);
            } else {
                renderAssistantMessage(m, i, /*isLast=*/ i === messages.length - 1 && isGenerating);
            }
        }
        // After loading history, if we're still generating, the last message
        // is an in-progress assistant turn. Wire up streaming targets.
        if (isGenerating && messages.length > 0) {
            const last = messages[messages.length - 1];
            if (last.role === 'assistant') {
                turnEl = messagesEl.lastElementChild;
                rebuildItemElsFromTurn(last);
            }
        }
        scrollToBottom();
    }

    // After re-rendering a still-streaming turn from history, rebuild the
    // itemEls map by walking the rendered DOM so subsequent deltas land on
    // the right element.
    function rebuildItemElsFromTurn(msg) {
        itemEls = new Map();
        if (!turnEl || !msg.items) return;
        const itemDivs = turnEl.querySelectorAll('[data-item-id]');
        for (const div of itemDivs) {
            const id = div.getAttribute('data-item-id');
            const found = msg.items.find(function (it) { return it.id === id; });
            if (!found) continue;
            itemEls.set(id, {
                kind: found.type,
                root: div,
                body: div.querySelector('.codex-item-body') || div,
                raw: found.text || '',
                parsedJSON: null,
            });
        }
    }

    function renderUserMessage(msg, index) {
        const wrapper = document.createElement('div');
        wrapper.className = 'codex-message codex-message-user';
        wrapper.dataset.messageIndex = String(index);

        const bubble = document.createElement('div');
        bubble.className = 'codex-bubble codex-bubble-user';
        const text = (msg.items && msg.items[0] && msg.items[0].text) || '';
        bubble.textContent = text;
        wrapper.appendChild(bubble);

        attachMessageActions(wrapper, function () {
            return text;
        }, /*role=*/ 'user');

        messagesEl.appendChild(wrapper);
    }

    function renderAssistantMessage(msg, index, isStreaming) {
        const wrapper = document.createElement('div');
        wrapper.className = 'codex-message codex-message-assistant';
        wrapper.dataset.messageIndex = String(index);

        const bubble = document.createElement('div');
        bubble.className = 'codex-bubble codex-bubble-assistant';
        wrapper.appendChild(bubble);

        const items = msg.items || [];
        for (const it of items) {
            const node = renderItemNode(it, /*completed=*/ !isStreaming);
            if (node) bubble.appendChild(node);
        }

        // Plain text for the "copy as quote" button — concat all items.
        const plain = items.map(itemPlain).filter(Boolean).join('\n\n');
        turnPlaintext.set(wrapper, plain);

        attachMessageActions(wrapper, function () {
            return turnPlaintext.get(wrapper) || '';
        }, /*role=*/ 'assistant');
        attachForkButton(wrapper, index);

        messagesEl.appendChild(wrapper);
    }

    // ---------- Per-message actions: copy, copy-as-quote, fork ----------
    //
    // Class names match the renamed claude.css → codex.css rules:
    //   .codex-message-copy → transparent button, hover-only visibility,
    //                         theme-aware foreground/background.
    //   .codex-message-fork → same, used for the fork button below.
    function attachMessageActions(wrapper, getText, role) {
        const copyBtn = document.createElement('button');
        copyBtn.type = 'button';
        copyBtn.className = 'codex-message-copy';
        copyBtn.title = 'Copy text';
        copyBtn.innerHTML = '<i class="fa-solid fa-copy"></i>';
        copyBtn.addEventListener('click', function () {
            navigator.clipboard.writeText(getText()).then(function () {
                copyBtn.innerHTML = '<i class="fa-solid fa-check"></i>';
                setTimeout(function () { copyBtn.innerHTML = '<i class="fa-solid fa-copy"></i>'; }, 1200);
            });
        });
        wrapper.appendChild(copyBtn);

        const quoteBtn = document.createElement('button');
        quoteBtn.type = 'button';
        quoteBtn.className = 'codex-message-copy';
        quoteBtn.title = 'Copy as quote (paste into Claude)';
        quoteBtn.innerHTML = '<i class="fa-solid fa-quote-right"></i>';
        quoteBtn.addEventListener('click', function () {
            navigator.clipboard.writeText(buildQuote(getText(), role, 'codex')).then(function () {
                quoteBtn.innerHTML = '<i class="fa-solid fa-check"></i>';
                setTimeout(function () { quoteBtn.innerHTML = '<i class="fa-solid fa-quote-right"></i>'; }, 1200);
            });
        });
        wrapper.appendChild(quoteBtn);
    }

    function buildQuote(text, role, agent) {
        if (!text) return '';
        const header = '[from ' + agent + ' · ' + role + ']';
        const lines = text.split('\n').map(function (l) { return '> ' + l; }).join('\n');
        return '> ' + header + '\n' + lines;
    }

    function attachForkButton(wrapper, index) {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'codex-message-fork';
        btn.title = 'Fork session here';
        btn.innerHTML = '<i class="fa-solid fa-code-branch"></i>';
        btn.addEventListener('click', function () { openForkModal(index); });
        wrapper.appendChild(btn);
    }

    // ---------- Fork modal ----------
    window.codexForkSubmit = function () {
        const idx = parseInt(document.getElementById('codexForkIndex').value, 10);
        const name = document.getElementById('codexForkName').value.trim();
        if (isNaN(idx)) return;
        fetch('/api/v1/codex/sessions/' + encodeURIComponent(window.CODEX_SESSION) + '/fork', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ message_index: idx, display_name: name }),
        })
            .then(function (r) {
                if (!r.ok) return r.json().then(function (d) { throw new Error((d.error && d.error.message) || 'Fork failed'); });
                return r.json();
            })
            .then(function (info) {
                bootstrap.Modal.getInstance(document.getElementById('codexForkModal')).hide();
                window.location.href = '/codex/' + encodeURIComponent(window.CODEX_WORKTREE) + '/' + encodeURIComponent(info.id);
            })
            .catch(function (err) { alert('Fork failed: ' + err.message); });
    };

    function openForkModal(index) {
        document.getElementById('codexForkIndex').value = String(index);
        document.getElementById('codexForkName').value = '';
        document.getElementById('codexForkSubtitle').textContent = 'Forks session at message #' + (index + 1) + '.';
        const modal = new bootstrap.Modal(document.getElementById('codexForkModal'));
        modal.show();
    }

    // ---------- Item rendering ----------
    function ensureTurn() {
        if (turnEl && turnEl.dataset.live === '1') return turnEl;
        const wrapper = document.createElement('div');
        wrapper.className = 'codex-message codex-message-assistant';
        wrapper.dataset.live = '1';

        const bubble = document.createElement('div');
        bubble.className = 'codex-bubble codex-bubble-assistant codex-bubble-live';
        wrapper.appendChild(bubble);

        messagesEl.appendChild(wrapper);
        turnEl = wrapper;
        itemEls = new Map();
        scrollToBottom();
        return turnEl;
    }

    function renderItem(item, completed) {
        if (!item || !item.id) return;
        const turn = ensureTurn();
        const bubble = turn.querySelector('.codex-bubble');

        const existing = itemEls.get(item.id);
        if (existing) {
            updateItem(existing, item, completed);
            return;
        }

        const node = renderItemNode(item, completed);
        if (!node) return;
        node.setAttribute('data-item-id', item.id);
        bubble.appendChild(node);

        itemEls.set(item.id, {
            kind: item.type,
            root: node,
            body: node.querySelector('.codex-item-body') || node,
            raw: item.text || '',
            parsedJSON: null,
        });
        scrollToBottom();
    }

    function renderItemNode(item, completed) {
        switch (item.type) {
            case 'agentMessage':
                return renderAgentMessageNode(item, completed);
            case 'reasoning':
                return renderReasoningNode(item, completed);
            case 'commandExecution':
                return renderCommandExecNode(item, completed);
            case 'fileChange':
                return renderFileChangeNode(item, completed);
            case 'plan':
                return renderPlanNode(item, completed);
            case 'webSearch':
                return renderToolNode(item, 'fa-magnifying-glass', 'Web search', item.text || '', null);
            case 'mcpToolCall':
                return renderToolNode(item, 'fa-plug', 'Tool', item.text || '', null);
            case 'userMessage':
                // We don't render echoed user messages; they're already shown
                // as the user bubble.
                return null;
            default:
                return renderToolNode(item, 'fa-circle', item.type, item.text || '', null);
        }
    }

    // Build a generic collapsed-by-default tool block matching Claude's
    // .codex-tool-use / .codex-tool-header / .codex-tool-body shape — these
    // class names map to existing rules in codex.css. Click the header to
    // toggle visibility of the body. `bodyEl` (optional) is appended inside
    // the body wrapper; pass null for a header-only display.
    function makeToolBlock(opts) {
        const root = document.createElement('div');
        root.className = 'codex-tool-use';

        const header = document.createElement('div');
        header.className = 'codex-tool-header';

        const icon = '<i class="fa-solid ' + (opts.icon || 'fa-wrench') + ' codex-tool-icon"></i>';
        const name = '<span class="codex-tool-name">' + escapeHtml(opts.name || 'Tool') + '</span>';
        const subtitle = '<span class="codex-tool-subtitle"></span>';
        const summary = '<span class="codex-tool-result-summary"></span>';
        const chevron = '<i class="fa-solid fa-chevron-right codex-tool-chevron"></i>';
        header.innerHTML = icon + name + subtitle + summary + chevron;

        const body = document.createElement('div');
        body.className = 'codex-tool-body';

        let hydrate = null;
        let hydrated = false;
        header.addEventListener('click', function () {
            if (!hydrated && hydrate) {
                hydrated = true;
                try { hydrate(); } catch (e) { console.error('codex: hydrate failed', e); }
            }
            header.classList.toggle('expanded');
            body.classList.toggle('show');
        });

        root.appendChild(header);
        root.appendChild(body);
        return {
            root: root,
            header: header,
            body: body,
            // Register a one-shot callback that runs the first time the user
            // expands the body. Use this for hljs work so collapsed bodies
            // stay off the cold-load critical path.
            onFirstExpand: function (fn) { hydrate = fn; },
        };
    }

    function setSubtitle(header, text) {
        const el = header.querySelector('.codex-tool-subtitle');
        if (el) el.textContent = text || '';
    }

    function setSummary(header, html) {
        const el = header.querySelector('.codex-tool-result-summary');
        if (el) el.innerHTML = html || '';
    }

    function renderToolNode(item, icon, name, subtitle, bodyEl) {
        const tool = makeToolBlock({ icon: icon, name: name });
        setSubtitle(tool.header, subtitle);
        if (bodyEl) tool.body.appendChild(bodyEl);
        return tool.root;
    }

    function renderAgentMessageNode(item, completed) {
        const div = document.createElement('div');
        div.className = 'codex-item codex-item-agent-message';
        const body = document.createElement('div');
        body.className = 'codex-item-body codex-text-content';
        body.innerHTML = renderMarkdown(item.text || '');
        addCopyButtonsToCode(body);
        div.appendChild(body);
        return div;
    }

    function renderReasoningNode(item, completed) {
        const div = document.createElement('div');
        div.className = 'codex-item codex-item-reasoning';

        const header = document.createElement('div');
        header.className = 'codex-item-header';
        header.innerHTML = '<i class="fa-solid fa-brain"></i> <span class="codex-item-label">Reasoning</span>';
        div.appendChild(header);

        const body = document.createElement('div');
        body.className = 'codex-item-body codex-reasoning-body';
        if (item.text) body.textContent = item.text;
        // Reasoning is collapsed by default; click the header to toggle.
        header.style.cursor = 'pointer';
        header.addEventListener('click', function () {
            div.classList.toggle('codex-collapsed');
        });
        div.classList.add('codex-collapsed');
        div.appendChild(body);
        return div;
    }

    function renderCommandExecNode(item, completed) {
        const tool = makeToolBlock({ icon: 'fa-terminal', name: 'Bash' });
        const cmdText = commandToString(item.command);

        // The header subtitle shows a one-line preview of the command.
        // The full command + output live inside the (collapsed) body so the
        // chat doesn't fill with terminal noise — match Claude's behavior.
        setSubtitle(tool.header, cmdText);
        setSummary(tool.header, commandSummaryHTML(item, completed));

        // Render the command in the body as `$ <command>` styled the same
        // way Claude renders bash input. Body is collapsed by default —
        // highlight on first expand instead of eagerly during history render.
        let cmdCodeEl = null;
        if (cmdText) {
            const cmdBlock = document.createElement('div');
            cmdBlock.className = 'codex-bash-block';
            const cmdLine = document.createElement('div');
            cmdLine.className = 'codex-bash-command';
            const prompt = document.createElement('span');
            prompt.className = 'codex-bash-prompt';
            prompt.textContent = '$ ';
            cmdCodeEl = document.createElement('code');
            cmdCodeEl.textContent = cmdText;
            cmdLine.appendChild(prompt);
            cmdLine.appendChild(cmdCodeEl);
            cmdBlock.appendChild(cmdLine);
            tool.body.appendChild(cmdBlock);
        }

        const out = document.createElement('pre');
        out.className = 'codex-bash-output';
        tool.body.appendChild(out);

        tool.onFirstExpand(function () {
            // textContent on multi-MB command output is the actual bottleneck
            // during cold-load history render — defer it to first expand.
            if (item.output) out.textContent = item.output;
            if (cmdCodeEl && cmdText && typeof hljs !== 'undefined') {
                try {
                    cmdCodeEl.innerHTML = hljs.highlight(cmdText, { language: 'bash' }).value;
                } catch (e) { /* leave as plain text */ }
            }
            if (item.output_truncated && item.id) {
                fetchFullItemContent(item.id, function (full) {
                    if (full && typeof full.output === 'string') {
                        out.textContent = full.output;
                    }
                });
            }
        });
        return tool.root;
    }

    function renderFileChangeNode(item, completed) {
        const change = item.changeType || 'change';
        const tool = makeToolBlock({ icon: 'fa-file-pen', name: change });
        setSubtitle(tool.header, item.path || '');

        if (item.diff) {
            const pre = document.createElement('pre');
            pre.className = 'codex-file-diff';
            const codeEl = document.createElement('code');
            codeEl.className = 'language-diff';
            pre.appendChild(codeEl);
            tool.body.appendChild(pre);
            tool.onFirstExpand(function () {
                // Defer both the escapeHtml of the diff body and the hljs
                // pass — both scale with diff size and were freezing the
                // main thread during cold-load history render.
                codeEl.innerHTML = escapeHtml(item.diff);
                if (typeof hljs !== 'undefined') {
                    try { hljs.highlightElement(codeEl); } catch (e) {}
                }
                if (item.diff_truncated && item.id) {
                    fetchFullItemContent(item.id, function (full) {
                        if (full && typeof full.diff === 'string') {
                            codeEl.innerHTML = escapeHtml(full.diff);
                            if (typeof hljs !== 'undefined') {
                                try { hljs.highlightElement(codeEl); } catch (e) {}
                            }
                        }
                    });
                }
            });
        }
        return tool.root;
    }

    // Fetch the full Output/Diff for a single item — used after first expand
    // for items that were truncated in the initial history dump.
    function fetchFullItemContent(itemId, cb) {
        if (!window.CODEX_SESSION || !itemId) return;
        const url = '/api/v1/codex/sessions/' +
            encodeURIComponent(window.CODEX_SESSION) +
            '/items/' + encodeURIComponent(itemId) + '/output';
        fetch(url, { credentials: 'same-origin' })
            .then(function (r) { return r.ok ? r.json() : null; })
            .then(function (data) { if (data) cb(data); })
            .catch(function (err) { console.warn('codex: fetch full content failed', err); });
    }

    function commandToString(cmd) {
        if (cmd == null) return '';
        if (Array.isArray(cmd)) {
            return cmd.map(function (a) { return /\s/.test(String(a)) ? JSON.stringify(a) : String(a); }).join(' ');
        }
        if (typeof cmd === 'object') {
            try { return JSON.stringify(cmd); } catch (e) { return String(cmd); }
        }
        return String(cmd);
    }

    function commandSummaryHTML(item, completed) {
        const status = item.status || (completed ? 'completed' : 'running');
        if (!completed && item.exitCode == null && status !== 'completed' && status !== 'failed') {
            return '<i class="fa-solid fa-spinner fa-spin"></i>';
        }
        if (item.exitCode != null && item.exitCode !== 0) {
            return '<i class="fa-solid fa-xmark text-danger"></i> exit ' + item.exitCode;
        }
        if (status === 'failed') {
            return '<i class="fa-solid fa-xmark text-danger"></i>';
        }
        return '<i class="fa-solid fa-check text-success"></i>';
    }

    function renderPlanNode(item, completed) {
        const tool = makeToolBlock({ icon: 'fa-list-check', name: 'Plan' });
        const md = document.createElement('div');
        md.className = 'codex-text-content';
        md.innerHTML = renderMarkdown(item.text || '');
        addCopyButtonsToCode(md);
        tool.body.appendChild(md);
        // Plans are usually worth showing — open by default.
        tool.header.classList.add('expanded');
        tool.body.classList.add('show');
        return tool.root;
    }

    function updateItem(entry, item, completed) {
        // Update in place rather than swapping the node out — that preserves
        // the user's expanded/collapsed state on tool blocks.
        if (entry.kind === 'agentMessage') {
            entry.raw = item.text || entry.raw || '';
            entry.body.innerHTML = renderMarkdown(entry.raw);
            addCopyButtonsToCode(entry.body);
        } else if (entry.kind === 'reasoning') {
            entry.raw = item.text || entry.raw || '';
            entry.body.textContent = entry.raw;
        } else if (entry.kind === 'commandExecution') {
            // Refresh subtitle (command may have only become known on
            // completion) and the status badge in the header.
            const header = entry.root.querySelector('.codex-tool-header');
            if (header) {
                const cmdText = commandToString(item.command);
                if (cmdText) setSubtitle(header, cmdText);
                setSummary(header, commandSummaryHTML(item, completed));
            }
            // Replace the body's <pre> content with the authoritative final
            // output. (Streaming deltas may have already populated it; the
            // completed event is the source of truth.)
            const out = entry.root.querySelector('.codex-bash-output');
            if (out && item.output != null) out.textContent = item.output;
        } else if (entry.kind === 'fileChange') {
            const header = entry.root.querySelector('.codex-tool-header');
            if (header) {
                if (item.changeType) {
                    const nameEl = header.querySelector('.codex-tool-name');
                    if (nameEl) nameEl.textContent = item.changeType;
                }
                if (item.path) setSubtitle(header, item.path);
            }
            if (item.diff) {
                const body = entry.root.querySelector('.codex-tool-body');
                if (body) {
                    body.innerHTML = '<pre class="codex-file-diff"><code class="language-diff">' + escapeHtml(item.diff) + '</code></pre>';
                    if (typeof hljs !== 'undefined') {
                        try { hljs.highlightElement(body.querySelector('code')); } catch (e) {}
                    }
                }
            }
        } else if (entry.kind === 'plan') {
            entry.raw = item.text || entry.raw || '';
            const md = entry.root.querySelector('.codex-text-content');
            if (md) {
                md.innerHTML = renderMarkdown(entry.raw);
                addCopyButtonsToCode(md);
            }
        } else if (item.text) {
            const body = entry.root.querySelector('.codex-tool-body') || entry.body;
            if (body) body.textContent = item.text;
        }
        scrollToBottom();
    }

    // ---------- Streaming deltas ----------
    function appendAgentDelta(itemId, delta) {
        ensureTurn();
        let entry = itemEls.get(itemId);
        if (!entry) {
            // The delta arrived before item/started — synthesize a placeholder.
            const synthetic = { id: itemId, type: 'agentMessage', text: '' };
            renderItem(synthetic, false);
            entry = itemEls.get(itemId);
            if (!entry) return;
        }
        if (entry.kind !== 'agentMessage') return;
        entry.raw = (entry.raw || '') + delta;
        entry.body.innerHTML = renderMarkdown(entry.raw);
        addCopyButtonsToCode(entry.body);
        scrollToBottom();
    }

    function appendCommandOutput(itemId, delta, stream) {
        ensureTurn();
        let entry = itemEls.get(itemId);
        if (!entry) {
            const synthetic = { id: itemId, type: 'commandExecution', output: '' };
            renderItem(synthetic, false);
            entry = itemEls.get(itemId);
            if (!entry) return;
        }
        if (entry.kind !== 'commandExecution') return;
        // Append to the (collapsed) output pre. The user can expand the
        // header to watch it live.
        const out = entry.root.querySelector('.codex-bash-output');
        if (out) {
            out.textContent = (out.textContent || '') + delta;
            // If the user has expanded the body, keep scrolled to bottom of
            // the inner pre so they see new lines as they arrive.
            const body = entry.root.querySelector('.codex-tool-body');
            if (body && body.classList.contains('show')) {
                out.scrollTop = out.scrollHeight;
            }
        }
        scrollToBottom();
    }

    // ---------- Turn lifecycle ----------
    function finishTurn() {
        if (turnEl) {
            turnEl.dataset.live = '0';
            turnEl.querySelector('.codex-bubble-live')?.classList.remove('codex-bubble-live');

            // Build plain text snapshot for cross-agent quote button
            const items = [];
            for (const [, entry] of itemEls) {
                items.push(itemRawForQuote(entry));
            }
            turnPlaintext.set(turnEl, items.filter(Boolean).join('\n\n'));

            // Attach per-message actions (copy / quote / fork) to the
            // wrapper now that the turn is complete. Use the next message
            // index — DOM order corresponds to history order.
            const wrapper = turnEl;
            const idx = wrapper.dataset.messageIndex
                ? parseInt(wrapper.dataset.messageIndex, 10)
                : messagesEl.children.length - 1;
            attachMessageActions(wrapper, function () {
                return turnPlaintext.get(wrapper) || '';
            }, 'assistant');
            attachForkButton(wrapper, idx);
        }
        turnEl = null;
        itemEls = new Map();
        hideWorking();
    }

    function itemRawForQuote(entry) {
        if (!entry) return '';
        switch (entry.kind) {
            case 'agentMessage':
                return entry.raw || '';
            case 'reasoning':
                return entry.raw ? '_(reasoning)_ ' + entry.raw : '';
            case 'commandExecution': {
                const header = entry.root.querySelector('.codex-command-text');
                const body = entry.root.querySelector('.codex-command-output');
                let s = '[command: ' + (header ? header.textContent : '?') + ']';
                if (body && body.textContent) s += '\n```\n' + body.textContent + '\n```';
                return s;
            }
            case 'fileChange': {
                const code = entry.root.querySelector('code');
                const path = entry.root.querySelector('.codex-item-header code');
                return '[file change: ' + (path ? path.textContent : '?') + ']' + (code ? '\n```diff\n' + code.textContent + '\n```' : '');
            }
            default:
                return '';
        }
    }

    // ---------- Working / status indicators ----------
    function showWorking() {
        if (workingEl) return;
        workingEl = document.createElement('div');
        workingEl.className = 'codex-working';
        workingEl.innerHTML = '<span class="codex-working-spinner"></span><span class="codex-working-label">Working…</span>';
        const turn = ensureTurn();
        turn.querySelector('.codex-bubble').appendChild(workingEl);
    }

    function hideWorking() {
        if (workingEl && workingEl.parentNode) {
            workingEl.parentNode.removeChild(workingEl);
        }
        workingEl = null;
    }

    function setGenerating(value) {
        generating = !!value;
        sendBtn.style.display = generating ? 'none' : '';
        cancelBtn.style.display = generating ? '' : 'none';
        inputEl.disabled = false;
    }

    function showError(message) {
        const div = document.createElement('div');
        div.className = 'codex-error';
        div.innerHTML = '<i class="fa-solid fa-triangle-exclamation"></i> ' + escapeHtml(message);
        messagesEl.appendChild(div);
        scrollToBottom();
    }

    function showEmptyState() {
        const div = document.createElement('div');
        div.className = 'codex-empty';
        div.textContent = 'Send a message to start a conversation with Codex.';
        messagesEl.appendChild(div);
    }

    // ---------- Context window display ----------
    function renderContextUsage() {
        const el = document.getElementById('codex-context-usage');
        if (!el || !tokenUsage) return;
        const total = tokenUsage.total_tokens || 0;
        if (total <= 0) {
            el.textContent = '';
            el.title = '';
            return;
        }
        const window = CODEX_CONTEXT_WINDOW_DEFAULT;
        const pct = Math.min(100, Math.round((total / window) * 100));
        el.textContent = formatTokens(total) + ' / ' + formatTokens(window) + ' (' + pct + '%)';
        el.title = [
            'input: ' + formatTokens(tokenUsage.input_tokens || 0),
            'cached: ' + formatTokens(tokenUsage.cached_input_tokens || 0),
            'output: ' + formatTokens(tokenUsage.output_tokens || 0),
            'reasoning: ' + formatTokens(tokenUsage.reasoning_output_tokens || 0),
        ].join(' · ');
    }

    function formatTokens(n) {
        if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
        if (n >= 1000) return (n / 1000).toFixed(1) + 'k';
        return String(n);
    }

    // ---------- Approval prompts ----------
    function showApprovalPrompt(ev) {
        const params = ev.params ? safeParseJSON(ev.params) : {};
        const method = ev.method || '';
        const isCommand = method.indexOf('commandExecution') !== -1 || method.indexOf('execCommand') !== -1;
        const reqID = ev.request_id;
        if (!reqID) return;

        ensureTurn();
        const turn = turnEl;
        const bubble = turn.querySelector('.codex-bubble');

        const wrap = document.createElement('div');
        wrap.className = 'codex-approval';
        wrap.dataset.requestId = reqID;

        const header = document.createElement('div');
        header.className = 'codex-approval-header';
        const title = isCommand ? 'Command needs approval' : 'File change needs approval';
        header.innerHTML = '<i class="fa-solid fa-shield-halved"></i> <strong>' + escapeHtml(title) + '</strong>';
        wrap.appendChild(wrap.appendChild(header));

        if (isCommand) {
            renderCommandApprovalDetail(wrap, params);
        } else {
            renderFileApprovalDetail(wrap, params);
        }

        if (params && params.reason) {
            const reason = document.createElement('div');
            reason.className = 'codex-approval-reason';
            reason.innerHTML = '<i class="fa-solid fa-circle-info"></i> <em>' + escapeHtml(params.reason) + '</em>';
            wrap.appendChild(reason);
        }

        const actions = document.createElement('div');
        actions.className = 'codex-approval-actions';

        function decideBtn(label, decision, cls, icon) {
            const b = document.createElement('button');
            b.className = 'btn btn-sm ' + cls;
            b.innerHTML = '<i class="fa-solid ' + icon + '"></i> ' + escapeHtml(label);
            b.addEventListener('click', function () {
                sendWS({ type: 'approval_response', request_id: reqID, decision: decision });
                const friendly = ({
                    accept: 'Approved',
                    acceptForSession: 'Approved for session',
                    decline: 'Declined',
                    cancel: 'Turn cancelled',
                })[decision] || decision;
                actions.innerHTML = '<span class="codex-approval-decided"><i class="fa-solid fa-check"></i> ' + escapeHtml(friendly) + '</span>';
            });
            return b;
        }
        actions.appendChild(decideBtn('Approve', 'accept', 'btn-success', 'fa-check'));
        actions.appendChild(decideBtn('Approve for session', 'acceptForSession', 'btn-outline-success', 'fa-circle-check'));
        actions.appendChild(decideBtn('Decline', 'decline', 'btn-outline-danger', 'fa-xmark'));
        actions.appendChild(decideBtn('Cancel turn', 'cancel', 'btn-outline-secondary', 'fa-stop'));
        wrap.appendChild(actions);

        bubble.appendChild(wrap);
        scrollToBottom();
    }

    function renderCommandApprovalDetail(wrap, params) {
        // Codex sends `command` either as a string or array of argv. Render as
        // a shell-like single line with cwd shown alongside.
        let cmd = params.command;
        if (Array.isArray(cmd)) {
            cmd = cmd.map(function (a) { return /\s/.test(a) ? JSON.stringify(a) : a; }).join(' ');
        } else if (cmd && typeof cmd === 'object') {
            try { cmd = JSON.stringify(cmd); } catch (e) { cmd = String(cmd); }
        }
        if (!cmd) return;

        const detail = document.createElement('div');
        detail.className = 'codex-approval-command';
        const meta = document.createElement('div');
        meta.className = 'codex-approval-meta';
        meta.innerHTML = '<i class="fa-solid fa-folder"></i> <code>' + escapeHtml(params.cwd || '?') + '</code>';
        detail.appendChild(meta);

        const pre = document.createElement('pre');
        pre.className = 'codex-approval-cmd';
        const code = document.createElement('code');
        code.className = 'language-bash';
        code.textContent = String(cmd);
        if (typeof hljs !== 'undefined') {
            try { hljs.highlightElement(code); } catch (e) {}
        }
        pre.appendChild(code);
        detail.appendChild(pre);
        wrap.appendChild(detail);
    }

    function renderFileApprovalDetail(wrap, params) {
        // v2 file change approval: just path + reason. v1 (applyPatchApproval)
        // carries fileChanges as a map of path → { add | delete | update }.
        const detail = document.createElement('div');
        detail.className = 'codex-approval-file';

        if (params.path) {
            const p = document.createElement('div');
            p.className = 'codex-approval-meta';
            p.innerHTML = '<i class="fa-solid fa-file-pen"></i> <code>' + escapeHtml(params.path) + '</code>';
            detail.appendChild(p);
        }

        if (params.fileChanges && typeof params.fileChanges === 'object') {
            for (const path in params.fileChanges) {
                const change = params.fileChanges[path] || {};
                const meta = document.createElement('div');
                meta.className = 'codex-approval-meta';
                let kind = 'changed';
                if (change.add || change.type === 'add') kind = 'create';
                else if (change.delete || change.type === 'delete') kind = 'delete';
                else if (change.update || change.type === 'update') kind = 'update';
                meta.innerHTML = '<i class="fa-solid fa-file-pen"></i> <span class="codex-approval-kind">' + escapeHtml(kind) + '</span> <code>' + escapeHtml(path) + '</code>';
                detail.appendChild(meta);

                const diff = (change.update && change.update.unified_diff)
                    || (change.update && change.update.diff)
                    || change.diff || '';
                if (diff) {
                    const pre = document.createElement('pre');
                    pre.className = 'codex-approval-diff';
                    pre.innerHTML = '<code class="language-diff">' + escapeHtml(diff) + '</code>';
                    if (typeof hljs !== 'undefined') {
                        try { hljs.highlightElement(pre.querySelector('code')); } catch (e) {}
                    }
                    detail.appendChild(pre);
                }
            }
        }
        wrap.appendChild(detail);
    }

    function safeParseJSON(v) {
        if (typeof v === 'object') return v;
        try { return JSON.parse(v); } catch (e) { return {}; }
    }

    // ---------- Send / cancel / input ----------
    window.codexSend = function () {
        const text = inputEl.value.trim();
        if (!text || generating) return;
        inputEl.value = '';
        sessionStorage.removeItem('codex-draft-' + (window.CODEX_SESSION || ''));
        autoResizeInput();

        // Optimistically render the user bubble — server confirms via history
        // on next reconnect anyway.
        const wrapper = document.createElement('div');
        wrapper.className = 'codex-message codex-message-user';
        const bubble = document.createElement('div');
        bubble.className = 'codex-bubble codex-bubble-user';
        bubble.textContent = text;
        wrapper.appendChild(bubble);
        attachMessageActions(wrapper, function () { return text; }, 'user');
        messagesEl.appendChild(wrapper);

        sendWS({ type: 'message', content: text });
        setGenerating(true);
        showWorking();
        scrollToBottom();
    };

    window.codexCancel = function () {
        if (!generating) return;
        sendWS({ type: 'cancel' });
        setGenerating(false);
        hideWorking();
    };

    function autoResizeInput() {
        inputEl.style.height = 'auto';
        const max = 240;
        inputEl.style.height = Math.min(inputEl.scrollHeight, max) + 'px';
    }

    // Persist draft input across navigation. Mirrors claude.js: sessionStorage
    // (per-tab) so different tabs don't clobber each other; cleared on send.
    const draftKey = 'codex-draft-' + (window.CODEX_SESSION || '');
    const savedDraft = sessionStorage.getItem(draftKey);
    if (savedDraft) {
        inputEl.value = savedDraft;
        autoResizeInput();
    }
    inputEl.addEventListener('input', function () {
        autoResizeInput();
        sessionStorage.setItem(draftKey, inputEl.value);
    });

    inputEl.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            window.codexSend();
        } else if (e.key === 'Escape' && generating) {
            e.preventDefault();
            window.codexCancel();
        }
    });

    // ---------- Init ----------
    connect();
})();
