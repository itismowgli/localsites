package engine

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/localsites/localsites/internal/notify"
	"github.com/localsites/localsites/internal/update"
)

// UpdateStatus is the update state plus the person's choice to skip it.
type UpdateStatus struct {
	update.Status
	Skipped bool `json:"skipped,omitempty"`
}

var updNotified sync.Map // version → already told the person

func (e *Engine) updater() *update.Updater {
	e.updOnce.Do(func() { e.upd = update.New(filepath.Join(e.P.Data, "updates")) })
	return e.upd
}

func (e *Engine) updChannel() string {
	if c := e.Store.Get().Settings.UpdateChannel; c != "" {
		return c
	}
	return "stable"
}

// UpdateState returns where an update is.
func (e *Engine) UpdateState() UpdateStatus {
	st := e.updater().Status()
	skip := e.Store.Get().Settings.UpdateSkip
	return UpdateStatus{Status: st, Skipped: st.Latest != nil && skip == st.Latest.Version}
}

// CheckUpdate asks for the newest release now (always allowed, even with automatic checks off).
func (e *Engine) CheckUpdate() UpdateStatus {
	e.updater().Check(e.updChannel())
	e.Changes.Bump()
	return e.UpdateState()
}

// DownloadUpdate starts downloading the available update.
func (e *Engine) DownloadUpdate() (UpdateStatus, error) {
	_, err := e.updater().Download(e.updateDownloaded)
	e.Changes.Bump()
	return e.UpdateState(), err
}

func (e *Engine) updateDownloaded(st update.Status) {
	e.Changes.Bump()
	if st.Phase != update.Ready || st.Latest == nil {
		return
	}
	if _, seen := updNotified.LoadOrStore("ready:"+st.Latest.Version, true); seen {
		return
	}
	when := "Restart LocalSites from Settings → Updates to install it, or it installs when you quit."
	if e.Store.Get().Settings.UpdateMode == "auto" {
		when = "It installs when you quit LocalSites, or tonight if it's still open."
	}
	notify.Send("LocalSites "+st.Latest.Version+" is ready", when)
}

// UpdateTick runs hourly in the service: with automatic checks on, check once a day, then tell the
// person, or download, as they chose.
func (e *Engine) UpdateTick() {
	s := e.Store.Get().Settings
	if !s.UpdateAutoCheck {
		return
	}
	u := e.updater()
	if st := u.Status(); time.Since(st.LastCheck) < 23*time.Hour || st.Phase == update.Downloading || st.Phase == update.Installing {
		return
	}
	st := u.Check(e.updChannel())
	e.Changes.Bump()
	if st.Latest == nil || (st.Phase != update.Available && st.Phase != update.Ready) || s.UpdateSkip == st.Latest.Version {
		return
	}
	if s.UpdateMode == "download" || s.UpdateMode == "auto" {
		if st.Phase == update.Available {
			_, _ = u.Download(e.updateDownloaded)
		}
		return
	}
	if _, seen := updNotified.LoadOrStore("found:"+st.Latest.Version, true); !seen {
		title := "LocalSites " + st.Latest.Version + " is available"
		if st.Latest.Security {
			title += " (security fix)"
		}
		notify.Send(title, "Open Settings → Updates to see what's new and install it.")
	}
}

// InstallUpdate swaps the app for the downloaded version (or, with rollback, for the kept one).
// appExe is the running app's executable, appPID its process; then is what starts afterwards:
// "open" (the app), "open-bg" (the app, without taking focus) or "service" (only the background service).
// The service stops right after this returns; the helper waits for it and the app to quit.
func (e *Engine) InstallUpdate(appExe string, appPID int, then string, rollback bool) error {
	switch then {
	case "open", "open-bg", "service":
	default:
		return errors.New("then must be open, open-bg or service")
	}
	if appPID <= 1 {
		return errors.New("app_pid is required")
	}
	app, err := update.BundleOf(appExe)
	if err != nil {
		return err
	}
	u := e.updater()
	p, err := u.Prepare(app, rollback)
	if err != nil {
		return err
	}
	p.Wait, p.Then = []int{appPID, os.Getpid()}, then
	p.ConfigFrom, err = e.backupConfig(p.ToVer)
	if err == nil {
		var self string
		if self, err = os.Executable(); err == nil {
			err = update.Spawn(self, p)
		}
	}
	if err != nil {
		u.Cancel(p, rollback)
		return err
	}
	return nil
}

// backupConfig copies the settings file before the app changes, keeping the newest three.
func (e *Engine) backupConfig(ver string) (string, error) {
	dir := filepath.Join(e.P.Data, "updates", "config-backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(e.P.Config, "localsites.json"))
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "localsites-"+ver+"-"+time.Now().Format("20060102-150405")+".json")
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return "", err
	}
	old, _ := filepath.Glob(filepath.Join(dir, "localsites-*.json"))
	sort.Slice(old, func(i, j int) bool {
		a, _ := os.Stat(old[i])
		b, _ := os.Stat(old[j])
		return a != nil && b != nil && a.ModTime().After(b.ModTime())
	})
	for i, f := range old {
		if i >= 3 {
			_ = os.Remove(f)
		}
	}
	return dst, nil
}
