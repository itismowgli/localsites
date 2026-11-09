package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/runtime"
)

// Adminer (single PHP file, Apache-2.0 / GPL-2.0) at https://adminer.<tld>, for people who like phpMyAdmin.
// A pinned release is downloaded once and refused unless its SHA-256 matches. No automatic login:
// sign in with a database's own login (Databases → Connect).
const (
	adminerVersion = "6.2.0"
	adminerURL     = "https://github.com/vrana/adminer/releases/download/v" + adminerVersion + "/adminer-" + adminerVersion + "-en.php"
	adminerSHA256  = "a1f521b93070bc3298f3a137786c4520496adfad536f312e80a898247e3bf3ae"
	adminerFolder  = "_adminer"
)

// AdminerURLOverride lets tests serve the file locally.
var AdminerURLOverride = ""

// adminerMu: one switch at a time, so two quick clicks don't download and write the file twice.
var adminerMu sync.Mutex

func (e *Engine) adminerDir() string { return filepath.Join(e.P.Data, "tools", "adminer") }

// AdminerInfo is Adminer's state for the UI.
type AdminerInfo struct {
	Enabled   bool   `json:"enabled"`
	Installed bool   `json:"installed"`
	URL       string `json:"url"`
	Version   string `json:"version"`
}

func (e *Engine) Adminer() AdminerInfo {
	_, err := os.Stat(filepath.Join(e.adminerDir(), "adminer.php"))
	return AdminerInfo{Enabled: e.Store.Get().Settings.Adminer, Installed: err == nil, URL: "https://adminer." + e.Store.Get().Settings.TLD, Version: adminerVersion}
}

// SetAdminer switches Adminer on (downloading and checking it the first time) or off, then applies.
func (e *Engine) SetAdminer(on bool) ([]string, error) {
	adminerMu.Lock()
	defer adminerMu.Unlock()
	if on {
		if err := e.installAdminer(); err != nil {
			return nil, err
		}
	}
	if err := e.Store.Update(func(c *config.Config) error { c.Settings.Adminer = on; return nil }); err != nil {
		return nil, err
	}
	return e.Apply(), nil
}

func (e *Engine) installAdminer() error {
	dir := e.adminerDir()
	file := filepath.Join(dir, "adminer.php")
	if b, err := os.ReadFile(file); err == nil && sha(b) == adminerSHA256 {
		return nil
	}
	src := adminerURL
	if AdminerURLOverride != "" {
		src = AdminerURLOverride
	}
	c := &http.Client{Timeout: 60 * time.Second}
	res, err := c.Get(src)
	if err != nil {
		return fmt.Errorf("could not download Adminer: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("could not download Adminer: %s", res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 5<<20))
	if err != nil {
		return err
	}
	if sha(b) != adminerSHA256 {
		return errors.New("the downloaded Adminer did not match its known fingerprint, so it was not installed")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(file, b); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "index.php"), []byte("<?php\n// Served by LocalSites. Adminer "+adminerVersion+" (https://www.adminer.org)\ninclude __DIR__ . '/adminer.php';\n"))
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// adminerSite is Adminer as a site Apply can serve, when it is switched on and installed.
func (e *Engine) adminerSite() (SiteView, bool) {
	a := e.Adminer()
	if !a.Enabled || !a.Installed {
		return SiteView{}, false
	}
	vs := runtime.PHPVersions()
	if len(vs) == 0 {
		return SiteView{}, false
	}
	v := SiteView{Folder: adminerFolder, Name: "Adminer", Path: e.adminerDir(), Runtime: "php", Type: KindPHP}
	v.Enabled, v.Domain, v.PHP, v.DBServer = true, "adminer."+e.Store.Get().Settings.TLD, vs[len(vs)-1].Version, config.DBLocalSites
	return v, true
}
