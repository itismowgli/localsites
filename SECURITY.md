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
  Keychain. They're passed to tools through stdin or an inherited pipe, never as command-line arguments or environment
  variables. The PostgreSQL add-on needs a password over TCP (also in the Keychain); LocalSites itself uses
  its socket, in a folder only you can open. MinIO gets a random password per Mac.
  The first-start password file is `0600` and deleted once MySQL is up.
- **Least privilege in MySQL.** Each project gets its own user with rights to its own database only.
  `LOAD DATA LOCAL` is disabled.
- **Isolation between projects.** One PHP-FPM pool per project with its own temp dir and logs; `clear_env` on.
  Apache denies `.ht*`, `.env` and `.git` files by default.
- **Copying from MAMP is read-only.** Source databases are dumped with `--single-transaction`; nothing is written
  to the source. The dump is kept as a backup.
- **Importing a .sql file can't run anything.** MySQL files load with the database's own login (it can't touch
  other databases or create logins). PostgreSQL files load with psql's own commands off (`\restrict`) and
  without superuser rights. SQLite files load in sqlite3's safe mode. A backup is made first.
- **Sharing a site.** Visitors need a password by default (bore always needs one). The site sees the visitor's
  address, never 127.0.0.1, so "localhost only" pages stay closed. Hidden files (`.env`, `.git`) are never served.
- **Agents.** API methods are labelled `mutates`, `destructive`, `approve` and `sensitive`. See below.

Report security issues privately to the maintainers rather than in public issues.

## AI agents

Risky requests from AI agents (MCP), scripts and other non-interactive callers wait in the app until the person
approves them: anything that stops sites or loses work, exports or imports data, edits a project's `.env`,
sets a start command or environment variables, or changes where projects live, what starts at login, or
how problem reports and updates behave. This protects against an agent acting on its own, for example after
reading a prompt injection in a project file. It is not a barrier against other programs running as you,
which can read the service token like any of your files.
