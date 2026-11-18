package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/localsites/localsites/internal/addons"
	"github.com/localsites/localsites/internal/notices"
	"github.com/localsites/localsites/internal/runtime"
)

// Setup finds what LocalSites needs from Homebrew (Apache, MySQL, the PHP versions projects use) and
// what is installed but broken, and fixes all of it with one click: nobody should need a terminal.

// phpTap has every PHP version from 5.6 up; Homebrew's own repository only has the newest few.
const phpTap = "shivammathur/php"

// SetupItem is one thing to install or repair.
type SetupItem struct {
	ID       string   `json:"id"`       // the formula: httpd, mysql@8.4, nginx, php, php@7.4
	Title    string   `json:"title"`    // "PHP 7.4"
	Why      string   `json:"why"`      // "can't start: libjpeg.9.dylib is missing · 15 projects use it"
	Kind     string   `json:"kind"`     // install | repair | system (a one-time change to macOS settings)
	Required bool     `json:"required"` // projects can't run without it
	Password bool     `json:"password"` // macOS asks for the Mac password once (files owned by root)
	Steps    []string `json:"steps"`    // the commands that will run, shown before they do
}

// SetupView is what the app shows.
type SetupView struct {
	Homebrew bool              `json:"homebrew"`
	Items    []SetupItem       `json:"items"`
	Running  bool              `json:"running"`
	Step     string            `json:"step,omitempty"`    // what is happening now
	Results  map[string]string `json:"results,omitempty"` // id: "ok" or what went wrong (last run)
	Log      string            `json:"log"`
}

type setupState struct {
	mu      sync.Mutex
	running bool
	step    string
	results map[string]string
}

// step is one command of a fix.
type step struct {
	label string   // shown to the person
	brew  []string // brew arguments
	chown []string // paths to give back to the person (asks for the password)
	clean []string // leftover folders to delete after chown (a reinstall that stopped half way)
	// domains: send .test lookups to LocalSites and trust its certificate (asks for the password)
	domains *domainsStep
}

type planItem struct {
	SetupItem
	steps []step
}

// planInput is everything the plan depends on, so tests can build it without Homebrew.
type planInput struct {
	apache, mysql, nginx bool // installed
	needNginx            bool
	php                  []runtime.PHP
	broken               []runtime.BrokenPHP
	users                map[string]int        // PHP version: projects set to it
	tapped, trust        bool                  // phpTap is tapped; this Homebrew has "brew trust"
	tapOf                func(f string) string // tap a formula was installed from ("" for Homebrew's own)
	rootOwned            func(f string) (paths, leftovers []string)
	domains              domainsState
}

func formulaFor(v string) string {
	if v == "latest" {
		return "php"
	}
	return "php@" + v
}

func buildPlan(in planInput) []planItem {
	var out []planItem
	if it, ok := domainsItem(in.domains); ok {
		out = append(out, it)
	}
	core := func(id, title, why string, required bool) {
		out = append(out, planItem{SetupItem{ID: id, Title: title, Why: why, Kind: "install", Required: required},
			[]step{{label: "brew install " + id, brew: []string{"install", id}}}})
	}
	if !in.apache {
		core("httpd", "Apache", "runs PHP projects", true)
	}
	if !in.mysql {
		core("mysql@8.4", "MySQL 8.4", "LocalSites’ own database server", false)
	}
	if in.needNginx && !in.nginx {
		core("nginx", "Nginx", "some projects are set to use Nginx", true)
	}
	if len(in.php) == 0 && len(in.broken) == 0 {
		core("php", "PHP", "no PHP is installed", true)
	}
	tapSteps := func(full string) []step {
		var s []step
		if !in.tapped {
			s = append(s, step{label: "brew tap " + phpTap, brew: []string{"tap", phpTap}})
		}
		if in.trust { // the whole tap: its formulas load each other (php@8.5 needs shivammathur/php/php)
			s = append(s, step{label: "brew trust " + phpTap, brew: []string{"trust", phpTap}})
		}
		return s
	}
	used := func(v string) string {
		if n := in.users[v]; n > 0 {
			if n == 1 {
				return " · 1 project uses it"
			}
			return fmt.Sprintf(" · %d projects use it", n)
		}
		return ""
	}
	repair := func(version, why string) {
		f := formulaFor(version)
		full := f
		var steps []step
		if tap := in.tapOf(f); tap != "" {
			full = tap + "/" + f
			if in.trust {
				steps = append(steps, step{label: "brew trust " + tap, brew: []string{"trust", tap}})
			}
		}
		paths, left := in.rootOwned(f)
		if len(paths) > 0 {
			// a reinstall can't replace files that belong to root (an install once run with sudo)
			steps = append([]step{{label: "give " + f + "’s files back to you (asks for your Mac password)", chown: paths, clean: left}}, steps...)
		} else if len(left) > 0 {
			steps = append([]step{{label: "remove a half-finished reinstall of " + f, clean: left}}, steps...)
		}
		steps = append(steps, step{label: "brew reinstall " + full, brew: []string{"reinstall", full}})
		title := "PHP " + version
		if version == "latest" {
			title = "PHP (latest)"
		}
		out = append(out, planItem{SetupItem{ID: f, Title: title, Why: why + used(version), Kind: "repair", Required: in.users[version] > 0, Password: len(paths) > 0}, steps})
	}
	have := map[string]bool{}
	for _, b := range in.broken {
		have[b.Version] = true
		repair(b.Version, "can’t start: "+b.Reason)
	}
	for _, p := range in.php {
		have[p.Version] = true
		if p.Warning != "" {
			why, _, _ := strings.Cut(p.Warning, ". Run:")
			repair(p.Version, why)
		}
	}
	var missing []string
	for v, n := range in.users {
		if n > 0 && !have[v] && validPHPVersion.MatchString(v) {
			missing = append(missing, v)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	for _, v := range missing {
		full := phpTap + "/php@" + v
		steps := append(tapSteps(full), step{label: "brew install " + full, brew: []string{"install", full}})
		out = append(out, planItem{SetupItem{ID: "php@" + v, Title: "PHP " + v, Why: "not installed" + used(v), Kind: "install", Required: true}, steps})
	}
	for i := range out {
		for _, s := range out[i].steps {
			out[i].Steps = append(out[i].Steps, s.label)
		}
	}
	return out
}

var (
	validPHPVersion = regexp.MustCompile(`^\d\.\d{1,2}$`)
	validFormula    = regexp.MustCompile(`^[a-z0-9][a-z0-9@._+-]*$`)
	validTapName    = regexp.MustCompile(`^[A-Za-z0-9_-]+/[A-Za-z0-9_-]+$`)
)

// noBrew: test sandboxes never install or change software on the Mac running them.
func noBrew() bool { return os.Getenv("LOCALSITES_NO_BREW") == "1" }

// brewPrefix is /opt/homebrew (Apple silicon) or /usr/local.
func brewPrefix(brew string) string { return filepath.Dir(filepath.Dir(brew)) }

var (
	trustOnce sync.Once
	trustOK   bool
)

func brewHasTrust(brew string) bool {
	trustOnce.Do(func() {
		cmd := exec.Command(brew, "trust", "--help")
		cmd.Env = brewEnv(brew, "")
		trustOK = cmd.Run() == nil
	})
	return trustOK
}

func tapOfFormula(prefix string) func(string) string {
	return func(f string) string {
		var r struct {
			Source struct {
				Tap string `json:"tap"`
			} `json:"source"`
		}
		b, err := os.ReadFile(filepath.Join(prefix, "opt", f, "INSTALL_RECEIPT.json"))
		if err != nil || json.Unmarshal(b, &r) != nil || r.Source.Tap == "homebrew/core" || !validTapName.MatchString(r.Source.Tap) {
			return ""
		}
		return r.Source.Tap
	}
}

// rootOwnedIn finds a formula's folders that hold files the person doesn't own, and folders a
// stopped reinstall left behind (Cellar/php@7.2/7.2.34_14.reinstall).
func rootOwnedIn(prefix string) func(string) ([]string, []string) {
	uid := uint32(os.Getuid())
	return func(f string) (paths, left []string) {
		if !validFormula.MatchString(f) {
			return nil, nil
		}
		cellar := filepath.Join(prefix, "Cellar", f)
		kegs, _ := os.ReadDir(cellar)
		for _, k := range kegs {
			dir := filepath.Join(cellar, k.Name())
			if strings.HasSuffix(k.Name(), ".reinstall") {
				left = append(left, dir)
			}
			_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if info, err := d.Info(); err == nil {
					if owner, ok := fileOwner(info); ok && owner != uid {
						paths = append(paths, cellar)
						return fs.SkipAll
					}
				}
				return nil
			})
			if len(paths) > 0 {
				break
			}
		}
		return paths, left
	}
}

func brewEnv(brew, home string) []string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return []string{"PATH=" + filepath.Dir(brew) + ":/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + home,
		"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_INSTALL_CLEANUP=1", "HOMEBREW_NO_ENV_HINTS=1", "NONINTERACTIVE=1"}
}

func (e *Engine) plan() ([]planItem, bool) {
	brew, ok := addons.Brew()
	in := planInput{users: map[string]int{}, tapOf: func(string) string { return "" }, rootOwned: func(string) ([]string, []string) { return nil, nil }}
	_, _, _, in.apache = runtime.Apache()
	_, in.mysql = runtime.MySQL()
	_, _, _, in.nginx = runtime.Nginx()
	in.php, in.broken = runtime.PHPVersions(), runtime.BrokenPHPVersions()
	in.domains = e.domainsState()
	for _, s := range e.Sites() {
		if s.Runtime == "php" && s.ServedBy == "" {
			in.users[s.PHP]++
			if s.Server == "nginx" && s.Enabled {
				in.needNginx = true
			}
		}
	}
	if ok {
		prefix := brewPrefix(brew)
		in.tapOf, in.rootOwned = tapOfFormula(prefix), rootOwnedIn(prefix)
		in.trust = brewHasTrust(brew)
		_, err := os.Stat(filepath.Join(prefix, "Library", "Taps", "shivammathur", "homebrew-php"))
		in.tapped = err == nil
	}
	return buildPlan(in), ok
}

func (e *Engine) setupLog() string { return filepath.Join(e.P.Logs, "setup.log") }

// Setup lists what is missing or broken, and the progress of a running fix.
func (e *Engine) Setup() SetupView {
	items, brew := e.plan()
	v := SetupView{Homebrew: brew, Items: []SetupItem{}, Log: e.setupLog()}
	for _, it := range items {
		v.Items = append(v.Items, it.SetupItem)
	}
	e.setup.mu.Lock()
	v.Running, v.Step = e.setup.running, e.setup.step
	if len(e.setup.results) > 0 {
		v.Results = map[string]string{}
		for k, r := range e.setup.results {
			v.Results[k] = r
		}
	}
	e.setup.mu.Unlock()
	return v
}

// setupExec runs one brew command (replaced in tests).
var setupExec = func(ctx context.Context, brew string, args []string, log io.Writer) error {
	cmd := exec.CommandContext(ctx, brew, args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = brewEnv(brew, ""), log, log
	return cmd.Run()
}

// setupApply looks for PHP again and restarts what the fixes touched (replaced in tests).
var setupApply = func(e *Engine) {
	runtime.ForgetBroken()
	e.Apply()
}

// setupChown gives folders back to the person, after the system's own password prompt (replaced in tests).
var setupChown = func(ctx context.Context, paths []string) error {
	owner := fmt.Sprintf("%d:admin", os.Getuid()) // macOS: Homebrew's files belong to the admin group
	if goruntime.GOOS != "darwin" {
		owner = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	}
	_, err := runAdmin(ctx, "LocalSites needs your password to fix file permissions in Homebrew, so it can repair PHP.",
		`owner="$1"; shift; exec chown -R "$owner" "$@"`, append([]string{owner}, paths...)...)
	if err != nil {
		return fmt.Errorf("couldn't change file owners: %v", err)
	}
	return nil
}

// RunSetup installs and repairs the chosen items (all of them when ids is empty) in the background.
func (e *Engine) RunSetup(ids []string) (SetupView, error) {
	if noBrew() {
		return e.Setup(), errors.New("installing with Homebrew is turned off here (LOCALSITES_NO_BREW)")
	}
	brew, ok := addons.Brew()
	items, _ := e.plan()
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var chosen []planItem
	for _, it := range items {
		if len(ids) == 0 || want[it.ID] {
			chosen = append(chosen, it)
		}
	}
	if len(chosen) == 0 {
		return e.Setup(), errors.New("nothing to install or repair")
	}
	if !ok {
		for _, it := range chosen {
			for _, s := range it.steps {
				if len(s.brew) > 0 {
					return e.Setup(), errors.New("Homebrew is needed first: use Install Homebrew")
				}
			}
		}
	}
	e.setup.mu.Lock()
	if e.setup.running {
		e.setup.mu.Unlock()
		return e.Setup(), errors.New("setup is already running")
	}
	e.setup.running, e.setup.results = true, map[string]string{}
	e.setup.mu.Unlock()
	prefix := ""
	if ok {
		prefix = brewPrefix(brew)
	}
	go e.runSetup(brew, prefix, chosen)
	return e.Setup(), nil
}

func (e *Engine) setStep(s string) {
	e.setup.mu.Lock()
	e.setup.step = s
	e.setup.mu.Unlock()
	e.Changes.Bump()
}

func (e *Engine) runSetup(brew, prefix string, items []planItem) {
	_ = os.MkdirAll(e.P.Logs, 0o700)
	log, err := os.OpenFile(e.setupLog(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		log, _ = os.Open(os.DevNull)
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	cellar := filepath.Join(prefix, "Cellar") + string(os.PathSeparator)
	if prefix == "" {
		cellar = "\x00" // no Homebrew: nothing can be inside its Cellar
	}
	for _, it := range items {
		var failed error
		for _, s := range it.steps {
			e.setStep(it.Title + ": " + s.label)
			fmt.Fprintf(log, "\n==> %s: %s\n", it.Title, s.label)
			switch {
			case s.domains != nil:
				failed = setupDomains(ctx, *s.domains)
			case len(s.chown) > 0:
				for _, p := range s.chown {
					if !strings.HasPrefix(filepath.Clean(p)+string(os.PathSeparator), cellar) {
						failed = fmt.Errorf("refusing to change owners outside Homebrew: %s", p)
					}
				}
				if failed == nil {
					failed = setupChown(ctx, s.chown)
				}
			case len(s.brew) > 0:
				failed = setupExec(ctx, brew, s.brew, log)
			}
			if failed == nil {
				for _, p := range s.clean {
					if strings.HasPrefix(p, cellar) && strings.HasSuffix(p, ".reinstall") {
						if err := os.RemoveAll(p); err != nil {
							failed = fmt.Errorf("couldn't remove %s: %v", p, err)
						}
					}
				}
			}
			if failed != nil {
				fmt.Fprintf(log, "failed: %v\n", failed)
				break
			}
		}
		e.setup.mu.Lock()
		if failed != nil {
			e.setup.results[it.ID] = failed.Error()
		} else {
			e.setup.results[it.ID] = "ok"
		}
		e.setup.mu.Unlock()
	}
	e.setStep("Starting projects")
	setupApply(e)
	e.setup.mu.Lock()
	e.setup.running, e.setup.step = false, ""
	var failed []string
	for _, it := range items {
		if r := e.setup.results[it.ID]; r != "ok" {
			failed = append(failed, it.Title)
		}
	}
	e.setup.mu.Unlock()
	if len(failed) == 0 {
		e.Notify(notices.Notice{Kind: notices.KindSetup, Title: "Everything is installed and repaired", Text: "Your projects were restarted.", Open: "view:overview"})
	} else {
		e.Notify(notices.Notice{Kind: notices.KindSetup, Title: "Setup finished, but some steps didn't work", Text: strings.Join(failed, ", ") + ". Overview has the details.", Open: "view:overview"})
	}
	e.Changes.Bump()
}

// InstallHomebrew downloads Homebrew's official installer and opens it. macOS checks the package is
// signed and notarized by Apple before LocalSites opens it; the installer asks for the Mac password.
func (e *Engine) InstallHomebrew() (string, error) {
	if noBrew() {
		return "", errors.New("installing with Homebrew is turned off here (LOCALSITES_NO_BREW)")
	}
	if _, ok := addons.Brew(); ok {
		return "", errors.New("Homebrew is already installed")
	}
	e.setup.mu.Lock()
	if e.setup.running {
		e.setup.mu.Unlock()
		return "", errors.New("setup is already running")
	}
	e.setup.running = true
	e.setup.mu.Unlock()
	go func() {
		err := e.fetchHomebrew()
		e.setup.mu.Lock()
		e.setup.running, e.setup.step = false, ""
		switch {
		case err != nil:
			e.setup.results = map[string]string{"homebrew": err.Error()}
		case goruntime.GOOS == "linux":
			e.setup.results = map[string]string{"homebrew": "ok"} // installed: the next step shows by itself
		default:
			e.setup.results = map[string]string{"homebrew": "opened"}
		}
		e.setup.mu.Unlock()
		e.Changes.Bump()
	}()
	return "Downloading Homebrew…", nil
}

var homebrewRelease = "https://api.github.com/repos/Homebrew/brew/releases/latest"

func (e *Engine) fetchHomebrew() error {
	if goruntime.GOOS == "linux" {
		return e.installHomebrewLinux()
	}
	e.setStep("Finding the latest Homebrew")
	c := &http.Client{Timeout: 30 * time.Second}
	res, err := c.Get(homebrewRelease)
	if err != nil {
		return fmt.Errorf("couldn't reach GitHub: %v", err)
	}
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&rel)
	res.Body.Close()
	if err != nil {
		return errors.New("GitHub's answer about Homebrew wasn't readable")
	}
	url := ""
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, ".pkg") && strings.HasPrefix(a.URL, "https://github.com/Homebrew/brew/releases/download/") {
			url = a.URL
		}
	}
	if url == "" {
		return errors.New("Homebrew's latest release has no installer package; see https://brew.sh")
	}
	e.setStep("Downloading Homebrew")
	dir, err := os.MkdirTemp("", "localsites-homebrew-")
	if err != nil {
		return err
	}
	pkg := filepath.Join(dir, "Homebrew.pkg")
	res, err = (&http.Client{Timeout: 15 * time.Minute}).Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", res.Status)
	}
	f, err := os.OpenFile(pkg, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(res.Body, 1<<30))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download failed: %v", err)
	}
	e.setStep("Checking Homebrew's signature")
	out, err := exec.Command("/usr/sbin/spctl", "--assess", "--type", "install", "-vv", pkg).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "source=Notarized Developer ID") {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("the download isn't signed and notarized as Homebrew's installer should be, so it was deleted: %s", strings.TrimSpace(string(out)))
	}
	e.setStep("Opening the Homebrew installer")
	if err := exec.Command("/usr/bin/open", pkg).Run(); err != nil {
		return fmt.Errorf("couldn't open the installer: %v", err)
	}
	return nil
}

// Homebrew on Linux: one password prompt creates /home/linuxbrew for the person (the only place Homebrew's
// prebuilt packages work) and installs the few tools it needs; then Homebrew's official install script
// runs as the person, without a terminal and without sudo.

const linuxBrewPrepScript = `set -e
mkdir -p /home/linuxbrew/.linuxbrew
chown -R "$1" /home/linuxbrew
need=""
for t in git curl file ps; do command -v "$t" >/dev/null 2>&1 || need=1; done
if [ -n "$need" ] || [ "$2" = 1 ]; then
  if command -v apt-get >/dev/null 2>&1; then
    tools="git curl file procps ca-certificates"; [ "$2" = 1 ] && tools="$tools build-essential"
    apt-get update -q >/dev/null && apt-get install -y -q $tools >/dev/null
  elif command -v dnf >/dev/null 2>&1; then
    tools="git curl file procps-ng"; [ "$2" = 1 ] && tools="$tools gcc gcc-c++ make"
    dnf install -y -q $tools >/dev/null
  elif command -v pacman >/dev/null 2>&1; then
    tools="git curl file procps-ng"; [ "$2" = 1 ] && tools="$tools base-devel"
    pacman -S --noconfirm --needed $tools >/dev/null
  fi
fi`

// homebrewInstallScript is Homebrew's official installer (https://brew.sh); tests replace it.
var homebrewInstallScript = "https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh"

// needsBuildTools: Homebrew's prebuilt packages expect glibc 2.39 or newer (Ubuntu 24.04); older systems
// (Debian 12, glibc 2.36) get Homebrew's own glibc and gcc, which need a system compiler to set up.
func needsBuildTools(glibc string) bool {
	parts := strings.SplitN(strings.TrimSpace(glibc), ".", 3)
	if len(parts) < 2 {
		return true
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	return major < 2 || (major == 2 && minor < 39)
}

func glibcVersion() string {
	out, _ := exec.Command("getconf", "GNU_LIBC_VERSION").Output() // "glibc 2.39"
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "glibc ")
}

func (e *Engine) installHomebrewLinux() error {
	_ = os.MkdirAll(e.P.Logs, 0o700)
	log, err := os.OpenFile(e.setupLog(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	e.setStep("Preparing Homebrew’s folder (asks for your password)")
	build := "0"
	if needsBuildTools(glibcVersion()) {
		build = "1"
	}
	owner := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if out, err := runAdmin(ctx, "LocalSites needs your password once to install Homebrew, which provides PHP, Apache and MySQL.", linuxBrewPrepScript, owner, build); err != nil {
		fmt.Fprintf(log, "preparing failed: %v\n%s\n", err, out)
		return err
	}

	e.setStep("Downloading Homebrew’s installer")
	res, err := (&http.Client{Timeout: 2 * time.Minute}).Get(homebrewInstallScript)
	if err != nil {
		return fmt.Errorf("couldn't download Homebrew's installer: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("couldn't download Homebrew's installer: %s", res.Status)
	}
	dir, err := os.MkdirTemp("", "localsites-homebrew-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "install.sh")
	f, err := os.OpenFile(script, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(res.Body, 4<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}

	e.setStep("Installing Homebrew (a few minutes)")
	home, _ := os.UserHomeDir()
	env := []string{"NONINTERACTIVE=1", "HOME=" + home, "PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin", "HOMEBREW_NO_ANALYTICS=1", "LANG=C.UTF-8"}
	var last error
	for try := 1; try <= 2; try++ { // the first "brew update" sometimes fails on a slow network
		cmd := exec.CommandContext(ctx, "/bin/bash", script)
		cmd.Env, cmd.Stdout, cmd.Stderr, cmd.Dir = env, log, log, dir
		if last = cmd.Run(); last == nil {
			break
		}
		fmt.Fprintf(log, "\n==> try %d failed: %v\n", try, last)
	}
	if last != nil {
		return fmt.Errorf("Homebrew's installer stopped (see the setup log): %v", last)
	}
	if _, ok := addons.Brew(); !ok {
		return errors.New("Homebrew's installer finished, but brew isn't in /home/linuxbrew/.linuxbrew")
	}
	return nil
}
