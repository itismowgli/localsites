// End-to-end UI tests: real desktop UI + real LocalSites service (isolated sandbox).
// Every test fails if the page logs any error.
const { test, expect } = require('@playwright/test');
const fs = require('fs');
const path = require('path');

const call = (page, method, params = {}) => page.evaluate(async ([m, p]) => {
  const r = await fetch('/__call/' + m, { method: 'POST', body: JSON.stringify(p) });
  const j = await r.json();
  if (!r.ok) throw new Error(j.error);
  return j;
}, [method, params]);
const siteState = async (page, folder) => (await call(page, 'sites.list')).find(s => s.folder === folder);

test.beforeEach(async ({ page }) => {
  page.errors = [];
  // WebKit reports a request cancelled by a page reload (the events.wait long poll, a refresh in flight)
  // as "… due to access control checks": that is the test navigating, not an app error
  page.on('pageerror', e => { if (!/due to access control checks/.test(e.message)) page.errors.push(e.message); });
  // Validation errors are HTTP 400 responses shown in the UI; the browser also logs them as "Failed to load resource".
  // Ignored: expected 400 responses, and WebKit's message for requests cancelled by a page reload
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource: .*status of 400|due to access control checks/.test(m.text())) page.errors.push(m.text()); });
  await page.goto('/');
  for (const f of ['hello', 'pubapp', 'blog']) await call(page, 'sites.update', { folder: f, enabled: false, php: '8.5' });
  await page.reload();
  await expect(page.locator('#statusbar')).toContainText('Apache');
});
test.afterEach(async ({ page }) => { expect(page.errors, 'page errors').toEqual([]); });

test('boots cleanly and never overwrites the Wails bridge globals', async ({ page }) => {
  // Regression: a top-level function named go() once replaced window.go and broke the Mac app
  expect(await page.evaluate(() => typeof window.go)).toBe('undefined');
  expect(await page.evaluate(() => typeof window.runtime)).toBe('undefined');
  for (const label of ['Overview', 'Projects', 'Databases', 'Agent', 'Services', 'Logs', 'Doctor', 'Import', 'Settings']) {
    await expect(page.locator('#side a', { hasText: label }).first()).toBeVisible();
  }
});

test('switch a project on from the list and it is really served', async ({ page }) => {
  await page.locator('#side a[data-view="projects"]').click();
  await page.locator('[data-toggle="hello"]').first().click();
  await expect(page.locator('.row[data-row="hello"] .ring.on')).toBeVisible();
  expect((await siteState(page, 'hello')).enabled).toBe(true);
  const checks = await call(page, 'sites.check', { folder: 'hello' });
  const https = checks.find(c => c.name.startsWith('HTTPS'));
  expect(https.detail).toContain('HTTP 200'); // served end to end through the front door
  await expect(page.locator('.sect-body', { hasText: 'hello' }).last()).toBeVisible(); // listed under Running
});

test('change PHP version from the detail panel', async ({ page }) => {
  await call(page, 'sites.update', { folder: 'hello', enabled: true });
  await page.goto('/#projects/hello');
  await expect(page.locator('#detail.open')).toBeVisible();
  await page.locator('#detail [data-field="php"]').selectOption('8.3');
  await expect.poll(async () => (await siteState(page, 'hello')).php).toBe('8.3');
  await expect(page.locator('.row[data-row="hello"]')).toContainText('PHP 8.3');
});

test('command palette runs actions', async ({ page }) => {
  await page.keyboard.press('ControlOrMeta+k');
  await expect(page.locator('#overlay.open')).toBeVisible();
  await page.keyboard.type('switch on pubapp');
  await expect(page.locator('#pList .item.sel')).toContainText('Switch on pubapp');
  await page.keyboard.press('Enter');
  await expect.poll(async () => (await siteState(page, 'pubapp')).enabled).toBe(true);
});

test('keyboard: J/K move, E toggles, G then D navigates', async ({ page }) => {
  await page.goto('/#projects');
  await page.locator('#view').click({ position: { x: 5, y: 5 } }).catch(() => {});
  await page.keyboard.press('j');
  await expect(page.locator('.row.sel')).toHaveCount(1);
  const folder = await page.locator('.row.sel').getAttribute('data-row');
  await page.keyboard.press('e');
  await expect.poll(async () => (await siteState(page, folder)).enabled).toBe(true);
  await page.keyboard.press('g');
  await page.keyboard.press('d');
  await expect(page.locator('#ph .title')).toContainText('Databases');
});

test('tabs, back and forward', async ({ page }) => {
  await page.keyboard.press('ControlOrMeta+t');
  await expect(page.locator('.tab')).toHaveCount(2);
  await page.locator('#side a[data-view="doctor"]').click();
  await expect(page.locator('.tab.on')).toContainText('Doctor');
  await page.locator('#tbBack').click();
  await expect(page.locator('.tab.on')).toContainText('Overview');
  await page.locator('#tbFwd').click();
  await expect(page.locator('.tab.on')).toContainText('Doctor');
  await page.keyboard.press('ControlOrMeta+w');
  await expect(page.locator('.tab')).toHaveCount(1);
});

test('create a new project from the compose button', async ({ page, browserName }) => {
  const name = 'demo-' + browserName;
  await page.locator('.switcher [data-cmd="newproject"]').click();
  await page.locator('#keysDialog input').fill(name);
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator(`.row[data-row="${name}"]`)).toBeVisible();
  expect(await siteState(page, name)).toBeTruthy();
  fs.rmSync(path.join(process.env.LS_HOME, 'sites', name), { recursive: true, force: true });
});

test('agent box turns plain words into actions', async ({ page }) => {
  await page.goto('/#agent');
  await page.locator('#agentInput').fill('switch hello to php 8.3');
  await expect(page.locator('.suggest .item.sel')).toContainText('Switch hello to PHP 8.3');
  await page.locator('#agentInput').press('Enter');
  await expect.poll(async () => (await siteState(page, 'hello')).php).toBe('8.3');
});

test('doctor shows checks', async ({ page }) => {
  await page.goto('/#doctor');
  await expect(page.locator('#view table')).toContainText('Apache running', { timeout: 30_000 });
});

test('favorites, sidebar toggle and shortcuts help', async ({ page }) => {
  await page.goto('/#projects/blog');
  await page.locator('#detail [data-fav="blog"]').click();
  await expect(page.locator('.sect[data-sect="favorites"] + .sect-body')).toContainText('blog');
  await page.keyboard.press('ControlOrMeta+\\');
  await expect(page.locator('#body.collapsed')).toHaveCount(1);
  await page.keyboard.press('ControlOrMeta+\\');
  await page.locator('#view').click({ position: { x: 5, y: 5 } }).catch(() => {});
  await page.keyboard.press('?');
  await expect(page.locator('#keysDialog')).toContainText('Keyboard shortcuts');
  await page.keyboard.press('Escape');
  await expect(page.locator('#keysOverlay.open')).toHaveCount(0);
});

test('activity feed records who changed what', async ({ page }) => {
  await call(page, 'sites.update', { folder: 'blog', enabled: true });
  await expect.poll(async () => (await call(page, 'activity'))[0]?.summary || '').toContain('"enabled":true');
  await page.goto('/#overview');
  await page.reload();
  const activity = page.locator('.ov-sec', { hasText: 'Recent activity' });
  await expect(activity).toContainText('Switched on blog');
  await expect(activity).toContainText('Dev');
});

test('icons and font are bundled (no network needed)', async ({ page }) => {
  expect(await page.locator('#side svg.svg-i').count()).toBeGreaterThan(8);
  expect(await page.evaluate(() => Object.keys(window.LS_ICONS || {}).length)).toBeGreaterThan(40);
});

test('settings: php.ini change saves and reaches the server; bad values show next to the field', async ({ page }) => {
  await page.goto('/#settings');
  await page.locator('[data-setsec="php"]').click();
  await page.locator('[data-setting="php_ini.memory_limit"]').fill('300M');
  await page.locator('[data-cmd="setsave"]').click();
  await expect(page.locator('.save-bar')).not.toContainText('Unsaved', { timeout: 30_000 }); // save finished
  await expect.poll(async () => (await call(page, 'settings.get')).php_ini?.memory_limit, { timeout: 30_000 }).toBe('300M');
  await page.locator('[data-setting="php_ini.memory_limit"]').fill('');
  await page.locator('[data-cmd="setsave"]').click();
  await expect(page.locator('.save-bar')).not.toContainText('Unsaved', { timeout: 30_000 });
  await expect.poll(async () => (await call(page, 'settings.get')).php_ini?.memory_limit ?? '', { timeout: 30_000 }).toBe('');

  await page.locator('[data-setsec="domains"]').click();
  await page.locator('[data-setting="apache_port"]').fill('80');
  await page.locator('[data-cmd="setsave"]').click();
  await expect(page.locator('.field-error')).toContainText('between 1024', { timeout: 15_000 });
  await expect(page.locator('[data-setting="apache_port"]')).toHaveClass(/invalid/);
  await page.locator('[data-cmd="setrevert"]').click();
});

test('settings: appearance switch', async ({ page }) => {
  await page.goto('/#settings');
  await page.locator('[data-theme-set="light"]').click();
  expect(await page.evaluate(() => document.documentElement.dataset.theme)).toBe('light');
  await page.locator('[data-theme-set="system"]').click();
  expect(await page.evaluate(() => document.documentElement.dataset.theme)).toBeUndefined();
});

test("help menu shows What's new and opens the full changelog", async ({ page }) => {
  await page.waitForFunction(() => window.__localsites.state().version);
  await page.locator('.help').click();
  await expect(page.locator('#menu.open')).toContainText('Keyboard shortcuts');
  await expect(page.locator('#menu .timeline')).toBeVisible();
  await page.locator('#menu .timeline button', { hasText: 'Full changelog' }).click();
  await expect(page.locator('.settings .page h2')).toHaveText('Updates');
  await expect(page.locator('.release').first()).toContainText((await call(page, 'version')).version);
  await expect(page.locator('.help .unread')).toHaveCount(0);
});

test('import: connect a project to its copied database, then undo', async ({ page }) => {
  const envFile = path.join(process.env.LS_HOME, 'sites', 'hello', '.env');
  const original = 'APP=1\nDB_HOST=localhost\nDB_NAME=helloshop\nDB_USER=root\nDB_PASS=secret\n';
  fs.writeFileSync(envFile, original);
  await call(page, 'db.create', { name: 'helloshop' });
  await page.goto('/#import');
  const row = page.locator('tr', { hasText: 'helloshop' }).last();
  await row.locator('[data-connectdb="hello"]').click();
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(row).toContainText('Connected');
  expect(fs.readFileSync(envFile, 'utf8')).toContain('DB_USER=ls_helloshop');
  await row.locator('[data-restoreenv="hello"]').click();
  await expect(row.locator('[data-connectdb="hello"]')).toBeVisible();
  expect(fs.readFileSync(envFile, 'utf8')).toBe(original);
  fs.rmSync(envFile);
});

test('every icon-only button has an accessible label', async ({ page }) => {
  await page.goto('/#projects/hello');
  const unlabeled = await page.evaluate(() => [...document.querySelectorAll('button')]
    .filter(b => !b.textContent.trim() && !b.getAttribute('aria-label')).map(b => b.outerHTML.slice(0, 80)));
  expect(unlabeled).toEqual([]);
});

test('data browser: tables, rows, sort, filter, structure, SQL and read-only', async ({ page, browserName }) => {
  const id = 'uisqlite-' + browserName;
  const file = path.join(process.env.LS_HOME, id + '.sqlite');
  await call(page, 'data.connection.save', { connection: { id, name: 'UI ' + browserName, driver: 'sqlite', file, read_only: false } });
  await call(page, 'data.query', { id, sql: 'CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT, qty INTEGER)', allow_write: true });
  await call(page, 'data.query', { id, sql: "DELETE FROM items", allow_write: true });
  await call(page, 'data.query', { id, sql: "INSERT INTO items (name, qty) VALUES ('bolt', 5), ('nut', 12), ('washer', NULL)", allow_write: true });
  await call(page, 'data.readonly', { id, read_only: true });

  await page.goto('/#data');
  await page.locator('#dConn').selectOption(id);
  await page.locator('.tlist [data-dtable="items"]').click();
  await expect(page.locator('table.grid tbody tr')).toHaveCount(3);
  await expect(page.locator('table.grid td.null')).toHaveCount(1);
  await page.locator('[data-sortcol="qty"]').click();
  await page.locator('[data-sortcol="qty"]').click(); // descending
  await expect(page.locator('table.grid tbody tr').first()).toContainText('nut');
  await page.locator('[data-cmd="dfiltermenu"]').click();
  await page.locator('#menu [data-dfcol="name"]').click();
  await page.locator('#dFVal').fill('was');
  await page.locator('[data-cmd="dfilter"]').click();
  await expect(page.locator('table.grid tbody tr')).toHaveCount(1);
  await expect(page.locator('.dbar .fchip')).toContainText('name');
  await expect(page.locator('.dbar').first()).toContainText('of 1');

  await page.locator('[data-dtab="structure"]').click();
  await expect(page.locator('#view table.t')).toContainText('qty');

  await page.locator('[data-dtab="sql"]').click();
  await page.locator('#dSQL').fill('DELETE FROM items');
  await page.keyboard.press('ControlOrMeta+Enter');
  await expect(page.locator('[role="alert"]')).toContainText('read-only');
  expect((await call(page, 'data.query', { id, sql: 'SELECT count(*) AS n FROM items' })).rows[0][0]).toBe('3');
  await page.locator('#dSQL').fill('SELECT name FROM items ORDER BY id');
  await page.locator('[data-cmd="drun"]').click();
  await expect(page.locator('table.grid tbody tr')).toHaveCount(3);

  await page.locator('[data-cmd="dwrite"]').click();
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator('[data-cmd="dreadonly"]')).toBeVisible();
  await page.locator('[data-cmd="dreadonly"]').click();
  await expect(page.locator('[data-cmd="dwrite"]')).toBeVisible();
  await call(page, 'data.connection.delete', { id });
});

test('multiple folders: Laravel in an account group, view tabs, group by git account', async ({ page }) => {
  const work = path.join(process.env.LS_HOME, 'work');
  const shop = path.join(work, 'acct-a', 'shop');
  const w = (rel, txt) => { const f = path.join(shop, rel); fs.mkdirSync(path.dirname(f), { recursive: true }); fs.writeFileSync(f, txt); };
  w('artisan', '#!/usr/bin/env php');
  w('composer.json', '{"require":{"php":"^8.2","laravel/framework":"^11.0"}}');
  w('vendor/autoload.php', '<?php');
  w('public/index.php', '<?php echo "laravel";');
  w('.git/HEAD', 'ref: refs/heads/develop\n');
  w('.git/config', '[remote "origin"]\n\turl = git@personal:itismowgli/shop.git\n');
  const before = (await call(page, 'settings.get')).roots;
  await call(page, 'settings.update', { roots: [...before, { path: work, label: 'work' }] });

  await page.goto('/#projects');
  await page.reload(); // pick up the new folder now instead of on the next refresh
  // Filter: Filter → Type → Laravel adds a chip "Type is Laravel"
  await page.locator('.vtabs [data-cmd="filtermenu"]').click();
  await page.locator('#menu [data-fprop="type"]').click();
  await page.locator('#menu [data-fval="laravel"]').click();
  await page.keyboard.press('Escape');
  await expect(page.locator('.fchip')).toContainText('Laravel');
  await expect(page.locator('.row')).toHaveCount(1);
  await expect(page.locator('.row')).toContainText('shop');
  await expect(page.locator('.row')).toContainText('develop');
  await page.locator('[data-cmd="displaymenu"]').click();
  await page.locator('#groupBy').selectOption('account');
  await page.keyboard.press('Escape');
  await expect(page.locator('.group').first()).toContainText('itismowgli');
  await page.locator('.row', { hasText: 'shop' }).click();
  await expect(page.locator('#detail')).toContainText('SSH alias personal');
  await expect(page.locator('#detail [data-field="docroot"]')).toHaveValue('public');

  await page.locator('#filter').fill('zzz-nothing');
  await expect(page.locator('.empty-state')).toContainText('No matching projects');
  await page.locator('.empty-state [data-cmd="clearfilter"]').click();
  await call(page, 'settings.update', { roots: before });
  fs.rmSync(work, { recursive: true, force: true });
});

test('settings: add and remove a projects folder, errors next to the row', async ({ page }) => {
  await page.goto('/#settings');
  await page.locator('[data-setsec="folders"]').click();
  const rows = page.locator('.root-row');
  const n = await rows.count();
  await page.locator('[data-cmd="rootadd"]').click();
  await expect(rows).toHaveCount(n + 1);
  await rows.last().locator('[data-rootf="path"]').fill('/no/such/folder');
  await page.locator('[data-cmd="setsave"]').click();
  await expect(rows.last().locator('.field-error')).toContainText('existing folder');
  const dir = path.join(process.env.LS_HOME, 'more');
  fs.mkdirSync(dir, { recursive: true });
  await rows.last().locator('[data-rootf="path"]').fill(dir);
  await page.locator('[data-cmd="setsave"]').click();
  await expect(page.locator('.save-bar')).not.toContainText('Unsaved', { timeout: 30_000 });
  await expect.poll(async () => (await call(page, 'settings.get')).roots.length).toBe(n + 1);
  await rows.last().locator('[data-rootdel]').click();
  await page.locator('[data-cmd="setsave"]').click();
  await expect.poll(async () => (await call(page, 'settings.get')).roots.length).toBe(n);
});

test('overview has sections and a status side panel', async ({ page }) => {
  await call(page, 'sites.update', { folder: 'hello', enabled: true });
  await page.goto('/#overview');
  await expect(page.locator('.ov-head h1')).toHaveText(/Good (morning|afternoon|evening)/);
  await expect(page.locator('.ov-sec', { hasText: 'Running' })).toContainText('hello');
  for (const h of ['Services', 'Health', 'Projects', 'Folders']) await expect(page.locator('.ov-side')).toContainText(h);
});

test('data browser: Edit mode stages changes, shows the SQL, saves in one go', async ({ page, browserName }) => {
  const id = 'uiedit-' + browserName;
  const file = path.join(process.env.LS_HOME, id + '.sqlite');
  await call(page, 'data.connection.save', { connection: { id, name: 'Edit ' + browserName, driver: 'sqlite', file, read_only: false } });
  await call(page, 'data.query', { id, sql: 'CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT)', allow_write: true });
  await call(page, 'data.query', { id, sql: 'DELETE FROM items', allow_write: true });
  await call(page, 'data.query', { id, sql: "INSERT INTO items VALUES (1, 'bolt'), (2, 'nut')", allow_write: true });
  await call(page, 'data.readonly', { id, read_only: true });
  await page.goto('/#data');
  await page.locator('#dConn').selectOption(id);
  await page.locator('.tlist [data-dtable="items"]').click();
  await expect(page.locator('table.grid tbody tr')).toHaveCount(2);
  // View mode: cells are not editable
  await expect(page.locator('td[data-cell]')).toHaveCount(0);
  // Edit on a read-only connection asks first
  await page.locator('[data-cmd="dedit"]').click();
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator('.editbar')).toContainText('Editing');
  await page.locator('td[data-cell="r:0:1"]').click();
  await page.locator('td.editing input').fill('bolt M6');
  await page.keyboard.press('Enter');
  await expect(page.locator('td.changed')).toHaveCount(1);
  await page.locator('[data-rowdel="1"]').click();
  await expect(page.locator('.editbar')).toContainText('2 changes');
  expect((await call(page, 'data.query', { id, sql: 'SELECT count(*) FROM items' })).rows[0][0]).toBe('2'); // nothing written yet
  await page.locator('[data-cmd="dreview"]').click();
  await expect(page.locator('#keysDialog .sqlprev')).toContainText("UPDATE");
  await expect(page.locator('#keysDialog .sqlprev')).toContainText("DELETE");
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect.poll(async () => (await call(page, 'data.query', { id, sql: "SELECT group_concat(name) FROM items" })).rows[0][0]).toBe('bolt M6');
  await expect(page.locator('table.grid tbody tr')).toHaveCount(1);
  await call(page, 'data.connection.delete', { id });
});

test('git panel shows branch, changes, branches and history', async ({ page }) => {
  const dir = path.join(process.env.LS_HOME, 'sites', 'hello');
  const { execFileSync } = require('child_process');
  const git = (...a) => execFileSync('git', ['-C', dir, ...a], { env: { ...process.env, GIT_AUTHOR_NAME: 't', GIT_AUTHOR_EMAIL: 't@x', GIT_COMMITTER_NAME: 't', GIT_COMMITTER_EMAIL: 't@x' } });
  fs.rmSync(path.join(dir, '.git'), { recursive: true, force: true });
  git('init', '-q', '-b', 'main'); git('add', '-A'); git('commit', '-qm', 'first commit'); git('branch', 'feature/login');
  fs.writeFileSync(path.join(dir, 'new.txt'), 'x');
  await page.goto('/#projects/hello');
  await page.locator('#detail .ptabs [data-dpane="git"]').click();
  await expect(page.locator('#detail .gtitle')).toContainText('main');
  await expect(page.locator('#detail .gfiles')).toContainText('new.txt');
  await expect(page.locator('#detail .gtree')).toContainText('feature');
  await expect(page.locator('#detail .ghist')).toContainText('first commit');
  // Switching is refused while there are uncommitted changes
  await page.locator('#detail [data-gbranch="feature/login"]').click();
  await expect(page.locator('#menu [data-gswitch]')).toBeDisabled();
  await expect(page.locator('#menu.open')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('#detail.open')).toBeVisible();
  fs.rmSync(path.join(dir, 'new.txt'));
  await page.locator('#detail [data-gitreload="hello"]').click();
  await expect(page.locator('#detail .ghead')).toContainText('Clean');
  await page.locator('#detail [data-gbranch="feature/login"]').click();
  await page.locator('#menu [data-gswitch]').click();
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator('#detail .gtitle')).toContainText('feature/login');
  fs.rmSync(path.join(dir, '.git'), { recursive: true, force: true });
  await page.evaluate(() => { localStorage.removeItem('ls.dpane'); });
});

test('sidebar: folders tree filters projects; narrow window keeps the layout intact', async ({ page }) => {
  await page.goto('/#projects');
  await page.locator('#side [data-scope="root"]').first().click();
  await expect(page.locator('#ph .crumb')).toBeVisible();
  await expect(page.locator('.fchip')).toContainText('Folder');
  await page.locator('[data-cmd="clearpf"]').click();
  await page.setViewportSize({ width: 640, height: 700 });
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth || document.querySelector('.vtabs').scrollWidth > document.querySelector('.vtabs').clientWidth + 1);
  expect(overflow).toBe(false);
  await page.locator('#tbSidebar').click();
  await expect(page.locator('#body.peek')).toHaveCount(1);
  await page.locator('#side a[data-view="doctor"]').click();
  await expect(page.locator('#body.peek')).toHaveCount(0);
  await page.locator('#tbSidebar').click();
  await expect(page.locator('#body.peek')).toHaveCount(1);
  await page.mouse.click(560, 400);                         // outside the sidebar, on the dimmed page
  await expect(page.locator('#body.peek')).toHaveCount(0);
  // The closing click must not also press what's underneath: the New tab button in the title bar
  const tabs = await page.locator('.tab').count();
  await page.locator('#tbSidebar').click();
  await page.locator('#tbNewTab').click();
  await expect(page.locator('#body.peek')).toHaveCount(0);
  await expect(page.locator('.tab')).toHaveCount(tabs);
  await page.locator('#tbNewTab').click();                  // the next click works normally
  await expect(page.locator('.tab')).toHaveCount(tabs + 1);
  await page.locator('#tbSidebar').click();
  await page.keyboard.press('Escape');
  await expect(page.locator('#body.peek')).toHaveCount(0);
});

test('services: add-on cards with status, licence and per-stack connect settings', async ({ page }) => {
  await page.goto('/#services');
  const card = page.locator('[data-addon="valkey"]');
  await expect(card).toContainText('Valkey');
  await expect(card.locator('.c-tag')).toContainText('BSD');
  await expect(card.locator('.c-status')).toBeVisible();
  const st = await call(page, 'addons.list');
  if (st.find(a => a.id === 'valkey').installed) {
    await card.locator('[data-addonconnect]').click();
    await page.locator('#keysDialog [data-stack="Node / Next.js"]').click();
    await expect(page.locator('#keysDialog .c-code')).toContainText('REDIS_URL=redis://127.0.0.1:');
    await page.locator('#keysDialog [data-stack="Django"]').click();
    await expect(page.locator('#keysDialog .c-code')).toContainText('RedisCache');
    await page.keyboard.press('Escape');
  } else {
    await expect(card.locator('[data-addoninstall]')).toBeVisible();
  }
});

test('narrow window: a project opens full width with property chips', async ({ page }) => {
  await call(page, 'sites.update', { folder: 'hello', enabled: true });
  await page.setViewportSize({ width: 820, height: 760 });
  await page.goto('/#projects/hello');
  await page.reload();
  await expect(page.locator('#detail.open .pchips')).toBeVisible();
  await expect(page.locator('#view')).toBeHidden();
  await page.locator('#detail .pchips [data-field="php"]').selectOption('8.3');
  await expect.poll(async () => (await siteState(page, 'hello')).php).toBe('8.3');
  await page.locator('#detail .back-btn').click();
  await expect(page.locator('#view')).toBeVisible();
  await page.setViewportSize({ width: 1400, height: 800 });
  await page.goto('/#projects/hello');
  await expect(page.locator('#detail .pchips')).toHaveCount(0); // wide: the regular form, list stays visible
  await expect(page.locator('#view')).toBeVisible();
});

test('MySQL: connect settings per stack with port and login; ports table', async ({ page }) => {
  await call(page, 'db.create', { name: 'connectme' }).catch(() => {});
  const set = await call(page, 'settings.get');
  await page.goto('/#databases');
  await page.locator('[data-dbmore="mysql|connectme"]').click();
  await page.locator('#menu [data-dbact="connect|mysql|connectme"]').click();
  const code = page.locator('#keysDialog .c-code');
  await expect(code).toContainText(`DB_PORT=${set.mysql_port}`);
  await expect(code).toContainText('DB_USERNAME=ls_connectme');
  await expect(code).toContainText('••••');             // password masked on screen
  const copied = await page.locator('#keysDialog .c-code [data-copy]').getAttribute('data-copy');
  expect(copied).not.toContain('••••');                  // …but copied in full
  await page.locator('#keysDialog [data-stack="Node / Next.js"]').click();
  await expect(code).toContainText(`@127.0.0.1:${set.mysql_port}/connectme`);
  await page.keyboard.press('Escape');
  await page.goto('/#services');
  await expect(page.locator('.c-page table.t').last()).toContainText(`127.0.0.1:${set.mysql_port}`);
});

test('responsive: no screen spills past its panel from 460 to 1440 px', async ({ page, browserName }) => {
  test.skip(browserName !== 'webkit', 'layout check runs once, in the macOS engine');
  test.setTimeout(420_000);
  await call(page, 'sites.update', { folder: 'hello', enabled: true });
  const problems = [];
  // the page must have finished rendering and applying its size classes before it is measured
  const settle = async () => { await page.waitForFunction(() => document.querySelector('#view')?.children.length > 0 && !document.querySelector('#view .skeleton')); await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))); };
  for (const v of ['overview', 'projects', 'projects/hello', 'databases', 'data', 'agent', 'services', 'logs', 'doctor', 'import', 'settings']) {
    for (const w of [1440, 1000, 800, 600, 460]) {
      await page.setViewportSize({ width: w, height: 820 });
      await page.goto('/#' + v); await page.reload(); await page.waitForTimeout(700); await settle();
      const measure = () => page.evaluate(() => {
        const panel = document.querySelector('.panel').getBoundingClientRect(), out = [];
        const scrollers = '.grid-wrap, .logbox, .c-tabs, .vt-tabs, .tabs, .tlist, pre, .mini, .settings nav, textarea, .menu-list';
        for (const el of document.querySelectorAll('#ph *, #view *, #detail *')) {
          if (el.closest(scrollers)) continue;
          const r = el.getBoundingClientRect();
          if (r.width && r.right > panel.right + 1) out.push(`${el.tagName}.${[...el.classList].join('.')} +${Math.round(r.right - panel.right)}px`);
        }
        const view = document.querySelector('#view');
        if (view.scrollWidth > view.clientWidth + 1) out.push(`view scrolls sideways ${view.scrollWidth - view.clientWidth}px`);
        return out.slice(0, 3);
      });
      let bad = await measure();
      if (bad.length) { await page.waitForTimeout(1500); await settle(); bad = await measure(); } // a busy machine can still be mid-layout; real problems stay
      if (bad.length) problems.push(`${v} @${w}px: ${bad.join(', ')}`);
    }
  }
  expect(problems).toEqual([]);
});

test('window dragging: empty header areas drag the window, controls stay clickable', async ({ page }) => {
  await page.goto('/#projects');
  await expect(page.locator('#ph .title')).toBeVisible();
  const drag = (sel) => page.evaluate((q) => getComputedStyle(document.querySelector(q)).getPropertyValue('--wails-draggable').trim(), sel);
  for (const sel of ['#topbar', '.switcher', '.ph', '.statusbar', '.side-scroll', '.ph .spacer']) expect(await drag(sel), sel).toBe('drag');
  for (const sel of ['#tbBack', '.switcher .ws', '.ph .title', '#filter', '.statusbar .item', '#side a[data-view="projects"]', '#side .sect']) expect(await drag(sel), sel).toBe('no-drag');
});

test('native drag regions: headers drag, controls and open dialogs never do', async ({ browser }) => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  page.on('pageerror', e => { throw e; });
  await page.addInitScript(() => {
    window.__regions = [];
    window.go = { main: { App: {
      Call: async (m, p) => { const r = await fetch('/__call/' + m, { method: 'POST', body: p }); const t = await r.text(); if (!r.ok) throw new Error(JSON.parse(t).error); return t; },
      SetDragRegions: (d, n) => { window.__regions.push([d, n]); },
      LogUI: () => {}, Executable: async () => '/x', CopyText: async () => {}, SetWindowColour: () => {},
    } } };
  });
  await page.goto('/#projects');
  await expect.poll(() => page.evaluate(() => window.__regions.length)).toBeGreaterThan(0);
  const [drag, no] = await page.evaluate(() => window.__regions.at(-1));
  const inside = (list, x, y) => list.some(([l, t, w, h]) => x >= l && x < l + w && y >= t && y < t + h);
  const center = async (sel) => page.locator(sel).first().evaluate(el => { const r = el.getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; });
  const tb = await page.locator('#topbar').boundingBox();
  expect(inside(drag, tb.x + tb.width - 60, tb.y + 10)).toBe(true);            // empty title bar
  for (const sel of ['#tbBack', '.ph .title', '#filter', '.switcher .ws', '.statusbar .item']) {
    const [x, y] = await center(sel);
    expect(inside(drag, x, y) && !inside(no, x, y), sel).toBe(false);          // controls stay clickable
  }
  await page.keyboard.press('ControlOrMeta+k');                                  // palette open: nothing drags
  await expect.poll(() => page.evaluate(() => window.__regions.at(-1)[0].length)).toBe(0);
  await ctx.close();
});

test('share: project panel offers sharing (install a tunnel first when none is installed)', async ({ page }) => {
  await call(page, 'sites.update', { folder: 'hello', enabled: true });
  await page.goto('/#projects/hello');
  await expect(page.locator('#detail .st', { hasText: /Share/ })).toBeVisible();
  const st = await call(page, 'status');
  if (st.share_providers.some(p => p.installed)) {
    await expect(page.locator('#detail [data-sharestart="hello"]')).toBeVisible();
    await page.locator('#detail [data-cmd="sharegenpw"]').click();
    await expect(page.locator('#sharePw')).toHaveValue(/^[a-z]+-[a-z]+-\d{3}$/);
  } else {
    await expect(page.locator('#detail [data-shareinstall="cloudflared"]')).toBeVisible();
  }
});

test('open in: preferred editor in settings, more apps menu in the project panel', async ({ page }) => {
  const apps = await call(page, 'apps.list');
  await page.goto('/#projects/hello');
  await page.locator('#detail [data-openmore]').click();
  await expect(page.locator('#menu.open')).toBeVisible();
  if (apps.some(a => a.kind === 'editor')) await expect(page.locator('#menu')).toContainText('Editors');
  await page.keyboard.press('Escape');
  await page.goto('/#services');
  await expect(page.locator('[data-tool="adminer"]')).toContainText('Adminer');
});

test('AI agents: settings list agent apps with connect buttons', async ({ page }) => {
  await page.goto('/#settings');
  await page.locator('[data-setsec="agents"]').click();
  const agents = await call(page, 'agents.list');
  const found = agents.filter(a => a.found);
  if (found.length) await expect(page.locator('[data-agent]')).toHaveCount(found.length);
  else await expect(page.locator('.settings .page')).toContainText('No agent apps found');
  expect(agents.map(a => a.id)).toContain('claude-desktop');
});

test('PHP project panel offers web server and Xdebug; settings list extensions', async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto('/#projects/hello');
  await expect(page.locator('#detail [data-field="web_server"]')).toBeVisible();
  await expect(page.locator('#detail [data-field="xdebug"]')).toHaveCount(1);
  await page.goto('/#settings');
  await page.locator('[data-setsec="php"]').click();
  await expect(page.locator('.ext-list').first()).toContainText(/pdo_mysql|mysqli/i);
});

test('settings: storage page lists what uses space', async ({ page }) => {
  await page.goto('/#settings');
  await page.locator('[data-setsec="storage"]').click();
  await expect(page.locator('.settings .page')).toContainText('LocalSites MySQL');
  await expect(page.locator('.settings .page')).toContainText('Database backups');
  await expect(page.locator('.settings .page .lead')).toContainText('in total');
});

test('settings: updates are opt-in, with a choice of what happens and a way back', async ({ page }) => {
  await call(page, 'settings.update', { update_auto_check: false, update_asked: false, update_mode: 'notify', update_channel: 'stable' });
  await page.goto('/#settings');
  await page.locator('[data-setsec="updates"]').click();
  const pg = page.locator('.settings .page');
  await expect(pg.locator('h2')).toHaveText('Updates');
  // nothing is checked until the person says yes
  expect((await call(page, 'settings.get')).update_auto_check).toBeFalsy();
  await expect(pg.locator('.upd.ask')).toContainText('Check for updates automatically?');
  await expect(pg.locator('.upd [data-cmd="updcheck"]')).toBeVisible();
  await pg.locator('.upd.ask [data-cmd="updoptin"]').click();
  await expect(pg.locator('.upd.ask')).toHaveCount(0);
  await expect(pg.locator('[data-updset="update_auto_check"]')).toBeChecked();
  const s1 = await call(page, 'settings.get');
  expect(s1.update_auto_check && s1.update_asked).toBeTruthy();
  // what happens when there is an update
  await expect(pg.locator('.choice-row')).toHaveCount(3);
  await pg.locator('.choice-row', { hasText: 'Install automatically' }).click();
  await expect(pg.locator('.choice-row.on')).toContainText('Install automatically');
  expect((await call(page, 'settings.get')).update_mode).toBe('auto');
  await pg.locator('[data-updchan="beta"]').click();
  await expect(pg.locator('.lead')).toContainText('beta channel');
  await pg.locator('[data-updchan="stable"]').click();
  await expect(pg.locator('.lead')).not.toContainText('beta');
  // a manual check against the (empty, local) release list
  await pg.locator('[data-cmd="updcheck"]').click();
  await expect(pg.locator('.upd').first()).toContainText('You’re up to date');
  await expect(pg.locator('.upd .c-status')).toHaveText('Up to date');
  // switching back is offered only once a version is kept; the signature promise is stated
  await expect(pg.locator('[data-cmd="updrollback"]')).toBeDisabled();
  await expect(pg.locator('.upd-safety')).toContainText('signed');
  await expect(pg.locator('.release').first()).toContainText((await call(page, 'version')).version);
  await call(page, 'settings.update', { update_auto_check: false, update_mode: 'notify' });
});

test('updates: a ready update offers restart now or when I quit, and remembers the choice', async ({ page }) => {
  await call(page, 'settings.update', { update_mode: 'notify', update_later: '', update_skip: '' });
  // the sandbox has no real release, so answer update.status with a ready update
  let onQuit = false;
  await page.route('**/__call/update.status', (route) => route.fulfill({ contentType: 'application/json', body: JSON.stringify({
    phase: 'ready', current: '1.0.2', supported: true, signed: true, on_quit: onQuit,
    latest: { version: '9.9.9', title: 'Test release', notes: ['First change'] } }) }));
  await page.goto('/#settings');
  await page.reload(); // the page loaded its update status before the route existed
  await page.locator('[data-setsec="updates"]').click();
  const card = page.locator('.settings .page .upd.ready');
  await expect(card).toContainText('LocalSites 9.9.9 is ready to install');
  await expect(card.locator('[data-cmd="updinstall"]')).toHaveText(/Restart now/);
  await expect(card.locator('[data-cmd="updlater"]')).toHaveText(/Install when I quit/);
  onQuit = true;
  await card.locator('[data-cmd="updlater"]').click();
  await expect.poll(async () => (await call(page, 'settings.get')).update_later).toBe('9.9.9');
  // once chosen, the card says so instead of asking again
  await expect(card.locator('[data-cmd="updlater"]')).toHaveCount(0);
  await expect(card).toContainText('Installs when you quit');
  await page.unroute('**/__call/update.status');
  await call(page, 'settings.update', { update_later: '' });
});

test('live: a change made outside the app (CLI, agent) shows at once, without polling', async ({ page }) => {
  await page.locator('#side a[data-view="projects"]').click();
  await expect(page.locator('.row[data-row="hello"]')).toBeVisible();
  await page.waitForTimeout(1500); // the page is now waiting on events.wait
  await call(page, 'sites.update', { folder: 'hello', enabled: true });
  await expect(page.locator('.row[data-row="hello"] .ring.on')).toBeVisible({ timeout: 2000 });
  await call(page, 'sites.update', { folder: 'hello', enabled: false });
  await expect(page.locator('.row[data-row="hello"] .ring.on')).toHaveCount(0, { timeout: 2000 });
});

test('contrast: active Filter label, its badge and tab counts are readable in both themes', async ({ page, browserName }) => {
  test.skip(browserName !== 'webkit', 'colours are the same in both engines');
  for (const theme of ['dark', 'light']) {
    await page.goto('/#projects');
    await page.evaluate(([t]) => { localStorage.setItem('ls.pf', JSON.stringify({ root: ['sites'] })); localStorage.setItem('ls.theme', JSON.stringify(t)); }, [theme]);
    await page.reload();
    await expect(page.locator('[data-cmd="filtermenu"] .badge')).toBeVisible();
    const ratios = await page.evaluate(() => {
      const rgb = (s) => s.match(/[\d.]+/g).slice(0, 4).map(Number);
      const lum = ([r, g, b]) => { const f = (c) => { c /= 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; }; return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b); };
      const bgOf = (el) => { while (el) { const c = rgb(getComputedStyle(el).backgroundColor); if (c[3] === undefined || c[3] > 0.9) return c; el = el.parentElement; } return [0, 0, 0]; };
      const ratio = (el) => { const a = lum(rgb(getComputedStyle(el).color)), b = lum(bgOf(el)); return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05); };
      const b = document.querySelector('[data-cmd="filtermenu"]');
      return { label: ratio(b.querySelector('.lbl')), badge: ratio(b.querySelector('.badge')), count: ratio(document.querySelector('.vt-tabs button span')) };
    });
    for (const [k, v] of Object.entries(ratios)) expect(v, `${theme} ${k} contrast`).toBeGreaterThanOrEqual(4.5);
  }
  await page.evaluate(() => { localStorage.removeItem('ls.pf'); localStorage.removeItem('ls.theme'); });
});

test('problems: listed in settings, reviewed (cleaned) and sent only when asked', async ({ page }) => {
  await call(page, 'problems.dismiss', { id: '' });
  await call(page, 'settings.update', { report_mode: 'off' });
  await call(page, 'problems.ui', { message: '[ui] error: Cannot read properties of undefined (reading \'x\')\nat render (DB_PASSWORD=hunter2 /Users/someone/x.js)' });
  await page.goto('/#settings');
  await page.locator('[data-setsec="problems"]').click();
  const pg = page.locator('.settings .page');
  await expect(pg.locator('h2')).toHaveText('Problems');
  await expect(pg.locator('[data-repmode="off"]')).toHaveClass(/on/);
  await expect(pg.locator('.prob')).toHaveCount(1);
  await expect(pg.locator('.prob-title')).toContainText('Cannot read properties of undefined');
  const before = (await (await fetch(process.env.LS_SENTRY + '/__received')).json()).received;
  // review shows the cleaned text; nothing is sent until the button is pressed
  await pg.locator('[data-probreview]').click();
  const pre = page.locator('#keysDialog pre');
  await expect(pre).toContainText('App error');
  await expect(pre).not.toContainText('hunter2');
  await expect(pre).toContainText('DB_PASSWORD=[removed]');
  expect((await (await fetch(process.env.LS_SENTRY + '/__received')).json()).received).toBe(before);
  await page.locator('#keysDialog [data-r="send"]').click();
  await expect(pg.locator('.prob.sent')).toHaveCount(1);
  await expect(pg.locator('.prob .note')).toContainText('Report sent');
  const got = await (await fetch(process.env.LS_SENTRY + '/__received')).json();
  expect(got.received).toBe(before + 1);
  expect(got.last).not.toContain('hunter2');
  expect(got.last).toContain('localsites@');
  // consent switch
  await pg.locator('[data-repmode="ask"]').click();
  await expect(pg.locator('[data-repmode="ask"]')).toHaveClass(/on/);
  expect((await call(page, 'settings.get')).report_mode).toBe('ask');
  await pg.locator('[data-probdismiss=""]').click();
  await expect(pg.locator('.prob-empty')).toContainText('No problems');
  await call(page, 'settings.update', { report_mode: 'off' });
});

test('databases: each row has Open and a menu with export, import, backups, duplicate, empty and delete', async ({ page }) => {
  const os = require('os');
  for (const n of ['menudb', 'menudb_copy']) await call(page, 'db.drop', { name: n }).catch(() => {});
  await call(page, 'db.create', { name: 'menudb' });
  await call(page, 'data.query', { id: 'localsites', schema: 'menudb', sql: 'SELECT 1' }).catch(() => {});
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'lsdbui'));
  const dump = path.join(dir, 'in.sql');
  fs.writeFileSync(dump, 'CREATE DATABASE elsewhere;\nUSE `elsewhere`;\nCREATE TABLE fromfile (x INT);\nINSERT INTO fromfile VALUES (1),(2),(3);\n');
  await page.goto('/#databases');
  const row = page.locator('tr[data-dbopen="menudb"]');
  await expect(row.locator('[data-dbopen="menudb"].btn')).toBeVisible();
  // the row never spills: everything else is in the menu
  const fits = await page.evaluate(() => { const c = document.querySelector('table.dbt').closest('.card'); return c.scrollWidth <= c.clientWidth + 1; });
  expect(fits).toBe(true);
  const menu = async (action) => { await page.locator('[data-dbmore="mysql|menudb"]').click(); await page.locator(`#menu [data-dbact="${action}|mysql|menudb"]`).click(); };
  await page.locator('[data-dbmore="mysql|menudb"]').click();
  for (const label of ['Export…', 'Import SQL file…', 'Backups and restore…', 'Duplicate…', 'Empty…', 'Delete…']) await expect(page.locator('#menu')).toContainText(label);
  await page.keyboard.press('Escape');

  // import (the browser preview asks for a path instead of a file dialog)
  await menu('import');
  await page.locator('#keysDialog input').fill(dump);
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator('#keysDialog')).toContainText('Import in.sql into menudb?');
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator('#toasts')).toContainText('Imported in.sql into menudb', { timeout: 30_000 });
  const rows = await call(page, 'data.query', { id: 'localsites', schema: 'menudb', sql: 'SELECT COUNT(*) AS n FROM fromfile' });
  expect(rows.rows[0][0]).toBe('3');
  expect((await call(page, 'db.list')).some(d => d.name === 'elsewhere')).toBe(false);

  // export: structure and data, compressed
  await menu('export');
  await expect(page.locator('#keysDialog')).toContainText('Structure and data');
  await page.locator('#keysDialog [data-r="ok"]').click();
  const out = path.join(dir, 'out.sql.gz');
  await page.locator('#keysDialog input').fill(out);
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator('#toasts')).toContainText('menudb exported to out.sql.gz', { timeout: 30_000 });
  expect(fs.statSync(out).size).toBeGreaterThan(100);

  // backups: the import made one first
  await menu('backups');
  await expect(page.locator('#keysDialog .bk').first()).toContainText('before importing in.sql');
  await page.locator('#keysDialog [data-r="close"]').click();

  // duplicate, then delete the copy (the name must be typed)
  await menu('duplicate');
  await page.locator('#keysDialog input').fill('menudb_copy');
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator('tr[data-dbopen="menudb_copy"]')).toBeVisible({ timeout: 30_000 });
  await page.locator('[data-dbmore="mysql|menudb_copy"]').click();
  await page.locator('#menu [data-dbact="drop|mysql|menudb_copy"]').click();
  await page.locator('#keysDialog input').fill('wrong');
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator('#toasts')).toContainText('didn’t match');
  await expect(page.locator('tr[data-dbopen="menudb_copy"]')).toBeVisible();
  await page.locator('[data-dbmore="mysql|menudb_copy"]').click();
  await page.locator('#menu [data-dbact="drop|mysql|menudb_copy"]').click();
  await page.locator('#keysDialog input').fill('menudb_copy');
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator('tr[data-dbopen="menudb_copy"]')).toHaveCount(0, { timeout: 30_000 });

  // empty keeps the database
  await menu('empty');
  await page.locator('#keysDialog [data-dlg="ok"]').click();
  await expect(page.locator('tr[data-dbopen="menudb"] .c-tables')).toHaveText('0', { timeout: 30_000 });
  await call(page, 'db.drop', { name: 'menudb' });
});

test('data browser: export and import buttons for the open connection', async ({ page }) => {
  await call(page, 'db.create', { name: 'browsex' }).catch(() => {});
  await page.goto('/#databases');
  await page.locator('tr[data-dbopen="browsex"]').click();
  await expect(page.locator('.dp-tools [data-cmd="dexport"]')).toBeVisible();
  await page.locator('.dp-tools [data-cmd="dimport"]').click();
  await expect(page.locator('#toasts')).toContainText('read-only');
  await call(page, 'db.drop', { name: 'browsex' });
});

test('recent activity menu: plain words, one-line times, repeats counted', async ({ page }) => {
  // Regression: the menu showed "app · tools.adminer" twice, with "09:50 AM" wrapped onto two lines
  await call(page, 'sites.update', { folder: 'hello', enabled: false });
  await call(page, 'sites.update', { folder: 'hello', enabled: false });
  await page.reload();
  await expect(page.locator('#statusbar')).toContainText('Apache');
  await page.locator('#tbActivity').click();
  const first = page.locator('#menu .act-row').first();
  await expect(first).toContainText(/Switched off hello ×2/);
  await expect(page.locator('#menu')).not.toContainText('sites.update');
  const time = first.locator('.act-time');
  const box = await time.boundingBox(), line = await time.evaluate(e => parseFloat(getComputedStyle(e).lineHeight) || 20);
  expect(box.height).toBeLessThan(line * 1.5);
});

test('buttons never show an icon name as text (a missing icon used to print "download")', async ({ page }) => {
  await page.locator('#side a[data-view="services"]').click();
  await expect(page.locator('#statusbar')).toContainText('Apache');
  await page.waitForTimeout(500);
  const texts = await page.locator('.btn').allTextContents();
  expect(texts.filter(t => /^(download|import|external|plus|copy)[A-Z]/.test(t.trim()))).toEqual([]);
});
