// Package gitops gives each project a safe git panel: status, branches, a commit graph, and
// actions that can't lose work (fetch, fast-forward-only pull, switch/create branch on a clean tree).
//
// git never prompts: credentials come from the user's existing setup (SSH agent, keychain helper);
// anything needing input fails fast with a clear message instead of hanging.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// File is one changed file.
type File struct {
	Path   string `json:"path"`
	Status string `json:"status"` // modified | added | deleted | renamed | untracked | conflict
	Staged bool   `json:"staged"`
}

// Status of a repository.
type Status struct {
	Branch   string `json:"branch"`
	Detached bool   `json:"detached"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Files    []File `json:"files"`
	Stashes  int    `json:"stashes"`
	Clean    bool   `json:"clean"`
	WebURL   string `json:"web_url,omitempty"` // https page of the repository (GitHub, GitLab, Bitbucket…)
	Fetched  string `json:"last_fetch,omitempty"`
}

// Branch is a local or remote branch.
type Branch struct {
	Name     string `json:"name"`
	Remote   bool   `json:"remote"`
	Current  bool   `json:"current"`
	Upstream string `json:"upstream,omitempty"`
	Track    string `json:"track,omitempty"` // "ahead 1, behind 2"
	Subject  string `json:"subject"`
	When     string `json:"when"`
}

// Commit is one line of the history graph.
type Commit struct {
	Hash    string   `json:"hash,omitempty"`
	Short   string   `json:"short,omitempty"`
	Subject string   `json:"subject,omitempty"`
	Author  string   `json:"author,omitempty"`
	When    string   `json:"when,omitempty"`
	Refs    []string `json:"refs,omitempty"`
	Parents []string `json:"parents"`
}

var ErrNotRepo = errors.New("not a git repository")

func run(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "color.ui=false", "-c", "core.quotepath=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/usr/bin/false", "SSH_ASKPASS=/usr/bin/false",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10", "LC_ALL=C")
	cmd.Stdin = nil
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", errors.New("git took too long (network or a credential prompt?)")
	}
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if strings.Contains(msg, "not a git repository") {
			return "", ErrNotRepo
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(friendly(msg))
	}
	return out.String(), nil
}

func friendly(msg string) string {
	lines := strings.Split(msg, "\n")
	last := strings.TrimPrefix(strings.TrimPrefix(lines[len(lines)-1], "fatal: "), "error: ")
	switch {
	case strings.Contains(msg, "Permission denied (publickey)") || strings.Contains(msg, "Host key verification failed"):
		return "the git server refused the SSH key. Check that your SSH agent has the right key for this account"
	case strings.Contains(msg, "could not read Username") || strings.Contains(msg, "Authentication failed"):
		return "the git server needs a login. Sign in once in Terminal (or set up an SSH key) and try again"
	case strings.Contains(msg, "Not possible to fast-forward") || strings.Contains(msg, "diverging branches"):
		return "this branch and the remote have both changed. Pull in your editor or terminal to merge or rebase"
	}
	return last
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	out, err := run(dir, 5*time.Second, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// GetStatus reads branch, upstream, ahead/behind and changed files.
func GetStatus(dir string) (Status, error) {
	out, err := run(dir, 15*time.Second, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return Status{}, err
	}
	st := Status{Files: []File{}}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			st.Branch = strings.TrimPrefix(line, "# branch.head ")
			st.Detached = st.Branch == "(detached)"
		case strings.HasPrefix(line, "# branch.upstream "):
			st.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			f := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(f) == 2 {
				st.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[0], "+"))
				b, _ := strconv.Atoi(strings.TrimPrefix(f[1], "-"))
				st.Behind = b
			}
		case strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 "):
			f := strings.SplitN(line, " ", 9)
			if len(f) < 9 {
				continue
			}
			xy, path := f[1], f[8]
			if strings.HasPrefix(line, "2 ") { // rename: "<path>\t<orig>"
				p := strings.SplitN(strings.SplitN(line, " ", 10)[9], "\t", 2)
				path = p[0]
			}
			st.Files = append(st.Files, File{Path: path, Status: statusWord(xy), Staged: xy[0] != '.'})
		case strings.HasPrefix(line, "u "):
			f := strings.SplitN(line, " ", 11)
			st.Files = append(st.Files, File{Path: f[len(f)-1], Status: "conflict"})
		case strings.HasPrefix(line, "? "):
			st.Files = append(st.Files, File{Path: strings.TrimPrefix(line, "? "), Status: "untracked"})
		}
	}
	st.Clean = len(st.Files) == 0
	if s, err := run(dir, 5*time.Second, "stash", "list"); err == nil && strings.TrimSpace(s) != "" {
		st.Stashes = len(strings.Split(strings.TrimSpace(s), "\n"))
	}
	if u, err := run(dir, 5*time.Second, "config", "--get", "remote.origin.url"); err == nil {
		st.WebURL = WebURL(strings.TrimSpace(u))
	}
	if fi, err := os.Stat(dir + "/.git/FETCH_HEAD"); err == nil {
		st.Fetched = fi.ModTime().Format(time.RFC3339)
	}
	return st, nil
}

func statusWord(xy string) string {
	c := xy[1]
	if c == '.' {
		c = xy[0]
	}
	switch c {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R', 'C':
		return "renamed"
	}
	return "modified"
}

var sshHostRe = regexp.MustCompile(`^(?:ssh://)?(?:[^@]+@)?([^:/]+)[:/](.+?)(?:\.git)?/?$`)

// WebURL turns a remote into its web page. SSH aliases ("personal") map to github.com by
// convention unless the alias name says otherwise.
func WebURL(remote string) string {
	if remote == "" {
		return ""
	}
	if strings.HasPrefix(remote, "http") {
		u := regexp.MustCompile(`//[^@/]+@`).ReplaceAllString(remote, "//") // never keep tokens
		return strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	}
	m := sshHostRe.FindStringSubmatch(remote)
	if m == nil {
		return ""
	}
	host := m[1]
	if !strings.Contains(host, ".") { // SSH config alias, e.g. "personal" → most often GitHub
		host = "github.com"
	}
	return "https://" + host + "/" + m[2]
}

// Branches lists local branches (with tracking info) and remote branches.
func Branches(dir string) ([]Branch, error) {
	out, err := run(dir, 10*time.Second, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname)%09%(HEAD)%09%(upstream:short)%09%(upstream:track,nobracket)%09%(contents:subject)%09%(committerdate:relative)",
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var list []Branch
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 6 {
			continue
		}
		b := Branch{Current: f[1] == "*", Upstream: f[2], Track: f[3], Subject: f[4], When: f[5]}
		if strings.HasPrefix(f[0], "refs/heads/") {
			b.Name = strings.TrimPrefix(f[0], "refs/heads/")
		} else {
			b.Name, b.Remote = strings.TrimPrefix(f[0], "refs/remotes/"), true
			if strings.HasSuffix(b.Name, "/HEAD") {
				continue
			}
		}
		list = append(list, b)
	}
	return list, nil
}

const sep = "\x1f"

// Log returns commits of all branches, newest first, with their parents so the UI can draw the graph.
func Log(dir string, limit int) ([]Commit, error) {
	if limit <= 0 || limit > 500 {
		limit = 80
	}
	out, err := run(dir, 15*time.Second, "log", "--all", "--date-order", fmt.Sprintf("-n%d", limit),
		"--format=%H"+sep+"%h"+sep+"%s"+sep+"%an"+sep+"%cr"+sep+"%D"+sep+"%P")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return []Commit{}, nil
		}
		return nil, err
	}
	list := []Commit{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Split(line, sep)
		if len(f) < 7 {
			continue
		}
		c := Commit{Hash: f[0], Short: f[1], Subject: f[2], Author: f[3], When: f[4], Parents: strings.Fields(f[6])}
		for _, r := range strings.Split(f[5], ", ") {
			if r = strings.TrimSpace(strings.TrimPrefix(r, "HEAD -> ")); r != "" && r != "HEAD" {
				c.Refs = append(c.Refs, r)
			}
		}
		list = append(list, c)
	}
	return list, nil
}

// Fetch updates remote branches (never changes your files).
func Fetch(dir string) error {
	_, err := run(dir, 90*time.Second, "fetch", "--all", "--prune", "--quiet")
	return err
}

// Pull fast-forwards the current branch. It refuses when there are uncommitted changes or the branch
// has diverged, so nothing can be lost or merged by surprise.
func Pull(dir string) (string, error) {
	st, err := GetStatus(dir)
	if err != nil {
		return "", err
	}
	if !st.Clean {
		return "", fmt.Errorf("%d uncommitted change(s): commit or stash them before pulling", len(st.Files))
	}
	if st.Upstream == "" {
		return "", errors.New("this branch has no upstream branch to pull from")
	}
	if _, err := run(dir, 120*time.Second, "pull", "--ff-only", "--quiet"); err != nil {
		return "", err
	}
	after, _ := GetStatus(dir)
	return fmt.Sprintf("Pulled %d commit(s) into %s", st.Behind, after.Branch), nil
}

var branchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,120}$`)

// Checkout switches branch on a clean tree. A remote branch ("origin/x") gets a local tracking branch.
func Checkout(dir, name string) error {
	if !branchRe.MatchString(name) || strings.Contains(name, "..") {
		return errors.New("that isn't a valid branch name")
	}
	st, err := GetStatus(dir)
	if err != nil {
		return err
	}
	if !st.Clean {
		return fmt.Errorf("%d uncommitted change(s): commit or stash them before switching branch", len(st.Files))
	}
	if remotes, _ := run(dir, 5*time.Second, "remote"); remotes != "" {
		for _, r := range strings.Fields(remotes) {
			if strings.HasPrefix(name, r+"/") {
				local := strings.TrimPrefix(name, r+"/")
				if _, err := run(dir, 10*time.Second, "rev-parse", "--verify", "--quiet", "refs/heads/"+local); err == nil {
					_, err := run(dir, 30*time.Second, "switch", local)
					return err
				}
				_, err := run(dir, 30*time.Second, "switch", "--track", name)
				return err
			}
		}
	}
	_, err = run(dir, 30*time.Second, "switch", name)
	return err
}

// CreateBranch makes a new branch from the current commit and switches to it (uncommitted changes come along).
func CreateBranch(dir, name string) error {
	if !branchRe.MatchString(name) || strings.Contains(name, "..") || strings.HasSuffix(name, ".lock") {
		return errors.New("that isn't a valid branch name")
	}
	_, err := run(dir, 30*time.Second, "switch", "-c", name)
	return err
}
