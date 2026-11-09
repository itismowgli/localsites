// Package paths decides where LocalSites keeps its files on each platform.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// Paths are absolute directories used by LocalSites.
type Paths struct {
	Home      string // user's home directory
	Data      string // config, databases, logs, backups (persistent)
	Run       string // sockets and pid files (no spaces, short: unix socket paths are limited to ~104 chars)
	Config    string
	Generated string // configs generated on every apply (Apache, PHP-FPM, MySQL)
	Logs      string
	SiteLogs  string
	MySQLData string
	Backups   string
	CA        string
}

// Default returns the platform paths. Override the data dir with LOCALSITES_HOME (used by tests).
func Default() Paths {
	home, _ := os.UserHomeDir()
	var data, run string
	switch runtime.GOOS {
	case "darwin":
		data = filepath.Join(home, "Library", "Application Support", "LocalSites")
		run = filepath.Join(home, "Library", "Caches", "LocalSites", "run")
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		data = filepath.Join(base, "LocalSites")
		run = filepath.Join(base, "LocalSites", "run")
	default: // linux and others: XDG
		data = envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
		data = filepath.Join(data, "localsites")
		run = filepath.Join(envOr("XDG_RUNTIME_DIR", filepath.Join(home, ".cache")), "localsites")
	}
	if v := os.Getenv("LOCALSITES_HOME"); v != "" {
		data = v
		run = filepath.Join(v, "run")
	}
	return Paths{
		Home:      home,
		Data:      data,
		Run:       run,
		Config:    filepath.Join(data, "config"),
		Generated: filepath.Join(data, "generated"),
		Logs:      filepath.Join(data, "logs"),
		SiteLogs:  filepath.Join(data, "logs", "sites"),
		MySQLData: filepath.Join(data, "mysql"),
		Backups:   filepath.Join(data, "backups"),
		CA:        filepath.Join(data, "ca"),
	}
}

// Ensure creates all directories with owner-only permissions.
func (p Paths) Ensure() error {
	for _, d := range []string{p.Data, p.Run, p.Config, p.Generated, p.Logs, p.SiteLogs, p.MySQLData, p.Backups, p.CA} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// Socket returns the path of a unix socket in the run dir.
func (p Paths) Socket(name string) string { return filepath.Join(p.Run, name+".sock") }

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
