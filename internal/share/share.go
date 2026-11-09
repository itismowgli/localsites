// Package share puts one local site on the internet for a while, through a free tunnel.
//
// Each share gets its own gateway on 127.0.0.1: it checks the optional password, sends requests to that one
// site (setting the Host header the site expects) and rewrites redirects to the public address. The tunnel
// only ever reaches the gateway, never other sites or services. Shares stop by themselves when they expire.
package share

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Provider is a tunnel service with a free, open-source client and no account needed.
type Provider struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Formula     string `json:"formula"`
	Bin         string `json:"-"`
	License     string `json:"license"`
	Description string `json:"description"`
	HTTPS       bool   `json:"https"`
	// Args builds the command for a local port; URLRe finds the public address in its output.
	Args  func(port int) []string `json:"-"`
	URLRe *regexp.Regexp          `json:"-"`
	// URLFrom turns the regexp match into the public URL (default: the match itself).
	URLFrom func(m []string) string `json:"-"`
}

// Providers, best first. Tests replace Bin with a stand-in.
var Providers = []Provider{
	{ID: "cloudflared", Name: "Cloudflare quick tunnel", Formula: "cloudflared", Bin: "cloudflared", License: "Apache-2.0", HTTPS: true,
		Description: "Free, no account. A random https://….trycloudflare.com address.",
		Args: func(p int) []string {
			return []string{"tunnel", "--no-autoupdate", "--url", fmt.Sprintf("http://127.0.0.1:%d", p)}
		},
		URLRe: regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)},
	{ID: "bore", Name: "bore", Formula: "bore-cli", Bin: "bore", License: "MIT",
		Description: "Free, no account, open source end to end (bore.pub or your own server). Plain http:// address.",
		Args:        func(p int) []string { return []string{"local", fmt.Sprint(p), "--to", "bore.pub"} },
		URLRe:       regexp.MustCompile(`listening at ([a-z0-9.-]+):(\d+)`),
		URLFrom:     func(m []string) string { return "http://" + m[1] + ":" + m[2] }},
}

// FindProvider returns a provider by id.
func FindProvider(id string) (Provider, bool) {
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Binary finds the installed client (Homebrew), or the Bin path itself when it is absolute (tests).
func (p Provider) Binary() (string, bool) {
	cands := []string{p.Bin}
	if !filepath.IsAbs(p.Bin) {
		cands = []string{filepath.Join("/opt/homebrew/bin", p.Bin), filepath.Join("/usr/local/bin", p.Bin)}
	}
	for _, f := range cands {
		if st, err := os.Stat(f); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return f, true
		}
	}
	return "", false
}

// Target is the local site a share forwards to.
type Target struct {
	Domain  string         // name.test, sent as the Host header
	Backend string         // https://127.0.0.1:8744 (Apache) or http://127.0.0.1:3100 (dev server)
	RootCAs *x509.CertPool // trusts the backend certificate (LocalSites CA); nil = skip checks (loopback only)
}

// Share is one running share.
type Share struct {
	ID       string    `json:"id"`
	Folder   string    `json:"folder"`
	Domain   string    `json:"domain"`
	Provider string    `json:"provider"`
	URL      string    `json:"url"`
	Password bool      `json:"password"`
	Started  time.Time `json:"started"`
	Expires  time.Time `json:"expires"`
	Gateway  string    `json:"gateway"`  // 127.0.0.1:port, for diagnostics
	Requests int64     `json:"requests"` // visits so far
	Blocked  int64     `json:"blocked"`  // refused: wrong password, too many tries, hidden files
	stats    *counters

	srv    *http.Server
	cancel context.CancelFunc
	timer  *time.Timer
	done   chan struct{}
}

// Manager keeps the running shares.
type Manager struct {
	mu       sync.Mutex
	shares   map[string]*Share
	LogDir   string
	OnChange func() // a share started or ended (also when it expires)
}

func NewManager(logDir string) *Manager { return &Manager{shares: map[string]*Share{}, LogDir: logDir} }

// List returns running shares, newest first.
func (m *Manager) List() []Share {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Share, 0, len(m.shares))
	for _, s := range m.shares {
		c := *s
		if s.stats != nil {
			c.Requests, c.Blocked = s.stats.requests.Load(), s.stats.blocked.Load()
		}
		out = append(out, c)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Started.After(out[j-1].Started); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Start opens a gateway for the target and a tunnel to it. It returns once the public address is known.
func (m *Manager) Start(folder string, t Target, providerID, password string, d time.Duration) (Share, error) {
	p, ok := FindProvider(providerID)
	if !ok {
		return Share{}, fmt.Errorf("unknown provider %q", providerID)
	}
	bin, ok := p.Binary()
	if !ok {
		return Share{}, fmt.Errorf("%s is not installed (brew install %s)", p.Name, p.Formula)
	}
	if d <= 0 || d > 24*time.Hour {
		return Share{}, errors.New("a share lasts between 1 minute and 24 hours")
	}
	m.mu.Lock()
	for _, s := range m.shares {
		if s.Folder == folder {
			m.mu.Unlock()
			return Share{}, errors.New(folder + " is already shared at " + s.URL)
		}
	}
	m.mu.Unlock()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Share{}, err
	}
	s := &Share{ID: strings.ReplaceAll(folder, "/", "-") + "-" + fmt.Sprint(time.Now().Unix()), Folder: folder, Domain: t.Domain,
		Provider: p.ID, Password: password != "", Started: time.Now(), Expires: time.Now().Add(d), Gateway: ln.Addr().String(), done: make(chan struct{})}
	s.stats = &counters{}
	gw, err := gateway(t, password, p.ID == "cloudflared", func() string { return s.URL }, s.stats)
	if err != nil {
		ln.Close()
		return Share{}, err
	}
	s.srv = &http.Server{Handler: gw, ReadHeaderTimeout: 15 * time.Second}
	go s.srv.Serve(ln)

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	cmd := exec.CommandContext(ctx, bin, p.Args(ln.Addr().(*net.TCPAddr).Port)...)
	logf, _ := os.Create(filepath.Join(m.LogDir, "share-"+s.ID+".log"))
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		cancel()
		s.srv.Close()
		return Share{}, err
	}
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			if logf != nil {
				fmt.Fprintln(logf, line)
			}
			if !sent {
				if mm := p.URLRe.FindStringSubmatch(line); mm != nil {
					u := mm[0]
					if p.URLFrom != nil {
						u = p.URLFrom(mm)
					}
					found <- u
					sent = true
				}
			}
		}
	}()
	go func() { _ = cmd.Wait(); pw.Close(); close(s.done); m.Stop(s.ID) }()

	select {
	case u := <-found:
		s.URL = u
	case <-s.done:
		s.srv.Close()
		return Share{}, fmt.Errorf("%s stopped before giving an address; see the share log", p.Name)
	case <-time.After(45 * time.Second):
		cancel()
		s.srv.Close()
		return Share{}, fmt.Errorf("%s did not give an address within 45 seconds (is this Mac online?)", p.Name)
	}
	s.timer = time.AfterFunc(d, func() { m.Stop(s.ID) })
	m.mu.Lock()
	m.shares[s.ID] = s
	m.mu.Unlock()
	return *s, nil
}

// Stop ends a share: the tunnel closes and the gateway stops answering.
func (m *Manager) Stop(id string) bool {
	m.mu.Lock()
	s, ok := m.shares[id]
	delete(m.shares, id)
	m.mu.Unlock()
	if !ok {
		return false
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.cancel()
	_ = s.srv.Close()
	if m.OnChange != nil {
		go m.OnChange()
	}
	return true
}

// StopAll ends every share (service shutdown).
func (m *Manager) StopAll() {
	for _, s := range m.List() {
		m.Stop(s.ID)
	}
}

// gateway is the HTTP handler in front of one site.
// counters are a share's visit statistics.
type counters struct{ requests, blocked atomic.Int64 }

// failures limits password guessing: at most maxFailures wrong passwords per visitor per failWindow.
type failures struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

const (
	maxFailures      = 10
	maxTotalFailures = 100 // from everyone together, so many addresses can't guess without limit
	failWindow       = 10 * time.Minute
)

func (f *failures) tooMany(who string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.recent(who)) >= maxFailures || len(f.recent("")) >= maxTotalFailures
}

func (f *failures) add(who string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) > 10000 { // many addresses: forget the ones with nothing recent
		for k := range f.seen {
			if len(f.recent(k)) == 0 {
				delete(f.seen, k)
			}
		}
	}
	f.seen[who] = append(f.recent(who), time.Now())
	f.seen[""] = append(f.recent(""), time.Now()) // all visitors together
}

func (f *failures) recent(who string) []time.Time {
	var keep []time.Time
	for _, t := range f.seen[who] {
		if time.Since(t) < failWindow {
			keep = append(keep, t)
		}
	}
	return keep
}

// visitor identifies who is knocking. Only Cloudflare's own header is trusted, and only behind a
// Cloudflare tunnel (Cloudflare overwrites it). Any other header can be set by the visitor, and other
// tunnels reach the gateway from 127.0.0.1, so those visitors share one limit.
func visitor(r *http.Request, trustCF bool) string {
	if trustCF {
		if v := strings.TrimSpace(r.Header.Get("Cf-Connecting-Ip")); v != "" {
			return v
		}
	}
	return "*"
}

// hiddenPath is true for dotfiles and dot-folders (.env, .git/…), except .well-known.
func hiddenPath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") && seg != ".well-known" {
			return true
		}
	}
	return false
}

func gateway(t Target, password string, trustCF bool, publicURL func() string, stats *counters) (http.Handler, error) {
	be, err := url.Parse(t.Backend)
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{ServerName: "localhost", RootCAs: t.RootCAs, MinVersion: tls.VersionTLS12}
	if t.RootCAs == nil { // e.g. Valet's own certificate; the backend is always on this Mac
		tlsCfg = &tls.Config{InsecureSkipVerify: true, ServerName: t.Domain}
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(be)
			r.Out.Host = t.Domain
			r.SetXForwarded()
			r.Out.Header.Set("X-Forwarded-Proto", "https")
			if pu, err := url.Parse(publicURL()); err == nil && pu.Host != "" {
				r.Out.Header.Set("X-Forwarded-Host", pu.Host)
				r.Out.Header.Set("X-Forwarded-Proto", pu.Scheme)
			}
		},
		Transport: &http.Transport{TLSClientConfig: tlsCfg, MaxIdleConnsPerHost: 8, IdleConnTimeout: 60 * time.Second},
		ModifyResponse: func(res *http.Response) error {
			// Redirects to the local address point at the public one instead
			if loc := res.Header.Get("Location"); loc != "" {
				for _, local := range []string{"https://" + t.Domain, "http://" + t.Domain} {
					if strings.HasPrefix(loc, local) {
						res.Header.Set("Location", strings.TrimRight(publicURL(), "/")+strings.TrimPrefix(loc, local))
					}
				}
			}
			return nil
		},
	}
	fails := &failures{seen: map[string][]time.Time{}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stats.requests.Add(1)
		if p, err := url.PathUnescape(r.URL.Path); err != nil || hiddenPath(p) || hiddenPath(r.URL.Path) {
			stats.blocked.Add(1)
			http.NotFound(w, r) // .env, .git and other hidden files never leave this Mac
			return
		}
		if password != "" {
			who := visitor(r, trustCF)
			if fails.tooMany(who) {
				stats.blocked.Add(1)
				http.Error(w, "Too many wrong passwords. Try again later.", http.StatusTooManyRequests)
				return
			}
			_, pw, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(pw), []byte(password)) != 1 {
				if ok { // a wrong password, not just the first request without one
					fails.add(who)
				}
				stats.blocked.Add(1)
				w.Header().Set("WWW-Authenticate", `Basic realm="Shared from LocalSites", charset="UTF-8"`)
				http.Error(w, "This shared site needs a password.", http.StatusUnauthorized)
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}), nil
}
