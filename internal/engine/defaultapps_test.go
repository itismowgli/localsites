package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localsites/localsites/internal/apps"
	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/paths"
)

func TestDefaultApps(t *testing.T) {
	dir := t.TempDir()
	st, err := config.Open(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{P: paths.Paths{Data: dir, Config: dir}, Store: st}

	mac := t.TempDir()
	for _, n := range []string{"Cursor.app", "Zed.app", "iTerm.app"} {
		if err := os.Mkdir(filepath.Join(mac, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldOS, oldDirs := apps.OS, apps.Dirs
	apps.OS, apps.Dirs = "darwin", func() []string { return []string{mac} }
	t.Cleanup(func() { apps.OS, apps.Dirs = oldOS, oldDirs })

	// Nothing is guessed: no default until the person picks, and the error lists what's installed.
	if d := e.InstalledApps().Defaults; len(d) != 0 {
		t.Fatalf("defaults before choosing = %v", d)
	}
	_, err = e.ResolveApp("editor")
	if err == nil || !strings.Contains(err.Error(), "No code editor chosen yet") || !strings.Contains(err.Error(), "Cursor (cursor), Zed (zed)") {
		t.Fatalf("unchosen editor error = %v", err)
	}

	if err := e.SetDefaultApp("editor", "zed"); err != nil {
		t.Fatal(err)
	}
	if id, err := e.ResolveApp("editor"); id != "zed" || err != nil {
		t.Fatalf("resolve = %q %v", id, err)
	}
	if id, _ := e.ResolveApp("cursor"); id != "cursor" {
		t.Fatalf("a plain app id must pass through, got %q", id)
	}

	// Wrong kind, unknown kind, not installed
	if err := e.SetDefaultApp("editor", "iterm"); err == nil || !strings.Contains(err.Error(), "not a code editor") {
		t.Fatalf("terminal as editor = %v", err)
	}
	if err := e.SetDefaultApp("browser", "zed"); err == nil {
		t.Fatal("browser kind accepted")
	}
	if err := e.SetDefaultApp("editor", "phpstorm"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("missing app = %v", err)
	}

	// The chosen editor is uninstalled: say so and list the others.
	os.Remove(filepath.Join(mac, "Zed.app"))
	if _, err := e.ResolveApp("editor"); err == nil || !strings.Contains(err.Error(), "(zed) is no longer installed") {
		t.Fatalf("uninstalled default = %v", err)
	}

	// Ask every time, then forget.
	if err := e.SetDefaultApp("editor", AskEveryTime); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ResolveApp("editor"); err == nil || !strings.Contains(err.Error(), "pick the code editor each time") {
		t.Fatalf("ask = %v", err)
	}
	if err := e.SetDefaultApp("editor", ""); err != nil {
		t.Fatal(err)
	}
	if d := e.InstalledApps().Defaults; len(d) != 0 {
		t.Fatalf("defaults after forgetting = %v", d)
	}

	// The choice survives a restart (it's in the settings file, not the app window).
	_ = e.SetDefaultApp("terminal", "iterm")
	st2, _ := config.Open(dir, dir)
	if got := st2.Get().Settings.Apps["terminal"]; got != "iterm" {
		t.Fatalf("saved terminal = %q", got)
	}
}
