//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
void lsTrayInit(const void *iconData, int iconLen);
void lsTraySetMenu(const char *spec);
void lsSetBadge(const char *label);
*/
import "C"

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

// trayIcon is the logo for the menu bar: black on transparent, which macOS tints for light and dark menu bars.
//
//go:embed trayicon.png
var trayIcon []byte

var trayApp *App
var trayOnce sync.Once

//export lsTrayClicked
func lsTrayClicked(id *C.char) {
	if trayApp != nil {
		go trayApp.trayAction(C.GoString(id))
	}
}

// startTray shows the menu bar icon and keeps its menu up to date (running projects, service state).
func (a *App) startTray() {
	trayOnce.Do(func() {
		trayApp = a
		icon := C.CBytes(trayIcon) // copied into NSData before lsTrayInit returns
		C.lsTrayInit(icon, C.int(len(trayIcon)))
		C.free(icon)
		go func() { // redraw only when the service says something changed (no polling)
			var ver uint64
			a.refreshTray()
			for {
				v, redraw := a.waitChange(ver)
				ver = v
				if redraw { // a wait that timed out with no change redraws nothing
					a.refreshTray()
				}
			}
		}()
	})
}

func (a *App) refreshTray() {
	lines, badge := a.trayMenu()
	setBadge(badge)
	cs := C.CString(strings.Join(lines, "\n"))
	defer C.free(unsafe.Pointer(cs))
	C.lsTraySetMenu(cs)
}

// setBadge shows the number on the Dock icon (nothing when it's zero).
func setBadge(n int) {
	label := ""
	if n > 0 {
		label = strconv.Itoa(n)
	}
	cs := C.CString(label)
	defer C.free(unsafe.Pointer(cs))
	C.lsSetBadge(cs)
}
