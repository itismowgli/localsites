// Package notify shows macOS notifications (best effort; never blocks, never in sandboxed test runs).
package notify

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AppFile is where the app writes its executable's path when it starts. macOS shows LocalSites' name
// and icon only on notifications sent from inside LocalSites.app, so a service started from the
// command line (bin/localsites) asks the app's binary to send them. Set by the service.
var AppFile string

// AppExe returns the LocalSites app's executable from AppFile, when it still exists and is the real app.
func AppExe() string {
	if AppFile == "" {
		return ""
	}
	b, err := os.ReadFile(AppFile)
	if err != nil {
		return ""
	}
	exe := strings.TrimSpace(string(b))
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

// Send is replaceable (the app sends natively; tests stub it).
var Send = func(title, text string) {
	if runtime.GOOS != "darwin" || os.Getenv("LOCALSITES_HOME") != "" {
		return
	}
	go func() {
		if exe := AppExe(); exe != "" {
			cmd := exec.Command(exe, "notify", title, text)
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
