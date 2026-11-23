package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Uptime: whether each switched-on project was being served, hour by hour, for the last day. The service
// samples once a minute (see SampleUptime); a project that is switched off isn't sampled, so "off" is never
// counted as "down". It lives in one small file and is only read by the app.

const uptimeHours = 24

// UptimeHour is one hour of one project: how many minutes it was checked, and how many of those it was down.
type UptimeHour struct {
	Hour time.Time `json:"hour"`
	Up   int       `json:"up"`
	Down int       `json:"down"`
}

type uptimeStore struct {
	mu     sync.Mutex
	loaded bool
	hours  map[string][]UptimeHour // project folder -> hours, oldest first
}

func (e *Engine) uptimeFile() string { return filepath.Join(e.P.Data, "uptime.json") }

func (e *Engine) uptimeLoad() {
	if e.uptime.loaded {
		return
	}
	e.uptime.loaded = true
	e.uptime.hours = map[string][]UptimeHour{}
	if b, err := os.ReadFile(e.uptimeFile()); err == nil {
		_ = json.Unmarshal(b, &e.uptime.hours)
	}
}

// served reports whether a switched-on project can answer right now: the front door, its web server and
// PHP version (PHP projects), or its dev server (Node, Next.js, custom commands).
func (e *Engine) served(s SiteView) bool {
	if !e.Front.Running() {
		return false
	}
	switch s.Runtime {
	case "app":
		return e.Sup.Status(appProc(s.Folder)).Running
	case "php":
		web := procApache
		if s.Server == "nginx" {
			web = procNginx
		}
		if !e.Sup.Status(web).Running {
			return false
		}
		return s.Type == KindStatic || e.Sup.Status("php-"+s.PHP).Running
	}
	return true
}

// SampleUptime records one minute for every switched-on project and forgets hours older than a day.
func (e *Engine) SampleUptime(now time.Time) {
	up := map[string]bool{}
	for _, s := range e.Sites() {
		if s.Enabled && s.ServedBy == "" && s.Runtime != "none" {
			up[s.Folder] = e.served(s)
		}
	}
	e.recordUptime(now, up)
}

// recordUptime adds one minute per project (folder: served or not) to the current hour and saves.
func (e *Engine) recordUptime(now time.Time, up map[string]bool) {
	hour := now.Truncate(time.Hour)
	e.uptime.mu.Lock()
	defer e.uptime.mu.Unlock()
	e.uptimeLoad()
	for folder, ok := range up {
		list := e.uptime.hours[folder]
		if n := len(list); n == 0 || !list[n-1].Hour.Equal(hour) {
			list = append(list, UptimeHour{Hour: hour})
		}
		if ok {
			list[len(list)-1].Up++
		} else {
			list[len(list)-1].Down++
		}
		e.uptime.hours[folder] = list
	}
	cut := hour.Add(-(uptimeHours - 1) * time.Hour)
	for k, list := range e.uptime.hours {
		i := sort.Search(len(list), func(i int) bool { return !list[i].Hour.Before(cut) })
		if list = list[i:]; len(list) == 0 {
			delete(e.uptime.hours, k)
		} else {
			e.uptime.hours[k] = list
		}
	}
	if b, err := json.Marshal(e.uptime.hours); err == nil {
		_ = os.WriteFile(e.uptimeFile(), b, 0o600)
	}
}

// Uptime is the last day of samples, by project folder.
func (e *Engine) Uptime() map[string][]UptimeHour {
	e.uptime.mu.Lock()
	defer e.uptime.mu.Unlock()
	e.uptimeLoad()
	out := make(map[string][]UptimeHour, len(e.uptime.hours))
	for k, v := range e.uptime.hours {
		out[k] = append([]UptimeHour(nil), v...)
	}
	return out
}
