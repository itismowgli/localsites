# Security model

LocalSites runs development servers on your own computer. The goal is that nothing it starts is reachable from
the network, and that its credentials and certificates can't be misused outside your machine.

- **Loopback only.** Apache (`127.0.0.1:8744`), MySQL (`127.0.0.1:3307`, X Protocol off), the front door and the
  DNS server bind to 127.0.0.1. Nothing listens on your LAN.
- **Control API.** A unix socket in an owner-only directory (`0600`) plus a random bearer token rotated on every
  start. Request bodies are size-limited.
- **Certificates.** The LocalSites root CA carries X.509 name constraints: it can only sign `*.test`, `localhost`
  and loopback IPs. A certificate for any real domain fails verification even if the CA key leaked
  (see `internal/ca/ca_test.go`). Leaf certificates last 397 days.
- **Secrets.** The MySQL root password and per-project passwords are generated randomly and stored in the macOS
  Keychain. They're passed to tools via stdin or `MYSQL_PWD`, never as command-line arguments.
  The first-start password file is `0600` and deleted once MySQL is up.
- **Least privilege in MySQL.** Each project gets its own user with rights to its own database only.
  `LOAD DATA LOCAL` is disabled.
- **Isolation between projects.** One PHP-FPM pool per project with its own temp dir and logs; `clear_env` on.
  Apache denies `.ht*`, `.env` and `.git` files by default.
- **Imports are read-only.** Source databases are dumped with `--single-transaction`; nothing is written to the
  source. The dump is kept as a backup.
- **Agents.** API methods are labelled `mutates`, `destructive` and `sensitive` so agent integrations can require
  confirmation before risky calls.

Report security issues privately to the maintainers rather than in public issues.

## AI agents

Risky requests from AI agents (MCP) wait in the app until the person approves them. This protects against an
agent acting on its own; it is not a barrier against other programs running as you, which can read the
service token like any of your files.
