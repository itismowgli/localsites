// LocalSites website: header, the Features menu, the phone menu, reveal on scroll, the feature catalogue
// search and the line figures. Shared by every page.
(() => {
  const nav = document.getElementById('nav');
  const onScroll = () => nav.classList.toggle('scrolled', scrollY > 8);
  addEventListener('scroll', onScroll, { passive: true }); onScroll();

  // where you are: the current page is marked in the header and the Features menu
  const page = document.body.dataset.page;
  document.querySelectorAll(`[data-nav="${page}"]`).forEach((a) => a.setAttribute('aria-current', 'page'));
  if (['projects', 'databases', 'agents', 'tools', 'features-all'].includes(page)) document.querySelector('.dd-mega .dd-btn')?.setAttribute('aria-current', 'page');

  // header menus (Features, Resources): open on click or hover, close on Esc or a click elsewhere
  const dds = [...document.querySelectorAll('.dd')];
  const setOpen = (dd, on) => { dd.classList.toggle('open', on); dd.querySelector('.dd-btn').setAttribute('aria-expanded', on); };
  const hover = () => matchMedia('(hover: hover) and (min-width: 901px)').matches;
  for (const dd of dds) {
    const btn = dd.querySelector('.dd-btn');
    let t;
    btn.addEventListener('click', () => { const on = !dd.classList.contains('open'); dds.forEach((d) => d !== dd && setOpen(d, false)); setOpen(dd, on); });
    dd.addEventListener('mouseenter', () => { if (!hover()) return; clearTimeout(t); dds.forEach((d) => setOpen(d, d === dd)); });
    dd.addEventListener('mouseleave', () => { if (hover()) t = setTimeout(() => setOpen(dd, false), 120); });
  }
  document.addEventListener('click', (e) => dds.forEach((dd) => { if (!dd.contains(e.target)) setOpen(dd, false); }));
  document.addEventListener('keydown', (e) => { if (e.key !== 'Escape') return; const dd = dds.find((d) => d.classList.contains('open')); if (dd) { setOpen(dd, false); dd.querySelector('.dd-btn').focus(); } });

  // phone menu
  const tog = document.querySelector('.nav-toggle');
  tog?.addEventListener('click', () => { const on = !nav.classList.contains('menu-open'); nav.classList.toggle('menu-open', on); tog.setAttribute('aria-expanded', on); document.documentElement.classList.toggle('menu-lock', on); });

  // theme: system, light or dark, remembered on this browser; screenshots follow the chosen theme
  const sources = [...document.querySelectorAll('source[media*="prefers-color-scheme: light"]')];
  const applyTheme = (t) => {
    if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t; else delete document.documentElement.dataset.theme;
    sources.forEach((s) => { s.media = t === 'light' ? 'all' : t === 'dark' ? 'not all' : '(prefers-color-scheme: light)'; });
    document.querySelectorAll('[data-theme-set]').forEach((b) => { const on = b.dataset.themeSet === t; b.setAttribute('aria-checked', on); b.tabIndex = on ? 0 : -1; });
  };
  let theme = 'system';
  try { theme = localStorage.getItem('ls-theme') || 'system'; } catch {}
  applyTheme(theme);
  const order = ['system', 'light', 'dark'];
  document.querySelectorAll('[data-theme-set]').forEach((b) => {
    b.addEventListener('click', () => { theme = b.dataset.themeSet; applyTheme(theme); try { localStorage.setItem('ls-theme', theme); } catch {} });
    b.addEventListener('keydown', (e) => {
      if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return; e.preventDefault();
      const next = order[(order.indexOf(theme) + (e.key === 'ArrowRight' ? 1 : 2)) % 3];
      document.querySelector(`[data-theme-set="${next}"]`).click(); document.querySelector(`[data-theme-set="${next}"]`).focus();
    });
  });

  // footer wordmark and status dot start when they come into view
  const mfo = new IntersectionObserver((es) => es.forEach((e) => e.target.classList.toggle('in', e.isIntersecting || e.target.classList.contains('mf-word') && e.target.classList.contains('in'))), { threshold: 0.3 });
  document.querySelectorAll('.mf-word, .mf-status').forEach((el) => mfo.observe(el));

  const io = new IntersectionObserver((es) => es.forEach((e) => { if (e.isIntersecting) { e.target.classList.add('in'); io.unobserve(e.target); } }), { rootMargin: '0px 0px -8% 0px' });
  document.querySelectorAll('.reveal').forEach((el) => io.observe(el));
  const frame = document.getElementById('heroFrame');
  if (frame) requestAnimationFrame(() => setTimeout(() => frame.classList.add('flat'), 250));

  // feature catalogue: search filters every feature; the index follows the group in view
  const q = document.getElementById('catQ');
  if (q) {
    const groups = [...document.querySelectorAll('.cat-group')], links = [...document.querySelectorAll('.cat-links a')];
    const e = (x) => x.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));
    const mark = (t, w) => { const i = w ? t.toLowerCase().indexOf(w) : -1; return i < 0 ? e(t) : e(t.slice(0, i)) + '<mark>' + e(t.slice(i, i + w.length)) + '</mark>' + e(t.slice(i + w.length)); };
    const items = groups.flatMap((g) => [...g.querySelectorAll('li')].map((li) => { const b = li.querySelector('b'), s = li.querySelector('span'); return { g, li, b, s, tb: b.textContent, ts: s.textContent }; }));
    const run = () => {
      const w = q.value.trim().toLowerCase();
      for (const it of items) { it.li.hidden = !!w && !(it.tb + ' ' + it.ts + ' ' + it.g.querySelector('h3').textContent).toLowerCase().includes(w); it.b.innerHTML = mark(it.tb, w); it.s.innerHTML = mark(it.ts, w); }
      let any = false;
      for (const g of groups) { const n = g.querySelectorAll('li:not([hidden])').length; g.hidden = !n; any ||= !!n;
        const a = links.find((l) => l.dataset.cat === g.dataset.cat); a.classList.toggle('empty', !n); a.querySelector('span').textContent = n; g.querySelector('.cat-n').textContent = n; }
      document.getElementById('catNone').hidden = any;
    };
    q.addEventListener('input', run);
    const pre = new URLSearchParams(location.search).get('q'); if (pre) { q.value = pre; run(); }
    const spy = new IntersectionObserver((es) => es.forEach((en) => { if (en.isIntersecting) links.forEach((l) => l.classList.toggle('on', l.dataset.cat === en.target.dataset.cat)); }), { rootMargin: '-30% 0px -60% 0px' });
    groups.forEach((g) => spy.observe(g));
  }

  // Hairline line figures (vendor/hairline.js): mounted when they scroll into view, each answering the pointer
  const figs = [...document.querySelectorAll('.fig[data-fig]')];
  if (figs.length) import('./vendor/hairline.js').then((HL) => {
    const seen = new IntersectionObserver((es) => es.forEach((en) => { if (!en.isIntersecting) return; seen.unobserve(en.target); const el = en.target;
      HL[el.dataset.fig]?.(el, { intensity: 0.55, label: el.getAttribute('aria-label'), play: 'play' in el.dataset }); }), { rootMargin: '200px 0px' });
    figs.forEach((f) => seen.observe(f));
  }).catch(() => {});
})();
