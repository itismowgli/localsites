package frontdoor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Valet drives Laravel Valet's `proxy` feature. It only removes proxies it created itself
// (tracked in stateFile), and can take over proxies made by the LocalSites prototype.
type Valet struct {
	Bin       string
	Home      string
	StateFile string
	TLD       string
}

type valetState struct {
	Proxies map[string]string `json:"proxies"` // label -> backend
}

func (v *Valet) Name() string { return "valet" }

func (v *Valet) load() valetState {
	s := valetState{Proxies: map[string]string{}}
	if b, err := os.ReadFile(v.StateFile); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.Proxies == nil {
		s.Proxies = map[string]string{}
	}
	return s
}

func (v *Valet) save(s valetState) {
	b, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(v.StateFile, b, 0o600)
}

func (v *Valet) nginxFile(domain string) string {
	return filepath.Join(v.Home, ".config", "valet", "Nginx", domain)
}

func (v *Valet) Running() bool {
	return exec.Command("/usr/bin/pgrep", "-f", "nginx: master").Run() == nil
}

// prototypeProxies are proxies created by the earlier ~/.localsites prototype (safe to take over).
func (v *Valet) prototypeProxies() map[string]bool {
	m := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(v.Home, ".localsites", "run", "state.json"))
	if err != nil {
		return m
	}
	var s valetState
	if json.Unmarshal(b, &s) == nil {
		for k := range s.Proxies {
			m[k] = true
		}
	}
	return m
}

func (v *Valet) Conflict(domain string) string {
	label := strings.TrimSuffix(domain, "."+v.TLD)
	if _, ours := v.load().Proxies[label]; ours || v.prototypeProxies()[label] {
		return ""
	}
	var cfg struct {
		Paths []string `json:"paths"`
	}
	if b, err := os.ReadFile(filepath.Join(v.Home, ".config", "valet", "config.json")); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	for _, p := range cfg.Paths {
		if _, err := os.Stat(filepath.Join(p, label)); err == nil {
			return domain + " is already a Valet site (" + filepath.Join(p, label) + ")"
		}
	}
	if _, err := os.Stat(v.nginxFile(domain)); err == nil {
		return domain + " is already configured in Valet"
	}
	return ""
}

func (v *Valet) Sync(routes []Route) ([]string, error) {
	var log []string
	st := v.load()
	want := map[string]string{}
	for _, r := range routes {
		want[strings.TrimSuffix(r.Domain, "."+v.TLD)] = r.Backend
	}
	for label := range st.Proxies {
		if _, keep := want[label]; keep {
			continue
		}
		if out, err := v.run("unproxy", label); err != nil {
			log = append(log, "Could not remove "+label+"."+v.TLD+": "+out)
			continue
		}
		delete(st.Proxies, label)
		log = append(log, "Removed https://"+label+"."+v.TLD)
	}
	for label, backend := range want {
		if st.Proxies[label] == backend && fileExistsF(v.nginxFile(label+"."+v.TLD)) && v.points(label, backend) {
			continue
		}
		if out, err := v.run("proxy", label, backend, "--secure"); err != nil {
			log = append(log, "Could not link "+label+"."+v.TLD+": "+out)
			continue
		}
		st.Proxies[label] = backend
		log = append(log, "Linked https://"+label+"."+v.TLD)
	}
	v.save(st)
	return log, nil
}

// points checks the existing nginx file proxies to backend (it may belong to the prototype).
func (v *Valet) points(label, backend string) bool {
	b, err := os.ReadFile(v.nginxFile(label + "." + v.TLD))
	return err == nil && strings.Contains(string(b), "proxy_pass "+backend+";")
}

func (v *Valet) run(args ...string) (string, error) {
	cmd := exec.Command(v.Bin, args...)
	cmd.Env = append(os.Environ(), "PATH=/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")
	cmd.Stdin = nil
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = cmd.CombinedOutput(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
		_ = cmd.Process.Kill()
		<-done
		return "timed out", os.ErrDeadlineExceeded
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return lines[len(lines)-1], err
}

func fileExistsF(p string) bool { _, err := os.Stat(p); return err == nil }
