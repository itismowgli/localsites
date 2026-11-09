package runtime

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// PHPInfo is what one PHP version has: its extensions and whether Xdebug can be used.
type PHPInfo struct {
	Version       string   `json:"version"`
	Extensions    []string `json:"extensions"`
	XdebugFile    string   `json:"xdebug_file,omitempty"` // xdebug.so in the extension folder
	XdebugLoaded  bool     `json:"xdebug_loaded"`         // already loaded by php.ini
	XdebugInstall string   `json:"xdebug_install"`        // command to install it
}

var infoCache = struct {
	sync.Mutex
	m map[string]infoEntry
}{m: map[string]infoEntry{}}

type infoEntry struct {
	sig  string
	info PHPInfo
}

// Info probes a PHP version once per build (cached by the binary's identity).
func Info(p PHP) PHPInfo {
	sig := phpSignature()
	infoCache.Lock()
	if e, ok := infoCache.m[p.Version]; ok && e.sig == sig {
		infoCache.Unlock()
		return e.info
	}
	infoCache.Unlock()
	in := PHPInfo{Version: p.Version, XdebugInstall: filepath.Join(filepath.Dir(p.CLI), "pecl") + " install xdebug"}
	for _, l := range strings.Split(run(p.CLI, "-m"), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "[") {
			continue
		}
		if strings.EqualFold(l, "xdebug") {
			in.XdebugLoaded = true
		}
		in.Extensions = append(in.Extensions, l)
	}
	sort.Slice(in.Extensions, func(i, j int) bool { return strings.ToLower(in.Extensions[i]) < strings.ToLower(in.Extensions[j]) })
	in.Extensions = dedupe(in.Extensions)
	if dir := run(p.CLI, "-r", "echo ini_get('extension_dir');"); dir != "" {
		if f := filepath.Join(dir, "xdebug.so"); isFile(f) {
			in.XdebugFile = f
		}
	}
	infoCache.Lock()
	infoCache.m[p.Version] = infoEntry{sig, in}
	infoCache.Unlock()
	return in
}

func isFile(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }

func dedupe(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || !strings.EqualFold(v, s[i-1]) {
			out = append(out, v)
		}
	}
	return out
}
