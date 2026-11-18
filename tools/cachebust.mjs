// Adds a content fingerprint to every image and font the website links to (?v=<8 hex>), so a changed
// file gets a new address and browsers, CDNs and link previews (LinkedIn, X) never show a stale copy.
// Unchanged files keep their address and their cache.
//   node tools/cachebust.mjs _site/index.html [more.html …]
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { dirname, join } from 'node:path';

const SITE_URL = 'https://itismowgli.github.io/localsites/';
// relative or absolute links into these folders, without a query yet
const ref = /(["'(=])((?:https:\/\/itismowgli\.github\.io\/localsites\/)?((?:screenshots|brand|fonts)\/[^"'?#)\s]+))(?=["')\s])/g;

for (const page of process.argv.slice(2)) {
  const root = dirname(page);
  let html = readFileSync(page, 'utf8'), changed = 0;
  const missing = new Set();
  html = html.replace(ref, (all, before, url, rel) => {
    const file = join(root, rel);
    if (!existsSync(file)) {
      missing.add(rel);
      return all;
    }
    changed++;
    return `${before}${url}?v=${createHash('sha256').update(readFileSync(file)).digest('hex').slice(0, 8)}`;
  });
  if (missing.size) {
    console.error(`${page}: linked but missing: ${[...missing].join(', ')}`);
    process.exit(1);
  }
  writeFileSync(page, html);
  console.log(`${page}: ${changed} links fingerprinted (${SITE_URL})`);
}
