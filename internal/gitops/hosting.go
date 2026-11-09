package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Hosting is what the code host knows about the current branch: open pull requests and recent CI runs.
// Read-only, through the GitHub CLI's own login.
type Hosting struct {
	Provider string `json:"provider"` // github
	Repo     string `json:"repo"`     // owner/name
	PRs      []PR   `json:"pull_requests"`
	Runs     []Run  `json:"runs"`
	Problem  string `json:"problem,omitempty"`
}

type PR struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	IsDraft  bool   `json:"isDraft"`
	Decision string `json:"reviewDecision"`
}

type Run struct {
	Workflow   string `json:"workflowName"`
	Status     string `json:"status"`     // queued | in_progress | completed
	Conclusion string `json:"conclusion"` // success | failure | cancelled …
	URL        string `json:"url"`
	Created    string `json:"createdAt"`
}

// GHPath finds the GitHub CLI (tests replace it).
var GHPath = func() (string, bool) {
	for _, p := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// GetHosting asks GitHub about the branch. The repository comes from the remote's web address, so SSH host
// aliases (git@personal:owner/repo) work too.
func GetHosting(dir string) (Hosting, error) {
	st, err := GetStatus(dir)
	if err != nil {
		return Hosting{}, err
	}
	u, err := url.Parse(st.WebURL)
	if err != nil || u.Host != "github.com" {
		return Hosting{}, errors.New("pull requests and CI are shown for GitHub repositories")
	}
	h := Hosting{Provider: "github", Repo: strings.Trim(u.Path, "/")}
	gh, ok := GHPath()
	if !ok {
		h.Problem = "Install the GitHub CLI (brew install gh) and run “gh auth login” to see pull requests and CI here"
		return h, nil
	}
	run := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, gh, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
		out, err := cmd.Output()
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return out, err
	}
	if !st.Detached && st.Branch != "" {
		if out, err := run("pr", "list", "--repo", h.Repo, "--head", st.Branch, "--state", "open", "--json", "number,title,url,isDraft,reviewDecision"); err == nil {
			_ = json.Unmarshal(out, &h.PRs)
		} else {
			h.Problem = friendlyGH(err.Error(), h.Repo)
			return h, nil
		}
		if out, err := run("run", "list", "--repo", h.Repo, "--branch", st.Branch, "--limit", "3", "--json", "workflowName,status,conclusion,url,createdAt"); err == nil {
			_ = json.Unmarshal(out, &h.Runs)
		}
	}
	return h, nil
}

func friendlyGH(msg, repo string) string {
	switch {
	case strings.Contains(msg, "auth login") || strings.Contains(msg, "not logged"):
		return "Sign the GitHub CLI in once: gh auth login"
	case strings.Contains(msg, "Could not resolve") || strings.Contains(msg, "not found"):
		return "The GitHub CLI's account can't see " + repo + " (it may belong to another of your accounts)"
	}
	return msg
}
