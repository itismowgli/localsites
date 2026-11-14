// Starts an isolated LocalSites: temp data dir, file secrets, built-in front door on random
// ports (never Valet), fixture projects. Then serves the desktop UI over HTTP for the browser.
const { spawn, execFileSync } = require('child_process');
const httpMod = require('http');
const fs = require('fs'), os = require('os'), path = require('path'), net = require('net');

const root = path.resolve(__dirname, '..', '..');
const freePort = () => new Promise(res => { const s = net.createServer(); s.listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => res(p)); }); });

module.exports = async () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'lsui'));
  const sites = path.join(home, 'sites');
  const fixture = (rel, content) => { const f = path.join(sites, rel); fs.mkdirSync(path.dirname(f), { recursive: true }); fs.writeFileSync(f, content); };
  fixture('hello/index.php', '<?php echo "php=" . PHP_MAJOR_VERSION . "." . PHP_MINOR_VERSION;');
  fixture('pubapp/public/index.php', '<?php echo "public-ok";');
  fixture('blog/index.html', '<h1>blog</h1>');

  const [apache, mysql, http, https, dns, ui] = await Promise.all([freePort(), freePort(), freePort(), freePort(), freePort(), freePort()]);
  const cfgDir = path.join(home, 'config');
  fs.mkdirSync(cfgDir, { recursive: true });
  fs.writeFileSync(path.join(cfgDir, 'localsites.json'), JSON.stringify({
    version: 1, sites: {},
    settings: { sites_root: sites, front_door: 'builtin', tld: 'test', apache_port: apache, mysql_port: mysql, http_port: http, https_port: https, dns_port: dns },
  }, null, 2));

  execFileSync('go', ['build', '-o', path.join(root, 'bin', 'localsites'), './cmd/localsites'], { cwd: root, stdio: 'inherit' });
  execFileSync('go', ['build', '-o', path.join(root, 'bin', 'devui'), './cmd/devui'], { cwd: root, stdio: 'inherit' });

  // A local, empty release list: Settings → Updates says "up to date" without contacting GitHub
  const feed = httpMod.createServer((req, res) => { res.setHeader('Content-Type', 'application/json'); res.end('[]'); });
  await new Promise(r => feed.listen(0, '127.0.0.1', r));
  feed.unref();
  // A stand-in for Sentry: counts the reports it gets and keeps the last one (to check what was sent)
  let received = 0, last = '';
  const sentry = httpMod.createServer((req, res) => {
    if (req.url === '/__received') { res.setHeader('Content-Type', 'application/json'); return res.end(JSON.stringify({ received, last })); }
    let body = ''; req.on('data', d => { body += d; }); req.on('end', () => { received++; last = body; res.end('{"id":"ok"}'); });
  });
  await new Promise(r => sentry.listen(0, '127.0.0.1', r));
  sentry.unref();
  process.env.LS_SENTRY = `http://127.0.0.1:${sentry.address().port}`;
  const token = require('crypto').randomBytes(24).toString('hex'); // devui answers only this browser cookie
  const env = { ...process.env, LOCALSITES_REPORT_DSN: `http://testkey@127.0.0.1:${sentry.address().port}/1`, LOCALSITES_UPDATE_FEED: `http://127.0.0.1:${feed.address().port}/releases`, LOCALSITES_HOME: home, LOCALSITES_SECRETS: 'file', LOCALSITES_DEVUI_ADDR: `127.0.0.1:${ui}`, LOCALSITES_DEVUI_TOKEN: token,
    LOCALSITES_LAUNCHAGENTS_DIR: path.join(home, 'launchagents'),
    LOCALSITES_DEVUI_DIR: path.join(root, 'desktop', 'frontend', 'dist') };
  const devui = spawn(path.join(root, 'bin', 'devui'), [path.join(root, 'bin', 'localsites')], { env, stdio: ['ignore', 'pipe', 'pipe'], detached: true });
  devui.stderr.on('data', d => process.env.LS_DEBUG && process.stderr.write(d));

  const url = `http://127.0.0.1:${ui}`;
  for (let i = 0; i < 300; i++) {
    try { const r = await fetch(url + '/__call/status', { method: 'POST', body: '{}', headers: { Cookie: 'lsdevui=' + token } }); if (r.ok) break; } catch (_) { /* not up yet */ }
    await new Promise(r => setTimeout(r, 200));
  }
  process.env.LS_UI_URL = url;
  fs.writeFileSync(path.join(os.tmpdir(), 'lsui-auth.json'), JSON.stringify({ cookies: [{ name: 'lsdevui', value: token, domain: '127.0.0.1', path: '/', expires: -1, httpOnly: true, secure: false, sameSite: 'Strict' }], origins: [] }));
  process.env.LS_HOME = home;
  process.env.LS_HTTPS_PORT = String(https);
  fs.writeFileSync(path.join(os.tmpdir(), 'lsui-state.json'), JSON.stringify({ home, devuiPid: devui.pid, root }));
  devui.unref();
};
