// Command localsites is the LocalSites CLI and background service.
//
//	localsites daemon                 run the background service (usually started automatically)
//	localsites status | sites         show what's running
//	localsites on|off <folder>        switch a project on/off
//	localsites set <folder> php=8.5 domain=shop.test docroot=public
//	localsites db list|create|backup|credentials <name>
//	localsites import sources|databases|database ...
//	localsites call <method> '{"json":"params"}'   any API method (for scripts and AI agents)
//
// Add --json to any command for machine-readable output.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/localsites/localsites/internal/api"
	"github.com/localsites/localsites/internal/daemon"
	"github.com/localsites/localsites/internal/engine"
	"github.com/localsites/localsites/internal/importers"
	"github.com/localsites/localsites/internal/mcp"
	"github.com/localsites/localsites/internal/paths"
	"github.com/localsites/localsites/internal/services"
	"github.com/localsites/localsites/internal/version"
)

var jsonOut bool

func main() {
	var args []string
	for _, a := range os.Args[1:] {
		if a == "--json" {
			jsonOut = true
		} else {
			args = append(args, a)
		}
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage()
		return
	}
	if len(args) > 1 && args[0] == "update-apply" { // the app-update helper (when the service runs from this tool)
		if err := daemon.ApplyUpdate(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if args[0] == "daemon" {
		if err := daemon.Run(); err != nil {
			fail(err)
		}
		return
	}
	self, _ := os.Executable()
	if args[0] == "mcp" && len(args) > 1 && args[1] == "config" {
		printMCPConfig(self)
		return
	}
	c, err := daemon.Ensure(self)
	if err != nil {
		fail(err)
	}
	if err := run(c.As("cli"), args); err != nil {
		fail(err)
	}
}

func run(c *api.Client, a []string) error {
	need := func(n int, help string) error {
		if len(a) < n {
			return fmt.Errorf("usage: localsites %s", help)
		}
		return nil
	}
	switch a[0] {
	case "status":
		return show(c, "status", nil, printStatus)
	case "sites", "ls":
		return show(c, "sites.list", nil, printSites)
	case "on", "off":
		if err := need(2, a[0]+" <folder>"); err != nil {
			return err
		}
		on := a[0] == "on"
		return show(c, "sites.update", map[string]any{"folder": a[1], "enabled": on}, printLog)
	case "set":
		if err := need(3, "set <folder> key=value ... (php, domain, docroot, db_server, database, enabled)"); err != nil {
			return err
		}
		p := map[string]any{"folder": a[1]}
		for _, kv := range a[2:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("expected key=value, got %q", kv)
			}
			if k == "enabled" {
				p[k] = v == "1" || v == "on" || v == "true" || v == "yes"
			} else {
				p[k] = v
			}
		}
		return show(c, "sites.update", p, printLog)
	case "quit":
		if _, err := c.Call("quit", nil); err != nil {
			return err
		}
		// Wait until the service and everything it runs have stopped
		for i := 0; i < 240; i++ {
			if _, err := api.Dial(paths.Default()); err != nil {
				fmt.Println("LocalSites stopped")
				return nil
			}
			time.Sleep(250 * time.Millisecond)
		}
		return fmt.Errorf("LocalSites is still stopping; check %s", paths.Default().Logs)
	case "apply", "restart", "stop":
		return show(c, a[0], nil, printLog)
	case "logs":
		if err := need(2, "logs apache|mysql|site-error|site-access|site-php [folder]"); err != nil {
			return err
		}
		p := map[string]any{"kind": a[1]}
		if len(a) > 2 {
			p["folder"] = a[2]
		}
		return show(c, "logs", p, func(b []byte) error {
			var r struct{ Text string }
			_ = json.Unmarshal(b, &r)
			fmt.Println(r.Text)
			return nil
		})
	case "db":
		if err := need(2, "db list|create|backup|credentials <name>"); err != nil {
			return err
		}
		switch a[1] {
		case "list", "ls":
			return show(c, "db.list", nil, printDBs)
		case "create", "backup", "credentials":
			if err := need(3, "db "+a[1]+" <name>"); err != nil {
				return err
			}
			return show(c, "db."+a[1], map[string]any{"name": a[2]}, printJSON)
		}
	case "import":
		return importCmd(c, a[1:])
	case "open":
		if err := need(3, "open <folder> finder|terminal|vscode|cursor|browser"); err != nil {
			return err
		}
		_, err := c.Call("open", map[string]any{"folder": a[1], "app": a[2]})
		return err
	case "methods":
		return show(c, "methods", nil, printJSON)
	case "version":
		return show(c, "version", nil, func(b []byte) error {
			var v struct {
				Version   string
				Changelog []struct{ Version, Date, Title string }
			}
			_ = json.Unmarshal(b, &v)
			fmt.Println("LocalSites", v.Version)
			for _, c := range v.Changelog {
				fmt.Printf("  %s  %s  %s\n", c.Version, c.Date, c.Title)
			}
			return nil
		})
	case "settings":
		if len(a) == 1 {
			return show(c, "settings.get", nil, printJSON)
		}
		// localsites settings key=value ...   (php.<key>=value for php.ini, empty value resets)
		p := map[string]any{}
		ini := map[string]any{}
		for _, kv := range a[1:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("expected key=value, got %q", kv)
			}
			switch {
			case strings.HasPrefix(k, "php."):
				ini[strings.TrimPrefix(k, "php.")] = v
			case strings.HasSuffix(k, "_port"):
				var n int
				if _, err := fmt.Sscan(v, &n); err != nil {
					return fmt.Errorf("%s must be a number", k)
				}
				p[k] = n
			case k == "start_at_login":
				p[k] = v == "on" || v == "true" || v == "1" || v == "yes"
			default:
				p[k] = v
			}
		}
		if len(ini) > 0 {
			p["php_ini"] = ini
		}
		return show(c, "settings.update", p, printLog)
	case "folders":
		// localsites folders | folders add <path> [label] [auto|flat|groups] | folders remove <label>
		b, err := c.Call("settings.get", nil)
		if err != nil {
			return err
		}
		var st struct {
			Roots []map[string]string `json:"roots"`
		}
		_ = json.Unmarshal(b, &st)
		if len(a) == 1 {
			w := table("LABEL", "LAYOUT", "PATH")
			for _, r := range st.Roots {
				l := r["layout"]
				if l == "" {
					l = "auto"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", r["label"], l, r["path"])
			}
			return w.Flush()
		}
		switch a[1] {
		case "add":
			if err := need(3, "folders add <path> [label] [auto|flat|groups]"); err != nil {
				return err
			}
			r := map[string]string{"path": a[2]}
			if len(a) > 3 {
				r["label"] = a[3]
			}
			if len(a) > 4 {
				r["layout"] = a[4]
			}
			st.Roots = append(st.Roots, r)
		case "remove":
			if err := need(3, "folders remove <label>"); err != nil {
				return err
			}
			var keep []map[string]string
			for _, r := range st.Roots {
				if r["label"] != a[2] {
					keep = append(keep, r)
				}
			}
			st.Roots = keep
		default:
			return fmt.Errorf("usage: localsites folders [add <path> [label] [layout] | remove <label>]")
		}
		return show(c, "settings.update", map[string]any{"roots": st.Roots}, printLog)
	case "install":
		if err := need(2, "install <project>"); err != nil {
			return err
		}
		return show(c, "sites.install", map[string]any{"folder": a[1]}, printJSON)
	case "connect-db":
		if err := need(3, "connect-db <folder> <database>"); err != nil {
			return err
		}
		return show(c, "sites.connect_db", map[string]any{"folder": a[1], "name": a[2]}, printLog)
	case "restore-env":
		if err := need(2, "restore-env <folder>"); err != nil {
			return err
		}
		return show(c, "sites.restore_env", map[string]any{"folder": a[1]}, printLog)
	case "mcp":
		allow := len(a) > 1 && a[1] == "--allow-secrets"
		srv := &mcp.Server{API: c.As("mcp"), AllowSecrets: allow, Version: version.Version}
		return srv.Serve(os.Stdin, os.Stdout)
	case "doctor":
		return show(c, "doctor", nil, printChecks)
	case "check":
		if err := need(2, "check <folder>"); err != nil {
			return err
		}
		return show(c, "sites.check", map[string]any{"folder": a[1]}, printChecks)
	case "debug-bundle":
		return show(c, "debug.bundle", nil, func(b []byte) error {
			var r struct{ File string }
			_ = json.Unmarshal(b, &r)
			fmt.Println("Debug bundle (secrets removed):", r.File)
			return nil
		})
	case "call":
		if err := need(2, "call <method> [json]"); err != nil {
			return err
		}
		var params any
		if len(a) > 2 {
			if err := json.Unmarshal([]byte(a[2]), &params); err != nil {
				return fmt.Errorf("params must be JSON: %w", err)
			}
		}
		jsonOut = true
		return show(c, a[1], params, nil)
	}
	return fmt.Errorf("unknown command %q (try: localsites help)", a[0])
}

func importCmd(c *api.Client, a []string) error {
	if len(a) == 0 || a[0] == "sources" {
		return show(c, "import.sources", nil, func(b []byte) error {
			var src []importers.Source
			_ = json.Unmarshal(b, &src)
			w := table("SOURCE", "NAME", "INSTALLED", "MYSQL")
			for _, s := range src {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, s.Name, yes(s.Found), yes(s.MySQLUp))
			}
			return w.Flush()
		})
	}
	// import databases <source> [--user root]          (password read from stdin or LOCALSITES_SOURCE_PASSWORD)
	// import database <source> <name> [--user root]
	user := "root"
	var rest []string
	for i := 1; i < len(a); i++ {
		if a[i] == "--user" && i+1 < len(a) {
			user = a[i+1]
			i++
		} else {
			rest = append(rest, a[i])
		}
	}
	if len(rest) < 1 {
		return fmt.Errorf("usage: localsites import databases <source> | import database <source> <name>")
	}
	pw := os.Getenv("LOCALSITES_SOURCE_PASSWORD")
	if pw == "" {
		fmt.Fprintf(os.Stderr, "MySQL password for %s on %s: ", user, rest[0])
		pw = readLine()
	}
	p := map[string]any{"source": rest[0], "user": user, "password": pw}
	switch a[0] {
	case "databases":
		return show(c, "import.databases", p, printJSON)
	case "database":
		if len(rest) < 2 {
			return fmt.Errorf("usage: localsites import database <source> <name>")
		}
		p["name"] = rest[1]
		return show(c, "import.database", p, printReport)
	}
	return fmt.Errorf("unknown import command %q", a[0])
}

/* ---------------------------------------------------------------- output */

func show(c *api.Client, method string, params any, pretty func([]byte) error) error {
	b, err := c.Call(method, params)
	if err != nil {
		return err
	}
	if jsonOut || pretty == nil {
		var v any
		_ = json.Unmarshal(b, &v)
		out, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	return pretty(b)
}

func printJSON(b []byte) error {
	var v any
	_ = json.Unmarshal(b, &v)
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
	return nil
}

func printLog(b []byte) error {
	var r struct{ Log []string }
	_ = json.Unmarshal(b, &r)
	for _, l := range r.Log {
		fmt.Println("•", l)
	}
	return nil
}

func printStatus(b []byte) error {
	var s engine.Status
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	w := table("SERVICE", "STATE", "DETAIL")
	fmt.Fprintf(w, "Front door\t%s\t%s\n", state(s.FrontUp), s.FrontDoor)
	fmt.Fprintf(w, "Apache\t%s\t%s\n", state(s.Apache.Running), s.Apache.LastErr)
	fmt.Fprintf(w, "MySQL 8.4\t%s\t%s\n", state(s.MySQL.Running), s.MySQL.LastErr)
	for _, p := range s.PHP {
		detail := fmt.Sprintf("%s · %d site(s)", p.Full, p.Sites)
		if p.Warning != "" {
			detail += " · " + p.Warning
		}
		fmt.Fprintf(w, "PHP %s\t%s\t%s\n", p.Version, state(p.Running), detail)
	}
	return w.Flush()
}

func printSites(b []byte) error {
	var sites []engine.SiteView
	if err := json.Unmarshal(b, &sites); err != nil {
		return err
	}
	w := table("", "PROJECT", "ADDRESS", "PHP", "WEB ROOT", "DATABASE")
	for _, s := range sites {
		on := "○"
		if s.Enabled {
			on = "●"
		}
		root := "/"
		if s.Docroot != "" {
			root = "/" + s.Docroot
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", on, s.Folder, "https://"+s.Domain, s.PHP, root, s.DBServer)
	}
	return w.Flush()
}

func printDBs(b []byte) error {
	var dbs []services.DBInfo
	if err := json.Unmarshal(b, &dbs); err != nil {
		return err
	}
	w := table("DATABASE", "TABLES", "SIZE", "USER")
	for _, d := range dbs {
		fmt.Fprintf(w, "%s\t%d\t%.1f MB\t%s\n", d.Name, d.Tables, d.SizeMB, d.Owner)
	}
	return w.Flush()
}

func printReport(b []byte) error {
	var r importers.Report
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	w := table("TABLE", "ROWS (SOURCE)", "ROWS (COPY)", "CHECK")
	for _, t := range r.Tables {
		check := "✓ identical"
		if !t.Match {
			check = "✗ " + t.Note
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\n", t.Table, t.SourceRows, t.DestRows, check)
	}
	w.Flush()
	if r.OK {
		fmt.Printf("\n✓ %s copied and verified in %s. Original untouched; its dump is kept at %s\n", r.Database, r.Duration, r.Backup)
	} else {
		fmt.Printf("\n✗ %s: some tables differ (see above). The original is untouched.\n", r.Database)
	}
	return nil
}

func printChecks(b []byte) error {
	var checks []engine.Check
	if err := json.Unmarshal(b, &checks); err != nil {
		return err
	}
	icons := map[string]string{"ok": "✓", "warn": "!", "fail": "✗", "skip": "–"}
	area, failed := "", 0
	for _, c := range checks {
		if c.Area != area {
			area = c.Area
			fmt.Printf("\n%s\n", area)
		}
		line := fmt.Sprintf("  %s %s", icons[c.Status], c.Name)
		if c.Detail != "" {
			line += "  " + c.Detail
		}
		fmt.Println(line)
		if c.Fix != "" && c.Status != "ok" {
			fmt.Println("      fix:", c.Fix)
		}
		if c.Status == "fail" {
			failed++
		}
	}
	if failed == 0 {
		fmt.Println("\nAll good.")
	} else {
		fmt.Printf("\n%d problem(s) found.\n", failed)
	}
	return nil
}

func printMCPConfig(self string) {
	b, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"localsites": map[string]any{"command": self, "args": []string{"mcp"}}}}, "", "  ")
	fmt.Printf(`Connect AI agents to LocalSites (Model Context Protocol, stdio).

Claude Code:
  claude mcp add localsites -- %q mcp

Claude Desktop (claude_desktop_config.json), Cursor (.cursor/mcp.json), and most other clients:
%s

Agents can read everything and change project settings. Stop/restart/quit need the user's approval
(confirm=true). Tools that show or take passwords are off; add "--allow-secrets" after "mcp" to enable them.
`, self, b)
}

func table(cols ...string) *tabwriter.Writer {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(cols, "\t"))
	return w
}

func state(on bool) string {
	if on {
		return "running"
	}
	return "stopped"
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func readLine() string {
	s, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(s, "\r\n")
}

func fail(err error) {
	if jsonOut {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintln(os.Stderr, string(b))
	} else {
		fmt.Fprintln(os.Stderr, "localsites:", err)
	}
	os.Exit(1)
}

func usage() {
	fmt.Print(`LocalSites: local PHP/MySQL sites with real HTTPS domains.

  localsites status                      services and PHP versions
  localsites sites                       projects and their addresses
  localsites on|off <folder>             switch a project on or off
  localsites set <folder> php=8.5 domain=shop.test docroot=public db_server=localsites
  localsites logs apache|mysql|site-error|site-access|site-php [folder]
  localsites db list | create <name> | backup <name> | credentials <name>
  localsites import sources
  localsites import databases <source> [--user root]
  localsites import database <source> <name> [--user root]   copy + verify, original untouched
  localsites open <folder> finder|terminal|vscode|cursor|browser
  localsites settings [key=value ...]    show or change settings (php.memory_limit=256M, mysql_port=3308, ...)
  localsites folders [add <path> [label] [auto|flat|groups] | remove <label>]   projects folders
  localsites install <project>           install dependencies (composer / npm / pnpm / yarn / bun)
  localsites connect-db <folder> <db>    use a LocalSites database in a project (backs up .env first)
  localsites restore-env <folder>        undo connect-db
  localsites version                     version and what's new
  localsites doctor                      health check of everything, with fixes
  localsites check <folder>              test one project: DNS, PHP, database, HTTPS, certificate
  localsites debug-bundle                zip of logs + configs (secrets removed) for bug reports
  localsites mcp [--allow-secrets]       MCP server for AI agents (stdio)
  localsites mcp config                  how to connect Claude Code, Claude Desktop, Cursor
  localsites apply | restart | stop
  localsites quit                        stop everything, including the background service
  localsites call <method> '<json>'      any API method (see: localsites methods)

Add --json to any command for machine-readable output.
`)
}
