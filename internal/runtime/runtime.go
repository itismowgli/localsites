// Package runtime finds the server software LocalSites runs: PHP, Apache, MySQL.
// Phase 1 uses Homebrew on macOS. Other providers (static PHP builds, Linux packages,
// Windows zips) plug in behind the same functions later.
package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
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

var (
	phpOnce  sync.Mutex
	phpCache []PHP
	phpSig   string
	verRe    = regexp.MustCompile(`^\d+\.\d+\.\d+`)
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
	for _, prefix := range brewPrefixes {
		dirs, _ := filepath.Glob(filepath.Join(prefix, "opt", "php@*"))
		dirs = append(dirs, filepath.Join(prefix, "opt", "php"))
		for _, dir := range dirs {
			fpm, cli := filepath.Join(dir, "sbin", "php-fpm"), filepath.Join(dir, "bin", "php")
			if !isExec(fpm) || !isExec(cli) {
				continue
			}
			full := run(cli, "-n", "-r", "echo PHP_VERSION;")
			if !verRe.MatchString(full) {
				continue // broken build (e.g. crashes on start)
			}
			mm := strings.Join(strings.SplitN(full, ".", 3)[:2], ".")
			if _, dup := seen[mm]; dup && filepath.Base(dir) == "php" {
				continue // prefer the versioned php@X.Y path
			}
			p := PHP{Version: mm, Full: verRe.FindString(full), FPM: fpm, CLI: cli}
			if out := runStderr(cli, "-v"); strings.Contains(out, "Unable to load") || strings.Contains(out, "Warning") {
				p.Warning = "Some extensions fail to load; run: brew reinstall php@" + mm
			}
			seen[mm] = p
		}
	}
	list := make([]PHP, 0, len(seen))
	for _, p := range seen {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return versionLess(list[i].Version, list[j].Version) })
	phpCache, phpSig = list, sig
	return list
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
