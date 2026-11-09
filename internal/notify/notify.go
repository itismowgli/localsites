// Package notify shows macOS notifications (best effort; never blocks, never in sandboxed test runs).
package notify

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Send is replaceable in tests.
var Send = func(title, text string) {
	if runtime.GOOS != "darwin" || os.Getenv("LOCALSITES_HOME") != "" {
		return
	}
	q := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
	go func() {
		_ = exec.Command("/usr/bin/osascript", "-e", "display notification "+q(text)+" with title "+q(title)).Run()
	}()
}
