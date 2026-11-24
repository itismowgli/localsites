// Live demos on the feature pages: small, working versions of what the app shows, filled with sample data.
// Each [data-demo="name"] element is built when it scrolls into view. Nothing here talks to a server.
(() => {
'use strict';
const esc = (s) => String(s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
const pad = (n, w = 2) => String(n).padStart(w, '0');
const svg = (p, vb = '0 0 24 24') => `<svg viewBox="${vb}" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${p}</svg>`;
const I = {
  shield: svg('<path d="M20 13c0 5-3.5 7.5-7.7 9a1 1 0 0 1-.6 0C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.2-2.7a1.2 1.2 0 0 1 1.6 0C14.5 3.8 17 5 19 5a1 1 0 0 1 1 1z"/><path d="M12 8v4M12 16h.01"/>'),
  timer: svg('<path d="M10 2h4M12 14l3-3"/><circle cx="12" cy="14" r="8"/>'),
  ticket: svg('<path d="M2 9a3 3 0 0 1 0 6v2a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-2a3 3 0 0 1 0-6V7a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2Z"/><path d="M13 5v2M13 17v2M13 11v2"/>'),
  check: svg('<path d="M20 6 9 17l-5-5"/>'),
  x: svg('<path d="M18 6 6 18M6 6l12 12"/>'),
  alert: svg('<circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/>'),
  ok: svg('<circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/>'),
  caret: svg('<path d="m6 9 6 6 6-6"/>'),
  pause: svg('<rect x="14" y="4" width="4" height="16" rx="1"/><rect x="6" y="4" width="4" height="16" rx="1"/>'),
  play: svg('<path d="M6 3 20 12 6 21Z"/>'),
  search: svg('<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>'),
  branch: svg('<path d="M6 3v12"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>'),
  merge: svg('<circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M6 21V9a9 9 0 0 0 9 9"/>'),
  tag: svg('<path d="M12.6 2.6A2 2 0 0 0 11.2 2H4a2 2 0 0 0-2 2v7.2a2 2 0 0 0 .6 1.4l8.7 8.7a2.4 2.4 0 0 0 3.4 0l6.6-6.6a2.4 2.4 0 0 0 0-3.4z"/><circle cx="7.5" cy="7.5" r=".5"/>'),
  down: svg('<path d="M12 5v14M19 12l-7 7-7-7"/>'),
};

/* --- Terminal: a replayed session, typed out line by line --- */
function terminal(el) {
  const lines = JSON.parse(el.dataset.lines);
  el.classList.add('dm-term'); el.innerHTML = '<div class="dm-term-bar"><i></i><i></i><i></i></div><pre></pre>';
  const pre = el.querySelector('pre');
  let i = 0;
  const next = () => {
    if (i >= lines.length) { setTimeout(() => { pre.innerHTML = ''; i = 0; next(); }, 4000); return; }
    const l = lines[i++];
    const row = document.createElement('div');
    if (l.startsWith('$ ')) {
      row.innerHTML = '<span class="p">$</span> <span class="c"></span><span class="cur"></span>'; pre.append(row);
      const c = row.querySelector('.c'), text = l.slice(2); let k = 0;
      const type = () => { c.textContent = text.slice(0, ++k); if (k < text.length) setTimeout(type, reduce ? 0 : 28); else { row.querySelector('.cur').remove(); setTimeout(next, 380); } };
      type();
    } else { row.innerHTML = l.replace(/^✓/, '<span class="ok">✓</span>').replace(/^#(.*)$/, '<span class="dim">#$1</span>'); pre.append(row); setTimeout(next, 140); }
  };
  next();
}

/* --- Approval card: the exact request, what's at stake, and a held press for what can't be undone --- */
function approve(el) {
  let left = 90, timer;
  const draw = () => {
    el.innerHTML = `<div class="dm-ap"><div class="dm-ap-h">${I.shield}<span><b>Claude Code</b> wants to</span><span class="dm-sp"></span><span class="dm-left">${I.timer}<span>${left}s</span></span></div>
      <div class="dm-ap-what">Delete database shop_staging</div>
      <div class="dm-ap-risk"><span class="st warn">Backed up first</span><span class="tg">Asks every time</span></div>
      <div class="dm-ap-cmd"><div class="dm-ap-cmd-h"><code>db_drop</code><span>the exact request</span></div><div class="kv"><span>name</span><code>shop_staging</code></div></div>
      <div class="dm-ap-acts"><button class="dbtn" data-act="deny">Deny</button><button class="dbtn primary hold"><span class="hold-fill"></span><span class="hold-t">Hold to approve</span></button></div>
      <div class="dm-ap-bar"><i style="transform:scaleX(${left / 90})"></i></div></div>`;
    const hold = el.querySelector('.hold');
    let t;
    const start = (e) => { e.preventDefault(); hold.classList.add('holding'); t = setTimeout(() => done('Approved. A backup was made, then the database was deleted.'), 900); };
    const stop = () => { hold.classList.remove('holding'); clearTimeout(t); };
    hold.addEventListener('pointerdown', start); hold.addEventListener('pointerup', stop); hold.addEventListener('pointerleave', stop);
    hold.addEventListener('keydown', (e) => { if ((e.key === 'Enter' || e.key === ' ') && !e.repeat) start(e); });
    hold.addEventListener('keyup', stop);
    el.querySelector('[data-act="deny"]').onclick = () => done('Denied. Nothing was done, and the agent was told not to retry.');
  };
  const done = (msg) => { clearInterval(timer); el.innerHTML = `<div class="dm-ap dm-done">${I.ok}<span>${msg}</span><button class="dbtn" data-act="again">Show again</button></div>`; el.querySelector('[data-act="again"]').onclick = () => { left = 90; draw(); tick(); }; };
  const tick = () => { clearInterval(timer); timer = setInterval(() => { left = left <= 1 ? 90 : left - 1; const s = el.querySelector('.dm-left span'), b = el.querySelector('.dm-ap-bar i'); if (s) s.textContent = left + 's'; if (b) b.style.transform = `scaleX(${left / 90})`; }, 1000); };
  draw(); tick();
}

/* --- Access pass: an "approve for 10 minutes" that counts down and can be ended early --- */
function pass(el) {
  const total = 600; let left = 437;
  const draw = () => { el.innerHTML = `<div class="dm-pass"><span class="dm-pass-l">${I.ticket}</span><div class="dm-pass-m"><div><b>Cursor</b> may <code>sites_update</code> on <b>storefront</b></div>
    <div class="dm-note">Change it without asking · <span class="dm-rem">${Math.floor(left / 60)} min</span> left</div><div class="dm-pass-bar"><i style="transform:scaleX(${left / total})"></i></div></div>
    <div class="dm-pass-r"><button class="dbtn sm">End now</button></div></div>`;
    el.querySelector('button').onclick = () => { el.innerHTML = `<div class="dm-pass dm-done">${I.ok}<span>Ended. The next request asks again.</span><button class="dbtn sm">Undo demo</button></div>`; el.querySelector('button').onclick = () => { left = 437; draw(); }; }; };
  draw();
  setInterval(() => { left = left > 1 ? left - 1 : total; const r = el.querySelector('.dm-rem'), b = el.querySelector('.dm-pass-bar i'); if (r) r.textContent = Math.max(1, Math.ceil(left / 60)) + ' min'; if (b) b.style.transform = `scaleX(${left / total})`; }, 1000);
}

/* --- Run list: every agent session, with status tabs, a live clock and its last steps --- */
function runlist(el) {
  const runs = [
    { t: 'Switched shop to PHP 8.3 and 1 more', who: 'Claude Code', on: 'shop', st: 'running', secs: 74, steps: ['Read the project context', 'Changed shop to PHP 8.3', 'Running a connection check'] },
    { t: 'Delete database shop_staging', who: 'Cursor', on: 'shop_staging', st: 'review', secs: 12, steps: ['Listed databases', 'Made a backup', 'Waiting for your approval'] },
    { t: 'Shared storefront on the internet', who: 'Claude Code', on: 'storefront', st: 'done', secs: 41, steps: ['Checked storefront is running', 'Started a share with a password', 'Copied the link'] },
    { t: 'Looked around: 6 calls', who: 'Devin', on: 'blog', st: 'failed', secs: 9, steps: ['Listed projects', 'Read the error log', 'Check failed: blog is switched off'] },
  ];
  const meta = { running: ['Running', 'info'], review: ['Needs review', 'warn'], failed: ['Failed', 'err'], done: ['Done', 'ok'] };
  const ic = { running: '<svg class="dm-spin" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="2" opacity=".2"/><path d="M8 2a6 6 0 0 1 6 6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>', review: I.alert, failed: I.x, done: I.ok };
  let filter = 'all', open = 0;
  const clock = (s) => `${Math.floor(s / 60)}:${pad(s % 60)}`;
  const draw = () => {
    const c = { all: runs.length }; runs.forEach(r => c[r.st] = (c[r.st] || 0) + 1);
    const shown = runs.map((r, i) => ({ r, i })).filter(x => filter === 'all' || x.r.st === filter);
    el.innerHTML = `<div class="dm-rl"><div class="dm-rl-h"><b>AI agents</b><span><i class="dm-dot"></i>${c.running || 0} running · ${c.review || 0} waiting on you</span></div>
      <div class="dm-tabs">${[['all', 'All'], ['running', 'Running'], ['review', 'Review'], ['failed', 'Failed'], ['done', 'Done']].map(([k, l]) => `<button data-f="${k}" class="${filter === k ? 'on' : ''}">${l}<span>${c[k] || 0}</span></button>`).join('')}</div>
      <ul>${shown.map(({ r, i }) => `<li class="${open === i ? 'open' : ''}"><button class="dm-rl-row" data-i="${i}"><span class="dm-rl-ic ${r.st}">${ic[r.st]}</span><span class="dm-rl-m"><b>${esc(r.t)}</b><span>${r.who} · <code>${r.on}</code></span></span>
        <span class="dm-rl-s"><span class="dm-clock ${r.st === 'running' ? 'live' : ''}">${clock(r.secs)}</span><span class="dm-badge ${meta[r.st][1]}">${meta[r.st][0]}</span></span><span class="dm-chev">${I.caret}</span></button>
        ${open === i ? `<ol class="dm-steps">${r.steps.map((s, k) => `<li class="${k === r.steps.length - 1 ? 'last' : ''}">${esc(s)}${k === r.steps.length - 1 && r.st === 'running' ? '<span class="dm-dots"><i></i><i></i><i></i></span>' : ''}</li>`).join('')}</ol>` : ''}</li>`).join('') || '<li class="dm-empty">Nothing here right now.</li>'}</ul></div>`;
    el.querySelectorAll('[data-f]').forEach(b => b.onclick = () => { filter = b.dataset.f; draw(); });
    el.querySelectorAll('[data-i]').forEach(b => b.onclick = () => { open = open === +b.dataset.i ? -1 : +b.dataset.i; draw(); });
  };
  draw();
  setInterval(() => { runs[0].secs++; const c = el.querySelector('.dm-clock.live'); if (c) c.textContent = clock(runs[0].secs); }, 1000);
}

/* --- Filter rules: a sentence of rules, written out as SQL, JSON or plain English --- */
function rules(el) {
  let tab = 'sql', comb = 'and';
  const draw = () => {
    const sql = `SELECT * FROM \`orders\`\nWHERE \`status\` IN (?, ?)\n  ${comb.toUpperCase()} \`total\` >= ?\n  ${comb.toUpperCase()} (\`country\` = ? OR \`coupon\` LIKE ?)`;
    const hi = esc(sql).replace(/\b(SELECT|FROM|WHERE|AND|OR|IN|LIKE)\b/g, '<span class="k">$1</span>').replace(/\?/g, '<span class="p">?</span>');
    const json = `{\n  "combinator": "${comb}",\n  "children": [\n    { "column": "status", "op": "in", "values": ["paid", "shipped"] },\n    { "column": "total", "op": ">=", "value": "50" },\n    { "combinator": "or", "children": [\n      { "column": "country", "op": "=", "value": "IN" },\n      { "column": "coupon", "op": "contains", "value": "DIWALI" } ] } ]\n}`;
    const text = `status is any of paid, shipped, ${comb} total at least 50, ${comb} (country equals IN, or coupon contains DIWALI)`;
    const sel = (v) => `<span class="dm-sel">${esc(v)}${I.caret}</span>`;
    el.innerHTML = `<div class="dm-rb"><div class="dm-rb-h"><div><b>Filter orders</b><span>${comb === 'and' ? '312' : '4,818'} of 5,230 rows match</span></div></div>
      <div class="dm-rb-body">
        <div class="dm-rb-row"><span class="dm-con">Where</span>${sel('status')}${sel('is any of')}<span class="dm-chips"><i>paid</i><i>shipped</i></span></div>
        <div class="dm-rb-row"><button class="dm-con dm-toggle" title="Switch and/or">${comb}${I.caret}</button>${sel('total')}${sel('at least')}<span class="dm-in">50</span></div>
        <div class="dm-rb-row"><span class="dm-con">${comb}</span><div class="dm-rb-group"><div class="dm-rb-gh">Any of these</div>
          <div class="dm-rb-row"><span class="dm-con">Where</span>${sel('country')}${sel('equals')}<span class="dm-in">IN</span></div>
          <div class="dm-rb-row"><span class="dm-con">or</span>${sel('coupon')}${sel('contains')}<span class="dm-in">DIWALI</span></div></div></div>
      </div>
      <div class="dm-rb-out"><div class="dm-tabs sm">${[['sql', 'SQL'], ['json', 'JSON'], ['text', 'Plain English']].map(([k, l]) => `<button data-t="${k}" class="${tab === k ? 'on' : ''}">${l}</button>`).join('')}</div>
        ${tab === 'sql' ? `<pre>${hi}</pre><div class="dm-params"><span><b>?1</b>"paid"</span><span><b>?2</b>"shipped"</span><span><b>?3</b>"50"</span><span><b>?4</b>"IN"</span><span><b>?5</b>"%DIWALI%"</span></div>` : tab === 'json' ? `<pre>${esc(json)}</pre>` : `<p>${esc(text)}</p>`}</div></div>`;
    el.querySelectorAll('[data-t]').forEach(b => b.onclick = () => { tab = b.dataset.t; draw(); });
    el.querySelector('.dm-toggle').onclick = () => { comb = comb === 'and' ? 'or' : 'and'; draw(); };
  };
  draw();
}

/* --- Log stream: lines arrive live; filter by level, search, pause, and repeats fold together --- */
function logs(el) {
  const svc = ['php', 'apache', 'mysql', 'http'];
  const pool = [
    ['info', 'http', 'GET / 200 in 41ms'], ['info', 'http', 'GET /cart 200 in 63ms'], ['debug', 'php', 'cache hit products:list'],
    ['warn', 'php', 'Deprecated: strlen(): Passing null to parameter #1'], ['info', 'mysql', 'connection accepted for ls_shop'],
    ['error', 'php', 'Uncaught TypeError: Cart::total() must return float, null returned'], ['info', 'http', 'POST /checkout 302 in 118ms'],
  ];
  let lines = [], levels = new Set(['debug', 'info', 'warn', 'error']), q = '', paused = false, shown = 0, seq = 0, open = new Set();
  const now = () => { const d = new Date(); return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`; };
  const push = (l) => { lines.push({ id: ++seq, ts: now(), lv: l[0], sv: l[1], m: l[2] }); if (lines.length > 200) lines.shift(); if (!paused) shown = lines.length; };
  for (let i = 0; i < 9; i++) push(pool[i % pool.length]);
  for (let i = 0; i < 4; i++) push(['error', 'php', 'Uncaught TypeError: Cart::total() must return float, null returned']);
  el.innerHTML = `<div class="dm-ls"><div class="dm-ls-bar"><div class="dm-lv"></div><label class="dm-q">${I.search}<input placeholder="Search" aria-label="Search the demo log"></label><button class="dbtn sm dm-pause"></button></div><div class="dm-ls-list"></div><div class="dm-ls-foot"></div></div>`;
  const list = el.querySelector('.dm-ls-list'), bar = el.querySelector('.dm-lv'), foot = el.querySelector('.dm-ls-foot'), pb = el.querySelector('.dm-pause');
  const tpl = (m) => m.replace(/\d+/g, '#');
  const mark = (t) => { if (!q) return esc(t); const i = t.toLowerCase().indexOf(q); return i < 0 ? esc(t) : esc(t.slice(0, i)) + '<mark>' + esc(t.slice(i, i + q.length)) + '</mark>' + esc(t.slice(i + q.length)); };
  const short = { debug: 'DBG', info: 'INF', warn: 'WRN', error: 'ERR' };
  const row = (l, nested) => `<div class="dm-l lv-${l.lv} ${nested ? 'nested' : ''}"><span class="t">${l.ts}</span><span class="lv">${short[l.lv]}</span><span class="sv">${l.sv}</span><span class="m">${mark(l.m)}</span></div>`;
  const draw = () => {
    const vis = lines.slice(0, shown), counts = { debug: 0, info: 0, warn: 0, error: 0 }; vis.forEach(l => counts[l.lv]++);
    bar.innerHTML = ['debug', 'info', 'warn', 'error'].map(k => `<button data-lv="${k}" class="${levels.has(k) ? 'on' : ''}"><i class="d lv-${k}"></i>${k[0].toUpperCase() + k.slice(1)}<span>${counts[k]}</span></button>`).join('');
    const f = vis.filter(l => levels.has(l.lv) && (!q || l.m.toLowerCase().includes(q)));
    let out = '';
    for (let i = 0; i < f.length;) { let j = i + 1; const key = f[i].lv + f[i].sv + tpl(f[i].m); while (j < f.length && f[j].lv + f[j].sv + tpl(f[j].m) === key) j++;
      if (j - i >= 3) { const g = f.slice(i, j), last = g[g.length - 1], id = g[0].id, o = open.has(id);
        out += `<div class="dm-g lv-${last.lv}"><button class="dm-l dm-gh" data-g="${id}"><span class="t">${last.ts}</span><span class="lv">${short[last.lv]}</span><span class="sv">${last.sv}</span><span class="m dm-gm"><span>${mark(last.m)}</span><span class="dm-sim ${o ? 'open' : ''}">${g.length} similar</span></span></button>${o ? `<div class="dm-nest">${g.map(l => row(l, true)).join('')}</div>` : ''}</div>`; }
      else for (let k = i; k < j; k++) out += row(f[k]);
      i = j; }
    const atBottom = list.scrollHeight - list.scrollTop - list.clientHeight < 12;
    list.innerHTML = out || '<div class="dm-empty">No lines match.</div>';
    if (atBottom || !paused) list.scrollTop = list.scrollHeight;
    pb.innerHTML = paused ? `${I.play}Resume` : `${I.pause}Pause`;
    foot.innerHTML = `<span class="dm-live ${paused ? 'paused' : ''}"></span>${paused ? `Paused · ${lines.length - shown} new lines waiting` : 'Live · following'}<span class="dm-sp"></span>${lines.length} lines`;
    bar.querySelectorAll('[data-lv]').forEach(b => b.onclick = () => { const k = b.dataset.lv; if (levels.has(k) && levels.size > 1) levels.delete(k); else levels.add(k); draw(); });
    list.querySelectorAll('[data-g]').forEach(b => b.onclick = () => { const id = +b.dataset.g; open.has(id) ? open.delete(id) : open.add(id); draw(); });
  };
  el.querySelector('input').oninput = (e) => { q = e.target.value.trim().toLowerCase(); draw(); };
  pb.onclick = () => { paused = !paused; if (!paused) shown = lines.length; draw(); };
  draw();
  setInterval(() => { push(pool[Math.floor(Math.random() * pool.length)]); draw(); }, reduce ? 4000 : 1400);
}

/* --- Branch graph: lanes, merges and refs, with a card that follows the row you hover --- */
function graph(el) {
  const C = [
    { s: 'a1f9c2e', m: 'Merge pull request #42 from feature/payments', a: 'Maya Chen', d: 'Nov 23', p: ['b7e51d0', 'c2d81f4'], r: [['main', 'branch', 1], ['v2.5.0', 'tag']] },
    { s: 'c2d81f4', m: 'Stripe payment intents', a: 'Maya Chen', d: 'Nov 22', p: ['e44f90b'], r: [['feature/payments', 'branch']], st: '+118 −24 · 5 files', body: 'Moves checkout to payment intents so 3-D Secure works for Indian cards.' },
    { s: 'b7e51d0', m: 'Fix rounding on EUR invoices with VAT', a: 'Jai Patel', d: 'Nov 22', p: ['f9c03b1'], st: '+12 −9 · 2 files' },
    { s: 'f9c03b1', m: 'Raise API rate limit to 120 requests a minute', a: 'Jai Patel', d: 'Nov 21', p: ['e44f90b'], st: '+3 −3 · 1 file' },
    { s: 'e44f90b', m: 'Cache invoice PDFs for 24 hours', a: 'Tara Roy', d: 'Nov 20', p: ['d31c6b2'], r: [['v2.4.0', 'tag']], st: '+64 −7 · 3 files' },
    { s: 'd31c6b2', m: 'Cart and checkout pages', a: 'Maya Chen', d: 'Nov 18', p: [], st: '+402 −0 · 14 files' },
  ];
  const ROW = 44, LANE = 16, PAD = 14, tone = (i) => `var(--lane-${[6, 1, 2][i % 3]})`;
  const row = new Map(C.map((c, i) => [c.s, i])), lanes = [], laneOf = [], pend = [];
  const free = (skip = -1) => { const i = lanes.findIndex((l, k) => l === null && k !== skip); return i < 0 ? lanes.length : i; };
  C.forEach((c, i) => { let ln = lanes.indexOf(c.s); if (ln < 0) ln = free(); lanes.forEach((l, k) => { if (l === c.s && k !== ln) lanes[k] = null; }); laneOf[i] = ln; lanes[ln] = c.p[0] ?? null;
    if (c.p[0]) pend.push({ f: i, p: c.p[0], r: ln }); for (const p of c.p.slice(1)) { let j = lanes.indexOf(p); if (j < 0) { j = free(ln); lanes[j] = p; } pend.push({ f: i, p, r: j }); } });
  const X = (l) => PAD + l * LANE, Y = (r) => r * ROW + ROW / 2, cv = (x1, y1, x2, y2) => { const m = (y2 - y1) / 2; return `C ${x1} ${y1 + m} ${x2} ${y2 - m} ${x2} ${y2}`; };
  const edges = pend.map(e => ({ ...e, t: row.get(e.p) ?? C.length }));
  const path = (e) => { const x1 = X(laneOf[e.f]), y1 = Y(e.f), xr = X(e.r), x2 = X(laneOf[e.t] ?? e.r), y2 = Y(e.t);
    if (e.t - e.f === 1) return `M ${x1} ${y1} ${x1 === x2 ? `L ${x2} ${y2}` : cv(x1, y1, x2, y2)}`; let d = `M ${x1} ${y1} `, cy = y1;
    if (xr !== x1) { d += cv(x1, y1, xr, y1 + ROW) + ' '; cy = y1 + ROW; } if (xr !== x2) { if (y2 - ROW > cy) d += `L ${xr} ${y2 - ROW} `; d += cv(xr, y2 - ROW, x2, y2); } else d += `L ${x2} ${y2}`; return d; };
  const W = PAD * 2 + Math.max(...laneOf, ...edges.map(e => e.r)) * LANE;
  const ini = (n) => n.split(' ').map(p => p[0]).join('');
  el.innerHTML = `<div class="dm-bg"><div class="dm-bg-h"><b>History</b><span>${I.branch}2 branches</span><span>${I.merge}1 merge</span></div><div class="dm-bg-b">
    <svg class="dm-bg-svg" width="${W}" height="${C.length * ROW}" fill="none">${edges.map(e => `<path d="${path(e)}" stroke="${tone(e.r)}" stroke-width="2" stroke-linecap="round"/>`).join('')}
      ${C.map((c, i) => `<g transform="translate(${X(laneOf[i])} ${Y(i)})" data-dot="${i}"><circle class="ring" r="9" stroke="${tone(laneOf[i])}" stroke-width="3" fill="none"/><circle class="node" r="${c.p.length > 1 ? 4.5 : 4}" fill="${c.p.length > 1 ? 'var(--card)' : tone(laneOf[i])}" stroke="${tone(laneOf[i])}" stroke-width="${c.p.length > 1 ? 2.5 : 0}"/></g>`).join('')}</svg>
    ${C.map((c, i) => `<div class="dm-bg-row" data-r="${i}" style="padding-left:${W + 4}px"><span class="l1"><span class="msg ${c.p.length > 1 ? 'merge' : ''}">${esc(c.m)}</span><code>${c.s}</code></span><span class="l2">${(c.r || []).map(([n, t, cur]) => `<span class="ref ${t} ${cur ? 'cur' : ''}" style="--t:${tone(laneOf[i])}">${t === 'tag' ? I.tag : I.branch}${n}</span>`).join('')}<span class="av" style="--t:${tone(i)}">${ini(c.a)}</span>${(c.r || []).length ? '' : `<span class="who">${c.a}</span>`}<span class="dt">${c.d}</span></span></div>`).join('')}
    <div class="dm-bg-card"></div></div></div>`;
  const card = el.querySelector('.dm-bg-card');
  let warm = false, wait;
  const show = (i) => { const c = C[i], below = i < C.length - 2;
    card.innerHTML = `<div class="in ${below ? '' : 'up'}"><b>${esc(c.m)}</b><span class="meta">${c.a} · ${c.d}, 2026</span>${c.body ? `<p>${esc(c.body)}</p>` : ''}<div class="foot"><i style="background:${tone(laneOf[i])}"></i><code>${c.s}</code>${c.p.length > 1 ? `<span class="dim">merges ${c.p.join(' + ')}</span>` : c.st ? `<span class="st">${c.st.replace(/^(\+\d+)\s(−\d+)/, '<em class="add">$1</em> <em class="del">$2</em>')}</span>` : ''}</div></div>`;
    card.style.left = 'auto'; card.style.right = '12px'; card.style.transform = `translateY(${below ? Y(i) + ROW / 2 + 6 : Y(i) - ROW / 2 - 6}px)`; card.classList.add('open'); };
  el.querySelectorAll('.dm-bg-row').forEach(r => {
    r.onmouseenter = () => { const i = +r.dataset.r; el.querySelectorAll('[data-dot]').forEach(g => g.classList.toggle('on', +g.dataset.dot === i)); el.querySelectorAll('.dm-bg-row').forEach(x => x.classList.toggle('hov', x === r));
      clearTimeout(wait); if (warm) show(i); else wait = setTimeout(() => { warm = true; show(i); }, 300); };
  });
  el.querySelector('.dm-bg-b').onmouseleave = () => { clearTimeout(wait); warm = false; card.classList.remove('open'); el.querySelectorAll('.on, .hov').forEach(x => x.classList.remove('on', 'hov')); };
}

const DEMOS = { terminal, approve, pass, runlist, rules, logs, graph };
const io = new IntersectionObserver((es) => es.forEach((e) => { if (!e.isIntersecting) return; io.unobserve(e.target); try { DEMOS[e.target.dataset.demo]?.(e.target); } catch (err) { console.error(err); } }), { rootMargin: '200px 0px' });
document.querySelectorAll('[data-demo]').forEach((el) => io.observe(el));
})();
