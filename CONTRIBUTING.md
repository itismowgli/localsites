# Contributing to LocalSites

Thanks for helping. LocalSites is a free, open-source alternative to MAMP PRO: a Go engine, a CLI,
an MCP server for AI agents and a desktop app (Wails, plain JavaScript UI).

## Getting set up

- macOS with Homebrew: `brew install go node httpd php mysql@8.4` (and `postgresql@17` for the Postgres tests)
- Desktop app: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- Build: `make build` (CLI in `bin/`, app in `desktop/build/bin/`)
- UI without the app: `go run ./cmd/devui` and open the printed address

## Before you open a pull request

Run `make test-all`. It runs, in order:

| Command | What it checks |
|---|---|
| `make lint` | gofmt, go vet, JavaScript syntax |
| `make test` | unit tests (seconds, no services) |
| `make e2e` | real Apache, PHP, MySQL and Postgres in a throwaway sandbox |
| `make e2e-ui` | the real UI in WebKit and Chromium against a sandboxed service |

The tests never touch your own sites, Keychain, Valet or MAMP: they use `LOCALSITES_HOME`,
`LOCALSITES_SECRETS=file`, `LOCALSITES_LAUNCHAGENTS_DIR` and random ports. Keep it that way in new tests.

## Rules we hold to

- **Never change user data without an explicit action.** Database connections are read-only by default;
  imports copy and verify, never overwrite; edits are staged and shown as SQL before saving.
- **No secrets in argv, logs or config files.** Passwords go to the system keychain.
- **Plain language in the UI.** Short sentences, no jargon where a common word works.
- **UI code stays inside the single IIFE in `desktop/frontend/dist/app.js`.** Never define globals
  (Wails owns `window.go` and `window.runtime`).
- **Motion:** transform/opacity, 200 ms or less, and respect `prefers-reduced-motion`.
- New features get a test at the right layer, an entry in `internal/version/changelog.json`
  and a row in `docs/PLAN.md`.

## Reporting bugs

Open an issue with the steps to reproduce. A debug bundle (Help → Send debug bundle) helps a lot;
passwords are removed from it, but please look through it before attaching.

Security problems: see [SECURITY.md](SECURITY.md) instead of opening a public issue.

## License

By contributing you agree that your work is released under the MIT license (see LICENSE).
