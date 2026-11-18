package main

import (
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// theApp receives presses on notification buttons (set in main).
var theApp *App

// noticeAction handles a press on a notification: a button (approve, deny, extend, stop, logs) or the
// notification itself (open). It acts as the app, exactly like the same button in the window.
func noticeAction(kind, ref, action string) {
	a := theApp
	if a == nil || action == "dismiss" {
		return
	}
	for i := 0; i < 50 && a.ctx == nil; i++ { // macOS may deliver the press while the app is still starting
		time.Sleep(100 * time.Millisecond)
	}
	call := func(method string, params map[string]any) {
		if c, err := a.conn(); err == nil {
			_, _ = c.Call(method, params)
		}
	}
	switch action {
	case "approve", "deny":
		if kind == "agent" && ref != "" {
			call("approvals.decide", map[string]any{"id": ref, "approve": action == "approve"})
		}
		return
	case "extend":
		call("share.extend", map[string]any{"id": ref, "minutes": 30})
		return
	case "stop":
		call("share.stop", map[string]any{"id": ref})
		return
	}
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
	a.emit("notice", kind, ref, action) // the page opens the right place (logs for "logs")
}
