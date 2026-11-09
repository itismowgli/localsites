//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
void lsWatchWindow(void);
*/
import "C"

var lifeApp *App

//export lsWindowVisible
func lsWindowVisible(v C.int) {
	if lifeApp != nil {
		go lifeApp.windowVisible(v != 0)
	}
}

// watchWindow reports when the window is hidden (closed to the menu bar, minimized, ⌘H) and shown again.
func (a *App) watchWindow() {
	lifeApp = a
	C.lsWatchWindow()
}
