// Builds tokens.css for the app and the website from design/tokens.json, so both use the same values.
//   node tools/build-tokens.mjs           write desktop/frontend/dist/tokens.css and site/tokens.css
//   node tools/build-tokens.mjs --check   fail when either file is out of date (make lint)
import { readFileSync, writeFileSync, existsSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const t = JSON.parse(readFileSync(join(root, 'design/tokens.json'), 'utf8'));
const vals = o => Object.entries(o).filter(([k]) => !k.startsWith('$'));
const prim = Object.fromEntries(vals(t.primitives));

// A theme sets every role for dark and light; all themes must name the same roles as the default.
const roles = Object.keys(t.themes.default.dark);
const theme = (name, mode) => {
  const set = t.themes[name][mode];
  const missing = roles.filter(r => !(r in set));
  if (missing.length) throw new Error(`${name}.${mode} is missing ${missing.join(', ')}`);
  return vals(set).map(([k, p]) => {
    if (!(p in prim)) throw new Error(`${name}.${mode}.${k}: no primitive named ${p}`);
    return `  --${k}: var(--${p});`;
  }).join('\n');
};
const others = vals(t.themes).filter(([n]) => n !== 'default').map(([n]) => `
:root[data-palette="${n}"] {
${theme(n, 'dark')}
}
@media (prefers-color-scheme: light) {
  :root[data-palette="${n}"]:not([data-theme="dark"]) {
${theme(n, 'light').replace(/^/gm, '  ')}
  }
}
:root[data-palette="${n}"][data-theme="light"] {
${theme(n, 'light')}
}`).join('\n');

const css = `/* Generated from design/tokens.json by tools/build-tokens.mjs. Don't edit; edit the JSON. */
@font-face { font-family: "Inter var"; src: url("fonts/inter-latin.woff2") format("woff2"); font-weight: 100 900; font-display: swap; }
:root {
  /* primitives */
${vals(t.primitives).map(([k, v]) => `  --${k}: ${v};`).join('\n')}
  /* scales */
${vals(t.scales).map(([k, v]) => `  --${k}: ${v};`).join('\n')}
  /* dark (default) */
${theme('default', 'dark')}
  color-scheme: dark;
}
@media (prefers-color-scheme: light) {
  :root:not([data-theme="dark"]) {
${theme('default', 'light').replace(/^/gm, '  ')}
    color-scheme: light;
  }
}
:root[data-theme="light"] {
${theme('default', 'light')}
  color-scheme: light;
}
${others}`;

// Readable text: every text role reaches 4.5:1 on every surface it sits on, in every theme and mode.
const TEXT = ['foreground', 'text-secondary', 'muted-foreground', 'text-faint', 'brand'];
const SURFACES = ['background', 'window', 'card', 'popover', 'muted', 'hover', 'accent'];
function srgb(v) { // "#rrggbb" or "oklch(L C H)" -> relative luminance
  if (v.startsWith('#')) {
    const n = [1, 3, 5].map(i => parseInt(v.slice(i, i + 2), 16) / 255).map(c => c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
    return 0.2126 * n[0] + 0.7152 * n[1] + 0.0722 * n[2];
  }
  const [L, C, H] = v.match(/oklch\(([^)]*)\)/)[1].split('/')[0].trim().split(/\s+/).map(Number);
  const a = C * Math.cos(H * Math.PI / 180), b = C * Math.sin(H * Math.PI / 180);
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3, m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3, s = (L - 0.0894841775 * a - 1.2914855480 * b) ** 3;
  const r = 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s, g = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s, bl = -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s;
  const c = x => Math.min(1, Math.max(0, x));
  return 0.2126 * c(r) + 0.7152 * c(g) + 0.0722 * c(bl);
}
const ratio = (x, y) => { const [a, b] = [srgb(x), srgb(y)].sort((p, q) => q - p); return (a + 0.05) / (b + 0.05); };
function contrast() {
  const out = [];
  for (const [name, modes] of vals(t.themes)) for (const mode of ['dark', 'light']) {
    const set = modes[mode];
    for (const fg of TEXT) for (const bg of SURFACES) {
      const r = ratio(prim[set[fg]], prim[set[bg]]);
      if (r < 4.5) out.push(`${name}.${mode}: ${fg} on ${bg} is ${r.toFixed(2)}:1, needs 4.5:1`);
    }
  }
  return out;
}
const low = contrast();
if (low.length) { console.error(low.join('\n')); process.exit(1); }

const outs = ['desktop/frontend/dist/tokens.css', 'site/tokens.css'];
// Styles that must use tokens only: the app's stylesheet and every stylesheet and <style> on the website.
function rawColors() {
  const files = ['desktop/frontend/dist/styles.css',
    ...readdirSync(join(root, 'site')).filter(f => /\.(css|html)$/.test(f) && f !== 'tokens.css').map(f => 'site/' + f)];
  const out = [];
  for (const f of files) {
    let text = readFileSync(join(root, f), 'utf8');
    if (f.endsWith('.html')) text = [...text.matchAll(/<style[^>]*>([\s\S]*?)<\/style>|style="([^"]*)"/g)].map(m => m[1] || m[2]).join('\n');
    text.split('\n').forEach((line, i) => {
      const m = line.match(/#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?|oklch|oklab|lab|lch)\(/);
      if (m) out.push(`${f}${f.endsWith('.html') ? '' : ':' + (i + 1)}: ${m[0]} (use a token from design/tokens.json)`);
      // the website's text uses the type-* styles only (the app keeps its own denser sizes)
      const t = f.startsWith('site/') && line.match(/font-size:\s*[0-9.]+px|font:\s*[0-9.]+px|font-size:\s*clamp/);
      if (t) out.push(`${f}${f.endsWith('.html') ? '' : ':' + (i + 1)}: ${t[0]} (use a type-* style from design/tokens.json)`);
      // and its spacing uses the space-* steps (1 to 3px nudges inside small controls are fine)
      const sp = f.startsWith('site/') && line.match(/(?:^|[\s;{])(?:padding|margin)(?:-[a-z]+)?\s*:[^;}]*?(?<![\d.])(?:[4-9]|\d{2,})px|(?:^|[\s;{])(?:row-|column-)?gap\s*:[^;}]*?(?<![\d.])(?:[4-9]|\d{2,})px/);
      if (sp) out.push(`${f}${f.endsWith('.html') ? '' : ':' + (i + 1)}: ${sp[0].trim()} (use a space-* step from design/tokens.json)`);
    });
  }
  return out;
}

// A page must not define a variable named like a theme role: the light theme is set with a stronger selector,
// so in light mode the role would win over the page's own value (this once turned the site's grey text into a
// background colour).
function clashes() {
  const files = ['desktop/frontend/dist/styles.css', ...readdirSync(join(root, 'site')).filter(f => /\.(css|html)$/.test(f) && f !== 'tokens.css').map(f => 'site/' + f)];
  const out = [];
  for (const f of files) {
    const names = new Set([...readFileSync(join(root, f), 'utf8').matchAll(/(?<![\w-])--([a-z0-9-]+)\s*:/g)].map(m => m[1]));
    for (const r of roles) if (names.has(r)) out.push(`${f}: defines --${r}, which is a theme role in tokens.css; use another name`);
  }
  return out;
}

if (process.argv.includes('--check')) {
  const clash = clashes();
  if (clash.length) { console.error(clash.join('\n')); process.exit(1); }
  const stale = outs.filter(f => !existsSync(join(root, f)) || readFileSync(join(root, f), 'utf8') !== css);
  if (stale.length) { console.error(`out of date: ${stale.join(', ')} (run node tools/build-tokens.mjs)`); process.exit(1); }
  const raw = rawColors();
  if (raw.length) { console.error(raw.slice(0, 20).join('\n')); process.exit(1); }
} else {
  for (const f of outs) writeFileSync(join(root, f), css);
  console.log('wrote', outs.join(', '));
}
