// Shared marked configuration, loaded on every page that renders Markdown
// (claude.qtpl, codex.qtpl, case_detail.qtpl) immediately after marked.min.js
// and before any page script that calls marked.parse().
(function () {
    'use strict';

    if (typeof marked === 'undefined') return;

    // marked's GFM strikethrough rule is /^(~~?).../ — it accepts a SINGLE
    // tilde as a delimiter, so prose using `~` to mean "approximately" gets
    // mangled. "averages ~10% ... peak of ~17% (~5.5GB" opens del at the first
    // tilde and closes it at the third, striking out everything between:
    //   "10% of 32GB with a 30-day peak of ~17% (".
    // Agent transcripts are full of ~ (approximate sizes, timings, percentages,
    // home paths), so this fired constantly. Require ~~ for strikethrough;
    // a lone ~ stays literal text.
    //
    // NOTE: this tokenizer must return undefined (not false) to decline —
    // marked.use() treats a `false` return as "fall through to the built-in
    // tokenizer", which would reinstate the single-tilde behavior.
    var DEL_RE = /^~~(?=[^\s~])((?:\\.|[^\\])*?(?:\\.|[^\s~\\]))~~(?=[^~]|$)/;

    marked.use({
        tokenizer: {
            del: function (src) {
                var cap = DEL_RE.exec(src);
                if (!cap) return;
                return {
                    type: 'del',
                    raw: cap[0],
                    text: cap[1],
                    tokens: this.lexer.inlineTokens(cap[1])
                };
            }
        }
    });
})();
