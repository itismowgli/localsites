// Package apps finds the Mac apps people use with their projects (editors, terminals, git and database
// apps, browsers) so the UI can offer "Open in …" for the ones actually installed.
package apps

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// App is one known application.
type App struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Kind  string   `json:"kind"` // editor | terminal | git | database | browser | files
	Names []string `json:"-"`    // bundle names to look for (first match wins)
	Path  string   `json:"path,omitempty"`
}

// Catalog lists the apps LocalSites knows, in display order per kind.
var Catalog = []App{
	{ID: "finder", Name: "Finder", Kind: "files", Names: []string{"/System/Library/CoreServices/Finder.app"}},
	{ID: "vscode", Name: "VS Code", Kind: "editor", Names: []string{"Visual Studio Code.app"}},
	{ID: "cursor", Name: "Cursor", Kind: "editor", Names: []string{"Cursor.app"}},
	{ID: "zed", Name: "Zed", Kind: "editor", Names: []string{"Zed.app"}},
	{ID: "windsurf", Name: "Windsurf", Kind: "editor", Names: []string{"Windsurf.app"}},
	{ID: "phpstorm", Name: "PhpStorm", Kind: "editor", Names: []string{"PhpStorm.app"}},
	{ID: "webstorm", Name: "WebStorm", Kind: "editor", Names: []string{"WebStorm.app"}},
	{ID: "sublime", Name: "Sublime Text", Kind: "editor", Names: []string{"Sublime Text.app"}},
	{ID: "nova", Name: "Nova", Kind: "editor", Names: []string{"Nova.app"}},
	{ID: "terminal", Name: "Terminal", Kind: "terminal", Names: []string{"/System/Applications/Utilities/Terminal.app", "Utilities/Terminal.app"}},
	{ID: "iterm", Name: "iTerm2", Kind: "terminal", Names: []string{"iTerm.app"}},
	{ID: "warp", Name: "Warp", Kind: "terminal", Names: []string{"Warp.app"}},
	{ID: "ghostty", Name: "Ghostty", Kind: "terminal", Names: []string{"Ghostty.app"}},
	{ID: "github", Name: "GitHub Desktop", Kind: "git", Names: []string{"GitHub Desktop.app"}},
	{ID: "fork", Name: "Fork", Kind: "git", Names: []string{"Fork.app"}},
	{ID: "tower", Name: "Tower", Kind: "git", Names: []string{"Tower.app"}},
	{ID: "sourcetree", Name: "Sourcetree", Kind: "git", Names: []string{"Sourcetree.app"}},
	{ID: "tableplus", Name: "TablePlus", Kind: "database", Names: []string{"TablePlus.app"}},
	{ID: "sequelace", Name: "Sequel Ace", Kind: "database", Names: []string{"Sequel Ace.app"}},
	{ID: "safari", Name: "Safari", Kind: "browser", Names: []string{"/Applications/Safari.app"}},
	{ID: "chrome", Name: "Google Chrome", Kind: "browser", Names: []string{"Google Chrome.app"}},
	{ID: "firefox", Name: "Firefox", Kind: "browser", Names: []string{"Firefox.app"}},
	{ID: "arc", Name: "Arc", Kind: "browser", Names: []string{"Arc.app"}},
	{ID: "brave", Name: "Brave", Kind: "browser", Names: []string{"Brave Browser.app"}},
	{ID: "edge", Name: "Microsoft Edge", Kind: "browser", Names: []string{"Microsoft Edge.app"}},
}

// Dirs are where apps are looked for (tests replace them).
var Dirs = func() []string {
	home, _ := os.UserHomeDir()
	return []string{"/Applications", filepath.Join(home, "Applications"), "/Applications/Setapp"}
}

func locate(a App) string {
	for _, n := range a.Names {
		if filepath.IsAbs(n) {
			if _, err := os.Stat(n); err == nil {
				return n
			}
			continue
		}
		for _, d := range Dirs() {
			if p := filepath.Join(d, n); exists(p) {
				return p
			}
		}
	}
	return ""
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Installed returns the apps found on this Mac.
func Installed() []App {
	if runtime.GOOS != "darwin" {
		return nil
	}
	var out []App
	for _, a := range Catalog {
		if p := locate(a); p != "" {
			a.Path = p
			out = append(out, a)
		}
	}
	return out
}

// Find returns an installed app by id.
func Find(id string) (App, error) {
	for _, a := range Catalog {
		if a.ID == id {
			if p := locate(a); p != "" {
				a.Path = p
				return a, nil
			}
			return a, errors.New(a.Name + " is not installed")
		}
	}
	return App{}, errors.New("unknown app")
}

// Open opens target (a folder or a URL) with the app. Arguments go to /usr/bin/open, never through a shell.
func Open(a App, target string) error {
	return exec.Command("/usr/bin/open", "-a", a.Path, target).Run()
}
