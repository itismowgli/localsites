package engine

import (
	"github.com/localsites/localsites/internal/gitops"
)

// Git runs a git panel action for a project (see package gitops for the safety rules).
func (e *Engine) Git(method, folder, name string, lines int) (any, error) {
	s, err := e.site(folder)
	if err != nil {
		return nil, err
	}
	dir := s.Path
	switch method {
	case "git.hosting":
		return gitops.GetHosting(dir)
	case "git.status":
		return gitops.GetStatus(dir)
	case "git.branches":
		return gitops.Branches(dir)
	case "git.log":
		return gitops.Log(dir, lines)
	case "git.fetch":
		return map[string]string{"log": "Fetched"}, gitops.Fetch(dir)
	case "git.pull":
		msg, err := gitops.Pull(dir)
		return map[string]string{"log": msg}, err
	case "git.checkout":
		return map[string]string{"log": "Switched to " + name}, gitops.Checkout(dir, name)
	case "git.branch":
		return map[string]string{"log": "Created and switched to " + name}, gitops.CreateBranch(dir, name)
	}
	return nil, nil
}
