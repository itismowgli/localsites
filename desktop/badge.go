package main

import (
	"fmt"
	"strings"
)

// needs is everything the Dock badge counts. The Overview's "Needs attention" list shows the same items.
type needs struct {
	Access    int // folders waiting for macOS access
	Checks    int // failed Doctor checks (from the last run)
	Sites     int // running projects with a problem
	Approvals int // agent requests waiting for approval
	Problems  int // problems not yet sent or dismissed
	Update    bool
}

func (n needs) parts() []string {
	var out []string
	add := func(c int, one, many string) {
		if c == 1 {
			out = append(out, "1 "+one)
		} else if c > 1 {
			out = append(out, fmt.Sprintf("%d %s", c, many))
		}
	}
	add(n.Access, "folder waiting for macOS access", "folders waiting for macOS access")
	add(n.Checks, "failed Doctor check", "failed Doctor checks")
	add(n.Sites, "running project with a problem", "running projects with a problem")
	add(n.Approvals, "agent request", "agent requests")
	add(n.Problems, "problem to look at", "problems to look at")
	if n.Update {
		out = append(out, "1 update")
	}
	return out
}

// total is the number on the Dock icon.
func (n needs) total() int {
	t := n.Access + n.Checks + n.Sites + n.Approvals + n.Problems
	if n.Update {
		t++
	}
	return t
}

// summary says what the number is made of, for the menu bar, e.g. "2 failed Doctor checks and 1 problem to look at".
func (n needs) summary() string {
	p := n.parts()
	switch len(p) {
	case 0:
		return ""
	case 1:
		return p[0]
	}
	return strings.Join(p[:len(p)-1], ", ") + " and " + p[len(p)-1]
}

// updateWaiting reports whether an update counts towards the badge: available or ready, and not skipped.
func updateWaiting(upd updState) bool {
	return upd.Latest != nil && !upd.Skipped && (upd.Phase == "available" || upd.Phase == "ready")
}
