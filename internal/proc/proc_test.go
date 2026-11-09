//go:build !windows

package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNoOrphansAfterStop(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	s := New()
	// A parent that starts a long-lived child in its own process group, then exits on SIGTERM without it
	// The child ignores SIGTERM (like a busy worker) and so survives a polite stop
	script := "(trap '' TERM; exec sleep 300) & echo $! > " + pidFile + "; trap 'exit 0' TERM; while true; do sleep 1; done"
	if err := s.Start(Spec{Name: "p", Cmd: "/bin/sh", Args: []string{"-c", script}, Log: filepath.Join(dir, "log")}); err != nil {
		t.Fatal(err)
	}
	var pid int
	for i := 0; i < 50 && pid == 0; i++ {
		time.Sleep(50 * time.Millisecond)
		b, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	if pid == 0 {
		t.Fatal("child did not start")
	}
	s.Stop("p", 3*time.Second)
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("child outlived its stopped parent")
	}
}
