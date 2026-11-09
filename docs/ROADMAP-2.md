# Roadmap 2: lighter, safer, and things no other local dev tool does

Status: plan, 2026-11-01. Builds on [ROADMAP.md](ROADMAP.md) (steps 1–8 done or waiting on an Apple Developer ID).

## Where we are (measured on the owner's Mac, idle, 44 projects, 1 running)

| What | Now | Note |
|---|---|---|
| LocalSites MySQL memory | 462 MB | 256 MB buffer pool + performance_schema; runs all the time once set up, even when every project uses MAMP's MySQL |
| LocalSites MySQL on disk | 207 MB | 100 MB redo log, 32 MB undo, 28 MB system tables, 12 MB temp; the databases themselves are under 1 MB |
| App window + its web page | 43 + 43 MB | system WebKit, no bundled browser |
| Background service | 15 MB, 0.6 % CPU | polls every 5 s while the app is open |
| Logs | 1.1 MB | rotated at 10 MB |
| App / CLI size | 20 MB / 16 MB | |

## 1. Use less memory, disk and battery

| # | Change | Expected effect |
|---|---|---|
| 1.1 | MySQL tuned for local work: `performance_schema=OFF`, 32–64 MB buffer pool (grows with data size), 16 MB redo log, small caches, 50 connections | ~460 MB → ~90 MB RAM; ~207 MB → ~70 MB disk |
| 1.2 | Start MySQL, PostgreSQL and add-ons **on demand**: when a project that uses them is on, or when you open Databases/Data; stop after 20 min unused (setting) | 0 MB when you only use MAMP's MySQL |
| 1.3 | Event-driven updates instead of polling: the service pushes changes to the app (one long-lived stream); folders are watched with FSEvents instead of rescanned | done in v0.14.0 (`events.wait` long poll; folders still rescanned every 60 s while the window is focused). Measured before: service 1.25 % CPU idle, all of it from the app's polling |
| 1.4 | App closed to the menu bar frees its page (the web view is reloaded on open) | done in v0.14.0. Measured: page process 47 → 36 MB, timers stopped. WebKit keeps its page process, so the estimated −40 MB isn't reachable this way |
| 1.5 | **Storage** page: what uses space (MySQL, Postgres, backups, logs, share logs, `.env` backups, add-on data), with clean-up buttons and retention rules (keep 10 backups / 30 days) | no silent growth |
| 1.6 | PHP-FPM pools idle-kill workers faster (ondemand, 10 s) and Apache keeps 2 spare processes, not 25 | fewer processes |

## 2. Updates inside the app

| # | Change |
|---|---|
| 2.1 | Signed update feed: releases carry an ed25519 signature (minisign) checked before anything is installed; the public key ships in the app |
| 2.2 | Download in the background, install on quit or with one click, keep the previous version for **one-click rollback** |
| 2.3 | Before upgrading, back up config and run migrations with a dry-run; refuse to start a newer config with an older app |
| 2.4 | Release notes appear in What's new before you install; security fixes are marked |
| 2.5 | Channels: stable / beta; Homebrew cask for people who prefer `brew upgrade` |
| needs | An Apple Developer ID for signing and notarization, otherwise macOS warns on every new download |

## 3. Notifications: in the app, on the Mac, on your phone

| # | Change |
|---|---|
| 3.1 | **Notification center** in the app (bell in the status bar): crashes, finished installs, shares about to expire, agent requests, updates, security alerts; history and per-type switches; Do Not Disturb |
| 3.2 | Real macOS notifications (UserNotifications) with **action buttons**: Approve / Deny an agent request, Extend / Stop a share, Open logs |
| 3.3 | Phone push through **ntfy** (open source, free, self-hostable, no account): "site down", "share expires in 5 min", "agent is waiting for you". Notify only; approvals never happen off this Mac |
| 3.4 | Quiet by default: only things that need you; everything else goes to the notification center |

## 4. Integrations

| # | Integration | What it does |
|---|---|---|
| 4.1 | GitHub (gh) | already: PR and CI status. Add: open a PR from a branch, check out a PR into its own worktree with its own `.test` address (see 5.1), link issues |
| 4.2 | GitLab (glab) | merge requests and pipelines in the git panel, same as GitHub |
| 4.3 | Bitbucket | read-only status through its REST API with a token in the keychain |
| 4.4 | AI apps | already: one-click MCP for Claude, Cursor, VS Code, Windsurf. Add: **local AI for the Agent box through Ollama** (free, offline, nothing leaves the Mac), with the same approval rules |
| 4.5 | Issue trackers | Jira / GitHub Issues: "start work on issue" → branch + worktree + site in one step |
| 4.6 | Chat | Slack / Discord webhooks for team notices (opt-in, per project) |
| 4.7 | Editors | open at the failing line from a PHP error or stack trace (vscode://, phpstorm://) |

## 5. Things no other local tool does (the reasons to switch)

| # | Feature | Why it matters |
|---|---|---|
| 5.1 | **Branch previews on your Mac**: every git worktree/branch gets its own address (`feature-x.shop.test`) and, if wanted, its **own copy of the database**. Review two branches side by side, or let an AI agent work in a branch without touching your main copy | Valet, MAMP and Herd give one address per folder; we haven't seen a local tool that also branches the database (to verify before announcing) |
| 5.2 | **Database time machine**: automatic snapshots before risky moments (migrations, data edits, imports, agent actions), browsable, one-click restore; small (only changed tables) | "I ran the wrong migration" becomes a 5-second fix |
| 5.3 | **Production data, made safe**: pull a copy of a remote database over SSH with masking rules (emails, names, phones, tokens replaced consistently), so you debug real shapes without real people's data | GDPR-friendly; often done by hand with ad-hoc scripts |
| 5.4 | **Request & webhook inspector**: the front door records each request to a project (method, path, status, timing, headers) with replay; a webhook inbox with a stable share URL; one timeline per project of requests, mail, logs and agent actions | similar to Laravel Telescope, but for any stack and with no code changes |
| 5.5 | **Clone & run**: paste a git URL → clone, detect, install dependencies, create the database, run migrations/seeders, open the site. `localsites.yaml` makes it repeatable for a team | onboarding in minutes, not a README |
| 5.6 | **Agent workspace with undo**: agents get per-project, time-limited permissions; every change they make is recorded with a diff and can be undone in one click (config, `.env` via backups, data via 5.2) | safe to let AI work on real projects |
| 5.7 | **PHP & stack health watch**: warns when an installed PHP/MySQL/Node version is end-of-life or has a known security fix, and offers the upgrade | keeps old client projects safe |

## 7. Problem reports (done in v0.15.0)

Local list of crashes (Go panics via `debug.SetCrashOutput`, macOS `.ips` reports), services that stop or keep crashing, failed installs and updates, page errors: Settings → Problems. Reports are opt-in (off / ask / automatic), cleaned on the Mac (`internal/problems/clean.go`) and sent in Sentry's envelope format to `problems.DSN` (empty until the owner creates the Sentry project; `LOCALSITES_REPORT_DSN` overrides). Without a DSN, reports can be copied for a GitHub issue.

## 6. Security hardening

| # | Change |
|---|---|
| 6.1 | Signed, notarized, hardened-runtime builds; SBOM per release; `govulncheck` and secret scanning in CI |
| 6.2 | Scoped API tokens: separate tokens for the app, CLI and each agent client, with scopes (read, write, destructive) and expiry; rotate on demand |
| 6.3 | Tamper-evident audit log: each activity entry includes the hash of the previous one |
| 6.4 | Strict Content-Security-Policy in the app's page; no remote content ever |
| 6.5 | Shares: password on by default, rate-limited login, `.env`, `.git` and dotfiles blocked at the gateway, live access log, maximum lifetime 24 h |
| 6.6 | Secret hygiene: warn when a project's `.env` holds what looks like live production keys; agents never see values (already) |
| 6.7 | Fuzz tests for API inputs and paths; path-traversal tests for every file-reading method |
| 6.8 | Uninstall cleanly: remove the CA from the keychain, LaunchAgents, proxies, data (with a choice to keep databases) |

## Order

| Phase | Items | Size | Needs from the owner |
|---|---|---|---|
| A (done in v0.12.0) | 1.1, 1.2, 1.5, 1.6, 6.4, 6.5 | M | – (measured: MySQL 486 → 172 MB RAM, 187 → 118 MB disk; 0 MB while asleep) |
| B | 3.1, 3.2 (next); 1.3, 1.4 done in v0.14.0 | M | – |
| C | 5.1 branch previews, 5.2 time machine | L | – |
| D | 4.2 GitLab, 4.4 Ollama, 4.7 editors, 5.5 clone & run | M | – |
| E | 5.4 inspector, 5.6 agent undo, 6.2, 6.3, 6.7 | L | – |
| F | 2.x updates (2.1–2.4 and channels done in v0.13.0), 6.1, 6.8, Homebrew cask | M | Apple Developer ID (name decided: LocalSites) |
| G | 3.3 ntfy, 4.3, 4.5, 4.6, 5.3, 5.7 | M | which issue tracker and chat the team uses |

Every phase ships like before: unit + e2e + UI tests, measured before/after for anything that claims to save memory or disk, changelog entry, PLAN.md rows.
