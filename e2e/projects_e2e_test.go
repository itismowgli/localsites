//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localsites/localsites/internal/api"
	"github.com/localsites/localsites/internal/engine"
	"github.com/localsites/localsites/internal/platform"
)

func siteIn(e *engine.Engine, folder string) (engine.SiteView, bool) {
	for _, s := range e.Sites() {
		if s.Folder == folder {
			return s, true
		}
	}
	return engine.SiteView{}, false
}

func TestHideRenameTrash(t *testing.T) {
	ev := setup(t)
	trash := filepath.Join(ev.home, "trash")
	t.Setenv("LOCALSITES_TRASH_DIR", trash)
	write(t, filepath.Join(ev.sites, "shop", "index.php"), `<?php echo "shop";`)
	write(t, filepath.Join(ev.sites, "shop", ".env"), "APP_URL=https://shop.test\n")
	ev.set("shop", engine.SitePatch{Enabled: on()})
	if _, body := ev.get("https://shop.test/"); body != "shop" {
		t.Fatalf("shop not served: %q", body)
	}

	t.Run("hide and show again", func(t *testing.T) {
		if _, err := ev.e.HideSite("shop"); err != nil {
			t.Fatal(err)
		}
		if _, ok := siteIn(ev.e, "shop"); ok {
			t.Fatal("hidden project still listed")
		}
		if code, _ := ev.get("https://shop.test/"); code != 404 {
			t.Fatalf("hidden project still served: %d", code)
		}
		if _, err := os.Stat(filepath.Join(ev.sites, "shop", "index.php")); err != nil {
			t.Fatal("hiding must not touch the folder")
		}
		if h := ev.e.HiddenSites(); len(h) != 1 || h[0].Folder != "shop" || h[0].Path == "" {
			t.Fatalf("hidden list = %+v", h)
		}
		if _, err := ev.e.UnhideSite("shop"); err != nil {
			t.Fatal(err)
		}
		s, ok := siteIn(ev.e, "shop")
		if !ok || !s.Enabled {
			t.Fatalf("unhidden project lost its settings: %+v", s)
		}
		if _, body := ev.get("https://shop.test/"); body != "shop" {
			t.Fatalf("not served after unhide: %q", body)
		}
	})

	t.Run("rename moves the folder, settings and address", func(t *testing.T) {
		log, err := ev.e.RenameSite("shop", "store")
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(log, "\n")
		if !strings.Contains(joined, "https://store.test") || !strings.Contains(joined, ".env still mentions shop.test") {
			t.Fatalf("log = %q", joined)
		}
		if _, err := os.Stat(filepath.Join(ev.sites, "store", "index.php")); err != nil {
			t.Fatal("folder not renamed")
		}
		if _, ok := siteIn(ev.e, "shop"); ok {
			t.Fatal("old name still listed")
		}
		s, ok := siteIn(ev.e, "store")
		if !ok || !s.Enabled || s.Domain != "store.test" {
			t.Fatalf("renamed project = %+v", s)
		}
		if _, body := ev.get("https://store.test/"); body != "shop" {
			t.Fatalf("renamed project not served: %q", body)
		}
		if code, _ := ev.get("https://shop.test/"); code != 404 {
			t.Fatalf("old address still served: %d", code)
		}
		if b, _ := os.ReadFile(filepath.Join(ev.sites, "store", ".env")); string(b) != "APP_URL=https://shop.test\n" {
			t.Fatal(".env must never be changed by a rename")
		}
	})

	t.Run("rename refuses bad names and existing folders", func(t *testing.T) {
		for _, bad := range []string{"", "../x", "a/b", ".hidden", "hello", "store"} {
			if _, err := ev.e.RenameSite("store", bad); err == nil {
				t.Errorf("accepted %q", bad)
			}
		}
		write(t, filepath.Join(ev.sites, "wt", ".git"), "gitdir: /somewhere/repo/.git/worktrees/wt\n")
		write(t, filepath.Join(ev.sites, "wt", "index.php"), "<?php")
		if _, err := ev.e.RenameSite("wt", "wt2"); err == nil || !strings.Contains(err.Error(), "worktree") {
			t.Fatalf("worktree rename = %v", err)
		}
	})

	t.Run("agents can't move projects to the Trash", func(t *testing.T) {
		srv := &api.Server{E: ev.e}
		if err := srv.Listen(); err != nil {
			t.Fatal(err)
		}
		defer srv.Close()
		c, err := api.Dial(ev.e.P)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.As("mcp").Call("sites.trash", map[string]any{"folder": "store", "name": "store"})
		if err == nil || !strings.Contains(err.Error(), "only the person") {
			t.Fatalf("agent trash = %v", err)
		}
		if _, err := os.Stat(filepath.Join(ev.sites, "store")); err != nil {
			t.Fatal("the folder must still be there")
		}
		raw, err := c.As("app").Call("sites.hidden", nil)
		var hidden []engine.HiddenSite
		if err != nil || json.Unmarshal(raw, &hidden) != nil {
			t.Fatalf("hidden list over the API: %v", err)
		}
	})

	t.Run("move to Trash needs the name and can be put back", func(t *testing.T) {
		if _, err := ev.e.TrashSite("store", "shop"); err == nil {
			t.Fatal("wrong confirmation accepted")
		}
		if _, err := ev.e.TrashSite("store", "store"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(ev.sites, "store")); !os.IsNotExist(err) {
			t.Fatal("folder still in the projects folder")
		}
		if _, err := os.Stat(filepath.Join(trash, "files", "store", "index.php")); err != nil {
			t.Fatal("folder not in the Trash")
		}
		info, err := os.ReadFile(filepath.Join(trash, "info", "store.trashinfo"))
		if err != nil || !strings.Contains(string(info), "Path="+filepath.Join(ev.sites, "store")) {
			t.Fatalf("trash info = %q %v", info, err)
		}
		if code, _ := ev.get("https://store.test/"); code != 404 {
			t.Fatalf("trashed project still served: %d", code)
		}
	})
}

// brewPath is a path inside Homebrew on this system (/opt/homebrew on a Mac, /home/linuxbrew/.linuxbrew on Linux).
func brewPath(rel string) string {
	for _, p := range platform.BrewPrefixes() {
		if _, err := os.Stat(p); err == nil {
			return filepath.Join(p, rel)
		}
	}
	return filepath.Join(platform.BrewPrefixes()[0], rel)
}
