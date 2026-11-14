package engine

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/proc"
	"github.com/localsites/localsites/internal/runtime"
)

// Dev-server projects (Next.js, Node, any custom command) run as supervised processes on
// 127.0.0.1:<port>; the front door gives them their https://name.test address.

const appPortStart = 3100

func appProc(id string) string     { return "app-" + config.Slug(id) }
func installProc(id string) string { return "install-" + config.Slug(id) }

// assignPorts gives every enabled dev-server project a fixed port (saved, so URLs and caches stay stable).
// Ports saved for other projects, even switched-off ones, are never reused.
func (e *Engine) assignPorts(sites []SiteView) {
	used := map[int]bool{}
	for _, s := range sites {
		if s.Port != 0 {
			used[s.Port] = true
		}
	}
	_ = e.Store.Update(func(c *config.Config) error {
		for _, s := range sites {
			if !s.Enabled || s.Runtime != "app" || s.Port != 0 || s.Type == KindDocker {
				continue
			}
			p := appPortStart
			for used[p] || !portFree(p) {
				p++
			}
			used[p] = true
			cp := s.Site
			cp.Port = p
			c.Sites[s.Folder] = &cp
		}
		return nil
	})
}

// movePort saves and returns a new free port for a project.
func (e *Engine) movePort(s SiteView) int {
	taken := map[int]bool{}
	for _, c := range e.Store.Get().Sites {
		taken[c.Port] = true
	}
	p := appPortStart
	for taken[p] || !portFree(p) {
		p++
	}
	_ = e.Store.Update(func(c *config.Config) error {
		cp := s.Site
		cp.Port = p
		c.Sites[s.Folder] = &cp
		return nil
	})
	return p
}

func portFree(p int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// appSpec builds the command that runs a project's dev server.
func (e *Engine) appSpec(s SiteView) (proc.Spec, error) {
	port := strconv.Itoa(s.Port)
	path := strings.Join(append(e.nodeBins(s), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"), string(os.PathListSeparator))
	env := []string{"PATH=" + path, "PORT=" + port, "HOST=127.0.0.1", "HOSTNAME=127.0.0.1", "BROWSER=none", "FORCE_COLOR=0"}
	for _, k := range sortedKeys(s.EnvVars) { // the project's own variables (validated when saved)
		env = append(env, k+"="+s.EnvVars[k])
	}
	if s.Type == KindDocker { // Docker CLI needs its config and socket from the user's environment
		env = append(env, "HOME="+e.P.Home)
		if v := os.Getenv("DOCKER_HOST"); v != "" {
			env = append(env, "DOCKER_HOST="+v)
		}
	}
	spec := proc.Spec{Name: appProc(s.Folder), Dir: s.Path, Env: env, Log: filepath.Join(e.P.SiteLogs, fileKey(s.Folder)+"-app.log")}
	switch s.Type {
	case "command", KindDocker:
		if s.Command == "" {
			return spec, errors.New("no command set")
		}
		line := strings.ReplaceAll(s.Command, "{port}", port)
		if goruntime.GOOS == "windows" {
			spec.Cmd, spec.Args = "cmd", []string{"/C", line}
		} else {
			spec.Cmd, spec.Args = "/bin/sh", []string{"-c", "exec " + line}
		}
	case KindNext, KindNode:
		pm := s.Framework.PM
		if pm == "" {
			pm = "npm"
		}
		bin, err := lookIn(pm, path)
		if err != nil {
			return spec, fmt.Errorf("%s is not installed (needed to run %s)", pm, s.Name)
		}
		script := "dev"
		if !hasScript(readJSON(filepath.Join(s.Path, "package.json")), "dev") {
			script = "start"
		}
		args := []string{"run", script}
		if s.Type == KindNext { // listen on this computer only, on our port
			extra := []string{"--port", port, "--hostname", "127.0.0.1"}
			if pm == "npm" {
				args = append(args, "--")
			}
			args = append(args, extra...)
		}
		spec.Cmd, spec.Args = bin, args
	default:
		return spec, fmt.Errorf("%s projects are not run as a dev server", s.Type)
	}
	return spec, nil
}

var nodeVersionRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,2}$`)

// nodeBins puts the project's Node version first when it pins one (.nvmrc) and nvm has it.
func (e *Engine) nodeBins(s SiteView) []string {
	want := s.Framework.Node
	if want == "" || !nodeVersionRe.MatchString(want) { // from the project's .nvmrc: a version, nothing else
		return nil
	}
	dirs, _ := filepath.Glob(filepath.Join(e.P.Home, ".nvm", "versions", "node", "v"+want+"*"))
	sort.Strings(dirs)
	if len(dirs) == 0 {
		return nil
	}
	return []string{filepath.Join(dirs[len(dirs)-1], "bin")}
}

func lookIn(name, pathList string) (string, error) {
	for _, d := range filepath.SplitList(pathList) {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// applyApps starts, restarts (when the command changed) and stops dev-server projects.
func (e *Engine) applyApps(sites []SiteView, add func(string, ...any)) {
	want := map[string]bool{}
	for _, s := range sites {
		if !s.Enabled || s.Runtime != "app" || s.ServedBy != "" {
			continue
		}
		if s.Type != KindDocker && !e.Sup.Status(appProc(s.Folder)).Running && !portFree(s.Port) {
			// Another program holds this project's port (e.g. a dev server started by hand): move to a free one
			old := s.Port
			s.Port = e.movePort(s)
			add("Port %d was taken by another program; %s now uses port %d", old, s.Name, s.Port)
		}
		spec, err := e.appSpec(s)
		if err != nil {
			add("%s: %v", s.Name, err)
			continue
		}
		want[spec.Name] = true
		if cur, ok := e.Sup.Spec(spec.Name); ok && e.Sup.Status(spec.Name).Running {
			if sameSpec(cur, spec) {
				continue
			}
			e.Sup.Stop(spec.Name, 10*time.Second)
		}
		if err := e.Sup.Start(spec); err != nil {
			add("%s failed to start: %v", s.Name, err)
			continue
		}
		add("Started %s on port %d", s.Name, s.Port)
	}
	if len(WaitingForAccess()) > 0 {
		return // a projects folder can't be read right now: don't stop dev servers that may live there
	}
	for _, n := range e.Sup.Names() {
		if strings.HasPrefix(n, "app-") && !want[n] {
			e.Sup.Stop(n, 10*time.Second)
			add("Stopped %s", strings.TrimPrefix(n, "app-"))
		}
	}
}

func sameSpec(a, b proc.Spec) bool {
	return a.Cmd == b.Cmd && a.Dir == b.Dir && strings.Join(a.Args, "\x00") == strings.Join(b.Args, "\x00") && strings.Join(a.Env, "\x00") == strings.Join(b.Env, "\x00")
}

// waitPorts waits until dev servers accept connections (they may still compile on first request).
func waitPorts(ports []int, timeout time.Duration) []int {
	deadline := time.Now().Add(timeout)
	var slow []int
	for _, p := range ports {
		for {
			c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 300*time.Millisecond)
			if err == nil {
				c.Close()
				break
			}
			if time.Now().After(deadline) {
				slow = append(slow, p)
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	return slow
}

// InstallDeps installs a project's dependencies (composer for PHP, npm/pnpm/yarn/bun for Node) in the
// background. Progress goes to the project's install log.
func (e *Engine) InstallDeps(folder string) (string, error) {
	s, err := e.site(folder)
	if err != nil {
		return "", err
	}
	name := installProc(s.Folder)
	if e.Sup.Status(name).Running {
		return "", errors.New("already installing")
	}
	path := strings.Join(append(e.nodeBins(s), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"), string(os.PathListSeparator))
	spec := proc.Spec{Name: name, Dir: s.Path, OneShot: true, Env: []string{"PATH=" + path, "COMPOSER_NO_INTERACTION=1", "CI=1", "FORCE_COLOR=0"},
		Log: filepath.Join(e.P.SiteLogs, fileKey(s.Folder)+"-install.log")}
	switch {
	case exists(filepath.Join(s.Path, "package.json")) && (s.Runtime == "app" || !exists(filepath.Join(s.Path, "composer.json"))):
		pm := s.Framework.PM
		if pm == "" {
			pm = packageManager(s.Path)
		}
		bin, err := lookIn(pm, path)
		if err != nil {
			return "", fmt.Errorf("%s is not installed", pm)
		}
		spec.Cmd, spec.Args = bin, []string{"install"}
	case exists(filepath.Join(s.Path, "composer.json")):
		composer, err := lookIn("composer", path)
		if err != nil {
			return "", errors.New("Composer is not installed (brew install composer)")
		}
		php := "php"
		if v, ok := runtime.FindPHP(s.PHP); ok { // run Composer with the project's PHP version
			php = v.CLI
		}
		spec.Cmd, spec.Args = php, []string{composer, "install", "--no-interaction", "--no-progress"}
	default:
		return "", errors.New("nothing to install: no package.json or composer.json")
	}
	_ = os.Remove(spec.Log)
	if err := e.Sup.Start(spec); err != nil {
		return "", err
	}
	return spec.Log, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
