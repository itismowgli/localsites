package apps

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeSystem points the finder at temporary folders for one OS.
func fakeSystem(t *testing.T, goos string) (macDir, binDir, appDir string) {
	t.Helper()
	root := t.TempDir()
	macDir, binDir, appDir = filepath.Join(root, "Applications"), filepath.Join(root, "bin"), filepath.Join(root, "share", "applications")
	for _, d := range []string{macDir, binDir, appDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldOS, oldDirs, oldBins, oldApps := OS, Dirs, BinDirs, AppDirs
	OS = goos
	Dirs = func() []string { return []string{macDir} }
	BinDirs = func() []string { return []string{"", "relative", binDir} }
	AppDirs = func() []string { return []string{appDir} }
	t.Cleanup(func() { OS, Dirs, BinDirs, AppDirs = oldOS, oldDirs, oldBins, oldApps })
	return
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func ids(list []App, kind string) []string {
	out := []string{}
	for _, a := range list {
		if kind == "" || a.Kind == kind {
			out = append(out, a.ID)
		}
	}
	return out
}

func TestMacFindsBundles(t *testing.T) {
	mac, _, _ := fakeSystem(t, "darwin")
	for _, n := range []string{"Cursor.app", "PhpStorm.app", "Alacritty.app"} {
		if err := os.Mkdir(filepath.Join(mac, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := Installed()
	if e := ids(got, "editor"); !reflect.DeepEqual(e, []string{"cursor", "phpstorm"}) {
		t.Fatalf("editors = %v", e)
	}
	c, _ := Find("cursor")
	name, args, _, err := Command(c, "/Users/me/Sites/shop")
	if err != nil || name != "/usr/bin/open" || !reflect.DeepEqual(args, []string{"-a", filepath.Join(mac, "Cursor.app"), "/Users/me/Sites/shop"}) {
		t.Fatalf("cursor command = %s %v %v", name, args, err)
	}
	al, _ := Find("alacritty")
	_, args, _, _ = Command(al, "/p")
	if !reflect.DeepEqual(args, []string{"-na", filepath.Join(mac, "Alacritty.app"), "--args", "--working-directory", "/p"}) {
		t.Fatalf("alacritty args = %v", args)
	}
	if _, err := Find("vscode"); err == nil || err.Error() != "VS Code is not installed" {
		t.Fatalf("missing app error = %v", err)
	}
	if _, err := Find("konsole"); err == nil || err.Error() != "Konsole isn't available on this system" {
		t.Fatalf("Linux-only app on Mac = %v", err)
	}
}

func TestLinuxFindsCommandsAndDesktopFiles(t *testing.T) {
	_, bin, apps := fakeSystem(t, "linux")
	write(t, filepath.Join(bin, "code"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(bin, "subl"), "not executable", 0o644) // ignored: not a program
	write(t, filepath.Join(bin, "flatpak"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(bin, "konsole"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(apps, "dev.zed.Zed.desktop"), `[Desktop Entry]
Name=Zed
Name[de]=Zed Editor
Type=Application
Exec=flatpak run --branch=stable --command=zed dev.zed.Zed --file-forwarding @@ %U @@
Icon=dev.zed.Zed
[Desktop Action NewWindow]
Exec=flatpak run dev.zed.Zed --new
`, 0o644)
	write(t, filepath.Join(apps, "jetbrains-phpstorm.desktop"), "[Desktop Entry]\nType=Application\nTryExec=/nowhere/phpstorm.sh\nExec=/nowhere/phpstorm.sh %f\n", 0o644)

	got := Installed()
	if e := ids(got, "editor"); !reflect.DeepEqual(e, []string{"vscode", "zed"}) {
		t.Fatalf("editors = %v (PhpStorm's TryExec program is missing, subl isn't executable)", e)
	}
	z, _ := Find("zed")
	name, args, _, _ := Command(z, "/home/me/Sites/shop")
	want := []string{"run", "--branch=stable", "--command=zed", "dev.zed.Zed", "--file-forwarding", "@@", "/home/me/Sites/shop", "@@"}
	if name != filepath.Join(bin, "flatpak") || !reflect.DeepEqual(args, want) {
		t.Fatalf("zed = %s %v", name, args)
	}
	k, _ := Find("konsole")
	name, args, dir, _ := Command(k, "/home/me/Sites/shop")
	if name != filepath.Join(bin, "konsole") || !reflect.DeepEqual(args, []string{"--workdir", "/home/me/Sites/shop"}) || dir != "/home/me/Sites/shop" {
		t.Fatalf("konsole = %s %v in %s", name, args, dir)
	}
	if _, err := Find("nova"); err == nil || err.Error() != "Nova isn't available on this system" {
		t.Fatalf("Mac-only app on Linux = %v", err)
	}
}

func TestReadDesktop(t *testing.T) {
	_, bin, apps := fakeSystem(t, "linux")
	write(t, filepath.Join(bin, "subl"), "#!/bin/sh\n", 0o755)
	cases := []struct {
		body string
		want []string
	}{
		{"[Desktop Entry]\nExec=subl --new-window %F\n", []string{filepath.Join(bin, "subl"), "--new-window", "{}"}},
		{"[Desktop Entry]\nExec=subl\n", []string{filepath.Join(bin, "subl"), "{}"}},                      // no field code: target goes last
		{"[Desktop Entry]\nExec=\"/opt/My Editor/run\" %u %i %c\n", []string{"/opt/My Editor/run", "{}"}}, // quoted path with a space
		{"[Desktop Entry]\nExec=subl --title=\"100%% \\\"done\\\"\" %f\n", []string{filepath.Join(bin, "subl"), "--title=100% \"done\"", "{}"}},
		{"[Desktop Entry]\nExec=subl\\s--wait %f\n", []string{filepath.Join(bin, "subl"), "--wait", "{}"}}, // \s is a space
	}
	for i, c := range cases {
		p := filepath.Join(apps, "t.desktop")
		write(t, p, c.body, 0o644)
		got, ok := readDesktop(p)
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d: got %q ok=%v, want %q", i, got, ok, c.want)
		}
	}
	for i, body := range []string{
		"[Desktop Entry]\nType=Link\nExec=subl\n",
		"[Desktop Entry]\nHidden=true\nExec=subl\n",
		"[Desktop Entry]\nExec=missing-program %f\n",
		"[Desktop Entry]\nExec=subl \"unclosed\n",
		"[Other]\nExec=subl\n",
		"[Desktop Entry]\nExec=./subl %f\n",
	} {
		p := filepath.Join(apps, "bad.desktop")
		write(t, p, body, 0o644)
		if got, ok := readDesktop(p); ok {
			t.Errorf("bad case %d accepted: %q", i, got)
		}
	}
}

func TestTerminalsStartInTheFolder(t *testing.T) {
	_, bin, _ := fakeSystem(t, "linux")
	for _, b := range []string{"gnome-terminal", "kitty", "wezterm", "warp-terminal"} {
		write(t, filepath.Join(bin, b), "#!/bin/sh\n", 0o755)
	}
	cases := map[string][]string{
		"gnome-terminal": {"--working-directory=/p"},
		"kitty":          {"--directory", "/p"},
		"wezterm":        {"start", "--cwd", "/p"},
		"warp":           {}, // no option: it starts in its working folder
	}
	for id, want := range cases {
		a, err := Find(id)
		if err != nil {
			t.Fatal(err)
		}
		_, args, dir, _ := Command(a, "/p")
		if !reflect.DeepEqual(args, want) || dir != "/p" {
			t.Errorf("%s: args %q dir %q, want %q", id, args, dir, want)
		}
	}
}

func TestUnsupportedOS(t *testing.T) {
	fakeSystem(t, "windows")
	if got := Installed(); len(got) != 0 {
		t.Fatalf("found %v", got)
	}
	if _, _, _, err := Command(App{Name: "X"}, "/p"); err == nil {
		t.Fatal("expected an error")
	}
}
