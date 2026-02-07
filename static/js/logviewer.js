// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Shared log entry rendering functions used by terminal log viewer and trace reports.

// Escape HTML to prevent XSS
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// Render key-value pairs for kvpairs column type
function renderKvPairs(entry, keys, maxPairs) {
    if (!entry.fields || !keys || keys.length === 0) {
        return '';
    }
    const pairs = [];
    for (const key of keys) {
        if (entry.fields[key] !== undefined) {
            pairs.push(key + '=' + String(entry.fields[key]));
            if (maxPairs > 0 && pairs.length >= maxPairs) {
                break;
            }
        }
    }
    return pairs.join(' | ');
}

// Format timestamp for display (absolute or relative)
function formatTimestamp(timestamp, useAbsolute) {
    if (!timestamp) return '';
    try {
        const date = new Date(timestamp);
        if (isNaN(date.getTime())) return String(timestamp);
        if (useAbsolute) {
            return date.toLocaleTimeString('en-US', { hour12: false }) + '.' +
                   String(date.getMilliseconds()).padStart(3, '0');
        } else {
            const now = new Date();
            const diffMs = now - date;
            const diffSec = Math.floor(diffMs / 1000);
            if (diffSec < 60) return diffSec + 's ago';
            const diffMin = Math.floor(diffSec / 60);
            if (diffMin < 60) return diffMin + 'm ago';
            const diffHour = Math.floor(diffMin / 60);
            if (diffHour < 24) return diffHour + 'h ago';
            return Math.floor(diffHour / 24) + 'd ago';
        }
    } catch (e) {
        return String(timestamp).substring(11, 23);
    }
}

// Format full timestamp for tooltips
function formatFullTimestamp(ts) {
    const d = new Date(ts);
    return d.toLocaleString();
}

// Render a single log entry row (Flexbox)
// ctx: { container, layout, fieldMap, formatTs, onExpand, entryIndex, rowClass, prependColumns }
function renderLogEntry(entry, ctx) {
    const row = document.createElement('div');
    row.className = ctx.rowClass || 'logviewer-entry';
    if (entry.level) {
        row.className += ' level-' + entry.level.toLowerCase();
    }
    if (entry.is_context) {
        row.className += ' context-line';
    }
    row.dataset.entryIndex = ctx.entryIndex;
    row.onclick = () => ctx.onExpand(entry);

    // Optional: prepend extra columns (e.g., source for trace reports)
    if (ctx.prependColumns) {
        for (const prepend of ctx.prependColumns) {
            const span = document.createElement('span');
            span.className = 'column column-' + prepend.name;
            if (prepend.width) {
                span.style.width = prepend.width;
            }
            span.textContent = prepend.getValue(entry);
            span.title = span.textContent;
            row.appendChild(span);
        }
    }

    // Render configured columns in order using layout
    for (const layoutCol of ctx.layout) {
        const span = document.createElement('span');

        // Handle kvpairs column type
        if (layoutCol.type === 'kvpairs') {
            span.className = 'column column-kvpairs';
            const value = renderKvPairs(entry, layoutCol.keys, layoutCol.max_pairs);
            span.textContent = value;
            span.title = value;

            // Apply width constraints
            if (layoutCol.min_width > 0) {
                span.style.width = layoutCol.min_width + 'ch';
            } else if (layoutCol.max_width > 0) {
                span.style.maxWidth = layoutCol.max_width + 'ch';
            }
            row.appendChild(span);
            continue;
        }

        // Regular field column
        const col = layoutCol.field;
        span.className = 'column column-' + col;

        // Check if this column maps to a core field via parser config
        const mappedField = ctx.fieldMap ? ctx.fieldMap[col] : null;
        if (mappedField && mappedField !== col) {
            span.className += ' column-' + mappedField;
        }

        // Add column-timestamp class if timestamp flag is set
        if (layoutCol.timestamp) {
            span.className += ' column-timestamp';
        }

        // Get value based on column name
        let value = '';
        if (layoutCol.timestamp) {
            value = ctx.formatTs(entry.timestamp);
            span.title = formatFullTimestamp(entry.timestamp);
        } else if (mappedField === 'timestamp') {
            value = ctx.formatTs(entry.timestamp);
            span.title = formatFullTimestamp(entry.timestamp);
        } else if (mappedField === 'level') {
            value = entry.level || 'INFO';
            span.className += ' column-level level-' + (entry.level || 'info').toLowerCase();
        } else if (mappedField === 'message') {
            value = entry.message || entry.raw || entry._raw || '';
        } else if (entry.fields && entry.fields[col] !== undefined) {
            value = String(entry.fields[col]);
        } else if (entry[col] !== undefined) {
            if (typeof entry[col] === 'object') {
                value = JSON.stringify(entry[col]);
            } else {
                value = String(entry[col]);
            }
        }

        // Apply width constraints
        // min_width > 0: fixed width (reservation)
        // max_width > 0 only: cap width but shrink to content
        if (layoutCol.min_width > 0) {
            span.style.width = layoutCol.min_width + 'ch';
        } else if (layoutCol.max_width > 0) {
            span.style.maxWidth = layoutCol.max_width + 'ch';
        }

        span.textContent = value;
        if (!span.title) {
            span.title = value;
        }
        row.appendChild(span);
    }

    ctx.container.appendChild(row);
}

// Helper to create a field row with copy button
// Uses data-value attribute with base64 encoding to safely handle any value
function fieldRow(name, value) {
    const escapedValue = escapeHtml(String(value));
    const copyId = 'copy-' + Math.random().toString(36).substr(2, 9);
    // Encode value as base64 to avoid any escaping issues with special characters
    const encodedValue = btoa(unescape(encodeURIComponent(String(value))));
    return '<div class="field">' +
           '<span class="field-name"><i class="fa-regular fa-copy copy-btn" id="' + copyId + '" title="Copy value" ' +
           'data-copy-value="' + encodedValue + '" onclick="copyFieldValue(this)"></i>' +
           escapeHtml(name) + ':</span>' +
           '<span class="field-value">' + escapedValue + '</span></div>';
}

// Copy field value to clipboard
function copyFieldValue(icon) {
    // Decode base64 value from data attribute
    const encodedValue = icon.getAttribute('data-copy-value');
    const value = decodeURIComponent(escape(atob(encodedValue)));
    navigator.clipboard.writeText(value).then(function() {
        // Show feedback
        icon.className = 'fa-solid fa-check copy-btn copied';
        setTimeout(function() {
            icon.className = 'fa-regular fa-copy copy-btn';
        }, 1500);
    }).catch(function(err) {
        console.error('Failed to copy:', err);
    });
}

// Expand an entry to show all fields
// ctx: { expandedEl, contentEl, fieldNames, tbodySelector, filteredEntries, onSelect,
//        fileField, lineField, worktreePicker, onOpenEditor }
function expandEntry(entry, ctx) {
    ctx.onSelect(entry);

    let html = '';

    // Show timestamp with original field name
    const tsFieldName = (ctx.fieldNames && ctx.fieldNames['timestamp']) || 'timestamp';
    if (entry.timestamp) {
        html += fieldRow(tsFieldName, entry.timestamp);
    }

    // Show source if present (for trace entries)
    if (entry.source) {
        html += fieldRow('source', entry.source);
    }

    // Show level with original field name (only if present)
    const levelFieldName = (ctx.fieldNames && ctx.fieldNames['level']) || 'level';
    if (entry.level) {
        html += fieldRow(levelFieldName, entry.level);
    }

    // Show all fields from entry.fields, skipping those already shown above
    if (entry.fields) {
        const skipFields = new Set();
        if (ctx.fieldNames) {
            if (ctx.fieldNames.timestamp) skipFields.add(ctx.fieldNames.timestamp);
            if (ctx.fieldNames.level) skipFields.add(ctx.fieldNames.level);
            if (ctx.fieldNames.message) skipFields.add(ctx.fieldNames.message);
        }
        for (const [key, value] of Object.entries(entry.fields)) {
            if (skipFields.has(key)) continue;
            let displayValue = value;
            if (typeof value === 'object') {
                displayValue = JSON.stringify(value, null, 2);
            }
            html += fieldRow(key, displayValue);
        }
    }

    // Add "Open in Editor" button if file/line fields are configured and present in entry
    const fileValue = ctx.fileField && entry.fields && entry.fields[ctx.fileField];
    const lineValue = ctx.lineField && entry.fields && entry.fields[ctx.lineField];
    if (fileValue && ctx.onOpenEditor) {
        html += '<div class="logviewer-open-editor">';
        if (ctx.worktreePicker) {
            html += '<select class="logviewer-worktree-select" id="open-editor-worktree"></select>';
        }
        html += '<button class="logviewer-open-editor-btn" onclick="document._openEditorHandler()">Open in Editor</button>';
        html += '</div>';
        // Store the handler on document so the onclick can find it
        document._openEditorHandler = function() {
            ctx.onOpenEditor(String(fileValue), lineValue ? parseInt(lineValue, 10) : 1);
        };
    }

    // Add raw line
    const rawLine = entry.raw || entry._raw;
    if (rawLine) {
        html += '<div class="raw-log">' + escapeHtml(rawLine) + '</div>';
    }

    ctx.contentEl.innerHTML = html;
    ctx.expandedEl.style.display = 'flex';

    // Populate worktree picker if present
    if (fileValue && ctx.onOpenEditor && ctx.worktreePicker && ctx.populateWorktreePicker) {
        ctx.populateWorktreePicker();
    }

    // Highlight selected row
    document.querySelectorAll(ctx.tbodySelector + ' .logviewer-entry.selected').forEach(el =>
        el.classList.remove('selected'));
    const idx = ctx.filteredEntries.indexOf(entry);
    const rows = document.querySelectorAll(ctx.tbodySelector + ' .logviewer-entry');
    if (rows[idx]) {
        rows[idx].classList.add('selected');
    }
}

// Build a field map from viewer config parser fields
function buildFieldMap(viewerConfig) {
    const fieldMap = {};
    if (viewerConfig) {
        if (viewerConfig.timestamp_field) {
            fieldMap[viewerConfig.timestamp_field] = 'timestamp';
        }
        if (viewerConfig.level_field) {
            fieldMap[viewerConfig.level_field] = 'level';
        }
        if (viewerConfig.message_field) {
            fieldMap[viewerConfig.message_field] = 'message';
        }
    }
    return fieldMap;
}

// Get field names for expanded view
function getFieldNames(viewerConfig) {
    return {
        timestamp: viewerConfig && viewerConfig.timestamp_field || 'timestamp',
        level: viewerConfig && viewerConfig.level_field || 'level',
        message: viewerConfig && viewerConfig.message_field || 'message'
    };
}

// Check if entry matches a filter query
function matchesLogFilter(entry, query) {
    if (!query) return true;

    const parts = query.toLowerCase().split(/\s+/);
    for (const part of parts) {
        if (!part) continue;

        if (part.startsWith('source:')) {
            const val = part.substring(7);
            if (!entry.source || !entry.source.toLowerCase().includes(val)) {
                return false;
            }
        } else if (part.startsWith('level:')) {
            const val = part.substring(6);
            if (!entry.level || !entry.level.toLowerCase().includes(val)) {
                return false;
            }
        } else {
            // Search in message, raw, and fields
            const msg = (entry.message || '').toLowerCase();
            const raw = (entry.raw || '').toLowerCase();
            let fieldsMatch = false;
            if (entry.fields) {
                for (const key in entry.fields) {
                    if (String(entry.fields[key]).toLowerCase().includes(part)) {
                        fieldsMatch = true;
                        break;
                    }
                }
            }
            if (!msg.includes(part) && !raw.includes(part) && !fieldsMatch) {
                return false;
            }
        }
    }
    return true;
}
