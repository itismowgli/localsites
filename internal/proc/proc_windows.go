//go:build windows

package proc

import (
	"os/exec"
	"strconv"
	"syscall"
)

// Windows has no reload signals; the supervisor restarts the process instead.
var (
	SigReloadPHP    = syscall.Signal(-1)
	SigReloadApache = syscall.Signal(-1)
	SigReloadNginx  = syscall.Signal(-1)
)

func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killGroup ends the process tree.
func killGroup(pid int) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}

func sendSignal(c *child, sig syscall.Signal) (restart bool, err error) { return true, nil }

func terminate(c *child) { killGroup(c.cmd.Process.Pid) }
