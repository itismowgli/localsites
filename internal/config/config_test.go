package config

import (
	"os"
	"path/filepath"
	"testing"
)

// An older config without "roots" must keep using its own sites_root (never the default folder).
func TestOldConfigKeepsItsSitesRoot(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "localsites.json"), []byte(`{"version":1,"settings":{"sites_root":"/tmp/my-sites"},"sites":{}}`), 0o600)
	s, err := Open(dir, "/Users/someone")
	if err != nil {
		t.Fatal(err)
	}
	c := s.Get()
	if len(c.Settings.Roots) != 1 || c.Settings.Roots[0].Path != "/tmp/my-sites" || c.Settings.SitesRoot != "/tmp/my-sites" {
		t.Fatalf("roots: %+v sites_root: %s", c.Settings.Roots, c.Settings.SitesRoot)
	}
}

// A config saved by a newer LocalSites (after switching back to an older version) is refused, not misread.
func TestNewerConfigIsRefused(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "localsites.json"), []byte(`{"version":99,"settings":{},"sites":{}}`), 0o600)
	if _, err := Open(dir, "/Users/someone"); err == nil {
		t.Fatal("a newer config format was accepted")
	}
}
