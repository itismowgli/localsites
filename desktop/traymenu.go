package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// trayMenu builds the menu-bar (macOS) or tray (Linux) menu as lines "kind\tid\ttitle" or "sep", and the
// number of things that need the person.
func (a *App) trayMenu() (lines []string, badge int) {
	lines = []string{}
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
		var probs struct {
			Problems []struct {
				Kind string `json:"kind"`
				Sent string `json:"sent"`
			} `json:"problems"`
		}
		if raw, err := c.Call("problems.list", nil); err == nil {
			_ = json.Unmarshal(raw, &probs)
		}
		var att struct {
			Access int `json:"waiting_for_access"`
			Checks int `json:"doctor_fails"`
			Sites  int `json:"site_problems"`
		}
		if raw, err := c.Call("attention", nil); err == nil {
			_ = json.Unmarshal(raw, &att)
		}
		n := needs{Access: att.Access, Checks: att.Checks, Sites: att.Sites, Approvals: len(approvals), Update: updateWaiting(upd)}
		for _, p := range probs.Problems { // the same rule as the app's Problems count
			if p.Sent == "" && p.Kind != "ui" {
				n.Problems++
			}
		}
		badge = n.total()
		if n.total() > 0 {
			add("item", "show", fmt.Sprintf("%d %s: %s", n.total(), map[bool]string{true: "thing needs you", false: "things need you"}[n.total() == 1], n.summary()))
			lines = append(lines, "sep")
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
	return lines, badge
}

// trayAction runs a menu item ("show", "open:<folder>", "web:<url>", …).
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
