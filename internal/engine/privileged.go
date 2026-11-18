package engine

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	goruntime "runtime"
	"strings"
)

// The few system changes LocalSites makes ask for the computer's password once, in the system's own
// dialog: macOS's administrator prompt, or polkit (pkexec) on Linux. The script is fixed text; paths and
// values are passed as separate arguments ($1, $2, …), never pasted into it.

// adminCommand is the command that runs script as root with args, showing prompt where the system allows.
func adminCommand(goos, prompt, script string, args ...string) (string, []string) {
	if goos == "darwin" {
		// argv = script, args…, prompt; the shell gets the script and the args, each quoted
		as := `on run argv
	set n to count of argv
	set cmd to "/bin/sh -c " & quoted form of (item 1 of argv) & " sh"
	repeat with i from 2 to (n - 1)
		set cmd to cmd & " " & quoted form of (item i of argv)
	end repeat
	do shell script cmd with prompt (item n of argv) with administrator privileges
end run`
		return "/usr/bin/osascript", append(append([]string{"-e", as, script}, args...), prompt)
	}
	// pkexec shows the desktop's password dialog (polkit). It needs a graphical session.
	return "pkexec", append([]string{"/bin/sh", "-c", script, "sh"}, args...)
}

// runAdmin runs script as root (replaced in tests). The output is returned for checks such as "TRUSTED".
var runAdmin = func(ctx context.Context, prompt, script string, args ...string) (string, error) {
	name, argv := adminCommand(goruntime.GOOS, prompt, script, args...)
	out, err := exec.CommandContext(ctx, name, argv...).CombinedOutput()
	if err == nil {
		return string(out), nil
	}
	text := strings.TrimSpace(string(out))
	switch {
	case strings.Contains(text, "-128"), strings.Contains(text, "Request dismissed"), strings.Contains(text, "Not authorized"):
		return text, errors.New("the password prompt was cancelled")
	case goruntime.GOOS == "linux" && (errors.Is(err, exec.ErrNotFound) || strings.Contains(text, "No session for cookie") || strings.Contains(text, "no authentication agent")):
		return text, errors.New("this computer can't show a password prompt here (no polkit agent). Run it in a terminal instead: sudo sh -c " + shellQuote(script) + " sh " + strings.Join(quoteAll(args), " "))
	}
	return text, fmt.Errorf("%s", text)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = shellQuote(a)
	}
	return out
}
