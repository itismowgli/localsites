// Release tool: makes the signing key and signs update manifests.
//
//	go run ./tools/release keygen
//	    makes the update signing key, saves the private half in the macOS keychain
//	    (LocalSites / release-signing-key) and prints the public half for internal/update/key.go.
//	go run ./tools/release manifest -out dist [-channel stable] [-security] dist/LocalSites-0.13.0-macos-arm64.zip ...
//	    writes dist/update.json (version, notes from changelog.json, size and SHA-256 of each zip, .deb
//	    and .rpm) and dist/update.json.sig. Upload both with the files to the GitHub release.
//	go run ./tools/release tag
//	    pushes the tag v<version> for the current commit, which starts GitHub building the Linux programs.
//	go run ./tools/release linux -out dist
//	    waits for that build, downloads the programs and packages them here as .deb and .rpm (nfpm), the
//	    .rpm signed with the key in the keychain (LocalSites / rpm-signing-key).
//	go run ./tools/release package-key < bundle
//	    saves the OpenPGP key for packages and repositories in the keychain (LocalSites /
//	    package-signing-key) and writes its public half to site/linux (see tests/linux/repo/README).
//	go run ./tools/release repos -out dist/release
//	    builds the signed apt and dnf repositories from the packages (in a container, tests/linux/repo).
//	go run ./tools/release publish -out dist/release
//	    creates the GitHub release, updates the apt repository and commits the dnf metadata (make publish).
//
// The private key never leaves the keychain except into this process's memory. Back it up once
// (Keychain Access → LocalSites → release-signing-key) in your password manager: without it,
// people can't get updates from inside the app, and a new key needs a manual download.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/localsites/localsites/internal/secrets"
	"github.com/localsites/localsites/internal/update"
	"github.com/localsites/localsites/internal/version"
)

const keyName = "release-signing-key"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: release keygen | release manifest -out DIR ZIP...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen()
	case "manifest":
		err = manifest(os.Args[2:])
	case "tag":
		err = tag()
	case "linux":
		err = linux(os.Args[2:])
	case "package-key":
		err = packageKeyCmd()
	case "repos":
		err = repos(os.Args[2:])
	case "publish":
		err = publish(os.Args[2:])
	case "sums":
		err = sumsCmd(os.Args[2:])
	case "changelog":
		err = changelogCmd(os.Args[2:])
	case "notes-sync":
		err = notesSync(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

// store is always the keychain: the signing key never goes into a file, even with LOCALSITES_SECRETS=file.
func store() (secrets.Store, error) { return secrets.Keychain() }

func keygen() error {
	s, err := store()
	if err != nil {
		return err
	}
	if _, err := s.Get(keyName); err == nil {
		return errors.New("a signing key already exists in the keychain; delete it first if you really want a new one (old releases can then no longer be verified)")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := s.Set(keyName, base64.StdEncoding.EncodeToString(priv.Seed())); err != nil {
		return err
	}
	fmt.Println("Saved the private key in the keychain (LocalSites / " + keyName + "). Back it up in your password manager.")
	fmt.Println("Public key for internal/update/key.go:")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

var (
	zipName = regexp.MustCompile(`^LocalSites-([0-9][0-9A-Za-z.-]*)-(darwin|macos|windows|linux)-(arm64|amd64|universal)\.zip$`)
	debName = regexp.MustCompile(`^localsites_([0-9][0-9A-Za-z.~+-]*)_(amd64|arm64)\.deb$`)
	rpmName = regexp.MustCompile(`^localsites-([0-9][0-9A-Za-z.~+]*)-\d+\.(x86_64|aarch64)\.rpm$`)
)

// releaseFile reads a release file's name: version, OS, architecture and format ("" for a zipped app).
func releaseFile(name string) (ver, osName, arch, format string, ok bool) {
	if g := zipName.FindStringSubmatch(name); g != nil {
		osName = map[string]string{"macos": "darwin"}[g[2]]
		if osName == "" {
			osName = g[2]
		}
		return g[1], osName, g[3], "", true
	}
	if g := debName.FindStringSubmatch(name); g != nil {
		return g[1], "linux", g[2], "deb", true
	}
	if g := rpmName.FindStringSubmatch(name); g != nil {
		return g[1], "linux", map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[g[2]], "rpm", true
	}
	return "", "", "", "", false
}

func manifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	out := fs.String("out", "dist", "folder for update.json and update.json.sig")
	channel := fs.String("channel", "stable", "stable or beta")
	security := fs.Bool("security", false, "this release fixes a security problem")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		return errors.New("give at least one file")
	}
	if *channel != "stable" && *channel != "beta" {
		return errors.New("-channel must be stable or beta")
	}
	s, err := store()
	if err != nil {
		return err
	}
	seed, err := s.Get(keyName)
	if err != nil {
		return errors.New("no signing key in the keychain; run: go run ./tools/release keygen")
	}
	sb, err := base64.StdEncoding.DecodeString(seed)
	if err != nil || len(sb) != ed25519.SeedSize {
		return errors.New("the signing key in the keychain is damaged")
	}
	priv := ed25519.NewKeyFromSeed(sb)
	if pub := base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)); pub != update.PublicKey {
		return errors.New("the keychain key doesn't match internal/update/key.go; the app would reject this release")
	}

	m := update.Manifest{Version: version.Version, Channel: *channel, Security: *security}
	for _, e := range version.Changelog() {
		if e.Version == version.Version {
			m.Date, m.Title, m.Notes = e.Date, e.Title, e.Items
		}
	}
	if m.Title == "" {
		return fmt.Errorf("changelog.json has no entry for %s", version.Version)
	}
	for _, z := range fs.Args() {
		ver, osName, arch, format, ok := releaseFile(filepath.Base(z))
		if !ok {
			return fmt.Errorf("%s: name it LocalSites-<version>-<os>-<arch>.zip, localsites_<version>_<arch>.deb or localsites-<version>-1.<arch>.rpm", z)
		}
		if ver != version.Version {
			return fmt.Errorf("%s is for %s, this build is %s", z, ver, version.Version)
		}
		size, sum, err := hashFile(z)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, update.File{Name: filepath.Base(z), OS: osName, Arch: arch, Format: format, Size: size, SHA256: sum})
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if _, err := update.Verify(raw, update.Sign(priv, raw)); err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "update.json"), raw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "update.json.sig"), update.Sign(priv, raw), 0o644); err != nil {
		return err
	}
	fmt.Printf("Signed %s/update.json for %s (%d file(s)).\n", *out, m.Version, len(m.Files))
	return nil
}

func hashFile(p string) (int64, string, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return n, hex.EncodeToString(h.Sum(nil)), err
}
