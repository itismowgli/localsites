package main

import "testing"

func TestNeeds(t *testing.T) {
	ver := &struct {
		Version string `json:"version"`
	}{"1.0.3"}
	cases := []struct {
		name    string
		n       needs
		total   int
		summary string
	}{
		{"nothing", needs{}, 0, ""},
		{"one kind", needs{Problems: 1}, 1, "1 problem to look at"},
		{"two kinds", needs{Checks: 2, Problems: 1}, 3, "2 failed Doctor checks and 1 problem to look at"},
		{"everything", needs{Access: 1, Checks: 1, Sites: 2, Approvals: 1, Problems: 3, Update: true}, 9,
			"1 folder waiting for macOS access, 1 failed Doctor check, 2 running projects with a problem, 1 agent request, 3 problems to look at and 1 update"},
	}
	for _, c := range cases {
		if got := c.n.total(); got != c.total {
			t.Errorf("%s: total %d, want %d", c.name, got, c.total)
		}
		if got := c.n.summary(); got != c.summary {
			t.Errorf("%s: summary %q, want %q", c.name, got, c.summary)
		}
	}
	if !updateWaiting(updState{Phase: "ready", Latest: ver}) || updateWaiting(updState{Phase: "downloading", Latest: ver}) ||
		updateWaiting(updState{Phase: "available", Latest: ver, Skipped: true}) {
		t.Error("updateWaiting: wrong answer")
	}
}
