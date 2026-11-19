package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/version"
)

// Linux releases: GitHub builds the programs (tag push), this Mac packages and signs them. The package
// signing key lives only in the keychain; it reaches nfpm and the repository tools as a temporary file or
// on stdin, and is deleted right after.

const (
	packageKeyName = "package-signing-key" // armored OpenPGP private key, base64 (the keychain tool can't take newlines)
	repoOwner      = "itismowgli/localsites"
	nfpmVersion    = "v2.48.0"
	aptRelease     = "linux-repo" // the GitHub release that holds the apt repository
)

func run(dir string, stdin io.Reader, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// tag pushes v<version> for the current commit (after checking it's committed and pushed).
func tag() error {
	t := "v" + version.Version
	if st, _ := output("git", "status", "--porcelain", "--untracked-files=no"); st != "" {
		return errors.New("commit your changes first")
	}
	head, err := output("git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if remote, _ := output("git", "ls-remote", "origin", "refs/heads/master"); !strings.HasPrefix(remote, head) {
		return errors.New("push this commit to master first (git push origin HEAD:master)")
	}
	if existing, _ := output("git", "rev-parse", "-q", "--verify", "refs/tags/"+t); existing != "" && existing != head {
		return fmt.Errorf("%s already points to another commit", t)
	}
	// annotated, carrying the release title, like every release tag (docs/RELEASING.md)
	e, ok := entryFor(version.Version)
	if !ok {
		return fmt.Errorf("changelog.json has no entry for %s", version.Version)
	}
	if err := run("", nil, "git", "tag", "-f", "-a", t, "-m", releaseTitle(e), head); err != nil {
		return err
	}
	return run("", nil, "git", "push", "origin", "refs/tags/"+t)
}

// packageKey writes the package signing key from the keychain to a temporary file; remove the folder after.
func packageKey() (file, dir string, err error) {
	s, err := store()
	if err != nil {
		return "", "", err
	}
	b64, err := s.Get(packageKeyName)
	if err != nil {
		return "", "", errors.New("no package signing key in the keychain; see go run ./tools/release package-key")
	}
	armored, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", "", errors.New("the package signing key in the keychain is damaged")
	}
	dir, err = os.MkdirTemp("", "localsites-key-")
	if err != nil {
		return "", "", err
	}
	file = filepath.Join(dir, "key.asc")
	return file, dir, os.WriteFile(file, armored, 0o600)
}

// linux waits for GitHub's build of this version, downloads the programs and packages them as .deb and
// .rpm in -out, the .rpm signed.
func linux(args []string) error {
	fs := flag.NewFlagSet("linux", flag.ExitOnError)
	out := fs.String("out", "dist/release", "folder for the packages")
	_ = fs.Parse(args)
	t := "v" + version.Version
	fmt.Println("Waiting for GitHub to build the Linux programs for " + t + "…")
	var id string
	for i := 0; i < 120; i++ { // the run appears a few seconds after the tag is pushed
		raw, err := output("gh", "run", "list", "-R", repoOwner, "--workflow", "Linux builds", "--branch", t, "--limit", "1", "--json", "databaseId,status,conclusion")
		var runs []struct {
			ID         int64  `json:"databaseId"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		}
		if err == nil && json.Unmarshal([]byte(raw), &runs) == nil && len(runs) > 0 {
			r := runs[0]
			id = fmt.Sprint(r.ID)
			if r.Status == "completed" {
				if r.Conclusion != "success" {
					return fmt.Errorf("GitHub's Linux build %s ended with %s: gh run view %s -R %s", id, r.Conclusion, id, repoOwner)
				}
				break
			}
		}
		time.Sleep(15 * time.Second)
		if i == 119 {
			return errors.New("GitHub's Linux build didn't finish within 30 minutes")
		}
	}
	work, err := os.MkdirTemp("", "localsites-linux-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := run("", nil, "gh", "run", "download", id, "-R", repoOwner, "-D", work); err != nil {
		return err
	}
	key, keyDir, err := packageKey()
	if err != nil {
		return err
	}
	defer os.RemoveAll(keyDir)
	root, _ := os.Getwd()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	absOut, _ := filepath.Abs(*out)
	for _, arch := range []string{"amd64", "arm64"} {
		dir := filepath.Join(work, "linux-"+arch) // the artifact keeps the repository's paths
		if err := copyTree(filepath.Join(root, "packaging"), filepath.Join(dir, "packaging")); err != nil {
			return err
		}
		for _, p := range []string{"deb", "rpm"} {
			cmd := exec.Command("go", "run", "github.com/goreleaser/nfpm/v2/cmd/nfpm@"+nfpmVersion, "package", "-f", "packaging/linux/nfpm.yaml", "-p", p, "-t", absOut)
			cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
			cmd.Env = append(os.Environ(), "VERSION="+version.Version, "ARCH="+arch, "RPM_SIGNING_KEY_FILE="+key, "NFPM_RPM_PASSPHRASE=")
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("packaging %s for %s: %v", p, arch, err)
			}
		}
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}

// packageKeyCmd saves an armored private key (stdin) in the keychain and writes the public half for the
// website: site/linux/localsites.asc (armored) for dnf, site/linux/localsites.gpg (binary) for apt.
func packageKeyCmd() error {
	s, err := store()
	if err != nil {
		return err
	}
	if _, err := s.Get(packageKeyName); err == nil {
		return errors.New("a package signing key already exists in the keychain; delete it first if you really want a new one (installed systems would then reject updates until they get the new key)")
	}
	in, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return err
	}
	priv, pubArmored, pubBinary, ok := splitKeyBundle(in)
	if !ok {
		return errors.New("expected an armored private key, an armored public key and a base64 binary public key on stdin")
	}
	if err := s.Set(packageKeyName, base64.StdEncoding.EncodeToString(priv)); err != nil {
		return err
	}
	for _, dir := range []string{"site/linux", "packaging/linux"} { // the website, and inside the packages
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "localsites.asc"), pubArmored, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "localsites.gpg"), pubBinary, 0o644); err != nil {
			return err
		}
	}
	fmt.Println("Saved the package signing key in the keychain (LocalSites / " + packageKeyName + "). Back it up in your password manager.")
	fmt.Println("Wrote its public half to site/linux and packaging/linux (localsites.asc, localsites.gpg). Commit those.")
	return nil
}

// splitKeyBundle reads what the key container prints: the armored private key, the armored public key,
// then the binary public key in base64 after a line "BINARY".
func splitKeyBundle(in []byte) (priv, pubArmored, pubBinary []byte, ok bool) {
	const (
		privBegin = "-----BEGIN PGP PRIVATE KEY BLOCK-----"
		privEnd   = "-----END PGP PRIVATE KEY BLOCK-----"
		pubBegin  = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
		pubEnd    = "-----END PGP PUBLIC KEY BLOCK-----"
	)
	block := func(begin, end string) []byte {
		i, j := bytes.Index(in, []byte(begin)), bytes.Index(in, []byte(end))
		if i < 0 || j < i {
			return nil
		}
		return append(bytes.TrimSpace(in[i:j+len(end)]), '\n')
	}
	priv, pubArmored = block(privBegin, privEnd), block(pubBegin, pubEnd)
	if k := bytes.Index(in, []byte("\nBINARY\n")); k >= 0 {
		pubBinary, _ = base64.StdEncoding.DecodeString(string(bytes.TrimSpace(in[k+8:])))
	}
	return priv, pubArmored, pubBinary, priv != nil && pubArmored != nil && len(pubBinary) > 0
}

// repos builds the signed apt repository (out/apt, for the "linux-repo" GitHub release) and the dnf metadata
// (site/linux/rpm, published with the website) from the packages in -out, in the tests/linux/repo container.
func repos(args []string) error {
	fs := flag.NewFlagSet("repos", flag.ExitOnError)
	out := fs.String("out", "dist/release", "folder with the .deb and .rpm files")
	_ = fs.Parse(args)
	in, _ := filepath.Abs(*out)
	if debs, _ := filepath.Glob(filepath.Join(in, "*.deb")); len(debs) == 0 {
		return errors.New("no .deb files in " + in)
	}
	key, keyDir, err := packageKey()
	if err != nil {
		return err
	}
	defer os.RemoveAll(keyDir)
	f, err := os.Open(key)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := run("", nil, "docker", "build", "-q", "-t", "localsites-repo-tools", "tests/linux/repo"); err != nil {
		return errors.New("Docker is needed to build the Linux repositories: start Docker Desktop and run make release again")
	}
	work, err := os.MkdirTemp("", "localsites-repos-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	base := "https://github.com/" + repoOwner + "/releases/download/v" + version.Version + "/"
	if err := run("", f, "docker", "run", "--rm", "-i", "-v", in+":/in:ro", "-v", work+":/out", "localsites-repo-tools", "build-repos.sh", base); err != nil {
		return err
	}
	// apt: next to the packages, uploaded to the linux-repo release by make publish
	_ = os.RemoveAll(filepath.Join(in, "apt"))
	if err := copyTree(filepath.Join(work, "apt"), filepath.Join(in, "apt")); err != nil {
		return err
	}
	// dnf: committed with the website
	_ = os.RemoveAll("site/linux/rpm")
	return copyTree(filepath.Join(work, "rpm"), "site/linux/rpm")
}

// publish creates the GitHub release for this version from -out (the signed update files and packages),
// updates the apt repository (the linux-repo release) and commits the dnf metadata for the website.
func publish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	out := fs.String("out", "dist/release", "folder made by make release")
	_ = fs.Parse(args)
	t := "v" + version.Version
	var files []string
	for _, pattern := range append(releaseAssets, "SHA256SUMS") {
		m, _ := filepath.Glob(filepath.Join(*out, pattern))
		files = append(files, m...)
	}
	if len(files) < 6 {
		return errors.New("run make release first: " + *out + " doesn't have the app, the packages, the signed update files and SHA256SUMS")
	}
	e, ok := entryFor(version.Version)
	if !ok {
		return fmt.Errorf("changelog.json has no entry for %s", version.Version)
	}
	title := releaseTitle(e)
	nf := filepath.Join(*out, "notes.md")
	if err := os.WriteFile(nf, []byte(releaseNotes(e, true)), 0o644); err != nil {
		return err
	}
	if err := run("", nil, "gh", append([]string{"release", "create", t, "-R", repoOwner, "--verify-tag", "--title", title, "--notes-file", nf}, files...)...); err != nil {
		return err
	}
	// apt: the linux-repo release holds the current packages and the signed index
	if _, err := output("gh", "release", "view", aptRelease, "-R", repoOwner); err != nil {
		if err := run("", nil, "gh", "release", "create", aptRelease, "-R", repoOwner, "--prerelease", "--title", "LocalSites apt repository: updates for the .deb packages",
			"--notes", "The apt repository LocalSites' .deb packages add on install. Download LocalSites from the latest release instead."); err != nil {
			return err
		}
	}
	apt, _ := filepath.Glob(filepath.Join(*out, "apt", "*"))
	keep := map[string]bool{}
	for _, f := range apt {
		keep[filepath.Base(f)] = true
	}
	if err := run("", nil, "gh", append([]string{"release", "upload", aptRelease, "-R", repoOwner, "--clobber"}, apt...)...); err != nil {
		return err
	}
	if names, err := output("gh", "release", "view", aptRelease, "-R", repoOwner, "--json", "assets", "--jq", ".assets[].name"); err == nil {
		for _, n := range strings.Fields(names) {
			if !keep[n] { // packages of older versions
				_ = run("", nil, "gh", "release", "delete-asset", aptRelease, n, "-R", repoOwner, "-y")
			}
		}
	}
	// dnf: the metadata is published with the website
	if err := run("", nil, "git", "add", "site/linux"); err != nil {
		return err
	}
	if st, _ := output("git", "status", "--porcelain", "site/linux"); st != "" {
		if err := run("", nil, "git", "commit", "-q", "-m", "linux: dnf repository for "+version.Version); err != nil {
			return err
		}
		if err := run("", nil, "git", "push", "-q", "origin", "HEAD:master"); err != nil {
			return err
		}
	}
	fmt.Println("Published " + t + ". The website (and the dnf repository) update in a minute.")
	return nil
}
