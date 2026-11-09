# LocalSites — end-to-end plan and build tracker

The single source of truth for **what is built, what is partly built, and what is not started**, with the test
that proves each piece. Update this file in the same change that adds or finishes a feature.

**Status:** ✅ done · 🟡 partly done / works with limits · ⬜ not started · 🔒 waiting on a decision
**Tests:** `U` unit (`make test`) · `E` engine end-to-end (`make e2e`) · `UI` desktop UI end-to-end in WebKit + Chromium (`make e2e-ui`) · `M` manual only

_Last updated: 2026-10-27 (v0.3.0)_

---

## 1. Goal

A free, open-source, MAMP PRO–class local environment for PHP & MySQL that is **light (≈12 MB app), keyboard-first
(a fast, keyboard-first UI), secure by default, and drivable by people, scripts and AI agents (CLI `--json`, local API, MCP)**,
on macOS, Windows and Linux, with optional Docker per project.

## 2. Principles (how we decide)

1. Never touch the user's data without an explicit OK. Imports copy and verify; the source stays untouched.
2. Everything listens on 127.0.0.1 only. Secrets live in the OS keychain, never in argv or logs.
3. One engine, many front ends: the app, CLI, MCP and UI tests all use the same API.
4. Every feature ships with an end-to-end test, or is marked `M` here with a reason.
5. The UI must work without a mouse.

---

## 3. Phase 1 — macOS, daily-usable (current)

### 3.1 Core engine

| Item | Status | Tests | Notes |
|---|---|---|---|
| Discover project folders, defaults per folder | ✅ | E | `internal/engine` |
| Settings store (JSON, atomic writes) | ✅ | E | `config/localsites.json` |
| `localsites.yaml` project file (read) | ✅ | M | overrides domain/php/docroot/database |
| `localsites.yaml` (write from app, "Save to project") | ⬜ | | |
| Create project (folder + starter page) | ✅ | UI | `sites.create` |
| Delete / rename project | ⬜ | | needs a careful confirm flow |
| Process supervisor, crash restart with backoff | ✅ | E | stale sockets + orphan workers cleaned |
| Readiness wait (no 502 on first visit) | ✅ | E | found by e2e |
| Start services at login | ✅ | E | Settings → General switch (per-user login item) |
| Log rotation (Apache, PHP, activity.jsonl) | ✅ | unit | over 10 MB: newest 5 MB kept in `.1`, file emptied in place (open writers keep working); checked every 30 min |

### 3.1b Projects across folders, languages and git accounts

| Item | Status | Tests | Notes |
|---|---|---|---|
| Many projects folders, each labelled; add/remove in Settings or `localsites folders` | ✅ | E UI | |
| Folder layouts: auto (detect group folders), flat, groups | ✅ | E | e.g. one group per git account |
| Type detection: Laravel, WordPress, Next.js, Node, PHP, static | ✅ | E | override per project |
| Laravel: web root `public/`, PHP matched to composer.json | ✅ | E | |
| Next.js / Node dev servers behind `name.test` (npm, pnpm, yarn, bun; `.nvmrc`) | ✅ | E | 127.0.0.1 only, crash restart |
| **Custom command** for any language (`{port}`) | ✅ | E | Python, Ruby, Go, Rust… |
| Stable ports per project; busy ports move automatically | ✅ | E | e2e caught routing to the old port |
| Install dependencies (composer with the project's PHP; npm/pnpm/yarn/bun) | ✅ | M | background job with live log |
| Git branch, host, account (GitHub, GitLab, Bitbucket, SSH aliases); tokens stripped | ✅ | E UI | |
| Group by folder / git account / type; view tabs | ✅ | UI | |
| Projects served by Laravel Valet shown as “Served by Valet” | ✅ | M | read-only; Valet keeps managing them |
| Take over a Valet site in one click | ✅ | | LocalSites' own Valet proxy for that domain wins; Valet config untouched; switch off to give it back |
| Git panel: status, fetch, ff-only pull, branch tree, switch/create, commit graph | ✅ | unit + UI | refuses with uncommitted changes; no prompts (BatchMode) |
| Per-project environment variables for dev servers | ✅ | e2e | validated names, single-line values; PATH/PORT/HOST reserved |
| Docker / docker-compose projects as a type | ✅ | e2e (validation) | any command + the port it publishes on 127.0.0.1; generated compose is phase 2 |

### 3.2 Web stack

| Item | Status | Tests | Notes |
|---|---|---|---|
| Apache 2.4 with `.htaccess` | ✅ | E | TLS on 127.0.0.1 |
| One PHP-FPM pool per project | ✅ | E | own temp dir, logs, `clear_env` |
| PHP 7.4 / 8.0 / 8.2 / 8.3 / 8.5 switch per project | ✅ | E UI | Homebrew 7.1–7.3 builds crash → hidden |
| MAMP-compatible php.ini defaults | ✅ | M | `output_buffering=4096` etc. |
| Per-project php.ini overrides (UI) | ✅ | e2e | same allowlist and value checks as Settings → PHP |
| Web root auto-detect (`public/`) | ✅ | E | |
| Dotfiles (`.env`, `.git`, `.ht*`) never served | ✅ | E | |
| Xdebug toggle per project | ✅ | | loaded only in LocalSites' PHP-FPM; install with `pecl -d php_ini= install xdebug` |

### 3.3 Domains & certificates

| Item | Status | Tests | Notes |
|---|---|---|---|
| Valet front door (share nginx/DNS with Valet) | ✅ | M | used on the dev Mac |
| Built-in front door (HTTPS proxy, www alias, HTTP→HTTPS) | ✅ | U E | |
| Built-in `.test` DNS server | ✅ | U | |
| OS setup for built-in: `/etc/resolver/test`, ports 80/443 | ⬜ | | privileged helper |
| LocalSites CA with name constraints (.test/localhost only) | ✅ | U | forged `bank.com` cert rejected |
| Trust CA in system keychain (one prompt) | ⬜ | | installer step |
| Doctor detects untrusted CA and shows the fix | ✅ | M | |

### 3.4 Databases

| Item | Status | Tests | Notes |
|---|---|---|---|
| MySQL 8.4 LTS, first-time setup, root password in Keychain | ✅ | E | race fixed (serialized setup) |
| Keep using MAMP PRO's MySQL per project (no migration needed) | ✅ | E | project MySQL switch; owner relies on MAMP PRO |
| MAMP-compatible `sql_mode`, `lower_case_table_names=2` | ✅ | M | |
| Create database + login limited to it | ✅ | E UI | |
| Backup (gzip) | ✅ | E | |
| Restore from backup | 🟡 | | engine function exists; no API/UI yet |
| Delete database | ⬜ | | confirm + automatic backup first |
| **Database browser (Data)**: MySQL/MariaDB, PostgreSQL, SQLite; tables, rows (sort, filter, paging), structure, SQL | ✅ | U E UI | TablePlus-style; MAMP PRO MySQL built in |
| Read-only by default (statement allowlist + server read-only transaction) | ✅ | U E UI | e2e caught MySQL DDL bypass; fixed |
| Edit rows inline, insert/delete rows from the grid | ✅ | unit + e2e (MySQL, Postgres, SQLite) + UI | View/Edit modes; staged; SQL preview; one transaction; each update/delete must hit exactly 1 row; needs a primary key |
| Export results (CSV file, SQL dump) | 🟡 | | CSV copy + save to file ✅; SQL dump ⬜ |
| SQL editor: autocomplete, history, saved queries, multiple result tabs | 🟡 | | autocomplete (keywords, tables, columns) + history ✅; saved queries, result tabs ⬜ |
| More engines: MariaDB-specific, SQL Server, Redis, MongoDB | ⬜ | | |
| SSH tunnel for remote databases | ✅ | unit (input checks) | system `ssh -N -L` with your keys/agent, BatchMode; host validated against option injection |
| Per-project "MySQL server" switch (MAMP ↔ LocalSites) | ✅ | E | via the pool's default socket |

### 3.5 Import / migration

| Item | Status | Tests | Notes |
|---|---|---|---|
| Read MAMP PRO hosts (PHP version, web root) | ✅ | M | prefills projects |
| Copy a database read-only + verify every row | ✅ | E | fake source MySQL in tests; source unchanged |
| Refuse to overwrite an existing database | ✅ | E | |
| Detect tampered rows during verify | ✅ | E | |
| **Copy this Mac's 3 MAMP PRO databases** | ✅ | M | done by the owner in the app: all tables verified identical |
| MAMP and MAMP PRO listed once when they share a MySQL | ✅ | M | |
| Connect a project to its copied database (`.env` backup + write + undo) | ✅ | E UI | Import → Connect projects; CLI `connect-db` / `restore-env` |
| MAMP (free) importer | 🟡 | | paths known, untested |
| XAMPP importer | 🟡 | | paths known, untested |
| Laragon / Valet / Herd importers | ⬜ | | phase 3 |

### 3.6 Interfaces

| Item | Status | Tests | Notes |
|---|---|---|---|
| Local API (unix socket + token, typed methods) | ✅ | E | wrong token → 401 |
| CLI with `--json`, `call`, `methods` | ✅ | M | |
| `localsites quit` waits for full shutdown | ✅ | UI | used by test teardown |
| MCP server (stdio), typed tools, safety annotations | ✅ | U E | destructive needs `confirm`, secrets opt-in |
| MCP resources (logs) and prompts | ✅ | unit | project summaries + logs as resources; diagnose/explain prompts |
| Activity feed (who changed what: app/cli/mcp) | ✅ | UI | `activity.jsonl` |
| Desktop app (Wails, native window, ≈12 MB) | ✅ | M | macOS built; Win/Linux compile |
| Keyboard-first UI: tabs, history, sidebar sections, palette | ✅ | UI | 11 scenarios × 2 engines |
| Agent box (plain words → actions, local only) | ✅ | UI | rule-based by design; real AI via MCP |
| Doctor, per-project connection check, debug bundle | ✅ | E UI | bundle not tested yet |
| UI error log with stack traces (`ui.log`) | ✅ | M | |
| Settings screen: projects folder, start at login, appearance | ✅ | E UI | per-field validation shown next to the field |
| Settings: domains & ports, default PHP, global php.ini, MySQL port | ✅ | E UI | changes apply live |
| Settings: per-project php.ini overrides | ✅ | | in the project panel |
| Menu-bar / tray icon | ✅ | | macOS (NSStatusItem); closing the window keeps the app running |
| Help menu (docs, debug bundle, shortcuts ⌘/, status, CLI setup, settings) | ✅ | UI | |
| **What's new feed** in Help menu + Settings, unread dot | ✅ | UI | built-in changelog (`internal/version/changelog.json`) |
| What's new from a signed remote feed (new releases, notices) | ◐ | | releases with signed notes: done (Settings → Updates); notices such as PHP CVEs: to do |
| Update checker + one-click update | 🟡 | unit | checker ✅ (GitHub releases); one-click install waits for signed releases |
| Design system: Lucide icons + Inter bundled offline; design rules (4px grid, radius 6/8, focus ring, disabled, reduced motion, aria-labels) | ✅ | UI | `tools/build-assets.mjs` |
| Light theme polish & accessibility pass | 🟡 | | follows OS; no audit yet |
| Overview (sections + status side panel) and empty states everywhere | ✅ | UI | |

---

## 4. Phase 2 — Docker, add-ons, deeper agent support

| Item | Status |
|---|---|
| Docker mode per project (generated compose, same settings) | ⬜ |
| Switch native ↔ Docker with one toggle | ⬜ |
| Redis, Mailpit (mail catcher), Meilisearch add-ons | 🟡 Mailpit, Valkey, Redis, Memcached ✅; Meilisearch ⬜ |
| Xdebug on/off per project | ✅ |
| Database browser: inline editing, SSH tunnels, more engines (see 3.4) | ⬜ |
| Share a site on the internet (tunnel) | ✅ cloudflared, bore; password, expiry, gateway |
| MCP resources (logs, configs) + prompts ("diagnose site") | ✅ |
| Agent activity: approve/deny queue for destructive calls in the app | ✅ |

## 5. Phase 3 — Windows, Linux, public release

| Item | Status |
|---|---|
| Code compiles for Windows and Linux | ✅ |
| Runtime providers: static PHP builds (all OSes) | ⬜ |
| Apache / MySQL packages for Windows and Linux | ⬜ |
| Built-in front door OS setup (Windows hosts/DNS, Linux resolved) | ⬜ |
| Native secret stores (Windows Credential Manager, libsecret) | ⬜ | 
| Installers: signed & notarized `.dmg`, `.msi`, `.deb/.rpm/AppImage` | ⬜ |
| Auto-update with signature check | ⬜ |
| CI: GitHub Actions matrix (lint, unit, e2e, UI e2e) | 🟡 lint + unit + build on macOS ✅; e2e/UI run locally |
| License (MIT) | ✅ |
| CONTRIBUTING, Code of Conduct, docs site | ⬜ |
| Public launch | ⬜ |

---

## 6. Test strategy

| Layer | Command | Runs | Covers today |
|---|---|---|---|
| Lint | `make lint` | gofmt, go vet, JS syntax | all code |
| Unit | `make test` | seconds | CA constraints, front door proxy + DNS, MCP protocol & safety |
| Engine e2e | `make e2e` | ~10 s, real Apache/PHP 8.5+8.3/MySQL 8.4 ×2 | 16 scenarios: HTTPS, `.htaccess`, dotfiles, PHP switch, web root, 404, PHP error log, input validation, switch off, doctor, crash restart, DB login isolation, backup, import verify/no-overwrite/tamper, API auth, MCP over real API |
| UI e2e | `make e2e-ui` | ~3 min, WebKit + Chromium | 18 scenarios × 2 (incl. settings, help menu, import connect, data browser, accessibility): clean boot & Wails globals, toggle + really served, PHP change, palette, keyboard, tabs/history, new project, agent box, doctor, favorites/sidebar/help, activity feed |
| Everything | `make test-all` | | |

**Isolation rules for every e2e test:** temp data dir (`LOCALSITES_HOME`), file secrets (`LOCALSITES_SECRETS=file`),
built-in front door on random ports, own MySQL on a random port. Tests never touch Valet, the Keychain or MAMP.

**Still to add:** debug-bundle content (no secrets) test · CLI `--json` golden tests · Windows/Linux CI runners ·
visual regression screenshots for the UI · load test (many projects) · upgrade test (old config → new version).

## 7. Updates & What's new (how news reaches users)

Everything new lands in one place: the **Help menu → What's new** timeline (with an unread dot) and **Settings → Updates**.

| Stage | Source | Status |
|---|---|---|
| 1 | Changelog built into each release (`internal/version/changelog.json`, API `version`) | ✅ |
| 2 | Signed remote feed (new releases, security notices such as PHP CVEs, tips) merged into the same list | ⬜ |
| 3 | Update checker + one-click signed update from the same screen: opt-in daily check, tell / download-on-quit / auto at night, stable or beta, skip, switch back (v0.13.0, `internal/update`, `docs/RELEASING.md`) | ✅ |

Rule: every release adds an entry to `changelog.json` in the same change.

## 8. Known issues

| Issue | Impact | Plan |
|---|---|---|
| Homebrew PHP 7.1–7.3 crash on start; 7.4 `intl` broken | those versions unavailable / partial | static PHP builds (phase 3) |
| Built-in front door needs one-time OS setup | only Valet mode is fully set up today | installer helper |
| Agent box is rule-based | understands common phrases only | by design; real AI via MCP |
| Prototype `~/.localsites` still on disk | none (stopped) | remove after migration |

## 9. This Mac (owner's migration checklist)

- [x] Projects picked up from MAMP PRO (PHP versions, web roots)
- [x] Main PHP project served by LocalSites on PHP 8.5 at its .test address
- [x] Valet CA trusted (no browser warning)
- [x] Copy the MAMP PRO databases and verify them (done in the app)
- [ ] Switch each project to LocalSites MySQL: Import → Connect projects (backs up `.env`, undo available)
- [ ] Switch on the remaining projects and check each with Doctor
- [ ] Retire MAMP PRO
- [x] MIT license
- [x] First git commit of `localsites-app`
- [ ] Add `~/Documents/websites` (Laravel, Valet) and `~/Documents/Project` (Next.js, account groups) as folders — done by the app owner or in setup
