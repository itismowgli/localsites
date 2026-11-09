# Architecture

```
 Browser ──https://shop.test──▶ Front door ──TLS, 127.0.0.1:8744──▶ Apache ──unix socket──▶ PHP-FPM pool "shop" (PHP 8.3)
                                (Valet nginx or built-in proxy)          │                         │
                                                                     .htaccess              MySQL 8.4 (127.0.0.1:3307 + socket)
 Desktop app ─┐                                                                                  or MAMP's MySQL during migration
 CLI ─────────┼──HTTP over unix socket + token──▶ localsites daemon (engine + supervisor)
 AI agents ───┘
```

## Pieces

| Package | Job |
|---|---|
| `internal/engine` | Turns config into running services: discovers projects, validates changes, writes configs, starts/reloads processes |
| `internal/proc` | Supervises Apache, PHP-FPM and MySQL as child processes; restarts on crash with backoff |
| `internal/services` | Renders `httpd.conf`, `php-fpm.conf` (one pool per project), `my.cnf`; MySQL admin, backup, restore |
| `internal/frontdoor` | `valet` driver (reuse Laravel Valet's nginx + DNS) or `builtin` (Go HTTPS proxy + `.test` DNS server) |
| `internal/ca` | Certificate authority **name-constrained** to `.test`, `localhost` and loopback IPs |
| `internal/secrets` | macOS Keychain (values passed on stdin, never in `ps`); owner-only file elsewhere until native stores land |
| `internal/importers` | Read MAMP PRO / MAMP / XAMPP hosts; copy databases read-only and verify row by row |
| `internal/api` | Local API (one method list shared by the app, the CLI and agents) |
| `internal/runtime` | Finds PHP, Apache, MySQL (Homebrew now; static builds and Linux/Windows packages next) |
| `desktop/` | Wails app: native window using the OS web view; plain HTML/CSS/JS UI, no build step |

## Decisions

- Go: one static binary per OS, easy process management, no runtime to install.
- Wails instead of Electron: it uses the system web view (WebKit/WebView2/WebKitGTK), so the app is about 19 MB instead of 150 MB or more.
- Apache behind a front door: `.htaccess` keeps working for classic PHP apps, and the front door gives clean
  `name.test` URLs and real certificates. On Macs with Valet, both tools share ports 80 and 443.
- One PHP-FPM pool per project: separate logs, temp dirs and settings, so a crash or setting in one project
  doesn't affect another.
- MySQL 8.4 LTS: supported until 2032. `sql_mode` and `lower_case_table_names` match MAMP's 5.7 so imported
  apps behave the same.
- Copy, never move: imports read the source, keep its dump as a backup, verify every table, and only then
  create the project's login. Switching a project to the new server is a separate, reversible choice.

## Roadmap

1. Phase 1 (this one), macOS: engine, CLI, desktop app, Apache/PHP/MySQL 8.4, MAMP PRO importer.
2. Phase 2: Docker mode per project (compose file generated from the same settings), MCP server, Redis & Mailpit add-ons.
3. Phase 3: Windows and Linux runtimes (static PHP builds, packaged Apache/MySQL), built-in front door with OS DNS
   setup, XAMPP/Laragon importers, signed installers and auto-update, public release.
