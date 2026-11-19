// Package notify shows macOS notifications (best effort; never blocks, never in sandboxed test runs).
package notify

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// AppFile is where the app writes its executable's path when it starts. macOS shows LocalSites' name
// and icon only on notifications sent from inside LocalSites.app, so a service started from the
// command line (bin/localsites) asks the app's binary to send them. Set by the service.
var AppFile string

// AppExe returns the LocalSites app's executable from AppFile, when it still exists and is the real app.
func AppExe() string {
	if AppFile != "" {
		if b, err := os.ReadFile(AppFile); err == nil {
			if exe := realApp(strings.TrimSpace(string(b))); exe != "" {
				return exe
			}
		}
	}
	// the app hasn't started since the service did: use the installed one, so the notification still has
	// LocalSites' icon instead of Script Editor's
	for _, exe := range installedApps() {
		if realApp(exe) != "" {
			return exe
		}
	}
	return ""
}

// installedApps: where an installed LocalSites app's executable may be (replaced in tests).
var installedApps = func() []string {
	home, _ := os.UserHomeDir()
	return []string{"/Applications/LocalSites.app/Contents/MacOS/LocalSites", filepath.Join(home, "Applications", "LocalSites.app", "Contents", "MacOS", "LocalSites")}
}

// realApp returns exe when it is the LocalSites app's executable.
func realApp(exe string) string {
	if !strings.HasSuffix(exe, ".app/Contents/MacOS/LocalSites") {
		return ""
	}
	if st, err := os.Stat(exe); err != nil || st.Mode()&0o111 == 0 {
		return ""
	}
	plist, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(exe)), "Info.plist"))
	if err != nil || !bytes.Contains(plist, []byte("com.wails.localsites")) {
		return ""
	}
	return exe
}

// Send shows a notification without buttons.
func Send(title, text string) { SendNotice(title, text, "", "") }

// SendNotice shows a notification. kind and ref (an approval or share id) let the macOS app add buttons
// (Approve / Deny, Extend / Stop, Open logs). Replaceable: the app sends natively; tests stub it.
var SendNotice = func(title, text, kind, ref string) {
	if os.Getenv("LOCALSITES_HOME") != "" || testing.Testing() { // never from test runs
		return
	}
	if runtime.GOOS == "linux" { // freedesktop notifications (GNOME, KDE, …); arguments, never a shell
		if ns, err := exec.LookPath("notify-send"); err == nil {
			go func() { _ = exec.Command(ns, "--app-name=LocalSites", "--icon=localsites", "--", title, text).Run() }()
		}
		return
	}
	if runtime.GOOS != "darwin" {
		return
	}
	go func() {
		if exe := AppExe(); exe != "" {
			cmd := exec.Command(exe, "notify", title, text, kind, ref)
			if err := cmd.Start(); err == nil {
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				select {
				case err := <-done:
					if err == nil {
						return
					}
				case <-time.After(10 * time.Second):
					_ = cmd.Process.Kill()
				}
			}
		}
		// no app found: macOS shows this one as coming from Script Editor
		q := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
		_ = exec.Command("/usr/bin/osascript", "-e", "display notification "+q(text)+" with title "+q(title)).Run()
	}()
}
