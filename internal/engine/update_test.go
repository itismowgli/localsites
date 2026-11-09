package engine

import (
	"path/filepath"
	"testing"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/paths"
	"github.com/localsites/localsites/internal/update"
)

func ptr[T any](v T) *T { return &v }

func TestUpdateSettings(t *testing.T) {
	dir := t.TempDir()
	st, err := config.Open(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{P: paths.Paths{Data: dir, Config: dir}, Store: st}
	if s := st.Get().Settings; s.UpdateAutoCheck || s.UpdateAsked {
		t.Fatal("update checks must be off until the person turns them on")
	}
	if _, err := e.UpdateSettings(SettingsPatch{UpdateAuto: ptr(true), UpdateMode: ptr("auto"), UpdateChan: ptr("beta")}); err != nil {
		t.Fatal(err)
	}
	s := st.Get().Settings
	if !s.UpdateAutoCheck || !s.UpdateAsked || s.UpdateMode != "auto" || s.UpdateChannel != "beta" {
		t.Fatalf("settings: %+v", s)
	}
	if _, err := e.UpdateSettings(SettingsPatch{UpdateMode: ptr("whenever")}); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	if !(SettingsPatch{UpdateSkip: ptr("1.0.0")}).onlyUpdates() || (SettingsPatch{UpdateSkip: ptr("1"), DefaultPHP: ptr("8.3")}).onlyUpdates() {
		t.Fatal("onlyUpdates")
	}
	if err := e.InstallUpdate(filepath.Join(dir, "bin", "localsites"), 123, "open", false); err == nil {
		t.Fatal("installed over something that isn't the app")
	}
	if err := e.InstallUpdate("/Applications/LocalSites.app/Contents/MacOS/LocalSites", 123, "reboot", false); err == nil {
		t.Fatal("accepted an unknown then")
	}
}

func TestUpdateStatusOnQuit(t *testing.T) {
	ready := update.Status{Phase: update.Ready, Latest: &update.Manifest{Version: "1.0.3"}}
	cases := []struct {
		name string
		set  config.Settings
		want bool
	}{
		{"tell me, no choice yet", config.Settings{UpdateMode: "notify"}, false},
		{"tell me, chose when I quit", config.Settings{UpdateMode: "notify", UpdateLater: "1.0.3"}, true},
		{"tell me, chose it for an older version", config.Settings{UpdateMode: "notify", UpdateLater: "1.0.2"}, false},
		{"download mode", config.Settings{UpdateMode: "download"}, true},
		{"automatic mode", config.Settings{UpdateMode: "auto"}, true},
		{"skipped wins", config.Settings{UpdateMode: "auto", UpdateSkip: "1.0.3"}, false},
	}
	for _, c := range cases {
		if got := updateStatus(ready, c.set).OnQuit; got != c.want {
			t.Errorf("%s: on_quit = %v, want %v", c.name, got, c.want)
		}
	}
}
