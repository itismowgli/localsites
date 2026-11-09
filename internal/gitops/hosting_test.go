package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostingUsesRepoFromAlias(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "feature/x")
	os.WriteFile(filepath.Join(dir, "a"), []byte("a"), 0o644)
	git("add", "-A")
	git("commit", "-qm", "one")
	git("remote", "add", "origin", "git@personal:acme/shop.git")

	bin := filepath.Join(t.TempDir(), "gh")
	argsFile := filepath.Join(t.TempDir(), "args")
	os.WriteFile(bin, []byte(`#!/bin/sh
echo "$@" >> `+argsFile+`
case "$1" in
  pr) echo '[{"number":12,"title":"Add checkout","url":"https://github.com/acme/shop/pull/12","isDraft":false,"reviewDecision":"APPROVED"}]';;
  run) echo '[{"workflowName":"CI","status":"completed","conclusion":"success","url":"https://github.com/acme/shop/actions/runs/1","createdAt":"2026-10-28T00:00:00Z"}]';;
esac
`), 0o755)
	old := GHPath
	GHPath = func() (string, bool) { return bin, true }
	defer func() { GHPath = old }()

	h, err := GetHosting(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.Repo != "acme/shop" || len(h.PRs) != 1 || h.PRs[0].Number != 12 || len(h.Runs) != 1 || h.Runs[0].Conclusion != "success" {
		t.Fatalf("hosting: %+v", h)
	}
	b, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(b), "--repo acme/shop") || !strings.Contains(string(b), "--head feature/x") {
		t.Fatalf("gh was not told the repository and branch: %s", b)
	}
}
