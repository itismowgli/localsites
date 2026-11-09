package problems

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// CaptureCrashes makes the Go runtime write a fatal crash (panic, fatal error) of this process to
// dir/crash-<who>.txt. A trace left by the previous run is kept as a .pending file for Collect.
// Call early in main. who is "app" or "service".
func CaptureCrashes(dir, who string) {
	_ = os.MkdirAll(dir, 0o700)
	f := filepath.Join(dir, "crash-"+who+".txt")
	if st, err := os.Stat(f); err == nil && st.Size() > 0 {
		_ = os.Rename(f, filepath.Join(dir, fmt.Sprintf("crash-%s-%d.pending", who, time.Now().UnixNano())))
	}
	out, err := os.OpenFile(f, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_ = debug.SetCrashOutput(out, debug.CrashOptions{})
	_ = out.Close() // the runtime keeps its own copy of the file
}

// Collect turns crash traces left by earlier runs, and new macOS crash reports for LocalSites, into
// problems. Cheap when there is nothing: one directory listing each.
func (s *Store) Collect(home string) {
	pending, _ := filepath.Glob(filepath.Join(s.dir, "crash-*.pending"))
	for _, f := range pending {
		b, err := os.ReadFile(f)
		_ = os.Remove(f)
		if err != nil || len(b) == 0 {
			continue
		}
		who := strings.SplitN(strings.TrimPrefix(filepath.Base(f), "crash-"), "-", 2)[0]
		s.Add(KindCrash, crashTitle(who, string(b)), string(b))
	}
	s.collectMacReports(home)
}

// crashTitle is "The service crashed: panic: runtime error: index out of range".
func crashTitle(who, trace string) string {
	name := map[string]string{"app": "The app", "service": "The background service"}[who]
	if name == "" {
		name = "LocalSites"
	}
	for _, l := range strings.Split(trace, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "panic:") || strings.HasPrefix(l, "fatal error:") {
			return name + " crashed: " + l
		}
	}
	return name + " crashed"
}

// collectMacReports reads macOS crash reports (~/Library/Logs/DiagnosticReports/LocalSites*.ips) not seen yet.
func (s *Store) collectMacReports(home string) {
	if home == "" {
		return
	}
	seenFile := filepath.Join(s.dir, "mac-reports-seen.json")
	seen := map[string]bool{}
	if b, err := os.ReadFile(seenFile); err == nil {
		_ = json.Unmarshal(b, &seen)
	}
	dir := filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	var files []string
	for _, pat := range []string{"LocalSites*.ips", "localsites*.ips"} {
		m, _ := filepath.Glob(filepath.Join(dir, pat))
		files = append(files, m...)
	}
	first := len(seen) == 0
	changed := false
	for _, f := range files {
		name := filepath.Base(f)
		if seen[name] {
			continue
		}
		seen[name], changed = true, true
		st, err := os.Stat(f)
		// the first time, only reports from the last week (older ones were before this feature)
		if err != nil || (first && time.Since(st.ModTime()) > 7*24*time.Hour) {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		s.Add(KindCrash, macTitle(string(b)), string(b))
	}
	if changed {
		b, _ := json.Marshal(seen)
		_ = os.WriteFile(seenFile, b, 0o600)
	}
}

// macTitle reads the exception from a .ips crash report: "LocalSites crashed: EXC_BAD_ACCESS (SIGSEGV)".
func macTitle(ips string) string {
	head, body, _ := strings.Cut(ips, "\n")
	var h struct {
		App string `json:"app_name"`
	}
	_ = json.Unmarshal([]byte(head), &h)
	var b struct {
		Exception struct {
			Type   string `json:"type"`
			Signal string `json:"signal"`
		} `json:"exception"`
	}
	_ = json.Unmarshal([]byte(body), &b)
	app := h.App
	if app == "" {
		app = "LocalSites"
	}
	t := app + " crashed (macOS crash report)"
	if b.Exception.Type != "" {
		t = fmt.Sprintf("%s crashed: %s (%s)", app, b.Exception.Type, b.Exception.Signal)
	}
	return t
}
