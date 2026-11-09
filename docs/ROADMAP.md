# Roadmap: from MAMP PRO replacement to a complete local stack

Status: plan, 2026-10-27. Tracking of built items stays in [PLAN.md](PLAN.md).

## Ground rules

- **Free and open source only.** Every service we install or call is open source, or a free public endpoint
  with an open-source client and a self-hosted alternative. No paid tiers, no accounts required by default.
- **Install on demand.** Nothing is bundled until you switch it on. Services come from Homebrew today
  (static builds later), run under the same supervisor as Apache and MySQL, and listen on 127.0.0.1.
- **Off by default, explained in one line, undoable.** The same safety model as the data browser:
  anything that exposes a site or changes data says exactly what it does before it does it.
- **One API for everything.** Every feature is an API method first, so the app, the CLI and AI agents get it at once.

## Where we stand against MAMP PRO

| MAMP PRO screen | What it does | Us today | Plan |
|---|---|---|---|
| Hosts | sites, PHP per host, aliases | ✅ projects in many folders, PHP/Node/custom/Docker | add aliases and extra domains per project (A2) |
| Ports & User | ports, run as user | ✅ Settings → Domains & ports | – |
| Editor | built-in code editor | open in VS Code etc. | one-click open in any installed editor (C1); no built-in editor |
| Cloud | paid backup/sync | – | not planned (paid); git covers sharing |
| Apache modules | toggle modules | fixed safe set | module list with toggles (A3) |
| Nginx | alternative web server | Apache only | Nginx as a per-project choice (A3) |
| MySQL | root password, network access, admin tools | ✅ per-project logins, data browser | root password change, LAN access toggle (A4) |
| Dynamic DNS | DNS-O-Matic, No-IP… | – | replaced by share tunnels, which need no router setup (B1) |
| Memcached | cache server | – | add-on (A1) |
| Redis | cache server | – | add-on, Valkey or Redis (A1) |
| MailHog | catch outgoing mail | – | Mailpit add-on, wired into every PHP project automatically (A1) |
| PHP | versions, extensions, Xdebug | ✅ versions, php.ini per project | extensions list, Xdebug toggle per project (A3) |
| Other languages | Python, Ruby, Node | ✅ Node; custom command for anything | detected Python/Ruby/Go projects (A2) |
| Recent hosts | quick list | ✅ Running + Favorites + palette | – |
| GroupStart | start a set together | ✅ everything switched on starts together | per-project "needs Redis + Mailpit" (A1) |

Things MAMP PRO doesn't do that we already do: real HTTPS on `.test`, Laravel/Next.js detection, git panel,
safe data editing, Postgres/SQLite, MCP for AI agents, CLI with JSON, health checks.

## Phase A: services and runtimes (next)

**A1. Add-on services** (Services → Add-ons; `localsites addon enable mailpit`)

| Add-on | Open source | Wiring done for you |
|---|---|---|
| Mailpit | MIT | PHP `sendmail_path` and `SMTP`/`MAIL_*` hints for Laravel; inbox at `mail.test` |
| Valkey (default) or Redis | BSD-3 / AGPL-3 | `REDIS_HOST/PORT` shown and copyable; per-project database number |
| Memcached | BSD | address shown; PHP `memcached` extension check |
| PostgreSQL | PostgreSQL licence | managed server like MySQL, per-project logins, data browser connection |
| Meilisearch / Typesense | MIT / GPL-3 | search server for Laravel Scout; key in the keychain |
| MinIO | AGPL-3 | S3-compatible storage for uploads; bucket per project |

Each add-on: install (one Homebrew command, shown before it runs), start/stop, logs, health check in Doctor,
"used by" list, and an MCP tool. Projects can declare `needs: [redis, mailpit]` in `localsites.yaml`.

**A2. Projects:** extra domains and aliases per project; Python (Django, Flask, FastAPI), Ruby (Rails) and Go
detection with sensible start commands; per-project `.env` editor with secrets masked.

**A3. Web server and PHP:** Nginx per project; Apache module toggles; PHP extensions list; Xdebug on/off per
project with the right `xdebug.mode` and port; OPcache toggle.

**A4. MySQL:** change root password (keychain), LAN access toggle with a clear warning, slow-query log view.

## Phase B: share and connect

**B1. Share a site on the internet** (replaces Dynamic DNS and ngrok). Choose a provider per share:

| Provider | Cost | Account | Client licence | Notes |
|---|---|---|---|---|
| Cloudflare quick tunnel (`cloudflared`) | free | none | Apache-2.0 | random `trycloudflare.com` URL, HTTPS |
| bore (`bore.pub` or your own server) | free | none | MIT | TCP; self-host with one binary |
| zrok (self-hosted or zrok.io free tier) | free | optional | Apache-2.0 | open source end to end |
| Tailscale Funnel | free plan | yes | BSD (client) | only if Tailscale is already installed |
| Your own frp server | free | none | Apache-2.0 | for teams with a VPS |

Shares are time-limited by default (1 hour), show a banner in the app while live, can require a password
(basic auth in front of the site), and are listed in Activity. ngrok is not offered by default because its
useful features are paid; anyone can add it as a custom provider command.

**B2. LAN preview:** open a project from a phone on the same Wi-Fi (`name.local` via Bonjour, QR code).

## Phase C: integrations with the tools you already use

**C1. Open in…** detected automatically, chosen per project:
editors (VS Code, Cursor, Zed, PhpStorm, Sublime Text, Nova), terminals (Terminal, iTerm2, Warp, Ghostty),
database apps (TablePlus, Sequel Ace, DBeaver, MySQL Workbench), git apps (GitHub Desktop, Fork, Tower,
Sourcetree), browsers (each installed one). Connection strings are passed via the app's URL scheme where
it has one (TablePlus, Sequel Ace), never on the command line.

**C2. Bundled web tools:** Adminer (Apache-2.0, single file) at `adminer.test`, auto-logged-in per project,
for people who like phpMyAdmin; Mailpit's inbox; a phpinfo page per PHP version.

**C3. Git hosting:** if the GitHub CLI (`gh`) or GitLab CLI (`glab`) is signed in, show open pull requests and
CI status for the current branch in the git panel. Read-only; uses the CLI's own login.

## Phase D: AI agents

**D1. One-click connect:** detect Claude Desktop, Claude Code, Cursor, VS Code, Windsurf and Zed; add the
MCP server to their config (backup first, shown as a diff, undo button).

**D2. Richer MCP:** resources (logs, generated configs, project files list), prompts ("diagnose this site",
"set up Laravel locally", "why is mail not sending"), tools for every new feature in A–C.

**D3. Approval queue:** destructive agent calls (stop, restart, data writes, shares) wait in the app for
Approve / Deny, with the exact change shown. Replaces the `confirm: true` convention.

**D4. Agent context:** a per-project summary agents can read (stack, URLs, database, env keys without values,
recent errors) so they stop guessing.

## Phase E: native Mac, then everywhere

**E1. Mac:** menu-bar icon (status, switch projects, open sites), notifications for crashes and finished
installs, Spotlight-style quick open, deep links (`localsites://open/<project>`), Services menu.
Done already: title-bar double-click, ⌃⌘F full screen, Window menu.

**E2. Release:** signed and notarized DMG (needs an Apple Developer ID), auto-update with signature check,
Homebrew cask, then Windows and Linux builds (Phase 3 in PLAN.md).

## Order and size

| Step | Items | Size | Done |
|---|---|---|---|
| 1 | Product name, push, CI on GitHub Actions | S | ✅ name stays LocalSites (decided 2026-11-02) |
| 2 | A1 Mailpit + Redis/Valkey + Memcached, add-on framework | M | ✅ v0.5.0 |
| 3 | B1 share tunnels (cloudflared + bore first), password + expiry | M | ✅ v0.6.0 |
| 4 | C1 Open in… + C2 Adminer | S | ✅ v0.7.0 |
| 5 | D1 one-click MCP connect + D3 approval queue | M | ✅ v0.8.0 |
| 6 | A3 Xdebug, Nginx, extensions; A4 MySQL options | M | ✅ v0.9.0 (MySQL LAN access deliberately left out) |
| 7 | A1 Postgres/Meilisearch/MinIO, C3 git hosting, D2/D4 | L | ✅ v0.10.0 (GitHub; GitLab later) |
| 8 | E1 menu bar, E2 release | M | 🟡 v0.11.0: menu bar, notifications, update check ✅; signing, notarization and auto-install need an Apple Developer ID |

Every step ships with unit + e2e + UI tests, a changelog entry and a PLAN.md row, like v0.4.0.
