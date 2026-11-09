package problems

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanRemovesSecretsAndPersonalData(t *testing.T) {
	c := Cleaner{Home: "/Users/alice", User: "alice", Projects: []string{"shop", "client-portal"}}
	in := strings.Join([]string{
		`DB_PASSWORD=hunter2 APP_KEY=base64:abc`,
		`{"password": "s3cret", "api_key":"k-123"}`,
		`mysql://root:pw123@127.0.0.1:3306/shop`,
		`Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig`,
		`GET /login?user=x&token=abc123 from 192.168.1.20`,
		`open /Users/alice/Sites/client-portal/index.php: permission denied (alice@example.com)`,
		`stripe sk_live_51HxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxXYZ`,
		`listening on 127.0.0.1:8744`,
	}, "\n")
	out := c.Clean(in)
	for _, leak := range []string{"hunter2", "base64:abc", "s3cret", "k-123", "pw123", "eyJhbGci", "abc123", "192.168.1.20", "/Users/alice", "alice", "client-portal", "example.com", "sk_live_51H"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in:\n%s", leak, out)
		}
	}
	for _, keep := range []string{"~/Sites/" + Hash("client-portal") + "/index.php", "127.0.0.1:8744", "permission denied", "DB_PASSWORD=[removed]"} {
		if !strings.Contains(out, keep) {
			t.Errorf("missing %q in:\n%s", keep, out)
		}
	}
	if (Cleaner{Projects: []string{"shop"}, KeepNames: true}).Clean("shop") != "shop" {
		t.Error("KeepNames")
	}
}

func TestRepeatsCountUp(t *testing.T) {
	s := Open(t.TempDir(), "1.0.0")
	added := 0
	s.OnAdd = func(Problem) { added++ }
	a := s.Add(KindService, "Apache stopped unexpectedly", "log 1")
	b := s.Add(KindService, "Apache stopped unexpectedly", "log 2")
	if a.ID != b.ID || b.Count != 2 || added != 1 {
		t.Fatalf("repeat: %+v added=%d", b, added)
	}
	s.MarkSent(a.ID, "ev")
	if c := s.Add(KindService, "Apache stopped unexpectedly", "log 3"); c.ID == a.ID {
		t.Fatal("a sent problem must not swallow new occurrences")
	}
	if len(s.List()) != 2 {
		t.Fatal("list")
	}
	s.Dismiss(a.ID)
	if len(s.List()) != 1 {
		t.Fatal("dismiss")
	}
	s.Dismiss("")
	if len(s.List()) != 0 {
		t.Fatal("dismiss all")
	}
}

func TestCrashTraceBecomesProblem(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir, "1.0.0")
	os.WriteFile(filepath.Join(dir, "crash-service-1.pending"), []byte("goroutine 1 [running]:\npanic: runtime error: index out of range [3] with length 2\n\nmain.main()"), 0o600)
	home := t.TempDir()
	reports := filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	os.MkdirAll(reports, 0o700)
	os.WriteFile(filepath.Join(reports, "LocalSites-2026-11-02-101010.ips"), []byte(`{"app_name":"LocalSites","bug_type":"309"}
{"exception":{"type":"EXC_BAD_ACCESS","signal":"SIGSEGV"}}`), 0o600)
	s.Collect(home)
	list := s.List()
	if len(list) != 2 {
		t.Fatalf("problems: %+v", list)
	}
	titles := list[0].Title + " | " + list[1].Title
	if !strings.Contains(titles, "The background service crashed: panic: runtime error: index out of range") || !strings.Contains(titles, "LocalSites crashed: EXC_BAD_ACCESS (SIGSEGV)") {
		t.Fatal(titles)
	}
	s.Collect(home) // nothing new
	if len(s.List()) != 2 {
		t.Fatal("a crash was collected twice")
	}
}

func TestParseDSN(t *testing.T) {
	d, err := parseDSN("https://abc123@o1.ingest.sentry.io/4507")
	if err != nil || d.endpoint != "https://o1.ingest.sentry.io/api/4507/envelope/" || d.key != "abc123" {
		t.Fatalf("%+v %v", d, err)
	}
	if d, err := parseDSN("https://k@glitchtip.example.com/prefix/7"); err != nil || d.endpoint != "https://glitchtip.example.com/prefix/api/7/envelope/" {
		t.Fatalf("%+v %v", d, err)
	}
	for _, bad := range []string{"", "https://o1.sentry.io/1", "http://k@example.com/1", "https://k@example.com/"} {
		if _, err := parseDSN(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestSendEnvelope(t *testing.T) {
	var got []map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("X-Sentry-Auth")
		if r.URL.Path != "/api/42/envelope/" {
			w.WriteHeader(404)
			return
		}
		rd := bufio.NewReader(r.Body)
		for {
			line, err := rd.ReadBytes('\n')
			if len(line) > 0 {
				var m map[string]any
				if json.Unmarshal(line, &m) == nil {
					got = append(got, m)
					if n, ok := m["length"].(float64); ok {
						body := make([]byte, int(n)+1)
						_, _ = io.ReadFull(rd, body)
						var ev map[string]any
						if json.Unmarshal(body[:int(n)], &ev) == nil {
							got = append(got, ev)
						}
					}
				}
			}
			if err != nil {
				break
			}
		}
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	dsn := strings.Replace(srv.URL, "http://", "http://pubkey@", 1) + "/42"
	r := Report{Kind: KindService, Title: "Apache stopped unexpectedly on port 8744", Version: "0.15.0", System: map[string]string{"macos": "26.1", "arch": "arm64"}, Detail: "AH00558", Count: 3}
	id, err := Send(dsn, r, []byte("PK.."))
	if err != nil || len(id) != 32 {
		t.Fatal(id, err)
	}
	if !strings.Contains(auth, "sentry_key=pubkey") {
		t.Fatal(auth)
	}
	var event map[string]any
	types := []string{}
	for _, m := range got {
		if ty, ok := m["type"].(string); ok {
			types = append(types, ty)
		}
		if m["release"] != nil {
			event = m
		}
	}
	if strings.Join(types, ",") != "event,attachment,attachment" {
		t.Fatalf("items: %v", types)
	}
	if event["release"] != "localsites@0.15.0" || event["user"] != nil || event["server_name"] != nil {
		t.Fatalf("event: %v", event)
	}
	if fp, _ := json.Marshal(event["fingerprint"]); string(fp) != `["service","Apache stopped unexpectedly on port N"]` {
		t.Fatalf("fingerprint %s", fp)
	}
}

// A real crash: a child process sets up capture, then panics; the trace becomes a problem.
func TestRealPanicIsCaptured(t *testing.T) {
	if d := os.Getenv("LS_CRASH_DIR"); d != "" {
		CaptureCrashes(d, "service")
		var m map[string]int
		m["boom"]++ // panic: assignment to entry in nil map
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRealPanicIsCaptured$")
	cmd.Env = append(os.Environ(), "LS_CRASH_DIR="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("the child should have crashed")
	}
	CaptureCrashes(dir, "service") // the next start keeps the trace for collection
	s := Open(dir, "1.0.0")
	s.Collect("")
	list := s.List()
	if len(list) != 1 || !strings.Contains(list[0].Title, "panic: assignment to entry in nil map") || !strings.Contains(list[0].Detail, "goroutine") {
		t.Fatalf("%+v", list)
	}
}

// A DNS blocker answers 0.0.0.0: the report must not go anywhere, and the message must say why.
func TestBlockedByDNS(t *testing.T) {
	old := LookupHost
	t.Cleanup(func() { LookupHost = old })
	LookupHost = func(string) ([]string, error) { return []string{"0.0.0.0", "::"}, nil }
	_, err := Send("https://k@o1.ingest.de.sentry.io/42", Report{Kind: KindUI, Title: "x", Version: "1", System: map[string]string{}}, nil)
	if err == nil || !strings.Contains(err.Error(), "blocks the report server") || !strings.Contains(err.Error(), "Allow *.sentry.io") {
		t.Fatalf("got %v", err)
	}
	LookupHost = func(string) ([]string, error) { return []string{"0.0.0.0", "34.120.195.249"}, nil }
	if err := reachable("https://o1.ingest.de.sentry.io/api/42/envelope/"); err != nil {
		t.Fatalf("one real address is enough: %v", err)
	}
}
