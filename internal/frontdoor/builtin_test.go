package frontdoor

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/localsites/localsites/internal/ca"
	"golang.org/x/net/dns/dnsmessage"
)

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestBuiltinProxyRoutesByHost(t *testing.T) {
	c, err := ca.Load(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	// Backend standing in for Apache: HTTPS with a CA-issued localhost cert
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "host="+r.Host+" proto="+r.Header.Get("X-Forwarded-Proto")+" ip="+r.Header.Get("X-Real-IP"))
	}))
	leaf, _ := c.Issue("localhost", "127.0.0.1")
	backend.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}}
	backend.StartTLS()
	defer backend.Close()

	b := &Builtin{HTTPAddr: freeAddr(t), HTTPSAddr: freeAddr(t), TLD: "test", GetCert: c.GetCertificate, Backend: c.Pool()}
	if _, err := b.Sync([]Route{{Domain: "shop.test", Backend: backend.URL}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	defer b.Stop()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: c.Pool(), ServerName: "shop.test"},
			// Every *.test name goes to our proxy (what the .test DNS does in real use)
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) { return net.Dial("tcp", b.HTTPSAddr) },
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	res, err := client.Get("https://shop.test/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "host=shop.test") || !strings.Contains(string(body), "proto=https") || !strings.Contains(string(body), "ip=127.0.0.1") {
		t.Fatalf("unexpected backend view: %s", body)
	}

	// www. alias goes to the same site; unknown names get 404
	res, _ = client.Get("https://www.shop.test/")
	if res.StatusCode != 200 {
		t.Fatalf("www alias: %d", res.StatusCode)
	}
	client.Transport.(*http.Transport).TLSClientConfig.ServerName = "other.test"
	res, _ = client.Get("https://other.test/")
	if res.StatusCode != 404 {
		t.Fatalf("unknown site: want 404, got %d", res.StatusCode)
	}

	// Plain HTTP redirects to HTTPS
	res, err = http.Get("http://" + b.HTTPAddr + "/x?y=1")
	if err == nil && res.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("http: want 301, got %d", res.StatusCode)
	}
}

func TestDNSAnswersOnlyTLD(t *testing.T) {
	b := &Builtin{TLD: "test"}
	ask := func(name string, typ dnsmessage.Type) dnsmessage.Message {
		q := dnsmessage.Message{Header: dnsmessage.Header{ID: 7, RecursionDesired: true},
			Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET}}}
		req, _ := q.Pack()
		resp, err := b.answer(req)
		if err != nil {
			t.Fatal(err)
		}
		var m dnsmessage.Message
		if err := m.Unpack(resp); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := ask("shop.test.", dnsmessage.TypeA)
	if len(m.Answers) != 1 || m.Answers[0].Body.(*dnsmessage.AResource).A != [4]byte{127, 0, 0, 1} || m.Header.ID != 7 {
		t.Fatalf("A shop.test: %+v", m)
	}
	if m := ask("shop.test.", dnsmessage.TypeAAAA); len(m.Answers) != 1 {
		t.Fatalf("AAAA shop.test: %+v", m)
	}
	if m := ask("google.com.", dnsmessage.TypeA); m.Header.RCode != dnsmessage.RCodeRefused || len(m.Answers) != 0 {
		t.Fatalf("google.com must be refused: %+v", m.Header)
	}
}

type fakeConn struct {
	net.Conn
	remote net.Addr
	closed *bool
}

func (c fakeConn) RemoteAddr() net.Addr { return c.remote }
func (c fakeConn) Close() error         { *c.closed = true; return nil }

type fakeListener struct {
	conns []net.Conn
	net.Listener
}

func (l *fakeListener) Accept() (net.Conn, error) {
	if len(l.conns) == 0 {
		return nil, net.ErrClosed
	}
	c := l.conns[0]
	l.conns = l.conns[1:]
	return c, nil
}

func TestLoopbackOnlyDropsOtherComputers(t *testing.T) {
	var lanClosed, v6Closed, localClosed bool
	lan := fakeConn{remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.20"), Port: 5000}, closed: &lanClosed}
	v6 := fakeConn{remote: &net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 5000}, closed: &v6Closed}
	local := fakeConn{remote: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5000}, closed: &localClosed}
	l := loopbackOnly{&fakeListener{conns: []net.Conn{lan, v6, local}}}
	c, err := l.Accept()
	if err != nil || c.RemoteAddr().String() != "127.0.0.1:5000" {
		t.Fatalf("accepted %v, %v", c, err)
	}
	if !lanClosed || !v6Closed || localClosed {
		t.Fatalf("closed: lan %v v6 %v local %v", lanClosed, v6Closed, localClosed)
	}
	if _, err := l.Accept(); err == nil {
		t.Fatal("expected the listener's error")
	}
	if !IsLoopback(&net.TCPAddr{IP: net.IPv6loopback}) || IsLoopback(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}) {
		t.Fatal("IsLoopback")
	}
}

func TestListenLocalPortInUse(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	_, err = listenLocal(busy.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "already used by another app") {
		t.Fatalf("err = %v", err)
	}
	ln, err := listenLocal("127.0.0.1:0") // a free port: a plain loopback listener
	if err != nil {
		t.Fatal(err)
	}
	if _, wrapped := ln.(loopbackOnly); wrapped {
		t.Fatal("a loopback address that works must not be widened")
	}
	ln.Close()
}
