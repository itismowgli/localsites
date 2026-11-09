// LocalSites desktop app (Wails: native window using the OS web view; no bundled browser).
// The same binary also runs the background service: `LocalSites daemon`.
package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/localsites/localsites/internal/daemon"
	"github.com/localsites/localsites/internal/mcp"
	"github.com/localsites/localsites/internal/paths"
	"github.com/localsites/localsites/internal/problems"
	"github.com/localsites/localsites/internal/version"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	useNativeNotifications()
	// update-selftest: an update checks the new app runs on this Mac before installing it
	if len(os.Args) > 1 && os.Args[1] == "update-selftest" {
		fmt.Println(version.Version)
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "update-apply" {
		if err := daemon.ApplyUpdate(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		if err := daemon.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// The app binary is also an MCP server for AI agents: LocalSites mcp [--allow-secrets]
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		self, _ := os.Executable()
		c, err := daemon.Ensure(self)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		srv := &mcp.Server{API: c.As("mcp"), AllowSecrets: len(os.Args) > 2 && os.Args[2] == "--allow-secrets", Version: version.Version}
		if err := srv.Serve(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// a fatal Go crash of the app is written to a file; the service turns it into a problem
	problems.CaptureCrashes(filepath.Join(paths.Default().Data, "problems"), "app")
	app := &App{}
	err := wails.Run(&options.App{
		Title:            "LocalSites",
		Width:            1240,
		Height:           780,
		MinWidth:         460, // a third of a 1440 px screen, so tiling tools (Rectangle, macOS tiling) can place it anywhere
		MinHeight:        420,
		BackgroundColour: &options.RGBA{R: 11, G: 11, B: 12, A: 255}, // same as the UI's --win; the UI updates it for light mode
		AssetServer:      &assetserver.Options{Assets: assets},
		// App, Edit, View and Window menus. ⌘W / ⌘T / ⌘[ stay with the app's tabs (the Window menu only has ⌘M and Zoom)
		Menu:       appMenu(app),
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		// after an update installed at night, LocalSites comes back in the menu bar only
		StartHidden: len(os.Args) > 1 && os.Args[len(os.Args)-1] == "--hidden",
		// Closing the window keeps LocalSites in the menu bar; Quit (⌘Q or the menu bar) ends it
		HideWindowOnClose: true,
		Bind:              []interface{}{app},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true, // while resizing, newly exposed areas show the window colour instead of white
			About:                &mac.AboutInfo{Title: "LocalSites", Message: "Local PHP & MySQL sites with real HTTPS domains."},
		},
		Windows: &windows.Options{Theme: windows.SystemDefault},
		Linux:   &linux.Options{ProgramName: "LocalSites"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func appMenu(app *App) *menu.Menu {
	view := menu.NewMenu()
	view.AddText("Toggle Sidebar", keys.CmdOrCtrl("\\"), func(*menu.CallbackData) { app.emit("menu", "togglesidebar") })
	view.AddSeparator()
	view.AddText("Enter Full Screen", keys.Combo("f", keys.CmdOrCtrlKey, keys.ControlKey), func(*menu.CallbackData) { app.ToggleFullscreen() })
	m := menu.NewMenuFromItems(menu.AppMenu(), menu.EditMenu())
	m.AddSubmenu("View").Merge(view)
	m.Append(menu.WindowMenu())
	return m
}
