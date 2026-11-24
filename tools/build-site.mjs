// Builds the website pages: site-src/layout.html wrapped around each site-src/pages/*.html, written to site/.
// The pages are committed (like tokens.css), so the site needs no build step to be served or published.
//   node tools/build-site.mjs                 write site/*.html
//   node tools/build-site.mjs --check         fail if a page in site/ is out of date (used by make lint)
//   node tools/build-site.mjs --sitemap FILE  write the sitemap (used when publishing; dates come from git)
//
// A page starts with <!--page {"title": …, "description": …, "path": "x.html", "nav": "x", "crumb": "X"}--> and may
// hold <!--head-->…<!--/head--> (extra tags for <head>) and <!--scripts-->…<!--/scripts--> (extra scripts).
// Structured data is written for every page from what the page shows: a WebPage, a breadcrumb for every page
// but the home page, and an FAQPage made from the page's visible questions, so the two can never disagree.
import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';

const root = new URL('..', import.meta.url).pathname;
const src = join(root, 'site-src'), out = join(root, 'site');
const check = process.argv.includes('--check');
const sitemapAt = process.argv.indexOf('--sitemap');
const SITE = 'https://itismowgli.github.io/localsites/';

const LOGO = readFileSync(join(src, 'logo.svg'), 'utf8').trim();
const GITHUB = '<svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>';
const layout = readFileSync(join(src, 'layout.html'), 'utf8');
const esc = (s) => s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;');
const block = (text, name) => { const m = text.match(new RegExp(`<!--${name}-->([\\s\\S]*?)<!--/${name}-->`)); return [m ? m[1].trim() : '', m ? text.replace(m[0], '') : text]; };
// visible HTML to plain text, for structured data
const plain = (html) => html.replace(/<[^>]+>/g, '').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&rarr;/g, '→').replace(/&amp;/g, '&').replace(/\s+/g, ' ').trim();

// the legal pages are written by hand, but belong in the sitemap
const LEGAL = ['terms.html', 'privacy.html', 'refunds.html', 'shipping.html', 'contact.html'];

function structured(page, body) {
  const url = page.path === 'index.html' ? SITE : SITE + page.path;
  const graph = [];
  if (page.path === 'index.html') {
    graph.push({ '@type': 'WebSite', '@id': SITE + '#website', url: SITE, name: 'LocalSites', description: 'Free, open-source local web development for Mac and Linux.', inLanguage: 'en', publisher: { '@id': SITE + '#org' } });
    graph.push({ '@type': 'Organization', '@id': SITE + '#org', name: 'LocalSites', url: SITE, logo: SITE + 'brand/app-icon-square-1024.png', sameAs: ['https://github.com/itismowgli/localsites'] });
  }
  const webpage = { '@type': 'WebPage', '@id': url + '#webpage', url, name: page.title.replace(/ · LocalSites$/, ''), description: page.description, isPartOf: { '@id': SITE + '#website' }, about: { '@id': SITE + '#software' }, inLanguage: 'en' };
  if (page.image) webpage.primaryImageOfPage = SITE + page.image;
  if (page.path !== 'index.html') {
    webpage.breadcrumb = { '@id': url + '#breadcrumb' };
    graph.push(webpage, { '@type': 'BreadcrumbList', '@id': url + '#breadcrumb', itemListElement: [
      { '@type': 'ListItem', position: 1, name: 'LocalSites', item: SITE },
      { '@type': 'ListItem', position: 2, name: page.crumb, item: url }] });
  } else graph.push(webpage);
  const qa = [...body.matchAll(/<details><summary>([\s\S]*?)<\/summary>\s*<p>([\s\S]*?)<\/p><\/details>/g)];
  if (qa.length) graph.push({ '@type': 'FAQPage', '@id': url + '#faq', mainEntity: qa.map(([, q, a]) => ({ '@type': 'Question', name: plain(q), acceptedAnswer: { '@type': 'Answer', text: plain(a) } })) });
  return `<script type="application/ld+json">\n${JSON.stringify({ '@context': 'https://schema.org', '@graph': graph }, null, 1)}\n</script>`;
}

const pages = [];
let stale = [];
for (const file of readdirSync(join(src, 'pages')).filter(f => f.endsWith('.html')).sort()) {
  let text = readFileSync(join(src, 'pages', file), 'utf8');
  const meta = text.match(/^<!--page (\{[\s\S]*?\})-->/);
  if (!meta) throw new Error(`${file}: no <!--page {…}--> header`);
  const page = JSON.parse(meta[1]);
  if (page.path !== 'index.html' && !page.crumb) throw new Error(`${file}: no "crumb" in the page header`);
  pages.push({ ...page, file: join('site-src/pages', file) });
  text = text.slice(meta[0].length);
  let head, scripts;
  [head, text] = block(text, 'head');
  [scripts, text] = block(text, 'scripts');
  const body = text.trim();
  const html = layout
    .replaceAll('{{title}}', esc(page.title)).replaceAll('{{description}}', esc(page.description))
    .replaceAll('{{path}}', page.path === 'index.html' ? '' : page.path).replaceAll('{{nav}}', page.nav)
    .replaceAll('{{logo}}', LOGO).replaceAll('{{github}}', GITHUB)
    .replace('{{llms}}', page.path === 'index.html' ? '<link rel="alternate" type="text/markdown" title="LocalSites summary for language models" href="llms.txt">\n' : '')
    .replace('{{head}}', [structured(page, body), head].filter(Boolean).join('\n'))
    .replace('{{scripts}}', scripts).replace('{{body}}', body);
  const dest = join(out, page.path);
  if (check) { if (!existsSync(dest) || readFileSync(dest, 'utf8') !== html) stale.push(page.path); }
  else if (sitemapAt < 0) { writeFileSync(dest, html); console.log(`site/${page.path}`); }
}
if (stale.length) { console.error(`out of date: ${stale.join(', ')} (run node tools/build-site.mjs)`); process.exit(1); }

if (sitemapAt > 0) {
  // last change of each page's source, from git (today when it isn't committed yet)
  const today = new Date().toISOString().slice(0, 10);
  const changed = (files) => { try { return execFileSync('git', ['log', '-1', '--format=%cs', '--', ...files], { cwd: root, encoding: 'utf8' }).trim() || today; } catch { return today; } };
  const shared = ['site-src/layout.html', 'site/site.css'];
  const urls = [
    ...pages.sort((a, b) => (b.path === 'index.html') - (a.path === 'index.html')).map((p) => [p.path === 'index.html' ? SITE : SITE + p.path, changed([p.file, ...shared])]),
    ...LEGAL.map((f) => [SITE + f, changed(['site/' + f])]),
  ];
  const xml = `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls.map(([u, d]) => `  <url><loc>${u}</loc><lastmod>${d}</lastmod></url>`).join('\n')}\n</urlset>\n`;
  writeFileSync(process.argv[sitemapAt + 1], xml);
  console.log(`sitemap: ${urls.length} pages`);
}
