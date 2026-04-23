// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0
//
// Trellis SPA navigation controller.
//
// Intercepts same-origin link clicks and navigates by fetching the target URL,
// extracting its .page-container element from the response HTML, and swapping
// it into the current document without a full reload. When the user navigates
// away, the outgoing container is detached and held in an LRU cache keyed by
// URL; returning to that URL re-attaches the cached DOM, preserving scroll
// position, WebSocket connections, intervals, and any other script state.
//
// Pages opt in by rendering their content inside a <div class="page-container">
// element (the BasePage Header emits one automatically). Inline scripts inside
// the container are expected to be wrapped in IIFEs and to expose any
// onclick-referenced handlers via `window.foo = ...` so they survive the
// navigation/re-render cycle without top-level-declaration conflicts.
//
// Lifecycle events dispatched on the container:
//   - trellis:page-entered  — fired when the page is shown (first render or
//                             restored from cache). detail.restored is true
//                             when restored from cache.
//   - trellis:page-leaving  — fired just before the page is detached into
//                             the cache. detail.nextUrl is the target URL.
//   - trellis:page-evicted  — fired when a cached page is evicted from LRU.
//                             The container is about to be discarded; stop any
//                             running WebSockets / timers here.
//
(function() {
    'use strict';

    var LRU_MAX = 8;

    // Active page container currently in the DOM.
    var currentContainer = null;
    var currentUrl = null;
    // Cache: URL -> { container, scrollTop }. Map preserves insertion order
    // for LRU eviction.
    var cache = new Map();
    var navigating = false;

    function normalizeUrl(url) {
        try {
            var u = new URL(url, window.location.origin);
            if (u.origin !== window.location.origin) return null;
            return u.pathname + u.search;
        } catch (e) {
            return null;
        }
    }

    function dispatch(target, name, detail) {
        if (!target) return;
        target.dispatchEvent(new CustomEvent(name, { detail: detail || {}, bubbles: true }));
    }

    function findContainerIn(root) {
        return root.querySelector('.page-container');
    }

    function mainEl() {
        return document.querySelector('main') || document.body;
    }

    function getScrollableAncestor(el) {
        var node = el;
        while (node && node !== document.body) {
            var style = window.getComputedStyle(node);
            if ((style.overflowY === 'auto' || style.overflowY === 'scroll') &&
                node.scrollHeight > node.clientHeight) {
                return node;
            }
            node = node.parentElement;
        }
        return null;
    }

    function init() {
        currentContainer = document.querySelector('.page-container');
        if (!currentContainer) {
            // SPA disabled for pages without a container; normal link behavior.
            return;
        }
        currentUrl = window.location.pathname + window.location.search;
        currentContainer.dataset.url = currentContainer.dataset.url || currentUrl;
        // Fire page-entered for the initial server-rendered page so scripts
        // can hook in with the same API they'll use for SPA-restored pages.
        dispatch(currentContainer, 'trellis:page-entered', { restored: false, firstLoad: true });

        document.addEventListener('click', onClick, false);
        window.addEventListener('popstate', onPopState, false);
    }

    function onClick(e) {
        if (e.defaultPrevented) return;
        if (e.button !== 0) return;
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;

        var a = e.target.closest('a');
        if (!a || !a.href) return;
        if (a.target && a.target !== '_self') return;
        if (a.hasAttribute('download')) return;
        if (a.dataset.noSpa === 'true') return;
        // Let query-param-only fragment navigation fall through.
        var target = normalizeUrl(a.href);
        if (!target) return;
        // Fragment-only links on the same page: let the browser scroll.
        var current = window.location.pathname + window.location.search;
        if (target === current) return;
        // If the CURRENT page has opted out of SPA (e.g. terminal mode), let
        // the browser do a full navigation.
        if (currentContainer && currentContainer.dataset.noSpa === 'true') return;

        e.preventDefault();
        navigate(target, { push: true });
    }

    function onPopState() {
        var target = window.location.pathname + window.location.search;
        if (target === currentUrl) return;
        navigate(target, { push: false });
    }

    function navigate(url, opts) {
        if (navigating) return;
        if (url === currentUrl) return;
        navigating = true;

        var leavingContainer = currentContainer;
        var leavingUrl = currentUrl;
        if (leavingContainer) {
            dispatch(leavingContainer, 'trellis:page-leaving', { nextUrl: url });
            cachePage(leavingUrl, leavingContainer);
        }

        var cached = cache.get(url);
        if (cached) {
            cache.delete(url);
            // pushState before mount so window.location.pathname reflects
            // the new URL by the time `trellis:page-entered` fires.
            if (opts.push) history.pushState({ spa: true }, '', url);
            mountContainer(cached.container, url, { restored: true, scrollTop: cached.scrollTop, title: cached.title });
            navigating = false;
            return;
        }

        fetchAndMount(url, opts.push);
    }

    function cachePage(url, container) {
        if (!url || !container) return;
        var scrollable = getScrollableAncestor(container) || document.scrollingElement || document.documentElement;
        var scrollTop = scrollable.scrollTop;
        container.remove();
        cache.set(url, { container: container, scrollTop: scrollTop, title: document.title });
        while (cache.size > LRU_MAX) {
            var oldest = cache.keys().next().value;
            var evicted = cache.get(oldest);
            cache.delete(oldest);
            if (evicted && evicted.container) {
                dispatch(evicted.container, 'trellis:page-evicted', { url: oldest });
            }
        }
    }

    function mountContainer(container, url, info) {
        var main = mainEl();
        // Remove any stale container left in DOM (defensive).
        var existing = main.querySelector('.page-container');
        if (existing && existing !== container) existing.remove();

        main.appendChild(container);
        currentContainer = container;
        currentUrl = url;
        if (info && info.title) document.title = info.title;
        if (info && typeof info.scrollTop === 'number') {
            // Restore scroll after layout settles. window scroll covers the
            // most common case; inner scroll containers restore their own
            // scrollTop via the cached DOM state.
            requestAnimationFrame(function() {
                window.scrollTo(0, info.scrollTop);
            });
        } else {
            window.scrollTo(0, 0);
        }
        dispatch(container, 'trellis:page-entered', {
            restored: !!(info && info.restored),
            firstLoad: !(info && info.restored)
        });
    }

    function fetchAndMount(url, push) {
        fetch(url, {
            credentials: 'same-origin',
            headers: { 'X-Trellis-SPA': '1' }
        }).then(function(resp) {
            if (!resp.ok) throw new Error('HTTP ' + resp.status);
            return resp.text();
        }).then(function(html) {
            var parser = new DOMParser();
            var doc = parser.parseFromString(html, 'text/html');
            var fetched = findContainerIn(doc);
            if (!fetched) {
                // Target doesn't have a .page-container — fall back to full nav.
                window.location.href = url;
                return;
            }
            if (fetched.dataset && fetched.dataset.noSpa === 'true') {
                // Target explicitly opted out of SPA. Full nav.
                window.location.href = url;
                return;
            }
            if (doc.title) document.title = doc.title;

            // Clone into the live document so scripts can execute.
            var adopted = document.importNode(fetched, true);
            // Remove the leaving container left in cache? No — it's already
            // detached. Just mount the new one.
            var main = mainEl();
            main.appendChild(adopted);
            // Scripts inside imported nodes don't execute automatically — we
            // must re-inject each <script> so the browser runs it.
            reexecuteScripts(adopted);

            currentContainer = adopted;
            currentUrl = url;

            if (push) history.pushState({ spa: true }, '', url);
            window.scrollTo(0, 0);
            dispatch(adopted, 'trellis:page-entered', { restored: false, firstLoad: true });
            navigating = false;
        }).catch(function(err) {
            console.warn('[spa] fetch failed, falling back to full nav:', err);
            navigating = false;
            window.location.href = url;
        });
    }

    // Replace each <script> in-place with a freshly-created <script> node so
    // the browser parses and executes it. innerHTML-inserted scripts are
    // inert by design; this makes dynamically-inserted pages behave as if
    // the HTML had been parsed normally.
    //
    // Dynamically-inserted <script src="..."> tags default to async, meaning
    // they execute whenever they finish loading — not in DOM order. That
    // breaks pages that rely on script order (e.g. claude.js assumes the
    // `marked` global from a preceding CDN script is already defined).
    // Setting `.async = false` on each fresh script restores the "execute in
    // insertion order" semantics.
    function reexecuteScripts(root) {
        var scripts = Array.prototype.slice.call(root.querySelectorAll('script'));
        scripts.forEach(function(old) {
            var fresh = document.createElement('script');
            for (var i = 0; i < old.attributes.length; i++) {
                var attr = old.attributes[i];
                fresh.setAttribute(attr.name, attr.value);
            }
            fresh.async = false;
            fresh.textContent = old.textContent;
            old.parentNode.replaceChild(fresh, old);
        });
    }

    // Expose for debugging / programmatic navigation.
    window.TrellisSPA = {
        navigate: function(url) { return navigate(normalizeUrl(url) || url, { push: true }); },
        cache: cache,
        current: function() { return { url: currentUrl, container: currentContainer }; }
    };

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
