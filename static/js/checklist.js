// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// checklist.js — front-end controller for the phased-checklist outer loop
// (see PHASE_LOOP_SPEC.md). Loaded by both claude.js and codex.js. It detects
// the agent page, exposes a "Start checklist run" item in the session toolbar
// drop-up, and renders a banner with phase progress + controls for the active
// run.
//
// A run repeatedly prompts the implementer to "implement the next phase", has
// a paired review loop (pair.js) review each phase, and stops when the
// implementer replies with the completion signal (default COMPLETED). All
// state round-trips through /api/v1/checklist; the WebSocket at
// /api/v1/checklist/ws is read-only and drives live banner updates.

(function () {
  'use strict';

  // The SPA (spa.js) swaps .page-container elements in and out of the DOM and
  // re-executes this script for every freshly fetched session page. Everything
  // this instance touches — its banner, its chat container, its identity —
  // must be scoped to the container it was loaded in. Anything attached to
  // document.body would survive navigation and leak onto other pages.
  const pageContainer = document.currentScript && document.currentScript.closest('.page-container');

  function detectAgent() {
    // Preferred source: data attributes stamped on the chat container by
    // claude.qtpl / codex.qtpl. Unlike window.CLAUDE_SESSION / CODEX_SESSION,
    // these can't go stale when the SPA swaps to another session's page.
    const chat = pageContainer && pageContainer.querySelector('.claude-chat-container, .codex-chat-container');
    if (chat && chat.dataset.session) {
      return { agent: chat.dataset.agent, session: chat.dataset.session, worktree: chat.dataset.worktree };
    }
    if (window.CLAUDE_SESSION) return { agent: 'claude', session: window.CLAUDE_SESSION, worktree: window.CLAUDE_WORKTREE };
    if (window.CODEX_SESSION) return { agent: 'codex', session: window.CODEX_SESSION, worktree: window.CODEX_WORKTREE };
    return null;
  }

  const me = detectAgent();
  if (!me) return; // not on a session page

  // Documented defaults (mirror checklist.DefaultConfig in Go) so the modal
  // shows editable text rather than blanks.
  const DEFAULTS = {
    advance_prompt: 'Implement the next phase from the checklist. Make only the changes for that ' +
      'one phase, then stop and wait for review. If there are no phases left to implement, reply ' +
      'with exactly COMPLETED on its own line and nothing else.',
    completion_signal: 'COMPLETED',
    review_prompt: 'Review the implementer\'s work on the current phase. If it fully and correctly ' +
      'satisfies that phase, reply with LGTM on its own line. Otherwise, give specific, actionable feedback.',
    feedback_prompt: 'Feedback:',
    review_stop_signal: 'LGTM',
    max_rounds: 10,
  };

  // ---------- DOM helpers ----------

  function el(tag, props, ...children) {
    const e = document.createElement(tag);
    if (props) {
      for (const [k, v] of Object.entries(props)) {
        if (k === 'class') e.className = v;
        else if (k === 'style') e.style.cssText = v;
        else if (k.startsWith('on') && typeof v === 'function') e.addEventListener(k.slice(2), v);
        else if (k === 'html') e.innerHTML = v;
        else e.setAttribute(k, v);
      }
    }
    for (const c of children) {
      if (c == null) continue;
      e.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
    }
    return e;
  }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, ch => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
    }[ch]));
  }

  // ---------- API ----------

  function unwrap(j) {
    if (j && typeof j === 'object' && 'data' in j && 'meta' in j) return j.data;
    return j;
  }

  async function api(method, path, body) {
    const opts = { method, headers: { 'Content-Type': 'application/json' } };
    if (body !== undefined) opts.body = JSON.stringify(body);
    const r = await fetch(path, opts);
    if (!r.ok) {
      const txt = await r.text();
      throw new Error(txt || ('HTTP ' + r.status));
    }
    if (r.status === 204) return null;
    return unwrap(await r.json());
  }

  async function fetchActiveRun() {
    try {
      return await api('GET', '/api/v1/checklist/by-session/' + encodeURIComponent(me.session));
    } catch (e) {
      return null;
    }
  }

  async function fetchInboxSessions() {
    const r = await fetch('/api/v1/inbox/sessions');
    if (!r.ok) return [];
    return unwrap(await r.json()) || [];
  }

  const sessionNameCache = Object.create(null);
  async function refreshSessionNameCache() {
    try {
      const sessions = await fetchInboxSessions();
      for (const s of sessions) {
        if (s && s.id && s.display_name) sessionNameCache[s.id] = s.display_name;
      }
    } catch (e) {}
  }

  function partnerLabel(ref) {
    const name = sessionNameCache[ref.session_id];
    if (name) return name + ' (' + ref.agent + ')';
    return ref.session_id.slice(0, 8) + '… (' + ref.agent + ')';
  }

  // ---------- Banner ----------

  let bannerEl = null;
  let currentRun = null;

  function ensureBannerContainer() {
    if (bannerEl) return bannerEl;
    // Blue-tinted, distinct from pair.js's amber banner. Inserted at the top
    // of this page's container so the banner navigates in and out with the
    // page, always above the pair banner if one is showing.
    bannerEl = el('div', {
      id: 'checklist-banner',
      style: 'position:sticky;top:0;z-index:1021;padding:10px 14px;' +
        'border-bottom:1px solid #0d6efd;' +
        'background:rgba(13, 110, 253, 0.12);' +
        'color:var(--bs-body-color, #222);' +
        'display:none;font-size:14px;'
    });
    const root = pageContainer || document.body;
    const pairBanner = root.querySelector('#pair-banner');
    if (pairBanner) root.insertBefore(bannerEl, pairBanner);
    else root.insertBefore(bannerEl, root.firstChild);
    return bannerEl;
  }

  function findChatContainer() {
    return (pageContainer || document).querySelector('.claude-chat-container, .codex-chat-container');
  }

  // Shared with pair.js: shrink the chat container by the combined height of
  // every session banner on this page so the input strip stays on-screen.
  function totalTrellisBannerHeight() {
    const root = pageContainer || document;
    let h = 0;
    ['#checklist-banner', '#pair-banner'].forEach(function (sel) {
      const b = root.querySelector(sel);
      if (b && b.style.display !== 'none') h += b.offsetHeight;
    });
    return h;
  }

  function applyChatContainerOffset() {
    const c = findChatContainer();
    if (!c) return;
    const px = totalTrellisBannerHeight();
    if (!px) { c.style.removeProperty('height'); return; }
    c.style.cssText += ';height: calc(100vh - 120px - ' + px + 'px);' +
                       'height: calc(100dvh - 120px - ' + px + 'px);';
  }

  function isParticipant(run) {
    return !!run &&
      (run.implementer.session_id === me.session || run.reviewer.session_id === me.session);
  }

  const STEP_LABEL = {
    'pending': 'starting…',
    'advance': 'starting next phase…',
    'probe': 'implementing phase…',
    'review': 'under review',
  };

  const PAUSE_LABEL = {
    'manual': 'paused',
    'phase_not_converged': 'phase hit its round cap',
    'pair_stopped': 'review pair stopped',
    'review_start_failed': 'could not start review',
  };

  function renderBanner() {
    const banner = ensureBannerContainer();
    if (!currentRun || currentRun.state === 'stopped' || !isParticipant(currentRun)) {
      banner.style.display = 'none';
      banner.innerHTML = '';
      applyChatContainerOffset();
      return;
    }
    banner.style.display = 'block';

    const iAmImplementer = currentRun.implementer.session_id === me.session;
    const partner = iAmImplementer ? currentRun.reviewer : currentRun.implementer;
    const myRole = iAmImplementer ? 'Implementer' : 'Reviewer';
    const done = currentRun.phases_done || 0;
    const stepLabel = STEP_LABEL[currentRun.step] || currentRun.state;
    const phaseNum = done + 1; // the phase currently being worked/reviewed

    let statusText = 'Phase ' + phaseNum + ' · ' + stepLabel + ' · ' + done + ' done';
    if (currentRun.state === 'paused') {
      statusText += ' · ' + (PAUSE_LABEL[currentRun.paused_reason] || 'paused');
    }

    banner.innerHTML = '';
    banner.appendChild(el('div', null,
      el('strong', null, '📋 Checklist run '),
      el('span', { html: '&nbsp;·&nbsp;with ' }),
      el('a', { href: '/' + partner.agent + '/' + encodeURIComponent(partner.worktree) + '/' + encodeURIComponent(partner.session_id) }, partnerLabel(partner)),
      el('span', { html: '&nbsp;·&nbsp;' + escapeHTML(myRole) + '&nbsp;·&nbsp;' + escapeHTML(statusText) })
    ));

    const buttons = el('div', { style: 'margin-top:6px;' });
    function actionBtn(label, op, danger) {
      const cls = 'btn btn-sm ' + (danger ? 'btn-outline-danger' : 'btn-outline-secondary');
      return el('button', { class: cls, style: 'margin-right:6px;', onclick: () => doAction(op) }, label);
    }

    if (currentRun.state === 'running') {
      buttons.appendChild(actionBtn('Pause', 'pause'));
      buttons.appendChild(actionBtn('Skip phase', 'skip'));
    } else if (currentRun.state === 'paused') {
      buttons.appendChild(actionBtn('Resume', 'resume'));
      buttons.appendChild(actionBtn('Retry phase', 'retry'));
      buttons.appendChild(actionBtn('Skip phase', 'skip'));
    }
    buttons.appendChild(actionBtn('Stop', 'stop', true));
    banner.appendChild(buttons);

    applyChatContainerOffset();
  }

  async function doAction(op) {
    if (!currentRun) return;
    if (op === 'stop' && !confirm('Stop this checklist run?')) return;
    try {
      const updated = await api('POST', '/api/v1/checklist/' + encodeURIComponent(currentRun.id) + '/' + op);
      currentRun = updated;
      renderBanner();
    } catch (e) {
      alert('Action failed: ' + e.message);
    }
  }

  // ---------- WebSocket ----------

  let ws = null;
  let wsReconnectTimer = null;
  let wsShutdown = false; // set when the SPA evicts this page; stops reconnects

  function connectWS() {
    if (wsShutdown) return;
    if (ws) try { ws.close(); } catch (e) {}
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    ws = new WebSocket(proto + '://' + location.host + '/api/v1/checklist/ws?session_id=' + encodeURIComponent(me.session));
    ws.onmessage = async () => {
      // Any checklist.* event we receive matched our filter, so it's for our
      // run — re-fetch to keep state authoritative.
      const [fresh] = await Promise.all([fetchActiveRun(), refreshSessionNameCache()]);
      currentRun = fresh;
      renderBanner();
    };
    ws.onclose = () => {
      clearTimeout(wsReconnectTimer);
      if (wsShutdown) return;
      wsReconnectTimer = setTimeout(connectWS, 3000);
    };
  }

  // When the SPA discards this page from its LRU cache the DOM is gone for
  // good — shut the socket down so evicted instances don't pile up
  // connections and reconnect timers in the background.
  if (pageContainer) {
    pageContainer.addEventListener('trellis:page-evicted', function () {
      wsShutdown = true;
      clearTimeout(wsReconnectTimer);
      if (ws) try { ws.close(); } catch (e) {}
    });
  }

  // ---------- Toolbar item ----------

  function injectToolbarButton() {
    // Anchor on the "Wrap up" item and append a matching dropdown-item to the
    // session toolbar drop-up (see claude.qtpl / codex.qtpl). Scoped to this
    // page's container so a cached page keeps its own button and a fresh page
    // gets its own.
    const root = pageContainer || document;
    const wrapUp = root.querySelector('[onclick*="showCommitModal(\'wrapup\')"]');
    if (!wrapUp || root.querySelector('#checklist-toolbar-btn')) return;
    const menu = wrapUp.closest('ul.dropdown-menu');
    if (!menu) return;
    const item = el('button', {
      id: 'checklist-toolbar-btn',
      type: 'button',
      class: 'dropdown-item',
      onclick: openCreateModal,
    });
    item.appendChild(el('i', { class: 'fa-solid fa-list-check fa-fw' }));
    item.appendChild(document.createTextNode(' Start checklist run'));
    menu.appendChild(el('li', null, item));
  }

  // ---------- Modal scaffolding (mirrors pair.js) ----------

  function openModal(opts) {
    const backdrop = el('div', {
      style: 'position:fixed;inset:0;background:rgba(0,0,0,0.5);z-index:1050;display:flex;align-items:center;justify-content:center;'
    });
    const dialog = el('div', {
      style: 'background:var(--trellis-modal-bg, #fff);color:var(--bs-body-color, #222);' +
        'max-width:640px;width:90%;max-height:90vh;overflow:auto;' +
        'border:1px solid var(--trellis-card-border, transparent);' +
        'border-radius:8px;box-shadow:0 8px 24px rgba(0,0,0,0.35);'
    });
    dialog.appendChild(el('div', {
      style: 'padding:14px 16px;border-bottom:1px solid var(--trellis-card-border, #eee);font-weight:600;',
    }, opts.title));
    const bodyWrap = el('div', { style: 'padding:14px 16px;' });
    bodyWrap.appendChild(opts.body);
    dialog.appendChild(bodyWrap);
    const footer = el('div', {
      style: 'padding:10px 16px;border-top:1px solid var(--trellis-card-border, #eee);text-align:right;',
    });
    (Array.isArray(opts.footer) ? opts.footer : [opts.footer]).forEach(b => { if (b) footer.appendChild(b); });
    dialog.appendChild(footer);
    backdrop.appendChild(dialog);

    function close() { backdrop.remove(); }
    backdrop.addEventListener('click', (e) => { if (e.target === backdrop) close(); });
    document.body.appendChild(backdrop);
    return { close, dialog };
  }

  const LABEL_STYLE = 'display:block;font-size:13px;color:var(--bs-secondary-color, #555);margin-bottom:3px;';
  const HELP_STYLE = 'font-size:12px;color:var(--bs-tertiary-color, #888);margin-top:3px;';

  function input(opts) {
    const wrapper = el('div', { style: 'margin-bottom:10px;' });
    if (opts.label) wrapper.appendChild(el('label', { style: LABEL_STYLE }, opts.label));
    const i = el('input', { type: opts.type || 'text', class: 'form-control', style: 'width:100%;' });
    if (opts.value !== undefined) i.value = opts.value;
    if (opts.placeholder) i.placeholder = opts.placeholder;
    wrapper.appendChild(i);
    if (opts.help) wrapper.appendChild(el('div', { style: HELP_STYLE }, opts.help));
    return { wrapper, input: i };
  }

  function textarea(opts) {
    const wrapper = el('div', { style: 'margin-bottom:10px;' });
    if (opts.label) wrapper.appendChild(el('label', { style: LABEL_STYLE }, opts.label));
    const t = el('textarea', { class: 'form-control', style: 'width:100%;min-height:64px;', rows: opts.rows || 3 });
    if (opts.value !== undefined) t.value = opts.value;
    wrapper.appendChild(t);
    if (opts.help) wrapper.appendChild(el('div', { style: HELP_STYLE }, opts.help));
    return { wrapper, input: t };
  }

  function btn(label, onClick, kind) {
    const cls = 'btn ' + (kind === 'primary' ? 'btn-primary' : kind === 'danger' ? 'btn-danger' : 'btn-secondary');
    return el('button', { class: cls, style: 'margin-left:6px;', onclick: onClick }, label);
  }

  // ---------- Create-run modal ----------

  async function openCreateModal() {
    if (currentRun && currentRun.state !== 'stopped') {
      alert('This session already has an active checklist run.');
      return;
    }

    const allSessions = await fetchInboxSessions();
    const partnerOptions = allSessions
      .filter(s => s.id !== me.session && !s.trashed)
      .sort((a, b) => a.agent.localeCompare(b.agent) || a.display_name.localeCompare(b.display_name));

    if (partnerOptions.length === 0) {
      alert('No other sessions available. Create a reviewer session (Claude or Codex) first.');
      return;
    }

    const remembered = JSON.parse(localStorage.getItem('checklist.defaults') || '{}');
    const cfg0 = Object.assign({}, DEFAULTS, remembered);

    const body = el('div');

    // This session is the implementer by default; the partner reviews. Swap
    // flips the roles (the advance prompt always drives the implementer).
    let currentRole = 'implementer';
    const partnerLabelText = () => currentRole === 'implementer' ? 'Reviewer session' : 'Implementer session';

    const roleP = el('p', { style: 'margin-bottom:10px;' });
    roleP.appendChild(el('strong', null, 'This session: '));
    const roleText = el('span', null, 'Implementer');
    roleP.appendChild(roleText);
    const partnerLbl = el('label', { style: LABEL_STYLE }, partnerLabelText());
    const swapBtn = el('button', { class: 'btn btn-sm btn-outline-secondary', style: 'margin-left:8px;', onclick: () => {
      currentRole = currentRole === 'implementer' ? 'reviewer' : 'implementer';
      roleText.textContent = currentRole === 'implementer' ? 'Implementer' : 'Reviewer';
      partnerLbl.textContent = partnerLabelText();
    } }, 'Swap');
    roleP.appendChild(swapBtn);
    body.appendChild(roleP);

    const partnerWrap = el('div', { style: 'margin-bottom:10px;' });
    partnerWrap.appendChild(partnerLbl);
    const partnerSel = el('select', { class: 'form-control', style: 'width:100%;' });
    for (const s of partnerOptions) {
      partnerSel.appendChild(el('option', { value: s.id }, '[' + s.agent.toUpperCase() + '] ' + s.display_name + ' — ' + s.worktree));
    }
    partnerWrap.appendChild(partnerSel);
    body.appendChild(partnerWrap);

    const advancePrompt = textarea({
      label: 'Advance prompt (sent to the implementer each phase)',
      value: cfg0.advance_prompt,
      rows: 4,
      help: 'Must tell the implementer to reply with the completion signal when no phases remain.',
    });
    body.appendChild(advancePrompt.wrapper);

    const completionSignal = input({
      label: 'Completion signal (implementer says this when done — matched as a line on its own)',
      value: cfg0.completion_signal,
    });
    body.appendChild(completionSignal.wrapper);

    const reviewPrompt = textarea({
      label: 'Review prompt (sent to the reviewer each phase)',
      value: cfg0.review_prompt,
      rows: 3,
    });
    body.appendChild(reviewPrompt.wrapper);

    const feedbackPrompt = input({
      label: 'Feedback prompt (prefix sent back to the implementer)',
      value: cfg0.feedback_prompt,
    });
    body.appendChild(feedbackPrompt.wrapper);

    const reviewStopSignal = input({
      label: 'Review stop signal (reviewer says this to approve a phase)',
      value: cfg0.review_stop_signal,
    });
    body.appendChild(reviewStopSignal.wrapper);

    const maxRounds = input({
      label: 'Max rounds per phase',
      type: 'number',
      value: cfg0.max_rounds,
      help: 'If a phase hits this without approval, the run pauses for you to retry, skip, or stop.',
    });
    body.appendChild(maxRounds.wrapper);

    let modal;
    const cancelBtn = btn('Cancel', () => modal.close());
    const startBtn = btn('Start run', async () => {
      const selectedPartner = partnerOptions.find(s => s.id === partnerSel.value);
      if (!selectedPartner) return;
      const mePartial = { agent: me.agent, worktree: me.worktree, session_id: me.session };
      const partnerPartial = { agent: selectedPartner.agent, worktree: selectedPartner.worktree, session_id: selectedPartner.id };
      const implementer = currentRole === 'implementer' ? mePartial : partnerPartial;
      const reviewer = currentRole === 'implementer' ? partnerPartial : mePartial;

      const cfg = {
        implementer,
        reviewer,
        advance_prompt: advancePrompt.input.value,
        completion_signal: completionSignal.input.value,
        review_prompt: reviewPrompt.input.value,
        feedback_prompt: feedbackPrompt.input.value,
        review_stop_signal: reviewStopSignal.input.value,
        max_rounds: parseInt(maxRounds.input.value, 10) || 10,
      };
      try {
        const run = await api('POST', '/api/v1/checklist', cfg);
        currentRun = run;
        localStorage.setItem('checklist.defaults', JSON.stringify({
          advance_prompt: cfg.advance_prompt,
          completion_signal: cfg.completion_signal,
          review_prompt: cfg.review_prompt,
          feedback_prompt: cfg.feedback_prompt,
          review_stop_signal: cfg.review_stop_signal,
          max_rounds: cfg.max_rounds,
        }));
        renderBanner();
        modal.close();
      } catch (e) {
        alert('Failed to start run: ' + e.message);
      }
    }, 'primary');

    modal = openModal({ title: 'Start a checklist run', body, footer: [cancelBtn, startBtn] });
  }

  // ---------- Init ----------

  async function init() {
    injectToolbarButton();
    const [run] = await Promise.all([fetchActiveRun(), refreshSessionNameCache()]);
    currentRun = run;
    renderBanner();
    connectWS();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
