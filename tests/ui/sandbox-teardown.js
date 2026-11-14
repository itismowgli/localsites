const fs = require('fs'), os = require('os'), path = require('path'), http = require('http');

// Asks the sandbox service to quit over its own socket, as the app does. `localsites quit` from a script
// would wait for approval in the app (scripts are treated like AI agents), and nobody approves here.
function quit(home) {
  const run = path.join(home, 'run');
  let token;
  try { token = fs.readFileSync(path.join(run, 'api.token'), 'utf8').trim(); } catch (_) { return Promise.resolve(); } // not running
  return new Promise(resolve => {
    const req = http.request({ socketPath: path.join(run, 'api.sock'), path: '/v1/quit', method: 'POST',
      headers: { Authorization: 'Bearer ' + token, 'X-LocalSites-Client': 'app', 'Content-Type': 'application/json' }, timeout: 10000 },
    res => { res.resume(); res.on('end', resolve); });
    req.on('error', resolve); req.on('timeout', () => { req.destroy(); resolve(); });
    req.end('{}');
  });
}

module.exports = async () => {
  const f = path.join(os.tmpdir(), 'lsui-state.json');
  if (!fs.existsSync(f)) return;
  const { home, devuiPid } = JSON.parse(fs.readFileSync(f, 'utf8'));
  await quit(home);
  for (let i = 0; i < 150 && fs.existsSync(path.join(home, 'run', 'api.sock')); i++) await new Promise(r => setTimeout(r, 100)); // stopped
  try { process.kill(devuiPid); } catch (_) { /* gone */ }
  if (!process.env.LS_KEEP) fs.rmSync(home, { recursive: true, force: true });
  fs.rmSync(f, { force: true });
};
