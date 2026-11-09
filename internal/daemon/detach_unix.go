//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detach runs the service in its own session so closing the app or terminal doesn't stop sites.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
