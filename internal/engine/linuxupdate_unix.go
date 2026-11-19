//go:build unix

package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// Updates on Linux: the downloaded package (checked against the signed manifest) is installed by the
// system's package manager after one password prompt. The service stops right after this returns; a small
// helper waits for it and the app to quit, then starts the new version.

const linuxInstallScript = `set -e
case "$2" in
  deb) apt-get install -y -q --allow-downgrades "$1" || dpkg -i "$1" ;;
  rpm) dnf install -y -q "$1" 2>/dev/null || rpm -U --replacepkgs "$1" ;;
  *) echo "unknown package type $2" >&2; exit 2 ;;
esac`

func (e *Engine) installLinuxPackage(appExe string, appPID int, then string) error {
	u := e.updater()
	file, ver, err := u.PackageFile()
	if err != nil {
		return err
	}
	format := filepath.Ext(file)[1:]
	if _, err := e.backupConfig(u.Current); err != nil {
		u.Installed(ver, err)
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if out, err := runAdmin(ctx, "LocalSites needs your password to install version "+ver+".", linuxInstallScript, file, format); err != nil {
		u.Installed(ver, fmt.Errorf("%v %s", err, out))
		return err
	}
	u.Installed(ver, nil)
	// start the new version once this service and the app have quit
	start := "/opt/LocalSites/LocalSites"
	args := ""
	switch then {
	case "open-bg":
		args = " --hidden"
	case "service":
		args = " daemon"
	}
	if _, err := os.Stat(start); err != nil {
		start = appExe
	}
	script := "while kill -0 " + strconv.Itoa(appPID) + " 2>/dev/null || kill -0 " + strconv.Itoa(os.Getpid()) + " 2>/dev/null; do sleep 0.3; done; exec \"$0\"" + args
	cmd := exec.Command("/bin/sh", "-c", script, start)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives this service
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
