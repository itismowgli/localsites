// Takes docs/screenshots/*.jpg (see screenshots.config.js). Demo projects and data only: nothing from
// the computer it runs on is shown.
const { test, expect } = require('@playwright/test');
const fs = require('fs'), os = require('os'), path = require('path');
const { execFileSync } = require('child_process');

const state = () => JSON.parse(fs.readFileSync(path.join(os.tmpdir(), 'lsui-state.json'), 'utf8'));
const out = (name) => path.join(state().root, 'docs', 'screenshots', name);
const call = (page, method, params = {}) => page.evaluate(async ([m, p]) => {
  const r = await fetch('/__call/' + m, { method: 'POST', body: JSON.stringify(p) });
  const j = await r.json(); if (!r.ok) throw new Error(j.error); return j;
}, [method, params]);
const shot = (page, name) => page.screenshot({ path: out(name), type: 'jpeg', quality: 86 });

// The same editors on every machine, so the pictures don't depend on what's installed here
const APPS = { apps: [
  { id: 'finder', name: 'Finder', kind: 'files' }, { id: 'vscode', name: 'VS Code', kind: 'editor' }, { id: 'cursor', name: 'Cursor', kind: 'editor' },
  { id: 'zed', name: 'Zed', kind: 'editor' }, { id: 'phpstorm', name: 'PhpStorm', kind: 'editor' }, { id: 'sublime', name: 'Sublime Text', kind: 'editor' },
  { id: 'iterm', name: 'iTerm2', kind: 'terminal' }, { id: 'ghostty', name: 'Ghostty', kind: 'terminal' }, { id: 'github', name: 'GitHub Desktop', kind: 'git' },
  { id: 'safari', name: 'Safari', kind: 'browser' }, { id: 'chrome', name: 'Google Chrome', kind: 'browser' }], defaults: { editor: 'cursor', terminal: 'ghostty' } };

// Fixed answers for what depends on this computer (installed apps, agent apps, GitHub account), and the
// sandbox's temporary folder shown as ~/Sites
const json = (body) => (r) => r.fulfill({ contentType: 'application/json', body: JSON.stringify(body) });
async function stubApps(page, defaults = APPS.defaults) {
  await page.route('**/__call/apps.list', json({ ...APPS, defaults }));
  await page.route('**/__call/agents.list', json([
    { id: 'claude-desktop', name: 'Claude Desktop', found: true, connected: true, config: '~/Library/Application Support/Claude/claude_desktop_config.json' },
    { id: 'claude-code', name: 'Claude Code', found: true, connected: true },
    { id: 'cursor', name: 'Cursor', found: true, connected: false, config: '~/.cursor/mcp.json' },
    { id: 'vscode', name: 'VS Code', found: true, connected: false },
    { id: 'windsurf', name: 'Windsurf', found: false, connected: false },
    { id: 'zed', name: 'Zed', found: true, connected: false, manual: '{"context_servers":{}}' }]));
  await page.route('**/__call/git.hosting', json({ provider: 'github', repo: 'acme/storefront',
    pull_requests: [{ number: 42, title: 'Stripe payment intents', url: 'https://github.com/acme/storefront/pull/42', isDraft: false, reviewDecision: 'APPROVED' }],
    runs: [{ workflowName: 'Tests', status: 'completed', conclusion: 'success', url: 'https://github.com/acme/storefront/actions', createdAt: '2026-11-12T16:35:00Z' }] }));
  const { home } = state();
  await page.addInitScript(([tmp, real]) => {
    const fix = () => { const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT); for (let n; (n = w.nextNode());) if (n.nodeValue.includes(tmp)) n.nodeValue = n.nodeValue.split(tmp).join(real); };
    addEventListener('DOMContentLoaded', () => { fix(); new MutationObserver(fix).observe(document.body, { subtree: true, childList: true, characterData: true }); });
  }, [path.join(fs.realpathSync(home), 'sites'), '~/Sites']);
  await page.addInitScript(([tmp, real]) => {
    const fix = () => { const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT); for (let n; (n = w.nextNode());) if (n.nodeValue.includes(tmp)) n.nodeValue = n.nodeValue.split(tmp).join(real); };
    addEventListener('DOMContentLoaded', () => { fix(); new MutationObserver(fix).observe(document.body, { subtree: true, childList: true, characterData: true }); });
  }, [path.join(home, 'sites'), '~/Sites']);
}

function demoWorkspace() {
  const { home } = state(), sites = path.join(home, 'sites');
  for (const old of ['hello', 'pubapp', 'blog']) fs.rmSync(path.join(sites, old), { recursive: true, force: true });
  const put = (rel, body) => { const f = path.join(sites, rel); fs.mkdirSync(path.dirname(f), { recursive: true }); fs.writeFileSync(f, body); };
  put('storefront/composer.json', JSON.stringify({ name: 'acme/storefront', require: { php: '^8.3', 'laravel/framework': '^12.0' } }, null, 2));
  put('storefront/artisan', '#!/usr/bin/env php\n');
  put('storefront/public/index.php', '<?php echo "storefront";');
  put('storefront/vendor/autoload.php', '<?php');
  put('storefront/app/Http/Controllers/CheckoutController.php', '<?php\n\nnamespace App\\Http\\Controllers;\n\nclass CheckoutController {}\n');
  put('api/composer.json', JSON.stringify({ name: 'acme/api', require: { php: '^8.2' } }, null, 2));
  put('api/vendor/autoload.php', '<?php');
  put('api/index.php', '<?php echo json_encode(["ok" => true]);');
  put('journal/wp-config.php', '<?php');
  put('journal/index.php', '<?php');
  put('landing/index.html', '<h1>Landing</h1>');
  put('dashboard/package.json', JSON.stringify({ name: 'dashboard', scripts: { dev: 'next dev' }, dependencies: { next: '15.0.0', react: '19.0.0' } }, null, 2));
  fs.mkdirSync(path.join(sites, 'dashboard', 'node_modules'), { recursive: true });
  put('docs-site/index.html', '<h1>Docs</h1>');
  // git history (fixed names and dates, so the pictures stay the same)
  const env = { ...process.env, GIT_AUTHOR_NAME: 'Maya Chen', GIT_AUTHOR_EMAIL: 'maya@example.com', GIT_COMMITTER_NAME: 'Maya Chen', GIT_COMMITTER_EMAIL: 'maya@example.com' };
  const git = (dir, ...args) => execFileSync('git', ['-C', path.join(sites, dir), ...args], { env, stdio: 'pipe' });
  for (const dir of ['storefront', 'api', 'journal', 'dashboard']) {
    git(dir, 'init', '-q', '-b', 'main');
    git(dir, 'remote', 'add', 'origin', `git@github.com:acme/${dir}.git`);
  }
  const commits = [['Set up Laravel 12', '2026-10-02T10:00:00'], ['Product catalogue and search', '2026-10-09T15:20:00'], ['Cart and checkout pages', '2026-10-21T11:45:00']];
  for (const dir of ['storefront', 'api', 'journal', 'dashboard']) {
    for (const [msg, date] of dir === 'storefront' ? commits : [['First version', '2026-10-01T09:00:00']]) {
      put(path.join(dir, 'CHANGES.md'), msg + '\n');
      git(dir, 'add', '-A');
      execFileSync('git', ['-C', path.join(sites, dir), 'commit', '-q', '-m', msg], { env: { ...env, GIT_AUTHOR_DATE: date, GIT_COMMITTER_DATE: date }, stdio: 'pipe' });
    }
  }
  git('storefront', 'checkout', '-q', '-b', 'feature/payments');
  put('storefront/app/Http/Controllers/PaymentController.php', '<?php\n\nnamespace App\\Http\\Controllers;\n\nclass PaymentController {}\n');
  git('storefront', 'add', '-A');
  execFileSync('git', ['-C', path.join(sites, 'storefront'), 'commit', '-q', '-m', 'Stripe payment intents'], { env: { ...env, GIT_AUTHOR_DATE: '2026-11-12T16:30:00', GIT_COMMITTER_DATE: '2026-11-12T16:30:00' }, stdio: 'pipe' });
  put('storefront/app/Http/Controllers/CheckoutController.php', '<?php\n\nnamespace App\\Http\\Controllers;\n\nclass CheckoutController\n{\n    // pay with the saved card\n}\n');
  put('storefront/resources/views/checkout.blade.php', '<form></form>\n');
  // the demo shop database
  const rows = ['Linen Overshirt', 'Merino Crew Sweater', 'Canvas Tote', 'Selvedge Denim', 'Leather Card Wallet', 'Wool Beanie', 'Oxford Shirt', 'Chore Jacket', 'Cotton Tee 3-Pack', 'Suede Desert Boots', 'Weekender Bag', 'Cashmere Scarf'];
  const prices = [89, 129, 39, 149, 59, 29, 79, 189, 49, 169, 219, 99], stock = [24, 12, 80, 9, 41, 63, 30, 7, 120, 15, 5, 18];
  const sql = `CREATE TABLE products (id INT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(80) NOT NULL, sku VARCHAR(20) NOT NULL, price DECIMAL(8,2) NOT NULL, stock INT NOT NULL, created_at DATETIME NOT NULL);
INSERT INTO products (name, sku, price, stock, created_at) VALUES ${rows.map((n, i) => `('${n}', 'LS-${1001 + i}', ${prices[i]}.00, ${stock[i]}, '2026-10-${String(2 + i * 2 > 28 ? 28 : 2 + i * 2).padStart(2, '0')} ${String(8 + (i % 9)).padStart(2, '0')}:${String((i * 17) % 60).padStart(2, '0')}:00')`).join(',\n')};
CREATE TABLE customers (id INT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(80), email VARCHAR(120));
INSERT INTO customers (name, email) VALUES ('Maya Chen', 'maya@example.com'), ('Leo Martin', 'leo@example.com'), ('Ava Patel', 'ava@example.com');
CREATE TABLE orders (id INT PRIMARY KEY AUTO_INCREMENT, customer_id INT, total DECIMAL(8,2), placed_at DATETIME);
INSERT INTO orders (customer_id, total, placed_at) VALUES (1, 218.00, '2026-11-02 10:12:00'), (2, 59.00, '2026-11-05 18:40:00'), (3, 338.00, '2026-11-11 09:05:00');
`;
  const file = path.join(home, 'storefront-demo.sql');
  fs.writeFileSync(file, sql);
  return file;
}

test.describe.configure({ mode: 'serial' });

test('set up the demo workspace', async ({ page }) => {
  const file = demoWorkspace();
  await page.goto('/');
  for (const f of ['api', 'journal', 'landing', 'storefront']) await call(page, 'sites.update', { folder: f, enabled: true, php: '8.5' });
  await call(page, 'db.create', { name: 'storefront' });
  await call(page, 'db.import', { name: 'storefront', file });
  await call(page, 'sites.update', { folder: 'storefront', database: 'storefront' });
});

test('projects (dark and light)', async ({ page, browser }) => {
  await stubApps(page);
  await page.goto('/#projects/storefront'); await page.reload();
  await expect(page.locator('.row[data-row="storefront"]')).toBeVisible();
  await expect(page.locator('#detail')).toContainText('storefront');
  await page.waitForTimeout(800);
  await shot(page, 'projects.jpg');
  const light = await browser.newContext({ storageState: path.join(os.tmpdir(), 'lsui-auth.json'), viewport: { width: 1280, height: 809 }, deviceScaleFactor: 1.5625, colorScheme: 'light' });
  const lp = await light.newPage();
  await stubApps(lp);
  await lp.goto('/#projects/storefront');
  await expect(lp.locator('#detail')).toContainText('storefront');
  await lp.waitForTimeout(800);
  await shot(lp, 'projects-light.jpg');
  await light.close();
});

test('choose your editor', async ({ page }) => {
  await stubApps(page, {});
  await page.goto('/#projects/storefront'); await page.reload();
  await page.locator('#detail [data-openwith="editor"]').scrollIntoViewIfNeeded();
  await page.locator('#detail [data-openwith="editor"]').click();
  await expect(page.locator('#keysDialog')).toContainText('Which code editor');
  await page.waitForTimeout(400);
  await shot(page, 'editor-chooser.jpg');
});

test('setup card', async ({ page }) => {
  const items = [
    { id: 'domains', title: '.test domains and HTTPS', why: '.test addresses don’t open yet; browsers would warn “Your connection is not private”', kind: 'system', required: true, password: true, steps: ['send .test addresses to LocalSites, then trust the LocalSites certificate (it can only be used for .test addresses)'] },
    { id: 'php@7.4', title: 'PHP 7.4', why: 'intl can’t load (icu4c@77 is missing) · 3 projects use it', kind: 'repair', required: true, password: false, steps: ['brew trust shivammathur/php', 'brew reinstall shivammathur/php/php@7.4'] },
    { id: 'php@8.2', title: 'PHP 8.2', why: 'not installed · 1 project uses it', kind: 'install', required: true, password: false, steps: ['brew install shivammathur/php/php@8.2'] }];
  await stubApps(page);
  await page.route('**/__call/setup.status', r => r.fulfill({ contentType: 'application/json', body: JSON.stringify({ homebrew: true, items, running: false, log: '' }) }));
  await page.goto('/#overview'); await page.reload();
  await expect(page.locator('.ov-sec.setup')).toContainText('Fix everything');
  await page.waitForTimeout(600);
  await shot(page, 'setup.jpg');
});

test('data browser', async ({ page }) => {
  await stubApps(page);
  await page.goto('/#data'); await page.reload();
  await expect(page.locator('#dSchema')).toBeVisible();
  await page.locator('#dSchema').selectOption('storefront');
  await page.locator('.tlist [data-dtable="products"]').click();
  await expect(page.locator('#view')).toContainText('Linen Overshirt');
  await page.waitForTimeout(500);
  await shot(page, 'data-browser.jpg');
});

test('git', async ({ page }) => {
  await stubApps(page);
  await page.goto('/#projects/storefront'); await page.reload();
  await page.locator('#detail [data-dpane="git"]').first().click();
  await expect(page.locator('#detail')).toContainText('Stripe payment intents');
  await page.waitForTimeout(800);
  await shot(page, 'git.jpg');
});

test('palette, services and agents', async ({ page }) => {
  await stubApps(page);
  await page.goto('/#projects'); await page.reload();
  await expect(page.locator('.row[data-row="storefront"]')).toBeVisible();
  await page.keyboard.press('Meta+k');
  await page.locator('#pInput').fill('store');
  await page.waitForTimeout(500);
  await shot(page, 'palette.jpg');
  await page.keyboard.press('Escape');
  await page.goto('/#services');
  const addons = page.locator('.c-section', { hasText: 'Add-ons' });
  await addons.waitFor();
  await addons.evaluate(el => el.scrollIntoView({ block: 'start' }));
  await page.waitForTimeout(800);
  await shot(page, 'services.jpg');
  await page.goto('/#settings');
  await page.locator('[data-setsec="agents"]').click();
  await expect(page.locator('#view')).toContainText('Claude Desktop');
  await page.waitForTimeout(500);
  await shot(page, 'agents.jpg');
});
