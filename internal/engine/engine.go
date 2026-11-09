// Package engine is the heart of LocalSites: it turns the saved config into running services.
package engine

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/localsites/localsites/internal/addons"
	"github.com/localsites/localsites/internal/ca"
	"github.com/localsites/localsites/internal/changes"
	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/dbclient"
	"github.com/localsites/localsites/internal/frontdoor"
	"github.com/localsites/localsites/internal/importers"
	"github.com/localsites/localsites/internal/paths"
	"github.com/localsites/localsites/internal/problems"
	"github.com/localsites/localsites/internal/proc"
	"github.com/localsites/localsites/internal/runtime"
	"github.com/localsites/localsites/internal/secrets"
	"github.com/localsites/localsites/internal/services"
	"github.com/localsites/localsites/internal/share"
	"github.com/localsites/localsites/internal/update"
)

const (
	procApache = "apache"
	procMySQL  = "mysql"
	secretRoot = "mysql-root"
)

type Engine struct {
	P            paths.Paths
	Store        *config.Store
	Secrets      secrets.Store
	CA           *ca.CA
	Sup          *proc.Supervisor
	Front        frontdoor.Driver
	Data         *dbclient.Manager // database browser
	Shares       *share.Manager    // sites shared on the internet
	upd          *update.Updater   // app updates (see update.go)
	Changes      changes.Bus       // clients wait on this instead of polling (events.wait)
	problems     *problems.Store   // things that went wrong (see problems.go)
	problemsOnce sync.Once
	pgMu         sync.Mutex
	pgList       []string // PostgreSQL add-on databases (see pgtools.go)
	updOnce      sync.Once

	applyMu     sync.Mutex
	lastRoutes  []frontdoor.Route // front door routes from the last apply
	mysqlMu     sync.Mutex        // MySQL setup must never run twice at once (it generates the root password)
	mysqlUsed   atomic.Int64      // last time something used LocalSites MySQL (unix nanoseconds)
	doctorFails atomic.Int64      // failed checks in the last Doctor run, for the Dock badge
}

// New loads config, CA and picks the front door.
func New() (*Engine, error) {
	p := paths.Default()
	if err := p.Ensure(); err != nil {
		return nil, err
	}
	store, err := config.Open(p.Config, p.Home)
	if err != nil {
		return nil, err
	}
	cfg := store.Get()
	authority, err := ca.Load(p.CA, cfg.Settings.TLD)
	if err != nil {
		return nil, err
	}
	e := &Engine{P: p, Store: store, Secrets: secrets.New(p.Config), CA: authority, Sup: proc.New()}
	e.Front = e.pickFrontDoor(cfg.Settings)
	e.Data = dbclient.New(p.Config, e.Secrets, e.builtinConnections)
	e.Data.Prepare = func(c dbclient.Conn) error { // the data browser wakes LocalSites MySQL when it is asleep
		if c.ID == "localsites" {
			return e.touchMySQL()
		}
		return nil
	}
	e.Shares = share.NewManager(p.Logs)
	e.Shares.OnChange = e.Changes.Bump
	e.Sup.OnChange = e.Changes.Bump
	return e, nil
}

func (e *Engine) pickFrontDoor(s config.Settings) frontdoor.Driver {
	if bin, ok := runtime.Valet(); ok && s.FrontDoor != "builtin" {
		return &frontdoor.Valet{Bin: bin, Home: e.P.Home, StateFile: filepath.Join(e.P.Config, "valet-proxies.json"), TLD: s.TLD}
	}
	return &frontdoor.Builtin{HTTPAddr: fmt.Sprintf("127.0.0.1:%d", s.HTTPPort), HTTPSAddr: fmt.Sprintf("127.0.0.1:%d", s.HTTPSPort),
		DNSAddr: fmt.Sprintf("127.0.0.1:%d", s.DNSPort), TLD: s.TLD, GetCert: e.CA.GetCertificate, Backend: e.CA.Pool()}
}

/* ------------------------------------------------------------------ sites */

// SiteView is a site as shown to the UI, CLI and agents.
type SiteView struct {
	config.Site
	Folder     string            `json:"folder"` // unique project ID ("aecor", or "websites/aecor" for other folders)
	Name       string            `json:"name"`   // folder name
	Path       string            `json:"path"`
	Root       string            `json:"root"`            // label of the projects folder it lives in
	RootIdx    int               `json:"root_index"`      // 0 = main projects folder
	Group      string            `json:"group,omitempty"` // group folder (e.g. a git account)
	Type       string            `json:"type"`            // effective kind (detected or overridden)
	Runtime    string            `json:"runtime"`         // php | app (dev server) | none
	Framework  Framework         `json:"framework"`
	Git        *Git              `json:"git,omitempty"`
	ServedBy   string            `json:"served_by,omitempty"` // "valet" when Laravel Valet serves this folder
	Installing bool              `json:"installing,omitempty"`
	ManagedBy  string            `json:"managed_by,omitempty"`
	URL        string            `json:"url,omitempty"`
	Imported   *importers.Host   `json:"imported_from,omitempty"`
	Env        *EnvInfo          `json:"env,omitempty"`
	Saved      bool              `json:"saved"`
	PHPMissing bool              `json:"php_missing,omitempty"`
	Problems   []string          `json:"problems,omitempty"`
	Extra      map[string]string `json:"-"`
}

// Sites lists every project in every projects folder, merged with saved settings, localsites.yaml,
// detection (framework, git) and import hints.
func (e *Engine) Sites() []SiteView {
	cfg := e.Store.Get()
	hosts := e.importHints()
	proto := prototypeSites(e.P.Home)
	valet := map[string]valetInfo{}
	if e.Front.Name() == "valet" {
		valet = e.valetSites()
	}
	var out []SiteView
	for _, c := range scanRoots(cfg.Settings.Roots) {
		fw := detectCached(c.Path)
		v := SiteView{Folder: c.ID, Name: c.Name, Path: c.Path, Root: c.RootLabel, RootIdx: c.RootIdx, Group: c.Group, Framework: fw, Git: gitInfo(c.Path)}
		if s, ok := cfg.Sites[c.ID]; ok {
			v.Site, v.Saved = *s, true
		} else {
			v.Site = e.defaultsFor(c, fw, hosts, proto)
		}
		if c.RootIdx == 0 {
			for docroot, h := range hosts {
				if docroot == c.Path || strings.HasPrefix(docroot, c.Path+"/") {
					hc := h
					v.Imported = &hc
					break
				}
			}
		}
		if ps, err := config.LoadProject(c.Path); err != nil {
			v.Problems = append(v.Problems, err.Error())
		} else if ps != nil {
			v.ManagedBy = config.ProjectFile
			if ps.Domain != "" {
				v.Domain = ps.Domain
			}
			if ps.PHP != "" {
				v.PHP = ps.PHP
			}
			if ps.Docroot != "" || v.Docroot == "" {
				v.Docroot = ps.Docroot
			}
			if ps.Database != "" {
				v.Database = ps.Database
			}
		}
		v.Type = fw.Kind
		if v.Kind != "" {
			v.Type = v.Kind
		}
		v.Runtime = runtimeOf(v.Type)
		v.Env = projectEnv(c.Path)
		v.Installing = e.Sup.Status("install-" + config.Slug(c.ID)).Running
		if real, err := filepath.EvalSymlinks(c.Path); err == nil {
			if vi, ok := valet[real]; ok && !v.Enabled {
				v.ServedBy, v.Domain = "valet", vi.Domain
				v.URL = map[bool]string{true: "https://", false: "http://"}[vi.Secure] + vi.Domain
				if vi.PHP != "" {
					v.PHP = vi.PHP
				}
			}
		}
		switch v.Runtime {
		case "php":
			if _, ok := runtime.FindPHP(v.PHP); !ok && v.ServedBy == "" {
				v.PHPMissing = true
				v.Problems = append(v.Problems, "PHP "+v.PHP+" is not installed")
			}
			if (v.Type == KindLaravel || v.Type == KindPHP) && !fw.Deps && exists(filepath.Join(c.Path, "composer.json")) && v.ServedBy == "" {
				v.Problems = append(v.Problems, "Composer packages are not installed")
			}
		case "app":
			if v.Type == "command" && strings.TrimSpace(v.Command) == "" {
				v.Problems = append(v.Problems, "Set the command that starts this project")
			} else if (v.Type == KindNext || v.Type == KindNode) && !fw.Deps {
				v.Problems = append(v.Problems, "Dependencies are not installed (node_modules is missing)")
			}
		}
		if v.Enabled {
			v.URL = "https://" + v.Domain
		}
		out = append(out, v)
	}
	// Default addresses must be unique across folders: "admin" in two folders becomes admin.test and admin-<group>.test
	used := map[string]bool{}
	for _, v := range out {
		if v.Saved || v.ServedBy != "" {
			used[v.Domain] = true
		}
	}
	tld := "." + cfg.Settings.TLD
	for i := range out {
		v := &out[i]
		if v.Saved || v.ServedBy != "" || v.ManagedBy != "" {
			continue
		}
		if used[v.Domain] {
			base := strings.TrimSuffix(v.Domain, tld)
			suffix := config.Slug(v.Group)
			if v.Group == "" {
				suffix = config.Slug(v.Root)
			}
			v.Domain = base + "-" + suffix + tld
			for n := 2; used[v.Domain]; n++ {
				v.Domain = fmt.Sprintf("%s-%s-%d%s", base, suffix, n, tld)
			}
		}
		used[v.Domain] = true
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.RootIdx != b.RootIdx {
			return a.RootIdx < b.RootIdx
		}
		if a.Group != b.Group {
			return strings.ToLower(a.Group) < strings.ToLower(b.Group)
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out
}

// runtimeOf says how a kind is served: by Apache+PHP, by its own dev server, or not at all.
func runtimeOf(kind string) string {
	switch kind {
	case KindPHP, KindLaravel, KindWordPress, KindStatic:
		return "php"
	case KindNext, KindNode, "command", KindDocker:
		return "app"
	}
	return "none"
}

var validKinds = map[string]bool{"": true, KindDocker: true, KindPHP: true, KindLaravel: true, KindWordPress: true, KindStatic: true, KindNext: true, KindNode: true, "command": true, KindOther: true}

func (e *Engine) importHints() map[string]importers.Host {
	m := map[string]importers.Host{}
	for _, s := range importers.Sources() {
		if !s.Found {
			continue
		}
		for _, h := range s.Hosts() {
			if _, seen := m[h.Docroot]; !seen {
				m[h.Docroot] = h
			}
		}
	}
	return m
}

func (e *Engine) defaultsFor(c candidate, fw Framework, hosts map[string]importers.Host, proto map[string]config.Site) config.Site {
	cfg := e.Store.Get()
	versions := runtime.PHPVersions()
	s := config.Site{Domain: config.Slug(c.Name) + "." + cfg.Settings.TLD, DBServer: config.DBLocalSites}
	if len(versions) > 0 {
		s.PHP = versions[len(versions)-1].Version
	}
	if d := cfg.Settings.DefaultPHP; d != "" {
		if _, ok := runtime.FindPHP(d); ok {
			s.PHP = d
		}
	}
	if v := phpFor(fw.PHPReq); v != "" { // e.g. composer.json "php": "^7.3|^8.0"
		s.PHP = v
	}
	if fw.Kind == KindLaravel {
		s.Docroot = "public"
	}
	if c.RootIdx == 0 {
		for docroot, h := range hosts {
			if docroot == c.Path || strings.HasPrefix(docroot, c.Path+"/") {
				s.Docroot = strings.TrimPrefix(strings.TrimPrefix(docroot, c.Path), "/")
				if v := runtime.ClosestPHP(h.PHP); v != "" {
					s.PHP = v
				}
				if h.Source == "mamp-pro" || h.Source == "mamp" {
					s.DBServer = config.DBMAMP // keep using MAMP's MySQL
				}
				break
			}
		}
		if p, ok := proto[c.ID]; ok { // settings chosen in the earlier prototype dashboard
			s.Enabled, s.Domain, s.Docroot = p.Enabled, p.Domain, p.Docroot
			if _, ok := runtime.FindPHP(p.PHP); ok {
				s.PHP = p.PHP
			}
		}
	}
	if s.Docroot == "" && !exists(filepath.Join(c.Path, "index.php")) && !exists(filepath.Join(c.Path, "index.html")) && exists(filepath.Join(c.Path, "public", "index.php")) {
		s.Docroot = "public"
	}
	return s
}

// SitePatch is a partial update from the UI/CLI/agents. nil fields are unchanged.
type SitePatch struct {
	Enabled  *bool              `json:"enabled,omitempty"`
	Domain   *string            `json:"domain,omitempty"`
	PHP      *string            `json:"php,omitempty"`
	Docroot  *string            `json:"docroot,omitempty"`
	DBServer *string            `json:"db_server,omitempty"`
	Database *string            `json:"database,omitempty"`
	Kind     *string            `json:"kind,omitempty"`    // "" = detect automatically
	Command  *string            `json:"command,omitempty"` // custom start command, {port} is replaced
	Port     *int               `json:"port,omitempty"`    // docker projects: the host port the container publishes
	EnvVars  *map[string]string `json:"env_vars,omitempty"`
	PHPIni   *map[string]string `json:"php_ini,omitempty"`
	Xdebug   *bool              `json:"xdebug,omitempty"`
	Server   *string            `json:"web_server,omitempty"` // apache | nginx
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// reservedEnv are set by LocalSites for every dev server.
var reservedEnv = map[string]bool{"PATH": true, "PORT": true, "HOST": true, "HOSTNAME": true}

// UpdateSite validates and saves settings for one folder.
func (e *Engine) UpdateSite(folder string, p SitePatch) error {
	var cur *SiteView
	all := e.Sites()
	for i := range all {
		if all[i].Folder == folder {
			cur = &all[i]
		}
	}
	if cur == nil {
		return fmt.Errorf("no project folder named %q", folder)
	}
	s := cur.Site
	if p.Enabled != nil {
		s.Enabled = *p.Enabled
	}
	if p.Domain != nil {
		s.Domain = strings.ToLower(strings.TrimSpace(*p.Domain))
	}
	if p.PHP != nil {
		s.PHP = *p.PHP
	}
	if p.Docroot != nil {
		s.Docroot = strings.Trim(strings.TrimSpace(*p.Docroot), "/")
	}
	if p.DBServer != nil {
		s.DBServer = *p.DBServer
	}
	if p.Database != nil {
		s.Database = *p.Database
	}
	if p.Kind != nil {
		s.Kind = *p.Kind
	}
	if p.Command != nil {
		s.Command = strings.TrimSpace(*p.Command)
	}
	var errs []string
	if p.Port != nil {
		s.Port = *p.Port
		if s.Port != 0 && (s.Port < 1024 || s.Port > 65535) {
			errs = append(errs, "Port must be between 1024 and 65535")
		}
	}
	if p.EnvVars != nil {
		s.EnvVars = map[string]string{}
		for k, v := range *p.EnvVars {
			switch {
			case !envNameRe.MatchString(k):
				errs = append(errs, "Variable names use letters, digits and _: "+k)
			case reservedEnv[strings.ToUpper(k)]:
				errs = append(errs, k+" is set by LocalSites")
			case strings.ContainsAny(v, "\n\r\x00") || len(v) > 4000:
				errs = append(errs, k+": the value must be a single line")
			default:
				s.EnvVars[k] = v
			}
		}
		if len(s.EnvVars) > 100 {
			errs = append(errs, "At most 100 variables")
		}
	}
	if p.Xdebug != nil {
		s.Xdebug = *p.Xdebug
	}
	if p.Server != nil {
		switch *p.Server {
		case "", "apache":
			s.Server = ""
		case "nginx":
			if _, _, _, ok := runtime.Nginx(); !ok {
				errs = append(errs, "Nginx is not installed (brew install nginx)")
			}
			s.Server = "nginx"
		default:
			errs = append(errs, "Web server must be apache or nginx")
		}
	}
	if p.PHPIni != nil {
		s.PHPIni = map[string]string{}
		for k, v := range *p.PHPIni {
			re, ok := PHPIniKeys[k]
			switch {
			case !ok:
				errs = append(errs, k+" can't be changed per project (allowed: "+strings.Join(sortedIniKeys(), ", ")+")")
			case !re.MatchString(v):
				errs = append(errs, k+": "+v+" is not a valid value")
			default:
				s.PHPIni[k] = v
			}
		}
	}
	tld := e.Store.Get().Settings.TLD
	kind := cur.Framework.Kind
	if s.Kind != "" {
		kind = s.Kind
	}
	rt := runtimeOf(kind)
	if !validKinds[s.Kind] {
		errs = append(errs, "Type must be one of php, laravel, wordpress, static, nextjs, node, command, docker, other")
	}
	if strings.ContainsAny(s.Command, "\n\r") || len(s.Command) > 500 {
		errs = append(errs, "The command must be a single line")
	}
	if s.Enabled && rt == "none" {
		errs = append(errs, "This folder isn't a website. Set its type (for example “custom command”) to serve it")
	}
	if s.Enabled && (kind == "command" || kind == KindDocker) && s.Command == "" {
		errs = append(errs, "Set the command that starts this project")
	}
	if s.Enabled && kind == KindDocker && s.Port == 0 {
		errs = append(errs, "Set the port your container publishes on 127.0.0.1")
	}
	if s.Enabled && cur.ServedBy == "valet" && !s.TakeOver && (p.Enabled != nil && *p.Enabled) {
		errs = append(errs, cur.Domain+" is served by Laravel Valet. Run “valet unlink” or remove its folder from “valet park” to let LocalSites serve it")
	}
	if !config.ValidDomain(s.Domain, tld) {
		errs = append(errs, fmt.Sprintf("Domain must look like name.%s (letters, numbers, dashes)", tld))
	}
	if s.DBServer != config.DBLocalSites && s.DBServer != config.DBMAMP {
		errs = append(errs, "Database server must be localsites or mamp")
	}
	if s.Database != "" && !config.ValidDatabaseName(s.Database) {
		errs = append(errs, "Database name may use letters, digits and _ only")
	}
	if rt == "php" { // PHP version and web root only matter for projects Apache + PHP serve
		if _, ok := runtime.FindPHP(s.PHP); !ok {
			errs = append(errs, "PHP "+s.PHP+" is not installed")
		}
		real, err := filepath.EvalSymlinks(filepath.Join(cur.Path, s.Docroot))
		base, _ := filepath.EvalSymlinks(cur.Path)
		if err != nil || !(real == base || strings.HasPrefix(real, base+string(os.PathSeparator))) {
			errs = append(errs, "Web root must be an existing folder inside the project")
		}
	}
	if s.Enabled && len(errs) == 0 {
		for _, o := range all {
			if o.Folder != folder && o.Enabled && o.Domain == s.Domain {
				errs = append(errs, s.Domain+" is already used by "+o.Folder)
			}
		}
		if c := e.Front.Conflict(s.Domain); c != "" && !s.TakeOver {
			errs = append(errs, c)
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return e.Store.Update(func(c *config.Config) error {
		cp := s
		c.Sites[folder] = &cp
		return nil
	})
}

// CreateSite makes a new project folder with a starter page.
func (e *Engine) CreateSite(folder string) (SiteView, error) {
	if !folderRe.MatchString(folder) {
		return SiteView{}, errors.New("use letters, numbers, dashes and underscores for the folder name")
	}
	path := filepath.Join(e.Store.Get().Settings.SitesRoot, folder)
	if _, err := os.Stat(path); err == nil {
		return SiteView{}, fmt.Errorf("%s already exists", folder)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return SiteView{}, err
	}
	page := "<?php\n// Created by LocalSites\n?><!doctype html><meta charset=\"utf-8\"><title>" + folder + "</title>\n" +
		"<body style=\"font:16px system-ui;padding:48px\"><h1>" + folder + "</h1><p>PHP <?= PHP_VERSION ?> is ready.</p>\n"
	if err := os.WriteFile(filepath.Join(path, "index.php"), []byte(page), 0o644); err != nil {
		return SiteView{}, err
	}
	for _, s := range e.Sites() {
		if s.Folder == folder {
			return s, nil
		}
	}
	return SiteView{}, errors.New("created, but the folder is not in the projects folder")
}

var folderRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)

/* ------------------------------------------------------------------ apply */

func (e *Engine) mysqlSvc() (services.MySQL, bool) {
	base, ok := runtime.MySQL()
	cfg := e.Store.Get()
	return services.MySQL{
		Base: base, DataDir: filepath.Join(e.P.MySQLData, "8.4"), Socket: e.P.Socket("mysql"), Port: cfg.Settings.MySQLPort,
		PIDFile: filepath.Join(e.P.Run, "mysql.pid"), LogFile: filepath.Join(e.P.Logs, "mysql-error.log"),
		CnfFile: filepath.Join(e.P.Generated, "my.cnf"),
	}, ok
}

func (e *Engine) mysqlSocketFor(s SiteView) string {
	if s.DBServer == config.DBMAMP {
		if src, ok := importers.FindSource("mamp-pro"); ok {
			return src.MySQLSock
		}
	}
	return e.P.Socket("mysql")
}

func (e *Engine) fpmSock(folder string) string { return e.P.Socket("fpm-" + config.Slug(folder)) }

// Apply regenerates every config and brings services in line. It returns a human-readable log.
func (e *Engine) Apply() []string {
	e.applyMu.Lock()
	defer e.applyMu.Unlock()
	var log []string
	add := func(f string, a ...any) { log = append(log, fmt.Sprintf(f, a...)) }
	cfg := e.Store.Get()
	sites := e.Sites()

	// Keep switched-on sites that only exist as defaults (e.g. taken over from the prototype)
	_ = e.Store.Update(func(c *config.Config) error {
		for _, s := range sites {
			if s.Enabled && !s.Saved {
				cp := s.Site
				c.Sites[s.Folder] = &cp
			}
		}
		return nil
	})

	httpd, modules, mime, ok := runtime.Apache()
	if !ok {
		return []string{"Apache is not installed (brew install httpd)"}
	}

	// PHP-FPM: one master per PHP version, one pool per site
	byVer := map[string][]services.FPMPool{}
	mailAdmin := e.mailAdmin()     // Mailpit on: PHP's mail() is delivered to it
	xdebugVer := map[string]bool{} // PHP versions where a project wants Xdebug
	var enabled []SiteView
	var apps []SiteView
	for _, s := range sites {
		if !s.Enabled || s.ServedBy != "" {
			continue
		}
		if s.Runtime == "app" {
			apps = append(apps, s)
			continue
		}
		if s.Runtime != "php" || s.PHPMissing {
			continue
		}
		enabled = append(enabled, s)
		tmp := filepath.Join(e.P.Run, "tmp", config.Slug(s.Folder))
		_ = os.MkdirAll(tmp, 0o700)
		byVer[s.PHP] = append(byVer[s.PHP], services.FPMPool{
			Name: config.Slug(s.Folder), Socket: e.fpmSock(s.Folder), MySQLSocket: e.mysqlSocketFor(s),
			ErrorLog: filepath.Join(e.P.SiteLogs, fileKey(s.Folder)+"-php.log"), TmpDir: tmp, Overrides: mergeIni(cfg.Settings.PHPIni, s.PHPIni), Admin: mergeIni(mailAdmin, xdebugAdmin(s)),
		})
		if s.Xdebug {
			xdebugVer[s.PHP] = true
		}
	}
	// Built-in web tools (Adminer)
	if s, ok := e.adminerSite(); ok {
		enabled = append(enabled, s)
		tmp := filepath.Join(e.P.Run, "tmp", config.Slug(s.Folder))
		_ = os.MkdirAll(tmp, 0o700)
		byVer[s.PHP] = append(byVer[s.PHP], services.FPMPool{Name: config.Slug(s.Folder), Socket: e.fpmSock(s.Folder), MySQLSocket: e.mysqlSocketFor(s),
			ErrorLog: filepath.Join(e.P.SiteLogs, fileKey(s.Folder)+"-php.log"), TmpDir: tmp, Overrides: cfg.Settings.PHPIni})
	}
	// Dev servers (Next.js, Node, custom commands)
	if len(apps) > 0 {
		e.assignPorts(sites) // every saved port stays reserved, so each project keeps its own port
		apps = nil
		for _, s := range e.Sites() {
			if s.Enabled && s.Runtime == "app" && s.ServedBy == "" {
				apps = append(apps, s)
			}
		}
	}
	e.applyApps(apps, add)
	if len(apps) > 0 { // ports may have moved (busy port): route to the ones in use now
		apps = nil
		for _, s := range e.Sites() {
			if s.Enabled && s.Runtime == "app" && s.ServedBy == "" {
				apps = append(apps, s)
			}
		}
	}
	for _, v := range runtime.PHPVersions() {
		name := "php-" + v.Version
		pools, need := byVer[v.Version]
		if !need {
			if e.Sup.Status(name).Running {
				e.Sup.Stop(name, 5*time.Second)
				add("Stopped PHP %s", v.Version)
			}
			continue
		}
		conf := filepath.Join(e.P.Generated, "php-fpm-"+v.Version+".conf")
		if err := os.WriteFile(conf, []byte(services.FPMConfig(v.Version, filepath.Join(e.P.Logs, "php-"+v.Version+"-fpm.log"), pools)), 0o600); err != nil {
			add("PHP %s: %v", v.Version, err)
			continue
		}
		if out, err := exec.Command(v.FPM, "-t", "-y", conf).CombinedOutput(); err != nil {
			add("PHP %s config error: %s", v.Version, lastLine(string(out)))
			continue
		}
		fpmArgs := []string{"-F", "-y", conf}
		if xdebugVer[v.Version] { // load Xdebug only in LocalSites' PHP, switched off except for projects that ask
			if info := runtime.Info(v); !info.XdebugLoaded && info.XdebugFile != "" {
				fpmArgs = append(fpmArgs, "-d", "zend_extension="+info.XdebugFile, "-d", "xdebug.mode=off")
			} else if !info.XdebugLoaded {
				add("PHP %s: Xdebug is not installed. Run: %s", v.Version, info.XdebugInstall)
			}
		}
		spec := proc.Spec{Name: name, Cmd: v.FPM, Args: fpmArgs, Log: filepath.Join(e.P.Logs, "php-"+v.Version+"-fpm.log"), PreStart: func() { removeStaleSockets(conf) }}
		if cur, ok := e.Sup.Spec(name); ok && e.Sup.Status(name).Running && !sameSpec(cur, spec) {
			e.Sup.Stop(name, 5*time.Second) // Xdebug loaded or unloaded: restart instead of reload
		}
		if e.Sup.Status(name).Running {
			_ = e.Sup.Signal(name, proc.SigReloadPHP)
			add("Reloaded PHP %s", v.Version)
		} else if err := e.Sup.Start(spec); err != nil {
			add("PHP %s failed to start: %v", v.Version, err)
		} else {
			add("Started PHP %s", v.Version)
		}
	}

	// Apache (TLS on 127.0.0.1 only)
	crt, key := filepath.Join(e.P.CA, "backend.crt"), filepath.Join(e.P.CA, "backend.key")
	if !exists(crt) {
		if err := e.CA.WritePair(crt, key, "localhost", "127.0.0.1"); err != nil {
			add("Certificate: %v", err)
		}
	}
	defRoot := filepath.Join(e.P.Data, "default-site")
	_ = services.DefaultPage(defRoot)
	ac := services.ApacheConfig{Port: cfg.Settings.ApachePort, ModulesDir: modules, MimeTypes: mime, ServerRoot: filepath.Dir(filepath.Dir(httpd)),
		RunDir: e.P.Run, LogDir: e.P.Logs, CertFile: crt, KeyFile: key, DefaultRoot: defRoot}
	var apacheSites, nginxSites []SiteView
	for _, s := range enabled {
		if s.Server == "nginx" {
			nginxSites = append(nginxSites, s)
		} else {
			apacheSites = append(apacheSites, s)
		}
	}
	for _, s := range apacheSites {
		ac.Sites = append(ac.Sites, services.ApacheSite{Folder: s.Folder, Domain: s.Domain, Path: s.Path,
			Docroot: filepath.Join(s.Path, s.Docroot), FPMSock: e.fpmSock(s.Folder), LogBase: filepath.Join(e.P.SiteLogs, fileKey(s.Folder))})
	}
	conf := filepath.Join(e.P.Generated, "httpd.conf")
	if err := os.WriteFile(conf, []byte(ac.Render()), 0o600); err != nil {
		add("Apache: %v", err)
	} else if out, err := exec.Command(httpd, "-t", "-f", conf).CombinedOutput(); err != nil {
		add("Apache config error: %s", lastLine(string(out)))
	} else if e.Sup.Status(procApache).Running {
		_ = e.Sup.Signal(procApache, proc.SigReloadApache) // graceful
		add("Reloaded Apache")
	} else if err := e.Sup.Start(proc.Spec{Name: procApache, Cmd: httpd, Args: []string{"-f", conf, "-DFOREGROUND"}, Log: filepath.Join(e.P.Logs, "apache-stdout.log")}); err != nil {
		add("Apache failed to start: %v", err)
	} else {
		add("Started Apache")
	}

	// Nginx, for projects that chose it
	nginxPort := e.applyNginx(nginxSites, add)

	// Don't report success until the sites actually answer (avoids a 502 on the first visit)
	var socks, domains []string
	for _, s := range enabled {
		socks = append(socks, e.fpmSock(s.Folder))
	}
	for _, s := range apacheSites {
		domains = append(domains, s.Domain)
	}
	if err := waitReady(cfg.Settings.ApachePort, socks, domains, 10*time.Second); err != nil {
		add("Not ready yet: %v", err)
	}
	var ports []int
	for _, s := range apps {
		ports = append(ports, s.Port)
	}
	if len(nginxSites) > 0 && nginxPort > 0 {
		ports = append(ports, nginxPort)
	}
	for _, p := range waitPorts(ports, 20*time.Second) {
		add("A dev server on port %d is still starting; see its log", p)
	}

	// Add-on services (mail catcher, caches)
	routes := e.applyAddons(add)

	// Front door
	backend := fmt.Sprintf("https://127.0.0.1:%d", cfg.Settings.ApachePort)
	for _, s := range apacheSites {
		routes = append(routes, frontdoor.Route{Domain: s.Domain, Backend: backend})
	}
	for _, s := range nginxSites {
		if nginxPort > 0 {
			routes = append(routes, frontdoor.Route{Domain: s.Domain, Backend: fmt.Sprintf("http://127.0.0.1:%d", nginxPort)})
		}
	}
	for _, s := range apps {
		routes = append(routes, frontdoor.Route{Domain: s.Domain, Backend: fmt.Sprintf("http://127.0.0.1:%d", s.Port)})
	}
	if b, ok := e.Front.(*frontdoor.Builtin); ok {
		if err := b.Start(); err != nil {
			add("Front door: %v", err)
		}
	}
	if len(WaitingForAccess()) > 0 { // keep addresses of projects in folders macOS hasn't let us read yet
		have := map[string]bool{}
		for _, r := range routes {
			have[r.Domain] = true
		}
		for _, r := range e.lastRoutes {
			if !have[r.Domain] {
				routes = append(routes, r)
			}
		}
	}
	e.lastRoutes = routes
	flog, err := e.Front.Sync(routes)
	log = append(log, flog...)
	if err != nil {
		add("Front door: %v", err)
	}

	// MySQL last, so a slow or broken database never keeps sites offline
	if e.mysqlWanted(sites) {
		if err := e.EnsureMySQL(); err != nil {
			add("MySQL: %v", err)
		}
	}
	return log
}

// builtinConnections are the database servers the browser offers without setup. Both start read-only.
func (e *Engine) builtinConnections() []dbclient.Conn {
	var out []dbclient.Conn
	for _, src := range importers.Sources() {
		if src.Found && src.SameAs == "" && src.MySQLUp {
			out = append(out, dbclient.Conn{ID: src.ID, Name: src.Name + " MySQL", Driver: "mysql", Socket: src.MySQLSock, User: "root",
				ReadOnly: true, Note: "Your " + src.Name + " databases. Enter its MySQL password once; it is kept in the keychain."})
		}
	}
	for _, a := range e.Addons() {
		if a.ID == "postgres" && a.Enabled && a.Installed {
			out = append(out, dbclient.Conn{ID: "localsites-pg", Name: "LocalSites PostgreSQL", Driver: "postgres", Host: "127.0.0.1", Port: a.Port, User: "postgres",
				Database: "postgres", SSLMode: "disable", ReadOnly: true, HasPassword: true, Note: "The PostgreSQL add-on (local connections only)."})
			for _, db := range e.pgNames() { // one connection per database, so each can be browsed
				out = append(out, dbclient.Conn{ID: PGConnID(db), Name: "PostgreSQL · " + db, Driver: "postgres", Host: "127.0.0.1", Port: a.Port, User: "postgres",
					Database: db, SSLMode: "disable", ReadOnly: true, HasPassword: true, Note: "A database in the PostgreSQL add-on."})
			}
		}
	}
	if m, ok := e.mysqlSvc(); ok && m.Initialized() {
		out = append(out, dbclient.Conn{ID: "localsites", Name: "LocalSites MySQL 8.4", Driver: "mysql", Socket: m.Socket, User: "root",
			ReadOnly: true, HasPassword: true, PasswordKey: secretRoot, Note: "LocalSites' own MySQL (root login from the keychain)."})
	}
	return out
}

// StopAll stops every service LocalSites runs (front door proxies stay configured).
func (e *Engine) StopAll() []string {
	names := e.Sup.Names()
	if e.Data != nil {
		e.Data.Close()
	}
	if e.Shares != nil {
		e.Shares.StopAll()
	}
	e.Sup.StopAll(15 * time.Second)
	if b, ok := e.Front.(*frontdoor.Builtin); ok {
		b.Stop()
	}
	if len(names) == 0 {
		return []string{"Nothing was running"}
	}
	sort.Strings(names)
	return []string{"Stopped " + strings.Join(names, ", ")}
}

func (e *Engine) mysqlWanted(sites []SiteView) bool {
	m, ok := e.mysqlSvc()
	if !ok {
		return false
	}
	if e.Store.Get().Settings.MySQLAlwaysOn && m.Initialized() {
		return true
	}
	if e.Store.Get().Settings.Adminer {
		return true
	}
	// A running PHP project set to LocalSites MySQL reaches it through "localhost" at any time
	for _, s := range sites {
		if s.Enabled && s.ServedBy == "" && s.Runtime == "php" && s.DBServer == config.DBLocalSites {
			return true
		}
	}
	return false
}

// EnsureMySQL initializes (first time) and starts MySQL 8.4, waiting until it accepts logins.
func (e *Engine) EnsureMySQL() error {
	e.mysqlUsed.Store(time.Now().UnixNano())
	e.mysqlMu.Lock()
	defer e.mysqlMu.Unlock()
	m, ok := e.mysqlSvc()
	if !ok {
		return errors.New("MySQL 8.4 is not installed (brew install mysql@8.4)")
	}
	if err := m.WriteCnf(); err != nil {
		return err
	}
	pw, err := e.Secrets.Get(secretRoot)
	initFile := ""
	if !m.Initialized() {
		if err != nil {
			pw = secrets.Generate(24)
			if err := e.Secrets.Set(secretRoot, pw); err != nil {
				return fmt.Errorf("saving root password: %w", err)
			}
		}
		if initFile, err = m.Initialize(pw); err != nil {
			return err
		}
	} else if err != nil {
		return errors.New("MySQL root password is missing from the keychain")
	}
	if e.Sup.Status(procMySQL).Running && initFile == "" {
		return m.WaitReady(pw, 10*time.Second)
	}
	if !e.Sup.Status(procMySQL).Running {
		if err := e.Sup.Start(proc.Spec{Name: procMySQL, Cmd: m.Bin("mysqld"), Args: m.Args(initFile), Log: filepath.Join(e.P.Logs, "mysql-stdout.log")}); err != nil {
			return err
		}
	}
	err = m.WaitReady(pw, 60*time.Second)
	if initFile != "" {
		os.Remove(initFile) // contained the root password; no longer needed
	}
	return err
}

func (e *Engine) admin() (*sql.DB, services.MySQL, string, error) {
	m, _ := e.mysqlSvc()
	if !e.Sup.Status(procMySQL).Running {
		if err := e.EnsureMySQL(); err != nil {
			return nil, m, "", err
		}
	}
	pw, err := e.Secrets.Get(secretRoot)
	if err != nil {
		return nil, m, "", errors.New("MySQL root password is missing from the keychain")
	}
	db, err := m.Admin(pw)
	if err != nil { // a password change stopped halfway: the new one is kept separately
		if next, nerr := e.Secrets.Get(secretRootNext); nerr == nil {
			if db2, err2 := m.Admin(next); err2 == nil {
				_ = e.Secrets.Set(secretRoot, next)
				_ = e.Secrets.Delete(secretRootNext)
				return db2, m, next, nil
			}
		}
	}
	return db, m, pw, err
}

/* -------------------------------------------------------------- databases */

func (e *Engine) Databases() ([]services.DBInfo, error) {
	db, _, _, err := e.admin()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	list, err := services.ListDatabases(db)
	for i := range list {
		if _, err := e.Secrets.Get("db-" + list[i].Name); err == nil {
			list[i].Owner = services.UserFor(list[i].Name)
		}
	}
	return list, err
}

// Credentials for a project database (password from the keychain).
type Credentials struct {
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	Socket   string `json:"socket"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

// CreateDatabase makes a database with its own user that can only reach that database.
func (e *Engine) CreateDatabase(name string) (Credentials, error) {
	if !config.ValidDatabaseName(name) {
		return Credentials{}, errors.New("database names may use letters, digits and _ only")
	}
	db, _, _, err := e.admin()
	if err != nil {
		return Credentials{}, err
	}
	defer db.Close()
	user := services.UserFor(name)
	pw, err := e.Secrets.Get("db-" + name)
	if err != nil {
		pw = secrets.Generate(24)
		if err := e.Secrets.Set("db-"+name, pw); err != nil {
			return Credentials{}, err
		}
	}
	if err := services.CreateDatabase(db, name, user, pw); err != nil {
		return Credentials{}, err
	}
	return e.creds(name, user, pw), nil
}

func (e *Engine) creds(name, user, pw string) Credentials {
	return Credentials{Database: name, User: user, Password: pw, Socket: e.P.Socket("mysql"), Host: "127.0.0.1", Port: e.Store.Get().Settings.MySQLPort}
}

// DatabaseCredentials returns the saved login for a database LocalSites created.
func (e *Engine) DatabaseCredentials(name string) (Credentials, error) {
	pw, err := e.Secrets.Get("db-" + name)
	if err != nil {
		return Credentials{}, fmt.Errorf("no LocalSites user for %s", name)
	}
	return e.creds(name, services.UserFor(name), pw), nil
}

func (e *Engine) Backup(name string) (string, error) {
	if !config.ValidDatabaseName(name) {
		return "", errors.New("invalid database name")
	}
	_, m, pw, err := e.admin()
	if err != nil {
		return "", err
	}
	return m.Backup(pw, name, e.P.Backups)
}

/* ----------------------------------------------------------------- import */

// ImportDatabase copies one database from another tool's MySQL into LocalSites and verifies it.
// The source is only read. It refuses to overwrite an existing database.
func (e *Engine) ImportDatabase(sourceID, srcUser, srcPassword, name string) (importers.Report, error) {
	src, ok := importers.FindSource(sourceID)
	if !ok || !src.MySQLUp {
		return importers.Report{Database: name}, fmt.Errorf("%s MySQL is not running", sourceID)
	}
	return e.ImportDatabaseFrom(src, srcUser, srcPassword, name)
}

// ImportDatabaseFrom copies one database from src (see ImportDatabase).
func (e *Engine) ImportDatabaseFrom(src importers.Source, srcUser, srcPassword, name string) (importers.Report, error) {
	start := time.Now()
	rep := importers.Report{Database: name}
	if !config.ValidDatabaseName(name) {
		return rep, errors.New("invalid database name")
	}
	sourceID := src.ID
	sdb, err := src.Connect(srcUser, srcPassword)
	if err != nil {
		return rep, err
	}
	defer sdb.Close()
	if found, err := importers.DatabaseExists(sdb, name); err != nil || !found {
		return rep, fmt.Errorf("%s has no database named %s", src.Name, name)
	}
	ddb, m, rootPw, err := e.admin()
	if err != nil {
		return rep, err
	}
	defer ddb.Close()
	if found, _ := importers.DatabaseExists(ddb, name); found {
		return rep, fmt.Errorf("LocalSites already has a database named %s; nothing was changed", name)
	}

	// 1. Dump from the source into a file we keep (also serves as a backup of the original)
	dumpFile := filepath.Join(e.P.Backups, fmt.Sprintf("import-%s-%s-%s.sql", sourceID, name, time.Now().Format("20060102-150405")))
	f, err := os.OpenFile(dumpFile, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return rep, err
	}
	if err := src.Dump(srcUser, srcPassword, name, f); err != nil {
		f.Close()
		os.Remove(dumpFile)
		return rep, err
	}
	f.Close()
	rep.Backup = dumpFile

	// 2. Load into LocalSites MySQL
	in, err := os.Open(dumpFile)
	if err != nil {
		return rep, err
	}
	err = m.Import(rootPw, io.Reader(in), "")
	in.Close()
	if err != nil {
		return rep, err
	}

	// 3. Verify every table, row by row
	checks, ok, err := importers.Verify(sdb, ddb, name)
	if err != nil {
		return rep, err
	}
	rep.Tables, rep.OK = checks, ok

	// 4. Give the database its own user
	if ok {
		if _, err := e.CreateDatabase(name); err != nil {
			return rep, fmt.Errorf("copied and verified, but creating its user failed: %w", err)
		}
	}
	rep.Duration = time.Since(start).Round(time.Millisecond).String()
	return rep, nil
}

/* ----------------------------------------------------------------- status */

type Status struct {
	FrontDoor  string             `json:"front_door"`
	FrontUp    bool               `json:"front_door_running"`
	Apache     proc.Status        `json:"apache"`
	MySQL      proc.Status        `json:"mysql"`
	Nginx      proc.Status        `json:"nginx"`
	MySQLReady bool               `json:"mysql_installed"`
	PHP        []PHPStatus        `json:"php"`
	Sources    []importers.Source `json:"import_sources"`
	SitesRoot  string             `json:"sites_root"`
	Roots      []config.Root      `json:"roots"`
	CA         string             `json:"ca_certificate"`
	Addons     []AddonView        `json:"addons"`
	Shares     []share.Share      `json:"shares"`
	Providers  []ProviderView     `json:"share_providers"`
	NoAccess   []string           `json:"waiting_for_access,omitempty"` // folders macOS hasn't allowed yet
	Adminer    AdminerInfo        `json:"adminer"`
}

type PHPStatus struct {
	runtime.PHP
	Running bool `json:"running"`
	Sites   int  `json:"sites"`
}

func (e *Engine) Status() Status {
	cfg := e.Store.Get()
	count := map[string]int{}
	for _, s := range e.Sites() {
		if s.Enabled {
			count[s.PHP]++
		}
	}
	st := Status{FrontDoor: e.Front.Name(), FrontUp: e.Front.Running(), Apache: e.Sup.Status(procApache), MySQL: e.Sup.Status(procMySQL), Nginx: e.Sup.Status(procNginx),
		Sources: importers.Sources(), SitesRoot: cfg.Settings.SitesRoot, Roots: cfg.Settings.Roots, CA: e.CA.RootPath()}
	_, st.MySQLReady = runtime.MySQL()
	st.Addons = e.Addons()
	st.Shares = e.Shares.List()
	st.Providers = e.ShareProviders()
	st.NoAccess = WaitingForAccess()
	st.Adminer = e.Adminer()
	for _, v := range runtime.PHPVersions() {
		st.PHP = append(st.PHP, PHPStatus{PHP: v, Running: e.Sup.Status("php-" + v.Version).Running, Sites: count[v.Version]})
	}
	return st
}

// Log returns the last lines of a log. kind: apache | mysql | site-error | site-access | site-php
func (e *Engine) Log(kind, folder string, lines int) (string, error) {
	folder = fileKey(strings.ReplaceAll(folder, "..", ""))
	var file string
	switch kind {
	case "apache":
		file = filepath.Join(e.P.Logs, "apache-error.log")
	case "mysql":
		file = filepath.Join(e.P.Logs, "mysql-error.log")
	case "site-error":
		file = filepath.Join(e.P.SiteLogs, folder+"-error.log")
	case "site-access":
		file = filepath.Join(e.P.SiteLogs, folder+"-access.log")
	case "site-php":
		file = filepath.Join(e.P.SiteLogs, folder+"-php.log")
	case "site-app":
		file = filepath.Join(e.P.SiteLogs, folder+"-app.log")
	case "addon", "addon-install":
		if _, ok := addons.Find(folder); !ok {
			return "", errors.New("unknown add-on")
		}
		name := "addon-" + folder
		if kind == "addon-install" {
			name += "-install"
		}
		file = filepath.Join(e.P.Logs, name+".log")
	case "site-install":
		file = filepath.Join(e.P.SiteLogs, folder+"-install.log")
	default:
		return "", errors.New("unknown log")
	}
	return tailFile(file, lines), nil
}

/* ---------------------------------------------------------------- helpers */

// prototypeSites reads settings from the earlier ~/.localsites dashboard so nothing is lost.
func prototypeSites(home string) map[string]config.Site {
	out := map[string]config.Site{}
	b, err := os.ReadFile(filepath.Join(home, ".localsites", "config", "sites.json"))
	if err != nil {
		return out
	}
	var v struct {
		Sites map[string]struct {
			Enabled bool   `json:"enabled"`
			Domain  string `json:"domain"`
			PHP     string `json:"php"`
			Docroot string `json:"docroot"`
		} `json:"sites"`
	}
	if json.Unmarshal(b, &v) == nil {
		for k, s := range v.Sites {
			out[k] = config.Site{Enabled: s.Enabled, Domain: s.Domain, PHP: s.PHP, Docroot: s.Docroot}
		}
	}
	return out
}

func tailFile(file string, n int) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// mergeIni layers a project's php.ini overrides on top of the global ones.
func mergeIni(global, site map[string]string) map[string]string {
	if len(site) == 0 {
		return global
	}
	out := map[string]string{}
	for k, v := range global {
		out[k] = v
	}
	for k, v := range site {
		out[k] = v
	}
	return out
}

func sortedIniKeys() []string {
	keys := make([]string, 0, len(PHPIniKeys))
	for k := range PHPIniKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TakeOver serves a folder that Valet serves (parked or linked) with LocalSites at the same address.
// Valet's own configuration is not edited: LocalSites adds its Valet proxy for that one domain, which
// takes precedence. Switching the project off removes the proxy and Valet serves it again.
func (e *Engine) TakeOver(folder string) ([]string, error) {
	s, err := e.site(folder)
	if err != nil {
		return nil, err
	}
	if s.ServedBy != "valet" {
		return nil, errors.New(s.Name + " is not served by Valet")
	}
	if s.Runtime == "none" {
		return nil, errors.New("set the project's type first")
	}
	cp := s.Site
	cp.TakeOver, cp.Enabled = true, true
	if s.Runtime == "php" {
		if _, ok := runtime.FindPHP(cp.PHP); !ok {
			if v := runtime.ClosestPHP(cp.PHP + ".0"); v != "" {
				cp.PHP = v
			}
		}
	}
	if err := e.Store.Update(func(c *config.Config) error { c.Sites[folder] = &cp; return nil }); err != nil {
		return nil, err
	}
	return append([]string{"LocalSites now serves " + cp.Domain + " (Valet keeps your other sites)"}, e.Apply()...), nil
}

// xdebugAdmin turns Xdebug on for projects that want it and keeps it off elsewhere (only matters where it is loaded).
func xdebugAdmin(s SiteView) map[string]string {
	if !s.Xdebug {
		return map[string]string{"xdebug.mode": "off"}
	}
	return map[string]string{"xdebug.mode": "debug,develop", "xdebug.start_with_request": "yes", "xdebug.client_host": "127.0.0.1", "xdebug.client_port": "9003"}
}
