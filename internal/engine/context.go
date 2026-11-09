package engine

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// SiteContext is a one-call summary of a project for AI agents: how it is served, where its data lives,
// what is wrong, without secrets (.env shows names only, never values).
type SiteContext struct {
	Name         string   `json:"name"`
	Folder       string   `json:"folder"`
	Path         string   `json:"path"`
	Type         string   `json:"type"`
	Version      string   `json:"framework_version,omitempty"`
	Running      bool     `json:"running"`
	URL          string   `json:"url,omitempty"`
	ServedBy     string   `json:"served_by"` // apache | nginx | dev server | valet | not served
	PHP          string   `json:"php,omitempty"`
	WebRoot      string   `json:"web_root,omitempty"`
	DevServer    string   `json:"dev_server,omitempty"` // 127.0.0.1:port
	Database     string   `json:"database,omitempty"`   // e.g. "MAMP PRO MySQL via localhost; .env database shop"
	EnvKeys      []string `json:"env_keys,omitempty"`   // names in .env (values are never included)
	Git          *Git     `json:"git,omitempty"`
	Problems     []string `json:"problems,omitempty"`
	RecentErrors string   `json:"recent_errors,omitempty"`
	AddOns       []string `json:"add_ons,omitempty"` // running add-ons and their addresses
	Hints        []string `json:"hints"`
}

// SiteContext builds the summary for one project.
func (e *Engine) SiteContext(folder string) (SiteContext, error) {
	s, err := e.site(folder)
	if err != nil {
		return SiteContext{}, err
	}
	c := SiteContext{Name: s.Name, Folder: s.Folder, Path: s.Path, Type: s.Type, Version: s.Framework.Version, Running: s.Enabled || s.ServedBy != "",
		URL: s.URL, Git: s.Git, Problems: s.Problems}
	switch {
	case s.ServedBy == "valet":
		c.ServedBy = "Laravel Valet"
	case s.Runtime == "php" && s.Server == "nginx":
		c.ServedBy = "nginx + PHP-FPM"
	case s.Runtime == "php":
		c.ServedBy = "Apache + PHP-FPM"
	case s.Runtime == "app":
		c.ServedBy = "its own dev server"
	default:
		c.ServedBy = "not served (not a website)"
	}
	if s.Runtime == "php" {
		c.PHP, c.WebRoot = s.PHP, filepath.Join(s.Path, s.Docroot)
		db := "LocalSites MySQL 8.4"
		if s.DBServer == "mamp" {
			db = "MAMP PRO MySQL"
		}
		c.Database = db + " (what \"localhost\" means to PHP here)"
	}
	if s.Runtime == "app" && s.Port != 0 {
		c.DevServer = fmt.Sprintf("127.0.0.1:%d", s.Port)
	}
	if _, vals, err := readEnv(filepath.Join(s.Path, ".env")); err == nil {
		for k := range vals {
			c.EnvKeys = append(c.EnvKeys, k)
		}
		sort.Strings(c.EnvKeys)
		if s.Env != nil {
			c.Database += "; .env uses database " + s.Env.Database
		}
	}
	kind := "site-error"
	if s.Runtime == "app" {
		kind = "site-app"
	}
	if txt, err := e.Log(kind, s.Folder, 25); err == nil {
		c.RecentErrors = strings.TrimSpace(txt)
	}
	for _, a := range e.Addons() {
		if a.Running {
			c.AddOns = append(c.AddOns, fmt.Sprintf("%s at 127.0.0.1:%d", a.Name, a.Port))
		}
	}
	c.Hints = []string{
		"Use sites_check to test DNS, PHP, the database socket and HTTPS end to end.",
		"Use logs with kind site-error, site-php or site-app for more lines.",
		"Never print or copy values from .env; ask the user if a secret is needed.",
	}
	return c, nil
}
