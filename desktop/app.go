package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/localsites/localsites/internal/api"
	"github.com/localsites/localsites/internal/daemon"
	"github.com/localsites/localsites/internal/paths"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is bound to the web UI. The UI only calls Call(); all logic lives in the background service.
type App struct {
	ctx    context.Context
	mu     sync.Mutex
	client *api.Client
	life   lifecycle
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	installNativeDrag()
	a.startTray()
	askNotificationPermission()
	a.watchWindow()
	go a.nightlyInstall()
}

func (a *App) shutdown(context.Context) { a.installOnQuit() }

func (a *App) conn() (*api.Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		return a.client, nil
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	c, err := daemon.Ensure(self)
	if err != nil {
		return nil, err
	}
	a.client = c.As("app")
	return a.client, nil
}

// Call runs an API method. params is a JSON string; the result is a JSON string.
func (a *App) Call(method, params string) (string, error) {
	c, err := a.conn()
	if err != nil {
		return "", err
	}
	var p any
	if params != "" {
		if err := json.Unmarshal([]byte(params), &p); err != nil {
			return "", errors.New("bad params")
		}
	}
	res, err := c.Call(method, p)
	if isConnErr(err) {
		// Service restarted (gone, or new token): reconnect once
		a.mu.Lock()
		a.client = nil
		a.mu.Unlock()
		if c, err2 := a.conn(); err2 == nil {
			res, err = c.Call(method, p)
		}
	}
	if err != nil {
		return "", err
	}
	return string(res), nil
}

// waitChange blocks until the service reports a change after version since (at most ~25 s). It returns
// the new version and whether to redraw: true after a change, and when the service can't be reached
// (after waiting 5 s), so callers can show that and retry.
func (a *App) waitChange(since uint64) (uint64, bool) {
	c, err := a.conn()
	if err == nil {
		var r struct {
			Version uint64 `json:"version"`
		}
		var raw []byte
		if raw, err = c.Call("events.wait", map[string]any{"since": since}); err == nil && json.Unmarshal(raw, &r) == nil {
			if r.Version == since {
				return since, false
			}
			time.Sleep(150 * time.Millisecond) // changes come in bursts: redraw once
			return r.Version, true
		}
		if isConnErr(err) {
			a.mu.Lock()
			a.client = nil
			a.mu.Unlock()
		}
	}
	time.Sleep(5 * time.Second)
	return since, true
}

// CopyText puts text on the clipboard (used for database credentials).
func (a *App) CopyText(text string) error { return runtime.ClipboardSetText(a.ctx, text) }

func isConnErr(err error) bool {
	// "unauthorized": the service restarted and has a new token, so connect again to read it
	return err != nil && (strings.Contains(err.Error(), "not responding") || strings.Contains(err.Error(), "not running") || strings.Contains(err.Error(), "unauthorized"))
}

// LogUI writes a UI error (with stack) to the LocalSites log for debugging.
func (a *App) LogUI(msg string) {
	if len(msg) > 8000 {
		msg = msg[:8000]
	}
	p := paths.Default()
	f, err := os.OpenFile(filepath.Join(p.Logs, "ui.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), msg)
	// script errors are problems; failed actions (a wrong value, the service restarting) are not
	if strings.HasPrefix(msg, "[ui] error:") || strings.HasPrefix(msg, "[ui] promise:") {
		if c, err := a.conn(); err == nil {
			go func() { _, _ = c.Call("problems.ui", map[string]string{"message": msg}) }()
		}
	}
}

// Executable is the app's own path (shown in Settings for MCP setup).
func (a *App) Executable() string {
	p, _ := os.Executable()
	return p
}

// ChooseFolder opens the native folder picker (Settings → Projects folders).
func (a *App) ChooseFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose a projects folder", CanCreateDirectories: true})
}

// SaveText asks where to save a file (CSV export) and writes it. Returns "" when cancelled.
func (a *App) SaveText(name, content string) (string, error) {
	p, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Export", DefaultFilename: filepath.Base(name), CanCreateDirectories: true,
		Filters: []runtime.FileFilter{{DisplayName: "CSV (*.csv)", Pattern: "*.csv"}}})
	if err != nil || p == "" {
		return "", err
	}
	return p, os.WriteFile(p, []byte(content), 0o644)
}

// ChooseSaveSQL asks where to save a database export. Returns "" when cancelled.
func (a *App) ChooseSaveSQL(name string) (string, error) {
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Export database", DefaultFilename: filepath.Base(name), CanCreateDirectories: true,
		DefaultDirectory: filepath.Join(os.Getenv("HOME"), "Downloads"),
		Filters:          []runtime.FileFilter{{DisplayName: "SQL (*.sql, *.sql.gz)", Pattern: "*.sql;*.gz"}}})
}

// ChooseOpenSQL asks for a .sql, .sql.gz or .zip file to import. Returns "" when cancelled.
func (a *App) ChooseOpenSQL() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "Import SQL file",
		Filters: []runtime.FileFilter{{DisplayName: "SQL dump (*.sql, *.sql.gz, *.zip)", Pattern: "*.sql;*.gz;*.zip;*.dump"}}})
}

// OpenURL opens a web page (a repository on GitHub, …) in the default browser. Only http(s).
func (a *App) OpenURL(u string) error {
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return errors.New("only web addresses can be opened")
	}
	runtime.BrowserOpenURL(a.ctx, u)
	return nil
}

// TitleBarDoubleClick does what macOS does for a double-click on a title bar: zoom (fill the screen)
// or minimize, following System Settings → Desktop & Dock → "Double-click a window's title bar to".
func (a *App) TitleBarDoubleClick() {
	out, _ := exec.Command("defaults", "read", "-g", "AppleActionOnDoubleClick").Output()
	switch strings.TrimSpace(string(out)) {
	case "Minimize":
		runtime.WindowMinimise(a.ctx)
	case "None", "Do Nothing":
	default:
		runtime.WindowToggleMaximise(a.ctx)
	}
}

// ToggleFullscreen enters or leaves macOS full screen (⌃⌘F).
func (a *App) ToggleFullscreen() {
	if runtime.WindowIsFullscreen(a.ctx) {
		runtime.WindowUnfullscreen(a.ctx)
	} else {
		runtime.WindowFullscreen(a.ctx)
	}
}

func (a *App) emit(name string, data ...interface{}) { runtime.EventsEmit(a.ctx, name, data...) }

// SetWindowColour matches the native window background to the UI theme ("#rrggbb"), so resizing
// and zooming never flash a different colour at the edges.
func (a *App) SetWindowColour(hex string) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b); err == nil {
		runtime.WindowSetBackgroundColour(a.ctx, r, g, b, 255)
	}
}
