// Package update finds, verifies and installs new versions of the LocalSites app.
//
// Each release carries a signed manifest (update.json and update.json.sig, ed25519). Nothing is installed
// unless the manifest's signature matches the public key built into the app, the download matches the
// size and SHA-256 in that manifest, and the new app reports the expected version when run. Checking
// only reads the public release list; nothing about this Mac is sent.
//
// The swap itself is done by a short-lived helper (Apply) after the app and the background service have
// quit: the running app moves to updates/previous (kept for switching back) and the new one takes its place.
package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/localsites/localsites/internal/version"
)

// BundleID is the app's CFBundleIdentifier; an update must have the same one.
const BundleID = "com.wails.localsites"

// AppName is the bundle folder name.
const AppName = "LocalSites.app"

// FeedURL lists releases (GitHub's format). Tests point it at a local server.
var FeedURL = "https://api.github.com/repos/" + version.Repo + "/releases?per_page=20"

// AllowHTTP lets tests download from this Mac (127.0.0.1) over http. Everything else must use https.
var AllowHTTP = false

func init() {
	// tests point the release list at a local server; manifests must still carry the built-in key's signature
	if f := os.Getenv("LOCALSITES_UPDATE_FEED"); f != "" {
		FeedURL, AllowHTTP = f, true
	}
}

// File is one download in a manifest.
type File struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"` // amd64, arm64 or universal
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest describes a release. Its exact bytes are signed.
type Manifest struct {
	Version  string   `json:"version"`
	Date     string   `json:"date"`
	Channel  string   `json:"channel"` // stable or beta
	Title    string   `json:"title"`
	Notes    []string `json:"notes"`
	Security bool     `json:"security,omitempty"` // contains a security fix
	Files    []File   `json:"files"`
}

// Phase is where an update is.
type Phase string

const (
	Idle        Phase = "idle"        // up to date, or never checked
	Checking    Phase = "checking"    // asking for the release list
	Available   Phase = "available"   // a newer, correctly signed release exists
	Downloading Phase = "downloading" // in the background
	Ready       Phase = "ready"       // downloaded, verified and unpacked; installs on restart or quit
	Installing  Phase = "installing"  // the helper is swapping the app
	Failed      Phase = "error"
)

// Status is what the app shows.
type Status struct {
	Current    string    `json:"current"`
	Phase      Phase     `json:"phase"`
	Latest     *Manifest `json:"latest,omitempty"`
	Size       int64     `json:"size,omitempty"` // download size for this Mac
	ReleaseURL string    `json:"release_url,omitempty"`
	Message    string    `json:"message,omitempty"`
	Error      string    `json:"error,omitempty"`
	Done       int64     `json:"done,omitempty"` // bytes downloaded
	LastCheck  time.Time `json:"last_check,omitzero"`
	Kept       string    `json:"kept,omitempty"` // version kept in updates/previous, for switching back
	Signed     bool      `json:"signed"`         // this build has an update key (otherwise it can only point to the release page)
	Supported  bool      `json:"supported"`      // in-app install works on this system
	Result     string    `json:"result,omitempty"`
}

// Updater keeps the state of one update at a time.
type Updater struct {
	Dir     string // where downloads, the kept version and state live
	Current string

	mu     sync.Mutex
	st     Status
	file   File // download for this Mac
	assets map[string]string
	notes  []byte // signed manifest bytes of st.Latest
	sig    []byte
	busy   bool
}

type saved struct {
	LastCheck time.Time `json:"last_check"`
}

// New loads the saved state from dir.
func New(dir string) *Updater {
	u := &Updater{Dir: dir, Current: version.Version}
	u.st = Status{Current: u.Current, Phase: Idle, Signed: PublicKey != "", Supported: runtime.GOOS == "darwin"}
	var s saved
	if b, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil && json.Unmarshal(b, &s) == nil {
		u.st.LastCheck = s.LastCheck
	}
	u.st.Kept = keptVersion(dir)
	if b, err := os.ReadFile(filepath.Join(dir, "result.json")); err == nil {
		var r Result
		if json.Unmarshal(b, &r) == nil {
			switch {
			case r.Error != "":
				u.st.Result = "The last update didn't install: " + r.Error
			case r.From != "" && r.To != "":
				u.st.Result = "Updated from " + r.From + " to " + r.To + "."
				if Less(r.To, r.From) {
					u.st.Result = "Switched back from " + r.From + " to " + r.To + "."
				}
			}
		}
		_ = os.Remove(filepath.Join(dir, "result.json"))
	}
	// a download from before a restart is still usable: Download checks it again
	return u
}

func keptVersion(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "previous", "version.txt"))
	if err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(dir, "previous", AppName)); err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Status returns a copy of the current state.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.st
	return s
}

func (u *Updater) set(f func(*Status)) {
	u.mu.Lock()
	f(&u.st)
	u.mu.Unlock()
}

type ghRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	HTMLURL    string `json:"html_url"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

var client = &http.Client{Timeout: 30 * time.Second}

func get(u string, limit int64) ([]byte, error) {
	if err := checkURL(u); err != nil {
		return nil, err
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "LocalSites/"+version.Version)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, &httpError{res.StatusCode, res.Status}
	}
	return io.ReadAll(io.LimitReader(res.Body, limit))
}

type httpError struct {
	code   int
	status string
}

func (h *httpError) Error() string { return "the server answered " + h.status }

func checkURL(raw string) error {
	p, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if p.Scheme == "https" || (AllowHTTP && p.Scheme == "http" && (p.Hostname() == "127.0.0.1" || p.Hostname() == "localhost")) {
		return nil
	}
	return fmt.Errorf("refusing to download over %s", p.Scheme)
}

// Check looks for the newest correctly signed release on the channel (stable or beta).
func (u *Updater) Check(channel string) Status {
	u.mu.Lock()
	if u.busy || u.st.Phase == Installing {
		s := u.st
		u.mu.Unlock()
		return s
	}
	prev := u.st.Phase
	u.st.Phase, u.st.Error, u.st.Message = Checking, "", ""
	u.mu.Unlock()

	m, raw, sig, rel, assets, err := u.newest(channel)
	now := time.Now()
	u.saveState(now)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.st.LastCheck = now
	switch {
	case err != nil:
		u.st.Phase, u.st.Error = Failed, err.Error()
		if prev == Ready { // a failed check doesn't throw away a finished download
			u.st.Phase = Ready
		}
	case m == nil:
		u.st.Phase, u.st.Latest, u.st.Message = Idle, nil, "You're up to date."
	case prev == Ready && u.st.Latest != nil && u.st.Latest.Version == m.Version:
		u.st.Phase = Ready
	default:
		f, ok := pick(m.Files)
		u.st.Latest, u.st.ReleaseURL, u.notes, u.sig, u.assets = m, rel, raw, sig, assets
		u.st.Phase, u.st.Message = Available, "LocalSites "+m.Version+" is available."
		u.file, u.st.Size = f, f.Size
		if !ok {
			u.st.Message = "LocalSites " + m.Version + " is available, but not yet for this Mac (" + runtime.GOOS + "/" + runtime.GOARCH + ")."
		}
		if u.ready(m.Version) {
			u.st.Phase = Ready
		}
	}
	return u.st
}

func (u *Updater) saveState(t time.Time) {
	_ = os.MkdirAll(u.Dir, 0o700)
	b, _ := json.Marshal(saved{LastCheck: t})
	_ = os.WriteFile(filepath.Join(u.Dir, "state.json"), b, 0o600)
}

// newest returns the highest release above the current version whose manifest is correctly signed.
// Releases with a missing or bad signature are skipped, never trusted.
func (u *Updater) newest(channel string) (*Manifest, []byte, []byte, string, map[string]string, error) {
	body, err := get(FeedURL, 4<<20)
	var he *httpError
	if errors.As(err, &he) && he.code == 404 {
		return nil, nil, nil, "", nil, errors.New("no public releases yet")
	}
	if err != nil {
		return nil, nil, nil, "", nil, fmt.Errorf("couldn't reach the release list (%v)", err)
	}
	var rels []ghRelease
	if err := json.Unmarshal(body, &rels); err != nil {
		return nil, nil, nil, "", nil, errors.New("the release list isn't readable")
	}
	var best *Manifest
	var bestRaw, bestSig []byte
	var bestURL string
	var bestAssets map[string]string
	var lastErr error
	for _, r := range rels {
		if r.Draft || (r.Prerelease && channel != "beta") {
			continue
		}
		v := strings.TrimPrefix(r.Tag, "v")
		if !Less(u.Current, v) || (best != nil && !Less(best.Version, v)) {
			continue
		}
		assets := map[string]string{}
		for _, a := range r.Assets {
			assets[a.Name] = a.URL
		}
		if assets["update.json"] == "" || assets["update.json.sig"] == "" {
			continue // a release without a signed manifest can't be installed from the app
		}
		raw, err := get(assets["update.json"], 1<<20)
		if err != nil {
			lastErr = err
			continue
		}
		sig, err := get(assets["update.json.sig"], 4096)
		if err != nil {
			lastErr = err
			continue
		}
		m, err := Verify(raw, sig)
		if err != nil {
			lastErr = fmt.Errorf("release %s: %v", v, err)
			continue
		}
		if m.Version != v {
			lastErr = fmt.Errorf("release %s: its manifest says %s", v, m.Version)
			continue
		}
		if m.Channel == "beta" && channel != "beta" { // signed as a beta: never offered as stable, whatever GitHub says
			continue
		}
		best, bestRaw, bestSig, bestURL, bestAssets = m, raw, sig, r.HTMLURL, assets
	}
	if best == nil && lastErr != nil {
		return nil, nil, nil, "", nil, lastErr
	}
	return best, bestRaw, bestSig, bestURL, bestAssets, nil
}

// Verify checks a manifest's signature with the built-in public key and parses it.
func Verify(raw, sig []byte) (*Manifest, error) {
	if PublicKey == "" {
		return nil, errors.New("this build has no update key")
	}
	pub, err := base64.StdEncoding.DecodeString(PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("the built-in update key is damaged")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(pub, raw, s) {
		return nil, errors.New("the signature doesn't match LocalSites' key, so it was ignored")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errors.New("the manifest isn't readable")
	}
	if m.Version == "" || len(m.Files) == 0 {
		return nil, errors.New("the manifest is incomplete")
	}
	return &m, nil
}

// Sign signs manifest bytes (used by the release tool).
func Sign(priv ed25519.PrivateKey, raw []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, raw)) + "\n")
}

func pick(files []File) (File, bool) {
	for _, f := range files {
		if f.OS == runtime.GOOS && f.Arch == runtime.GOARCH {
			return f, true
		}
	}
	for _, f := range files {
		if f.OS == runtime.GOOS && f.Arch == "universal" {
			return f, true
		}
	}
	return File{}, false
}

func (u *Updater) verDir(v string) string { return filepath.Join(u.Dir, "v"+v) }

// ready reports whether version v is downloaded, verified and unpacked (call with u.mu held).
func (u *Updater) ready(v string) bool {
	_, err := os.Stat(filepath.Join(u.verDir(v), "ok"))
	return err == nil
}

// Download fetches and unpacks the available update in the background. done is called when it finishes.
func (u *Updater) Download(done func(Status)) (Status, error) {
	u.mu.Lock()
	if u.st.Latest == nil || (u.st.Phase != Available && u.st.Phase != Failed) {
		s := u.st
		u.mu.Unlock()
		if s.Phase == Ready || s.Phase == Downloading {
			return s, nil
		}
		return s, errors.New("there's no update to download. Check for updates first")
	}
	if u.file.Name == "" {
		u.mu.Unlock()
		return u.Status(), errors.New("this release has no download for this Mac")
	}
	if !u.st.Supported {
		u.mu.Unlock()
		return u.Status(), errors.New("installing from the app works on macOS only for now. Download it from the release page")
	}
	m, f, link, raw, sig := u.st.Latest, u.file, u.assets[u.file.Name], u.notes, u.sig
	u.busy = true
	u.st.Phase, u.st.Done, u.st.Error = Downloading, 0, ""
	s := u.st
	u.mu.Unlock()
	go func() {
		err := u.fetch(m, f, link, raw, sig)
		u.mu.Lock()
		u.busy = false
		if err != nil {
			u.st.Phase, u.st.Error = Failed, err.Error()
		} else {
			u.st.Phase, u.st.Done, u.st.Message = Ready, f.Size, "LocalSites "+m.Version+" is ready to install."
		}
		st := u.st
		u.mu.Unlock()
		if done != nil {
			done(st)
		}
	}()
	return s, nil
}

func (u *Updater) fetch(m *Manifest, f File, link string, raw, sig []byte) error {
	if link == "" {
		return errors.New("the release has no " + f.Name)
	}
	if err := checkURL(link); err != nil {
		return err
	}
	dir := u.verDir(m.Version)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	zip := filepath.Join(dir, "update.zip")
	if err := u.download(link, zip, f); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	_ = os.WriteFile(filepath.Join(dir, "update.json"), raw, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "update.json.sig"), sig, 0o600)
	if err := unpack(zip, filepath.Join(dir, "app"), m.Version); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	return os.WriteFile(filepath.Join(dir, "ok"), []byte(m.Version), 0o600)
}

func (u *Updater) download(link, dst string, f File) error {
	req, _ := http.NewRequest("GET", link, nil)
	req.Header.Set("User-Agent", "LocalSites/"+version.Version)
	c := &http.Client{Timeout: 30 * time.Minute}
	res, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", res.Status)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	h := sha256.New()
	w := &progress{u: u}
	n, err := io.Copy(io.MultiWriter(out, h, w), io.LimitReader(res.Body, f.Size+1))
	if err != nil {
		return fmt.Errorf("download failed: %v", err)
	}
	if n != f.Size {
		return fmt.Errorf("the download is %d bytes, the signed manifest says %d; it was deleted", n, f.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, f.SHA256) {
		return errors.New("the download doesn't match the signed checksum; it was deleted")
	}
	return out.Close()
}

type progress struct{ u *Updater }

func (p *progress) Write(b []byte) (int, error) {
	p.u.set(func(s *Status) { s.Done += int64(len(b)) })
	return len(b), nil
}

// unpack extracts the zip with ditto (keeps code signatures and permissions) and checks the app inside.
func unpack(zip, dst, want string) error {
	_ = os.RemoveAll(dst)
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	if out, err := exec.Command("/usr/bin/ditto", "-x", "-k", zip, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("couldn't unpack the update: %s", strings.TrimSpace(string(out)))
	}
	ents, _ := os.ReadDir(dst)
	if len(ents) != 1 || ents[0].Name() != AppName {
		return errors.New("the update doesn't contain " + AppName)
	}
	return CheckBundle(filepath.Join(dst, AppName), want)
}

// CheckBundle makes sure path is a LocalSites app of version want (any version if want is "")
// that runs on this Mac.
func CheckBundle(app, want string) error {
	plist := filepath.Join(app, "Contents", "Info.plist")
	read := func(key string) string {
		out, _ := exec.Command("/usr/bin/plutil", "-extract", key, "raw", "-o", "-", plist).Output()
		return strings.TrimSpace(string(out))
	}
	if id := read("CFBundleIdentifier"); id != BundleID {
		return fmt.Errorf("%s is not LocalSites (bundle id %q)", filepath.Base(app), id)
	}
	if want != "" {
		if v := read("CFBundleShortVersionString"); v != want {
			return fmt.Errorf("the app inside says version %q, expected %s", v, want)
		}
	}
	bin := filepath.Join(app, "Contents", "MacOS", "LocalSites")
	cmd := exec.Command(bin, "update-selftest")
	cmd.Env = append(os.Environ(), "LOCALSITES_SELFTEST=1")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("the new app doesn't run on this Mac: %v", err)
	}
	if want != "" && strings.TrimSpace(string(out)) != want {
		return fmt.Errorf("the new app reports version %q, expected %s", strings.TrimSpace(string(out)), want)
	}
	return nil
}

// BundleOf turns the app's executable path into its .app folder.
func BundleOf(exe string) (string, error) {
	exe = filepath.Clean(exe)
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if filepath.Base(exe) != "LocalSites" || filepath.Base(filepath.Dir(exe)) != "MacOS" || !strings.HasSuffix(app, ".app") {
		return "", errors.New("LocalSites isn't running from its app here (perhaps the command-line tool). Update it the way you installed it")
	}
	if err := CheckBundle(app, ""); err != nil {
		return "", err
	}
	if err := writable(filepath.Dir(app)); err != nil {
		return "", fmt.Errorf("can't replace the app in %s: %v. Move LocalSites to Applications or your home folder", filepath.Dir(app), err)
	}
	return app, nil
}

func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".localsites-write-test-*")
	if err != nil {
		return errors.New("no permission to write there")
	}
	f.Close()
	return os.Remove(f.Name())
}

// Plan is what the helper does.
type Plan struct {
	From       string `json:"from"`        // the app to put in place
	To         string `json:"to"`          // where the running app is
	FromVer    string `json:"from_ver"`    // version of From
	ToVer      string `json:"to_ver"`      // version currently at To (kept for switching back)
	Wait       []int  `json:"wait"`        // processes that must quit first (the app, the service)
	Then       string `json:"then"`        // "open" (start the app), "open-bg" (start it without focus), "service" (start only the service), ""
	Dir        string `json:"dir"`         // the updates folder
	ConfigFrom string `json:"config_from"` // config backup made before the swap
}

// Result is what the helper leaves for the next start.
type Result struct {
	From  string    `json:"from"`
	To    string    `json:"to"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
}

// Prepare stages an install of the downloaded version (or a switch back to the kept one) and returns the
// plan the helper runs. It checks the staged app again right before use.
func (u *Updater) Prepare(target string, rollback bool) (Plan, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.st.Phase == Installing {
		return Plan{}, errors.New("an update is already being installed")
	}
	if !u.st.Supported {
		return Plan{}, errors.New("installing from the app works on macOS only for now")
	}
	inc := filepath.Join(u.Dir, "incoming")
	_ = os.RemoveAll(inc)
	if err := os.MkdirAll(inc, 0o700); err != nil {
		return Plan{}, err
	}
	p := Plan{To: target, ToVer: u.Current, Dir: u.Dir}
	if rollback {
		kept := keptVersion(u.Dir)
		if kept == "" {
			return Plan{}, errors.New("no other version is kept")
		}
		if err := CheckBundle(filepath.Join(u.Dir, "previous", AppName), kept); err != nil {
			return Plan{}, err
		}
		if err := move(filepath.Join(u.Dir, "previous", AppName), filepath.Join(inc, AppName)); err != nil {
			return Plan{}, err
		}
		p.FromVer = kept
	} else {
		if u.st.Latest == nil || !u.ready(u.st.Latest.Version) {
			return Plan{}, errors.New("the update isn't downloaded yet")
		}
		v := u.st.Latest.Version
		dir := u.verDir(v)
		// check the saved files again: signature, checksum, then unpack afresh
		raw, _ := os.ReadFile(filepath.Join(dir, "update.json"))
		sig, _ := os.ReadFile(filepath.Join(dir, "update.json.sig"))
		m, err := Verify(raw, sig)
		if err != nil || m.Version != v {
			return Plan{}, errors.New("the downloaded update no longer matches its signature; download it again")
		}
		f, ok := pick(m.Files)
		if !ok || hashFile(filepath.Join(dir, "update.zip")) != strings.ToLower(f.SHA256) {
			return Plan{}, errors.New("the downloaded update was changed on disk; download it again")
		}
		if err := unpack(filepath.Join(dir, "update.zip"), inc, v); err != nil {
			return Plan{}, err
		}
		p.FromVer = v
	}
	p.From = filepath.Join(inc, AppName)
	u.st.Phase = Installing
	return p, nil
}

// Cancel returns to the state before Prepare when the helper couldn't be started.
func (u *Updater) Cancel(p Plan, rollback bool) {
	if rollback {
		_ = move(p.From, filepath.Join(u.Dir, "previous", AppName))
	}
	u.set(func(s *Status) {
		s.Phase = Idle
		if s.Latest != nil && u.ready(s.Latest.Version) {
			s.Phase = Ready
		}
	})
}

func hashFile(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Spawn starts the helper (exe update-apply <plan file>) in its own session, so it outlives the service.
func Spawn(exe string, p Plan) error {
	b, _ := json.Marshal(p)
	pf := filepath.Join(p.Dir, "plan.json")
	if err := os.WriteFile(pf, b, 0o600); err != nil {
		return err
	}
	logf, _ := os.OpenFile(filepath.Join(p.Dir, "update.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	cmd := exec.Command(exe, "update-apply", pf)
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Apply runs in the helper: wait for the app and service to quit, keep the running app in
// updates/previous, put the new one in its place, and undo everything if a step fails.
func Apply(planFile string) (Plan, error) {
	var p Plan
	b, err := os.ReadFile(planFile)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	_ = os.Remove(planFile)
	res := Result{From: p.ToVer, To: p.FromVer, At: time.Now()}
	err = apply(p)
	if err != nil {
		res.Error = err.Error()
	}
	rb, _ := json.Marshal(res)
	_ = os.WriteFile(filepath.Join(p.Dir, "result.json"), rb, 0o600)
	return p, err
}

func apply(p Plan) error {
	if !strings.HasSuffix(p.To, ".app") || !strings.HasSuffix(p.From, ".app") {
		return errors.New("bad paths")
	}
	deadline := time.Now().Add(90 * time.Second)
	for _, pid := range p.Wait {
		for pid > 0 && alive(pid) {
			if time.Now().After(deadline) {
				return fmt.Errorf("LocalSites (process %d) didn't quit, so nothing was changed", pid)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	keep := filepath.Join(p.Dir, "previous")
	old := filepath.Join(keep, AppName)
	_ = os.RemoveAll(keep)
	if err := os.MkdirAll(keep, 0o700); err != nil {
		return err
	}
	if err := move(p.To, old); err != nil {
		return fmt.Errorf("couldn't move the current app aside: %v", err)
	}
	if err := move(p.From, p.To); err != nil {
		if rerr := move(old, p.To); rerr != nil {
			return fmt.Errorf("couldn't put the new app in place (%v) nor restore the old one (%v); it is in %s", err, rerr, old)
		}
		return fmt.Errorf("couldn't put the new app in place: %v; the old one was restored", err)
	}
	_ = os.WriteFile(filepath.Join(keep, "version.txt"), []byte(p.ToVer+"\n"), 0o600)
	_ = os.RemoveAll(filepath.Dir(p.From))
	// downloads of versions now installed or older are no longer needed
	ents, _ := os.ReadDir(p.Dir)
	for _, e := range ents {
		if v, ok := strings.CutPrefix(e.Name(), "v"); ok && e.IsDir() && !Less(p.FromVer, v) {
			_ = os.RemoveAll(filepath.Join(p.Dir, e.Name()))
		}
	}
	return nil
}

// move renames, or copies with ditto when the folders are on different volumes.
func move(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if out, err := exec.Command("/usr/bin/ditto", from, to).CombinedOutput(); err != nil {
		_ = os.RemoveAll(to)
		return errors.New(strings.TrimSpace(string(bytes.TrimSpace(out))))
	}
	return os.RemoveAll(from)
}

// Less compares dotted versions numerically (0.9.0 < 0.10.0); a pre-release suffix ("-beta.1") sorts first.
func Less(a, b string) bool {
	ca, pa := splitPre(a)
	cb, pb := splitPre(b)
	x, y := strings.Split(ca, "."), strings.Split(cb, ".")
	for i := 0; i < len(x) || i < len(y); i++ {
		var m, n int
		if i < len(x) {
			m, _ = strconv.Atoi(x[i])
		}
		if i < len(y) {
			n, _ = strconv.Atoi(y[i])
		}
		if m != n {
			return m < n
		}
	}
	switch {
	case pa == pb:
		return false
	case pa == "":
		return false
	case pb == "":
		return true
	}
	return pa < pb
}

func splitPre(v string) (string, string) {
	c, p, _ := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	return c, p
}
