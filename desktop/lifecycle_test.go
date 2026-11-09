package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestPageSuspendsOnlyWhileHidden(t *testing.T) {
	var asked, reloaded atomic.Int32
	oldAsk, oldReload, oldAfter := askSuspend, reloadPage, SuspendAfter
	askSuspend = func(*App) { asked.Add(1) }
	reloadPage = func(*App) { reloaded.Add(1) }
	SuspendAfter = 50 * time.Millisecond
	t.Cleanup(func() { askSuspend, reloadPage, SuspendAfter = oldAsk, oldReload, oldAfter })
	a := &App{}

	a.windowVisible(true)
	a.windowVisible(false)
	a.windowVisible(true) // shown again before the timer: no request
	time.Sleep(120 * time.Millisecond)
	if asked.Load() != 0 {
		t.Fatal("asked to suspend although the window came back")
	}

	a.windowVisible(false)
	time.Sleep(120 * time.Millisecond)
	if asked.Load() != 1 {
		t.Fatalf("asked %d times", asked.Load())
	}
	if !a.PageSuspended() {
		t.Fatal("hidden window refused to suspend")
	}
	a.windowVisible(true)
	if reloaded.Load() != 1 {
		t.Fatal("page not reloaded when shown")
	}
	a.windowVisible(true) // repeated reports change nothing
	if reloaded.Load() != 1 {
		t.Fatal("reloaded twice")
	}
	if a.PageSuspended() {
		t.Fatal("a visible window agreed to suspend")
	}
}
