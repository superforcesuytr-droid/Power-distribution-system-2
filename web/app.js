/* Power Distribution System - embedded single page UI.
 * Plain JavaScript, no build step. Talks to the local Go API under /api/.
 */
(function () {
  'use strict';

  // ------------------------------------------------------------ state
  const state = {
    role: localStorage.getItem('pds.role') || 'supervisor',
    status: null,          // /api/status payload
    overview: null,        // { buildings, boards, totals, settings }
    boardId: Number(localStorage.getItem('pds.boardId')) || 0,
    board: null,           // current BoardDetail
    collapsed: new Set(JSON.parse(localStorage.getItem('pds.collapsed') || '[]')),
    route: { view: 'dashboard', params: {} },
    explorer: { building_id: '', board_id: '', status: '', q: '' },
    focus: null,
  };

  const $ = (sel, root) => (root || document).querySelector(sel);
  const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));
  const app = $('#app');

  // ------------------------------------------------------------ helpers
  function esc(v) {
    return String(v == null ? '' : v)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }
  function fmtA(v) {
    const n = Number(v) || 0;
    return Number.isInteger(n) ? String(n) : n.toFixed(1).replace(/\.0$/, '');
  }
  function plural(n, word) { return n + ' ' + word + (n === 1 ? '' : 's'); }
  function fmtDate(d) {
    return new Date(d).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
  }
  function fmtDateTime(d) {
    return new Date(d).toLocaleString('en-GB', { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' });
  }
  function levelClass(level) { return 'level-' + (level || 'normal'); }
  function bar(pct, level, extra) {
    return `<div class="bar ${extra || ''}"><i class="bg-${level || 'normal'}" style="width:${Math.max(0, Math.min(100, pct || 0))}%"></i></div>`;
  }
  function statusPill(s) {
    const label = { active: 'Active', maintenance: 'Maintenance', inactive: 'Inactive' }[s] || s;
    return `<span class="status status-${esc(s)}">${esc(label)}</span>`;
  }
  const debounce = (fn, ms) => { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; };

  const RANK = { viewer: 1, technician: 2, supervisor: 3 };
  function can(min) { return (RANK[state.role] || 0) >= (RANK[min] || 99); }
  const canEdit = () => can('technician');
  const canManage = () => can('supervisor');

  async function api(method, path, body) {
    const res = await fetch(path, {
      method,
      headers: Object.assign({ 'X-Role': state.role }, body ? { 'Content-Type': 'application/json' } : {}),
      body: body ? JSON.stringify(body) : undefined,
    });
    let data = null;
    const text = await res.text();
    try { data = text ? JSON.parse(text) : null; } catch (_) { data = { error: text }; }
    if (!res.ok) {
      if (res.status === 503) { state.status = null; showSetup(data && data.error); }
      throw new Error((data && data.error) || (res.status + ' ' + res.statusText));
    }
    return data;
  }

  function toast(msg, kind) {
    const root = $('#toast-root');
    const el = document.createElement('div');
    el.className = 'toast' + (kind ? ' toast-' + kind : '');
    el.textContent = msg;
    root.appendChild(el);
    setTimeout(() => { el.style.opacity = '0'; el.style.transition = 'opacity .3s'; setTimeout(() => el.remove(), 320); }, kind === 'error' ? 6000 : 3200);
  }

  // ------------------------------------------------------------ routing
  function parseHash() {
    const raw = location.hash.replace(/^#\/?/, '');
    const [pathPart, query] = raw.split('?');
    const parts = pathPart.split('/').filter(Boolean);
    const params = {};
    (query || '').split('&').filter(Boolean).forEach(kv => { const [k, v] = kv.split('='); params[decodeURIComponent(k)] = decodeURIComponent(v || ''); });
    const view = parts[0] || 'dashboard';
    if (parts[1] === 'board' && parts[2]) params.board = Number(parts[2]);
    return { view, params };
  }
  function navigate(hash) { if (location.hash === hash) render(); else location.hash = hash; }
  function boardHash(view, id, extra) { return '#/' + view + (id ? '/board/' + id : '') + (extra ? '?' + extra : ''); }

  function setActiveTab(view) {
    $$('#nav-tabs a').forEach(a => a.classList.toggle('active', a.dataset.tab === view));
    $$('#nav-tabs a').forEach(a => {
      if (a.dataset.tab === 'dashboard' || a.dataset.tab === 'sld') a.href = boardHash(a.dataset.tab, state.boardId);
    });
  }

  async function render() {
    state.route = parseHash();
    const { view, params } = state.route;
    if (params.board) { state.boardId = params.board; localStorage.setItem('pds.boardId', String(params.board)); }
    if (params.focus) state.focus = params.focus;
    setActiveTab(view);
    if (!state.status || state.status.db.state !== 'ready') {
      await refreshStatus();
      if (state.status.db.state !== 'ready') { showSetup(state.status.db.message); return; }
    }
    try {
      switch (view) {
        case 'explorer': await renderExplorer(); break;
        case 'sld': await renderSLD(); break;
        case 'activity': await renderActivity(); break;
        default: await renderDashboard();
      }
    } catch (err) {
      if (state.status && state.status.db.state === 'ready') {
        app.innerHTML = `<div class="page"><div class="alert alert-error">${esc(err.message)}</div></div>`;
      }
    }
  }

  async function refreshStatus() {
    const res = await fetch('/api/status', { headers: { 'X-Role': state.role } });
    state.status = await res.json();
    return state.status;
  }

  async function loadOverview() {
    state.overview = await api('GET', '/api/overview');
    return state.overview;
  }

  // ------------------------------------------------------------ setup screen
  function dbForm(db, opts) {
    db = db || {};
    return `
      <div class="form-grid">
        <div class="field"><label>Host</label><input name="host" value="${esc(db.host || 'localhost')}" placeholder="localhost" required></div>
        <div class="field"><label>Port</label><input name="port" type="number" min="1" max="65535" value="${esc(db.port || 5432)}" required></div>
        <div class="field"><label>Database</label><input name="database" value="${esc(db.database || 'power_distribution')}" required>
          <span class="hint">Created automatically if it does not exist and the user is allowed to.</span></div>
        <div class="field"><label>SSL mode</label>
          <select name="sslmode">${['prefer', 'disable', 'require', 'verify-ca', 'verify-full'].map(m => `<option ${m === (db.sslmode || 'prefer') ? 'selected' : ''}>${m}</option>`).join('')}</select></div>
        <div class="field"><label>User</label><input name="user" value="${esc(db.user || 'postgres')}" required></div>
        <div class="field"><label>Password</label><input name="password" type="password" placeholder="${opts && opts.hasSaved ? '(unchanged)' : ''}" autocomplete="off"></div>
      </div>`;
  }

  function bindDbForm(form, msgEl, onSaved) {
    const read = () => {
      const fd = new FormData(form);
      return { host: fd.get('host'), port: Number(fd.get('port')), database: fd.get('database'), user: fd.get('user'), password: fd.get('password'), sslmode: fd.get('sslmode') };
    };
    const setMsg = (text, kind) => { msgEl.innerHTML = text ? `<div class="alert alert-${kind}">${esc(text)}</div>` : ''; };
    $('[data-act=test]', form).addEventListener('click', async () => {
      setMsg('Testing connection…', 'info');
      try { const r = await api('POST', '/api/setup/test', read()); setMsg('Connected: ' + r.server, 'ok'); }
      catch (e) { setMsg(e.message, 'error'); }
    });
    form.addEventListener('submit', async ev => {
      ev.preventDefault();
      setMsg('Connecting…', 'info');
      const btn = $('[type=submit]', form); btn.disabled = true;
      try {
        const r = await api('POST', '/api/setup', read());
        setMsg(r.warning || ('Connected and saved to ' + r.config_path), r.warning ? 'info' : 'ok');
        await refreshStatus();
        onSaved && onSaved();
      } catch (e) { setMsg(e.message, 'error'); }
      finally { btn.disabled = false; }
    });
  }

  async function showSetup(message) {
    let saved = {};
    try { saved = await (await fetch('/api/setup')).json(); } catch (_) { /* ignore */ }
    const db = saved.database || {};
    app.innerHTML = `
      <div class="setup">
        <h1>Connect to PostgreSQL</h1>
        <p class="lead">This application stores all boards, breakers and circuits in a PostgreSQL database. Enter the connection details to continue.</p>
        <div id="setup-msg">${message ? `<div class="alert alert-error">${esc(message)}</div>` : ''}</div>
        ${state.status && state.status.dsn_from_env ? '<div class="alert alert-info">DATABASE_URL is set in the environment and overrides these settings.</div>' : ''}
        <form id="setup-form">
          ${dbForm(db, { hasSaved: !!db.host })}
          <div class="form-actions">
            <button type="button" class="btn" data-act="test">Test connection</button>
            <button type="submit" class="btn btn-primary">Connect &amp; save</button>
          </div>
        </form>
        <p class="faint" style="margin-top:18px;font-size:12px">Settings file: <span class="mono">${esc(saved.config_path || '')}</span></p>
      </div>`;
    bindDbForm($('#setup-form'), $('#setup-msg'), () => { toast('Database connected', 'ok'); render(); });
  }

  // ------------------------------------------------------------ dashboard
  async function renderDashboard() {
    const ov = await loadOverview();
    if (state.boardId && !ov.boards.some(b => b.id === state.boardId)) state.boardId = 0;
    if (!state.boardId && ov.boards.length) state.boardId = ov.boards[0].id;
    state.board = state.boardId ? await api('GET', '/api/boards/' + state.boardId) : null;
    setActiveTab('dashboard');
    app.innerHTML = `<div class="dashboard"><aside class="sidebar" id="sidebar"></aside><section class="content" id="content"></section></div>`;
    renderSidebar();
    renderBoard();
    applyFocus();
  }

  function renderSidebar() {
    const ov = state.overview;
    const t = ov.totals;
    const curBuilding = state.board ? state.board.building_id : 0;
    $('#sidebar').innerHTML = `
      <div class="side-head">
        <span class="side-title">BUILDINGS</span>
        ${canManage() ? '<button class="btn-plus" data-action="add-building" title="Add building">+</button>' : ''}
      </div>
      <div class="stat-tiles">
        <div class="stat-tile"><div class="n">${t.buildings}</div><div class="l">Buildings</div></div>
        <div class="stat-tile"><div class="n">${t.boards}</div><div class="l">Boards</div></div>
        <div class="stat-tile"><div class="n">${t.circuits}</div><div class="l">Circuits</div></div>
      </div>
      ${ov.buildings.map(b => {
        const boards = ov.boards.filter(x => x.building_id === b.id);
        const open = b.id === curBuilding;
        return `
        <div class="building-card ${open ? 'active' : ''}" data-action="open-building" data-id="${b.id}">
          <div class="name"><span class="ico">${esc(b.icon)}</span>${esc(b.name)}</div>
          <div class="meta">
            <span>${plural(b.board_count, 'board')}</span>
            ${b.active_circuits ? `<span><i class="dot dot-green"></i>${b.active_circuits}</span>` : ''}
            ${b.maintenance_circuits ? `<span><i class="dot dot-amber"></i>${b.maintenance_circuits}</span>` : ''}
            ${b.inactive_circuits ? `<span><i class="dot dot-grey"></i>${b.inactive_circuits}</span>` : ''}
          </div>
          ${open ? `<div class="building-boards">
              ${boards.length ? boards.map(bo => `
                <div class="board-link ${bo.id === state.boardId ? 'active' : ''}" data-action="open-board" data-id="${bo.id}">
                  <span class="code">${esc(bo.code)}</span>
                  <span class="pct ${bo.id === state.boardId ? '' : levelClass(bo.util_level)}">${fmtA(bo.total_current)} A · ${bo.util_pct}%</span>
                </div>`).join('') : '<div class="side-empty">No boards yet</div>'}
              ${canManage() ? `<div class="board-link" data-action="add-board" data-building="${b.id}" style="color:var(--primary)">+ Add board</div>` : ''}
            </div>` : ''}
          <div class="foot">
            ${canManage() ? `<button class="btn btn-ghost btn-sm" data-action="edit-building" data-id="${b.id}" title="Edit">✎</button>` : ''}
            <a data-action="view-building" data-id="${b.id}">View →</a>
          </div>
        </div>`;
      }).join('')}
      ${!ov.buildings.length ? '<div class="side-empty">No buildings yet. Use + to add one.</div>' : ''}`;
  }

  function renderBoard() {
    const el = $('#content');
    const b = state.board;
    if (!b) {
      const ov = state.overview;
      el.innerHTML = `
        <div class="empty">
          <h2>No board selected</h2>
          <p>${ov.boards.length ? 'Choose a board from the list.' : 'Add a building and a distribution board to get started.'}</p>
          ${canManage() && ov.buildings.length ? '<button class="btn btn-primary" data-action="add-board">+ Add board</button>' : ''}
        </div>
        ${ov.boards.length ? `<div class="boards-grid">${ov.boards.map(boardCard).join('')}</div>` : ''}`;
      return;
    }
    const t = b.totals;
    const sub = [b.level, b.location].filter(Boolean).join(', ');
    el.innerHTML = `
      <div class="board-head">
        <div>
          <div class="board-title">
            <h1>${esc(b.code)}</h1>
            <span class="pill">${esc(b.voltage)} ${esc(b.phases)}</span>
            ${canManage() ? `<button class="btn btn-ghost btn-sm" data-action="edit-board" title="Edit board">✎</button>` : ''}
          </div>
          <div class="board-sub">${[sub, b.technician, plural(t.mccb_count, 'MCCB'), plural(t.mcb_count, 'MCB'), plural(t.circuit_count, 'circuit')].filter(Boolean).map(esc).join(' · ')}</div>
        </div>
        <div class="board-actions">
          ${canEdit() ? `<button class="btn btn-primary btn-lg" data-action="add-circuit" ${t.mcb_count ? '' : 'disabled title="Add an MCB first"'}>+ Add New Circuit</button>` : ''}
        </div>
      </div>

      <div class="totals">
        <span class="cap">CURRENT TOTALS</span>
        <div class="tot"><span class="l">Total Current</span><span class="v ${levelClass(t.util_level)}">${fmtA(t.total_current)}<span class="unit">A</span></span></div>
        <div class="tot"><span class="l">MCB Capacity</span><span class="v">${fmtA(t.mcb_capacity)}<span class="unit">A</span></span></div>
        <div class="tot"><span class="l">Active Circuits</span><span class="v level-normal">${t.active_circuits}</span></div>
        <div class="tot"><span class="l">Under Maintenance</span><span class="v level-warning">${t.maintenance_circuits}</span></div>
        ${t.inactive_circuits ? `<div class="tot"><span class="l">Inactive</span><span class="v muted">${t.inactive_circuits}</span></div>` : ''}
        <div class="tot util"><span class="l">Utilisation (${t.util_pct}%)</span>${bar(t.util_pct, t.util_level, 'lg')}</div>
      </div>

      <div class="toolbar">
        ${canEdit() ? '<button class="btn btn-primary" data-action="add-mccb">+ Add MCCB</button>' : ''}
      </div>

      ${b.mccbs.length ? b.mccbs.map(mccbSection).join('') : `
        <div class="empty"><h2>No MCCBs on this board</h2><p>Add an MCCB, then MCBs beneath it, then circuits.</p></div>`}`;
  }

  function boardCard(bo) {
    return `<div class="board-card" data-action="open-board" data-id="${bo.id}">
      <div class="code">${esc(bo.code)}</div>
      <div class="sub">${esc(bo.building_name)} · ${esc(bo.voltage)} ${esc(bo.phases)}</div>
      ${bar(bo.util_pct, bo.util_level)}
      <div class="row"><span>${fmtA(bo.total_current)} / ${fmtA(bo.mcb_capacity)} A</span><span class="${levelClass(bo.util_level)}">${bo.util_pct}%</span></div>
      <div class="row"><span>${bo.mccb_count} MCCB · ${bo.mcb_count} MCB</span><span>${plural(bo.circuit_count, 'circuit')}</span></div>
    </div>`;
  }

  function mccbSection(m) {
    const collapsed = state.collapsed.has('mccb-' + m.id);
    return `
      <div class="mccb ${collapsed ? 'collapsed' : ''}" id="mccb-${m.id}">
        <div class="mccb-head">
          <span class="caret" data-action="toggle-mccb" data-id="${m.id}">▼</span>
          <span class="name">${esc(m.name)}</span>
          <span class="pill pill-blue">${fmtA(m.rating_a)}A</span>
          ${m.maintenance_circuits ? `<span class="warn-badge">⚠ ${m.maintenance_circuits} maintenance</span>` : ''}
          <span class="stat"><b>${m.mcb_count}</b> ${m.mcb_count === 1 ? 'MCB' : 'MCBs'}</span>
          <span class="stat"><b>${m.circuit_count}</b> ${m.circuit_count === 1 ? 'circuit' : 'circuits'}</span>
          <span class="load">
            ${bar(m.pct, m.level)}
            <span class="cur ${levelClass(m.level)}">${fmtA(m.current)} A</span>
            <span class="muted">/ ${fmtA(m.capacity)} A</span>
            <span class="${levelClass(m.level)}">(${m.pct}%)</span>
          </span>
          ${canEdit() ? `<button class="btn btn-soft btn-sm" data-action="add-mcb" data-id="${m.id}">+ Add MCB</button>
            <button class="btn btn-ghost btn-sm" data-action="edit-mccb" data-id="${m.id}" title="Edit MCCB">✎</button>` : ''}
          ${canManage() ? `<button class="btn btn-ghost btn-sm" data-action="delete-mccb" data-id="${m.id}" title="Delete MCCB">🗑</button>` : ''}
        </div>
        <div class="mccb-body">
          ${m.mcbs.length ? m.mcbs.map(mcbSection).join('') : '<div class="no-circuits">No MCBs under this MCCB</div>'}
        </div>
      </div>`;
  }

  function mcbSection(mb) {
    return `
      <div class="mcb" id="mcb-${mb.id}">
        <div class="mcb-head">
          <span class="name">${esc(mb.name)}</span>
          <span class="rating">${fmtA(mb.rating_a)}A</span>
          ${bar(mb.pct, mb.level)}
          <span class="cur ${levelClass(mb.level)}">${fmtA(mb.current)} A</span>
          <span class="cap">/ ${fmtA(mb.capacity)} A</span>
          <span class="mono ${levelClass(mb.level)}">(${mb.pct}%)</span>
          <span class="spacer"></span>
          <span class="count">${plural(mb.circuit_count, 'circuit')}</span>
          ${canEdit() ? `<button class="btn btn-soft btn-sm" data-action="add-circuit" data-mcb="${mb.id}">+ Circuit</button>
            <button class="btn btn-ghost btn-sm" data-action="edit-mcb" data-id="${mb.id}" title="Edit MCB">✎</button>` : ''}
          ${canManage() ? `<button class="btn btn-ghost btn-sm" data-action="delete-mcb" data-id="${mb.id}" title="Delete MCB">🗑</button>` : ''}
        </div>
        ${mb.circuits.length ? `<div class="circuits">${mb.circuits.map(circuitCard).join('')}</div>` : '<div class="no-circuits">No circuits</div>'}
      </div>`;
  }

  function circuitCard(c) {
    return `
      <div class="circuit" id="circuit-${c.id}">
        <div class="top">
          <div><div class="name">${esc(c.name)}</div><div class="code">${esc(c.code)}</div></div>
          ${statusPill(c.status)}
        </div>
        ${bar(c.pct, c.level)}
        <div class="load">
          <span><span class="cur ${levelClass(c.level)}">${fmtA(c.load_a)} A</span><span class="cap">/ ${fmtA(c.capacity)} A</span></span>
          <span>
            <span class="pct ${levelClass(c.level)}">${c.pct}%</span>
            <span class="actions">
              ${canEdit() ? `<button class="btn btn-soft btn-sm" data-action="edit-circuit" data-id="${c.id}" title="Edit circuit">✎</button>` : ''}
              ${canManage() ? `<button class="btn btn-ghost btn-sm" data-action="delete-circuit" data-id="${c.id}" title="Delete circuit">🗑</button>` : ''}
            </span>
          </span>
        </div>
        <div class="foot">
          <span>⚡ ${plural(c.equipment_count, 'equipment').replace('equipments', 'equipment')}</span>
          ${c.service ? `<span class="sep">·</span><span>${esc(c.service)}</span>` : ''}
          ${c.notes ? `<span class="sep">·</span><span title="${esc(c.notes)}">📝</span>` : ''}
        </div>
      </div>`;
  }

  function applyFocus() {
    if (!state.focus) return;
    const el = document.getElementById(state.focus);
    state.focus = null;
    if (!el) return;
    const mccb = el.closest('.mccb');
    if (mccb && mccb.classList.contains('collapsed')) {
      mccb.classList.remove('collapsed');
      state.collapsed.delete(mccb.id); persistCollapsed();
    }
    el.scrollIntoView({ behavior: 'smooth', block: 'center' });
    el.classList.add('flash');
    setTimeout(() => el.classList.remove('flash'), 1700);
  }
  function persistCollapsed() { localStorage.setItem('pds.collapsed', JSON.stringify(Array.from(state.collapsed))); }

  // ------------------------------------------------------------ explorer
  async function renderExplorer() {
    const ov = await loadOverview();
    const f = state.explorer;
    const params = new URLSearchParams();
    Object.entries(f).forEach(([k, v]) => { if (v) params.set(k, v); });
    const rows = await api('GET', '/api/circuits?' + params.toString());
    const boards = ov.boards.filter(b => !f.building_id || b.building_id === Number(f.building_id));
    const totalLoad = rows.reduce((s, r) => s + Number(r.load_a), 0);
    app.innerHTML = `
      <div class="page">
        <div class="filters">
          <label>FILTER</label>
          <select id="f-building"><option value="">All buildings</option>${ov.buildings.map(b => `<option value="${b.id}" ${String(b.id) === String(f.building_id) ? 'selected' : ''}>${esc(b.name)}</option>`).join('')}</select>
          <select id="f-board"><option value="">All boards</option>${boards.map(b => `<option value="${b.id}" ${String(b.id) === String(f.board_id) ? 'selected' : ''}>${esc(b.code)}</option>`).join('')}</select>
          <select id="f-status"><option value="">All statuses</option>${['active', 'maintenance', 'inactive'].map(s => `<option value="${s}" ${s === f.status ? 'selected' : ''}>${s[0].toUpperCase() + s.slice(1)}</option>`).join('')}</select>
          <input id="f-q" placeholder="Search name, code, service, breaker…" value="${esc(f.q)}">
          <span class="grow"></span>
          <a class="btn" href="/api/circuits.csv?${params.toString()}" download>Export CSV</a>
          ${canEdit() && ov.boards.length ? '<button class="btn btn-primary" data-action="add-circuit-any">+ Add circuit</button>' : ''}
        </div>
        <p class="summary-line">${plural(rows.length, 'circuit')} · total load <b class="mono">${fmtA(totalLoad)} A</b>${rows.filter(r => r.level === 'critical').length ? ` · <span class="level-critical">${rows.filter(r => r.level === 'critical').length} critical</span>` : ''}</p>
        <div class="table-wrap">
          <table class="grid">
            <thead><tr>
              <th>Code</th><th>Circuit</th><th>Building</th><th>Board</th><th>MCCB</th><th>MCB</th>
              <th class="right">Load</th><th class="right">Capacity</th><th>Utilisation</th><th>Status</th><th>Service</th><th>Equip.</th><th></th>
            </tr></thead>
            <tbody>
              ${rows.length ? rows.map(r => `
                <tr class="row" data-action="edit-circuit" data-id="${r.id}" data-board="${r.board_id}">
                  <td class="mono faint">${esc(r.code)}</td>
                  <td class="mono"><b>${esc(r.name)}</b></td>
                  <td>${esc(r.building_name)}</td>
                  <td class="mono"><a href="${boardHash('dashboard', r.board_id, 'focus=circuit-' + r.id)}" data-stop>${esc(r.board_code)}</a></td>
                  <td class="mono">${esc(r.mccb_name)}</td>
                  <td class="mono">${esc(r.mcb_name)} <span class="faint">${fmtA(r.mcb_rating)}A</span></td>
                  <td class="mono right ${levelClass(r.level)}"><b>${fmtA(r.load_a)} A</b></td>
                  <td class="mono right muted">${fmtA(r.capacity)} A</td>
                  <td class="bar-cell">${bar(r.pct, r.level)}<span class="mono ${levelClass(r.level)}">${r.pct}%</span></td>
                  <td>${statusPill(r.status)}</td>
                  <td>${esc(r.service)}</td>
                  <td class="mono">${r.equipment_count}</td>
                  <td>${canEdit() ? '<button class="btn btn-ghost btn-sm">✎</button>' : ''}</td>
                </tr>`).join('') : '<tr><td colspan="13" class="no-circuits">No circuits match the current filters.</td></tr>'}
            </tbody>
          </table>
        </div>
      </div>`;
    const rerun = () => renderExplorer();
    $('#f-building').addEventListener('change', e => { f.building_id = e.target.value; f.board_id = ''; rerun(); });
    $('#f-board').addEventListener('change', e => { f.board_id = e.target.value; rerun(); });
    $('#f-status').addEventListener('change', e => { f.status = e.target.value; rerun(); });
    $('#f-q').addEventListener('input', debounce(async e => {
      f.q = e.target.value;
      const pos = e.target.selectionStart;
      await rerun();
      const input = $('#f-q');
      if (input) { input.focus(); input.setSelectionRange(pos, pos); }
    }, 250));
  }

  // ------------------------------------------------------------ single line diagram
  async function renderSLD() {
    const ov = await loadOverview();
    if (state.boardId && !ov.boards.some(b => b.id === state.boardId)) state.boardId = 0;
    if (!state.boardId && ov.boards.length) state.boardId = ov.boards[0].id;
    const b = state.boardId ? await api('GET', '/api/boards/' + state.boardId) : null;
    state.board = b;
    setActiveTab('sld');
    const st = ov.settings;
    app.innerHTML = `
      <div class="page">
        <div class="chips"><span class="lbl">BOARD:</span>
          ${ov.boards.map(x => `<button class="btn btn-chip ${x.id === state.boardId ? 'active' : ''}" data-action="sld-board" data-id="${x.id}">${esc(x.code)}</button>`).join('')}
          ${!ov.boards.length ? '<span class="muted">No boards yet</span>' : ''}
        </div>
        ${b ? `
        <div class="sld-summary">
          <span class="code">${esc(b.code)}</span>
          <span class="muted">${esc(b.voltage)} ${esc(b.phases)}${[b.level, b.location].filter(Boolean).length ? ' · ' + esc([b.level, b.location].filter(Boolean).join(', ')) : ''}</span>
          <div class="counts">
            <div><div class="n">${b.totals.mccb_count}</div><div class="l">MCCBs</div></div>
            <div><div class="n">${b.totals.mcb_count}</div><div class="l">MCBs</div></div>
            <div><div class="n">${b.totals.circuit_count}</div><div class="l">Circuits</div></div>
            <div><div class="n ${levelClass(b.totals.util_level)}">${fmtA(b.totals.total_current)} A</div><div class="l">Load</div></div>
          </div>
        </div>
        <div class="legend"><span class="lbl">LOAD:</span>
          <span><i style="background:var(--green)"></i>Normal &lt; ${st.warn_pct}%</span>
          <span><i style="background:var(--amber)"></i>Warning ${st.warn_pct}–${st.crit_pct}%</span>
          <span><i style="background:var(--red)"></i>Critical &gt; ${st.crit_pct}%</span>
          <span class="faint" style="margin-left:auto">Capacity = rating × ${st.trip_factor}</span>
        </div>
        ${canEdit() ? '<p class="sld-hint">Edit the diagram directly: <b>✎</b> changes a breaker\'s name or rating, <b>+ MCCB</b> and <b>+ MCB</b> add one, and the drawing redraws itself immediately. Click a breaker body to open it on the dashboard.</p>' : ''}
        <div class="sld-canvas">${sldSVG(b)}</div>` : '<div class="empty"><h2>No board to draw</h2></div>'}
      </div>`;
    applyFocus();
  }

  // Icons drawn as paths so they stay crisp and match the line colours.
  const SLD_ICONS = {
    edit: '<path d="M3.4 16.6h3.2L17 6.2a1.7 1.7 0 0 0-2.4-2.4L4.2 14.2v2.4z"/>',
    del: '<path d="M4 6h13M8.2 6V4.2h4.6V6M5.7 6l.9 10.8h7L14.4 6"/>',
  };

  function sldIconBtn(x, y, icon, action, id, title, col) {
    return `<g class="sld-btn" data-action="${action}" data-id="${id}"><title>${esc(title)}</title>` +
      `<rect x="${x}" y="${y}" width="26" height="26" rx="7" fill="#fff" stroke="${col}" stroke-opacity=".4"/>` +
      `<g transform="translate(${x + 3} ${y + 3})" fill="none" stroke="${col}" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">${SLD_ICONS[icon]}</g></g>`;
  }

  function sldPill(cx, cy, w, label, attrs, col, title) {
    return `<g class="sld-pill" ${attrs}><title>${esc(title)}</title>` +
      `<rect x="${cx - w / 2}" y="${cy - 14}" width="${w}" height="28" rx="14" fill="#fff" stroke="${col}" stroke-width="1.6"/>` +
      `<text x="${cx}" y="${cy + 5}" text-anchor="middle" font-size="13" font-weight="700" fill="${col}">${esc(label)}</text></g>`;
  }

  function sldSVG(b) {
    const COLORS = { normal: '#0f8a4f', warning: '#d97a06', critical: '#d32f2f', bus: '#0b74c4' };
    const edit = canEdit(), manage = canManage();
    const NODE_W = 178, NODE_H = 150, MCB_GAP = 22, MCCB_W = 226, MCCB_H = 150, GROUP_GAP = 64, MARGIN = 40;
    const groups = b.mccbs.map(m => {
      const n = Math.max(1, m.mcbs.length);
      return { m, width: Math.max(MCCB_W, n * NODE_W + (n - 1) * MCB_GAP) };
    });
    const anyMCB = groups.some(g => g.m.mcbs.length);
    const contentW = groups.reduce((s, g) => s + g.width, 0) + Math.max(0, groups.length - 1) * GROUP_GAP;
    const rightExtra = edit ? 130 : 0;
    const W = Math.max(560, contentW + MARGIN * 2 + rightExtra);
    const supplyY = 30, busY = supplyY + 120;
    const mccbSwY = busY + 26, mccbBoxY = busY + 86;
    const dropGap = edit ? 84 : 56;
    const mcbBusY = mccbBoxY + MCCB_H + dropGap, mcbSwY = mcbBusY + 26, mcbBoxY = mcbBusY + 86;
    const addMcbY = mccbBoxY + MCCB_H + 42;
    let H;
    if (!groups.length) H = busY + 110;
    else if (!anyMCB) H = mccbBoxY + MCCB_H + (edit ? 90 : 40);
    else H = mcbBoxY + NODE_H + MARGIN;

    const startX = MARGIN + (W - MARGIN * 2 - rightExtra - contentW) / 2;
    const centers = [];
    let x = startX;
    groups.forEach(g => { centers.push(x + g.width / 2); x += g.width + GROUP_GAP; });
    const cCenter = groups.length ? startX + contentW / 2 : W / 2;

    const out = [];
    // Incoming supply: transformer squiggle and drop to the main busbar.
    out.push(`<g stroke="${COLORS.bus}" stroke-width="3" fill="none" stroke-linecap="round">
      <line x1="${cCenter}" y1="${supplyY}" x2="${cCenter}" y2="${busY}"/>
      ${[0, 1, 2].map(i => `<path d="M ${cCenter - 11} ${supplyY + 22 + i * 14} q 5.5 -8 11 0 t 11 0"/>`).join('')}
    </g>
    <text x="${cCenter + 22}" y="${supplyY + 26}" font-size="14" font-weight="700" fill="${COLORS.bus}">3~ ${esc(b.voltage)} ${esc(b.phases)}</text>
    <text x="${cCenter + 22}" y="${supplyY + 45}" font-size="11" fill="#94a3b8" letter-spacing="1.5">INCOMING SUPPLY</text>`);

    // Main busbar, extended to the right to carry the "add MCCB" control.
    let busL, busR;
    if (groups.length > 1) { busL = Math.min(centers[0], cCenter); busR = Math.max(centers[centers.length - 1], cCenter); }
    else { busL = cCenter - 110; busR = cCenter + 110; }
    const addMccbX = busR + 64;
    const busEnd = edit ? addMccbX - 38 : busR;
    out.push(`<line x1="${busL}" y1="${busY}" x2="${busEnd}" y2="${busY}" stroke="${COLORS.bus}" stroke-width="6" stroke-linecap="round"/>`);
    out.push(`<circle cx="${cCenter}" cy="${busY}" r="5" fill="${COLORS.bus}"/>`);
    if (edit) out.push(sldPill(addMccbX, busY, 76, '+ MCCB', 'data-action="add-mccb"', COLORS.bus, 'Add an MCCB to this board'));

    if (!groups.length) {
      out.push(`<text x="${cCenter}" y="${busY + 46}" text-anchor="middle" font-size="13" fill="#64748b">No MCCBs on this board${edit ? ' - use + MCCB to add the first one' : ''}</text>`);
      return wrap(W, H, out.join(''));
    }

    groups.forEach((g, gi) => {
      const m = g.m, cx = centers[gi], col = COLORS[m.level] || COLORS.normal;
      out.push(`<circle cx="${cx}" cy="${busY}" r="5" fill="${COLORS.bus}"/>`);
      out.push(`<line x1="${cx}" y1="${busY}" x2="${cx}" y2="${mccbBoxY}" stroke="${col}" stroke-width="3"/>`);
      out.push(breakerSymbol(cx, mccbSwY, col));
      out.push(nodeBox(cx - MCCB_W / 2, mccbBoxY, MCCB_W, MCCB_H, col, 'MCCB', m.name,
        [`Rated ${fmtA(m.rating_a)} A`, { text: `${fmtA(m.current)} A used`, color: col, bold: true },
          `${m.mcb_count} MCB${m.mcb_count === 1 ? '' : 's'} · ${m.circuit_count} cct${m.circuit_count === 1 ? '' : 's'}`],
        m.pct, 'mccb-' + m.id, [
          edit ? { icon: 'edit', action: 'edit-mccb', id: m.id, title: 'Edit ' + m.name } : null,
          manage ? { icon: 'del', action: 'delete-mccb', id: m.id, title: 'Delete ' + m.name } : null,
        ]));

      const n = m.mcbs.length;
      if (!n) {
        if (edit) {
          out.push(`<line x1="${cx}" y1="${mccbBoxY + MCCB_H}" x2="${cx}" y2="${addMcbY - 14}" stroke="${col}" stroke-width="3" stroke-dasharray="5 5"/>`);
          out.push(sldPill(cx, addMcbY, 74, '+ MCB', `data-action="add-mcb" data-id="${m.id}"`, col, 'Add an MCB under ' + m.name));
        }
        return;
      }
      const spanW = n * NODE_W + (n - 1) * MCB_GAP;
      const left = cx - spanW / 2;
      out.push(`<line x1="${cx}" y1="${mccbBoxY + MCCB_H}" x2="${cx}" y2="${mcbBusY}" stroke="${col}" stroke-width="3"/>`);
      if (n > 1) out.push(`<line x1="${left + NODE_W / 2}" y1="${mcbBusY}" x2="${left + spanW - NODE_W / 2}" y2="${mcbBusY}" stroke="${col}" stroke-width="3" stroke-linecap="round"/>`);
      m.mcbs.forEach((mb, i) => {
        const mx = left + i * (NODE_W + MCB_GAP) + NODE_W / 2;
        const mcol = COLORS[mb.level] || COLORS.normal;
        out.push(`<circle cx="${mx}" cy="${mcbBusY}" r="4" fill="${mcol}"/>`);
        out.push(`<line x1="${mx}" y1="${mcbBusY}" x2="${mx}" y2="${mcbBoxY}" stroke="${mcol}" stroke-width="3"/>`);
        out.push(breakerSymbol(mx, mcbSwY, mcol));
        out.push(nodeBox(mx - NODE_W / 2, mcbBoxY, NODE_W, NODE_H, mcol, 'MCB', mb.name,
          [`${fmtA(mb.rating_a)} A rated`, { text: `${fmtA(mb.current)} A`, color: mcol, bold: true },
            `${mb.circuit_count} cct${mb.circuit_count === 1 ? '' : 's'}${mb.maintenance_circuits ? ' · ⚠' + mb.maintenance_circuits : ''}`],
          mb.pct, 'mcb-' + mb.id, [
            edit ? { icon: 'edit', action: 'edit-mcb', id: mb.id, title: 'Edit ' + mb.name } : null,
            manage ? { icon: 'del', action: 'delete-mcb', id: mb.id, title: 'Delete ' + mb.name } : null,
          ]));
      });
      // The "add MCB" control sits on the drop feeding this MCCB's sub-bus.
      if (edit) out.push(sldPill(cx, addMcbY, 74, '+ MCB', `data-action="add-mcb" data-id="${m.id}"`, col, 'Add an MCB under ' + m.name));
    });
    return wrap(W, H, out.join(''));

    function wrap(w, h, inner) {
      return `<svg viewBox="0 0 ${w} ${h}" width="${w}" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Single line diagram of ${esc(b.code)}">${inner}</svg>`;
    }
    function breakerSymbol(cx, cy, col) {
      return `<g transform="translate(${cx - 12} ${cy - 12})"><rect width="24" height="24" rx="4" fill="#fff" stroke="${col}" stroke-width="2.5"/><line x1="5" y1="19" x2="19" y2="5" stroke="${col}" stroke-width="2.5" stroke-linecap="round"/></g>`;
    }
    function nodeBox(x, y, w, h, col, kind, title, lines, pct, focusId, actions) {
      const pad = 16;
      let ty = y + 22;
      let t = `<text x="${x + pad}" y="${ty}" font-size="11" fill="#94a3b8" letter-spacing="1.5">${esc(kind)}</text>`;
      ty += 24;
      t += `<text x="${x + pad}" y="${ty}" font-size="17" font-weight="700" fill="#1c2733">${esc(title)}</text>`;
      lines.forEach(l => {
        ty += 22;
        const o = typeof l === 'string' ? { text: l } : l;
        t += `<text x="${x + pad}" y="${ty}" font-size="13" font-weight="${o.bold ? 700 : 400}" fill="${o.color || '#475569'}">${esc(o.text)}</text>`;
      });
      const bw = w - pad * 2, by = y + h - 14;
      t += `<rect x="${x + pad}" y="${by}" width="${bw}" height="6" rx="3" fill="#e5eaf0"/><rect x="${x + pad}" y="${by}" width="${bw * Math.min(100, pct) / 100}" height="6" rx="3" fill="${col}"/>`;
      const btns = (actions || []).filter(Boolean);
      let bx = x + w - 12 - btns.length * 26 - (btns.length - 1) * 6;
      btns.forEach(a => { t += sldIconBtn(bx, y + 11, a.icon, a.action, a.id, a.title, col); bx += 32; });
      return `<g id="${focusId}" class="sld-node" data-action="open-node" data-focus="${focusId}">` +
        `<title>${esc(title)} - click to open on the dashboard</title>` +
        `<rect class="box" x="${x}" y="${y}" width="${w}" height="${h}" rx="10" fill="#f7fbfd" stroke="${col}" stroke-width="2.5"/>${t}</g>`;
    }
  }

  // ------------------------------------------------------------ activity
  async function renderActivity() {
    const entries = await api('GET', '/api/audit?limit=200');
    app.innerHTML = `
      <div class="page">
        <div class="activity-head">
          <div><h2 style="margin:0;font-size:18px">Activity log</h2><span class="muted">Every change made through the application, most recent first.</span></div>
          <button class="btn" data-action="refresh">Refresh</button>
        </div>
        <div class="table-wrap"><table class="grid">
          <thead><tr><th>When</th><th>Role</th><th>Action</th><th>Entity</th><th>Details</th></tr></thead>
          <tbody>${entries.length ? entries.map(e => `<tr>
            <td class="mono muted">${esc(fmtDateTime(e.at))}</td>
            <td>${esc(e.actor_role)}</td>
            <td><span class="act-action act-${esc(e.action)}">${esc(e.action)}</span></td>
            <td class="mono">${esc(e.entity)}${e.entity_id ? ' #' + e.entity_id : ''}</td>
            <td style="white-space:normal">${esc(e.summary)}</td>
          </tr>`).join('') : '<tr><td colspan="5" class="no-circuits">No activity yet.</td></tr>'}</tbody>
        </table></div>
      </div>`;
  }

  // ------------------------------------------------------------ modals
  function openModal(title, bodyHTML, opts) {
    closeModal();
    const root = $('#modal-root');
    root.innerHTML = `
      <div class="modal-backdrop" data-close>
        <div class="modal ${opts && opts.wide ? 'wide' : ''}" role="dialog" aria-modal="true" aria-label="${esc(title)}">
          <div class="modal-head"><h2>${esc(title)}</h2><button class="btn btn-ghost btn-sm" data-close aria-label="Close">✕</button></div>
          ${opts && opts.tabs ? `<div class="mtabs">${opts.tabs.map((t, i) => `<button data-mtab="${t.id}" class="${i === 0 ? 'active' : ''}">${esc(t.label)}</button>`).join('')}</div>` : ''}
          <div class="modal-body">${bodyHTML}</div>
        </div>
      </div>`;
    root.querySelector('.modal-backdrop').addEventListener('click', e => { if (e.target.hasAttribute('data-close')) closeModal(); });
    const first = root.querySelector('input:not([type=hidden]):not([readonly]), select, textarea');
    if (first) setTimeout(() => first.focus(), 30);
    return root.querySelector('.modal');
  }
  function closeModal() { $('#modal-root').innerHTML = ''; }
  document.addEventListener('keydown', e => { if (e.key === 'Escape') { closeModal(); hideSearch(); } });

  function field(label, name, value, opts) {
    opts = opts || {};
    const attrs = `name="${name}" ${opts.required ? 'required' : ''} ${opts.attrs || ''}`;
    let control;
    if (opts.type === 'select') {
      control = `<select ${attrs}>${opts.options.map(o => `<option value="${esc(o.value)}" ${String(o.value) === String(value) ? 'selected' : ''} ${o.disabled ? 'disabled' : ''}>${esc(o.label)}</option>`).join('')}</select>`;
    } else if (opts.type === 'textarea') {
      control = `<textarea ${attrs}>${esc(value)}</textarea>`;
    } else {
      control = `<input type="${opts.type || 'text'}" ${attrs} value="${esc(value)}" ${opts.placeholder ? `placeholder="${esc(opts.placeholder)}"` : ''}>`;
    }
    return `<div class="field ${opts.full ? 'full' : ''}"><label>${esc(label)}</label>${control}${opts.hint ? `<span class="hint">${esc(opts.hint)}</span>` : ''}</div>`;
  }

  function formModal(title, fieldsHTML, onSubmit, opts) {
    opts = opts || {};
    const modal = openModal(title, `
      <form id="modal-form">
        <div class="form-grid">${fieldsHTML}</div>
        <div class="form-error" id="form-error"></div>
        <div class="form-actions">
          ${opts.deleteLabel ? `<button type="button" class="btn btn-danger left" data-act="delete">${esc(opts.deleteLabel)}</button>` : ''}
          <button type="button" class="btn" data-close>Cancel</button>
          <button type="submit" class="btn btn-primary">${esc(opts.submitLabel || 'Save')}</button>
        </div>
      </form>`, opts);
    const form = $('#modal-form', modal);
    form.addEventListener('submit', async ev => {
      ev.preventDefault();
      const data = {};
      new FormData(form).forEach((v, k) => { data[k] = v; });
      const btn = $('[type=submit]', form); btn.disabled = true;
      try { await onSubmit(data, form); closeModal(); }
      catch (e) { $('#form-error', form).textContent = e.message; }
      finally { btn.disabled = false; }
    });
    if (opts.onDelete) $('[data-act=delete]', form).addEventListener('click', opts.onDelete);
    return form;
  }

  function confirmModal(title, message, onConfirm, label) {
    const modal = openModal(title, `
      <p style="margin:0 0 6px">${message}</p>
      <div class="form-error" id="form-error"></div>
      <div class="form-actions">
        <button type="button" class="btn" data-close>Cancel</button>
        <button type="button" class="btn btn-danger" data-act="confirm">${esc(label || 'Delete')}</button>
      </div>`);
    $('[data-act=confirm]', modal).addEventListener('click', async ev => {
      ev.target.disabled = true;
      try { await onConfirm(); closeModal(); }
      catch (e) { $('#form-error', modal).textContent = e.message; ev.target.disabled = false; }
    });
  }

  async function afterChange(msg) {
    if (msg) toast(msg, 'ok');
    await render();
  }

  // ---- buildings
  const ICONS = ['🏭', '🏢', '🏗️', '🏬', '🏥', '🏫', '🏛️', '🏠', '⚡', '🔌'];
  function buildingForm(b) {
    const isEdit = !!(b && b.id);
    formModal(isEdit ? 'Edit building' : 'Add building', `
      ${field('Name', 'name', b ? b.name : '', { required: true, full: true, placeholder: 'e.g. Main FAB' })}
      ${field('Icon', 'icon', b ? b.icon : ICONS[0], { type: 'select', options: ICONS.map(i => ({ value: i, label: i })) })}`,
      async d => {
        if (isEdit) { await api('PUT', '/api/buildings/' + b.id, { name: d.name, icon: d.icon }); await afterChange('Building updated'); }
        else { await api('POST', '/api/buildings', { name: d.name, icon: d.icon }); await afterChange('Building added'); }
      },
      isEdit ? { deleteLabel: 'Delete building', onDelete: () => deleteBuilding(b) } : {});
  }
  function deleteBuilding(b) {
    confirmModal('Delete building', `Delete <b>${esc(b.name)}</b> together with its <b>${plural(b.board_count, 'board')}</b> and every breaker and circuit beneath them? This cannot be undone.`,
      async () => { await api('DELETE', '/api/buildings/' + b.id); await afterChange('Building deleted'); });
  }

  // ---- boards
  function boardForm(b, buildingId) {
    const isEdit = !!(b && b.id);
    const ov = state.overview;
    formModal(isEdit ? 'Edit board ' + b.code : 'Add distribution board', `
      ${field('Building', 'building_id', isEdit ? b.building_id : (buildingId || (ov.buildings[0] || {}).id), { type: 'select', required: true, options: ov.buildings.map(x => ({ value: x.id, label: x.name })) })}
      ${field('Board code', 'code', isEdit ? b.code : '', { required: true, placeholder: 'e.g. FAC6', attrs: 'style="text-transform:uppercase"' })}
      ${field('Voltage', 'voltage', isEdit ? b.voltage : '400V', { placeholder: '400V' })}
      ${field('Phases', 'phases', isEdit ? b.phases : '3PH', { type: 'select', options: ['3PH', '1PH'].map(v => ({ value: v, label: v })) })}
      ${field('Level', 'level', isEdit ? b.level : '', { placeholder: 'Level 1' })}
      ${field('Location', 'location', isEdit ? b.location : '', { placeholder: 'Panel Room A' })}
      ${field('Responsible technician', 'technician', isEdit ? b.technician : '', { full: true })}`,
      async d => {
        const body = { building_id: Number(d.building_id), code: d.code, voltage: d.voltage, phases: d.phases, level: d.level, location: d.location, technician: d.technician };
        if (isEdit) { await api('PUT', '/api/boards/' + b.id, body); await afterChange('Board updated'); }
        else { const r = await api('POST', '/api/boards', body); state.boardId = r.id; navigate(boardHash('dashboard', r.id)); toast('Board added', 'ok'); }
      },
      isEdit ? { deleteLabel: 'Delete board', onDelete: () => deleteBoard(b) } : {});
  }
  function deleteBoard(b) {
    confirmModal('Delete board', `Delete board <b>${esc(b.code)}</b> with its ${plural(b.totals.mccb_count, 'MCCB')}, ${plural(b.totals.mcb_count, 'MCB')} and ${plural(b.totals.circuit_count, 'circuit')}? This cannot be undone.`,
      async () => { await api('DELETE', '/api/boards/' + b.id); state.boardId = 0; await afterChange('Board deleted'); });
  }

  // ---- breakers
  const RATINGS = [6, 10, 16, 20, 25, 32, 40, 50, 63, 80, 100, 125, 160, 200, 250, 315, 400, 630, 800, 1000, 1250, 1600];
  function breakerForm(kind, item, parentId) {
    const isEdit = !!(item && item.id);
    const path = kind === 'mccb' ? '/api/mccbs' : '/api/mcbs';
    const label = kind.toUpperCase();
    const board = state.board;
    const parent = kind === 'mcb' ? findMCCB(isEdit ? item.mccb_id : Number(parentId)) : null;
    // An MCB can be moved to another MCCB on the same board, so its parent is
    // always a field; an MCCB always belongs to the board being viewed.
    const parentField = kind === 'mcb'
      ? field('Fed from MCCB', 'mccb_id', parent ? parent.id : (board.mccbs[0] || {}).id, {
        type: 'select', required: true, full: true,
        options: board.mccbs.map(m => ({ value: m.id, label: `${m.name} - ${fmtA(m.rating_a)} A rated, ${fmtA(m.current)} A used` })),
        hint: isEdit ? 'Change this to move the MCB to a different MCCB.' : '',
      })
      : '';
    const title = isEdit ? 'Edit ' + item.name
      : (kind === 'mccb' ? 'Add MCCB to ' + board.code : 'Add MCB' + (parent ? ' under ' + parent.name : ''));
    formModal(title, `
      ${parentField}
      ${field(label + ' name', 'name', isEdit ? item.name : suggestBreakerName(kind, parentId), { required: true, placeholder: kind === 'mccb' ? 'MCCB-C' : 'MCB-05' })}
      ${field('Rating (A)', 'rating_a', isEdit ? item.rating_a : (kind === 'mccb' ? 100 : 32), { type: 'number', required: true, attrs: 'min="1" max="10000" step="any" list="ratings"', hint: 'Capacity shown on the diagram = rating × ' + (state.overview ? state.overview.settings.trip_factor : 1.14) })}
      <datalist id="ratings">${RATINGS.map(r => `<option value="${r}">`).join('')}</datalist>`,
      async d => {
        const body = { name: d.name, rating_a: Number(d.rating_a) };
        if (kind === 'mccb') body.board_id = board.id; else body.mccb_id = Number(d.mccb_id);
        if (isEdit) {
          await api('PUT', path + '/' + item.id, kind === 'mccb' ? { name: body.name, rating_a: body.rating_a } : { name: body.name, rating_a: body.rating_a, mccb_id: body.mccb_id });
          state.focus = kind + '-' + item.id;
          await afterChange(label + ' updated');
        } else {
          const r = await api('POST', path, body);
          state.focus = kind + '-' + r.id;
          await afterChange(label + ' added');
        }
      },
      isEdit && canManage() ? { deleteLabel: 'Delete ' + label, onDelete: () => deleteBreaker(kind, item) } : {});
  }
  function suggestBreakerName(kind, parentId) {
    const b = state.board;
    if (!b) return '';
    if (kind === 'mccb') {
      const used = new Set(b.mccbs.map(m => m.name.toUpperCase()));
      for (let i = 0; i < 26; i++) { const n = 'MCCB-' + String.fromCharCode(65 + i); if (!used.has(n)) return n; }
      return '';
    }
    const used = new Set(b.mccbs.flatMap(m => m.mcbs.map(x => x.name.toUpperCase())));
    for (let i = 1; i < 200; i++) { const n = 'MCB-' + String(i).padStart(2, '0'); if (!used.has(n)) return n; }
    return '';
  }
  function deleteBreaker(kind, item) {
    const label = kind.toUpperCase();
    const detail = kind === 'mccb' ? `${plural(item.mcb_count, 'MCB')} and ${plural(item.circuit_count, 'circuit')}` : plural(item.circuit_count, 'circuit');
    confirmModal('Delete ' + label, `Delete <b>${esc(item.name)}</b> and its ${detail}? This cannot be undone.`,
      async () => { await api('DELETE', (kind === 'mccb' ? '/api/mccbs/' : '/api/mcbs/') + item.id); await afterChange(label + ' deleted'); });
  }

  // ---- circuits
  function mcbOptions(board) {
    const opts = [];
    board.mccbs.forEach(m => m.mcbs.forEach(mb => opts.push({ value: mb.id, label: `${m.name} / ${mb.name} (${fmtA(mb.rating_a)} A, ${fmtA(mb.current)} A used)` })));
    return opts;
  }
  async function circuitForm(c, mcbId, board) {
    board = board || state.board;
    if (!board) return;
    const isEdit = !!(c && c.id);
    const opts = mcbOptions(board);
    if (!opts.length) { toast('Add an MCB to this board before adding circuits.', 'error'); return; }
    const initialMcb = isEdit ? c.mcb_id : (mcbId || opts[0].value);
    const form = formModal(isEdit ? 'Edit circuit ' + c.name : 'Add circuit to ' + board.code, `
      ${field('Feeding MCB', 'mcb_id', initialMcb, { type: 'select', required: true, full: true, options: opts })}
      ${field('Circuit name', 'name', isEdit ? c.name : '', { required: true, placeholder: 'e.g. AHU-3A' })}
      ${field('Circuit code', 'code', isEdit ? c.code : '', { required: !isEdit ? false : true, placeholder: 'auto', hint: isEdit ? '' : 'Leave blank to generate automatically', attrs: 'style="text-transform:uppercase"' })}
      ${field('Load (A)', 'load_a', isEdit ? c.load_a : 0, { type: 'number', required: true, attrs: 'min="0" max="10000" step="any"' })}
      ${field('Status', 'status', isEdit ? c.status : 'active', { type: 'select', options: [{ value: 'active', label: 'Active' }, { value: 'maintenance', label: 'Under maintenance' }, { value: 'inactive', label: 'Inactive' }] })}
      ${field('Equipment count', 'equipment_count', isEdit ? c.equipment_count : 1, { type: 'number', attrs: 'min="0" step="1"' })}
      ${field('Service / area', 'service', isEdit ? c.service : (board.location ? '' : ''), { placeholder: 'e.g. Facility 5' })}
      ${field('Notes', 'notes', isEdit ? c.notes : '', { type: 'textarea', full: true })}`,
      async d => {
        const body = { mcb_id: Number(d.mcb_id), name: d.name, code: d.code, load_a: Number(d.load_a), status: d.status, equipment_count: Number(d.equipment_count || 0), service: d.service, notes: d.notes };
        if (isEdit) { await api('PUT', '/api/circuits/' + c.id, body); await afterChange('Circuit updated'); }
        else { const r = await api('POST', '/api/circuits', body); state.focus = 'circuit-' + r.id; await afterChange('Circuit ' + r.code + ' added'); }
      },
      isEdit && canManage() ? { deleteLabel: 'Delete circuit', onDelete: () => deleteCircuit(c) } : {});
    if (!isEdit) {
      const codeInput = $('[name=code]', form), mcbSel = $('[name=mcb_id]', form);
      const suggest = async () => { try { const r = await api('GET', '/api/mcbs/' + mcbSel.value + '/next-code'); codeInput.placeholder = r.code; } catch (_) { /* ignore */ } };
      mcbSel.addEventListener('change', suggest);
      suggest();
    }
  }
  function deleteCircuit(c) {
    confirmModal('Delete circuit', `Delete circuit <b>${esc(c.name)}</b> (${esc(c.code)})? This cannot be undone.`,
      async () => { await api('DELETE', '/api/circuits/' + c.id); await afterChange('Circuit deleted'); });
  }
  function findCircuit(id) {
    for (const m of state.board ? state.board.mccbs : []) for (const mb of m.mcbs) for (const c of mb.circuits) if (c.id === id) return c;
    return null;
  }
  function findMCCB(id) { return (state.board ? state.board.mccbs : []).find(m => m.id === id); }
  function findMCB(id) { for (const m of state.board ? state.board.mccbs : []) { const x = m.mcbs.find(mb => mb.id === id); if (x) return x; } return null; }

  // ---- settings
  async function settingsModal() {
    const [setup, st] = await Promise.all([(await fetch('/api/setup')).json(), refreshStatus()]);
    const modal = openModal('Settings', `
      <div data-pane="db">
        ${st.db.state === 'ready' ? `<div class="alert alert-ok">Connected: ${esc(st.db.server)}</div>` : `<div class="alert alert-error">${esc(st.db.message || 'Not connected')}</div>`}
        ${st.dsn_from_env ? '<div class="alert alert-info">DATABASE_URL is set in the environment and overrides these settings on start-up.</div>' : ''}
        <form id="db-form">
          ${dbForm(setup.database, { hasSaved: !!setup.database.host })}
          <div id="db-msg" style="margin-top:12px"></div>
          <div class="form-actions">
            <button type="button" class="btn" data-act="test">Test connection</button>
            <button type="submit" class="btn btn-primary">Connect &amp; save</button>
          </div>
        </form>
      </div>
      <div data-pane="eng" hidden>
        <form id="eng-form">
          <div class="form-grid">
            ${field('Capacity factor (× rating)', 'trip_factor', st.settings.trip_factor, { type: 'number', attrs: 'min="0.5" max="2" step="0.01"', hint: 'Capacity = breaker rating × this factor. 1.14 ≈ IEC 60898 conventional non-tripping current (1.13 In).', full: true })}
            ${field('Warning threshold (%)', 'warn_pct', st.settings.warn_pct, { type: 'number', attrs: 'min="1" max="99" step="1"' })}
            ${field('Critical threshold (%)', 'crit_pct', st.settings.crit_pct, { type: 'number', attrs: 'min="2" max="100" step="1"' })}
          </div>
          <div class="form-error" id="eng-error"></div>
          <div class="form-actions">
            <button type="submit" class="btn btn-primary" ${canManage() ? '' : 'disabled title="Supervisor role required"'}>Save</button>
          </div>
        </form>
      </div>
      <div data-pane="about" hidden>
        <dl class="kv">
          <dt>Application</dt><dd>Power Distribution System ${esc(st.version)}</dd>
          <dt>Database</dt><dd>${esc(st.db.server || st.db.state)}</dd>
          <dt>Settings file</dt><dd>${esc(st.config_path)}</dd>
          <dt>Log file</dt><dd>${esc(st.log_path || '-')}</dd>
          <dt>UI address</dt><dd>${esc(location.origin)}</dd>
          ${st.dev ? '<dt>Mode</dt><dd>dev - UI served from disk, not from the executable</dd>' : ''}
          <dt>Current role</dt><dd>${esc(state.role)}</dd>
        </dl>
        <p class="muted" style="margin-top:16px;font-size:12px">Roles: <b>Viewer</b> can only read. <b>Technician</b> can add and edit breakers and circuits. <b>Supervisor</b> can also delete, manage buildings and boards, and change these settings.</p>
      </div>`,
      { wide: true, tabs: [{ id: 'db', label: 'Database' }, { id: 'eng', label: 'Engineering' }, { id: 'about', label: 'About' }] });
    $$('[data-mtab]', modal).forEach(btn => btn.addEventListener('click', () => {
      $$('[data-mtab]', modal).forEach(b => b.classList.toggle('active', b === btn));
      $$('[data-pane]', modal).forEach(p => { p.hidden = p.dataset.pane !== btn.dataset.mtab; });
    }));
    bindDbForm($('#db-form', modal), $('#db-msg', modal), () => { toast('Database connected', 'ok'); render(); });
    $('#eng-form', modal).addEventListener('submit', async ev => {
      ev.preventDefault();
      const fd = new FormData(ev.target);
      try {
        await api('PUT', '/api/settings', { trip_factor: Number(fd.get('trip_factor')), warn_pct: Number(fd.get('warn_pct')), crit_pct: Number(fd.get('crit_pct')) });
        closeModal(); await afterChange('Engineering settings saved');
      } catch (e) { $('#eng-error', modal).textContent = e.message; }
    });
  }

  // ------------------------------------------------------------ search
  const searchInput = $('#search-input'), searchResults = $('#search-results');
  let searchHits = [], searchIndex = -1;
  function hideSearch() { searchResults.hidden = true; searchIndex = -1; }
  const runSearch = debounce(async () => {
    const q = searchInput.value.trim();
    if (!q) { hideSearch(); return; }
    try {
      searchHits = await api('GET', '/api/search?q=' + encodeURIComponent(q));
    } catch (e) { searchHits = []; }
    searchIndex = -1;
    searchResults.innerHTML = searchHits.length ? searchHits.map((h, i) => `
      <div class="search-hit" data-i="${i}"><span class="kind">${esc(h.kind)}</span><span class="title">${esc(h.title)}</span><span class="sub">${esc(h.subtitle)}</span></div>`).join('')
      : `<div class="search-empty">No matches for “${esc(q)}”</div>`;
    searchResults.hidden = false;
  }, 180);
  function gotoHit(h) {
    hideSearch(); searchInput.value = '';
    if (h.kind === 'building') {
      const bo = (state.overview ? state.overview.boards : []).find(b => b.building_id === h.id);
      navigate(boardHash('dashboard', bo ? bo.id : 0));
      return;
    }
    const focus = h.kind === 'board' ? '' : 'focus=' + h.kind + '-' + h.id;
    navigate(boardHash('dashboard', h.board_id, focus));
  }
  searchInput.addEventListener('input', runSearch);
  searchInput.addEventListener('focus', () => { if (searchInput.value.trim() && searchHits.length) searchResults.hidden = false; });
  searchInput.addEventListener('keydown', e => {
    if (searchResults.hidden) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      searchIndex = (searchIndex + (e.key === 'ArrowDown' ? 1 : -1) + searchHits.length) % searchHits.length;
      $$('.search-hit', searchResults).forEach((el, i) => el.classList.toggle('active', i === searchIndex));
    } else if (e.key === 'Enter') {
      const h = searchHits[searchIndex >= 0 ? searchIndex : 0];
      if (h) gotoHit(h);
    }
  });
  searchResults.addEventListener('mousedown', e => {
    const hit = e.target.closest('.search-hit');
    if (hit) { e.preventDefault(); gotoHit(searchHits[Number(hit.dataset.i)]); }
  });
  document.addEventListener('click', e => { if (!e.target.closest('#search')) hideSearch(); });

  // ------------------------------------------------------------ event delegation
  document.addEventListener('click', async e => {
    if (e.target.closest('[data-stop]')) return;
    const el = e.target.closest('[data-action]');
    if (!el) return;
    const action = el.dataset.action, id = Number(el.dataset.id);
    // Don't let a nested action (edit button inside a card) bubble to the card.
    e.stopPropagation();
    switch (action) {
      case 'open-building': {
        if (e.target.closest('.board-link, .foot')) return;
        const boards = state.overview.boards.filter(b => b.building_id === id);
        if (boards.length && !boards.some(b => b.id === state.boardId)) navigate(boardHash('dashboard', boards[0].id));
        else if (!boards.length) { state.board = null; state.boardId = 0; localStorage.removeItem('pds.boardId'); renderSidebar(); openBuildingOnly(id); }
        else { renderSidebar(); }
        break;
      }
      case 'view-building': {
        const boards = state.overview.boards.filter(b => b.building_id === id);
        if (boards.length) navigate(boardHash('dashboard', boards[0].id));
        else openBuildingOnly(id);
        break;
      }
      case 'open-board': navigate(boardHash('dashboard', id)); break;
      case 'open-node': navigate(boardHash('dashboard', state.boardId, 'focus=' + el.dataset.focus)); break;
      case 'sld-board': navigate(boardHash('sld', id)); break;
      case 'toggle-mccb': {
        const key = 'mccb-' + id, sec = document.getElementById(key);
        if (state.collapsed.has(key)) state.collapsed.delete(key); else state.collapsed.add(key);
        persistCollapsed(); sec.classList.toggle('collapsed');
        break;
      }
      case 'add-building': buildingForm(null); break;
      case 'edit-building': buildingForm(state.overview.buildings.find(b => b.id === id)); break;
      case 'add-board': boardForm(null, Number(el.dataset.building) || (state.board && state.board.building_id)); break;
      case 'edit-board': boardForm(state.board); break;
      case 'add-mccb': breakerForm('mccb', null); break;
      case 'edit-mccb': breakerForm('mccb', findMCCB(id)); break;
      case 'delete-mccb': deleteBreaker('mccb', findMCCB(id)); break;
      case 'add-mcb': breakerForm('mcb', null, id); break;
      case 'edit-mcb': breakerForm('mcb', findMCB(id), findMCB(id).mccb_id); break;
      case 'delete-mcb': deleteBreaker('mcb', findMCB(id)); break;
      case 'add-circuit': circuitForm(null, Number(el.dataset.mcb) || 0); break;
      case 'add-circuit-any': {
        const bo = state.overview.boards.find(b => b.id === state.boardId) || state.overview.boards[0];
        const detail = await api('GET', '/api/boards/' + bo.id);
        state.board = detail; state.boardId = bo.id;
        circuitForm(null, 0, detail);
        break;
      }
      case 'edit-circuit': {
        if (!canEdit()) return;
        let c = findCircuit(id), board = state.board;
        if (!c && el.dataset.board) { board = await api('GET', '/api/boards/' + el.dataset.board); state.board = board; for (const m of board.mccbs) for (const mb of m.mcbs) for (const x of mb.circuits) if (x.id === id) c = x; }
        if (c) circuitForm(c, 0, board);
        break;
      }
      case 'delete-circuit': deleteCircuit(findCircuit(id)); break;
      case 'refresh': render(); break;
    }
  });

  function openBuildingOnly(buildingId) {
    // A building with no boards: show it in the sidebar and offer to add a board.
    const b = state.overview.buildings.find(x => x.id === buildingId);
    state.board = null;
    $$('.building-card').forEach(c => c.classList.toggle('active', Number(c.dataset.id) === buildingId));
    $('#content').innerHTML = `
      <div class="empty">
        <h2>${esc(b.icon)} ${esc(b.name)} has no boards yet</h2>
        <p>Add a distribution board to start recording MCCBs, MCBs and circuits.</p>
        ${canManage() ? `<button class="btn btn-primary" data-action="add-board" data-building="${b.id}">+ Add board</button>` : ''}
      </div>`;
  }

  // ------------------------------------------------------------ init
  $('#today').textContent = fmtDate(new Date());
  const roleSel = $('#role-select');
  roleSel.value = state.role;
  roleSel.addEventListener('change', () => {
    state.role = roleSel.value; localStorage.setItem('pds.role', state.role);
    toast('Role: ' + state.role[0].toUpperCase() + state.role.slice(1));
    render();
  });
  $('#settings-btn').addEventListener('click', settingsModal);
  window.addEventListener('hashchange', render);
  render();
})();
