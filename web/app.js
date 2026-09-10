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
    boardFilter: { q: '', level: '' },
    hv: null,
    hvBoards: [],
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
  function plural(n, word, many) { return n + ' ' + (n === 1 ? word : (many || word + 's')); }
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
    // #/hv/<id> picks which drawing to show.
    if (view === 'hv' && parts[1] && parts[1] !== 'board') params.network = Number(parts[1]) || 0;
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
        case 'hv': await renderHV(params.network || 0); break;
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

  // Narrows the sidebar to the boards worth looking at: by text across the
  // board's code, level, location, technician and building, and by how heavily
  // loaded it is.
  function sidebarMatches() {
    const ov = state.overview, f = state.boardFilter;
    const q = f.q.trim().toLowerCase();
    const active = q !== '' || f.level !== '';
    const levelOK = bo => {
      if (f.level === 'warning') return bo.util_level !== 'normal';
      if (f.level === 'critical') return bo.util_level === 'critical';
      return true;
    };
    const textOK = bo => !q || [bo.code, bo.level, bo.location, bo.technician, bo.building_name]
      .some(v => String(v || '').toLowerCase().includes(q));
    const boards = ov.boards.filter(bo => levelOK(bo) && textOK(bo));
    const buildings = active
      ? ov.buildings.filter(b => boards.some(bo => bo.building_id === b.id))
      : ov.buildings;
    return { active, boards, buildings };
  }

  function renderSidebar() {
    const ov = state.overview;
    const t = ov.totals;
    const f = state.boardFilter;
    const m = sidebarMatches();
    const curBuilding = state.board ? state.board.building_id : 0;
    $('#sidebar').innerHTML = `
      <div class="side-head">
        <span class="side-title">BUILDINGS</span>
        ${canManage() ? '<button class="btn-plus" data-action="add-building" title="Add building">+</button>' : ''}
      </div>
      <div class="side-filter">
        <input id="board-filter" type="search" placeholder="Filter boards…" value="${esc(f.q)}" autocomplete="off" spellcheck="false">
      </div>
      <div class="level-chips">
        <button data-action="filter-level" data-level="" class="${f.level === '' ? 'on' : ''}">All</button>
        <button data-action="filter-level" data-level="warning" class="${f.level === 'warning' ? 'on' : ''}">Warning +</button>
        <button data-action="filter-level" data-level="critical" class="${f.level === 'critical' ? 'on' : ''}">Critical</button>
      </div>
      ${m.active
        ? `<div class="filter-count">${plural(m.boards.length, 'board')} of ${t.boards} · <a data-action="filter-clear">clear</a></div>`
        : `<div class="stat-tiles">
        <div class="stat-tile"><div class="n">${t.buildings}</div><div class="l">Buildings</div></div>
        <div class="stat-tile"><div class="n">${t.boards}</div><div class="l">Boards</div></div>
        <div class="stat-tile"><div class="n">${t.circuits}</div><div class="l">Circuits</div></div>
      </div>`}
      ${m.buildings.map(b => {
        const boards = m.boards.filter(x => x.building_id === b.id);
        // While filtering, every listed building is opened so the matches show.
        const open = m.active || b.id === curBuilding;
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
      ${!ov.buildings.length ? '<div class="side-empty">No buildings yet. Use + to add one.</div>' : ''}
      ${ov.buildings.length && !m.buildings.length ? '<div class="filter-none">No boards match this filter.</div>' : ''}`;

    const input = $('#board-filter');
    if (input) {
      input.addEventListener('input', () => {
        state.boardFilter.q = input.value;
        const pos = input.selectionStart;
        renderSidebar();
        const again = $('#board-filter');
        if (again) { again.focus(); again.setSelectionRange(pos, pos); }
      });
    }
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
    el.scrollIntoView({ behavior: 'smooth', block: 'center', inline: 'center' });
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

  // ------------------------------------------------------------ diagram zoom
  // Both diagrams are one SVG with a viewBox, so scaling is a matter of the
  // width it is rendered at; the canvas around it scrolls. That keeps text
  // crisp at any zoom, which a bitmap scale would not.
  const ZOOM_MIN = 0.25, ZOOM_MAX = 3, CANVAS_MIN_H = 360;

  function zoomBar() {
    return `<div class="zoom-bar">
      <button class="btn btn-sm" data-zoom="out" title="Zoom out" aria-label="Zoom out">−</button>
      <span class="zoom-label" id="zoom-label">100%</span>
      <button class="btn btn-sm" data-zoom="in" title="Zoom in" aria-label="Zoom in">+</button>
      <button class="btn btn-sm" data-zoom="fit" title="Fit the whole diagram in view">Fit</button>
      <button class="btn btn-sm" data-zoom="reset" title="Actual size">1:1</button>
      <span class="zoom-hint">Ctrl or ⌘ and scroll to zoom · drag to pan</span>
    </div>`;
  }

  // The canvas takes whatever is left of the window rather than only as much
  // as the drawing needs, so a wide diagram has the whole screen to be read in
  // instead of a band across the top with the page empty underneath.
  let canvasFillBound = false;
  const canvasScroll = {};
  function fillCanvas() {
    const canvas = $('#diagram-canvas');
    if (!canvas) return;
    if (!canvasFillBound) {
      canvasFillBound = true;
      window.addEventListener('resize', fillCanvas);
    }
    const top = canvas.getBoundingClientRect().top + window.scrollY;
    const want = Math.max(CANVAS_MIN_H, window.innerHeight - top - 24);
    canvas.style.height = want + 'px';
    // Give back whatever now hangs below the window, so the page itself does
    // not gain a scrollbar on top of the one inside the canvas.
    const over = document.documentElement.scrollHeight - window.innerHeight;
    if (over > 0 && want - over >= CANVAS_MIN_H) canvas.style.height = (want - over) + 'px';
  }

  // How wide the sheet is on screen, so a drawing can be set out across it
  // rather than run off the bottom. Taken from the canvas as it stands, which
  // is the one being redrawn; before there is one, from the window.
  function hvSheetWidth() {
    const canvas = $('#diagram-canvas');
    const w = (canvas && canvas.clientWidth) || (window.innerWidth - 300);
    return Math.max(960, Math.round(w) - 28);
  }

  function mountZoom(key) {
    const canvas = $('#diagram-canvas'), svg = canvas && $('svg', canvas);
    if (!svg) return;
    fillCanvas();
    const baseW = (svg.viewBox && svg.viewBox.baseVal && svg.viewBox.baseVal.width) || svg.getBoundingClientRect().width;
    if (!baseW) return;
    const store = 'pds.zoom.' + key;
    let zoom = 1;

    const apply = z => {
      zoom = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, z));
      svg.style.maxWidth = 'none';
      svg.style.width = (baseW * zoom) + 'px';
      const label = $('#zoom-label', canvas.parentNode);
      if (label) label.textContent = Math.round(zoom * 100) + '%';
      try { localStorage.setItem(store, String(zoom)); } catch (_) { /* private window */ }
    };
    // Fit never enlarges: a diagram narrower than the canvas stays at full size.
    const fitZoom = () => Math.min(1, Math.max(ZOOM_MIN, (canvas.clientWidth - 48) / baseW));

    let saved = 0;
    try { saved = Number(localStorage.getItem(store)) || 0; } catch (_) { /* ignore */ }
    apply(saved || fitZoom());

    // Editing the diagram redraws it, and on a wide one that would otherwise
    // throw the view back to the far left, away from what was just changed.
    const keep = canvasScroll[key];
    if (keep) { canvas.scrollLeft = keep[0]; canvas.scrollTop = keep[1]; }
    canvas.addEventListener('scroll', () => {
      canvasScroll[key] = [canvas.scrollLeft, canvas.scrollTop];
    }, { passive: true });

    $$('[data-zoom]', canvas.parentNode).forEach(btn => btn.addEventListener('click', () => {
      const what = btn.dataset.zoom;
      if (what === 'in') apply(zoom * 1.25);
      else if (what === 'out') apply(zoom / 1.25);
      else if (what === 'fit') apply(fitZoom());
      else apply(1);
    }));

    // Ctrl or Cmd with the wheel zooms about the pointer, so what you are
    // looking at stays under it. A plain wheel keeps scrolling the canvas.
    canvas.addEventListener('wheel', e => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      const rect = canvas.getBoundingClientRect();
      const ox = e.clientX - rect.left, oy = e.clientY - rect.top;
      const px = ox + canvas.scrollLeft, py = oy + canvas.scrollTop;
      const before = zoom;
      apply(zoom * (e.deltaY < 0 ? 1.12 : 1 / 1.12));
      const k = zoom / before;
      canvas.scrollLeft = px * k - ox;
      canvas.scrollTop = py * k - oy;
    }, { passive: false });

    // Drag the background to pan. Anything clickable, and anything that can be
    // picked up and moved, keeps the pointer for itself.
    let from = null;
    canvas.addEventListener('pointerdown', e => {
      if (e.button !== 0 || e.target.closest('[data-action], [data-drag]')) return;
      from = { x: e.clientX, y: e.clientY, l: canvas.scrollLeft, t: canvas.scrollTop };
      canvas.classList.add('grabbing');
      canvas.setPointerCapture(e.pointerId);
    });
    canvas.addEventListener('pointermove', e => {
      if (!from) return;
      canvas.scrollLeft = from.l - (e.clientX - from.x);
      canvas.scrollTop = from.t - (e.clientY - from.y);
    });
    ['pointerup', 'pointercancel'].forEach(t => canvas.addEventListener(t, () => {
      from = null;
      canvas.classList.remove('grabbing');
    }));
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
        ${zoomBar()}
        <div class="sld-canvas" id="diagram-canvas">${sldSVG(b)}</div>` : '<div class="empty"><h2>No board to draw</h2></div>'}
      </div>`;
    mountZoom('sld');
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

  // ------------------------------------------------------------ HV overview
  // The site-wide 22 kV picture that sits above the distribution boards:
  // incoming feeders, the switchgear each lands on, couplers between them, and
  // the outgoing ways that carry supply down to a board.
  const DEVICE_LABEL = {
    switchgear: 'Switchgear', isolator: 'Isolator', rccb: 'RCCB', elr: 'ELR',
    elcb: 'ELCB', transformer: 'Transformer', fuse: 'Fuse', meter: 'Meter', chiller: 'Chiller',
  };
  // What can stand where a way taps the bus, drawn on the conductor itself.
  const HEAD_KINDS = { switchgear: 'Switchgear', isolator: 'Switch' };
  const isHead = d => !!d && (d.kind === 'switchgear' || d.kind === 'isolator');

  async function renderHV(networkId) {
    const [net, boards, nets, sbs, refs] = await Promise.all([
      api('GET', '/api/hv' + (networkId ? '?network=' + networkId : '')),
      api('GET', '/api/boards'),
      api('GET', '/api/hv/networks'),
      api('GET', '/api/hv/switchboards'),
      api('GET', '/api/hv/refs'),
    ]);
    state.hv = net;
    state.hvBoards = boards;
    state.hvNets = nets;
    state.hvAllBoards = sbs;
    state.hvRefs = refs;
    setActiveTab('hv');
    app.innerHTML = `
      <div class="page">
        <div class="hv-head">
          <div>
            <h1 class="hv-title">${esc(net.name)}
              ${canManage() ? '<button class="btn btn-ghost btn-sm" data-action="hv-rename" title="Rename">✎</button>' : ''}
            </h1>
            <div class="muted">${plural(net.board_count, 'switchboard')} · ${plural(net.section_count, 'bus section')} · ${plural(net.feeder_count, 'feeder')} · ${plural(net.way_count, 'outgoing way')}</div>
          </div>
          <div class="hv-counts">
            <div><div class="n">${net.board_count}</div><div class="l">Boards</div></div>
            <div><div class="n">${net.section_count}</div><div class="l">Sections</div></div>
            <div><div class="n">${net.feeder_count}</div><div class="l">Feeders</div></div>
            <div><div class="n">${net.way_count}</div><div class="l">Ways</div></div>
            <div><div class="n">${net.transformer_count}</div><div class="l">Transformers</div></div>
            <div><div class="n">${net.protected_count}</div><div class="l">Protected</div></div>
            <div><div class="n">${net.linked_count}</div><div class="l">Linked</div></div>
          </div>
        </div>
        ${canEdit() ? `<p class="sld-hint">Click a destination box to open that board. <b>+ Way</b> taps a bus section, which every feeder on it backs. On a way, <b>+</b> fits a device below and clicking one changes or removes it. To arrange a bar, take a way or feeder by its dot on the busbar and drop it wherever along that bar it belongs. A way can feed a switchboard drawn below, or one on another drawing, instead of a destination box.</p>` : ''}
        ${nets.length > 1 || canManage() ? `<div class="hv-tiers">
          ${nets.map(x => `<a class="tier-chip${x.id === net.id ? ' on' : ''}" href="#/hv/${x.id}">
            <b>${esc(x.name)}</b>${x.tier ? `<span class="tier-tag">${x.tier === 'ht' ? 'HT' : 'LT'}</span>` : ''}</a>`).join('')}
          ${canManage() ? '<button class="btn btn-sm" data-action="hv-add-network">+ Add drawing</button>' : ''}
        </div>` : ''}
        <div class="hv-toolbar">
          ${canManage() ? '<button class="btn btn-primary" data-action="hv-add-board">+ Add switchboard</button>' : ''}
          ${canManage() ? '<button class="btn" data-action="hv-add-section">+ Add bus section</button>' : ''}
          ${canManage() && net.section_count ? '<button class="btn" data-action="hv-add-coupler">+ Add coupler</button>' : ''}
        </div>
        ${net.section_count && canEdit() ? `<div class="palette">
          <span class="palette-label">DRAG ON:</span>
          ${Object.entries(DEVICE_LABEL).map(([k, l]) => `<span class="palette-chip" data-kind="${k}">${esc(l)}</span>`).join('')}
          <span class="palette-hint">Drop one onto a way, or drag a device to move it along its conductor.</span>
        </div>` : ''}
        ${net.section_count ? zoomBar() : ''}
        <div class="sld-canvas" id="diagram-canvas">${net.section_count ? hvSVG(net) : '<div class="empty"><h2>Nothing on the busbar yet</h2><p>Add a switchboard, then the feeders backing it and the ways tapping it.</p></div>'}</div>
      </div>`;
    mountZoom('hv-' + net.id);
    hvMountDrag();
    applyFocus();
  }

  // Dragging on the diagram. A drop lands the item at a place on a way and the
  // conductor is redrawn through it, so the connection follows from where it
  // was dropped rather than having to be drawn by hand.
  let hvDragEndedAt = 0;

  function hvMountDrag() {
    const canvas = $('#diagram-canvas'), svg = canvas && $('svg', canvas);
    if (!svg || !canEdit()) return;
    const NS = 'http://www.w3.org/2000/svg';

    const hint = document.createElementNS(NS, 'rect');
    hint.setAttribute('class', 'drop-hint');
    hint.setAttribute('rx', '8');
    hint.style.display = 'none';
    svg.appendChild(hint);

    const toSvg = (cx, cy) => {
      const m = svg.getScreenCTM();
      if (!m) return null;
      try { return new DOMPoint(cx, cy).matrixTransform(m.inverse()); } catch (_) { return null; }
    };
    const zoneAt = (cx, cy) => {
      const p = toSvg(cx, cy);
      if (!p) return null;
      return hvDrops.find(z => p.x >= z.x && p.x <= z.x + z.w && p.y >= z.y && p.y <= z.y + z.h) || null;
    };
    // Where a whole column would land. The nearest section is taken rather than
    // only the one under the pointer, so a column can be dragged into the gap
    // at either end of a bar without having to hit it exactly.
    // Where a column would land: anywhere along the bus section under the
    // pointer, on a light grid so columns still line up with one another.
    const GRID = 6;
    const slotAt = (cx, cy, kind) => {
      const p = toSvg(cx, cy);
      if (!p || !hvLayout) return null;
      let best = null, bestGap = Infinity;
      hvLayout.groups.forEach(run => {
        if (run.kind !== kind || p.y < run.top || p.y > run.bottom) return;
        const gap = p.x < run.secLeft ? run.secLeft - p.x
          : (p.x > run.secLeft + run.secWidth ? p.x - run.secLeft - run.secWidth : 0);
        if (gap < bestGap) { bestGap = gap; best = run; }
      });
      if (!best || bestGap > 140) return null;
      const from = best.secLeft + Math.round((p.x - best.secLeft) / GRID) * GRID;
      const x = Math.min(best.secLeft + best.secWidth, Math.max(best.secLeft, from));
      return {
        sectionId: best.sectionId, name: best.name, x,
        offset: (x - best.secLeft) / best.secWidth,
        y: best.top, h: best.bottom - best.top,
      };
    };

    let drag = null, press = null;

    const colEl = (kind, id) => svg.querySelector(`g.hv-col[data-col="${kind}-${id}"]`);
    const shift = (el, dx) => el.setAttribute('transform', dx ? `translate(${dx} 0)` : '');

    // While a column is dragged it comes with the pointer and the rest of the
    // bar opens a gap where it would land, so what will happen is the drawing
    // itself rather than a marker beside it.
    function layOut(e) {
      const el = colEl(drag.column, drag.id);
      if (!el) return;
      const held = drag.zone;
      if (held) {
        // It lands where it was dropped, so it follows the pointer there.
        drag.snapTo = held.x - Number(el.dataset.home);
        shift(el, drag.snapTo);
        return;
      }
      const p = toSvg(e.clientX, e.clientY);
      if (p) shift(el, p.x - drag.fromX);
    }

    function begin(source, e) {
      drag = { ...source, zone: null };
      if (!drag.column) {
        const ghost = document.createElement('div');
        ghost.className = 'drag-ghost';
        ghost.textContent = source.label;
        document.body.appendChild(ghost);
        drag.ghost = ghost;
      } else {
        const p = toSvg(e.clientX, e.clientY);
        drag.fromX = p ? p.x : 0;
        const el = colEl(drag.column, drag.id);
        if (el) el.classList.add('dragging');
        svg.classList.add('moving-column');
      }
      canvas.classList.add('dropping');
      move(e);
    }
    function move(e) {
      if (!drag) return;
      if (drag.ghost) {
        drag.ghost.style.left = e.clientX + 14 + 'px';
        drag.ghost.style.top = e.clientY + 14 + 'px';
      }
      const z = drag.column ? slotAt(e.clientX, e.clientY, drag.column) : zoneAt(e.clientX, e.clientY);
      drag.zone = z;
      if (drag.column) {
        // A guide down the bar at the place it will land.
        if (z) {
          hint.setAttribute('x', z.x - 1.5); hint.setAttribute('y', z.y);
          hint.setAttribute('width', 3); hint.setAttribute('height', z.h);
          hint.classList.add('bar');
          hint.style.display = '';
        } else {
          hint.style.display = 'none';
        }
        layOut(e);
        return;
      }
      if (z) {
        hint.setAttribute('x', z.x); hint.setAttribute('y', z.y);
        hint.setAttribute('width', z.w); hint.setAttribute('height', z.h);
        hint.style.display = '';
      } else {
        hint.style.display = 'none';
      }
      drag.ghost.classList.toggle('over', !!z);
    }
    // Put every column back where the drawing says it belongs.
    function unshift() {
      $$('g.hv-col', svg).forEach(el => { el.removeAttribute('transform'); el.classList.remove('dragging'); });
      svg.classList.remove('moving-column');
    }

    async function finish() {
      const d = drag;
      drag = null;
      if (!d) return;
      if (d.ghost) d.ghost.remove();
      hint.style.display = 'none';
      canvas.classList.remove('dropping');
      hvDragEndedAt = Date.now();
      if (d.column) {
        // Let go and the column snaps into the gap the others opened for it,
        // then the diagram is redrawn from what was saved.
        const el = colEl(d.column, d.id);
        if (el) { el.classList.remove('dragging'); shift(el, d.zone ? (d.snapTo || 0) : 0); }
        if (!d.zone) { unshift(); return; }
        // The slot counts the columns as drawn, so a column moving right
        // within its own section passes over its own place on the way.
        try {
          await api('POST', `/api/hv/${d.column === 'way' ? 'ways' : 'feeders'}/${d.id}/place`,
            { section_id: d.zone.sectionId, offset_x: Number(d.zone.offset.toFixed(5)) });
          await afterChange(d.label + ' moved');
        } catch (err) { unshift(); toast(err.message, 'error'); }
        return;
      }
      if (!d.zone) return;
      try {
        const on = { way_id: d.zone.wayId || 0, feeder_id: d.zone.feederId || 0, after_id: d.zone.afterId };
        if (d.mode === 'new') {
          await api('POST', '/api/hv/devices', { ...on, kind: d.kind, name: '' });
          await afterChange(d.label + ' fitted');
        } else {
          await api('POST', '/api/hv/devices/' + d.deviceId + '/place', on);
          await afterChange(d.label + ' moved');
        }
      } catch (err) { toast(err.message, 'error'); }
    }

    $$('.palette-chip').forEach(chip => chip.addEventListener('pointerdown', e => {
      e.preventDefault();
      chip.setPointerCapture(e.pointerId);
      begin({ mode: 'new', kind: chip.dataset.kind, label: chip.textContent.trim() }, e);
      const onMove = ev => move(ev);
      const onUp = () => {
        chip.removeEventListener('pointermove', onMove);
        chip.removeEventListener('pointerup', onUp);
        finish();
      };
      chip.addEventListener('pointermove', onMove);
      chip.addEventListener('pointerup', onUp);
    }));

    // Something on the diagram: a small movement is a click to open it, a
    // larger one picks it up. A device moves along its own conductor; a way or
    // a feeder moves as a whole column along the bar.
    svg.addEventListener('pointerdown', e => {
      if (e.target.closest('.sld-btn, .sld-pill') || e.button !== 0) return;
      // Deliberately no pointer capture yet: capturing here would retarget the
      // click that follows, and a press is a click until it moves.
      const col = e.target.closest('[data-drag]');
      if (col) {
        press = { column: col.dataset.drag, id: Number(col.dataset.dragId), label: col.dataset.dragLabel || 'Column',
          x: e.clientX, y: e.clientY, pointerId: e.pointerId };
        return;
      }
      const g = e.target.closest('g.hv-node[data-action=hv-edit-device]');
      if (!g) return;
      press = { id: Number(g.dataset.id), x: e.clientX, y: e.clientY, pointerId: e.pointerId,
        label: (g.querySelector('title') || {}).textContent || 'Device' };
    });
    svg.addEventListener('pointermove', e => {
      if (drag) { move(e); return; }
      if (press && Math.hypot(e.clientX - press.x, e.clientY - press.y) > 6) {
        if (press.column) {
          begin({ mode: 'column', column: press.column, id: press.id, label: press.label }, e);
        } else {
          begin({ mode: 'move', deviceId: press.id, label: press.label.split(' - ')[0] }, e);
        }
        try { svg.setPointerCapture(press.pointerId); } catch (_) { /* already gone */ }
        press = null;
      }
    });
    ['pointerup', 'pointercancel'].forEach(t => svg.addEventListener(t, () => { press = null; finish(); }));
  }

  // Where a dragged item may be dropped, in diagram coordinates, gathered while
  // the drawing is built so the two can never disagree.
  let hvDrops = [];

  // The bands a whole column may be dragged along, and the order the columns
  // are currently in, so a drop can be turned into a position.
  let hvLayout = null;

  function hvSVG(net) {
    hvDrops = [];
    const C = { bus: '#0b74c4', line: '#334155', tx: '#7c3aed', prot: '#d97a06', dest: '#0f8a4f', muted: '#94a3b8', label: '#475569' };
    const WAY_W = 182, WAY_GAP = 16, FEEDER_W = 248, SECTION_GAP = 120, MARGIN = 44, BOARD_GAP = 104;
    const STACK_GAP = 150;
    const DEV_GAP = 64, DEST_H = 58;
    // How far to the left of a conductor a rotated designation is written, far
    // enough out that it never sits on the symbol it names.
    const TAG_X = 30;
    // The step between one sideways run into a shared box and the next.
    const ROUTE_ROW = 18;
    // Room enough that a symbol and its rotated designation never crowd the
    // next one down the conductor.
    const devHeight = d => (d.kind === 'transformer' ? 84 : (d.kind === 'chiller' ? 88 : 58));
    // How far a chain of devices reaches below the head switchgear, which is
    // drawn on the conductor itself rather than below it.
    const chainOf = list => {
      let h = 0;
      (list || []).forEach((d, i) => { if (!(i === 0 && isHead(d))) h += devHeight(d); });
      return h;
    };

    // Each switchboard is laid out on its own and they are stacked down the
    // page, the way a site steps down from 22kV through 6.6kV to 400V. A
    // section is as wide as whichever it has more of: incomers above the bar or
    // ways below it. The ways are spread along the whole section, because any
    // of them is fed by the section rather than by one particular feeder.
    // A bar can be fed from both ends. When anything on a section carries a
    // side, the incomers take a band in the middle and the ways run out along
    // the bar either side of it, so no way ever hangs off the point an incomer
    // lands on. With no sides in play a section is laid out as it always was.
    const SIDE_PAD = 32;
    const runW = n => (n ? n * WAY_W + (n - 1) * WAY_GAP : 0);
    const split = (list, side) => list.filter(x => (x.side === 'r') === (side === 'r'));
    const planSection = sec => {
      const sided = sec.feeders.some(f => f.side) || sec.ways.some(w => w.side);
      const plan = { sec, sided };
      plan.autoF = sec.feeders.filter(f => f.offset_x == null);
      plan.autoW = sec.ways.filter(w => w.offset_x == null);
      if (!sided) {
        const nf = Math.max(1, plan.autoF.length), nw = Math.max(1, plan.autoW.length);
        plan.width = Math.max(nf * FEEDER_W, runW(nw), 420);
        return plan;
      }
      plan.lf = split(sec.feeders, 'l');
      plan.rf = split(sec.feeders, 'r');
      plan.lw = split(sec.ways, 'l');
      plan.rw = split(sec.ways, 'r');
      plan.bandW = Math.max(1, plan.autoF.length) * FEEDER_W;
      plan.lwW = runW(plan.lw.filter(w => w.offset_x == null).length);
      plan.rwW = runW(plan.rw.filter(w => w.offset_x == null).length);
      plan.width = Math.max(plan.bandW + plan.lwW + plan.rwW + SIDE_PAD * 2, 420);
      return plan;
    };
    const boards = (net.switchboards || []).map(board => {
      const secs = board.sections.map(planSection);
      const contentW = secs.reduce((s, g) => s + g.width, 0) + Math.max(0, secs.length - 1) * SECTION_GAP;
      // Ways that name the same destination share a box and are run into it
      // sideways, and each of those runs needs a level of its own above the
      // boxes so no two lie on top of one another. They are counted across the
      // whole board, because a board fed from both ends can have the two
      // supplies on different lengths of its bar.
      const seen = {};
      board.sections.forEach(sec => sec.ways.forEach(w => {
        const k = w.dest_switchboard_id ? 'sb:' + w.dest_switchboard_id
          : (w.dest_board_id ? 'bd:' + w.dest_board_id
            : ((w.dest_label || '').trim().toUpperCase() ? 'la:' + (w.dest_label || '').trim().toUpperCase() : ''));
        if (k) seen[k] = (seen[k] || 0) + 1;
      }));
      const routeRows = Object.values(seen).reduce((t, n) => t + (n > 1 ? n : 0), 0);
      let feederChain = 0, wayChain = 0;
      board.sections.forEach(sec => {
        // An incomer is drawn from its first symbol down, so the room it needs
        // below that symbol is every symbol on it but the last.
        sec.feeders.forEach(f => {
          const d = f.devices || [];
          let h = 0;
          for (let k = 0; k < d.length - 1; k++) h += devHeight(d[k]);
          feederChain = Math.max(feederChain, h);
        });
        sec.ways.forEach(w => { wayChain = Math.max(wayChain, chainOf(w.devices)); });
      });
      return { board, secs, contentW, feederChain, wayChain, routeRows };
    });

    const boardAt = {};
    boards.forEach((g, i) => {
      g.i = i;
      boardAt[g.board.id] = g;
      g.height = 22 + 78 + 88 + g.feederChain + 56 + DEV_GAP + g.wayChain + 46
        + g.routeRows * ROUTE_ROW + DEST_H;
    });

    // Boards a way runs down into stay in one stack, one above the next,
    // because that conductor is drawn straight down the sheet. Stacks with
    // nothing running between them stand side by side instead, so a drawing of
    // several boards spreads across the sheet rather than off the bottom of it.
    const stackOf = new Map();
    let stacks = boards.map(g => {
      const s = { members: [g] };
      stackOf.set(g.board.id, s);
      return s;
    });
    boards.forEach(g => g.board.sections.forEach(sec => sec.ways.forEach(w => {
      const down = w.dest_switchboard_id ? boardAt[w.dest_switchboard_id] : null;
      if (!down || down.i <= g.i) return;
      const a = stackOf.get(g.board.id), b = stackOf.get(down.board.id);
      if (!a || !b || a === b) return;
      b.members.forEach(m => stackOf.set(m.board.id, a));
      a.members = a.members.concat(b.members).sort((x, y) => x.i - y.i);
      b.members = [];
    })));
    stacks = stacks.filter(s => s.members.length);
    stacks.forEach(s => {
      s.width = s.members.reduce((m, g) => Math.max(m, g.contentW), 260);
      s.height = s.members.reduce((t, g) => t + g.height, 0)
        + Math.max(0, s.members.length - 1) * BOARD_GAP;
    });

    // Fill the sheet across before going down: stacks are set out in rows as
    // wide as the canvas on screen, and a row wraps once the next one will not
    // fit beside it.
    const avail = Math.max(900, hvSheetWidth() - MARGIN * 2);
    const rows = [];
    stacks.forEach(s => {
      let row = rows[rows.length - 1];
      if (!row || row.width + STACK_GAP + s.width > avail) {
        row = { stacks: [], width: 0, height: 0 };
        rows.push(row);
      }
      row.width += (row.stacks.length ? STACK_GAP : 0) + s.width;
      row.height = Math.max(row.height, s.height);
      row.stacks.push(s);
    });
    const W = Math.max(760, rows.reduce((m, r) => Math.max(m, r.width), 260) + MARGIN * 2);

    // Everything is placed before anything is drawn, so a way can be run down
    // to a bus on a board that has not been drawn yet.
    let rowTop = MARGIN + 16;
    rows.forEach(r => {
      let x = MARGIN + (W - MARGIN * 2 - r.width) / 2;
      r.stacks.forEach(s => {
        let top = rowTop;
        s.members.forEach(g => {
          g.left = x + (s.width - g.contentW) / 2;
          g.feederY = top + 22;
          g.swY = g.feederY + 78;
          g.busY = g.swY + 88 + g.feederChain;
          g.wayTapY = g.busY + 56;
          g.destY = g.wayTapY + DEV_GAP + g.wayChain + 46 + g.routeRows * ROUTE_ROW;
          g.bottom = g.destY + DEST_H;
          top = g.bottom + BOARD_GAP;
        });
        x += s.width + STACK_GAP;
      });
      rowTop += r.height + BOARD_GAP;
    });
    const H = boards.length ? rowTop - BOARD_GAP + MARGIN : 240;

    // Each board's sections and the columns along them, across its own width.
    boards.forEach(g => {
      let x = g.left;
      g.secs.forEach(sg => {
        sg.left = x; sg.cx = x + sg.width / 2;
        sg.busL = sg.left; sg.busR = sg.left + sg.width;
        // Every column's place on the bar, and the groups a column may be
        // dragged within: one group per kind on an unsided section, one per
        // kind per side on a sided one.
        const at = new Map();
        sg.groups = [];
        const lay = (list, kind, side, start, pitch, colW) => {
          const auto = list.filter(item => item.offset_x == null);
          auto.forEach((item, k) => at.set(item.id, start + k * pitch + colW / 2));
          const width = auto.length ? auto.length * pitch - (pitch - colW) : colW;
          sg.groups.push({
            kind, side, ids: auto.map(item => item.id), left: start, width,
            cx: start + width / 2, pitch, colW,
          });
        };
        if (!sg.sided) {
          const nf = sg.autoF.length, nw = sg.autoW.length;
          lay(sg.sec.feeders, 'feeder', '', sg.cx - nf * FEEDER_W / 2, FEEDER_W, FEEDER_W);
          lay(sg.sec.ways, 'way', '', sg.cx - runW(nw) / 2, WAY_W + WAY_GAP, WAY_W);
        } else {
          const spare = sg.width - (sg.bandW + sg.lwW + sg.rwW);
          const pad = Math.max(SIDE_PAD, spare / 2);
          const bandLeft = sg.left + sg.lwW + pad;
          lay(sg.lw, 'way', 'l', sg.left + Math.max(0, (spare - pad * 2) / 2), WAY_W + WAY_GAP, WAY_W);
          lay(sg.lf, 'feeder', 'l', bandLeft, FEEDER_W, FEEDER_W);
          lay(sg.rf, 'feeder', 'r', bandLeft + sg.lf.length * FEEDER_W, FEEDER_W, FEEDER_W);
          lay(sg.rw, 'way', 'r', bandLeft + sg.bandW + pad, WAY_W + WAY_GAP, WAY_W);
        }
        // A column placed by hand sits exactly where it was put, as a fraction
        // of the section's width, and takes no slot from the rest.
        const placed = item => sg.left + Math.min(1, Math.max(0, Number(item.offset_x))) * sg.width;
        sg.sec.feeders.forEach(f => { if (f.offset_x != null) at.set(f.id, placed(f)); });
        sg.sec.ways.forEach(w => { if (w.offset_x != null) at.set(w.id, placed(w)); });
        sg.feederX = sg.sec.feeders.map(f => at.get(f.id));
        sg.wayX = sg.sec.ways.map(w => at.get(w.id));
        x += sg.width + SECTION_GAP;
      });
    });

    // A way feeding the board below lands on the nearest length of its bus,
    // and the bus is run out to meet it the way a drawing runs it out.
    boards.forEach(g => g.secs.forEach(sg => sg.sec.ways.forEach((way, i) => {
      const down = way.dest_switchboard_id ? boardAt[way.dest_switchboard_id] : null;
      if (!down || down.i <= g.i) return;
      const x = sg.wayX[i];
      let best = null, bestGap = Infinity;
      down.secs.forEach(t => {
        const gap = x < t.left ? t.left - x : (x > t.left + t.width ? x - t.left - t.width : 0);
        if (gap < bestGap) { bestGap = gap; best = t; }
      });
      if (!best) return;
      best.busL = Math.min(best.busL, x - 14);
      best.busR = Math.max(best.busR, x + 14);
    })));
    // A board's caption sits at the left-hand end of the bus it names, which
    // may reach further left than the columns once a way has landed on it.
    boards.forEach(g => { g.busL = g.secs.reduce((m, sg) => Math.min(m, sg.busL), g.left); });

    // Where a whole column may be dropped when one is dragged along a bar: one
    // run per kind, and per side where a bar is fed from both ends. Every run
    // carries its own band, because the boards are stacked and a point on the
    // page belongs to one board's ways or another's incomers.
    hvLayout = { groups: [] };
    boards.forEach(g => g.secs.forEach(sg => sg.groups.forEach(run => {
      hvLayout.groups.push({
        ...run, sectionId: sg.sec.id, name: sg.sec.name,
        secLeft: sg.left, secWidth: sg.width,
        top: run.kind === 'way' ? g.busY - 20 : g.feederY - 46,
        bottom: run.kind === 'way' ? g.bottom + 20 : g.busY + 20,
      });
    })));

    const out = [];

    boards.forEach(g => {
      const board = g.board;
      const { feederY, swY, busY, wayTapY, destY } = g;
      const secAt = {};
      g.secs.forEach((sg, i) => { secAt[sg.sec.id] = { ...sg, i }; });

      // Worked out before anything on this board is drawn: where each way's
      // chain of symbols sits, where its conductor ends, and which ways feed
      // the same thing. A board fed from both ends of the bar has a supply
      // from each - and they can be on different lengths of the bar - so the
      // ways are gathered across the whole board rather than one section at a
      // time, and a drawing runs all of them into one box.
      const planOf = {};
      const allPlans = [];
      g.secs.forEach(sg => sg.sec.ways.forEach((way, i) => {
        const down = way.dest_switchboard_id ? boardAt[way.dest_switchboard_id] : null;
        const feedsDown = !!(down && down.i > g.i);
        const across = !down && !!way.dest_switchboard_id && !!way.dest_network_id;
        const head = way.devices[0];
        const headIsSwitch = isHead(head);
        const chain = [];
        let cy = wayTapY + DEV_GAP;
        way.devices.forEach((dev, di) => {
          if (di === 0 && headIsSwitch) return;
          chain.push({ dev, y: cy });
          cy += devHeight(dev);
        });
        const tail = chain.length ? chain[chain.length - 1] : null;
        const endsAtLoad = !feedsDown && !!tail && tail.dev.kind === 'chiller';
        const pl = { way, i, wx: sg.wayX[i], down, feedsDown, across, head, headIsSwitch, chain, cy, tail, endsAtLoad };
        planOf[way.id] = pl;
        allPlans.push(pl);
      }));
      // Two ways that name the same destination land in the same box.
      const destKey = w => (w.dest_switchboard_id ? 'sb:' + w.dest_switchboard_id
        : (w.dest_board_id ? 'bd:' + w.dest_board_id
          : ((w.dest_label || '').trim().toUpperCase() ? 'la:' + (w.dest_label || '').trim().toUpperCase() : '')));
      const boxes = [];
      const boxByKey = {};
      allPlans.forEach(pl => {
        if (pl.feedsDown || pl.endsAtLoad) return;
        const key = destKey(pl.way);
        let box = key ? boxByKey[key] : null;
        if (!box) {
          box = { members: [], w: WAY_W - 12 };
          boxes.push(box);
          if (key) boxByKey[key] = box;
        }
        pl.box = box;
        pl.feed = box.members.length;
        box.members.push(pl);
      });
      // Centre each box under the ways feeding it, then hold the boxes apart
      // so two of them can never sit on top of one another.
      boxes.forEach(box => {
        box.cx = box.members.reduce((t, m) => t + m.wx, 0) / box.members.length;
        box.w = WAY_W - 12 + (box.members.length - 1) * 24;
      });
      boxes.sort((a, b) => a.cx - b.cx);
      let edge = -Infinity;
      boxes.forEach(box => {
        if (box.cx - box.w / 2 < edge) box.cx = edge + box.w / 2;
        edge = box.cx + box.w / 2 + 14;
      });
      // Where each supply enters the top of its box, and which level its run
      // takes to get there. Levels run across the whole board, longest run
      // highest, so the runs nest and never share a line.
      const routed = [];
      boxes.forEach(box => box.members.forEach((m, k) => {
        m.enterX = box.members.length > 1
          ? box.cx - box.w / 2 + box.w * (k + 1) / (box.members.length + 1)
          : m.wx;
        if (box.members.length > 1) routed.push(m);
      }));
      routed.sort((a, b) => Math.abs(b.wx - b.enterX) - Math.abs(a.wx - a.enterX));
      routed.forEach((m, k) => { m.level = k; });

      // The switchboard itself: what it is and what it is rated at.
      const rating = [board.phases, board.frequency].filter(Boolean).join(', ')
        + (board.current_a != null || board.fault_ka != null
          ? (board.phases || board.frequency ? ' ' : '')
            + [board.current_a != null ? fmtA(board.current_a) + 'A' : '',
              board.fault_ka != null ? fmtA(board.fault_ka) + 'kA' : ''].filter(Boolean).join('/')
          : '');
      out.push(`<g id="hv-board-${board.id}" class="${canManage() ? 'hv-node' : ''}" ${canManage() ? `data-action="hv-edit-board" data-id="${board.id}"` : ''}>
        <title>${esc(board.name)}${rating ? ' · ' + esc(rating) : ''}${canManage() ? ' - click to change its rating' : ''}</title>
        <rect x="${g.busL}" y="${busY - 62}" width="300" height="40" fill="transparent"/>
        <text x="${g.busL + 2}" y="${busY - 44}" font-size="15" font-weight="700" fill="${C.bus}">${esc(board.name)}</text>
        <text x="${g.busL + 2}" y="${busY - 29}" font-size="11" font-weight="600" fill="${C.muted}" letter-spacing=".4">${esc(rating || (canManage() ? 'Set the rating' : ''))}</text>
      </g>`);

      g.secs.forEach(sg => {
        const sec = sg.sec;

        // The section's own busbar. A coupler is what separates it from the next.
        out.push(`<line x1="${sg.busL}" y1="${busY}" x2="${sg.busR}" y2="${busY}"
          stroke="${C.bus}" stroke-width="6" stroke-linecap="round"/>`);

        // Incomers, spread across the top of the section.
        const nf = sec.feeders.length;
        sec.feeders.forEach((f, i) => {
          const cx = sg.feederX[i];
          const at = out.length;
          const fGrab = canManage() ? ` data-drag="feeder" data-drag-id="${f.id}" data-drag-label="${esc(f.name)}"` : '';
          out.push(`<rect class="hv-col-plate${canManage() ? ' hv-grab' : ''}"${fGrab} fill="${C.bus}" fill-opacity="0"
            x="${cx - FEEDER_W / 2 + 8}" y="${feederY - 30}" width="${FEEDER_W - 16}" height="${busY - feederY + 20}" rx="12"/>`);
          const gen = f.kind === 'generator';
          const fRef = hvRefFor(f.name, 'way');
          out.push(`<g${canManage() ? ' class="hv-grab"' : ''}${fGrab}${fRef ? ` data-action="hv-open-ref" data-id="${fRef.network_id}" data-ref="hv-${fRef.kind}-${fRef.id}"` : ''}>
            <title>${esc(f.name)}${fRef ? ' · on ' + esc(fRef.network_name) + ' as a way - click to open it there'
              : (f.name ? ' · no way of this designation on another drawing' : '')}${f.side ? ' · backs the ' + (f.side === 'r' ? 'right' : 'left') + ' of the bar' : ''}${gen ? ' · generator' : ''}${canManage() ? ' - drag it anywhere along the busbar' : ''}</title>
            <rect x="${cx - FEEDER_W / 2 + 10}" y="${feederY - 26}" width="${FEEDER_W - 20}" height="${gen ? 56 : 46}" rx="9" fill="transparent"/>
            <text x="${cx}" y="${gen ? feederY - 12 : feederY}" text-anchor="middle" font-size="19" font-weight="700" fill="${fRef ? C.dest : C.label}">${esc(f.name)}${f.side ? ` <tspan font-size="15" fill="${C.bus}">(${f.side.toUpperCase()})</tspan>` : ''}</text>
            ${gen ? hvGenerator(cx, feederY + 12, C.line)
              : `<text x="${cx}" y="${feederY + 18}" text-anchor="middle" font-size="12" font-weight="600" fill="${C.muted}" letter-spacing=".8">${esc(f.voltage)}${f.source ? ' · ' + esc(f.source).toUpperCase() : ''}</text>`}
          </g>`);
          out.push(`<line x1="${cx}" y1="${feederY + (gen ? 36 : 28)}" x2="${cx}" y2="${busY}" stroke="${C.bus}" stroke-width="3"/>`);
          const fHandle = hvHandle(cx, busY, C.bus, canManage() ? fGrab : '',
            esc(f.name) + ' - drag it anywhere along the busbar');

          if (canManage()) {
            out.push(`<g class="sld-btn" data-action="hv-edit-feeder" data-id="${f.id}"><title>Edit feeder ${esc(f.name)}</title>
              <rect x="${cx - 68}" y="${swY - 12}" width="24" height="24" rx="7" fill="#fff" stroke="${C.bus}" stroke-opacity=".4"/>
              <g transform="translate(${cx - 66} ${swY - 10})" fill="none" stroke="${C.bus}" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">${SLD_ICONS.edit}</g></g>`);
          }
          // Whatever is on the incomer, in the order it is on it: on a
          // high-tension board the switchgear and then anything below, on a
          // low-tension one the transformer and then the switchgear it lands
          // through. An incomer drawn before there were devices still gets its
          // breaker.
          const fDevs = f.devices || [];
          // A low-tension incomer lands through switchgear named after the
          // board itself, so leaving the designation blank is the normal case.
          const fAlt = net.tier === 'lt' ? board.name : (f.switchgear || f.name);
          if (!fDevs.length) {
            out.push(hvBreaker(cx, swY, C.bus));
            out.push(hvTag(cx - TAG_X, swY + 26, f.switchgear || f.name, C.label, 13));
            if (canEdit()) hvDrops.push({ x: cx - 46, y: swY + 24, w: 92, h: 30, feederId: f.id, afterId: 0 });
          }
          let fy = swY;
          fDevs.forEach(dev => {
            out.push(hvDevice(cx, fy, dev, isHead(dev) ? fAlt : ''));
            fy += devHeight(dev);
            if (canEdit()) hvDrops.push({ x: cx - 46, y: fy - 34, w: 92, h: 30, feederId: f.id, afterId: dev.id });
          });
          out.push(fHandle);
          out.push(`<g class="hv-col" id="hv-feeder-${f.id}" data-col="feeder-${f.id}" data-home="${cx}">${out.splice(at).join('')}</g>`);
        });
        if (!nf) {
          out.push(`<text x="${sg.cx}" y="${swY}" text-anchor="middle" font-size="12" fill="${C.muted}">No incoming feeder on this section</text>`);
        }

        // The section's name, under its own length of bar, and the controls
        // that add to it.
        out.push(`<g class="${canManage() ? 'hv-node' : ''}" ${canManage() ? `data-action="hv-edit-section" data-id="${sec.id}"` : ''}>
          <title>${esc(sec.name)}${sec.feeders.length ? ' · backed by ' + esc(sec.feeders.map(f => f.name).join(', ')) : ''}${canManage() ? ' - click to rename' : ''}</title>
          <rect x="${sg.left}" y="${busY - 26}" width="${Math.min(240, sg.width)}" height="20" fill="transparent"/>
          <text x="${sg.left + 8}" y="${busY - 12}" font-size="12" font-weight="700" fill="${C.bus}" letter-spacing="1">${esc(sec.name.toUpperCase())}</text>
        </g>`);
        if (canEdit()) {
          out.push(sldPill(sg.left + sg.width - 52, busY - 26, 78, '+ Way', `data-action="hv-add-way" data-id="${sec.id}"`, C.bus, 'Add an outgoing way to ' + sec.name));
        }
        if (canManage()) {
          out.push(sldPill(sg.left + sg.width - 148, busY - 26, 86, '+ Feeder', `data-action="hv-add-feeder" data-id="${sec.id}"`, C.bus, 'Add an incoming feeder to ' + sec.name));
        }

        // Ways, spread along the whole section.
        const nw = sec.ways.length;
        if (!nw) {
          out.push(`<text x="${sg.cx}" y="${busY + 46}" text-anchor="middle" font-size="12" fill="${C.muted}">No outgoing ways on this section yet</text>`);
        }
        sec.ways.map(way => planOf[way.id]).forEach(pl => {
          const { way, i, wx, down, feedsDown, across, head, headIsSwitch, chain, cy, endsAtLoad, box } = pl;
          const at = out.length;
          const wGrab = canEdit() ? ` data-drag="way" data-drag-id="${way.id}" data-drag-label="${esc(way.name)}"` : '';
          // Where the conductor ends. A way sharing a box steps sideways into
          // it, so it runs down only as far as its own turn.
          const shared = !!box && box.members.length > 1;
          const turnY = shared ? destY - 26 - pl.level * ROUTE_ROW : destY;
          const enterX = shared ? pl.enterX : wx;
          const foot = feedsDown ? down.busY : (endsAtLoad ? pl.tail.y : turnY);
          const below = feedsDown || endsAtLoad;
          out.push(`<rect class="hv-col-plate${canEdit() ? ' hv-grab' : ''}"${wGrab} fill="${C.bus}" fill-opacity="0"
            x="${wx - WAY_W / 2 + 6}" y="${busY + 8}" width="${WAY_W - 12}" height="${(below ? foot + 30 : (shared ? turnY + 12 : destY + DEST_H)) - busY}" rx="12"/>`);
          const wHandle = hvHandle(wx, busY, C.bus, wGrab,
            esc(way.name) + ' - drag it anywhere along the bar');
          out.push(`<line x1="${wx}" y1="${busY}" x2="${wx}" y2="${foot}" stroke="${C.line}" stroke-width="2.5"/>`);
          if (shared && enterX !== wx) {
            out.push(`<polyline points="${wx},${turnY} ${enterX},${turnY} ${enterX},${destY}" fill="none"
              stroke="${C.line}" stroke-width="2.5" stroke-linejoin="round"/>`);
          } else if (shared) {
            out.push(`<line x1="${wx}" y1="${turnY}" x2="${wx}" y2="${destY}" stroke="${C.line}" stroke-width="2.5"/>`);
          }

          if (headIsSwitch) {
            out.push(hvDevice(wx, wayTapY, head, '', hvRefFor(head.name || way.name, 'feeder')));
          } else {
            // A low-tension board's way carries nothing at the bar: it simply
            // leaves it, and only its designation is written up the side.
            if (net.tier !== 'lt') out.push(hvBreaker(wx, wayTapY, C.line));
            out.push(hvRefTag(wx - TAG_X, wayTapY + 26, way.name, 12, hvRefFor(way.name, 'feeder')));
          }

          const headId = headIsSwitch ? head.id : 0;
          if (canEdit()) hvDrops.push({ x: wx - 46, y: wayTapY + DEV_GAP - 34, w: 92, h: 30, wayId: way.id, afterId: headId });
          chain.forEach(({ dev, y: dy }) => {
            out.push(hvDevice(wx, dy, dev));
            if (canEdit()) hvDrops.push({ x: wx - 46, y: dy + devHeight(dev) - 34, w: 92, h: 30, wayId: way.id, afterId: dev.id });
          });
          const y = cy;
          const lastId = way.devices.length ? way.devices[way.devices.length - 1].id : headId;
          if (canEdit()) {
            hvDrops.push({ x: wx - 46, y: (below || shared ? y + 14 : turnY - 34), w: 92, h: 30, wayId: way.id, afterId: lastId });
            out.push(sldPill(wx, below || shared ? y + 29 : turnY - 26, 34, '+', `data-action="hv-add-device" data-way="${way.id}"`, C.line, 'Fit another device on this way'));
          }

          if (endsAtLoad) {
            // Nothing to draw: the machine at the foot of the chain is where
            // the way ends, and it carries its own name.
          } else if (feedsDown) {
            // It lands on the switchboard below rather than in a box, the way
            // a drawing simply runs the line onto the next bus.
            out.push(`<g class="hv-node" data-action="hv-edit-way" data-id="${way.id}"${wGrab}>
              <title>${esc(way.name)} feeds ${esc(down.board.name)}${canEdit() ? ' - click to change where it lands' : ''}</title>
              <rect x="${wx - 22}" y="${foot - 30}" width="44" height="52" fill="transparent"/>
              <circle cx="${wx}" cy="${foot}" r="6" fill="${C.bus}"/>
            </g>`);
          } else if (pl.feed === 0) {
            // The destination box: what this way actually feeds. Where more
            // than one way feeds it, the first of them draws the one box and
            // the rest step into it.
            const linked = !!way.dest_board_id || across;
            const dcol = linked ? C.dest : C.muted;
            const label = across ? way.dest_switchboard_name : (way.dest_label || way.dest_board_code || 'Not assigned');
            const fed = shared ? plural(box.members.length, 'supply', 'supplies') : '';
            const sub = fed || (across ? (way.dest_detail || way.dest_network_name)
              : (way.dest_detail
                || (linked ? (way.dest_building_name || 'Open on the dashboard')
                  : (way.dest_label ? 'External' : 'Set a destination'))));
            const destAct = across ? `data-action="hv-open-drawing" data-id="${way.dest_network_id}" data-board="${way.dest_switchboard_id}"`
              : (way.dest_board_id ? `data-action="hv-open-dest" data-id="${way.dest_board_id}"`
                : (canEdit() ? `data-action="hv-edit-way" data-id="${way.id}"` : ''));
            const names = box.members.map(m => m.way.name).join(', ');
            out.push(`<g class="${destAct ? 'hv-node ' : ''}${linked ? 'hv-linked' : ''}" ${destAct}${wGrab}>
              <title>${esc(label)}${shared ? ' · fed by ' + esc(names) : (way.dest_detail ? ' · ' + esc(way.dest_detail) : '')}${across ? ' on ' + esc(way.dest_network_name) + ' - click to open that drawing' : (linked ? ' - click to open this board' : (destAct ? ' - click to set where this way feeds' : ''))}</title>
              <rect x="${box.cx - box.w / 2}" y="${destY}" width="${box.w}" height="${DEST_H}" rx="9"
                    fill="${linked ? '#f2fbf6' : '#f8fafc'}" stroke="${dcol}" stroke-width="2.5"/>
              <text x="${box.cx}" y="${destY + 25}" text-anchor="middle" font-size="15" font-weight="700" fill="${linked ? '#0f8a4f' : '#64748b'}">${esc(label)}</text>
              <text x="${box.cx}" y="${destY + 43}" text-anchor="middle" font-size="10" fill="${C.muted}">${esc(sub)}</text>
            </g>`);
          }
          if (canEdit()) {
            const ey = below || shared ? y + 12 : turnY - 32;
            out.push(`<g class="sld-btn" data-action="hv-edit-way" data-id="${way.id}"><title>Edit ${esc(way.name)}</title>
              <rect x="${wx + WAY_W / 2 - 32}" y="${ey}" width="26" height="26" rx="7" fill="#fff" stroke="${C.line}" stroke-opacity=".35"/>
              <g transform="translate(${wx + WAY_W / 2 - 29} ${ey + 3})" fill="none" stroke="${C.line}" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">${SLD_ICONS.edit}</g></g>`);
          }
          out.push(wHandle);
          out.push(`<g class="hv-col" id="hv-way-${way.id}" data-col="way-${way.id}" data-home="${wx}">${out.splice(at).join('')}</g>`);
        });
      });

      // Couplers sit in the gap between the sections they tie.
      (board.couplers || []).forEach(c => {
        const l = secAt[c.left_section_id], r = secAt[c.right_section_id];
        if (!l || !r) return;
        const a = l.i <= r.i ? l : r, z = l.i <= r.i ? r : l;
        const mid = (a.busR + z.busL) / 2;
        const col = c.closed ? C.bus : C.muted;
        out.push(`<line x1="${a.busR}" y1="${busY}" x2="${mid - 15}" y2="${busY}" stroke="${col}" stroke-width="4" stroke-linecap="round"/>`);
        out.push(`<line x1="${mid + 15}" y1="${busY}" x2="${z.busL}" y2="${busY}" stroke="${col}" stroke-width="4" stroke-linecap="round"/>`);
        out.push(`<g class="hv-node" data-action="hv-edit-coupler" data-id="${c.id}">
          <title>${esc(c.name)} - ${c.closed ? 'closed, so ' + esc(a.sec.name) + ' and ' + esc(z.sec.name) + ' are tied and one can back up the other' : 'open, so each section stands alone'}${canManage() ? '. Click to change.' : ''}</title>
          <rect x="${mid - 26}" y="${busY - 22}" width="52" height="44" fill="transparent"/>
          <g stroke="${col}" stroke-width="3" stroke-linecap="round">
            <line x1="${mid - 13}" y1="${busY - 13}" x2="${mid + 13}" y2="${busY + 13}"/>
            <line x1="${mid + 13}" y1="${busY - 13}" x2="${mid - 13}" y2="${busY + 13}"/>
          </g>
          <text x="${mid}" y="${busY - 24}" text-anchor="middle" font-size="12" font-weight="700" fill="${col}">${esc(c.name)}</text>
          <text x="${mid}" y="${busY + 34}" text-anchor="middle" font-size="10" letter-spacing="1" fill="${c.closed ? C.prot : C.muted}">${c.closed ? 'CLOSED · TIED' : 'OPEN'}</text>
        </g>`);
      });
    });

    return `<svg viewBox="0 0 ${W} ${H}" width="${W}" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="${esc(net.name)}">${out.join('')}</svg>`;

    // The tap on the busbar, which doubles as the handle a whole column is
    // dragged by. It reads as a point on the bar until the pointer is over it.
    function hvHandle(cx, cy, col, grab, title) {
      if (!grab) return `<circle cx="${cx}" cy="${cy}" r="5" fill="${col}"/>`;
      return `<g class="hv-grab hv-handle"${grab}>
        <title>${title}</title>
        <circle cx="${cx}" cy="${cy}" r="15" fill="transparent"/>
        <circle class="hv-handle-ring" cx="${cx}" cy="${cy}" r="8" fill="#fff" stroke="${col}" stroke-width="2.5"/>
        <circle cx="${cx}" cy="${cy}" r="3.2" fill="${col}"/>
      </g>`;
    }

    // A generator, drawn the way a drawing draws a machine: a circle on a
    // stand, in place of the voltage line a plain supply carries.
    function hvGenerator(cx, cy, col) {
      return `<g>
        <path d="M ${cx - 20} ${cy + 24} L ${cx + 20} ${cy + 24} L ${cx} ${cy + 3} Z" fill="#fff" stroke="${col}" stroke-width="2.2" stroke-linejoin="round"/>
        <circle cx="${cx}" cy="${cy}" r="16" fill="#fff" stroke="${col}" stroke-width="2.5"/>
        <text x="${cx}" y="${cy + 6}" text-anchor="middle" font-size="15" font-weight="700" fill="${col}">G</text>
      </g>`;
    }

    function hvTag(x, y, text, col, size) {
      if (!text) return '';
      return `<text x="${x}" y="${y}" transform="rotate(-90 ${x} ${y})" text-anchor="start"
        font-size="${size || 12}" font-weight="700" fill="${col}" letter-spacing=".5">${esc(text)}</text>`;
    }

    // The same designation drawn as a link, when another drawing carries it.
    function hvRefTag(x, y, text, size, ref) {
      if (!ref) return hvTag(x, y, text, C.label, size);
      return `<g class="hv-node hv-ref" data-action="hv-open-ref" data-id="${ref.network_id}" data-ref="hv-${ref.kind}-${ref.id}">
        <title>${esc(text)} is on ${esc(ref.network_name)}${ref.board_name ? ', at ' + esc(ref.board_name) : ''} - click to open it there</title>
        <rect x="${x - (size || 12) - 4}" y="${y - 96}" width="${(size || 12) + 12}" height="100" fill="transparent"/>
        ${hvTag(x, y, text, C.dest, size)}
      </g>`;
    }

    function hvDevice(cx, cy, d, alt, ref) {
      const g = [];
      const kind = d.kind;
      const col = kind === 'transformer' ? C.tx : (['rccb', 'elr', 'elcb'].includes(kind) ? C.prot : C.line);
      const label = (DEVICE_LABEL[kind] || kind).toUpperCase();
      if (kind === 'transformer') {
        g.push(`<rect x="${cx - 17}" y="${cy - 26}" width="34" height="52" fill="#fff"/>
          <circle cx="${cx}" cy="${cy - 9}" r="16" fill="none" stroke="${col}" stroke-width="2.5"/>
          <circle cx="${cx}" cy="${cy + 9}" r="16" fill="none" stroke="${col}" stroke-width="2.5"/>`);
        if (d.kva) g.push(`<text x="${cx + 26}" y="${cy - 2}" font-size="10" fill="${C.muted}">${fmtA(d.kva)} kVA</text>`);
        if (d.ratio) g.push(`<text x="${cx + 26}" y="${cy + 11}" font-size="10" fill="${C.muted}">${esc(d.ratio)}</text>`);
      } else if (kind === 'switchgear') {
        g.push(hvBreaker(cx, cy, col));
      } else if (kind === 'isolator') {
        // The open switch as a drawing draws it: a contact at each end of the
        // gap and the blade swung clear of the top one.
        g.push(`<rect x="${cx - 14}" y="${cy - 18}" width="28" height="36" fill="#fff"/>
          <line x1="${cx}" y1="${cy + 15}" x2="${cx + 15}" y2="${cy - 11}" stroke="${col}" stroke-width="2.5" stroke-linecap="round"/>
          <circle cx="${cx}" cy="${cy + 15}" r="3.4" fill="#fff" stroke="${col}" stroke-width="2"/>
          <circle cx="${cx}" cy="${cy - 15}" r="3.4" fill="#fff" stroke="${col}" stroke-width="2"/>`);
      } else if (kind === 'chiller') {
        // A machine on its stand, named inside the circle the way a drawing
        // names it, since the way ends here rather than in a box.
        g.push(`<path d="M ${cx - 22} ${cy + 26} L ${cx + 22} ${cy + 26} L ${cx} ${cy + 4} Z" fill="#fff" stroke="${col}" stroke-width="2.2" stroke-linejoin="round"/>
          <circle cx="${cx}" cy="${cy}" r="18" fill="#fff" stroke="${col}" stroke-width="2.5"/>
          <text x="${cx}" y="${cy + 5}" text-anchor="middle" font-size="${d.name && d.name.length > 4 ? 10 : 12}" font-weight="700" fill="${col}">${esc(d.name || 'M')}</text>`);
      } else if (kind === 'fuse') {
        g.push(`<rect x="${cx - 9}" y="${cy - 15}" width="18" height="30" rx="2" fill="#fff" stroke="${col}" stroke-width="2.5"/>`);
      } else if (kind === 'meter') {
        g.push(`<circle cx="${cx}" cy="${cy}" r="14" fill="#fff" stroke="${col}" stroke-width="2.5"/>
          <text x="${cx}" y="${cy + 5}" text-anchor="middle" font-size="12" font-weight="700" fill="${col}">M</text>`);
      } else {
        g.push(`<rect x="${cx - 34}" y="${cy - 15}" width="68" height="30" rx="7" fill="#fffaf0" stroke="${col}" stroke-width="2"/>
          <text x="${cx}" y="${cy + 5}" text-anchor="middle" font-size="12" font-weight="700" fill="${col}">${esc(label)}</text>`);
      }
      const tag = d.name || alt || '';
      if (tag && kind !== 'chiller') g.push(hvRefTag(cx - TAG_X, cy + 22, tag, 11, ref));
      const title = `${label}${tag ? ' ' + tag : ''}${d.notes ? ' · ' + d.notes : ''}`;
      let inner = `<g class="${canEdit() ? 'hv-node' : ''}" ${canEdit() ? `data-action="hv-edit-device" data-id="${d.id}"` : ''}>
        <title>${esc(title)}${canEdit() ? ' - click to change or remove' : ''}</title>
        <rect x="${cx - 40}" y="${cy - 26}" width="80" height="52" fill="transparent"/>${g.join('')}</g>`;
      if (canEdit()) {
        // Clear of the widest symbol and of a transformer's rating beside it.
        inner += `<g class="sld-btn" data-action="hv-add-device" data-way="${d.way_id || ''}" data-feeder="${d.feeder_id || ''}" data-after="${d.id}">
          <title>Fit another device below this one</title>
          <rect x="${cx + 100}" y="${cy - 11}" width="22" height="22" rx="6" fill="#fff" stroke="${C.line}" stroke-opacity=".3"/>
          <text x="${cx + 111}" y="${cy + 5}" text-anchor="middle" font-size="15" font-weight="700" fill="${C.line}">+</text></g>`;
      }
      return inner;
    }

    // Switchgear is drawn the way it is on a single line diagram: the conductor
    // is broken and an X marks the breaker.
    function hvBreaker(cx, cy, col) {
      const r = 11;
      return `<rect x="${cx - r - 2}" y="${cy - r - 2}" width="${(r + 2) * 2}" height="${(r + 2) * 2}" fill="#fff"/>
        <g stroke="${col}" stroke-width="2.8" stroke-linecap="round">
          <line x1="${cx - r}" y1="${cy - r}" x2="${cx + r}" y2="${cy + r}"/>
          <line x1="${cx + r}" y1="${cy - r}" x2="${cx - r}" y2="${cy + r}"/>
        </g>`;
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
          ${opts.extra || ''}
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

  // ---- high-voltage overview
  function hvRenameForm() { hvNetworkForm(state.hv); }

  function hvFeederForm(f, sectionId) {
    const isEdit = !!(f && f.id);
    const secs = hvSections();
    // On a low-tension board the incomer comes in through its transformer and
    // lands on switchgear named after the board itself, so neither is asked for.
    const lowTension = ((state.hv || {}).tier === 'lt');
    formModal(isEdit ? 'Edit feeder ' + f.name : 'Add feeder', `
      ${field('Backs bus section', 'section_id', isEdit ? f.section_id : (Number(sectionId) || (secs[0] || {}).id), { type: 'select', required: true, full: true, options: secs.map(x => ({ value: x.id, label: hvSectionLabel(x) + (x.feeders.length ? ' · with ' + x.feeders.map(y => y.name).join(', ') : '') })) })}
      ${field('Incomer', 'kind', isEdit ? (f.kind || 'supply') : 'supply', { type: 'select', required: true, options: [{ value: 'supply', label: 'Incoming supply' }, { value: 'generator', label: 'Generator' }], hint: 'A generator is drawn with its own symbol.' })}
      ${field('Feeder name', 'name', isEdit ? f.name : hvNextFeederName(), { required: true, placeholder: 'F5', hint: 'Written above the incoming line.', attrs: 'style="text-transform:uppercase"' })}
      ${lowTension ? field('Backs which end', 'side', isEdit ? (f.side || '') : 'l', { type: 'select', required: false,
        options: [{ value: '', label: '— the whole bar —' }, { value: 'l', label: 'Left' }, { value: 'r', label: 'Right' }],
        hint: 'Marked (L) or (R) beside the name, and the ways on that end are drawn under it.' }) : ''}
      ${isEdit ? '' : field('Lands through', 'arrangement', lowTension ? 'transformer' : 'switchgear', { type: 'select', required: true, full: true,
        options: [{ value: 'switchgear', label: 'Switchgear' }, { value: 'transformer', label: 'A transformer, then the switchgear' }],
        hint: 'A low-tension board is fed through its transformer first, so the transformer is drawn above the switchgear.' })}
      ${isEdit ? '' : field('Transformer designation', 'transformer', '', { placeholder: 'TX33', hint: 'Only when it lands through one.', attrs: 'style="text-transform:uppercase"' })}
      ${lowTension ? '' : field('Switchgear designation', 'switchgear', isEdit ? (f.switchgear || '') : '', { placeholder: '22SGI5', hint: 'Written up the side of the breaker.', attrs: 'style="text-transform:uppercase"' })}
      ${field('Voltage', 'voltage', isEdit ? f.voltage : (state.hv.voltage || '22kV'), { placeholder: '22kV' })}
      ${field('Source', 'source', isEdit ? f.source : '', { full: true, placeholder: 'e.g. Incoming supply 5, intake substation' })}
      ${field('Switchgear rating (A)', 'rating_a', isEdit && f.rating_a != null ? f.rating_a : '', { type: 'number', attrs: 'min="0" max="100000" step="any"', hint: 'Optional' })}
      ${isEdit && f.offset_x != null ? `<div class="field inline full"><input type="checkbox" name="auto" id="f_auto"><label for="f_auto">Space this incomer automatically along the bar again</label></div>` : ''}`,
      async d => {
        const body = { name: d.name, switchgear: d.switchgear === undefined ? (isEdit ? (f.switchgear || '') : '') : d.switchgear, kind: d.kind, section_id: Number(d.section_id), voltage: d.voltage, source: d.source, rating_a: d.rating_a === '' ? null : Number(d.rating_a), arrangement: d.arrangement || 'switchgear', transformer: d.transformer || '', side: d.side === undefined ? (isEdit ? (f.side || '') : '') : d.side };
        if (isEdit) {
          state.focus = 'hv-feeder-' + f.id;
          await api('PUT', '/api/hv/feeders/' + f.id, body);
          if (d.auto === 'on') await api('POST', '/api/hv/feeders/' + f.id + '/place', { auto: true });
          await afterChange('Feeder updated');
        }
        else { const r = await api('POST', '/api/hv/feeders', body); state.focus = 'hv-feeder-' + r.id; await afterChange('Feeder added'); }
      },
      isEdit && canManage() ? { deleteLabel: 'Delete feeder', onDelete: () => hvDeleteFeeder(f) } : {});
  }
  function hvNextFeederName() {
    const used = new Set(hvFeeders().map(f => f.name.toUpperCase()));
    for (let i = 1; i < 100; i++) { const n = 'F' + i; if (!used.has(n)) return n; }
    return '';
  }
  function hvDeleteFeeder(f) {
    confirmModal('Delete feeder', `Delete incoming feeder <b>${esc(f.name)}</b> and everything fitted on it? The bus section it backs, and the ways tapping that section, stay where they are.`,
      async () => { await api('DELETE', '/api/hv/feeders/' + f.id); await afterChange('Feeder deleted'); });
  }

  function hvWayForm(way, sectionId) {
    const isEdit = !!(way && way.id);
    const lowTension = ((state.hv || {}).tier === 'lt');
    const secs = hvSections();
    const boards = state.hvBoards || [];
    const parent = secs.find(x => x.id === (isEdit ? way.section_id : Number(sectionId))) || secs[0];
    // On this drawing a way can only land on a switchboard below its own, or
    // the line would have to run back up the page. On another drawing there is
    // no line to draw, so any board there can be named.
    const own = parent ? hvBoardOfSection(parent.id) : null;
    const here = (state.hv || {}).id;
    const below = (state.hvAllBoards || []).filter(b =>
      b.network_id !== here || !own || b.position > own.position);
    formModal(isEdit ? 'Edit way ' + way.name : 'Add way to ' + (parent ? parent.name : 'the busbar'), `
      ${field('Taps bus section', 'section_id', parent ? parent.id : '', { type: 'select', required: true, full: true,
        options: secs.map(x => ({ value: x.id, label: hvSectionLabel(x) + (x.feeders.length ? ' · backed by ' + x.feeders.map(y => y.name).join(', ') : ' · no feeder yet') })),
        hint: 'The section feeds this way, so every incomer on it backs the way.' })}
      ${field('Way name', 'name', isEdit ? way.name : hvNextWayName(parent), { required: true, placeholder: '22SG05' })}
      ${field('Rating (A)', 'rating_a', isEdit && way.rating_a != null ? way.rating_a : '', { type: 'number', attrs: 'min="0" max="100000" step="any"', hint: 'Optional' })}
      ${lowTension ? field('Taps which end', 'side', isEdit ? (way.side || '') : 'l', { type: 'select', required: false,
        options: [{ value: '', label: '— the whole bar —' }, { value: 'l', label: 'Left' }, { value: 'r', label: 'Right' }],
        hint: 'Ways on an end are drawn along that run of the bar, clear of where the incomers land.' }) : ''}
      ${isEdit || lowTension ? '' : field('At the busbar', 'head_kind', 'switchgear', { type: 'select', required: true,
        options: Object.entries(HEAD_KINDS).map(([value, label]) => ({ value, label })),
        hint: 'Switchgear is the X; a switch is the open blade.' })}
      ${isEdit ? '' : field('Runs into chiller', 'chiller', '', { placeholder: 'e.g. CH#1',
        hint: 'Name one and the way runs from its switch straight into the machine, with no destination box.', attrs: 'style="text-transform:uppercase"' })}
      ${field('Feeds switchboard', 'dest_switchboard_id', isEdit && way.dest_switchboard_id ? way.dest_switchboard_id : '', { type: 'select', full: true,
        options: [{ value: '', label: '— does not feed a switchboard —' }].concat(below.map(b => ({
          value: b.id,
          label: (b.network_id === here ? '' : b.network_name + ' · ') + b.name + (b.voltage ? ' · ' + b.voltage : ''),
        }))),
        hint: 'On this drawing the way runs on down to that board\'s busbar. On another drawing the destination box links across to it.' })}
      ${field('Or feeds board', 'dest_board_id', isEdit && way.dest_board_id ? way.dest_board_id : '', { type: 'select', full: true,
        options: [{ value: '', label: '— not a board in this system —' }].concat(boards.map(b => ({ value: b.id, label: b.code + ' · ' + b.building_name }))),
        hint: 'Linking a board makes the destination box clickable.' })}
      ${field('Or destination label', 'dest_label', isEdit ? way.dest_label : '', { placeholder: 'e.g. FAC1/PE1, when it is not a board here' })}
      ${field('Where at that destination', 'dest_detail', isEdit ? (way.dest_detail || '') : '', { placeholder: 'e.g. TX15', hint: 'The transformer or panel the way terminates on.' })}
      ${field('Notes', 'notes', isEdit ? way.notes : '', { type: 'textarea', full: true })}
      ${isEdit && way.offset_x != null ? `<div class="field inline full"><input type="checkbox" name="auto" id="way_auto"><label for="way_auto">Space this way automatically along the bar again</label></div>` : ''}
      ${isEdit ? `<p class="field full hint" style="margin:0">What is fitted on this way is set on the diagram: the <b>+</b> beside a device adds another below it, and clicking a device changes or removes it. Drag its dot on the busbar to put it wherever it belongs.</p>` : ''}`,
      async d => {
        const body = {
          section_id: Number(d.section_id), name: d.name,
          rating_a: d.rating_a === '' ? null : Number(d.rating_a),
          dest_board_id: d.dest_board_id ? Number(d.dest_board_id) : null,
          dest_switchboard_id: d.dest_switchboard_id ? Number(d.dest_switchboard_id) : null,
          dest_label: d.dest_label || '', dest_detail: d.dest_detail || '', notes: d.notes || '',
          head_kind: lowTension ? 'none' : (d.head_kind || 'switchgear'), chiller: d.chiller || '',
          side: d.side === undefined ? (isEdit ? (way.side || '') : '') : d.side,
        };
        if (isEdit) {
          state.focus = 'hv-way-' + way.id;
          await api('PUT', '/api/hv/ways/' + way.id, body);
          if (d.auto === 'on') await api('POST', '/api/hv/ways/' + way.id + '/place', { auto: true });
          await afterChange('Way updated');
        }
        else { const r = await api('POST', '/api/hv/ways', body); state.focus = 'hv-way-' + r.id; await afterChange('Way added'); }
      },
      isEdit && canManage() ? { wide: true, deleteLabel: 'Delete way', onDelete: () => hvDeleteWay(way) } : { wide: true });
  }

  // ---- devices fitted on a way
  function hvDeviceForm(dev, wayId, afterId, feederId) {
    const isEdit = !!(dev && dev.id);
    const kinds = Object.entries(DEVICE_LABEL).map(([value, label]) => ({ value, label }));
    const kind = isEdit ? dev.kind : 'rccb';
    const form = formModal(isEdit ? 'Edit ' + (DEVICE_LABEL[dev.kind] || dev.kind) : 'Fit a device', `
      ${field('Device', 'kind', kind, { type: 'select', required: true, full: true, options: kinds })}
      ${field('Designation', 'name', isEdit ? dev.name : '', { placeholder: 'e.g. 22SG05, TX05', hint: 'Written up the side of the symbol.' })}
      ${field('Rating (A)', 'rating_a', isEdit && dev.rating_a != null ? dev.rating_a : '', { type: 'number', attrs: 'min="0" max="100000" step="any"' })}
      <div class="full" id="tx-fields" ${kind === 'transformer' ? '' : 'hidden'}>
        <div class="form-grid">
          ${field('Rating (kVA)', 'kva', isEdit && dev.kva != null ? dev.kva : '', { type: 'number', attrs: 'min="0" max="1000000" step="any"' })}
          ${field('Ratio', 'ratio', isEdit ? dev.ratio : '', { placeholder: '22kV / 400V' })}
        </div>
      </div>
      ${field('Notes', 'notes', isEdit ? dev.notes : '', { full: true, placeholder: 'e.g. 300 mA, 0.5 s' })}`,
      async d => {
        const body = {
          kind: d.kind, name: d.name || '', notes: d.notes || '', ratio: d.ratio || '',
          rating_a: d.rating_a === '' ? null : Number(d.rating_a),
          kva: d.kva ? Number(d.kva) : null,
        };
        if (isEdit) { await api('PUT', '/api/hv/devices/' + dev.id, body); await afterChange('Device updated'); }
        else {
          if (feederId) body.feeder_id = Number(feederId); else body.way_id = Number(wayId);
          if (afterId) body.after_id = Number(afterId);
          await api('POST', '/api/hv/devices', body);
          await afterChange('Device fitted');
        }
      },
      isEdit ? {
        deleteLabel: 'Remove', onDelete: () => hvDeleteDevice(dev),
        extra: `<button type="button" class="btn btn-sm" data-act="up" title="Move up the way">↑</button>
                <button type="button" class="btn btn-sm" data-act="down" title="Move down the way">↓</button>`,
      } : {});
    // The transformer figures only apply to a transformer.
    const sel = $('[name=kind]', form), tx = $('#tx-fields', form);
    sel.addEventListener('change', () => { tx.hidden = sel.value !== 'transformer'; });
    if (isEdit) {
      $$('[data-act=up], [data-act=down]', form).forEach(btn => btn.addEventListener('click', async () => {
        try {
          await api('POST', '/api/hv/devices/' + dev.id + '/move', { delta: btn.dataset.act === 'up' ? -1 : 1 });
          closeModal();
          await afterChange('Device moved');
        } catch (e) { $('#form-error', form).textContent = e.message; }
      }));
    }
  }
  function hvDeleteDevice(dev) {
    confirmModal('Remove device', `Remove the <b>${esc(DEVICE_LABEL[dev.kind] || dev.kind)}</b>${dev.name ? ' <b>' + esc(dev.name) + '</b>' : ''} from this way? Everything below it stays where it is.`,
      async () => { await api('DELETE', '/api/hv/devices/' + dev.id); await afterChange('Device removed'); }, 'Remove');
  }
  // A device sits on a way or on an incoming feeder, so both have to be
  // searched or clicking one on a feeder finds nothing to edit or remove.
  function hvFindDevice(id) {
    for (const sec of hvSections()) {
      for (const f of sec.feeders) {
        const own = (f.devices || []).find(x => x.id === id);
        if (own) return own;
      }
      for (const w of sec.ways) {
        const d = (w.devices || []).find(x => x.id === id);
        if (d) return d;
      }
    }
    return null;
  }

  function hvNextWayName(section) {
    const used = new Set(((section && section.ways) || []).map(w => w.name.toUpperCase()));
    for (let i = 1; i < 200; i++) { const n = 'W' + i; if (!used.has(n)) return n; }
    return '';
  }
  function hvDeleteWay(way) {
    confirmModal('Delete way', `Delete way <b>${esc(way.name)}</b>${way.dest_board_code || way.dest_label ? ' feeding ' + esc(way.dest_label || way.dest_board_code) : ''}? This cannot be undone.`,
      async () => { await api('DELETE', '/api/hv/ways/' + way.id); await afterChange('Way deleted'); });
  }

  function hvCouplerForm(c) {
    const isEdit = !!(c && c.id);
    // A coupler needs only a position: the gap after a section, on that
    // section's own switchboard. Naming both sides would be the same choice
    // twice over, and could be set to disagree. Asked for at the end of a bus
    // it divides it, so a new switchboard can be given one straight away.
    const many = hvBoards().length > 1;
    const gaps = hvBoards().flatMap(b => {
      const on = b.sections;
      if (!on.length) return [];
      const lead = many ? b.name + ': ' : '';
      const out = on.slice(0, -1).map((x, i) => ({
        value: x.id, label: `${lead}between ${x.name} and ${on[i + 1].name}`,
      }));
      if (!isEdit) {
        out.push({ value: on[on.length - 1].id, label: `${lead}at the end of the bus - splits it into a new section` });
      }
      return out;
    });
    if (!gaps.length) { toast('Add a bus section before adding a coupler.', 'error'); return; }
    formModal(isEdit ? 'Edit coupler ' + c.name : 'Add bus coupler', `
      ${field('Coupler name', 'name', isEdit ? c.name : 'BC-' + (hvCouplers().length + 1), { required: true, attrs: 'style="text-transform:uppercase"' })}
      ${field('Rating (A)', 'rating_a', isEdit && c.rating_a != null ? c.rating_a : '', { type: 'number', attrs: 'min="0" max="100000" step="any"', hint: 'Optional' })}
      ${field('Position on the busbar', 'after_id', isEdit ? c.left_section_id : gaps[0].value, { type: 'select', required: true, full: true, options: gaps, hint: 'Which two lengths of bus it ties together. At the end of a bus it divides it, and the new section appears on its far side.' })}
      <div class="field inline full"><input type="checkbox" name="closed" id="cp_closed" ${isEdit && c.closed ? 'checked' : ''}><label for="cp_closed">Coupler is closed (the sections either side are tied together)</label></div>`,
      async d => {
        const body = { name: d.name, after_id: Number(d.after_id), closed: d.closed === 'on',
          rating_a: d.rating_a === '' ? null : Number(d.rating_a) };
        if (isEdit) { await api('PUT', '/api/hv/couplers/' + c.id, body); await afterChange('Coupler updated'); }
        else { await api('POST', '/api/hv/couplers', body); await afterChange('Coupler added'); }
      },
      isEdit && canManage() ? { deleteLabel: 'Delete coupler', onDelete: () => hvDeleteCoupler(c) } : {});
  }
  function hvDeleteCoupler(c) {
    confirmModal('Delete coupler', `Delete coupler <b>${esc(c.name)}</b>?`,
      async () => { await api('DELETE', '/api/hv/couplers/' + c.id); await afterChange('Coupler deleted'); });
  }

  // ---- drawings
  function hvNetworkForm(net) {
    const isEdit = !!(net && net.id);
    formModal(isEdit ? 'Rename ' + net.name : 'Add a drawing', `
      ${field('Title', 'name', isEdit ? net.name : '', { required: true, full: true, placeholder: 'e.g. SSMC Low Tension Distribution' })}
      ${field('Tension', 'tier', isEdit ? (net.tier || 'lt') : 'lt', { type: 'select', required: true,
        options: [{ value: 'ht', label: 'High tension' }, { value: 'lt', label: 'Low tension' }] })}
      ${field('Voltage', 'voltage', isEdit ? net.voltage : '400V', { placeholder: '400V' })}
      <p class="field full hint" style="margin:0">A drawing of its own, with its own switchboards. A way on another drawing can land on a switchboard here, and its destination box will link across.</p>`,
      async d => {
        const body = { name: d.name, voltage: d.voltage, tier: d.tier };
        if (isEdit) { await api('PUT', '/api/hv?network=' + net.id, body); await afterChange('Drawing renamed'); }
        else {
          const r = await api('POST', '/api/hv/networks', body);
          navigate('#/hv/' + r.id);
          toast('Drawing added');
        }
      },
      isEdit && canManage() && (state.hvNets || []).length > 1
        ? { wide: true, deleteLabel: 'Delete drawing', onDelete: () => hvDeleteNetwork(net) } : { wide: true });
  }
  function hvDeleteNetwork(net) {
    if ((net.board_count || (net.switchboards || []).length) > 0) {
      toast('Delete the switchboards on ' + net.name + ' first.', 'error');
      return;
    }
    confirmModal('Delete drawing', `Delete <b>${esc(net.name)}</b>?`,
      async () => {
        await api('DELETE', '/api/hv/networks/' + net.id);
        navigate('#/hv');
        toast('Drawing deleted');
      });
  }

  // The same designation on another drawing: a circuit is a way where it
  // leaves one board and an incomer where it lands on the next, so one can be
  // followed to the other.
  // Designations are typed by hand on two drawings, so they are matched on
  // their letters and digits alone: 22SG05, 22 SG05 and 22-SG05 are one
  // circuit, and only a real difference in the designation breaks the link.
  const refKey = s => (s || '').toUpperCase().replace(/[^A-Z0-9]+/g, '');
  function hvRefFor(name, kind) {
    const key = refKey(name);
    if (!key) return null;
    const here = (state.hv || {}).id;
    return (state.hvRefs || []).find(r =>
      r.kind === kind && r.network_id !== here && refKey(r.name) === key) || null;
  }

  const hvBoards = () => (state.hv && state.hv.switchboards) || [];
  const hvSections = () => hvBoards().flatMap(b => b.sections);
  const hvFeeders = () => hvSections().flatMap(sec => sec.feeders);
  const hvCouplers = () => hvBoards().flatMap(b => b.couplers);
  function hvFindSection(id) { return hvSections().find(sec => sec.id === id); }
  // With more than one switchboard a section name alone is ambiguous.
  function hvSectionLabel(sec) {
    if (hvBoards().length < 2) return sec.name;
    const b = hvBoardOfSection(sec.id);
    return (b ? b.name + ' · ' : '') + sec.name;
  }
  function hvFindBoard(id) { return hvBoards().find(b => b.id === id); }
  // The switchboard a section, and so a way, belongs to.
  function hvBoardOfSection(id) { return hvBoards().find(b => b.sections.some(sec => sec.id === id)); }

  // ---- switchboards
  function hvBoardForm(board) {
    const isEdit = !!(board && board.id);
    formModal(isEdit ? 'Edit ' + board.name : 'Add switchboard', `
      ${field('Switchboard name', 'name', isEdit ? board.name : '', { required: true, full: true, placeholder: 'e.g. 6.6kV Switchboard' })}
      ${field('Voltage', 'voltage', isEdit ? board.voltage : '', { placeholder: '6.6kV' })}
      ${field('Phases', 'phases', isEdit ? board.phases : '3p', { placeholder: '3p' })}
      ${field('Frequency', 'frequency', isEdit ? board.frequency : '50Hz', { placeholder: '50Hz' })}
      ${field('Rated current (A)', 'current_a', isEdit && board.current_a != null ? board.current_a : '', { type: 'number', attrs: 'min="0" max="100000" step="any"', placeholder: '1250' })}
      ${field('Fault rating (kA)', 'fault_ka', isEdit && board.fault_ka != null ? board.fault_ka : '', { type: 'number', attrs: 'min="0" max="1000" step="any"', placeholder: '25' })}
      <p class="field full hint" style="margin:0">Written under the busbar the way a drawing writes it: 3p, 50Hz 1250A/25kA. A way on the board above lands on this one by naming it as its destination.</p>`,
      async d => {
        const body = { name: d.name, voltage: d.voltage, phases: d.phases, frequency: d.frequency,
          current_a: d.current_a === '' ? null : Number(d.current_a),
          fault_ka: d.fault_ka === '' ? null : Number(d.fault_ka) };
        if (isEdit) { await api('PUT', '/api/hv/switchboards/' + board.id, body); await afterChange('Switchboard updated'); }
        else { await api('POST', '/api/hv/switchboards?network=' + ((state.hv || {}).id || 0), body); await afterChange('Switchboard added'); }
      },
      isEdit && canManage() ? { wide: true, deleteLabel: 'Delete switchboard', onDelete: () => hvDeleteBoard(board) } : { wide: true });
  }
  function hvDeleteBoard(board) {
    if (board.sections.length) {
      toast('Delete the bus sections on ' + board.name + ' first.', 'error');
      return;
    }
    confirmModal('Delete switchboard', `Delete <b>${esc(board.name)}</b>?`,
      async () => { await api('DELETE', '/api/hv/switchboards/' + board.id); await afterChange('Switchboard deleted'); });
  }

  function hvSectionForm(sec, boardId) {
    const isEdit = !!(sec && sec.id);
    const boards = hvBoards();
    const on = isEdit ? hvBoardOfSection(sec.id) : (hvFindBoard(Number(boardId)) || boards[0]);
    formModal(isEdit ? 'Rename ' + sec.name : 'Add bus section', `
      ${isEdit ? '' : field('On switchboard', 'switchboard_id', on ? on.id : '', { type: 'select', required: true, full: true, options: boards.map(b => ({ value: b.id, label: b.name })) })}
      ${field('Section name', 'name', isEdit ? sec.name : 'Section ' + String.fromCharCode(65 + ((on && on.sections.length) || 0)), { required: true, full: true, hint: 'A length of busbar. Every feeder on it backs every way tapping it.' })}`,
      async d => {
        if (isEdit) { await api('PUT', '/api/hv/sections/' + sec.id, { name: d.name }); await afterChange('Section renamed'); }
        else { await api('POST', '/api/hv/sections', { name: d.name, switchboard_id: Number(d.switchboard_id) }); await afterChange('Bus section added'); }
      },
      isEdit && canManage() ? { deleteLabel: 'Delete section', onDelete: () => hvDeleteSection(sec) } : {});
  }
  function hvDeleteSection(sec) {
    confirmModal('Delete bus section', `Delete <b>${esc(sec.name)}</b> with its ${plural(sec.feeders.length, 'incoming feeder')} and ${plural(sec.ways.length, 'outgoing way')}, and any coupler tied to it? This cannot be undone.`,
      async () => { await api('DELETE', '/api/hv/sections/' + sec.id); await afterChange('Bus section deleted'); });
  }
  function hvFindFeeder(id) { return hvFeeders().find(f => f.id === id); }
  function hvFindWay(id) {
    for (const sec of hvSections()) { const w = sec.ways.find(x => x.id === id); if (w) return w; }
    return null;
  }
  function hvFindCoupler(id) { return hvCouplers().find(c => c.id === id); }

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
    // The click that follows a drag belongs to the drag, not to whatever it
    // was dropped on. Anything later is a real click.
    if (Date.now() - hvDragEndedAt < 350) { hvDragEndedAt = 0; e.stopPropagation(); return; }
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
      case 'filter-level': state.boardFilter.level = el.dataset.level; renderSidebar(); break;
      case 'filter-clear': state.boardFilter = { q: '', level: '' }; renderSidebar(); break;
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
      case 'hv-rename': hvRenameForm(); break;
      case 'hv-add-section': hvSectionForm(null); break;
      case 'hv-add-network': if (canManage()) hvNetworkForm(null); break;
      case 'hv-add-board': if (canManage()) hvBoardForm(null); break;
      case 'hv-edit-board': if (canManage()) hvBoardForm(hvFindBoard(id)); break;
      case 'hv-edit-section': if (canManage()) hvSectionForm(hvFindSection(id)); break;
      case 'hv-add-feeder': hvFeederForm(null, id); break;
      case 'hv-edit-feeder': if (canManage()) hvFeederForm(hvFindFeeder(id)); break;
      case 'hv-add-way': hvWayForm(null, id); break;
      case 'hv-edit-way': if (canEdit()) hvWayForm(hvFindWay(id)); break;
      case 'hv-add-device': if (canEdit()) hvDeviceForm(null, el.dataset.way, el.dataset.after, el.dataset.feeder); break;
      case 'hv-edit-device': if (canEdit()) hvDeviceForm(hvFindDevice(id)); break;
      case 'hv-add-coupler': hvCouplerForm(null); break;
      case 'hv-edit-coupler': if (canManage()) hvCouplerForm(hvFindCoupler(id)); break;
      case 'hv-open-dest': navigate(boardHash('dashboard', id)); break;
      // Across to another drawing, landing on the switchboard the way feeds.
      case 'hv-open-drawing': navigate('#/hv/' + id + (el.dataset.board ? '?focus=hv-board-' + el.dataset.board : '')); break;
      // The same designation on another drawing.
      case 'hv-open-ref': navigate('#/hv/' + id + '?focus=' + el.dataset.ref); break;
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
  // Hold a connection open for as long as this window is on screen. The local
  // application watches these connections to know when its window has been
  // closed and it can stop; without it, it would go on running unseen after the
  // window is gone, because the browser showing the window is often one the
  // user already had running and does not exit with it.
  try {
    const session = new EventSource('/api/session');
    session.onerror = () => { /* EventSource reconnects on its own */ };
    window.addEventListener('pagehide', () => session.close());
  } catch (_) { /* no EventSource: the application falls back to its own timeout */ }

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
