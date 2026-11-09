const { execFileSync } = require('child_process');
const fs = require('fs'), os = require('os'), path = require('path');
module.exports = async () => {
  const f = path.join(os.tmpdir(), 'lsui-state.json');
  if (!fs.existsSync(f)) return;
  const { home, devuiPid, root } = JSON.parse(fs.readFileSync(f, 'utf8'));
  try { execFileSync(path.join(root, 'bin', 'localsites'), ['quit'], { env: { ...process.env, LOCALSITES_HOME: home, LOCALSITES_SECRETS: 'file' }, timeout: 30000 }); } catch (_) { /* already stopped */ }
  try { process.kill(devuiPid); } catch (_) { /* gone */ }
  if (!process.env.LS_KEEP) fs.rmSync(home, { recursive: true, force: true });
  fs.rmSync(f, { force: true });
};
