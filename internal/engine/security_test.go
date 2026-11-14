package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localsites/localsites/internal/paths"
)

// .env writes never follow links planted in a project.
func TestWriteFileAtomicRefusesLinks(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "zshrc")
	_ = os.WriteFile(victim, []byte("keep"), 0o600)
	env := filepath.Join(dir, ".env")
	_ = os.WriteFile(env, []byte("A=1\n"), 0o644)
	_ = os.Symlink(victim, env+".localsites-tmp") // the old, predictable temp name
	if err := writeFileAtomic(env, []byte("A=2\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatal("followed a planted temp link")
	}
	if b, _ := os.ReadFile(env); string(b) != "A=2\n" {
		t.Fatalf(".env: %q", b)
	}
	link := filepath.Join(dir, "linked.env")
	_ = os.Symlink(victim, link)
	if err := writeFileAtomic(link, []byte("x")); err == nil {
		t.Fatal("wrote through a link")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatal("victim changed")
	}
}

// Exports stay in the home folder (or a temporary one) even through a link, and never write over a link.
func TestExportPathFollowsLinks(t *testing.T) {
	home := t.TempDir()
	e := &Engine{P: paths.Paths{Home: home}}
	_ = os.Symlink("/usr", filepath.Join(home, "sys"))
	if err := e.exportPath(filepath.Join(home, "sys", "x.sql"), false); err == nil || !strings.Contains(err.Error(), "home") {
		t.Fatalf("export through a link out of home: %v", err)
	}
	_ = os.Symlink("/etc/hosts", filepath.Join(home, "h.sql"))
	if err := e.exportPath(filepath.Join(home, "h.sql"), true); err == nil {
		t.Fatal("export onto a link accepted")
	}
	if err := e.exportPath(filepath.Join(home, "ok.sql"), false); err != nil {
		t.Fatal(err)
	}
}

func TestLoaderEnvRefused(t *testing.T) {
	if !loaderEnv("DYLD_INSERT_LIBRARIES") || !loaderEnv("ld_preload") || loaderEnv("NODE_ENV") {
		t.Fatal("loader env check")
	}
}
