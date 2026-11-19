package share

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTunnlArgs(t *testing.T) {
	a := strings.Join(tunnlArgs(4321, "/data/logs"), " ")
	for _, want := range []string{"-F /dev/null", "IdentitiesOnly=yes", "IdentityAgent=none", "UserKnownHostsFile=/data/logs/tunnl-known-hosts",
		"StrictHostKeyChecking=accept-new", "PasswordAuthentication=no", "-R 80:127.0.0.1:4321 proxy.tunnl.gg"} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %s", want, a)
		}
	}
	p, _ := FindProvider("tunnl")
	// what tunnl.gg prints (colors included); the pricing link must not be taken for the address
	out := "\x1b[38;5;245m  URL      \x1b[0m\x1b[38;5;141mhttps://sunny-ocean-ae07fcd0.tunnl.gg\x1b[0m\r\n" +
		"Pro: from $5/mo: https://tunnl.gg/pricing?utm_source=ssh\r\n"
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if m := p.URLRe.FindString(line); m != "" {
			got = append(got, m)
		}
	}
	if len(got) != 1 || got[0] != "https://sunny-ocean-ae07fcd0.tunnl.gg" {
		t.Fatalf("addresses found: %q", got)
	}
}

// A real share through tunnl.gg, with a password. Needs the internet: LOCALSITES_LIVE_TUNNEL=1 go test -run Live ./internal/share
func TestLiveTunnl(t *testing.T) {
	if os.Getenv("LOCALSITES_LIVE_TUNNEL") == "" {
		t.Skip("set LOCALSITES_LIVE_TUNNEL=1 to open a real tunnel")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "secret page") }))
	defer site.Close()
	m := NewManager(t.TempDir())
	s, err := m.Start("demo", Target{Domain: "demo.test", Backend: site.URL}, "tunnl", "correct-horse-1", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(s.ID)
	t.Log("address:", s.URL)
	resp, err := http.Get(s.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "secret page") {
		t.Fatal("the page was served without the password")
	}
	req, _ := http.NewRequest("GET", s.URL+"/", nil)
	req.SetBasicAuth("", "correct-horse-1")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	t.Logf("with password: %d %q", resp.StatusCode, string(body))
}
