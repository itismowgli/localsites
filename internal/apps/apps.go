// Package apps finds the apps people use with their projects (editors, terminals, git and database apps,
// browsers) on macOS and Linux, so the UI can offer "Open in …" for the ones actually installed.
package apps

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// App is one known application.
type App struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // editor | terminal | git | database | browser | files
	Path string `json:"path,omitempty"`

	Mac     []string `json:"-"` // macOS: bundle names or absolute paths (first match wins)
	MacArgs []string `json:"-"` // macOS: arguments after --args instead of passing the target ({} = target)
	Bins    []string `json:"-"` // Linux: commands looked for on PATH and in the usual install folders
	Desktop []string `json:"-"` // Linux: .desktop file ids (Flatpak, Snap, JetBrains Toolbox…)
	DirArg  []string `json:"-"` // Linux terminals: how to start in a folder ({} = folder); nil = folder as argument

	exec []string // Linux: the command line found (binary or .desktop Exec line), {} = target
}

// Catalog lists the apps LocalSites knows, in display order per kind.
var Catalog = []App{
	{ID: "finder", Name: "Finder", Kind: "files", Mac: []string{"/System/Library/CoreServices/Finder.app"}},
	{ID: "files", Name: "Files", Kind: "files", Bins: []string{"xdg-open"}},

	{ID: "vscode", Name: "VS Code", Kind: "editor", Mac: []string{"Visual Studio Code.app"}, Bins: []string{"code"}, Desktop: []string{"code.desktop", "com.visualstudio.code.desktop", "code_code.desktop"}},
	{ID: "vscodium", Name: "VSCodium", Kind: "editor", Mac: []string{"VSCodium.app"}, Bins: []string{"codium"}, Desktop: []string{"codium.desktop", "com.vscodium.codium.desktop", "codium_codium.desktop"}},
	{ID: "cursor", Name: "Cursor", Kind: "editor", Mac: []string{"Cursor.app"}, Bins: []string{"cursor"}, Desktop: []string{"cursor.desktop", "co.anysphere.cursor.desktop"}},
	{ID: "windsurf", Name: "Windsurf", Kind: "editor", Mac: []string{"Windsurf.app"}, Bins: []string{"windsurf"}, Desktop: []string{"windsurf.desktop"}},
	{ID: "zed", Name: "Zed", Kind: "editor", Mac: []string{"Zed.app"}, Bins: []string{"zed", "zeditor"}, Desktop: []string{"dev.zed.Zed.desktop", "zed.desktop"}},
	{ID: "phpstorm", Name: "PhpStorm", Kind: "editor", Mac: []string{"PhpStorm.app"}, Bins: []string{"phpstorm", "phpstorm.sh"}, Desktop: []string{"jetbrains-phpstorm.desktop", "phpstorm_phpstorm.desktop", "com.jetbrains.PhpStorm.desktop"}},
	{ID: "webstorm", Name: "WebStorm", Kind: "editor", Mac: []string{"WebStorm.app"}, Bins: []string{"webstorm", "webstorm.sh"}, Desktop: []string{"jetbrains-webstorm.desktop", "webstorm_webstorm.desktop", "com.jetbrains.WebStorm.desktop"}},
	{ID: "idea", Name: "IntelliJ IDEA", Kind: "editor", Mac: []string{"IntelliJ IDEA.app", "IntelliJ IDEA Ultimate.app", "IntelliJ IDEA CE.app"}, Bins: []string{"idea", "idea.sh"}, Desktop: []string{"jetbrains-idea.desktop", "jetbrains-idea-ce.desktop", "intellij-idea-ultimate_intellij-idea-ultimate.desktop", "com.jetbrains.IntelliJ-IDEA-Ultimate.desktop"}},
	{ID: "sublime", Name: "Sublime Text", Kind: "editor", Mac: []string{"Sublime Text.app"}, Bins: []string{"subl"}, Desktop: []string{"sublime_text.desktop", "com.sublimetext.three.desktop"}},
	{ID: "nova", Name: "Nova", Kind: "editor", Mac: []string{"Nova.app"}},
	{ID: "bbedit", Name: "BBEdit", Kind: "editor", Mac: []string{"BBEdit.app"}},
	{ID: "kiro", Name: "Kiro", Kind: "editor", Mac: []string{"Kiro.app"}, Bins: []string{"kiro"}, Desktop: []string{"kiro.desktop"}},
	{ID: "antigravity", Name: "Antigravity", Kind: "editor", Mac: []string{"Antigravity.app"}, Bins: []string{"antigravity"}, Desktop: []string{"antigravity.desktop"}},
	{ID: "rune", Name: "Rune", Kind: "editor", Mac: []string{"Rune.app"}, Bins: []string{"rune"}, Desktop: []string{"rune.desktop"}},

	{ID: "terminal", Name: "Terminal", Kind: "terminal", Mac: []string{"/System/Applications/Utilities/Terminal.app", "Utilities/Terminal.app"}},
	{ID: "iterm", Name: "iTerm2", Kind: "terminal", Mac: []string{"iTerm.app"}},
	{ID: "gnome-terminal", Name: "GNOME Terminal", Kind: "terminal", Bins: []string{"gnome-terminal"}, DirArg: []string{"--working-directory={}"}},
	{ID: "ptyxis", Name: "Ptyxis", Kind: "terminal", Bins: []string{"ptyxis"}, Desktop: []string{"org.gnome.Ptyxis.desktop"}, DirArg: []string{"--new-window", "--working-directory={}"}},
	{ID: "konsole", Name: "Konsole", Kind: "terminal", Bins: []string{"konsole"}, DirArg: []string{"--workdir", "{}"}},
	{ID: "xfce-terminal", Name: "Xfce Terminal", Kind: "terminal", Bins: []string{"xfce4-terminal"}, DirArg: []string{"--working-directory={}"}},
	{ID: "tilix", Name: "Tilix", Kind: "terminal", Bins: []string{"tilix"}, DirArg: []string{"--working-directory={}"}},
	{ID: "warp", Name: "Warp", Kind: "terminal", Mac: []string{"Warp.app"}, Bins: []string{"warp-terminal"}, DirArg: []string{}},
	{ID: "ghostty", Name: "Ghostty", Kind: "terminal", Mac: []string{"Ghostty.app"}, Bins: []string{"ghostty"}, Desktop: []string{"com.mitchellh.ghostty.desktop"}, DirArg: []string{"--working-directory={}"}},
	{ID: "kitty", Name: "kitty", Kind: "terminal", Mac: []string{"kitty.app"}, Bins: []string{"kitty"}, DirArg: []string{"--directory", "{}"}},
	{ID: "alacritty", Name: "Alacritty", Kind: "terminal", Mac: []string{"Alacritty.app"}, MacArgs: []string{"--working-directory", "{}"}, Bins: []string{"alacritty"}, DirArg: []string{"--working-directory", "{}"}},
	{ID: "wezterm", Name: "WezTerm", Kind: "terminal", Mac: []string{"WezTerm.app"}, MacArgs: []string{"start", "--cwd", "{}"}, Bins: []string{"wezterm"}, DirArg: []string{"start", "--cwd", "{}"}},

	{ID: "github", Name: "GitHub Desktop", Kind: "git", Mac: []string{"GitHub Desktop.app"}, Bins: []string{"github-desktop"}, Desktop: []string{"github-desktop.desktop", "io.github.shiftey.Desktop.desktop"}},
	{ID: "fork", Name: "Fork", Kind: "git", Mac: []string{"Fork.app"}},
	{ID: "tower", Name: "Tower", Kind: "git", Mac: []string{"Tower.app"}},
	{ID: "sourcetree", Name: "Sourcetree", Kind: "git", Mac: []string{"Sourcetree.app"}},
	{ID: "gitkraken", Name: "GitKraken", Kind: "git", Mac: []string{"GitKraken.app"}, Bins: []string{"gitkraken"}, Desktop: []string{"gitkraken.desktop", "com.axosoft.GitKraken.desktop"}},
	{ID: "smerge", Name: "Sublime Merge", Kind: "git", Mac: []string{"Sublime Merge.app"}, Bins: []string{"smerge"}, Desktop: []string{"sublime_merge.desktop"}},

	{ID: "tableplus", Name: "TablePlus", Kind: "database", Mac: []string{"TablePlus.app"}},
	{ID: "sequelace", Name: "Sequel Ace", Kind: "database", Mac: []string{"Sequel Ace.app"}},

	{ID: "safari", Name: "Safari", Kind: "browser", Mac: []string{"/Applications/Safari.app"}},
	{ID: "chrome", Name: "Google Chrome", Kind: "browser", Mac: []string{"Google Chrome.app"}, Bins: []string{"google-chrome", "google-chrome-stable"}, Desktop: []string{"google-chrome.desktop", "com.google.Chrome.desktop"}},
	{ID: "chromium", Name: "Chromium", Kind: "browser", Bins: []string{"chromium", "chromium-browser"}, Desktop: []string{"chromium.desktop", "chromium_chromium.desktop", "org.chromium.Chromium.desktop"}},
	{ID: "firefox", Name: "Firefox", Kind: "browser", Mac: []string{"Firefox.app"}, Bins: []string{"firefox"}, Desktop: []string{"firefox.desktop", "firefox_firefox.desktop", "org.mozilla.firefox.desktop"}},
	{ID: "arc", Name: "Arc", Kind: "browser", Mac: []string{"Arc.app"}},
	{ID: "brave", Name: "Brave", Kind: "browser", Mac: []string{"Brave Browser.app"}, Bins: []string{"brave-browser", "brave"}, Desktop: []string{"brave-browser.desktop", "com.brave.Browser.desktop"}},
	{ID: "edge", Name: "Microsoft Edge", Kind: "browser", Mac: []string{"Microsoft Edge.app"}, Bins: []string{"microsoft-edge", "microsoft-edge-stable"}, Desktop: []string{"microsoft-edge.desktop", "com.microsoft.Edge.desktop"}},
}

// DefaultKinds can have a default app, chosen by the person (never guessed).
var DefaultKinds = []string{"editor", "terminal"}

// IsDefaultKind reports whether kind can have a default app.
func IsDefaultKind(kind string) bool { return kind == "editor" || kind == "terminal" }

// OS is the platform apps are looked up for (tests change it).
var OS = runtime.GOOS

// Dirs are where macOS apps are looked for (tests replace them).
var Dirs = func() []string {
	home, _ := os.UserHomeDir()
	return []string{"/Applications", filepath.Join(home, "Applications"), "/Applications/Setapp"}
}

// BinDirs are where Linux commands are looked for. The background service may have been started without
// the desktop's PATH, so the usual install places are listed after PATH.
var BinDirs = func() []string {
	home, _ := os.UserHomeDir()
	dirs := filepath.SplitList(os.Getenv("PATH"))
	return append(dirs, filepath.Join(home, ".local", "bin"), "/usr/local/bin", "/usr/bin", "/bin", "/snap/bin",
		filepath.Join(home, ".local", "share", "JetBrains", "Toolbox", "scripts"),
		filepath.Join(home, ".local", "share", "flatpak", "exports", "bin"), "/var/lib/flatpak/exports/bin")
}

// AppDirs are the freedesktop folders that hold .desktop files, most specific first.
var AppDirs = func() []string {
	home, _ := os.UserHomeDir()
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	dirs := []string{filepath.Join(data, "applications"),
		filepath.Join(home, ".local", "share", "flatpak", "exports", "share", "applications"),
		"/var/lib/flatpak/exports/share/applications", "/var/lib/snapd/desktop/applications"}
	sys := os.Getenv("XDG_DATA_DIRS")
	if sys == "" {
		sys = "/usr/local/share:/usr/share"
	}
	for _, d := range filepath.SplitList(sys) {
		dirs = append(dirs, filepath.Join(d, "applications"))
	}
	return dirs
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// locate fills Path (and the Linux command line). It returns false when the app isn't installed.
func locate(a *App) bool {
	switch OS {
	case "darwin":
		for _, n := range a.Mac {
			if filepath.IsAbs(n) {
				if exists(n) {
					a.Path = n
					return true
				}
				continue
			}
			for _, d := range Dirs() {
				if p := filepath.Join(d, n); exists(p) {
					a.Path = p
					return true
				}
			}
		}
	case "linux":
		for _, b := range a.Bins {
			for _, d := range BinDirs() {
				if d == "" || !filepath.IsAbs(d) { // a relative PATH entry would depend on the current folder
					continue
				}
				if p := filepath.Join(d, b); isExec(p) {
					a.Path, a.exec = p, []string{p, "{}"}
					return true
				}
			}
		}
		for _, id := range a.Desktop {
			for _, d := range AppDirs() {
				p := filepath.Join(d, id)
				if cmd, ok := readDesktop(p); ok {
					a.Path, a.exec = p, cmd
					return true
				}
			}
		}
	}
	return false
}

// Installed returns the apps found on this computer.
func Installed() []App {
	out := []App{}
	for _, a := range Catalog {
		if locate(&a) {
			out = append(out, a)
		}
	}
	return out
}

// Find returns an installed app by id.
func Find(id string) (App, error) {
	for _, a := range Catalog {
		if a.ID != id {
			continue
		}
		if locate(&a) {
			return a, nil
		}
		if (OS == "darwin" && len(a.Mac) == 0) || (OS == "linux" && len(a.Bins)+len(a.Desktop) == 0) {
			return a, errors.New(a.Name + " isn't available on this system")
		}
		return a, errors.New(a.Name + " is not installed")
	}
	return App{}, errors.New("unknown app " + id)
}

// Command is the program and arguments that open target (a folder or a URL) in the app. Nothing goes
// through a shell. Dir is the folder the program starts in (terminals start in the project).
func Command(a App, target string) (name string, args []string, dir string, err error) {
	switch OS {
	case "darwin":
		if a.Path == "" {
			return "", nil, "", errors.New(a.Name + " is not installed")
		}
		if len(a.MacArgs) > 0 { // apps that ignore a folder handed over by macOS take it as an option
			return "/usr/bin/open", append([]string{"-na", a.Path, "--args"}, fill(a.MacArgs, target)...), "", nil
		}
		return "/usr/bin/open", []string{"-a", a.Path, target}, "", nil
	case "linux":
		if len(a.exec) == 0 {
			return "", nil, "", errors.New(a.Name + " is not installed")
		}
		line := a.exec
		if a.Kind == "terminal" {
			dir = target
			if a.DirArg != nil { // the terminal's own "start in this folder" option replaces the target
				line = append(dropTarget(line), a.DirArg...)
			}
		}
		line = fill(line, target)
		return line[0], line[1:], dir, nil
	}
	return "", nil, "", errors.New("opening apps isn't supported on this system yet")
}

// Open opens target with the app and doesn't wait for the app to quit.
func Open(a App, target string) error {
	name, args, dir, err := Command(a, target)
	if err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if OS == "darwin" {
		return cmd.Run() // open returns as soon as the app has the request
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // the app keeps running; only collect its exit status
	return nil
}

func fill(line []string, target string) []string {
	out := make([]string, len(line))
	for i, s := range line {
		out[i] = strings.ReplaceAll(s, "{}", target)
	}
	return out
}

// dropTarget removes the arguments that would pass the target, keeping the program and its fixed options.
func dropTarget(line []string) []string {
	out := []string{}
	for _, s := range line {
		if !strings.Contains(s, "{}") {
			out = append(out, s)
		}
	}
	return out
}
