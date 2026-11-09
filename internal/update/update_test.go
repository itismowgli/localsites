package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.9.0", "0.10.0", true}, {"0.10.0", "0.9.0", false}, {"1.0.0", "1.0.0", false},
		{"1.0.0-beta.1", "1.0.0", true}, {"1.0.0", "1.0.0-beta.1", false}, {"1.0.0-beta.1", "1.0.0-beta.2", true}, {"v0.12.0", "0.12.1", true},
	} {
		if got := Less(c.a, c.b); got != c.want {
			t.Errorf("Less(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	old := PublicKey
	PublicKey = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { PublicKey = old })
	return priv
}

func TestVerifyRejectsWrongKeyAndChangedBytes(t *testing.T) {
	priv := testKey(t)
	raw := []byte(`{"version":"9.9.9","files":[{"name":"a.zip","os":"darwin","arch":"arm64","size":1,"sha256":"00"}]}`)
	if _, err := Verify(raw, Sign(priv, raw)); err != nil {
		t.Fatal(err)
	}
	changed := []byte(strings.Replace(string(raw), "9.9.9", "9.9.8", 1))
	if _, err := Verify(changed, Sign(priv, raw)); err == nil {
		t.Fatal("a changed manifest was accepted")
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Verify(raw, Sign(other, raw)); err == nil {
		t.Fatal("a manifest signed with another key was accepted")
	}
}

// fakeApp makes a minimal LocalSites.app whose binary answers update-selftest with ver.
func fakeApp(t *testing.T, dir, ver string) string {
	t.Helper()
	app := filepath.Join(dir, AppName)
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>%s</string><key>CFBundleShortVersionString</key><string>%s</string></dict></plist>`, BundleID, ver)
	_ = os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644)
	_ = os.WriteFile(filepath.Join(app, "Contents", "MacOS", "LocalSites"), []byte("#!/bin/sh\necho "+ver+"\n"), 0o755)
	return app
}

type feed struct {
	srv  *httptest.Server
	zip  []byte
	hits map[string]int
}

// newFeed serves a GitHub-style release list with one signed release of ver.
func newFeed(t *testing.T, priv ed25519.PrivateKey, ver string, tamper bool) *feed {
	t.Helper()
	f := &feed{hits: map[string]int{}}
	src := t.TempDir()
	fakeApp(t, src, ver)
	zp := filepath.Join(t.TempDir(), "a.zip")
	if out, err := exec.Command("/usr/bin/ditto", "-c", "-k", "--keepParent", filepath.Join(src, AppName), zp).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	f.zip, _ = os.ReadFile(zp)
	sum := sha256.Sum256(f.zip)
	name := "LocalSites-" + ver + "-macos-" + runtime.GOARCH + ".zip"
	m := Manifest{Version: ver, Date: "2026-11-02", Channel: "stable", Title: "Test", Notes: []string{"A change"}, Security: true,
		Files: []File{{Name: name, OS: runtime.GOOS, Arch: runtime.GOARCH, Size: int64(len(f.zip)), SHA256: hex.EncodeToString(sum[:])}}}
	raw, _ := json.Marshal(m)
	sig := Sign(priv, raw)
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		u := f.srv.URL
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tag_name": "v" + ver, "html_url": u + "/page", "assets": []map[string]string{
				{"name": "update.json", "browser_download_url": u + "/update.json"},
				{"name": "update.json.sig", "browser_download_url": u + "/update.json.sig"},
				{"name": name, "browser_download_url": u + "/zip"}}},
			{"tag_name": "v99.0.0", "prerelease": true, "assets": []map[string]string{}}, // beta, no manifest: ignored
		})
	})
	mux.HandleFunc("/update.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) })
	mux.HandleFunc("/update.json.sig", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(sig) })
	mux.HandleFunc("/zip", func(w http.ResponseWriter, r *http.Request) {
		f.hits["zip"]++
		b := f.zip
		if tamper {
			b = append([]byte{}, b...)
			b[len(b)/2] ^= 0xff
		}
		_, _ = w.Write(b)
	})
	FeedURL, AllowHTTP = f.srv.URL+"/releases", true
	t.Cleanup(func() { FeedURL, AllowHTTP = "https://example.invalid", false })
	return f
}

func waitPhase(t *testing.T, u *Updater, want ...Phase) Status {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s := u.Status()
		for _, w := range want {
			if s.Phase == w {
				return s
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("phase stayed %s", u.Status().Phase)
	return Status{}
}

func needMac(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("in-app install is macOS only")
	}
}

func TestCheckDownloadInstallAndSwitchBack(t *testing.T) {
	needMac(t)
	priv := testKey(t)
	newFeed(t, priv, "0.13.0", false)
	dir := t.TempDir()
	u := New(dir)
	u.Current = "0.12.0"

	st := u.Check("stable")
	if st.Phase != Available || st.Latest == nil || st.Latest.Version != "0.13.0" || !st.Latest.Security || st.Size == 0 {
		t.Fatalf("check: %+v", st)
	}
	if _, err := u.Download(nil); err != nil {
		t.Fatal(err)
	}
	if s := waitPhase(t, u, Ready, Failed); s.Phase != Ready {
		t.Fatalf("download: %+v", s)
	}
	// a later check keeps the finished download
	if s := u.Check("stable"); s.Phase != Ready {
		t.Fatalf("check after download: %s", s.Phase)
	}

	apps := t.TempDir()
	target := fakeApp(t, apps, "0.12.0")
	p, err := u.Prepare(target, false)
	if err != nil {
		t.Fatal(err)
	}
	if u.Status().Phase != Installing {
		t.Fatal("not marked installing")
	}
	runPlan(t, p)
	if v := bundleVersion(t, target); v != "0.13.0" {
		t.Fatalf("installed app is %s", v)
	}
	if _, err := os.Stat(u.verDir("0.13.0")); !os.IsNotExist(err) {
		t.Fatal("the installed download was not cleaned up")
	}

	u2 := New(dir) // the service after the restart
	u2.Current = "0.13.0"
	if s := u2.Status(); s.Kept != "0.12.0" || !strings.Contains(s.Result, "0.12.0 to 0.13.0") {
		t.Fatalf("after install: kept %q result %q", s.Kept, s.Result)
	}
	p, err = u2.Prepare(target, true)
	if err != nil {
		t.Fatal(err)
	}
	runPlan(t, p)
	if v := bundleVersion(t, target); v != "0.12.0" {
		t.Fatalf("after switching back the app is %s", v)
	}
	if k := keptVersion(dir); k != "0.13.0" {
		t.Fatalf("after switching back, kept %q (want 0.13.0 so you can switch forward)", k)
	}
}

func runPlan(t *testing.T, p Plan) {
	t.Helper()
	b, _ := json.Marshal(p)
	pf := filepath.Join(p.Dir, "plan.json")
	_ = os.WriteFile(pf, b, 0o600)
	if _, err := Apply(pf); err != nil {
		t.Fatal(err)
	}
}

func bundleVersion(t *testing.T, app string) string {
	out, err := exec.Command(filepath.Join(app, "Contents", "MacOS", "LocalSites"), "update-selftest").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestTamperedDownloadIsDeleted(t *testing.T) {
	needMac(t)
	priv := testKey(t)
	newFeed(t, priv, "0.13.0", true)
	dir := t.TempDir()
	u := New(dir)
	u.Current = "0.12.0"
	if st := u.Check("stable"); st.Phase != Available {
		t.Fatalf("check: %+v", st)
	}
	_, _ = u.Download(nil)
	s := waitPhase(t, u, Ready, Failed)
	if s.Phase != Failed || !strings.Contains(s.Error, "checksum") {
		t.Fatalf("tampered download: %+v", s)
	}
	if _, err := os.Stat(u.verDir("0.13.0")); !os.IsNotExist(err) {
		t.Fatal("tampered download was kept")
	}
	if _, err := u.Prepare(fakeApp(t, t.TempDir(), "0.12.0"), false); err == nil {
		t.Fatal("prepared an install without a verified download")
	}
}

func TestUnsignedReleaseIsIgnored(t *testing.T) {
	needMac(t)
	signer := testKey(t)
	newFeed(t, signer, "0.13.0", false)
	testKey(t) // the app now trusts a different key
	u := New(t.TempDir())
	u.Current = "0.12.0"
	st := u.Check("stable")
	if st.Phase != Failed || st.Latest != nil || !strings.Contains(st.Error, "signature") {
		t.Fatalf("a release signed with another key was offered: %+v", st)
	}
}

func TestChangedDownloadOnDiskIsRefused(t *testing.T) {
	needMac(t)
	priv := testKey(t)
	newFeed(t, priv, "0.13.0", false)
	u := New(t.TempDir())
	u.Current = "0.12.0"
	u.Check("stable")
	_, _ = u.Download(nil)
	waitPhase(t, u, Ready)
	zip := filepath.Join(u.verDir("0.13.0"), "update.zip")
	b, _ := os.ReadFile(zip)
	b[10] ^= 0xff
	_ = os.WriteFile(zip, b, 0o600)
	if _, err := u.Prepare(fakeApp(t, t.TempDir(), "0.12.0"), false); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("a download changed after verifying was accepted: %v", err)
	}
}

func TestFailedSwapRestoresTheApp(t *testing.T) {
	needMac(t)
	dir := t.TempDir()
	target := fakeApp(t, t.TempDir(), "0.12.0")
	p := Plan{From: filepath.Join(dir, "incoming", AppName), To: target, FromVer: "0.13.0", ToVer: "0.12.0", Dir: dir} // From is missing
	b, _ := json.Marshal(p)
	pf := filepath.Join(dir, "plan.json")
	_ = os.WriteFile(pf, b, 0o600)
	if _, err := Apply(pf); err == nil {
		t.Fatal("expected an error")
	}
	if v := bundleVersion(t, target); v != "0.12.0" {
		t.Fatalf("the app was not restored: %s", v)
	}
	if New(dir).Status().Result == "" {
		t.Fatal("the failure isn't reported after the restart")
	}
}

func TestBundleOf(t *testing.T) {
	needMac(t)
	app := fakeApp(t, t.TempDir(), "0.12.0")
	if got, err := BundleOf(filepath.Join(app, "Contents", "MacOS", "LocalSites")); err != nil || got != app {
		t.Fatalf("BundleOf = %q, %v", got, err)
	}
	if _, err := BundleOf("/usr/local/bin/localsites"); err == nil {
		t.Fatal("the command-line tool was taken for the app")
	}
}
