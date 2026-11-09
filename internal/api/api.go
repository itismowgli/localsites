// Package api is the local control API used by the desktop app, the CLI and AI agents.
//
// Transport: HTTP over a unix socket in the run dir (owner-only permissions, works on
// macOS, Linux and Windows 10+), plus a random token so a socket path leak alone is not enough.
// Every call is POST /v1/<method> with a JSON body and returns JSON.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/agentcfg"
	"github.com/localsites/localsites/internal/apps"
	"github.com/localsites/localsites/internal/dbclient"
	"github.com/localsites/localsites/internal/engine"
	"github.com/localsites/localsites/internal/importers"
	"github.com/localsites/localsites/internal/paths"
	"github.com/localsites/localsites/internal/problems"
	lsruntime "github.com/localsites/localsites/internal/runtime"
	"github.com/localsites/localsites/internal/secrets"
	"github.com/localsites/localsites/internal/version"
)

// Version of the API; bump on breaking changes.
const Version = "1"

// Param is one input of an API method (also used to build MCP tool schemas).
type Param struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // string | boolean | integer
	Description string   `json:"description"`
	Required    bool     `json:"required,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// Method describes one API call (used for help, the CLI, and the MCP tool list).
type Method struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Params      []Param `json:"params,omitempty"`
	Mutates     bool    `json:"mutates"`     // changes config or starts/stops things
	Destructive bool    `json:"destructive"` // can take sites offline or lose work: agents must confirm
	Sensitive   bool    `json:"sensitive"`   // takes or returns secrets
}

var (
	pFolder    = Param{Name: "folder", Type: "string", Description: "Project folder name, as listed by sites.list", Required: true}
	pDB        = Param{Name: "name", Type: "string", Description: "Database name (letters, digits, _)", Required: true}
	pFile      = Param{Name: "file", Type: "string", Description: "Full path of a .sql, .sql.gz or .zip file", Required: true}
	pMode      = Param{Name: "mode", Type: "string", Description: "full (default), structure or data", Enum: []string{"full", "structure", "data"}}
	pOverwrite = Param{Name: "overwrite", Type: "boolean", Description: "Replace an existing file"}
	pNewName   = Param{Name: "new_name", Type: "string", Description: "Name of the new database", Required: true}
	pSource    = Param{Name: "source", Type: "string", Description: "Tool to import from", Required: true, Enum: []string{"mamp-pro", "mamp", "xampp"}}
	pSrcUser   = Param{Name: "user", Type: "string", Description: "MySQL user of the source tool (usually root)", Required: true}
	pSrcPass   = Param{Name: "password", Type: "string", Description: "MySQL password of the source tool (not stored)", Required: true}
)

var Methods = []Method{
	{Name: "status", Description: "Services, PHP versions, front door and import sources"},
	{Name: "sites.list", Description: "Every project folder with its address, PHP version, web root, database server and state"},
	{Name: "sites.update", Description: "Change a project's settings and apply them. Only the given fields change.", Mutates: true, Params: []Param{
		pFolder,
		{Name: "enabled", Type: "boolean", Description: "Switch the project on (serve it) or off"},
		{Name: "domain", Type: "string", Description: "Address, e.g. shop.test"},
		{Name: "php", Type: "string", Description: "PHP version X.Y, one of the installed versions in status.php"},
		{Name: "docroot", Type: "string", Description: "Web root relative to the project folder, e.g. public; empty for the folder itself"},
		{Name: "db_server", Type: "string", Description: "MySQL server that \"localhost\" connects to", Enum: []string{"localsites", "mamp"}},
		{Name: "database", Type: "string", Description: "Database owned by this project"},
		{Name: "kind", Type: "string", Description: "Override the detected type (empty = detect)", Enum: []string{"", "php", "laravel", "wordpress", "static", "nextjs", "node", "command", "docker", "other"}},
		{Name: "port", Type: "integer", Description: "Docker projects: the port the container publishes on 127.0.0.1"},
		{Name: "xdebug", Type: "boolean", Description: "Step debugging for this PHP project (Xdebug on port 9003)"},
		{Name: "web_server", Type: "string", Description: "Web server for PHP projects", Enum: []string{"apache", "nginx"}},
		{Name: "env_vars", Type: "object", Description: "Environment variables for the dev server (replaces the whole set; the project's .env file is not changed)"},
		{Name: "php_ini", Type: "object", Description: "php.ini overrides for this project only (replaces the whole set), e.g. {\"memory_limit\": \"512M\"}"},
		{Name: "command", Type: "string", Description: "Start command for custom projects; {port} is replaced, e.g. python manage.py runserver {port}"},
	}},
	{Name: "sites.context", Description: "Everything an agent needs about one project in one call: stack, address, PHP and web server, database, .env setting names (no values), git, problems and recent errors", Params: []Param{pFolder}},
	{Name: "sites.takeover", Description: "Serve a project that Laravel Valet serves with LocalSites instead, at the same address (Valet's own config is not changed; switching off gives it back)", Mutates: true, Params: []Param{pFolder}},
	{Name: "sites.create", Description: "Create a new project folder with a starter PHP page (switched off until enabled)", Mutates: true,
		Params: []Param{{Name: "folder", Type: "string", Description: "New folder name (letters, numbers, - and _)", Required: true}}},
	{Name: "version", Description: "LocalSites version and the What's new feed"},
	{Name: "settings.get", Description: "All settings: projects folder, domains and ports, PHP defaults, MySQL, start at login"},
	{Name: "settings.update", Description: "Change settings and apply them. Only the given fields change; errors name the field.", Mutates: true, Params: []Param{
		{Name: "sites_root", Type: "string", Description: "Folder that holds your projects"},
		{Name: "front_door", Type: "string", Description: "How *.test addresses are served", Enum: []string{"auto", "valet", "builtin"}},
		{Name: "apache_port", Type: "integer", Description: "Internal Apache port on 127.0.0.1"},
		{Name: "mysql_port", Type: "integer", Description: "LocalSites MySQL port on 127.0.0.1"},
		{Name: "http_port", Type: "integer", Description: "Built-in front door HTTP port"},
		{Name: "https_port", Type: "integer", Description: "Built-in front door HTTPS port"},
		{Name: "dns_port", Type: "integer", Description: "Built-in .test DNS port"},
		{Name: "default_php", Type: "string", Description: "PHP version for new projects (empty = newest)"},
		{Name: "start_at_login", Type: "boolean", Description: "Start LocalSites when you log in (macOS)"},
	}},
	{Name: "sites.connect_db", Description: "Point a project at a LocalSites database: backs up .env, writes the database's own login, switches MySQL to LocalSites", Mutates: true,
		Params: []Param{pFolder, {Name: "name", Type: "string", Description: "LocalSites database name", Required: true}}},
	{Name: "sites.restore_env", Description: "Undo sites.connect_db: restore the last .env backup and switch back to MAMP's MySQL", Mutates: true, Params: []Param{pFolder}},
	{Name: "data.connections", Description: "Database browser connections (MAMP PRO MySQL, LocalSites MySQL, saved MySQL/PostgreSQL/SQLite)"},
	{Name: "data.connection.save", Description: "Add or change a saved connection: {connection, password?}", Mutates: true, Sensitive: true},
	{Name: "data.connection.delete", Description: "Remove a saved connection (the database itself is not touched)", Mutates: true, Params: []Param{{Name: "id", Type: "string", Description: "Connection id", Required: true}}},
	{Name: "data.password", Description: "Remember the password for a connection: {id, password}", Mutates: true, Sensitive: true},
	{Name: "data.readonly", Description: "Turn writing off (read_only=true) or on for a connection", Mutates: true, Destructive: true,
		Params: []Param{{Name: "id", Type: "string", Description: "Connection id", Required: true}, {Name: "read_only", Type: "boolean", Description: "true = block all changes", Required: true}}},
	{Name: "data.test", Description: "Check a connection works and show the server version", Params: []Param{{Name: "id", Type: "string", Description: "Connection id", Required: true}}},
	{Name: "data.schemas", Description: "Databases (MySQL) or schemas (PostgreSQL) on a connection", Params: []Param{{Name: "id", Type: "string", Description: "Connection id", Required: true}}},
	{Name: "data.tables", Description: "Tables and views with row estimates and sizes", Params: []Param{{Name: "id", Type: "string", Description: "Connection id", Required: true}, {Name: "schema", Type: "string", Description: "Database or schema", Required: true}}},
	{Name: "data.structure", Description: "Columns and indexes of a table", Params: []Param{{Name: "id", Type: "string", Description: "Connection id", Required: true}, {Name: "schema", Type: "string", Description: "Database or schema", Required: true}, {Name: "table", Type: "string", Description: "Table", Required: true}}},
	{Name: "data.rows", Description: "Rows of a table, paged, sorted and filtered (read-only)", Sensitive: true},
	{Name: "data.query", Description: "Run one SQL statement. Read-only unless the connection allows changes and allow_write is true", Mutates: true, Destructive: true, Sensitive: true},
	{Name: "data.preview", Description: "Show the SQL that saving staged row edits would run", Sensitive: true},
	{Name: "data.save", Description: "Save staged row edits in one transaction (each update/delete must match exactly one row)", Mutates: true, Destructive: true, Sensitive: true},
	{Name: "git.status", Description: "A project's branch, upstream, ahead/behind and changed files", Params: []Param{pFolder}},
	{Name: "git.hosting", Description: "Open pull requests and recent CI runs for the project's current branch (GitHub, through the gh CLI's login)", Params: []Param{pFolder}},
	{Name: "git.branches", Description: "Local and remote branches of a project", Params: []Param{pFolder}},
	{Name: "git.log", Description: "Commit graph of a project (all branches)", Params: []Param{pFolder, {Name: "lines", Type: "integer", Description: "How many commits (default 80)"}}},
	{Name: "git.fetch", Description: "Fetch from all remotes (never changes files)", Mutates: true, Params: []Param{pFolder}},
	{Name: "git.pull", Description: "Fast-forward the current branch (refused with uncommitted changes or diverged branches)", Mutates: true, Params: []Param{pFolder}},
	{Name: "git.checkout", Description: "Switch branch (refused with uncommitted changes)", Mutates: true, Params: []Param{pFolder, {Name: "name", Type: "string", Description: "Branch, e.g. main or origin/feature", Required: true}}},
	{Name: "git.branch", Description: "Create a branch from the current commit and switch to it", Mutates: true, Params: []Param{pFolder, {Name: "name", Type: "string", Description: "New branch name", Required: true}}},
	{Name: "share.list", Description: "Projects currently shared on the internet, with their public address and expiry"},
	{Name: "share.start", Description: "Put a running project on the internet through a free tunnel (Cloudflare quick tunnel or bore) for a limited time. Anyone with the address can open it; a password is recommended", Mutates: true, Destructive: true, Params: []Param{pFolder,
		{Name: "provider", Type: "string", Description: "Tunnel", Enum: []string{"cloudflared", "bore"}},
		{Name: "password", Type: "string", Description: "Visitors must enter this password (any user name). Required unless no_password is true"},
		{Name: "no_password", Type: "boolean", Description: "Share without a password: anyone with the address can open it"},
		{Name: "minutes", Type: "integer", Description: "Stop sharing after this many minutes (default 60, max 1440)"}}},
	{Name: "share.stop", Description: "Stop sharing (the public address stops working immediately)", Mutates: true, Params: []Param{{Name: "id", Type: "string", Description: "Share id from share.list", Required: true}}},
	{Name: "share.install", Description: "Install a tunnel client with Homebrew (brew install cloudflared or bore-cli)", Mutates: true, Params: []Param{{Name: "provider", Type: "string", Required: true, Enum: []string{"cloudflared", "bore"}}}},
	{Name: "addons.list", Description: "Add-on services (Mailpit, Valkey, Redis, Memcached, PostgreSQL, Meilisearch, MinIO): installed, running, ports, and settings for Laravel, Node/Next.js, Django, Rails or any language"},
	{Name: "addons.update", Description: "Switch an add-on on or off, or change its port and options, then apply", Mutates: true, Params: []Param{
		{Name: "id", Type: "string", Description: "Add-on id", Required: true, Enum: []string{"mailpit", "valkey", "redis", "memcached", "postgres", "meilisearch", "minio"}},
		{Name: "enabled", Type: "boolean", Description: "Run it"},
		{Name: "port", Type: "integer", Description: "Port on 127.0.0.1 (SMTP port for Mailpit)"},
		{Name: "ui_port", Type: "integer", Description: "Mailpit inbox port"},
		{Name: "keep_mail", Type: "boolean", Description: "Mailpit: keep emails after a restart"},
		{Name: "persist", Type: "boolean", Description: "Valkey/Redis: save data to disk"},
		{Name: "memory_mb", Type: "integer", Description: "Memcached memory limit in MB"}}},
	{Name: "addons.install", Description: "Install an add-on with Homebrew in the background (brew install <formula>)", Mutates: true, Params: []Param{
		{Name: "id", Type: "string", Description: "Add-on id", Required: true, Enum: []string{"mailpit", "valkey", "redis", "memcached", "postgres", "meilisearch", "minio"}}}},
	{Name: "addons.test_mail", Description: "Send a test email through Mailpit", Mutates: true},
	{Name: "addons.flush", Description: "Empty a cache add-on (Valkey, Redis or Memcached). Cached data is lost", Mutates: true, Destructive: true, Params: []Param{
		{Name: "id", Type: "string", Description: "Add-on id", Required: true, Enum: []string{"valkey", "redis", "memcached"}}}},
	{Name: "activity", Description: "Recent changes made through the app, CLI and AI agents (newest first)"},
	{Name: "sites.install", Description: "Install a project's dependencies in the background (composer or npm/pnpm/yarn/bun)", Mutates: true, Params: []Param{pFolder}},
	{Name: "sites.check", Description: "Test one project end to end: DNS, PHP pool, database socket, HTTPS response and certificate trust", Params: []Param{pFolder}},
	{Name: "doctor", Description: "Full health check of LocalSites with a fix for each problem"},
	{Name: "logs", Description: "Last lines of a log", Params: []Param{
		{Name: "kind", Type: "string", Description: "Which log", Required: true, Enum: []string{"apache", "mysql", "site-error", "site-access", "site-php", "site-app", "site-install", "addon", "addon-install"}},
		{Name: "folder", Type: "string", Description: "Project folder (for site-* logs) or add-on id (for addon logs)"},
		{Name: "lines", Type: "integer", Description: "How many lines (default 300, max 5000)"},
	}},
	{Name: "apply", Description: "Regenerate configs and (re)start services as needed", Mutates: true},
	{Name: "restart", Description: "Stop and start Apache, PHP and MySQL (sites blink offline)", Mutates: true, Destructive: true},
	{Name: "stop", Description: "Stop all services (sites go offline)", Mutates: true, Destructive: true},
	{Name: "db.list", Description: "Databases in LocalSites MySQL 8.4 with sizes"},
	{Name: "db.create", Description: "Create a database with its own login limited to that database", Mutates: true, Params: []Param{pDB}},
	{Name: "db.backup", Description: "Write a compressed backup of a database and return its file path", Mutates: true, Params: []Param{pDB}},
	{Name: "db.credentials", Description: "Login (including password) for a project database", Sensitive: true, Params: []Param{pDB}},
	{Name: "db.export", Description: "Export a LocalSites MySQL database to a .sql or .sql.gz file (mode: full, structure or data)", Mutates: true, Params: []Param{pDB, pFile, pMode, pOverwrite}},
	{Name: "db.import", Description: "Load a .sql, .sql.gz or .zip file into a LocalSites MySQL database (a backup is made first)", Mutates: true, Destructive: true, Params: []Param{pDB, pFile}},
	{Name: "db.backups", Description: "Saved backups of a LocalSites MySQL database, newest first", Params: []Param{pDB}},
	{Name: "db.restore", Description: "Replace a database with one of its backups (what is there now is backed up first)", Mutates: true, Destructive: true, Params: []Param{pDB, pFile}},
	{Name: "db.duplicate", Description: "Copy a database (structure and data) into a new one with its own login", Mutates: true, Params: []Param{pDB, pNewName}},
	{Name: "db.empty", Description: "Remove every table from a database (backed up first); the database and login stay", Mutates: true, Destructive: true, Params: []Param{pDB}},
	{Name: "db.drop", Description: "Delete a database and its login (backed up first)", Mutates: true, Destructive: true, Params: []Param{pDB}},
	{Name: "pg.list", Description: "Databases in the PostgreSQL add-on with sizes"},
	{Name: "pg.create", Description: "Create a database in the PostgreSQL add-on", Mutates: true, Params: []Param{pDB}},
	{Name: "pg.export", Description: "Export a PostgreSQL add-on database to a .sql or .sql.gz file", Mutates: true, Params: []Param{pDB, pFile, pMode, pOverwrite}},
	{Name: "pg.import", Description: "Load a .sql, .sql.gz or .zip file into a PostgreSQL add-on database (backed up first)", Mutates: true, Destructive: true, Params: []Param{pDB, pFile}},
	{Name: "pg.duplicate", Description: "Copy a PostgreSQL add-on database into a new one", Mutates: true, Params: []Param{pDB, pNewName}},
	{Name: "pg.empty", Description: "Remove every table from a PostgreSQL add-on database (backed up first)", Mutates: true, Destructive: true, Params: []Param{pDB}},
	{Name: "pg.drop", Description: "Delete a PostgreSQL add-on database (backed up first)", Mutates: true, Destructive: true, Params: []Param{pDB}},
	{Name: "data.export", Description: "Export a database (MySQL), schema (PostgreSQL) or SQLite file from a saved connection to a .sql or .sql.gz file. Only reads", Mutates: true, Params: []Param{
		{Name: "id", Type: "string", Description: "Connection id", Required: true}, {Name: "schema", Type: "string"}, pFile, pMode, pOverwrite}},
	{Name: "data.import", Description: "Load a .sql, .sql.gz or .zip file through a saved connection that allows changes (what is there is exported first)", Mutates: true, Destructive: true, Params: []Param{
		{Name: "id", Type: "string", Description: "Connection id", Required: true}, {Name: "schema", Type: "string"}, pFile}},
	{Name: "import.sources", Description: "Other local tools found on this computer (MAMP PRO, MAMP, XAMPP)"},
	{Name: "import.databases", Description: "List databases in another tool's MySQL", Sensitive: true, Params: []Param{pSource, pSrcUser, pSrcPass}},
	{Name: "import.database", Description: "Copy one database read-only from another tool into LocalSites and verify every row. Never overwrites.", Mutates: true, Sensitive: true,
		Params: []Param{pSource, pSrcUser, pSrcPass, pDB}},
	{Name: "debug.bundle", Description: "Zip logs, generated configs and a doctor report (secrets removed) for a bug report; returns the file path", Mutates: true},
	{Name: "open", Description: "Open a project in an app on this computer", Params: []Param{pFolder,
		{Name: "app", Type: "string", Description: "App id from apps.list (editor, terminal, git app, browser), or \"browser\" for the default browser", Required: true}}},
	{Name: "tools.adminer", Description: "Switch Adminer (web database tool, like phpMyAdmin) on or off at https://adminer.test. The first time, a pinned release is downloaded and checked", Mutates: true, Params: []Param{{Name: "enabled", Type: "boolean", Required: true}}},
	{Name: "mysql.root_password", Description: "Change the LocalSites MySQL root password (random and strong if none is given); kept in the keychain", Mutates: true, Destructive: true, Sensitive: true,
		Params: []Param{{Name: "password", Type: "string", Description: "New password (optional, at least 12 characters)"}}},
	{Name: "version.check", Description: "Check GitHub for a newer LocalSites release (reads only; nothing is installed)"},
	{Name: "update.status", Description: "Where an app update is: up to date, available (with signed release notes), downloading, ready, installing; and the version kept for switching back"},
	{Name: "update.check", Description: "Look for a newer signed release now (reads the public release list only; works even with automatic checks off)"},
	{Name: "update.download", Description: "Download, verify and unpack the available update in the background (nothing is installed yet)", Mutates: true},
	{Name: "update.install", Description: "Install the downloaded update: LocalSites quits, the app is swapped (the current one is kept for switching back) and it starts again. The app only", Mutates: true, Destructive: true, Params: []Param{
		{Name: "app_path", Type: "string", Description: "The running app's executable", Required: true}, {Name: "app_pid", Type: "integer", Required: true},
		{Name: "then", Type: "string", Required: true, Enum: []string{"open", "open-bg", "service"}}}},
	{Name: "update.rollback", Description: "Switch to the version kept from before the last update (same steps as update.install). The app only", Mutates: true, Destructive: true, Params: []Param{
		{Name: "app_path", Type: "string", Required: true}, {Name: "app_pid", Type: "integer", Required: true},
		{Name: "then", Type: "string", Required: true, Enum: []string{"open", "open-bg", "service"}}}},
	{Name: "problems.list", Description: "Things that went wrong on this Mac (crashes, services that stopped, failed updates and installs, app errors), newest first, and whether reports can be sent"},
	{Name: "problems.preview", Description: "Exactly what a problem report would contain, cleaned (no passwords, tokens, e-mails, user name; project names hashed)", Params: []Param{
		{Name: "id", Type: "string", Required: true}, {Name: "bundle", Type: "boolean", Description: "Include a cleaned debug bundle"}}},
	{Name: "problems.send", Description: "Send the cleaned report (the app only, after the person reviewed it)", Mutates: true, Params: []Param{
		{Name: "id", Type: "string", Required: true}, {Name: "bundle", Type: "boolean"}}},
	{Name: "problems.dismiss", Description: "Remove a problem from the list (all when id is empty)", Mutates: true, Params: []Param{{Name: "id", Type: "string"}}},
	{Name: "problems.ui", Description: "Record an error in the app's page (the app only)", Params: []Param{{Name: "message", Type: "string", Required: true}}},
	{Name: "storage.list", Description: "What LocalSites keeps on disk (databases, add-on data, backups, logs, .env backups) and the clean-ups available"},
	{Name: "storage.clean", Description: "Run one clean-up from storage.list (removes only files LocalSites created)", Mutates: true, Destructive: true, Params: []Param{{Name: "id", Type: "string", Required: true}}},
	{Name: "php.info", Description: "Each installed PHP version's extensions and whether Xdebug is installed"},
	{Name: "agents.list", Description: "AI agent apps on this Mac (Claude Desktop, Claude Code, Cursor, VS Code, Windsurf, Zed) and whether LocalSites is connected to each"},
	{Name: "agents.connect", Description: "Add LocalSites to an agent app's MCP settings (backs up the file first; only the localsites entry changes)", Mutates: true, Params: []Param{{Name: "id", Type: "string", Required: true}}},
	{Name: "agents.disconnect", Description: "Remove LocalSites from an agent app's MCP settings", Mutates: true, Params: []Param{{Name: "id", Type: "string", Required: true}}},
	{Name: "approvals.list", Description: "Requests from AI agents waiting for the user's approval in the app"},
	{Name: "approvals.decide", Description: "Approve or deny a waiting request (the app only; agents can't)", Params: []Param{{Name: "id", Type: "string", Required: true}, {Name: "approve", Type: "boolean", Required: true}}},
	{Name: "apps.list", Description: "Editors, terminals, git apps, database apps and browsers installed on this Mac (for open)"},
	{Name: "db.open_in", Description: "Open a LocalSites database in TablePlus or Sequel Ace (host, port, user and database filled in; the password is not passed on the command line)", Params: []Param{pDB,
		{Name: "app", Type: "string", Required: true, Enum: []string{"tableplus", "sequelace"}}}},
	{Name: "quit", Description: "Stop all services and the LocalSites background service", Mutates: true, Destructive: true},
	{Name: "events.wait", Description: "Wait until something changes (services, projects, settings, shares, agent requests, updates), up to 25 seconds; returns the new version. Pass the version you have as since", Params: []Param{
		{Name: "since", Type: "integer", Description: "Version from the last call (0 the first time)"}}},
	{Name: "methods", Description: "This list"},
}

// FindMethod returns the method description by name.
func FindMethod(name string) (Method, bool) {
	for _, m := range Methods {
		if m.Name == name {
			return m, true
		}
	}
	return Method{}, false
}

/* ----------------------------------------------------------------- server */

type Server struct {
	E        *engine.Engine
	activity activityLog
	Quit     chan struct{} // closed when a client asks the service to shut down
	token    string
	srv      *http.Server
	pending  approvals
}

func socketPath(p paths.Paths) string { return p.Socket("api") }
func tokenPath(p paths.Paths) string  { return filepath.Join(p.Run, "api.token") }

// Listen starts serving on the socket (removing a stale one).
func (s *Server) Listen() error {
	p := s.E.P
	if c, err := net.DialTimeout("unix", socketPath(p), 300*time.Millisecond); err == nil {
		c.Close()
		return errors.New("LocalSites is already running")
	}
	_ = os.Remove(socketPath(p))
	s.pending.onChange = s.E.Changes.Bump
	s.token = secrets.Generate(40)
	if err := os.WriteFile(tokenPath(p), []byte(s.token), 0o600); err != nil {
		return err
	}
	ln, err := net.Listen("unix", socketPath(p))
	if err != nil {
		return err
	}
	_ = os.Chmod(socketPath(p), 0o600)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/{method}", s.handle)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go s.srv.Serve(ln)
	return nil
}

func (s *Server) Close() {
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(ctx)
	}
	_ = os.Remove(socketPath(s.E.P))
	_ = os.Remove(tokenPath(s.E.P))
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	method := r.PathValue("method")
	client := r.Header.Get("X-LocalSites-Client")
	start := time.Now()
	var res any
	var err error
	switch {
	case method == "events.wait":
		var w struct {
			Since uint64 `json:"since"`
		}
		_ = json.Unmarshal(body, &w)
		res = map[string]uint64{"version": s.E.Changes.Wait(r.Context().Done(), w.Since, 25*time.Second)}
	case method == "approvals.list":
		res = s.pending.list()
	case method == "approvals.decide":
		if client == "mcp" {
			err = errors.New("only the person using LocalSites can approve requests")
			break
		}
		var d struct {
			ID      string `json:"id"`
			Approve bool   `json:"approve"`
		}
		_ = json.Unmarshal(body, &d)
		err = s.pending.decide(d.ID, d.Approve)
		res = map[string]any{"ok": err == nil}
	case client == "mcp" && (method == "update.install" || method == "update.rollback"):
		err = errors.New("only the person using LocalSites can install updates, from the app")
	case client == "mcp" && (method == "problems.send" || method == "problems.ui"):
		err = errors.New("only the person using LocalSites can send problem reports, from the app")
	case client == "mcp" && isDestructive(method):
		if err = s.pending.wait(method, client, body, r.Context().Done()); err == nil {
			res, err = s.call(method, body)
		}
	default:
		res, err = s.call(method, body)
	}
	if m, ok := FindMethod(method); ok && m.Mutates {
		s.E.Changes.Bump() // tell waiting clients (the app, the menu bar) to refresh
	}
	s.record(client, method, body, err, time.Since(start))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		out := map[string]any{"error": err.Error()}
		var fe engine.FieldErrors
		if errors.As(err, &fe) {
			out["fields"] = fe // lets the UI show each message next to its field
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	_ = json.NewEncoder(w).Encode(res)
}

type args struct {
	Folder     string `json:"folder"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Lines      int    `json:"lines"`
	Source     string `json:"source"`
	User       string `json:"user"`
	Password   string `json:"password"`
	App        string `json:"app"`
	ID         string `json:"id"`
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	Provider   string `json:"provider"`
	Minutes    int    `json:"minutes"`
	NoPassword bool   `json:"no_password"`
	AppPath    string `json:"app_path"`
	File       string `json:"file"`
	Mode       string `json:"mode"`
	Overwrite  bool   `json:"overwrite"`
	NewName    string `json:"new_name"`
	AppPID     int    `json:"app_pid"`
	Then       string `json:"then"`
	Bundle     bool   `json:"bundle"`
	Message    string `json:"message"`
	engine.SitePatch
}

func (s *Server) call(method string, body []byte) (any, error) {
	var a args
	if len(body) > 0 {
		if err := json.Unmarshal(body, &a); err != nil {
			return nil, fmt.Errorf("bad JSON: %w", err)
		}
	}
	e := s.E
	switch method {
	case "methods":
		return Methods, nil
	case "doctor":
		return e.Doctor(), nil
	case "sites.check":
		return e.CheckSite(a.Folder), nil
	case "debug.bundle":
		f, err := e.DebugBundle()
		return map[string]string{"file": f}, err
	case "quit":
		if s.Quit != nil {
			go func() { time.Sleep(200 * time.Millisecond); close(s.Quit) }()
		}
		return map[string]string{"status": "stopping"}, nil
	case "status":
		return e.Status(), nil
	case "sites.list":
		return e.Sites(), nil
	case "sites.create":
		return e.CreateSite(a.Folder)
	case "sites.install":
		log, err := e.InstallDeps(a.Folder)
		return map[string]string{"log": log}, err
	case "activity":
		return s.activity.list(), nil
	case "data.connections":
		return e.Data.List(), nil
	case "data.connection.save":
		var p struct {
			Connection dbclient.Conn `json:"connection"`
			Password   *string       `json:"password"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("bad JSON: %w", err)
		}
		return e.Data.Save(p.Connection, p.Password)
	case "data.connection.delete":
		return map[string]bool{"ok": true}, e.Data.Delete(a.ID)
	case "data.password":
		return map[string]bool{"ok": true}, e.Data.SetPassword(a.ID, a.Password)
	case "data.readonly":
		var p struct {
			ID       string `json:"id"`
			ReadOnly bool   `json:"read_only"`
		}
		_ = json.Unmarshal(body, &p)
		return map[string]bool{"ok": true}, e.Data.SetReadOnly(p.ID, p.ReadOnly)
	case "data.test":
		return e.Data.Test(a.ID)
	case "data.schemas":
		return e.Data.Schemas(a.ID)
	case "data.tables":
		return e.Data.Tables(a.ID, a.Schema)
	case "data.structure":
		return e.Data.Structure(a.ID, a.Schema, a.Table)
	case "data.rows":
		var r dbclient.RowsRequest
		_ = json.Unmarshal(body, &r)
		return e.Data.Rows(a.ID, r)
	case "share.list":
		return e.Shares.List(), nil
	case "share.start":
		if a.Provider == "" {
			a.Provider = "cloudflared"
		}
		return e.StartShare(a.Folder, a.Provider, a.Password, a.Minutes, a.NoPassword)
	case "share.stop":
		if !e.Shares.Stop(a.ID) {
			return nil, errors.New("that share is not running")
		}
		return map[string]any{"log": []string{"Stopped sharing"}}, nil
	case "share.install":
		logf, err := e.InstallShareProvider(a.Provider)
		return map[string]any{"log": []string{"Installing… progress: " + logf}}, err
	case "addons.list":
		return e.Addons(), nil
	case "addons.update":
		var p engine.AddonPatch
		_ = json.Unmarshal(body, &p)
		if err := e.SetAddon(a.ID, p); err != nil {
			return nil, err
		}
		return map[string]any{"log": e.Apply(), "addons": e.Addons()}, nil
	case "addons.install":
		logf, err := e.InstallAddon(a.ID)
		return map[string]any{"log": []string{"Installing… progress: " + logf}, "addons": e.Addons()}, err
	case "addons.test_mail":
		msg, err := e.AddonTestMail()
		return map[string]any{"log": []string{msg}}, err
	case "addons.flush":
		msg, err := e.AddonFlush(a.ID)
		return map[string]any{"log": []string{msg}}, err
	case "data.preview", "data.save":
		var ch dbclient.Changes
		if err := json.Unmarshal(body, &ch); err != nil {
			return nil, fmt.Errorf("bad JSON: %w", err)
		}
		if method == "data.preview" {
			return e.Data.Preview(a.ID, ch)
		}
		return e.Data.SaveChanges(a.ID, ch)
	case "git.hosting", "git.status", "git.branches", "git.log", "git.fetch", "git.pull", "git.checkout", "git.branch":
		return e.Git(method, a.Folder, a.Name, a.Lines)
	case "data.query":
		var r dbclient.QueryRequest
		_ = json.Unmarshal(body, &r)
		return e.Data.Query(a.ID, r)
	case "version":
		return map[string]any{"version": version.Version, "changelog": version.Changelog()}, nil
	case "settings.get":
		return e.Settings(), nil
	case "settings.update":
		var p engine.SettingsPatch
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("bad JSON: %w", err)
		}
		log, err := e.UpdateSettings(p)
		if err != nil {
			return nil, err
		}
		return map[string]any{"log": log, "settings": e.Settings()}, nil
	case "sites.connect_db":
		log, err := e.ConnectDatabase(a.Folder, a.Name)
		return map[string]any{"log": log, "sites": e.Sites()}, err
	case "sites.restore_env":
		log, err := e.RestoreEnv(a.Folder)
		return map[string]any{"log": log, "sites": e.Sites()}, err
	case "sites.context":
		return e.SiteContext(a.Folder)
	case "sites.takeover":
		log, err := e.TakeOver(a.Folder)
		return map[string]any{"log": log, "sites": e.Sites()}, err
	case "sites.update":
		if err := e.UpdateSite(a.Folder, a.SitePatch); err != nil {
			return nil, err
		}
		return map[string]any{"log": e.Apply(), "sites": e.Sites()}, nil
	case "apply":
		return map[string]any{"log": e.Apply()}, nil
	case "stop":
		return map[string]any{"log": e.StopAll()}, nil
	case "restart":
		return map[string]any{"log": append(e.StopAll(), e.Apply()...)}, nil
	case "logs":
		if a.Lines <= 0 || a.Lines > 5000 {
			a.Lines = 300
		}
		text, err := e.Log(a.Kind, a.Folder, a.Lines)
		return map[string]string{"text": text}, err
	case "db.list":
		return e.Databases()
	case "db.create":
		c, err := e.CreateDatabase(a.Name)
		c.Password = ""
		return c, err
	case "db.credentials":
		return e.DatabaseCredentials(a.Name)
	case "db.backup":
		f, err := e.Backup(a.Name)
		return map[string]string{"file": f}, err
	case "db.export":
		return e.ExportDatabase(a.Name, a.File, a.Mode, a.Overwrite)
	case "db.import":
		log, err := e.ImportFile(a.Name, a.File)
		return map[string]any{"log": log}, err
	case "db.backups":
		return e.Backups(a.Name)
	case "db.restore":
		log, err := e.RestoreBackup(a.Name, a.File)
		return map[string]any{"log": log}, err
	case "db.duplicate":
		log, err := e.DuplicateDatabase(a.Name, a.NewName)
		return map[string]any{"log": log}, err
	case "db.empty":
		log, err := e.EmptyDatabase(a.Name)
		return map[string]any{"log": log}, err
	case "db.drop":
		log, err := e.DropDatabase(a.Name)
		return map[string]any{"log": log}, err
	case "pg.list":
		return e.PGDatabases()
	case "pg.create":
		log, err := e.PGCreate(a.Name)
		return map[string]any{"log": log}, err
	case "pg.export":
		return e.PGExport(a.Name, a.File, a.Mode, a.Overwrite)
	case "pg.import":
		log, err := e.PGImport(a.Name, a.File)
		return map[string]any{"log": log}, err
	case "pg.duplicate":
		log, err := e.PGDuplicate(a.Name, a.NewName)
		return map[string]any{"log": log}, err
	case "pg.empty":
		log, err := e.PGEmpty(a.Name)
		return map[string]any{"log": log}, err
	case "pg.drop":
		log, err := e.PGDrop(a.Name)
		return map[string]any{"log": log}, err
	case "data.export":
		return e.ExportConnection(a.ID, a.Schema, a.File, a.Mode, a.Overwrite)
	case "data.import":
		log, err := e.ImportConnection(a.ID, a.Schema, a.File)
		return map[string]any{"log": log}, err
	case "import.sources":
		return importers.Sources(), nil
	case "import.databases":
		src, ok := importers.FindSource(a.Source)
		if !ok {
			return nil, errors.New("unknown source")
		}
		db, err := src.Connect(a.User, a.Password)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		return listSourceDBs(db)
	case "import.database":
		return e.ImportDatabase(a.Source, a.User, a.Password, a.Name)
	case "tools.adminer":
		on := a.Enabled != nil && *a.Enabled
		log, err := e.SetAdminer(on)
		return map[string]any{"log": log, "adminer": e.Adminer()}, err
	case "mysql.root_password":
		pw, err := e.SetRootPassword(a.Password)
		return map[string]any{"password": pw, "log": []string{"Root password changed and saved in the keychain"}}, err
	case "version.check":
		return version.Check(), nil
	case "update.status":
		return e.UpdateState(), nil
	case "update.check":
		return e.CheckUpdate(), nil
	case "update.download":
		return e.DownloadUpdate()
	case "update.install", "update.rollback":
		if err := e.InstallUpdate(a.AppPath, a.AppPID, a.Then, method == "update.rollback"); err != nil {
			return nil, err
		}
		if s.Quit != nil { // the helper waits for this service to stop before swapping the app
			go func() { time.Sleep(300 * time.Millisecond); close(s.Quit) }()
		}
		return map[string]string{"status": "installing"}, nil
	case "problems.list":
		return e.Problems(), nil
	case "problems.preview":
		return e.PreviewProblem(a.ID, a.Bundle, false)
	case "problems.send":
		id, err := e.SendProblem(a.ID, a.Bundle)
		return map[string]any{"event_id": id, "log": []string{"Report sent. Thank you!"}}, err
	case "problems.dismiss":
		e.DismissProblem(a.ID)
		return map[string]bool{"ok": true}, nil
	case "problems.ui":
		msg := strings.TrimSpace(a.Message)
		title := strings.TrimPrefix(strings.TrimPrefix(msg, "[ui] error: "), "[ui] promise: ")
		e.AddProblem(problems.KindUI, "App error: "+title, msg)
		return map[string]bool{"ok": true}, nil
	case "storage.list":
		return e.Storage(), nil
	case "storage.clean":
		msg, err := e.CleanStorage(a.ID)
		return map[string]any{"log": []string{msg}}, err
	case "php.info":
		var out []lsruntime.PHPInfo
		for _, v := range lsruntime.PHPVersions() {
			out = append(out, lsruntime.Info(v))
		}
		return out, nil
	case "agents.list":
		return agentcfg.List(mcpCommand()), nil
	case "agents.connect":
		added, err := agentcfg.Connect(a.ID, mcpCommand())
		return map[string]any{"added": added, "log": []string{"Connected. Restart the agent app to pick it up."}}, err
	case "agents.disconnect":
		return map[string]any{"log": []string{"Disconnected"}}, agentcfg.Disconnect(a.ID)
	case "apps.list":
		return apps.Installed(), nil
	case "db.open_in":
		return map[string]any{"log": []string{"Opened " + a.Name}}, openDatabaseIn(e, a.Name, a.App)
	case "open":
		return nil, openProject(e, a.Folder, a.App)
	}
	return nil, fmt.Errorf("unknown method %q (see methods)", method)
}

func openProject(e *engine.Engine, folder, app string) error {
	var site *engine.SiteView
	for _, s := range e.Sites() {
		if s.Folder == folder {
			cp := s
			site = &cp
		}
	}
	if site == nil {
		return errors.New("unknown project")
	}
	if app == "browser" {
		if site.URL == "" { // switched off, and not served by Valet either
			return errors.New("project is switched off")
		}
		return openURL(site.URL)
	}
	if runtime.GOOS != "darwin" {
		return errors.New("opening apps is only supported on macOS for now")
	}
	a, err := apps.Find(app)
	if err != nil {
		return err
	}
	switch a.Kind {
	case "browser":
		if site.URL == "" {
			return errors.New("project is switched off")
		}
		return apps.Open(a, site.URL)
	case "database":
		return errors.New("open a database from Databases instead")
	}
	return apps.Open(a, site.Path)
}

// openDatabaseIn opens a LocalSites database in a database app, without the password (it never goes on a command line).
func openDatabaseIn(e *engine.Engine, name, app string) error {
	a, err := apps.Find(app)
	if err != nil {
		return err
	}
	if a.Kind != "database" {
		return errors.New("not a database app")
	}
	c, err := e.DatabaseCredentials(name)
	if err != nil {
		return err
	}
	u := url.URL{Scheme: "mysql", User: url.User(c.User), Host: fmt.Sprintf("127.0.0.1:%d", c.Port), Path: "/" + c.Database}
	return apps.Open(a, u.String())
}

func openURL(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("/usr/bin/open", u).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Run()
	default:
		return exec.Command("xdg-open", u).Run()
	}
}

/* ----------------------------------------------------------------- client */

// Client talks to a running LocalSites daemon.
type Client struct {
	hc    *http.Client
	token string
	name  string // shown in the activity feed: app, cli, mcp, devui
}

// As returns a copy of the client that identifies itself as name in the activity feed.
func (c *Client) As(name string) *Client {
	cp := *c
	cp.name = name
	return &cp
}

// Dial connects to the daemon. Returns an error if it is not running.
func Dial(p paths.Paths) (*Client, error) {
	tok, err := os.ReadFile(tokenPath(p))
	if err != nil {
		return nil, errors.New("LocalSites is not running")
	}
	sock := socketPath(p)
	c := &Client{token: strings.TrimSpace(string(tok)), hc: &http.Client{
		Timeout: 10 * time.Minute, // imports and first MySQL start can take a while
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
	}}
	if _, err := c.Call("status", nil); err != nil {
		return nil, err
	}
	return c, nil
}

// Call invokes method with params (any JSON-able value) and returns the raw JSON result.
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	var body io.Reader = strings.NewReader("{}")
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		body = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(http.MethodPost, "http://localsites/v1/"+method, body)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.name != "" {
		req.Header.Set("X-LocalSites-Client", c.name)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LocalSites is not responding: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error  string             `json:"error"`
			Fields engine.FieldErrors `json:"fields"`
		}
		_ = json.Unmarshal(b, &e)
		if len(e.Fields) > 0 {
			return nil, e.Fields
		}
		return nil, errors.New(e.Error)
	}
	return b, nil
}

func isDestructive(method string) bool {
	for _, m := range Methods {
		if m.Name == method {
			return m.Destructive
		}
	}
	return false
}

// mcpCommand is the program agents start for MCP: this service's own executable (the app or the CLI).
func mcpCommand() string {
	p, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}
