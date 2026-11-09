//go:build e2e

// End-to-end tests: real Apache, PHP-FPM and MySQL 8.4 in a throwaway sandbox.
//
//	go test -tags e2e -v ./e2e/        (make e2e)
//
// Isolation: own data dir (LOCALSITES_HOME), file-based secrets (never the Keychain),
// built-in front door on random high ports (never Valet), its own MySQL on a random port.
// A second throwaway MySQL plays the "MAMP" source for import tests.
package e2e

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/localsites/localsites/internal/api"
	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/dbclient"
	"github.com/localsites/localsites/internal/engine"
	"github.com/localsites/localsites/internal/importers"
	"github.com/localsites/localsites/internal/mcp"
	"github.com/localsites/localsites/internal/paths"
	lsruntime "github.com/localsites/localsites/internal/runtime"
	"github.com/localsites/localsites/internal/services"
	"github.com/localsites/localsites/internal/share"
)

type env struct {
	t         *testing.T
	home      string
	sites     string
	e         *engine.Engine
	httpsPort int
	client    *http.Client
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setup builds the sandbox with fixture projects and starts the engine.
var sweepOnce sync.Once

// sweepStaleSandboxes ends servers left by earlier test runs that were killed halfway: their sandbox
// folder is gone but Apache, PHP-FPM or MySQL from it may still be running.
func sweepStaleSandboxes() {
	out, _ := exec.Command("ps", "-axo", "pid=,pgid=,command=").Output()
	re := regexp.MustCompile(`(/[^\s"]*/lse2e[0-9]+)/`)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, err := os.Stat(m[1]); err == nil {
			continue // a sandbox still in use
		}
		if pgid, err := strconv.Atoi(f[1]); err == nil && pgid > 1 {
			syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}
}

func setup(t *testing.T) *env {
	sweepOnce.Do(sweepStaleSandboxes)
	if _, _, _, ok := lsruntime.Apache(); !ok {
		t.Skip("Apache not installed")
	}
	if _, ok := lsruntime.FindPHP("8.5"); !ok {
		t.Skip("PHP 8.5 not installed")
	}
	// Short base path: unix socket paths are limited to ~104 bytes
	home, err := os.MkdirTemp("", "lse2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALSITES_HOME", home)
	t.Setenv("LOCALSITES_SECRETS", "file")
	sites := filepath.Join(home, "sites")

	write(t, filepath.Join(sites, "hello", "index.php"), `<?php echo "php=" . PHP_MAJOR_VERSION . "." . PHP_MINOR_VERSION . " via=" . ($_GET["via"] ?? "direct");`)
	write(t, filepath.Join(sites, "hello", ".htaccess"), "RewriteEngine On\nRewriteRule ^pretty$ index.php?via=rewrite [L]\n")
	write(t, filepath.Join(sites, "hello", ".env"), "SECRET=never-served\n")
	write(t, filepath.Join(sites, "pubapp", "public", "index.php"), `<?php echo "public-ok";`)
	write(t, filepath.Join(sites, "pubapp", "secret.txt"), "outside the web root")
	write(t, filepath.Join(sites, "broken", "index.php"), `<?php throw new Exception("boom");`)

	p := paths.Default()
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(p.Home)
	cfg.Settings.SitesRoot = sites
	cfg.Settings.Roots = []config.Root{{Path: sites, Label: "localhost"}}
	cfg.Settings.FrontDoor = "builtin"
	cfg.Settings.ApachePort, cfg.Settings.MySQLPort, cfg.Settings.NginxPort = freePort(t), freePort(t), freePort(t)
	cfg.Settings.HTTPPort, cfg.Settings.HTTPSPort, cfg.Settings.DNSPort = freePort(t), freePort(t), freePort(t)
	b, _ := json.MarshalIndent(cfg, "", "  ")
	write(t, filepath.Join(p.Config, "localsites.json"), string(b))

	e, err := engine.New()
	if err != nil {
		t.Fatal(err)
	}
	ev := &env{t: t, home: home, sites: sites, e: e, httpsPort: cfg.Settings.HTTPSPort}
	ev.client = &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy:           nil,
			TLSClientConfig: &tls.Config{RootCAs: e.CA.Pool()},
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { // every *.test goes to our front door
				var d net.Dialer
				return d.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", ev.httpsPort))
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	t.Cleanup(func() {
		e.StopAll()
		if os.Getenv("E2E_KEEP") == "" {
			os.RemoveAll(home)
		} else {
			t.Logf("sandbox kept at %s", home)
		}
	})
	return ev
}

func (ev *env) get(url string) (int, string) {
	ev.t.Helper()
	res, err := ev.client.Get(url)
	if err != nil {
		ev.t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func (ev *env) set(folder string, p engine.SitePatch) {
	ev.t.Helper()
	if err := ev.e.UpdateSite(folder, p); err != nil {
		ev.t.Fatalf("update %s: %v", folder, err)
	}
	for _, l := range ev.e.Apply() {
		if strings.Contains(strings.ToLower(l), "fail") || strings.Contains(strings.ToLower(l), "error") {
			ev.t.Fatalf("apply: %s", l)
		}
	}
}

func on() *bool            { b := true; return &b }
func off() *bool           { b := false; return &b }
func str(s string) *string { return &s }

/* -------------------------------------------------------------------------- */

func TestSitesEndToEnd(t *testing.T) {
	ev := setup(t)

	// Discovery: every fixture folder is listed, off by default, with defaults filled in
	sites := ev.e.Sites()
	if len(sites) != 3 {
		t.Fatalf("want 3 projects, got %d", len(sites))
	}
	for _, s := range sites {
		if s.Enabled {
			t.Errorf("%s should start switched off", s.Folder)
		}
	}

	t.Run("serve over HTTPS with PHP 8.5", func(t *testing.T) {
		ev.set("hello", engine.SitePatch{Enabled: on(), PHP: str("8.5")})
		code, body := ev.get("https://hello.test/")
		if code != 200 || body != "php=8.5 via=direct" {
			t.Fatalf("got %d %q", code, body)
		}
	})

	t.Run(".htaccess rewrites work", func(t *testing.T) {
		code, body := ev.get("https://hello.test/pretty")
		if code != 200 || body != "php=8.5 via=rewrite" {
			t.Fatalf("got %d %q", code, body)
		}
	})

	t.Run(".env and dotfiles are never served", func(t *testing.T) {
		for _, path := range []string{"/.env", "/.htaccess"} {
			if code, body := ev.get("https://hello.test" + path); code != 403 || strings.Contains(body, "never-served") {
				t.Fatalf("%s: got %d %q", path, code, body)
			}
		}
	})

	t.Run("switch PHP version live", func(t *testing.T) {
		if _, ok := lsruntime.FindPHP("8.3"); !ok {
			t.Skip("PHP 8.3 not installed")
		}
		ev.set("hello", engine.SitePatch{PHP: str("8.3")})
		if _, body := ev.get("https://hello.test/"); body != "php=8.3 via=direct" {
			t.Fatalf("after switch: %q", body)
		}
		ev.set("hello", engine.SitePatch{PHP: str("8.5")})
	})

	t.Run("web root subfolder", func(t *testing.T) {
		ev.set("pubapp", engine.SitePatch{Enabled: on()})
		if _, body := ev.get("https://pubapp.test/"); body != "public-ok" {
			t.Fatalf("auto-detected public/ web root: %q", body)
		}
		if code, _ := ev.get("https://pubapp.test/../secret.txt"); code == 200 {
			t.Fatal("file outside the web root was served")
		}
	})

	t.Run("www alias and plain HTTP redirect", func(t *testing.T) {
		if _, body := ev.get("https://www.hello.test/"); !strings.HasPrefix(body, "php=") {
			t.Fatalf("www alias: %q", body)
		}
	})

	t.Run("unknown site gets 404", func(t *testing.T) {
		if code, _ := ev.get("https://nope.test/"); code != 404 {
			t.Fatalf("got %d", code)
		}
	})

	t.Run("PHP errors are logged per project", func(t *testing.T) {
		ev.set("broken", engine.SitePatch{Enabled: on()})
		if code, _ := ev.get("https://broken.test/"); code != 500 {
			t.Fatalf("want 500, got %d", code)
		}
		time.Sleep(300 * time.Millisecond)
		text, _ := ev.e.Log("site-php", "broken", 50)
		if !strings.Contains(text, "boom") {
			t.Fatalf("exception not in the project's PHP log: %q", text)
		}
	})

	t.Run("bad input is rejected", func(t *testing.T) {
		for name, p := range map[string]engine.SitePatch{
			"domain":    {Domain: str("evil.com")},
			"php":       {PHP: str("5.2")},
			"docroot":   {Docroot: str("../../")},
			"db_server": {DBServer: str("oracle")},
			"database":  {Database: str("x; DROP")},
			"duplicate": {Enabled: on(), Domain: str("hello.test")},
		} {
			if err := ev.e.UpdateSite("pubapp", p); err == nil {
				t.Errorf("%s: accepted bad value", name)
			}
		}
	})

	t.Run("switch off", func(t *testing.T) {
		ev.set("broken", engine.SitePatch{Enabled: off()})
		if code, _ := ev.get("https://broken.test/"); code != 404 {
			t.Fatalf("switched-off site still served: %d", code)
		}
	})

	t.Run("doctor and site check", func(t *testing.T) {
		for _, c := range ev.e.CheckSite("hello") {
			if c.Status == "fail" && !strings.HasPrefix(c.Name, "DNS") { // .test DNS isn't set up for the sandbox
				t.Errorf("check %s failed: %s", c.Name, c.Detail)
			}
		}
	})

	t.Run("crashed PHP is restarted", func(t *testing.T) {
		st := ev.e.Sup.Status("php-8.5")
		if !st.Running {
			t.Fatal("php-8.5 not running")
		}
		p, _ := os.FindProcess(st.PID)
		_ = p.Kill()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if s := ev.e.Sup.Status("php-8.5"); s.Running && s.PID != st.PID {
				if _, body := ev.get("https://hello.test/"); strings.HasPrefix(body, "php=") {
					return
				}
			}
			time.Sleep(300 * time.Millisecond)
		}
		t.Fatal("PHP was not restarted after a crash")
	})
}

func TestDatabasesAndImport(t *testing.T) {
	ev := setup(t)
	if _, ok := lsruntime.MySQL(); !ok {
		t.Skip("MySQL 8.4 not installed")
	}

	// A project page that uses its own database login via "localhost" (the pool's default socket)
	creds, err := ev.e.CreateDatabase("shop")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ev.sites, "hello", "db.php"), fmt.Sprintf(`<?php
$pdo = new PDO("mysql:host=localhost;dbname=%s", "%s", "%s", [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
$pdo->exec("CREATE TABLE IF NOT EXISTS t (id INT PRIMARY KEY AUTO_INCREMENT, v VARCHAR(20))");
$pdo->exec("INSERT INTO t (v) VALUES ('x')");
echo "rows=" . $pdo->query("SELECT COUNT(*) FROM t")->fetchColumn();
try { $pdo->query("SELECT * FROM mysql.user"); echo " escaped"; } catch (Exception $e) { echo " isolated"; }`, creds.Database, creds.User, creds.Password))
	ev.set("hello", engine.SitePatch{Enabled: on(), DBServer: str(config.DBLocalSites)})

	t.Run("project login works through localhost and is limited to its database", func(t *testing.T) {
		_, body := ev.get("https://hello.test/db.php")
		if body != "rows=1 isolated" {
			t.Fatalf("got %q", body)
		}
	})

	t.Run("backup", func(t *testing.T) {
		file, err := ev.e.Backup("shop")
		if err != nil {
			t.Fatal(err)
		}
		if st, err := os.Stat(file); err != nil || st.Size() < 100 {
			t.Fatalf("backup missing or empty: %v", err)
		}
	})

	t.Run("import copies, verifies and never touches the source", func(t *testing.T) {
		src := startSourceMySQL(t, ev.home)
		sdb, err := src.Connect("root", "")
		if err != nil {
			t.Fatal(err)
		}
		defer sdb.Close()
		for _, q := range []string{
			"CREATE DATABASE legacy CHARACTER SET utf8 COLLATE utf8_general_ci",
			"CREATE TABLE legacy.products (id INT PRIMARY KEY, title VARCHAR(100), price DECIMAL(10,2), img BLOB, added DATETIME) ENGINE=InnoDB",
			"CREATE TABLE legacy.log (id INT AUTO_INCREMENT PRIMARY KEY, msg TEXT) ENGINE=MyISAM",
			"INSERT INTO legacy.products VALUES (1,'Fuse holder',12.50,X'00FF10',NOW()),(2,'Ünïcödé — 日本',0.99,NULL,'2020-01-01 10:00:00'),(3,NULL,NULL,'',NULL)",
			"INSERT INTO legacy.log (msg) VALUES ('a'),('b'),(REPEAT('z', 70000))",
		} {
			if _, err := sdb.Exec(q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		before, _, _ := importers.Verify(sdb, sdb, "legacy")

		rep, err := ev.e.ImportDatabaseFrom(src, "root", "", "legacy")
		if err != nil {
			t.Fatal(err)
		}
		if !rep.OK || len(rep.Tables) != 2 {
			t.Fatalf("import report: %+v", rep)
		}
		for _, tc := range rep.Tables {
			if !tc.Match || tc.SourceRows != 3 {
				t.Errorf("table %s: %+v", tc.Table, tc)
			}
		}
		after, _, _ := importers.Verify(sdb, sdb, "legacy")
		if fmt.Sprint(before) != fmt.Sprint(after) {
			t.Fatal("the source database changed during import")
		}
		if _, err := ev.e.ImportDatabaseFrom(src, "root", "", "legacy"); err == nil || !strings.Contains(err.Error(), "already has") {
			t.Fatalf("second import must refuse to overwrite, got %v", err)
		}

		// Verification must catch a difference
		admin := ev.adminDB(t)
		defer admin.Close()
		if _, err := admin.Exec("UPDATE legacy.products SET price = 13.00 WHERE id = 1"); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := importers.Verify(sdb, admin, "legacy"); ok {
			t.Fatal("Verify did not notice a changed row")
		}
	})
}

func TestAPIAndMCP(t *testing.T) {
	ev := setup(t)
	srv := &api.Server{E: ev.e}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	c, err := api.Dial(ev.e.P)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call("sites.update", map[string]any{"folder": "hello", "enabled": true}); err != nil {
		t.Fatal(err)
	}
	raw, _ := c.Call("sites.list", nil)
	if !strings.Contains(string(raw), `"folder":"hello"`) || !strings.Contains(string(raw), `"enabled":true`) {
		t.Fatalf("sites.list: %s", raw)
	}
	if _, err := c.Call("no.such.method", nil); err == nil {
		t.Fatal("unknown method accepted")
	}

	// Wrong token is refused
	bad := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("unix", ev.e.P.Socket("api"))
	}}}
	req, _ := http.NewRequest("POST", "http://x/v1/status", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer wrong")
	if res, err := bad.Do(req); err != nil || res.StatusCode != 401 {
		t.Fatalf("wrong token: %v %v", res, err)
	}

	// MCP over the real API
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sites_list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"sites_update","arguments":{"folder":"pubapp","enabled":true}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"quit","arguments":{}}}`,
	}, "\n")
	var out strings.Builder
	m := &mcp.Server{API: c.As("mcp"), Version: "e2e"} // as in real use: requests are tagged as an AI agent
	go func() {                                        // the person denies the agent's quit in the app
		app := c.As("app")
		for i := 0; i < 200; i++ {
			var list []api.Approval
			raw, _ := app.Call("approvals.list", nil)
			json.Unmarshal(raw, &list)
			if len(list) > 0 {
				app.Call("approvals.decide", map[string]any{"id": list[0].ID, "approve": false})
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	}()
	if err := m.Serve(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || !strings.Contains(lines[1], "hello") || strings.Contains(lines[2], `"isError":true`) || !strings.Contains(lines[3], `"isError":true`) || !strings.Contains(lines[3], "denied") {
		t.Fatalf("MCP transcript:\n%s", out.String())
	}
	if _, body := ev.get("https://pubapp.test/"); body != "public-ok" {
		t.Fatalf("agent's change was not applied: %q", body)
	}
}

/* -------------------------------------------------------------------------- */

func (ev *env) adminDB(t *testing.T) *sql.DB {
	pw, err := ev.e.Secrets.Get("mysql-root")
	if err != nil {
		t.Fatal(err)
	}
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd, cfg.Net, cfg.Addr = "root", pw, "unix", ev.e.P.Socket("mysql")
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// startSourceMySQL runs a second throwaway MySQL that plays the role of MAMP's.
func startSourceMySQL(t *testing.T, home string) importers.Source {
	base, _ := lsruntime.MySQL()
	dir := filepath.Join(home, "src")
	m := services.MySQL{Base: base, DataDir: filepath.Join(dir, "data"), Socket: filepath.Join(dir, "s.sock"), Port: freePort(t),
		PIDFile: filepath.Join(dir, "pid"), LogFile: filepath.Join(dir, "err.log"), CnfFile: filepath.Join(dir, "my.cnf")}
	os.MkdirAll(dir, 0o700)
	if err := m.WriteCnf(); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(m.Bin("mysqld"), "--defaults-file="+m.CnfFile, "--initialize-insecure").CombinedOutput(); err != nil {
		t.Fatalf("init source: %v %s", err, out)
	}
	cmd := exec.Command(m.Bin("mysqld"), "--defaults-file="+m.CnfFile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	if err := m.WaitReady("", 60*time.Second); err != nil {
		t.Fatal(err)
	}
	return importers.Source{ID: "test-source", Name: "Test source", MySQLSock: m.Socket, MySQLBin: filepath.Join(base, "bin"), MySQLUp: true}
}

func TestSettingsAndEnvConnect(t *testing.T) {
	ev := setup(t)
	t.Setenv("LOCALSITES_LAUNCHAGENTS_DIR", filepath.Join(ev.home, "launchagents"))

	t.Run("bad settings are rejected per field", func(t *testing.T) {
		bad := 80
		_, err := ev.e.UpdateSettings(engine.SettingsPatch{SitesRoot: str("/nope/nowhere"), ApachePort: &bad, DefaultPHP: str("4.0"),
			PHPIni: map[string]*string{"memory_limit": str("lots"), "disable_functions": str("")}})
		var fe engine.FieldErrors
		if !errors.As(err, &fe) {
			t.Fatalf("want field errors, got %v", err)
		}
		fields := map[string]bool{}
		for _, f := range fe {
			fields[f.Field] = true
		}
		for _, want := range []string{"sites_root", "apache_port", "default_php", "php_ini.memory_limit", "php_ini.disable_functions"} {
			if !fields[want] {
				t.Errorf("no error for %s (got %v)", want, fe)
			}
		}
	})

	t.Run("php.ini change reaches PHP", func(t *testing.T) {
		write(t, filepath.Join(ev.sites, "hello", "ini.php"), `<?php echo ini_get("memory_limit");`)
		ev.set("hello", engine.SitePatch{Enabled: on()})
		if _, err := ev.e.UpdateSettings(engine.SettingsPatch{PHPIni: map[string]*string{"memory_limit": str("321M")}}); err != nil {
			t.Fatal(err)
		}
		if _, body := ev.get("https://hello.test/ini.php"); body != "321M" {
			t.Fatalf("memory_limit = %q", body)
		}
		if _, err := ev.e.UpdateSettings(engine.SettingsPatch{PHPIni: map[string]*string{"memory_limit": str("")}}); err != nil {
			t.Fatal(err)
		}
		if _, body := ev.get("https://hello.test/ini.php"); body != "128M" {
			t.Fatalf("reset to default: %q", body)
		}
	})

	t.Run("start at login writes and removes the login item", func(t *testing.T) {
		if _, err := ev.e.UpdateSettings(engine.SettingsPatch{StartAtLogin: on()}); err != nil {
			t.Fatal(err)
		}
		if !ev.e.Settings().StartAtLogin {
			t.Fatal("not enabled")
		}
		if _, err := ev.e.UpdateSettings(engine.SettingsPatch{StartAtLogin: off()}); err != nil {
			t.Fatal(err)
		}
		if ev.e.Settings().StartAtLogin {
			t.Fatal("not disabled")
		}
	})

	t.Run("connect a project to its database, then undo", func(t *testing.T) {
		if _, ok := lsruntime.MySQL(); !ok {
			t.Skip("MySQL 8.4 not installed")
		}
		if _, err := ev.e.CreateDatabase("shopdb"); err != nil {
			t.Fatal(err)
		}
		original := "# app settings\nAPP=1\nDB_HOST=localhost\nDB_NAME=shopdb\nDB_USER=root\nDB_PASS=old-secret\n"
		write(t, filepath.Join(ev.sites, "hello", ".env"), original)
		write(t, filepath.Join(ev.sites, "hello", "env.php"), `<?php
$e = parse_ini_file(__DIR__ . "/.env");
$pdo = new PDO("mysql:host=" . $e["DB_HOST"] . ";dbname=" . $e["DB_NAME"], $e["DB_USER"], $e["DB_PASS"]);
echo "db=" . $pdo->query("SELECT DATABASE()")->fetchColumn();`)
		if _, err := ev.e.ConnectDatabase("hello", "shopdb"); err != nil {
			t.Fatal(err)
		}
		if _, body := ev.get("https://hello.test/env.php"); body != "db=shopdb" {
			t.Fatalf("after connect: %q", body)
		}
		b, _ := os.ReadFile(filepath.Join(ev.sites, "hello", ".env"))
		if strings.Contains(string(b), "old-secret") || !strings.Contains(string(b), "DB_USER=ls_shopdb") || !strings.Contains(string(b), "APP=1") {
			t.Fatalf(".env after connect:\n%s", b)
		}
		if s, _ := siteByFolder(ev.e, "hello"); s.DBServer != config.DBLocalSites || s.Env == nil || s.Env.Backups != 1 {
			t.Fatalf("site after connect: %+v", s)
		}
		if _, err := ev.e.RestoreEnv("hello"); err != nil {
			t.Fatal(err)
		}
		b, _ = os.ReadFile(filepath.Join(ev.sites, "hello", ".env"))
		if string(b) != original {
			t.Fatalf("undo did not restore .env exactly:\n%s", b)
		}
		if s, _ := siteByFolder(ev.e, "hello"); s.DBServer != config.DBMAMP {
			t.Fatalf("undo did not switch MySQL back: %s", s.DBServer)
		}
		if _, err := ev.e.ConnectDatabase("pubapp", "shopdb"); err == nil {
			t.Fatal("connect without a .env must fail")
		}
		if _, err := ev.e.ConnectDatabase("hello", "nosuchdb"); err == nil {
			t.Fatal("connect to an unknown database must fail")
		}
	})
}

func siteByFolder(e *engine.Engine, folder string) (engine.SiteView, bool) {
	for _, s := range e.Sites() {
		if s.Folder == folder {
			return s, true
		}
	}
	return engine.SiteView{}, false
}

func TestDataBrowser(t *testing.T) {
	ev := setup(t)
	if _, ok := lsruntime.MySQL(); !ok {
		t.Skip("MySQL 8.4 not installed")
	}
	if _, err := ev.e.CreateDatabase("shop"); err != nil { // also starts LocalSites MySQL
		t.Fatal(err)
	}
	admin := ev.adminDB(t)
	defer admin.Close()
	for _, q := range []string{
		"CREATE TABLE shop.products (id INT PRIMARY KEY, name VARCHAR(50), price DECIMAL(8,2), note TEXT, KEY idx_name (name))",
		"INSERT INTO shop.products VALUES (1,'Fuse',1.50,NULL),(2,'Socket',3.00,'x'),(3,'Switch',2.25,NULL),(4,'Fuse holder',0.99,'it''s')",
	} {
		if _, err := admin.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	d := ev.e.Data

	t.Run("built-in LocalSites connection: schemas, tables, structure", func(t *testing.T) {
		info, err := d.Test("localsites")
		if err != nil || !strings.Contains(info.Server, "8.4") {
			t.Fatalf("test: %v %+v", err, info)
		}
		schemas, _ := d.Schemas("localsites")
		if !slicesContains(schemas, "shop") || slicesContains(schemas, "mysql") {
			t.Fatalf("schemas: %v", schemas)
		}
		tables, err := d.Tables("localsites", "shop")
		if err != nil || len(tables) != 1 || tables[0].Name != "products" {
			t.Fatalf("tables: %v %+v", err, tables)
		}
		st, err := d.Structure("localsites", "shop", "products")
		if err != nil || len(st.Columns) != 4 || st.Columns[0].Key != "PRI" || len(st.Indexes) != 2 {
			t.Fatalf("structure: %v %+v", err, st)
		}
	})

	t.Run("rows: filter, sort, page, NULLs", func(t *testing.T) {
		res, err := d.Rows("localsites", dbclient.RowsRequest{Schema: "shop", Table: "products", Limit: 2, OrderBy: "price", Desc: true})
		if err != nil || res.Total != 4 || len(res.Rows) != 2 || *res.Rows[0][1] != "Socket" {
			t.Fatalf("sorted page: %v %+v", err, res)
		}
		res, _ = d.Rows("localsites", dbclient.RowsRequest{Schema: "shop", Table: "products", Filters: []dbclient.Filter{{Column: "name", Op: "contains", Value: "fuse"}}})
		if res.Total != 2 {
			t.Fatalf("contains filter: %+v", res)
		}
		res, _ = d.Rows("localsites", dbclient.RowsRequest{Schema: "shop", Table: "products", Filters: []dbclient.Filter{{Column: "note", Op: "null"}}})
		if res.Total != 2 || res.Rows[0][3] != nil {
			t.Fatalf("null filter: %+v", res)
		}
		// Injection attempts are bound values or rejected column names
		res, _ = d.Rows("localsites", dbclient.RowsRequest{Schema: "shop", Table: "products", Filters: []dbclient.Filter{{Column: "name", Op: "=", Value: "x' OR '1'='1"}}})
		if res.Total != 0 {
			t.Fatalf("filter value was not bound: %+v", res)
		}
		if _, err := d.Rows("localsites", dbclient.RowsRequest{Schema: "shop", Table: "products", OrderBy: "id; DROP TABLE products"}); err == nil {
			t.Fatal("bad order column accepted")
		}
	})

	t.Run("read-only by default: the server blocks changes", func(t *testing.T) {
		for _, q := range []string{"DELETE FROM products", "DROP TABLE products", "UPDATE products SET price = 0"} {
			if _, err := d.Query("localsites", dbclient.QueryRequest{Schema: "shop", SQL: q, AllowWrite: true}); err == nil {
				t.Fatalf("%s ran on a read-only connection", q)
			}
		}
		var n int
		admin.QueryRow("SELECT COUNT(*) FROM shop.products").Scan(&n)
		if n != 4 {
			t.Fatalf("data changed: %d rows", n)
		}
		res, err := d.Query("localsites", dbclient.QueryRequest{Schema: "shop", SQL: "SELECT name FROM products WHERE id = 4"})
		if err != nil || *res.Rows[0][0] != "Fuse holder" {
			t.Fatalf("select: %v %+v", err, res)
		}
	})

	t.Run("allow changes: writes need the connection switch and allow_write", func(t *testing.T) {
		if err := d.SetReadOnly("localsites", false); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Query("localsites", dbclient.QueryRequest{Schema: "shop", SQL: "DELETE FROM products WHERE id = 4"}); err == nil {
			t.Fatal("write ran without allow_write")
		}
		res, err := d.Query("localsites", dbclient.QueryRequest{Schema: "shop", SQL: "DELETE FROM products WHERE id = 4", AllowWrite: true})
		if err != nil || res.Affected != 1 {
			t.Fatalf("allowed write: %v %+v", err, res)
		}
		// Staged grid edits on MySQL: one transaction, exactly one row per update/delete
		sp := func(v string) *string { return &v }
		cols, _ := d.Structure("localsites", "shop", "products")
		var text string
		for _, c := range cols.Columns {
			if c.Key != "PRI" && (strings.Contains(c.Type, "char") || strings.Contains(c.Type, "text")) {
				text = c.Name
				break
			}
		}
		if text == "" {
			t.Fatalf("no text column in products: %+v", cols.Columns)
		}
		if _, err := d.SaveChanges("localsites", dbclient.Changes{Schema: "shop", Table: "products",
			Updates: []dbclient.RowUpdate{{Key: map[string]*string{"id": sp("1")}, Set: map[string]*string{text: sp("Edited ' quote")}}}}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if r, _ := d.Query("localsites", dbclient.QueryRequest{Schema: "shop", SQL: "SELECT " + text + " FROM products WHERE id = 1"}); len(r.Rows) == 0 || *r.Rows[0][0] != "Edited ' quote" {
			t.Fatalf("update not saved: %+v", r)
		}
		_, err = d.SaveChanges("localsites", dbclient.Changes{Schema: "shop", Table: "products",
			Updates: []dbclient.RowUpdate{{Key: map[string]*string{"id": sp("1")}, Set: map[string]*string{text: sp("must roll back")}}},
			Deletes: []map[string]*string{{"id": sp("4")}}}) // already deleted above
		if err == nil {
			t.Fatal("stale delete saved")
		}
		if r, _ := d.Query("localsites", dbclient.QueryRequest{Schema: "shop", SQL: "SELECT " + text + " FROM products WHERE id = 1"}); len(r.Rows) == 0 || *r.Rows[0][0] != "Edited ' quote" {
			t.Fatalf("not rolled back: %+v", r)
		}
		if err := d.SetReadOnly("localsites", true); err != nil {
			t.Fatal(err)
		}
		if c, _ := d.Get("localsites"); !c.ReadOnly {
			t.Fatal("read-only switch not restored")
		}
	})

	t.Run("saved MySQL connection with a remembered password", func(t *testing.T) {
		creds, _ := ev.e.DatabaseCredentials("shop")
		pw := creds.Password
		c, err := d.Save(dbclient.Conn{Name: "Shop login", Driver: "mysql", Socket: creds.Socket, User: creds.User, ReadOnly: true}, &pw)
		if err != nil || !c.HasPassword {
			t.Fatalf("save: %v %+v", err, c)
		}
		if schemas, err := d.Schemas(c.ID); err != nil || !slicesContains(schemas, "shop") {
			t.Fatalf("schemas via project login: %v %v", err, schemas)
		}
		if err := d.SetPassword(c.ID, "wrong"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Test(c.ID); err == nil || !strings.Contains(err.Error(), "refused the login") {
			t.Fatalf("wrong password message: %v", err)
		}
		if err := d.Delete(c.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := ev.e.Secrets.Get("conn-" + c.ID); err == nil {
			t.Fatal("password left behind after delete")
		}
	})

	t.Run("SQLite file", func(t *testing.T) {
		file := filepath.Join(ev.home, "app.sqlite")
		c, err := d.Save(dbclient.Conn{Name: "Local SQLite", Driver: "sqlite", File: file, ReadOnly: false}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Query(c.ID, dbclient.QueryRequest{SQL: "CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)", AllowWrite: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Query(c.ID, dbclient.QueryRequest{SQL: "INSERT INTO notes (body) VALUES ('hello'), (NULL)", AllowWrite: true}); err != nil {
			t.Fatal(err)
		}
		res, err := d.Rows(c.ID, dbclient.RowsRequest{Schema: "main", Table: "notes"})
		if err != nil || res.Total != 2 || res.Rows[1][1] != nil {
			t.Fatalf("rows: %v %+v", err, res)
		}
		if err := d.SetReadOnly(c.ID, true); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Query(c.ID, dbclient.QueryRequest{SQL: "DELETE FROM notes", AllowWrite: true}); err == nil {
			t.Fatal("read-only SQLite allowed a delete")
		}
	})
}

func slicesContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestDataBrowserPostgres(t *testing.T) {
	bin := "/opt/homebrew/opt/postgresql@17/bin"
	if _, err := os.Stat(filepath.Join(bin, "initdb")); err != nil {
		t.Skip("PostgreSQL not installed")
	}
	ev := setup(t)
	dir := filepath.Join(ev.home, "pg")
	if out, err := exec.Command(filepath.Join(bin, "initdb"), "-D", dir, "-U", "tester", "--auth=trust", "-E", "UTF8").CombinedOutput(); err != nil {
		t.Fatalf("initdb: %v %s", err, out)
	}
	port := freePort(t)
	sock := filepath.Join(ev.home, "pgs")
	os.MkdirAll(sock, 0o700)
	if out, err := exec.Command(filepath.Join(bin, "pg_ctl"), "-D", dir, "-w", "-o", fmt.Sprintf("-p %d -k %s -h 127.0.0.1", port, sock), "-l", filepath.Join(ev.home, "pg.log"), "start").CombinedOutput(); err != nil {
		t.Fatalf("pg_ctl start: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command(filepath.Join(bin, "pg_ctl"), "-D", dir, "-m", "fast", "stop").Run() })

	d := ev.e.Data
	c, err := d.Save(dbclient.Conn{Name: "Postgres test", Driver: "postgres", Host: "127.0.0.1", Port: port, User: "tester", Database: "postgres", SSLMode: "disable", ReadOnly: false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"CREATE SCHEMA app",
		"CREATE TABLE app.users (id serial PRIMARY KEY, email text UNIQUE NOT NULL, created timestamptz DEFAULT now(), meta jsonb)",
		`INSERT INTO app.users (email, meta) VALUES ('a@x.test', '{"vip": true}'), ('b@x.test', NULL), ('c@y.test', '{}')`,
	} {
		if _, err := d.Query(c.ID, dbclient.QueryRequest{SQL: q, AllowWrite: true}); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if info, err := d.Test(c.ID); err != nil || !strings.Contains(info.Server, "PostgreSQL 17") {
		t.Fatalf("test: %v %+v", err, info)
	}
	if schemas, _ := d.Schemas(c.ID); !slicesContains(schemas, "app") || slicesContains(schemas, "pg_catalog") {
		t.Fatalf("schemas: %v", schemas)
	}
	st, err := d.Structure(c.ID, "app", "users")
	if err != nil || len(st.Columns) != 4 || st.Columns[0].Key != "PRI" {
		t.Fatalf("structure: %v %+v", err, st)
	}
	res, err := d.Rows(c.ID, dbclient.RowsRequest{Schema: "app", Table: "users", Filters: []dbclient.Filter{{Column: "email", Op: "contains", Value: "@x."}}, OrderBy: "email", Desc: true})
	if err != nil || res.Total != 2 || *res.Rows[0][1] != "b@x.test" {
		t.Fatalf("rows: %v %+v", err, res)
	}
	res, _ = d.Rows(c.ID, dbclient.RowsRequest{Schema: "app", Table: "users", Filters: []dbclient.Filter{{Column: "meta", Op: "contains", Value: "vip"}}})
	if res.Total != 1 {
		t.Fatalf("filter on jsonb as text: %+v", res)
	}
	// Staged grid edits: typed columns (serial, timestamptz, jsonb) get their values as text literals
	sp := func(v string) *string { return &v }
	saved, err := d.SaveChanges(c.ID, dbclient.Changes{Schema: "app", Table: "users",
		Updates: []dbclient.RowUpdate{{Key: map[string]*string{"id": sp("1")}, Set: map[string]*string{"meta": sp(`{"vip": false}`), "created": sp("2026-01-02 03:04:05+00")}}},
		Inserts: []map[string]*string{{"email": sp("d@z.test")}}})
	if err != nil || saved.Updated != 1 || saved.Inserted != 1 {
		t.Fatalf("save: %v %+v", err, saved)
	}
	if _, err := d.SaveChanges(c.ID, dbclient.Changes{Schema: "app", Table: "users", Deletes: []map[string]*string{{"id": sp("999")}}}); err == nil {
		t.Fatal("deleting a missing row must fail")
	}
	if res, err := d.Query(c.ID, dbclient.QueryRequest{SQL: "SELECT count(*) || '|' || (SELECT meta->>'vip' FROM app.users WHERE id=1) FROM app.users"}); err != nil || *res.Rows[0][0] != "4|false" {
		t.Fatalf("after save: %v %+v", err, res)
	}
	if err := d.SetReadOnly(c.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SaveChanges(c.ID, dbclient.Changes{Schema: "app", Table: "users", Deletes: []map[string]*string{{"id": sp("1")}}}); err == nil {
		t.Fatal("read-only connection saved changes")
	}
	for _, q := range []string{"DELETE FROM app.users", "DROP TABLE app.users", "WITH d AS (DELETE FROM app.users RETURNING *) SELECT * FROM d"} {
		if _, err := d.Query(c.ID, dbclient.QueryRequest{SQL: q, AllowWrite: true}); err == nil {
			t.Fatalf("read-only Postgres ran: %s", q)
		}
	}
	if res, err := d.Query(c.ID, dbclient.QueryRequest{SQL: "SELECT count(*) FROM app.users"}); err != nil || *res.Rows[0][0] != "4" {
		t.Fatalf("data changed or select failed: %v %+v", err, res)
	}
}

func TestMultiFolderProjects(t *testing.T) {
	ev := setup(t)
	extra := filepath.Join(ev.home, "work")
	// Group folder "acct-a": a Laravel app with a git remote that uses an SSH host alias
	lara := filepath.Join(extra, "acct-a", "shop")
	write(t, filepath.Join(lara, "artisan"), "#!/usr/bin/env php\n")
	write(t, filepath.Join(lara, "composer.json"), `{"require":{"php":"^8.2","laravel/framework":"^11.0"}}`)
	write(t, filepath.Join(lara, "composer.lock"), `{"packages":[{"name":"laravel/framework","version":"v11.9.2"}]}`)
	write(t, filepath.Join(lara, "vendor", "autoload.php"), "<?php")
	write(t, filepath.Join(lara, "public", "index.php"), `<?php echo "laravel-public php=" . PHP_MAJOR_VERSION . "." . PHP_MINOR_VERSION;`)
	write(t, filepath.Join(lara, ".git", "HEAD"), "ref: refs/heads/develop\n")
	write(t, filepath.Join(lara, ".git", "config"), "[remote \"origin\"]\n\turl = git@personal:itismowgli/shop.git\n")
	// Group folder "acct-b": a Next.js-style app (tiny server instead of next, no download needed)
	next := filepath.Join(extra, "acct-b", "dash")
	write(t, filepath.Join(next, "package.json"), `{"name":"dash","scripts":{"dev":"node server.js"},"dependencies":{"next":"15.1.0"}}`)
	write(t, filepath.Join(next, "node_modules", ".keep"), "")
	write(t, filepath.Join(next, "server.js"), `const http=require('http');const i=process.argv.indexOf('--port');const port=i>0?process.argv[i+1]:process.env.PORT;
const h=process.argv[process.argv.indexOf('--hostname')+1];
http.createServer((q,r)=>r.end('next-ok host='+h+' path='+q.url)).listen(port,'127.0.0.1');`)
	write(t, filepath.Join(next, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(next, ".git", "config"), "[remote \"origin\"]\n\turl = https://user:SECRET-TOKEN@gitlab.example.com/team-b/dash.git\n")
	// Any language: a custom command (Python's built-in web server)
	py := filepath.Join(extra, "acct-b", "pydocs")
	// A plain socketserver (http.server's HTTPServer does a reverse-DNS lookup on start, slow on some networks)
	write(t, filepath.Join(py, "server.py"), `import http.server, socketserver, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b"python-served")
    def log_message(self, *a): pass
socketserver.TCPServer.allow_reuse_address = True  # like http.server and most dev servers
socketserver.TCPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
`)
	write(t, filepath.Join(py, ".git", "HEAD"), "ref: refs/heads/main\n")
	// Same name as a project in the main folder
	write(t, filepath.Join(extra, "acct-a", "hello", "index.php"), `<?php echo "second hello";`)

	if _, err := ev.e.UpdateSettings(engine.SettingsPatch{Roots: &[]config.Root{{Path: ev.sites, Label: "localhost"}, {Path: "/no/such", Label: "x"}}}); err == nil {
		t.Fatal("missing folder accepted")
	}
	if _, err := ev.e.UpdateSettings(engine.SettingsPatch{Roots: &[]config.Root{{Path: ev.sites, Label: "same"}, {Path: extra, Label: "same"}}}); err == nil {
		t.Fatal("duplicate labels accepted")
	}
	if _, err := ev.e.UpdateSettings(engine.SettingsPatch{Roots: &[]config.Root{{Path: ev.sites, Label: "localhost"}, {Path: extra, Label: "work"}}}); err != nil {
		t.Fatal(err)
	}

	byID := map[string]engine.SiteView{}
	for _, s := range ev.e.Sites() {
		byID[s.Folder] = s
	}
	shop, ok := byID["work/acct-a/shop"]
	if !ok {
		t.Fatalf("Laravel app not found; have %v", keys(byID))
	}
	if shop.Type != "laravel" || shop.Framework.Version != "11.9.2" || shop.Docroot != "public" || shop.Group != "acct-a" || shop.Root != "work" {
		t.Fatalf("laravel detection: %+v", shop)
	}
	if shop.Git == nil || shop.Git.Branch != "develop" || shop.Git.Host != "personal" || shop.Git.Account != "itismowgli" || shop.Git.Repo != "shop" {
		t.Fatalf("git: %+v", shop.Git)
	}
	if _, has83 := lsruntime.FindPHP("8.3"); has83 && shop.PHP < "8.2" {
		t.Fatalf("PHP for ^8.2 should be >= 8.2, got %s", shop.PHP)
	}
	dash := byID["work/acct-b/dash"]
	if dash.Type != "nextjs" || dash.Runtime != "app" || dash.Framework.PM != "npm" || dash.Git.Account != "team-b" {
		t.Fatalf("next detection: %+v %+v", dash, dash.Git)
	}
	if strings.Contains(dash.Git.Remote, "SECRET-TOKEN") {
		t.Fatal("git token leaked into the project info")
	}
	if h := byID["work/acct-a/hello"]; h.Domain != "hello-acct-a.test" || byID["hello"].Domain != "hello.test" {
		t.Fatalf("duplicate names need different addresses: %s vs %s", h.Domain, byID["hello"].Domain)
	}

	t.Run("Laravel app served from public/", func(t *testing.T) {
		ev.set("work/acct-a/shop", engine.SitePatch{Enabled: on()})
		code, body := ev.get("https://shop.test/")
		if code != 200 || !strings.HasPrefix(body, "laravel-public php=") {
			t.Fatalf("got %d %q", code, body)
		}
	})

	t.Run("Next.js dev server behind its address, on 127.0.0.1 only", func(t *testing.T) {
		if _, err := exec.LookPath("npm"); err != nil {
			t.Skip("npm not installed")
		}
		ev.set("work/acct-b/dash", engine.SitePatch{Enabled: on()})
		code, body := ev.get("https://dash.test/some/page")
		if code != 200 || body != "next-ok host=127.0.0.1 path=/some/page" {
			t.Fatalf("got %d %q", code, body)
		}
		s, _ := siteByFolder(ev.e, "work/acct-b/dash")
		if s.Port < 3100 {
			t.Fatalf("no port assigned: %d", s.Port)
		}
		ev.set("work/acct-b/dash", engine.SitePatch{Enabled: off()})
		time.Sleep(500 * time.Millisecond)
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", s.Port), time.Second); err == nil {
			c.Close()
			t.Fatal("dev server still running after switching the project off")
		}
	})

	t.Run("a port taken by another program moves the project to a free one", func(t *testing.T) {
		if _, err := exec.LookPath("npm"); err != nil {
			t.Skip("npm not installed")
		}
		s, _ := siteByFolder(ev.e, "work/acct-b/dash")
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.Port)) // someone else's dev server
		if err != nil {
			t.Skip("port busy already")
		}
		defer l.Close()
		ev.set("work/acct-b/dash", engine.SitePatch{Enabled: on()})
		if code, body := ev.get("https://dash.test/"); code != 200 || !strings.HasPrefix(body, "next-ok") {
			t.Fatalf("got %d %q", code, body)
		}
		if moved, _ := siteByFolder(ev.e, "work/acct-b/dash"); moved.Port == s.Port {
			t.Fatal("port was not moved")
		}
		ev.set("work/acct-b/dash", engine.SitePatch{Enabled: off()})
	})

	t.Run("any language: custom command with {port}", func(t *testing.T) {
		if _, err := exec.LookPath("python3"); err != nil {
			t.Skip("python3 not installed")
		}
		if err := ev.e.UpdateSite("work/acct-b/pydocs", engine.SitePatch{Enabled: on(), Kind: str("command")}); err == nil {
			t.Fatal("command type without a command accepted")
		}
		ev.set("work/acct-b/pydocs", engine.SitePatch{Enabled: on(), Kind: str("command"), Command: str("python3 server.py {port}")})
		if code, body := ev.get("https://pydocs.test/"); code != 200 || body != "python-served" {
			t.Fatalf("got %d %q", code, body)
		}
		ev.set("work/acct-b/pydocs", engine.SitePatch{Enabled: off()})
	})

	t.Run("removing a folder hides its projects", func(t *testing.T) {
		ev.set("work/acct-a/shop", engine.SitePatch{Enabled: off()})
		if _, err := ev.e.UpdateSettings(engine.SettingsPatch{Roots: &[]config.Root{{Path: ev.sites, Label: "localhost"}}}); err != nil {
			t.Fatal(err)
		}
		if _, ok := siteByFolder(ev.e, "work/acct-a/shop"); ok {
			t.Fatal("project of a removed folder still listed")
		}
	})
}

func keys(m map[string]engine.SiteView) []string {
	var k []string
	for id := range m {
		k = append(k, id)
	}
	return k
}

func TestProjectOverrides(t *testing.T) {
	ev := setup(t)
	write(t, filepath.Join(ev.sites, "ini", "index.php"), `<?php echo ini_get("memory_limit");`)
	write(t, filepath.Join(ev.sites, "envapp", "server.py"), `import http.server, socketserver, os, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(os.environ.get("GREETING", "missing").encode())
socketserver.TCPServer.allow_reuse_address = True
socketserver.TCPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
`)
	ini := map[string]string{"memory_limit": "321M"}
	ev.set("ini", engine.SitePatch{Enabled: on(), PHPIni: &ini})
	if code, body := ev.get("https://ini.test/"); code != 200 || body != "321M" {
		t.Fatalf("per-project php.ini: %d %q", code, body)
	}
	if code, body := ev.get("https://hello.test/"); code == 200 && strings.Contains(body, "321M") {
		t.Fatal("override leaked to another project")
	}
	bad := map[string]string{"disable_functions": "x"}
	if err := ev.e.UpdateSite("ini", engine.SitePatch{PHPIni: &bad}); err == nil {
		t.Fatal("php.ini key outside the allowlist accepted")
	}
	inj := map[string]string{"memory_limit": "1M\nphp_admin_value[open_basedir]=/"}
	if err := ev.e.UpdateSite("ini", engine.SitePatch{PHPIni: &inj}); err == nil {
		t.Fatal("multi-line php.ini value accepted")
	}

	vars := map[string]string{"GREETING": "hello from env"}
	ev.set("envapp", engine.SitePatch{Enabled: on(), Kind: str("command"), Command: str("python3 server.py {port}"), EnvVars: &vars})
	if code, body := ev.get("https://envapp.test/"); code != 200 || body != "hello from env" {
		t.Fatalf("env vars: %d %q", code, body)
	}
	for _, bad := range []map[string]string{{"PATH": "/tmp"}, {"1BAD": "x"}, {"OK": "two\nlines"}} {
		b := bad
		if err := ev.e.UpdateSite("envapp", engine.SitePatch{EnvVars: &b}); err == nil {
			t.Fatalf("bad variable accepted: %v", bad)
		}
	}
	port := 0
	if err := ev.e.UpdateSite("envapp", engine.SitePatch{Kind: str("docker"), Command: str("docker compose up"), Port: &port}); err == nil {
		t.Fatal("docker project without a port accepted")
	}
	ev.set("envapp", engine.SitePatch{Enabled: off()})
	ev.set("ini", engine.SitePatch{Enabled: off()})
}

func TestAddons(t *testing.T) {
	for _, b := range []string{"mailpit", "valkey-server", "memcached"} {
		if _, err := exec.LookPath("/opt/homebrew/bin/" + b); err != nil {
			t.Skip(b + " not installed")
		}
	}
	ev := setup(t)
	smtpPort, uiPort, kvPort, mcPort := freePort(t), freePort(t), freePort(t), freePort(t)
	on := true
	for id, p := range map[string]engine.AddonPatch{
		"mailpit":   {Enabled: &on, Port: &smtpPort, UIPort: &uiPort},
		"valkey":    {Enabled: &on, Port: &kvPort},
		"memcached": {Enabled: &on, Port: &mcPort},
	} {
		if err := ev.e.SetAddon(id, p); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	write(t, filepath.Join(ev.sites, "mailer", "index.php"), `<?php echo mail("someone@example.test", "Hello from PHP", "body") ? "sent" : "failed";`)
	ev.set("mailer", engine.SitePatch{Enabled: on2()})

	// PHP mail() goes to Mailpit without any project setup
	if code, body := ev.get("https://mailer.test/"); code != 200 || body != "sent" {
		t.Fatalf("mail(): %d %q", code, body)
	}
	count := func() string {
		_, body := ev.get("https://mail.test/api/v1/messages")
		return body
	}
	if b := count(); !strings.Contains(b, "Hello from PHP") {
		t.Fatalf("message not in Mailpit (inbox via the front door): %s", b)
	}
	if _, err := ev.e.AddonTestMail(); err != nil {
		t.Fatal(err)
	}
	if b := count(); !strings.Contains(b, "Test email from LocalSites") {
		t.Fatalf("test email missing: %s", b)
	}

	// Caches answer, and flush empties them
	talk := func(port int, send string) string {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		c.Write([]byte(send))
		buf := make([]byte, 256)
		n, _ := c.Read(buf)
		return string(buf[:n])
	}
	if r := talk(kvPort, "SET k v\r\n"); !strings.HasPrefix(r, "+OK") {
		t.Fatalf("valkey: %q", r)
	}
	if r := talk(mcPort, "set k 0 0 1\r\nv\r\n"); !strings.HasPrefix(r, "STORED") {
		t.Fatalf("memcached: %q", r)
	}
	for _, id := range []string{"valkey", "memcached"} {
		if _, err := ev.e.AddonFlush(id); err != nil {
			t.Fatalf("flush %s: %v", id, err)
		}
	}
	if r := talk(kvPort, "EXISTS k\r\n"); !strings.HasPrefix(r, ":0") {
		t.Fatalf("valkey not flushed: %q", r)
	}
	if r := talk(mcPort, "get k\r\n"); !strings.HasPrefix(r, "END") {
		t.Fatalf("memcached not flushed: %q", r)
	}
	for _, c := range ev.e.Doctor() {
		if c.Area == "Services" && (strings.HasPrefix(c.Name, "Mailpit") || strings.HasPrefix(c.Name, "Valkey") || strings.HasPrefix(c.Name, "Memcached")) && c.Status != "ok" {
			t.Fatalf("doctor: %+v", c)
		}
	}
	// Two add-ons can't share a port
	if err := ev.e.SetAddon("redis", engine.AddonPatch{Enabled: &on, Port: &kvPort}); err == nil {
		t.Fatal("port clash accepted")
	}
	off := false
	for _, id := range []string{"mailpit", "valkey", "memcached"} {
		if err := ev.e.SetAddon(id, engine.AddonPatch{Enabled: &off}); err != nil {
			t.Fatal(err)
		}
	}
	ev.e.Apply()
	if portFreeE2E(kvPort) == false || portFreeE2E(smtpPort) == false {
		t.Fatal("add-ons still running after switching off")
	}
}

func on2() *bool { b := true; return &b }

func portFreeE2E(p int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func TestShareRealSite(t *testing.T) {
	ev := setup(t)
	bin := filepath.Join(ev.home, "fake-tunnel")
	write(t, bin, "#!/bin/sh\necho 'https://e2e-test.trycloudflare.com'\nsleep 60\n")
	os.Chmod(bin, 0o755)
	old := share.Providers
	defer func() { share.Providers = old }()
	p := share.Providers[0]
	p.Bin = bin
	share.Providers = []share.Provider{p}

	if _, err := ev.e.StartShare("hello", "cloudflared", "pw", 5, false); err == nil {
		t.Fatal("shared a project that is switched off")
	}
	ev.set("hello", engine.SitePatch{Enabled: on()})
	if _, err := ev.e.StartShare("hello", "cloudflared", "", 5, false); err == nil {
		t.Fatal("shared without a password and without asking for that")
	}
	s, err := ev.e.StartShare("hello", "cloudflared", "pw", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "http://"+s.Gateway+"/", nil)
	req.SetBasicAuth("x", "pw")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(string(body), "php=8.5") {
		t.Fatalf("through the gateway: %d %q", res.StatusCode, body)
	}
	// .env exists in the project and Apache would refuse it too, but the gateway never even asks
	req2, _ := http.NewRequest("GET", "http://"+s.Gateway+"/.env", nil)
	req2.SetBasicAuth("x", "pw")
	if res2, err := http.DefaultClient.Do(req2); err != nil || res2.StatusCode != 404 {
		t.Fatalf(".env through the share: %v %v", err, res2)
	}
	if st := ev.e.Status(); len(st.Shares) != 1 || st.Shares[0].URL != "https://e2e-test.trycloudflare.com" {
		t.Fatalf("status shares: %+v", st.Shares)
	}
	ev.e.StopAll()
	if len(ev.e.Shares.List()) != 0 {
		t.Fatal("share survived StopAll")
	}
}

func TestAdminer(t *testing.T) {
	ev := setup(t)
	tampered := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<?php system($_GET['x']);") }))
	defer tampered.Close()
	engine.AdminerURLOverride = tampered.URL
	defer func() { engine.AdminerURLOverride = "" }()
	if _, err := ev.e.SetAdminer(true); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("tampered download accepted: %v", err)
	}
	real := os.Getenv("ADMINER_FILE")
	if real == "" {
		t.Skip("set ADMINER_FILE to a downloaded adminer-6.2.0-en.php to test serving it")
	}
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, real) }))
	defer good.Close()
	engine.AdminerURLOverride = good.URL
	if _, err := ev.e.SetAdminer(true); err != nil {
		t.Fatal(err)
	}
	if code, body := ev.get("https://adminer.test/"); code != 200 || !strings.Contains(body, "Adminer") {
		t.Fatalf("adminer.test: %d %.200q", code, body)
	}
	if _, err := ev.e.SetAdminer(false); err != nil {
		t.Fatal(err)
	}
	if code, _ := ev.get("https://adminer.test/"); code == 200 {
		t.Fatal("still served after switching off")
	}
}

func TestAgentApprovals(t *testing.T) {
	ev := setup(t)
	srv := &api.Server{E: ev.e}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	c, err := api.Dial(ev.e.P)
	if err != nil {
		t.Fatal(err)
	}
	agent, app := c.As("mcp"), c.As("app")
	pending := func() []api.Approval {
		var list []api.Approval
		for i := 0; i < 50; i++ {
			raw, _ := app.Call("approvals.list", nil)
			json.Unmarshal(raw, &list)
			if len(list) > 0 {
				return list
			}
			time.Sleep(50 * time.Millisecond)
		}
		return nil
	}
	// Denied: nothing happens
	done := make(chan error, 1)
	go func() { _, err := agent.Call("stop", nil); done <- err }()
	list := pending()
	if len(list) != 1 || list[0].Method != "stop" {
		t.Fatalf("pending: %+v", list)
	}
	if _, err := agent.Call("approvals.decide", map[string]any{"id": list[0].ID, "approve": true}); err == nil {
		t.Fatal("an agent approved its own request")
	}
	if _, err := app.Call("approvals.decide", map[string]any{"id": list[0].ID, "approve": false}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("denied request: %v", err)
	}
	// Approved: the call runs (and reports its own result)
	go func() { _, err := agent.Call("addons.flush", map[string]any{"id": "valkey"}); done <- err }()
	list = pending()
	app.Call("approvals.decide", map[string]any{"id": list[0].ID, "approve": true})
	if err := <-done; err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("approved request should have run: %v", err)
	}
	// The app itself is never held up
	if _, err := app.Call("addons.flush", map[string]any{"id": "valkey"}); err == nil || strings.Contains(err.Error(), "approval") {
		t.Fatalf("app call: %v", err)
	}
}

func TestRootPassword(t *testing.T) {
	ev := setup(t)
	if _, ok := lsruntime.MySQL(); !ok {
		t.Skip("MySQL 8.4 not installed")
	}
	if _, err := ev.e.CreateDatabase("pwtest"); err != nil {
		t.Fatal(err)
	}
	if _, err := ev.e.SetRootPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := ev.e.SetRootPassword("has a space in it!"); err == nil {
		t.Fatal("password with spaces accepted")
	}
	pw, err := ev.e.SetRootPassword("")
	if err != nil || len(pw) != 24 {
		t.Fatalf("random password: %v %q", err, pw)
	}
	// Everything still works with the new password: admin access, a new database, the data browser
	if _, err := ev.e.CreateDatabase("pwtest2"); err != nil {
		t.Fatalf("after change: %v", err)
	}
	if _, err := ev.e.Data.Test("localsites"); err != nil {
		t.Fatalf("data browser after change: %v", err)
	}
	if got, _ := ev.e.Secrets.Get("mysql-root"); got != pw {
		t.Fatal("keychain not updated")
	}
}

func TestNginxProject(t *testing.T) {
	if _, _, _, ok := lsruntime.Nginx(); !ok {
		t.Skip("nginx not installed")
	}
	ev := setup(t)
	nginx := "nginx"
	ev.set("hello", engine.SitePatch{Enabled: on(), Server: &nginx})
	if code, body := ev.get("https://hello.test/"); code != 200 || !strings.HasPrefix(body, "php=8.5 via=direct") {
		t.Fatalf("nginx /: %d %q", code, body)
	}
	if code, body := ev.get("https://hello.test/some/pretty/url"); code != 200 || !strings.HasPrefix(body, "php=8.5") { // falls back to index.php
		t.Fatalf("nginx pretty url: %d %q", code, body)
	}
	if code, _ := ev.get("https://hello.test/.env"); code != 403 {
		t.Fatalf("dotfile served by nginx: %d", code)
	}
	if !ev.e.Status().Nginx.Running {
		t.Fatal("nginx not running")
	}
	apache := "apache"
	ev.set("hello", engine.SitePatch{Server: &apache})
	if code, body := ev.get("https://hello.test/pretty"); code != 200 || body != "php=8.5 via=rewrite" { // .htaccess works again
		t.Fatalf("back on apache: %d %q", code, body)
	}
	if ev.e.Status().Nginx.Running {
		t.Fatal("nginx still running with no projects")
	}
}

func TestAddonsDataSearchStorage(t *testing.T) {
	for _, b := range []string{"/opt/homebrew/opt/postgresql@17/bin/postgres", "/opt/homebrew/bin/meilisearch", "/opt/homebrew/bin/minio"} {
		if _, err := os.Stat(b); err != nil {
			t.Skip(b + " not installed")
		}
	}
	ev := setup(t)
	on := true
	pg, ms, mn, mnUI := freePort(t), freePort(t), freePort(t), freePort(t)
	for id, p := range map[string]engine.AddonPatch{"postgres": {Enabled: &on, Port: &pg}, "meilisearch": {Enabled: &on, Port: &ms, UIPort: &ms}, "minio": {Enabled: &on, Port: &mn, UIPort: &mnUI}} {
		if err := ev.e.SetAddon(id, p); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	for _, l := range ev.e.Apply() {
		if strings.Contains(strings.ToLower(l), "fail") || strings.Contains(l, "isn't answering") {
			t.Fatalf("apply: %s", l)
		}
	}
	// PostgreSQL: initialised on first start, browsable, keeps data across a restart
	if _, err := ev.e.Data.Test("localsites-pg"); err != nil {
		t.Fatalf("postgres via data browser: %v", err)
	}
	if err := ev.e.Data.SetReadOnly("localsites-pg", false); err != nil {
		t.Fatal(err)
	}
	if _, err := ev.e.Data.Query("localsites-pg", dbclient.QueryRequest{SQL: "CREATE TABLE kept (x int); INSERT INTO kept VALUES (42)", AllowWrite: true}); err != nil {
		t.Fatal(err)
	}
	off := false
	ev.e.SetAddon("postgres", engine.AddonPatch{Enabled: &off})
	ev.e.Apply()
	ev.e.SetAddon("postgres", engine.AddonPatch{Enabled: &on})
	ev.e.Apply()
	ev.e.Data.Close()
	if r, err := ev.e.Data.Query("localsites-pg", dbclient.QueryRequest{SQL: "SELECT x FROM kept"}); err != nil || len(r.Rows) != 1 || *r.Rows[0][0] != "42" {
		t.Fatalf("postgres data after restart: %v %+v", err, r)
	}
	// Meilisearch and MinIO answer, and their pages open at name.test
	if code, body := ev.get(fmt.Sprintf("https://meilisearch.test/health")); code != 200 || !strings.Contains(body, "available") {
		t.Fatalf("meilisearch: %d %q", code, body)
	}
	res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/minio/health/live", mn))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("minio: %v %v", err, res)
	}
	if code, _ := ev.get("https://minio.test/"); code != 200 {
		t.Fatalf("minio console: %d", code)
	}
	for _, c := range ev.e.Doctor() {
		if c.Area == "Services" && (strings.HasPrefix(c.Name, "PostgreSQL") || strings.HasPrefix(c.Name, "Meilisearch") || strings.HasPrefix(c.Name, "MinIO")) && c.Status != "ok" {
			t.Fatalf("doctor: %+v", c)
		}
	}
}

func TestSiteContextHasNoSecrets(t *testing.T) {
	ev := setup(t)
	ev.set("hello", engine.SitePatch{Enabled: on()})
	c, err := ev.e.SiteContext("hello")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	if !slicesContains(c.EnvKeys, "SECRET") || strings.Contains(string(b), "never-served") {
		t.Fatalf("env keys must be listed without values: %s", b)
	}
	if !c.Running || c.ServedBy != "Apache + PHP-FPM" || c.PHP != "8.5" || c.URL != "https://hello.test" {
		t.Fatalf("context: %s", b)
	}
}

// MySQL stays small: memory footprint and data-folder size right after setup (guards the tuned my.cnf).
func TestMySQLFootprint(t *testing.T) {
	ev := setup(t)
	if _, ok := lsruntime.MySQL(); !ok {
		t.Skip("MySQL 8.4 not installed")
	}
	if _, err := ev.e.CreateDatabase("small"); err != nil {
		t.Fatal(err)
	}
	pid := ev.e.Sup.Status("mysql").PID
	out, err := exec.Command("/usr/bin/footprint", "-p", fmt.Sprint(pid)).Output()
	if err != nil {
		t.Skip("footprint not available")
	}
	m := regexp.MustCompile(`phys_footprint:\s+([\d.]+)\s+(KB|MB|GB)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("footprint output: %s", out)
	}
	mb, _ := strconv.ParseFloat(m[1], 64)
	if m[2] == "GB" {
		mb *= 1024
	}
	var size int64
	filepath.Walk(filepath.Join(ev.home, "Library"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.Contains(p, "/mysql/") {
			size += fi.Size()
		}
		return nil
	})
	if size == 0 { // data folder location differs by platform; fall back to the whole sandbox
		filepath.Walk(ev.home, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && strings.Contains(p, "mysql") {
				size += fi.Size()
			}
			return nil
		})
	}
	t.Logf("MySQL memory %.0f MB, data folder %.0f MB", mb, float64(size)/(1<<20))
	if mb > 200 {
		t.Fatalf("MySQL uses %.0f MB of memory; expected under 200 MB", mb)
	}
	if size > 120<<20 {
		t.Fatalf("MySQL data folder is %d MB; expected under 120 MB", size>>20)
	}
}

func TestMySQLSleepsWhenIdle(t *testing.T) {
	ev := setup(t)
	if _, ok := lsruntime.MySQL(); !ok {
		t.Skip("MySQL 8.4 not installed")
	}
	if _, err := ev.e.CreateDatabase("nap"); err != nil { // starts MySQL
		t.Fatal(err)
	}
	old := engine.MySQLIdle
	engine.MySQLIdle = 0
	defer func() { engine.MySQLIdle = old }()
	if !ev.e.StopIdleMySQL() || ev.e.Sup.Status("mysql").Running {
		t.Fatal("idle MySQL kept running")
	}
	// The data browser wakes it
	if _, err := ev.e.Data.Test("localsites"); err != nil {
		t.Fatalf("data browser after sleep: %v", err)
	}
	if !ev.e.Sup.Status("mysql").Running {
		t.Fatal("MySQL not woken")
	}
	// A running PHP project that uses LocalSites MySQL keeps it awake
	db := "localsites"
	ev.set("hello", engine.SitePatch{Enabled: on(), DBServer: &db})
	if ev.e.StopIdleMySQL() {
		t.Fatal("stopped MySQL that a running project uses")
	}
	if _, err := ev.e.DatabaseCredentials("nap"); err != nil {
		t.Fatal(err)
	}
}
