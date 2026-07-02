// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// pair.js — front-end controller for the paired-review-loop feature.
// Loaded by both claude.js and codex.js. Detects which agent page it is on
// via window.CLAUDE_SESSION / window.CODEX_SESSION, queries for an active
// pair, renders a banner with controls, and exposes a "Pair for Review"
// button on the page toolbar.
//
// All state changes round-trip through the backend /api/v1/pair endpoints;
// the WebSocket at /api/v1/pair/ws is read-only and used purely for live
// banner updates.

(function () {
  'use strict';

  function detectAgent() {
    if (window.CLAUDE_SESSION) return { agent: 'claude', session: window.CLAUDE_SESSION, worktree: window.CLAUDE_WORKTREE };
    if (window.CODEX_SESSION) return { agent: 'codex', session: window.CODEX_SESSION, worktree: window.CODEX_WORKTREE };
    return null;
  }

  const me = detectAgent();
  if (!me) return; // not on a session page

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

  // All API responses go through handlers.WriteJSON which wraps the body in
  // { data, meta }. Peel the wrapper so callers see the raw payload.
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

  async function fetchActivePair() {
    try {
      return await api('GET', '/api/v1/pair/by-session/' + encodeURIComponent(me.session));
    } catch (e) {
      return null;
    }
  }

  async function fetchInboxSessions() {
    const r = await fetch('/api/v1/inbox/sessions');
    if (!r.ok) return [];
    return unwrap(await r.json()) || [];
  }

  // sessionNameCache maps session_id -> display_name. Populated from the
  // inbox sessions endpoint so the banner can show the partner's name
  // instead of a truncated UUID. Refreshed every time we refetch the pair
  // so renames are reflected promptly.
  const sessionNameCache = Object.create(null);

  async function refreshSessionNameCache() {
    try {
      const sessions = await fetchInboxSessions();
      for (const s of sessions) {
        if (s && s.id && s.display_name) sessionNameCache[s.id] = s.display_name;
      }
    } catch (e) {}
  }

  function partnerLabel(partner) {
    const name = sessionNameCache[partner.session_id];
    if (name) return name + ' (' + partner.agent + ')';
    return partner.session_id.slice(0, 8) + '… (' + partner.agent + ')';
  }

  // ---------- Banner ----------

  let bannerEl = null;
  let currentPair = null;

  function ensureBannerContainer() {
    if (bannerEl) return bannerEl;
    // Mirrors the .alert-warning styling from theme.css (translucent amber
    // over body bg) so it stands out in both light and dark modes.
    bannerEl = el('div', {
      id: 'pair-banner',
      style: 'position:sticky;top:0;z-index:1020;padding:10px 14px;' +
        'border-bottom:1px solid #ffc107;' +
        'background:rgba(255, 193, 7, 0.15);' +
        'color:var(--bs-body-color, #222);' +
        'display:none;font-size:14px;'
    });
    // Insert at the very top of the body so it spans the whole page.
    document.body.insertBefore(bannerEl, document.body.firstChild);
    return bannerEl;
  }

  // findChatContainer returns the page's chat container, whose height needs
  // to shrink by the banner height to keep the input strip on-screen.
  function findChatContainer() {
    return document.querySelector('.claude-chat-container, .codex-chat-container');
  }

  // totalTrellisBannerHeight sums the visible heights of every top-of-page
  // session banner (pair + checklist). A checklist run's review phase is a
  // pair, so both banners can be on-screen at once; the chat must shrink by
  // their combined height. checklist.js runs the same computation, so
  // whichever renders last still arrives at the correct total.
  function totalTrellisBannerHeight() {
    let h = 0;
    ['checklist-banner', 'pair-banner'].forEach(function (id) {
      const b = document.getElementById(id);
      if (b && b.style.display !== 'none') h += b.offsetHeight;
    });
    return h;
  }

  // applyChatContainerOffset re-sizes the chat container by subtracting the
  // combined banner height from the height the page CSS would otherwise
  // compute. Inline-style specificity beats the class rule, so this reliably
  // wins regardless of media-query order or cascade subtleties.
  function applyChatContainerOffset() {
    const c = findChatContainer();
    if (!c) return;
    const bannerPx = totalTrellisBannerHeight();
    if (!bannerPx) {
      c.style.removeProperty('height');
      return;
    }
    // Mirror the original page rule (calc(100vh - 120px) / 100dvh) and just
    // subtract the banners. The second declaration wins where dvh is
    // supported, the first is the legacy fallback.
    c.style.cssText += ';height: calc(100vh - 120px - ' + bannerPx + 'px);' +
                       'height: calc(100dvh - 120px - ' + bannerPx + 'px);';
  }

  function renderBanner() {
    const banner = ensureBannerContainer();
    // Defensive participant check: even if the server hands back a pair
    // record, only render the banner when this session is actually one of
    // the two participants. Without this, a stale fetch or a server-side
    // bug could surface another pair's banner on an unrelated session.
    const isParticipant = !!currentPair &&
      (currentPair.implementer.session_id === me.session ||
       currentPair.reviewer.session_id === me.session);
    if (!currentPair || currentPair.state === 'stopped' || !isParticipant) {
      banner.style.display = 'none';
      banner.innerHTML = '';
      applyChatContainerOffset();
      return;
    }
    banner.style.display = 'block';

    const partner = currentPair.implementer.session_id === me.session ? currentPair.reviewer : currentPair.implementer;
    const myRole = currentPair.implementer.session_id === me.session ? 'Implementer' : 'Reviewer';
    const stepLabel = ({
      'pending': 'starting…',
      'await_implementer': currentPair.implementer.session_id === me.session ? 'awaiting this side' : 'waiting for partner',
      'await_reviewer': currentPair.reviewer.session_id === me.session ? 'awaiting this side' : 'waiting for partner',
      'relay_to_reviewer': 'relaying…',
      'relay_to_implementer': 'relaying…',
      'confirm_relay': 'awaiting your approval',
    })[currentPair.step] || currentPair.state;

    const stateBadge = currentPair.state === 'paused' ? ' · <strong>paused</strong>' : '';
    const round = currentPair.round_count || 0;
    const max = (currentPair.config && currentPair.config.max_rounds) || 10;

    banner.innerHTML = '';
    banner.appendChild(el('div', null,
      el('strong', null, '🔗 Paired with '),
      el('a', { href: '/' + partner.agent + '/' + encodeURIComponent(partner.worktree) + '/' + encodeURIComponent(partner.session_id) }, partnerLabel(partner)),
      el('span', { html: '&nbsp;·&nbsp;' + escapeHTML(myRole) + '&nbsp;·&nbsp;Round ' + round + ' / ' + max + '&nbsp;·&nbsp;' + escapeHTML(stepLabel) + stateBadge })
    ));

    const buttons = el('div', { style: 'margin-top:6px;' });
    function actionBtn(label, fn, danger) {
      const cls = 'btn btn-sm ' + (danger ? 'btn-outline-danger' : 'btn-outline-secondary');
      return el('button', { class: cls, style: 'margin-right:6px;', onclick: fn }, label);
    }

    if (currentPair.state === 'running') {
      buttons.appendChild(actionBtn('Pause', () => doAction('pause')));
    } else if (currentPair.state === 'paused') {
      buttons.appendChild(actionBtn('Resume', () => doAction('resume')));
    }
    buttons.appendChild(actionBtn('Stop', () => doAction('stop'), true));
    buttons.appendChild(actionBtn('Force relay', () => doAction('force-relay')));
    buttons.appendChild(actionBtn('Settings', openSettingsModal));
    if (currentPair.step === 'confirm_relay' && currentPair.pending_confirm) {
      buttons.appendChild(actionBtn('Review pending relay…', openConfirmModal));
    }
    banner.appendChild(buttons);

    // Resize the chat container to make room for the banner(s). Reading
    // offsetHeight inside the helper forces synchronous layout so we get the
    // post-render value of a banner whose display just flipped to block.
    applyChatContainerOffset();
  }

  async function doAction(op) {
    if (!currentPair) return;
    try {
      const updated = await api('POST', '/api/v1/pair/' + encodeURIComponent(currentPair.id) + '/' + op);
      currentPair = updated;
      renderBanner();
    } catch (e) {
      alert('Action failed: ' + e.message);
    }
  }

  // followToActiveSession navigates this window to whichever side just
  // received a relay and is now generating. The browser only leaves the
  // current page if a different session is the target — otherwise we're
  // already in the right place.
  function followToActiveSession(p, ev) {
    const dir = ev && ev.payload && ev.payload.direction;
    if (!dir) return;
    const target = dir === 'to_reviewer' ? p.reviewer : p.implementer;
    if (!target || !target.session_id) return;
    if (target.session_id === me.session) return;
    const url = '/' + encodeURIComponent(target.agent) +
                '/' + encodeURIComponent(target.worktree) +
                '/' + encodeURIComponent(target.session_id);
    window.location.href = url;
  }

  // ---------- WebSocket ----------

  let ws = null;
  let wsReconnectTimer = null;

  function connectWS() {
    if (ws) try { ws.close(); } catch (e) {}
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    ws = new WebSocket(proto + '://' + location.host + '/api/v1/pair/ws?session_id=' + encodeURIComponent(me.session));
    ws.onmessage = async (evt) => {
      try {
        const ev = JSON.parse(evt.data);
        // Every pair.* event we receive matched our filter so it's for us.
        // Re-fetch the pair (and refresh the partner-name cache) to keep
        // state authoritative.
        const [fresh] = await Promise.all([fetchActivePair(), refreshSessionNameCache()]);
        currentPair = fresh;
        renderBanner();
        // On a fresh round, follow the action: navigate to whichever side
        // just received the relay and is now generating.
        if (ev.type === 'pair.round' && fresh) {
          followToActiveSession(fresh, ev);
        }
      } catch (e) {}
    };
    ws.onclose = () => {
      clearTimeout(wsReconnectTimer);
      wsReconnectTimer = setTimeout(connectWS, 3000);
    };
  }

  // ---------- Pair-for-Review button ----------

  function injectToolbarButton() {
    // The session toolbar is now a drop-up menu (see claude.qtpl / codex.qtpl).
    // Anchor on the "Wrap up" item and append a matching dropdown-item to the
    // same menu.
    const wrapUp = document.querySelector('[onclick*="showCommitModal(\'wrapup\')"]');
    if (!wrapUp || document.getElementById('pair-toolbar-btn')) return;
    const menu = wrapUp.closest('ul.dropdown-menu');
    if (!menu) return;
    const item = el('button', {
      id: 'pair-toolbar-btn',
      type: 'button',
      class: 'dropdown-item',
      onclick: openCreateModal,
    });
    item.appendChild(el('i', { class: 'fa-solid fa-link fa-fw' }));
    item.appendChild(document.createTextNode(' Pair for review'));
    menu.appendChild(el('li', null, item));
  }

  // ---------- Modal scaffolding ----------

  function openModal(opts) {
    // opts: { title, body (Node), footer (Node|Array) }
    const backdrop = el('div', {
      style: 'position:fixed;inset:0;background:rgba(0,0,0,0.5);z-index:1050;display:flex;align-items:center;justify-content:center;'
    });
    const dialog = el('div', {
      style: 'background:var(--trellis-modal-bg, #fff);color:var(--bs-body-color, #222);' +
        'max-width:600px;width:90%;max-height:90vh;overflow:auto;' +
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
    (Array.isArray(opts.footer) ? opts.footer : [opts.footer]).forEach(b => {
      if (b) footer.appendChild(b);
    });
    dialog.appendChild(footer);
    backdrop.appendChild(dialog);

    function close() { backdrop.remove(); }
    backdrop.addEventListener('click', (e) => { if (e.target === backdrop) close(); });
    document.body.appendChild(backdrop);
    return { close, dialog };
  }

  // Form labels and helper text both adapt via Bootstrap's --bs-secondary-color,
  // which the theme already redefines per [data-theme]. Inputs themselves
  // inherit the form-control styling and need no inline overrides.
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
    return { wrapper, input: t };
  }

  function checkbox(opts) {
    const id = 'pair-cb-' + Math.random().toString(36).slice(2, 8);
    const wrapper = el('div', { style: 'margin-bottom:10px;' });
    const cb = el('input', { type: 'checkbox', id });
    if (opts.checked) cb.checked = true;
    const lbl = el('label', { for: id, style: 'margin-left:6px;' }, opts.label);
    wrapper.appendChild(cb);
    wrapper.appendChild(lbl);
    return { wrapper, input: cb };
  }

  function btn(label, onClick, kind) {
    const cls = 'btn ' + (kind === 'primary' ? 'btn-primary' : kind === 'danger' ? 'btn-danger' : 'btn-secondary');
    return el('button', { class: cls, style: 'margin-left:6px;', onclick: onClick }, label);
  }

  // ---------- Create-pair modal ----------

  async function openCreateModal() {
    const allSessions = await fetchInboxSessions();
    const partnerOptions = allSessions
      .filter(s => s.id !== me.session && !s.trashed)
      .sort((a, b) => a.agent.localeCompare(b.agent) || a.display_name.localeCompare(b.display_name));

    if (partnerOptions.length === 0) {
      alert('No other sessions available to pair with. Create another session (Claude or Codex) first.');
      return;
    }

    // LocalStorage-remembered defaults.
    const remembered = JSON.parse(localStorage.getItem('pair.defaults') || '{}');

    const body = el('div');

    const roleP = el('p', { style: 'margin-bottom:10px;' });
    roleP.appendChild(el('strong', null, 'This session: '));
    const roleText = el('span', null, 'Implementer');
    roleP.appendChild(roleText);
    const swapBtn = el('button', { class: 'btn btn-sm btn-outline-secondary', style: 'margin-left:8px;', onclick: () => {
      currentRole = currentRole === 'implementer' ? 'reviewer' : 'implementer';
      roleText.textContent = currentRole === 'implementer' ? 'Implementer' : 'Reviewer';
      partnerLabel.textContent = partnerLabelText();
    } }, 'Swap');
    roleP.appendChild(swapBtn);
    body.appendChild(roleP);
    let currentRole = 'implementer';
    const partnerLabelText = () => currentRole === 'implementer' ? 'Reviewer session' : 'Implementer session';

    const partnerWrap = el('div', { style: 'margin-bottom:10px;' });
    const partnerLabel = el('label', { style: LABEL_STYLE }, partnerLabelText());
    partnerWrap.appendChild(partnerLabel);
    const partnerSel = el('select', { class: 'form-control', style: 'width:100%;' });
    for (const s of partnerOptions) {
      partnerSel.appendChild(el('option', { value: s.id }, '[' + s.agent.toUpperCase() + '] ' + s.display_name + ' — ' + s.worktree));
    }
    partnerWrap.appendChild(partnerSel);
    body.appendChild(partnerWrap);

    const reviewPrompt = textarea({
      label: 'Review prompt (prefix sent to reviewer)',
      value: remembered.review_prompt || 'Review this. If it is good, reply with LGTM on its own line.',
      rows: 2,
    });
    body.appendChild(reviewPrompt.wrapper);

    const feedbackPrompt = textarea({
      label: 'Feedback prompt (prefix sent to implementer)',
      value: remembered.feedback_prompt || 'Feedback:',
      rows: 2,
    });
    body.appendChild(feedbackPrompt.wrapper);

    const stopSignal = input({
      label: 'Stop signal (matched on its own line, case-insensitive)',
      value: remembered.stop_signal || 'LGTM',
    });
    body.appendChild(stopSignal.wrapper);

    const maxRounds = input({
      label: 'Max rounds',
      type: 'number',
      value: remembered.max_rounds || 10,
    });
    body.appendChild(maxRounds.wrapper);

    const kickoffWrap = el('div', { style: 'margin-bottom:10px;' });
    kickoffWrap.appendChild(el('label', { style: LABEL_STYLE }, 'Kickoff'));
    const kickoffSel = el('select', { class: 'form-control', style: 'width:100%;' },
      el('option', { value: 'wait_for_next' }, 'Wait for implementer\'s next turn (recommended)'),
      el('option', { value: 'use_current' }, 'Use implementer\'s current last message'),
    );
    kickoffWrap.appendChild(kickoffSel);
    body.appendChild(kickoffWrap);

    const confirmBefore = checkbox({ label: 'Confirm before each relay (review/edit each outbound message)', checked: !!remembered.confirm_before_relay });
    body.appendChild(confirmBefore.wrapper);

    let modal;
    const cancelBtn = btn('Cancel', () => modal.close());
    const startBtn = btn('Start pair', async () => {
      const selectedPartner = partnerOptions.find(s => s.id === partnerSel.value);
      if (!selectedPartner) return;
      const mePartial = { agent: me.agent, worktree: me.worktree, session_id: me.session };
      const partnerPartial = { agent: selectedPartner.agent, worktree: selectedPartner.worktree, session_id: selectedPartner.id };
      const implementer = currentRole === 'implementer' ? mePartial : partnerPartial;
      const reviewer = currentRole === 'implementer' ? partnerPartial : mePartial;

      const cfg = {
        implementer,
        reviewer,
        review_prompt: reviewPrompt.input.value,
        feedback_prompt: feedbackPrompt.input.value,
        stop_signal: stopSignal.input.value,
        max_rounds: parseInt(maxRounds.input.value, 10) || 10,
        confirm_before_relay: confirmBefore.input.checked,
        kickoff: kickoffSel.value,
      };
      try {
        const p = await api('POST', '/api/v1/pair', cfg);
        currentPair = p;
        // Remember defaults for next time.
        localStorage.setItem('pair.defaults', JSON.stringify({
          review_prompt: cfg.review_prompt,
          feedback_prompt: cfg.feedback_prompt,
          stop_signal: cfg.stop_signal,
          max_rounds: cfg.max_rounds,
          confirm_before_relay: cfg.confirm_before_relay,
        }));
        renderBanner();
        modal.close();
      } catch (e) {
        alert('Failed to start pair: ' + e.message);
      }
    }, 'primary');

    modal = openModal({ title: 'Start a paired review loop', body, footer: [cancelBtn, startBtn] });
  }

  // ---------- Settings modal (mid-loop reconfig) ----------

  function openSettingsModal() {
    if (!currentPair) return;
    const isStopped = currentPair.state === 'stopped';
    const c = currentPair.config || {};

    const body = el('div');
    body.appendChild(el('p', { style: 'color:var(--bs-secondary-color, #666);font-size:13px;margin-bottom:12px;' },
      isStopped ? 'Read-only — this pair has stopped.' : 'Changes apply on the next relay. Partner and roles can\'t be changed mid-loop.'));

    const reviewPrompt = textarea({ label: 'Review prompt', value: c.review_prompt || '', rows: 2 });
    body.appendChild(reviewPrompt.wrapper);
    const feedbackPrompt = textarea({ label: 'Feedback prompt', value: c.feedback_prompt || '', rows: 2 });
    body.appendChild(feedbackPrompt.wrapper);
    const stopSignal = input({ label: 'Stop signal', value: c.stop_signal || '' });
    body.appendChild(stopSignal.wrapper);
    const maxRounds = input({ label: 'Max rounds', type: 'number', value: c.max_rounds || 10 });
    body.appendChild(maxRounds.wrapper);
    const confirmBefore = checkbox({ label: 'Confirm before each relay', checked: !!c.confirm_before_relay });
    body.appendChild(confirmBefore.wrapper);

    if (isStopped) {
      [reviewPrompt.input, feedbackPrompt.input, stopSignal.input, maxRounds.input, confirmBefore.input].forEach(i => i.disabled = true);
    }

    let modal;
    const cancelBtn = btn(isStopped ? 'Close' : 'Cancel', () => modal.close());
    const saveBtn = isStopped ? null : btn('Save', async () => {
      try {
        const updated = await api('POST', '/api/v1/pair/' + encodeURIComponent(currentPair.id) + '/config', {
          review_prompt: reviewPrompt.input.value,
          feedback_prompt: feedbackPrompt.input.value,
          stop_signal: stopSignal.input.value,
          max_rounds: parseInt(maxRounds.input.value, 10) || 10,
          confirm_before_relay: confirmBefore.input.checked,
        });
        currentPair = updated;
        renderBanner();
        modal.close();
      } catch (e) {
        alert('Save failed: ' + e.message);
      }
    }, 'primary');

    modal = openModal({ title: 'Pair settings', body, footer: [cancelBtn, saveBtn] });
  }

  // ---------- Confirm-relay modal (edit-before-send) ----------

  function openConfirmModal() {
    if (!currentPair || !currentPair.pending_confirm) return;
    const pc = currentPair.pending_confirm;
    const dirLabel = pc.direction === 'to_reviewer' ? 'reviewer' : 'implementer';

    const body = el('div');
    body.appendChild(el('p', null, 'About to send to ', el('strong', null, dirLabel), ':'));
    const ta = textarea({ value: pc.prepared_text, rows: 12 });
    body.appendChild(ta.wrapper);

    let modal;
    const stopBtn = btn('Stop loop', async () => {
      await doAction('stop');
      modal.close();
    }, 'danger');
    const skipBtn = btn('Skip', async () => {
      try {
        const updated = await api('POST', '/api/v1/pair/' + encodeURIComponent(currentPair.id) + '/confirm', { action: 'skip' });
        currentPair = updated;
        renderBanner();
        modal.close();
      } catch (e) { alert('Skip failed: ' + e.message); }
    });
    const sendBtn = btn('Send', async () => {
      try {
        const updated = await api('POST', '/api/v1/pair/' + encodeURIComponent(currentPair.id) + '/confirm', { action: 'send', edited_text: ta.input.value });
        currentPair = updated;
        renderBanner();
        modal.close();
      } catch (e) { alert('Send failed: ' + e.message); }
    }, 'primary');

    modal = openModal({ title: 'Review pending relay', body, footer: [stopBtn, skipBtn, sendBtn] });
  }

  // ---------- Init ----------

  async function init() {
    injectToolbarButton();
    const [pair] = await Promise.all([fetchActivePair(), refreshSessionNameCache()]);
    currentPair = pair;
    renderBanner();
    connectWS();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
