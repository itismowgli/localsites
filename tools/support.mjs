// Turns the support options on or off everywhere, from one file: .github/FUNDING.yml
//
//   github: itismowgli          GitHub Sponsors
//   ko_fi: yourname             Ko-fi
//   buy_me_a_coffee: yourname   Buy Me a Coffee
//
// Delete a line, or put # in front of it, to turn that option off. Then run `make support`.
// It updates the README; the website is updated when it's published.
//
//   node tools/support.mjs                  update README.md
//   node tools/support.mjs --check          fail if README.md is out of date (used by CI)
//   node tools/support.mjs --site <file>    fill in the website page (used when publishing)
import { readFileSync, writeFileSync } from 'node:fs';

const LABELS = {
  github: (u) => `**[Sponsor on GitHub](https://github.com/sponsors/${u})**: monthly or one-time, and GitHub takes no fee`,
  ko_fi: (u) => `**[Ko-fi](https://ko-fi.com/${u})**: a one-off tip with a card or PayPal`,
  buy_me_a_coffee: (u) => `**[Buy Me a Coffee](https://buymeacoffee.com/${u})**`,
};
const ORDER = Object.keys(LABELS);
const NAME = /^[A-Za-z0-9_-]{1,64}$/;

export function readFunding(text) {
  const on = {};
  for (const raw of text.split('\n')) {
    const line = raw.replace(/#.*/, '').trim();
    const m = line.match(/^([a-z_]+):\s*(.*)$/);
    if (!m || !(m[1] in LABELS)) continue;
    const user = m[2].trim().replace(/^["']|["']$/g, '');
    if (!user) continue;
    if (!NAME.test(user)) throw new Error(`.github/FUNDING.yml: "${user}" doesn't look like a ${m[1]} username`);
    on[m[1]] = user;
  }
  return on;
}

export function readme(text, on) {
  const keys = ORDER.filter((k) => on[k]);
  const body = keys.length === 0 ? '' : [
    '## Support LocalSites',
    '',
    'LocalSites is free, with no ads, paid tier or tracking. If it saves you time, you can help keep it going:',
    '',
    ...keys.map((k) => '- ' + LABELS[k](on[k])),
    '',
    'Starring the repo and telling a friend helps too.',
    '',
  ].join('\n');
  const re = /<!-- support:start -->[\s\S]*?<!-- support:end -->/;
  if (!re.test(text)) throw new Error('README.md has no <!-- support:start --> / <!-- support:end --> markers');
  return text.replace(re, `<!-- support:start -->\n${body}<!-- support:end -->`);
}

export function site(html, on) {
  const any = ORDER.some((k) => on[k]);
  for (const k of [...ORDER, 'any']) {
    const keep = k === 'any' ? any : Boolean(on[k]);
    const re = new RegExp(`[ \\t]*<!-- fund:${k} -->([\\s\\S]*?)<!-- /fund:${k} -->\\n?`, 'g');
    html = html.replace(re, (_, inner) => (keep ? inner : ''));
    if (keep && k !== 'any') html = html.replaceAll(`{{${k}}}`, on[k]);
  }
  html = html.replace(/<div class="alts">\s*<\/div>\s*/, '');
  const left = html.match(/{{[a-z_]+}}|<!-- \/?fund:/);
  if (left) throw new Error('website: something was left unfilled: ' + left[0]);
  return html;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const on = readFunding(readFileSync('.github/FUNDING.yml', 'utf8'));
  const args = process.argv.slice(2);
  if (args[0] === '--site') {
    writeFileSync(args[1], site(readFileSync(args[1], 'utf8'), on));
    console.log('Website support options:', Object.keys(on).join(', ') || 'none');
  } else {
    const cur = readFileSync('README.md', 'utf8');
    const next = readme(cur, on);
    if (args[0] === '--check') {
      if (cur !== next) { console.error('README.md support section is out of date: run `make support`'); process.exit(1); }
    } else {
      writeFileSync('README.md', next);
      console.log('README support options:', Object.keys(on).join(', ') || 'none');
    }
  }
}
