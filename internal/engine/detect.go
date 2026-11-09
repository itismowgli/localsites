package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/runtime"
)

// Project kinds.
const (
	KindPHP       = "php"
	KindLaravel   = "laravel"
	KindWordPress = "wordpress"
	KindNext      = "nextjs"
	KindNode      = "node"
	KindStatic    = "static"
	KindOther     = "other"  // a folder or repo that isn't a website
	KindDocker    = "docker" // any command (docker compose up…) serving a port the user chose
)

// Framework is what was detected in a project.
type Framework struct {
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"` // e.g. Laravel 10.48.4, Next.js 15.1.0
	PHPReq  string `json:"php_requirement,omitempty"`
	PM      string `json:"package_manager,omitempty"` // npm | pnpm | yarn | bun
	Deps    bool   `json:"dependencies_installed"`
	Node    string `json:"node_version,omitempty"` // from .nvmrc / engines
}

// Git is the repository state of a project.
type Git struct {
	Branch  string `json:"branch,omitempty"`
	Remote  string `json:"remote,omitempty"`  // origin URL without credentials
	Host    string `json:"host,omitempty"`    // github.com, or an SSH host alias such as "personal"
	Account string `json:"account,omitempty"` // owner / organisation, e.g. itismowgli
	Repo    string `json:"repo,omitempty"`
}

// candidate is a folder found while scanning a projects folder.
type candidate struct {
	ID, Name, Group, Path string
	RootIdx               int
	RootLabel             string
}

var skipDirs = map[string]bool{"node_modules": true, "vendor": true, ".git": true, ".idea": true, ".vscode": true}

// scanRoots finds projects. Root 0 keeps the classic behaviour (every folder is a project, ID = folder
// name). Other roots list real projects only, looking one level into group folders (e.g. one per account).
func scanRoots(roots []config.Root) []candidate {
	var out []candidate
	for i, r := range roots {
		entries, _ := readDir(r.Path) // never waits on a macOS permission prompt
		for _, de := range entries {
			name := de.Name()
			if strings.HasPrefix(name, ".") || skipDirs[name] {
				continue
			}
			path := filepath.Join(r.Path, name)
			if !isDir(path) {
				continue
			}
			layout := r.Layout
			if layout == "" || layout == "auto" {
				layout = "auto"
				if i == 0 {
					layout = "flat" // the main folder: every subfolder is a project (classic MAMP behaviour)
				}
			}
			id := func(rel string) string {
				if i == 0 {
					return rel
				}
				return r.Label + "/" + rel
			}
			if layout == "flat" {
				out = append(out, candidate{ID: id(name), Name: name, Path: path, RootIdx: i, RootLabel: r.Label})
				continue
			}
			if layout == "auto" && looksLikeProject(path) {
				out = append(out, candidate{ID: id(name), Name: name, Path: path, RootIdx: i, RootLabel: r.Label})
				continue
			}
			// Group folder: list the projects inside it
			sub, _ := os.ReadDir(path)
			for _, se := range sub {
				sp := filepath.Join(path, se.Name())
				if strings.HasPrefix(se.Name(), ".") || skipDirs[se.Name()] || !isDir(sp) || !looksLikeProject(sp) {
					continue
				}
				out = append(out, candidate{ID: id(name + "/" + se.Name()), Name: se.Name(), Group: name, Path: sp, RootIdx: i, RootLabel: r.Label})
			}
		}
	}
	return out
}

func isDir(p string) bool {
	st, err := os.Stat(p) // follows symlinks
	return err == nil && st.IsDir()
}

func looksLikeProject(dir string) bool {
	for _, f := range []string{".git", "composer.json", "package.json", "artisan", "index.php", "index.html", "wp-config.php", "public/index.php"} {
		if exists(filepath.Join(dir, f)) {
			return true
		}
	}
	return false
}

// detect works out what kind of project a folder is.
func detect(dir string) Framework {
	f := Framework{Kind: KindOther}
	composer := readJSON(filepath.Join(dir, "composer.json"))
	pkg := readJSON(filepath.Join(dir, "package.json"))
	req := mapOf(composer["require"])
	if v, ok := req["php"].(string); ok {
		f.PHPReq = v
	}
	switch {
	case exists(filepath.Join(dir, "artisan")) && req["laravel/framework"] != nil:
		f.Kind = KindLaravel
		f.Version = lockVersion(filepath.Join(dir, "composer.lock"), "laravel/framework")
		f.Deps = exists(filepath.Join(dir, "vendor", "autoload.php"))
	case exists(filepath.Join(dir, "wp-config.php")) || exists(filepath.Join(dir, "wp-load.php")):
		f.Kind = KindWordPress
		f.Deps = true
	case pkg != nil && depVersion(pkg, "next") != "":
		f.Kind = KindNext
		f.Version = strings.TrimLeft(depVersion(pkg, "next"), "^~>=")
	case pkg != nil && composer == nil && !exists(filepath.Join(dir, "index.php")) && hasScript(pkg, "dev", "start"):
		f.Kind = KindNode
	case composer != nil || exists(filepath.Join(dir, "index.php")) || exists(filepath.Join(dir, "public", "index.php")) || hasPHPFiles(dir):
		f.Kind = KindPHP
		f.Deps = composer == nil || exists(filepath.Join(dir, "vendor", "autoload.php")) || req == nil
	case exists(filepath.Join(dir, "index.html")):
		f.Kind = KindStatic
		f.Deps = true
	}
	if f.Kind == KindNext || f.Kind == KindNode {
		f.PM = packageManager(dir)
		f.Deps = exists(filepath.Join(dir, "node_modules"))
		if b, err := os.ReadFile(filepath.Join(dir, ".nvmrc")); err == nil {
			f.Node = strings.TrimPrefix(strings.TrimSpace(string(b)), "v")
		}
	}
	return f
}

func packageManager(dir string) string {
	switch {
	case exists(filepath.Join(dir, "pnpm-lock.yaml")):
		return "pnpm"
	case exists(filepath.Join(dir, "yarn.lock")):
		return "yarn"
	case exists(filepath.Join(dir, "bun.lockb")) || exists(filepath.Join(dir, "bun.lock")):
		return "bun"
	}
	return "npm"
}

func hasPHPFiles(dir string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, "*.php"))
	return len(m) > 0
}

func readJSON(file string) map[string]any {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func depVersion(pkg map[string]any, name string) string {
	for _, k := range []string{"dependencies", "devDependencies"} {
		if v, ok := mapOf(pkg[k])[name].(string); ok {
			return v
		}
	}
	return ""
}

func hasScript(pkg map[string]any, names ...string) bool {
	s := mapOf(pkg["scripts"])
	for _, n := range names {
		if _, ok := s[n]; ok {
			return true
		}
	}
	return false
}

// lockVersion reads an installed package version from composer.lock.
func lockVersion(file, name string) string {
	lock := readJSON(file)
	for _, key := range []string{"packages", "packages-dev"} {
		list, _ := lock[key].([]any)
		for _, p := range list {
			pm := mapOf(p)
			if pm["name"] == name {
				v, _ := pm["version"].(string)
				return strings.TrimPrefix(v, "v")
			}
		}
	}
	return ""
}

var constraintRe = regexp.MustCompile(`^\s*(\^|~|>=|>|=|==)?\s*v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(\.\*)?\s*$`)

// phpFor picks the newest installed PHP version that satisfies a composer constraint
// ("^7.1.3", "^7.3|^8.0", ">=5.6.4"). Returns "" when nothing installed fits or it can't tell.
func phpFor(constraint string) string {
	if constraint == "" {
		return ""
	}
	installed := runtime.PHPVersions()
	best := ""
	for _, part := range regexp.MustCompile(`\s*\|\|?\s*`).Split(constraint, -1) {
		// "^7.3 <8.0"-style ranges: use the first condition only
		part = strings.Fields(part + " ")[0]
		m := constraintRe.FindStringSubmatch(part)
		if m == nil {
			continue
		}
		major, _ := strconv.Atoi(m[2])
		minor, _ := strconv.Atoi(m[3])
		for _, p := range installed {
			pm := strings.SplitN(p.Version, ".", 2)
			vMaj, _ := strconv.Atoi(pm[0])
			vMin, _ := strconv.Atoi(pm[1])
			ok := false
			switch m[1] {
			case "^", "~":
				ok = vMaj == major && vMin >= minor
			case ">=", ">":
				ok = vMaj > major || (vMaj == major && vMin >= minor)
			default:
				ok = vMaj == major && (m[3] == "" || m[5] != "" || vMin == minor)
			}
			if ok && (best == "" || versionLess(best, p.Version)) {
				best = p.Version
			}
		}
	}
	return best
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

var remoteRe = regexp.MustCompile(`^(?:(?:ssh|https?|git)://)?(?:[^@/]+@)?([^/:]+)(?::\d+)?[:/]([^/]+)/([^/]+?)(?:\.git)?/?$`)

// gitInfo reads the branch and origin from .git without running git (fast for many projects).
func gitInfo(dir string) *Git {
	gitDir := filepath.Join(dir, ".git")
	if st, err := os.Stat(gitDir); err != nil {
		return nil
	} else if !st.IsDir() { // worktree/submodule: "gitdir: <path>"
		b, _ := os.ReadFile(gitDir)
		p := strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		gitDir = p
	}
	g := &Git{}
	if b, err := os.ReadFile(filepath.Join(gitDir, "HEAD")); err == nil {
		head := strings.TrimSpace(string(b))
		if strings.HasPrefix(head, "ref: refs/heads/") {
			g.Branch = strings.TrimPrefix(head, "ref: refs/heads/")
		} else if len(head) >= 7 {
			g.Branch = head[:7] // detached
		}
	}
	if f, err := os.Open(filepath.Join(gitDir, "config")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		inOrigin := false
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "[") {
				inOrigin = line == `[remote "origin"]`
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok && inOrigin && strings.TrimSpace(k) == "url" {
				g.Remote = stripCredentials(strings.TrimSpace(v))
			}
		}
	}
	if m := remoteRe.FindStringSubmatch(g.Remote); m != nil {
		g.Host, g.Account, g.Repo = m[1], m[2], m[3]
	}
	return g
}

// stripCredentials removes user:token@ from https remotes so tokens never reach the UI or logs.
func stripCredentials(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 && at < strings.Index(rest+"/", "/") {
			return u[:i+3] + rest[at+1:]
		}
	}
	return u
}

// valetInfo tells whether Laravel Valet already serves a folder, and at which address / PHP version.
type valetInfo struct {
	Domain string
	Secure bool
	PHP    string
}

func (e *Engine) valetSites() map[string]valetInfo {
	out := map[string]valetInfo{}
	if _, ok := runtime.Valet(); !ok || e.P.Home == "" {
		return out
	}
	base := filepath.Join(e.P.Home, ".config", "valet")
	cfg := readJSON(filepath.Join(base, "config.json"))
	tld, _ := cfg["tld"].(string)
	if tld == "" {
		tld = "test"
	}
	add := func(name, path string) {
		domain := strings.ToLower(name) + "." + tld
		vi := valetInfo{Domain: domain, Secure: exists(filepath.Join(base, "Certificates", domain+".crt"))}
		if b, err := os.ReadFile(filepath.Join(base, "Nginx", domain)); err == nil {
			if strings.Contains(string(b), "proxy_pass") {
				return // a proxy (e.g. one of ours), not a Valet-served folder
			}
			if m := regexp.MustCompile(`valet(\d)(\d+)\.sock`).FindStringSubmatch(string(b)); m != nil {
				vi.PHP = m[1] + "." + m[2]
			}
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			real = path
		}
		out[real] = vi
	}
	if paths, ok := cfg["paths"].([]any); ok {
		for _, p := range paths {
			parked, _ := p.(string)
			if parked == "" {
				continue
			}
			entries, _ := readDir(parked)
			for _, de := range entries {
				if !strings.HasPrefix(de.Name(), ".") {
					add(de.Name(), filepath.Join(parked, de.Name()))
				}
			}
		}
	}
	return out
}

// fileKey turns a project ID ("websites/aecor") into something safe for file names.
func fileKey(id string) string { return strings.ReplaceAll(id, "/", "__") }

// Detection reads composer.lock and package.json, which can be large. The result is kept until one of
// the files it looks at changes (folder entries, manifests, lock files, vendor, node_modules).
var detectCache = struct {
	sync.Mutex
	m map[string]detectEntry
}{m: map[string]detectEntry{}}

type detectEntry struct {
	sig string
	fw  Framework
}

func detectSignature(dir string) string {
	var b strings.Builder
	for _, f := range []string{"", "composer.json", "composer.lock", "package.json", ".nvmrc", "vendor", "public"} {
		if st, err := os.Stat(filepath.Join(dir, f)); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", f, st.ModTime().UnixNano(), st.Size())
		}
	}
	return b.String()
}

func detectCached(dir string) Framework {
	sig := detectSignature(dir)
	detectCache.Lock()
	e, ok := detectCache.m[dir]
	detectCache.Unlock()
	if ok && e.sig == sig {
		return e.fw
	}
	fw := detect(dir)
	detectCache.Lock()
	detectCache.m[dir] = detectEntry{sig, fw}
	detectCache.Unlock()
	return fw
}
