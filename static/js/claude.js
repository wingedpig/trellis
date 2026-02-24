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

    function appendPlanModeBanner(bubble, name, input, toolId, planContent) {
        var isEnter = (name === 'EnterPlanMode');
        var icon = isEnter ? 'fa-clipboard-list' : 'fa-clipboard-check';
        var label = isEnter ? 'Entering plan mode' : 'Plan ready for review';

        var banner = document.createElement('div');
        banner.className = 'claude-plan-mode';
        banner.dataset.planMode = name;
        if (toolId) banner.dataset.toolId = toolId;
        banner.innerHTML =
            '<i class="fa-solid ' + icon + '"></i>' +
            '<span class="claude-plan-mode-label">' + escapeHtml(label) + '</span>';

        // For ExitPlanMode, show allowed prompts if present
        if (!isEnter && input && input.allowedPrompts && input.allowedPrompts.length > 0) {
            var permsDiv = document.createElement('div');
            permsDiv.className = 'claude-plan-mode-permissions';
            var ul = document.createElement('ul');
            for (var i = 0; i < input.allowedPrompts.length; i++) {
                var li = document.createElement('li');
                li.textContent = input.allowedPrompts[i].prompt || '';
                ul.appendChild(li);
            }
            permsDiv.appendChild(ul);
            banner.appendChild(permsDiv);
        }

        // For ExitPlanMode, add plan content area (populated now or later via fillToolResult)
        if (!isEnter) {
            var contentDiv = document.createElement('div');
            contentDiv.className = 'claude-plan-mode-content';
            if (planContent) {
                contentDiv.innerHTML = marked.parse(planContent);
                addCopyButtons(contentDiv);
            }
            banner.appendChild(contentDiv);
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
            try {
                const msg = JSON.parse(e.data);
                handleServerMessage(msg);
            } catch (err) {
                console.error('Failed to parse WS message:', err);
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
                renderHistory(msg.messages || []);
                if (msg.generating) {
                    setGenerating(true);
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
                        // Insert permanent compaction marker in timeline
                        currentBubble = null;
                        currentTextEl = null;
                        accumulatedText = '';
                        insertCompactionMarker();
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
                                if (lastToolName === 'ExitPlanMode') {
                                    var contentDiv = lastBanner.querySelector('.claude-plan-mode-content');
                                    if (contentDiv && !contentDiv.innerHTML) {
                                        var toolDivs = currentBubble.querySelectorAll('.claude-tool-use');
                                        for (var ti = toolDivs.length - 1; ti >= 0; ti--) {
                                            var nameEl = toolDivs[ti].querySelector('.claude-tool-name');
                                            var subEl = toolDivs[ti].querySelector('.claude-tool-subtitle');
                                            if (nameEl && nameEl.textContent === 'Write' &&
                                                subEl && subEl.textContent.indexOf('/plans/') !== -1) {
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

                                // TodoWrite: replace the tool block with a rendered checklist
                                if (lastToolName === 'TodoWrite' && input.todos) {
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
        subtitleEl.addEventListener('click', function(e) {
            e.stopPropagation();
            navigator.clipboard.writeText(path).then(function() {
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

    function insertCompactionMarker() {
        var marker = document.createElement('div');
        marker.className = 'claude-compaction-marker';
        marker.innerHTML =
            '<div class="claude-compaction-line"></div>' +
            '<span class="claude-compaction-label"><i class="fa-solid fa-compress"></i> Context compacted</span>' +
            '<div class="claude-compaction-line"></div>';
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
        currentBubble = null;
        currentTextEl = null;
        accumulatedText = '';
        streamingToolInput = '';
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

    function renderHistory(messages) {
        messagesEl.innerHTML = '';
        currentBubble = null;
        currentTextEl = null;
        accumulatedText = '';

        if (messages.length === 0) {
            showEmptyState();
            return;
        }

        // Build a map of tool_use_id → tool_result block across all messages
        // (tool_results live in user messages, tool_uses in assistant messages)
        var toolResults = {};
        for (const msg of messages) {
            if (msg.content) {
                for (const block of msg.content) {
                    if (block.type === 'tool_result' && block.tool_use_id) {
                        toolResults[block.tool_use_id] = block;
                    }
                }
            }
        }

        for (const msg of messages) {
            if (msg.role === 'user') {
                renderUserMessage(msg);
            } else if (msg.role === 'assistant') {
                renderAssistantMessage(msg, toolResults);
            }
        }
        scrollToBottom();
    }

    function renderUserMessage(msg) {
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

        wrapper.appendChild(bubble);
        messagesEl.appendChild(wrapper);
    }

    function renderAssistantMessage(msg, toolResults) {
        if (!msg.content || msg.content.length === 0) return;

        const wrapper = document.createElement('div');
        wrapper.className = 'claude-message claude-message-assistant';

        const bubble = document.createElement('div');
        bubble.className = 'claude-bubble claude-bubble-assistant';

        // Identify Write tool blocks that wrote plan files (will be shown in ExitPlanMode banner instead)
        var planWriteIds = {};
        for (var pi = 0; pi < msg.content.length; pi++) {
            var pb = msg.content[pi];
            if (pb.type === 'tool_use' && pb.name === 'ExitPlanMode') {
                // Find the preceding Write to a plan file
                for (var pj = pi - 1; pj >= 0; pj--) {
                    var wb = msg.content[pj];
                    if (wb.type === 'tool_use' && wb.name === 'Write' && wb.input &&
                        wb.input.file_path && wb.input.file_path.indexOf('/plans/') !== -1) {
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
                renderStaticToolUse(bubble, block, msg.content, toolResults);
            } else if (block.type === 'tool_result') {
                // Results are handled inside renderStaticToolUse
            }
        }

        // Render remaining text
        if (textAcc) {
            const textEl = document.createElement('div');
            textEl.className = 'claude-text-content';
            textEl.innerHTML = marked.parse(textAcc);
            addCopyButtons(textEl);
            bubble.appendChild(textEl);
        }

        wrapper.appendChild(bubble);
        messagesEl.appendChild(wrapper);
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

    function renderStaticToolUse(bubble, block, allBlocks, toolResults) {
        // TodoWrite renders as an inline checklist
        if (block.name === 'TodoWrite' && block.input && block.input.todos) {
            renderTodoList(bubble, block.input.todos);
            return;
        }

        // Plan mode tools render as banners
        if (isPlanModeTool(block.name)) {
            var planContent = '';
            if (block.name === 'ExitPlanMode') {
                // The plan content is in the Write tool that wrote to .claude/plans/
                // Search backwards from the current block for the most recent plan file write
                var blockIdx = allBlocks.indexOf(block);
                for (var i = blockIdx - 1; i >= 0; i--) {
                    var b = allBlocks[i];
                    if (b.type === 'tool_use' && b.name === 'Write' && b.input &&
                        b.input.file_path && b.input.file_path.indexOf('/plans/') !== -1) {
                        planContent = b.input.content || '';
                        break;
                    }
                }
            }
            appendPlanModeBanner(bubble, block.name, block.input, block.id, planContent);
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
                    subEl && subEl.textContent.indexOf('/plans/') !== -1) {
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
                navigator.clipboard.writeText(text).then(function() {
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
    showEmptyState();
    connect();
    inputEl.focus();

})();
