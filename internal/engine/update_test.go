package engine

import (
	"path/filepath"
	"testing"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/paths"
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
