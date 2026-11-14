package share

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The site sees the visitor's address (or a placeholder), never 127.0.0.1 or what the visitor typed,
// and never the share's password; visitors with no wrong guesses get in during a flood.
func TestShareHeaders(t *testing.T) {
	fakeProvider(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ip=%s xff=%s auth=%s", r.Header.Get("X-Real-IP"), r.Header.Get("X-Forwarded-For"), r.Header.Get("Authorization"))
	}))
	defer backend.Close()
	m := NewManager(t.TempDir())
	s, err := m.Start("shop", Target{Domain: "shop.test", Backend: backend.URL}, "cloudflared", "s3cret-long", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	get := func(pw string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest("GET", "http://"+s.Gateway+"/", nil)
		if pw != "" {
			req.SetBasicAuth("guest", pw)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	code, body := get("s3cret-long", map[string]string{"X-Real-IP": "127.0.0.1", "X-Forwarded-For": "127.0.0.1"})
	if code != 200 || !strings.Contains(body, "ip="+placeholderIP) || !strings.Contains(body, "xff="+placeholderIP) || !strings.Contains(body, "auth= ") && !strings.HasSuffix(body, "auth=") {
		t.Fatalf("forged loopback: %d %s", code, body)
	}
	if _, body := get("s3cret-long", map[string]string{"Cf-Connecting-Ip": "203.0.113.7"}); !strings.Contains(body, "ip=203.0.113.7") {
		t.Fatalf("visitor address: %s", body)
	}
	// a flood of wrong guesses from many addresses
	for i := 0; i < maxTotalFailures+5; i++ {
		get("wrong", map[string]string{"Cf-Connecting-Ip": fmt.Sprintf("198.51.100.%d", i%250)})
	}
	if code, _ := get("s3cret-long", map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}); code != 200 {
		t.Fatalf("a visitor with no wrong guesses was locked out by the flood: %d", code)
	}
	if code, _ := get("s3cret-long", map[string]string{"Cf-Connecting-Ip": "198.51.100.3"}); code != 429 {
		t.Fatalf("a guessing address got in during the flood: %d", code)
	}
}
