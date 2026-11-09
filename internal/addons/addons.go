// Package addons describes the optional services LocalSites can install and run next to your sites
// (mail catcher, caches). Everything is open source, installed with Homebrew on demand, and listens on
// 127.0.0.1 only.
package addons

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Def is one add-on in the catalog.
type Def struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"` // mail | cache
	Description string `json:"description"`
	License     string `json:"license"`
	Homepage    string `json:"homepage"`
	Formula     string `json:"formula"` // Homebrew formula
	Bin         string `json:"-"`       // executable name
	Port        int    `json:"default_port"`
	UIPort      int    `json:"default_ui_port,omitempty"` // web interface (mail inbox)
	// Conflicts: add-ons that serve the same purpose and default port (Valkey and Redis)
	Conflicts []string `json:"conflicts,omitempty"`
	// OwnPackageOnly: only accept the binary from this formula's own folder (Valkey installs a redis-server link)
	OwnPackageOnly bool `json:"-"`
	// UIName is the web page's address: <UIName>.<tld> (mail.test, minio.test …)
	UIName string `json:"ui_name,omitempty"`
	// Init prepares the data folder before the first start (PostgreSQL's initdb)
	Init func(bin, dataDir string) error `json:"-"`
	// ProcEnv are extra environment variables for the process (MinIO's login)
	ProcEnv func(o Options) []string `json:"-"`
}

// Catalog lists every add-on, in the order the app shows them.
var Catalog = []Def{
	{ID: "mailpit", Name: "Mailpit", Kind: "mail", License: "MIT", Homepage: "https://mailpit.axllent.org",
		Description: "Catches every email your projects send and shows it in an inbox, so nothing reaches real people.",
		Formula:     "mailpit", Bin: "mailpit", Port: 1025, UIPort: 8025, UIName: "mail"},
	{ID: "valkey", Name: "Valkey", Kind: "cache", License: "BSD-3-Clause", Homepage: "https://valkey.io",
		Description: "Open-source Redis-compatible store for caches, queues and sessions. Works with every Redis client.",
		Formula:     "valkey", Bin: "valkey-server", Port: 6379, Conflicts: []string{"redis"}},
	{ID: "redis", Name: "Redis", Kind: "cache", License: "AGPL-3.0", Homepage: "https://redis.io",
		Description: "Redis itself, for projects that need it exactly. Use Valkey unless you have a reason.",
		Formula:     "redis", Bin: "redis-server", Port: 6379, Conflicts: []string{"valkey"}, OwnPackageOnly: true},
	{ID: "memcached", Name: "Memcached", Kind: "cache", License: "BSD-3-Clause", Homepage: "https://memcached.org",
		Description: "Simple in-memory cache many PHP apps (WordPress, Drupal, Laravel) can use.",
		Formula:     "memcached", Bin: "memcached", Port: 11211},
	{ID: "postgres", Name: "PostgreSQL", Kind: "database", License: "PostgreSQL", Homepage: "https://www.postgresql.org",
		Description: "A second database server next to MySQL, for projects built on Postgres. Browse it in Data.",
		Formula:     "postgresql@17", Bin: "postgres", Port: 5433, OwnPackageOnly: true, Init: initPostgres},
	{ID: "meilisearch", Name: "Meilisearch", Kind: "search", License: "MIT", Homepage: "https://www.meilisearch.com",
		Description: "Fast search for your app (Laravel Scout, InstantSearch). Its dashboard opens in the browser.",
		Formula:     "meilisearch", Bin: "meilisearch", Port: 7700, UIPort: 7700, UIName: "meilisearch"},
	{ID: "minio", Name: "MinIO", Kind: "storage", License: "AGPL-3.0", Homepage: "https://min.io",
		Description: "S3-compatible file storage for uploads, so code that uses S3 works locally.",
		Formula:     "minio", Bin: "minio", Port: 9000, UIPort: 9001, UIName: "minio",
		ProcEnv: func(Options) []string {
			return []string{"MINIO_ROOT_USER=" + MinIOUser, "MINIO_ROOT_PASSWORD=" + MinIOPassword, "MINIO_BROWSER_REDIRECT=false"}
		}},
}

// MinIO's login. Local only (127.0.0.1), shown in the app so projects can use it.
const (
	MinIOUser     = "localsites"
	MinIOPassword = "localsites-dev"
)

// initPostgres creates the data folder once: superuser "postgres", local connections only.
func initPostgres(bin, dataDir string) error {
	data := filepath.Join(dataDir, "data")
	if _, err := os.Stat(filepath.Join(data, "PG_VERSION")); err == nil {
		return nil
	}
	out, err := exec.Command(filepath.Join(filepath.Dir(bin), "initdb"), "-D", data, "-U", "postgres", "--auth=trust", "-E", "UTF8", "--locale=C").CombinedOutput()
	if err != nil {
		return fmt.Errorf("initdb: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Find returns the add-on with this id.
func Find(id string) (Def, bool) {
	for _, d := range Catalog {
		if d.ID == id {
			return d, true
		}
	}
	return Def{}, false
}

// Prefixes where Homebrew installs programs (Apple silicon, Intel).
var Prefixes = []string{"/opt/homebrew", "/usr/local"}

// Binary finds the installed executable.
func (d Def) Binary() (string, bool) {
	for _, p := range Prefixes {
		files := []string{filepath.Join(p, "opt", d.Formula, "bin", d.Bin)}
		if !d.OwnPackageOnly {
			files = append(files, filepath.Join(p, "bin", d.Bin))
		}
		for _, f := range files {
			if st, err := os.Stat(f); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				return f, true
			}
		}
	}
	return "", false
}

// Brew finds the Homebrew executable.
func Brew() (string, bool) {
	for _, p := range Prefixes {
		f := filepath.Join(p, "bin", "brew")
		if _, err := os.Stat(f); err == nil {
			return f, true
		}
	}
	return "", false
}

// Options a user can set per add-on.
type Options struct {
	Port      int
	UIPort    int
	KeepMail  bool // Mailpit: keep messages after a restart
	MemoryMB  int  // Memcached
	PersistDB bool // Valkey/Redis: save data to disk
}

// Args builds the command line. dataDir is the add-on's own folder.
func (d Def) Args(o Options, dataDir string) []string {
	p := strconv.Itoa(o.Port)
	switch d.ID {
	case "mailpit":
		a := []string{"--smtp", "127.0.0.1:" + p, "--listen", "127.0.0.1:" + strconv.Itoa(o.UIPort),
			// Apps configured with an SMTP user/password still deliver here
			"--smtp-auth-accept-any", "--smtp-auth-allow-insecure", "--disable-version-check"}
		if o.KeepMail {
			a = append(a, "--database", filepath.Join(dataDir, "mailpit.db"))
		}
		return a
	case "valkey", "redis":
		a := []string{"--bind", "127.0.0.1", "--port", p, "--protected-mode", "yes", "--daemonize", "no", "--dir", dataDir, "--logfile", ""}
		if o.PersistDB {
			a = append(a, "--save", "3600 1 300 100", "--dbfilename", "dump.rdb")
		} else {
			a = append(a, "--save", "")
		}
		return a
	case "postgres":
		return []string{"-D", filepath.Join(dataDir, "data"), "-p", p, "-h", "127.0.0.1", "-k", dataDir}
	case "meilisearch":
		return []string{"--http-addr", "127.0.0.1:" + p, "--db-path", filepath.Join(dataDir, "data.ms"), "--dump-dir", filepath.Join(dataDir, "dumps"), "--env", "development", "--no-analytics"}
	case "minio":
		return []string{"server", filepath.Join(dataDir, "data"), "--address", "127.0.0.1:" + p, "--console-address", "127.0.0.1:" + strconv.Itoa(o.UIPort), "--quiet"}
	case "memcached":
		mb := o.MemoryMB
		if mb <= 0 {
			mb = 64
		}
		return []string{"-l", "127.0.0.1", "-p", p, "-U", "0", "-m", strconv.Itoa(mb)}
	}
	return nil
}

// Snippet is ready-to-copy configuration for one kind of project.
type Snippet struct {
	Stack string `json:"stack"` // Laravel, Node / Next.js, Django, Rails, Any language
	File  string `json:"file"`  // where it goes, e.g. ".env"
	Text  string `json:"text"`
}

// Snippets are the settings projects in any language use to reach the add-on. The protocols are standard
// (SMTP, the Redis protocol, the memcached protocol), so every framework's usual library works.
func (d Def) Snippets(o Options) []Snippet {
	p := strconv.Itoa(o.Port)
	addr := "127.0.0.1:" + p
	lines := func(l ...string) string { return strings.Join(l, "\n") }
	switch d.ID {
	case "mailpit":
		return []Snippet{
			{"Laravel", ".env", lines("MAIL_MAILER=smtp", "MAIL_HOST=127.0.0.1", "MAIL_PORT="+p, "MAIL_USERNAME=null", "MAIL_PASSWORD=null", "MAIL_ENCRYPTION=null")},
			{"Node / Next.js", ".env.local (Nodemailer)", lines("SMTP_HOST=127.0.0.1", "SMTP_PORT="+p, "SMTP_SECURE=false")},
			{"Django", "settings.py", lines(`EMAIL_BACKEND = "django.core.mail.backends.smtp.EmailBackend"`, `EMAIL_HOST = "127.0.0.1"`, "EMAIL_PORT = "+p, "EMAIL_USE_TLS = False")},
			{"Rails", "config/environments/development.rb", lines("config.action_mailer.delivery_method = :smtp", `config.action_mailer.smtp_settings = { address: "127.0.0.1", port: `+p+" }")},
			{"Any language", "SMTP", lines("Host: 127.0.0.1", "Port: "+p, "No TLS, any username and password are accepted", "Plain PHP mail() is sent here automatically")},
		}
	case "valkey", "redis":
		return []Snippet{
			{"Laravel", ".env", lines("REDIS_CLIENT=phpredis", "REDIS_HOST=127.0.0.1", "REDIS_PORT="+p, "REDIS_PASSWORD=null", "CACHE_STORE=redis", "SESSION_DRIVER=redis", "QUEUE_CONNECTION=redis")},
			{"Node / Next.js", ".env.local (ioredis, node-redis, BullMQ)", lines("REDIS_URL=redis://" + addr)},
			{"Django", "settings.py", lines(`CACHES = {"default": {"BACKEND": "django.core.cache.backends.redis.RedisCache", "LOCATION": "redis://` + addr + `"}}`)},
			{"Rails", "config/environments/development.rb", lines(`config.cache_store = :redis_cache_store, { url: "redis://`+addr+`" }`, "# Sidekiq: REDIS_URL=redis://"+addr)},
			{"Any language", "Redis URL", lines("redis://" + addr)},
		}
	case "postgres":
		url := "postgres://postgres@" + addr + "/postgres"
		return []Snippet{
			{"Laravel", ".env", lines("DB_CONNECTION=pgsql", "DB_HOST=127.0.0.1", "DB_PORT="+p, "DB_DATABASE=postgres", "DB_USERNAME=postgres", "DB_PASSWORD=")},
			{"Node / Next.js", ".env.local (Prisma, Drizzle, pg)", lines(`DATABASE_URL="` + url + `"`)},
			{"Django", "settings.py", lines(`DATABASES = {"default": {"ENGINE": "django.db.backends.postgresql", "NAME": "postgres", "USER": "postgres", "HOST": "127.0.0.1", "PORT": "` + p + `"}}`)},
			{"Rails", "config/database.yml", lines("development:", "  adapter: postgresql", "  host: 127.0.0.1", "  port: "+p, "  database: postgres", "  username: postgres")},
			{"Any language", "Connection URL", lines(url, "# no password: only this Mac can connect")},
		}
	case "meilisearch":
		return []Snippet{
			{"Laravel", ".env (Scout)", lines("SCOUT_DRIVER=meilisearch", "MEILISEARCH_HOST=http://"+addr, "MEILISEARCH_KEY=")},
			{"Node / Next.js", ".env.local (meilisearch-js)", lines("MEILISEARCH_HOST=http://" + addr)},
			{"Django", "settings.py", lines(`MEILISEARCH_URL = "http://` + addr + `"`)},
			{"Rails", "config/initializers/meilisearch.rb", lines(`Meilisearch::Rails.configuration = { meilisearch_url: "http://` + addr + `" }`)},
			{"Any language", "URL", lines("http://"+addr, "# no API key in development mode")},
		}
	case "minio":
		return []Snippet{
			{"Laravel", ".env", lines("FILESYSTEM_DISK=s3", "AWS_ACCESS_KEY_ID="+MinIOUser, "AWS_SECRET_ACCESS_KEY="+MinIOPassword, "AWS_DEFAULT_REGION=us-east-1", "AWS_BUCKET=local", "AWS_ENDPOINT=http://"+addr, "AWS_USE_PATH_STYLE_ENDPOINT=true")},
			{"Node / Next.js", ".env.local (AWS SDK)", lines("S3_ENDPOINT=http://"+addr, "S3_ACCESS_KEY_ID="+MinIOUser, "S3_SECRET_ACCESS_KEY="+MinIOPassword, "S3_FORCE_PATH_STYLE=true", "S3_REGION=us-east-1")},
			{"Django", "settings.py (django-storages)", lines(`AWS_S3_ENDPOINT_URL = "http://`+addr+`"`, `AWS_ACCESS_KEY_ID = "`+MinIOUser+`"`, `AWS_SECRET_ACCESS_KEY = "`+MinIOPassword+`"`)},
			{"Rails", "config/storage.yml", lines("local_s3:", "  service: S3", "  endpoint: http://"+addr, "  access_key_id: "+MinIOUser, "  secret_access_key: "+MinIOPassword, "  region: us-east-1", "  bucket: local", "  force_path_style: true")},
			{"Any language", "S3 endpoint", lines("http://"+addr, "Access key: "+MinIOUser, "Secret key: "+MinIOPassword, "Create buckets in the console")},
		}
	case "memcached":
		return []Snippet{
			{"Laravel", ".env", lines("CACHE_STORE=memcached", "MEMCACHED_HOST=127.0.0.1", "MEMCACHED_PORT="+p)},
			{"Node / Next.js", ".env.local (memjs)", lines("MEMCACHIER_SERVERS="+addr, "MEMCACHED_SERVERS="+addr)},
			{"Django", "settings.py", lines(`CACHES = {"default": {"BACKEND": "django.core.cache.backends.memcached.PyMemcacheCache", "LOCATION": "` + addr + `"}}`)},
			{"Rails", "config/environments/development.rb", lines(`config.cache_store = :mem_cache_store, "` + addr + `"`)},
			{"Any language", "Server", lines(addr)},
		}
	}
	return nil
}

// SendmailPath is PHP's sendmail_path that delivers mail() to Mailpit.
func SendmailPath(mailpitBin string, port int) string {
	return strings.Join([]string{mailpitBin, "sendmail", "-S", "127.0.0.1:" + strconv.Itoa(port)}, " ")
}
