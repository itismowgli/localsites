package frontdoor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Builtin is LocalSites' own front door: HTTPS reverse proxy + .test DNS.
//
// One-time OS setup (the "Set up .test domains" step in Setup, one password prompt):
//   - macOS: /etc/resolver/<tld> -> nameserver 127.0.0.1, port <DNSPort>
//   - trust the LocalSites CA (name-constrained to .<tld>)
//
// Ports 80 and 443 need no setup: when the system refuses 127.0.0.1 (macOS keeps ports below 1024 on a
// single address for root), the proxy listens on all addresses and drops every connection that doesn't
// come from this computer before reading anything.
type Builtin struct {
	HTTPAddr  string // "127.0.0.1:80"
	HTTPSAddr string // "127.0.0.1:443"
	DNSAddr   string // "127.0.0.1:5354"
	TLD       string
	GetCert   func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	Backend   *x509.CertPool // CA that signed the backend (Apache) cert

	mu      sync.RWMutex
	routes  map[string]*httputil.ReverseProxy
	servers []*http.Server
	dns     net.PacketConn
	running bool
	lastErr error // why the last Start failed
}

// LastError says why the front door couldn't start (nil when it runs or wasn't tried).
func (b *Builtin) LastError() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.running {
		return nil
	}
	return b.lastErr
}

func (b *Builtin) Name() string { return "builtin" }

func (b *Builtin) Running() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.running
}

func (b *Builtin) Conflict(string) string { return "" }

func (b *Builtin) Sync(routes []Route) ([]string, error) {
	m := map[string]*httputil.ReverseProxy{}
	for _, r := range routes {
		u, err := url.Parse(r.Backend)
		if err != nil {
			return nil, err
		}
		m[strings.ToLower(r.Domain)] = b.proxy(u)
	}
	b.mu.Lock()
	b.routes = m
	b.mu.Unlock()
	return []string{"Built-in front door serving " + itoa(len(m)) + " site(s)"}, nil
}

func (b *Builtin) proxy(target *url.URL) *httputil.ReverseProxy {
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{RootCAs: b.Backend, ServerName: "localhost", MinVersion: tls.VersionTLS12},
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = r.In.Host // keep the site's name for Apache's virtual host
			r.SetXForwarded()
			r.Out.Header.Set("X-Real-IP", clientIP(r.In.RemoteAddr))
		},
		Transport: tr,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "LocalSites: the site's web server is not reachable ("+err.Error()+")", http.StatusBadGateway)
		},
	}
}

// ServeHTTP routes by Host (exact name or www.name).
func (b *Builtin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	b.mu.RLock()
	p := b.routes[host]
	if p == nil {
		p = b.routes[strings.TrimPrefix(host, "www.")]
	}
	b.mu.RUnlock()
	if p == nil {
		http.Error(w, "LocalSites: no site is switched on for "+host, http.StatusNotFound)
		return
	}
	p.ServeHTTP(w, r)
}

// Start begins listening. Ports below 1024 may need the OS permission set up by the installer.
func (b *Builtin) Start() (err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return nil
	}
	defer func() { b.lastErr = err }()
	tlsCfg := &tls.Config{GetCertificate: b.GetCert, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
	https := &http.Server{Addr: b.HTTPSAddr, Handler: b, TLSConfig: tlsCfg, ReadHeaderTimeout: 10 * time.Second}
	redirect := &http.Server{Addr: b.HTTPAddr, ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		target := "https://" + host
		if _, port, _ := net.SplitHostPort(b.HTTPSAddr); port != "443" {
			target += ":" + port
		}
		http.Redirect(w, r, target+r.URL.RequestURI(), http.StatusMovedPermanently)
	})}
	ln, err := listenLocal(b.HTTPSAddr)
	if err != nil {
		return err
	}
	ln80, err := listenLocal(b.HTTPAddr)
	if err != nil {
		ln.Close()
		return err
	}
	go https.Serve(tls.NewListener(ln, tlsCfg))
	go redirect.Serve(ln80)
	b.servers = []*http.Server{https, redirect}
	if b.DNSAddr != "" {
		pc, err := net.ListenPacket("udp", b.DNSAddr)
		if err != nil {
			ln.Close()
			ln80.Close()
			b.servers = nil
			return fmt.Errorf("DNS on %s: %w", b.DNSAddr, err)
		}
		b.dns = pc
		go b.serveDNS(pc)
	}
	b.running = true
	return nil
}

func (b *Builtin) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, s := range b.servers {
		_ = s.Shutdown(ctx)
	}
	if b.dns != nil {
		b.dns.Close()
	}
	b.servers, b.dns, b.running = nil, nil, false
}

// serveDNS answers A/AAAA for *.<tld> with loopback; everything else gets NXDOMAIN/REFUSED.
func (b *Builtin) serveDNS(pc net.PacketConn) {
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if resp, err := b.answer(buf[:n]); err == nil {
			_, _ = pc.WriteTo(resp, addr)
		}
	}
}

func (b *Builtin) answer(req []byte) ([]byte, error) {
	var p dnsmessage.Parser
	h, err := p.Start(req)
	if err != nil {
		return nil, err
	}
	q, err := p.Question()
	if err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
	rh := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, OpCode: h.OpCode, RecursionDesired: h.RecursionDesired}
	ours := name == b.TLD || strings.HasSuffix(name, "."+b.TLD)
	if !ours {
		rh.RCode = dnsmessage.RCodeRefused
	}
	bld := dnsmessage.NewBuilder(make([]byte, 0, 512), rh)
	bld.EnableCompression()
	_ = bld.StartQuestions()
	_ = bld.Question(q)
	_ = bld.StartAnswers()
	rr := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}
	if ours {
		switch q.Type {
		case dnsmessage.TypeA:
			_ = bld.AResource(rr, dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}})
		case dnsmessage.TypeAAAA:
			_ = bld.AAAAResource(rr, dnsmessage.AAAAResource{AAAA: [16]byte{15: 1}})
		}
	}
	return bld.Finish()
}

func clientIP(remote string) string {
	if h, _, err := net.SplitHostPort(remote); err == nil {
		return h
	}
	return remote
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

// listenLocal listens on a loopback address. If the system refuses it for lack of privileges, it listens
// on all addresses instead and accepts only connections from this computer.
func listenLocal(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	host, port, _ := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() || !errors.Is(err, os.ErrPermission) {
		return nil, describeListenErr(addr, err)
	}
	all, err2 := net.Listen("tcp", ":"+port)
	if err2 != nil {
		return nil, describeListenErr(addr, err2)
	}
	return loopbackOnly{all}, nil
}

func describeListenErr(addr string, err error) error {
	if errors.Is(err, syscall.EADDRINUSE) {
		_, port, _ := net.SplitHostPort(addr)
		return fmt.Errorf("port %s is already used by another app", port)
	}
	return err
}

// loopbackOnly accepts connections from this computer only; others are closed at once.
type loopbackOnly struct{ net.Listener }

func (l loopbackOnly) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if IsLoopback(c.RemoteAddr()) {
			return c, nil
		}
		c.Close()
	}
}

// IsLoopback reports whether a connection comes from this computer.
func IsLoopback(a net.Addr) bool {
	t, ok := a.(*net.TCPAddr)
	return ok && t.IP.IsLoopback()
}
