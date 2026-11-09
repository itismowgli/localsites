package main

import (
	"encoding/json"
	"os"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var swapping atomic.Bool // an install is underway: quitting must not start a second one

// InstallUpdate installs the downloaded update now: LocalSites quits, the app is swapped and opens again.
// Projects pause for a few seconds while the services restart.
func (a *App) InstallUpdate() error { return a.swap("update.install", "open") }

// RollbackUpdate switches to the version kept from before the last update, the same way.
func (a *App) RollbackUpdate() error { return a.swap("update.rollback", "open") }

func (a *App) swap(method, then string) error {
	c, err := a.conn()
	if err != nil {
		return err
	}
	exe, _ := os.Executable()
	if _, err := c.Call(method, map[string]any{"app_path": exe, "app_pid": os.Getpid(), "then": then}); err != nil {
		return err
	}
	swapping.Store(true)
	go func() { time.Sleep(400 * time.Millisecond); runtime.Quit(a.ctx) }()
	return nil
}

type updState struct {
	Phase   string `json:"phase"`
	Skipped bool   `json:"skipped"`
	Latest  *struct {
		Version string `json:"version"`
	} `json:"latest"`
}

// updateWanted reports whether a downloaded update should install without asking now
// (the person chose "download" or "auto"), and the mode.
func (a *App) updateWanted() (bool, string) {
	c, err := a.conn()
	if err != nil {
		return false, ""
	}
	var set struct {
		Mode string `json:"update_mode"`
	}
	var st updState
	if raw, err := c.Call("settings.get", nil); err == nil {
		_ = json.Unmarshal(raw, &set)
	}
	if raw, err := c.Call("update.status", nil); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	ok := st.Phase == "ready" && !st.Skipped && (set.Mode == "download" || set.Mode == "auto")
	return ok, set.Mode
}

// installOnQuit runs when LocalSites quits: a ready update installs then, and the background
// service starts again from the new app (projects keep running, as they do after a normal quit).
func (a *App) installOnQuit() {
	if swapping.Load() {
		return
	}
	if ok, _ := a.updateWanted(); !ok {
		return
	}
	c, err := a.conn()
	if err != nil {
		return
	}
	exe, _ := os.Executable()
	_, _ = c.Call("update.install", map[string]any{"app_path": exe, "app_pid": os.Getpid(), "then": "service"})
}

// nightlyInstall: with "Install automatically", a ready update installs between 2 and 5 am
// and LocalSites comes back in the menu bar.
func (a *App) nightlyInstall() {
	for {
		time.Sleep(10 * time.Minute)
		if h := time.Now().Hour(); h < 2 || h >= 5 || swapping.Load() {
			continue
		}
		if ok, mode := a.updateWanted(); ok && mode == "auto" {
			_ = a.swap("update.install", "open-bg")
		}
	}
}
