// Package runtime finds the server software LocalSites runs: PHP, Apache, MySQL.
// Phase 1 uses Homebrew on macOS. Other providers (static PHP builds, Linux packages,
// Windows zips) plug in behind the same functions later.
package runtime

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var brewPrefixes = []string{"/opt/homebrew", "/usr/local"}

// PHP is one installed PHP version.
type PHP struct {
	Version string `json:"version"` // "8.5"
	Full    string `json:"full"`    // "8.5.12"
	FPM     string `json:"fpm"`
	CLI     string `json:"cli"`
	Warning string `json:"warning,omitempty"`
}

// BrokenPHP is an installed PHP build that can't start (usually a library Homebrew removed).
type BrokenPHP struct {
	Version string `json:"version"` // from the folder name: "7.2"
	Path    string `json:"path"`
	Reason  string `json:"reason"` // "libXpm.4.dylib is missing"
	Fix     string `json:"fix"`    // a command
}

var (
	phpOnce   sync.Mutex
	phpCache  []PHP
	phpBroken []BrokenPHP
	phpSig    string
	verRe     = regexp.MustCompile(`^\d+\.\d+\.\d+`)
)

// PHPVersions lists usable PHP versions, oldest first. Results are cached for a minute.
func PHPVersions() []PHP {
	phpOnce.Lock()
	defer phpOnce.Unlock()
	// Probing runs every PHP binary, so only redo it when the installed builds change (brew install/upgrade)
	sig := phpSignature()
	if phpCache != nil && sig == phpSig {
		return phpCache
	}
	seen := map[string]PHP{}
	var broken []BrokenPHP
	for _, prefix := range brewPrefixes {
		dirs, _ := filepath.Glob(filepath.Join(prefix, "opt", "php@*"))
		dirs = append(dirs, filepath.Join(prefix, "opt", "php"))
		for _, dir := range dirs {
			fpm, cli := filepath.Join(dir, "sbin", "php-fpm"), filepath.Join(dir, "bin", "php")
			if !isExec(fpm) || !isExec(cli) {
				continue
			}
			if b, ok := knownBroken(dir, fpm); ok {
				broken = append(broken, b)
				continue // don't start it again: each try leaves a macOS crash report
			}
			full, startErr := runCombined(cli, "-n", "-r", "echo PHP_VERSION;")
			if !verRe.MatchString(full) {
				b := brokenBuild(dir, cli, startErr+full)
				broken = append(broken, b)
				rememberBroken(dir, fpm, b)
				continue
			}
			mm := strings.Join(strings.SplitN(full, ".", 3)[:2], ".")
			if _, dup := seen[mm]; dup && filepath.Base(dir) == "php" {
				continue // prefer the versioned php@X.Y path
			}
			p := PHP{Version: mm, Full: verRe.FindString(full), FPM: fpm, CLI: cli}
			// startup warnings go to stdout for the CLI, so read both streams
			if out, errOut := runCombined(cli, "-v"); strings.Contains(out+errOut, "Unable to load") || strings.Contains(out+errOut, "Warning") {
				p.Warning = extensionWarning(out+errOut, filepath.Base(dir))
			}
			seen[mm] = p
		}
	}
	list := make([]PHP, 0, len(seen))
	for _, p := range seen {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return versionLess(list[i].Version, list[j].Version) })
	phpCache, phpBroken, phpSig = list, broken, sig
	return list
}

// BrokenPHPVersions lists installed PHP builds that can't start.
func BrokenPHPVersions() []BrokenPHP {
	PHPVersions()
	phpOnce.Lock()
	defer phpOnce.Unlock()
	return append([]BrokenPHP(nil), phpBroken...)
}

var (
	missingLib = regexp.MustCompile(`Library not loaded: (\S+)`)
	failedExt  = regexp.MustCompile(`Unable to load dynamic library '([^']+)'`)
	optFormula = regexp.MustCompile(`/opt/([A-Za-z0-9@._+-]+)/lib/`)
)

// brokenBuild explains why a PHP build didn't start, from dyld's message.
func brokenBuild(dir, cli, out string) BrokenPHP {
	formula := filepath.Base(dir) // php@7.2
	b := BrokenPHP{Version: strings.TrimPrefix(formula, "php@"), Path: cli, Reason: "it doesn't start", Fix: "brew reinstall " + formula}
	if formula == "php" {
		b.Version = "latest"
	}
	if m := missingLib.FindStringSubmatch(out); m != nil {
		b.Reason = filepath.Base(m[1]) + " is missing"
		if f := optFormula.FindStringSubmatch(m[1]); f != nil {
			b.Fix = "brew install " + f[1] + " (or: brew reinstall " + formula + ")"
		}
	}
	return b
}

// extensionWarning names the extensions that fail to load and, when it can tell, the missing library.
func extensionWarning(out, formula string) string {
	var exts []string
	for _, m := range failedExt.FindAllStringSubmatch(out, -1) {
		exts = append(exts, strings.TrimSuffix(filepath.Base(m[1]), ".so"))
	}
	if len(exts) == 0 {
		return "PHP prints a warning when it starts; run: brew reinstall " + formula
	}
	w := strings.Join(exts, ", ") + " can't load"
	if m := missingLib.FindStringSubmatch(out); m != nil { // the library dyld couldn't find, not the extension's own path
		if f := optFormula.FindStringSubmatch(m[1]); f != nil {
			return w + " (" + f[1] + " is missing). Run: brew install " + f[1] + " (or: brew reinstall " + formula + ")"
		}
	}
	return w + ". Run: brew reinstall " + formula
}

// A build that crashed at launch is remembered for an hour (per build), so LocalSites, the CLI and
// the app don't start it again and again: macOS writes a crash report every time.
type brokenEntry struct {
	Key   string    `json:"key"`
	Until time.Time `json:"until"`
	B     BrokenPHP `json:"b"`
}

func brokenFile() string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "LocalSites", "php-broken.json")
}

func buildKey(dir, fpm string) string {
	real, _ := filepath.EvalSymlinks(dir)
	k := real
	if st, err := os.Stat(fpm); err == nil {
		k += "|" + st.ModTime().UTC().Format(time.RFC3339Nano)
	}
	return k
}

func readBroken() map[string]brokenEntry {
	m := map[string]brokenEntry{}
	if f := brokenFile(); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			_ = json.Unmarshal(b, &m)
		}
	}
	return m
}

func knownBroken(dir, fpm string) (BrokenPHP, bool) {
	e, ok := readBroken()[dir]
	if !ok || e.Key != buildKey(dir, fpm) || time.Now().After(e.Until) {
		return BrokenPHP{}, false
	}
	return e.B, true
}

func rememberBroken(dir, fpm string, b BrokenPHP) {
	f := brokenFile()
	if f == "" {
		return
	}
	m := readBroken()
	m[dir] = brokenEntry{Key: buildKey(dir, fpm), Until: time.Now().Add(time.Hour), B: b}
	if out, err := json.Marshal(m); err == nil {
		_ = os.MkdirAll(filepath.Dir(f), 0o700)
		_ = os.WriteFile(f, out, 0o600)
	}
}

// phpSignature names every PHP build and its modification time: cheap stat calls only.
func phpSignature() string {
	var b strings.Builder
	for _, prefix := range brewPrefixes {
		dirs, _ := filepath.Glob(filepath.Join(prefix, "opt", "php@*"))
		for _, dir := range append(dirs, filepath.Join(prefix, "opt", "php")) {
			real, _ := filepath.EvalSymlinks(dir)
			b.WriteString(real)
			if st, err := os.Stat(filepath.Join(dir, "sbin", "php-fpm")); err == nil {
				b.WriteString(st.ModTime().String())
			}
			b.WriteByte(0)
		}
	}
	return b.String()
}

// FindPHP returns the installed version "X.Y".
func FindPHP(v string) (PHP, bool) {
	for _, p := range PHPVersions() {
		if p.Version == v {
			return p, true
		}
	}
	return PHP{}, false
}

// Closest returns the installed version matching X.Y of full, else the next newer one.
func ClosestPHP(full string) string {
	parts := strings.SplitN(full, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	want := parts[0] + "." + parts[1]
	for _, p := range PHPVersions() {
		if p.Version == want || versionLess(want, p.Version) {
			return p.Version
		}
	}
	return ""
}

// Apache returns the httpd binary and its module dir.
func Apache() (bin, modules, mimeTypes string, ok bool) {
	for _, prefix := range brewPrefixes {
		bin = filepath.Join(prefix, "opt", "httpd", "bin", "httpd")
		if isExec(bin) {
			return bin, filepath.Join(prefix, "opt", "httpd", "lib", "httpd", "modules"), filepath.Join(prefix, "etc", "httpd", "mime.types"), true
		}
	}
	return "", "", "", false
}

// MySQL returns the base dir of MySQL 8.4 LTS.
// Nginx finds nginx and its mime.types and fastcgi_params (Homebrew).
func Nginx() (bin, mime, fastcgi string, ok bool) {
	for _, prefix := range brewPrefixes {
		bin = filepath.Join(prefix, "opt", "nginx", "bin", "nginx")
		if !isExec(bin) {
			bin = filepath.Join(prefix, "bin", "nginx")
		}
		etc := filepath.Join(prefix, "etc", "nginx")
		if isExec(bin) {
			return bin, filepath.Join(etc, "mime.types"), filepath.Join(etc, "fastcgi_params"), true
		}
	}
	return "", "", "", false
}

func MySQL() (base string, ok bool) {
	for _, prefix := range brewPrefixes {
		base = filepath.Join(prefix, "opt", "mysql@8.4")
		if isExec(filepath.Join(base, "bin", "mysqld")) {
			return base, true
		}
	}
	return "", false
}

// Valet returns the valet binary if Laravel Valet is installed.
func Valet() (string, bool) {
	for _, prefix := range brewPrefixes {
		b := filepath.Join(prefix, "bin", "valet")
		if isExec(b) {
			return b, true
		}
	}
	return "", false
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func run(name string, args ...string) string {
	out, _ := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out))
}

// runCombined returns stdout and stderr separately (both trimmed of surrounding space).
func runCombined(name string, args ...string) (string, string) {
	cmd := exec.Command(name, args...)
	var o, e strings.Builder
	cmd.Stdout, cmd.Stderr = &o, &e
	_ = cmd.Run()
	return strings.TrimSpace(o.String()), strings.TrimSpace(e.String())
}

func runStderr(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var b strings.Builder
	cmd.Stderr = &b
	_ = cmd.Run()
	return b.String()
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return atoi(pa[i]) < atoi(pb[i])
		}
	}
	return len(pa) < len(pb)
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}
