// Package proc supervises long-running child processes (Apache, PHP-FPM, MySQL).
// Children run in the foreground, log to files, and are restarted with backoff if they crash.
package proc

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Spec struct {
	Name string
	Cmd  string
	Args []string
	Env  []string
	Log  string // stdout+stderr file
	// PreStart runs before every launch, including restarts after a crash (e.g. remove stale sockets).
	PreStart func()
	Dir      string // working directory
	OneShot  bool   // a job (e.g. npm install): never restarted
	// Reload, if set, is sent to the running process instead of restarting on Ensure with a changed config.
	Reload syscall.Signal
}

type child struct {
	spec     Spec
	cmd      *exec.Cmd
	done     chan struct{}
	stopping bool
	started  time.Time
	restarts int
	lastErr  string
}

type Supervisor struct {
	mu       sync.Mutex
	children map[string]*child
	OnChange func() // called (in its own goroutine) when a process starts, stops or crashes
}

func (s *Supervisor) changed() {
	if s.OnChange != nil {
		go s.OnChange()
	}
}

func New() *Supervisor { return &Supervisor{children: map[string]*child{}} }

// Status of one process.
type Status struct {
	Name     string    `json:"name"`
	Running  bool      `json:"running"`
	PID      int       `json:"pid"`
	Since    time.Time `json:"since"`
	Restarts int       `json:"restarts"`
	LastErr  string    `json:"last_error,omitempty"`
}

// Start runs spec if not already running.
func (s *Supervisor) Start(spec Spec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.children[spec.Name]; ok && c.running() {
		return nil
	}
	c := &child{spec: spec}
	s.children[spec.Name] = c
	return s.launch(c)
}

func (s *Supervisor) launch(c *child) error {
	if c.spec.PreStart != nil {
		c.spec.PreStart()
	}
	logf, err := os.OpenFile(c.spec.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(c.spec.Cmd, c.spec.Args...)
	cmd.Dir = c.spec.Dir
	cmd.Env = append(os.Environ(), c.spec.Env...)
	cmd.Stdout, cmd.Stderr = logf, logf
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		logf.Close()
		c.lastErr = err.Error()
		return fmt.Errorf("%s: %w", c.spec.Name, err)
	}
	c.cmd, c.done, c.started, c.stopping = cmd, make(chan struct{}), time.Now(), false
	go s.wait(c, logf)
	s.changed()
	return nil
}

func (s *Supervisor) wait(c *child, logf *os.File) {
	err := c.cmd.Wait()
	// The main process is gone: end anything it left behind in its process group (PHP-FPM workers,
	// Apache children), so no orphan keeps running and holding memory.
	killGroup(c.cmd.Process.Pid)
	logf.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.changed()
	close(c.done)
	if c.stopping || s.children[c.spec.Name] != c {
		return
	}
	if err != nil {
		c.lastErr = err.Error()
	} else {
		c.lastErr = "exited"
	}
	if c.spec.OneShot {
		if err == nil {
			c.lastErr = "finished"
		}
		killGroup(c.cmd.Process.Pid)
		return
	}
	killGroup(c.cmd.Process.Pid) // the crashed process's own children (e.g. PHP workers) would hold its sockets
	// Crashed: restart with backoff; give up after 5 quick failures
	if time.Since(c.started) > time.Minute {
		c.restarts = 0
	}
	c.restarts++
	if c.restarts > 5 {
		c.lastErr += " (gave up after repeated crashes; see log)"
		return
	}
	delay := time.Duration(c.restarts) * time.Second
	go func() {
		time.Sleep(delay)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.children[c.spec.Name] == c && !c.stopping && !c.running() {
			_ = s.launch(c)
		}
	}()
}

func (c *child) running() bool {
	if c.cmd == nil || c.done == nil {
		return false
	}
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// Signal sends sig to a running process (e.g. SIGUSR2 = reload for php-fpm).
func (s *Supervisor) Signal(name string, sig syscall.Signal) error {
	s.mu.Lock()
	c, ok := s.children[name]
	if !ok || !c.running() {
		s.mu.Unlock()
		return fmt.Errorf("%s is not running", name)
	}
	restart, err := sendSignal(c, sig)
	spec := c.spec
	s.mu.Unlock()
	if restart {
		s.Stop(name, 10*time.Second)
		return s.Start(spec)
	}
	return err
}

// Stop terminates a process and waits up to timeout before killing it.
func (s *Supervisor) Stop(name string, timeout time.Duration) {
	s.mu.Lock()
	c, ok := s.children[name]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.children, name)
	if !c.running() {
		s.mu.Unlock()
		return
	}
	c.stopping = true
	terminate(c)
	done := c.done
	s.mu.Unlock()
	select {
	case <-done:
	case <-time.After(timeout):
		killGroup(c.cmd.Process.Pid)
		<-done
	}
}

// StopAll stops every process.
func (s *Supervisor) StopAll(timeout time.Duration) {
	for _, n := range s.Names() {
		s.Stop(n, timeout)
	}
}

// Spec returns the spec a process was started with.
func (s *Supervisor) Spec(name string) (Spec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.children[name]
	if !ok {
		return Spec{}, false
	}
	return c.spec, true
}

func (s *Supervisor) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n []string
	for k := range s.children {
		n = append(n, k)
	}
	return n
}

func (s *Supervisor) Status(name string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Name: name}
	if c, ok := s.children[name]; ok {
		st.Running = c.running()
		if st.Running {
			st.PID, st.Since = c.cmd.Process.Pid, c.started
		}
		st.Restarts, st.LastErr = c.restarts, c.lastErr
	}
	return st
}
