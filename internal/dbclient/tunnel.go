package dbclient

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SSH tunnels use the system ssh with the user's own keys/agent (BatchMode: never asks for or stores passwords).

var sshHostRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*(@[A-Za-z0-9_][A-Za-z0-9._-]*)?$`)

type tunnel struct {
	cmd  *exec.Cmd
	port int
	done chan struct{}
}

var tunnels = struct {
	sync.Mutex
	m map[string]*tunnel
}{m: map[string]*tunnel{}}

func validSSH(c Conn) error {
	if c.SSHHost == "" {
		return nil
	}
	if c.Driver == "sqlite" {
		return errors.New("SQLite files are opened locally, not over SSH")
	}
	if !sshHostRe.MatchString(c.SSHHost) {
		return errors.New("SSH host: use user@host or a Host alias from ~/.ssh/config")
	}
	if c.SSHPort != 0 && (c.SSHPort < 1 || c.SSHPort > 65535) {
		return errors.New("SSH port must be between 1 and 65535")
	}
	return nil
}

// tunnelFor starts (or reuses) ssh -L for a connection and returns the local port.
func tunnelFor(c Conn, remotePort int) (int, error) {
	tunnels.Lock()
	defer tunnels.Unlock()
	if t, ok := tunnels.m[c.ID]; ok {
		select {
		case <-t.done: // exited: start a new one
		default:
			return t.port, nil
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	}
	args := []string{"-N", "-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=30", "-o", "ConnectTimeout=10",
		"-L", fmt.Sprintf("127.0.0.1:%d:%s:%d", port, host, remotePort)}
	if c.SSHPort != 0 {
		args = append(args, "-p", strconv.Itoa(c.SSHPort))
	}
	args = append(args, "--", c.SSHHost)
	cmd := exec.Command("ssh", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("could not start ssh: %v", err)
	}
	t := &tunnel{cmd: cmd, port: port, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(t.done) }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond); err == nil {
			conn.Close()
			break
		}
		select {
		case <-t.done:
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = "ssh exited"
			}
			return 0, fmt.Errorf("SSH tunnel to %s failed: %s (keys or ssh-agent are needed; passwords can't be typed here)", c.SSHHost, msg)
		default:
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return 0, fmt.Errorf("SSH tunnel to %s timed out", c.SSHHost)
		}
		time.Sleep(150 * time.Millisecond)
	}
	tunnels.m[c.ID] = t
	return port, nil
}

func closeTunnel(id string) {
	tunnels.Lock()
	defer tunnels.Unlock()
	if t, ok := tunnels.m[id]; ok {
		_ = t.cmd.Process.Kill()
		delete(tunnels.m, id)
	}
}
