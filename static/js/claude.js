// Claude Code Chat Interface
(function() {
    'use strict';

    const messagesEl = document.getElementById('claude-messages');
    const inputEl = document.getElementById('claude-input');
    const sendBtn = document.getElementById('claude-send-btn');
    const cancelBtn = document.getElementById('claude-cancel-btn');
    const resetBtn = document.getElementById('claude-reset-btn');

    let ws = null;
    let reconnectTimer = null;
    let generating = false;
    let currentBubble = null;     // Current assistant bubble element
    let currentTextEl = null;     // Current text span for streaming text
    let accumulatedText = '';     // Accumulated raw text for current assistant turn
    let streamingToolInput = '';  // Accumulated JSON for tool_use input from stream deltas
    let usingStreamEvents = false; // True once stream_event events are seen (skip assistant events)
    let lastToolName = '';        // Name of the last tool_use block, for working indicator
    let lastToolInput = null;     // Parsed input of the last tool_use block
    let streamingPlanMode = false; // True when streaming a plan mode tool block
    let slashCommands = [];       // Available slash commands from system init
    let inputTokens = 0;          // Most recent input token count for context usage
    const contextWindow = 200000; // Claude context window size
    let tokenBreakdown = { base: 0, cacheCreate: 0, cacheRead: 0, total: 0 };

    function isPlanModeTool(name) {
        return name === 'EnterPlanMode' || name === 'ExitPlanMode';
    }

    // Check if a file path looks like a plan file (markdown).
    function isPlanFilePath(filePath) {
        if (!filePath) return false;
        return filePath.match(/\.md$/i) !== null;
    }

    function appendPlanModeBanner(bubble, name, input, toolId, planContent, interactive) {
        var isEnter = (name === 'EnterPlanMode');
        var icon = isEnter ? 'fa-clipboard-list' : 'fa-clipboard-check';
        var label = isEnter ? 'Entering plan mode' : 'Plan ready for review';

        var banner = document.createElement('div');
        banner.className = 'claude-plan-mode';
        if (interactive) banner.classList.add('claude-plan-mode-interactive');
        banner.dataset.planMode = name;
        if (toolId) banner.dataset.toolId = toolId;
        banner.innerHTML =
            '<i class="fa-solid ' + icon + '"></i>' +
            '<span class="claude-plan-mode-label">' + escapeHtml(label) + '</span>';

        // For ExitPlanMode, add plan content area first (before permissions)
        if (!isEnter) {
            var contentDiv = document.createElement('div');
            contentDiv.className = 'claude-plan-mode-content';
            if (planContent) {
                contentDiv.innerHTML = marked.parse(planContent);
                addCopyButtons(contentDiv);
            }
            banner.appendChild(contentDiv);
        }

        // For ExitPlanMode, show allowed prompts if present
        if (!isEnter && input && input.allowedPrompts && input.allowedPrompts.length > 0) {
            var permsDiv = document.createElement('div');
            permsDiv.className = 'claude-plan-mode-permissions';
            var permsLabel = document.createElement('div');
            permsLabel.className = 'claude-plan-permissions-label';
            permsLabel.textContent = 'Requested permissions:';
            permsDiv.appendChild(permsLabel);
            var ul = document.createElement('ul');
            for (var i = 0; i < input.allowedPrompts.length; i++) {
                var li = document.createElement('li');
                li.textContent = input.allowedPrompts[i].prompt || '';
                ul.appendChild(li);
            }
            permsDiv.appendChild(ul);
            banner.appendChild(permsDiv);
        }

        // For ExitPlanMode with no tool_result, add Approve/Reject buttons
        if (!isEnter && interactive) {
            var actions = document.createElement('div');
            actions.className = 'claude-plan-mode-actions';

            var approveBtn = document.createElement('button');
            approveBtn.className = 'btn btn-success btn-sm';
            approveBtn.textContent = 'Approve Plan';
            approveBtn.addEventListener('click', function() {
                // Send approval as a user message
                var text = 'I approve this plan. Please proceed with the implementation.';
                var wrapper = document.createElement('div');
                wrapper.className = 'claude-message claude-message-user';
                var userBubble = document.createElement('div');
                userBubble.className = 'claude-bubble claude-bubble-user';
                userBubble.textContent = text;
                wrapper.appendChild(userBubble);
                messagesEl.appendChild(wrapper);
                scrollToBottom();

                sendWS({ type: 'message', content: text });
                setGenerating(true);
                showWorkingIndicator();

                // Mark as handled
                actions.innerHTML = '<span class="claude-permission-allowed"><i class="fa-solid fa-check"></i> Approved</span>';
                banner.classList.remove('claude-plan-mode-interactive');
            });

            var rejectBtn = document.createElement('button');
            rejectBtn.className = 'btn btn-outline-danger btn-sm';
            rejectBtn.textContent = 'Reject';
            rejectBtn.addEventListener('click', function() {
                var text = 'I reject this plan. Please reconsider the approach.';
                var wrapper = document.createElement('div');
                wrapper.className = 'claude-message claude-message-user';
                var userBubble = document.createElement('div');
                userBubble.className = 'claude-bubble claude-bubble-user';
                userBubble.textContent = text;
                wrapper.appendChild(userBubble);
                messagesEl.appendChild(wrapper);
                scrollToBottom();

                sendWS({ type: 'message', content: text });
                setGenerating(true);
                showWorkingIndicator();

                actions.innerHTML = '<span class="claude-permission-denied"><i class="fa-solid fa-xmark"></i> Rejected</span>';
                banner.classList.remove('claude-plan-mode-interactive');
            });

            actions.appendChild(approveBtn);
            actions.appendChild(rejectBtn);
            banner.appendChild(actions);
        }

        bubble.appendChild(banner);

        // Create a new text element after the banner for subsequent text
        var newTextEl = document.createElement('div');
        newTextEl.className = 'claude-text-content';
        bubble.appendChild(newTextEl);
        currentTextEl = newTextEl;
        accumulatedText = '';

        scrollToBottom();
    }

    // Configure marked for rendering markdown
    marked.setOptions({
        highlight: function(code, lang) {
            if (lang && hljs.getLanguage(lang)) {
                return hljs.highlight(code, { language: lang }).value;
            }
            return hljs.highlightAuto(code).value;
        },
        breaks: false,
        gfm: true
    });

    // Theme switching for highlight.js
    window.addEventListener('trellis-theme-change', function(e) {
        const theme = e.detail.theme;
        document.getElementById('hljs-light').disabled = (theme === 'dark');
        document.getElementById('hljs-dark').disabled = (theme !== 'dark');
    });

    // --- Syntax Highlighting Helpers ---

    function getLanguageFromPath(filePath) {
        if (!filePath) return '';
        var basename = filePath.split('/').pop() || '';
        // Check basename patterns
        var basenameMap = {
            'Dockerfile': 'dockerfile', 'Makefile': 'makefile', 'Jenkinsfile': 'groovy',
            'Vagrantfile': 'ruby', '.gitignore': 'plaintext', '.env': 'bash',
            'CMakeLists.txt': 'cmake'
        };
        if (basenameMap[basename]) return basenameMap[basename];

        var ext = basename.indexOf('.') !== -1 ? basename.split('.').pop().toLowerCase() : '';
        if (!ext) return '';

        var extMap = {
            'js': 'javascript', 'jsx': 'javascript', 'mjs': 'javascript', 'cjs': 'javascript',
            'ts': 'typescript', 'tsx': 'typescript',
            'py': 'python', 'pyw': 'python',
            'rb': 'ruby', 'erb': 'erb',
            'go': 'go',
            'rs': 'rust',
            'java': 'java',
            'kt': 'kotlin', 'kts': 'kotlin',
            'c': 'c', 'h': 'c',
            'cpp': 'cpp', 'cc': 'cpp', 'cxx': 'cpp', 'hpp': 'cpp', 'hxx': 'cpp',
            'cs': 'csharp',
            'swift': 'swift',
            'php': 'php',
            'r': 'r',
            'scala': 'scala',
            'lua': 'lua',
            'pl': 'perl', 'pm': 'perl',
            'sh': 'bash', 'bash': 'bash', 'zsh': 'bash',
            'ps1': 'powershell',
            'html': 'xml', 'htm': 'xml', 'xhtml': 'xml',
            'xml': 'xml', 'svg': 'xml', 'xsl': 'xml',
            'css': 'css', 'scss': 'scss', 'less': 'less', 'sass': 'scss',
            'json': 'json', 'jsonc': 'json',
            'yaml': 'yaml', 'yml': 'yaml',
            'toml': 'ini', 'ini': 'ini', 'cfg': 'ini', 'conf': 'ini',
            'md': 'markdown', 'markdown': 'markdown',
            'sql': 'sql',
            'graphql': 'graphql', 'gql': 'graphql',
            'proto': 'protobuf',
            'tf': 'hcl', 'hcl': 'hcl',
            'dockerfile': 'dockerfile',
            'makefile': 'makefile',
            'cmake': 'cmake',
            'diff': 'diff', 'patch': 'diff',
            'vim': 'vim', 'vimrc': 'vim',
            'el': 'lisp', 'lisp': 'lisp', 'clj': 'clojure',
            'hs': 'haskell',
            'erl': 'erlang', 'ex': 'elixir', 'exs': 'elixir',
            'dart': 'dart',
            'groovy': 'groovy', 'gradle': 'groovy',
            'hjson': 'json'
        };
        return extMap[ext] || '';
    }

    function highlightContent(content, language) {
        if (!language || !hljs.getLanguage(language)) return null;
        try {
            return hljs.highlight(content, { language: language }).value;
        } catch(e) {
            return null;
        }
    }

    function setPreContent(pre, content, lang) {
        if (lang) {
            var highlighted = highlightContent(content, lang);
            if (highlighted) {
                var code = document.createElement('code');
                code.className = 'hljs language-' + lang;
                code.innerHTML = highlighted;
                pre.innerHTML = '';
                pre.appendChild(code);
                return;
            }
        }
        pre.textContent = content;
    }

    function renderToolResultContent(pre, content, toolName, filePath) {
        var truncateThreshold = 3000;
        var lang = (toolName === 'Read') ? getLanguageFromPath(filePath) : '';

        if (content.length <= truncateThreshold) {
            setPreContent(pre, content, lang);
            return;
        }

        // Show truncated by default
        var truncated = content.substring(0, truncateThreshold);
        setPreContent(pre, truncated, lang);

        var toggle = document.createElement('a');
        toggle.className = 'claude-truncation-toggle';
        toggle.textContent = 'Show full output (' + content.length.toLocaleString() + ' chars)';
        toggle.href = '#';
        var expanded = false;
        toggle.addEventListener('click', function(e) {
            e.preventDefault();
            e.stopPropagation();
            expanded = !expanded;
            if (expanded) {
                setPreContent(pre, content, lang);
                toggle.textContent = 'Show less';
                pre.parentNode.insertBefore(toggle, pre.nextSibling);
            } else {
                setPreContent(pre, truncated, lang);
                toggle.textContent = 'Show full output (' + content.length.toLocaleString() + ' chars)';
                pre.parentNode.insertBefore(toggle, pre.nextSibling);
            }
        });
        pre.parentNode.insertBefore(toggle, pre.nextSibling);
    }

    // --- WebSocket Connection ---

    function connect() {
        if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
            return;
        }
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        let url;
        if (typeof CLAUDE_SESSION !== 'undefined' && CLAUDE_SESSION) {
            url = proto + '//' + location.host + '/api/v1/claude/sessions/' + encodeURIComponent(CLAUDE_SESSION) + '/ws';
        } else {
            url = proto + '//' + location.host + '/api/v1/claude/' + encodeURIComponent(CLAUDE_WORKTREE) + '/ws';
        }
        ws = new WebSocket(url);

        ws.onopen = function() {
            clearTimeout(reconnectTimer);
        };

        ws.onmessage = function(e) {
            if (typeof e.data !== 'string' || e.data.length === 0) {
                console.warn('Ignoring WS frame: typeof=', typeof e.data,
                    'length=', (e.data && e.data.length) || 0,
                    'ctor=', e.data && e.data.constructor && e.data.constructor.name);
                return;
            }
            try {
                const msg = JSON.parse(e.data);
                handleServerMessage(msg);
            } catch (err) {
                console.error('Failed to parse WS message:', err, 'data:', e.data);
            }
        };

        ws.onclose = function() {
            ws = null;
            reconnectTimer = setTimeout(connect, 3000);
        };

        ws.onerror = function() {
            // onclose will fire after onerror
        };
    }

    function sendWS(msg) {
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify(msg));
        }
    }

    // --- Message Handling ---

    function handleServerMessage(msg) {
        switch (msg.type) {
            case 'history':
                renderHistory(msg.messages || [], !!msg.generating);
                if (msg.generating) {
                    setGenerating(true);
                } else {
                    requestAnimationFrame(function() { inputEl.focus(); });
                }
                if (msg.input_tokens) {
                    inputTokens = msg.input_tokens;
                    if (msg.input_tokens_base || msg.cache_creation_input_tokens || msg.cache_read_input_tokens) {
                        tokenBreakdown = {
                            base: msg.input_tokens_base || 0,
                            cacheCreate: msg.cache_creation_input_tokens || 0,
                            cacheRead: msg.cache_read_input_tokens || 0,
                            total: msg.input_tokens
                        };
                    }
                    updateContextUsage();
                }
                if (msg.slash_commands || msg.skills) {
                    slashCommands = (msg.slash_commands || []).concat(msg.skills || []);
                    slashCommands = slashCommands.filter(function(v, i, a) { return a.indexOf(v) === i; });
                    slashCommands.sort();
                }
                break;
            case 'stream':
                handleStreamEvent(msg.event);
                break;
            case 'done':
                finishAssistantTurn();
                setGenerating(false);
                break;
            case 'error':
                showError(msg.message || 'Unknown error');
                setGenerating(false);
                break;
            case 'status':
                setGenerating(msg.generating || false);
                break;
        }
    }

    function handleStreamEvent(event) {
        if (!event) return;

        switch (event.type) {
            case 'assistant':
                // Always extract token usage from assistant events
                if (event.message && event.message.usage) {
                    updateTokenBreakdown(event.message.usage);
                }
                // Skip content rendering if stream_event is handling it (avoids double rendering)
                if (usingStreamEvents) break;
                if (event.message && event.message.content) {
                    for (const block of event.message.content) {
                        handleContentBlock(block);
                    }
                }
                break;
            case 'result':
                // Show permission denials if any
                if (event.permission_denials && event.permission_denials.length > 0) {
                    showPermissionDenials(event.permission_denials);
                }
                // If the result has text that wasn't already streamed, show it
                if (event.result && !accumulatedText) {
                    ensureAssistantBubble();
                    accumulatedText = event.result;
                    renderAssistantMarkdown();
                }
                break;
            case 'system':
                if (event.subtype === 'init') {
                    slashCommands = (event.slash_commands || []).concat(event.skills || []);
                    // Deduplicate
                    slashCommands = slashCommands.filter(function(v, i, a) { return a.indexOf(v) === i; });
                    slashCommands.sort();
                }
                if (event.subtype === 'status') {
                    if (event.status === 'compacting') {
                        // Reset in-progress bubble state; the completion marker
                        // is inserted when compact_boundary arrives.
                        currentBubble = null;
                        currentTextEl = null;
                        accumulatedText = '';
                    }
                    if (event.status) {
                        var statusLabels = {
                            'compacting': 'Compacting conversation...',
                            'thinking': 'Thinking...',
                            'processing': 'Processing...'
                        };
                        showWorkingIndicator(null, null, statusLabels[event.status] || event.status);
                    } else {
                        removeWorkingIndicator();
                    }
                }
                if (event.subtype === 'compact_boundary') {
                    insertCompactionMarker(event.compact_metadata);
                }
                break;
            case 'control_request':
                if (event.request_id && event.request) {
                    showPermissionPrompt(event.request_id, event.request);
                }
                break;
            case 'stream_event':
                if (event.event) {
                    handleInnerStreamEvent(event.event);
                }
                break;
            case 'diff_enrichment':
                // Live diff enrichment for Edit tool blocks
                if (event.message) {
                    var diffData = event.message;
                    if (diffData.tool_use_id && diffData.diff_html) {
                        applyDiffEnrichment(diffData.tool_use_id, diffData.diff_html);
                    }
                }
                break;
            case 'user':
                // Echoed user messages contain tool_result blocks from tool execution
                if (event.message && event.message.content) {
                    for (const block of event.message.content) {
                        if (block.type === 'tool_result') {
                            fillToolResult(block);
                        }
                    }
                }
                break;
            case 'rate_limit_event':
                // Claude CLI emits these as you approach rate limits; not surfaced in UI.
                break;
            default:
                console.log('claude: unhandled event type:', event.type, event);
                break;
        }
    }

    function handleContentBlock(block) {
        if (!block) return;

        switch (block.type) {
            case 'text':
                ensureAssistantBubble();
                accumulatedText += block.text;
                renderAssistantMarkdown();
                break;
            case 'tool_use':
                ensureAssistantBubble();
                appendToolUse(block);
                break;
            case 'tool_result':
                fillToolResult(block);
                break;
        }
    }

    function handleInnerStreamEvent(inner) {
        usingStreamEvents = true;
        switch (inner.type) {
            case 'message_start':
                if (inner.message && inner.message.usage) {
                    updateTokenBreakdown(inner.message.usage);
                }
                break;
            case 'content_block_start':
                removeWorkingIndicator();
                if (inner.content_block) {
                    if (inner.content_block.type === 'text') {
                        ensureAssistantBubble();
                        if (currentBubble && !currentTextEl) {
                            var newTextEl = document.createElement('div');
                            newTextEl.className = 'claude-text-content';
                            currentBubble.appendChild(newTextEl);
                            currentTextEl = newTextEl;
                            accumulatedText = '';
                        }
                    } else if (inner.content_block.type === 'tool_use') {
                        ensureAssistantBubble();
                        lastToolName = inner.content_block.name || 'Tool';
                        if (isPlanModeTool(inner.content_block.name)) {
                            // Render banner immediately; input will be updated on content_block_stop
                            appendPlanModeBanner(currentBubble, inner.content_block.name, {}, inner.content_block.id);
                            streamingToolInput = '';
                            streamingPlanMode = true;
                        } else {
                            appendToolUse({
                                type: 'tool_use',
                                id: inner.content_block.id,
                                name: inner.content_block.name,
                                input: {}
                            });
                            streamingToolInput = '';
                            streamingPlanMode = false;
                        }
                    }
                }
                break;
            case 'content_block_delta':
                if (inner.delta) {
                    if (inner.delta.type === 'text_delta' && inner.delta.text) {
                        ensureAssistantBubble();
                        accumulatedText += inner.delta.text;
                        renderAssistantMarkdown();
                    } else if (inner.delta.type === 'input_json_delta' && inner.delta.partial_json !== undefined) {
                        streamingToolInput += inner.delta.partial_json;
                    }
                }
                break;
            case 'content_block_stop':
                // Update tool block input and header subtitle if we accumulated JSON
                if (streamingToolInput && currentBubble) {
                    try {
                        var input = JSON.parse(streamingToolInput);
                        lastToolInput = input;
                        if (streamingPlanMode) {
                            var banners = currentBubble.querySelectorAll('.claude-plan-mode');
                            var lastBanner = banners.length > 0 ? banners[banners.length - 1] : null;
                            if (lastBanner) {
                                // Update banner with permissions for ExitPlanMode
                                if (input.allowedPrompts && input.allowedPrompts.length > 0) {
                                    var permsDiv = document.createElement('div');
                                    permsDiv.className = 'claude-plan-mode-permissions';
                                    var ul = document.createElement('ul');
                                    for (var i = 0; i < input.allowedPrompts.length; i++) {
                                        var li = document.createElement('li');
                                        li.textContent = input.allowedPrompts[i].prompt || '';
                                        ul.appendChild(li);
                                    }
                                    permsDiv.appendChild(ul);
                                    lastBanner.appendChild(permsDiv);
                                }
                                // For ExitPlanMode, find plan content from preceding Write tool
                                // Search all messages (the Write may be in a previous turn/bubble)
                                if (lastToolName === 'ExitPlanMode') {
                                    var contentDiv = lastBanner.querySelector('.claude-plan-mode-content');
                                    if (contentDiv && !contentDiv.innerHTML) {
                                        var toolDivs = messagesEl.querySelectorAll('.claude-tool-use');
                                        for (var ti = toolDivs.length - 1; ti >= 0; ti--) {
                                            var nameEl = toolDivs[ti].querySelector('.claude-tool-name');
                                            var subEl = toolDivs[ti].querySelector('.claude-tool-subtitle');
                                            if (nameEl && nameEl.textContent === 'Write' &&
                                                subEl && isPlanFilePath(subEl.textContent)) {
                                                try {
                                                    var pre = toolDivs[ti].querySelector('.claude-tool-body pre');
                                                    var writeInput = JSON.parse(pre.textContent);
                                                    if (writeInput.content) {
                                                        contentDiv.innerHTML = marked.parse(writeInput.content);
                                                        addCopyButtons(contentDiv);
                                                        // Hide the Write tool block since plan is shown in banner
                                                        toolDivs[ti].style.display = 'none';
                                                    }
                                                } catch(e) {}
                                                break;
                                            }
                                        }
                                    }
                                }
                            }
                            streamingPlanMode = false;
                        } else {
                            var toolDivs = currentBubble.querySelectorAll('.claude-tool-use');
                            if (toolDivs.length > 0) {
                                var lastTool = toolDivs[toolDivs.length - 1];

                                // AskUserQuestion: remove the tool placeholder; the control_request
                                // will render the interactive prompt immediately after.
                                if (lastToolName === 'AskUserQuestion' && input.questions) {
                                    lastTool.remove();
                                // TodoWrite: replace the tool block with a rendered checklist
                                } else if (lastToolName === 'TodoWrite' && input.todos) {
                                    lastTool.remove();
                                    renderTodoList(currentBubble, input.todos);
                                } else {
                                    var inputPre = lastTool.querySelector('.claude-tool-body pre');
                                    if (inputPre) {
                                        inputPre.textContent = JSON.stringify(input, null, 2);
                                    }
                                    // Update header with descriptive subtitle
                                    var subtitle = getToolSubtitle(lastToolName, input);
                                    var subtitleEl = lastTool.querySelector('.claude-tool-subtitle');
                                    if (subtitleEl && subtitle) {
                                        subtitleEl.textContent = subtitle;
                                        if ((lastToolName === 'Read' || lastToolName === 'Write' || lastToolName === 'Edit') && input.file_path) {
                                            makeCopyableSubtitle(subtitleEl, input.file_path);
                                        }
                                    }
                                    // Bash: render terminal-styled command
                                    if (lastToolName === 'Bash' && input.command) {
                                        var oldBash = lastTool.querySelector('.claude-bash-block');
                                        if (oldBash) oldBash.remove();
                                        var tbody = lastTool.querySelector('.claude-tool-body');
                                        if (tbody) {
                                            renderBashInput(tbody, input);
                                            if (inputPre) inputPre.style.display = 'none';
                                        }
                                    }
                                    // Task: render subagent display
                                    if (lastToolName === 'Task' && (input.prompt || input.description)) {
                                        var oldTask = lastTool.querySelector('.claude-subagent');
                                        if (oldTask) oldTask.remove();
                                        var tbody2 = lastTool.querySelector('.claude-tool-body');
                                        if (tbody2) {
                                            renderTaskInput(tbody2, input);
                                            if (inputPre) inputPre.style.display = 'none';
                                        }
                                    }
                                }
                            }
                        }
                    } catch(e) {}
                    streamingToolInput = '';
                }
                break;
            case 'message_delta':
                // Tool is about to execute — show working indicator
                if (inner.delta && inner.delta.stop_reason === 'tool_use') {
                    if (isPlanModeTool(lastToolName)) {
                        var modeLabel = lastToolName === 'EnterPlanMode' ?
                            'Entering plan mode...' : 'Waiting for plan approval...';
                        showWorkingIndicator(null, null, modeLabel);
                    } else {
                        showWorkingIndicator(lastToolName, lastToolInput);
                    }
                }
                break;
        }
    }

    // --- Copyable Subtitles ---

    function makeCopyableSubtitle(subtitleEl, path) {
        if (!subtitleEl || !path) return;
        subtitleEl.classList.add('claude-tool-subtitle-copyable');
        subtitleEl.dataset.copyPath = path;
        subtitleEl.addEventListener('click', function(e) {
            e.stopPropagation();
            (window.trellisCopyToClipboard || navigator.clipboard.writeText.bind(navigator.clipboard))(path).then(function() {
                subtitleEl.classList.add('claude-tool-subtitle-copied');
                var original = subtitleEl.textContent;
                subtitleEl.textContent = 'Copied!';
                setTimeout(function() {
                    subtitleEl.textContent = original;
                    subtitleEl.classList.remove('claude-tool-subtitle-copied');
                }, 1200);
            });
        });
    }

    // --- Diff Enrichment ---

    function applyDiffEnrichment(toolUseId, diffHtml) {
        var escaped = CSS.escape(toolUseId);
        var toolDiv = messagesEl.querySelector('.claude-tool-use[data-tool-id="' + escaped + '"]');
        if (!toolDiv) return;

        var body = toolDiv.querySelector('.claude-tool-body');
        if (!body) return;

        // Insert diff before the existing pre (raw JSON input)
        var inputPre = body.querySelector('pre');
        var wrapper = document.createElement('div');
        wrapper.className = 'claude-diff-wrapper';
        wrapper.innerHTML = diffHtml;
        if (inputPre) {
            body.insertBefore(wrapper, inputPre);
            inputPre.style.display = 'none';
        } else {
            body.appendChild(wrapper);
        }
    }

    // --- Working Indicator ---

    function formatGrepSubtitle(input) {
        var pat = input.pattern || '';
        if (pat.length > 50) pat = pat.substring(0, 50) + '...';
        var parts = ['pattern: "' + pat + '"'];
        if (input.path) parts.push('path: "' + input.path + '"');
        if (input.glob) parts.push('glob: "' + input.glob + '"');
        return 'Search(' + parts.join(', ') + ')';
    }

    function formatGrepResultSummary(content) {
        if (!content) return '';
        var lines = content.split('\n').filter(function(l) { return l.length > 0; });
        if (lines.length === 0) return 'No matches';
        // files_with_matches mode: "Found N files"
        if (lines[0].match(/^Found \d+ files?$/)) return lines[0];
        // count mode: already has counts
        if (lines[0].match(/^\d+$/)) return 'Found ' + lines[0] + ' matches';
        // content mode: count non-empty lines
        return 'Found ' + lines.length + ' lines';
    }

    function getToolSubtitle(name, input) {
        if (!name || !input) return '';
        switch (name) {
            case 'Read':
                return input.file_path || '';
            case 'Write':
                return input.file_path || '';
            case 'Edit':
                return input.file_path || '';
            case 'Bash':
                if (input.description) return input.description;
                var cmd = input.command || '';
                if (cmd.length > 80) cmd = cmd.substring(0, 80) + '...';
                return cmd;
            case 'Glob':
                return input.pattern || '';
            case 'Grep':
                return formatGrepSubtitle(input);
            case 'Task':
                return input.description || '';
            case 'WebFetch':
                return input.url || '';
            case 'WebSearch':
                return input.query || '';
            case 'AskUserQuestion':
                if (input.questions && input.questions.length > 0) {
                    return input.questions[0].header || input.questions[0].question || '';
                }
                return 'Asking question';
            case 'TodoWrite':
                if (input.todos) return input.todos.length + ' items';
                return '';
            case 'EnterPlanMode':
                return 'Planning implementation';
            case 'ExitPlanMode':
                return 'Plan ready for review';
            default:
                return '';
        }
    }

    function describeToolRun(name, input) {
        if (!input) return 'Running ' + name + '...';
        switch (name) {
            case 'Read':
                return 'Reading ' + (input.file_path || '');
            case 'Write':
                return 'Writing ' + (input.file_path || '');
            case 'Edit':
                return 'Editing ' + (input.file_path || '');
            case 'Bash':
                var cmd = input.command || '';
                if (cmd.length > 80) cmd = cmd.substring(0, 80) + '...';
                return 'Running: ' + cmd;
            case 'Glob':
                return 'Searching for ' + (input.pattern || '');
            case 'Grep':
                return 'Searching: ' + formatGrepSubtitle(input);
            case 'Task':
                return input.description || 'Running task...';
            case 'WebFetch':
                return 'Fetching ' + (input.url || '');
            case 'WebSearch':
                return 'Searching: ' + (input.query || '');
            case 'AskUserQuestion':
                return 'Waiting for answer...';
            case 'TodoWrite':
                return 'Updating tasks...';
            case 'EnterPlanMode':
                return 'Entering plan mode...';
            case 'ExitPlanMode':
                return 'Submitting plan...';
            default:
                return 'Running ' + name + '...';
        }
    }

    function showWorkingIndicator(toolName, toolInput, explicitLabel) {
        removeWorkingIndicator();
        var label = explicitLabel || 'Working...';
        if (!explicitLabel && toolName) {
            label = describeToolRun(toolName, toolInput);
        }
        var indicator = document.createElement('div');
        indicator.className = 'claude-working';
        indicator.id = 'claude-working';
        indicator.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> <span>' + escapeHtml(label) + '</span>';
        messagesEl.appendChild(indicator);
        scrollToBottom();
    }

    function removeWorkingIndicator() {
        var indicator = document.getElementById('claude-working');
        if (indicator) indicator.remove();
    }

    function insertCompactionMarker(metadata) {
        var marker = document.createElement('div');
        marker.className = 'claude-compaction-marker';
        var detail = '';
        if (metadata && typeof metadata === 'object') {
            var pre = metadata.pre_tokens;
            var post = metadata.post_tokens;
            if (typeof pre === 'number' && typeof post === 'number') {
                detail = ' (' + pre.toLocaleString() + ' → ' + post.toLocaleString() + ' tokens)';
            }
        }
        var label = document.createElement('span');
        label.className = 'claude-compaction-label';
        label.innerHTML = '<i class="fa-solid fa-compress"></i> Context compacted';
        if (detail) {
            var extra = document.createElement('span');
            extra.className = 'claude-compaction-detail';
            extra.textContent = detail;
            label.appendChild(extra);
        }
        var lineLeft = document.createElement('div');
        lineLeft.className = 'claude-compaction-line';
        var lineRight = document.createElement('div');
        lineRight.className = 'claude-compaction-line';
        marker.appendChild(lineLeft);
        marker.appendChild(label);
        marker.appendChild(lineRight);
        messagesEl.appendChild(marker);
        scrollToBottom();
    }

    function renderBashInput(body, input) {
        var block = document.createElement('div');
        block.className = 'claude-bash-block';
        if (input.description) {
            var desc = document.createElement('div');
            desc.className = 'claude-bash-description';
            desc.textContent = input.description;
            block.appendChild(desc);
        }
        var cmdDiv = document.createElement('div');
        cmdDiv.className = 'claude-bash-command';
        var prompt = document.createElement('span');
        prompt.className = 'claude-bash-prompt';
        prompt.textContent = '$ ';
        var code = document.createElement('code');
        try {
            code.innerHTML = hljs.highlight(input.command, { language: 'bash' }).value;
        } catch(e) {
            code.textContent = input.command;
        }
        cmdDiv.appendChild(prompt);
        cmdDiv.appendChild(code);
        block.appendChild(cmdDiv);
        body.insertBefore(block, body.firstChild);
    }

    function renderBashResult(resultPre, content) {
        resultPre.className = 'claude-bash-output';
        var truncateThreshold = 3000;
        if (content.length <= truncateThreshold) {
            resultPre.textContent = content;
            return;
        }
        var truncated = content.substring(0, truncateThreshold);
        resultPre.textContent = truncated;
        var toggle = document.createElement('a');
        toggle.className = 'claude-truncation-toggle';
        toggle.textContent = 'Show full output (' + content.length.toLocaleString() + ' chars)';
        toggle.href = '#';
        var expanded = false;
        toggle.addEventListener('click', function(e) {
            e.preventDefault();
            e.stopPropagation();
            expanded = !expanded;
            if (expanded) {
                resultPre.textContent = content;
                toggle.textContent = 'Show less';
            } else {
                resultPre.textContent = truncated;
                toggle.textContent = 'Show full output (' + content.length.toLocaleString() + ' chars)';
            }
        });
        resultPre.parentNode.insertBefore(toggle, resultPre.nextSibling);
    }

    function renderTaskInput(body, input) {
        var container = document.createElement('div');
        container.className = 'claude-subagent';
        var header = document.createElement('div');
        header.className = 'claude-subagent-header';
        if (input.subagent_type) {
            var badge = document.createElement('span');
            badge.className = 'claude-subagent-badge badge';
            badge.textContent = input.subagent_type;
            header.appendChild(badge);
        }
        if (input.model) {
            var modelBadge = document.createElement('span');
            modelBadge.className = 'claude-subagent-model-badge badge';
            modelBadge.textContent = input.model;
            header.appendChild(modelBadge);
        }
        if (input.description) {
            var desc = document.createElement('span');
            desc.className = 'claude-subagent-desc';
            desc.textContent = input.description;
            header.appendChild(desc);
        }
        container.appendChild(header);
        if (input.prompt) {
            var promptDiv = document.createElement('div');
            promptDiv.className = 'claude-subagent-prompt';
            promptDiv.textContent = input.prompt;
            container.appendChild(promptDiv);
        }
        body.insertBefore(container, body.firstChild);
    }

    function renderTaskResult(resultDiv, content) {
        var resultLabel = resultDiv.querySelector('.claude-tool-result-label');
        var resultPre = resultDiv.querySelector('pre');
        var rendered = document.createElement('div');
        rendered.className = 'claude-subagent-result';
        rendered.innerHTML = marked.parse(content);
        addCopyButtons(rendered);
        if (resultPre) resultPre.style.display = 'none';
        if (resultLabel) {
            resultDiv.insertBefore(rendered, resultLabel.nextSibling);
        } else {
            resultDiv.appendChild(rendered);
        }
    }

    // --- Rendering ---

    function ensureAssistantBubble() {
        if (currentBubble) return;
        removeWorkingIndicator();

        const wrapper = document.createElement('div');
        wrapper.className = 'claude-message claude-message-assistant';

        const bubble = document.createElement('div');
        bubble.className = 'claude-bubble claude-bubble-assistant';

        const textEl = document.createElement('div');
        textEl.className = 'claude-text-content';
        bubble.appendChild(textEl);

        wrapper.appendChild(bubble);
        // Live-streaming bubble: copy button reflects the accumulated text
        // during streaming, or the snapshot stashed on the wrapper after the
        // turn finishes (since accumulatedText is cleared at that point).
        attachCopyButton(wrapper, function() {
            return wrapper.__markdownSource || accumulatedText;
        });
        messagesEl.appendChild(wrapper);

        currentBubble = bubble;
        currentTextEl = textEl;
        accumulatedText = '';
        scrollToBottom();
    }

    function renderAssistantMarkdown() {
        if (!currentTextEl) return;
        currentTextEl.innerHTML = marked.parse(accumulatedText);
        addCopyButtons(currentTextEl);
        scrollToBottom();
    }

    function finishAssistantTurn() {
        removeWorkingIndicator();
        if (currentTextEl) {
            renderAssistantMarkdown();
        }
        // Snapshot the final markdown on the streaming wrapper so its copy
        // button still works after accumulatedText is cleared. Also write it
        // to the button's data attribute so it survives a cache restore.
        if (currentBubble && currentBubble.parentElement) {
            var parentWrapper = currentBubble.parentElement;
            parentWrapper.__markdownSource = accumulatedText;
            var copyBtn = parentWrapper.querySelector('.claude-message-copy');
            if (copyBtn) copyBtn.dataset.copyMarkdown = accumulatedText;
        }
        currentBubble = null;
        currentTextEl = null;
        accumulatedText = '';
        streamingToolInput = '';
        usingStreamEvents = false;
    }

    function appendToolUse(block) {
        if (!currentBubble) return;

        // Plan mode tools render as banners, not generic tool blocks
        if (isPlanModeTool(block.name)) {
            if (accumulatedText && currentTextEl) {
                renderAssistantMarkdown();
            }
            appendPlanModeBanner(currentBubble, block.name, block.input, block.id);
            return;
        }

        // Render any accumulated text first, then reset text tracking
        if (accumulatedText && currentTextEl) {
            renderAssistantMarkdown();
            currentTextEl = null;
            accumulatedText = '';
        }

        const toolDiv = document.createElement('div');
        toolDiv.className = 'claude-tool-use';
        toolDiv.dataset.toolId = block.id || '';

        const header = document.createElement('div');
        header.className = 'claude-tool-header';
        var toolIcon = (block.name === 'Task') ? 'fa-diagram-project' : 'fa-wrench';
        header.innerHTML =
            '<i class="fa-solid ' + toolIcon + ' claude-tool-icon"></i>' +
            '<span class="claude-tool-name">' + escapeHtml(block.name || 'Tool') + '</span>' +
            '<span class="claude-tool-subtitle"></span>' +
            '<i class="fa-solid fa-chevron-right claude-tool-chevron"></i>';

        header.addEventListener('click', function() {
            this.classList.toggle('expanded');
            body.classList.toggle('show');
        });

        const body = document.createElement('div');
        body.className = 'claude-tool-body';

        // Format input JSON
        let inputText = '';
        if (block.input) {
            try {
                if (typeof block.input === 'string') {
                    inputText = block.input;
                } else {
                    inputText = JSON.stringify(block.input, null, 2);
                }
            } catch (e) {
                inputText = String(block.input);
            }
        }

        const inputPre = document.createElement('pre');
        inputPre.textContent = inputText;
        body.appendChild(inputPre);

        // Bash: render terminal-styled command
        if (block.name === 'Bash' && block.input && block.input.command) {
            renderBashInput(body, block.input);
            inputPre.style.display = 'none';
        }
        // Task: render subagent display
        if (block.name === 'Task' && block.input && (block.input.prompt || block.input.description)) {
            renderTaskInput(body, block.input);
            inputPre.style.display = 'none';
        }

        // Placeholder for result
        const resultDiv = document.createElement('div');
        resultDiv.className = 'claude-tool-result';
        resultDiv.style.display = 'none';
        const resultLabel = document.createElement('div');
        resultLabel.className = 'claude-tool-result-label';
        resultLabel.textContent = 'Result';
        const resultPre = document.createElement('pre');
        resultDiv.appendChild(resultLabel);
        resultDiv.appendChild(resultPre);
        body.appendChild(resultDiv);

        toolDiv.appendChild(header);
        toolDiv.appendChild(body);
        currentBubble.appendChild(toolDiv);

        // Create a new text element after the tool block for subsequent text
        const newTextEl = document.createElement('div');
        newTextEl.className = 'claude-text-content';
        currentBubble.appendChild(newTextEl);
        currentTextEl = newTextEl;

        scrollToBottom();
    }

    function fillToolResult(block) {
        if (!block.tool_use_id) return;

        var escaped = CSS.escape(block.tool_use_id);

        // Check for plan mode banner with this tool ID — render plan content as markdown
        var planBanner = currentBubble ?
            currentBubble.querySelector('.claude-plan-mode[data-tool-id="' + escaped + '"]') :
            messagesEl.querySelector('.claude-plan-mode[data-tool-id="' + escaped + '"]');
        if (planBanner) {
            var contentDiv = planBanner.querySelector('.claude-plan-mode-content');
            if (contentDiv) {
                var content = block.content || '';
                if (typeof content !== 'string') {
                    try { content = JSON.stringify(content, null, 2); } catch(e) { content = String(content); }
                }
                if (content) {
                    contentDiv.innerHTML = marked.parse(content);
                    addCopyButtons(contentDiv);
                    scrollToBottom();
                }
            }
            return;
        }

        const toolDiv = currentBubble ?
            currentBubble.querySelector('[data-tool-id="' + escaped + '"]') :
            messagesEl.querySelector('[data-tool-id="' + escaped + '"]');

        if (!toolDiv) return;

        const resultDiv = toolDiv.querySelector('.claude-tool-result');
        if (resultDiv) {
            resultDiv.style.display = 'block';
            const resultPre = resultDiv.querySelector('pre');
            if (resultPre) {
                let content = block.content || '';
                if (typeof content !== 'string') {
                    try { content = JSON.stringify(content, null, 2); } catch(e) { content = String(content); }
                }
                // Determine tool name and file path for highlighting
                var nameEl = toolDiv.querySelector('.claude-tool-name');
                var toolName = nameEl ? nameEl.textContent : '';
                var subEl = toolDiv.querySelector('.claude-tool-subtitle');
                var filePath = (toolName === 'Read' && subEl) ? subEl.textContent : '';
                if (toolName === 'Bash') {
                    renderBashResult(resultPre, content);
                } else if (toolName === 'Task') {
                    renderTaskResult(resultDiv, content);
                } else {
                    renderToolResultContent(resultPre, content, toolName, filePath);
                }

                // Add Grep result summary to header
                if (toolName === 'Grep') {
                    var summary = formatGrepResultSummary(content);
                    if (summary) {
                        var header = toolDiv.querySelector('.claude-tool-header');
                        if (header && !header.querySelector('.claude-tool-result-summary')) {
                            var summaryEl = document.createElement('span');
                            summaryEl.className = 'claude-tool-result-summary';
                            summaryEl.textContent = summary;
                            var chevron = header.querySelector('.claude-tool-chevron');
                            if (chevron) {
                                header.insertBefore(summaryEl, chevron);
                            } else {
                                header.appendChild(summaryEl);
                            }
                        }
                    }
                }
            }
        }
    }

    function renderHistory(messages, generating) {
        messagesEl.innerHTML = '';
        currentBubble = null;
        currentTextEl = null;
        accumulatedText = '';
        usingStreamEvents = false;

        if (messages.length === 0) {
            showEmptyState();
            return;
        }

        // Detach messagesEl from the DOM for the duration of the render.
        // Each renderAssistantMessage appends a wrapper; doing that against a
        // live, scrollable, flex-laid-out parent triggers layout/paint mid-loop
        // and the browser shows messages "streaming in" with visible scrolling.
        // Building inside a detached node keeps all work off the render tree.
        var parent = messagesEl.parentNode;
        var nextSibling = parent ? messagesEl.nextSibling : null;
        if (parent) parent.removeChild(messagesEl);

        // Build a map of tool_use_id → tool_result block across all messages
        // (tool_results live in user messages, tool_uses in assistant messages)
        var toolResults = {};
        // Collect all Write-to-.md contents across messages for cross-turn plan lookup
        var planWrites = [];
        for (const msg of messages) {
            if (msg.content) {
                for (const block of msg.content) {
                    if (block.type === 'tool_result' && block.tool_use_id) {
                        toolResults[block.tool_use_id] = block;
                    }
                    if (block.type === 'tool_use' && block.name === 'Write' &&
                        block.input && block.input.file_path &&
                        isPlanFilePath(block.input.file_path) && block.input.content) {
                        planWrites.push(block.input.content);
                    }
                }
            }
        }

        try {
            for (var mi = 0; mi < messages.length; mi++) {
                var msg = messages[mi];
                var isLast = mi === messages.length - 1;
                if (msg.role === 'user') {
                    renderUserMessage(msg, mi);
                } else if (msg.role === 'assistant') {
                    // The server synthesizes an in-progress assistant message as the
                    // trailing entry when generating=true. Treat that one as pending
                    // so subsequent stream deltas append to its bubble.
                    renderAssistantMessage(msg, toolResults, planWrites, generating && isLast, mi);
                }
            }
        } finally {
            // Re-attach so the single batched paint shows the full list at once.
            if (parent) parent.insertBefore(messagesEl, nextSibling);
        }
        // Jump instantly to the bottom on initial render. scroll-behavior:smooth
        // on .claude-messages would otherwise animate through every message.
        scrollToBottomInstant();
    }

    function scrollToBottomInstant() {
        requestAnimationFrame(function() {
            var prev = messagesEl.style.scrollBehavior;
            messagesEl.style.scrollBehavior = 'auto';
            messagesEl.scrollTop = messagesEl.scrollHeight;
            messagesEl.style.scrollBehavior = prev;
        });
    }

    function renderUserMessage(msg, messageIndex) {
        const wrapper = document.createElement('div');
        wrapper.className = 'claude-message claude-message-user';

        const bubble = document.createElement('div');
        bubble.className = 'claude-bubble claude-bubble-user';

        // Get text from content blocks
        let text = '';
        if (msg.content) {
            for (const block of msg.content) {
                if (block.type === 'text') text += block.text;
            }
        }
        bubble.textContent = text;

        // Buttons go BEFORE the bubble in the DOM — flex-end right-aligns the
        // row, so the buttons appear to the left of the bubble.
        attachCopyButton(wrapper, function() { return text; });
        if (typeof messageIndex === 'number') attachForkButton(wrapper, messageIndex);
        wrapper.appendChild(bubble);
        messagesEl.appendChild(wrapper);
    }

    // Extract an assistant message as markdown by concatenating its text
    // blocks in order. Non-text blocks (tool_use, tool_result) are skipped
    // since they have no natural markdown representation.
    function assistantMessageMarkdown(msg) {
        if (!msg || !msg.content) return '';
        var parts = [];
        for (var i = 0; i < msg.content.length; i++) {
            var block = msg.content[i];
            if (block.type === 'text' && block.text) parts.push(block.text);
        }
        return parts.join('\n\n');
    }

    // attachForkButton creates a branch-icon button that opens the fork dialog
    // at this message's index. messageIndex is the 0-based index of the
    // message in the session's history; forking at index N creates a new
    // session containing messages[0..N] (inclusive).
    function attachForkButton(wrapper, messageIndex) {
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'claude-message-fork';
        btn.title = 'Fork session at this message';
        btn.dataset.messageIndex = String(messageIndex);
        btn.innerHTML = '<i class="fa-solid fa-code-branch"></i>';
        btn.addEventListener('click', function(e) {
            e.preventDefault();
            e.stopPropagation();
            openForkModal(messageIndex);
        });
        wrapper.appendChild(btn);
    }

    function openForkModal(messageIndex) {
        var modalEl = document.getElementById('claudeForkModal');
        if (!modalEl) return;
        var input = document.getElementById('claudeForkName');
        var idxEl = document.getElementById('claudeForkIndex');
        var subtitle = document.getElementById('claudeForkSubtitle');
        if (input) input.value = '';
        if (idxEl) idxEl.value = String(messageIndex);
        if (subtitle) {
            subtitle.textContent = 'The new session will contain the first ' +
                (messageIndex + 1) + ' message' + (messageIndex === 0 ? '' : 's') +
                ' of this conversation.';
        }
        var bs = bootstrap.Modal.getOrCreateInstance(modalEl);
        bs.show();
        setTimeout(function() { if (input) input.focus(); }, 100);
    }

    // Called from the fork modal's Create button (exposed on window).
    window.claudeForkSubmit = function claudeForkSubmit() {
        var idxEl = document.getElementById('claudeForkIndex');
        var input = document.getElementById('claudeForkName');
        var btn = document.getElementById('claudeForkConfirm');
        if (!idxEl || !input || !btn) return;
        var messageIndex = parseInt(idxEl.value, 10);
        var displayName = (input.value || '').trim();
        if (!displayName) { input.focus(); return; }
        if (typeof CLAUDE_SESSION === 'undefined' || !CLAUDE_SESSION) return;
        btn.disabled = true;
        fetch('/api/v1/claude/sessions/' + encodeURIComponent(CLAUDE_SESSION) + '/fork', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ message_index: messageIndex, display_name: displayName })
        })
        .then(function(r) {
            if (!r.ok) return r.json().then(function(d) {
                throw new Error((d && d.error && d.error.message) || 'Fork failed');
            });
            return r.json();
        })
        .then(function(payload) {
            var data = (payload && payload.data) || payload;
            var modalEl = document.getElementById('claudeForkModal');
            if (modalEl) bootstrap.Modal.getInstance(modalEl).hide();
            if (data && data.id && typeof CLAUDE_WORKTREE !== 'undefined') {
                window.location.href = '/claude/' + encodeURIComponent(CLAUDE_WORKTREE) + '/' + encodeURIComponent(data.id);
            }
        })
        .catch(function(err) { alert('Fork failed: ' + err); })
        .finally(function() { btn.disabled = false; });
    };

    // attachCopyButton creates an icon button that copies getText() to the
    // clipboard and inserts it into wrapper. The initial text is also stored
    // in data-copy-markdown so it survives a sessionStorage restore where the
    // closure is lost. For the streaming bubble, finishAssistantTurn updates
    // that attribute with the final text.
    function attachCopyButton(wrapper, getText) {
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'claude-message-copy';
        btn.title = 'Copy message as markdown';
        btn.innerHTML = '<i class="fa-regular fa-copy"></i>';
        try { btn.dataset.copyMarkdown = getText() || ''; } catch (e) {}
        btn.addEventListener('click', function(e) {
            e.preventDefault();
            e.stopPropagation();
            var text = getText() || btn.dataset.copyMarkdown || '';
            if (!text) return;
            (window.trellisCopyToClipboard || navigator.clipboard.writeText.bind(navigator.clipboard))(text).then(function() {
                btn.innerHTML = '<i class="fa-solid fa-check"></i>';
                setTimeout(function() {
                    btn.innerHTML = '<i class="fa-regular fa-copy"></i>';
                }, 1500);
            });
        });
        wrapper.appendChild(btn);

        // Cross-agent quote button: copies a markdown blockquote with
        // attribution so it pastes cleanly into the other agent's input.
        var role = wrapper.classList.contains('claude-message-user') ? 'user' : 'assistant';
        var qbtn = document.createElement('button');
        qbtn.type = 'button';
        qbtn.className = 'claude-message-copy';
        qbtn.title = 'Copy as quote (paste into Codex)';
        qbtn.innerHTML = '<i class="fa-solid fa-quote-right"></i>';
        qbtn.addEventListener('click', function(e) {
            e.preventDefault();
            e.stopPropagation();
            var text = getText() || '';
            if (!text) return;
            var header = '[from claude · ' + role + ']';
            var lines = text.split('\n').map(function(l) { return '> ' + l; }).join('\n');
            var quoted = '> ' + header + '\n' + lines;
            (window.trellisCopyToClipboard || navigator.clipboard.writeText.bind(navigator.clipboard))(quoted).then(function() {
                qbtn.innerHTML = '<i class="fa-solid fa-check"></i>';
                setTimeout(function() {
                    qbtn.innerHTML = '<i class="fa-solid fa-quote-right"></i>';
                }, 1500);
            });
        });
        wrapper.appendChild(qbtn);
    }

    function renderAssistantMessage(msg, toolResults, planWrites, pending, messageIndex) {
        if (!msg.content || msg.content.length === 0) return;

        const wrapper = document.createElement('div');
        wrapper.className = 'claude-message claude-message-assistant';

        const bubble = document.createElement('div');
        bubble.className = 'claude-bubble claude-bubble-assistant';
        const markdownSource = assistantMessageMarkdown(msg);

        // Identify Write tool blocks that wrote plan files (will be shown in ExitPlanMode banner instead)
        var planWriteIds = {};
        for (var pi = 0; pi < msg.content.length; pi++) {
            var pb = msg.content[pi];
            if (pb.type === 'tool_use' && pb.name === 'ExitPlanMode') {
                // Find the preceding Write to a plan/markdown file
                for (var pj = pi - 1; pj >= 0; pj--) {
                    var wb = msg.content[pj];
                    if (wb.type === 'tool_use' && wb.name === 'Write' && wb.input &&
                        wb.input.file_path && isPlanFilePath(wb.input.file_path)) {
                        planWriteIds[wb.id] = true;
                        break;
                    }
                }
            }
        }

        let textAcc = '';
        for (const block of msg.content) {
            if (block.type === 'text') {
                textAcc += block.text;
            } else if (block.type === 'tool_use') {
                // Skip Write tools whose content is shown in an ExitPlanMode banner
                if (planWriteIds[block.id]) continue;
                // Render accumulated text before tool
                if (textAcc) {
                    const textEl = document.createElement('div');
                    textEl.className = 'claude-text-content';
                    textEl.innerHTML = marked.parse(textAcc);
                    addCopyButtons(textEl);
                    bubble.appendChild(textEl);
                    textAcc = '';
                }
                // Render tool use block
                renderStaticToolUse(bubble, block, msg.content, toolResults, planWrites);
            } else if (block.type === 'tool_result') {
                // Results are handled inside renderStaticToolUse
            }
        }

        // Render remaining text. In pending mode, hand the trailing text
        // element off to the streaming state so new deltas append to the same
        // bubble instead of spawning a new one.
        var trailingTextEl = null;
        if (textAcc) {
            trailingTextEl = document.createElement('div');
            trailingTextEl.className = 'claude-text-content';
            trailingTextEl.innerHTML = marked.parse(textAcc);
            if (!pending) addCopyButtons(trailingTextEl);
            bubble.appendChild(trailingTextEl);
        }

        wrapper.appendChild(bubble);
        if (markdownSource && !pending) {
            attachCopyButton(wrapper, function() { return markdownSource; });
        }
        // Fork button only for completed messages with a known index.
        if (!pending && typeof messageIndex === 'number') {
            attachForkButton(wrapper, messageIndex);
        }
        messagesEl.appendChild(wrapper);

        if (pending) {
            currentBubble = bubble;
            currentTextEl = trailingTextEl; // may be null if last block was tool_use
            accumulatedText = textAcc || '';
            usingStreamEvents = true;
            attachCopyButton(wrapper, function() {
                return wrapper.__markdownSource || accumulatedText || markdownSource;
            });
        }
    }

    function renderTodoList(bubble, todos) {
        var container = document.createElement('div');
        container.className = 'claude-todo-list';
        for (var i = 0; i < todos.length; i++) {
            var todo = todos[i];
            var item = document.createElement('div');
            item.className = 'claude-todo-item';
            if (todo.status === 'completed') item.classList.add('claude-todo-completed');
            if (todo.status === 'in_progress') item.classList.add('claude-todo-in-progress');

            var checkbox = document.createElement('span');
            checkbox.className = 'claude-todo-checkbox';
            if (todo.status === 'completed') {
                checkbox.innerHTML = '<i class="fa-solid fa-square-check"></i>';
            } else if (todo.status === 'in_progress') {
                checkbox.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i>';
            } else {
                checkbox.innerHTML = '<i class="fa-regular fa-square"></i>';
            }

            var label = document.createElement('span');
            label.className = 'claude-todo-label';
            label.textContent = todo.content || '';

            item.appendChild(checkbox);
            item.appendChild(label);
            container.appendChild(item);
        }
        bubble.appendChild(container);
    }

    // renderAskUserQuestion renders a question card.
    // interactive: if true, options are clickable and a Submit button sends answers as a user message.
    function renderAskUserQuestion(bubble, input, interactive) {
        if (!input || !input.questions) return;
        var questions = input.questions;
        var container = document.createElement('div');
        container.className = 'claude-ask-question';
        if (interactive) container.classList.add('claude-ask-question-interactive');

        var selections = {};
        var submitBtn = null;

        // Declared at function scope so click handlers in the loop can reach it.
        var updateSubmitState = function() {
            if (!submitBtn) return;
            var allAnswered = true;
            for (var k in selections) {
                var val = selections[k];
                if (Array.isArray(val) ? val.length === 0 : !val) {
                    allAnswered = false;
                    break;
                }
            }
            submitBtn.disabled = !allAnswered;
        };

        for (var i = 0; i < questions.length; i++) {
            (function(qi, q) {
                var qDiv = document.createElement('div');
                qDiv.className = 'claude-ask-question-item';

                var header = document.createElement('div');
                header.className = 'claude-ask-question-header';
                if (q.header) {
                    var badge = document.createElement('span');
                    badge.className = 'claude-ask-question-badge';
                    badge.textContent = q.header;
                    header.appendChild(badge);
                }
                var qText = document.createElement('span');
                qText.className = 'claude-ask-question-text';
                qText.textContent = q.question || '';
                header.appendChild(qText);
                qDiv.appendChild(header);

                if (q.options) {
                    var optionsDiv = document.createElement('div');
                    optionsDiv.className = 'claude-ask-question-options';

                    if (interactive) {
                        if (q.multiSelect) {
                            selections[qi] = [];
                        } else {
                            selections[qi] = '';
                        }
                    }

                    for (var j = 0; j < q.options.length; j++) {
                        (function(oi, opt) {
                            if (interactive) {
                                var optBtn = document.createElement('button');
                                optBtn.className = 'claude-ask-prompt-option';
                                optBtn.type = 'button';
                                var optLabel = document.createElement('div');
                                optLabel.className = 'claude-ask-prompt-option-label';
                                optLabel.textContent = opt.label || '';
                                optBtn.appendChild(optLabel);
                                if (opt.description) {
                                    var optDesc = document.createElement('div');
                                    optDesc.className = 'claude-ask-prompt-option-desc';
                                    optDesc.textContent = opt.description;
                                    optBtn.appendChild(optDesc);
                                }
                                optBtn.addEventListener('click', function() {
                                    if (q.multiSelect) {
                                        optBtn.classList.toggle('selected');
                                        var idx = selections[qi].indexOf(opt.label);
                                        if (idx >= 0) {
                                            selections[qi].splice(idx, 1);
                                        } else {
                                            selections[qi].push(opt.label);
                                        }
                                    } else {
                                        var siblings = optionsDiv.querySelectorAll('.claude-ask-prompt-option');
                                        siblings.forEach(function(s) { s.classList.remove('selected'); });
                                        optBtn.classList.add('selected');
                                        selections[qi] = opt.label;
                                        // Hide the Other text input
                                        var otherInput = qDiv.querySelector('.claude-ask-prompt-other-input');
                                        if (otherInput) {
                                            otherInput.style.display = 'none';
                                            otherInput.value = '';
                                        }
                                    }
                                    updateSubmitState();
                                });
                                optionsDiv.appendChild(optBtn);
                            } else {
                                var optDiv = document.createElement('div');
                                optDiv.className = 'claude-ask-question-option';
                                var optLabel = document.createElement('div');
                                optLabel.className = 'claude-ask-question-option-label';
                                optLabel.textContent = opt.label || '';
                                optDiv.appendChild(optLabel);
                                if (opt.description) {
                                    var optDesc = document.createElement('div');
                                    optDesc.className = 'claude-ask-question-option-desc';
                                    optDesc.textContent = opt.description;
                                    optDiv.appendChild(optDesc);
                                }
                                optionsDiv.appendChild(optDiv);
                            }
                        })(j, q.options[j]);
                    }

                    // "Other" option for interactive mode
                    if (interactive) {
                        var otherBtn = document.createElement('button');
                        otherBtn.className = 'claude-ask-prompt-option claude-ask-prompt-option-other';
                        otherBtn.type = 'button';
                        var otherLabel = document.createElement('div');
                        otherLabel.className = 'claude-ask-prompt-option-label';
                        otherLabel.textContent = 'Other';
                        otherBtn.appendChild(otherLabel);

                        var otherInput = document.createElement('input');
                        otherInput.type = 'text';
                        otherInput.className = 'claude-ask-prompt-other-input';
                        otherInput.placeholder = 'Type your answer...';
                        otherInput.style.display = 'none';

                        (function(qi2) {
                            otherBtn.addEventListener('click', function() {
                                if (!q.multiSelect) {
                                    var siblings = optionsDiv.querySelectorAll('.claude-ask-prompt-option');
                                    siblings.forEach(function(s) { s.classList.remove('selected'); });
                                    selections[qi2] = '';
                                }
                                otherBtn.classList.toggle('selected');
                                if (otherBtn.classList.contains('selected')) {
                                    otherInput.style.display = 'block';
                                    otherInput.focus();
                                } else {
                                    otherInput.style.display = 'none';
                                    otherInput.value = '';
                                    updateSubmitState();
                                }
                            });
                            otherInput.addEventListener('input', function() {
                                if (q.multiSelect) {
                                    selections[qi2] = selections[qi2].filter(function(v) {
                                        return q.options.some(function(o) { return o.label === v; });
                                    });
                                    if (otherInput.value.trim()) {
                                        selections[qi2].push(otherInput.value.trim());
                                    }
                                } else {
                                    selections[qi2] = otherInput.value.trim();
                                }
                                updateSubmitState();
                            });
                        })(qi);

                        optionsDiv.appendChild(otherBtn);
                        qDiv.appendChild(optionsDiv);
                        qDiv.appendChild(otherInput);
                    } else {
                        qDiv.appendChild(optionsDiv);
                    }
                }

                container.appendChild(qDiv);
            })(i, questions[i]);
        }

        // Submit button for interactive mode (send answers as a user message)
        if (interactive) {
            var actions = document.createElement('div');
            actions.className = 'claude-ask-question-actions';

            submitBtn = document.createElement('button');
            submitBtn.className = 'btn btn-success btn-sm';
            submitBtn.textContent = 'Submit';
            submitBtn.disabled = true;
            actions.appendChild(submitBtn);
            container.appendChild(actions);

            submitBtn.addEventListener('click', function() {
                // Build answer text as a user message
                var parts = [];
                for (var k = 0; k < questions.length; k++) {
                    var val = selections[k];
                    var answer = Array.isArray(val) ? val.join(', ') : val;
                    var label = questions[k].header || ('Question ' + (k + 1));
                    parts.push(label + ': ' + answer);
                }
                var text = parts.join('\n');

                // Send as a user message
                var wrapper = document.createElement('div');
                wrapper.className = 'claude-message claude-message-user';
                var userBubble = document.createElement('div');
                userBubble.className = 'claude-bubble claude-bubble-user';
                userBubble.textContent = text;
                wrapper.appendChild(userBubble);
                messagesEl.appendChild(wrapper);
                scrollToBottom();

                sendWS({ type: 'message', content: text });
                setGenerating(true);
                showWorkingIndicator();

                // Replace interactive card with static answered display
                container.classList.remove('claude-ask-question-interactive');
                var actionsEl = container.querySelector('.claude-ask-question-actions');
                if (actionsEl) actionsEl.remove();
                // Disable all buttons
                var btns = container.querySelectorAll('.claude-ask-prompt-option');
                btns.forEach(function(b) {
                    b.disabled = true;
                    if (!b.classList.contains('selected')) b.style.opacity = '0.4';
                });
                var otherInputs = container.querySelectorAll('.claude-ask-prompt-other-input');
                otherInputs.forEach(function(inp) { inp.disabled = true; });
            });
        }

        bubble.appendChild(container);
    }

    function showAskUserQuestionPrompt(requestId, request, input) {
        // Hide any existing interactive question card from history rendering
        // to avoid duplication (the control_request prompt supersedes it).
        var existingCards = messagesEl.querySelectorAll('.claude-ask-question-interactive');
        existingCards.forEach(function(card) { card.remove(); });

        var div = document.createElement('div');
        div.className = 'claude-permission claude-permission-question';
        div.dataset.requestId = requestId;

        var header = document.createElement('div');
        header.className = 'claude-permission-header';
        header.innerHTML =
            '<i class="fa-solid fa-circle-question"></i> ' +
            '<strong>Claude is asking a question</strong>';
        div.appendChild(header);

        var questions = input.questions || [];
        var selections = {}; // questionIndex → selected label(s)

        var questionsContainer = document.createElement('div');
        questionsContainer.className = 'claude-ask-prompt-body';

        for (var i = 0; i < questions.length; i++) {
            (function(qi, q) {
                var qDiv = document.createElement('div');
                qDiv.className = 'claude-ask-prompt-question';

                var qHeader = document.createElement('div');
                qHeader.className = 'claude-ask-prompt-question-header';
                if (q.header) {
                    var badge = document.createElement('span');
                    badge.className = 'claude-ask-question-badge';
                    badge.textContent = q.header;
                    qHeader.appendChild(badge);
                }
                var qText = document.createElement('span');
                qText.className = 'claude-ask-prompt-question-text';
                qText.textContent = q.question || '';
                qHeader.appendChild(qText);
                qDiv.appendChild(qHeader);

                if (q.options) {
                    var optionsDiv = document.createElement('div');
                    optionsDiv.className = 'claude-ask-prompt-options';

                    if (q.multiSelect) {
                        selections[qi] = [];
                    } else {
                        selections[qi] = '';
                    }

                    for (var j = 0; j < q.options.length; j++) {
                        (function(oi, opt) {
                            var optBtn = document.createElement('button');
                            optBtn.className = 'claude-ask-prompt-option';
                            optBtn.type = 'button';
                            var labelSpan = document.createElement('div');
                            labelSpan.className = 'claude-ask-prompt-option-label';
                            labelSpan.textContent = opt.label || '';
                            optBtn.appendChild(labelSpan);
                            if (opt.description) {
                                var descSpan = document.createElement('div');
                                descSpan.className = 'claude-ask-prompt-option-desc';
                                descSpan.textContent = opt.description;
                                optBtn.appendChild(descSpan);
                            }

                            optBtn.addEventListener('click', function() {
                                if (q.multiSelect) {
                                    optBtn.classList.toggle('selected');
                                    var idx = selections[qi].indexOf(opt.label);
                                    if (idx >= 0) {
                                        selections[qi].splice(idx, 1);
                                    } else {
                                        selections[qi].push(opt.label);
                                    }
                                } else {
                                    var siblings = optionsDiv.querySelectorAll('.claude-ask-prompt-option');
                                    siblings.forEach(function(s) { s.classList.remove('selected'); });
                                    optBtn.classList.add('selected');
                                    selections[qi] = opt.label;
                                    // Hide the Other text input if a button is selected
                                    var otherInput = qDiv.querySelector('.claude-ask-prompt-other-input');
                                    if (otherInput) {
                                        otherInput.style.display = 'none';
                                        otherInput.value = '';
                                    }
                                }
                                updateSubmitState();
                            });
                            optionsDiv.appendChild(optBtn);
                        })(j, q.options[j]);
                    }

                    // "Other" button with text input
                    var otherBtn = document.createElement('button');
                    otherBtn.className = 'claude-ask-prompt-option claude-ask-prompt-option-other';
                    otherBtn.type = 'button';
                    var otherLabel = document.createElement('div');
                    otherLabel.className = 'claude-ask-prompt-option-label';
                    otherLabel.textContent = 'Other';
                    otherBtn.appendChild(otherLabel);

                    var otherInput = document.createElement('input');
                    otherInput.type = 'text';
                    otherInput.className = 'claude-ask-prompt-other-input';
                    otherInput.placeholder = 'Type your answer...';
                    otherInput.style.display = 'none';

                    (function(qi2) {
                        otherBtn.addEventListener('click', function() {
                            if (!q.multiSelect) {
                                var siblings = optionsDiv.querySelectorAll('.claude-ask-prompt-option');
                                siblings.forEach(function(s) { s.classList.remove('selected'); });
                                selections[qi2] = '';
                            }
                            otherBtn.classList.toggle('selected');
                            if (otherBtn.classList.contains('selected')) {
                                otherInput.style.display = 'block';
                                otherInput.focus();
                            } else {
                                otherInput.style.display = 'none';
                                otherInput.value = '';
                                updateSubmitState();
                            }
                        });
                        otherInput.addEventListener('input', function() {
                            if (q.multiSelect) {
                                // Remove previous "other" value
                                selections[qi2] = selections[qi2].filter(function(v) {
                                    return q.options.some(function(o) { return o.label === v; });
                                });
                                if (otherInput.value.trim()) {
                                    selections[qi2].push(otherInput.value.trim());
                                }
                            } else {
                                selections[qi2] = otherInput.value.trim();
                            }
                            updateSubmitState();
                        });
                    })(qi);

                    optionsDiv.appendChild(otherBtn);
                    qDiv.appendChild(optionsDiv);
                    qDiv.appendChild(otherInput);
                }

                questionsContainer.appendChild(qDiv);
            })(i, questions[i]);
        }
        div.appendChild(questionsContainer);

        // Actions
        var actions = document.createElement('div');
        actions.className = 'claude-permission-actions';

        var submitBtn = document.createElement('button');
        submitBtn.className = 'btn btn-success btn-sm';
        submitBtn.textContent = 'Submit';
        submitBtn.disabled = true;

        function updateSubmitState() {
            var allAnswered = true;
            for (var k in selections) {
                var val = selections[k];
                if (Array.isArray(val) ? val.length === 0 : !val) {
                    allAnswered = false;
                    break;
                }
            }
            submitBtn.disabled = !allAnswered;
        }

        submitBtn.addEventListener('click', function() {
            // Build answers map
            var answers = {};
            for (var k in selections) {
                var val = selections[k];
                answers[k] = Array.isArray(val) ? val.join(', ') : val;
            }
            // Send control_response with answers in updatedInput
            var updatedInput = JSON.parse(JSON.stringify(input));
            updatedInput.answers = answers;
            var response = {
                type: 'control_response',
                response: {
                    subtype: 'success',
                    request_id: requestId,
                    response: {
                        behavior: 'allow',
                        updatedInput: updatedInput
                    }
                }
            };
            sendWS({ type: 'permission_response', data: response });
            // Mark as handled
            var actionsEl = div.querySelector('.claude-permission-actions');
            if (actionsEl) {
                var answeredHtml = '<span class="claude-permission-allowed"><i class="fa-solid fa-check"></i> Answered: ';
                var answerParts = [];
                for (var ak in answers) {
                    answerParts.push(escapeHtml(answers[ak]));
                }
                answeredHtml += answerParts.join('; ') + '</span>';
                actionsEl.innerHTML = answeredHtml;
            }
            showWorkingIndicator();
        });

        var denyBtn = document.createElement('button');
        denyBtn.className = 'btn btn-outline-danger btn-sm';
        denyBtn.textContent = 'Deny';
        denyBtn.addEventListener('click', function() {
            respondToPermission(requestId, false, request, false);
            markPermissionHandled(div, false);
        });

        actions.appendChild(submitBtn);
        actions.appendChild(denyBtn);
        div.appendChild(actions);

        messagesEl.appendChild(div);
        scrollToBottom();
    }

    function renderStaticToolUse(bubble, block, allBlocks, toolResults, planWrites) {
        // AskUserQuestion renders as an inline question card
        // Interactive if there's no tool_result (unanswered question)
        if (block.name === 'AskUserQuestion' && block.input && block.input.questions) {
            var hasResult = block.id && toolResults[block.id];
            renderAskUserQuestion(bubble, block.input, !hasResult);
            return;
        }

        // TodoWrite renders as an inline checklist
        if (block.name === 'TodoWrite' && block.input && block.input.todos) {
            renderTodoList(bubble, block.input.todos);
            return;
        }

        // Plan mode tools render as banners
        if (isPlanModeTool(block.name)) {
            var planContent = '';
            if (block.name === 'ExitPlanMode') {
                // The plan content is in the Write tool that wrote a markdown file
                // First search within this message's blocks
                var blockIdx = allBlocks.indexOf(block);
                for (var i = blockIdx - 1; i >= 0; i--) {
                    var b = allBlocks[i];
                    if (b.type === 'tool_use' && b.name === 'Write' && b.input &&
                        b.input.file_path && isPlanFilePath(b.input.file_path)) {
                        planContent = b.input.content || '';
                        break;
                    }
                }
                // Fallback: use cross-message plan writes (Write was in a previous turn)
                if (!planContent && planWrites && planWrites.length > 0) {
                    planContent = planWrites[planWrites.length - 1];
                }
            }
            var hasResult = block.id && toolResults[block.id];
            appendPlanModeBanner(bubble, block.name, block.input, block.id, planContent, !hasResult);
            return;
        }

        const toolDiv = document.createElement('div');
        toolDiv.className = 'claude-tool-use';
        toolDiv.dataset.toolId = block.id || '';

        var subtitle = getToolSubtitle(block.name, block.input);

        // For Grep, append result summary to subtitle
        var resultSummary = '';
        if (block.name === 'Grep' && block.id) {
            var tr = toolResults[block.id];
            if (tr) {
                var rc = tr.content || '';
                if (typeof rc !== 'string') {
                    try { rc = JSON.stringify(rc); } catch(e) { rc = ''; }
                }
                resultSummary = formatGrepResultSummary(rc);
            }
        }

        var toolIcon = (block.name === 'Task') ? 'fa-diagram-project' : 'fa-wrench';
        const header = document.createElement('div');
        header.className = 'claude-tool-header';
        header.innerHTML =
            '<i class="fa-solid ' + toolIcon + ' claude-tool-icon"></i>' +
            '<span class="claude-tool-name">' + escapeHtml(block.name || 'Tool') + '</span>' +
            '<span class="claude-tool-subtitle">' + escapeHtml(subtitle) + '</span>' +
            (resultSummary ? '<span class="claude-tool-result-summary">' + escapeHtml(resultSummary) + '</span>' : '') +
            '<i class="fa-solid fa-chevron-right claude-tool-chevron"></i>';

        // Make file path subtitles copyable on click
        if ((block.name === 'Read' || block.name === 'Write' || block.name === 'Edit') && block.input && block.input.file_path) {
            var subtitleEl = header.querySelector('.claude-tool-subtitle');
            if (subtitleEl) {
                makeCopyableSubtitle(subtitleEl, block.input.file_path);
            }
        }

        const body = document.createElement('div');
        body.className = 'claude-tool-body';

        header.addEventListener('click', function() {
            this.classList.toggle('expanded');
            body.classList.toggle('show');
        });

        let inputText = '';
        if (block.input) {
            try {
                if (typeof block.input === 'string') inputText = block.input;
                else inputText = JSON.stringify(block.input, null, 2);
            } catch (e) {
                inputText = String(block.input);
            }
        }

        // If diff_html is available, show rendered diff; hide raw JSON
        if (block.diff_html) {
            var diffWrapper = document.createElement('div');
            diffWrapper.className = 'claude-diff-wrapper';
            diffWrapper.innerHTML = block.diff_html;
            body.appendChild(diffWrapper);
        }

        const inputPre = document.createElement('pre');
        inputPre.textContent = inputText;
        if (block.diff_html) {
            inputPre.style.display = 'none';
        }
        body.appendChild(inputPre);

        // Bash: render terminal-styled command
        if (block.name === 'Bash' && block.input && block.input.command) {
            renderBashInput(body, block.input);
            inputPre.style.display = 'none';
        }
        // Task: render subagent display
        if (block.name === 'Task' && block.input && (block.input.prompt || block.input.description)) {
            renderTaskInput(body, block.input);
            inputPre.style.display = 'none';
        }

        // Find matching tool_result
        if (block.id) {
            for (const b of allBlocks) {
                if (b.type === 'tool_result' && b.tool_use_id === block.id) {
                    const resultDiv = document.createElement('div');
                    resultDiv.className = 'claude-tool-result';
                    const resultLabel = document.createElement('div');
                    resultLabel.className = 'claude-tool-result-label';
                    resultLabel.textContent = 'Result';
                    const resultPre = document.createElement('pre');
                    let content = b.content || '';
                    if (typeof content !== 'string') {
                        try { content = JSON.stringify(content, null, 2); } catch(e) { content = String(content); }
                    }
                    var filePath = (block.input && block.input.file_path) ? block.input.file_path : '';
                    resultDiv.appendChild(resultLabel);
                    resultDiv.appendChild(resultPre);
                    body.appendChild(resultDiv);
                    if (block.name === 'Bash') {
                        renderBashResult(resultPre, content);
                    } else if (block.name === 'Task') {
                        renderTaskResult(resultDiv, content);
                    } else {
                        renderToolResultContent(resultPre, content, block.name, filePath);
                    }
                    break;
                }
            }
        }

        toolDiv.appendChild(header);
        toolDiv.appendChild(body);
        bubble.appendChild(toolDiv);
    }

    function showEmptyState() {
        messagesEl.innerHTML =
            '<div class="claude-empty">' +
            '<i class="fa-solid fa-robot"></i>' +
            '<p>Claude Code</p>' +
            '<small>Ask questions about your codebase, run commands, or get help writing code.</small>' +
            '</div>';
    }

    // Shown briefly while the WebSocket connects and the first `history`
    // message arrives. Using this instead of showEmptyState() on init avoids
    // flashing the empty-state marketing copy for sessions that already have
    // plenty of messages.
    function showInitialLoading() {
        messagesEl.innerHTML =
            '<div class="claude-empty claude-loading">' +
            '<i class="fa-solid fa-spinner fa-spin"></i>' +
            '</div>';
    }

    function showError(message) {
        const errDiv = document.createElement('div');
        errDiv.className = 'claude-error';
        errDiv.textContent = message;
        messagesEl.appendChild(errDiv);
        scrollToBottom();
    }

    function showPermissionDenials(denials) {
        var unique = {};
        denials.forEach(function(d) {
            unique[d.tool_name] = true;
        });
        var names = Object.keys(unique);

        var div = document.createElement('div');
        div.className = 'claude-denial';
        div.innerHTML =
            '<i class="fa-solid fa-triangle-exclamation"></i>' +
            '<span>Permission denied for: <strong>' + escapeHtml(names.join(', ')) + '</strong></span>';
        messagesEl.appendChild(div);
        scrollToBottom();
    }

    // --- Permission Prompts ---

    function showPermissionPrompt(requestId, request) {
        // Finalize current bubble so post-approval text appears below the permission prompt
        removeWorkingIndicator();
        if (currentTextEl) {
        }
        currentBubble = null;
        currentTextEl = null;
        accumulatedText = '';

        var toolName = request.tool_name || 'Unknown';
        var input = request.input || {};

        // ExitPlanMode gets special rendering as a plan review panel
        if (toolName === 'ExitPlanMode') {
            showPlanPermissionPrompt(requestId, request, input);
            return;
        }

        // AskUserQuestion gets interactive question UI
        if (toolName === 'AskUserQuestion') {
            showAskUserQuestionPrompt(requestId, request, input);
            return;
        }

        // Build description based on tool type
        var detail = '';
        var editDiffEl = null; // Rendered diff element for Edit blocks
        var isBashCommand = false;
        switch (toolName) {
            case 'Write':
                // Check if a diff was rendered for this Write block
                var writeDiffWrappers = messagesEl.querySelectorAll('.claude-diff-wrapper');
                if (writeDiffWrappers.length > 0) {
                    editDiffEl = writeDiffWrappers[writeDiffWrappers.length - 1];
                }
                if (!editDiffEl) {
                    detail = input.file_path || '';
                }
                break;
            case 'Edit':
                // Check if a diff was rendered for this Edit block
                var allDiffWrappers = messagesEl.querySelectorAll('.claude-diff-wrapper');
                if (allDiffWrappers.length > 0) {
                    editDiffEl = allDiffWrappers[allDiffWrappers.length - 1];
                }
                if (editDiffEl) {
                    detail = '';
                } else {
                    detail = input.file_path || '';
                    if (input.old_string) {
                        detail += '\n' + input.old_string;
                    }
                }
                break;
            case 'Bash':
                detail = input.command || '';
                isBashCommand = true;
                break;
            default:
                try { detail = JSON.stringify(input, null, 2); } catch(e) { detail = ''; }
        }

        var div = document.createElement('div');
        div.className = 'claude-permission';
        div.dataset.requestId = requestId;

        var header = document.createElement('div');
        header.className = 'claude-permission-header';
        header.innerHTML =
            '<i class="fa-solid fa-shield-halved"></i> ' +
            '<strong>' + escapeHtml(toolName) + '</strong>';

        div.appendChild(header);

        if (editDiffEl) {
            var diffDetail = document.createElement('div');
            diffDetail.className = 'claude-permission-detail claude-permission-diff';
            diffDetail.innerHTML = editDiffEl.innerHTML;
            div.appendChild(diffDetail);
        } else if (detail) {
            var detailEl = document.createElement('pre');
            detailEl.className = 'claude-permission-detail';
            if (isBashCommand) {
                var codeEl = document.createElement('code');
                codeEl.className = 'hljs language-bash';
                try {
                    codeEl.innerHTML = hljs.highlight(detail, { language: 'bash' }).value;
                } catch(e) {
                    codeEl.textContent = detail;
                }
                detailEl.appendChild(codeEl);
            } else {
                detailEl.textContent = detail;
            }
            div.appendChild(detailEl);
        }

        var actions = document.createElement('div');
        actions.className = 'claude-permission-actions';

        var allowBtn = document.createElement('button');
        allowBtn.className = 'btn btn-success btn-sm';
        allowBtn.textContent = 'Allow';
        allowBtn.addEventListener('click', function() {
            respondToPermission(requestId, true, request, false);
            markPermissionHandled(div, true);
        });

        var allowSessionBtn = document.createElement('button');
        allowSessionBtn.className = 'btn btn-outline-success btn-sm';
        allowSessionBtn.textContent = 'Allow for session';
        allowSessionBtn.addEventListener('click', function() {
            respondToPermission(requestId, true, request, true);
            markPermissionHandled(div, true);
        });

        var denyBtn = document.createElement('button');
        denyBtn.className = 'btn btn-outline-danger btn-sm';
        denyBtn.textContent = 'Deny';
        denyBtn.addEventListener('click', function() {
            respondToPermission(requestId, false, request, false);
            markPermissionHandled(div, false);
        });

        actions.appendChild(allowBtn);
        if (request.permission_suggestions && request.permission_suggestions.length > 0) {
            actions.appendChild(allowSessionBtn);
        }
        actions.appendChild(denyBtn);
        div.appendChild(actions);

        messagesEl.appendChild(div);
        scrollToBottom();
    }

    function showPlanPermissionPrompt(requestId, request, input) {
        // Hide any existing interactive plan banner from history rendering
        var existingBanners = messagesEl.querySelectorAll('.claude-plan-mode-interactive');
        existingBanners.forEach(function(b) { b.remove(); });

        var div = document.createElement('div');
        div.className = 'claude-permission claude-permission-plan';
        div.dataset.requestId = requestId;

        // Plan mode header
        var header = document.createElement('div');
        header.className = 'claude-permission-header';
        header.innerHTML =
            '<i class="fa-solid fa-clipboard-check"></i> ' +
            '<strong>Plan ready for review</strong>';
        div.appendChild(header);

        // Render plan content as markdown
        var planText = input.plan || '';
        if (!planText) {
            // Fallback: try to find plan content from preceding Write tool
            var toolDivs = messagesEl.querySelectorAll('.claude-tool-use');
            for (var i = toolDivs.length - 1; i >= 0; i--) {
                var nameEl = toolDivs[i].querySelector('.claude-tool-name');
                var subEl = toolDivs[i].querySelector('.claude-tool-subtitle');
                if (nameEl && nameEl.textContent === 'Write' &&
                    subEl && isPlanFilePath(subEl.textContent)) {
                    try {
                        var pre = toolDivs[i].querySelector('.claude-tool-body pre');
                        var writeInput = JSON.parse(pre.textContent);
                        if (writeInput.content) planText = writeInput.content;
                    } catch(e) {}
                    break;
                }
            }
        }

        if (planText) {
            var contentDiv = document.createElement('div');
            contentDiv.className = 'claude-plan-mode-content';
            contentDiv.innerHTML = marked.parse(planText);
            addCopyButtons(contentDiv);
            div.appendChild(contentDiv);
        }

        // Show allowed prompts if present
        if (input.allowedPrompts && input.allowedPrompts.length > 0) {
            var permsDiv = document.createElement('div');
            permsDiv.className = 'claude-plan-mode-permissions';
            var permsLabel = document.createElement('div');
            permsLabel.className = 'claude-plan-permissions-label';
            permsLabel.textContent = 'Requested permissions:';
            permsDiv.appendChild(permsLabel);
            var ul = document.createElement('ul');
            for (var i = 0; i < input.allowedPrompts.length; i++) {
                var li = document.createElement('li');
                li.textContent = input.allowedPrompts[i].prompt || '';
                ul.appendChild(li);
            }
            permsDiv.appendChild(ul);
            div.appendChild(permsDiv);
        }

        // Allow / Deny actions
        var actions = document.createElement('div');
        actions.className = 'claude-permission-actions';

        var allowBtn = document.createElement('button');
        allowBtn.className = 'btn btn-success btn-sm';
        allowBtn.textContent = 'Approve Plan';
        allowBtn.addEventListener('click', function() {
            respondToPermission(requestId, true, request, false);
            markPermissionHandled(div, true);
        });

        var denyBtn = document.createElement('button');
        denyBtn.className = 'btn btn-outline-danger btn-sm';
        denyBtn.textContent = 'Reject';
        denyBtn.addEventListener('click', function() {
            respondToPermission(requestId, false, request, false);
            markPermissionHandled(div, false);
        });

        actions.appendChild(allowBtn);
        actions.appendChild(denyBtn);
        div.appendChild(actions);

        messagesEl.appendChild(div);
        scrollToBottom();
    }

    function respondToPermission(requestId, allowed, request, forSession) {
        var response;
        if (allowed) {
            var innerResponse = {
                behavior: 'allow',
                updatedInput: (request && request.input) ? request.input : {}
            };
            if (forSession && request && request.permission_suggestions) {
                innerResponse.updatedPermissions = request.permission_suggestions;
            }
            response = {
                type: 'control_response',
                response: {
                    subtype: 'success',
                    request_id: requestId,
                    response: innerResponse
                }
            };
        } else {
            response = {
                type: 'control_response',
                response: {
                    subtype: 'success',
                    request_id: requestId,
                    response: {
                        behavior: 'deny',
                        message: 'User denied this action'
                    }
                }
            };
        }
        sendWS({ type: 'permission_response', data: response });
    }

    function markPermissionHandled(div, allowed) {
        var actions = div.querySelector('.claude-permission-actions');
        if (actions) {
            actions.innerHTML = allowed ?
                '<span class="claude-permission-allowed"><i class="fa-solid fa-check"></i> Allowed</span>' :
                '<span class="claude-permission-denied"><i class="fa-solid fa-xmark"></i> Denied</span>';
        }
        if (allowed) {
            showWorkingIndicator();
        }
    }

    // --- Actions ---

    window.claudeSend = function() {
        const text = inputEl.value.trim();
        if (!text || generating) return;

        // Show user message immediately
        const wrapper = document.createElement('div');
        wrapper.className = 'claude-message claude-message-user';
        const bubble = document.createElement('div');
        bubble.className = 'claude-bubble claude-bubble-user';
        bubble.textContent = text;
        wrapper.appendChild(bubble);

        // Remove empty state if present
        const empty = messagesEl.querySelector('.claude-empty');
        if (empty) empty.remove();

        messagesEl.appendChild(wrapper);
        scrollToBottom();

        // Send to server
        sendWS({ type: 'message', content: text });

        // Clear input and draft, show working indicator
        inputEl.value = '';
        sessionStorage.removeItem(draftKey);
        autoResize();
        setGenerating(true);
        showWorkingIndicator();
    };

    window.claudeCancel = function() {
        sendWS({ type: 'cancel' });
    };

    window.claudeReset = function() {
        sendWS({ type: 'reset' });
        inputEl.value = '';
        sessionStorage.removeItem(draftKey);
        autoResize();
        messagesEl.innerHTML = '';
        currentBubble = null;
        currentTextEl = null;
        accumulatedText = '';
        streamingToolInput = '';
        usingStreamEvents = false;
        inputTokens = 0;
        tokenBreakdown = { base: 0, cacheCreate: 0, cacheRead: 0, total: 0 };
        updateContextUsage();
        setGenerating(false);
        showEmptyState();
    };

    function setGenerating(value) {
        generating = value;
        sendBtn.style.display = value ? 'none' : 'flex';
        cancelBtn.style.display = value ? 'flex' : 'none';
        inputEl.focus();
        if (!value) {
            // Re-focus after any pending DOM updates (markdown rendering, scroll, etc.)
            requestAnimationFrame(function() { inputEl.focus(); });
        }
    }

    // --- Context Usage ---

    function updateTokenBreakdown(usage) {
        if (!usage) return;
        var base = usage.input_tokens || 0;
        var cacheCreate = usage.cache_creation_input_tokens || 0;
        var cacheRead = usage.cache_read_input_tokens || 0;
        var total = base + cacheCreate + cacheRead;
        if (total > 0) {
            tokenBreakdown = { base: base, cacheCreate: cacheCreate, cacheRead: cacheRead, total: total };
            inputTokens = total;
            updateContextUsage();
        }
    }

    function buildTokenPopoverContent() {
        var pct = Math.round(tokenBreakdown.total / contextWindow * 100);
        var barClass = 'claude-token-bar-fill';
        if (pct >= 70) barClass += ' danger';
        else if (pct >= 50) barClass += ' warn';
        return '<div class="claude-token-breakdown">' +
            '<div class="claude-token-row"><span>Input</span><span>' + tokenBreakdown.base.toLocaleString() + '</span></div>' +
            '<div class="claude-token-row"><span>Cache read</span><span>' + tokenBreakdown.cacheRead.toLocaleString() + '</span></div>' +
            '<div class="claude-token-row"><span>Cache write</span><span>' + tokenBreakdown.cacheCreate.toLocaleString() + '</span></div>' +
            '<div class="claude-token-row claude-token-row-total"><span>Total</span><span>' + tokenBreakdown.total.toLocaleString() + '</span></div>' +
            '<div class="claude-token-bar-track"><div class="' + barClass + '" style="width:' + Math.min(pct, 100) + '%"></div></div>' +
            '</div>';
    }

    function updateTokenPopover(el) {
        if (!el || !tokenBreakdown.total) return;
        var existing = bootstrap.Popover.getInstance(el);
        if (existing) {
            existing.dispose();
        }
        new bootstrap.Popover(el, {
            trigger: 'hover focus',
            placement: 'top',
            html: true,
            content: buildTokenPopoverContent
        });
    }

    function updateContextUsage() {
        var el = document.getElementById('claude-context-usage');
        if (!el) return;
        if (!inputTokens) {
            el.textContent = '';
            el.className = 'claude-context-usage';
            return;
        }
        var pct = Math.round(inputTokens / contextWindow * 100);
        var tokensK = Math.round(inputTokens / 1000);
        el.textContent = tokensK + 'K / ' + (contextWindow / 1000) + 'K tokens (' + pct + '%)';
        if (pct >= 70) {
            el.className = 'claude-context-usage danger';
        } else if (pct >= 50) {
            el.className = 'claude-context-usage warn';
        } else {
            el.className = 'claude-context-usage';
        }
        updateTokenPopover(el);
    }

    // --- Helpers ---

    function scrollToBottom() {
        requestAnimationFrame(function() {
            messagesEl.scrollTop = messagesEl.scrollHeight;
        });
    }

    function escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    function addCopyButtons(container) {
        const pres = container.querySelectorAll('pre');
        pres.forEach(function(pre) {
            if (pre.querySelector('.claude-code-copy')) return;
            const btn = document.createElement('button');
            btn.className = 'claude-code-copy';
            btn.innerHTML = '<i class="fa-regular fa-copy"></i>';
            btn.title = 'Copy';
            btn.addEventListener('click', function() {
                const code = pre.querySelector('code');
                const text = code ? code.textContent : pre.textContent;
                (window.trellisCopyToClipboard || navigator.clipboard.writeText.bind(navigator.clipboard))(text).then(function() {
                    btn.innerHTML = '<i class="fa-solid fa-check"></i>';
                    setTimeout(function() {
                        btn.innerHTML = '<i class="fa-regular fa-copy"></i>';
                    }, 1500);
                });
            });
            pre.style.position = 'relative';
            pre.appendChild(btn);
        });
    }

    // Auto-resize textarea
    function autoResize() {
        inputEl.style.height = 'auto';
        inputEl.style.height = Math.min(inputEl.scrollHeight, 200) + 'px';
    }

    inputEl.addEventListener('input', autoResize);

    // Persist draft input across navigation
    var draftKey = 'claude-draft-' + (typeof CLAUDE_SESSION !== 'undefined' ? CLAUDE_SESSION : '');
    var savedDraft = sessionStorage.getItem(draftKey);
    if (savedDraft) {
        inputEl.value = savedDraft;
        autoResize();
    }
    inputEl.addEventListener('input', function() {
        sessionStorage.setItem(draftKey, inputEl.value);
    });

    // --- Slash Command Selector ---

    var slashMenu = null;
    var slashSelectedIndex = 0;

    function showSlashMenu() {
        hideSlashMenu();
        var filter = inputEl.value.substring(1).toLowerCase();
        var matches = slashCommands.filter(function(cmd) {
            return cmd.toLowerCase().indexOf(filter) === 0;
        });
        if (matches.length === 0) return;

        slashSelectedIndex = 0;
        slashMenu = document.createElement('div');
        slashMenu.className = 'claude-slash-menu';

        for (var i = 0; i < matches.length; i++) {
            var item = document.createElement('div');
            item.className = 'claude-slash-item' + (i === 0 ? ' selected' : '');
            item.dataset.index = i;
            item.dataset.command = matches[i];
            item.innerHTML = '<span class="claude-slash-cmd">/' + escapeHtml(matches[i]) + '</span>';
            item.addEventListener('click', function() {
                selectSlashCommand(this.dataset.command);
            });
            item.addEventListener('mouseenter', function() {
                var items = slashMenu.querySelectorAll('.claude-slash-item');
                items.forEach(function(el) { el.classList.remove('selected'); });
                this.classList.add('selected');
                slashSelectedIndex = parseInt(this.dataset.index);
            });
            slashMenu.appendChild(item);
        }

        var inputArea = document.getElementById('claude-input-area');
        inputArea.insertBefore(slashMenu, inputArea.firstChild);
    }

    function hideSlashMenu() {
        if (slashMenu) {
            slashMenu.remove();
            slashMenu = null;
        }
    }

    function selectSlashCommand(cmd) {
        inputEl.value = '/' + cmd;
        hideSlashMenu();
        inputEl.focus();
    }

    function navigateSlashMenu(direction) {
        if (!slashMenu) return;
        var items = slashMenu.querySelectorAll('.claude-slash-item');
        if (items.length === 0) return;
        items[slashSelectedIndex].classList.remove('selected');
        slashSelectedIndex += direction;
        if (slashSelectedIndex < 0) slashSelectedIndex = items.length - 1;
        if (slashSelectedIndex >= items.length) slashSelectedIndex = 0;
        items[slashSelectedIndex].classList.add('selected');
        items[slashSelectedIndex].scrollIntoView({ block: 'nearest' });
    }

    inputEl.addEventListener('input', function() {
        if (inputEl.value.match(/^\/\S*$/) && slashCommands.length > 0) {
            showSlashMenu();
        } else {
            hideSlashMenu();
        }
    });

    inputEl.addEventListener('keydown', function(e) {
        if (slashMenu) {
            if (e.key === 'ArrowDown') {
                e.preventDefault();
                navigateSlashMenu(1);
                return;
            } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                navigateSlashMenu(-1);
                return;
            } else if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                var selected = slashMenu.querySelector('.claude-slash-item.selected');
                if (selected) {
                    selectSlashCommand(selected.dataset.command);
                }
                return;
            } else if (e.key === 'Escape') {
                e.preventDefault();
                hideSlashMenu();
                return;
            } else if (e.key === 'Tab') {
                e.preventDefault();
                var selected = slashMenu.querySelector('.claude-slash-item.selected');
                if (selected) {
                    selectSlashCommand(selected.dataset.command);
                }
                return;
            }
        }
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            claudeSend();
        }
        if (e.key === 'Escape' && generating) {
            e.preventDefault();
            claudeCancel();
        }
    });

    // --- Reconnect on page visibility change ---

    var hiddenAt = 0;

    function forceReconnect() {
        if (ws) {
            ws.onclose = null; // prevent duplicate reconnect from onclose handler
            ws.close();
            ws = null;
        }
        clearTimeout(reconnectTimer);
        connect();
    }

    document.addEventListener('visibilitychange', function() {
        if (document.hidden) {
            hiddenAt = Date.now();
        } else {
            // Page became visible — reconnect if hidden for >5s or WS is dead
            var elapsed = hiddenAt ? (Date.now() - hiddenAt) : 0;
            if (elapsed > 5000 || !ws || ws.readyState !== WebSocket.OPEN) {
                forceReconnect();
            }
        }
    });

    window.addEventListener('pageshow', function(e) {
        if (e.persisted) {
            // Page restored from bfcache — WebSocket is definitely dead
            forceReconnect();
        }
    });

    // Initialize
    showInitialLoading();
    connect();
    inputEl.focus();

})();
