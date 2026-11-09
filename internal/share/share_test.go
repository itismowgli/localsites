package share

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A stand-in tunnel client: prints an address like cloudflared does, then waits.
func fakeProvider(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-tunnel")
	os.WriteFile(bin, []byte("#!/bin/sh\necho 'starting'\necho 'INF |  https://test-abc.trycloudflare.com  |'\nsleep 60\n"), 0o755)
	old := Providers
	t.Cleanup(func() { Providers = old })
	p := Providers[0]
	p.Bin = bin
	Providers = []Provider{p}
}

func TestShareGateway(t *testing.T) {
	fakeProvider(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.Redirect(w, r, "https://shop.test/account", http.StatusFound)
			return
		}
		fmt.Fprintf(w, "host=%s proto=%s", r.Host, r.Header.Get("X-Forwarded-Proto"))
	}))
	defer backend.Close()
	m := NewManager(t.TempDir())
	s, err := m.Start("shop", Target{Domain: "shop.test", Backend: backend.URL}, "cloudflared", "s3cret", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://test-abc.trycloudflare.com" {
		t.Fatalf("url %q", s.URL)
	}
	get := func(path, pw string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", "http://"+s.Gateway+path, nil)
		if pw != "" {
			req.SetBasicAuth("guest", pw)
		}
		res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	if res, _ := get("/", ""); res.StatusCode != 401 {
		t.Fatalf("no password: %d", res.StatusCode)
	}
	if res, _ := get("/", "wrong"); res.StatusCode != 401 {
		t.Fatalf("wrong password: %d", res.StatusCode)
	}
	if _, body := get("/", "s3cret"); body != "host=shop.test proto=https" {
		t.Fatalf("proxied: %q", body)
	}
	for _, p := range []string{"/.env", "/.git/config", "/a/.htpasswd", "/%2eenv"} {
		if res, _ := get(p, "s3cret"); res.StatusCode != 404 {
			t.Fatalf("%s through the share: %d", p, res.StatusCode)
		}
	}
	if res, _ := get("/.well-known/x", "s3cret"); res.StatusCode != 200 {
		t.Fatalf(".well-known blocked: %d", res.StatusCode)
	}
	if res, _ := get("/login", "s3cret"); res.Header.Get("Location") != "https://test-abc.trycloudflare.com/account" {
		t.Fatalf("redirect not rewritten: %q", res.Header.Get("Location"))
	}
	if _, err := m.Start("shop", Target{Domain: "shop.test", Backend: backend.URL}, "cloudflared", "", time.Minute); err == nil {
		t.Fatal("same site shared twice")
	}
	for i := 0; i < 10; i++ { // guessing is limited
		get("/", "guess")
	}
	if res, _ := get("/", "s3cret"); res.StatusCode != 429 {
		t.Fatalf("after 10 wrong passwords even the right one must wait: %d", res.StatusCode)
	}
	if l := m.List(); len(l) != 1 || l[0].Requests == 0 || l[0].Blocked == 0 {
		t.Fatalf("visit counts: %+v", l)
	}
	time.Sleep(2500 * time.Millisecond) // expires by itself
	if len(m.List()) != 0 {
		t.Fatal("share did not expire")
	}
	if _, err := http.Get("http://" + s.Gateway + "/"); err == nil {
		t.Fatal("gateway still answering after expiry")
	}
}

func TestShareLimits(t *testing.T) {
	fakeProvider(t)
	m := NewManager(t.TempDir())
	if _, err := m.Start("a", Target{Domain: "a.test", Backend: "http://127.0.0.1:1"}, "cloudflared", "", 48*time.Hour); err == nil {
		t.Fatal("48h share accepted")
	}
	if _, err := m.Start("a", Target{Domain: "a.test", Backend: "http://127.0.0.1:1"}, "nope", "", time.Minute); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

// Visitors can set X-Forwarded-For (and, without Cloudflare, Cf-Connecting-Ip) themselves: changing it on
// every try must not reset the wrong-password limit.
func TestShareLimitIgnoresForgedHeaders(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer backend.Close()
	for _, trustCF := range []bool{false, true} {
		gw, err := gateway(Target{Domain: "shop.test", Backend: backend.URL}, "s3cret", trustCF, func() string { return "" }, &counters{})
		if err != nil {
			t.Fatal(err)
		}
		try := func(i int, pw string) int {
			req := httptest.NewRequest("GET", "/", nil)
			req.SetBasicAuth("guest", pw)
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.%d.%d", i/250, i%250))
			if !trustCF {
				req.Header.Set("Cf-Connecting-Ip", fmt.Sprintf("10.1.%d.%d", i/250, i%250))
			}
			rec := httptest.NewRecorder()
			gw.ServeHTTP(rec, req)
			return rec.Code
		}
		for i := 0; i < maxFailures; i++ {
			try(i, "wrong")
		}
		if code := try(999, "wrong"); code != http.StatusTooManyRequests {
			t.Fatalf("trustCF=%v: a new forged address got another try: %d", trustCF, code)
		}
	}
}
