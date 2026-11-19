//go:build linux

package main

import (
	_ "embed"
	"strings"
	"sync"

	"fyne.io/systray"
	"github.com/godbus/dbus/v5"
)

// The tray icon on Linux: the same menu as the macOS menu bar, through the StatusNotifierItem protocol
// (KDE, Ubuntu's GNOME with AppIndicator support, Xfce, Cinnamon…). Without a tray on the desktop there is no
// icon, and closing the window quits the app as before.

//go:embed trayicon-linux.png
var trayIconLinux []byte

var (
	trayOnce  sync.Once
	trayShown bool
	trayLast  string // the menu drawn last, so an unchanged menu isn't rebuilt
)

// trayAvailable reports whether the desktop shows tray icons (a StatusNotifierWatcher runs).
func trayAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	var has bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&has); err != nil {
		return false
	}
	return has
}

func (a *App) startTray() {
	trayOnce.Do(func() {
		if !trayAvailable() {
			return
		}
		trayShown = true
		start, _ := systray.RunWithExternalLoop(func() {
			systray.SetIcon(trayIconLinux)
			systray.SetTooltip("LocalSites")
			go func() { // redraw only when the service says something changed (no polling)
				var ver uint64
				a.refreshTray()
				for {
					v, redraw := a.waitChange(ver)
					ver = v
					if redraw {
						a.refreshTray()
					}
				}
			}()
		}, nil)
		start()
	})
}

func (a *App) refreshTray() {
	lines, badge := a.trayMenu()
	key := strings.Join(lines, "\n")
	if key == trayLast {
		return
	}
	trayLast = key
	if badge > 0 {
		systray.SetTooltip("LocalSites: " + plural(badge, "thing needs you", "things need you"))
	} else {
		systray.SetTooltip("LocalSites")
	}
	systray.ResetMenu() // removing an item closes its channel, which ends its listener below
	for _, l := range lines {
		if l == "sep" {
			systray.AddSeparator()
			continue
		}
		parts := strings.SplitN(l, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		kind, id, title := parts[0], parts[1], parts[2]
		mi := systray.AddMenuItem(title, "")
		if kind != "item" {
			mi.Disable()
			continue
		}
		go func() {
			for range mi.ClickedCh {
				a.trayAction(id)
			}
		}()
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
