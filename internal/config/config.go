// Package config holds LocalSites settings and the list of sites.
//
// Two layers:
//   - config/localsites.json   global settings + per-site settings (written by the app)
//   - <project>/localsites.yaml optional, checked into the project; wins over the app's settings
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const ProjectFile = "localsites.yaml"

// Database servers a site's PHP connects to when code uses host "localhost".
const (
	DBLocalSites = "localsites" // LocalSites' own MySQL 8.4
	DBMAMP       = "mamp"       // MAMP / MAMP PRO MySQL (during migration)
)

// Site is one project folder.
type Site struct {
	Folder   string            `json:"-"`
	Path     string            `json:"-"`
	Enabled  bool              `json:"enabled"`
	Domain   string            `json:"domain"`
	PHP      string            `json:"php"`
	Docroot  string            `json:"docroot"`                  // relative to Path, "" = project root
	DBServer string            `json:"db_server"`                // DBLocalSites or DBMAMP
	Database string            `json:"database,omitempty"`       // database name owned by this site (optional)
	Docker   bool              `json:"docker,omitempty"`         // phase 2
	Port     int               `json:"port,omitempty"`           // dev server port for Next.js / Node / custom commands (assigned on first start)
	Kind     string            `json:"kind,omitempty"`           // override detection: php | laravel | wordpress | static | nextjs | node | command | other
	Command  string            `json:"command,omitempty"`        // custom start command; {port} is replaced (e.g. "python manage.py runserver {port}")
	EnvVars  map[string]string `json:"env_vars,omitempty"`       // extra environment for dev servers (never written to .env)
	PHPIni   map[string]string `json:"php_ini,omitempty"`        // php.ini overrides for this project only
	TakeOver bool              `json:"valet_takeover,omitempty"` // serve a Valet-parked/linked folder with LocalSites (our Valet proxy wins)
	Xdebug   bool              `json:"xdebug,omitempty"`         // step debugging for this project (port 9003)
	Server   string            `json:"web_server,omitempty"`     // PHP projects: "" or "apache" (default), "nginx"

	// Filled at load time, not saved
	ManagedBy string `json:"-"` // "localsites.yaml" when the project file controls settings
}

// Root is a folder that holds projects (directly, or inside group folders such as one per git account).
type Root struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	// Layout: "auto" (default) detects group folders, "flat" = every subfolder is a project,
	// "groups" = every subfolder is a group (e.g. one per git account) holding projects.
	Layout string `json:"layout,omitempty"`
}

// Settings are global options.
type Settings struct {
	SitesRoot  string `json:"sites_root"` // first entry of Roots (kept for older configs and the CLI)
	Roots      []Root `json:"roots,omitempty"`
	FrontDoor  string `json:"front_door"` // auto | valet | builtin
	ApachePort int    `json:"apache_port"`
	NginxPort  int    `json:"nginx_port,omitempty"` // internal port for projects served by nginx
	MySQLPort  int    `json:"mysql_port"`
	TLD        string `json:"tld"`
	// Built-in front door ports (only used when front_door is "builtin")
	HTTPPort  int `json:"http_port,omitempty"`
	HTTPSPort int `json:"https_port,omitempty"`
	DNSPort   int `json:"dns_port,omitempty"`
	// PHP version for new projects ("" = newest installed)
	DefaultPHP string `json:"default_php,omitempty"`
	// php.ini values applied to every project (on top of LocalSites' MAMP-compatible defaults)
	PHPIni map[string]string `json:"php_ini,omitempty"`
	// Add-on services (mail catcher, caches), keyed by add-on id
	Addons map[string]*Addon `json:"addons,omitempty"`
	// Adminer at https://adminer.<tld>
	Adminer bool `json:"adminer,omitempty"`
	// Keep LocalSites MySQL running even when nothing uses it (default: it sleeps after 20 idle minutes)
	MySQLAlwaysOn bool `json:"mysql_always_on,omitempty"`
	// Updates. Checking is opt-in: nothing is asked of GitHub until the person turns it on.
	UpdateAutoCheck bool   `json:"update_auto_check,omitempty"`
	UpdateMode      string `json:"update_mode,omitempty"`    // "notify" (default): tell me; "download": download, install on quit; "auto": also install at night
	UpdateChannel   string `json:"update_channel,omitempty"` // "stable" (default) or "beta"
	UpdateAsked     bool   `json:"update_asked,omitempty"`   // the one-time question has been answered
	UpdateSkip      string `json:"update_skip,omitempty"`    // a version the person chose to skip
	UpdateLater     string `json:"update_later,omitempty"`   // a version the person chose to install when they quit
	// Problem reports: "off" (default: nothing is sent), "ask" (send after I review), "auto" (send cleaned reports by themselves)
	ReportMode string `json:"report_mode,omitempty"`
}

// Addon is the saved state of one add-on service.
type Addon struct {
	Enabled   bool `json:"enabled"`
	Port      int  `json:"port,omitempty"`
	UIPort    int  `json:"ui_port,omitempty"`
	KeepMail  bool `json:"keep_mail,omitempty"`
	MemoryMB  int  `json:"memory_mb,omitempty"`
	PersistDB bool `json:"persist,omitempty"`
}

// Config is the saved state.
type Config struct {
	Version  int              `json:"version"`
	Settings Settings         `json:"settings"`
	Sites    map[string]*Site `json:"sites"`
}

// Store loads and saves the config file safely across goroutines.
type Store struct {
	mu   sync.Mutex
	file string
	cfg  Config
}

// SchemaVersion is the config format this build writes. A newer file (from a newer LocalSites, after
// switching back to an older version) is refused rather than misread.
const SchemaVersion = 1

func Defaults(home string) Config {
	root := filepath.Join(home, "Sites", "localhost")
	return Config{
		Version: 1,
		Settings: Settings{
			SitesRoot:  root,
			Roots:      []Root{{Path: root, Label: "localhost"}},
			FrontDoor:  "auto",
			ApachePort: 8744,
			MySQLPort:  3307,
			TLD:        "test",
			HTTPPort:   80,
			HTTPSPort:  443,
			DNSPort:    5354,
		},
		Sites: map[string]*Site{},
	}
}

func Open(dir, home string) (*Store, error) {
	s := &Store{file: filepath.Join(dir, "localsites.json"), cfg: Defaults(home)}
	b, err := os.ReadFile(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return s, s.saveLocked()
	}
	if err != nil {
		return nil, err
	}
	s.cfg.Settings.Roots = nil // an older file without "roots" must use its own sites_root, not the default folder
	if err := json.Unmarshal(b, &s.cfg); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", s.file, err)
	}
	if s.cfg.Version > SchemaVersion {
		return nil, fmt.Errorf("%s was saved by a newer LocalSites (format %d; this version reads %d). Install the newer version again, or restore a backup from the updates folder", s.file, s.cfg.Version, SchemaVersion)
	}
	def := Defaults(home)
	if s.cfg.Settings.ApachePort == 0 {
		s.cfg.Settings.ApachePort = def.Settings.ApachePort
	}
	if s.cfg.Settings.MySQLPort == 0 {
		s.cfg.Settings.MySQLPort = def.Settings.MySQLPort
	}
	if s.cfg.Settings.TLD == "" {
		s.cfg.Settings.TLD = def.Settings.TLD
	}
	if s.cfg.Settings.FrontDoor == "" {
		s.cfg.Settings.FrontDoor = "auto"
	}
	if s.cfg.Settings.HTTPPort == 0 {
		s.cfg.Settings.HTTPPort, s.cfg.Settings.HTTPSPort, s.cfg.Settings.DNSPort = 80, 443, 5354
	}
	if s.cfg.Sites == nil {
		s.cfg.Sites = map[string]*Site{}
	}
	s.cfg.Settings.NormalizeRoots()
	return s, nil
}

// Get returns a deep copy of the config.
func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Sites = make(map[string]*Site, len(s.cfg.Sites))
	for k, v := range s.cfg.Sites {
		cp := *v
		c.Sites[k] = &cp
	}
	return c
}

// Update changes the config inside fn and saves it.
func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.cfg); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.file), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s.cfg, "", "  ")
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}

// NormalizeRoots keeps Roots and SitesRoot in step: Roots[0] is the main projects folder.
func (s *Settings) NormalizeRoots() {
	if len(s.Roots) == 0 && s.SitesRoot != "" {
		s.Roots = []Root{{Path: s.SitesRoot, Label: filepath.Base(s.SitesRoot)}}
	}
	if len(s.Roots) > 0 {
		s.SitesRoot = s.Roots[0].Path
	}
	for i := range s.Roots {
		if s.Roots[i].Label == "" {
			s.Roots[i].Label = filepath.Base(s.Roots[i].Path)
		}
	}
}

// ProjectSettings is the optional localsites.yaml in a project.
type ProjectSettings struct {
	Domain   string `yaml:"domain"`
	PHP      string `yaml:"php"`
	Docroot  string `yaml:"docroot"`
	Database string `yaml:"database"`
}

// LoadProject reads <dir>/localsites.yaml if present.
func LoadProject(dir string) (*ProjectSettings, error) {
	b, err := os.ReadFile(filepath.Join(dir, ProjectFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p ProjectSettings
	if err := yaml.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", ProjectFile, err)
	}
	return &p, nil
}

var (
	labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	slugRe  = regexp.MustCompile(`[^a-z0-9-]+`)
	dbRe    = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
)

// ValidDomain checks "name.<tld>" with a single DNS label.
func ValidDomain(domain, tld string) bool {
	suffix := "." + tld
	if !strings.HasSuffix(domain, suffix) {
		return false
	}
	return labelRe.MatchString(strings.TrimSuffix(domain, suffix))
}

// ValidDatabaseName allows the characters safe to use unquoted in MySQL identifiers.
func ValidDatabaseName(name string) bool { return dbRe.MatchString(name) }

// Slug turns a folder name into a domain label.
func Slug(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		return "site"
	}
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	return s
}
