// Turns the support options on or off everywhere, from one file: .github/FUNDING.yml
//
//   github: itismowgli                        GitHub Sponsors
//   custom: ["https://rzp.io/rzp/yourpage"]   Razorpay Payment Page (UPI and cards)
//   buy_me_a_coffee: yourname                 Buy Me a Coffee
//
// Delete a line, or put # in front of it, to turn that option off. Then run `make support`.
// It updates the README; the website is updated when it's published.
//
//   node tools/support.mjs                  update README.md
//   node tools/support.mjs --check          fail if README.md is out of date (used by CI)
//   node tools/support.mjs --site <file>    fill in the website page (used when publishing)
import { readFileSync, writeFileSync } from 'node:fs';

const LINKS = {
  github: (v) => `https://github.com/sponsors/${v}`,
  razorpay: (v) => v,
  buy_me_a_coffee: (v) => `https://buymeacoffee.com/${v}`,
};
const LABELS = {
  github: (v) => `**[Sponsor on GitHub](${LINKS.github(v)})**: monthly or one-time, and GitHub takes no fee`,
  razorpay: (v) => `**[Pay with UPI or card](${LINKS.razorpay(v)})** (Razorpay): a one-off payment, handy in India`,
  buy_me_a_coffee: (v) => `**[Buy Me a Coffee](${LINKS.buy_me_a_coffee(v)})**`,
};
const ORDER = Object.keys(LABELS);
const NAME = /^[A-Za-z0-9_-]{1,64}$/;
const RAZORPAY_HOSTS = ['rzp.io', 'razorpay.me', 'pages.razorpay.com'];

function razorpayURL(raw) {
  let u;
  try { u = new URL(raw); } catch { return null; }
  if (u.protocol !== 'https:' || !RAZORPAY_HOSTS.includes(u.hostname) || u.username || u.password) return null;
  return u.href;
}

export function readFunding(text) {
  const on = {};
  for (const raw of text.split('\n')) {
    const line = raw.replace(/\s#.*$|^#.*$/, '').trim();
    const m = line.match(/^([a-z_]+):\s*(.*)$/);
    if (!m) continue;
    const [, key, rest] = m;
    if (key === 'custom') {
      // custom: https://… or custom: ["https://…", …]; only Razorpay links are used here
      const urls = rest.replace(/^\[|\]$/g, '').split(',').map((s) => s.trim().replace(/^["']|["']$/g, '')).filter(Boolean);
      for (const url of urls) {
        const ok = razorpayURL(url);
        if (ok) on.razorpay = ok;
        else if (/razorpay|rzp\.io/.test(url)) throw new Error(`.github/FUNDING.yml: "${url}" isn't a Razorpay https link (rzp.io, razorpay.me or pages.razorpay.com)`);
      }
      continue;
    }
    if (!(key in LABELS) || key === 'razorpay') continue;
    const user = rest.trim().replace(/^["']|["']$/g, '');
    if (!user) continue;
    if (!NAME.test(user)) throw new Error(`.github/FUNDING.yml: "${user}" doesn't look like a ${key} username`);
    on[key] = user;
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

const escAttr = (s) => s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

export function site(html, on) {
  const any = ORDER.some((k) => on[k]);
  for (const k of [...ORDER, 'any']) {
    const keep = k === 'any' ? any : Boolean(on[k]);
    const re = new RegExp(`[ \\t]*<!-- fund:${k} -->([\\s\\S]*?)<!-- /fund:${k} -->\\n?`, 'g');
    html = html.replace(re, (_, inner) => (keep ? inner : ''));
    if (keep && k !== 'any') html = html.replaceAll(`{{${k}}}`, escAttr(LINKS[k](on[k])));
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
