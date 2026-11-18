// Package platform holds the few facts that differ between macOS and Linux: where Homebrew lives and the
// program search path background processes get.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// BrewPrefixes are where Homebrew installs programs, most common first.
//   - macOS: /opt/homebrew (Apple silicon), /usr/local (Intel)
//   - Linux: /home/linuxbrew/.linuxbrew (the default, needed for prebuilt packages), ~/.linuxbrew
func BrewPrefixes() []string {
	if runtime.GOOS == "linux" {
		out := []string{"/home/linuxbrew/.linuxbrew"}
		if home, err := os.UserHomeDir(); err == nil {
			out = append(out, filepath.Join(home, ".linuxbrew"))
		}
		return out
	}
	return []string{"/opt/homebrew", "/usr/local"}
}

// BrewBins are the bin folders of BrewPrefixes.
func BrewBins() []string {
	var out []string
	for _, p := range BrewPrefixes() {
		out = append(out, filepath.Join(p, "bin"))
	}
	return out
}

// SystemPath is PATH for programs LocalSites starts (PHP, dev servers, add-ons): Homebrew first, then the
// system folders. The person's own PATH isn't used, so what runs doesn't depend on how the app was started.
func SystemPath(extra ...string) string {
	dirs := append(append([]string{}, extra...), BrewBins()...)
	if runtime.GOOS == "darwin" {
		dirs = append(dirs, "/usr/local/bin") // Intel Homebrew and other installers, even on Apple silicon
	}
	dirs = append(dirs, "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	seen, out := map[string]bool{}, []string{}
	for _, d := range dirs {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// FindBrewBin finds a program in Homebrew's bin folders.
func FindBrewBin(name string) (string, bool) {
	for _, d := range BrewBins() {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}
