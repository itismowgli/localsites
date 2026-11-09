package frontdoor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Builtin is LocalSites' own front door: HTTPS reverse proxy + .test DNS.
//
// One-time OS setup (done by the installer's privileged step, not here):
//   - macOS: /etc/resolver/<tld> -> nameserver 127.0.0.1, port <DNSPort>
//   - trust the LocalSites CA (name-constrained to .<tld>)
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
func (b *Builtin) Start() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return nil
	}
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
	ln, err := net.Listen("tcp", b.HTTPSAddr)
	if err != nil {
		return err
	}
	ln80, err := net.Listen("tcp", b.HTTPAddr)
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
			return err
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
