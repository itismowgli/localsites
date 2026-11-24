// Builds the website pages: site-src/layout.html wrapped around each site-src/pages/*.html, written to site/.
// The pages are committed (like tokens.css), so the site needs no build step to be served or published.
//   node tools/build-site.mjs           write site/*.html
//   node tools/build-site.mjs --check   fail if a page in site/ is out of date (used by make lint)
//
// A page starts with <!--page {"title": …, "description": …, "path": "x.html", "nav": "x"}--> and may hold
// <!--head-->…<!--/head--> (extra tags for <head>) and <!--scripts-->…<!--/scripts--> (extra scripts).
import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const root = new URL('..', import.meta.url).pathname;
const src = join(root, 'site-src'), out = join(root, 'site');
const check = process.argv.includes('--check');

const LOGO = readFileSync(join(src, 'logo.svg'), 'utf8').trim();
const GITHUB = '<svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>';
const layout = readFileSync(join(src, 'layout.html'), 'utf8');
const esc = (s) => s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;');
const block = (text, name) => { const m = text.match(new RegExp(`<!--${name}-->([\\s\\S]*?)<!--/${name}-->`)); return [m ? m[1].trim() : '', m ? text.replace(m[0], '') : text]; };

let stale = [];
for (const file of readdirSync(join(src, 'pages')).filter(f => f.endsWith('.html')).sort()) {
  let text = readFileSync(join(src, 'pages', file), 'utf8');
  const meta = text.match(/^<!--page (\{[\s\S]*?\})-->/);
  if (!meta) throw new Error(`${file}: no <!--page {…}--> header`);
  const page = JSON.parse(meta[1]);
  text = text.slice(meta[0].length);
  let head, scripts;
  [head, text] = block(text, 'head');
  [scripts, text] = block(text, 'scripts');
  const html = layout
    .replaceAll('{{title}}', esc(page.title)).replaceAll('{{description}}', esc(page.description))
    .replaceAll('{{path}}', page.path === 'index.html' ? '' : page.path).replaceAll('{{nav}}', page.nav)
    .replaceAll('{{logo}}', LOGO).replaceAll('{{github}}', GITHUB)
    .replace('{{head}}', head).replace('{{scripts}}', scripts).replace('{{body}}', text.trim());
  const dest = join(out, page.path);
  if (check) { if (!existsSync(dest) || readFileSync(dest, 'utf8') !== html) stale.push(page.path); }
  else { writeFileSync(dest, html); console.log(`site/${page.path}`); }
}
if (stale.length) { console.error(`out of date: ${stale.join(', ')} (run node tools/build-site.mjs)`); process.exit(1); }
