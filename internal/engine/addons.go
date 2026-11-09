package engine

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/addons"
	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/frontdoor"
	"github.com/localsites/localsites/internal/proc"
)

// AddonView is an add-on with its install and run state, for the app, CLI and agents.
type AddonView struct {
	addons.Def
	Installed  bool             `json:"installed"`
	Binary     string           `json:"binary,omitempty"`
	Installing bool             `json:"installing"`
	Enabled    bool             `json:"enabled"`
	Running    bool             `json:"running"`
	Port       int              `json:"port"`
	UIPort     int              `json:"ui_port,omitempty"`
	UIURL      string           `json:"ui_url,omitempty"`
	KeepMail   bool             `json:"keep_mail,omitempty"`
	MemoryMB   int              `json:"memory_mb,omitempty"`
	PersistDB  bool             `json:"persist,omitempty"`
	Snippets   []addons.Snippet `json:"snippets"`
	Install    string           `json:"install_command"`
	Problem    string           `json:"problem,omitempty"`
}

func addonProc(id string) string        { return "addon-" + id }
func addonInstallProc(id string) string { return "addon-install-" + id }

func (e *Engine) addonOptions(d addons.Def) (config.Addon, addons.Options) {
	var a config.Addon
	if c := e.Store.Get().Settings.Addons[d.ID]; c != nil {
		a = *c
	}
	o := addons.Options{Port: a.Port, UIPort: a.UIPort, KeepMail: a.KeepMail, MemoryMB: a.MemoryMB, PersistDB: a.PersistDB}
	if o.Port == 0 {
		o.Port = d.Port
	}
	if o.UIPort == 0 {
		o.UIPort = d.UIPort
	}
	if d.ID == "memcached" && o.MemoryMB == 0 {
		o.MemoryMB = 64
	}
	return a, o
}

// uiDomain is where an add-on's web page opens: <name>.<tld> (mail.test, minio.test …). If a project
// already uses that address, the add-on's own name is used instead (mailpit.test).
func (e *Engine) uiDomain(d addons.Def) string {
	tld := e.Store.Get().Settings.TLD
	name := d.UIName
	if name == "" {
		name = d.ID
	}
	for _, s := range e.Store.Get().Sites {
		if s.Domain == name+"."+tld {
			return d.ID + "." + tld
		}
	}
	return name + "." + tld
}

// Addons lists the catalog with each add-on's state.
func (e *Engine) Addons() []AddonView {
	brew, _ := addons.Brew()
	out := make([]AddonView, 0, len(addons.Catalog))
	for _, d := range addons.Catalog {
		a, o := e.addonOptions(d)
		v := AddonView{Def: d, Enabled: a.Enabled, Port: o.Port, UIPort: o.UIPort, KeepMail: o.KeepMail, MemoryMB: o.MemoryMB, PersistDB: o.PersistDB,
			Snippets: d.Snippets(o), Install: "brew install " + d.Formula}
		if brew == "" {
			v.Install = ""
		}
		v.Binary, v.Installed = d.Binary()
		v.Installing = e.Sup.Status(addonInstallProc(d.ID)).Running
		st := e.Sup.Status(addonProc(d.ID))
		v.Running = st.Running
		if v.Enabled && !v.Running && st.LastErr != "" {
			v.Problem = st.LastErr
		}
		if d.UIPort != 0 {
			v.UIURL = "https://" + e.uiDomain(d)
		}
		out = append(out, v)
	}
	return out
}

func (e *Engine) addon(id string) (AddonView, error) {
	for _, v := range e.Addons() {
		if v.ID == id {
			return v, nil
		}
	}
	return AddonView{}, fmt.Errorf("no add-on named %q", id)
}

// AddonPatch changes an add-on. Only the given fields change.
type AddonPatch struct {
	Enabled   *bool `json:"enabled,omitempty"`
	Port      *int  `json:"port,omitempty"`
	UIPort    *int  `json:"ui_port,omitempty"`
	KeepMail  *bool `json:"keep_mail,omitempty"`
	MemoryMB  *int  `json:"memory_mb,omitempty"`
	PersistDB *bool `json:"persist,omitempty"`
}

// SetAddon validates and saves an add-on's settings (call Apply afterwards to start or stop it).
func (e *Engine) SetAddon(id string, p AddonPatch) error {
	v, err := e.addon(id)
	if err != nil {
		return err
	}
	cfg := e.Store.Get()
	a := config.Addon{}
	if c := cfg.Settings.Addons[id]; c != nil {
		a = *c
	}
	var errs []string
	if p.Enabled != nil {
		a.Enabled = *p.Enabled
	}
	checkPort := func(n int, what string) {
		if n != 0 && (n < 1024 || n > 65535) {
			errs = append(errs, what+" must be between 1024 and 65535")
		}
	}
	if p.Port != nil {
		a.Port = *p.Port
		checkPort(a.Port, "Port")
	}
	if p.UIPort != nil {
		a.UIPort = *p.UIPort
		checkPort(a.UIPort, "Inbox port")
	}
	if p.KeepMail != nil {
		a.KeepMail = *p.KeepMail
	}
	if p.PersistDB != nil {
		a.PersistDB = *p.PersistDB
	}
	if p.MemoryMB != nil {
		if *p.MemoryMB < 16 || *p.MemoryMB > 4096 {
			errs = append(errs, "Memory must be between 16 and 4096 MB")
		}
		a.MemoryMB = *p.MemoryMB
	}
	if a.Enabled {
		if !v.Installed {
			errs = append(errs, v.Name+" is not installed yet. Install it first")
		}
		port := a.Port
		if port == 0 {
			port = v.Def.Port
		}
		for _, other := range e.Addons() {
			if other.ID != id && other.Enabled && other.Port == port {
				errs = append(errs, fmt.Sprintf("%s already uses port %d. Pick another port for %s", other.Name, port, v.Name))
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return e.Store.Update(func(c *config.Config) error {
		if c.Settings.Addons == nil {
			c.Settings.Addons = map[string]*config.Addon{}
		}
		cp := a
		c.Settings.Addons[id] = &cp
		return nil
	})
}

// InstallAddon runs "brew install <formula>" in the background; progress goes to the add-on's install log.
// Only formulas from the catalog can be installed.
func (e *Engine) InstallAddon(id string) (string, error) {
	d, ok := addons.Find(id)
	if !ok {
		return "", fmt.Errorf("no add-on named %q", id)
	}
	if _, ok := d.Binary(); ok {
		return "", errors.New(d.Name + " is already installed")
	}
	return e.brewInstall(addonInstallProc(id), d.Formula, filepath.Join(e.P.Logs, "addon-"+id+"-install.log"))
}

// applyAddons starts, restarts (settings changed) and stops add-ons. Returns the front door routes they need.
func (e *Engine) applyAddons(add func(string, ...any)) []frontdoor.Route {
	var routes []frontdoor.Route
	for _, v := range e.Addons() {
		name := addonProc(v.ID)
		if !v.Enabled || !v.Installed {
			if e.Sup.Status(name).Running {
				e.Sup.Stop(name, 5*time.Second)
				add("Stopped %s", v.Name)
			}
			continue
		}
		_, o := e.addonOptions(v.Def)
		dir := filepath.Join(e.P.Data, "addons", v.ID)
		_ = os.MkdirAll(dir, 0o700)
		env := []string{"PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin", "HOME=" + e.P.Home}
		if v.Def.ProcEnv != nil {
			env = append(env, v.Def.ProcEnv(o)...)
		}
		spec := proc.Spec{Name: name, Cmd: v.Binary, Args: v.Def.Args(o, dir), Dir: dir, Env: env, Log: filepath.Join(e.P.Logs, "addon-"+v.ID+".log")}
		if cur, ok := e.Sup.Spec(name); ok && e.Sup.Status(name).Running {
			if sameSpec(cur, spec) {
				if v.UIPort != 0 {
					routes = append(routes, frontdoor.Route{Domain: e.uiDomain(v.Def), Backend: fmt.Sprintf("http://127.0.0.1:%d", o.UIPort)})
				}
				continue
			}
			e.Sup.Stop(name, 5*time.Second)
		}
		if v.Def.Init != nil {
			if err := v.Def.Init(v.Binary, dir); err != nil {
				add("%s: %v", v.Name, err)
				continue
			}
		}
		if !portFree(o.Port) {
			add("%s: port %d is used by another program. Pick another port in Services → Add-ons", v.Name, o.Port)
			continue
		}
		if err := e.Sup.Start(spec); err != nil {
			add("%s failed to start: %v", v.Name, err)
			continue
		}
		if slow := waitPorts([]int{o.Port}, 8*time.Second); len(slow) > 0 {
			add("%s started but isn't answering on port %d yet; see its log", v.Name, o.Port)
		} else {
			add("Started %s on port %d", v.Name, o.Port)
		}
		if v.UIPort != 0 {
			routes = append(routes, frontdoor.Route{Domain: e.uiDomain(v.Def), Backend: fmt.Sprintf("http://127.0.0.1:%d", o.UIPort)})
		}
	}
	return routes
}

// mailAdmin is the php_admin_value set that sends PHP's mail() to Mailpit while it is on.
func (e *Engine) mailAdmin() map[string]string {
	for _, v := range e.Addons() {
		if v.ID == "mailpit" && v.Enabled && v.Installed {
			return map[string]string{"sendmail_path": addons.SendmailPath(v.Binary, v.Port)}
		}
	}
	return nil
}

// AddonTestMail sends a short test email through Mailpit.
func (e *Engine) AddonTestMail() (string, error) {
	v, err := e.addon("mailpit")
	if err != nil {
		return "", err
	}
	if !v.Running {
		return "", errors.New("switch Mailpit on first")
	}
	msg := "From: LocalSites <localsites@localhost>\r\nTo: you@example.test\r\nSubject: Test email from LocalSites\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Mailpit is catching your mail. Emails your projects send show up in this inbox and never reach real people.\r\n"
	if err := smtp.SendMail(fmt.Sprintf("127.0.0.1:%d", v.Port), nil, "localsites@localhost", []string{"you@example.test"}, []byte(msg)); err != nil {
		return "", fmt.Errorf("could not send: %v", err)
	}
	return "Sent a test email. Open " + v.UIURL + " to see it", nil
}

// AddonFlush empties a cache add-on (Valkey, Redis, Memcached).
func (e *Engine) AddonFlush(id string) (string, error) {
	v, err := e.addon(id)
	if err != nil {
		return "", err
	}
	if v.Kind != "cache" {
		return "", errors.New(v.Name + " is not a cache")
	}
	if !v.Running {
		return "", errors.New(v.Name + " is not running")
	}
	cmd := "FLUSHALL\r\n"
	if id == "memcached" {
		cmd = "flush_all\r\n"
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", v.Port), 3*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", err
	}
	line, _ := bufio.NewReader(conn).ReadString('\n')
	if line = strings.TrimSpace(line); line != "+OK" && line != "OK" {
		return "", fmt.Errorf("%s answered %q", v.Name, line)
	}
	return "Emptied " + v.Name, nil
}

// addonChecks are Doctor checks for switched-on add-ons.
func (e *Engine) addonChecks() []Check {
	var out []Check
	for _, v := range e.Addons() {
		if !v.Enabled {
			continue
		}
		if !v.Installed {
			out = append(out, fail("Services", v.Name, "switched on but not installed", v.Install))
			continue
		}
		out = append(out, procCheck(v.Name, e.Sup.Status(addonProc(v.ID)), true))
		out = append(out, portCheck("Services", v.Name+" port", fmt.Sprintf("127.0.0.1:%d", v.Port)))
	}
	return out
}
