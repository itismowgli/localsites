package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=T", "-c", "user.email=t@x", "-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestStatusBranchesLogSwitchPull(t *testing.T) {
	root := t.TempDir()
	remote, work := filepath.Join(root, "remote.git"), filepath.Join(root, "work")
	git(t, root, "init", "--bare", "-b", "main", remote)
	git(t, root, "clone", remote, work)
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("1"), 0o644)
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "first")
	git(t, work, "push", "-u", "origin", "main")
	git(t, work, "switch", "-c", "feature")
	os.WriteFile(filepath.Join(work, "b.txt"), []byte("2"), 0o644)
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feature work")
	git(t, work, "switch", "main")

	st, err := GetStatus(work)
	if err != nil || st.Branch != "main" || !st.Clean || st.Upstream != "origin/main" {
		t.Fatalf("status: %v %+v", err, st)
	}
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("changed"), 0o644)
	os.WriteFile(filepath.Join(work, "new.txt"), []byte("n"), 0o644)
	st, _ = GetStatus(work)
	if st.Clean || len(st.Files) != 2 {
		t.Fatalf("dirty status: %+v", st)
	}
	if err := Checkout(work, "feature"); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("switch with changes must be refused, got %v", err)
	}
	if _, err := Pull(work); err == nil {
		t.Fatal("pull with changes must be refused")
	}
	exec.Command("git", "-C", work, "checkout", "--", "a.txt").Run()
	os.Remove(filepath.Join(work, "new.txt"))

	br, err := Branches(work)
	if err != nil || len(br) < 3 {
		t.Fatalf("branches: %v %+v", err, br)
	}
	log, err := Log(work, 20)
	if err != nil || len(log) < 2 || len(log[0].Parents) == 0 || log[0].Hash == "" {
		t.Fatalf("log: %v %+v", err, log)
	}
	if err := Checkout(work, "feature"); err != nil {
		t.Fatal(err)
	}
	if err := Checkout(work, "../evil"); err == nil {
		t.Fatal("bad branch name accepted")
	}
	if err := CreateBranch(work, "fix/login"); err != nil {
		t.Fatal(err)
	}

	// Someone else pushes to main; our main fast-forwards
	other := filepath.Join(root, "other")
	git(t, root, "clone", remote, other)
	os.WriteFile(filepath.Join(other, "c.txt"), []byte("3"), 0o644)
	git(t, other, "add", ".")
	git(t, other, "commit", "-m", "from colleague")
	git(t, other, "push")
	if err := Checkout(work, "main"); err != nil {
		t.Fatal(err)
	}
	if err := Fetch(work); err != nil {
		t.Fatal(err)
	}
	if st, _ := GetStatus(work); st.Behind != 1 {
		t.Fatalf("behind after fetch: %+v", st)
	}
	if msg, err := Pull(work); err != nil || !strings.Contains(msg, "Pulled 1") {
		t.Fatalf("pull: %v %s", err, msg)
	}
}

func TestWebURL(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme/shop.git":                       "https://github.com/acme/shop",
		"git@personal:itismowgli/example-app.git":            "https://github.com/itismowgli/example-app",
		"https://user:TOKEN@gitlab.example.com/team/app.git": "https://gitlab.example.com/team/app",
		"ssh://git@bitbucket.org/team/app.git":               "https://bitbucket.org/team/app",
	} {
		if got := WebURL(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}
