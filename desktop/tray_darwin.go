//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
void lsTrayInit(const void *iconData, int iconLen);
void lsTraySetMenu(const char *spec);
*/
import "C"

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
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
	lines := []string{}
	add := func(kind, id, title string) {
		title = strings.NewReplacer("\t", " ", "\n", " ").Replace(title)
		lines = append(lines, kind+"\t"+id+"\t"+title)
	}
	c, err := a.conn()
	if err != nil {
		add("header", "", "LocalSites is not running")
	} else {
		var sites []struct {
			Folder, Name, Domain, URL string
			Enabled                   bool
			ServedBy                  string `json:"served_by"`
		}
		if raw, err := c.Call("sites.list", nil); err == nil {
			_ = json.Unmarshal(raw, &sites)
		}
		var running []string
		for _, s := range sites {
			if s.Enabled {
				running = append(running, s.Folder+"\x00"+s.Domain)
			}
		}
		var shares []struct{ Folder, URL string }
		if raw, err := c.Call("share.list", nil); err == nil {
			_ = json.Unmarshal(raw, &shares)
		}
		var approvals []struct{ ID string }
		if raw, err := c.Call("approvals.list", nil); err == nil {
			_ = json.Unmarshal(raw, &approvals)
		}
		var upd updState
		if raw, err := c.Call("update.status", nil); err == nil {
			_ = json.Unmarshal(raw, &upd)
		}
		if upd.Latest != nil && !upd.Skipped {
			switch upd.Phase {
			case "ready":
				add("item", "installupdate", "Restart to install LocalSites "+upd.Latest.Version)
				lines = append(lines, "sep")
			case "available", "downloading":
				add("item", "updates", "LocalSites "+upd.Latest.Version+" is available…")
				lines = append(lines, "sep")
			}
		}
		if n := len(approvals); n > 0 {
			add("item", "show", fmt.Sprintf("⚠︎ %d agent request%s waiting for approval…", n, map[bool]string{true: "", false: "s"}[n == 1]))
			lines = append(lines, "sep")
		}
		add("header", "", fmt.Sprintf("%d project%s running", len(running), map[bool]string{true: "", false: "s"}[len(running) == 1]))
		for i, r := range running {
			if i >= 15 {
				add("disabled", "", fmt.Sprintf("and %d more…", len(running)-15))
				break
			}
			f, d, _ := strings.Cut(r, "\x00")
			add("item", "open:"+f, "Open "+d)
		}
		for _, s := range shares {
			add("item", "web:"+s.URL, "Shared: "+s.Folder+" ↗")
		}
	}
	lines = append(lines, "sep")
	add("item", "show", "Open LocalSites")
	add("item", "restart", "Restart services")
	lines = append(lines, "sep")
	add("item", "quit", "Quit LocalSites")
	cs := C.CString(strings.Join(lines, "\n"))
	defer C.free(unsafe.Pointer(cs))
	C.lsTraySetMenu(cs)
}

func (a *App) trayAction(id string) {
	switch {
	case id == "show":
		runtime.WindowShow(a.ctx)
		runtime.WindowUnminimise(a.ctx)
	case id == "updates":
		runtime.WindowShow(a.ctx)
		a.emit("menu", "whatsnew")
	case id == "installupdate":
		if err := a.InstallUpdate(); err != nil {
			runtime.WindowShow(a.ctx)
			a.emit("menu", "whatsnew")
		}
	case id == "restart":
		if c, err := a.conn(); err == nil {
			_, _ = c.Call("restart", nil)
		}
	case id == "quit":
		runtime.Quit(a.ctx)
	case strings.HasPrefix(id, "open:"):
		if c, err := a.conn(); err == nil {
			_, _ = c.Call("open", map[string]any{"folder": strings.TrimPrefix(id, "open:"), "app": "browser"})
		}
	case strings.HasPrefix(id, "web:"):
		_ = a.OpenURL(strings.TrimPrefix(id, "web:"))
	}
	a.refreshTray()
}
