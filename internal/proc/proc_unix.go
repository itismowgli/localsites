//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
)

// Reload signals: php-fpm reloads on USR2, Apache restarts gracefully on USR1.
var (
	SigReloadPHP    = syscall.SIGUSR2
	SigReloadApache = syscall.SIGUSR1
	SigReloadNginx  = syscall.SIGHUP
)

// ownGroup puts the child in its own process group so terminal signals don't reach it.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killGroup force-kills the child and anything it spawned.
func killGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }

// sendSignal delivers sig; restart is never needed on unix.
func sendSignal(c *child, sig syscall.Signal) (restart bool, err error) {
	return false, c.cmd.Process.Signal(sig)
}

// terminate asks the whole process group to stop (npm starts node, which starts workers, ...).
func terminate(c *child) { _ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM) }
