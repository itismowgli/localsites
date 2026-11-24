/* LocalSites desktop UI. Plain JS, no framework, no build step.
   Everything is inside this function: nothing leaks into window (Wails owns window.go / window.runtime). */
(() => {
'use strict';

/* ------------------------------------------------------------------ bridge */
const bridge = {
  async call(method, params = {}) {
    if (window.go?.main?.App) {
      try { return JSON.parse(await window.go.main.App.Call(method, JSON.stringify(params)) || 'null'); }
      catch (e) { const m = String(e?.message || e); const fields = parseFieldErrors(m); throw Object.assign(new Error(m), fields ? { fields } : {}); }
    }
    if (!location.protocol.startsWith('http')) throw new Error('The app could not reach its background service. Please restart LocalSites.');
    const r = await fetch('/__call/' + method, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(params) });
    const j = await r.json();
    if (!r.ok) throw Object.assign(new Error(j.error || r.statusText), { fields: j.fields });
    return j;
  },
  copy(text) { return window.go?.main?.App ? window.go.main.App.CopyText(text) : navigator.clipboard.writeText(text); },
};
const inApp = !!window.go?.main?.App;
// Desktop errors arrive as text ("field: message; field: message"); turn them back into field errors.
function parseFieldErrors(msg) {
  const parts = msg.split('; ').map(p => p.match(/^([a-z_]+(?:\.[a-z0-9_.]+)?): (.+)$/));
  return parts.length && parts.every(Boolean) && parts.every(p => /^(sites_root|roots(\.\d+)?|front_door|[a-z]+_port|default_php|php_ini\..+|start_at_login)$/.test(p[1])) ? parts.map(p => ({ field: p[1], message: p[2] })) : null;
}

/* --------------------------------------------------------------- debugging */
function reportError(where, err) {
  const msg = `[ui] ${where}: ${err?.message || err}${err?.stack ? '\n' + err.stack : ''}`;
  console.error(msg);
  try { window.runtime?.LogError?.(msg); } catch (_) { /* never throw from logging */ }
  try { window.go?.main?.App?.LogUI?.(msg); } catch (_) { /* ignore */ }
}
window.addEventListener('error', (e) => reportError('error', e.error || e.message));
window.addEventListener('unhandledrejection', (e) => reportError('promise', e.reason));

const isMac = /Mac/.test(navigator.platform) || /Mac OS/.test(navigator.userAgent);
const MOD = isMac ? '⌘' : 'Ctrl+';
const OS_NAME = isMac ? 'Mac' : 'computer';
const FILES_APP = isMac ? { id: 'finder', name: 'Finder' } : { id: 'files', name: 'Files' };

/* ------------------------------------------------------------------- icons */
const TRI = '<svg viewBox="0 0 10 10" aria-hidden="true"><path d="M1.5 3h7L5 7.5z" fill="currentColor"/></svg>';
// Lucide icons (tools/build-assets.mjs → icons.js). icon.name returns an inline SVG string.
const icon = new Proxy({}, { get: (_, name) => name === 'tri' ? TRI : (window.LS_ICONS && window.LS_ICONS[name]
  ? `<svg class="svg-i" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${window.LS_ICONS[name]}</svg>` : '') });

const brand = (name, color) => window.LS_BRANDS?.[name]
  ? `<svg class="svg-i brand" viewBox="0 0 24 24" aria-hidden="true" style="${color ? `color:${color}` : ''}"><path fill="currentColor" d="${window.LS_BRANDS[name]}"/></svg>` : '';
// Project types: label, icon and brand colour (colours only on the small type badge)
const TYPES = {
  php: { label: 'PHP', icon: () => brand('php', '#8892BF') },
  laravel: { label: 'Laravel', icon: () => brand('laravel', '#FF2D20') },
  wordpress: { label: 'WordPress', icon: () => brand('wordpress', '#3E8BB8') },
  static: { label: 'Static', icon: () => brand('html5', '#E8693C') },
  nextjs: { label: 'Next.js', icon: () => brand('nextdotjs') },
  node: { label: 'Node', icon: () => brand('nodedotjs', '#5FA04E') },
  command: { label: 'Custom', icon: () => icon.terminal },
  docker: { label: 'Docker', icon: () => brand('docker', '#2496ED') },
  other: { label: 'Not a website', icon: () => icon.folder },
};
const typeOf = (s) => TYPES[s.type] || TYPES.other;
function hostIcon(host = '') {
  const h = host.toLowerCase();
  if (h.includes('gitlab')) return brand('gitlab', '#E24329');
  if (h.includes('bitbucket')) return brand('bitbucket', '#2684FF');
  if (h.includes('github')) return brand('github');
  return brand('git', '#F05033');
}
// Empty state: icon tile, title, one sentence, actions, optional keyboard hint
// fig: a Hairline figure (vendor/hairline.js) shown instead of the icon, for the empty states it explains
function emptyState({ ic = 'inbox', fig = '', title, text = '', actions = '', hint = '' }) {
  return `<div class="empty-state">${fig ? `<div class="es-fig" data-fig="${fig}" aria-hidden="true"></div>` : `<div class="es-icon">${icon[ic] || ic}</div>`}<h3>${esc(title)}</h3>${text ? `<p>${text}</p>` : ''}
    ${actions ? `<div class="es-actions">${actions}</div>` : ''}${hint ? `<div class="es-hint">${hint}</div>` : ''}</div>`;
}

/* --------------------------------------------------- Hairline figures */
// Line figures that answer the pointer (vendor/hairline.js, MIT). Any element with data-fig is mounted when it
// appears and destroyed when it leaves the page; the module is only loaded the first time one is needed.
const figures = new Map(); // element -> figure
let hairline = null;
function mountFigures() {
  for (const [el, f] of figures) if (!el.isConnected) { f.destroy(); figures.delete(el); }
  const fresh = [...document.querySelectorAll('[data-fig]')].filter(el => !figures.has(el));
  if (!fresh.length) return;
  (hairline ||= import('./vendor/hairline.js')).then(HL => {
    for (const el of fresh) if (el.isConnected && !figures.has(el) && HL[el.dataset.fig]) figures.set(el, HL[el.dataset.fig](el, { intensity: 0.5 }));
  }).catch(() => {});
}
new MutationObserver(() => mountFigures()).observe(document.documentElement, { childList: true, subtree: true });

/* --------------------------------------------------- design system (docs/DESIGN.md) */
// Shared building blocks. Screens compose these instead of writing one-off markup, so every service looks the same.
const UI = {
  section: ({ title, note = '', right = '' }) => `<div class="c-section"><div><h2>${esc(title)}</h2>${note ? `<p class="note">${note}</p>` : ''}</div>${right ? `<div class="c-section-r">${right}</div>` : ''}</div>`,
  status: (state, label) => `<span class="c-status ${state}">${esc(label)}</span>`, // state: on | off | warn | err | busy
  tag: (text) => `<span class="c-tag">${esc(text)}</span>`,
  kv: (label, value) => `<div class="c-kv"><span>${esc(label)}</span><span>${value}</span></div>`,
  btn: (label, { ic = '', kind = '', sm = true, attrs = '', title = '' } = {}) =>
    `<button class="btn ${kind} ${sm ? 'sm' : ''}" ${attrs} ${title ? `title="${esc(title)}"` : ''}>${ic ? icon[ic] || ic : ''}${label ? esc(label) : ''}</button>`,
  iconBtn: (ic, title, attrs = '') => `<button class="icon-btn" title="${esc(title)}" ${attrs}>${icon[ic] || ic}</button>`,
  toggle: (on, attrs, label = '') => `<label class="switch" title="${esc(label)}"><input type="checkbox" ${on ? 'checked' : ''} ${attrs} aria-label="${esc(label)}"><span></span></label>`,
  code: (text, shown = text) => `<pre class="c-code">${esc(shown)}<button class="btn ghost sm" data-copy="${esc(text)}">${icon.copy}Copy</button></pre>`,
  tabs: (items, active, attr) => `<div class="c-tabs" role="tablist">${items.map(([id, label, ic]) => `<button ${attr}="${esc(id)}" class="${id === active ? 'on' : ''}" role="tab" aria-selected="${id === active}">${ic || ''}${esc(label)}</button>`).join('')}</div>`,
  card: ({ ic, title, tags = '', sub = '', status = '', body = '', foot = '', on = false, attrs = '' }) =>
    `<article class="c-card ${on ? 'on' : ''}" ${attrs}><div class="c-card-h"><span class="c-ic">${ic}</span><div style="min-width:0;flex:1"><div class="c-title">${esc(title)}${tags}</div>${sub ? `<div class="c-sub">${sub}</div>` : ''}</div>${status}</div>
      ${body ? `<div class="c-card-b">${body}</div>` : ''}${foot ? `<div class="c-card-f">${foot}</div>` : ''}</article>`,
  field: (label, control, hint = '') => `<label class="c-field"><span>${esc(label)}</span>${control}${hint ? `<span class="hint">${hint}</span>` : ''}</label>`,
};

/* ------------------------------------------------------------- persistence */
const store = {
  get(k, def) { try { const v = localStorage.getItem('ls.' + k); return v === null ? def : JSON.parse(v); } catch (_) { return def; } },
  set(k, v) { try { localStorage.setItem('ls.' + k, JSON.stringify(v)); } catch (_) { /* storage may be blocked */ } },
};

// The LocalSites logo (folder and globe), the same artwork as the app and menu bar icons
const LOGO = '<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="currentColor" d="M1.5 6.75C1.5 5.75544 1.89509 4.80161 2.59835 4.09835C3.30161 3.39509 4.25544 3 5.25 3H8.379C8.97554 3.00053 9.54745 3.23792 9.969 3.66L12 5.688L8.91 8.778C8.84029 8.84832 8.75734 8.90415 8.66595 8.94225C8.57455 8.98035 8.47652 8.99998 8.3775 9H1.5V6.75ZM1.5 10.5V17.25C1.5 18.2446 1.89509 19.1984 2.59835 19.9016C3.30161 20.6049 4.25544 21 5.25 21H9.9C9.0367 19.3101 8.7824 17.3742 9.17994 15.5186C9.57747 13.663 10.6026 12.0013 12.0824 10.8133C13.5623 9.62538 15.4064 8.98393 17.304 8.99708C19.2017 9.01023 21.0367 9.67717 22.5 10.8855V9.75C22.5 8.75544 22.1049 7.80161 21.4016 7.09835C20.6984 6.39509 19.7446 6 18.75 6H13.8105L9.9705 9.84C9.76141 10.0493 9.51308 10.2154 9.23973 10.3287C8.96638 10.4419 8.67338 10.5001 8.3775 10.5H1.5ZM15.825 12.225C15.39 13.3155 15.0855 14.808 15.015 16.5H19.485C19.4145 14.808 19.11 13.3155 18.675 12.225C18.4245 11.5995 18.1455 11.142 17.8725 10.854C17.6025 10.569 17.3925 10.5 17.25 10.5C17.1075 10.5 16.8975 10.569 16.6275 10.854C16.3545 11.142 16.0755 11.5995 15.825 12.225ZM14.745 10.98C14.629 11.2041 14.5244 11.4339 14.4315 11.6685C13.9155 12.96 13.584 14.649 13.515 16.5H10.5405C10.676 15.2855 11.1387 14.1305 11.8791 13.1584C12.6196 12.1862 13.6101 11.4333 14.745 10.98ZM19.755 10.98C19.867 11.198 19.971 11.4275 20.067 11.6685C20.5845 12.9615 20.9145 14.649 20.985 16.5H23.958C23.8226 15.2857 23.3602 14.1308 22.62 13.1587C21.8798 12.1865 20.8896 11.4335 19.755 10.98ZM23.958 18H20.985C20.9145 19.851 20.5845 21.54 20.067 22.8315C19.971 23.0715 19.867 23.301 19.755 23.52C20.8896 23.0665 21.8798 22.3135 22.62 21.3413C23.3602 20.3692 23.8226 19.2143 23.958 18ZM17.25 24C17.391 24 17.6025 23.931 17.8725 23.646C18.1455 23.358 18.4245 22.9005 18.675 22.275C19.11 21.1845 19.4145 19.692 19.485 18H15.015C15.0855 19.692 15.39 21.1845 15.825 22.275C16.0755 22.9005 16.3545 23.358 16.6275 23.646C16.8975 23.931 17.1075 24 17.25 24ZM14.745 23.52C13.6099 23.0669 12.619 22.3141 11.8783 21.3419C11.1375 20.3697 10.6746 19.2147 10.539 18H13.515C13.5855 19.851 13.917 21.54 14.433 22.8315C14.53 23.0715 14.634 23.301 14.745 23.52Z"/></svg>';

/* ------------------------------------------------------------------- state */
const VIEWS = {
  overview: { title: 'Overview', icon: 'overview', key: 'o' },
  projects: { title: 'Projects', icon: 'projects', key: 'p' },
  databases: { title: 'Databases', icon: 'db', key: 'd' },
  data: { title: 'Data', icon: 'table', key: 't' },
  agent: { title: 'Agent', icon: 'agent', key: 'a' },
  services: { title: 'Services', icon: 'services', key: 's' },
  logs: { title: 'Logs', icon: 'logs', key: 'l' },
  doctor: { title: 'Doctor', icon: 'doctor', key: 'h' },
  import: { title: 'Import', icon: 'import', key: 'i' },
  settings: { title: 'Settings', icon: 'settings', key: ',' },
};
const newEdits = () => ({ upd: {}, del: new Set(), ins: [] });
const S = {
  sites: [], status: null, dbs: null, dbErr: null, activity: [], doctor: null, doctorBusy: false, checks: {}, exe: '',
  filter: '', ptab: ['all', 'running', 'attention'].includes(store.get('ptab', 'all')) ? store.get('ptab', 'all') : 'all',
  groupBy: store.get('groupBy', 'folder'), order: store.get('order', 'status'),
  pf: store.get('pf', {}), props: new Set(store.get('props', ['domain', 'branch', 'database', 'account', 'version', 'type'])),
  log: { kind: 'apache', folder: '' }, logAuto: true, logText: '',
  tabs: store.get('tabs', [{ view: 'overview', sel: null }]).map(t => ({ ...t, back: [], fwd: [] })), tab: store.get('tab', 0),
  closed: store.get('closed', {}), expanded: new Set(store.get('expanded', ['nav:projects'])), favs: new Set(store.get('favs', [])),
  sideHidden: store.get('sideHidden', false), sidePeek: false,
  theme: store.get('theme', 'system'), version: null, seenVersion: store.get('seenVersion', ''),
  settings: null, setSec: 'general', setDraft: {}, setErrors: {}, setSaving: false,
  agent: { text: '', sel: 0, result: null, busy: false },
  dpane: store.get('dpane', 'details'), git: {},
  data: { conns: null, conn: store.get('dataConn', ''), info: null, err: '', needPw: false, schemas: [], schema: '', tables: [], tfilter: '',
    table: '', tab: 'rows', page: 0, size: 100, order: '', desc: false, filter: { column: '', op: 'contains', value: '', idx: -1 }, filters: [],
    result: null, structure: null, sql: '', qres: null, qerr: '', allowWrite: false, busy: false,
    mode: 'view', ed: newEdits(), cols: {}, ac: null },
  imp: { source: 'mamp-pro', user: 'root', password: '', dbs: null, chosen: new Set(), reports: {}, running: false, err: '' },
};
if (!S.tabs.length || !S.tabs.every(t => VIEWS[t.view])) S.tabs = [{ view: 'overview', sel: null, back: [], fwd: [] }];
if (S.tab >= S.tabs.length) S.tab = 0;
const cur = () => S.tabs[S.tab];
const $ = (s) => document.querySelector(s);
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const site = (f) => S.sites.find(s => s.folder === f);
const uniq = (a) => [...new Set(a)];
const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;
function saveUI() {
  store.set('tabs', S.tabs.map(t => ({ view: t.view, sel: t.sel })));
  store.set('tab', S.tab); store.set('closed', S.closed); store.set('expanded', [...S.expanded]);
  store.set('favs', [...S.favs]); store.set('sideHidden', S.sideHidden); store.set('theme', S.theme); store.set('ptab', S.ptab); store.set('groupBy', S.groupBy);
  store.set('seenVersion', S.seenVersion); store.set('pf', S.pf); store.set('props', [...S.props]); store.set('order', S.order); store.set('dpane', S.dpane);
}
// Open a web page: the desktop app hands it to the default browser
function openWeb(url) {
  if (!/^https?:\/\//.test(url || '')) return;
  if (window.go?.main?.App?.OpenURL) return act(() => window.go.main.App.OpenURL(url));
  window.open(url, '_blank', 'noopener');
}

/* ------------------------------------------------------------------ toasts */
// A deck: the newest toast on top, older ones tucked behind it. Hovering fans them out and pauses their
// timers; each one closes with its x or a swipe to the right.
const TOAST_MAX = 6;
// action: an optional button, { label, attrs } (attrs are data-* the click router knows, e.g. data-menu="whatsnew")
function toast(text, kind = 'ok', action = null) {
  const t = document.createElement('div');
  t.className = 'toast';
  t.setAttribute('role', kind === 'err' ? 'alert' : 'status');
  const act = action ? `<button class="btn sm toast-act" ${action.attrs}>${esc(action.label)}</button>` : '';
  t.innerHTML = `<span class="dot ${kind === 'err' ? 'err' : kind === 'warn' ? 'warn' : 'on'}"></span><pre>${esc(text)}</pre>${act}<button class="toast-x" title="Dismiss">${icon.x}</button>`;
  t._left = kind === 'err' ? 9000 : action ? 9000 : 4200;
  $('#toasts').append(t);
  const all = [...$('#toasts').querySelectorAll('.toast:not(.out)')];
  for (const old of all.slice(0, Math.max(0, all.length - TOAST_MAX))) dropToast(old);
  armToast(t); layoutToasts();
}
function armToast(t) {
  if (!t._left || t.id === 'updAsk') return; // the update question waits for an answer
  clearTimeout(t._timer); t._from = Date.now();
  t._timer = setTimeout(() => dropToast(t), t._left);
}
function dropToast(t, dx = 0) {
  if (t.classList.contains('out')) return;
  clearTimeout(t._timer); t.classList.add('out');
  if (dx) t.style.setProperty('--dx', dx + 'px');
  setTimeout(() => { t.remove(); layoutToasts(); }, 180);
}
// Collapsed: each older toast sits 8px higher and a little smaller. Open: stacked with gaps, newest at the bottom.
function layoutToasts() {
  const box = $('#toasts'), list = [...box.querySelectorAll('.toast:not(.out)')].reverse(), open = box.matches(':hover') || box.contains(document.activeElement);
  let y = 0;
  list.forEach((t, i) => {
    t.style.zIndex = String(100 - i);
    if (open) { t.style.transform = `translateY(${-y}px)`; t.style.opacity = '1'; y += t.offsetHeight + 8; }
    else { t.style.transform = `translateY(${-i * 8}px) scale(${1 - i * 0.04})`; t.style.opacity = i > 2 ? '0' : '1'; }
    t.classList.toggle('behind', !open && i > 0);
  });
  box.style.height = (!list.length ? 0 : open ? Math.max(0, y - 8) : list[0].offsetHeight + Math.min(2, list.length - 1) * 8) + 'px';
}
{
  const box = $('#toasts');
  box.addEventListener('mouseenter', () => { for (const t of box.querySelectorAll('.toast')) { clearTimeout(t._timer); if (t._from) t._left = Math.max(1200, t._left - (Date.now() - t._from)); } layoutToasts(); });
  box.addEventListener('mouseleave', () => { for (const t of box.querySelectorAll('.toast:not(.out)')) armToast(t); layoutToasts(); });
  box.addEventListener('click', (e) => { const x = e.target.closest('.toast-x, .toast-act'); if (x) dropToast(x.closest('.toast')); });
  let drag = null;
  box.addEventListener('pointerdown', (e) => { const t = e.target.closest('.toast'); if (!t || e.target.closest('button, a, input')) return; drag = { t, x: e.clientX }; t.setPointerCapture(e.pointerId); t.classList.add('dragging'); });
  box.addEventListener('pointermove', (e) => { if (!drag) return; const dx = Math.max(0, e.clientX - drag.x); drag.t.style.translate = dx + 'px 0'; drag.t.style.opacity = String(Math.max(0.2, 1 - dx / 240)); });
  const end = (e) => { if (!drag) return; const { t, x } = drag, dx = e.clientX - x; drag = null; t.classList.remove('dragging');
    if (dx > 80) dropToast(t, dx); else { t.style.translate = ''; t.style.opacity = ''; } };
  box.addEventListener('pointerup', end); box.addEventListener('pointercancel', end);
}
/* ------------------------------------------------------------------ timers */
// One clock for every countdown on the page: [data-remain=ISO] counts down, [data-elapsed=ms since epoch]
// counts up, [data-bar-from][data-bar-to] is a bar that empties as the time runs out.
function fmtLeft(ms) {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return s + 's';
  const m = Math.round(s / 60);
  return m < 60 ? m + ' min' : `${Math.floor(m / 60)} h ${m % 60} min`;
}
function fmtClock(ms) {
  const s = Math.max(0, Math.floor(ms / 1000)), h = Math.floor(s / 3600), m = Math.floor(s / 60) % 60, ss = String(s % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`;
}
function tickTimes() {
  const now = Date.now();
  const put = (el, t) => { if (el.textContent !== t) el.textContent = t; }; // unchanged text: no DOM change, so no new tick
  for (const el of document.querySelectorAll('[data-remain]')) put(el, fmtLeft(new Date(el.dataset.remain) - now));
  for (const el of document.querySelectorAll('[data-elapsed]')) put(el, fmtClock(now - Number(el.dataset.elapsed)));
  for (const el of document.querySelectorAll('[data-bar-to]')) {
    const a = new Date(el.dataset.barFrom).getTime(), b = new Date(el.dataset.barTo).getTime();
    el.style.transform = `scaleX(${b > a ? Math.max(0, Math.min(1, (b - now) / (b - a))) : 0})`;
  }
}
setInterval(() => { if (!document.hidden) tickTimes(); }, 1000);
new MutationObserver(() => { clearTimeout(tickTimes.q); tickTimes.q = setTimeout(tickTimes, 0); }).observe(document.body, { childList: true, subtree: true });

function logToast(res) {
  const lines = res?.log || [];
  if (lines.length) toast(lines.join('\n'), lines.some(l => /fail|error|could not|not ready/i.test(l)) ? 'err' : 'ok');
}
// Requests cancelled because the page is reloading or closing are not errors
let unloading = false;
addEventListener('pagehide', () => { unloading = true; });
addEventListener('beforeunload', () => { unloading = true; });
async function act(fn, label) {
  try { return await fn(); } catch (e) {
    if (unloading) return;
    reportError(label || 'action', e);
    if (S.offline && !label) return; // the banner already says the service isn't answering
    toast((label ? label + ': ' : '') + (e.message || e), 'err');
  }
}

/* ------------------------------------------------------- dialogs (in-app) */
function dialog(html) {
  return new Promise((resolve) => {
    const ov = $('#keysOverlay'), box = $('#keysDialog');
    box.innerHTML = html; ov.classList.add('open');
    const done = (v) => { ov.classList.remove('open'); ov.onmousedown = null; box.onkeydown = null; resolve(v); };
    ov.onmousedown = (e) => { if (e.target === ov) done(null); };
    box.onkeydown = (e) => { if (e.key === 'Escape') { e.preventDefault(); done(null); } };
    box.querySelectorAll('[data-dlg]').forEach(b => { b.onclick = () => done(b.dataset.dlg === 'ok' ? (box.querySelector('input')?.value.trim() ?? true) : b.dataset.dlg === 'cancel' ? null : b.dataset.dlg); });
    const inp = box.querySelector('input');
    if (inp) { inp.focus(); inp.onkeydown = (e) => { if (e.key === 'Enter') done(inp.value.trim()); }; } else box.querySelector('[data-dlg="ok"]')?.focus();
  });
}
const askText = (title, placeholder, okLabel = 'Create') => dialog(`<h3>${esc(title)}</h3>
  <input class="input" style="width:100%" placeholder="${esc(placeholder)}" spellcheck="false">
  <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn ghost" data-dlg="cancel">Cancel</button><button class="btn primary" data-dlg="ok">${esc(okLabel)}</button></div>`);
const confirmBox = (title, text, okLabel) => dialog(`<h3>${esc(title)}</h3><p class="note">${esc(text)}</p>
  <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn ghost" data-dlg="cancel">Cancel</button><button class="btn primary" data-dlg="ok">${esc(okLabel)}</button></div>`);
// Shortcut Sheet: every shortcut, grouped, with a search box (opens with ? or Cmd+/)
function shortcutGroups() {
  return [
    ['General', [['Command launcher', [MOD + 'K']], ['Keyboard shortcuts', [MOD + '/', '?']], ['Close a panel, menu or dialog', ['Esc']]]],
    ['Go to', Object.values(VIEWS).map(v => [v.title, ['G', v.key.toUpperCase()]])],
    ['Window', [['New tab', [MOD + 'T']], ['Close tab', [MOD + 'W']], ['Switch to tab 1 to 9', [MOD + '1…9']], ['Back', [MOD + '[']], ['Forward', [MOD + ']']], ['Show or hide the sidebar', [MOD + '\\']]]],
    ['Projects', [['Search projects', ['/']], ['Filter by type, folder, branch…', ['F']], ['Next / previous project', ['J', 'K']], ['Switch the selected project on or off', ['E']], ['Open it in the browser', ['O']]]],
    ['Data', [['Run the SQL', [MOD + '↵']], ['Accept a suggestion', ['Tab']]]],
  ];
}
function keysHTML(q = '') {
  q = q.trim().toLowerCase();
  const groups = shortcutGroups().map(([g, list]) => [g, list.filter(([l, keys]) => !q || (g + ' ' + l + ' ' + keys.join(' ')).toLowerCase().includes(q))]).filter(([, l]) => l.length);
  return groups.length ? groups.map(([g, list]) => `<div class="keys-g"><div class="keys-h">${esc(g)}</div>${list.map(([l, keys]) =>
    `<div class="keys-r"><span>${esc(l)}</span><span>${keys.map(k => k === 'then' ? '<span class="note">then</span>' : `<kbd>${esc(k)}</kbd>`).join(' ')}</span></div>`).join('')}</div>`).join('')
    : `<div class="menu-empty">No shortcut matches “${esc(q)}”.</div>`;
}
function showKeys() {
  const p = dialog(`<div class="keys-top"><h3>Keyboard shortcuts</h3><label class="qbox">${icon.search}<input class="input sm" id="keysQ" placeholder="Search shortcuts" spellcheck="false" autocomplete="off" aria-label="Search shortcuts"></label></div>
    <div class="keys-sheet" id="keysList">${keysHTML()}</div>
    <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn" data-dlg="ok">Done</button></div>`);
  const qi = $('#keysQ'); qi.onkeydown = null;
  qi.oninput = () => { $('#keysList').innerHTML = keysHTML(qi.value); };
  return p;
}

/* -------------------------------------------------------------------- data */
async function loadStatus() { S.status = await bridge.call('status'); }
async function loadSites() { S.sites = await bridge.call('sites.list') || []; }
async function loadSetup() {
  const was = S.setup?.running, prev = S.setup;
  try { S.setup = await bridge.call('setup.status'); } catch { S.setup = null; }
  trackSetup(prev, S.setup);
  if (was && S.setup && !S.setup.running) { // a fix just finished: say how it went and check again
    const bad = Object.entries(S.setup.results || {}).filter(([, v]) => v !== 'ok' && v !== 'opened');
    toast(bad.length ? `Setup finished, but ${bad.map(([k]) => k).join(', ')} didn’t work; see Overview` : 'Everything is installed and repaired', bad.length ? 'warn' : 'ok');
    command('doctor', { quiet: true });
  }
}
async function loadActivity() { S.activity = await bridge.call('activity') || []; }
async function loadNotices() { try { S.notices = await bridge.call('notices.list'); } catch (_) { S.notices = S.notices || null; /* an older service */ } }
async function loadVersion() { S.version = await bridge.call('version'); }
async function loadSettings() { S.settings = await bridge.call('settings.get'); S.settingsErr = null; }
let settingsLoading = false;
async function loadSettingsSafe() {
  if (settingsLoading) return;
  settingsLoading = true;
  try { await loadSettings(); } catch (e) { S.settingsErr = e.message?.includes('unknown method') ? 'The background service is an older version. Restart services (Services → Restart all) to update it.' : e.message; }
  settingsLoading = false; render();
}
function applyTheme() {
  if (S.theme === 'system') document.documentElement.removeAttribute('data-theme'); else document.documentElement.dataset.theme = S.theme;
  if (typeof applyLook === 'function' && S.look) applyLook(); // the accent's text colour depends on the mode
  syncWindowColour();
}
// The native window shows its own colour while it resizes; keep it equal to the page background
function syncWindowColour() {
  const c = getComputedStyle(document.documentElement).getPropertyValue('--win').trim();
  try { window.go?.main?.App?.SetWindowColour?.(c); } catch (_) { /* not in the desktop app */ }
}
try { matchMedia('(prefers-color-scheme: dark)').addEventListener('change', syncWindowColour); } catch (_) { /* old engines */ }
let dbsInFlight = null;
function loadDBs() {
  dbsInFlight ??= (async () => {
    try { S.dbs = await bridge.call('db.list') || []; S.dbErr = null; } catch (e) { S.dbs = []; S.dbErr = e.message; } finally { dbsInFlight = null; }
    // the PostgreSQL add-on's databases (only when it is on)
    if ((S.status?.addons || []).some(a => a.id === 'postgres' && a.enabled)) {
      try { S.pgdbs = await bridge.call('pg.list') || []; S.pgErr = null; } catch (e) { S.pgdbs = []; S.pgErr = /unknown method/i.test(e.message) ? null : e.message; }
    } else { S.pgdbs = null; S.pgErr = null; }
  })();
  return dbsInFlight;
}
async function refresh() {
  await act(() => Promise.all([loadStatus(), loadSites(), loadActivity(), loadSetup(), loadNotices()]));
  render();
}
async function updateSite(folder, patch) {
  const res = await act(() => bridge.call('sites.update', { folder, ...patch }));
  if (res) { S.sites = res.sites; logToast(res); await act(() => Promise.all([loadStatus(), loadActivity()])); }
  render();
  return res;
}

/* ------------------------------------------------------------- navigation */
function navigate(view, sel = null, opts = {}) {
  const t0 = cur(), changed = opts.newTab || t0.view !== view;
  if (opts.newTab) { S.tabs.push({ view, sel, back: [], fwd: [] }); S.tab = S.tabs.length - 1; }
  else {
    const t = cur();
    if (t.view !== view || t.sel !== sel) { t.back.push({ view: t.view, sel: t.sel }); t.fwd = []; }
    t.view = view; t.sel = sel;
  }
  if (S.sidePeek) { S.sidePeek = false; }
  onEnter(view); saveUI(); render();
  if (changed) animateView();
}
// A short fade when the view changes (never on background refreshes)
function animateView() {
  const v = $('#view'); if (!v) return;
  v.classList.remove('enter'); void v.offsetWidth; v.classList.add('enter');
  clearTimeout(animateView.t); animateView.t = setTimeout(() => v.classList.remove('enter'), 220);
}
function onEnter(view) {
  if (view === 'databases' || view === 'import') loadDBs().then(render);
  if ((view === 'databases' || view === 'services') && !S.settings) loadSettingsSafe();
  if (view === 'settings') loadSettingsSafe();
  if (view === 'data') dataInit();
  if (view === 'logs') loadLog();
  if (view === 'doctor' && !S.doctor && !S.doctorBusy) command('doctor');
  if (view === 'overview' && !S.doctor && !S.doctorBusy) command('doctor', { quiet: true });
  if (view === 'overview') act(async () => { await loadProblems(); if (cur().view === 'overview') renderView(); });
  if (view === 'overview') { loadUptime(); if (!S.agents) act(async () => { S.agents = await bridge.call('agents.list') || []; if (cur().view === 'overview') renderView(); }); }
  if (view === 'agent') setTimeout(() => $('#agentInput')?.focus(), 30);
}
function goHistory(dir) {
  const t = cur(), from = dir < 0 ? t.back : t.fwd, to = dir < 0 ? t.fwd : t.back;
  if (!from.length) return;
  to.push({ view: t.view, sel: t.sel });
  const view = t.view;
  Object.assign(t, from.pop());
  onEnter(t.view); saveUI(); render();
  if (view !== t.view) animateView();
}
function closeTab(i) {
  if (S.tabs.length === 1) S.tabs[0] = { view: 'overview', sel: null, back: [], fwd: [] };
  else { S.tabs.splice(i, 1); if (S.tab >= i && S.tab > 0) S.tab--; }
  onEnter(cur().view); saveUI(); render(); animateView();
}
const narrowWin = () => innerWidth <= 760;
function toggleSidebar() {
  if (narrowWin()) { S.sidePeek = !S.sidePeek; $('#body').classList.toggle('peek', S.sidePeek); return; }
  S.sideHidden = !S.sideHidden; saveUI(); $('#body').classList.toggle('collapsed', S.sideHidden);
}

/* ------------------------------------------------------------------ render */
// Replace a region only when its content changed: background refreshes then cost nothing and never
// swap out a button under the pointer. Any click or typing invalidates, so direct DOM changes get redrawn.
// While a mouse button is held inside a region, its redraw waits until just after the click: replacing
// the element under the pointer between mousedown and mouseup would lose the click.
let pressedIn = null;
const deferred = new Map();
document.addEventListener('pointerdown', (e) => { pressedIn = e.target; }, true);
// The click itself is being delivered: catch up on held redraws first, so the click's own redraws happen in order
function releasePress() {
  pressedIn = null;
  for (const [el, html] of deferred) { deferred.delete(el); if (el._html !== html) { el.innerHTML = html; el._html = html; } }
}
document.addEventListener('click', releasePress, true);
document.addEventListener('pointerup', () => setTimeout(releasePress, 0), true); // a press that didn't end in a click
document.addEventListener('pointercancel', releasePress, true);
function setHTML(el, html) {
  if (el._html === html) return false;
  if (pressedIn && el.contains(pressedIn)) { deferred.set(el, html); return false; }
  el.innerHTML = html; el._html = html; return true;
}
function invalidateRenders() { for (const id of ['side', 'ph', 'statusbar', 'tabs', 'view', 'detail']) { const el = document.getElementById(id); if (el) el._html = null; } }
for (const type of ['click', 'input', 'change', 'keydown']) document.addEventListener(type, invalidateRenders, true);
function render() { renderTop(); renderSide(); renderHead(); renderView(); renderDetail(); renderStatus(); labelIconButtons(); }
function labelIconButtons() {
  document.querySelectorAll('button[title]:not([aria-label])').forEach(b => b.setAttribute('aria-label', b.title.replace(/\s*\(.*\)$/, '')));
}

function tabLabel(t) { return t.sel ? t.sel : VIEWS[t.view].title; }
function renderTop() {
  $('#topbar').classList.toggle('mac', isMac && inApp);
  $('#tbSidebar').innerHTML = icon.sidebar; $('#tbActivity').innerHTML = icon.clock;
  $('#tbBack').innerHTML = icon.back; $('#tbFwd').innerHTML = icon.fwd; $('#tbNewTab').innerHTML = icon.plus;
  $('#tbBack').disabled = !cur().back.length; $('#tbFwd').disabled = !cur().fwd.length;
  setHTML($('#tabs'), S.tabs.map((t, i) => `<div class="tab ${i === S.tab ? 'on' : ''}" data-tab="${i}" title="${esc(tabLabel(t))}">
    ${t.sel ? icon.site : icon[VIEWS[t.view].icon]}<span>${esc(tabLabel(t))}</span><button class="x" data-closetab="${i}" title="Close tab (${MOD}W)">${icon.x}</button></div>`).join(''));
}

// Sidebar: sections, items with nested trees (folders → groups, databases), favorites, running
function renderSide() {
  const body = $('#body');
  body.classList.toggle('collapsed', S.sideHidden); body.classList.toggle('peek', S.sidePeek);
  const on = S.sites.filter(s => s.enabled), t = cur(), roots = S.status?.roots || [];
  const pfKey = JSON.stringify(S.pf), plain = !Object.keys(S.pf).length;
  const link = (view, count = '') => `<a data-view="${view}" class="${t.view === view && !t.sel && (view !== 'projects' || plain) ? 'on' : ''}">${icon[VIEWS[view].icon]}<span class="lbl">${VIEWS[view].title}</span><span class="count">${count}</span></a>`;
  const sect = (id, title, body) => `<div class="sect ${S.closed[id] ? 'closed' : ''}" data-sect="${id}">${title}${icon.tri}</div><div class="sect-body ${S.closed[id] ? 'closed' : ''}"><div class="sb-in nav">${body}</div></div>`;
  // An item with children: the chevron opens/closes the tree, the label navigates
  const node = (key, a, kids) => {
    if (!kids) return a;
    const open = S.expanded.has(key);
    return `<div class="node ${open ? 'open' : ''}" data-node="${esc(key)}">${a.replace(/<\/a>$/, `<button class="twist" data-twist="${esc(key)}" title="Show or hide">${icon.chevronR}</button></a>`)}<div class="kids"><div class="kids-in">${kids}</div></div></div>`;
  };
  const scoped = (k, v) => t.view === 'projects' && !t.sel && pfKey === JSON.stringify({ [k]: [v] });
  const rootKids = roots.map(r => {
    const list = S.sites.filter(s => s.root === r.label);
    const groups = uniq(list.map(s => s.group).filter(Boolean)).sort((a, b) => a.localeCompare(b));
    const a = `<a data-scope="root" data-val="${esc(r.label)}" class="${scoped('root', r.label) ? 'on' : ''}" title="${esc(r.path)}">${icon.folder}<span class="lbl">${esc(r.label)}</span><span class="count">${list.length}</span></a>`;
    const kids = groups.map(g => { const v = r.label + '/' + g;
      return `<a data-scope="group" data-val="${esc(v)}" class="leaf ${scoped('group', v) ? 'on' : ''}"><span class="lbl">${esc(g)}</span><span class="count">${list.filter(s => s.group === g).length}</span></a>`; }).join('');
    return node('root:' + r.label, a, kids);
  }).join('');
  const mamp = (S.status?.import_sources || []).find(s => s.id === 'mamp-pro' && s.found);
  const dbKids = (S.dbs === null ? '<a class="leaf note" style="cursor:default">Loading…</a>'
    : S.dbErr ? '<a class="leaf note" style="cursor:default">MySQL is not running</a>'
    : S.dbs.map(d => `<a data-dbopen="${esc(d.name)}" class="leaf ${t.view === 'data' && S.data.conn === 'localsites' && S.data.schema === d.name ? 'on' : ''}" title="Open ${esc(d.name)} in the data browser">${icon.table}<span class="lbl">${esc(d.name)}</span><span class="count">${d.tables}</span></a>`).join('') || '<a class="leaf note" style="cursor:default">No databases yet</a>')
    + (mamp ? `<a data-dataconn="mamp-pro" class="leaf" title="Browse MAMP PRO databases (read-only)">${icon.db}<span class="lbl">MAMP PRO MySQL</span></a>` : '');
  const favs = S.sites.filter(s => S.favs.has(s.folder));
  const fails = S.doctor ? S.doctor.filter(c => c.status === 'fail').length : 0;
  const projLink = (s) => {
    const open = S.expanded.has(s.folder) && s.enabled;
    const a = `<a data-proj="${esc(s.folder)}" class="${t.view === 'projects' && t.sel === s.folder ? 'on' : ''}"><span class="dot ${s.enabled ? 'on' : 'off'}"></span><span class="lbl">${esc(s.name)}</span></a>`;
    if (!s.enabled) return a;
    const kids = `<a data-proj="${esc(s.folder)}" class="leaf"><span class="lbl">Details</span></a><a data-projgit="${esc(s.folder)}" class="leaf"><span class="lbl">Git</span></a><a data-projlog="${esc(s.folder)}" class="leaf"><span class="lbl">Logs</span></a><a data-projcheck="${esc(s.folder)}" class="leaf"><span class="lbl">Health check</span></a><a data-openurl="${esc(s.folder)}" class="leaf"><span class="lbl">Open site</span>${icon.external}</a>`;
    return `<div class="node ${open ? 'open' : ''}" data-node="${esc(s.folder)}">${a.replace(/<\/a>$/, `<button class="twist" data-twist="${esc(s.folder)}" title="Shortcuts">${icon.chevronR}</button></a>`)}<div class="kids"><div class="kids-in">${kids}</div></div></div>`;
  };
  setHTML($('#side'), `<div class="side-in">
    <div class="switcher"><button class="ws" id="wsBtn"><span class="logo">${LOGO}</span><span class="lbl">LocalSites</span>${icon.caret}</button><span class="spacer"></span>
      <button class="tb-btn" data-cmd="palette" title="Search (${MOD}K)">${icon.search}</button>
      <button class="tb-btn compose" data-cmd="newproject" title="New project">${icon.compose}</button></div>
    <div class="side-scroll">
      <nav class="nav">${link('overview')}${link('agent')}</nav>
      ${sect('workspace', 'Workspace', node('nav:projects', link('projects', on.length + '/' + S.sites.length), rootKids) + node('nav:databases', link('databases', S.dbs ? S.dbs.length : ''), dbKids) + link('data') + link('import'))}
      ${sect('system', 'System', node('nav:services', link('services'), (S.status?.addons || []).filter(a => a.enabled).map(a =>
        `<a data-view="services" class="leaf"><span class="dot ${a.running ? 'on' : 'off'}"></span><span class="lbl">${esc(a.name)}</span></a>`).join('')) + link('logs') + link('doctor', fails || '') + link('settings'))}
      ${sect('favorites', 'Favorites', favs.length ? favs.map(projLink).join('') : '<a class="note" style="cursor:default">Star a project to pin it here</a>')}
      ${sect('running', 'Running', on.length ? on.map(projLink).join('') : '<a class="note" style="cursor:default">No projects running</a>')}
    </div>
    <div class="side-foot"><button class="help" data-cmd="helpmenu" title="${updPending() ? 'Help (an update is available)' : "Help and what's new"}">?${(S.version && S.version.version !== S.seenVersion) || updPending() ? '<span class="unread"></span>' : ''}</button></div></div>`);
}
// Open or close a tree / section in place so the height animates (no re-render)
function toggleNode(key) {
  const open = !S.expanded.has(key);
  open ? S.expanded.add(key) : S.expanded.delete(key); saveUI();
  document.querySelectorAll(`#side .node[data-node="${CSS.escape(key)}"]`).forEach(n => n.classList.toggle('open', open));
  if (open && key === 'nav:databases' && S.dbs === null) loadDBs().then(renderSide);
}
function toggleSect(id) {
  S.closed[id] = !S.closed[id]; saveUI();
  const h = document.querySelector(`#side .sect[data-sect="${CSS.escape(id)}"]`);
  h?.classList.toggle('closed', S.closed[id]); h?.nextElementSibling?.classList.toggle('closed', S.closed[id]);
}

function renderHead() {
  const t = cur(), v = VIEWS[t.view];
  let sub = '', right = '';
  if (t.view === 'projects') {
    sub = `${S.sites.filter(s => s.enabled).length} running · ${S.sites.length} total`;
    right = `<label class="search"><input class="input filter" id="filter" placeholder="Search" value="${esc(S.filter)}" spellcheck="false" aria-label="Search projects">${S.filter ? '' : '<kbd>/</kbd>'}</label>
      <button class="btn" data-cmd="newproject" title="New project">${icon.plus}<span class="lbl">New project</span></button>`;
  } else if (t.view === 'databases') {
    sub = S.settings ? `LocalSites MySQL 8.4 · 127.0.0.1:${S.settings.mysql_port}` : 'LocalSites MySQL 8.4';
    right = `<button class="btn" data-cmd="adminer" title="Adminer: manage databases in the browser">${icon.globe}<span class="lbl">Adminer</span></button><button class="btn" data-cmd="mysqlconnect" title="Connection settings">${icon.plugZap}<span class="lbl">Connect</span></button><button class="btn primary" data-cmd="newdb" title="New database">${icon.plus}<span class="lbl">New database</span></button>`;
  } else if (t.view === 'services') {
    right = `<button class="btn" data-cmd="restart" title="Restart all">${icon.refresh}<span class="lbl">Restart all</span></button><button class="btn danger" data-cmd="stop" title="Stop all">${icon.stop}<span class="lbl">Stop all</span></button>`;
  } else if (t.view === 'logs') {
    const opts = [['apache', '', 'Apache'], ['mysql', '', 'MySQL'], ['setup', '', 'Setup (Homebrew)']];
    for (const a of (S.status?.addons || []).filter(a => a.installed || a.installing)) opts.push(['addon', a.id, a.name], ['addon-install', a.id, `${a.name} · install`]);
    if (!opts.some(([k, f]) => k === S.log.kind && f === S.log.folder) && S.log.kind.startsWith('addon')) opts.push([S.log.kind, S.log.folder, S.log.folder]);
    for (const s of S.sites.filter(s => s.enabled)) opts.push(['site-error', s.folder, `${s.folder} · errors`], ['site-php', s.folder, `${s.folder} · PHP`], ['site-access', s.folder, `${s.folder} · visits`]);
    right = `<select class="input" id="logSrc" aria-label="Log">${opts.map(([k, f, l]) => `<option value="${k}|${esc(f)}" ${S.log.kind === k && S.log.folder === f ? 'selected' : ''}>${esc(l)}</option>`).join('')}</select>
      <button class="btn icon-only" data-cmd="reloadlog" title="Reload">${icon.refresh}</button>`;
  } else if (t.view === 'doctor') {
    const fails = (S.doctor || []).filter(c => c.status === 'fail').length;
    sub = S.doctor ? (fails ? plural(fails, 'problem') : 'All good') : '';
    right = `<button class="btn" data-cmd="bundle" title="Debug bundle">${icon.bug}<span class="lbl">Debug bundle</span></button><button class="btn primary" data-cmd="doctor" ${S.doctorBusy ? 'disabled' : ''}>${icon.doctor}${S.doctorBusy ? 'Checking…' : 'Run checks'}</button>`;
  }
  const crumb = t.view === 'projects' && Object.keys(S.pf).length === 1 && (S.pf.root || S.pf.group)?.length === 1 ? (S.pf.root || S.pf.group)[0] : '';
  setHTML($('#ph'), `<button class="title" data-cmd="palette" title="Go to… (${MOD}K)">${esc(v.title)}${icon.caret}</button>${crumb ? `<span class="crumb">${icon.chevronR}${esc(crumb)}</span>` : ''}<span class="sub">${esc(sub)}</span><span class="spacer"></span>${right}`);
}

function renderStatus() {
  const st = S.status;
  const dot = (on, label) => `<span class="item" data-view="services"><span class="dot ${on ? 'on' : 'off'}"></span>${esc(label)}</span>`;
  const php = st ? st.php.filter(p => p.running).map(p => p.version).join(', ') : '';
  const left = st ? dot(st.front_door_running, 'Domains') + dot(st.apache.running, 'Apache') + dot(st.mysql.running, 'MySQL') + dot(!!php, 'PHP ' + php)
    : '<span class="item"><span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>Connecting…</span>';
  const agentCalls = S.activity.filter(a => a.client === 'mcp').length;
  const shares = st?.shares || [];
  const sharing = shares.length ? `<button class="item sharing" data-proj="${esc(shares[0].folder)}" title="${esc(shares.map(x => `${x.folder}: ${x.url}`).join('\n'))}"><span class="dot on"></span>Sharing ${shares.length > 1 ? shares.length + ' sites' : esc(shares[0].folder)}</button>` : '';
  setHTML($('#statusbar'), `${left}${sharing}<span class="spacer"></span>
    ${(S.grants || []).length ? `<button class="item pass-item" data-cmd="passmenu" title="Agent actions allowed without asking">${icon.ticket}${S.grants.length === 1 ? '1 pass' : S.grants.length + ' passes'} · <span data-remain="${esc(S.grants[0].until)}">${fmtLeft(new Date(S.grants[0].until) - Date.now())}</span></button>` : ''}
    <button class="item" data-view="agent">${icon.agent}Agent${agentCalls ? ` · ${agentCalls}` : ''}</button>
    ${bellButton()}
    <button class="item" data-cmd="activitymenu" title="Recent activity">${icon.clock}</button>`);
}

function renderView() {
  const fn = { overview: viewOverview, projects: viewProjects, databases: viewDatabases, data: viewData, agent: viewAgent, services: viewServices,
    logs: viewLogs, doctor: viewDoctor, import: viewImport, settings: viewSettings }[cur().view];
  const el = $('#view'), scroll = el.scrollTop, focusId = document.activeElement?.id;
  if (!setHTML(el, fn())) return;
  el.scrollTop = scroll;
  if (focusId === 'agentInput') { const a = $('#agentInput'); if (a) { a.focus(); a.setSelectionRange(a.value.length, a.value.length); } }
}

/* --- overview --- */
function ago(t) {
  const sec = Math.max(1, Math.round((Date.now() - new Date(t)) / 1000));
  if (sec < 60) return 'just now';
  const m = Math.round(sec / 60); if (m < 60) return m + 'm ago';
  const h = Math.round(m / 60); if (h < 24) return h + 'h ago';
  return Math.round(h / 24) + 'd ago';
}
// Activity in plain words: "Switched on primec", "Changed shop to PHP 8.3"
function describe(a) {
  let p = {}; try { p = JSON.parse(a.summary || '{}'); } catch (_) { /* summary may be empty */ }
  const name = p.folder ? (site(p.folder)?.name || p.folder) : '';
  switch (a.method) {
    case 'sites.update':
      if (p.enabled === true) return `Switched on ${name}`;
      if (p.enabled === false) return `Switched off ${name}`;
      if (p.php) return `Changed ${name} to PHP ${p.php}`;
      if (p.kind !== undefined) return `Set ${name} type to ${TYPES[p.kind]?.label || 'detected'}`;
      return `Changed ${name}`;
    case 'sites.create': return `Created project ${name}`;
    case 'sites.install': return `Installed dependencies for ${name}`;
    case 'sites.connect_db': return `Connected ${name} to ${p.name}`;
    case 'sites.restore_env': return `Restored .env for ${name}`;
    case 'settings.update': return 'Changed settings';
    case 'db.create': return `Created database ${p.name}`;
    case 'db.backup': return `Backed up ${p.name}`;
    case 'import.database': return `Copied database ${p.name}`;
    case 'restart': return 'Restarted services';
    case 'stop': return 'Stopped services';
    case 'data.query': return 'Ran a query';
    case 'data.readonly': return 'Changed a connection’s write access';
    case 'tools.adminer': return p.enabled === false ? 'Switched off Adminer' : 'Switched on Adminer';
    case 'sites.takeover': return `Took over ${name} from Valet`;
    case 'db.drop': case 'pg.drop': return `Deleted database ${p.name || ''}`.trim();
    case 'db.empty': case 'pg.empty': return `Emptied database ${p.name || ''}`.trim();
    case 'db.import': case 'pg.import': case 'data.import': return 'Imported data';
    case 'db.export': case 'pg.export': case 'data.export': return 'Exported data';
    case 'db.restore': return 'Restored a backup';
    case 'data.save': return 'Saved table changes';
    case 'share.start': return `Shared ${name || 'a site'}`;
    case 'share.stop': return 'Stopped sharing';
    case 'addons.install': return 'Installed an add-on';
    case 'git.pull': return `Pulled ${name}`;
    case 'git.checkout': return `Switched branch in ${name}`;
    case 'update.install': return 'Installed an update';
    case 'agents.connect': return 'Connected an AI agent';
    case 'agents.disconnect': return 'Disconnected an AI agent';
    case 'storage.clean': return 'Cleaned up storage';
    case 'problems.send': return 'Sent a problem report';
  }
  return a.method;
}
// Back-to-back identical entries (a double click, a retry) show once, with a count.
function collapseActivity(items) {
  const out = [];
  for (const a of items) {
    const prev = out[out.length - 1];
    if (prev && prev.client === a.client && prev.method === a.method && prev.summary === a.summary && !prev.error === !a.error) prev.n++;
    else out.push({ ...a, n: 1 });
  }
  return out;
}
const CLIENTS = { app: ['A', 'App'], cli: ['>', 'CLI'], 'cli-script': ['>', 'Script'], mcp: ['✦', 'AI agent'], devui: ['D', 'Dev'], unknown: ['?', 'Unknown'] };
function activityRows(items, empty) {
  if (!items.length) return `<div class="ov-empty">${esc(empty)}</div>`;
  return collapseActivity(items).slice(0, 10).map(a => { const [ch, who] = CLIENTS[a.client] || CLIENTS.unknown;
    return `<div class="ov-row"><span class="avatar ${a.client}" title="${who}">${ch}</span><span class="ov-title">${esc(describe(a))}${a.n > 1 ? ` ×${a.n}` : ''}</span>
      <span class="note">${who}</span>${a.error ? '<span class="chip warn">failed</span>' : ''}<span class="spacer"></span><span class="note" title="${esc(new Date(a.time).toLocaleString())}">${ago(a.time)}</span></div>`; }).join('');
}
// Setup: what LocalSites needs from Homebrew and isn't there or is broken, fixed with one click.
function setupCard() {
  const su = S.setup; if (!su) return '';
  const res = su.results || {}, failed = Object.entries(res).filter(([k, v]) => v !== 'ok' && v !== 'opened');
  if (su.running) { const at = S.setupRun?.at || Date.now();
    return `<section class="ov-sec setup"><div class="setup-h"><span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span><div><h2>Getting LocalSites ready</h2>
    <p class="note">${esc(su.step || 'Working…')}</p></div><span class="spacer"></span><span class="pl-total note" title="Time so far"><span data-elapsed="${at}">${fmtClock(Date.now() - at)}</span></span>${UI.btn('Log', { ic: 'logs', kind: 'ghost', attrs: 'data-cmd="setuplog"' })}</div>
    ${setupPipeline(su)}
    <p class="note">This can take a few minutes. You can keep using LocalSites; projects restart by themselves when it’s done.</p></section>`; }
  if (!su.homebrew) return `<section class="ov-sec setup"><div class="setup-h"><span class="st-ic">${icon.download2}</span><div><h2>One step before your projects run</h2>
    <p class="note">${isMac ? 'LocalSites runs Apache, MySQL and PHP from Homebrew, the free installer most Mac developers use. LocalSites downloads Homebrew’s official installer, checks Apple has verified it, and opens it: click Continue and enter your Mac password.'
      : 'LocalSites runs Apache, MySQL and PHP from Homebrew, a free package manager that keeps them separate from your system’s own packages. LocalSites installs it for you: enter your password once, then it takes a few minutes.'}</p></div></div>
    ${res.homebrew === 'opened' ? '<p class="note">Finish the Homebrew installer, then come back here: the next step appears by itself.</p>' : res.homebrew && res.homebrew !== 'ok' ? `<p class="note warn">${esc(res.homebrew)}</p>` : ''}
    <div class="form-row">${UI.btn(res.homebrew === 'opened' ? 'Open the installer again' : 'Install Homebrew', { ic: 'download2', kind: 'primary', attrs: 'data-cmd="setuphomebrew"' })}</div>
    ${su.items.filter(i => i.kind === 'system').map(i => `<div class="ov-row"><span class="chip">Set up</span><span class="ov-title">${esc(i.title)}</span><span class="note ellipsis" title="${esc(i.why)}">${esc(i.why)}</span>
      <button class="btn sm" data-setupfix="${esc(i.id)}">${icon.wrench}Set up</button></div>`).join('')}</section>`;
  if (!su.items.length) return failed.length ? `<section class="ov-sec setup"><div class="setup-h"><span class="bad">${icon.alertCircle}</span><div><h2>Some fixes didn’t finish</h2>
    ${failed.map(([k, v]) => `<p class="note">${esc(k)}: ${esc(v)}</p>`).join('')}</div><span class="spacer"></span>${UI.btn('Log', { ic: 'logs', kind: 'ghost', attrs: 'data-cmd="setuplog"' })}</div>
    <div class="form-row">${failed.map(([k]) => `<button class="btn sm" data-setupfix="${esc(k)}">${icon.rotateCcw}Retry ${esc(k)}</button>`).join('')}</div></section>` : '';
  const need = su.items.filter(i => i.required).length, pw = su.items.some(i => i.password);
  const title = need ? `${need === 1 ? 'One thing needs' : `${need} things need`} fixing before every project can run` : 'LocalSites can fix these for you';
  return `<section class="ov-sec setup"><div class="setup-h"><span class="${need ? 'bad' : 'note'}">${icon.alertCircle}</span><div><h2>${esc(title)}</h2>
    <p class="note">${su.items.every(i => i.kind === 'system') ? 'One click sets this up' : 'One click installs or repairs everything below with Homebrew'}${pw ? (isMac ? '. macOS asks for your Mac password once' : '. It asks for your password once') : ''}.</p></div></div>
    ${su.items.map(i => `<div class="ov-row su-row"><span class="chip">${{ repair: 'Repair', system: 'Set up' }[i.kind] || 'Install'}</span><span class="su-t"><span class="ov-title">${esc(i.title)}</span><span class="note">${esc(i.why)}</span></span>
      ${res[i.id] && res[i.id] !== 'ok' ? `<span class="bad note" title="${esc(res[i.id])}">failed last time</span><button class="btn sm" data-setupfix="${esc(i.id)}">${icon.rotateCcw}Retry</button>` : ''}</div>`).join('')}
    <details class="setup-cmds"><summary class="note">Commands it runs</summary><pre class="mini">${esc(su.items.flatMap(i => i.steps).join('\n'))}</pre></details>
    <div class="form-row">${UI.btn(need ? 'Fix everything' : 'Fix these', { ic: 'wrench', kind: 'primary', attrs: 'data-cmd="setuprun"' })}${failed.length ? UI.btn('Log', { ic: 'logs', kind: 'ghost', attrs: 'data-cmd="setuplog"' }) : ''}</div></section>`;
}
/* --- Launch List: what's left to set up, with a ring that fills as it gets done --- */
function launchSteps() {
  const su = S.setup, b = (label, attrs) => `<button class="btn sm" ${attrs}>${esc(label)}</button>`;
  return [
    ['Your sites', [
      { title: 'Apache, MySQL and PHP installed', text: 'What serves your projects, from Homebrew.', done: !!(su && su.homebrew && !su.items.some(i => i.required)),
        action: su && !su.homebrew ? b('Install Homebrew', 'data-cmd="setuphomebrew"') : b('Fix', 'data-cmd="setuprun"') },
      { title: 'Your projects folder added', text: 'Every folder inside becomes a project.', done: S.sites.length > 0, action: b('Add a folder', 'data-cmd="addfolder"') },
      { title: 'A project switched on', text: 'It gets its own https://name.test address.', done: S.sites.some(s => s.enabled), action: b('Choose one', 'data-view="projects"') }]],
    ['Extras', [
      { title: 'A database of your own', text: 'Each one gets its own login.', done: !!(S.dbs || []).length || S.sites.some(s => s.database), action: b('New database', 'data-cmd="newdb"') },
      { title: 'An AI agent connected', text: 'Claude, Cursor and others use LocalSites over MCP.', done: (S.agents || []).some(a => a.connected), action: b('Connect', 'data-cmd="agentsetup"') }]],
  ];
}
function ring(done, total) {
  const r = 9, c = 2 * Math.PI * r, f = total ? done / total : 0;
  return `<svg class="ring-p" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="${r}" class="rp-bg"/><circle cx="12" cy="12" r="${r}" class="rp-fg" stroke-dasharray="${(c * f).toFixed(2)} ${c.toFixed(2)}" transform="rotate(-90 12 12)"/></svg>`;
}
function launchList() {
  // wait until every step can be judged (setup and agents load after the page), so a finished setup never flashes
  if (store.get('hide.launch', false) || !S.status || !S.setup || !Array.isArray(S.agents)) return '';
  const groups = launchSteps(), all = groups.flatMap(([, l]) => l), done = all.filter(x => x.done).length;
  if (done === all.length) return '';
  return `<section class="launch" aria-label="Set up LocalSites"><div class="launch-h">${ring(done, all.length)}<div><h2>Set up LocalSites</h2><p class="note">${done} of ${all.length} done</p></div>
      <span class="spacer"></span><button class="icon-btn" data-launchhide="1" title="Hide this list">${icon.x}</button></div>
    ${groups.map(([g, list]) => `<div class="launch-g"><div class="label2">${esc(g)}</div>${list.map(x => `<div class="launch-i ${x.done ? 'done' : ''}">
      <span class="li-c">${x.done ? icon.check : ''}</span><span class="li-t"><span class="li-title">${esc(x.title)}</span><span class="note">${esc(x.text)}</span></span>${x.done ? '' : x.action}</div>`).join('')}</div>`).join('')}</section>`;
}

/* --- Deploy Pipeline: a running fix, one stage per thing it installs, with timers --- */
// The list of stages is kept from when the fix started: finished items drop out of setup.status.
function trackSetup(prev, su) {
  if (!su?.running) { S.setupRun = null; return; }
  const R = S.setupRun ||= { at: Date.now(), items: [], started: {}, ended: {} };
  for (const i of [...(prev?.items || []), ...su.items]) if (!R.items.some(x => x.id === i.id)) R.items.push({ id: i.id, title: i.title });
  const now = Date.now(), res = su.results || {};
  for (const i of R.items) {
    if ((su.step || '').startsWith(i.title + ':') && !R.started[i.id]) R.started[i.id] = now;
    if (res[i.id] && !R.ended[i.id]) { R.ended[i.id] = now; R.started[i.id] ||= now; }
  }
  if (su.step === 'Starting projects' && !R.started.apply) R.started.apply = now;
}
function setupPipeline(su) {
  const R = S.setupRun || { at: Date.now(), items: su.items.map(i => ({ id: i.id, title: i.title })), started: {}, ended: {} };
  const res = su.results || {}, step = su.step || '';
  const took = (id) => R.started[id] && R.ended[id] ? fmtClock(R.ended[id] - R.started[id]) : '';
  return pipelineHTML([...R.items.map(i => {
    const r = res[i.id], running = step.startsWith(i.title + ':');
    return { title: i.title, state: r === 'ok' ? 'done' : r ? 'fail' : running ? 'run' : 'wait', since: R.started[i.id], took: r === 'ok' ? took(i.id) : '',
      sub: running ? step.slice(i.title.length + 1).trim() : r && r !== 'ok' ? r : '' };
  }), { title: 'Start projects again', state: step === 'Starting projects' ? 'run' : 'wait', since: R.started.apply }]);
}

/* --- Uptime Bars: was each switched-on project served, hour by hour, for the last day --- */
async function loadUptime() {
  try { S.uptime = await bridge.call('uptime'); } catch (_) { S.uptime = null; return; } // an older service
  if (cur().view === 'overview') renderView();
}
function uptimeRow(s, hours) {
  const now = new Date(Math.floor(Date.now() / 3600000) * 3600000); // the service's hours start on the UTC hour (half-hour time zones too)
  let up = 0, down = 0;
  const bars = Array.from({ length: 24 }, (_, i) => {
    const t = new Date(now.getTime() - (23 - i) * 3600000), h = hours.find(x => new Date(x.hour).getTime() === t.getTime());
    const at = t.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
    if (!h) return `<i class="ub none" title="${at}: switched off or no data"></i>`;
    up += h.up; down += h.down;
    const cls = !h.down ? 'ok' : !h.up ? 'down' : 'part';
    return `<i class="ub ${cls}" title="${at}: ${h.down ? `down ${h.down} min, served ${h.up} min` : `served the whole time it was on (${h.up} min)`}"></i>`;
  }).join('');
  const pct = up + down ? (100 * up / (up + down)) : 100;
  return { down, html: `<div class="ov-row up-row link" data-proj="${esc(s.folder)}"><span class="ov-title">${esc(s.name)}</span><span class="spacer"></span><span class="ubars" aria-label="Last 24 hours">${bars}</span>
    <span class="up-pct ${down ? 'warnc' : ''}">${pct >= 99.95 ? '100' : pct.toFixed(1)}%</span></div>` };
}
function uptimeSection(running) {
  if (!S.uptime) return '';
  const rows = running.map(s => ({ s, hours: S.uptime[s.folder] || [] })).filter(x => x.hours.length).map(x => ({ ...uptimeRow(x.s, x.hours), s: x.s }));
  if (!rows.length) return '';
  const hit = rows.filter(r => r.down);
  const sum = hit.length ? `${hit.map(r => `${esc(r.s.name)} was down ${r.down} min`).join(', ')} in the last day.` : 'Every switched-on project answered the whole time.';
  return ovSection('Uptime', '', `<p class="note ov-why">${sum} Checked once a minute; switched-off time doesn’t count.</p>${rows.map(r => r.html).join('')}`);
}

function ovSection(title, count, body, link) {
  return `<section class="ov-sec"><div class="ov-sec-h"><h2>${esc(title)}</h2>${count !== '' ? `<span class="n">${count}</span>` : ''}<span class="spacer"></span>${link || ''}</div>${body}</section>`;
}
// Things outside a project that need the person. Together with the items above them in "Needs attention"
// they make up the number on the Dock icon (the service counts the same things for the badge).
function needsYou() {
  const out = [], pr = probNew(), u = S.upd;
  for (const a of S.approvals || []) out.push({ ic: a.destructive ? 'shieldAlert' : 'agent', title: describe({ method: a.method, summary: a.summary }), text: `${whoAsks(a)} is waiting · ${toolName(a.method)}`, action: `<span class="ov-acts">${approvalActions(a, true)}</span>` });
  if (pr) { const last = (S.prob?.problems || []).find(p => !p.sent && p.kind !== 'ui');
    out.push({ ic: 'alertCircle', title: pr === 1 ? 'A problem to look at' : `${pr} problems to look at`, text: last?.title || 'Something went wrong, such as a crash or a failed install.', action: `<button class="btn sm" data-cmd="openproblems">Review</button>` }); }
  if (u?.latest && !u.skipped && ['available', 'ready'].includes(u.phase))
    out.push({ ic: 'sparkles', title: u.phase === 'ready' ? `LocalSites ${u.latest.version} is ready to install` : `LocalSites ${u.latest.version} is available`, text: u.latest.title || 'A new version of LocalSites.', action: `<button class="btn sm" data-cmd="whatsnew">See update</button>` });
  return out;
}
// One sentence that says what the Dock number is made of, e.g. "The 3 on the Dock icon: 2 failed Doctor
// checks and 1 problem to look at."
function badgeWhy(total, c) {
  if (!isMac) return ''; // only macOS shows a number on the app icon
  const u = S.upd, parts = [
    [c.access, 'folder waiting for macOS access', 'folders waiting for macOS access'],
    [c.checks, 'failed Doctor check', 'failed Doctor checks'],
    [c.sites, 'running project with a problem', 'running projects with a problem'],
    [(S.approvals || []).length, 'agent request', 'agent requests'],
    [probNew(), 'problem to look at', 'problems to look at'],
    [u?.latest && !u.skipped && ['available', 'ready'].includes(u.phase) ? 1 : 0, 'update', 'updates'],
  ].filter(([n]) => n).map(([n, one, many]) => `${n} ${n === 1 ? one : many}`);
  const list = parts.length > 1 ? parts.slice(0, -1).join(', ') + ' and ' + parts.at(-1) : parts[0] || '';
  return `The ${total} on the LocalSites icon in the Dock: ${list}. It goes down as you deal with each one.`;
}
function viewOverview() {
  const st = S.status, d = S.doctor;
  const running = S.sites.filter(s => s.enabled);
  const valetCount = S.sites.filter(s => s.served_by === 'valet').length;
  const fails = d ? d.filter(c => c.status === 'fail') : [];
  const attention = [
    ...(st?.waiting_for_access || []).map(p => ({ ic: 'lock', title: 'Waiting for folder access', text: `macOS is asking whether LocalSites may read ${p}. Click Allow in the macOS prompt, or turn LocalSites on in System Settings → Privacy & Security → Files and Folders.`, action: '' })),
    ...fails.filter(c => c.name !== 'Folder access').map(c => ({ ic: 'alertCircle', title: c.name, text: c.detail, action: `<button class="btn sm" data-view="doctor">Details</button>` })),
    ...S.sites.filter(s => s.enabled && s.problems?.length).map(s => ({ ic: 'alertCircle', title: s.name, text: s.problems[0], action: `<button class="btn sm" data-proj="${esc(s.folder)}">Open</button>` })),
    ...needsYou(),
  ];
  // the title says where things stand, not hello
  const greet = !S.sites.length ? 'No projects yet' : running.length ? `${running.length} of ${S.sites.length} ${S.sites.length === 1 ? 'project' : 'projects'} running` : 'Nothing running';
  const summary = `${valetCount ? `${valetCount} on Valet · ` : ''}${S.sites.length} projects in ${(st?.roots || []).length || 1} folder${(st?.roots || []).length === 1 ? '' : 's'}`;
  const runRows = running.map(s => `<div class="ov-row link" data-proj="${esc(s.folder)}"><span class="tbadge">${typeOf(s).icon()}</span><span class="ov-title">${esc(s.name)}</span>
      ${s.group ? `<span class="note">${esc(s.group)}</span>` : ''}<span class="spacer"></span>
      ${s.git?.branch ? branchChip(s.git.branch) : ''}<span class="chip mono">${s.runtime === 'php' ? 'PHP ' + esc(s.php) : esc(typeOf(s).label)}</span>
      <a class="note" data-openurl="${esc(s.folder)}">${esc(s.domain)} ↗</a></div>`).join('');
  const byType = {};
  for (const s of S.sites) byType[s.type] = (byType[s.type] || 0) + 1;
  const side = `<aside class="ov-side">
    <div class="ov-prop-h">Services</div>
    ${st ? [['Domains', st.front_door_running, st.front_door === 'valet' ? 'via Valet' : 'built-in'], ['Apache', st.apache.running, ''], ['MySQL 8.4', st.mysql.running, ''],
      ...st.php.filter(p => p.running).map(p => ['PHP ' + p.version, true, `${p.sites} project${p.sites === 1 ? '' : 's'}`])].map(([n, ok, x]) =>
      `<div class="ov-prop" data-view="services"><span class="dot ${ok ? 'on' : 'off'}"></span><span>${esc(n)}</span><span class="spacer"></span><span class="note">${esc(x || (ok ? 'Running' : 'Stopped'))}</span></div>`).join('') : '<span class="skeleton" style="width:60%"></span>'}
    <div class="ov-prop-h">Health</div>
    <div class="ov-prop" data-view="doctor">${d ? `<span class="${fails.length ? 'bad' : 'ok'}">${fails.length ? icon.alertCircle : icon.status}</span><span>${fails.length ? `${fails.length} problem${fails.length > 1 ? 's' : ''}` : 'All good'}</span><span class="spacer"></span><span class="note">${d.length} checks</span>` : '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span><span class="note">Checking…</span>'}</div>
    <div class="ov-prop-h">Projects</div>
    ${Object.entries(byType).sort((a, b) => b[1] - a[1]).map(([k, n]) => `<div class="ov-prop" data-ptabgo="${k}"><span class="tbadge">${(TYPES[k] || TYPES.other).icon()}</span><span>${esc((TYPES[k] || TYPES.other).label)}</span><span class="spacer"></span><span class="note">${n}</span></div>`).join('')}
    <div class="ov-prop-h">Folders<span class="spacer"></span><button class="icon-btn" data-cmd="addfolder" title="Add a folder">${icon.plus}</button></div>
    ${(st?.roots || []).map(r => `<div class="ov-prop" title="${esc(r.path)}">${icon.folder}<span>${esc(r.label)}</span><span class="spacer"></span><span class="note">${S.sites.filter(s => s.root === r.label).length}</span></div>`).join('')}
  </aside>`;
  return `<div class="ov"><div class="ov-main">
    <header class="ov-head"><h1>${greet}</h1><p class="note">${esc(summary)}</p></header>
    <div class="ov-chips">${st ? [['Domains', st.front_door_running], ['Apache', st.apache.running], ['MySQL', st.mysql.running], ...st.php.filter(p => p.running).map(p => ['PHP ' + p.version, true]),
      ...(st.addons || []).filter(a => a.enabled).map(a => [a.name, a.running])].map(([n, ok]) => `<button class="pchip" data-view="services"><span class="dot ${ok ? 'on' : 'off'}"></span>${esc(n)}</button>`).join('') : ''}
      ${d ? `<button class="pchip" data-view="doctor">${fails.length ? icon.alertCircle : icon.status}${fails.length ? plural(fails.length, 'problem') : 'All good'}</button>` : ''}
      ${(st?.roots || []).map(r => `<button class="pchip" data-scope="root" data-val="${esc(r.label)}">${icon.folder}${esc(r.label)} <span class="note">${S.sites.filter(s => s.root === r.label).length}</span></button>`).join('')}</div>
    ${setupCard()}
    ${launchList()}
    ${attention.length ? ovSection('Needs attention', attention.length, `<p class="note ov-why">${esc(badgeWhy(attention.length, {
        access: (st?.waiting_for_access || []).length, checks: fails.filter(c => c.name !== 'Folder access').length,
        sites: S.sites.filter(s => s.enabled && s.problems?.length).length }))}</p>` + attention.map(a => `<div class="ov-row"><span class="bad">${icon[a.ic]}</span><span class="ov-title">${esc(a.title)}</span><span class="note ellipsis">${esc(a.text || '')}</span><span class="spacer"></span>${a.action}</div>`).join('')) : ''}
    ${ovSection('Running', running.length, running.length ? runRows : `<div class="ov-empty">Nothing is running. <a data-view="projects">Switch a project on</a> to give it a <code>.test</code> address.</div>`,
      `<button class="btn ghost sm" data-view="projects">All projects</button>`)}
    ${uptimeSection(running)}
    ${ovSection('Recent activity', '', activityRows(S.activity, 'Changes from the app, the command line and AI agents show up here.'))}
  </div>${side}</div>`;
}

/* --- projects --- */
const PTABS = [['all', 'All projects'], ['running', 'Running'], ['attention', 'Needs attention']];
function inTab(s, tab) {
  if (tab === 'running') return s.enabled || s.served_by === 'valet';
  if (tab === 'attention') return !!s.problems?.length;
  return true;
}
// Filter properties ("Type is Laravel"): values are [value, label, icon]; get(s) lists a project's values
const PF = {
  status: { label: 'Status', ic: 'circleDot',
    vals: () => [['running', 'Running', '<span class="dot on"></span>'], ['off', 'Off', '<span class="dot off"></span>'], ['valet', 'Served by Valet', '<span class="dot valet"></span>'],
      ['attention', 'Needs attention', icon.alertCircle], ['none', 'Not a website', icon.circleDashed]],
    get: s => [s.served_by === 'valet' ? 'valet' : s.runtime === 'none' ? 'none' : s.enabled ? 'running' : 'off', ...(s.problems?.length ? ['attention'] : [])] },
  type: { label: 'Type', ic: 'layers', vals: () => Object.keys(TYPES).filter(k => S.sites.some(s => s.type === k)).map(k => [k, TYPES[k].label, TYPES[k].icon()]), get: s => [s.type] },
  root: { label: 'Folder', ic: 'folder', vals: () => (S.status?.roots || []).map(r => [r.label, r.label, icon.folder]), get: s => [s.root] },
  group: { label: 'Group', ic: 'folderTree', vals: () => uniq(S.sites.filter(s => s.group).map(s => s.root + '/' + s.group)).sort().map(v => [v, v.replace('/', ' / '), icon.folderOpen]),
    get: s => s.group ? [s.root + '/' + s.group] : [] },
  account: { label: 'Git account', ic: 'user', vals: () => [...uniq(S.sites.map(s => s.git?.account).filter(Boolean)).sort().map(a => [a, a, hostIcon(S.sites.find(s => s.git?.account === a)?.git?.host)]),
    ...(S.sites.some(s => !s.git?.account) ? [['', 'No git remote', icon.circleDashed]] : [])], get: s => [s.git?.account || ''] },
  php: { label: 'PHP version', ic: 'php', vals: () => uniq(S.sites.filter(s => s.runtime === 'php' && s.php).map(s => s.php)).sort().map(v => [v, 'PHP ' + v, icon.php]),
    get: s => s.runtime === 'php' ? [s.php] : [] },
};
const activePF = () => Object.entries(S.pf).filter(([k, v]) => PF[k] && v?.length);
const matchPF = (s) => activePF().every(([k, vals]) => PF[k].get(s).some(v => vals.includes(v)));
function filteredSites() {
  const q = S.filter.trim().toLowerCase();
  return S.sites.filter(s => inTab(s, S.ptab) && matchPF(s) && (!q || [s.name, s.folder, s.domain, s.php, s.group, s.git?.account, s.git?.branch, typeOf(s).label]
    .some(v => (v || '').toLowerCase().includes(q))));
}
function groupKey(s) {
  if (S.groupBy === 'none') return '';
  if (S.groupBy === 'account') return s.git?.account ? `${s.git.account}` : 'No git remote';
  if (S.groupBy === 'type') return typeOf(s).label;
  return s.group ? `${s.root} / ${s.group}` : s.root;
}
const ORDERS = {
  status: (a, b) => ((b.enabled || !!b.served_by) - (a.enabled || !!a.served_by)) || a.name.localeCompare(b.name),
  name: (a, b) => a.name.localeCompare(b.name),
  type: (a, b) => typeOf(a).label.localeCompare(typeOf(b).label) || a.name.localeCompare(b.name),
};
// Rows in the order they are shown (used by J/K too)
function groupedSites() {
  const groups = new Map();
  for (const s of filteredSites()) { const k = groupKey(s); if (!groups.has(k)) groups.set(k, []); groups.get(k).push(s); }
  for (const list of groups.values()) list.sort(ORDERS[S.order] || ORDERS.status);
  return groups;
}
function visibleSites() { const out = []; for (const [k, l] of groupedSites()) if (!S.closed['g:' + k]) out.push(...l); return out; }
function filterChips() {
  const list = activePF();
  if (!list.length) return '';
  return `<div class="fchips">${list.map(([k, vals]) => {
    const p = PF[k], all = p.vals(), names = vals.map(v => (all.find(x => x[0] === v) || [v, v || 'none'])[1]);
    return `<span class="fchip"><button class="fc-k" data-fopen="${k}">${icon[p.ic]}${esc(p.label)}</button><span class="fc-op">${vals.length > 1 ? 'is any of' : 'is'}</span>
      <button class="fc-v" data-fopen="${k}" title="${esc(names.join(', '))}">${esc(names.length > 2 ? names.length + ' values' : names.join(', '))}</button><button class="fc-x" data-fclear="${k}" title="Remove filter">${icon.x}</button></span>`;
  }).join('')}<button class="btn ghost sm icon-only" data-cmd="filtermenu" title="Add filter">${icon.plus}</button><span class="spacer"></span><button class="btn ghost sm" data-cmd="clearpf">Clear</button></div>`;
}
// Filter menu: properties first, then values with checkboxes; typing searches every value
const FM = { prop: null, q: '' };
function filterMenuHTML() {
  const q = FM.q.trim().toLowerCase();
  const valBtn = (k, v, l, ic, withProp) => {
    const on = (S.pf[k] || []).includes(v), n = S.sites.filter(s => PF[k].get(s).includes(v)).length;
    return `<button data-fprop="${k}" data-fval="${esc(v)}" class="fv ${on ? 'on' : ''}" role="menuitemcheckbox" aria-checked="${on}"><span class="cbox">${on ? icon.check : ''}</span>${ic || ''}<span>${withProp ? `<span class="note">${esc(PF[k].label)}:</span> ` : ''}${esc(l)}</span><span class="kb">${n}</span></button>`;
  };
  let items = [];
  if (!FM.prop) {
    if (q) for (const [k, p] of Object.entries(PF)) for (const [v, l, ic] of p.vals()) { if (l.toLowerCase().includes(q) || p.label.toLowerCase().includes(q)) items.push(valBtn(k, v, l, ic, true)); }
    else items = Object.entries(PF).filter(([, p]) => p.vals().length).map(([k, p]) => `<button data-fprop="${k}">${icon[p.ic]}<span>${esc(p.label)}</span>${S.pf[k]?.length ? `<span class="badge">${S.pf[k].length}</span>` : ''}<span class="kb">${icon.chevronR}</span></button>`);
  } else {
    items = PF[FM.prop].vals().filter(([, l]) => !q || l.toLowerCase().includes(q)).map(([v, l, ic]) => valBtn(FM.prop, v, l, ic, false));
  }
  return `${FM.prop ? `<button class="menu-back" data-fback="1">${icon.back}<span>${esc(PF[FM.prop].label)}</span></button>` : ''}
    <input class="menu-q" id="fmQ" placeholder="${FM.prop ? 'Search ' + esc(PF[FM.prop].label.toLowerCase()) + '…' : 'Filter…'}" value="${esc(FM.q)}" spellcheck="false" autocomplete="off" aria-label="Filter">
    <div class="menu-list">${items.join('') || '<div class="menu-empty">No matches</div>'}</div>`;
}
function showFilterMenu(anchor, prop = null) {
  FM.prop = prop; FM.q = '';
  menuAt(anchor, filterMenuHTML(), 'fmenu');
  setTimeout(() => $('#fmQ')?.focus(), 0);
}
function refreshFilterMenu() {
  const m = $('#menu'), pos = $('#fmQ')?.selectionStart;
  m.innerHTML = filterMenuHTML();
  const q = $('#fmQ'); if (q) { q.focus(); if (pos !== undefined) q.setSelectionRange(pos, pos); }
}
function displayMenuHTML() {
  const sel = (id, value, opts) => `<select class="input sm" id="${id}">${opts.map(([v, l]) => `<option value="${v}" ${value === v ? 'selected' : ''}>${l}</option>`).join('')}</select>`;
  return `<div class="dm">
    <div class="dm-row"><span>${icon.rows}Grouping</span>${sel('groupBy', S.groupBy, [['folder', 'Folder'], ['account', 'Git account'], ['type', 'Type'], ['none', 'No grouping']])}</div>
    <div class="dm-row"><span>${icon.sort}Ordering</span>${sel('orderBy', S.order, [['status', 'Running first'], ['name', 'Name'], ['type', 'Type']])}</div>
    <div class="dm-h">Display properties</div>
    <div class="dm-props">${[['type', 'Type icon'], ['domain', 'Address'], ['branch', 'Branch'], ['database', 'Database'], ['account', 'Git account'], ['version', 'Version']].map(([k, l]) =>
      `<button data-dprop="${k}" class="pp ${S.props.has(k) ? 'on' : ''}" aria-pressed="${S.props.has(k)}">${S.props.has(k) ? icon.check : ''}${l}</button>`).join('')}</div></div>`;
}
function startCards(title, cards, dismiss = '') {
  if (dismiss && store.get('hide.' + dismiss, false)) return '';
  return `<div class="starts">${title ? `<div class="starts-h">${esc(title)}${dismiss ? `<button class="icon-btn" data-hidecards="${dismiss}" title="Hide examples">${icon.x}</button>` : ''}</div>` : ''}<div class="starts-grid">${cards.map(c =>
    `<button class="start" ${c.attrs}><span class="st-ic">${icon[c.ic] || c.ic}</span><span class="st-t">${esc(c.title)}</span><span class="st-d">${esc(c.text)}</span></button>`).join('')}</div></div>`;
}
/* --- No Results: what was searched, a likely spelling, and which filter is hiding things --- */
function editDistance(a, b) {
  const d = Array.from({ length: b.length + 1 }, (_, i) => i);
  for (let i = 1; i <= a.length; i++) { let prev = d[0]; d[0] = i;
    for (let j = 1; j <= b.length; j++) { const t = d[j]; d[j] = Math.min(d[j] + 1, d[j - 1] + 1, prev + (a[i - 1] === b[j - 1] ? 0 : 1)); prev = t; } }
  return d[b.length];
}
function noResults() {
  const q = S.filter.trim(), ql = q.toLowerCase();
  // close spellings of project names, from every project (any tab, any filter)
  const dist = (x) => Math.min(editDistance(ql, x.name.toLowerCase()), ...x.name.toLowerCase().split(/[-_.]/).map(w => editDistance(ql, w)));
  const near = q ? S.sites.map(x => ({ name: x.name, d: dist(x) })).filter(o => o.d > 0 && o.d <= Math.max(1, Math.floor(ql.length / 3)))
    .sort((a, b) => a.d - b.d).slice(0, 3).map(o => o.name) : [];
  // the same search in every tab, without the filters
  const hit = (x) => !ql || [x.name, x.folder, x.domain, x.php, x.group, x.git?.account, x.git?.branch, typeOf(x).label].some(v => (v || '').toLowerCase().includes(ql));
  const inAll = S.sites.filter(hit).length, filtered = activePF().length || S.ptab !== 'all';
  const hints = [];
  if (S.ptab !== 'all' && inAll) hints.push(`<button class="nr-hint" data-ptab="all">${icon.search}<span>${inAll === 1 ? '1 match' : inAll + ' matches'} in All projects</span></button>`);
  for (const [k, vals] of activePF()) {
    const p = PF[k], names = vals.map(v => (p.vals().find(x => x[0] === v) || [v, v || 'none'])[1]).join(', ');
    hints.push(`<button class="nr-hint" data-fclear="${k}">${icon.x}<span>Remove <b>${esc(p.label)} is ${esc(names)}</b></span></button>`);
  }
  return `<div class="empty-state nr"><div class="es-icon">${icon.search2}</div><h3>No matching projects</h3>
    <p>${q ? `Nothing matches <b>“${esc(q)}”</b>${filtered ? ' with these filters' : ''}.` : 'No projects match these filters.'}</p>
    ${near.length ? `<p class="nr-near">Did you mean ${near.map(n => `<button class="linkish" data-suggest="${esc(n)}">${esc(n)}</button>`).join(', ')}?</p>` : ''}
    ${hints.length ? `<div class="nr-hints">${hints.join('')}</div>` : ''}
    <div class="es-actions"><button class="btn" data-cmd="clearfilter">Clear search and filters</button></div>
    <div class="es-hint">Search looks at names, addresses, PHP versions, branches and git accounts.</div></div>`;
}
function viewProjects() {
  if (!S.status) return Array.from({ length: 6 }, (_, i) => `<div class="skel-row"><span class="skeleton" style="width:14px;height:14px;border-radius:50%"></span><span class="skeleton" style="width:${120 + (i * 37) % 90}px"></span><span class="skeleton" style="width:140px;margin-left:auto"></span></div>`).join('');
  const counts = Object.fromEntries(PTABS.map(([id]) => [id, S.sites.filter(s => inTab(s, id)).length]));
  const nf = activePF().length;
  const bar = `<div class="vtabs"><div class="vt-tabs" role="tablist">${PTABS.filter(([id]) => id === 'all' || counts[id] || S.ptab === id).map(([id, l]) =>
      `<button data-ptab="${id}" class="${S.ptab === id ? 'on' : ''}" role="tab" aria-selected="${S.ptab === id}">${l}<span>${counts[id]}</span></button>`).join('')}</div>
    <span class="spacer"></span>
    <button class="btn ghost sm ${nf ? 'active' : ''}" data-cmd="filtermenu" title="Filter (F)">${icon.listFilter}<span class="lbl">Filter</span>${nf ? `<span class="badge">${nf}</span>` : ''}</button>
    <button class="btn ghost sm" data-cmd="displaymenu" title="Display options">${icon.sliders}<span class="lbl">Display</span></button></div>${filterChips()}`;
  if (!S.sites.length) return bar + emptyState({ fig: 'rail', ic: 'projects', title: 'No projects yet', text: 'Add a folder that holds your projects, or create a new one. LocalSites detects PHP, Laravel, WordPress, Next.js and Node projects.',
    actions: `<button class="btn primary" data-cmd="newproject">${icon.plus}New project</button><button class="btn" data-cmd="addfolder">${icon.folderPlus}Add a folder</button>` })
    + startCards('Get started', [
      { ic: 'folderPlus', title: 'Add a projects folder', text: 'Every project inside shows up here.', attrs: 'data-cmd="addfolder"' },
      { ic: 'import', title: 'Move from MAMP PRO', text: 'Bring over PHP versions and databases.', attrs: 'data-view="import"' },
      { ic: 'compose', title: 'Start a new project', text: 'An empty folder with its own .test address.', attrs: 'data-cmd="newproject"' }]);
  const groups = groupedSites();
  if (!groups.size) return bar + noResults();
  let h = bar + (S.status?.waiting_for_access || []).map(p => `<div class="callout warn" style="margin:12px 20px 0">${icon.lock}<div><b>macOS hasn't let LocalSites read ${esc(p)} yet.</b> Projects there appear as soon as you click <b>Allow</b> in the macOS prompt (or turn LocalSites on in System Settings → Privacy &amp; Security → Files and Folders). Projects already running keep running.</div></div>`).join('');
  for (const [k, list] of groups) {
    const closed = S.closed['g:' + k], on = list.filter(s => s.enabled || s.served_by).length;
    if (S.groupBy !== 'none') h += `<div class="group" data-sect="g:${esc(k)}"><span class="caret ${closed ? 'closed' : ''}">${icon.tri}</span>${S.groupBy === 'account' && list[0].git ? hostIcon(list[0].git.host) : ''}<span class="gname">${esc(k)}</span><span class="n">${list.length}</span>${on ? `<span class="n">· ${on} running</span>` : ''}</div>`;
    if (!closed || S.groupBy === 'none') h += list.map(row).join('');
  }
  return h;
}
// A branch as a quiet chip: the usual base branches (main, master…) stay muted so feature branches stand out.
function branchChip(name, attrs = '') {
  const base = /^(main|master|trunk|develop|dev)$/.test(name);
  const tag = attrs ? 'button' : 'span';
  return `<${tag} class="chip mono br ${base ? 'base' : ''}" title="Git branch ${esc(name)}${attrs ? ' · open Git' : ''}" ${attrs}>${icon.branch}<span class="bn">${esc(name)}</span></${tag}>`;
}
function row(s) {
  const err = s.problems?.length, valet = s.served_by === 'valet', t = typeOf(s), P = S.props;
  const ver = s.runtime === 'php' ? `PHP ${s.php}` : s.framework?.version ? `${t.label} ${s.framework.version.split('.').slice(0, 2).join('.')}` : '';
  const chips = [];
  if (err) chips.push(`<span class="chip warn" title="${esc(s.problems.join('\n'))}">${icon.alertCircle}<span class="lbl">Needs attention</span></span>`);
  if (valet) chips.push(`<span class="chip valet" title="Served by Laravel Valet">Valet</span>`);
  if (P.has('branch') && s.git?.branch) chips.push(branchChip(s.git.branch));
  if (P.has('account') && s.git?.account && S.groupBy !== 'account') chips.push(`<span class="chip acct" title="${esc(s.git.remote || '')}">${hostIcon(s.git.host)}${esc(s.git.account)}</span>`);
  if (P.has('version') && ver) chips.push(`<span class="chip mono ver">${esc(ver)}</span>`);
  const dbn = s.database || s.env?.database || s.wp_database;
  if (P.has('database') && dbn) chips.push(`<button class="chip mono dbc" ${s.db_server === 'mamp' ? `data-dbmamp="${esc(dbn)}"` : `data-dbopen="${esc(dbn)}"`} title="Database ${esc(dbn)}${s.db_server === 'mamp' ? ' on MAMP PRO MySQL' : ''}: open it">${icon.db}<span>${esc(dbn)}</span></button>`);
  const live = s.enabled || valet;
  const ring = s.runtime === 'none' ? `<span class="ring off" title="Not a website"></span>`
    : `<span class="ring ${live ? 'on' : ''} ${err ? 'err' : ''} ${valet ? 'valet' : ''}" ${valet ? 'title="Served by Laravel Valet"' : `data-toggle="${esc(s.folder)}" title="${s.enabled ? 'Switch off' : 'Switch on'} (E)"`}></span>`;
  return `<div class="row ${cur().sel === s.folder ? 'sel' : ''}" data-row="${esc(s.folder)}">
    ${ring}${P.has('type') ? `<span class="tbadge" title="${esc(t.label)}">${t.icon()}</span>` : ''}
    <span class="name" title="${esc(s.name)}">${esc(s.name)}</span>${S.favs.has(s.folder) ? '<span class="fav">★</span>' : ''}
    ${P.has('domain') ? `<span class="meta">${live && s.url ? `<a data-openurl="${esc(s.folder)}">${esc(s.domain)}</a>` : esc(s.runtime === 'none' ? (s.group || s.root) : s.domain)}</span>` : '<span class="meta"></span>'}
    <div class="right"><div class="hover-actions">
      ${live ? `<button class="icon-btn" title="Open in browser (O)" data-openurl="${esc(s.folder)}">${icon.globe}</button>` : ''}
      <button class="icon-btn" title="Show in ${FILES_APP.name}" data-app="${FILES_APP.id}" data-folder="${esc(s.folder)}">${icon.folder}</button>
      <button class="icon-btn" title="${prefApp('editor') ? 'Open in ' + esc(prefApp('editor').name) : 'Open in your code editor'}" data-openwith="editor" data-folder="${esc(s.folder)}">${icon.code}</button>
    </div>${chips.join('')}</div></div>`;
}

/* --- detail panel --- */
const statusIcon = { ok: '<span class="ok">✓</span>', warn: '<span class="warnc">!</span>', fail: '<span class="bad">✗</span>', skip: '<span class="note">-</span>' };
// The setup item that fixes a Doctor check (Apache, MySQL or a PHP version), when there is one.
function setupFor(c) {
  if (c.status === 'ok' || !S.setup || S.setup.running) return null;
  const m = /^PHP (\d+\.\d+|latest)$/.exec(c.name);
  const domains = /^\.[a-z]+ addresses$/.test(c.name) || c.name === 'Web ports 80 and 443' || (c.area === 'Certificates' && /^LocalSites Development CA/.test(c.name));
  const id = domains ? 'domains' : { 'Apache installed': 'httpd', 'MySQL 8.4 installed': 'mysql@8.4', PHP: 'php' }[c.name] || (m && (m[1] === 'latest' ? 'php' : 'php@' + m[1]));
  return (S.setup.items || []).find(i => i.id === id) || null;
}
function checksTable(checks, withArea = true) {
  let area = '', h = '';
  for (const c of checks) {
    if (withArea && c.area !== area) { area = c.area; h += `<tr id="${areaId(area)}"><td colspan="3" class="note" style="height:32px;padding-top:12px"><b>${esc(area)}</b></td></tr>`; }
    const copyable = /^(sudo|brew|valet|localsites) /.test(c.fix || '');
    const su = setupFor(c);
    const fix = su ? `<div class="note" style="margin:2px 0 6px"><button class="btn sm primary" data-setupfix="${esc(su.id)}">${icon.wrench}${{ repair: 'Repair', system: 'Set up' }[su.kind] || 'Install'} ${esc(su.title)}</button>${su.password ? ' <span class="note">asks for your Mac password once</span>' : ''}</div>`
      : c.fix && c.status !== 'ok' ? `<div class="note" style="margin:2px 0 6px">${esc(c.fix)}${copyable ? ` <button class="btn ghost" data-copy="${esc(c.fix)}" style="height:22px">${icon.copy}Copy</button>` : ''}</div>` : '';
    h += `<tr><td style="width:30px;text-align:center">${statusIcon[c.status] || ''}</td><td style="width:38%"><span style="font-weight:500">${esc(c.name)}</span></td><td class="note">${esc(c.detail || '')}${fix}</td></tr>`;
  }
  return `<table class="t checks"><tbody>${h}</tbody></table>`;
}
let detailOpen = false, miniLogAt = {};
function renderDetail() {
  const d = $('#detail'), t = cur();
  const s = t.view === 'projects' && t.sel ? site(t.sel) : null;
  if (!!s !== detailOpen) { // animate only when the panel opens or closes
    detailOpen = !!s;
    if (s) { d.classList.add('entering'); setTimeout(() => d.classList.remove('entering'), 220); }
  }
  d.classList.toggle('open', !!s);
  $('.pm').classList.toggle('has-detail', !!s);
  if (!s) { d.innerHTML = ''; return; }
  const pane = s.git && S.dpane === 'git' ? 'git' : 'details';
  const gs = S.git[s.folder]?.st;
  const tabs = s.git ? `<div class="ptabs" role="tablist"><button data-dpane="details" class="${pane === 'details' ? 'on' : ''}" role="tab" aria-selected="${pane === 'details'}">Details</button>
    <button data-dpane="git" class="${pane === 'git' ? 'on' : ''}" role="tab" aria-selected="${pane === 'git'}">${icon.branch}Git${gs && gs.files?.length ? `<span class="badge">${gs.files.length}</span>` : ''}</button></div>` : '';
  const changed = setHTML(d, `
    <div class="dh"><button class="icon-btn back-btn" data-cmd="closedetail" title="Back to projects">${icon.back}</button><span class="tbadge">${typeOf(s).icon()}</span><h2 title="${esc(s.folder)}">${esc(s.name)}</h2>
      <button class="star ${S.favs.has(s.folder) ? 'on' : ''}" data-fav="${esc(s.folder)}" title="Favorite">${icon.star}</button><span style="flex:1"></span>
      <button class="icon-btn" data-cmd="closedetail" title="Close (Esc)">${icon.x}</button></div>${tabs}
    <div class="db">${pane === 'git' ? gitPane(s) : detailsPane(s)}</div>`);
  if (!changed && pane !== 'git' && miniLogAt[s.folder + (s.installing ? 'site-install' : s.runtime === 'app' ? 'site-app' : 'site-error')] && Date.now() - miniLogAt[s.folder + (s.installing ? 'site-install' : s.runtime === 'app' ? 'site-app' : 'site-error')].at < 4000) return;
  if (pane === 'git') { const g = S.git[s.folder]; if (!g || (!g.busy && Date.now() - g.at > 30000)) gitLoad(s.folder); return; }
  if (s.served_by !== 'valet') {
    const kind = s.installing ? 'site-install' : s.runtime === 'app' ? 'site-app' : 'site-error';
    const k = s.folder + kind, el = $('#miniLog');
    if (miniLogAt[k] && Date.now() - miniLogAt[k].at < 4000) { if (el) el.innerHTML = miniLogHTML(miniLogAt[k].text, s); return; }
    act(async () => {
      const r = await bridge.call('logs', { kind, folder: s.folder, lines: 14 });
      const text = r.text || (s.runtime === 'app' ? 'No output yet.' : 'No errors logged.');
      miniLogAt[k] = { at: Date.now(), text };
      const box = $('#miniLog'); if (box) box.innerHTML = miniLogHTML(text, s);
    });
  }
}
// Recent log lines in a project's panel: newest last, one per row with time and level, repeats folded,
// paths shortened to the project (./) or your home folder (~)
function miniLogHTML(text, s) {
  if (text === undefined) return '<p class="note ml-empty">Loading…</p>';
  const raw = String(text).split('\n').filter(l => l.trim());
  if (!raw.length || /^No (errors logged|output yet)/.test(raw[0])) return `<p class="note ml-empty">${esc(raw[0] || 'Nothing logged yet.')}</p>`;
  const short = (m) => m.split(s.path + '/').join('./').replace(/\/(Users|home)\/[^/\s]+\//g, '~/');
  let prev = null; const rows = [];
  for (const r of raw) {
    const x = parseLogLine(r, s.runtime === 'app' ? 'app' : 'apache', prev); prev = x.ts;
    const key = x.level + '|' + lsTemplate(x.message), last = rows[rows.length - 1];
    if (last && last.key === key) { last.n++; last.x = x; continue; }
    rows.push({ key, x, n: 1 });
  }
  const today = new Date().toDateString();
  return rows.slice(-8).map(({ x, n }) => { const d = x.ts ? new Date(x.ts) : null;
    const when = d ? (d.toDateString() === today ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : d.toLocaleDateString([], { day: 'numeric', month: 'short' })) : '';
    const full = short(x.message), msg = full.replace(/^AH\d{5}: /, ''); // Apache's message codes only on hover
    return `<div class="ml-row lv-${x.level}"><span class="ml-t">${esc(when)}</span><span class="ml-lv">${{ error: 'ERR', warn: 'WRN', info: 'INF', debug: 'DBG' }[x.level]}</span><span class="ml-m" title="${esc(full)}">${esc(msg)}${n > 1 ? `<span class="ml-n">×${n}</span>` : ''}</span></div>`; }).join('');
}
// The database a project uses: its own, the one named in .env, or WordPress' wp-config.php
function projectDB(s) {
  const name = s.database || s.env?.database || s.wp_database;
  const from = s.database ? 'its own database' : s.env?.database ? `from ${baseName(s.env.file)}` : s.wp_database ? 'from wp-config.php' : '';
  if (!name) return `<div class="st">Database</div><p class="note pdb-none">No database named in .env or wp-config.php. ${UI.btn('New database', { ic: 'plus', kind: 'ghost', attrs: 'data-cmd="newdb"' })}</p>`;
  const mamp = s.db_server === 'mamp';
  if (!mamp && S.dbs === null && !dbsInFlight) loadDBs().then(() => renderDetail());
  const d = !mamp ? (S.dbs || []).find(x => x.name === name) : null, missing = !mamp && S.dbs && !d;
  const facts = d ? `${plural(d.tables, 'table')} · ${d.size_mb.toFixed(1)} MB${d.owner ? ` · login <code>${esc(d.owner)}</code>` : ''}` : mamp ? 'on MAMP PRO MySQL (read-only here)' : missing ? 'not in LocalSites MySQL yet' : '';
  const open = mamp ? `data-dbmamp="${esc(name)}"` : missing ? 'disabled' : `data-dbopen="${esc(name)}"`;
  return `<div class="st">Database</div>
    <div class="pdb"><button class="pdb-main" ${open} title="${missing ? 'Copy it from MAMP PRO in Import first' : 'Open in the data browser'}"><span class="pdb-ic">${icon.db}</span>
      <span class="pdb-t"><span class="pdb-n">${esc(name)}</span><span class="note">${esc(from)}${facts ? ' · ' : ''}${facts}</span></span>${missing ? '' : icon.chevronR}</button>
      <div class="pdb-acts">${missing ? UI.btn('Copy from MAMP PRO', { ic: 'import', kind: 'ghost', attrs: 'data-view="import"' })
        : !mamp ? `${d?.owner ? UI.btn('Connect', { ic: 'plugZap', kind: 'ghost', attrs: `data-mysqlconnect="${esc(name)}"`, title: 'Settings for .env and other apps' }) : ''}${UI.btn('Backups', { ic: 'backup', kind: 'ghost', attrs: `data-dbbackups="${esc(name)}"` })}` : ''}</div></div>`;
}
function detailsPane(s) {
  const valet = s.served_by === 'valet', php = s.runtime === 'php', app = s.runtime === 'app';
  const phpOpts = (S.status?.php || []).map(p => `<option value="${p.version}" ${p.version === s.php ? 'selected' : ''}>PHP ${p.version} (${p.full})</option>`).join('');
  const detected = TYPES[s.framework?.kind]?.label || 'Not a website';
  const kindOpts = [['', `Detected: ${detected}`], ['php', 'PHP'], ['laravel', 'Laravel'], ['wordpress', 'WordPress'], ['static', 'Static HTML'], ['nextjs', 'Next.js'], ['node', 'Node (npm run dev)'], ['command', 'Custom command'], ['docker', 'Docker / any command'], ['other', 'Not a website']]
    .map(([v, l]) => `<option value="${v}" ${(s.kind || '') === v ? 'selected' : ''}>${l}</option>`).join('');
  const needsDeps = (s.problems || []).some(p => /not installed \(node_modules|Composer packages/.test(p));
  const imported = s.imported_from ? `<p class="note">Was <b>${esc(s.imported_from.name)}</b> in ${s.imported_from.source === 'mamp-pro' ? 'MAMP PRO' : esc(s.imported_from.source)}${s.imported_from.php ? ' · PHP ' + esc(s.imported_from.php) : ''}.</p>` : '';
  const envCount = Object.keys(s.env_vars || {}).length, iniCount = Object.keys(s.php_ini || {}).length;
  const narrow = $('.pm')?.classList.contains('lt-pm-880'); // chips row instead of a form when space is tight
  const sel = (field, opts, ic, dis) => `<label class="pchip ${dis ? 'disabled' : ''}">${ic}<select data-field="${field}" ${dis ? 'disabled' : ''} aria-label="${field}">${opts}</select></label>`;
  const chips = `<div class="pchips">
      ${valet || s.runtime === 'none' ? `<span class="pchip disabled"><span class="dot ${valet ? 'on' : 'off'}"></span>${valet ? 'Running (Valet)' : 'Not a website'}</span>`
        : `<label class="pchip" title="${s.enabled ? 'Switch off' : 'Switch on'}"><input type="checkbox" data-field="enabled" ${s.enabled ? 'checked' : ''} aria-label="Running"><span class="dot ${s.enabled ? 'on' : 'off'}"></span>${s.enabled ? 'Running' : 'Off'}</label>`}
      ${sel('kind', kindOpts, typeOf(s).icon(), valet)}
      ${php ? sel('php', phpOpts, icon.php, valet) + sel('db_server', `<option value="mamp" ${s.db_server === 'mamp' ? 'selected' : ''}>MAMP PRO MySQL</option><option value="localsites" ${s.db_server === 'localsites' ? 'selected' : ''}>LocalSites MySQL</option>`, icon.db, valet) : ''}
      ${s.git?.branch ? `<button class="pchip" data-dpane="git">${icon.branch}${esc(s.git.branch)}</button>` : ''}
      ${s.enabled || valet ? `<button class="pchip" data-openurl="${esc(s.folder)}">${icon.globe}${esc(s.domain)}</button>` : ''}
    </div>`;
  const prop = (html) => narrow ? '' : html;
  return `${narrow ? chips : ''}
      ${s.problems?.length ? `<div class="callout warn">${icon.alertCircle}<div>${s.problems.map(esc).join('<br>')}
        ${needsDeps ? `<div style="margin-top:8px"><button class="btn sm" data-install="${esc(s.folder)}" ${s.installing ? 'disabled' : ''}>${s.installing ? '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>Installing…' : `${icon.download}Install dependencies`}</button></div>` : ''}</div></div>` : ''}
      ${valet ? `<div class="callout">${icon.info}<div>Served by <b>Laravel Valet</b> at <a data-openurl="${esc(s.folder)}">${esc(s.domain)}</a>${s.php ? ` with PHP ${esc(s.php)}` : ''}. Valet keeps managing it; LocalSites shows it so all your projects are in one place.
        <div style="margin-top:8px"><button class="btn sm" data-takeover="${esc(s.folder)}">${icon.login}Serve with LocalSites instead…</button></div></div></div>` : ''}
      ${prop(`<div class="prop chipped"><label>Status</label><div class="form-row">${valet ? '<span class="note">Running (Valet)</span>' : s.runtime === 'none' ? '<span class="note">Not a website. Set a type to serve it.</span>'
        : `<label class="switch"><input type="checkbox" data-field="enabled" ${s.enabled ? 'checked' : ''}><span></span></label><span class="note">${s.enabled ? 'Running' : 'Off'}</span>`}</div></div>
      <div class="prop chipped"><label>Type</label><select class="input" data-field="kind" ${valet ? 'disabled' : ''}>${kindOpts}</select></div>`)}
      ${s.type === 'command' || s.type === 'docker' ? `<div class="prop"><label>Command</label><input class="input mono" data-field="command" value="${esc(s.command || '')}" placeholder="${s.type === 'docker' ? 'docker compose up' : 'python manage.py runserver {port}'}" spellcheck="false"></div>
        ${s.type === 'docker' ? `<div class="prop"><label>Port</label><input class="input mono" data-field="port" value="${esc(s.port || '')}" placeholder="8080" inputmode="numeric" spellcheck="false"></div>
        <p class="note">The command runs in the project folder; the address forwards to this port on 127.0.0.1 (publish it in your compose file, e.g. <code>"127.0.0.1:8080:80"</code>).</p>`
        : `<p class="note">Runs in the project folder. <code>{port}</code> is replaced; <code>PORT</code> is also set. Listen on 127.0.0.1.</p>`}` : ''}
      <div class="prop"><label>Address</label><input class="input" data-field="domain" value="${esc(s.domain)}" spellcheck="false" ${valet ? 'disabled' : ''}></div>
      ${php ? `${prop(`<div class="prop chipped"><label>PHP</label><select class="input" data-field="php" ${valet ? 'disabled' : ''}>${phpOpts}</select></div>`)}
        ${s.framework?.php_requirement ? `<p class="note">composer.json needs PHP <code>${esc(s.framework.php_requirement)}</code>.</p>` : ''}
        <div class="prop"><label>Web root</label><input class="input" data-field="docroot" value="${esc(s.docroot)}" placeholder="(project folder)" spellcheck="false" ${valet ? 'disabled' : ''}></div>
        ${valet ? '' : `<div class="prop"><label>Web server</label><select class="input" data-field="web_server"><option value="apache" ${s.web_server !== 'nginx' ? 'selected' : ''}>Apache (.htaccess works)</option><option value="nginx" ${s.web_server === 'nginx' ? 'selected' : ''}>Nginx</option></select></div>
        ${s.web_server === 'nginx' ? '<p class="note">Nginx ignores .htaccess files. Pretty URLs fall back to index.php, like Laravel and WordPress expect.</p>' : ''}
        <div class="prop"><label>Xdebug</label><div class="form-row">${UI.toggle(!!s.xdebug, 'data-field="xdebug"', 'Xdebug')}<span class="note">${s.xdebug ? 'Debugging on port 9003' : 'Off'}</span></div></div>
        ${s.xdebug ? xdebugHint(s.php) : ''}`}
        ${prop(`<div class="prop chipped"><label>MySQL</label><select class="input" data-field="db_server" ${valet ? 'disabled' : ''}>
          <option value="mamp" ${s.db_server === 'mamp' ? 'selected' : ''}>MAMP PRO MySQL</option>
          <option value="localsites" ${s.db_server === 'localsites' ? 'selected' : ''}>LocalSites MySQL 8.4</option></select></div>`)}` : ''}
      ${app ? `<div class="prop"><label>Dev server</label><div class="note">${s.port ? `127.0.0.1:${s.port}` : 'Port assigned when switched on'}${s.framework?.package_manager ? ` · ${esc(s.framework.package_manager)}` : ''}${s.framework?.node_version ? ` · Node ${esc(s.framework.node_version)}` : ''}</div></div>` : ''}
      ${!valet && s.runtime !== 'none' ? `<div class="prop"><label>${app ? 'Environment' : 'Overrides'}</label><div class="form-row">
        ${app ? `<button class="btn sm" data-envedit="${esc(s.folder)}">${icon.key}Variables${envCount ? `<span class="badge">${envCount}</span>` : ''}</button>` : ''}
        ${php ? `<button class="btn sm" data-iniedit="${esc(s.folder)}">${icon.sliders}php.ini${iniCount ? `<span class="badge">${iniCount}</span>` : ''}</button>` : ''}</div></div>` : ''}
      ${imported}${s.managed_by ? `<p class="note">Settings from <code>${esc(s.managed_by)}</code> override these.</p>` : ''}
      ${s.runtime === 'php' && s.served_by !== 'valet' ? projectDB(s) : ''}
      ${s.runtime !== 'none' ? sharePane(s) : ''}
      <div class="st">Open</div>${openSplit(s)}
      ${s.git ? `<div class="st">Git<span class="spacer"></span><button class="btn ghost sm" data-dpane="git">${icon.branch}Open git</button></div>
        <div class="kv"><span>Branch</span><span class="form-row" style="gap:8px">${s.git.branch ? branchChip(s.git.branch, `data-dpane="git"`) : '<span class="note">No branch yet</span>'}${S.git[s.folder]?.st?.files?.length ? `<span class="note">${plural(S.git[s.folder].st.files.length, 'change')}</span>` : ''}</span></div>
        ${s.git.account ? `<div class="kv"><span>Account</span><span class="form-row" style="gap:6px">${hostIcon(s.git.host)}${esc(s.git.account)}${s.git.host && !/\./.test(s.git.host) ? ` <span class="note">(SSH alias ${esc(s.git.host)})</span>` : ''}</span></div>` : ''}
        ${s.git.repo ? `<div class="kv"><span>Repository</span><span>${esc(s.git.repo)}</span></div>` : ''}` : ''}
      ${!valet && s.runtime !== 'none' ? `<div class="st">Connection<span class="spacer"></span><button class="btn ghost sm" data-check="${esc(s.folder)}" ${s.enabled ? '' : 'disabled'}>${icon.doctor}Check</button></div>
      <div id="siteChecks">${S.checks[s.folder] ? checksTable(S.checks[s.folder], false) : '<p class="note" style="margin:0">Tests DNS, the server, the database connection and HTTPS, like a browser would.</p>'}</div>` : ''}
      ${!valet ? `<div class="st">${app ? 'Dev server output' : s.installing ? 'Install output' : 'Recent errors'}<span class="spacer"></span><button class="btn ghost sm" data-projlog="${esc(s.folder)}">${icon.logs}All logs</button></div>
      <div class="ml" id="miniLog">${miniLogHTML(miniLogAt[s.folder + (s.installing ? 'site-install' : app ? 'site-app' : 'site-error')]?.text, s)}</div>` : ''}
      <div class="st">Folder</div><div class="note" style="word-break:break-all">${esc(s.path)}</div>
      <div class="form-row proj-manage">${valet ? '<span class="note">Served by Laravel Valet: rename or remove it in Valet.</span>' : `${UI.btn('Rename…', { ic: 'edit', kind: 'ghost', attrs: `data-projrename="${esc(s.folder)}"` })}
        ${UI.btn('Hide', { ic: 'eyeOff', kind: 'ghost', attrs: `data-projhide="${esc(s.folder)}"`, title: 'Stop listing and serving it; the folder stays' })}
        ${UI.btn('Move to Trash…', { ic: 'trash', kind: 'ghost danger', attrs: `data-projtrash="${esc(s.folder)}"` })}`}</div>`;
}

/* --- PHP details --- */
async function loadPHPInfo() { if (S.phpInfo || loadPHPInfo.busy) return; loadPHPInfo.busy = true; S.phpInfo = await act(() => bridge.call('php.info')) || []; loadPHPInfo.busy = false; render(); }
function xdebugHint(ver) {
  if (!S.phpInfo) { loadPHPInfo(); return ''; }
  const i = S.phpInfo.find(x => x.version === ver);
  if (!i || i.xdebug_loaded || i.xdebug_file) return '<p class="note">Listen in your editor on port 9003 (VS Code: PHP Debug, PhpStorm: Start Listening).</p>';
  const cmd = `${i.xdebug_install.replace('pecl install', 'pecl -d php_ini= install')}`;
  return `<div class="callout">${icon.info}<div>Xdebug isn't installed for PHP ${esc(ver)} yet. Run this once in Terminal (it doesn't change your php.ini; LocalSites loads it only for projects that turn it on):${UI.code(cmd)}</div></div>`;
}

/* --- open in the apps you use --- */
// The person chooses their code editor and terminal; nothing is guessed. The choice is saved in LocalSites
// settings, so the CLI and agents use it too. "ask" means show the chooser every time.
const KIND_NAME = { editor: 'code editor', terminal: 'terminal' };
const appsOf = (kind) => (S.apps || []).filter(a => a.kind === kind);
// The chosen app for a kind, or null when nothing (usable) is chosen.
function prefApp(kind) {
  const id = (S.appDefaults || {})[kind];
  return id && id !== 'ask' ? appsOf(kind).find(a => a.id === id) || null : null;
}
// Button label: the chosen app's name, or a prompt to choose one.
const prefLabel = (kind) => prefApp(kind)?.name || (kind === 'editor' ? 'Code editor' : 'Terminal');
async function loadApps() {
  const r = await bridge.call('apps.list').catch(() => null);
  S.apps = Array.isArray(r) ? r : r?.apps || []; // an older service returns a plain list
  S.appDefaults = Array.isArray(r) ? {} : r?.defaults || {};
  // A choice saved by an older version in this window's storage moves to settings once.
  for (const kind of ['editor', 'terminal']) {
    const old = store.get('app.' + kind, '');
    if (!old) continue;
    store.set('app.' + kind, '');
    if (!S.appDefaults[kind] && appsOf(kind).some(a => a.id === old)) {
      try { await bridge.call('apps.default', { kind, app: old }); S.appDefaults[kind] = old; } catch (_) { /* kept unchosen */ }
    }
  }
}
async function setDefaultApp(kind, id, quiet) {
  await bridge.call('apps.default', { kind, app: id });
  S.appDefaults = { ...S.appDefaults }; if (id) S.appDefaults[kind] = id; else delete S.appDefaults[kind];
  if (!quiet) toast(id === 'ask' ? `LocalSites will ask which ${KIND_NAME[kind]} to use each time` : id ? `${appsOf(kind).find(a => a.id === id)?.name || id} is now your ${KIND_NAME[kind]}` : `No default ${KIND_NAME[kind]}`);
  render();
}
// Open a project in the chosen editor or terminal; ask first when nothing is chosen.
async function openWith(kind, folder) {
  const a = prefApp(kind);
  if (a) return act(() => bridge.call('open', { folder, app: a.id }), 'Open');
  const list = appsOf(kind);
  if (!list.length) return toast(`No ${KIND_NAME[kind]} found on this ${OS_NAME}. Install one, then try again.`, 'warn');
  const saved = (S.appDefaults || {})[kind], ask = saved === 'ask';
  const gone = saved && !ask ? `<p class="note">The ${KIND_NAME[kind]} you chose before (${esc(saved)}) isn’t installed anymore.</p>` : '';
  const pick = await dialog(`<h3>Which ${KIND_NAME[kind]} do you want to use?</h3>${gone}
    <div class="app-pick">${list.map(x => `<button class="btn" data-dlg="${esc(x.id)}">${kind === 'editor' ? icon.code : icon.terminal}${esc(x.name)}</button>`).join('')}</div>
    <label class="form-row app-remember"><input type="checkbox" id="appRemember" ${ask ? '' : 'checked'}> Remember my choice</label>
    <p class="note">You can change it any time in Settings → General.</p>
    <div class="form-row" style="justify-content:flex-end;margin-top:12px"><button class="btn ghost" data-dlg="cancel">Cancel</button></div>`);
  const chosen = list.find(x => x.id === pick);
  if (!chosen) return;
  const remember = !!$('#appRemember')?.checked;
  await act(async () => {
    await bridge.call('open', { folder, app: chosen.id, remember });
    if (remember) { S.appDefaults = { ...S.appDefaults, [kind]: chosen.id }; toast(`${chosen.name} is now your ${KIND_NAME[kind]}`); render(); }
  }, 'Open');
}
// Split Button: the way you last opened a project is the big button; the arrow lists the others, and
// picking one makes it the big button next time.
function openTargets(s) {
  const live = s.enabled || s.served_by === 'valet', f = esc(s.folder);
  return {
    browser: { ic: icon.globe, label: 'Browser', attrs: `data-openurl="${f}"`, off: !live && 'Switch the project on first' },
    files: { ic: icon.folder, label: FILES_APP.name, attrs: `data-app="${FILES_APP.id}" data-folder="${f}"` },
    terminal: { ic: icon.terminal, label: prefLabel('terminal'), attrs: `data-openwith="terminal" data-folder="${f}"` },
    editor: { ic: icon.code, label: prefLabel('editor'), attrs: `data-openwith="editor" data-folder="${f}"` },
    ...(s.database ? { database: { ic: icon.table, label: 'Data browser', attrs: `data-dbopen="${esc(s.database)}"`, hint: s.database } } : {}),
  };
}
function openPrimary(s) {
  const T = openTargets(s), want = store.get('openPrimary', '');
  return T[want] && !T[want].off ? want : !T.browser.off ? 'browser' : 'editor';
}
function openSplit(s) {
  const k = openPrimary(s), t = openTargets(s)[k];
  return `<div class="split" role="group" aria-label="Open"><button class="btn split-main" ${t.attrs} title="Open in ${esc(t.label)}">${t.ic}<span>Open in ${esc(t.label)}</span></button><button class="btn split-caret" data-openmore="${esc(s.folder)}" title="Other ways to open it" aria-haspopup="menu">${icon.caret}</button></div>`;
}
const APP_KINDS = [['editor', 'Editors'], ['terminal', 'Terminals'], ['git', 'Git apps'], ['browser', 'Browsers']];
function showOpenMenu(anchor, folder) {
  const s = site(folder), live = s.enabled || s.served_by === 'valet', T = openTargets(s), main = openPrimary(s);
  const top = `<div class="label2">Open in</div>` + Object.entries(T).map(([k, t]) => `<button data-openuse="${k}" ${t.attrs} ${t.off ? `disabled title="${esc(t.off)}"` : ''}>${t.ic}<span>${esc(t.label)}${t.hint ? ` <span class="note">${esc(t.hint)}</span>` : ''}</span>${k === main ? `<span class="kb">${icon.check}</span>` : ''}</button>`).join('') + '<hr>';
  const groups = APP_KINDS.map(([k, label]) => { const list = appsOf(k); if (!list.length) return '';
    return `<div class="label2">${label}</div>` + list.map(a => {
      const def = (k === 'editor' || k === 'terminal') && prefApp(k)?.id === a.id;
      const mk = (k === 'editor' || k === 'terminal') && !def ? `<span class="kb mkdef" data-mkdefault="${k}:${esc(a.id)}" title="Use ${esc(a.name)} for the Open buttons">Make default</span>` : '';
      return `<button data-app="${a.id}" data-folder="${esc(folder)}" ${k === 'browser' && !live ? 'disabled title="Switch the project on first"' : ''}><span>${esc(a.name)}</span>${def ? '<span class="kb">default</span>' : mk}</button>`;
    }).join(''); }).join('');
  menuAt(anchor, top + (groups || `<div class="menu-empty">No other apps found on this ${OS_NAME}.</div>`));
}

/* --- AI agents: connect and approve --- */
async function loadAgents() { if (loadAgents.busy) return; loadAgents.busy = true; S.agents = await act(() => bridge.call('agents.list')) || []; loadAgents.busy = false; if (cur().view === 'settings') renderView(); }
const AGENT_IC = { 'claude-desktop': () => icon.agent, 'claude-code': () => icon.cli, cursor: () => icon.code, vscode: () => icon.code, windsurf: () => icon.code, zed: () => icon.code };
function agentCard(c) {
  const foot = c.manual ? UI.btn('Copy settings', { ic: 'copy', attrs: `data-copy="${esc(c.manual)}"` }) + '<span class="note">Paste into Zed settings</span>'
    : c.problem ? `<span class="note warn">${esc(c.problem)}</span>`
    : c.connected ? UI.btn('Disconnect', { kind: 'ghost', attrs: `data-agentoff="${c.id}"` })
    : UI.btn('Connect', { ic: 'plugZap', kind: 'primary', attrs: `data-agenton="${c.id}"` });
  return UI.card({ ic: (AGENT_IC[c.id] || (() => icon.agent))(), title: c.name, status: c.manual ? UI.status('off', 'Manual') : UI.status(c.connected ? 'on' : 'off', c.connected ? 'Connected' : 'Not connected'),
    sub: c.config ? `<code title="${esc(c.config)}">${esc(c.config.replace(/^\/Users\/[^/]+/, '~')).replace(/\//g, '/<wbr>')}</code>` : c.id === 'claude-code' ? 'Uses <code>claude mcp add</code>' : '', foot, on: c.connected, attrs: `data-agent="${c.id}"` });
}
/* --- Go / No-go: an agent's risky request, shown as exactly what will run --- */
const whoAsks = (a) => a.client === 'mcp' ? (a.agent || 'An AI agent') : { cli: 'A command in the terminal', 'unproven-app': 'A program calling itself LocalSites' }[a.client] || 'A script or command (no terminal)';
const toolName = (m) => m.replace(/\./g, '_'); // the name agents know it by (MCP tool names)
const scopeLabel = (sc = '') => { const i = sc.indexOf(':'); return i < 0 ? sc : sc.slice(i + 1); };
// How much is at stake, from the method list (not guessed from the name)
function riskChips(a) {
  const backed = /backed up first|backup is made first|exported first/i.test(a.description || '');
  const out = [a.destructive ? (backed ? UI.status('warn', 'Backed up first') : UI.status('err', 'Can’t be undone')) : UI.status('busy', 'Needs your OK')];
  if (!a.grantable) out.push(UI.tag('Asks every time'));
  return out.join('');
}
// The request's parameters as readable lines (the service already masked secrets)
function paramLines(summary) {
  let p = {}; try { p = JSON.parse(summary || '{}'); } catch (_) { /* summary may be empty */ }
  return Object.entries(p).map(([k, v]) => `<div class="ap-kv"><span>${esc(k)}</span><code>${esc(typeof v === 'string' ? v : JSON.stringify(v))}</code></div>`).join('');
}
// A request that can't be undone needs the button held for a moment, so a stray click or Enter can't approve it.
const needsHold = (a) => a.destructive && !a.grantable;
function approvalActions(a, compact = false) {
  const grant = a.grantable && !compact ? UI.btn('Approve for 10 min', { attrs: `data-approve="${a.id}|1|w"`, title: `Also allow ${a.method.replace('.', ' ')} by ${whoAsks(a)} on this ${a.scope?.startsWith('folder:') ? 'project' : 'item'} for 10 minutes without asking` }) : '';
  const ok = needsHold(a) ? `<button class="btn primary ${compact ? 'sm' : ''} hold" data-hold="${a.id}" title="Hold to approve"><span class="hold-fill" aria-hidden="true"></span><span class="hold-t">Hold to approve</span></button>`
    : UI.btn('Approve', { kind: 'primary', sm: compact, attrs: `data-approve="${a.id}|1"` });
  return `${UI.btn('Deny', { sm: compact, attrs: `data-approve="${a.id}|0"` })}${grant}${ok}`;
}
function approvalCard(a) {
  const what = describe({ method: a.method, summary: a.summary });
  return `<div class="approval ${a.destructive ? 'risky' : ''}" role="alertdialog" aria-label="Approve ${esc(a.method)}" data-ap="${a.id}">
    <div class="ap-h">${a.destructive ? icon.shieldAlert : icon.agent}<span class="ap-who">${esc(whoAsks(a))} wants to</span><span class="spacer"></span><span class="ap-left" title="Time left to answer">${icon.timer}<span data-remain="${esc(a.expires)}">${fmtLeft(new Date(a.expires) - Date.now())}</span></span></div>
    <div class="ap-what">${esc(what === a.method ? a.method.replace('.', ' ') : what)}</div>
    <div class="ap-risk">${riskChips(a)}</div>
    <div class="ap-cmd"><div class="ap-cmd-h"><code>${esc(toolName(a.method))}</code><span class="spacer"></span><span>the exact request</span></div>${paramLines(a.summary) || '<div class="ap-kv"><span>No parameters</span></div>'}</div>
    ${a.description ? `<p class="note ap-desc">${esc(a.description)}.</p>` : ''}
    <div class="ap-acts">${approvalActions(a)}</div>
    <div class="ap-bar" aria-hidden="true"><i data-bar-from="${esc(a.created)}" data-bar-to="${esc(a.expires)}"></i></div></div>`;
}
let approvalsShown = '';
function renderApprovals(list) {
  const key = JSON.stringify(list.map(a => a.id));
  if (key === approvalsShown) return;
  approvalsShown = key; S.approvals = list;
  if (cur().view === 'overview' || cur().view === 'agent') setTimeout(renderView);
  $('#approvals').innerHTML = list.map(approvalCard).join('');
  if ($('#menu').classList.contains('open') && $('#menu').classList.contains('notices')) $('#menu').innerHTML = noticeMenuHTML();
}
async function pollApprovals() {
  const [list, grants] = await Promise.all([bridge.call('approvals.list').catch(() => []), bridge.call('approvals.grants').catch(() => null)]);
  renderApprovals(list || []);
  setGrants(grants);
}
// Hold to approve: the fill runs while the press lasts; letting go early cancels it.
const HOLD_MS = 900;
let holding = null;
function holdStart(btn) {
  if (holding || btn.disabled) return;
  btn.classList.add('holding');
  holding = { btn, t: setTimeout(() => { const id = btn.dataset.hold; holdEnd(); btn.disabled = true; decideApproval(id, true, false); }, HOLD_MS) };
}
function holdEnd() { if (!holding) return; clearTimeout(holding.t); holding.btn.classList.remove('holding'); holding = null; }
document.addEventListener('pointerdown', (e) => { const b = e.target.closest('[data-hold]'); if (b && e.button === 0) { e.preventDefault(); holdStart(b); } });
document.addEventListener('pointerup', holdEnd, true);
document.addEventListener('pointercancel', holdEnd, true);
document.addEventListener('pointerout', (e) => { if (holding && e.target === holding.btn && !holding.btn.contains(e.relatedTarget)) holdEnd(); }, true);
document.addEventListener('keydown', (e) => { const b = e.target.closest?.('[data-hold]'); if (b && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); e.stopPropagation(); if (!e.repeat) holdStart(b); } }, true);
document.addEventListener('keyup', (e) => { if (e.key === 'Enter' || e.key === ' ') holdEnd(); }, true);
async function decideApproval(id, ok, forAWhile) {
  await act(() => bridge.call('approvals.decide', { id, approve: ok, for_a_while: forAWhile }), 'Approval');
  toast(!ok ? 'Denied. Nothing was done.' : forAWhile ? 'Approved. The same request is allowed for 10 minutes.' : 'Approved. The agent carries on.', ok ? 'ok' : 'warn');
  approvalsShown = ''; return pollApprovals();
}

/* --- Access Pass: an "approve for 10 minutes", shown as a pass you can end early --- */
let grantsTimer = 0;
function setGrants(list) {
  if (!Array.isArray(list)) return; // an older service
  const now = Date.now(), live = list.filter(g => new Date(g.until) > now);
  const changed = JSON.stringify(live.map(g => g.id)) !== JSON.stringify((S.grants || []).map(g => g.id));
  S.grants = live;
  clearTimeout(grantsTimer); // look again right after the next one runs out
  if (live.length) grantsTimer = setTimeout(pollApprovals, Math.max(1000, Math.min(...live.map(g => new Date(g.until) - now)) + 500));
  if (!changed) return;
  renderStatus();
  if (cur().view === 'agent') renderView();
  const m = $('#menu');
  if (m.classList.contains('open') && m.classList.contains('passes')) m.innerHTML = passMenuHTML();
}
function passCard(g) {
  const action = describe({ method: g.method, summary: '{}' });
  const what = action === g.method ? g.method.replace('.', ' ') : action.trim();
  const from = new Date(new Date(g.until) - 10 * 60000).toISOString();
  return `<div class="pass" data-pass="${esc(g.id)}"><div class="pass-l">${icon.ticket}</div>
    <div class="pass-m"><div class="pass-t"><b>${esc(g.agent || whoAsks(g))}</b> may <code>${esc(toolName(g.method))}</code>${g.scope ? ` on <b>${esc(scopeLabel(g.scope))}</b>` : ''}</div>
      <div class="note">${esc(what)} without asking · until ${new Date(g.until).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}, <span data-remain="${esc(g.until)}">${fmtLeft(new Date(g.until) - Date.now())}</span> left</div>
      <div class="pass-bar" aria-hidden="true"><i data-bar-from="${from}" data-bar-to="${esc(g.until)}"></i></div></div>
    <div class="pass-r"><button class="btn sm" data-revoke="${esc(g.id)}" title="The next request of this kind asks again">End now</button></div></div>`;
}
function passMenuHTML() {
  const list = S.grants || [];
  return `<div class="notice-h"><b>Allowed for a while</b><span class="spacer"></span><span class="note">${list.length} ${list.length === 1 ? 'pass' : 'passes'}</span></div>
    ${list.length ? `<div class="passes">${list.map(passCard).join('')}</div>` : '<div class="menu-empty">Nothing is allowed without asking right now. “Approve for 10 min” on a request adds a pass here.</div>'}`;
}

/* --- sharing a site on the internet --- */
const SF = { prov: '', mins: 60, pw: '', open: false }; // share form, kept across refreshes
// The password of a share started from this window, by share id: memory only, never saved. The service never
// hands passwords back, so after a restart the panel can only give the hint.
const SHARE_PW = {};
// four of 256 words and a number: about 39 bits, far beyond what the gateway's guess limits allow in a day
const SHARE_WORDS = ['acorn', 'actor', 'agent', 'album', 'alarm', 'alpha', 'amber', 'angle', 'apple', 'apron', 'arena', 'arrow', 'aspen', 'atlas', 'attic', 'autumn', 'badge', 'bagel', 'baker', 'bamboo', 'banjo', 'barley', 'basil', 'basin', 'beach', 'beacon', 'berry', 'bison', 'blade', 'blaze', 'bloom', 'board', 'bonus', 'brave', 'bread', 'brick', 'bridge', 'brook', 'brush', 'bucket', 'bugle', 'cabin', 'cable', 'cactus', 'camel', 'canal', 'candle', 'canoe', 'canvas', 'carbon', 'cargo', 'carrot', 'castle', 'cedar', 'cello', 'chalk', 'charm', 'cherry', 'chess', 'chief', 'cider', 'cinema', 'circle', 'citrus', 'clay', 'cliff', 'cloud', 'clover', 'coast', 'cobalt', 'cocoa', 'comet', 'copper', 'coral', 'cotton', 'crane', 'crater', 'cricket', 'crown', 'crystal', 'cube', 'daisy', 'dance', 'delta', 'denim', 'desert', 'diesel', 'dolphin', 'domino', 'dragon', 'drum', 'dune', 'eagle', 'earth', 'echo', 'ember', 'engine', 'falcon', 'fable', 'feather', 'fern', 'fiddle', 'field', 'fig', 'flame', 'flint', 'flute', 'forest', 'fossil', 'fox', 'frost', 'fudge', 'galaxy', 'garden', 'garnet', 'gecko', 'ginger', 'glacier', 'globe', 'gold', 'grape', 'gravel', 'grove', 'guitar', 'habit', 'harbor', 'harvest', 'hazel', 'helmet', 'heron', 'hiking', 'honey', 'horizon', 'hotel', 'husky', 'igloo', 'indigo', 'iris', 'island', 'ivory', 'jacket', 'jaguar', 'jasmine', 'jelly', 'jewel', 'jungle', 'kayak', 'kelp', 'kettle', 'kiwi', 'koala', 'ladder', 'lagoon', 'lantern', 'lava', 'lemon', 'lily', 'linen', 'lion', 'lobster', 'lotus', 'lumen', 'lunar', 'magnet', 'mango', 'maple', 'marble', 'meadow', 'melon', 'mesa', 'meteor', 'mint', 'mirror', 'mocha', 'moon', 'mosaic', 'moss', 'motor', 'mountain', 'muffin', 'nectar', 'needle', 'nickel', 'noodle', 'nova', 'oak', 'oasis', 'ocean', 'olive', 'onyx', 'opal', 'orbit', 'orchid', 'otter', 'owl', 'paddle', 'palm', 'panda', 'paper', 'parrot', 'pastel', 'peach', 'pearl', 'pebble', 'pepper', 'piano', 'pilot', 'pine', 'pixel', 'planet', 'plum', 'polar', 'pony', 'poppy', 'prism', 'pumpkin', 'quartz', 'quill', 'rabbit', 'radar', 'radio', 'rain', 'raven', 'reef', 'ribbon', 'river', 'robin', 'rocket', 'rose', 'ruby', 'saddle', 'saffron', 'salmon', 'sand', 'sapphire', 'satin', 'scarf', 'shell', 'sierra', 'silk', 'silver', 'sketch', 'sky', 'slate', 'snow', 'solar', 'spark', 'spice', 'spruce', 'squid', 'star', 'stone', 'storm', 'sugar', 'summit', 'sunset'];
function newSharePassword() {
  const r = crypto.getRandomValues(new Uint32Array(5)), w = i => SHARE_WORDS[r[i] % 256];
  return `${w(0)}-${w(1)}-${w(2)}-${w(3)}-${10 + r[4] % 90}`; }
const SHARE_MINUTES = [[15, '15 minutes'], [60, '1 hour'], [240, '4 hours'], [1440, '24 hours']];
function minsLeft(t) { const m = Math.max(0, Math.round((new Date(t) - Date.now()) / 60000)); return m >= 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${m} min`; }
function sharePane(s) {
  const st = S.status || {}, live = (st.shares || []).find(x => x.folder === s.folder), provs = st.share_providers || [];
  if (live) return `<div class="st">Shared on the internet</div>
    <div class="share-live"><div class="form-row" style="gap:6px;flex-wrap:nowrap">${UI.status('on', 'Live')}<a class="link ellipsis" data-openweb="${esc(live.url)}" title="${esc(live.url)}">${esc(live.url.replace(/^https?:\/\//, ''))}</a></div>
      <p class="note" style="margin:6px 0">Stops in ${minsLeft(live.expires)}${live.password ? ' · password protected' : ' · anyone with the address can open it'} · ${plural(live.requests || 0, 'visit')}${live.blocked ? ` · ${live.blocked} refused` : ''}</p>
      ${live.password ? `<p class="note" style="margin:0 0 8px">Visitors see a sign-in box: they leave the username empty and type the password${SHARE_PW[live.id] ? `: <code>${esc(SHARE_PW[live.id])}</code>` : '.'}</p>` : ''}
      <div class="form-row">${UI.btn('Copy link', { ic: 'copy', attrs: `data-copy="${esc(live.url)}"` })}${live.password && SHARE_PW[live.id] ? UI.btn('Copy password', { ic: 'key', attrs: `data-copy="${esc(SHARE_PW[live.id])}"` }) : ''}${UI.btn('Open', { ic: 'external', kind: 'ghost', attrs: `data-openweb="${esc(live.url)}"` })}<span class="spacer"></span>${UI.btn('Stop sharing', { ic: 'stop', kind: 'danger', attrs: `data-sharestop="${esc(live.id)}"` })}</div></div>`;
  const ready = provs.filter(p => p.installed);
  const what = `<p class="note" style="margin:0 0 10px">A public https link with a password, for a limited time. Only this project is reachable; <code>.env</code> and <code>.git</code> are never served.</p>`;
  const chosen = ready.find(p => p.id === SF.prov) || ready[0];
  // the tunnels, laid out like the Open buttons: pick one, or install one that isn't here yet
  const short = (p) => ({ cloudflared: 'Cloudflare' })[p.id] || p.name;
  const tag = (p) => p.installing ? 'installing…' : !p.installed ? 'install' : p.id === 'tunnl' ? 'nothing to install' : !p.https ? 'http only' : 'installed';
  const tunnels = `<div class="grid2 tunnels" role="radiogroup" aria-label="Tunnel">${provs.map(p => p.installed
    ? `<button class="btn tunnel ${chosen?.id === p.id ? 'on' : ''}" role="radio" aria-checked="${chosen?.id === p.id}" data-shareprov="${p.id}" title="${esc(p.description)}">${icon.globe}<span class="tw"><span class="tn">${esc(short(p))}</span><span class="tt">${tag(p)}</span></span></button>`
    : `<button class="btn tunnel" data-shareinstall="${p.id}" title="${esc(p.install_command || p.description)}" ${p.installing ? 'disabled' : ''}>${p.installing ? '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>' : icon.download2}<span class="tw"><span class="tn">${esc(short(p))}</span><span class="tt">${tag(p)}</span></span></button>`).join('')}</div>`;
  if (!ready.length) return `<div class="st">Share on the internet</div>${what}${tunnels}
    <p class="note" style="margin:8px 0 0">tunnl.gg needs the <code>ssh</code> that comes with your computer, and it wasn't found here. Install one of the others to share.</p>`;
  if (!s.enabled && s.served_by !== 'valet') return `<div class="st">Share on the internet</div>${what}<p class="note" style="margin:0">Switch the project on to share it.</p>`;
  if (!SF.pw && !SF.open) SF.pw = newSharePassword(); // a password by default
  return `<div class="st">Share on the internet</div>${what}${tunnels}
    <div class="share-form" style="margin-top:6px">
      <select class="input sm" id="shareMins" aria-label="For how long" style="grid-column:1/-1">${SHARE_MINUTES.map(([m, l]) => `<option value="${m}" ${m === SF.mins ? 'selected' : ''}>For ${l}</option>`).join('')}</select>
      <label class="form-row note" style="gap:6px;grid-column:1/-1"><input type="checkbox" id="shareOpen" ${SF.open ? 'checked' : ''}> No password (anyone with the address can open it)</label>
      <div class="form-row" style="gap:6px;flex-wrap:nowrap;${SF.open ? 'display:none' : ''}"><input class="input sm" id="sharePw" value="${esc(SF.pw)}" placeholder="Password" autocomplete="off" spellcheck="false" style="flex:1;min-width:0" aria-label="Password">
        ${UI.iconBtn('key', 'Make up a password', 'data-cmd="sharegenpw"')}</div>
      ${UI.btn('Share', { ic: 'globe', kind: 'primary', attrs: `data-sharestart="${esc(s.folder)}" data-shareprov-use="${chosen.id}"` })}
    </div><p class="note" style="margin:8px 0 0">${esc(chosen.description || '')}</p>`;
}


/* --- git panel --- */
async function gitLoad(folder) {
  const g = S.git[folder] = { ...(S.git[folder] || {}), busy: true, err: '' };
  try {
    const [st, br, log] = await Promise.all([bridge.call('git.status', { folder }), bridge.call('git.branches', { folder }), bridge.call('git.log', { folder, lines: 60 })]);
    Object.assign(g, { st, br, log, at: Date.now() });
  } catch (e) { g.err = e.message; g.at = Date.now(); }
  g.busy = false;
  if (cur().sel === folder) renderDetail();
  if (g.st?.web_url?.includes('github.com')) { // pull requests and CI: separate, so a slow GitHub never delays the panel
    g.host = await bridge.call('git.hosting', { folder }).catch(e => ({ problem: e.message }));
    if (cur().sel === folder) renderDetail();
  }
}
async function gitAction(folder, method, params = {}, label = '') {
  const g = S.git[folder] || (S.git[folder] = {});
  g.op = method; renderDetail();
  try { const r = await bridge.call(method, { folder, ...params }); if (r?.log) toast(r.log); }
  catch (e) { toast((label ? label + ': ' : '') + e.message, 'err'); }
  g.op = '';
  await Promise.all([gitLoad(folder), act(loadSites)]);
  render();
}
const GS = { modified: ['M', 'mod'], added: ['A', 'add'], deleted: ['D', 'del'], renamed: ['R', 'ren'], untracked: ['U', 'unt'], conflict: ['!', 'con'] };
function branchTree(list, folder, dirty) {
  const root = { dirs: new Map(), items: [] };
  for (const b of list) {
    const parts = b.name.split('/'); let n = root;
    for (const p of parts.slice(0, -1)) { if (!n.dirs.has(p)) n.dirs.set(p, { dirs: new Map(), items: [] }); n = n.dirs.get(p); }
    n.items.push({ ...b, leaf: parts[parts.length - 1] });
  }
  const count = (n) => n.items.length + [...n.dirs.values()].reduce((a, c) => a + count(c), 0);
  const draw = (n) => [...n.dirs.entries()].map(([name, c]) => `<details class="gdir" open><summary>${icon.chevronR}${icon.folder}<span>${esc(name)}</span><span class="n">${count(c)}</span></summary><div class="gkids">${draw(c)}</div></details>`).join('')
    + n.items.map(b => `<button class="gbr ${b.current ? 'cur' : ''}" data-gbranch="${esc(b.name)}" data-gfolder="${esc(folder)}" data-remote="${b.remote ? 1 : ''}" title="${esc(b.subject || '')}">
        ${b.current ? '<span class="dot on"></span>' : icon.branch}<span class="gname">${esc(b.leaf)}</span>${b.track ? `<span class="chip mono">${esc(b.track)}</span>` : ''}<span class="spacer"></span><span class="note">${esc(b.when || '')}</span></button>`).join('');
  return draw(root);
}
function gitPane(s) {
  const g = S.git[s.folder];
  if (!g || (!g.st && !g.err)) return '<div class="gskel"><span class="skeleton" style="width:60%"></span><span class="skeleton" style="width:40%"></span><span class="skeleton" style="width:80%"></span></div>';
  if (g.err && !g.st) return emptyState({ ic: 'branch', title: 'Git is not available here', text: esc(g.err), actions: `<button class="btn sm" data-gitreload="${esc(s.folder)}">${icon.refresh}Try again</button>` });
  const st = g.st, files = st.files || [], dirty = files.length > 0, op = g.op || '';
  const pullWhy = !st.upstream ? 'This branch has no upstream to pull from' : dirty ? `Commit or stash your ${plural(files.length, 'change')} first` : st.behind === 0 ? 'Already up to date' : '';
  const local = (g.br || []).filter(b => !b.remote), remote = (g.br || []).filter(b => b.remote);
  const head = `<div class="ghead">
      <div class="gtitle">${st.detached ? icon.commit : icon.branch}<code>${esc(st.detached ? 'detached HEAD' : st.branch)}</code>${st.upstream ? `<span class="note">tracks ${esc(st.upstream)}</span>` : '<span class="note">no upstream</span>'}</div>
      <div class="gsync">${st.upstream ? `<span class="chip ${st.ahead ? 'accent' : ''}" title="Commits to push">${icon.up}${st.ahead}</span><span class="chip ${st.behind ? 'accent' : ''}" title="Commits to pull">${icon.down}${st.behind}</span>` : ''}
        ${dirty ? `<span class="chip warn">${plural(files.length, 'change')}</span>` : '<span class="chip ok">' + icon.check + 'Clean</span>'}${st.stashes ? `<span class="chip">${plural(st.stashes, 'stash')}</span>` : ''}</div>
      <div class="gacts">
        <button class="btn sm" data-gitop="fetch" data-gfolder="${esc(s.folder)}" ${op ? 'disabled' : ''} title="Download new commits from the remote. Your files are not changed.">${op === 'git.fetch' ? '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>' : icon.fetch}Fetch</button>
        <button class="btn sm" data-gitop="pull" data-gfolder="${esc(s.folder)}" ${op || pullWhy ? 'disabled' : ''} title="${esc(pullWhy || 'Fast-forward to ' + st.upstream + ' (never merges or rewrites)')}">${op === 'git.pull' ? '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>' : icon.pull}Pull${st.behind ? ` ${st.behind}` : ''}</button>
        <button class="btn sm" data-gitop="branch" data-gfolder="${esc(s.folder)}" ${op ? 'disabled' : ''}>${icon.plus}Branch</button>
        <span class="spacer"></span>
        ${st.web_url ? `<button class="icon-btn" data-openweb="${esc(st.web_url)}" title="Open repository on the web">${hostIcon(st.web_url)}</button>` : ''}
        <button class="icon-btn" data-gitreload="${esc(s.folder)}" title="Refresh">${g.busy ? '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>' : icon.refresh}</button></div>
      ${st.last_fetch ? `<p class="note" style="margin:6px 0 0">Last fetched ${esc(ago(st.last_fetch))}</p>` : ''}${hostingRow(g.host)}</div>`;
  const shown = g.allFiles ? files : files.slice(0, 12);
  const changes = `<div class="st">Changes<span class="n">${files.length || ''}</span></div>${files.length ? `<div class="gfiles">${shown.map(f => { const [ch, cls] = GS[f.status] || ['?', 'unt'];
      const i = f.path.lastIndexOf('/'); return `<div class="gfile" title="${esc(f.path)}"><span class="gs ${cls}">${ch}</span><span class="gp">${i >= 0 ? `<span class="note">${esc(f.path.slice(0, i + 1))}</span>` : ''}${esc(f.path.slice(i + 1))}</span>${f.staged ? '<span class="note">staged</span>' : ''}</div>`; }).join('')}
      ${files.length > shown.length ? `<button class="btn ghost sm" data-gitall="${esc(s.folder)}">Show all ${files.length}</button>` : ''}</div>`
    : '<p class="note" style="margin:0">Working tree clean. Nothing to commit.</p>'}`;
  const branches = `<div class="st">Branches<span class="n">${local.length}</span></div><div class="gtree">${branchTree(local, s.folder, dirty) || '<p class="note">No local branches</p>'}</div>
    ${remote.length ? `<details class="gdir remote"><summary>${icon.chevronR}${icon.globe}<span>Remote branches</span><span class="n">${remote.length}</span></summary><div class="gkids">${branchTree(remote, s.folder, dirty)}</div></details>` : ''}`;
  return head + changes + branches + (branchGraph(s.folder, g.log || [], st, st.web_url) || '<div class="st">History</div><p class="note">No commits yet</p>');
}
/* --- Branch Graph. Each branch gets its
   own coloured lane, merges sweep in on curves, refs and tags sit on their commits, and hovering a row slides a
   detail card along with you. Colours are the --lane tokens. --- */
const BG = { ROW: 48, LANE: 16, PAD: 14, R: 4, MAXL: 8 }; // two lines per commit: message and hash, then refs, author and date
const bgTone = (i) => `var(--lane-${[6, 1, 2, 4, 5, 3][i % 6]})`; // sky, violet, emerald, amber, rose, orange
function bgLayout(commits) {
  const row = new Map(commits.map((c, i) => [c.sha, i]));
  const lanes = [], laneOf = [], pending = [];
  const free = (skip = -1) => { const i = lanes.findIndex((l, k) => l === null && k !== skip); return i === -1 ? lanes.length : i; };
  commits.forEach((c, i) => {
    let lane = lanes.indexOf(c.sha);
    if (lane === -1) lane = free();
    lanes.forEach((l, k) => { if (l === c.sha && k !== lane) lanes[k] = null; });
    laneOf[i] = lane;
    lanes[lane] = c.parents[0] ?? null;
    if (c.parents[0]) pending.push({ from: i, parent: c.parents[0], route: lane });
    for (const p of c.parents.slice(1)) {
      let j = lanes.indexOf(p);
      if (j === -1) { j = free(lane); lanes[j] = p; }
      pending.push({ from: i, parent: p, route: j });
    }
    while (lanes.length && lanes[lanes.length - 1] === null) lanes.pop();
  });
  const edges = pending.map((e) => { const to = row.get(e.parent);
    return to === undefined ? { from: e.from, to: commits.length, fromLane: laneOf[e.from], toLane: e.route, route: e.route }
      : { from: e.from, to, fromLane: laneOf[e.from], toLane: laneOf[to], route: e.route }; });
  const width = Math.min(BG.MAXL, Math.max(0, ...laneOf, ...edges.map(e => e.route)) + 1);
  return { laneOf, edges, width };
}
const bgX = (lane) => BG.PAD + Math.min(lane, BG.MAXL - 1) * BG.LANE;
const bgY = (r) => r * BG.ROW + BG.ROW / 2;
const bgCurve = (x1, y1, x2, y2) => { const m = (y2 - y1) / 2; return `C ${x1} ${y1 + m} ${x2} ${y2 - m} ${x2} ${y2}`; };
function bgEdgePath(e) {
  const x1 = bgX(e.fromLane), y1 = bgY(e.from), xr = bgX(e.route), x2 = bgX(e.toLane), y2 = bgY(e.to);
  if (e.to - e.from === 1) return `M ${x1} ${y1} ${x1 === x2 ? `L ${x2} ${y2}` : bgCurve(x1, y1, x2, y2)}`;
  let d = `M ${x1} ${y1} `, cy = y1;
  if (xr !== x1) { d += `${bgCurve(x1, y1, xr, y1 + BG.ROW)} `; cy = y1 + BG.ROW; }
  if (xr !== x2) { if (y2 - BG.ROW > cy) d += `L ${xr} ${y2 - BG.ROW} `; d += bgCurve(xr, y2 - BG.ROW, x2, y2); }
  else d += `L ${x2} ${y2}`;
  return d;
}
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const bgShort = (iso) => { const d = new Date(iso); return isNaN(d) ? '' : `${MONTHS[d.getMonth()]} ${d.getDate()}`; };
const bgLong = (iso) => { const d = new Date(iso); return isNaN(d) ? '' : `${MONTHS[d.getMonth()]} ${d.getDate()}, ${d.getFullYear()} · ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`; };
const bgInitials = (n) => n.split(/\s+/).map(p => p[0] || '').join('').slice(0, 2).toUpperCase();
const bgAuthorTone = (n) => bgTone([...n].reduce((a, ch) => a + ch.charCodeAt(0), 0));
const bgAvatar = (name, size = 'sm') => `<span class="bg-av ${size}" style="--t:${bgAuthorTone(name)}" aria-hidden="true">${esc(bgInitials(name))}</span>`;
// A local branch and its remote copy on the same commit show as one ref, marked as in sync
function bgRefs(list, st) {
  const tags = list.filter(r => r.startsWith('tag: ')).map(r => ({ name: r.slice(5), type: 'tag' }));
  const br = list.filter(r => !r.startsWith('tag: ') && !/\/HEAD$/.test(r));
  const remoteName = (r) => { const i = r.indexOf('/'); return i > 0 ? r.slice(i + 1) : null; }; // origin/main -> main
  const out = br.filter(r => !br.includes(remoteName(r))) // origin/main next to main: folded into main
    .map(r => ({ name: r, type: 'branch', current: r === st?.branch, synced: br.some(x => remoteName(x) === r) }));
  return [...out, ...tags];
}
// Our log (git.log) in the component's shape
function bgCommits(log, st) {
  return log.map(c => ({ sha: c.hash, message: c.subject, body: c.body || '', author: c.author, date: c.date || '', parents: c.parents || [],
    refs: bgRefs(c.refs || [], st),
    stats: c.files ? { files: c.files, additions: c.added || 0, deletions: c.removed || 0 } : null }));
}
const bgState = { folder: '', commits: [], laneOf: [], graphW: 0, hover: null, shown: null, warm: false, delay: 0, cool: 0, focus: 0, drawn: new Set() };
function branchGraph(folder, log, st, web) {
  const commits = bgCommits(log, st);
  if (!commits.length) return '';
  const { laneOf, edges, width } = bgLayout(commits), graphW = BG.PAD * 2 + (width - 1) * BG.LANE;
  Object.assign(bgState, { folder, commits, laneOf, graphW, web });
  const anim = !bgState.drawn.has(folder) && !matchMedia('(prefers-reduced-motion: reduce)').matches; // draw in once per project
  bgState.drawn.add(folder);
  const branches = new Set(commits.flatMap(c => c.refs.filter(r => r.type === 'branch').map(r => r.name.replace(/^[^/]+\//, '')))).size;
  const merges = commits.filter(c => c.parents.length > 1).length;
  const paths = edges.map(e => `<path d="${bgEdgePath(e)}" stroke="${bgTone(e.route)}" stroke-width="2" stroke-linecap="round" pathLength="1" ${anim ? `class="bg-draw" style="animation-delay:${(e.from * 0.035).toFixed(3)}s"` : ''}/>`).join('');
  const dots = commits.map((c, i) => { const merge = c.parents.length > 1, t = bgTone(laneOf[i]);
    return `<g transform="translate(${bgX(laneOf[i])} ${bgY(i)})" data-bgdot="${i}" style="--t:${t}"><circle class="bg-ring" r="9" fill="none" stroke-width="3"/>
      <circle class="bg-node ${anim ? 'bg-pop' : ''}" ${anim ? `style="animation-delay:${(i * 0.035).toFixed(3)}s"` : ''} r="${merge ? BG.R + 0.5 : BG.R}" fill="${merge ? 'var(--card)' : t}" stroke="${t}" stroke-width="${merge ? 2.5 : 0}"/></g>`; }).join('');
  const rows = commits.map((c, i) => { const t = bgTone(laneOf[i]);
    // the current branch first; at most two refs, the rest as +N
    const sorted = [...c.refs].sort((a, b) => (b.current - a.current) || (a.type === 'tag') - (b.type === 'tag'));
    const refs = sorted.slice(0, 2).map(r => `<span class="bg-ref ${r.type}${r.current ? ' cur' : ''}" style="--t:${t}" title="${esc(r.name)}">${r.type === 'tag' ? icon.tag : icon.branch}<span>${esc(r.name)}</span>${r.synced ? `<span class="bg-sync" title="Same commit on the remote">${icon.fetch}</span>` : ''}</span>`).join('')
      + (sorted.length > 2 ? `<span class="bg-more" title="${esc(sorted.slice(2).map(r => r.name).join(', '))}">+${sorted.length - 2}</span>` : '');
    return `<div class="bg-row" role="listitem" data-bgrow="${i}" tabindex="${i === 0 ? 0 : -1}" style="padding-inline-start:${graphW + 4}px" aria-label="${esc(`${c.message}, ${c.author}, ${bgShort(c.date)}, ${c.sha.slice(0, 7)}`)}">
      <span class="bg-l1"><span class="bg-msg ${c.parents.length > 1 ? 'merge' : ''}">${esc(c.message)}</span><code class="bg-sha">${esc(c.sha.slice(0, 7))}</code></span>
      <span class="bg-l2">${refs}${bgAvatar(c.author)}${refs ? '' : `<span class="bg-who">${esc(c.author)}</span>`}<span class="bg-date">${esc(bgShort(c.date))}</span></span></div>`; }).join('');
  return `<div class="bg" data-bgfolder="${esc(folder)}"><div class="bg-h"><h3>History</h3><span>${icon.branch}${plural(branches, 'branch').replace('branchs', 'branches')}</span><span>${icon.merge}${plural(merges, 'merge')}</span></div>
    <div class="bg-body"><svg class="bg-svg" aria-hidden="true" fill="none" width="${graphW}" height="${commits.length * BG.ROW}">${paths}${dots}</svg>
      <div role="list" aria-label="Commits">${rows}</div><div class="bg-card" aria-hidden="true"></div></div></div>`;
}
// Hover: the first card waits a moment, then follows the pointer from row to row straight away
function bgSetHover(i) {
  const S2 = bgState; clearTimeout(S2.delay);
  document.querySelectorAll('.bg-row.hov').forEach(r => r.classList.remove('hov'));
  document.querySelectorAll('.bg [data-bgdot].on').forEach(g => g.classList.remove('on'));
  S2.hover = i;
  if (i === null) { S2.cool = setTimeout(() => { S2.shown = null; S2.warm = false; bgShowCard(); }, 120); return; }
  clearTimeout(S2.cool);
  document.querySelector(`.bg-row[data-bgrow="${i}"]`)?.classList.add('hov');
  document.querySelector(`.bg [data-bgdot="${i}"]`)?.classList.add('on');
  if (S2.warm) { S2.shown = i; bgShowCard(); }
  else S2.delay = setTimeout(() => { S2.shown = i; S2.warm = true; bgShowCard(); }, 380);
}
function bgShowCard() {
  const el = document.querySelector('.bg .bg-card'), S2 = bgState, i = S2.shown;
  if (!el) return;
  if (i === null || !S2.commits[i]) { el.classList.remove('open'); return; }
  const c = S2.commits[i], below = i < S2.commits.length - 3;
  el.innerHTML = `<div class="bg-card-in ${below ? '' : 'up'}"><div class="bg-card-top">${bgAvatar(c.author, 'md')}<div><p class="bg-card-msg">${esc(c.message)}</p><p class="bg-card-meta">${esc(c.author)} · ${esc(bgLong(c.date))}</p></div></div>
    ${c.body ? `<p class="bg-card-body">${esc(c.body)}</p>` : ''}
    <div class="bg-card-foot"><span class="bg-dot" style="background:${bgTone(S2.laneOf[i])}"></span><span class="bg-card-sha">${esc(c.sha.slice(0, 10))}</span>
      ${c.parents.length > 1 ? `<span class="bg-card-merge">merges ${c.parents.map(p => p.slice(0, 7)).join(' + ')}</span>`
        : c.stats ? `<span class="bg-card-stats"><span class="add">+${c.stats.additions}</span><span class="del">−${c.stats.deletions}</span><span class="files">· ${plural(c.stats.files, 'file')}</span></span>` : ''}</div></div>`;
  el.style.left = (S2.graphW + 2) + 'px';
  el.style.maxWidth = `calc(100% - ${S2.graphW + 14}px)`;
  el.style.transform = `translateY(${below ? bgY(i) + BG.ROW / 2 + 6 : bgY(i) - BG.ROW / 2 - 6}px)`;
  el.classList.add('open');
}
document.addEventListener('mouseover', (e) => { const r = e.target.closest?.('.bg-row'); if (r && +r.dataset.bgrow !== bgState.hover) bgSetHover(+r.dataset.bgrow); });
document.addEventListener('mouseout', (e) => { const b = e.target.closest?.('.bg-body'); if (b && !b.contains(e.relatedTarget)) bgSetHover(null); });
document.addEventListener('focusin', (e) => { const r = e.target.closest?.('.bg-row'); if (r) bgSetHover(+r.dataset.bgrow); });
document.addEventListener('focusout', (e) => { if (e.target.closest?.('.bg-row') && !e.relatedTarget?.closest?.('.bg-row')) bgSetHover(null); });
document.addEventListener('keydown', (e) => {
  const r = e.target.closest?.('.bg-row'); if (!r) return;
  const i = +r.dataset.bgrow, n = bgState.commits.length;
  const next = e.key === 'ArrowDown' ? i + 1 : e.key === 'ArrowUp' ? i - 1 : e.key === 'Home' ? 0 : e.key === 'End' ? n - 1 : -1;
  if (next >= 0 && next < n) { e.preventDefault(); e.stopPropagation(); r.tabIndex = -1; const t = document.querySelector(`.bg-row[data-bgrow="${next}"]`); t.tabIndex = 0; t.focus(); }
  else if (e.key === 'Enter') { e.preventDefault(); bgSelect(i); }
  else if (e.key === 'Escape') bgSetHover(null);
}, true);
// Clicking a commit opens it on GitHub or GitLab, or copies its hash
function bgSelect(i) {
  const c = bgState.commits[i]; if (!c) return;
  if (/github\.com|gitlab\.com/.test(bgState.web || '')) return openWeb(`${bgState.web.replace(/\/$/, '')}/commit/${c.sha}`);
  act(() => bridge.copy(c.sha)).then(() => toast(`Copied ${c.sha.slice(0, 10)}`));
}
document.addEventListener('click', (e) => { const r = e.target.closest?.('.bg-row'); if (r) bgSelect(+r.dataset.bgrow); });

const CI = { success: ['on', 'passed'], failure: ['err', 'failed'], cancelled: ['off', 'cancelled'], skipped: ['off', 'skipped'], timed_out: ['err', 'timed out'], action_required: ['warn', 'needs action'] };
function hostingRow(h) {
  if (!h) return '';
  if (h.problem) return `<p class="note" style="margin:8px 0 0">${icon.info} ${esc(h.problem)}</p>`;
  const prs = (h.pull_requests || []).map(p => `<a class="hchip" data-openweb="${esc(p.url)}" title="${esc(p.title)}">${brand('github')}#${p.number} ${esc(p.title.length > 34 ? p.title.slice(0, 34) + '…' : p.title)}${p.isDraft ? ' · draft' : p.reviewDecision === 'APPROVED' ? ' · approved' : p.reviewDecision === 'CHANGES_REQUESTED' ? ' · changes requested' : ''}</a>`).join('');
  const r = (h.runs || [])[0];
  const ci = r ? (() => { const [state, word] = r.status !== 'completed' ? ['busy', 'running'] : (CI[r.conclusion] || ['off', r.conclusion]); return `<a class="hchip" data-openweb="${esc(r.url)}" title="${esc(r.workflowName)}">${UI.status(state, `CI ${word}`)}<span class="ellipsis">${esc(r.workflowName)}</span></a>`; })() : '';
  return prs || ci ? `<div class="hrow">${prs}${ci}</div>` : `<p class="note" style="margin:8px 0 0">No open pull request for this branch on GitHub.</p>`;
}
function showBranchMenu(anchor, folder, name, remote) {
  const st = S.git[folder]?.st, dirty = (st?.files || []).length > 0;
  const current = !remote && st?.branch === name;
  menuAt(anchor, `${current ? `<button disabled>${icon.check}<span>Current branch</span></button>` : `<button data-gswitch="${esc(name)}" data-gfolder="${esc(folder)}" ${dirty ? 'disabled' : ''} title="${dirty ? 'Commit or stash your changes first' : ''}">${icon.branch}<span>${remote ? 'Check out' : 'Switch to'} ${esc(name)}</span></button>`}
    ${dirty && !current ? `<div class="menu-note">${plural(st.files.length, 'uncommitted change')}: commit or stash them first. LocalSites never discards your work.</div>` : ''}
    <button data-copy="${esc(name)}">${icon.copy}<span>Copy name</span></button>`);
}

/* --- databases --- */
/* --- database tools: export, import, backups, duplicate, empty, delete, for every kind of database --- */
const MORE = '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="5" cy="12" r="1.6" fill="currentColor"/><circle cx="12" cy="12" r="1.6" fill="currentColor"/><circle cx="19" cy="12" r="1.6" fill="currentColor"/></svg>';
const pgConnID = (db) => 'localsites-pg-' + db.toLowerCase().replace(/_/g, '-').slice(0, 26); // same as engine.PGConnID
const fileStamp = () => new Date().toISOString().slice(0, 16).replace(/[-:]/g, '').replace('T', '-');
const baseName = (p) => String(p).split('/').pop();
// A dialog with its own buttons (data-r): resolves with collect(box) on OK, null on cancel
function formDialog(html, collect, okLabel) {
  return new Promise((resolve) => {
    const ov = $('#keysOverlay'), box = $('#keysDialog');
    box.innerHTML = html + `<div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn ghost" data-r="cancel">Cancel</button><button class="btn primary" data-r="ok">${esc(okLabel)}</button></div>`;
    ov.classList.add('open');
    const done = (v) => { ov.classList.remove('open'); ov.onmousedown = null; box.onkeydown = null; resolve(v); };
    ov.onmousedown = (e) => { if (e.target === ov) done(null); };
    box.onkeydown = (e) => { if (e.key === 'Escape') { e.preventDefault(); done(null); } if (e.key === 'Enter' && e.target.tagName === 'INPUT' && e.target.type === 'text') done(collect(box)); };
    box.querySelector('[data-r="cancel"]').onclick = () => done(null);
    box.querySelector('[data-r="ok"]').onclick = () => done(collect(box));
    (box.querySelector('input[type="text"]') || box.querySelector('[data-r="ok"]')).focus();
  });
}
async function pickSavePath(name) {
  if (window.go?.main?.App?.ChooseSaveSQL) return (await window.go.main.App.ChooseSaveSQL(name)) || null;
  return askText('Save the export as (full path)', '/tmp/' + name, 'Export'); // the browser preview has no file dialog
}
async function pickOpenPath() {
  if (window.go?.main?.App?.ChooseOpenSQL) return (await window.go.main.App.ChooseOpenSQL()) || null;
  return askText('SQL file to import (full path)', '/path/to/dump.sql', 'Choose');
}
async function exportFlow(label, base, call) {
  const opt = await formDialog(`<h3>Export ${esc(label)}</h3><p class="note">Saves a .sql file you can import anywhere: here, on a server, or in another app.</p>
    <div class="choice" role="radiogroup" aria-label="What to export" style="margin-top:12px">${[['full', 'Structure and data', 'Tables, rows, views and routines.'], ['structure', 'Structure only', 'Tables and views without rows. Good for a fresh copy.'], ['data', 'Data only', 'Rows only, for tables that already exist.']].map(([v, l, d], i) =>
      `<label class="choice-row ${i ? '' : 'on'}"><input type="radio" name="xmode" value="${v}" ${i ? '' : 'checked'}><span><span class="label">${l}</span><span class="desc">${d}</span></span></label>`).join('')}</div>
    <label class="form-row note" style="gap:8px;margin-top:12px"><input type="checkbox" id="xgz" checked> Compress (.sql.gz), usually 5 to 10 times smaller</label>`,
    (box) => ({ mode: box.querySelector('input[name="xmode"]:checked').value, gz: box.querySelector('#xgz').checked }), 'Choose where to save');
  if (!opt) return;
  const file = await pickSavePath(`${base}-${fileStamp()}${opt.mode === 'full' ? '' : '-' + opt.mode}.sql${opt.gz ? '.gz' : ''}`);
  if (!file) return;
  toast(`Exporting ${label}…`);
  const r = await act(() => call({ file, mode: opt.mode, overwrite: true }), 'Export');
  if (r) toast(`${label} exported to ${baseName(r.file)} (${r.bytes >= 1 << 20 ? (r.bytes / (1 << 20)).toFixed(1) + ' MB' : Math.max(1, Math.round(r.bytes / 1024)) + ' KB'})`);
}
async function importFlow(label, backedUp, call) {
  const file = await pickOpenPath(); if (!file) return false;
  if (!await confirmBox(`Import ${baseName(file)} into ${label}?`, `${backedUp} Tables in the file replace tables with the same names; other tables stay. Lines that would create or switch to another database are skipped, so everything lands in ${label}.`, 'Import')) return false;
  toast(`Importing ${baseName(file)}… large files can take a few minutes`);
  const r = await act(() => call(file), 'Import');
  if (r) logToast(r);
  return !!r;
}
const sizeOf = (b) => b >= 1 << 20 ? (b / (1 << 20)).toFixed(1) + ' MB' : Math.max(1, Math.round(b / 1024)) + ' KB';
async function backupsDialog(name) {
  const list = await act(() => bridge.call('db.backups', { name }), 'Backups'); if (!list) return;
  const ov = $('#keysOverlay'), box = $('#keysDialog');
  box.innerHTML = `<h3>Backups of ${esc(name)}</h3><p class="note">Made with Back up now, and automatically before imports, restores, emptying and deleting. Restoring backs up what is there first.</p>
    ${list.length ? `<div class="bk-list">${list.map(b => `<div class="bk"><div><div>${esc(new Date(b.time).toLocaleString())}</div><div class="note">${esc(sizeOf(b.bytes))}${b.note ? ' · ' + esc(b.note) : ''}</div></div>${UI.btn('Restore', { attrs: `data-r="restore" data-file="${esc(b.file)}"` })}</div>`).join('')}</div>` : '<p class="note">No backups yet.</p>'}
    <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn" data-r="now">${icon.backup}Back up now</button><button class="btn primary" data-r="close">Close</button></div>`;
  ov.classList.add('open');
  const close = () => { ov.classList.remove('open'); ov.onmousedown = null; box.onkeydown = null; };
  ov.onmousedown = (e) => { if (e.target === ov) close(); };
  box.onkeydown = (e) => { if (e.key === 'Escape') { e.preventDefault(); close(); } };
  box.querySelector('[data-r="close"]').onclick = close;
  box.querySelector('[data-r="now"]').onclick = async () => { const r = await act(() => bridge.call('db.backup', { name }), 'Back up'); if (r) { toast('Backup saved: ' + baseName(r.file)); close(); backupsDialog(name); } };
  box.querySelectorAll('[data-r="restore"]').forEach(b => { b.onclick = async () => {
    close();
    if (!await confirmBox(`Restore ${name}?`, `${name} is replaced with the backup from ${new Date(list.find(x => x.file === b.dataset.file).time).toLocaleString()}. What is there now is backed up first.`, 'Restore')) return;
    const r = await act(() => bridge.call('db.restore', { name, file: b.dataset.file }), 'Restore'); if (r) { logToast(r); await loadDBs(); render(); }
  }; });
  box.querySelector('[data-r="close"]').focus();
}
function showDbMenu(anchor, kind, name) {
  const d = (kind === 'pg' ? S.pgdbs : S.dbs).find(x => x.name === name); if (!d) return;
  const apps = kind === 'mysql' && d.owner ? (S.apps || []).filter(a => a.kind === 'database') : [];
  const item = (act, ic, label, extra = '') => `<button data-dbact="${act}|${kind}|${esc(name)}" ${extra}>${icon[ic] || ic}${label}</button>`;
  menuAt(anchor, [
    kind === 'mysql' && d.owner ? item('connect', 'plugZap', 'Connect settings…') : '',
    ...apps.map(a => `<button data-dbopenin="${esc(name)}|${esc(a.id)}">${icon.external}Open in ${esc(a.name)}</button>`),
    kind === 'pg' ? item('copyurl', 'copy', 'Copy connection URL') : '',
    '<hr>',
    item('export', 'download2', 'Export…'), item('import', 'import', 'Import SQL file…'),
    kind === 'mysql' ? item('backups', 'history2', 'Backups and restore…') : '',
    '<hr>',
    item('duplicate', 'copy', 'Duplicate…'), item('empty', 'eraser', 'Empty…'), item('drop', 'trash', 'Delete…', 'class="danger"'),
  ].join('').replace(/^<hr>|<hr>(?=<hr>)/g, ''));
}
async function dbAction(action, kind, name) {
  const api = kind === 'pg' ? 'pg' : 'db', label = name, after = async () => { await loadDBs(); render(); };
  switch (action) {
    case 'connect': return mysqlConnectDialog(name);
    case 'copyurl': { const port = (S.status?.addons || []).find(a => a.id === 'postgres')?.port || 5433; await act(() => bridge.copy(`postgresql://postgres@127.0.0.1:${port}/${name}`)); return toast('Connection URL copied'); }
    case 'export': return exportFlow(label, name, (o) => bridge.call(api + '.export', { name, ...o }));
    case 'import': if (await importFlow(label, `A backup of ${name} is made first, so you can undo this from Backups.`, (file) => bridge.call(api + '.import', { name, file }))) await after(); return;
    case 'backups': return backupsDialog(name);
    case 'duplicate': {
      const nn = await askText(`Duplicate ${name}`, `${name}_copy`, 'Duplicate'); if (!nn) return;
      const r = await act(() => bridge.call(api + '.duplicate', { name, new_name: nn }), 'Duplicate'); if (r) { logToast(r); await after(); } return;
    }
    case 'empty': {
      if (!await confirmBox(`Empty ${name}?`, `Every table in ${name} is removed. The database${kind === 'mysql' ? ' and its login stay' : ' stays'}. A backup is made first.`, 'Empty database')) return;
      const r = await act(() => bridge.call(api + '.empty', { name }), 'Empty'); if (r) { logToast(r); await after(); } return;
    }
    case 'drop': {
      const typed = await askText(`Delete ${name}? Type its name to confirm`, name, 'Delete database');
      if (typed === null) return;
      if (typed !== name) return toast('The name didn’t match, so nothing was deleted', 'warn');
      const r = await act(() => bridge.call(api + '.drop', { name }), 'Delete'); if (r) { logToast(r); await after(); } return;
    }
  }
}
function viewDatabases() {
  if (S.dbs === null) return emptyState({ ic: '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>', title: 'Starting LocalSites MySQL…', text: 'This takes a few seconds. The very first start sets up MySQL and can take up to a minute.' });
  if (S.dbErr) return emptyState({ ic: 'alertCircle', title: 'MySQL 8.4 is not available', text: esc(S.dbErr), actions: `<button class="btn" data-view="doctor">${icon.doctor}Run Doctor</button>` });
  if (!S.dbs.length) return emptyState({ fig: 'drawer', ic: 'db', title: 'No databases in LocalSites MySQL', text: 'Your projects can keep using MAMP PRO’s MySQL. Create a database here when a project should have its own, or copy one from MAMP PRO.',
    actions: `<button class="btn primary" data-cmd="newdb">${icon.plus}New database</button>` }) + startCards('Or start from', [
      { ic: 'import', title: 'Copy from MAMP PRO', text: 'Read-only copy, verified row by row.', attrs: 'data-view="import"' },
      { ic: 'table', title: 'Browse MAMP PRO data', text: 'Look through your existing databases without changing them.', attrs: 'data-dataconn="mamp-pro"' },
      { ic: 'plus', title: 'Connect another server', text: 'MySQL or PostgreSQL (also over SSH), or a SQLite file.', attrs: 'data-cmd="dnewconnview"' }]);
  const row = (kind, d) => `<tr class="link" data-${kind === 'pg' ? 'pgopen' : 'dbopen'}="${esc(d.name)}" title="Open in the data browser">
      <td class="c-name"><span class="dbn">${icon.db}<b title="${esc(d.name)}">${esc(d.name)}</b></span></td><td class="num c-tables">${d.tables}</td><td class="num nowrap">${d.size_mb.toFixed(1)} MB</td>
      <td class="c-login">${kind === 'pg' ? '<span class="chip mono">postgres</span>' : d.owner ? `<span class="chip mono" title="${esc(d.owner)}">${esc(d.owner)}</span>` : '<span class="note">root only</span>'}</td>
      <td class="c-acts"><div class="acts"><button class="btn sm" data-${kind === 'pg' ? 'pgopen' : 'dbopen'}="${esc(d.name)}" title="Open in the data browser">${icon.table}<span class="lbl">Open</span></button>${UI.iconBtn(MORE, `More for ${d.name}: export, import, backups, duplicate, delete`, `data-dbmore="${kind}|${esc(d.name)}"`)}</div></td></tr>`;
  const table = (kind, list) => `<div class="card"><table class="t dbt"><thead><tr><th>Database</th><th class="num c-tables">Tables</th><th class="num">Size</th><th class="c-login">Login</th><th><span class="sr">Actions</span></th></tr></thead><tbody>${list.map(d => row(kind, d)).join('')}</tbody></table></div>`;
  const pg = S.pgdbs === null || S.pgdbs === undefined ? '' : `<div class="db-sect">${UI.section({ title: 'PostgreSQL', note: S.pgErr ? `<span role="alert">${esc(S.pgErr)}</span>` : 'The PostgreSQL add-on. Log in as postgres from this Mac, no password.',
      right: UI.btn('New database', { ic: 'plus', attrs: 'data-cmd="pgnew"' }) })}${S.pgdbs.length ? table('pg', S.pgdbs) : S.pgErr ? '' : '<p class="note">No databases yet.</p>'}</div>`;
  return `<div class="pad">${pg ? UI.section({ title: 'LocalSites MySQL 8.4', note: 'Each database has its own login that can only see that database. Passwords live in the system keychain.' }) : ''}${table('mysql', S.dbs)}
    <p class="note" style="margin-top:12px">Click a database to browse it (read-only until you switch to Edit). Use ⋯ to export, import a .sql file, restore a backup, duplicate, empty or delete. Anything that removes data is backed up first.</p>${pg}</div>`;
}

/* --- data (database browser) --- */
const D = () => S.data;
const dcall = (m, p = {}) => bridge.call(m, { id: D().conn, ...p });
const pending = () => { const e = D().ed; return Object.keys(e.upd).length + e.del.size + e.ins.length; };
// Ask before throwing away staged edits (table switch, paging, sort, filter, leaving Edit)
async function guardEdits() {
  const n = pending();
  if (!n) return true;
  if (!await confirmBox(`Discard ${plural(n, 'unsaved change')}?`, 'Nothing has been written to the database yet. Discarding keeps the data exactly as it is.', 'Discard')) return false;
  D().ed = newEdits(); return true;
}
async function dataInit() {
  const d = D();
  if (!d.conns) { d.conns = await act(() => bridge.call('data.connections')) || []; }
  if (!d.conns.some(c => c.id === d.conn)) d.conn = d.conns[0]?.id || '';
  if (d.conn && !d.info && !d.err) await dataConnect();
  render();
}
// Open a database (from Databases or the sidebar) in the data browser
async function openDatabase(connId, schema) {
  const d = D();
  if (!await guardEdits()) return;
  if (schema) store.set('dataSchema.' + connId, schema);
  if (d.conn === connId && d.info && schema) { d.schema = schema; navigate('data'); await act(dataLoadTables); return renderView(); }
  d.conn = connId; d.info = null; d.err = ''; d.mode = 'view';
  navigate('data');
}
async function dataConnect() {
  const d = D();
  Object.assign(d, { info: null, err: '', needPw: false, schemas: [], tables: [], table: '', result: null, structure: null, qres: null, qerr: '', mode: 'view', ed: newEdits() });
  store.set('dataConn', d.conn);
  renderView();
  try {
    d.info = await dcall('data.test');
    d.schemas = await dcall('data.schemas');
    const remembered = store.get('dataSchema.' + d.conn, '');
    d.schema = d.schemas.includes(remembered) ? remembered : (d.schemas.find(x => x === 'public') || d.schemas[0] || '');
    if (d.schema) await dataLoadTables();
  } catch (e) {
    d.err = e.message;
    d.needPw = /refused the login|password/i.test(e.message);
  }
  renderView(); renderSide();
}
async function dataLoadTables() {
  const d = D();
  store.set('dataSchema.' + d.conn, d.schema);
  d.tables = await act(() => dcall('data.tables', { schema: d.schema })) || [];
  d.table = ''; d.result = null; d.structure = null; d.ed = newEdits(); d.cols = {};
}
async function dataOpenTable(name) {
  const d = D();
  if (!await guardEdits()) return;
  Object.assign(d, { table: name, page: 0, order: '', desc: false, filters: [], result: null, structure: null, ed: newEdits(), rb: null }); rbApplied = '';
  if (d.tab === 'sql') d.tab = 'rows';
  renderView();
  d.structure = await act(() => dcall('data.structure', { schema: d.schema, table: name }));
  if (d.structure) d.cols[name] = d.structure.columns.map(c => c.name);
  if (d.mode === 'edit' && !pkCols().length) d.mode = 'view';
  await dataLoadRows();
}
async function dataLoadRows() {
  const d = D();
  if (!d.table) return;
  d.busy = true; renderView();
  const where = d.rb ? rbWire(d.rb.root) : null;
  try { d.result = await dcall('data.rows', { schema: d.schema, table: d.table, limit: d.size, offset: d.page * d.size, order_by: d.order, desc: d.desc, filters: d.filters, ...(where ? { where } : {}) }); d.err = ''; }
  catch (e) { d.result = { error: e.message }; }
  d.busy = false; d.ed = newEdits(); renderView();
}
const SQLHIST = 60;
async function dataRunSQL() {
  const d = D();
  if (!d.sql.trim() || d.busy) return;
  d.busy = true; d.qerr = ''; d.ac = null; renderView();
  try {
    d.qres = await dcall('data.query', { schema: d.schema, sql: d.sql, allow_write: d.allowWrite });
    const h = store.get('sqlHistory', []).filter(x => x.sql !== d.sql.trim());
    h.unshift({ sql: d.sql.trim(), conn: d.conn, schema: d.schema, at: Date.now() }); store.set('sqlHistory', h.slice(0, SQLHIST));
  } catch (e) { d.qres = null; d.qerr = e.message; }
  d.busy = false; renderView();
  if (d.allowWrite && d.qres && !d.qres.columns) { d.tables = await act(() => dcall('data.tables', { schema: d.schema })) || d.tables; renderView(); }
}
function grid(res, sortable) {
  if (!res) return '';
  if (res.error) return `<p class="note warn" role="alert" style="padding:16px 20px">${esc(res.error)}</p>`;
  if (!res.columns) return `<p class="note" style="padding:16px 20px">${res.affected ?? 0} row${res.affected === 1 ? '' : 's'} changed · ${res.ms} ms</p>`;
  const d = D();
  const head = res.columns.map(c => {
    const on = sortable && d.order === c;
    return `<th ${sortable ? `data-sortcol="${esc(c)}" style="cursor:pointer"` : ''} title="${esc(c)}">${esc(c)}${on ? (d.desc ? ' ↓' : ' ↑') : ''}</th>`;
  }).join('');
  const body = res.rows.map((r, i) => `<tr><td class="rn">${(sortable ? d.page * d.size : 0) + i + 1}</td>${r.map(v => v === null ? '<td class="null">NULL</td>' : `<td title="${esc(v.length > 80 ? v.slice(0, 500) : '')}">${esc(v.length > 200 ? v.slice(0, 200) + '…' : v)}</td>`).join('')}</tr>`).join('');
  return `<div class="grid-wrap"><table class="grid"><thead><tr><th class="rn">#</th>${head}</tr></thead><tbody>${body || `<tr><td colspan="${res.columns.length + 1}" class="note" style="padding:16px">No rows</td></tr>`}</tbody></table></div>`;
}
/* Edit mode: cells change only in this grid until "Review & save" shows the SQL and saves it in one transaction */
const pkCols = () => (D().structure?.columns || []).filter(c => c.key === 'PRI').map(c => c.name);
const MAXCELL = 4000;
function colLocked(name) {
  const c = (D().structure?.columns || []).find(x => x.name === name);
  return !c || /blob|binary|bytea|\bbit\b|geometry|point|polygon/i.test(c.type) || /generated|virtual|stored/i.test(c.extra || '');
}
const cellLocked = (v) => typeof v === 'string' && (v.length > MAXCELL || /^0x[0-9a-f]{0,128}…/.test(v));
function cellHTML(v, ref, changed, editable) {
  const txt = v === null || v === undefined ? '<span class="nul">NULL</span>' : esc(v.length > 200 ? v.slice(0, 200) + '…' : v);
  return `<td class="${v === null || v === undefined ? 'null' : ''} ${changed ? 'changed' : ''} ${editable ? 'ed' : 'lockc'}" ${editable ? `data-cell="${ref}"` : 'title="This value can’t be edited here (binary, generated or too long)"'}>${txt}</td>`;
}
function rowsGrid() {
  const d = D(), r = d.result;
  if (!r) return '<div class="pad"><span class="skeleton" style="width:50%"></span></div>';
  if (r.error) return `<p class="note warn" role="alert" style="padding:16px 20px">${esc(r.error)}</p>`;
  const edit = d.mode === 'edit', cols = r.columns, e = d.ed;
  const head = cols.map(c => `<th data-sortcol="${esc(c)}" style="cursor:pointer" title="${esc(c)}">${pkCols().includes(c) ? icon.keyIcon : ''}${esc(c)}${d.order === c ? (d.desc ? ' ↓' : ' ↑') : ''}</th>`).join('');
  const ins = edit ? e.ins.map((o, k) => `<tr class="ins"><td class="rn"><button class="rowx" data-insdel="${k}" title="Remove this new row">${icon.x}</button></td>${cols.map((c, j) => cellHTML(c in o ? o[c] : undefined, `i:${k}:${j}`, c in o, !colLocked(c))).join('')}</tr>`).join('') : '';
  const body = r.rows.map((row, i) => {
    const del = e.del.has(i), up = e.upd[i] || {};
    return `<tr class="${del ? 'del' : ''}"><td class="rn">${edit ? `<button class="rowx" data-rowdel="${i}" title="${del ? 'Keep this row' : 'Delete this row'}">${del ? icon.undo : icon.trash}</button>` : ''}<span>${d.page * d.size + i + 1}</span></td>${row.map((v, j) => {
      const c = cols[j], ch = c in up;
      return edit ? cellHTML(ch ? up[c] : v, `r:${i}:${j}`, ch, !del && !colLocked(c) && !cellLocked(v)) : (v === null ? '<td class="null">NULL</td>' : `<td title="${esc(v.length > 80 ? v.slice(0, 500) : '')}">${esc(v.length > 200 ? v.slice(0, 200) + '…' : v)}</td>`);
    }).join('')}</tr>`;
  }).join('');
  return `<div class="grid-wrap"><table class="grid ${edit ? 'editing' : ''}"><thead><tr><th class="rn">#</th>${head}</tr></thead><tbody>${ins}${body || (ins ? '' : `<tr><td colspan="${cols.length + 1}" class="note" style="padding:16px">No rows</td></tr>`)}</tbody></table></div>`;
}
// Start editing a cell in place
function editCell(td) {
  const d = D(), [kind, a, j] = td.dataset.cell.split(':'), i = +a, col = d.result.columns[+j];
  const cur0 = kind === 'i' ? d.ed.ins[i][col] : (col in (d.ed.upd[i] || {}) ? d.ed.upd[i][col] : d.result.rows[i][+j]);
  td.classList.add('editing');
  td.innerHTML = `<input class="cell-in" spellcheck="false" value="${esc(cur0 ?? '')}" placeholder="${cur0 === null || cur0 === undefined ? 'NULL' : ''}" aria-label="${esc(col)}"><button class="nullbtn" title="Set to NULL" tabindex="-1">NULL</button>`;
  const inp = td.querySelector('input'); inp.focus(); inp.select();
  let done = false;
  const commit = (val, move) => {
    if (done) return; done = true;
    if (val !== false) {
      if (kind === 'i') { if (val === undefined) delete d.ed.ins[i][col]; else d.ed.ins[i][col] = val; }
      else {
        const orig = d.result.rows[i][+j], up = d.ed.upd[i] || (d.ed.upd[i] = {});
        if (val === orig || (val === '' && orig === null && cur0 === null)) delete up[col]; else up[col] = val;
        if (!Object.keys(up).length) delete d.ed.upd[i];
      }
    }
    renderView();
    if (move) { const cells = [...document.querySelectorAll('td[data-cell]')], k = cells.findIndex(c => c.dataset.cell === td.dataset.cell); const n = cells[k + move]; if (n) editCell(n); }
  };
  inp.onkeydown = (ev) => {
    if (ev.key === 'Enter') { ev.preventDefault(); commit(inp.value); }
    else if (ev.key === 'Escape') { ev.preventDefault(); ev.stopPropagation(); commit(false); }
    else if (ev.key === 'Tab') { ev.preventDefault(); commit(inp.value, ev.shiftKey ? -1 : 1); }
  };
  inp.onblur = () => setTimeout(() => commit(inp.value), 120);
  td.querySelector('.nullbtn').onmousedown = (ev) => { ev.preventDefault(); commit(null); };
}
function buildChanges() {
  const d = D(), r = d.result, pk = pkCols(), e = d.ed;
  const keyOf = (i) => Object.fromEntries(pk.map(c => [c, r.rows[i][r.columns.indexOf(c)]]));
  return { schema: d.schema, table: d.table,
    updates: Object.entries(e.upd).filter(([i]) => !e.del.has(+i)).map(([i, set]) => ({ key: keyOf(+i), set })),
    deletes: [...e.del].map(keyOf), inserts: e.ins.filter(o => Object.keys(o).length) };
}
async function reviewAndSave() {
  const ch = buildChanges();
  if (!ch.updates.length && !ch.deletes.length && !ch.inserts.length) return toast('Nothing to save: new rows are empty', 'warn');
  const sql = await act(() => dcall('data.preview', ch), 'Review');
  if (!sql) return;
  const n = ch.updates.length + ch.deletes.length + ch.inserts.length;
  const ok = await dialog(`<h3>Save ${plural(n, 'change')} to ${esc(D().table)}?</h3>
    <p class="note">These statements run in one transaction. If any row changed or disappeared since you loaded it, nothing is saved.</p>
    <pre class="mini sqlprev">${esc(sql.join('\n'))}</pre>
    ${ch.deletes.length ? `<p class="note warn">${icon.alert} ${plural(ch.deletes.length, 'row')} will be deleted.</p>` : ''}
    <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn ghost" data-dlg="cancel">Keep editing</button><button class="btn primary ${ch.deletes.length ? 'danger-fill' : ''}" data-dlg="ok">Save ${plural(n, 'change')}</button></div>`);
  if (!ok) return;
  const res = await act(() => dcall('data.save', ch), 'Save');
  if (!res) return;
  toast(`Saved to ${D().table}: ${[res.updated && res.updated + ' updated', res.inserted && res.inserted + ' added', res.deleted && res.deleted + ' deleted'].filter(Boolean).join(', ')} · ${res.ms} ms`);
  D().ed = newEdits();
  await dataLoadRows();
  D().tables = await act(() => dcall('data.tables', { schema: D().schema })) || D().tables; renderView();
}
/* --- Rule Builder. A nested filter that
   reads like a sentence: Where, and, or, groups inside groups, drag or arrow-key rules into place, undo
   anything, and watch the SQL, JSON and plain English rewrite themselves. The service runs it with every
   value bound and every column checked (dbclient treeSQL).  --- */
const RB_MAX_DEPTH = 2;
let rbSeq = 0;
const rbId = () => 'n' + (++rbSeq);
// What kind of field a column is, from its SQL type
function rbKind(col) {
  const t = (col.type || '').toLowerCase();
  if (/^enum\(/.test(t)) return 'enum';
  if (/^(tinyint\(1\)|bool|boolean)/.test(t)) return 'boolean';
  if (/int|decimal|numeric|float|double|real|serial|money/.test(t)) return 'number';
  if (/date|time|year/.test(t)) return 'date';
  return 'text';
}
const rbEnumOptions = (col) => [...(col.type || '').matchAll(/'((?:[^']|'')*)'/g)].map(m => m[1].replace(/''/g, "'"));
const RB_OPS = {
  text: [['contains', 'contains'], ['notcontains', 'doesn’t contain'], ['=', 'equals'], ['!=', 'doesn’t equal'], ['starts', 'starts with'], ['ends', 'ends with'], ['in', 'is any of'], ['empty', 'is empty'], ['notempty', 'is not empty']],
  number: [['=', 'equals'], ['!=', 'doesn’t equal'], ['>', 'more than'], ['>=', 'at least'], ['<', 'less than'], ['<=', 'at most'], ['between', 'between'], ['in', 'is any of'], ['null', 'is empty'], ['notnull', 'is not empty']],
  date: [['=', 'is on'], ['>', 'is after'], ['<', 'is before'], ['between', 'is between'], ['null', 'is empty'], ['notnull', 'is not empty']],
  enum: [['=', 'equals'], ['!=', 'doesn’t equal'], ['in', 'is any of'], ['notin', 'is none of'], ['null', 'is empty']],
  boolean: [['=', 'is'], ['null', 'is empty']],
};
const RB_ICON = { text: 'textCursor', number: 'hash', date: 'calendar', enum: 'list', boolean: 'circleDot' };
const rbArity = (op) => ['null', 'notnull', 'empty', 'notempty'].includes(op) ? 'none' : op === 'between' ? 'two' : ['in', 'notin'].includes(op) ? 'list' : 'one';
const RB = () => D().rb;
function rbFields() { return (D().structure?.columns || []).map(c => ({ name: c.name, kind: rbKind(c), options: rbEnumOptions(c) })); }
const rbField = (name) => rbFields().find(f => f.name === name);
function rbNewRule() { const f = rbFields()[0]; return { id: rbId(), kind: 'rule', column: f?.name || '', op: RB_OPS[f?.kind || 'text'][0][0], value: '', values: [] }; }
function rbInit() { D().rb = { root: { id: rbId(), kind: 'group', combinator: 'and', not: false, children: [] }, past: [], future: [], tab: store.get('rbTab', 'sql'), open: true }; }
function rbFind(id, n = RB().root, parent = null, depth = 0) {
  if (n.id === id) return { node: n, parent, depth };
  if (n.kind === 'group') for (const ch of n.children) { const r = rbFind(id, ch, n, depth + 1); if (r) return r; }
  return null;
}
const rbClone = (n) => n.kind === 'group' ? { ...n, id: rbId(), children: n.children.map(rbClone) } : { ...n, id: rbId(), values: [...n.values] };
// Every change goes through here: history for undo, then the view and the rows
function rbChange(fn, { redraw = true } = {}) {
  const rb = RB();
  rb.past.push(JSON.stringify(rb.root)); if (rb.past.length > 60) rb.past.shift();
  rb.future = [];
  fn();
  rbAfter(redraw);
}
function rbUndo(dir = -1) {
  const rb = RB(), from = dir < 0 ? rb.past : rb.future, to = dir < 0 ? rb.future : rb.past;
  if (!from.length) return;
  to.push(JSON.stringify(rb.root)); rb.root = JSON.parse(from.pop());
  rbAfter(true);
}
function rbAfter(redraw) {
  if (redraw) rbRedraw(); else rbRedrawOut();
  clearTimeout(rbAfter.t); rbAfter.t = setTimeout(rbApply, 350);
}
// The tree the service runs: only complete rules (incomplete ones are skipped, as the output says)
function rbComplete(r) { const a = rbArity(r.op); return a === 'none' || (a === 'one' && r.value !== '') || (a === 'two' && r.values[0] && r.values[1]) || (a === 'list' && r.values.length > 0); }
function rbWire(n) {
  if (n.kind === 'rule') return rbComplete(n) ? { kind: 'rule', column: n.column, op: n.op, value: n.value, values: n.values.filter(v => v !== '') } : null;
  const children = n.children.map(rbWire).filter(Boolean);
  return children.length ? { kind: 'group', combinator: n.combinator, not: n.not, children } : null;
}
let rbApplied = '';
async function rbApply() {
  const w = JSON.stringify(rbWire(RB().root));
  if (w === rbApplied) return;
  if (!await guardEdits()) return;
  rbApplied = w; D().page = 0; dataLoadRows();
}
/* output: SQL (with the connection's own placeholders), JSON and plain English */
function rbDialect() { const c = D().conns?.find(x => x.id === D().conn); return c?.driver || 'mysql'; }
function rbEncode(n, params, dialect) {
  const qi = (c) => dialect === 'mysql' ? '`' + c.replace(/`/g, '``') + '`' : '"' + c.replace(/"/g, '""') + '"';
  const ph = (v) => { params.push(v); return dialect === 'postgres' ? '$' + params.length : '?'; };
  if (n.kind === 'group') {
    const parts = n.children.map(ch => rbEncode(ch, params, dialect)).filter(Boolean);
    if (!parts.length) return '';
    const s = parts.length === 1 && !n.not ? parts[0] : '(' + parts.join(n.combinator === 'or' ? ' OR ' : ' AND ') + ')';
    return n.not ? 'NOT ' + (parts.length === 1 ? '(' + parts[0] + ')' : s) : s;
  }
  if (!rbComplete(n)) return '';
  const q = qi(n.column), vs = n.values.filter(v => v !== '');
  switch (n.op) {
    case 'contains': return `${q} LIKE ${ph('%' + n.value + '%')}`;
    case 'notcontains': return `${q} NOT LIKE ${ph('%' + n.value + '%')}`;
    case 'starts': return `${q} LIKE ${ph(n.value + '%')}`;
    case 'ends': return `${q} LIKE ${ph('%' + n.value)}`;
    case 'in': return `${q} IN (${vs.map(ph).join(', ')})`;
    case 'notin': return `${q} NOT IN (${vs.map(ph).join(', ')})`;
    case 'between': return `${q} BETWEEN ${ph(vs[0])} AND ${ph(vs[1])}`;
    case 'null': return `${q} IS NULL`;
    case 'notnull': return `${q} IS NOT NULL`;
    case 'empty': return `(${q} IS NULL OR ${q} = '')`;
    case 'notempty': return `(${q} IS NOT NULL AND ${q} <> '')`;
    default: return `${q} ${n.op === '!=' ? '<>' : n.op} ${ph(n.value)}`;
  }
}
// The top-level AND and OR each start a new line
function rbPretty(sql) {
  let depth = 0, quoted = false, out = '';
  for (let i = 0; i < sql.length; i++) {
    const ch = sql[i];
    if (ch === "'") quoted = !quoted;
    if (!quoted && ch === '(') depth++;
    if (!quoted && ch === ')') depth--;
    if (!quoted && depth === 0) { const rest = sql.slice(i), j = rest.startsWith(' AND ') ? 'AND' : rest.startsWith(' OR ') ? 'OR' : null; if (j) { out += `\n  ${j} `; i += j.length + 1; continue; } }
    out += ch;
  }
  return out;
}
function rbOutput() {
  const d = D(), params = [], dialect = rbDialect(), root = RB().root;
  const where = rbEncode({ ...root, children: root.children }, params, dialect).replace(/^\((.*)\)$/s, (m, inner) => root.not ? m : inner);
  const table = (dialect === 'mysql' ? '`' + d.table + '`' : '"' + d.table + '"');
  return { sql: where ? `SELECT * FROM ${table}\nWHERE ${rbPretty(where)}` : `SELECT * FROM ${table}`, params, json: JSON.stringify(rbWire(root) || { kind: 'group', combinator: 'and', children: [] }, null, 2), text: rbEnglish(root) || 'Every row' };
}
// The same filter with its values written in, for the SQL tab
function rbInlined() {
  const o = rbOutput(), lit = (v) => /^-?\d+(\.\d+)?$/.test(v) ? v : "'" + String(v).replace(/'/g, "''") + "'";
  let i = 0;
  return o.sql.replace(/\$\d+|\?/g, (m) => lit(o.params[m === '?' ? i++ : +m.slice(1) - 1])) + '\nLIMIT 100';
}
function rbEnglish(n, top = true) {
  if (n.kind === 'group') {
    const parts = n.children.map(ch => rbEnglish(ch, false)).filter(Boolean);
    if (!parts.length) return '';
    const s = parts.join(n.combinator === 'or' ? ', or ' : ', and ');
    return n.not ? `none of (${s})` : top || parts.length === 1 ? s : `(${s})`;
  }
  if (!rbComplete(n)) return '';
  const op = (RB_OPS[rbField(n.column)?.kind || 'text'].find(o => o[0] === n.op) || [n.op, n.op])[1];
  const vs = n.values.filter(v => v !== ''), a = rbArity(n.op);
  return `${n.column} ${op}${a === 'none' ? '' : a === 'two' ? ` ${vs[0]} and ${vs[1]}` : a === 'list' ? ` ${vs.join(', ')}` : ` ${n.value}`}`;
}
const rbHiSQL = (sql) => esc(sql).replace(/(\$\d+|\?)|(&#39;(?:[^&]|&(?!#39;))*&#39;)|\b(SELECT|FROM|WHERE|AND|OR|NOT|IN|LIKE|BETWEEN|IS|NULL)\b/g,
  (m, p, s, k) => p ? `<span class="rb-p">${p}</span>` : s ? `<span class="rb-s">${s}</span>` : `<span class="rb-k">${k}</span>`);
const rbHiJSON = (j) => esc(j).replace(/(&quot;(?:[^&]|&(?!quot;))*&quot;)(\s*:)?|\b(-?\d+(?:\.\d+)?|true|false|null)\b/g,
  (m, s, colon, lit) => s ? (colon ? `<span class="rb-key">${s}${colon}</span>` : `<span class="rb-s">${s}</span>`) : `<span class="rb-p">${lit}</span>`);
/* view */
const RB_SEL = (attrs, opts, cur, cls = '') => `<label class="rb-sel ${cls}"><select ${attrs}>${opts.map(([v, l]) => `<option value="${esc(v)}" ${String(cur) === String(v) ? 'selected' : ''}>${esc(l)}</option>`).join('')}</select>${icon.chevronsUD || ''}</label>`;
function rbValueEditor(r, f) {
  const a = rbArity(r.op), kind = f?.kind || 'text', type = kind === 'number' ? 'text" inputmode="decimal' : kind === 'date' ? 'date' : 'text';
  const inp = (attr, v, ph, lab) => `<input class="rb-in" type="${type}" ${attr} value="${esc(v)}" placeholder="${ph}" spellcheck="false" autocomplete="off" aria-label="${esc(lab)}">`;
  if (a === 'none') return '';
  if (a === 'two') return `<div class="rb-two">${inp(`data-rbv="${r.id}" data-i="0"`, r.values[0] || '', kind === 'number' ? '0' : 'From', r.column + ' from')}<span>and</span>${inp(`data-rbv="${r.id}" data-i="1"`, r.values[1] || '', kind === 'number' ? '0' : 'To', r.column + ' to')}</div>`;
  if (kind === 'boolean') return RB_SEL(`data-rbval="${r.id}" aria-label="${esc(r.column)}"`, [['', 'Choose…'], ['1', 'Yes'], ['0', 'No']], r.value);
  if (kind === 'enum' && a === 'one') return RB_SEL(`data-rbval="${r.id}" aria-label="${esc(r.column)}"`, [['', 'Choose…'], ...f.options.map(o => [o, o])], r.value);
  if (kind === 'enum' && a === 'list') return `<div class="rb-multi" role="group" aria-label="${esc(r.column)} values">${f.options.map(o => `<button class="${r.values.includes(o) ? 'on' : ''}" data-rbopt="${r.id}" data-v="${esc(o)}" aria-pressed="${r.values.includes(o)}">${esc(o)}</button>`).join('')}</div>`;
  if (a === 'list') return inp(`data-rblist="${r.id}"`, r.values.join(', '), 'Comma, separated', r.column + ' values');
  return inp(`data-rbval="${r.id}"`, r.value, kind === 'number' ? '0' : 'Value', r.column);
}
function rbConnector(g, i, node) {
  const grp = node.kind === 'group' ? ' grp' : '';
  if (i === 0) return `<span class="rb-con${grp}">Where</span>`;
  if (i === 1) return `<button class="rb-con rb-toggle${grp}" data-rbcomb="${g.id}" aria-label="Combine with ${g.combinator === 'and' ? 'or' : 'and'} instead"><span>${g.combinator}</span>${icon.chevronsUD || ''}</button>`;
  return `<span class="rb-con rb-word${grp}">${g.combinator}</span>`;
}
function rbRule(r) {
  const fields = rbFields(), f = rbField(r.column), kind = f?.kind || 'text';
  return `<div class="rb-rule" data-rbnode="${r.id}"><button class="rb-grip" draggable="true" data-rbgrip="${r.id}" aria-label="Move rule on ${esc(r.column)}. Drag, or use the arrow keys.">${icon.grip}</button>
    ${RB_SEL(`data-rbcol="${r.id}" aria-label="Field"`, fields.map(x => [x.name, x.name]), r.column, 'f')}${''}
    ${RB_SEL(`data-rbop="${r.id}" aria-label="Operator"`, RB_OPS[kind], r.op, 'o')}
    <div class="rb-val ${rbArity(r.op) === 'none' ? 'hide' : ''}">${rbValueEditor(r, f)}</div>
    <button class="rb-more" data-rbmenu="${r.id}" aria-label="Actions for rule on ${esc(r.column)}">${MORE}</button></div>`;
}
function rbGroup(g, depth) {
  const list = `<div class="rb-list">${g.children.map((ch, i) => `<div class="rb-item">${rbConnector(g, i, ch)}<div class="rb-node">${ch.kind === 'group' ? rbGroup(ch, depth + 1) : rbRule(ch)}</div></div>`).join('')}
    ${g.children.length ? '' : `<div class="rb-empty" data-rbzone="${g.id}">${depth === 0 ? 'No rules yet, so every row matches.' : 'Empty group. Add a rule or drop one here.'}</div>`}</div>`;
  if (depth === 0) return `${list}<div class="rb-adds ${g.children.length ? 'ind' : ''}"><button class="btn sm" data-rbadd="${g.id}">${icon.plus}Add rule</button><button class="btn ghost sm" data-rbaddg="${g.id}">${icon.folderPlus}Add group</button></div>`;
  const summary = g.not ? 'None of these' : g.combinator === 'and' ? 'All of these' : 'Any of these';
  return `<div class="rb-group" data-rbnode="${g.id}"><div class="rb-gh"><button class="rb-grip" draggable="true" data-rbgrip="${g.id}" aria-label="Move group. Drag, or use the arrow keys.">${icon.grip}</button><span class="rb-sum">${summary}</span>
      <button class="rb-more" data-rbadd="${g.id}" aria-label="Add rule to group" title="Add rule">${icon.plus}</button><button class="rb-more" data-rbmenu="${g.id}" aria-label="Group actions">${MORE}</button></div>
    <div class="rb-gb">${list}</div></div>`;
}
function rbOutHTML() {
  const rb = RB(), o = rbOutput(), incomplete = (function count(n) { return n.kind === 'rule' ? (rbComplete(n) ? 0 : 1) : n.children.reduce((a, c) => a + count(c), 0); })(rb.root);
  const tab = rb.tab, body = tab === 'sql' ? `<pre class="rb-code">${rbHiSQL(o.sql)}</pre>${incomplete ? `<p class="note">Skipping ${plural(incomplete, 'incomplete rule')} until ${incomplete === 1 ? 'it has' : 'they have'} a value.</p>` : ''}
      ${o.params.length ? `<div class="rb-params">${o.params.map((p, i) => `<span><b>${rbDialect() === 'postgres' ? '$' + (i + 1) : '?' + (i + 1)}</b>${esc(JSON.stringify(p))}</span>`).join('')}</div>` : ''}`
    : tab === 'json' ? `<pre class="rb-code">${rbHiJSON(o.json)}</pre>` : `<p class="rb-text">${esc(o.text)}</p>`;
  return `<div class="rb-oh"><div class="rb-tabs" role="tablist">${[['sql', 'SQL'], ['json', 'JSON'], ['text', 'Plain English']].map(([k, l]) => `<button role="tab" data-rbtab="${k}" class="${tab === k ? 'on' : ''}" aria-selected="${tab === k}">${l}</button>`).join('')}</div>
    <span class="spacer"></span><button class="btn ghost sm" data-rbtosql="1" title="Open this query in the SQL tab, values written in">${icon.code}Edit as SQL</button><button class="rb-more" data-rbcopy="1" aria-label="Copy">${icon.copy}</button></div>
    <div class="rb-ob">${body}</div>`;
}
function rbCountText() {
  const d = D(), r = d.result, rules = (function count(n) { return n.kind === 'rule' ? 1 : n.children.reduce((a, c) => a + count(c), 0); })(RB().root);
  const all = d.tables.find(t => t.name === d.table)?.rows;
  if (!rules) return all ? `${all.toLocaleString()} rows, no filter` : 'No filter';
  return r && !r.error && r.total >= 0 ? `${r.total.toLocaleString()}${all ? ` of ${all.toLocaleString()}` : ''} rows match` : plural(rules, 'rule');
}
function ruleBuilder() {
  const d = D(); if (!d.rb) rbInit();
  const rb = RB();
  return `<div class="rb" id="rb"><div class="rb-h"><div><h3>Filter ${esc(d.table)}</h3><p class="rb-count">${esc(rbCountText())}</p></div><span class="spacer"></span>
      <button class="rb-more" data-rbundo="-1" ${rb.past.length ? '' : 'disabled'} title="Undo (${MOD}Z)" aria-label="Undo">${icon.undo}</button><button class="rb-more" data-rbundo="1" ${rb.future.length ? '' : 'disabled'} title="Redo (⇧${MOD}Z)" aria-label="Redo">${icon.redo || icon.fwd}</button></div>
    <div class="rb-body">${rbGroup(rb.root, 0)}</div><div class="rb-out" id="rbOut">${rbOutHTML()}</div></div>`;
}
// Redraw the builder in place, keeping focus on the same control
function rbRedraw() {
  const el = $('#rb'); if (!el) return renderView();
  const a = document.activeElement, key = a && el.contains(a) ? [...a.attributes].filter(x => x.name.startsWith('data-rb') || x.name === 'data-i').map(x => `[${x.name}="${CSS.escape(x.value)}"]`).join('') : '', pos = a?.selectionStart;
  el.outerHTML = ruleBuilder();
  if (key) { const n = $('#rb ' + key); if (n) { n.focus(); if (pos != null && n.setSelectionRange) try { n.setSelectionRange(pos, pos); } catch (_) { /* date inputs */ } } }
  $('#view')._html = viewData();
}
function rbRedrawOut() { const o = $('#rbOut'); if (o) o.innerHTML = rbOutHTML(); const c = $('#rb .rb-count'); if (c) c.textContent = rbCountText(); }
/* events */
function rbMove(id, delta) {
  const f = rbFind(id); if (!f?.parent) return;
  const sib = f.parent.children, i = sib.indexOf(f.node), j = i + delta;
  if (j < 0 || j >= sib.length) return;
  rbChange(() => { sib.splice(i, 1); sib.splice(j, 0, f.node); });
  setTimeout(() => $(`#rb [data-rbgrip="${id}"]`)?.focus());
}
function rbMenu(anchor, id) {
  const f = rbFind(id), g = f.node.kind === 'group';
  menuAt(anchor, g ? `<button data-rbm="addg|${id}" ${f.depth >= RB_MAX_DEPTH ? 'disabled' : ''}>${icon.folderPlus}<span>Add nested group</span></button><button data-rbm="dup|${id}">${icon.copy}<span>Duplicate group</span></button>
      <button data-rbm="not|${id}">${f.node.not ? icon.check : '<span style="width:15px"></span>'}<span>Match none of these</span></button><hr><button data-rbm="del|${id}" class="danger">${icon.trash}<span>Delete group</span></button>`
    : `<button data-rbm="dup|${id}">${icon.copy}<span>Duplicate</span></button><hr><button data-rbm="del|${id}" class="danger">${icon.trash}<span>Delete rule</span></button>`);
}
async function rbClick(t, d) {
  if (d.rbadd !== undefined) { const g = rbFind(d.rbadd).node; const r = rbNewRule(); rbChange(() => g.children.push(r)); return setTimeout(() => $(`#rb [data-rbcol="${r.id}"]`)?.focus()); }
  if (d.rbaddg !== undefined) { const g = rbFind(d.rbaddg).node; return rbChange(() => g.children.push({ id: rbId(), kind: 'group', combinator: 'or', not: false, children: [rbNewRule()] })); }
  if (d.rbcomb) { const g = rbFind(d.rbcomb).node; return rbChange(() => { g.combinator = g.combinator === 'and' ? 'or' : 'and'; }); }
  if (d.rbmenu) return rbMenu(t, d.rbmenu);
  if (d.rbm) {
    $('#menu').classList.remove('open');
    const [what, id] = d.rbm.split('|'), f = rbFind(id); if (!f) return;
    if (what === 'del') return rbChange(() => f.parent.children.splice(f.parent.children.indexOf(f.node), 1));
    if (what === 'dup') return rbChange(() => f.parent.children.splice(f.parent.children.indexOf(f.node) + 1, 0, rbClone(f.node)));
    if (what === 'not') return rbChange(() => { f.node.not = !f.node.not; });
    if (what === 'addg') return rbChange(() => f.node.children.push({ id: rbId(), kind: 'group', combinator: 'or', not: false, children: [rbNewRule()] }));
  }
  if (d.rbopt) { const r = rbFind(d.rbopt).node, v = d.v; return rbChange(() => { r.values = r.values.includes(v) ? r.values.filter(x => x !== v) : [...r.values, v]; }); }
  if (d.rbundo) return rbUndo(+d.rbundo);
  if (d.rbtab) { RB().tab = d.rbtab; store.set('rbTab', d.rbtab); return rbRedrawOut(); }
  if (d.rbcopy) { const o = rbOutput(), tab = RB().tab; await act(() => bridge.copy(tab === 'sql' ? o.sql : tab === 'json' ? o.json : o.text)); return toast('Copied'); }
  if (d.rbtosql) { const D2 = D(); D2.sql = rbInlined(); D2.tab = 'sql'; renderView(); return setTimeout(() => $('#dSQL')?.focus(), 20); }
  if (d.rbtoggle) { const D2 = D(); if (!D2.rb) { rbInit(); D2.rb.open = false; D2.rb.root.children.push(rbNewRule()); } D2.rb.open = !D2.rb.open; renderView(); return setTimeout(() => $('#rb [data-rbcol]')?.focus(), 30); }
}
document.addEventListener('change', (e) => {
  const t = e.target, d = t.dataset;
  if (d.rbcol) { const r = rbFind(d.rbcol).node, from = rbField(r.column)?.kind, f = rbField(t.value);
    return rbChange(() => { r.column = t.value; if (from !== f.kind || !RB_OPS[f.kind].some(o => o[0] === r.op)) r.op = RB_OPS[f.kind][0][0]; r.value = ''; r.values = []; }); } // another kind of column: its own first condition
  if (d.rbop) { const r = rbFind(d.rbop).node, from = rbArity(r.op), to = rbArity(t.value); return rbChange(() => { r.op = t.value; if (from !== to) { if (to === 'list' && r.value) r.values = [r.value]; else if (to === 'one' && r.values[0]) r.value = r.values[0]; if (to === 'two') r.values = [r.value || '', '']; } }); }
  if (d.rbval && t.tagName === 'SELECT') { const r = rbFind(d.rbval).node; return rbChange(() => { r.value = t.value; }); }
});
// Typing updates the query and the output as you go; the history keeps one step per field you edit
document.addEventListener('focusin', (e) => { if (e.target.closest?.('#rb input')) e.target._rbSnap = JSON.stringify(RB().root); });
document.addEventListener('input', (e) => {
  const t = e.target, d = t.dataset;
  if (t._rbSnap && t.closest('#rb')) { const rb = RB(); rb.past.push(t._rbSnap); rb.future = []; t._rbSnap = null; } // one undo step per field edit
  if (d.rbval !== undefined && t.tagName === 'INPUT') { rbFind(d.rbval).node.value = t.value.trim(); return rbAfter(false); }
  if (d.rbv !== undefined) { const r = rbFind(d.rbv).node; r.values[+d.i] = t.value.trim(); return rbAfter(false); }
  if (d.rblist !== undefined) { rbFind(d.rblist).node.values = t.value.split(',').map(x => x.trim()).filter(Boolean); return rbAfter(false); }
});
document.addEventListener('keydown', (e) => {
  if (!e.target.closest?.('#rb')) return;
  const g = e.target.dataset?.rbgrip;
  if (g && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) { e.preventDefault(); e.stopPropagation(); return rbMove(g, e.key === 'ArrowUp' ? -1 : 1); }
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'z' && !e.target.closest('input, textarea')) { e.preventDefault(); e.stopPropagation(); rbUndo(e.shiftKey ? 1 : -1); }
}, true);
// Drag a rule or group by its grip onto another rule (before or after it) or into an empty group
let rbDrag = null;
document.addEventListener('dragstart', (e) => { const g = e.target.closest?.('[data-rbgrip]'); if (!g) return; rbDrag = g.dataset.rbgrip; e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', rbDrag); g.closest('[data-rbnode]')?.classList.add('dragging'); });
document.addEventListener('dragend', () => { rbDrag = null; document.querySelectorAll('#rb .dragging, #rb .drop-before, #rb .drop-after, #rb .drop-in').forEach(n => n.classList.remove('dragging', 'drop-before', 'drop-after', 'drop-in')); });
document.addEventListener('dragover', (e) => {
  if (!rbDrag) return;
  const zone = e.target.closest?.('[data-rbzone]'), node = e.target.closest?.('#rb [data-rbnode]');
  document.querySelectorAll('#rb .drop-before, #rb .drop-after, #rb .drop-in').forEach(n => n.classList.remove('drop-before', 'drop-after', 'drop-in'));
  if (zone) { e.preventDefault(); zone.classList.add('drop-in'); return; }
  if (!node || node.dataset.rbnode === rbDrag || node.closest(`[data-rbnode="${rbDrag}"]`)) return;
  e.preventDefault();
  const r = node.getBoundingClientRect(); node.classList.add(e.clientY < r.top + r.height / 2 ? 'drop-before' : 'drop-after');
});
document.addEventListener('drop', (e) => {
  if (!rbDrag) return;
  const zone = e.target.closest?.('[data-rbzone]'), node = e.target.closest?.('#rb [data-rbnode]'), id = rbDrag, src = rbFind(id);
  e.preventDefault(); rbDrag = null;
  if (!src) return;
  if (zone) { const g = rbFind(zone.dataset.rbzone).node; return rbChange(() => { src.parent.children.splice(src.parent.children.indexOf(src.node), 1); g.children.push(src.node); }); }
  if (!node || node.dataset.rbnode === id) return;
  const dst = rbFind(node.dataset.rbnode), before = e.clientY < node.getBoundingClientRect().top + node.getBoundingClientRect().height / 2;
  if (src.node.kind === 'group' && dst.depth > RB_MAX_DEPTH - 1) return toast('Groups can’t go that deep', 'warn');
  rbChange(() => { src.parent.children.splice(src.parent.children.indexOf(src.node), 1); const sib = dst.parent.children; sib.splice(sib.indexOf(dst.node) + (before ? 0 : 1), 0, src.node); });
});

/* SQL editor: autocomplete for keywords, tables and columns */
const SQLKW = 'SELECT FROM WHERE AND OR NOT NULL IS IN LIKE BETWEEN ORDER BY GROUP HAVING LIMIT OFFSET JOIN LEFT RIGHT INNER OUTER ON AS DISTINCT COUNT SUM AVG MIN MAX INSERT INTO VALUES UPDATE SET DELETE CREATE TABLE ALTER DROP INDEX DESC ASC UNION ALL EXISTS CASE WHEN THEN ELSE END WITH SHOW DESCRIBE EXPLAIN'.split(' ');
function acWords() {
  const d = D(), w = new Set(SQLKW);
  for (const t of d.tables) w.add(t.name);
  for (const list of Object.values(d.cols)) for (const c of list) w.add(c);
  return [...w];
}
function acUpdate(ta) {
  const d = D(), before = ta.value.slice(0, ta.selectionStart), m = before.match(/([A-Za-z_][\w]*)\.?([\w]*)$/);
  const dot = before.match(/([A-Za-z_]\w*)\.(\w*)$/);
  let word, list;
  if (dot) { // table.column: load that table's columns once
    const t = dot[1]; word = dot[2];
    if (!d.cols[t] && d.tables.some(x => x.name === t)) { d.cols[t] = []; act(async () => { const st = await dcall('data.structure', { schema: d.schema, table: t }); d.cols[t] = st.columns.map(c => c.name); acUpdate(ta); }); }
    list = (d.cols[t] || []).filter(c => c.toLowerCase().startsWith(word.toLowerCase()) && c !== word);
  } else if (m && m[1].length >= 2) {
    word = m[1]; const lw = word.toLowerCase();
    list = acWords().filter(x => x.toLowerCase().startsWith(lw) && x.toLowerCase() !== lw).sort((a, b) => a.length - b.length);
  }
  const box = $('#acBox');
  if (!list?.length) { d.ac = null; if (box) box.hidden = true; return; }
  d.ac = { word, list: list.slice(0, 8), sel: 0 };
  acDraw(ta);
}
function acDraw(ta) {
  const d = D(), box = $('#acBox'); if (!box || !d.ac) return;
  const p = caretXY(ta);
  box.hidden = false; box.style.left = Math.min(p.x, ta.clientWidth - 220) + 'px'; box.style.top = (p.y + 20) + 'px';
  box.innerHTML = d.ac.list.map((w, i) => `<div class="ac ${i === d.ac.sel ? 'sel' : ''}" data-ac="${i}">${SQLKW.includes(w) ? icon.code : d.tables.some(t => t.name === w) ? icon.table : icon.columns}<span>${esc(w)}</span></div>`).join('');
}
function acAccept(ta, i) {
  const d = D(); if (!d.ac) return;
  const w = d.ac.list[i ?? d.ac.sel], pos = ta.selectionStart, start = pos - d.ac.word.length;
  const ins = SQLKW.includes(w) ? w + ' ' : w;
  ta.value = ta.value.slice(0, start) + ins + ta.value.slice(pos);
  ta.selectionStart = ta.selectionEnd = start + ins.length;
  d.sql = ta.value; d.ac = null; $('#acBox').hidden = true; ta.focus();
}
// Pixel position of the caret inside a textarea (mirror element)
function caretXY(ta) {
  const m = document.createElement('div'), cs = getComputedStyle(ta);
  for (const p of ['font', 'padding', 'border', 'lineHeight', 'letterSpacing', 'whiteSpace', 'wordWrap', 'boxSizing']) m.style[p] = cs[p];
  Object.assign(m.style, { position: 'absolute', visibility: 'hidden', whiteSpace: 'pre-wrap', width: ta.clientWidth + 'px', top: 0, left: 0 });
  m.textContent = ta.value.slice(0, ta.selectionStart);
  const s = document.createElement('span'); s.textContent = '​'; m.append(s);
  ta.parentElement.append(m);
  const r = { x: s.offsetLeft, y: s.offsetTop - ta.scrollTop };
  m.remove(); return r;
}
function showSQLHistory(anchor) {
  const h = store.get('sqlHistory', []).filter(x => x.conn === D().conn);
  menuAt(anchor, h.length ? `<div class="menu-list" style="max-height:340px">${h.slice(0, 25).map((x, i) => `<button data-sqlh="${i}" title="${esc(x.sql)}"><span class="mono-s">${esc(x.sql.replace(/\s+/g, ' ').slice(0, 70))}</span><span class="kb">${ago(x.at)}</span></button>`).join('')}</div><hr><button data-sqlhclear="1">${icon.trash}<span>Clear history</span></button>`
    : '<div class="menu-empty">Queries you run on this connection appear here.</div>', 'wide');
}
function viewData() {
  const d = D();
  if (!d.conns) return '<div class="pad"><span class="skeleton" style="width:30%"></span></div>';
  if (!d.conns.length) return emptyState({ fig: 'plug', ic: 'table', title: 'No database connections', text: 'Browse MySQL, MariaDB, PostgreSQL or SQLite. Connections are read-only until you allow changes.',
    actions: `<button class="btn primary" data-cmd="dnewconn">${icon.plus}New connection</button>` });
  const conn = d.conns.find(c => c.id === d.conn);
  const lock = conn?.read_only
    ? `<button class="chip" data-cmd="dwrite" title="Read-only: changes are blocked by the server. Click to allow changes.">${icon.lock}Read-only</button>`
    : `<button class="chip warn" data-cmd="dreadonly" title="Changes allowed. Click to make read-only again.">${icon.unlock}Changes allowed</button>`;
  const tables = d.tables.filter(t => !d.tfilter || t.name.toLowerCase().includes(d.tfilter.toLowerCase()));
  const left = `<aside class="dpane">
    <div class="form-row" style="gap:6px;flex-wrap:nowrap"><select class="input" id="dConn" style="flex:1" aria-label="Connection">${d.conns.map(c => `<option value="${esc(c.id)}" ${c.id === d.conn ? 'selected' : ''}>${esc(c.name)}</option>`).join('')}</select>
      <button class="tb-btn" data-cmd="dnewconn" title="New connection">${icon.plus}</button>${conn && !conn.builtin ? `<button class="tb-btn" data-cmd="ddelconn" title="Remove connection">${icon.trash}</button>` : ''}</div>
    <div class="form-row dp-status" style="margin:8px 0;justify-content:space-between">${d.info ? `<span class="note ellipsis"><span class="dot on"></span> ${esc(d.info.server)}</span>` : d.err ? '<span class="note"><span class="dot err"></span> Not connected</span>' : '<span class="note"><span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span></span>'}${lock}</div>
    ${d.schemas.length ? `<select class="input" id="dSchema" style="width:100%" aria-label="Database">${d.schemas.map(x => `<option ${x === d.schema ? 'selected' : ''}>${esc(x)}</option>`).join('')}</select>` : ''}
    ${conn && d.info ? `<div class="form-row dp-tools">${UI.btn('Export', { ic: 'download2', kind: 'ghost', attrs: 'data-cmd="dexport"', title: `Export ${d.schema || conn.name} to a .sql file` })}${UI.btn('Import', { ic: 'import', kind: 'ghost', attrs: 'data-cmd="dimport"', title: conn.read_only ? 'Read-only: allow changes first' : `Import a .sql file into ${d.schema || conn.name}` })}</div>` : ''}
    ${d.tables.length ? `<select class="input dtsel" id="dTableSel" aria-label="Table"><option value="">Choose a table…</option>${d.tables.map(t => `<option ${t.name === d.table ? 'selected' : ''}>${esc(t.name)}</option>`).join('')}</select>` : ''}
    ${d.tables.length ? `<input class="input filter" id="dTFilter" placeholder="Filter tables" value="${esc(d.tfilter)}" style="width:100%;margin-top:8px" spellcheck="false" aria-label="Filter tables">` : ''}
    <div class="tlist">${tables.map(t => `<a data-dtable="${esc(t.name)}" class="${t.name === d.table ? 'on' : ''}">${icon[t.type === 'view' ? 'list' : 'table']}<span>${esc(t.name)}</span><span class="count">${t.rows ? t.rows.toLocaleString() : ''}</span></a>`).join('')}</div>
  </aside>`;
  let main;
  if (d.err) {
    const firstTime = d.needPw && conn && !conn.has_password;
    main = emptyState({ ic: firstTime ? 'key' : 'plug', fig: firstTime ? '' : 'plug', title: firstTime ? `Connect to ${conn.name}` : `Can’t connect to ${conn?.name || ''}`,
      text: firstTime ? `Enter the ${esc(conn.user || 'root')} password for this server once. Browsing is read-only.` : `<span role="alert">${esc(d.err)}</span>`,
      actions: d.needPw ? `<input class="input" id="dPw" type="password" placeholder="${esc(conn?.user || 'root')} password" style="width:220px" aria-label="Password"><button class="btn primary" data-cmd="dsavepw">Connect</button>` : `<button class="btn" data-cmd="dretry">${icon.refresh}Try again</button>`,
      hint: d.needPw ? 'Saved in the system keychain.' : esc(conn?.note || '') });
  } else {
    const tabs = `<div class="dtabs">${[['rows', 'Rows', 'table'], ['structure', 'Structure', 'columns'], ['sql', 'SQL', 'code']].map(([id, l, ic]) =>
      `<button data-dtab="${id}" class="${d.tab === id ? 'on' : ''}" ${id !== 'sql' && !d.table ? 'disabled' : ''}>${icon[ic]}${l}</button>`).join('')}
      <span class="spacer"></span>${d.table && d.tab !== 'sql' ? `<span class="note ellipsis">${esc(d.schema)}.${esc(d.table)}</span>` : ''}</div>`;
    let body = '';
    if (d.tab === 'rows') {
      if (!d.table) {
        const big = [...d.tables].sort((a, b) => b.rows - a.rows).slice(0, 6);
        body = emptyState({ ic: 'table', title: d.tables.length ? 'Pick a table' : 'No tables here', text: d.tables.length ? 'Choose a table on the left to see its rows, or write a query.' : 'This database has no tables yet.',
          actions: `<button class="btn" data-dtab="sql">${icon.code}Write SQL</button>`, hint: `<kbd>${MOD}K</kbd> then “Open table”` })
          + (big.length ? startCards('Largest tables', big.map(t => ({ ic: 'table', title: t.name, text: `${(t.rows || 0).toLocaleString()} rows${t.size_kb ? ' · ' + (t.size_kb > 1024 ? (t.size_kb / 1024).toFixed(1) + ' MB' : t.size_kb + ' KB') : ''}`, attrs: `data-dtable="${esc(t.name)}"` }))) : '');
      } else {
        const r = d.result, total = r?.total ?? 0, from = total ? d.page * d.size + 1 : 0, to = Math.min(total, (d.page + 1) * d.size);
        const edit = d.mode === 'edit', n = pending(), noKey = d.structure && !pkCols().length;
        const modeSeg = `<div class="seg mode" role="group" aria-label="Mode"><button data-cmd="dview" class="${edit ? '' : 'on'}" title="Browse only: nothing can change">${icon.eye}View</button>
          <button data-cmd="dedit" class="${edit ? 'on' : ''}" ${noKey ? 'disabled title="This table has no primary key, so rows can’t be identified safely. Use SQL."' : 'title="Change, add and delete rows; nothing is saved until you review it"'}>${icon.edit}Edit</button></div>`;
        const rules = d.rb ? (function count(n) { return n.kind === 'rule' ? 1 : n.children.reduce((a, c) => a + count(c), 0); })(d.rb.root) : 0, rbOpen = d.rb?.open;
        body = `<div class="dbar">${modeSeg}<button class="btn ghost sm ${rbOpen || rules ? 'active' : ''}" data-rbtoggle="1" aria-expanded="${!!rbOpen}" title="Filter rows with rules (and, or, groups)">${icon.listFilter}<span class="lbl">Filter</span>${rules ? `<span class="badge">${rules}</span>` : ''}</button>
          <span class="spacer"></span>${d.busy ? '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>' : ''}<span class="note">${r && !r.error ? `${from.toLocaleString()} to ${to.toLocaleString()} of ${total.toLocaleString()}${r.ms !== undefined ? ` · ${r.ms} ms` : ''}` : ''}</span>
          <button class="tb-btn" data-cmd="dprev" title="Previous page" ${d.page > 0 ? '' : 'disabled'}>${icon.back}</button><button class="tb-btn" data-cmd="dnext" title="Next page" ${to < total ? '' : 'disabled'}>${icon.fwd}</button>
          <button class="tb-btn" data-cmd="dexport" title="Export rows">${icon.download2}</button></div>
          ${edit ? `<div class="editbar" role="status"><span class="pill">${icon.edit}Editing</span><span class="note">Click a cell to change it. Nothing is written until you review and save.</span><span class="spacer"></span>
            <button class="btn ghost sm" data-cmd="dinsert">${icon.plus}Row</button>
            ${n ? `<span class="note"><b>${plural(n, 'change')}</b></span><button class="btn ghost sm" data-cmd="ddiscard">Discard</button><button class="btn primary sm" data-cmd="dreview">${icon.save}Review & save</button>` : ''}</div>` : ''}
          ${d.structure && d.rb?.open ? `<div class="rb-wrap">${ruleBuilder()}</div>` : ''}
          ${rowsGrid()}`;
      }
    } else if (d.tab === 'structure') {
      const st = d.structure;
      body = st ? `<div class="pad"><div class="card"><table class="t"><thead><tr><th>Column</th><th>Type</th><th>Null</th><th>Default</th><th>Key</th><th>Extra</th></tr></thead><tbody>
        ${st.columns.map(c => `<tr><td><b style="font-weight:500">${esc(c.name)}</b></td><td><code>${esc(c.type)}</code></td><td>${c.nullable ? 'yes' : ''}</td><td class="note">${c.default === null ? '' : esc(c.default)}</td><td>${c.key ? `<span class="chip mono">${esc(c.key)}</span>` : ''}</td><td class="note">${esc(c.extra)}</td></tr>`).join('')}
        </tbody></table></div>${st.indexes?.length ? `<div class="card"><div class="ch"><h3>Indexes</h3></div><table class="t"><tbody>${st.indexes.map(i => `<tr><td><b style="font-weight:500">${esc(i.name)}</b></td><td>${i.unique ? '<span class="chip">unique</span>' : ''}</td><td class="note">${i.columns.map(esc).join(', ')}</td></tr>`).join('')}</tbody></table></div>` : ''}</div>` : '<div class="pad"><span class="skeleton" style="width:50%"></span></div>';
    } else {
      body = `<div class="sqlbox"><div class="sqlwrap"><textarea id="dSQL" class="sql" spellcheck="false" autocomplete="off" placeholder="SELECT * FROM ${esc(d.table || 'table')} LIMIT 50   (${MOD}↵ to run, Tab to complete)">${esc(d.sql)}</textarea><div class="acbox" id="acBox" hidden></div></div>
        <div class="dbar"><button class="btn primary" data-cmd="drun" ${d.busy ? 'disabled' : ''}>${icon.play}Run</button><span class="note">${MOD}↵</span>
          <button class="btn ghost sm" data-cmd="dhistory">${icon.history2}History</button>
          ${conn && !conn.read_only ? `<label class="form-row note" style="gap:6px"><input type="checkbox" id="dAllowWrite" ${d.allowWrite ? 'checked' : ''}> Allow this query to change data</label>` : '<span class="note">Read-only: queries can’t change anything.</span>'}
          <span class="spacer"></span>${d.qres ? `<span class="note">${d.qres.columns ? `${d.qres.rows.length.toLocaleString()} row${d.qres.rows.length === 1 ? '' : 's'}${d.qres.truncated ? ' (first 1,000)' : ''}` : ''} · ${d.qres.ms} ms</span><button class="tb-btn" data-cmd="dexportq" title="Export result">${icon.download2}</button>` : ''}</div></div>
        ${d.qerr ? `<p class="note warn" role="alert" style="padding:12px 20px">${esc(d.qerr)}</p>` : grid(d.qres, false)}`;
    }
    main = tabs + `<div class="dbody">${body}</div>`;
  }
  return `<div class="dataview">${left}<section class="dmain">${main}</section></div>`;
}
function toCSV(res) {
  const q = (v) => v === null ? '' : /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
  return [res.columns.map(q).join(','), ...res.rows.map(r => r.map(q).join(','))].join('\n');
}
function showExportMenu(anchor, res, name) {
  if (!res?.columns) return;
  menuAt(anchor, `<button data-export="copy">${icon.copy}<span>Copy as CSV</span><span class="kb">${plural(res.rows.length, 'row')}</span></button>
    <button data-export="file" data-ename="${esc(name)}">${icon.download2}<span>Save as CSV file…</span></button>`);
  exportRes = res;
}
let exportRes = null;
async function exportCSV(kind, name) {
  if (!exportRes) return;
  const csv = toCSV(exportRes);
  if (kind === 'copy') { await bridge.copy(csv); return toast(`Copied ${plural(exportRes.rows.length, 'row')} as CSV`); }
  if (window.go?.main?.App?.SaveText) { const p = await act(() => window.go.main.App.SaveText(name + '.csv', csv)); if (p) toast('Saved ' + p); return; }
  const a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv' })); a.download = name + '.csv'; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}
function connectionDialog() {
  return dialog(`<h3>New connection</h3>
    <div class="cform">
      <label>Name<input class="input" id="cName" placeholder="Production copy"></label>
      <label>Type<select class="input" id="cDriver"><option value="mysql">MySQL / MariaDB</option><option value="postgres">PostgreSQL</option><option value="sqlite">SQLite file</option></select></label>
      <label class="net">Host<input class="input" id="cHost" value="127.0.0.1"></label>
      <label class="net">Port<input class="input" id="cPort" placeholder="3306 / 5432" inputmode="numeric"></label>
      <label class="net">User<input class="input" id="cUser" value="root"></label>
      <label class="net">Password<input class="input" id="cPass" type="password" placeholder="Saved in the keychain"></label>
      <label class="net">Database<input class="input" id="cDb" placeholder="optional for MySQL"></label>
      <label class="my">Socket<input class="input" id="cSock" placeholder="optional, e.g. /tmp/mysql.sock"></label>
      <label class="pg" hidden>Encryption<select class="input" id="cSSL"><option value="">Required for remote servers (recommended)</option><option value="require">Always required</option><option value="verify-full">Required, and check the certificate</option><option value="disable">Off (server without TLS)</option></select></label>
      <label class="lite" hidden>File<input class="input" id="cFile" placeholder="/path/to/database.sqlite"></label>
      <label class="net full"><span class="form-row" style="gap:6px"><input type="checkbox" id="cSSH"> Connect through an SSH tunnel</span></label>
      <label class="ssh" hidden>SSH host<input class="input" id="cSSHHost" placeholder="user@server.example.com"></label>
      <label class="ssh" hidden>SSH port<input class="input" id="cSSHPort" placeholder="22" inputmode="numeric"></label>
    </div>
    <p class="note ssh" hidden>Uses your own <code>ssh</code> and its keys/agent (no passwords stored). Host and port above are as seen from the SSH server, e.g. 127.0.0.1:3306.</p>
    <label class="form-row note" style="margin-top:12px;gap:6px"><input type="checkbox" id="cRO" checked> Read-only (recommended: the server blocks every change)</label>
    <p class="note warn" id="cErr" role="alert"></p>
    <div class="form-row" style="justify-content:flex-end;margin-top:8px"><button class="btn ghost" data-dlg="cancel">Cancel</button><button class="btn primary" id="cSave">Save & connect</button></div>`);
}

/* --- agent --- */
function parseIntent(text) {
  const q = text.toLowerCase().trim();
  if (!q) return [];
  const found = S.sites.filter(s => q.includes(s.folder.toLowerCase()) || q.includes(s.domain.split('.')[0])).sort((a, b) => b.folder.length - a.folder.length)[0];
  const php = (q.match(/\b(\d)\.(\d)\b/) || [])[0];
  const out = [];
  const add = (label, iconName, run) => out.push({ label, icon: iconName, run, hint: 'Enter' });
  if (found && php && S.status?.php.some(p => p.version === php)) add(`Switch ${found.folder} to PHP ${php}`, 'php', () => updateSite(found.folder, { php }));
  if (found && /\b(start|turn on|switch on|enable|run)\b/.test(q)) add(`Switch on ${found.folder}`, 'power', () => updateSite(found.folder, { enabled: true }));
  if (found && /\b(stop|turn off|switch off|disable)\b/.test(q)) add(`Switch off ${found.folder}`, 'power', () => updateSite(found.folder, { enabled: false }));
  if (found && /\b(check|broken|down|slow|wrong|working|why|health)\b/.test(q)) add(`Check connection: ${found.folder}`, 'doctor', async () => ({ checks: await bridge.call('sites.check', { folder: found.folder }) }));
  if (found && /\b(log|logs|error|errors)\b/.test(q)) add(`Show logs for ${found.folder}`, 'logs', () => { S.log = { kind: 'site-error', folder: found.folder }; navigate('logs'); return { nav: true }; });
  if (found && /\bopen\b/.test(q)) add(`Open ${found.folder} in browser`, 'globe', () => bridge.call('open', { folder: found.folder, app: 'browser' }));
  const dbName = (q.match(/\b(?:database|db)\s+([a-z0-9_]+)/) || [])[1];
  if (dbName && /\b(create|new|make|add)\b/.test(q)) add(`Create database ${dbName}`, 'db', async () => { const r = await bridge.call('db.create', { name: dbName }); S.dbs = null; loadDBs(); return { log: [`Created ${r.database} with its own login ${r.user}`] }; });
  const projName = (q.match(/\b(?:project|site)\s+([a-z0-9_-]+)/) || [])[1];
  if (projName && /\b(create|new|make|add)\b/.test(q) && !site(projName)) add(`Create project ${projName}`, 'compose', async () => { await bridge.call('sites.create', { folder: projName }); await loadSites(); navigate('projects', projName); return { nav: true }; });
  if (/\b(doctor|health|wrong|diagnos)/.test(q) && !found) add('Run Doctor (full health check)', 'doctor', async () => ({ checks: await bridge.call('doctor') }));
  if (/\brestart\b/.test(q) && !found) add('Restart all services', 'refresh', () => command('restart'));
  for (const c of commands().map(c => ({ ...c, s: score(c.label, q) })).filter(c => c.s > 0).sort((a, b) => b.s - a.s).slice(0, 6)) {
    if (!out.some(o => o.label === c.label)) out.push({ label: c.label, icon: c.icon, run: c.run, hint: c.hint });
  }
  return out.slice(0, 7);
}
/* --- Run List and Tool Receipts: what each AI agent did, call by call, with Undo where it can be undone --- */
const RUN_GAP = 10 * 60000; // calls from the same agent this close together are one run
function agentRuns() {
  const runs = [];
  for (const a of S.activity.filter(x => x.client === 'mcp').slice().reverse()) { // oldest first
    const t = new Date(a.time).getTime(), who = a.agent || 'An AI agent', r = runs[runs.length - 1];
    if (r && r.who === who && t - r.end <= RUN_GAP) { r.calls.push(a); r.end = t; }
    else runs.push({ who, start: t, end: t, calls: [a], key: who + '|' + a.time });
  }
  return runs.reverse();
}
const argsOf = (a) => { try { return JSON.parse(a.summary || '{}'); } catch (_) { return {}; } };
const callKey = (a) => a.time + '|' + a.method;
// Rewind: a change can be undone when the opposite action exists and nothing has changed it since.
const UNDO = {
  'sites.update': (p) => typeof p.enabled === 'boolean' && site(p.folder) && site(p.folder).enabled === p.enabled
    ? { label: p.enabled ? `Switch ${p.folder} off` : `Switch ${p.folder} on`, run: () => updateSite(p.folder, { enabled: !p.enabled }) } : null,
  'share.start': (p) => { const live = (S.status?.shares || []).find(x => x.folder === p.folder);
    return live ? { label: `Stop sharing ${p.folder}`, run: () => bridge.call('share.stop', { id: live.id }) } : null; },
  'tools.adminer': (p) => p.enabled && S.status?.adminer?.enabled ? { label: 'Switch Adminer off', run: () => setAdminer(false) } : null,
  'agents.connect': (p) => (S.agents || []).some(c => c.id === p.id && c.connected) ? { label: 'Disconnect it again', run: () => bridge.call('agents.disconnect', { id: p.id }) } : null,
  'data.readonly': (p) => p.read_only === false ? { label: 'Make the connection read-only again', run: () => bridge.call('data.readonly', { id: p.id, read_only: true }) } : null,
  'sites.connect_db': (p) => ({ label: `Put ${p.folder}’s .env back`, run: () => bridge.call('sites.restore_env', { folder: p.folder }) }),
  'sites.hide': (p) => ({ label: `Show ${p.folder} again`, run: () => bridge.call('sites.unhide', { folder: p.folder }) }),
};
// Changes with no undo button: what to do instead
const LOCKED = {
  'db.drop': 'Backed up first: restore it from Databases → Backups', 'db.empty': 'Backed up first: restore it from Databases → Backups',
  'db.import': 'Backed up first: restore it from Databases → Backups', 'db.restore': 'What was there is in Databases → Backups',
  'pg.drop': 'Backed up first', 'pg.empty': 'Backed up first', 'pg.import': 'Backed up first', 'data.import': 'What was there was exported first',
  'data.save': 'Row changes can’t be undone here', 'data.query': 'A query can’t be undone here', 'storage.clean': 'Removed files can’t come back',
  'addons.flush': 'The cache is empty; projects refill it', 'restart': '', 'stop': 'Start services again from Services',
};
const undone = new Set();
let undoFns = {};
function receiptLine(a) {
  const p = argsOf(a), args = Object.entries(p).map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`).join(' ');
  const u = !a.error && !undone.has(callKey(a)) && UNDO[a.method]?.(p), lock = !a.error && a.method in LOCKED;
  if (u) undoFns[callKey(a)] = u;
  const right = undone.has(callKey(a)) ? '<span class="note">undone</span>'
    : u ? `<button class="btn ghost sm" data-undo="${esc(callKey(a))}" title="${esc(u.label)}">${icon.undo}Undo</button>`
    : lock && /^db\.(drop|empty|import|restore)$/.test(a.method) && p.name ? `<button class="btn ghost sm" data-dbbackups="${esc(p.name)}" title="${esc(LOCKED[a.method])}">${icon.backup}Backups</button>`
    : lock ? `<span class="rc-lock" title="${esc(LOCKED[a.method] || 'Can’t be undone')}">${icon.lock}</span>` : '';
  return `<div class="rc-line ${a.error ? 'fail' : ''}"><span class="rc-time">${new Date(a.time).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' })}</span>
    <span class="rc-call"><code>${esc(toolName(a.method))}</code>${args ? `<span class="rc-args" title="${esc(args)}">${esc(args)}</span>` : ''}</span>
    <span class="rc-ms">${a.ms ? a.ms + ' ms' : ''}</span><span class="rc-st">${a.error ? icon.x : icon.check}</span><span class="rc-r">${right}</span></div>
    ${a.error ? `<div class="rc-err">${esc(a.error)}</div>` : ''}`;
}
/* --- Run List. Every agent session in one calm
   list, with live clocks, counts by status and a click to see the last few steps or answer what it's waiting
   for. A run is the calls one agent made with no gap longer than 10 minutes. --- */
const RL_ORDER = { review: 0, failed: 1, running: 2, done: 3 };
const RL_META = { running: ['Running', 'info'], review: ['Needs review', 'warn'], failed: ['Failed', 'err'], done: ['Done', 'ok'] };
const RL_FILTERS = [['all', 'All'], ['running', 'Running'], ['review', 'Review'], ['failed', 'Failed'], ['done', 'Done']];
const RL_EMPTY = { all: ['No runs yet', 'When Claude, Cursor or another agent uses LocalSites over MCP, each session shows up here with a live clock.'],
  running: ['Nothing running', 'Every agent is idle. New runs appear here as they start.'], review: ['You’re all caught up', 'Nothing is waiting on your approval.'],
  failed: ['No failed runs', 'Every run finished or is still going.'], done: ['Nothing finished yet', 'Finished runs land here with their last steps.'] };
let rlFilter = 'all', rlOpen = '', rlReceipt = '';
function rlRuns() {
  const aps = S.approvals || [];
  return agentRuns().map(r => {
    const live = Date.now() - r.end < 2 * 60000, waiting = aps.filter(a => a.client === 'mcp' && (a.agent || 'An AI agent') === r.who);
    const last = r.calls.at(-1), status = waiting.length ? 'review' : live ? 'running' : last.error ? 'failed' : 'done';
    const folders = uniq(r.calls.map(c => argsOf(c).folder || argsOf(c).name).filter(Boolean));
    const changes = r.calls.filter(c => !c.error && (UNDO[c.method] || c.method in LOCKED || /\.(update|create|connect|start|install|backup|takeover|drop|empty|import)$/.test(c.method)));
    const title = changes.length ? describe(changes.at(-1)) + (changes.length > 1 ? ` and ${changes.length - 1} more` : '') : `Looked around: ${plural(r.calls.length, 'call')}`;
    return { ...r, status, waiting, folders, title };
  });
}
function rlIcon(status) {
  if (status === 'running') return '<svg class="rl-spin" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="2" opacity=".2"/><path d="M8 2a6 6 0 0 1 6 6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>';
  return { review: icon.alertCircle, failed: icon.circleX, done: icon.status }[status];
}
function runList() {
  const runs = rlRuns(); undoFns = {};
  const counts = { all: runs.length, running: 0, review: 0, failed: 0, done: 0 }; for (const r of runs) counts[r.status]++;
  const shown = runs.filter(r => rlFilter === 'all' || r.status === rlFilter).sort((a, b) => RL_ORDER[a.status] - RL_ORDER[b.status] || b.end - a.end).slice(0, 20);
  const rows = shown.map(r => {
    const open = rlOpen === r.key, [label, tone] = RL_META[r.status], el = r.status === 'running';
    const steps = r.calls.slice(-3).map((c, i, all) => { const lastStep = i === all.length - 1;
      return `<li class="${lastStep ? 'last' : ''} ${c.error ? 'err' : ''}"><span class="rl-sd"></span><span>${esc(describe(c) === c.method ? toolName(c.method) : describe(c))}${c.error ? ` <span class="warnc">failed</span>` : ''}</span>${lastStep && el ? '<span class="rl-dots"><i></i><i></i><i></i></span>' : ''}</li>`; }).join('');
    const acts = [
      ...r.waiting.map(a => `<span class="rl-ap"><span class="note">${esc(describe({ method: a.method, summary: a.summary }))}</span>${approvalActions(a, true)}</span>`),
      UI.btn(rlReceipt === r.key ? 'Hide calls' : 'Every call', { ic: 'receipt', kind: 'ghost', attrs: `data-rlreceipt="${esc(r.key)}"` }),
    ].join('');
    return `<li class="rl-item ${open ? 'open' : ''}"><button class="rl-row" data-rlopen="${esc(r.key)}" aria-expanded="${open}">
        <span class="rl-ic ${r.status}">${rlIcon(r.status)}</span>
        <span class="rl-main"><span class="rl-title">${esc(r.title)}</span><span class="rl-sub">${esc(r.who)}${r.folders.length ? ` · <span class="mono">${esc(r.folders.slice(0, 2).join(', '))}${r.folders.length > 2 ? '…' : ''}</span>` : ''}</span></span>
        <span class="rl-side"><span class="rl-clock ${el ? 'live' : ''}" ${el ? `data-elapsed="${r.start}"` : ''}>${fmtClock((el ? Date.now() : r.end) - r.start)}</span><span class="rl-badge ${tone}">${label}</span></span>
        <span class="rl-chev">${icon.caret}</span></button>
      ${open ? `<div class="rl-panel"><ol class="rl-steps">${steps}</ol><div class="rl-acts">${acts}</div>
        ${rlReceipt === r.key ? `<div class="receipt"><div class="rc-head"><span>${esc(r.who)}</span><span>${new Date(r.start).toLocaleDateString([], { day: 'numeric', month: 'short' })} · ${plural(r.calls.length, 'call')}</span></div>${r.calls.slice().reverse().map(receiptLine).join('')}</div>` : ''}</div>` : ''}</li>`;
  }).join('');
  const [et, eb] = RL_EMPTY[rlFilter];
  return `<div class="rl"><div class="rl-h"><h3>AI agents</h3><p class="rl-count"><span class="rl-pulse ${counts.running ? 'on' : ''}"></span>${counts.running} running · ${counts.review} waiting on you</p></div>
    <div class="rl-tabs" role="tablist" aria-label="Filter runs">${RL_FILTERS.map(([v, l]) => `<button role="tab" data-rlfilter="${v}" class="${rlFilter === v ? 'on' : ''}" aria-selected="${rlFilter === v}">${l}<span>${counts[v]}</span></button>`).join('')}</div>
    <ul class="rl-list">${rows || `<li class="rl-empty"><b>${et}</b><span>${eb}</span></li>`}</ul></div>`;
}
async function undoCall(key) {
  const u = undoFns[key]; if (!u) return;
  if (!await confirmBox(`${u.label}?`, 'This undoes what the agent did. It runs as you, from the app.', 'Undo')) return;
  const r = await act(() => u.run(), 'Undo');
  if (r === undefined) return;
  undone.add(key); logToast(r); toast('Undone');
  await act(() => Promise.all([loadStatus(), loadSites(), loadActivity()])); render();
}

function viewAgent() {
  const sug = parseIntent(S.agent.text);
  S.agent.sel = Math.min(S.agent.sel, Math.max(0, sug.length - 1));
  const res = S.agent.result;
  return `<div class="agent">
    <div class="glyph-fig" data-fig="hub" aria-hidden="true"></div>
    <div class="composer"><textarea id="agentInput" rows="1" placeholder="Ask LocalSites…  e.g. “switch primec to PHP 8.3” or “why is shop broken”">${esc(S.agent.text)}</textarea>
      <div class="composer-bar"><span class="note">Matched on this computer · nothing is sent anywhere</span><span class="spacer"></span>
        <button class="send" data-cmd="agentsend" ${sug.length ? '' : 'disabled'} title="Run (Enter)">${S.agent.busy ? '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>' : icon.send}</button></div></div>
    ${sug.length ? `<div class="suggest">${sug.map((c, i) => `<div class="item ${i === S.agent.sel ? 'sel' : ''}" data-agentrun="${i}">${icon[c.icon] || ''}<span>${esc(c.label)}</span><span class="hint">${i === S.agent.sel ? '↵' : esc(c.hint || '')}</span></div>`).join('')}</div>` : ''}
    ${!S.agent.text && !res ? startCards('Get started with some examples', [
      { ic: 'php', title: 'Change a PHP version', text: `switch ${S.sites[0]?.folder || 'shop'} to php 8.3`, attrs: `data-agentex="switch ${esc(S.sites[0]?.folder || 'shop')} to php 8.3"` },
      { ic: 'doctor', title: 'Find out what’s broken', text: `why is ${S.sites.find(s => s.enabled)?.folder || 'shop'} broken`, attrs: `data-agentex="why is ${esc(S.sites.find(s => s.enabled)?.folder || 'shop')} broken"` },
      { ic: 'db', title: 'Create a database', text: 'create database shop', attrs: 'data-agentex="create database shop"' },
      { ic: 'compose', title: 'Start a new project', text: 'new project demo', attrs: 'data-agentex="new project demo"' }], 'agentEx') : ''}
    ${res ? `<div class="card" style="margin-top:14px"><div class="ch">${icon.info}<h3>${esc(res.title)}</h3></div>${res.checks ? checksTable(res.checks, res.checks.some(c => c.area !== res.checks[0].area)) : `<pre class="mini" style="border:0;max-height:300px">${esc(res.text)}</pre>`}</div>` : ''}
    ${(S.grants || []).length ? `<div class="card" style="margin-top:24px"><div class="ch">${icon.ticket}<h3>Allowed for a while</h3><span class="spacer"></span><span class="note">The next request after a pass ends asks again</span></div><div class="cb passes">${S.grants.map(passCard).join('')}</div></div>` : ''}
    <div style="margin-top:24px">${runList()}</div>
    <p class="note" style="text-align:center;margin-top:10px"><a class="link" data-cmd="agentsetup">Connect an agent</a> · Undo and backups are in each run’s calls.</p>
    <p class="note" style="text-align:center;margin-top:14px">For a real AI assistant, connect Claude or Cursor over MCP.</p>
  </div>`;
}
async function agentRun(i) {
  const c = parseIntent(S.agent.text)[i];
  if (!c || S.agent.busy) return;
  S.agent.busy = true; renderView();
  const out = await act(() => c.run());
  S.agent.busy = false;
  S.agent.result = out?.nav ? null : out?.checks ? { title: c.label, checks: out.checks } : { title: c.label, text: out?.log ? out.log.join('\n') : 'Done.' };
  S.agent.text = ''; S.agent.sel = 0;
  await act(loadActivity);
  render();
}

/* --- connecting to LocalSites MySQL --- */
// Ready-to-paste settings for every common stack. c: {host, port, socket, database, user, password}
function mysqlSnippets(c) {
  const db = c.database || 'your_database', u = c.user || 'root', pw = c.password ?? '', P = String(c.port);
  const url = `mysql://${encodeURIComponent(u)}:${encodeURIComponent(pw)}@127.0.0.1:${P}/${db}`;
  return [
    ['Laravel', '.env', `DB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_PORT=${P}\nDB_DATABASE=${db}\nDB_USERNAME=${u}\nDB_PASSWORD=${pw}\n# or use the socket instead of host and port:\n# DB_SOCKET=${c.socket}`],
    ['Node / Next.js', '.env.local (Prisma, Drizzle, mysql2)', `DATABASE_URL="${url}"\n# mysql2 options: { host: "127.0.0.1", port: ${P}, user: "${u}", password: "${pw}", database: "${db}" }`],
    ['Django', 'settings.py', `DATABASES = {\n    "default": {\n        "ENGINE": "django.db.backends.mysql",\n        "NAME": "${db}",\n        "USER": "${u}",\n        "PASSWORD": "${pw}",\n        "HOST": "127.0.0.1",\n        "PORT": "${P}",\n    }\n}`],
    ['Rails', 'config/database.yml', `development:\n  adapter: mysql2\n  host: 127.0.0.1\n  port: ${P}\n  database: ${db}\n  username: ${u}\n  password: "${pw}"`],
    ['WordPress', 'wp-config.php', `define('DB_NAME', '${db}');\ndefine('DB_USER', '${u}');\ndefine('DB_PASSWORD', '${pw}');\ndefine('DB_HOST', '127.0.0.1:${P}');`],
    ['PHP (PDO)', 'any .php file', `$pdo = new PDO('mysql:host=127.0.0.1;port=${P};dbname=${db};charset=utf8mb4', '${u}', '${pw}');\n// "localhost" also works in LocalSites projects: it uses the socket automatically`],
    ['Terminal', 'mysql command line', `mysql -h 127.0.0.1 -P ${P} -u ${u} -p ${db}\n# or through the socket:\nmysql -S ${c.socket} -u ${u} -p ${db}`],
    ['GUI apps', 'TablePlus, Sequel Ace, DBeaver', `Host: 127.0.0.1\nPort: ${P}\nUser: ${u}\nPassword: ${pw}\nDatabase: ${db}\nSocket (optional): ${c.socket}`],
  ];
}
async function mysqlConnectDialog(dbName = '') {
  if (!S.settings) await act(loadSettings);
  let c = { host: '127.0.0.1', port: S.settings?.mysql_port || 3306, socket: S.settings?.mysql_socket || '', database: '', user: '', password: '' };
  if (dbName) { const r = await act(() => bridge.call('db.credentials', { name: dbName }), 'Login'); if (!r) return; c = { ...c, ...r }; }
  const items = mysqlSnippets(c);
  let curS = store.get('mysqlStack', 'Laravel'); if (!items.some(x => x[0] === curS)) curS = items[0][0];
  let reveal = false;
  const mask = (t) => reveal || !c.password ? t : t.split(c.password).join('•'.repeat(10)).split(encodeURIComponent(c.password)).join('•'.repeat(10));
  const draw = () => { const [, file, text] = items.find(x => x[0] === curS);
    return `${UI.tabs(items.map(([k]) => [k, k]), curS, 'data-stack')}<p class="note" style="margin:12px 0 6px">Paste into <code>${esc(file)}</code>:</p>${UI.code(text, mask(text))}
      ${c.password ? `<label class="form-row note" style="gap:6px;margin-top:8px"><input type="checkbox" id="revealPw" ${reveal ? 'checked' : ''}> Show password</label>` : ''}`; };
  const head = dbName ? `<p class="note">Database <code>${esc(dbName)}</code> with its own login <code>${esc(c.user)}</code>, which can only see this database. Copy keeps the real password.</p>`
    : `<p class="note">Fill in your database name, user and password. Each database you create in LocalSites gets its own login: open it in Databases → Connect to get settings with that login filled in.</p>`;
  const p = dialog(`<h3>Connect to LocalSites MySQL${dbName ? ' · ' + esc(dbName) : ''}</h3>${head}
    <div class="facts">${[['Host', '127.0.0.1'], ['Port', String(c.port)], ['Socket', c.socket]].map(([k, v]) => `<button class="fact" data-copy="${esc(v)}" title="Copy ${esc(v)}"><span>${k}</span><code>${esc(v)}</code></button>`).join('')}</div>
    <div id="stackBox">${draw()}</div>
    <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn primary" data-dlg="ok">Done</button></div>`);
  const box = $('#keysDialog');
  box.onclick = (e) => { const b = e.target.closest('[data-stack]'); if (!b) return; curS = b.dataset.stack; store.set('mysqlStack', curS); $('#stackBox').innerHTML = draw(); };
  box.onchange = (e) => { if (e.target.id === 'revealPw') { reveal = e.target.checked; $('#stackBox').innerHTML = draw(); } };
  return p;
}
// Every address LocalSites uses on this Mac, so nothing about ports is guesswork
function portsTable() {
  const st = S.status, set = S.settings;
  if (!set) { loadSettingsSafe(); return '<div class="card"><div class="cb"><span class="skeleton" style="width:40%"></span></div></div>'; }
  const row = (what, addr, use, copy = addr) => `<tr><td class="pt-name">${esc(what)}</td><td><div class="pt-addr"><code title="${esc(addr)}">${esc(addr)}</code>${copy ? `<button class="icon-btn" data-copy="${esc(copy)}" title="Copy ${esc(addr)}">${icon.copy}</button>` : ''}</div></td><td class="note">${esc(use)}</td></tr>`;
  const valet = set.front_door_used === 'valet';
  const rows = [
    row('Your sites', `https://<name>.${set.tld}`, valet ? 'Ports 443/80 through Laravel Valet, forwarded to Apache or the dev server' : `Built-in front door on ports ${set.https_port}/${set.http_port}`, ''),
    row('Apache (internal)', `127.0.0.1:${set.apache_port}`, 'Serves PHP projects behind the front door. You never need to open this directly.'),
    ...(st?.nginx?.running ? [row('Nginx (internal)', `127.0.0.1:${set.nginx_port || 8746}`, 'Serves PHP projects that chose Nginx, behind the front door.')] : []),
    row('LocalSites MySQL', `127.0.0.1:${set.mysql_port}`, 'TCP for apps, GUI tools and Docker. Use the socket below for "localhost".'),
    row('LocalSites MySQL socket', set.mysql_socket, 'What "localhost" means to PHP in LocalSites projects set to LocalSites MySQL'),
    ...((st?.import_sources || []).filter(x => x.id === 'mamp-pro' && x.found).map(() => row('MAMP PRO MySQL', '127.0.0.1:8889', 'Your MAMP PRO databases (socket /Applications/MAMP/tmp/mysql/mysql.sock). Projects set to MAMP PRO use it for "localhost".'))),
    ...S.sites.filter(x => x.runtime === 'app' && x.port).map(x => row(`${x.name} dev server`, `127.0.0.1:${x.port}`, `${typeOf(x).label} · https://${x.domain}`)),
    ...(st?.addons || []).filter(a => a.installed).map(a => row(a.name, `127.0.0.1:${a.port}`, a.id === 'mailpit' ? `SMTP; inbox on 127.0.0.1:${a.ui_port} and ${a.ui_url}` : a.enabled ? 'Running when switched on' : 'Off')),
  ];
  return `<div class="card"><table class="t ports"><thead><tr><th>Service</th><th>Address</th><th>Used for</th></tr></thead><tbody>${rows.join('')}</tbody></table></div>`;
}

/* --- services --- */
const ADDON_IC = { mailpit: () => icon.mail, valkey: () => icon.memory, redis: () => brand('redis', '#FF4438') || icon.memory, memcached: () => icon.memory,
  postgres: () => brand('postgresql', '#4169E1') || icon.db, meilisearch: () => icon.search, minio: () => icon.box2 };
function addonState(a) {
  if (a.installing) return ['busy', 'Installing…'];
  if (!a.installed) return ['off', 'Not installed'];
  if (a.problem) return ['err', 'Stopped'];
  if (a.running) return ['on', 'Running'];
  if (a.enabled) return ['warn', 'Starting…'];
  return ['off', 'Off'];
}
function addonCard(a) {
  const [state, label] = addonState(a);
  const clash = (S.status?.addons || []).find(o => o.id !== a.id && o.enabled && (a.conflicts || []).includes(o.id));
  let foot;
  if (!a.installed) foot = a.installing ? `<span class="note"><span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span> Installing with Homebrew…</span><span class="spacer"></span>${UI.btn('Log', { ic: 'logs', kind: 'ghost', attrs: `data-addonlog="${a.id}" data-install="1"` })}`
    : `${UI.btn('Install', { ic: 'download2', kind: 'primary', attrs: `data-addoninstall="${a.id}"`, title: a.install_command })}<span class="note mono-s">${esc(a.install_command || 'Homebrew is needed: brew.sh')}</span>`;
  else foot = `${UI.toggle(a.enabled, `data-addontoggle="${a.id}"`, a.enabled ? 'Switch off' : 'Switch on')}
    ${a.id === 'mailpit' && a.running ? UI.btn('Open inbox', { ic: 'inbox', attrs: `data-openweb="${esc(a.ui_url)}"` }) + UI.btn('Send test', { ic: 'mailCheck', kind: 'ghost', attrs: 'data-cmd="addontestmail"' }) : ''}
    ${a.id !== 'mailpit' && a.ui_url && a.running ? UI.btn('Open dashboard', { ic: 'external', attrs: `data-openweb="${esc(a.ui_url)}"` }) : ''}
    ${a.id === 'postgres' && a.running ? UI.btn('Browse', { ic: 'table', attrs: 'data-dataconn="localsites-pg"' }) : ''}
    ${a.kind === 'cache' && a.running ? UI.btn('Flush', { ic: 'eraser', kind: 'ghost', attrs: `data-addonflush="${a.id}"`, title: 'Empty the cache' }) : ''}
    <span class="spacer"></span>${UI.iconBtn('plugZap', 'Connect a project', `data-addonconnect="${a.id}"`)}${UI.iconBtn('gear', 'Options', `data-addonopts="${a.id}"`)}${UI.iconBtn('logs', 'Log', `data-addonlog="${a.id}"`)}`;
  const body = [
    a.installed ? UI.kv(a.id === 'mailpit' ? 'SMTP' : 'Address', `<code>127.0.0.1:${a.port}</code>`) : '',
    a.id === 'minio' && a.installed ? UI.kv('Login', '<code>localsites</code> / <code>localsites-dev</code>') : '',
    a.ui_url && a.installed ? UI.kv(a.id === 'mailpit' ? 'Inbox' : 'Dashboard', a.running ? `<a class="link" data-openweb="${esc(a.ui_url)}">${esc(a.ui_url.replace('https://', ''))}</a>` : '<span class="note">when running</span>') : '',
    a.problem ? `<div class="note warn">${esc(a.problem)}</div>` : '',
    clash && !a.enabled ? `<div class="note">${esc(clash.name)} is on and uses the same kind of service. Run one, or give this one another port.</div>` : '',
  ].join('');
  return UI.card({ ic: ADDON_IC[a.id]?.() || icon.box2, title: a.name, tags: UI.tag(a.license), sub: esc(a.description), status: UI.status(state, label), body, foot, on: a.running, attrs: `data-addon="${a.id}"` });
}
function adminerCard() {
  const a = S.status?.adminer || {};
  return UI.card({ ic: icon.db, title: 'Adminer', tags: UI.tag('Apache-2.0'), sub: 'Manage databases in the browser, like phpMyAdmin. Sign in with a database’s own login (Databases → Connect).',
    status: UI.status(a.enabled ? 'on' : 'off', a.enabled ? 'On' : 'Off'), on: a.enabled,
    body: a.enabled ? UI.kv('Address', `<a class="link" data-openweb="${esc(a.url)}">${esc((a.url || '').replace('https://', ''))}</a>`) : UI.kv('Version', esc(a.version || '')),
    foot: `${UI.toggle(a.enabled, 'data-adminertoggle="1"', a.enabled ? 'Switch off' : 'Switch on')}${a.enabled ? UI.btn('Open', { ic: 'external', attrs: `data-openweb="${esc(a.url)}"` }) : '<span class="note">Downloads a checked copy the first time</span>'}`,
    attrs: 'data-tool="adminer"' });
}
// One switch at a time: the first setup downloads Adminer (several seconds), and a second click
// while that runs used to start a second download.
async function setAdminer(on) {
  if (setAdminer.busy) return setAdminer.busy;
  if (on && !S.status?.adminer?.installed) toast('Setting up Adminer…');
  setAdminer.busy = (async () => {
    const r = await act(() => bridge.call('tools.adminer', { enabled: on }), 'Adminer');
    await act(loadStatus); render();
    if (r && on) { toast('Adminer is ready at ' + r.adminer.url); openWeb(r.adminer.url); }
    return r;
  })();
  try { return await setAdminer.busy; } finally { setAdminer.busy = null; }
}
function viewServices() {
  const st = S.status; if (!st) return '';
  const since = (p) => p.running && p.since ? 'since ' + new Date(p.since).toLocaleTimeString() : (p.last_error || '');
  const rows = [
    [st.front_door_running, 'Domains (*.test)', st.front_door === 'valet' ? 'Using Laravel Valet’s nginx; Valet sites keep working' : 'Built-in HTTPS proxy and DNS', ''],
    [st.apache.running, 'Apache', since(st.apache), st.apache.pid ? 'pid ' + st.apache.pid : ''],
    ...(st.nginx?.running ? [[true, 'Nginx', since(st.nginx), st.nginx.pid ? 'pid ' + st.nginx.pid : '']] : []),
    [st.mysql.running, 'MySQL 8.4 LTS', st.mysql_installed ? (since(st.mysql) || 'Starts when a project or database needs it') : 'Not installed: brew install mysql@8.4', st.mysql.pid ? 'pid ' + st.mysql.pid : ''],
    ...st.php.map(p => [p.running, 'PHP ' + p.version, `${p.full} · ${p.sites} project${p.sites === 1 ? '' : 's'}${p.warning ? ' · ' + p.warning : ''}`, '']),
  ];
  const core = `<div class="card"><table class="t core"><tbody>` + rows.map(([on, name, detail, extra]) =>
    `<tr><td style="width:120px">${UI.status(on ? 'on' : 'off', on ? 'Running' : 'Stopped')}</td><td style="width:190px"><span style="font-weight:500">${esc(name)}</span></td><td class="note">${esc(detail)}</td><td class="note num">${esc(extra)}</td></tr>`).join('') + `</tbody></table></div>`;
  const ads = st.addons || [];
  return `<div class="c-page">
    ${UI.section({ title: 'Core', note: 'What serves your sites. These start and stop with the projects that need them.', right: UI.btn('Connect to MySQL', { ic: 'plugZap', attrs: 'data-cmd="mysqlconnect"' }) })}${core}
    ${UI.section({ title: 'Ports and addresses', note: 'Everything listens on 127.0.0.1, so only this Mac can reach it. Click to copy.' })}${portsTable()}
    ${UI.section({ title: 'Add-ons', note: 'Open-source services that work with any language: Laravel, Next.js, Django, Rails and more. Installed with Homebrew when you ask, and only reachable from this Mac.' })}
    <div class="c-grid">${ads.map(addonCard).join('')}</div>
    ${UI.section({ title: 'Web tools', note: 'Small tools LocalSites serves for you, only on this Mac.' })}
    <div class="c-grid">${adminerCard()}</div></div>`;
}
function addonConnectDialog(a) {
  const stacks = a.snippets || [];
  let cur0 = store.get('addonStack', 'Laravel');
  if (!stacks.some(x => x.stack === cur0)) cur0 = stacks[0]?.stack;
  const ic = { 'Laravel': brand('laravel', '#FF2D20'), 'Node / Next.js': brand('nodedotjs', '#5FA04E'), 'Django': brand('django', '#44B78B'), 'Rails': brand('rubyonrails', '#D30001'), 'Any language': icon.code };
  const draw = (k) => { const sn = stacks.find(x => x.stack === k);
    return `${UI.tabs(stacks.map(x => [x.stack, x.stack, ic[x.stack] || '']), k, 'data-stack')}<p class="note" style="margin:12px 0 6px">Add to <code>${esc(sn.file)}</code>:</p>${UI.code(sn.text)}`; };
  const p = dialog(`<h3>Connect a project to ${esc(a.name)}</h3><div id="stackBox">${draw(cur0)}</div>
    <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn primary" data-dlg="ok">Done</button></div>`);
  $('#keysDialog').onclick = (e) => { const b = e.target.closest('[data-stack]'); if (!b) return; store.set('addonStack', b.dataset.stack); $('#stackBox').innerHTML = draw(b.dataset.stack); };
  return p;
}
async function addonOptionsDialog(a) {
  const num = (id, v, ph) => `<input class="input" id="${id}" value="${esc(v || '')}" placeholder="${esc(ph)}" inputmode="numeric" style="width:120px">`;
  const fields = [UI.field(a.id === 'mailpit' ? 'SMTP port' : 'Port', num('aoPort', a.port, a.default_port), '127.0.0.1 only. Change it if another program already uses this port.')];
  if (a.id === 'mailpit') fields.push(UI.field('Inbox port', num('aoUI', a.ui_port, a.default_ui_port)), UI.field('Keep emails', `<span>${UI.toggle(a.keep_mail, 'id="aoKeep"', 'Keep emails')}</span>`, 'Off: the inbox empties when Mailpit restarts.'));
  if (a.kind === 'cache' && a.id !== 'memcached') fields.push(UI.field('Save to disk', `<span>${UI.toggle(a.persist, 'id="aoPersist"', 'Save to disk')}</span>`, 'Off: data lives in memory only, like a fresh cache after restarts.'));
  if (a.id === 'memcached') fields.push(UI.field('Memory (MB)', num('aoMem', a.memory_mb, 64)));
  const p = dialog(`<h3>${esc(a.name)} options</h3><div class="c-form">${fields.join('')}</div><p class="note warn" id="aoErr" role="alert"></p>
    <div class="form-row" style="justify-content:flex-end;margin-top:8px"><button class="btn ghost" data-dlg="cancel">Cancel</button><button class="btn primary" id="aoSave">Save</button></div>`);
  const box = $('#keysDialog');
  box.querySelector('#aoSave').onclick = async () => {
    const v = (id) => box.querySelector(id);
    const patch = { id: a.id, port: Number(v('#aoPort').value) || 0 };
    if (v('#aoUI')) patch.ui_port = Number(v('#aoUI').value) || 0;
    if (v('#aoKeep')) patch.keep_mail = v('#aoKeep').checked;
    if (v('#aoPersist')) patch.persist = v('#aoPersist').checked;
    if (v('#aoMem')) patch.memory_mb = Number(v('#aoMem').value) || 64;
    try { const r = await bridge.call('addons.update', patch); box.querySelector('[data-dlg="cancel"]').click(); logToast(r); await act(loadStatus); render(); }
    catch (e) { box.querySelector('#aoErr').textContent = e.message; }
  };
  return p;
}
async function pollShareInstall(id) {
  for (let i = 0; i < 300; i++) {
    await new Promise(r => setTimeout(r, 2000));
    await act(loadStatus); renderDetail();
    const p = (S.status?.share_providers || []).find(x => x.id === id);
    if (p && !p.installing) { toast(p.installed ? `${p.name} installed` : `${p.name} could not be installed`, p.installed ? 'ok' : 'err'); return render(); }
  }
}
async function pollAddonInstall(id) {
  for (let i = 0; i < 300; i++) {
    await new Promise(r => setTimeout(r, 2000));
    await act(loadStatus); if (cur().view === 'services') renderView();
    const a = (S.status?.addons || []).find(x => x.id === id);
    if (a && !a.installing) { toast(a.installed ? `${a.name} installed. Switch it on when you need it.` : `${a.name} could not be installed; see the log`, a.installed ? 'ok' : 'err'); render(); return; }
  }
}

/* --- Log Stream. Level filters with counts,
   search that highlights as it filters, pause and follow with a "new lines" pill, and bursts of the same line
   folded into one "12 similar" row. Fed by the logs LocalSites already keeps. --- */
const LS_LEVELS = [['debug', 'Debug', 'DBG'], ['info', 'Info', 'INF'], ['warn', 'Warn', 'WRN'], ['error', 'Error', 'ERR']];
const LSV = { lines: [], seq: 0, levels: new Set(['debug', 'info', 'warn', 'error']), q: '', paused: false, shown: 0, follow: true, behind: 0, open: new Set(), seen: new Set(), arrivals: [], key: '' };
const MON = { Jan: 0, Feb: 1, Mar: 2, Apr: 3, May: 4, Jun: 5, Jul: 6, Aug: 7, Sep: 8, Oct: 9, Nov: 10, Dec: 11 };
// One line of any log LocalSites keeps (Apache, PHP, MySQL, access logs, dev servers, Homebrew) as
// { ts, level, service, message }. Lines without a time keep the one before them (stack traces).
function parseLogLine(text, source, prevTs) {
  let m, ts = null, level = '', service = source, msg = text;
  if ((m = text.match(/^\[(?:\w{3} )?(\w{3}) (\d{1,2}) (\d\d):(\d\d):(\d\d)(?:\.(\d+))? (\d{4})\] \[(?:([\w-]+):)?(\w+)\] (?:\[pid \d+(?::tid \d+)?\] )?(?:\[client [^\]]+\] )?(.*)$/))) { // Apache
    ts = new Date(+m[7], MON[m[1]], +m[2], +m[3], +m[4], +m[5], +(m[6] || '0').slice(0, 3).padEnd(3, '0')).getTime(); service = m[8] || 'apache'; level = m[9]; msg = m[10];
  } else if ((m = text.match(/^\[(\d{1,2})-(\w{3})-(\d{4}) (\d\d):(\d\d):(\d\d)(?: ([\w/+-]+))?\] (.*)$/))) { // PHP
    ts = new Date(+m[3], MON[m[2]], +m[1], +m[4], +m[5], +m[6]).getTime(); service = 'php'; msg = m[8];
    const k = msg.match(/^PHP (Fatal error|Parse error|Warning|Notice|Deprecated|Strict Standards)/); level = k ? k[1] : '';
  } else if ((m = text.match(/^(\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:?\d\d)?)\s+(?:\d+\s+)?(?:\[(\w+)\]\s+)?(?:\[[A-Z]{2}-\d+\]\s+)?(?:\[(\w+)\]\s+)?(.*)$/))) { // MySQL, ISO stamps
    ts = Date.parse(m[1].replace(' ', 'T')); level = m[2] || ''; service = m[3] ? m[3].toLowerCase() : source; msg = m[4];
  } else if ((m = text.match(/^(\S+) \S+ \S+ \[(\d\d)\/(\w{3})\/(\d{4}):(\d\d):(\d\d):(\d\d) [^\]]+\] "([^"]*)" (\d{3}) (.*)$/))) { // access log
    ts = new Date(+m[4], MON[m[3]], +m[2], +m[5], +m[6], +m[7]).getTime(); service = 'http'; msg = `${m[8]} ${m[9]} ${m[10]}`;
    level = +m[9] >= 500 ? 'error' : +m[9] >= 400 ? 'warn' : 'info';
  }
  const l = (level || msg).toLowerCase();
  const lv = /^(error|err|fatal|crit|critical|emerg|alert|panic|fatal error|parse error)$/.test(level.toLowerCase()) || (!level && /\b(error|fatal|panic|exception|failed|denied)\b/.test(l)) ? 'error'
    : /^(warn|warning|deprecated|strict standards)$/.test(level.toLowerCase()) || (!level && /\b(warn|warning|deprecated)\b/.test(l)) ? 'warn'
    : /^(debug|trace\d*|note)$/.test(level.toLowerCase()) ? 'debug' : 'info';
  return { ts: Number.isFinite(ts) ? ts : prevTs, level: lv, service: String(service).slice(0, 12), message: msg };
}
const lsSource = () => ({ apache: 'apache', mysql: 'mysql', setup: 'brew', addon: S.log.folder, 'addon-install': S.log.folder, 'site-error': 'apache', 'site-php': 'php', 'site-access': 'http', 'site-app': 'app', 'site-install': 'install' }[S.log.kind] || S.log.kind);
// A new tail of the log: keep the lines we already have, append only what's new
function lsIngest(text) {
  const key = S.log.kind + '|' + S.log.folder, raw = text.split('\n').filter(l => l.trim());
  if (key !== LSV.key) Object.assign(LSV, { key, lines: [], shown: 0, open: new Set(), seen: new Set(), behind: 0, follow: true, arrivals: [] });
  const old = LSV.lines.map(l => l.raw);
  let start = 0;
  if (old.length) {
    const tail = old.slice(-3).join('\n');
    start = -1;
    for (let p = raw.length - 1; p >= 0; p--) if (raw.slice(Math.max(0, p - 2), p + 1).join('\n') === tail) { start = p + 1; break; }
    if (start === -1) { LSV.lines = []; start = 0; } // rotated or cut: start over
  }
  const fresh = raw.slice(start), first = !old.length;
  let prev = LSV.lines.at(-1)?.ts ?? null;
  for (const r of fresh) { const x = parseLogLine(r, lsSource(), prev); prev = x.ts; LSV.lines.push({ id: ++LSV.seq, raw: r, ...x }); if (first) LSV.seen.add(LSV.seq); }
  if (LSV.lines.length > 500) LSV.lines = LSV.lines.slice(-500);
  if (!first && fresh.length) { const now = Date.now(); for (let i = 0; i < fresh.length; i++) LSV.arrivals.push(now); }
  if (!LSV.paused) LSV.shown = LSV.lines.length;
  return fresh.length;
}
const lsTemplate = (m) => m.replace(/\b[0-9a-f]{6,}\b/gi, '#').replace(/\d+(\.\d+)?/g, '#');
const lsClock = (ts) => { if (ts == null) return ''; const d = new Date(ts), p = (n, w = 2) => String(n).padStart(w, '0'); return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`; };
const lsMark = (text, q) => { if (!q) return esc(text); let out = '', at = 0; const lower = text.toLowerCase(); let hit = lower.indexOf(q);
  while (hit !== -1) { out += esc(text.slice(at, hit)) + `<mark>${esc(text.slice(hit, hit + q.length))}</mark>`; at = hit + q.length; hit = lower.indexOf(q, at); }
  return out + esc(text.slice(at)); };
const lsVisible = () => LSV.paused ? LSV.lines.slice(0, LSV.shown) : LSV.lines;
function lsFiltered() { const q = LSV.q.trim().toLowerCase(); return lsVisible().filter(l => LSV.levels.has(l.level) && (!q || l.message.toLowerCase().includes(q) || l.service.includes(q))); }
function lsRow(l, q, nested) {
  const lv = LS_LEVELS.find(x => x[0] === l.level), fresh = !LSV.seen.has(l.id);
  return `<div class="ls-row lv-${l.level} ${nested ? 'nested' : ''} ${fresh ? 'fresh' : ''}"><span class="ls-ts">${lsClock(l.ts)}</span><span class="ls-lv">${lv[2]}</span><span class="ls-svc">${esc(l.service)}</span><span class="ls-msg">${lsMark(l.message, q)}</span></div>`;
}
function lsRowsHTML() {
  const q = LSV.q.trim().toLowerCase(), f = lsFiltered(), out = [];
  if (!f.length) return `<div class="ls-empty"><div><p>${q ? `No lines match “${esc(LSV.q.trim())}”` : 'Waiting for logs…'}</p><p class="note">${q ? 'Try fewer words or turn on more levels.' : 'New lines show up here as they arrive.'}</p></div></div>`;
  for (let i = 0; i < f.length;) {
    const k = (l) => `${l.level}|${l.service}|${lsTemplate(l.message)}`, key = k(f[i]);
    let j = i + 1; while (j < f.length && k(f[j]) === key) j++;
    if (j - i >= 3) {
      const g = f.slice(i, j), last = g.at(-1), id = g[0].id, open = LSV.open.has(id), lv = LS_LEVELS.find(x => x[0] === last.level);
      out.push(`<div class="ls-group lv-${last.level}"><button class="ls-row ls-gh" data-lsgroup="${id}" aria-expanded="${open}"><span class="ls-ts">${lsClock(last.ts)}</span><span class="ls-lv">${lv[2]}</span><span class="ls-svc">${esc(last.service)}</span>
        <span class="ls-msg ls-gmsg"><span class="ls-gtext">${lsMark(last.message, q)}</span><span class="ls-sim ${open ? 'open' : ''}">${icon.chevronR}${g.length} similar</span></span></button>
        ${open ? `<div class="ls-nest">${g.map(l => lsRow(l, q, true)).join('')}</div>` : ''}</div>`);
    } else for (let x = i; x < j; x++) out.push(lsRow(f[x], q, false));
    i = j;
  }
  return out.join('');
}
function lsToolbar() {
  const counts = { debug: 0, info: 0, warn: 0, error: 0 }; for (const l of lsVisible()) counts[l.level]++;
  return `<div class="ls-bar"><div class="ls-levels" role="group" aria-label="Levels">${LS_LEVELS.map(([id, label]) => { const on = LSV.levels.has(id);
      return `<button class="ls-lvbtn ${on ? 'on' : ''}" data-lslevel="${id}" aria-pressed="${on}"><span class="ls-dot lv-${id}"></span>${label}<span class="n">${counts[id]}</span></button>`; }).join('')}</div>
    <div class="ls-tools"><label class="ls-search">${icon.search}<input id="logQ" placeholder="Search" value="${esc(LSV.q)}" spellcheck="false" autocomplete="off" aria-label="Search logs">${LSV.q ? `<button class="ls-clear" data-lsclear="1" aria-label="Clear search">${icon.x}</button>` : ''}</label>
      <button class="ls-pause" data-lspause="1" aria-pressed="${LSV.paused}" aria-label="${LSV.paused ? 'Resume stream' : 'Pause stream'}">${LSV.paused ? icon.play : icon.pause}<span>${LSV.paused ? 'Resume' : 'Pause'}</span></button></div></div>`;
}
function lsFoot() {
  const now = Date.now(); LSV.arrivals = LSV.arrivals.filter(t => now - t < 5000);
  const q = LSV.q.trim(), f = lsFiltered().length, v = lsVisible().length;
  return `<span class="ls-live ${LSV.paused ? 'paused' : ''}"><span></span></span><span class="ls-state">${LSV.paused ? 'Paused · buffering' : LSV.follow ? 'Live · following' : 'Live · scrolled back'} · ${(LSV.arrivals.length / 5).toFixed(1)} lines/s</span>
    <span class="ls-count">${q ? `${f} of ${v}` : plural(LSV.lines.length, 'line')}</span>`;
}
function lsPill() {
  const n = LSV.paused ? Math.max(0, LSV.lines.length - LSV.shown) : LSV.behind;
  return n ? `<button class="ls-pill" data-lsjump="1">${icon.down}${n} new ${n === 1 ? 'line' : 'lines'}</button>` : '';
}
function viewLogs() {
  return `<div class="ls"><div class="ls-top">${lsToolbar()}</div><div class="ls-wrap"><div class="ls-list" id="logbox" role="log" aria-live="off" tabindex="0">${lsRowsHTML()}</div><div class="ls-pillbox">${lsPill()}</div></div><div class="ls-foot">${lsFoot()}</div></div>`;
}
// Redraw in place: the toolbar keeps its search box (and focus); the list follows new lines only while it's
// at the bottom; lines that arrived while scrolled back are counted on the pill.
function lsRedraw({ added = 0, bar = true } = {}) {
  const box = $('#logbox');
  if (!box || cur().view !== 'logs') return;
  if (bar) { const q = $('#logQ'), focused = document.activeElement === q, pos = q?.selectionStart; $('.ls-top').innerHTML = lsToolbar(); if (focused) { const n = $('#logQ'); n.focus(); n.setSelectionRange(pos, pos); } }
  box.innerHTML = lsRowsHTML();
  for (const l of LSV.lines) LSV.seen.add(l.id);
  if (LSV.follow && !LSV.paused) { box.scrollTop = box.scrollHeight; LSV.behind = 0; } else if (added && !LSV.paused) LSV.behind += added;
  $('.ls-pillbox').innerHTML = lsPill(); $('.ls-foot').innerHTML = lsFoot();
  $('#view')._html = viewLogs(); // matches the page: a background refresh leaves it alone
}
function lsJump() {
  LSV.paused = false; LSV.follow = true; LSV.behind = 0; LSV.shown = LSV.lines.length;
  lsRedraw();
  const box = $('#logbox'); if (box) box.scrollTo({ top: box.scrollHeight, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
}
document.addEventListener('scroll', (e) => {
  if (e.target.id !== 'logbox') return;
  const el = e.target, at = el.scrollHeight - el.scrollTop - el.clientHeight < 12;
  if (at !== LSV.follow) { LSV.follow = at; if (at) LSV.behind = 0; $('.ls-pillbox').innerHTML = lsPill(); $('.ls-foot').innerHTML = lsFoot(); }
}, true);
setInterval(() => { if (cur().view === 'logs' && !document.hidden) { const f = $('.ls-foot'); if (f) f.innerHTML = lsFoot(); } }, 1000);
async function loadLog() {
  const r = await act(() => bridge.call('logs', { kind: S.log.kind, folder: S.log.folder, lines: 400 }));
  if (!r) return;
  const switched = LSV.key !== S.log.kind + '|' + S.log.folder, added = lsIngest(r.text || '');
  if (cur().view !== 'logs') return;
  if (switched || !$('#logbox')) { renderView(); const b = $('#logbox'); if (b) b.scrollTop = b.scrollHeight; return; }
  if (added) lsRedraw({ added });
}

/* --- doctor --- */
function viewDoctor() {
  if (!S.doctor) return emptyState({ ic: '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>', title: 'Running checks…', text: 'Runtimes, services, domains, certificates and every running project.' });
  return `<div class="pad"><div class="card doc-run"><div class="ch">${icon.doctor}<h3>${S.doctorBusy ? 'Checking again…' : (S.doctor || []).some(c => c.status === 'fail') ? 'Some checks failed' : 'Everything passed'}</h3><span class="spacer"></span>
      <span class="note">${S.doctorBusy ? '' : S.doctorTook ? `took ${(S.doctorTook / 1000).toFixed(1)} s · ` : ''}${S.doctorWhen && !S.doctorBusy ? 'checked ' + ago(S.doctorWhen) : ''}</span>
</div>
      <div class="cb">${doctorPipeline()}</div></div>
    <div class="card">${checksTable(S.doctor)}</div>
    <p class="note" style="margin-top:12px">Same checks from the terminal: <code>localsites doctor</code>. AI agents can run them with the <code>doctor</code> and <code>sites_check</code> tools.</p></div>`;
}

/* --- import --- */
function viewImport() {
  const st = S.status; if (!st) return '';
  const srcs = st.import_sources.filter(s => (s.found || s.mysql_running) && !s.same_mysql_as), im = S.imp;
  if (!srcs.length) return emptyState({ fig: 'loupe', ic: 'import', title: 'Nothing to import', text: 'LocalSites didn’t find MAMP, MAMP PRO or XAMPP on this computer. Projects in your folders are picked up automatically.',
    actions: `<button class="btn" data-cmd="addfolder">${icon.folderPlus}Add a projects folder</button>` });
  const srcChips = srcs.length ? srcs.map(s => `<span class="chip"><span class="dot ${s.mysql_running ? 'on' : 'off'}"></span>${esc(s.name)} ${s.mysql_running ? '· MySQL running' : '· MySQL stopped'}</span>`).join(' ') : '<span class="note">No MAMP, MAMP PRO or XAMPP found.</span>';
  const hosts = S.sites.filter(s => s.imported_from).length;
  // MAMP and XAMPP keep projects in htdocs: offer to add it as a projects folder
  const htdocs = st.import_sources.filter(s => s.found && s.docroot && s.docroot_projects && !S.sites.some(x => x.path.startsWith(s.docroot + '/')));
  const htdocsPart = htdocs.map(s => `<div class="callout">${icon.folderPlus}<div><b>${s.docroot_projects === 1 ? '1 project' : s.docroot_projects + ' projects'} in ${esc(s.name)}’s htdocs folder</b>
    <p class="note">${esc(s.docroot)} isn’t one of your projects folders yet. Add it and LocalSites lists those projects, with the PHP version ${esc(s.name)} used. Nothing in it is changed.</p>
    <button class="btn sm" data-addroot="${esc(s.docroot)}" data-addrootlabel="${esc(s.id)}-htdocs">${icon.folderPlus}Add as a projects folder</button></div></div>`).join('');
  let dbPart = `<div class="form-row">
      <select class="input" id="impSource">${srcs.map(s => `<option value="${s.id}" ${s.id === im.source ? 'selected' : ''}>${esc(s.name)}</option>`).join('')}</select>
      <input class="input" id="impUser" value="${esc(im.user)}" placeholder="MySQL user" style="width:120px">
      <input class="input" id="impPass" type="password" placeholder="MySQL password" style="width:180px" value="${esc(im.password)}">
      <button class="btn" data-cmd="impconnect">Connect</button></div>
      <p class="note">The password is used only for this copy and is never saved.</p>${im.err ? `<p class="note warn">${esc(im.err)}</p>` : ''}`;
  if (im.dbs) {
    const existing = new Set((S.dbs || []).map(d => d.name));
    dbPart += `<table class="t" style="margin-top:10px"><thead><tr><th></th><th>Database</th><th class="num">Tables</th><th class="num">Size</th><th>Result</th></tr></thead><tbody>` +
      im.dbs.map(d => {
        const rep = im.reports[d.name], already = existing.has(d.name) && !rep;
        let result = already ? '<span class="note">Already in LocalSites</span>' : '';
        if (rep?.running) result = '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>';
        else if (rep?.error) result = `<span class="bad">${esc(rep.error)}</span>`;
        else if (rep) result = rep.ok ? `<span class="ok">✓ ${rep.tables.length} tables identical · ${esc(rep.duration)}</span>` : '<span class="bad">✗ differences found</span>';
        return `<tr><td style="width:30px"><input type="checkbox" data-impdb="${esc(d.name)}" ${im.chosen.has(d.name) ? 'checked' : ''} ${already || rep ? 'disabled' : ''}></td>
          <td><b>${esc(d.name)}</b></td><td class="num">${d.tables}</td><td class="num">${d.size_mb.toFixed(1)} MB</td><td>${result}</td></tr>`;
      }).join('') + `</tbody></table>
      <div class="form-row" style="margin-top:12px"><button class="btn primary" data-cmd="impcopy" ${im.chosen.size && !im.running ? '' : 'disabled'}>Copy & verify ${im.chosen.size || ''} database${im.chosen.size === 1 ? '' : 's'}</button>
      <span class="note">Read-only on the source. Every table is compared row by row after copying.</span></div>`;
  }
  return `<div class="pad">
    <div class="card"><div class="ch"><h3>Found on this computer</h3></div><div class="cb">${srcChips}
      <p class="note" style="margin-bottom:0">${hosts} project${hosts === 1 ? '' : 's'} picked up their PHP version and web root from these tools.</p>${htdocsPart}</div></div>
    <div class="card"><div class="ch"><h3>Copy databases to LocalSites MySQL 8.4</h3></div><div class="cb">${dbPart}</div></div>
    <div class="card"><div class="ch">${icon.plug}<h3>Connect projects to their copied database</h3></div>${connectTable()}</div></div>`;
}

function connectTable() {
  const dbs = new Set((S.dbs || []).filter(d => d.owner).map(d => d.name));
  const rows = S.sites.filter(s => s.env?.database);
  if (!rows.length) return '<p class="note" style="padding:12px 16px;margin:0">No project has database settings in a <code>.env</code> file. Use <b>Copy .env</b> in Databases to get a login for other setups.</p>';
  return `<table class="t ct"><thead><tr><th>Project</th><th>Database in .env</th><th>Uses</th><th></th></tr></thead><tbody>${rows.map(s => {
    const connected = s.db_server === 'localsites' && s.database === s.env.database;
    let action;
    if (connected) action = `<span class="ok">${icon.check} Connected</span>${s.env.backups ? ` <button class="btn ghost" data-restoreenv="${esc(s.folder)}">${icon.undo}Undo</button>` : ''}`;
    else if (dbs.has(s.env.database)) action = `<button class="btn primary" data-connectdb="${esc(s.folder)}" data-db="${esc(s.env.database)}">${icon.plug}Connect</button>`;
    else action = '<span class="note">Copy this database first</span>';
    return `<tr><td><b>${esc(s.folder)}</b></td><td><code>${esc(s.env.database)}</code></td><td>${s.db_server === 'mamp' ? 'MAMP MySQL' : 'MySQL 8.4'}</td><td style="text-align:right;white-space:nowrap">${action}</td></tr>`;
  }).join('')}</tbody></table>
  <p class="note" style="padding:0 16px 12px;margin:8px 0 0">Connect backs up <code>.env</code>, writes the database’s own login into it and switches the project to LocalSites MySQL. Undo puts everything back. Your MAMP databases are never changed.</p>`;
}

/* --- Env Editor: a project's variables (or php.ini overrides) as rows, values hidden until asked --- */
// Lines from a .env file: NAME=value, "export NAME=value", quotes and # comments
function parseEnv(text) {
  const out = [];
  for (let line of text.split('\n')) {
    line = line.trim().replace(/^export\s+/, '');
    if (!line || line.startsWith('#')) continue;
    const i = line.indexOf('=');
    if (i < 1) { out.push({ bad: line }); continue; }
    let v = line.slice(i + 1).trim();
    const q = v[0];
    if ((q === '"' || q === "'") && v.lastIndexOf(q) > 0) v = v.slice(1, v.lastIndexOf(q));
    else v = v.replace(/\s+#.*$/, '');
    out.push({ k: line.slice(0, i).trim(), v });
  }
  return out;
}
function kvEditor(s, env) {
  let rows = Object.entries((env ? s.env_vars : s.php_ini) || {}).map(([k, v]) => ({ k, v })), show = !env;
  if (!rows.length) rows.push({ k: '', v: '' });
  const nameOK = env ? /^[A-Za-z_][A-Za-z0-9_]*$/ : /^[a-z0-9_.]+$/i;
  const dup = (i) => rows[i].k && rows.findIndex(r => r.k === rows[i].k) !== i;
  const rowsHTML = () => rows.map((r, i) => `<div class="kv-row"><input class="input mono sm ${(r.k && !nameOK.test(r.k)) || dup(i) ? 'invalid' : ''}" data-kvk="${i}" value="${esc(r.k)}" placeholder="${env ? 'NAME' : 'memory_limit'}" spellcheck="false" autocomplete="off" aria-label="Name">
      <span class="kv-eq">=</span><input class="input mono sm" data-kvv="${i}" type="${show ? 'text' : 'password'}" value="${esc(r.v)}" placeholder="${env ? 'value' : '512M'}" spellcheck="false" autocomplete="off" aria-label="Value">
      <button class="icon-btn" data-kvdel="${i}" title="Remove">${icon.trash}</button></div>`).join('');
  const p = dialog(`<h3>${env ? 'Environment variables' : 'php.ini overrides'} · ${esc(s.name)}</h3>
    <p class="note">${env ? 'Set for the dev server when it starts. Your <code>.env</code> file is not changed.' : 'For this project only, on top of Settings → PHP.'}</p>
    <div class="kv-tools">${UI.btn('Add', { ic: 'plus', attrs: 'data-kvadd="1"' })}${UI.btn(env ? 'Paste .env' : 'Paste lines', { ic: 'copy', kind: 'ghost', attrs: 'data-kvpaste="1"' })}<span class="spacer"></span>
      ${env ? `<label class="form-row note" style="gap:6px"><input type="checkbox" id="kvShow"> Show values</label>` : ''}</div>
    <div class="kv-paste" id="kvPaste" hidden><textarea class="input mono" id="kvPasteText" rows="5" spellcheck="false" placeholder="${env ? 'API_URL=http://127.0.0.1:8000\nexport DEBUG=true' : 'memory_limit=512M\nmax_execution_time=120'}"></textarea>
      <div class="form-row" style="justify-content:flex-end">${UI.btn('Add these', { kind: 'primary', attrs: 'data-kvimport="1"' })}</div></div>
    <div class="kv-head"><span>Name</span><span></span><span>Value</span><span></span></div>
    <div class="kv-rows" id="kvRows">${rowsHTML()}</div>
    <p class="note warn" id="kvErr" role="alert"></p>
    <div class="form-row" style="justify-content:flex-end;margin-top:8px"><button class="btn ghost" data-dlg="cancel">Cancel</button><button class="btn primary" id="kvSave">Save</button></div>`);
  const box = $('#keysDialog'), draw = (focus) => { box.querySelector('#kvRows').innerHTML = rowsHTML(); if (focus) box.querySelector(focus)?.focus(); };
  const first = box.querySelector('input'); if (first) first.onkeydown = null; // Enter in a field must not close the editor
  box.querySelector('[data-kvk="0"]')?.focus();
  box.oninput = (e) => { const t = e.target;
    if (t.dataset.kvk !== undefined) { rows[+t.dataset.kvk].k = t.value.trim(); const pos = t.selectionStart; draw(`[data-kvk="${t.dataset.kvk}"]`); box.querySelector(`[data-kvk="${t.dataset.kvk}"]`).setSelectionRange(pos, pos); }
    if (t.dataset.kvv !== undefined) rows[+t.dataset.kvv].v = t.value; };
  box.onchange = (e) => { if (e.target.id === 'kvShow') { show = e.target.checked; draw(); } };
  box.onclick = (e) => { const t = e.target.closest('[data-kvadd],[data-kvdel],[data-kvpaste],[data-kvimport]'); if (!t) return;
    if (t.dataset.kvadd) { rows.push({ k: '', v: '' }); draw(`[data-kvk="${rows.length - 1}"]`); }
    if (t.dataset.kvdel !== undefined) { rows.splice(+t.dataset.kvdel, 1); if (!rows.length) rows.push({ k: '', v: '' }); draw(); }
    if (t.dataset.kvpaste) { const pz = box.querySelector('#kvPaste'); pz.hidden = !pz.hidden; if (!pz.hidden) box.querySelector('#kvPasteText').focus(); }
    if (t.dataset.kvimport) {
      const got = parseEnv(box.querySelector('#kvPasteText').value), bad = got.filter(x => x.bad);
      let added = 0, updated = 0;
      rows = rows.filter(r => r.k || r.v);
      for (const { k, v } of got.filter(x => !x.bad)) { const r = rows.find(x => x.k === k); if (r) { r.v = v; updated++; } else { rows.push({ k, v }); added++; } }
      if (!rows.length) rows.push({ k: '', v: '' });
      box.querySelector('#kvPasteText').value = ''; box.querySelector('#kvPaste').hidden = true; draw();
      box.querySelector('#kvErr').textContent = [added || updated ? `Added ${added}, updated ${updated}.` : '', bad.length ? `Skipped ${plural(bad.length, 'line')} without NAME=value: ${bad[0].bad}` : ''].filter(Boolean).join(' ');
    } };
  box.querySelector('#kvSave').onclick = async () => {
    const list = rows.filter(r => r.k || r.v), err = box.querySelector('#kvErr');
    const bad = list.find(r => !nameOK.test(r.k)), twice = list.find((r, i) => list.findIndex(x => x.k === r.k) !== i);
    if (bad) { err.textContent = bad.k ? `“${bad.k}” isn’t a valid name${env ? ': letters, digits and _, not starting with a digit' : ''}.` : 'A value has no name.'; return; }
    if (twice) { err.textContent = `${twice.k} is there twice.`; return; }
    try {
      const r = await bridge.call('sites.update', { folder: s.folder, [env ? 'env_vars' : 'php_ini']: Object.fromEntries(list.map(x => [x.k, x.v])) });
      box.querySelector('[data-dlg="cancel"]').click();
      S.sites = r.sites; logToast(r); render();
    } catch (e) { err.textContent = e.message; }
  };
  p.then(() => { box.oninput = box.onchange = box.onclick = null; });
  return p;
}

/* --- settings --- */
function snippet(title, text) {
  return `<div class="st">${esc(title)}<span class="spacer"></span><button class="btn ghost" data-copy="${esc(text)}" style="height:24px">${icon.copy}Copy</button></div><pre class="mini" style="max-height:none">${esc(text)}</pre>`;
}
const SETTING_SECTIONS = [
  ['general', 'General', 'sliders'], ['folders', 'Projects folders', 'folder'], ['domains', 'Domains & ports', 'network'], ['php', 'PHP', 'php'], ['mysql', 'MySQL', 'db'],
  ['agents', 'AI agents (MCP)', 'agent'], ['storage', 'Storage', 'hardDrive'], ['problems', 'Problems', 'alertCircle'], ['notifications', 'Notifications', 'bell'], ['updates', 'Updates', 'sparkles'], ['advanced', 'Advanced', 'cpu'],
];
function setVal(key) { return key in S.setDraft ? S.setDraft[key] : S.settings?.[key]; }
function iniVal(key) { const k = 'php_ini.' + key; return k in S.setDraft ? S.setDraft[k] : (S.settings?.php_ini?.[key] ?? ''); }
function settingRow(label, help, control, field) {
  const err = field && S.setErrors[field];
  return `<div class="setting"><div><div class="label">${esc(label)}</div>${help ? `<div class="desc">${help}</div>` : ''}</div><div class="control">${control}</div>${err ? `<div class="field-error" role="alert">${esc(err)}</div>` : ''}</div>`;
}
function textInput(field, value, attrs = '') {
  return `<input class="input ${S.setErrors[field] ? 'invalid' : ''}" data-setting="${field}" value="${esc(value ?? '')}" spellcheck="false" ${attrs}>`;
}
function saveBar() {
  const dirty = Object.keys(S.setDraft).length;
  return `<div class="save-bar">${dirty ? '<span class="note">Unsaved changes</span>' : ''}
    <button class="btn ghost" data-cmd="setrevert" ${dirty ? '' : 'disabled'}>Discard</button>
    <button class="btn primary" data-cmd="setsave" ${dirty && !S.setSaving ? '' : 'disabled'}>${S.setSaving ? 'Saving…' : 'Save changes'}</button></div>`;
}
/* --- updates: opt-in checks, signed downloads, install on restart or quit, switch back --- */
const UPD_MODES = [
  ['notify', 'Tell me', 'A notification and a note here. You choose when to install.'],
  ['download', 'Download it, install when I quit', 'Downloads in the background. Installs the next time you quit LocalSites, or when you click Restart and install.'],
  ['auto', 'Install automatically', 'Same, and if LocalSites is still open it installs itself between 2 and 5 am and comes back in the menu bar.'],
];
function updPending() { const u = S.upd; return !!(u?.latest && !u.skipped && ['available', 'downloading', 'ready'].includes(u.phase)); }
const mb = (b) => b >= 1 << 20 ? (b / (1 << 20)).toFixed(1) + ' MB' : Math.max(1, Math.round(b / 1024)) + ' KB';
async function loadUpdate() { S.upd = await bridge.call('update.status'); }
function pollUpdate() {
  clearTimeout(pollUpdate.t);
  if (!['checking', 'downloading', 'installing'].includes(S.upd?.phase)) return;
  pollUpdate.t = setTimeout(async () => {
    const before = S.upd?.phase;
    try { await loadUpdate(); } catch (_) { /* the service may be restarting for the update */ }
    if (S.updAfter && before === 'downloading' && S.upd?.phase === 'ready') { S.updAfter = false; installUpdate(); }
    if (S.upd?.phase === 'error' && before === 'downloading') toast(S.upd.error, 'err');
    if (before === 'downloading' && S.upd?.phase === 'ready') promptUpdateReady();
    if (cur().view === 'settings' && S.setSec === 'updates') renderView();
    renderSide(); pollUpdate();
  }, 600);
}
// When an update has finished downloading and the person hasn't said when to install it, ask once.
async function promptUpdateReady() {
  const u = S.upd, v = u?.latest?.version;
  if (!v || u.phase !== 'ready' || u.skipped || u.on_quit || S.updAfter || S.updPrompted === v || !window.go?.main?.App?.InstallUpdate) return;
  S.updPrompted = v;
  const items = (u.latest.notes || []).slice(0, 3).map(i => `<li>${esc(i)}</li>`).join('');
  const r = await dialog(`<h3>LocalSites ${esc(v)} is ready to install</h3>${items ? `<ul class="upd-notes">${items}</ul>` : ''}
    <p class="note">Restarting takes a few seconds and your projects keep their settings. If you choose later, it installs the next time you quit LocalSites.</p>
    <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn ghost" data-dlg="cancel">Not now</button><button class="btn" data-dlg="later">When I quit</button><button class="btn primary" data-dlg="ok">Restart now</button></div>`);
  if (r === true) return installUpdate();
  if (r === 'later') return command('updlater');
}
async function installUpdate(rollback = false) {
  if (!window.go?.main?.App?.InstallUpdate) return toast('Install updates from the LocalSites app.', 'warn');
  S.upd = { ...S.upd, phase: 'installing' }; renderView();
  try { await (rollback ? window.go.main.App.RollbackUpdate() : window.go.main.App.InstallUpdate()); }
  catch (e) { toast(String(e?.message || e), 'err'); await act(loadUpdate); renderView(); }
}
async function setUpdSetting(k, v) {
  const r = await act(() => bridge.call('settings.update', { [k]: v }), 'Updates');
  if (r) { await act(loadSettings); renderView(); }
  return r;
}
function updCard(u) {
  const notes = (m) => m?.notes?.length ? `<ul class="upd-notes">${m.notes.map(i => `<li>${esc(i)}</li>`).join('')}</ul>` : '';
  const head = (ic, title, sub, status = '') => `<div class="upd-h"><span class="c-ic">${icon[ic]}</span><div class="upd-t"><div class="c-title">${title}</div>${sub ? `<div class="c-sub">${sub}</div>` : ''}</div>${status}</div>`;
  const checked = u.last_check ? `Checked ${ago(u.last_check)}.` : '';
  const m = u.latest, ver = esc(m?.version || '');
  const facts = m ? [m.security ? UI.status('warn', 'Security fix') : '', m.date ? UI.tag(m.date) : '', u.size ? UI.tag(mb(u.size)) : '', m.channel === 'beta' ? UI.tag('beta') : ''].join('') : '';
  const page = u.release_url ? UI.btn('Release page', { ic: 'external', kind: 'ghost', attrs: `data-openweb="${esc(u.release_url)}"` }) : '';
  const canInstall = u.supported && u.signed;
  if (u.phase === 'checking') return `<div class="upd">${head('refresh', 'Checking for updates…', 'Reading the list of releases.', UI.status('busy', 'Checking'))}</div>`;
  if (u.phase === 'installing') return `<div class="upd">${head('rocket', 'Installing…', 'LocalSites quits, swaps the app and opens again. Projects pause for a few seconds.', UI.status('busy', 'Installing'))}${m ? updPipeline(u) : ''}</div>`;
  if (m && u.phase === 'downloading') {
    const pct = u.size ? Math.min(100, Math.round(100 * (u.done || 0) / u.size)) : 0;
    return `<div class="upd">${head('download2', `Downloading LocalSites ${ver}`, `${mb(u.done || 0)} of ${mb(u.size || 0)}`, UI.status('busy', pct + '%'))}
      <div class="bar" role="progressbar" aria-valuenow="${pct}" aria-valuemin="0" aria-valuemax="100" aria-label="Download progress"><span style="width:${pct}%"></span></div>${updPipeline(u)}
      <div class="upd-f"><span class="note">When it's downloaded:</span><div class="seg" role="group" aria-label="When it's downloaded">${[['updnow', 'Restart and install', S.updAfter], ['updquit', 'Install when I quit', !S.updAfter && u.on_quit]].map(([c, l, on]) => `<button data-cmd="${c}" class="${on ? 'on' : ''}">${l}</button>`).join('')}</div></div>
      <p class="note">${S.updAfter ? 'LocalSites restarts as soon as the update is downloaded and checked.' : u.on_quit ? 'You can keep working. It installs the next time you quit LocalSites.' : 'You can keep working. Nothing changes until you install it.'}</p></div>`;
  }
  if (m && u.phase === 'ready') {
    const later = u.on_quit;
    return `<div class="upd ready">${head('rocket', `LocalSites ${ver} is ready to install`, m.title ? esc(m.title) : '', `<span class="upd-facts">${facts}</span>${later ? UI.status('on', 'Installs when you quit') : ''}`)}${updPipeline(u)}${notes(m)}
      <div class="upd-f">${UI.btn('Restart now', { kind: 'primary', ic: 'refresh', attrs: 'data-cmd="updinstall"' })}${later ? '' : UI.btn('Install when I quit', { attrs: 'data-cmd="updlater"' })}<span class="spacer"></span>${UI.btn('Skip this version', { kind: 'ghost', attrs: 'data-cmd="updskip"' })}</div>
      <p class="note">Projects pause for a few seconds while LocalSites restarts. Settings are backed up first.${later ? ' If you don’t restart now, it installs the next time you quit LocalSites.' : ''}</p></div>`;
  }
  if (m && (u.phase === 'available' || (u.phase === 'error' && m))) {
    const err = u.phase === 'error' ? `<p class="upd-err" role="alert">${esc(u.error)}</p>` : '';
    if (u.skipped) return `<div class="upd">${head('sparkles', `LocalSites ${ver} is available`, 'You chose to skip this version. Automatic checks won’t mention it again.', `<span class="upd-facts">${facts}</span>`)}
      <div class="upd-f">${UI.btn('Show it again', { attrs: 'data-cmd="updunskip"' })}${page}</div></div>`;
    const main = canInstall ? UI.btn(u.phase === 'error' ? 'Try again' : 'Download and install', { kind: 'primary', ic: 'download2', attrs: 'data-cmd="updget"' })
      : UI.btn('Download from the release page', { kind: 'primary', ic: 'external', attrs: `data-openweb="${esc(u.release_url || '')}"` });
    return `<div class="upd">${head('sparkles', `LocalSites ${ver} is available`, m.title ? esc(m.title) : '', `<span class="upd-facts">${facts}</span>`)}${notes(m)}${err}
      <div class="upd-f">${main}${canInstall ? UI.btn('Download only', { attrs: 'data-cmd="upddl"' }) : ''}<span class="spacer"></span>${UI.btn('Skip this version', { kind: 'ghost', attrs: 'data-cmd="updskip"' })}</div>
      ${canInstall ? `<p class="note">You’re on ${esc(u.current)}. ${u.size ? mb(u.size) + ' download. ' : ''}LocalSites restarts to install it; projects pause for a few seconds.${u.release_url ? ` <a class="link" data-openweb="${esc(u.release_url)}">Release page</a>` : ''}</p>`
        : `<p class="note">${u.supported ? 'This build has no update key, so it can’t install updates itself.' : (isMac ? 'Installing from inside the app works on macOS for now.' : 'This copy wasn’t installed from the .deb or .rpm package, so it can’t update itself. Install the package from the release page; after that, updates also arrive with your system updates.')}</p>`}</div>`;
  }
  if (u.phase === 'error') return `<div class="upd">${head('alert', 'Couldn’t check for updates', esc(u.error), UI.status('err', 'Problem'))}<div class="upd-f">${UI.btn('Try again', { ic: 'refresh', attrs: 'data-cmd="updcheck"' })}</div></div>`;
  const auto = setVal('update_auto_check');
  return `<div class="upd">${head('check', u.last_check ? 'You’re up to date' : 'Not checked yet', u.last_check ? `LocalSites ${esc(u.current)} is the newest version. ${checked}` : auto ? 'The first check runs within the hour.' : 'Automatic checks are off. You can check at any time.', u.last_check ? UI.status('on', 'Up to date') : '')}
    <div class="upd-f">${UI.btn('Check now', { ic: 'refresh', attrs: 'data-cmd="updcheck"' })}</div></div>`;
}
function updatesPage() {
  const v = S.version, u = S.upd, mode = setVal('update_mode') || 'notify', chan = setVal('update_channel') || 'stable';
  if (!u) act(async () => { await loadUpdate(); pollUpdate(); renderView(); });
  const ask = S.settings && !setVal('update_asked') ? `<div class="upd ask"><div class="upd-h"><span class="c-ic">${icon.sparkles}</span><div class="upd-t"><div class="c-title">Check for updates automatically?</div>
      <div class="c-sub">Once a day LocalSites reads the public list of releases on GitHub. Nothing about you, this Mac or your projects is sent. You decide what happens when there’s a new version.</div></div></div>
      <div class="upd-f">${UI.btn('Turn on', { kind: 'primary', attrs: 'data-cmd="updoptin"' })}${UI.btn('Not now', { kind: 'ghost', attrs: 'data-cmd="updoptout"' })}</div></div>` : '';
  const choice = `<div class="choice" role="radiogroup" aria-label="When there’s an update">${UPD_MODES.map(([id, label, desc]) => `<label class="choice-row ${mode === id ? 'on' : ''}">
      <input type="radio" name="updmode" value="${id}" data-updset="update_mode" ${mode === id ? 'checked' : ''}><span><span class="label">${esc(label)}</span><span class="desc">${esc(desc)}</span></span></label>`).join('')}</div>`;
  return `<h2>Updates</h2><p class="lead">You’re on LocalSites ${esc(v?.version || u?.current || '')}${chan === 'beta' ? ' (beta channel)' : ''}.</p>
    ${u?.result ? `<p class="upd-result">${icon.check}${esc(u.result)}</p>` : ''}
    ${ask}
    ${u ? updCard(u) : '<div class="upd"><span class="skeleton" style="width:40%"></span></div>'}
    <h3 class="sub">How updates work</h3>
    ${settingRow('Check automatically', 'Once a day, only while LocalSites is running. Off means LocalSites never contacts GitHub unless you click Check now.', UI.toggle(!!setVal('update_auto_check'), 'data-updset="update_auto_check"', 'Check automatically'))}
    <div class="setting col"><div><div class="label">When there’s an update</div><div class="desc">Applies to automatic checks and to updates you download.</div></div>${choice}</div>
    ${settingRow('Channel', 'Beta gets new features a few days early and may have rough edges.', `<div class="seg" role="group" aria-label="Channel">${[['stable', 'Stable'], ['beta', 'Beta']].map(([id, l]) => `<button data-updchan="${id}" class="${chan === id ? 'on' : ''}">${l}</button>`).join('')}</div>`)}
    ${settingRow('Switch back', u?.kept ? `LocalSites ${esc(u.kept)} is kept from before the last update.` : 'After an update, the previous version is kept here so you can go back in one click.',
      UI.btn(u?.kept ? `Switch to ${u.kept}` : 'Nothing kept yet', { ic: 'history2', attrs: `data-cmd="updrollback" ${u?.kept && u?.supported ? '' : 'disabled'}` }))}
    <p class="note upd-safety">${icon.shield}<span>Every update is signed. Before installing, LocalSites checks the signature against the key built into this app, the download’s checksum, and that the new app starts on this Mac. If any check fails, the download is deleted and nothing changes.</span></p>
    <h3 class="sub">What's new</h3>
    ${(v?.changelog || []).map(r => `<div class="release"><h3>${esc(r.version)} · ${esc(r.title)}</h3><div class="note">${esc(r.date)}</div><ul>${r.items.map(i => `<li>${esc(i)}</li>`).join('')}</ul></div>`).join('')}`;
}
/* --- problems: what went wrong on this Mac, and opt-in reports (cleaned, reviewed before sending) --- */
const PROB_KIND = { crash: ['Crash', 'err'], service: ['Service', 'warn'], update: ['Update', 'warn'], install: ['Install', 'warn'], ui: ['App', 'off'] };
const REPORT_MODES = [['off', 'Off'], ['ask', 'Ask me'], ['auto', 'Automatic']];
async function loadProblems() { // an older service doesn't know problems yet: stay quiet until it restarts
  try { S.prob = await bridge.call('problems.list'); } catch (e) { if (!/unknown method/i.test(String(e?.message || e))) throw e; S.prob = { problems: [], report: { mode: 'off', can_send: false }, old: true }; }
}
function probNew() { return (S.prob?.problems || []).filter(p => !p.sent && p.kind !== 'ui').length; }
async function reviewProblem(id) {
  const p = (S.prob?.problems || []).find(x => x.id === id); if (!p) return;
  const canSend = S.prob?.report?.can_send;
  const ov = $('#keysOverlay'), box = $('#keysDialog');
  let bundle = false, text = '';
  const load = async () => {
    const r = await act(() => bridge.call('problems.preview', { id, bundle }), 'Report'); text = r?.text || '';
    const files = r?.bundle_files?.length ? `\n\nFiles in the debug bundle (cleaned the same way; saved connections and web access logs are left out):\n${r.bundle_files.map(f => '  ' + f).join('\n')}` : '';
    const pre = box.querySelector('pre'); if (pre) pre.textContent = text + files; };
  box.innerHTML = `<h3>${canSend ? 'Send a problem report?' : 'Problem report'}</h3>
    <p class="note">This is exactly what ${canSend ? 'will be sent' : 'you can copy'}. Passwords, tokens, e-mail addresses, IP addresses and your user name were removed; project names are replaced with codes.</p>
    <pre class="report-preview" tabindex="0" aria-label="Report">Loading…</pre>
    ${canSend ? `<label class="form-row note" style="gap:8px;margin-top:10px"><input type="checkbox" id="repBundle"> Attach a debug bundle (logs and settings, cleaned the same way; its files are listed above)</label>` : '<p class="note">This build can’t send reports. Copy it into a GitHub issue instead.</p>'}
    <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn ghost" data-r="cancel">Cancel</button><button class="btn" data-r="copy">${icon.copy}Copy</button>${canSend ? `<button class="btn primary" data-r="send">${icon.send}Send report</button>` : ''}</div>`;
  ov.classList.add('open');
  const close = () => { ov.classList.remove('open'); ov.onmousedown = null; box.onkeydown = null; };
  ov.onmousedown = (e) => { if (e.target === ov) close(); };
  box.onkeydown = (e) => { if (e.key === 'Escape') { e.preventDefault(); close(); } };
  box.querySelector('#repBundle')?.addEventListener('change', (e) => { bundle = e.target.checked; load(); });
  box.querySelector('[data-r="cancel"]').onclick = close;
  box.querySelector('[data-r="copy"]').onclick = async () => { await act(() => bridge.copy(text)); toast('Report copied'); };
  const send = box.querySelector('[data-r="send"]');
  if (send) {
    send.focus();
    send.onclick = async () => {
      send.disabled = true; send.textContent = 'Sending…';
      const r = await act(() => bridge.call('problems.send', { id, bundle }), 'Report');
      close(); if (r) { toast('Report sent. Thank you'); await act(loadProblems); renderView(); }
    };
  }
  await load();
}
function problemsPage() {
  if (!S.prob) act(async () => { await loadProblems(); renderView(); });
  const list = S.prob?.problems || [], info = S.prob?.report || { mode: 'off', can_send: false }, mode = setVal('report_mode') || info.mode || 'off';
  const row = (p) => { const [label, st] = PROB_KIND[p.kind] || [p.kind, 'off'];
    return `<div class="prob ${p.sent ? 'sent' : ''}"><div class="prob-main">
      <div class="prob-t">${UI.status(st, label)}<span class="prob-title">${esc(p.title)}</span></div>
      <div class="note">${p.count > 1 ? `${p.count} times · last ` : ''}${ago(p.last)} · LocalSites ${esc(p.version)}${p.sent ? ` · ${icon.check} Report sent ${ago(p.sent)}` : ''}</div></div>
      <div class="prob-a">${UI.btn(p.sent ? 'View report' : info.can_send ? 'Review and send' : 'View report', { attrs: `data-probreview="${esc(p.id)}"` })}${UI.iconBtn('x', 'Dismiss', `data-probdismiss="${esc(p.id)}"`)}</div></div>`; };
  return `<h2>Problems</h2><p class="lead">Things that went wrong on this Mac: crashes, services that stopped, failed updates and installs, and app errors. They stay here unless you choose to send a report.</p>
    ${settingRow('Send problem reports', 'Reports help fix bugs. Each report is cleaned first: passwords, tokens, e-mail and IP addresses and your user name are removed, project names become codes. Off means nothing is ever sent; with Ask me, you review each report first.',
      `<div class="seg" role="group" aria-label="Send problem reports">${REPORT_MODES.map(([id, l]) => `<button data-repmode="${id}" class="${mode === id ? 'on' : ''}">${l}</button>`).join('')}</div>`)}
    ${info.can_send ? '' : '<p class="note">This build has no report address yet, so reports can be copied (for a GitHub issue) but not sent.</p>'}
    <h3 class="sub">On this Mac${list.length ? ` · ${list.length}` : ''}</h3>
    ${!S.prob ? '<span class="skeleton" style="width:40%"></span>' : list.length ? list.map(row).join('') + `<div class="form-row" style="justify-content:flex-end;margin-top:12px">${UI.btn('Clear all', { kind: 'ghost', attrs: 'data-probdismiss=""' })}</div>`
      : `<div class="prob-empty">${icon.check}<div><div class="label">No problems</div><div class="note">When something goes wrong, it appears here with the details needed to fix it.</div></div></div>`}`;
}
/* --- Theme Studio. Pick an accent hue,
   drag the corner radius, watch a sample card restyle as you go, then copy the CSS variables. Saved on this
   computer only; fonts stay the same. --- */
const ACCENT_PRESETS = [['Default', null], ['Violet', 300], ['Rose', 12], ['Amber', 62], ['Emerald', 158], ['Sky', 230]];
const LOOK_DEFAULT = { palette: 'default', hue: null, radius: 6 };
S.look = (() => { const v = { ...LOOK_DEFAULT, ...store.get('look', {}) }; if (typeof v.radius !== 'number') v.radius = 6; if (typeof v.hue !== 'number') v.hue = null; return v; })();
const isDarkNow = () => S.theme === 'dark' || (S.theme !== 'light' && matchMedia('(prefers-color-scheme: dark)').matches);
// One lightness and chroma per mode keeps every hue readable with its button text
const accentOf = (hue, dark) => `oklch(${dark ? 0.7 : 0.56} ${dark ? 0.15 : 0.18} ${Math.round(hue)})`;
function lum([r, g, b]) { const f = (c) => { c /= 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; }; return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b); }
const contrast = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((m, n) => n - m); return (x + 0.05) / (y + 0.05); };
// Paint a colour onto a 1px canvas to read it back as sRGB (works for oklch in every engine)
function srgb(value) {
  const c = (srgb.c ||= document.createElement('canvas')); c.width = c.height = 1;
  const x = c.getContext('2d', { willReadFrequently: true }); x.clearRect(0, 0, 1, 1); x.fillStyle = '#000'; x.fillStyle = value; x.fillRect(0, 0, 1, 1);
  return [...x.getImageData(0, 0, 1, 1).data.slice(0, 3)];
}
const hexOf = (rgb) => '#' + rgb.map(v => Math.round(v).toString(16).padStart(2, '0')).join('');
function applyLook() {
  const root = document.documentElement, L = S.look, st = root.style;
  if (L.palette === 'mono') root.dataset.palette = 'mono'; else delete root.dataset.palette;
  for (const v of ['--brand', '--primary', '--ring', '--primary-foreground', '--radius', '--radius-xl']) st.removeProperty(v);
  if (L.hue !== null) {
    const a = accentOf(L.hue, isDarkNow()), rgb = srgb(a);
    st.setProperty('--brand', a); st.setProperty('--primary', a); st.setProperty('--ring', a);
    // the text on it: whichever of the near-white and near-black ink reads better on this exact colour
    st.setProperty('--primary-foreground', contrast(rgb, srgb(cssVar('--paper-ffffff'))) >= contrast(rgb, srgb(cssVar('--ink-0b0b0c'))) ? 'var(--paper-ffffff)' : 'var(--ink-0b0b0c)');
  }
  if (L.radius !== LOOK_DEFAULT.radius) { st.setProperty('--radius', L.radius + 'px'); st.setProperty('--radius-xl', Math.round(L.radius * 4 / 3) + 'px'); }
}
try { matchMedia('(prefers-color-scheme: dark)').addEventListener('change', applyLook); } catch (_) { /* old engines */ }
function setLook(patch, redraw = true) { S.look = { ...S.look, ...patch }; store.set('look', S.look); applyLook(); syncWindowColour(); if (redraw) renderView(); else tsLive(); }
const lookChanged = () => S.look.palette !== LOOK_DEFAULT.palette || S.look.hue !== LOOK_DEFAULT.hue || S.look.radius !== LOOK_DEFAULT.radius;
const cssVar = (v) => getComputedStyle(document.documentElement).getPropertyValue(v).trim();
// WCAG 2 contrast of the colours actually on screen (custom properties are read resolved, then painted to sRGB)
function lookChecks() {
  const get = (v) => srgb(cssVar(v));
  const btn = contrast(get('--primary'), get('--primary-foreground')), link = contrast(get('--brand'), get('--background'));
  const tag = (n, min) => `<span class="${n >= min ? 'ok' : 'warnc'}">${n.toFixed(1)}:1</span>`;
  return `Button text ${tag(btn, 4.5)} · links ${tag(link, 3)}${btn < 4.5 || link < 3 ? ' · may be hard to read' : ''}`;
}
function lookCSS() {
  const L = S.look, lines = [], dark = [];
  if (L.hue !== null) {
    const fg = (dark) => { const rgb = srgb(accentOf(L.hue, dark)), w = srgb(cssVar('--paper-ffffff')), k = srgb(cssVar('--ink-0b0b0c')); return hexOf(contrast(rgb, w) >= contrast(rgb, k) ? w : k); };
    lines.push(`  --primary: ${accentOf(L.hue, false)};`, `  --primary-foreground: ${fg(false)};`);
    dark.push(`  --primary: ${accentOf(L.hue, true)};`, `  --primary-foreground: ${fg(true)};`);
  }
  if (L.radius !== LOOK_DEFAULT.radius) lines.push(`  --radius: ${L.radius}px;`, `  --radius-xl: ${Math.round(L.radius * 4 / 3)}px;`);
  return `:root${L.palette === 'mono' ? '[data-palette="mono"]' : ''} {\n${lines.join('\n') || '  /* the default theme */'}\n}${dark.length ? `\n\n[data-theme="dark"] {\n${dark.join('\n')}\n}` : ''}`;
}
const tsCssHTML = () => esc(lookCSS()).split('\n').map(l => { const m = l.match(/^(\s*)(--[\w-]+)(:\s*)(.+)(;)$/); return `<span class="ts-l">${m ? `${m[1]}<span class="ts-var">${m[2]}</span>${m[3]}<span class="ts-val">${m[4]}</span>${m[5]}` : l || ' '}</span>`; }).join('');
let tsView = 'preview';
function themeStudio() {
  const L = S.look, preset = ACCENT_PRESETS.find(([, h]) => h === L.hue || (h !== null && L.hue !== null && Math.abs(h - L.hue) < 2));
  const dark = isDarkNow(), seg = (attr, cur, opts) => `<div class="ts-seg" role="radiogroup">${opts.map(([v, l]) => `<button role="radio" data-${attr}="${v}" class="${cur === v ? 'on' : ''}" aria-checked="${cur === v}">${l}</button>`).join('')}</div>`;
  const swatches = ACCENT_PRESETS.map(([name, h]) => { const on = preset?.[0] === name;
    return `<button class="ts-sw ${on ? 'on' : ''}" role="radio" aria-checked="${on}" aria-label="${name}" data-tshue="${h === null ? 'none' : h}" style="--sw:${h === null ? 'var(--indigo-5e6ad2)' : accentOf(h, dark)}">${on ? icon.check : ''}</button>`; }).join('');
  const grad = Array.from({ length: 13 }, (_, i) => accentOf(i * 30, dark)).join(', ');
  return `<h3 style="margin-top:28px">Theme</h3><p class="note">Saved on this computer only. Fonts stay the same.</p>
  <div class="ts">
    <div class="ts-ctl">
      <div class="ts-f"><div class="ts-fh"><span>Colours</span></div>${seg('themepal', L.palette, [['default', 'Default'], ['mono', 'Mono']])}</div>
      <div class="ts-f"><div class="ts-fh"><span>Accent</span><span class="ts-v" id="tsHueV">${preset ? preset[0] : Math.round(L.hue) + '°'}</span></div>
        <div class="ts-sws" role="radiogroup" aria-label="Accent presets">${swatches}</div>
        <input type="range" class="ts-hue" id="tsHue" min="0" max="360" value="${L.hue ?? 268}" aria-label="Accent hue" style="--grad:linear-gradient(to right, ${grad});--thumb:${L.hue === null ? 'var(--indigo-5e6ad2)' : accentOf(L.hue, dark)}"></div>
      <div class="ts-f"><div class="ts-fh"><span>Corners</span><span class="ts-v">${L.radius}px</span></div>
        ${seg('tsrad', String(L.radius), [['3', 'Sharp'], ['6', 'Default'], ['9', 'Round']])}</div>
      <p class="note ts-check" id="tsCheck">${lookChecks()}</p>
    </div>
    <div class="ts-out">
      <div class="ts-oh">${seg('tsview', tsView, [['preview', 'Preview'], ['css', 'CSS']])}<span class="spacer"></span>
        <button class="ts-act" data-themereset="1" ${lookChanged() ? '' : 'disabled'}>${icon.rotateCcw}Reset</button><button class="ts-act" data-themecopy="1">${icon.copy}Copy CSS</button></div>
      <div class="ts-stage">${tsView === 'css' ? `<pre class="ts-css"><code>${tsCssHTML()}</code></pre>` : `<div class="ts-card">
        <div class="ts-card-h"><span class="ts-ic">${TYPES.laravel.icon()}</span><div><p class="ts-t">shop</p><p class="note">Laravel · PHP 8.4</p></div><span class="ts-pill">Running</span></div>
        <label class="ts-field"><span>Address</span><span class="ts-inp"><span class="note">https://</span>shop.test</span></label>
        <div class="ts-row"><div><p class="ts-t2">Xdebug</p><p class="note">Port 9003</p></div><label class="switch"><input type="checkbox" checked tabindex="-1" aria-label="Preview switch"><span></span></label></div>
        <div><div class="ts-row note"><span>Disk used</span><span>68%</span></div><div class="ts-bar"><i></i></div></div>
        <div class="ts-btns"><button class="btn" tabindex="-1">Logs</button><button class="btn primary" tabindex="-1">${icon.globe}Open</button></div></div>`}</div>
    </div>
  </div>`;
}
// While a slider moves, restyle in place; a full redraw happens when it's let go
function tsLive() {
  const L = S.look, dark = isDarkNow(), hv = $('#tsHueV'), rv = $('#tsRadV'), hue = $('#tsHue'), rad = $('#tsRad'), ck = $('#tsCheck');
  if (hv) hv.textContent = (ACCENT_PRESETS.find(([, h]) => h === L.hue) || [L.hue === null ? 'Default' : Math.round(L.hue) + '°'])[0];
  if (hue) hue.style.setProperty('--thumb', L.hue === null ? 'var(--indigo-5e6ad2)' : accentOf(L.hue, dark));
  if (rv) rv.textContent = L.radius + 'px';
  if (rad) rad.style.setProperty('--pct', (L.radius / 14) * 100 + '%');
  if (ck) ck.innerHTML = lookChecks();
  document.querySelectorAll('.ts-sw').forEach(b => { const on = b.dataset.tshue === String(L.hue ?? 'none'); b.classList.toggle('on', on); b.setAttribute('aria-checked', on); b.innerHTML = on ? icon.check : ''; });
}
document.addEventListener('input', (e) => {
  if (e.target.id === 'tsHue') setLook({ hue: +e.target.value }, false);
  if (e.target.id === 'tsRad') setLook({ radius: +e.target.value }, false);
});
document.addEventListener('change', (e) => { if (e.target.id === 'tsHue' || e.target.id === 'tsRad') renderView(); });

/* --- AI agents: one list of agent apps, searchable, each a row with its config file and one action --- */
let agentQ = '';
// ~/Library/Application Support/Claude/claude_desktop_config.json → ~/…/Claude/claude_desktop_config.json
function shortPath(p = '') {
  const h = p.replace(/^\/Users\/[^/]+/, '~').replace(/^\/home\/[^/]+/, '~'), parts = h.split('/');
  return parts.length > 4 ? [parts[0], '…', ...parts.slice(-2)].join('/') : h;
}
function agentRow(c) {
  const ic = (AGENT_IC[c.id] || (() => icon.agent))();
  const where = c.config ? `<span class="ag-path" title="${esc(c.config)}"><code>${esc(shortPath(c.config))}</code>${UI.iconBtn('copy', 'Copy the full path', `data-copy="${esc(c.config)}"`)}</span>`
    : c.id === 'claude-code' ? '<span class="ag-path"><code>claude mcp add</code></span>' : c.manual ? '<span class="ag-path"><code>Paste the settings into the app</code></span>' : '';
  const [st, word] = !c.found ? ['off', 'Not installed'] : c.manual ? ['off', 'Set up by hand'] : c.problem ? ['warn', 'Needs a look'] : c.connected ? ['on', 'Connected'] : ['off', 'Not connected'];
  const state = `<span class="ag-st ${st}"><span class="dot ${st === 'on' ? 'on' : st === 'warn' ? 'warn' : 'off'}"></span>${word}</span>`;
  const action = !c.found ? '' : c.manual ? UI.btn('Copy settings', { ic: 'copy', attrs: `data-copy="${esc(c.manual)}"` })
    : c.connected ? UI.btn('Disconnect', { kind: 'ghost', attrs: `data-agentoff="${c.id}"` })
    : UI.btn('Connect', { ic: 'plugZap', kind: 'primary', attrs: `data-agenton="${c.id}"`, title: c.config ? `Adds LocalSites to ${c.config} (a backup is saved next to it)` : '' });
  return `<div class="ag-row ${c.connected ? 'on' : ''} ${c.found ? '' : 'missing'}" ${c.found ? `data-agent="${c.id}"` : ''}><span class="ag-ic">${ic}</span>
    <div class="ag-m"><div class="ag-name">${esc(c.name)}${state}</div>${where}${c.problem ? `<div class="note warnc">${esc(c.problem)}</div>` : ''}</div>
    <div class="ag-a">${action}</div></div>`;
}
function agentsPage() {
  const list = S.agents || [], q = agentQ.trim().toLowerCase();
  const shown = list.filter(c => !q || c.name.toLowerCase().includes(q) || (c.config || '').toLowerCase().includes(q));
  const found = shown.filter(c => c.found).sort((a, b) => (b.connected - a.connected) || a.name.localeCompare(b.name)), missing = shown.filter(c => !c.found);
  const on = list.filter(c => c.connected).length;
  return `<h2>AI agents (MCP)</h2><p class="lead">Let Claude, Cursor and other agents list projects, switch PHP versions, read logs and run health checks.
      Risky actions (stopping services, sharing a site, saving data edits) wait for you to click Approve here. Tools that reveal passwords stay off unless you add <code>--allow-secrets</code>.</p>
    ${S.agents ? `<div class="ag-top"><label class="qbox">${icon.search}<input class="input sm" id="agentQ" placeholder="Find an agent app" value="${esc(agentQ)}" spellcheck="false" autocomplete="off" aria-label="Find an agent app"></label>
        <span class="spacer"></span><span class="note">${on ? `${on} connected` : 'None connected yet'} · ${plural(list.filter(c => c.found).length, 'app')} found on this ${OS_NAME}</span></div>
      <div class="ag-list">${found.map(agentRow).join('') || (q ? '' : `<div class="ag-none">${icon.agent}<span>No agent apps found on this ${OS_NAME}. Any MCP client works with the settings below.</span></div>`)}${missing.length ? `<div class="ag-sep">Not installed</div>${missing.map(agentRow).join('')}` : ''}
        ${!shown.length ? `<div class="ag-none">${icon.search2}<span>No agent app matches “${esc(agentQ)}”. Any MCP client works with the settings below.</span></div>` : ''}</div>`
      : '<span class="skeleton" style="width:40%"></span>'}
    ${(S.grants || []).length ? `<h3 style="margin-top:24px">Allowed for a while</h3><div class="passes">${S.grants.map(passCard).join('')}</div>` : ''}
    ${snippet('Any other MCP client (JSON)', JSON.stringify({ mcpServers: { localsites: { command: S.exe, args: ['mcp'] } } }, null, 2))}`;
}

/* --- Pipelines: one stage per step, each with its state and time (setup, update install, Doctor) --- */
// stage: { title, sub, state: done | run | fail | wait, since (ms, while running), took (text), extra (html) }
function pipelineHTML(stages) {
  const ic = { done: icon.check, fail: icon.x, run: '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>', wait: '<span class="pl-wait"></span>' };
  return `<ol class="pipeline">${stages.map(x => `<li class="pl ${x.state}"><span class="pl-ic">${ic[x.state]}</span><span class="pl-t"><span class="pl-title">${x.titleHTML || esc(x.title)}</span>${x.sub ? `<span class="note">${esc(x.sub)}</span>` : ''}${x.extra || ''}</span>
    <span class="pl-time">${x.state === 'run' && x.since ? `<span data-elapsed="${x.since}">${fmtClock(Date.now() - x.since)}</span>` : esc(x.took || (x.state === 'fail' ? 'failed' : ''))}</span></li>`).join('')}</ol>`;
}
// Update install: download, check its signature, swap the app and restart
function updPipeline(u) {
  const T = S.updT ||= {};
  if (u.phase === 'downloading') T.dl ||= Date.now();
  if (u.phase === 'ready' && T.dl && !T.dlEnd) T.dlEnd = Date.now();
  if (u.phase === 'installing') T.inst ||= Date.now();
  const pct = u.size ? Math.min(100, Math.round(100 * (u.done || 0) / u.size)) : 0;
  const got = ['ready', 'installing'].includes(u.phase);
  return pipelineHTML([
    { title: 'Download', sub: got ? mb(u.size || 0) : `${mb(u.done || 0)} of ${mb(u.size || 0)} · ${pct}%`, state: got ? 'done' : 'run', since: T.dl, took: T.dl && T.dlEnd ? fmtClock(T.dlEnd - T.dl) : '' },
    { title: 'Check the signature', sub: got ? 'Signed with the LocalSites release key' : 'Before anything is installed', state: got ? 'done' : 'wait' },
    { title: 'Restart with the new version', sub: u.phase === 'installing' ? 'LocalSites quits, swaps the app and opens again' : 'Settings are backed up first; projects pause for a few seconds', state: u.phase === 'installing' ? 'run' : 'wait', since: T.inst },
  ]);
}
// Doctor: one stage per area, with how many checks passed
const areaId = (a) => 'area-' + String(a).toLowerCase().replace(/[^a-z0-9]+/g, '-');
function doctorPipeline() {
  const D = S.doctor || [], areas = uniq(D.map(c => c.area));
  if (S.doctorBusy) return pipelineHTML((areas.length ? areas : ['Runtimes', 'Services', 'Domains', 'Certificates', 'Projects']).map((a, i) => ({ title: a, sub: i ? 'Waiting' : 'Checking…', state: i ? 'wait' : 'run', since: i ? 0 : S.doctorAt })));
  return pipelineHTML(areas.map(a => {
    const list = D.filter(c => c.area === a), bad = list.filter(c => c.status === 'fail').length, warn = list.filter(c => c.status === 'warn').length;
    return { titleHTML: `<a class="link" data-goarea="${areaId(a)}">${esc(a)}</a>`, sub: bad ? `${bad} of ${list.length} failed` : warn ? `${list.length - warn} passed, ${plural(warn, 'warning')}` : `${plural(list.length, 'check')} passed`,
      state: bad ? 'fail' : 'done', took: bad ? 'failed' : '' };
  }));
}

function viewSettings() {
  if (S.settingsErr) return `<div class="empty"><h3>Settings could not be loaded</h3><p>${esc(S.settingsErr)}</p><button class="btn" data-cmd="setreload">${icon.refresh}Try again</button></div>`;
  if (!S.settings) { loadSettingsSafe(); return '<div class="pad"><span class="skeleton" style="width:40%"></span></div>'; }
  const st = S.settings, sec = S.setSec;
  const nav = SETTING_SECTIONS.map(([id, label, ic]) => `<a data-setsec="${id}" class="${sec === id ? 'on' : ''}">${icon[ic]}${esc(label)}${id === 'updates' && updPending() ? '<span class="unread" aria-label="Update available"></span>' : ''}${id === 'problems' && probNew() ? `<span class="nav-n" aria-label="${probNew()} new">${probNew()}</span>` : ''}</a>`).join('');
  let page = '';
  if (sec === 'general') {
    page = `<h2>General</h2><p class="lead">Where your projects live and how LocalSites starts.</p>
      ${settingRow('Start at login', st.start_at_login_supported ? 'Start Apache, PHP and MySQL when you log in, so sites are always ready.' : 'Available on macOS for now.',
        `<label class="switch"><input type="checkbox" data-setting="start_at_login" ${setVal('start_at_login') ? 'checked' : ''} ${st.start_at_login_supported ? '' : 'disabled'}><span></span></label>`, 'start_at_login')}
      ${['editor', 'terminal'].map(k => { const list = appsOf(k), saved = (S.appDefaults || {})[k] || '', ok = saved === 'ask' || list.some(a => a.id === saved);
        return settingRow(k === 'editor' ? 'Code editor' : 'Terminal', list.length ? `Used by the Open buttons, <code>localsites open</code> and AI agents. ${list.length} found on this ${OS_NAME}.` : `None found on this ${OS_NAME}. Install one and it shows up here.`,
          list.length ? `<select class="input" data-prefapp="${k}">${!ok ? `<option value="" selected disabled>${saved ? 'Not installed anymore: choose' : 'Not chosen yet'}</option>` : ''}${list.map(a => `<option value="${a.id}" ${saved === a.id ? 'selected' : ''}>${esc(a.name)}</option>`).join('')}<option value="ask" ${saved === 'ask' ? 'selected' : ''}>Ask every time</option></select>` : ''); }).join('')}
      ${settingRow('Appearance', 'Saved on this computer only.', `<div class="seg" role="group" aria-label="Appearance">${[['system', 'System', 'monitor'], ['dark', 'Dark', 'moon'], ['light', 'Light', 'sun']]
        .map(([v, l, ic]) => `<button data-theme-set="${v}" class="${S.theme === v ? 'on' : ''}">${icon[ic]}${l}</button>`).join('')}</div>`)}
      ${themeStudio()}
      ${saveBar()}`;
  } else if (sec === 'folders') {
    const roots = setVal('roots') || [];
    page = `<h2>Projects folders</h2><p class="lead">Add every folder that holds projects, in any language and from any git account. LocalSites finds PHP, Laravel, WordPress, Next.js and Node projects; set a custom command for anything else in the project’s panel.</p>
      ${roots.map((r, i) => `<div class="root-row">
        <span class="root-idx" title="${i === 0 ? 'Main folder' : ''}">${i === 0 ? icon.star : icon.folder}</span>
        <input class="input" data-root="${i}" data-rootf="label" value="${esc(r.label || '')}" placeholder="Label" style="width:130px" aria-label="Label">
        <input class="input mono ${S.setErrors['roots.' + i] ? 'invalid' : ''}" data-root="${i}" data-rootf="path" value="${esc(r.path || '')}" placeholder="/Users/you/Projects" style="flex:1" aria-label="Path" spellcheck="false">
        ${inApp ? `<button class="btn sm" data-rootpick="${i}">Choose…</button>` : ''}
        <select class="input sm" data-root="${i}" data-rootf="layout" aria-label="Layout">${[['auto', 'Detect groups'], ['flat', 'Every folder is a project'], ['groups', 'Folders are groups']].map(([v, l]) => `<option value="${v}" ${(r.layout || (i === 0 ? 'flat' : 'auto')) === v ? 'selected' : ''}>${l}</option>`).join('')}</select>
        <button class="icon-btn" data-rootdel="${i}" title="Remove folder" ${roots.length === 1 ? 'disabled' : ''}>${icon.trash}</button>
        ${S.setErrors['roots.' + i] ? `<div class="field-error" role="alert">${esc(S.setErrors['roots.' + i])}</div>` : ''}</div>`).join('')}
      <div class="form-row" style="margin-top:12px"><button class="btn" data-cmd="rootadd">${icon.folderPlus}Add folder</button></div>
      ${(st.hidden_projects || []).length ? `<h3 style="margin-top:24px">Hidden projects</h3><p class="note">LocalSites doesn’t list or serve these. Their folders are untouched.</p>
        ${st.hidden_projects.map(f => `<div class="root-row"><span class="root-idx">${icon.eyeOff}</span><span class="mono" style="flex:1">${esc(f)}</span><button class="btn sm" data-unhide="${esc(f)}">Show again</button></div>`).join('')}` : ''}
      <p class="note" style="margin-top:16px"><b>Layouts.</b> “Detect groups” treats subfolders that only contain projects as groups (for example one folder per git account). The first folder is your main folder. Folders already served by Laravel Valet are shown as “Served by Valet”.</p>
      ${saveBar()}`;
  } else if (sec === 'domains') {
    const fd = setVal('front_door');
    page = `<h2>Domains & ports</h2><p class="lead">Projects open at <code>https://name.${esc(st.tld)}</code>. Everything listens on 127.0.0.1 only.</p>
      ${settingRow('Serve addresses with', st.valet_found ? 'Automatic uses Laravel Valet when it is installed, so your Valet sites keep working.' : 'Built-in uses LocalSites’ own HTTPS proxy and DNS.',
        `<select class="input" data-setting="front_door">${[['auto', 'Automatic'], ['valet', 'Laravel Valet'], ['builtin', 'Built-in']].map(([v, l]) => `<option value="${v}" ${fd === v ? 'selected' : ''}>${l}</option>`).join('')}</select>`, 'front_door')}
      ${settingRow('Apache port', 'Internal; the front door forwards to it.', textInput('apache_port', setVal('apache_port'), 'inputmode="numeric"'), 'apache_port')}
      ${settingRow('Nginx port', 'Internal, for projects that use Nginx.', textInput('nginx_port', setVal('nginx_port') || 8746, 'inputmode="numeric"'), 'nginx_port')}
      ${st.front_door_used === 'builtin' || fd === 'builtin' ? settingRow('HTTPS port', 'Built-in front door.', textInput('https_port', setVal('https_port'), 'inputmode="numeric"'), 'https_port') +
        settingRow('HTTP port', 'Redirects to HTTPS.', textInput('http_port', setVal('http_port'), 'inputmode="numeric"'), 'http_port') +
        settingRow('DNS port', 'Answers for *.test only.', textInput('dns_port', setVal('dns_port'), 'inputmode="numeric"'), 'dns_port') : ''}
      <p class="note">In use now: ${esc(st.front_door_used === 'valet' ? 'Laravel Valet' : 'Built-in')}.</p>${saveBar()}`;
  } else if (sec === 'php') {
    const vers = S.status?.php || [];
    page = `<h2>PHP</h2><p class="lead">Defaults for every project. Leave a field empty to use LocalSites’ default, which matches MAMP PRO.</p>
      ${settingRow('PHP for new projects', 'Existing projects keep their own version.',
        `<select class="input" data-setting="default_php"><option value="">Newest installed</option>${vers.map(p => `<option value="${p.version}" ${setVal('default_php') === p.version ? 'selected' : ''}>PHP ${p.version} (${p.full})</option>`).join('')}</select>`, 'default_php')}
      ${(st.php_ini_keys || []).map(k => settingRow(k, `Default: <code>${esc(st.php_defaults[k] ?? 'PHP default')}</code>`,
        `<input class="input ${S.setErrors['php_ini.' + k] ? 'invalid' : ''}" data-setting="php_ini.${k}" value="${esc(iniVal(k))}" placeholder="${esc(st.php_defaults[k] ?? '')}" spellcheck="false">`, 'php_ini.' + k)).join('')}
      ${saveBar()}
      <h2 style="margin-top:32px">Extensions</h2><p class="lead">What each installed PHP version has. Install more with <code>pecl</code> or Homebrew.</p>
      ${S.phpInfo ? S.phpInfo.map(i => `<div class="setting" style="grid-template-columns:120px 1fr"><div class="label">PHP ${esc(i.version)}</div><div class="ext-list">${i.extensions.map(x => `<span class="c-tag">${esc(x)}</span>`).join('')}
        ${i.xdebug_loaded || i.xdebug_file ? '<span class="c-tag ok">Xdebug available</span>' : ''}</div></div>`).join('') : (loadPHPInfo(), '<span class="skeleton" style="width:40%"></span>')}`;
  } else if (sec === 'mysql') {
    page = `<h2>MySQL</h2><p class="lead">LocalSites runs MySQL 8.4 LTS. Each project database has its own login; passwords live in the system keychain.</p>
      ${settingRow('Port', 'TCP on 127.0.0.1. Projects using “localhost” connect through the socket below.', textInput('mysql_port', setVal('mysql_port'), 'inputmode="numeric"'), 'mysql_port')}
      ${settingRow('Keep running', 'Off: LocalSites MySQL sleeps after 20 minutes nobody uses it and starts again by itself when a project, Databases or the data browser needs it. Saves about 170 MB of memory.',
        `<label class="switch"><input type="checkbox" data-setting="mysql_always_on" ${setVal('mysql_always_on') ? 'checked' : ''}><span></span></label>`, 'mysql_always_on')}
      ${settingRow('Socket', '', `<code style="word-break:break-all">${esc(st.mysql_socket)}</code>`)}
      ${settingRow('Backups', '', `<code style="word-break:break-all">${esc(st.backups_dir)}</code>`)}
      ${settingRow('Root password', 'Kept in the system keychain. LocalSites, the data browser and Adminer keep working after a change. Projects use their own logins, not root.', UI.btn('Change…', { attrs: 'data-cmd="rootpw"' }))}
      ${saveBar()}`;
  } else if (sec === 'agents') {
    if (!S.agents) { loadAgents(); }
    const list = S.agents || [];
    page = agentsPage();
  } else if (sec === 'storage') {
    if (!S.storage) { act(async () => { S.storage = await bridge.call('storage.list'); renderView(); }); }
    const list = S.storage || [], total = list.reduce((a, x) => a + x.bytes, 0);
    const size = (b) => b >= 1 << 30 ? (b / (1 << 30)).toFixed(1) + ' GB' : b >= 1 << 20 ? (b / (1 << 20)).toFixed(1) + ' MB' : Math.max(1, Math.round(b / 1024)) + ' KB';
    page = `<h2>Storage</h2><p class="lead">What LocalSites keeps on this Mac${S.storage ? `: ${size(total)} in total` : ''}. Clean-ups only remove files LocalSites made, and ask first.</p>
      ${S.storage ? list.map(x => settingRow(`${x.name} · ${size(x.bytes)}`, esc(x.detail), x.clean ? UI.btn(x.can_clean ? 'Clean up' : 'Nothing to clean', { attrs: `data-storageclean="${esc(x.id)}" ${x.can_clean ? '' : 'disabled'}`, title: x.clean }) : '<span class="note">Nothing to clean</span>')).join('') : '<span class="skeleton" style="width:40%"></span>'}`;
  } else if (sec === 'notifications') {
    if (!S.notices) { loadNotices().then(renderView); return '<div class="pad"><span class="skeleton" style="width:40%"></span></div>'; }
    const off = new Set(S.notices.off || []), q = quietUntil();
    page = `<h2>Notifications</h2><p class="lead">Everything LocalSites tells you is kept under the bell in the status bar for 30 days. Choose what also pops up on the screen.</p>
      ${Object.entries(NOTICE_KINDS).map(([k, [label]]) => settingRow(label, k === 'agent' ? 'Always on: an agent waits for your answer.' : '',
        `<label class="switch"><input type="checkbox" data-noticeoff="${k}" ${off.has(k) ? '' : 'checked'} ${k === 'agent' ? 'disabled' : ''}><span></span></label>`)).join('')}
      ${settingRow('Do Not Disturb', q ? `On ${esc(quietLabel(q))}. Notifications still go under the bell.` : 'Nothing pops up for a while, except agent requests. Everything still goes under the bell.',
        q ? '<button class="btn" data-quiet="0">Turn off</button>' : '<div class="form-row"><button class="btn" data-quiet="60">1 hour</button><button class="btn" data-quiet="tomorrow">Until tomorrow</button></div>')}`;
  } else if (sec === 'problems') {
    page = problemsPage();
  } else if (sec === 'updates') {
    page = updatesPage();
  } else if (sec === 'advanced') {
    page = `<h2>Advanced</h2><p class="lead">Files LocalSites keeps, and tools for troubleshooting.</p>
      ${settingRow('Data folder', '', `<code style="word-break:break-all">${esc(st.data_dir)}</code>`)}
      ${settingRow('Logs', '', `<code style="word-break:break-all">${esc(st.logs_dir)}</code>`)}
      ${settingRow('Health check', 'Runtimes, services, domains, certificates and every running project.', `<button class="btn" data-view="doctor">${icon.doctor}Run Doctor</button>`)}
      ${settingRow('Debug bundle', 'Logs, configs and a health report, with passwords removed.', `<button class="btn" data-cmd="bundle">${icon.bug}Create</button>`)}
      ${settingRow('Command line', 'Same features as the app, with <code>--json</code> for scripts.', `<button class="btn" data-cmd="clihelp">${icon.cli}Set up</button>`)}`;
  }
  return `<div class="settings"><nav aria-label="Settings sections">${nav}</nav><div class="page">${page}</div></div>`;
}

/* ------------------------------------------------------------------ events */
const CLICKABLE = ['openuse', 'dbmamp', 'rbadd', 'rbaddg', 'rbcomb', 'rbmenu', 'rbm', 'rbopt', 'rbundo', 'rbtab', 'rbcopy', 'rbtosql', 'rbtoggle', 'rlopen', 'rlfilter', 'rlreceipt', 'lslevel', 'lsclear', 'lspause', 'lsjump', 'lsgroup', 'goarea', 'dbbackups', 'notice', 'revoke', 'ntab', 'undo', 'splitopen', 'splitmenu', 'tshue', 'tsview', 'tsrad', 'themepal', 'themereset', 'themecopy', 'launchhide', 'retry', 'suggest', 'nrdrop', 'noticeall', 'quiet', 'noticeoff', 'addroot', 'openwith', 'mkdefault', 'projrename', 'projhide', 'projtrash', 'unhide', 'twist', 'scope', 'dbopen', 'dataconn', 'fopen', 'fclear', 'fprop', 'fback', 'dprop', 'dpane', 'gitop', 'gitreload', 'gitall', 'gbranch', 'gswitch', 'openweb',
  'projgit', 'storageclean', 'agenton', 'agentoff', 'approve', 'openmore', 'dbopenin', 'adminer', 'sharestart', 'sharestop', 'shareinstall', 'mysqlconnect', 'hidecards', 'addoninstall', 'addonflush', 'addonconnect', 'addonopts', 'addonlog', 'cell', 'rowdel', 'insdel', 'export', 'sqlh', 'sqlhclear', 'ac', 'agentex', 'takeover', 'envedit', 'iniedit',
  'rootdel', 'rootpick', 'setupfix', 'ptabgo', 'ptab', 'openurl', 'install', 'dtable', 'dtab', 'sortcol', 'setsec', 'theme-set', 'connectdb', 'restoreenv', 'tab', 'closetab', 'view', 'proj',
  'projlog', 'projcheck', 'sect', 'fav', 'toggle', 'app', 'row', 'cmd', 'dbbackup', 'dbcopy', 'impdb', 'copy', 'check', 'agentrun', 'menu', 'updchan', 'repmode', 'probreview', 'probdismiss', 'dbmore', 'dbact', 'pgopen'];
// Slide-over sidebar: a press outside it only closes it. The press, and the click that follows, never reach
// whatever is underneath (a row, a tab, a button).
let swallowClick = false;
document.addEventListener('pointerdown', (e) => {
  if (!S.sidePeek || e.target.closest('#side, #tbSidebar')) return;
  e.preventDefault(); e.stopPropagation();
  S.sidePeek = false; $('#body').classList.remove('peek');
  swallowClick = true; setTimeout(() => { swallowClick = false; }, 600);
}, true);
// Popups close as soon as the pointer goes down outside them. A click alone isn't enough: on the window's
// drag areas (sidebar, header, status bar) a press moves the window and no click ever arrives.
function closePopups() {
  $('#menu')?.classList.remove('open');
  const ac = $('#acBox'); if (ac && !ac.hidden) { ac.hidden = true; if (S.data) S.data.ac = null; }
}
document.addEventListener('pointerdown', (e) => {
  const m = $('#menu');
  if (m?.classList.contains('open') && !e.target.closest('#menu')) {
    m.classList.remove('open');
    const a = e.target.closest(CLICK_SEL);
    if (a && anchorKey(a) === menuKey) { reopenBlock = menuKey; setTimeout(() => { reopenBlock = ''; }, 600); }
  }
  const ac = $('#acBox');
  if (ac && !ac.hidden && !e.target.closest('#acBox, #dSQL')) { ac.hidden = true; if (S.data) S.data.ac = null; }
}, true);
window.addEventListener('blur', closePopups); // clicked another app or outside the window
window.addEventListener('resize', () => $('#menu')?.classList.remove('open')); // its position would be wrong
for (const type of ['mousedown', 'mouseup', 'click', 'dblclick']) {
  document.addEventListener(type, (e) => {
    if (!swallowClick || e.target.closest('#side')) return;
    e.preventDefault(); e.stopImmediatePropagation();
    if (type === 'click') swallowClick = false; // done: the next click is a normal one
  }, true);
}
const CLICK_SEL = CLICKABLE.map(a => `[data-${a}]`).join(',') + ',#wsBtn,#tbSidebar,#tbBack,#tbFwd,#tbNewTab,#tbActivity';
document.addEventListener('click', async (e) => {
  const inMenu = e.target.closest('#menu');
  if (!inMenu) $('#menu').classList.remove('open');
  const t = e.target.closest(CLICK_SEL);
  if (!t) return;
  const d = t.dataset;
  if (t.id === 'tbSidebar') return toggleSidebar();
  if (t.id === 'tbBack') return goHistory(-1);
  if (t.id === 'tbFwd') return goHistory(1);
  if (t.id === 'tbNewTab') return navigate('overview', null, { newTab: true });
  if (t.id === 'tbActivity') return showActivityMenu(t);
  if (t.id === 'wsBtn') return showWorkspaceMenu(t);
  if (Object.keys(d).some(k => /^rb[a-z]/.test(k) && k !== 'rbnode' && k !== 'rbzone')) return rbClick(t, d);
  if (d.openuse) { store.set('openPrimary', d.openuse); setTimeout(renderDetail); } // Open menu: remember it, then open it below
  // sidebar trees
  if (d.twist !== undefined) { e.stopPropagation(); return toggleNode(d.twist); }
  if (d.scope) { S.pf = { [d.scope]: [d.val] }; S.ptab = 'all'; saveUI(); return navigate('projects'); }
  if (d.dbopen) return openDatabase('localsites', d.dbopen);
  if (d.pgopen) { S.data.conns = await act(() => bridge.call('data.connections')) || S.data.conns; return openDatabase(pgConnID(d.pgopen), 'public'); }
  if (d.dbmore) { e.stopPropagation(); const [kind, name] = d.dbmore.split('|'); return showDbMenu(t, kind, name); }
  if (d.dbact) { $('#menu').classList.remove('open'); const [a, kind, name] = d.dbact.split('|'); return dbAction(a, kind, name); }
  if (d.dataconn) return openDatabase(d.dataconn, '');
  // project filters & display
  if (d.fopen) return showFilterMenu(t, d.fopen);
  if (d.fclear) { delete S.pf[d.fclear]; saveUI(); return render(); }
  if (d.fback) { FM.prop = null; FM.q = ''; return refreshFilterMenu(); }
  if (d.fprop && d.fval !== undefined) {
    const list = S.pf[d.fprop] || [], v = d.fval;
    S.pf[d.fprop] = list.includes(v) ? list.filter(x => x !== v) : [...list, v];
    if (!S.pf[d.fprop].length) delete S.pf[d.fprop];
    saveUI(); renderView(); renderHead(); renderSide(); return refreshFilterMenu();
  }
  if (d.fprop) { FM.prop = d.fprop; FM.q = ''; return refreshFilterMenu(); }
  if (d.dprop) { S.props.has(d.dprop) ? S.props.delete(d.dprop) : S.props.add(d.dprop); saveUI(); renderView(); $('#menu').innerHTML = displayMenuHTML(); return; }
  // project panel & git
  if (d.dpane) { S.dpane = d.dpane; saveUI(); return renderDetail(); }
  if (d.projgit) { S.dpane = 'git'; saveUI(); return navigate('projects', d.projgit); }
  if (d.gitreload) return gitLoad(d.gitreload);
  if (d.gitall) { S.git[d.gitall].allFiles = true; return renderDetail(); }
  if (d.gitop) {
    const f = d.gfolder;
    if (d.gitop === 'fetch') return gitAction(f, 'git.fetch', {}, 'Fetch');
    if (d.gitop === 'pull') return gitAction(f, 'git.pull', {}, 'Pull');
    if (d.gitop === 'branch') { const name = await askText('New branch', 'feature/my-change'); if (name) gitAction(f, 'git.branch', { name }, 'New branch'); return; }
  }
  if (d.gbranch) return showBranchMenu(t, d.gfolder, d.gbranch, !!d.remote);
  if (d.gswitch) {
    $('#menu').classList.remove('open');
    if (!await confirmBox(`Switch to ${d.gswitch}?`, 'Your files change to that branch. Uncommitted work is never touched: switching is refused if there is any.', 'Switch')) return;
    return gitAction(d.gfolder, 'git.checkout', { name: d.gswitch }, 'Switch branch');
  }
  if (d.openweb) return openWeb(d.openweb);
  if (d.takeover) return command('takeover', { folder: d.takeover });
  if (d.envedit) return command('envedit', { folder: d.envedit });
  if (d.iniedit) return command('iniedit', { folder: d.iniedit });
  // data browser
  if (d.cell && S.data.mode === 'edit' && !t.classList.contains('editing')) return editCell(t);
  if (d.rowdel !== undefined) { const i = +d.rowdel, del = S.data.ed.del; del.has(i) ? del.delete(i) : del.add(i); return renderView(); }
  if (d.insdel !== undefined) { S.data.ed.ins.splice(+d.insdel, 1); return renderView(); }
  if (d.export) { $('#menu').classList.remove('open'); return exportCSV(d.export, d.ename || 'export'); }
  if (d.sqlh !== undefined) { const h = store.get('sqlHistory', []).filter(x => x.conn === S.data.conn)[+d.sqlh]; $('#menu').classList.remove('open'); if (h) { S.data.sql = h.sql; renderView(); $('#dSQL')?.focus(); } return; }
  if (d.sqlhclear) { store.set('sqlHistory', store.get('sqlHistory', []).filter(x => x.conn !== S.data.conn)); $('#menu').classList.remove('open'); return toast('History cleared'); }
  if (d.ac !== undefined) return acAccept($('#dSQL'), +d.ac);
  if (d.setupfix) {
    const r = await act(() => bridge.call('setup.run', { ids: [d.setupfix] }), 'Setup');
    if (r) { S.setup = r; toast('Working on it in the background; Overview shows the progress'); render(); }
    return;
  }
  if (d.addoninstall) {
    const a = S.status.addons.find(x => x.id === d.addoninstall);
    if (!await confirmBox(`Install ${a.name}?`, `This runs “${a.install_command}” in the background. ${a.name} is open source (${a.license}). It only runs when you switch it on, and only this Mac can reach it.`, 'Install')) return;
    const r = await act(() => bridge.call('addons.install', { id: a.id }), 'Install');
    if (r) { await act(loadStatus); render(); pollAddonInstall(a.id); }
    return;
  }
  if (d.addonflush) {
    const a = S.status.addons.find(x => x.id === d.addonflush);
    if (!await confirmBox(`Empty ${a.name}?`, 'Every cached value, session and queued job stored in it is removed. Your projects rebuild caches as they run.', 'Empty cache')) return;
    return logToast(await act(() => bridge.call('addons.flush', { id: a.id }), 'Flush'));
  }
  if (d.openmore) return showOpenMenu(t, d.openmore);
  if (d.storageclean) {
    const x = S.storage.find(i => i.id === d.storageclean);
    if (!await confirmBox(`${x.name}: clean up?`, x.clean + '. This can\'t be undone.', 'Clean up')) return;
    logToast(await act(() => bridge.call('storage.clean', { id: x.id }), 'Clean up'));
    S.storage = null; return renderView();
  }
  if (d.agenton) {
    const c = S.agents.find(x => x.id === d.agenton);
    const r = await act(() => bridge.call('agents.connect', { id: c.id }), 'Connect');
    if (r) { await dialog(`<h3>${esc(c.name)} is connected</h3><p class="note">${c.config ? `Added to <code>${esc(c.config.replace(/^\/Users\/[^/]+/, '~'))}</code> (a backup was saved next to it):` : 'Ran:'}</p>${UI.code(r.added)}
      <p class="note">Restart ${esc(c.name)} to pick it up, then ask it something like “list my LocalSites projects”.</p><div class="form-row" style="justify-content:flex-end;margin-top:12px"><button class="btn primary" data-dlg="ok">Done</button></div>`); }
    S.agents = null; return loadAgents();
  }
  if (d.agentoff) { logToast(await act(() => bridge.call('agents.disconnect', { id: d.agentoff }), 'Disconnect')); S.agents = null; return loadAgents(); }
  if (d.approve) { const [id, ok, w] = d.approve.split('|'); t.disabled = true; return decideApproval(id, ok === '1', w === 'w'); }
  if (d.revoke) {
    const r = await act(() => bridge.call('approvals.revoke', { id: d.revoke }), 'End pass');
    if (r) toast('Ended. The next request of that kind asks again.');
    return pollApprovals();
  }
  if (d.dbopenin) {
    const [name, app] = d.dbopenin.split('|');
    const c = await act(() => bridge.call('db.credentials', { name }), 'Login'); if (!c) return;
    await act(() => bridge.copy(c.password));
    if (await act(() => bridge.call('db.open_in', { name, app }), 'Open')) toast(`Opening ${name}. The password is on your clipboard: paste it when asked.`);
    return;
  }
  if (d.shareprov) { SF.prov = d.shareprov; return renderDetail(); }
  if (d.sharestart) {
    const s = site(d.sharestart), prov = d.shareprovUse, mins = +$('#shareMins').value, pw = SF.open ? '' : $('#sharePw').value.trim();
    if (!pw && !SF.open) return toast('Enter a password, or tick “No password”', 'warn');
    const until = new Date(Date.now() + mins * 60000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    if (!await confirmBox(`Share ${s.name} on the internet?`, `Anyone with the address can open ${s.domain} until ${until}${pw ? ' (they need the password)' : ', without a password'}. Only this site is reachable; your other sites and databases are not. You can stop it at any time.`, 'Share')) return;
    t.disabled = true; t.innerHTML = '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>Connecting…';
    const r = await act(() => bridge.call('share.start', { folder: s.folder, provider: prov, password: pw, minutes: mins, no_password: SF.open }), 'Share');
    await act(loadStatus); render();
    if (r) {
      if (pw) SHARE_PW[r.id] = pw;
      await act(() => bridge.copy(r.url)); // the link only: send the password separately
      toast(`${s.name} is live at ${r.url} (link copied${pw ? '; copy the password with its own button' : ''})`); render(); // the form keeps this password, so sharing again doesn't switch to a new one
    }
    return;
  }
  if (d.sharestop) { logToast(await act(() => bridge.call('share.stop', { id: d.sharestop }), 'Stop sharing')); await act(loadStatus); return render(); }
  if (d.shareinstall) {
    const p = S.status.share_providers.find(x => x.id === d.shareinstall);
    if (!await confirmBox(`Install ${p.name}?`, `This runs “${p.install_command}” in the background. ${p.name} is free and open source (${p.license}); it only runs while you share a site.`, 'Install')) return;
    if (await act(() => bridge.call('share.install', { provider: p.id }), 'Install')) { await act(loadStatus); render(); pollShareInstall(p.id); }
    return;
  }
  if (d.mysqlconnect) return mysqlConnectDialog(d.mysqlconnect);
  if (d.addonconnect) return addonConnectDialog(S.status.addons.find(x => x.id === d.addonconnect));
  if (d.addonopts) return addonOptionsDialog(S.status.addons.find(x => x.id === d.addonopts));
  if (d.addonlog) { S.log = { kind: d.install ? 'addon-install' : 'addon', folder: d.addonlog }; return navigate('logs'); }
  if (d.hidecards) { store.set('hide.' + d.hidecards, true); return renderView(); }
  if (d.agentex) { S.agent.text = d.agentex; S.agent.sel = 0; renderView(); return $('#agentInput')?.focus(); }
  if (d.menu !== undefined) { $('#menu').classList.remove('open'); return command(d.menu); }
  if (d.closetab !== undefined) { e.stopPropagation(); return closeTab(+d.closetab); }
  if (d.tab !== undefined) { const v0 = cur().view; S.tab = +d.tab; onEnter(cur().view); saveUI(); render(); if (v0 !== cur().view) animateView(); return; }
  if (d.view) return navigate(d.view);
  if (d.proj) return navigate('projects', d.proj);
  if (d.projlog) { S.log = { kind: 'site-error', folder: d.projlog }; return navigate('logs'); }
  if (d.projcheck) { S.dpane = 'details'; navigate('projects', d.projcheck); return runCheck(d.projcheck); }
  if (d.sect) { if (d.sect.startsWith('g:')) { S.closed[d.sect] = !S.closed[d.sect]; saveUI(); return renderView(); } return toggleSect(d.sect); }
  if (d.fav) { S.favs.has(d.fav) ? S.favs.delete(d.fav) : S.favs.add(d.fav); saveUI(); return render(); }
  if (d.toggle) { e.stopPropagation(); const s = site(d.toggle); return updateSite(s.folder, { enabled: !s.enabled }); }
  if (d.notice) { e.preventDefault(); return noticeClicked(d.notice); }
  if (d.suggest) { S.filter = d.suggest; renderHead(); renderView(); return $('#filter')?.focus(); }
  if (d.goarea) { document.getElementById(d.goarea)?.scrollIntoView({ behavior: 'smooth', block: 'start' }); return; }
  if (d.dbbackups) return backupsDialog(d.dbbackups);
  if (d.dbmamp) return openDatabase('mamp-pro', d.dbmamp);
  if (d.rlopen) { rlOpen = rlOpen === d.rlopen ? '' : d.rlopen; return renderView(); }
  if (d.rlfilter) { rlFilter = d.rlfilter; rlOpen = ''; return renderView(); }
  if (d.rlreceipt) { rlReceipt = rlReceipt === d.rlreceipt ? '' : d.rlreceipt; return renderView(); }
  if (d.undo) return undoCall(d.undo);
  if (d.launchhide) { store.set('hide.launch', true); toast('Hidden. Overview still shows anything that needs fixing.'); return renderView(); }
  if (d.lslevel) { const L = LSV.levels; if (L.has(d.lslevel) && L.size > 1) L.delete(d.lslevel); else L.add(d.lslevel); return lsRedraw(); }
  if (d.lsclear) { LSV.q = ''; lsRedraw(); return $('#logQ')?.focus(); }
  if (d.lspause) { if (LSV.paused) return lsJump(); LSV.paused = true; LSV.shown = LSV.lines.length; return lsRedraw(); }
  if (d.lsjump) return lsJump();
  if (d.lsgroup) { const id = +d.lsgroup; LSV.open.has(id) ? LSV.open.delete(id) : LSV.open.add(id); return lsRedraw({ bar: false }); }
  if (d.ntab) { e.preventDefault(); noticeTab = d.ntab; $('#menu').innerHTML = noticeMenuHTML(); return; }
  if (d.noticeall) {
    e.preventDefault();
    await act(() => bridge.call(d.noticeall === 'clear' ? 'notices.clear' : 'notices.read', {}), 'Notifications');
    await loadNotices(); renderStatus();
    if ($('#menu').classList.contains('open')) $('#menu').innerHTML = noticeMenuHTML();
    return;
  }
  if (d.quiet !== undefined) {
    e.preventDefault();
    const minutes = d.quiet === 'tomorrow' ? minutesToMorning() : +d.quiet;
    const r = await act(() => bridge.call('notices.quiet', { minutes }), 'Do Not Disturb');
    if (r) { S.notices = r; toast(minutes ? `Do Not Disturb ${quietLabel(quietUntil())}` : 'Do Not Disturb is off'); }
    renderStatus(); if (cur().view === 'settings') renderView();
    if ($('#menu').classList.contains('open')) $('#menu').innerHTML = noticeMenuHTML();
    return;
  }
  if (d.addroot) {
    const st = await act(() => bridge.call('settings.get'), 'Settings'); if (!st) return;
    const roots = [...(st.roots || []), { path: d.addroot, label: d.addrootlabel || 'htdocs' }];
    const r = await act(() => bridge.call('settings.update', { roots }), 'Add folder');
    if (r) { toast(`Added ${d.addroot} as a projects folder`); S.settings = null; await refresh(); render(); }
    return;
  }
  if (d.projrename) {
    const s = site(d.projrename); if (!s) return;
    const name = await dialog(`<h3>Rename ${esc(s.name)}</h3><p class="note">Renames the folder in ${esc(s.path.slice(0, s.path.length - s.name.length - 1))}. Settings and logs move along, and the address follows the new name unless you set your own. The database keeps its name.</p>
      <input class="input" style="width:100%" value="${esc(s.name)}" spellcheck="false" aria-label="New name">
      <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn ghost" data-dlg="cancel">Cancel</button><button class="btn primary" data-dlg="ok">Rename</button></div>`);
    if (!name || name === s.name) return;
    const r = await act(() => bridge.call('sites.rename', { folder: s.folder, new_name: name }), 'Rename');
    if (r) { logToast(r); await refresh(); const nf = s.folder.includes('/') ? s.folder.replace(/[^/]+$/, name) : name; if (site(nf)) navigate('projects', nf); }
    return;
  }
  if (d.projhide) {
    const s = site(d.projhide); if (!s) return;
    if (!await confirmBox(`Hide ${s.name}?`, 'LocalSites stops listing and serving it. The folder, its files and its database stay as they are. Settings → Projects folders can show it again.', 'Hide')) return;
    const r = await act(() => bridge.call('sites.hide', { folder: s.folder }), 'Hide');
    if (r) { logToast(r); cur().sel = null; await refresh(); render(); }
    return;
  }
  if (d.projtrash) {
    const s = site(d.projtrash); if (!s) return;
    const typed = await dialog(`<h3>Move ${esc(s.name)} to the Trash?</h3>
      <p class="note">The folder <code>${esc(s.path)}</code> goes to the Trash, where you can put it back.${s.database ? ` The database <b>${esc(s.database)}</b> is kept.` : ''}</p>
      <p class="note">Type <b>${esc(s.name)}</b> to confirm.</p>
      <input class="input" style="width:100%" spellcheck="false" autocomplete="off" aria-label="Project name">
      <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn ghost" data-dlg="cancel">Cancel</button><button class="btn danger" data-dlg="ok">Move to Trash</button></div>`);
    if (typed == null) return;
    if (typed !== s.name) return toast('The name didn’t match, so nothing was moved', 'warn');
    const r = await act(() => bridge.call('sites.trash', { folder: s.folder, name: typed }), 'Move to Trash');
    if (r) { logToast(r); cur().sel = null; await refresh(); render(); }
    return;
  }
  if (d.unhide) {
    const r = await act(() => bridge.call('sites.unhide', { folder: d.unhide }), 'Show');
    if (r) { logToast(r); await loadSettings(); await refresh(); render(); }
    return;
  }
  if (d.mkdefault) { e.preventDefault(); e.stopPropagation(); $('#menu').classList.remove('open'); const [k, id] = d.mkdefault.split(':'); return act(() => setDefaultApp(k, id), 'Save'); }
  if (d.openwith) { e.preventDefault(); e.stopPropagation(); $('#menu').classList.remove('open'); return openWith(d.openwith, d.folder); }
  if (d.app) { e.preventDefault(); e.stopPropagation(); $('#menu').classList.remove('open'); return act(() => bridge.call('open', { folder: d.folder, app: d.app }), 'Open'); }
  if (d.row) return navigate('projects', cur().sel === d.row ? null : d.row);
  if (d.dbbackup) { const r = await act(() => bridge.call('db.backup', { name: d.dbbackup })); if (r) toast('Backup saved:\n' + r.file); return; }
  if (d.dbcopy) {
    const c = await act(() => bridge.call('db.credentials', { name: d.dbcopy }));
    if (c) { await bridge.copy(`DB_HOST=localhost\nDB_NAME=${c.database}\nDB_USER=${c.user}\nDB_PASS=${c.password}\n`); toast(`.env lines for ${c.database} copied`); }
    return;
  }
  if (d.impdb !== undefined && t.type === 'checkbox') { t.checked ? S.imp.chosen.add(d.impdb) : S.imp.chosen.delete(d.impdb); return renderView(); }
  if (d.copy !== undefined) { $('#menu').classList.remove('open'); await act(() => bridge.copy(d.copy)); return toast('Copied'); }
  if (d.check) return runCheck(d.check);
  if (d.agentrun !== undefined) return agentRun(+d.agentrun);
  if (d.setsec) { S.setSec = d.setsec; S.setErrors = {}; return renderView(); }
  if (d.repmode) { if (d.repmode !== (setVal('report_mode') || 'off')) { await setUpdSetting('report_mode', d.repmode); await act(loadProblems); renderView(); } return; }
  if (d.probreview) return reviewProblem(d.probreview);
  if (d.probdismiss !== undefined) { await act(() => bridge.call('problems.dismiss', { id: d.probdismiss })); await act(loadProblems); return renderView(); }
  if (d.updchan) { if (d.updchan !== (setVal('update_channel') || 'stable')) await setUpdSetting('update_channel', d.updchan); return; }
  if (d.rootdel !== undefined) { const r = rootsDraft(); r.splice(+d.rootdel, 1); setDraft('roots', r); return renderView(); }
  if (d.rootpick !== undefined) { const p = await act(() => window.go.main.App.ChooseFolder()); if (p) { const r = rootsDraft(); r[+d.rootpick].path = p; if (!r[+d.rootpick].label) r[+d.rootpick].label = p.split('/').pop(); setDraft('roots', r); renderView(); } return; }
  if (d.themeSet) { S.theme = d.themeSet; saveUI(); applyTheme(); return renderView(); }
  if (d.themepal) return setLook({ palette: d.themepal });
  if (d.tshue) return setLook({ hue: d.tshue === 'none' ? null : +d.tshue });
  if (d.tsview) { tsView = d.tsview; return renderView(); }
  if (d.tsrad) return setLook({ radius: +d.tsrad });
  if (d.themereset) { setLook({ ...LOOK_DEFAULT }); return toast('Back to the default theme'); }
  if (d.themecopy) { await act(() => bridge.copy(lookCSS())); return toast('Theme copied as CSS variables'); }
  if (d.connectdb) {
    if (!await confirmBox(`Connect ${d.connectdb} to ${d.db}?`, `Its .env is backed up, then the database name, user and password are replaced with ${d.db}'s own LocalSites login. You can undo this.`, 'Connect')) return;
    const r = await act(() => bridge.call('sites.connect_db', { folder: d.connectdb, name: d.db }), 'Connect');
    if (r) { S.sites = r.sites; logToast(r); render(); }
    return;
  }
  if (d.restoreenv) {
    const r = await act(() => bridge.call('sites.restore_env', { folder: d.restoreenv }), 'Undo');
    if (r) { S.sites = r.sites; logToast(r); render(); }
    return;
  }
  if (d.ptab) { S.ptab = d.ptab; saveUI(); return renderView(); }
  if (d.ptabgo) { S.pf = { type: [d.ptabgo] }; S.ptab = 'all'; saveUI(); return navigate('projects'); }
  if (d.openurl) { e.preventDefault(); e.stopPropagation(); return act(() => bridge.call('open', { folder: d.openurl, app: 'browser' })); }
  if (d.install) { const r = await act(() => bridge.call('sites.install', { folder: d.install }), 'Install'); if (r) { toast('Installing dependencies…', 'warn'); await act(loadSites); render(); pollInstall(d.install); } return; }
  if (d.dtable) { if (cur().view !== 'data') navigate('data'); return dataOpenTable(d.dtable); }
  if (d.dtab) { S.data.tab = d.dtab; renderView(); if (d.dtab === 'sql') setTimeout(() => $('#dSQL')?.focus(), 20); return; }
  if (d.sortcol) { if (!await guardEdits()) return; const D2 = S.data; if (D2.order === d.sortcol) D2.desc = !D2.desc; else { D2.order = d.sortcol; D2.desc = false; } D2.page = 0; return dataLoadRows(); }
  if (d.cmd) return command(d.cmd, { anchor: t });
});
async function pollInstall(folder) {
  for (let i = 0; i < 600; i++) {
    await new Promise(r => setTimeout(r, 2000));
    await act(loadSites);
    const s = site(folder);
    if (cur().sel === folder) renderDetail();
    if (!s?.installing) { toast(s?.problems?.length ? `Install finished for ${s.name}; check the log` : `Dependencies installed for ${s?.name}`, s?.problems?.length ? 'warn' : 'ok'); render(); return; }
  }
}
async function runCheck(folder) {
  const box = $('#siteChecks'); if (box) box.innerHTML = '<span class="spinner" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></span>';
  S.checks[folder] = await act(() => bridge.call('sites.check', { folder })) || [];
  renderDetail();
}
function rootsDraft() { return JSON.parse(JSON.stringify(setVal('roots') || [])); }
function setDraft(key, value) {
  if (key === 'roots') {
    if (JSON.stringify(value) === JSON.stringify(S.settings.roots)) delete S.setDraft.roots; else S.setDraft.roots = value;
    Object.keys(S.setErrors).filter(k => k.startsWith('roots')).forEach(k => delete S.setErrors[k]);
    const bar = document.querySelector('.save-bar'); if (bar) bar.outerHTML = saveBar();
    return;
  }
  const orig = key.startsWith('php_ini.') ? (S.settings.php_ini?.[key.slice(8)] ?? '') : S.settings[key];
  // While a save is in flight the saved values aren't known yet: keep every edit as a change
  if (!S.setSaving && String(value) === String(orig ?? '')) delete S.setDraft[key]; else S.setDraft[key] = value;
  delete S.setErrors[key];
  const bar = document.querySelector('.save-bar'); if (bar) bar.outerHTML = saveBar();
}
document.addEventListener('change', async (e) => {
  if (e.target.dataset.root !== undefined && e.target.tagName === 'SELECT') { const r = rootsDraft(); r[+e.target.dataset.root].layout = e.target.value; setDraft('roots', r); return; }
  if (e.target.dataset.updset) { const k = e.target.dataset.updset; return setUpdSetting(k, e.target.type === 'checkbox' ? e.target.checked : e.target.value); }
  if (e.target.dataset.setting && (e.target.type === 'checkbox' || e.target.tagName === 'SELECT')) {
    setDraft(e.target.dataset.setting, e.target.type === 'checkbox' ? e.target.checked : e.target.value); return;
  }
  const f = e.target.dataset.field, sel = cur().sel;
  if (f && sel) {
    let v = e.target.type === 'checkbox' ? e.target.checked : e.target.value.trim();
    if (f === 'port') v = Number(v) || 0;
    if (site(sel)[f] !== v) updateSite(sel, { [f]: v });
    return;
  }
  if (e.target.id === 'logSrc') { const [k, f2] = e.target.value.split('|'); S.log = { kind: k, folder: f2 }; loadLog(); }
  if (e.target.id === 'logAuto') S.logAuto = e.target.checked;
  if (e.target.dataset.adminertoggle) { if (!await setAdminer(e.target.checked)) e.target.checked = !e.target.checked; return; }
  if (e.target.dataset.addontoggle) {
    const id = e.target.dataset.addontoggle, on = e.target.checked;
    const r = await act(() => bridge.call('addons.update', { id, enabled: on }), on ? 'Switch on' : 'Switch off');
    if (!r) e.target.checked = !on; else logToast(r);
    await act(loadStatus); render(); return;
  }
  if (e.target.id === 'impSource') S.imp.source = e.target.value;
  if (e.target.id === 'groupBy') { S.groupBy = e.target.value; saveUI(); return renderView(); }
  if (e.target.id === 'orderBy') { S.order = e.target.value; saveUI(); return renderView(); }
  if (e.target.id === 'dConn') { const v = e.target.value; if (!await guardEdits()) { e.target.value = S.data.conn; return; } S.data.conn = v; dataConnect(); }
  if (e.target.id === 'dSchema') { const v = e.target.value; if (!await guardEdits()) { e.target.value = S.data.schema; return; } S.data.schema = v; act(dataLoadTables).then(() => { renderView(); renderSide(); }); }
  if (e.target.dataset.prefapp) return act(() => setDefaultApp(e.target.dataset.prefapp, e.target.value), 'Save');
  if (e.target.dataset.noticeoff) {
    const off = new Set(S.notices?.off || []);
    if (e.target.checked) off.delete(e.target.dataset.noticeoff); else off.add(e.target.dataset.noticeoff);
    const r = await act(() => bridge.call('notices.off', { kinds: [...off] }), 'Notifications');
    if (r) S.notices = r; else e.target.checked = !e.target.checked;
    return;
  }
  if (e.target.id === 'shareOpen') { SF.open = e.target.checked; return renderDetail(); }
  if (e.target.id === 'shareMins') SF.mins = +e.target.value;
  if (e.target.id === 'dTableSel' && e.target.value) return dataOpenTable(e.target.value);
  if (e.target.id === 'dAllowWrite') S.data.allowWrite = e.target.checked;
});
document.addEventListener('input', (e) => {
  if (e.target.dataset.setting && e.target.type !== 'checkbox' && e.target.tagName !== 'SELECT') { setDraft(e.target.dataset.setting, e.target.value); return; }
  if (e.target.dataset.root !== undefined && e.target.tagName !== 'SELECT') { const r = rootsDraft(); r[+e.target.dataset.root][e.target.dataset.rootf] = e.target.value; setDraft('roots', r); return; }
  if (e.target.id === 'filter') { S.filter = e.target.value; renderView(); }
  if (e.target.id === 'fmQ') { FM.q = e.target.value; refreshFilterMenu(); }
  if (e.target.id === 'sharePw') SF.pw = e.target.value;
  if (e.target.id === 'agentQ') { agentQ = e.target.value; const pos = e.target.selectionStart; renderView(); const q = $('#agentQ'); if (q) { q.focus(); q.setSelectionRange(pos, pos); } }
  if (e.target.id === 'logQ') { const had = !!LSV.q; LSV.q = e.target.value; lsRedraw({ bar: had !== !!LSV.q }); }
  if (e.target.id === 'impUser') S.imp.user = e.target.value;
  if (e.target.id === 'impPass') S.imp.password = e.target.value;
  if (e.target.id === 'agentInput') { S.agent.text = e.target.value; S.agent.sel = 0; renderView(); }
  if (e.target.id === 'dTFilter') { S.data.tfilter = e.target.value; const pos = e.target.selectionStart; renderView(); const f = $('#dTFilter'); if (f) { f.focus(); f.setSelectionRange(pos, pos); } }
  if (e.target.id === 'dSQL') { S.data.sql = e.target.value; acUpdate(e.target); }
});

// Pressing the button that opened a menu closes it (the press already did); its click must not reopen it.
const anchorKey = (el) => el ? el.id || JSON.stringify(el.dataset) + el.className : '';
let menuKey = '', reopenBlock = '';
function menuAt(anchor, html, cls = '') {
  if (reopenBlock && reopenBlock === anchorKey(anchor)) { reopenBlock = ''; return; }
  const m = $('#menu'), r = anchor.getBoundingClientRect();
  menuKey = anchorKey(anchor);
  m.className = 'menu ' + cls;
  m.innerHTML = html;
  const w = cls.includes('wide') ? 420 : cls.includes('fmenu') ? 300 : 260;
  m.style.left = Math.max(8, Math.min(r.left, innerWidth - w - 8)) + 'px';
  if (r.bottom > innerHeight - 260) { m.style.top = ''; m.style.bottom = (innerHeight - r.top + 6) + 'px'; m.style.transformOrigin = 'bottom left'; }
  else { m.style.bottom = ''; m.style.top = (r.bottom + 6) + 'px'; m.style.transformOrigin = 'top left'; }
  void m.offsetWidth; // start the open animation from the closed state
  m.classList.add('open');
}

function showWorkspaceMenu(anchor) {
  menuAt(anchor, `<button data-menu="goto-settings">${icon.settings}Settings</button><button data-menu="goto-doctor">${icon.doctor}Doctor</button>
    <button data-menu="newproject">${icon.compose}New project</button><hr>
    <button data-menu="restart">${icon.refresh}Restart services</button><button data-menu="stop">${icon.stop}Stop services</button><hr>
    <button data-menu="keys">${icon.info}Keyboard shortcuts</button>`);
}
function showHelpMenu(anchor) {
  const fails = S.doctor ? S.doctor.filter(c => c.status === 'fail').length : null;
  const latest = S.version?.changelog?.[0];
  const u = S.upd, upd = updPending() ? `<button data-menu="whatsnew">${icon.sparkles}${u.phase === 'ready' ? `Restart to install ${esc(u.latest.version)}` : `LocalSites ${esc(u.latest.version)} is available`}${u.latest.security ? '<span class="kb"><span class="bad">Security fix</span></span>' : ''}</button><hr>` : '';
  menuAt(anchor, `${upd}<button data-menu="docs">${icon.docs}Docs</button>
    <button data-menu="bundle">${icon.flare}Send debug bundle</button>
    <button data-menu="keys">${icon.keyboard}Keyboard shortcuts<span class="kb">${MOD}/</span></button>
    <button data-menu="goto-doctor">${icon.status}LocalSites status<span class="kb">${fails === null ? '' : fails ? `<span class="bad">${fails} problem${fails > 1 ? 's' : ''}</span>` : '<span class="ok">All good</span>'}</span></button>
    <button data-menu="clihelp">${icon.cli}Command-line tool</button>
    <button data-menu="goto-settings">${icon.settings}Settings<span class="kb">G then ,</span></button>
    ${latest ? `<div class="label2">What's new</div><div class="timeline">${latest.items.slice(0, 3).map(i => `<button data-menu="whatsnew" title="${esc(i)}"><span>${esc(i.split(/[:;,(]/)[0].trim())}</span></button>`).join('')}
      <button data-menu="whatsnew">Full changelog</button></div>` : ''}`);
  if (S.version) { S.seenVersion = S.version.version; saveUI(); renderSide(); }
}
function showDocs() {
  return dialog(`<h3>Getting started</h3><div class="note" style="line-height:1.7">
    <p><b>Projects</b>: every folder in your projects folder is a project. Switch one on and open <code>https://folder.test</code>.</p>
    <p><b>PHP</b>: pick a version per project in its panel. Global php.ini defaults are in Settings → PHP.</p>
    <p><b>Databases</b>: each database gets its own login. Copy MAMP PRO databases in Import, then <b>Connect</b> projects there.</p>
    <p><b>Problems</b>: run Doctor, or Check in a project’s panel. Send a debug bundle when reporting an issue.</p>
    <p><b>Keyboard</b>: ${MOD}K palette, ${MOD}T tabs, G then a letter to jump, ${MOD}/ all shortcuts.</p>
    <p><b>AI agents</b>: Settings → AI agents (MCP) shows how to connect Claude or Cursor.</p></div>
    <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn primary" data-dlg="ok">Done</button></div>`);
}
/* --- notification center: everything LocalSites showed, with Do Not Disturb --- */
const NOTICE_KINDS = { agent: ['AI agent requests', 'agent'], crash: ['Services that stopped', 'alertCircle'], install: ['Finished installs', 'download2'],
  setup: ['Setup finished', 'wrench'], share: ['Shares ending', 'globe'], update: ['Updates', 'sparkles'], problem: ['Problems', 'bug'] };
const quietUntil = () => S.notices?.quiet_until ? new Date(S.notices.quiet_until) : null;
function quietLabel(d) {
  if (!d) return '';
  if (d.getFullYear() > new Date().getFullYear() + 50) return 'until you turn it off';
  const sameDay = d.toDateString() === new Date().toDateString();
  return 'until ' + (sameDay ? '' : 'tomorrow ') + d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}
function bellButton() {
  const n = S.notices?.unread || 0, q = quietUntil();
  return `<button class="item bell" data-cmd="noticemenu" title="${q ? 'Do Not Disturb ' + esc(quietLabel(q)) : 'Notifications'}" aria-label="Notifications${n ? `, ${n} unread` : ''}">${q ? icon.bellOff : icon.bell}${n ? `<span class="nbadge">${n > 99 ? '99+' : n}</span>` : ''}</button>`;
}
function noticeDay(t) {
  const d = new Date(t), today = new Date(), y = new Date(); y.setDate(today.getDate() - 1);
  return d.toDateString() === today.toDateString() ? 'Today' : d.toDateString() === y.toDateString() ? 'Yesterday' : d.toLocaleDateString([], { weekday: 'long', day: 'numeric', month: 'short' });
}
// Tabs sort the list by what it's about; agent requests still waiting are answered right here.
const NOTICE_TABS = [['all', 'All', null], ['requests', 'Requests', ['agent']], ['problems', 'Problems', ['crash', 'problem']], ['updates', 'Updates', ['install', 'setup', 'share', 'update']]];
let noticeTab = 'all';
function noticeMenuHTML() {
  const v = S.notices || { notices: [], unread: 0 }, q = quietUntil(), aps = S.approvals || [];
  const kinds = (NOTICE_TABS.find(t => t[0] === noticeTab) || NOTICE_TABS[0])[2];
  const list = v.notices.filter(n => !kinds || kinds.includes(n.kind));
  const count = (k) => { const ks = NOTICE_TABS.find(t => t[0] === k)[2]; return v.notices.filter(n => !n.read && (!ks || ks.includes(n.kind))).length + (k === 'requests' || k === 'all' ? aps.length : 0); };
  const tabs = `<div class="ntabs" role="tablist">${NOTICE_TABS.map(([k, l]) => { const n = count(k);
    return `<button data-ntab="${k}" class="${noticeTab === k ? 'on' : ''}" role="tab" aria-selected="${noticeTab === k}">${l}${n ? `<span class="n">${n}</span>` : ''}</button>`; }).join('')}</div>`;
  const waiting = (!kinds || kinds.includes('agent')) && aps.length ? `<div class="label2">Waiting for you</div>${aps.map(a => `<div class="n-ap ${a.destructive ? 'risky' : ''}">
      ${a.destructive ? icon.shieldAlert : icon.agent}<span class="nt"><b>${esc(describe({ method: a.method, summary: a.summary }))}</b><span class="note">${esc(whoAsks(a))} · <code>${esc(toolName(a.method))}</code> · <span data-remain="${esc(a.expires)}">${fmtLeft(new Date(a.expires) - Date.now())}</span> left</span>
      <span class="n-acts">${approvalActions(a, true)}</span></span></div>`).join('')}` : '';
  let day = '', rows = '';
  for (const n of list.slice(0, 60)) {
    const d = noticeDay(n.time);
    if (d !== day) { day = d; rows += `<div class="label2">${esc(d)}</div>`; }
    const k = NOTICE_KINDS[n.kind] || ['', 'info'];
    rows += `<button class="notice ${n.read ? '' : 'unread'}" data-notice="${esc(n.id)}">${icon[k[1]] || icon.info}<span class="nt"><b>${esc(n.title)}</b>${n.text ? `<span class="note">${esc(n.text)}</span>` : ''}</span>
      <span class="kb">${new Date(n.time).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}</span></button>`;
  }
  const head = `<div class="notice-h"><b>Notifications</b><span class="spacer"></span>${v.unread ? '<button class="linkish" data-noticeall="read">Mark all as read</button>' : ''}${v.notices.length ? '<button class="linkish" data-noticeall="clear">Clear</button>' : ''}</div>`;
  const dnd = q ? `<div class="notice-dnd on">${icon.bellOff}<span>Do Not Disturb ${esc(quietLabel(q))}</span><span class="spacer"></span><button class="linkish" data-quiet="0">Turn off</button></div>`
    : `<div class="notice-dnd">${icon.moonStar}<span>Do Not Disturb</span><span class="spacer"></span><button class="linkish" data-quiet="60">1 hour</button><button class="linkish" data-quiet="tomorrow">Until tomorrow</button><button class="linkish" data-quiet="-1">Until I turn it off</button></div>`;
  const empty = { all: 'Nothing yet. Services that stop, finished installs, agent requests, shares about to end, updates and problems show up here.',
    requests: 'No agent requests. When an AI agent asks to do something risky, it waits here for you.', problems: 'No problems. Services that stop unexpectedly and crashes show up here.',
    updates: 'Nothing here yet. Finished installs, setup, shares ending and new versions show up here.' }[noticeTab];
  return head + tabs + dnd + `<div class="n-list">${waiting + rows || `<div class="menu-empty">${empty}</div>`}</div>`;
}
function showNoticeMenu(anchor) { menuAt(anchor, noticeMenuHTML(), 'wide notices'); }
// Minutes until 8:00 tomorrow
function minutesToMorning() { const t = new Date(); t.setDate(t.getDate() + 1); t.setHours(8, 0, 0, 0); return Math.max(1, Math.round((t - new Date()) / 60000)); }
// Opens what a notice is about ("view:settings/updates", "proj:shop", "approval:<id>", "log:apache")
function openNotice(open) {
  if (!open) return;
  const [what, arg = ''] = [open.slice(0, open.indexOf(':')), open.slice(open.indexOf(':') + 1)];
  if (what === 'view') { const [view, sec] = arg.split('/'); if (sec) S.setSec = sec; return navigate(view); }
  if (what === 'proj') return site(arg) ? navigate('projects', arg) : navigate('projects');
  if (what === 'approval') return navigate('overview');
  if (what === 'log') { if (['apache', 'mysql', 'nginx'].includes(arg)) S.log = { ...S.log, kind: arg, folder: '' }; return navigate('logs'); }
}
async function noticeClicked(id) {
  const n = (S.notices?.notices || []).find(x => x.id === id);
  $('#menu').classList.remove('open');
  if (n && !n.read) { n.read = true; S.notices.unread = Math.max(0, S.notices.unread - 1); bridge.call('notices.read', { id }).catch(() => {}); }
  renderStatus();
  if (n) openNotice(n.open);
}

function showActivityMenu(anchor) {
  const items = collapseActivity(S.activity).slice(0, 8);
  menuAt(anchor, items.length ? items.map(a => { const who = (CLIENTS[a.client] || CLIENTS.unknown)[1];
    return `<button class="act-row" data-menu="goto-overview" title="${esc(new Date(a.time).toLocaleString())}"><span class="act-time">${new Date(a.time).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}</span><span class="act-what">${esc(describe(a))}${a.n > 1 ? ` ×${a.n}` : ''}</span>${a.error ? '<span class="chip warn">failed</span>' : ''}<span class="act-who">${who}</span></button>`; }).join('')
    : '<button disabled>No recent changes</button>', 'fmenu');
}

async function command(cmd, opts = {}) {
  if (cmd.startsWith('goto-')) return navigate(cmd.slice(5));
  switch (cmd) {
    case 'filtermenu': return showFilterMenu(opts.anchor?.closest?.('.vtabs, .fchips') ? opts.anchor : $('[data-cmd="filtermenu"]'));
    case 'displaymenu': return menuAt(opts.anchor || $('[data-cmd="displaymenu"]'), displayMenuHTML(), 'fmenu');
    case 'clearpf': S.pf = {}; saveUI(); return render();
    case 'dview': if (!await guardEdits()) return; S.data.mode = 'view'; return renderView();
    case 'dedit': {
      const d = S.data, c = d.conns.find(x => x.id === d.conn);
      if (!pkCols().length) return toast('This table has no primary key, so rows can’t be identified safely. Use SQL instead.', 'warn');
      if (c.read_only) {
        if (!await confirmBox(`Allow changes to ${c.name}?`, 'Editing needs this connection to allow changes. In Edit mode nothing is written until you review the exact SQL and confirm. You can switch back to read-only at any time.', 'Allow changes & edit')) return;
        if (!await act(() => bridge.call('data.readonly', { id: c.id, read_only: false }))) return;
        d.conns = await act(() => bridge.call('data.connections')) || d.conns;
      }
      d.mode = 'edit'; return renderView();
    }
    case 'dinsert': { S.data.ed.ins.unshift({}); renderView(); const c = document.querySelector('tr.ins td[data-cell]'); if (c) editCell(c); return; }
    case 'ddiscard': S.data.ed = newEdits(); return renderView();
    case 'dreview': return reviewAndSave();
    case 'dexport': return showExportMenu(opts.anchor, S.data.result, S.data.table || 'rows');
    case 'dexportq': return showExportMenu(opts.anchor, S.data.qres, 'query');
    case 'dhistory': return showSQLHistory(opts.anchor);
    case 'takeover': {
      const s = site(opts.folder);
      if (!await confirmBox(`Serve ${s.name} with LocalSites?`, `Valet stops serving ${s.domain} (valet unlink / unpark for this folder only), and LocalSites switches it on at the same address with its own PHP. Other Valet sites keep working. Undo: switch it off here and run “valet link” in the folder.`, 'Take over')) return;
      const r = await act(() => bridge.call('sites.takeover', { folder: s.folder }), 'Take over');
      if (r) { S.sites = r.sites; logToast(r); render(); }
      return;
    }
    case 'envedit': case 'iniedit': return kvEditor(site(opts.folder), cmd === 'envedit');
    case 'palette': return openPalette();
    case 'keys': return showKeys();
    case 'helpmenu': return showHelpMenu(document.querySelector('.help'));
    case 'docs': return showDocs();
    case 'clihelp': return dialog(`<h3>Command-line tool</h3><p class="note">Run this once in Terminal to use <code>localsites</code> anywhere:</p>
      <pre class="mini">sudo ln -sf "${esc(S.exe)}" /usr/local/bin/localsites</pre><p class="note">Then try <code>localsites status</code>, <code>localsites sites</code> or <code>localsites help</code>. Add <code>--json</code> for scripts.</p>
      <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn" data-copy="sudo ln -sf &quot;${esc(S.exe)}&quot; /usr/local/bin/localsites">${icon.copy}Copy</button><button class="btn primary" data-dlg="ok">Done</button></div>`);
    case 'openproblems': S.setSec = 'problems'; return navigate('settings');
    case 'whatsnew': S.seenVersion = S.version?.version || ''; saveUI(); S.setSec = 'updates'; return navigate('settings');
    case 'clearfilter': S.filter = ''; S.pf = {}; S.ptab = 'all'; saveUI(); return render();
    case 'addfolder': S.setSec = 'folders'; return navigate('settings');
    case 'drun': return dataRunSQL();
    case 'dretry': return dataConnect();
    case 'dprev': if (!await guardEdits()) return; S.data.page = Math.max(0, S.data.page - 1); return dataLoadRows();
    case 'dnext': if (!await guardEdits()) return; S.data.page++; return dataLoadRows();
    case 'dsavepw': {
      const pw = $('#dPw')?.value || '';
      if (!pw) return;
      await act(() => bridge.call('data.password', { id: S.data.conn, password: pw }));
      S.data.conns = await act(() => bridge.call('data.connections')) || S.data.conns;
      return dataConnect();
    }
    case 'dwrite': {
      const c = S.data.conns.find(x => x.id === S.data.conn);
      if (!await confirmBox(`Allow changes to ${c.name}?`, 'Queries you run with “Allow this query to change data” will be able to insert, update and delete rows, or drop tables. Browsing stays read-only. You can switch back at any time.', 'Allow changes')) return;
      await act(() => bridge.call('data.readonly', { id: c.id, read_only: false }));
      S.data.conns = await act(() => bridge.call('data.connections')) || S.data.conns; return renderView();
    }
    case 'dreadonly': {
      if (!await guardEdits()) return;
      S.data.mode = 'view';
      await act(() => bridge.call('data.readonly', { id: S.data.conn, read_only: true }));
      S.data.allowWrite = false; S.data.conns = await act(() => bridge.call('data.connections')) || S.data.conns; return renderView();
    }
    case 'ddelconn': {
      const c = S.data.conns.find(x => x.id === S.data.conn);
      if (!await confirmBox(`Remove ${c.name}?`, 'Only the saved connection and its password are removed. The database itself is not touched.', 'Remove')) return;
      await act(() => bridge.call('data.connection.delete', { id: c.id }));
      S.data.conns = null; S.data.conn = ''; return dataInit();
    }
    case 'dnewconn': {
      const p = connectionDialog();
      const box = $('#keysDialog');
      const sync = () => { const dr = box.querySelector('#cDriver').value, ssh = box.querySelector('#cSSH').checked && dr !== 'sqlite';
        box.querySelectorAll('.net').forEach(l => l.hidden = dr === 'sqlite'); box.querySelector('.my').hidden = dr !== 'mysql' || ssh; box.querySelector('.lite').hidden = dr !== 'sqlite'; box.querySelector('.pg').hidden = dr !== 'postgres';
        box.querySelectorAll('.ssh').forEach(l => l.hidden = !ssh); };
      box.querySelector('#cDriver').onchange = sync; box.querySelector('#cSSH').onchange = sync; sync();
      box.querySelector('#cName').onkeydown = null; // Enter must not submit the half-filled form
      box.querySelector('#cName').focus();
      box.querySelector('#cSave').onclick = async () => {
        const v = (id) => box.querySelector(id).value.trim();
        const conn = { name: v('#cName'), driver: v('#cDriver'), host: v('#cHost'), port: Number(v('#cPort')) || 0, user: v('#cUser'), database: v('#cDb'), socket: v('#cSock'), file: v('#cFile'), read_only: box.querySelector('#cRO').checked };
        if (conn.driver === 'postgres' && v('#cSSL')) conn.ssl_mode = v('#cSSL');
        if (box.querySelector('#cSSH').checked && conn.driver !== 'sqlite') { conn.ssh_host = v('#cSSHHost'); conn.ssh_port = Number(v('#cSSHPort')) || 0; conn.socket = ''; }
        try {
          const saved = await bridge.call('data.connection.save', { connection: conn, password: box.querySelector('#cPass').value || null });
          box.querySelector('[data-dlg="cancel"]').click();
          S.data.conns = await bridge.call('data.connections'); S.data.conn = saved.id; dataConnect();
        } catch (e) { box.querySelector('#cErr').textContent = e.message; }
      };
      return p;
    }
    case 'rootadd': { const r = rootsDraft(); r.push({ label: '', path: '', layout: 'auto' }); setDraft('roots', r); renderView(); setTimeout(() => document.querySelector(`[data-root="${r.length - 1}"][data-rootf="path"]`)?.focus(), 20); return; }
    case 'setreload': S.settingsErr = null; S.settings = null; return loadSettingsSafe();
    case 'setrevert': S.setDraft = {}; S.setErrors = {}; return renderView();
    case 'setsave': {
      const patch = {}, ini = {};
      for (const [k, v] of Object.entries(S.setDraft)) {
        if (k.startsWith('php_ini.')) ini[k.slice(8)] = v;
        else if (/_port$/.test(k)) patch[k] = Number(v);
        else patch[k] = v;
      }
      if (Object.keys(ini).length) patch.php_ini = ini;
      const sent = { ...S.setDraft };
      S.setSaving = true; renderView();
      try {
        const r = await bridge.call('settings.update', patch);
        S.settings = r.settings; S.setErrors = {};
        // Only clear what was saved: edits typed while saving stay as unsaved changes
        for (const [k, v] of Object.entries(sent)) if (S.setDraft[k] === v) delete S.setDraft[k];
        logToast(r); toast('Settings saved');
        await act(() => Promise.all([loadStatus(), loadSites()]));
      } catch (e) {
        S.setErrors = {};
        for (const f of e.fields || []) S.setErrors[f.field] = f.message;
        if (!e.fields) toast(e.message, 'err');
      }
      S.setSaving = false; return render();
    }
    case 'activitymenu': return showActivityMenu($('#statusbar [data-cmd="activitymenu"]'));
    case 'noticemenu': await loadNotices(); renderStatus(); return showNoticeMenu($('#statusbar [data-cmd="noticemenu"]'));
    case 'passmenu': return menuAt($('#statusbar [data-cmd="passmenu"]'), passMenuHTML(), 'wide passes');
    case 'reconnect': try { await bridge.call('status'); offlineBack(); } catch (_) { S.offlineNext = Date.now() + 5000; renderOffline(); toast('Still not answering. If it stays this way, quit and open LocalSites again.', 'warn'); } return;
    case 'restart': { toast('Restarting services…', 'warn'); logToast(await act(() => bridge.call('restart'))); return refresh(); }
    case 'stop': {
      if (!await confirmBox('Stop all services?', 'Your sites go offline until you start them again.', 'Stop')) return;
      logToast(await act(() => bridge.call('stop'))); return refresh();
    }
    case 'closedetail': return navigate('projects', null);
    case 'togglesidebar': return toggleSidebar();
    case 'reloadlog': return loadLog();
    case 'logauto': S.logAuto = !S.logAuto; return renderHead();
    case 'newproject': {
      const name = await askText('New project', 'Folder name, e.g. shop');
      if (!name) return;
      const s = await act(() => bridge.call('sites.create', { folder: name }), 'New project');
      if (s) { await act(loadSites); toast(`Created ${s.folder}. Switch it on to open https://${s.domain}`); navigate('projects', s.folder); }
      return;
    }
    case 'pgnew': {
      const name = await askText('New PostgreSQL database', 'app_dev', 'Create'); if (!name) return;
      const r = await act(() => bridge.call('pg.create', { name }), 'Create'); if (r) { logToast(r); await loadDBs(); render(); } return;
    }
    case 'dexport': { const dd = D(), c = dd.conns.find(x => x.id === dd.conn); if (!c) return;
      return exportFlow(dd.schema || c.name, (dd.schema || c.name).replace(/[^\w.-]+/g, '_'), (o) => bridge.call('data.export', { id: c.id, schema: dd.schema || '', ...o })); }
    case 'dimport': { const dd = D(), c = dd.conns.find(x => x.id === dd.conn); if (!c) return;
      if (c.read_only) return toast(`${c.name} is read-only. Click “Read-only” to allow changes first, then import.`, 'warn');
      if (await importFlow(dd.schema || c.name, 'What is there now is exported to the backups folder first.', (file) => bridge.call('data.import', { id: c.id, schema: dd.schema || '', file }))) { await act(dataLoadTables); renderView(); }
      return; }
    case 'newdb': {
      const name = await askText('New database', 'Name (letters, digits, _)');
      if (!name) return;
      const r = await act(() => bridge.call('db.create', { name }), 'New database');
      if (r) { toast(`Created ${r.database} with its own login ${r.user}`); await loadDBs(); render(); }
      return;
    }
    case 'doctor': {
      if (S.doctorBusy) return;
      S.doctorBusy = true; S.doctorAt = Date.now(); if (cur().view === 'doctor') { renderHead(); if (S.doctor) renderView(); }
      S.doctor = await act(() => bridge.call('doctor')) || [];
      S.doctorBusy = false; S.doctorTook = Date.now() - S.doctorAt; S.doctorWhen = new Date().toISOString();
      const fails = S.doctor.filter(c => c.status === 'fail').length;
      if (!opts.quiet && cur().view !== 'doctor') toast(fails ? `Doctor found ${fails} problem(s)` : 'Doctor: all good', fails ? 'err' : 'ok');
      return render();
    }
    case 'bundle': { const r = await act(() => bridge.call('debug.bundle')); if (r) toast('Debug bundle saved (passwords removed):\n' + r.file); return; }
    case 'agentsend': return agentRun(S.agent.sel);
    case 'mysqlconnect': return mysqlConnectDialog('');
    case 'updcheck': { S.upd = { ...(S.upd || {}), phase: 'checking' }; renderView(); const r = await act(() => bridge.call('update.check'), 'Update check'); if (r) S.upd = r; else await act(loadUpdate); renderView(); renderSide(); if (r?.phase === 'idle') toast(r.message || 'You’re up to date.'); return; }
    case 'upddl': case 'updget': {
      S.updAfter = cmd === 'updget';
      const r = await act(() => bridge.call('update.download'), 'Download'); if (!r) { S.updAfter = false; return; }
      S.upd = r; renderView(); if (r.phase === 'ready' && S.updAfter) { S.updAfter = false; return installUpdate(); } pollUpdate(); return;
    }
    case 'updinstall': return installUpdate();
    case 'updlater': if (S.upd?.latest && await setUpdSetting('update_later', S.upd.latest.version)) { await act(loadUpdate); renderView(); renderSide(); toast(`LocalSites ${S.upd.latest.version} installs the next time you quit.`); } return;
    case 'updnow': S.updAfter = true; if (S.upd?.latest) await setUpdSetting('update_later', ''); await act(loadUpdate); renderView(); return;
    case 'updquit': S.updAfter = false; if (S.upd?.latest) await setUpdSetting('update_later', S.upd.latest.version); await act(loadUpdate); renderView(); return;
    case 'updrollback': {
      if (!S.upd?.kept) return;
      if (!await confirmBox(`Switch to LocalSites ${S.upd.kept}?`, `LocalSites quits and opens again as ${S.upd.kept}. Projects pause for a few seconds. ${S.upd.current} is kept, so you can switch back the same way.`, `Switch to ${S.upd.kept}`)) return;
      return installUpdate(true);
    }
    case 'updskip': if (S.upd?.latest) { await setUpdSetting('update_skip', S.upd.latest.version); await act(loadUpdate); renderView(); renderSide(); } return;
    case 'updunskip': await setUpdSetting('update_skip', ''); await act(loadUpdate); renderView(); renderSide(); return;
    case 'updoptin': if (await setUpdSetting('update_auto_check', true)) { { const a = $('#updAsk'); if (a) dropToast(a); } toast('LocalSites checks for updates once a day. Change this in Settings → Updates.'); } return;
    case 'updoptout': if (await setUpdSetting('update_asked', true)) { { const a = $('#updAsk'); if (a) dropToast(a); } toast('Automatic checks stay off. You can check any time in Settings → Updates.'); } return;
    case 'checkupdate': {
      const u = await act(() => bridge.call('version.check'), 'Update check'); if (!u) return;
      if (u.newer) { if (await confirmBox(`LocalSites ${u.latest} is available`, `You're on ${u.current}. Open the release page to download it?`, 'Open release page')) openWeb(u.url); }
      else toast(u.message, 'ok');
      return;
    }
    case 'rootpw': {
      const pw = await dialog(`<h3>Change the MySQL root password</h3><p class="note">Leave empty for a strong random password. At least 12 characters; no quotes, backslashes or spaces.</p>
        <input class="input" type="password" placeholder="New password (optional)" style="width:100%" autocomplete="new-password">
        <div class="form-row" style="justify-content:flex-end;margin-top:16px"><button class="btn ghost" data-dlg="cancel">Cancel</button><button class="btn primary" data-dlg="ok">Change</button></div>`);
      if (pw === null) return;
      const r = await act(() => bridge.call('mysql.root_password', { password: pw }), 'Root password');
      if (r) await dialog(`<h3>Root password changed</h3><p class="note">Saved in the keychain. Copy it now if you need it elsewhere:</p>${UI.code(r.password, '•'.repeat(r.password.length))}<div class="form-row" style="justify-content:flex-end;margin-top:12px"><button class="btn primary" data-dlg="ok">Done</button></div>`);
      return;
    }
    case 'adminer': {
      const a = S.status?.adminer || {};
      if (a.enabled) return openWeb(a.url);
      if (setAdminer.busy) return toast('Adminer is still being set up…');
      if (!await confirmBox('Set up Adminer?', `Adminer ${a.version || ''} (open source) will be downloaded from its GitHub release, checked against its known fingerprint, and served at ${a.url || 'https://adminer.test'}, only on this Mac. Sign in with a database's own login.`, 'Set up')) return;
      return setAdminer(true);
    }
    case 'sharegenpw': { const w = ['amber', 'basil', 'cedar', 'delta', 'ember', 'fjord', 'grove', 'harbor', 'iris', 'juniper', 'kelp', 'lumen', 'maple', 'nova', 'orbit', 'pine'];
      void w; SF.pw = newSharePassword(); $('#sharePw').value = SF.pw; return; }
    case 'addontestmail': return logToast(await act(() => bridge.call('addons.test_mail'), 'Test email'));
    case 'agentsetup': S.setSec = 'agents'; return navigate('settings');
    case 'setuprun': {
      const r = await act(() => bridge.call('setup.run', {}), 'Setup');
      if (r) { S.setup = r; toast('Installing and repairing in the background'); render(); }
      return;
    }
    case 'setuphomebrew': {
      const r = await act(() => bridge.call('setup.homebrew'), 'Homebrew');
      if (r) { await act(loadSetup); render(); }
      return;
    }
    case 'setuplog': S.log = { ...S.log, kind: 'setup', folder: '' }; return navigate('logs');
    case 'dnewconnview': navigate('data'); return command('dnewconn');
    case 'agentexamples': {
      const ex = ['switch primec to php 8.3', 'why is myproject broken', 'create database shop', 'new project demo', 'doctor'];
      S.agent.text = ex[(ex.indexOf(S.agent.text) + 1) % ex.length]; S.agent.sel = 0;
      return renderView();
    }
    case 'impconnect': {
      S.imp.err = ''; S.imp.dbs = null;
      try { S.imp.dbs = await bridge.call('import.databases', { source: S.imp.source, user: S.imp.user, password: S.imp.password }); } catch (e) { S.imp.err = e.message; }
      if (S.dbs === null) await loadDBs();
      return render();
    }
    case 'impcopy': {
      S.imp.running = true;
      for (const name of [...S.imp.chosen]) {
        S.imp.reports[name] = { running: true }; renderView();
        try { S.imp.reports[name] = await bridge.call('import.database', { source: S.imp.source, user: S.imp.user, password: S.imp.password, name }); }
        catch (e) { S.imp.reports[name] = { error: e.message }; }
        S.imp.chosen.delete(name); renderView();
      }
      S.imp.running = false; await loadDBs(); render(); return;
    }
  }
}

/* ---------------------------------------------------------------- keyboard */
let gPending = 0;
document.addEventListener('keydown', (e) => {
  const typing = e.target.matches('input, select, textarea');
  const mod = e.metaKey || e.ctrlKey, k = e.key.toLowerCase();
  if (mod && k === 'k') { e.preventDefault(); return openPalette(); }
  if ($('#overlay').classList.contains('open')) return paletteKey(e);
  if ($('#keysOverlay').classList.contains('open')) return;
  if (mod && e.key === '/') { e.preventDefault(); return showKeys(); }
  if (mod && k === 't') { e.preventDefault(); return navigate('overview', null, { newTab: true }); }
  if (mod && k === 'w') { e.preventDefault(); return closeTab(S.tab); }
  if (mod && e.key === '[') { e.preventDefault(); return goHistory(-1); }
  if (mod && e.key === ']') { e.preventDefault(); return goHistory(1); }
  if (mod && e.key === '\\') { e.preventDefault(); return toggleSidebar(); }
  if (mod && /^[1-9]$/.test(e.key) && S.tabs[+e.key - 1]) { e.preventDefault(); S.tab = +e.key - 1; onEnter(cur().view); saveUI(); return render(); }
  if (e.target.id === 'dSQL') {
    const ac = S.data.ac, box = $('#acBox');
    if (ac && box && !box.hidden) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); ac.sel = (ac.sel + (e.key === 'ArrowDown' ? 1 : ac.list.length - 1)) % ac.list.length; return acDraw(e.target); }
      if (e.key === 'Tab' || (e.key === 'Enter' && !mod)) { e.preventDefault(); return acAccept(e.target); }
      if (e.key === 'Escape') { e.preventDefault(); S.data.ac = null; box.hidden = true; return; }
    }
    if (mod && e.key === 'Enter') { e.preventDefault(); return dataRunSQL(); }
  }
  if ($('#menu').classList.contains('open') && e.key === 'Escape') { e.preventDefault(); $('#menu').classList.remove('open'); return; }
  if (e.target.id === 'fmQ' && e.key === 'Enter') { e.preventDefault(); return $('#menu .menu-list button')?.click(); }
  if (e.target.id === 'fmQ' && (e.key === 'ArrowDown')) { e.preventDefault(); return $('#menu .menu-list button')?.focus(); }
  if (e.target.id === 'dPw' && e.key === 'Enter') { e.preventDefault(); return command('dsavepw'); }
  if (e.target.id === 'agentInput') {
    const n = parseIntent(S.agent.text).length;
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); return agentRun(S.agent.sel); }
    if (e.key === 'ArrowDown' && n) { e.preventDefault(); S.agent.sel = Math.min(n - 1, S.agent.sel + 1); return renderView(); }
    if (e.key === 'ArrowUp' && n) { e.preventDefault(); S.agent.sel = Math.max(0, S.agent.sel - 1); return renderView(); }
    if (e.key === 'Escape') { S.agent.text = ''; return renderView(); }
    return;
  }
  if (e.target.id === 'logQ' && e.key === 'Escape' && LSV.q) { e.preventDefault(); LSV.q = ''; return lsRedraw(); }
  if (e.key === 'Escape' && S.sidePeek) { S.sidePeek = false; $('#body').classList.remove('peek'); return; }
  if (e.key === 'Escape') { $('#menu').classList.remove('open'); if (typing) e.target.blur(); else if (cur().sel) navigate('projects', null); return; }
  if (typing) { if (e.key === 'Enter' && e.target.dataset.field) e.target.blur(); return; }
  if (mod || e.altKey) return;
  if (e.key === '?') return showKeys();
  if (gPending && Date.now() - gPending < 1200) {
    gPending = 0;
    const v = Object.entries(VIEWS).find(([, d]) => d.key === k);
    if (v) { e.preventDefault(); navigate(v[0]); }
    return;
  }
  if (k === 'g') { gPending = Date.now(); return; }
  if (k === '/' && cur().view === 'projects') { e.preventDefault(); $('#filter')?.focus(); return; }
  if (k === 'f' && cur().view === 'projects') { e.preventDefault(); return command('filtermenu'); }
  if (cur().view !== 'projects') return;
  const list = visibleSites();
  const i = list.findIndex(s => s.folder === cur().sel);
  if (k === 'j' || e.key === 'ArrowDown') { e.preventDefault(); selectRow(list[Math.min(list.length - 1, i + 1)]?.folder); }
  else if (k === 'k' || e.key === 'ArrowUp') { e.preventDefault(); selectRow(list[Math.max(0, i - 1)]?.folder); }
  else if (cur().sel && k === 'e' && site(cur().sel).runtime !== 'none' && !site(cur().sel).served_by) updateSite(cur().sel, { enabled: !site(cur().sel).enabled });
  else if (cur().sel && k === 'o') act(() => bridge.call('open', { folder: cur().sel, app: 'browser' }));
});
function selectRow(folder) { if (!folder) return; cur().sel = folder; saveUI(); render(); document.querySelector('.row.sel')?.scrollIntoView({ block: 'nearest' }); }

/* ---------------------------------------------------------- command palette */
let pItems = [], pSel = 0;
function commands() {
  const c = [];
  for (const [k, v] of Object.entries(VIEWS)) c.push({ sect: 'Go to', label: `Go to ${v.title}`, icon: v.icon, hint: `G ${v.key.toUpperCase()}`, run: () => navigate(k) });
  c.push({ sect: 'Projects', label: 'New project…', icon: 'compose', run: () => command('newproject') });
  for (const s of S.sites) {
    c.push({ sect: 'Projects', label: s.enabled ? `Switch off ${s.folder}` : `Switch on ${s.folder}`, icon: 'power', run: () => updateSite(s.folder, { enabled: !s.enabled }) });
    if (s.enabled) c.push({ sect: 'Projects', label: `Open ${s.folder} in browser`, icon: 'globe', hint: s.domain, run: () => bridge.call('open', { folder: s.folder, app: 'browser' }) });
    c.push({ sect: 'Projects', label: `Open ${s.folder} in ${prefApp('editor')?.name || 'your code editor'}`, icon: 'code', run: () => openWith('editor', s.folder) });
    c.push({ sect: 'Projects', label: `Show ${s.folder}`, icon: 'site', dot: s.runtime === 'none' ? '' : s.enabled || s.served_by ? 'on' : 'off', hint: s.enabled ? s.domain : typeOf(s).label, run: () => navigate('projects', s.folder) });
    c.push({ sect: 'Projects', label: `Open ${s.folder} in a new tab`, icon: 'plus', run: () => navigate('projects', s.folder, { newTab: true }) });
    for (const p of S.status?.php || []) if (p.version !== s.php) c.push({ sect: 'PHP', label: `${s.folder} → PHP ${p.version}`, icon: 'php', run: () => updateSite(s.folder, { php: p.version }) });
    const live = (S.status?.shares || []).find(x => x.folder === s.folder);
    if (live) c.push({ sect: 'Share', label: `Stop sharing ${s.folder}`, icon: 'stop', hint: live.url.replace(/^https?:\/\//, ''), run: () => bridge.call('share.stop', { id: live.id }).then(logToast).then(refresh) });
    else if (s.enabled && s.runtime !== 'none') c.push({ sect: 'Share', label: `Share ${s.folder} on the internet…`, icon: 'globe', run: () => { S.dpane = 'details'; navigate('projects', s.folder); setTimeout(() => $('#detail .share-form, #detail .tunnels')?.scrollIntoView({ block: 'center', behavior: 'smooth' }), 60); } });
    if (s.database) c.push({ sect: 'Databases', label: `Open ${s.folder}’s database (${s.database})`, icon: 'table', run: () => openDatabase('localsites', s.database) });
    if (s.enabled) c.push({ sect: 'Debug', label: `Check connection: ${s.folder}`, icon: 'doctor', run: () => { navigate('projects', s.folder); return runCheck(s.folder); } });
  }
  c.push({ sect: 'Debug', label: 'Run Doctor (health check)', icon: 'doctor', run: () => { navigate('doctor'); return command('doctor'); } });
  c.push({ sect: 'Debug', label: 'Create debug bundle', icon: 'bug', run: () => command('bundle') });
  c.push({ sect: 'Debug', label: 'Copy MCP setup for Claude Code', icon: 'agent', run: async () => { await bridge.copy(`claude mcp add localsites -- "${S.exe}" mcp`); toast('Copied'); } });
  c.push({ sect: 'Services', label: 'Restart all services', icon: 'refresh', run: () => command('restart') });
  c.push({ sect: 'Services', label: 'Stop all services', icon: 'stop', run: () => command('stop') });
  c.push({ sect: 'Databases', label: 'Create database…', icon: 'db', run: () => { navigate('databases'); return command('newdb'); } });
  for (const d of S.dbs || []) c.push({ sect: 'Databases', label: `Open database ${d.name}`, icon: 'table', hint: plural(d.tables, 'table'), run: () => openDatabase('localsites', d.name) });
  c.push({ sect: 'Databases', label: 'Copy databases from MAMP PRO', icon: 'import', run: () => navigate('import') });
  c.push({ sect: 'Data', label: 'Browse data (tables, rows, SQL)', icon: 'table', hint: 'G T', run: () => navigate('data') });
  c.push({ sect: 'Data', label: 'New database connection…', icon: 'plus', run: () => { navigate('data'); return command('dnewconn'); } });
  for (const t of S.data.tables.slice(0, 200)) c.push({ sect: 'Data', label: `Open table ${t.name}`, icon: 'table', run: () => { navigate('data'); return dataOpenTable(t.name); } });
  c.push({ sect: 'Window', label: 'New tab', icon: 'plus', hint: MOD + 'T', run: () => navigate('overview', null, { newTab: true }) });
  c.push({ sect: 'Window', label: 'Toggle sidebar', icon: 'sidebar', hint: MOD + '\\', run: () => toggleSidebar() });
  c.push({ sect: 'Window', label: 'Keyboard shortcuts', icon: 'info', hint: '?', run: () => showKeys() });
  return c;
}
function score(label, q) {
  if (!q) return 1;
  const l = label.toLowerCase(); let i = 0, s = 0, last = -2;
  for (const ch of q) { if (ch === ' ') continue; const j = l.indexOf(ch, i); if (j < 0) return 0; s += j === last + 1 ? 3 : 1; if (j === 0 || l[j - 1] === ' ') s += 2; last = j; i = j + 1; }
  return s - l.length * 0.01;
}
// Launcher: recent commands first, then everything grouped; typing searches every command and project.
const recentCmds = () => store.get('recentCmds', []);
function rememberCmd(label) { store.set('recentCmds', [label, ...recentCmds().filter(l => l !== label)].slice(0, 5)); }
function openPalette() { $('#overlay').classList.add('open'); $('#pInput').value = ''; pSel = 0; paletteRender(); $('#pInput').focus(); }
function closePalette() { $('#overlay').classList.remove('open'); }
function runPaletteItem(c) { closePalette(); if (!c) return; rememberCmd(c.label); act(() => c.run()); }
function paletteRender() {
  const q = $('#pInput').value.trim().toLowerCase(), all = commands();
  let list;
  if (q) {
    const hits = all.map(c => ({ ...c, s: score(c.label, q) })).filter(c => c.s > 0).sort((a, b) => b.s - a.s).slice(0, 60);
    const order = uniq(hits.map(c => c.sect)); // groups in the order of their best match
    list = order.flatMap(g => hits.filter(c => c.sect === g));
  } else {
    const rec = recentCmds().map(l => all.find(c => c.label === l)).filter(Boolean).map(c => ({ ...c, sect: 'Recent' }));
    list = [...rec, ...all.filter(c => c.sect !== 'PHP' && !rec.some(r => r.label === c.label)).slice(0, 40)];
  }
  pItems = list;
  pSel = Math.min(pSel, Math.max(0, pItems.length - 1));
  let last = '';
  $('#pList').innerHTML = pItems.map((c, i) => {
    const head = c.sect !== last ? `<div class="sect2">${esc(c.sect)}</div>` : ''; last = c.sect;
    return `${head}<div class="item ${i === pSel ? 'sel' : ''}" data-pi="${i}">${c.dot ? `<span class="dot ${c.dot}"></span>` : icon[c.icon] || ''}<span>${esc(c.label)}</span><span class="hint">${esc(c.hint || '')}</span></div>`;
  }).join('') || `<div class="p-none">${icon.search2}<span>Nothing matches “${esc(q)}”. Try a project name, a view or an action like “restart”.</span></div>`;
  $('#pCount').textContent = q ? plural(pItems.length, 'result') : '';
  $('#pList .item.sel')?.scrollIntoView({ block: 'nearest' });
}
function paletteKey(e) {
  if (e.key === 'Escape') { e.preventDefault(); closePalette(); }
  else if (e.key === 'ArrowDown') { e.preventDefault(); pSel = Math.min(pItems.length - 1, pSel + 1); paletteRender(); }
  else if (e.key === 'ArrowUp') { e.preventDefault(); pSel = Math.max(0, pSel - 1); paletteRender(); }
  else if (e.key === 'Enter') { e.preventDefault(); runPaletteItem(pItems[pSel]); }
}
$('#pInput').addEventListener('input', () => { pSel = 0; paletteRender(); });
$('#pList').addEventListener('click', (e) => { const it = e.target.closest('[data-pi]'); if (it) runPaletteItem(pItems[+it.dataset.pi]); });
$('#overlay').addEventListener('mousedown', (e) => { if (e.target.id === 'overlay') closePalette(); });

/* -------------------------------------------------------------------- boot */
// Deep links: #view or #projects/<folder> (used by tests and links), on load and when the hash changes
function applyHash() {
  const [v, f] = decodeURIComponent(location.hash.slice(1)).split('/');
  if (!VIEWS[v]) return false;
  cur().view = v; cur().sel = v === 'projects' && f ? f : null;
  return true;
}
document.documentElement.classList.toggle('mac-app', isMac && inApp);
// Responsive breakpoints per region (panel, list, project area). CSS container queries re-check every rule on
// every frame of a window resize, which made zooming lag; this sets a class only when a breakpoint is crossed.
const SIZE_BREAKPOINTS = { panel: [600, 760, 820], view: [520, 560, 720], pm: [880] };
const sizeObserver = new ResizeObserver((entries) => {
  for (const en of entries) {
    const name = en.target.dataset.size, w = en.contentRect.width;
    for (const bp of SIZE_BREAKPOINTS[name]) {
      const cls = `lt-${name}-${bp}`, want = w <= bp;
      if (en.target.classList.contains(cls) !== want) { en.target.classList.toggle(cls, want); if (name === 'pm') queueMicrotask(renderDetail); }
    }
  }
});
for (const [sel, name] of [['.panel', 'panel'], ['#view', 'view'], ['.pm', 'pm']]) { const el = $(sel); el.dataset.size = name; sizeObserver.observe(el); }
// Native window behaviour. Drag starts only once the mouse moves, so clicks and double-clicks on drag areas
// still work; double-clicking any drag area zooms (or minimizes, per the macOS setting), like a title bar.
if (window.wails?.flags) window.wails.flags.deferDragToMouseMove = true;
// macOS: report drag areas to the native side so a mouse-down there starts a real window drag (tiling and
// snapping work like a title bar). Controls inside drag areas, menus, dialogs and toasts are never draggable.
const DRAG_AREAS = '#topbar, .switcher, .ph, .statusbar, .side-foot, .side-scroll';
const NO_DRAG = 'button, a, input, select, textarea, label, .tab, .sect, .item, [data-sect], [data-twist], [data-tab], [data-view], [data-proj], [data-scope], [data-cmd], .search';
let lastRegions = '', regionsQueued = false;
function syncDragRegions() {
  if (!window.go?.main?.App?.SetDragRegions || regionsQueued || document.documentElement.classList.contains('resizing')) return;
  regionsQueued = true;
  requestAnimationFrame(() => {
    regionsQueued = false;
    const rect = (el) => { const r = el.getBoundingClientRect(); return [r.left, r.top, r.width, r.height].map(v => Math.round(v * 10) / 10); };
    const visible = (el) => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0; };
    // While a dialog, the palette or the slide-over sidebar is open, clicks must reach the page (to close it)
    const blocked = $('#overlay').classList.contains('open') || $('#keysOverlay').classList.contains('open') || S.sidePeek;
    const drag = blocked ? [] : [...document.querySelectorAll(DRAG_AREAS)].filter(visible).map(rect);
    const no = [];
    if (!blocked) {
      for (const area of document.querySelectorAll(DRAG_AREAS)) for (const el of area.querySelectorAll(NO_DRAG)) if (visible(el)) no.push(rect(el));
      if ($('#menu').classList.contains('open')) no.push(rect($('#menu')));
      for (const t of document.querySelectorAll('.toast, .approval')) no.push(rect(t));
      if (S.sidePeek) no.push(rect($('#side')));
    }
    const key = JSON.stringify([drag, no]);
    if (key === lastRegions) return;
    lastRegions = key;
    try { window.go.main.App.SetDragRegions(drag, no); } catch (_) { /* older app build */ }
  });
}
new MutationObserver(syncDragRegions).observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['class'] });
addEventListener('resize', syncDragRegions, { passive: true });
document.addEventListener('scroll', syncDragRegions, { capture: true, passive: true });
document.addEventListener('transitionend', syncDragRegions);
document.addEventListener('dblclick', (e) => {
  if (e.target.closest('button, input, select, textarea, a, [data-tab], [data-row], [data-cell]')) return;
  if (getComputedStyle(e.target).getPropertyValue('--wails-draggable').trim() !== 'drag') return;
  window.go?.main?.App?.TitleBarDoubleClick?.();
});
try { window.runtime?.EventsOn?.('menu', (cmd) => command(cmd)); } catch (_) { /* not in the desktop app */ }
// A macOS notification (or its Open logs button) was clicked: open what it's about
try {
  window.runtime?.EventsOn?.('notice', async (kind, ref, action) => {
    if (action === 'logs') return openNotice('log:' + ref);
    await loadNotices();
    const n = (S.notices?.notices || []).find(x => (ref && x.ref === ref) || (!ref && x.kind === kind));
    if (n) return noticeClicked(n.id);
    openNotice({ agent: 'approval:', share: 'view:projects', update: 'view:settings/updates', problem: 'view:settings/problems', setup: 'view:overview' }[kind] || '');
  });
} catch (_) { /* not in the desktop app */ }
// Hidden for a few minutes, the app asks the page to unload itself to free memory; it reloads on the same
// tabs when the window is shown. Never while something is unsaved or in progress.
function unsavedWork() {
  const ed = S.data?.ed;
  return !!((ed && (Object.keys(ed.upd).length || ed.del.size || ed.ins.length)) || Object.keys(S.setDraft || {}).length || S.setSaving
    || $('#overlay').classList.contains('open') || $('#keysOverlay').classList.contains('open') || S.agent.busy || S.data?.busy || S.imp?.running
    || ['downloading', 'installing'].includes(S.upd?.phase));
}
try {
  window.runtime?.EventsOn?.('lifecycle', async (what) => {
    if (what !== 'suspend' || unsavedWork()) return;
    saveUI();
    store.set('resume', { setSec: S.setSec, sql: S.data?.sql || '', agent: S.agent.text || '', at: Date.now() });
    if (await window.go.main.App.PageSuspended().catch(() => false)) location.replace('wails://suspended/suspended.html');
    else store.set('resume', null);
  });
} catch (_) { /* not in the desktop app */ }
applyHash();
window.addEventListener('hashchange', () => { if (applyHash()) { onEnter(cur().view); saveUI(); render(); } });
applyTheme();
render();
onEnter(cur().view);
refresh();
act(loadVersion).then(() => {
  renderSide();
  // Right after an update the app starts on Overview (other tabs stay) and says what changed. Before 1.7.1 there
  // was no lastVersion, so the version last seen in What's new stands in for it once.
  const v = S.version?.version, last = store.get('lastVersion', '') || S.seenVersion;
  if (v && last && last !== v) {
    if (!location.hash && cur().view !== 'overview') { cur().view = 'overview'; cur().sel = null; saveUI(); onEnter('overview'); render(); }
    toast(`Updated to LocalSites ${v}`, 'ok', { label: "What's new", attrs: 'data-menu="whatsnew"' });
  }
  if (v) store.set('lastVersion', v);
});
{ // back from a suspended page: the same settings section, SQL and agent text
  const r = store.get('resume', null);
  if (r && Date.now() - (r.at || 0) < 7 * 864e5) { if (r.setSec) S.setSec = r.setSec; if (r.sql) S.data.sql = r.sql; if (r.agent) S.agent.text = r.agent; }
  store.set('resume', null);
}
loadApps().then(render);
pollApprovals();
act(async () => { await loadUpdate(); pollUpdate(); renderSide(); promptUpdateReady(); });
// Once, in the desktop app: ask whether to check for updates automatically (off until the person says yes)
setTimeout(() => {
  if (!window.go?.main?.App || !S.settings || S.settings.update_asked || $('#updAsk')) return;
  const t = document.createElement('div');
  t.className = 'toast ask'; t.id = 'updAsk'; t.setAttribute('role', 'dialog'); t.setAttribute('aria-label', 'Automatic update checks');
  t.innerHTML = `<span class="c-ic">${icon.sparkles}</span><div><div class="label">Check for updates automatically?</div>
    <div class="note">Once a day LocalSites reads the public list of releases. Nothing about you or your projects is sent.</div>
    <div class="form-row" style="margin-top:8px">${UI.btn('Turn on', { kind: 'primary', attrs: 'data-cmd="updoptin"' })}${UI.btn('Not now', { kind: 'ghost', attrs: 'data-cmd="updoptout"' })}${UI.btn('Settings', { kind: 'ghost', attrs: 'data-menu="whatsnew"' })}</div></div>`;
  $('#toasts').append(t); layoutToasts();
}, 8000);
window.addEventListener('focus', () => pollApprovals());
act(async () => { S.exe = window.go?.main?.App?.Executable ? await window.go.main.App.Executable() : '/path/to/localsites'; render(); });
// Background refresh. The service holds an events.wait request open until something changes, so the page
// refreshes right after a change and otherwise sleeps. A slow fallback (60 s focused, 5 min behind other
// windows) picks up project folders added on disk. Nothing runs while the window is hidden. An older
// service without events.wait gets the previous 5 s / 30 s polling.
let lastPoll = 0, lastSnap = '', stale = false, evVer = 0, watching = false, legacy = false;
const busyUI = () => $('#overlay').classList.contains('open') || $('#keysOverlay').classList.contains('open');
async function refreshAll() {
  if (busyUI()) { stale = true; return; } // a dialog is open: refresh when it closes
  stale = false; lastPoll = Date.now();
  if (document.activeElement?.matches('input,select,textarea') || $('#menu').classList.contains('open')) { await act(() => Promise.all([loadStatus(), loadActivity()])); renderStatus(); stale = true; return; }
  await act(() => Promise.all([loadStatus(), loadSites(), loadActivity(), loadProblems(), loadSetup(), loadNotices()]));
  const snap = JSON.stringify([S.status, S.sites, S.activity[0]?.time, S.prob?.problems?.length, probNew(), S.setup]);
  if (snap !== lastSnap) { lastSnap = snap; render(); } else renderStatus();
  if (legacy) pollApprovals();
}
/* --- Connection Lost: the background service stopped answering; say so and keep trying --- */
let misses = 0;
function offlineMiss() {
  if (++misses < 2) return; // one miss is a restart in progress
  S.offline ||= Date.now(); S.offlineNext = Date.now() + 5000; renderOffline();
}
function offlineBack() {
  if (!misses && !S.offline) return;
  const was = S.offline; misses = 0; S.offline = 0; renderOffline();
  if (was) { toast('Connected to the background service again'); refreshAll(); pollApprovals(); }
}
function renderOffline() {
  let el = $('#offline');
  if (!S.offline) { el?.remove(); return; }
  if (!el) { el = document.createElement('div'); el.id = 'offline'; el.className = 'offline'; el.setAttribute('role', 'status'); $('.panel').prepend(el); }
  el.innerHTML = `${icon.wifiOff}<div><b>LocalSites’ background service isn’t answering.</b> <span class="note">Your sites may be unreachable too. Not answering for <span data-elapsed="${S.offline}">${fmtClock(Date.now() - S.offline)}</span>; trying again in <span data-remain="${new Date(S.offlineNext).toISOString()}">5s</span>.</span></div>
    <span class="spacer"></span><button class="btn sm" data-cmd="reconnect">${icon.refresh}Try now</button>`;
}
async function watchChanges() {
  if (watching || legacy) return;
  watching = true;
  try {
    while (!document.hidden && !unloading) {
      let v;
      try { v = (await bridge.call('events.wait', { since: evVer })).version; }
      catch (e) {
        if (/unknown method/i.test(String(e?.message || e))) { legacy = true; break; }
        offlineMiss(); await new Promise(r => setTimeout(r, 5000)); continue; // service restarting
      }
      offlineBack();
      if (v === evVer || document.hidden) continue;
      evVer = v;
      await new Promise(r => setTimeout(r, 150)); // changes come in bursts: refresh once
      await refreshAll(); pollApprovals();
    }
  } finally { watching = false; }
}
setInterval(() => {
  if (document.hidden) return;
  if (!legacy && !watching) watchChanges();
  const every = legacy ? (document.hasFocus() ? 5000 : 30000) : (document.hasFocus() ? 60000 : 300000);
  if ((stale && !busyUI() && !document.activeElement?.matches('input,select,textarea') && !$('#menu').classList.contains('open')) || Date.now() - lastPoll >= every - 100) refreshAll();
}, 1000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) { lastPoll = 0; watchChanges(); } });
window.addEventListener('focus', () => { lastPoll = 0; act(async () => { const was = updPending(); await loadUpdate(); pollUpdate(); if (was !== updPending()) renderSide(); promptUpdateReady(); }); });
// While the window is being resized (zoom, full screen, dragging an edge) the native animation drives the size:
// switch CSS transitions off so the layout follows it frame by frame instead of easing behind it.
let resizeT = 0;
addEventListener('resize', () => {
  const root = document.documentElement;
  if (!root.classList.contains('resizing')) root.classList.add('resizing');
  clearTimeout(resizeT); resizeT = setTimeout(() => { root.classList.remove('resizing'); syncDragRegions(); }, 160);
  if (!narrowWin() && S.sidePeek) { S.sidePeek = false; $('#body').classList.remove('peek'); }
}, { passive: true });
setInterval(() => { if (!document.hidden && cur().view === 'logs' && S.logAuto) loadLog(false); }, 2000);
setInterval(() => { if (!document.hidden && cur().view === 'overview') loadUptime(); }, 5 * 60000); // samples arrive once a minute

// Read-only hook for automated UI tests (does not touch window.go / window.runtime).
Object.defineProperty(window, '__localsites', { value: Object.freeze({ state: () => S, commands: () => commands().map(c => c.label) }) });
})();
