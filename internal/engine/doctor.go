package engine

import (
	"archive/zip"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/proc"
	"github.com/localsites/localsites/internal/runtime"
)

// Check is one diagnostic result.
type Check struct {
	Area   string `json:"area"` // Runtime | Services | Domains | Certificates | Project
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail | skip
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"` // what to do about it, in plain words or a command
	MS     int64  `json:"ms,omitempty"`  // time taken, for network checks
}

func ok(area, name, detail string) Check {
	return Check{Area: area, Name: name, Status: "ok", Detail: detail}
}
func warn(area, name, detail, fix string) Check {
	return Check{Area: area, Name: name, Status: "warn", Detail: detail, Fix: fix}
}
func fail(area, name, detail, fix string) Check {
	return Check{Area: area, Name: name, Status: "fail", Detail: detail, Fix: fix}
}

// Doctor checks the whole setup and every switched-on project.
func (e *Engine) Doctor() []Check {
	var out []Check
	cfg := e.Store.Get()

	// Runtime
	if bin, _, _, ok2 := runtime.Apache(); ok2 {
		out = append(out, ok("Runtime", "Apache installed", bin))
	} else {
		out = append(out, fail("Runtime", "Apache installed", "httpd not found", "brew install httpd"))
	}
	if base, ok2 := runtime.MySQL(); ok2 {
		out = append(out, ok("Runtime", "MySQL 8.4 installed", base))
	} else {
		out = append(out, warn("Runtime", "MySQL 8.4 installed", "not found", "brew install mysql@8.4"))
	}
	versions := runtime.PHPVersions()
	if len(versions) == 0 {
		out = append(out, fail("Runtime", "PHP", "no working PHP found", "brew install php"))
	}
	for _, v := range versions {
		if v.Warning != "" {
			out = append(out, warn("Runtime", "PHP "+v.Version, v.Full+": "+v.Warning, "brew reinstall php@"+v.Version))
		} else {
			out = append(out, ok("Runtime", "PHP "+v.Version, v.Full))
		}
	}

	// Services
	sites := e.Sites()
	out = append(out, procCheck("Apache", e.Sup.Status(procApache), true))
	out = append(out, portCheck("Services", "Apache port", fmt.Sprintf("127.0.0.1:%d", cfg.Settings.ApachePort)))
	if httpd, _, _, ok2 := runtime.Apache(); ok2 {
		conf := filepath.Join(e.P.Generated, "httpd.conf")
		if outb, err := exec.Command(httpd, "-t", "-f", conf).CombinedOutput(); err != nil {
			out = append(out, fail("Services", "Apache config", lastLine(string(outb)), "Open Logs → Apache, or run: localsites apply"))
		} else {
			out = append(out, ok("Services", "Apache config", "valid"))
		}
	}
	needPHP := map[string]bool{}
	for _, s := range sites {
		if s.Enabled {
			needPHP[s.PHP] = true
		}
	}
	for v := range needPHP {
		out = append(out, procCheck("PHP "+v, e.Sup.Status("php-"+v), true))
	}
	if m, ok2 := e.mysqlSvc(); ok2 && m.Initialized() {
		out = append(out, procCheck("MySQL 8.4", e.Sup.Status(procMySQL), true))
		out = append(out, socketCheck("Services", "MySQL 8.4 socket", m.Socket))
	}
	for _, s := range sites {
		if s.Enabled && s.DBServer == config.DBMAMP {
			out = append(out, mampFix(socketCheck("Services", "MAMP PRO MySQL (used by "+s.Folder+")", e.mysqlSocketFor(s))))
			break
		}
	}

	out = append(out, e.addonChecks()...)
	for _, p := range WaitingForAccess() {
		out = append(out, fail("Services", "Folder access", p+": macOS is asking whether LocalSites may read this folder",
			"Click Allow in the macOS prompt, or turn LocalSites on in System Settings → Privacy & Security → Files and Folders"))
	}

	// Domains and certificates
	if e.Front.Running() {
		out = append(out, ok("Domains", "Front door", e.Front.Name()))
	} else {
		fix := "Start Laravel Valet: valet start"
		if e.Front.Name() == "builtin" {
			fix = "Restart LocalSites"
		}
		out = append(out, fail("Domains", "Front door", e.Front.Name()+" is not running", fix))
	}
	out = append(out, e.trustCheck())

	// Projects
	for _, s := range sites {
		if s.Enabled {
			out = append(out, e.CheckSite(s.Folder)...)
		}
	}
	return append(out, e.exposureChecks(sites)...)
}

// exposureChecks: things on this Mac that others could use. Valet's certificate authority key readable
// by every user (anyone with it can impersonate any website to this Mac), and dev servers that listen
// on every network instead of only 127.0.0.1 (people on the same Wi-Fi can open them).
func (e *Engine) exposureChecks(sites []SiteView) []Check {
	var out []Check
	key := filepath.Join(e.P.Home, ".config", "valet", "CA", "LaravelValetCASelfSigned.key")
	if st, err := os.Stat(key); err == nil {
		if st.Mode().Perm()&0o077 != 0 {
			out = append(out, warn("Certificates", "Valet's certificate key", "other users on this Mac can read "+key,
				"chmod 600 "+key))
		} else {
			out = append(out, ok("Certificates", "Valet's certificate key", "only you can read it"))
		}
	}
	ports := map[int]string{}
	for _, s := range sites {
		if s.Enabled && s.Runtime == "app" && s.Port > 0 {
			ports[s.Port] = s.Name
		}
	}
	if len(ports) == 0 {
		return out
	}
	raw, _ := exec.Command("/usr/sbin/lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
	open := map[int]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		addr, found := strings.CutPrefix(l, "n")
		if !found {
			continue
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			continue
		}
		var p int
		fmt.Sscan(port, &p)
		if ip := net.ParseIP(host); host == "*" || ip != nil && !ip.IsLoopback() {
			open[p] = true
		}
	}
	for p, name := range ports {
		if open[p] {
			out = append(out, warn("Projects", name+": reachable from the network", fmt.Sprintf("its dev server listens on every network on port %d, not only 127.0.0.1", p),
				"Start it on 127.0.0.1 (for example --host 127.0.0.1, or -p 127.0.0.1:PORT:PORT for Docker)"))
		}
	}
	return out
}

func procCheck(name string, st proc.Status, _ bool) Check {
	if st.Running {
		return ok("Services", name+" running", fmt.Sprintf("pid %d, since %s", st.PID, st.Since.Format("15:04:05")))
	}
	detail := "stopped"
	if st.LastErr != "" {
		detail = st.LastErr
	}
	return fail("Services", name+" running", detail, "Restart services in the app (or: localsites restart); see Logs")
}

// trustCheck tells whether browsers will trust the site certificates.
func (e *Engine) trustCheck() Check {
	if goruntime.GOOS != "darwin" {
		return Check{Area: "Certificates", Name: "Certificate authority trusted", Status: "skip", Detail: "check not available on this OS yet"}
	}
	name, file := "LocalSites Development CA", e.CA.RootPath()
	if e.Front.Name() == "valet" {
		name, file = "Laravel Valet CA", filepath.Join(e.P.Home, ".config", "valet", "CA", "LaravelValetCASelfSigned.pem")
	}
	out, _ := exec.Command("/usr/bin/security", "dump-trust-settings", "-d").CombinedOutput()
	if strings.Contains(string(out), strings.TrimSuffix(name, " CA")) {
		return ok("Certificates", name+" trusted", "browsers will show the padlock")
	}
	return fail("Certificates", name+" trusted", "browsers will warn “Your connection is not private”",
		"sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain \""+file+"\"")
}

// CheckSite tests everything one request to the project touches.
func (e *Engine) CheckSite(folder string) []Check {
	var s *SiteView
	for _, v := range e.Sites() {
		if v.Folder == folder {
			cp := v
			s = &cp
		}
	}
	area := "Project " + folder
	if s == nil {
		return []Check{fail(area, "Project", "no such folder", "")}
	}
	if !s.Enabled {
		return []Check{{Area: area, Name: "Switched on", Status: "skip", Detail: "project is off"}}
	}
	var out []Check
	for _, p := range s.Problems {
		out = append(out, fail(area, "Settings", p, "Fix the project's settings in the app"))
	}

	// DNS
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	addrs, err := net.DefaultResolver.LookupHost(ctx, s.Domain)
	cancel()
	dns := Check{Area: area, Name: "DNS " + s.Domain, MS: time.Since(start).Milliseconds()}
	if err != nil || len(addrs) == 0 {
		dns.Status, dns.Detail, dns.Fix = "fail", "does not resolve", "For Valet: valet start. Built-in: re-run the LocalSites installer (sets up .test DNS)."
	} else if ip := net.ParseIP(addrs[0]); ip == nil || !ip.IsLoopback() {
		dns.Status, dns.Detail, dns.Fix = "fail", "resolves to "+addrs[0]+", not this computer", "Check /etc/hosts and your DNS settings"
	} else {
		dns.Status, dns.Detail = "ok", strings.Join(addrs, ", ")
	}
	out = append(out, dns)

	// PHP pool and database socket
	if s.Runtime == "app" {
		out = append(out, portCheck(area, "Dev server", fmt.Sprintf("127.0.0.1:%d", s.Port)))
	} else {
		out = append(out, socketCheck(area, "PHP "+s.PHP+" pool", e.fpmSock(s.Folder)))
	}
	dbName := "MySQL 8.4"
	if s.DBServer == config.DBMAMP {
		dbName = "MAMP PRO MySQL"
	}
	dbc := socketCheck(area, dbName+" (localhost)", e.mysqlSocketFor(*s))
	if s.DBServer == config.DBMAMP {
		dbc = mampFix(dbc)
	}
	if m, ok := e.mysqlSvc(); dbc.Status == "fail" && s.DBServer == config.DBLocalSites && s.Database == "" && (!ok || !m.Initialized()) {
		dbc.Status, dbc.Detail, dbc.Fix = "warn", "not set up yet (only matters if this project uses a database)", "Create a database in Databases, or set MySQL to MAMP PRO"
	}
	out = append(out, dbc)

	// HTTPS through the front door, as a browser would
	if dns.Status == "ok" {
		url := "https://" + s.Domain + "/"
		if p := e.Store.Get().Settings.HTTPSPort; e.Front.Name() == "builtin" && p != 443 {
			url = fmt.Sprintf("https://%s:%d/", s.Domain, p)
		}
		out = append(out, httpsCheck(area, url))
	}
	return out
}

func httpsCheck(area, url string) Check {
	c := Check{Area: area, Name: "HTTPS " + strings.TrimSuffix(strings.TrimPrefix(url, "https://"), "/")}
	get := func(insecure bool) (*http.Response, time.Duration, error) {
		client := &http.Client{Timeout: 15 * time.Second,
			Transport:     &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}}, //nolint:gosec // second try only, to tell "untrusted" from "down"
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		start := time.Now()
		res, err := client.Get(url)
		return res, time.Since(start), err
	}
	res, took, err := get(false)
	trusted := err == nil
	if err != nil && strings.Contains(err.Error(), "certificate") {
		res, took, err = get(true) // tells "untrusted certificate" apart from "site down"
	}
	c.MS = took.Milliseconds()
	if err != nil {
		c.Status, c.Detail, c.Fix = "fail", shortErr(err), "Check Services in the app; run: localsites doctor"
		return c
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	c.Detail = fmt.Sprintf("HTTP %d in %d ms", res.StatusCode, c.MS)
	switch {
	case !trusted:
		c.Status, c.Fix = "warn", "Site works but the certificate is not trusted; see Certificates above"
		c.Detail += " (certificate not trusted)"
	case res.StatusCode >= 500:
		c.Status, c.Fix = "fail", "The project's PHP code failed; open Logs → project errors / PHP"
	case res.StatusCode >= 400 && res.StatusCode != 401 && res.StatusCode != 403:
		c.Status, c.Fix = "warn", "Check the project's web root"
	default:
		c.Status = "ok"
		if loc := res.Header.Get("Location"); loc != "" {
			c.Detail += " → " + loc
		}
	}
	return c
}

func socketCheck(area, name, path string) Check {
	start := time.Now()
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	c := Check{Area: area, Name: name, MS: time.Since(start).Milliseconds()}
	if err != nil {
		c.Status, c.Detail, c.Fix = "fail", "not accepting connections", "Restart services in the app (or: localsites restart)"
		return c
	}
	conn.Close()
	c.Status, c.Detail = "ok", "accepting connections"
	return c
}

// mampFix gives the right advice when MAMP PRO's MySQL is down: LocalSites can't start it, so restarting
// LocalSites won't help. Either start it in MAMP PRO or move the project to LocalSites MySQL.
func mampFix(c Check) Check {
	if c.Status == "fail" {
		c.Fix = "Start MySQL in MAMP PRO, or move the project to LocalSites MySQL: in Import, copy its database, then click Connect"
	}
	return c
}

func portCheck(area, name, addr string) Check {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return fail(area, name, addr+" is not listening", "Restart services in the app (or: localsites restart)")
	}
	conn.Close()
	return ok(area, name, addr)
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i > 0 && len(s) > 120 {
		s = s[i+2:]
	}
	return s
}

/* ------------------------------------------------------------ debug bundle */

var secretLine = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|key)(\s*[=:]\s*)\S+`)

// DebugBundle zips logs, generated configs and settings (secrets removed) for bug reports.
func (e *Engine) DebugBundle() (string, error) {
	file := filepath.Join(e.P.Backups, "localsites-debug-"+time.Now().Format("20060102-150405")+".zip")
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = e.writeBundle(f, func(s string) string { return secretLine.ReplaceAllString(s, "$1$2[removed]") })
	return file, err
}

// writeBundle writes the debug zip to w, passing every file through clean.
// writeBundle writes the debug bundle and returns the names of the files in it. Left out: secrets,
// keys, dumps, saved database connections (hosts and logins; only a count goes in) and web access
// logs (visitors' addresses and requested URLs). Environment variable values become "(hidden)".
func (e *Engine) writeBundle(w io.Writer, clean func(string) string) ([]string, error) {
	zw := zip.NewWriter(w)
	var names []string
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err == nil {
			_, _ = io.WriteString(w, clean(content))
			names = append(names, name)
		}
	}
	var report strings.Builder
	for _, c := range e.Doctor() {
		fmt.Fprintf(&report, "[%s] %s / %s: %s %s\n", c.Status, c.Area, c.Name, c.Detail, c.Fix)
	}
	add("doctor.txt", report.String())
	add("system.txt", fmt.Sprintf("os=%s arch=%s front_door=%s\n", goruntime.GOOS, goruntime.GOARCH, e.Front.Name()))
	for _, dir := range []string{e.P.Config, e.P.Generated, e.P.Logs, e.P.SiteLogs} {
		entries, _ := os.ReadDir(dir)
		for _, de := range entries {
			n := de.Name()
			if de.IsDir() || strings.Contains(n, "secret") || strings.HasSuffix(n, ".key") || strings.HasSuffix(n, ".sql") || strings.Contains(n, "access") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, n))
			if err != nil {
				continue
			}
			if n == "connections.json" {
				var list []map[string]any
				_ = json.Unmarshal(b, &list)
				add("config/connections.txt", fmt.Sprintf("%d saved connection(s); details left out\n", len(list)))
				continue
			}
			if strings.HasSuffix(n, ".json") {
				var v any
				if json.Unmarshal(b, &v) == nil {
					if out, err := json.MarshalIndent(hideEnvValues(v), "", "  "); err == nil {
						b = out
					}
				}
			}
			if len(b) > 256<<10 { // last 256 KB of big logs
				b = b[len(b)-256<<10:]
			}
			add(filepath.Base(dir)+"/"+de.Name(), string(b))
		}
	}
	return names, zw.Close()
}

// hideEnvValues replaces every "env_vars" value (often API keys) with "(hidden)", keeping the names.
func hideEnvValues(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if env, ok := x.(map[string]any); ok && k == "env_vars" {
				for ek := range env {
					env[ek] = "(hidden)"
				}
				continue
			}
			t[k] = hideEnvValues(x)
		}
	case []any:
		for i := range t {
			t[i] = hideEnvValues(t[i])
		}
	}
	return v
}
