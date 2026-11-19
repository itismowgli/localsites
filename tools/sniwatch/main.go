// sniwatch stands in for a desktop's tray (a StatusNotifierWatcher) in tests: it waits for an app to
// register a tray icon, then prints the icon's menu labels (com.canonical.dbusmenu). Linux only.
//
//	sniwatch [seconds]
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

type watcher struct{ items chan string }

func (w *watcher) RegisterStatusNotifierItem(sender dbus.Sender, service string) *dbus.Error {
	if !strings.HasPrefix(service, "/") {
		w.items <- service
	} else {
		w.items <- string(sender) + service
	}
	return nil
}

func (w *watcher) RegisterStatusNotifierHost(string) *dbus.Error { return nil }

func main() {
	wait := 30
	if len(os.Args) > 1 {
		wait, _ = strconv.Atoi(os.Args[1])
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		fmt.Println("no session bus:", err)
		os.Exit(1)
	}
	w := &watcher{items: make(chan string, 4)}
	const path, iface = "/StatusNotifierWatcher", "org.kde.StatusNotifierWatcher"
	_ = conn.Export(w, path, iface)
	_, _ = prop.Export(conn, path, prop.Map{iface: {
		"IsStatusNotifierHostRegistered": {Value: true},
		"RegisteredStatusNotifierItems":  {Value: []string{}},
		"ProtocolVersion":                {Value: int32(0)},
	}})
	if reply, err := conn.RequestName(iface, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		fmt.Println("couldn't own", iface)
		os.Exit(1)
	}
	fmt.Println("watcher ready")
	select {
	case item := <-w.items:
		fmt.Println("registered:", item)
		dest, objPath := item, dbus.ObjectPath("/StatusNotifierItem")
		if i := strings.Index(item, "/"); i > 0 {
			dest, objPath = item[:i], dbus.ObjectPath(item[i:])
		}
		time.Sleep(2 * time.Second) // let the app build its menu
		obj := conn.Object(dest, objPath)
		v, err := obj.GetProperty("org.kde.StatusNotifierItem.Menu")
		if err != nil {
			fmt.Println("no menu:", err)
			os.Exit(1)
		}
		menu := conn.Object(dest, v.Value().(dbus.ObjectPath))
		var rev uint32
		var layout struct {
			ID       int32
			Props    map[string]dbus.Variant
			Children []dbus.Variant
		}
		if err := menu.Call("com.canonical.dbusmenu.GetLayout", 0, int32(0), int32(-1), []string{"label", "type", "enabled"}).Store(&rev, &layout); err != nil {
			fmt.Println("GetLayout:", err)
			os.Exit(1)
		}
		for _, c := range layout.Children {
			child := c.Value().([]interface{})
			props := child[1].(map[string]dbus.Variant)
			label, kind, enabled := "", "", true
			if l, ok := props["label"]; ok {
				label = l.Value().(string)
			}
			if t, ok := props["type"]; ok {
				kind = t.Value().(string)
			}
			if e, ok := props["enabled"]; ok {
				enabled = e.Value().(bool)
			}
			switch {
			case kind == "separator":
				fmt.Println("  ────")
			case !enabled:
				fmt.Println("  (" + label + ")")
			default:
				fmt.Println("  " + label)
			}
		}
	case <-time.After(time.Duration(wait) * time.Second):
		fmt.Println("no tray icon registered within", wait, "seconds")
		os.Exit(1)
	}
}
