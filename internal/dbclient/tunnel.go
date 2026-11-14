package dbclient

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	addr string // the socket (MySQL) or the folder holding it (PostgreSQL)
	done chan struct{}
}

// tunnelDir is a folder only this user can open. Tunnels end in sockets there, not on a local TCP
// port, which another program on this Mac could take first and so receive the database login.
func tunnelDir() (string, error) {
	d := filepath.Join(os.TempDir(), "localsites-ssh")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	st, err := os.Lstat(d)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		if err == nil && st.IsDir() {
			if os.Chmod(d, 0o700) == nil {
				return d, nil
			}
		}
		return "", errors.New("the SSH tunnel folder isn't private")
	}
	return d, nil
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

// tunnelFor starts (or reuses) ssh -L for a connection. It returns the local socket for MySQL, and for
// PostgreSQL the folder holding ".s.PGSQL.<remotePort>" (how PostgreSQL clients name sockets).
func tunnelFor(c Conn, remotePort int) (string, error) {
	tunnels.Lock()
	defer tunnels.Unlock()
	if t, ok := tunnels.m[c.ID]; ok {
		select {
		case <-t.done: // exited: start a new one
		default:
			return t.addr, nil
		}
	}
	base, err := tunnelDir()
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(c.ID))
	id := hex.EncodeToString(h[:6])
	addr := filepath.Join(base, id+".sock")
	sock := addr
	if c.Driver == "postgres" {
		addr = filepath.Join(base, id)
		if err := os.MkdirAll(addr, 0o700); err != nil {
			return "", err
		}
		sock = filepath.Join(addr, ".s.PGSQL."+strconv.Itoa(remotePort))
	}
	_ = os.Remove(sock)
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	}
	args := []string{"-N", "-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=30", "-o", "ConnectTimeout=10",
		"-o", "StreamLocalBindUnlink=yes", "-o", "StreamLocalBindMask=0177",
		"-L", fmt.Sprintf("%s:%s:%d", sock, host, remotePort)}
	if c.SSHPort != 0 {
		args = append(args, "-p", strconv.Itoa(c.SSHPort))
	}
	args = append(args, "--", c.SSHHost)
	cmd := exec.Command("ssh", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("could not start ssh: %v", err)
	}
	t := &tunnel{cmd: cmd, addr: addr, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(t.done) }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if conn, err := net.DialTimeout("unix", sock, 300*time.Millisecond); err == nil {
			conn.Close()
			break
		}
		select {
		case <-t.done:
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = "ssh exited"
			}
			return "", fmt.Errorf("SSH tunnel to %s failed: %s (keys or ssh-agent are needed; passwords can't be typed here)", c.SSHHost, msg)
		default:
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return "", fmt.Errorf("SSH tunnel to %s timed out", c.SSHHost)
		}
		time.Sleep(150 * time.Millisecond)
	}
	tunnels.m[c.ID] = t
	return addr, nil
}

func closeTunnel(id string) {
	tunnels.Lock()
	defer tunnels.Unlock()
	if t, ok := tunnels.m[id]; ok {
		_ = t.cmd.Process.Kill()
		delete(tunnels.m, id)
	}
}
