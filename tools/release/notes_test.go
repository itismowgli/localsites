package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localsites/localsites/internal/version"
)

func TestReleaseNotesFollowThePattern(t *testing.T) {
	e := version.Entry{Version: "1.6.0", Title: "T", Items: []string{"a fix", "a feature", "a hole closed"}, Kinds: []string{"fixed", "new", "security"}}
	n := releaseNotes(e, true)
	for _, want := range []string{"### New\n\n- a feature", "### Fixed\n\n- a fix", "### Security\n\n- a hole closed", "## Install", "localsites_1.6.0_amd64.deb", "## Verify", "SHA256SUMS", "/compare/v1.5.0...v1.6.0"} {
		if !strings.Contains(n, want) {
			t.Errorf("notes miss %q:\n%s", want, n)
		}
	}
	if strings.Index(n, "### New") > strings.Index(n, "### Fixed") {
		t.Error("groups are out of order")
	}
	if strings.Contains(releaseNotes(e, false), ".deb") {
		t.Error("a Mac-only release mentions Linux packages")
	}
	if releaseTitle(e) != "LocalSites 1.6.0: T" {
		t.Error(releaseTitle(e))
	}
	if previousRelease("1.0.0") != "" { // 0.x was never published on GitHub
		t.Error("1.0.0 links to an unpublished version")
	}
}

func TestSumsListEveryReleaseFile(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"LocalSites-1.6.0-macos-arm64.zip", "localsites_1.6.0_amd64.deb", "update.json", "notes.md"} {
		_ = os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644)
	}
	if err := writeSums(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	s := string(b)
	if strings.Count(s, "\n") != 3 || strings.Contains(s, "notes.md") || !strings.Contains(s, "  update.json\n") {
		t.Fatalf("SHA256SUMS:\n%s", s)
	}
}

func TestChangelogIsCurrent(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil || string(b) != changelogMarkdown() {
		t.Fatal("CHANGELOG.md is out of date: go run ./tools/release changelog")
	}
}
