package main

import (
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// SuspendAfter is how long the window must stay hidden before its page is unloaded
// (LOCALSITES_SUSPEND_AFTER=20s shortens it for testing).
var SuspendAfter = func() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("LOCALSITES_SUSPEND_AFTER")); err == nil && d > 0 {
		return d
	}
	return 3 * time.Minute
}()

// While the window is off screen (in the menu bar, minimized or hidden) for a few minutes, the page is
// swapped for an empty one, which frees the page's memory and stops all of its timers. It comes back, on the
// same tabs, as soon as the window is visible again. The page itself refuses while anything is
// unsaved (staged data edits, a settings draft, an open dialog).
// replaced in tests
var (
	askSuspend = func(a *App) { a.emit("lifecycle", "suspend") } // the page checks for unsaved work, then calls PageSuspended
	reloadPage = func(a *App) { runtime.WindowReloadApp(a.ctx) }
)

type lifecycle struct {
	mu        sync.Mutex
	visible   bool
	since     time.Time
	suspended bool
	gen       int
}

func (a *App) windowVisible(v bool) {
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	if v == a.life.visible && !a.life.since.IsZero() {
		return
	}
	a.life.visible, a.life.since = v, time.Now()
	a.life.gen++
	if v {
		if a.life.suspended {
			a.life.suspended = false
			reloadPage(a)
		}
		return
	}
	gen := a.life.gen
	time.AfterFunc(SuspendAfter, func() {
		a.life.mu.Lock()
		still := !a.life.visible && a.life.gen == gen && !a.life.suspended
		a.life.mu.Unlock()
		if still {
			askSuspend(a)
		}
	})
}

// PageSuspended is called by the page right before it unloads itself.
func (a *App) PageSuspended() bool {
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	if a.life.visible { // shown again meanwhile: stay
		return false
	}
	a.life.suspended = true
	return true
}
