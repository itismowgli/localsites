package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/localsites/localsites/internal/version"
)

// The release pattern (docs/RELEASING.md): every release has an annotated tag vX.Y.Z, the title
// "LocalSites X.Y.Z: <changelog title>", and notes in one shape: the changes grouped as New, Improved, Fixed
// and Security, then how to install, how to check the downloads against SHA256SUMS, and a link to every change
// since the release before. CHANGELOG.md is written from the same changelog.json.

const repoURL = "https://github.com/itismowgli/localsites"

// Item kinds, in the order their groups appear.
var kindTitles = []struct{ kind, title string }{
	{"new", "New"}, {"improved", "Improved"}, {"fixed", "Fixed"}, {"security", "Security"},
}

func entryFor(v string) (version.Entry, bool) {
	for _, e := range version.Changelog() {
		if e.Version == v {
			return e, true
		}
	}
	return version.Entry{}, false
}

// previousRelease is the version published on GitHub before v (releases start at 1.0.0), or "".
func previousRelease(v string) string {
	all := version.Changelog() // newest first
	for i, e := range all {
		if e.Version == v && i+1 < len(all) && !strings.HasPrefix(all[i+1].Version, "0.") {
			return all[i+1].Version
		}
	}
	return ""
}

func releaseTitle(e version.Entry) string { return "LocalSites " + e.Version + ": " + e.Title }

// changeList writes the items grouped by kind, each group under a heading of the given level.
func changeList(e version.Entry, heading string) string {
	var b strings.Builder
	if len(e.Kinds) != len(e.Items) { // older entries without kinds: one list
		for _, it := range e.Items {
			b.WriteString("- " + it + "\n")
		}
		return b.String()
	}
	for _, k := range kindTitles {
		first := true
		for i, it := range e.Items {
			if e.Kinds[i] != k.kind {
				continue
			}
			if first {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(heading + " " + k.title + "\n\n")
				first = false
			}
			b.WriteString("- " + it + "\n")
		}
	}
	return b.String()
}

// releaseNotes is the body of the GitHub release for e. linux says whether the release has Linux packages.
func releaseNotes(e version.Entry, linux bool) string {
	var b strings.Builder
	b.WriteString(changeList(e, "###"))
	b.WriteString(installNotes(e.Version, linux))
	b.WriteString("\n## Verify\n\nEvery file's SHA-256 is in `SHA256SUMS`. Download it next to the files and run:\n\n```sh\n" +
		"shasum -a 256 -c SHA256SUMS --ignore-missing     # macOS\nsha256sum -c SHA256SUMS --ignore-missing         # Linux\n```\n\n" +
		"In-app updates also check `update.json`, which is signed.\n")
	if prev := previousRelease(e.Version); prev != "" {
		b.WriteString(fmt.Sprintf("\n**Full changes:** [v%s…v%s](%s/compare/v%s...v%s)\n", prev, e.Version, repoURL, prev, e.Version))
	}
	return b.String()
}

func installNotes(v string, linux bool) string {
	s := fmt.Sprintf("\n## Install\n\n**macOS (Apple silicon):** download `LocalSites-%s-macos-arm64.zip`, unzip it and drag LocalSites into Applications. Existing installs update from Settings → Updates.\n", v)
	if linux {
		s += fmt.Sprintf("\n**Linux (beta):** Ubuntu 24.04+, Debian 12+ and Fedora, x86_64 and arm64. Install the package once; it adds the signed LocalSites repository, so later versions arrive with your normal system updates.\n\n"+
			"```sh\nsudo apt install ./localsites_%[1]s_amd64.deb        # Ubuntu, Debian (arm64: _arm64.deb)\nsudo dnf install ./localsites-%[1]s-1.x86_64.rpm     # Fedora (arm64: .aarch64.rpm)\n```\n", v)
	}
	return s
}

// releaseAssets are the files a release ships and SHA256SUMS lists.
var releaseAssets = []string{"LocalSites-*.zip", "*.deb", "*.rpm", "update.json", "update.json.sig"}

// writeSums writes dir/SHA256SUMS for the release files in dir, in the format shasum -c reads.
func writeSums(dir string) error {
	var names []string
	for _, p := range releaseAssets {
		m, _ := filepath.Glob(filepath.Join(dir, p))
		for _, f := range m {
			names = append(names, filepath.Base(f))
		}
	}
	if len(names) == 0 {
		return errors.New("no release files in " + dir)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		f, err := os.Open(filepath.Join(dir, n))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		b.WriteString(hex.EncodeToString(h.Sum(nil)) + "  " + n + "\n")
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(b.String()), 0o644)
}

func sumsCmd(args []string) error {
	fs := flag.NewFlagSet("sums", flag.ExitOnError)
	out := fs.String("out", "dist/release", "folder with the release files")
	_ = fs.Parse(args)
	if err := writeSums(*out); err != nil {
		return err
	}
	fmt.Println("Wrote " + filepath.Join(*out, "SHA256SUMS"))
	return nil
}

// changelogMarkdown is CHANGELOG.md, written from changelog.json.
func changelogMarkdown() string {
	var b strings.Builder
	b.WriteString("# Changelog\n\nEvery LocalSites release, newest first. Written from `internal/version/changelog.json` by\n" +
		"`go run ./tools/release changelog`; the app shows the same list under What's new. Versions follow\n" +
		"[semantic versioning](https://semver.org): a new feature raises the middle number, a fix the last one.\n")
	for _, e := range version.Changelog() {
		b.WriteString(fmt.Sprintf("\n## %s (%s): %s\n\n", e.Version, e.Date, e.Title))
		b.WriteString(changeList(e, "###"))
	}
	return b.String()
}

func changelogCmd(args []string) error {
	fs := flag.NewFlagSet("changelog", flag.ExitOnError)
	check := fs.Bool("check", false, "fail when CHANGELOG.md is out of date")
	_ = fs.Parse(args)
	want := changelogMarkdown()
	if *check {
		if got, _ := os.ReadFile("CHANGELOG.md"); string(got) != want {
			return errors.New("CHANGELOG.md is out of date: go run ./tools/release changelog")
		}
		return nil
	}
	return os.WriteFile("CHANGELOG.md", []byte(want), 0o644)
}

// notesSync brings every published release to the pattern: title, notes and a SHA256SUMS file. It only
// edits text on GitHub and adds SHA256SUMS where it's missing; no other file changes.
func notesSync(args []string) error {
	fs := flag.NewFlagSet("notes-sync", flag.ExitOnError)
	dry := fs.Bool("dry-run", false, "print what would change")
	_ = fs.Parse(args)
	tags, err := output("gh", "release", "list", "-R", repoOwner, "--limit", "200", "--json", "tagName", "--jq", ".[].tagName")
	if err != nil {
		return err
	}
	for _, t := range strings.Fields(tags) {
		if !strings.HasPrefix(t, "v") {
			continue // linux-repo holds the apt repository, not a version
		}
		e, ok := entryFor(strings.TrimPrefix(t, "v"))
		if !ok {
			return fmt.Errorf("changelog.json has no entry for %s", t)
		}
		assets, _ := output("gh", "release", "view", t, "-R", repoOwner, "--json", "assets", "--jq", ".assets[].name")
		linux := strings.Contains(assets, ".deb")
		if !strings.Contains(assets, "SHA256SUMS") {
			dir, err := os.MkdirTemp("", "ls-sums-")
			if err != nil {
				return err
			}
			args := []string{"release", "download", t, "-R", repoOwner, "-D", dir}
			for _, p := range releaseAssets {
				args = append(args, "-p", p)
			}
			if err := run("", nil, "gh", args...); err != nil {
				os.RemoveAll(dir)
				return err
			}
			if err := writeSums(dir); err != nil {
				os.RemoveAll(dir)
				return err
			}
			if *dry {
				fmt.Println(t + ": would add SHA256SUMS")
			} else if err := run("", nil, "gh", "release", "upload", t, "-R", repoOwner, filepath.Join(dir, "SHA256SUMS")); err != nil {
				os.RemoveAll(dir)
				return err
			}
			os.RemoveAll(dir)
		}
		nf, err := os.CreateTemp("", "ls-notes-*.md")
		if err != nil {
			return err
		}
		_, _ = nf.WriteString(releaseNotes(e, linux))
		nf.Close()
		if *dry {
			fmt.Printf("%s: %s\n", t, releaseTitle(e))
		} else if err := run("", nil, "gh", "release", "edit", t, "-R", repoOwner, "--title", releaseTitle(e), "--notes-file", nf.Name()); err != nil {
			os.Remove(nf.Name())
			return err
		}
		os.Remove(nf.Name())
	}
	return nil
}
