package main

import "testing"

func TestAttention(t *testing.T) {
	ver := &struct {
		Version string `json:"version"`
	}{"1.0.3"}
	cases := []struct {
		name      string
		approvals int
		problems  int
		upd       updState
		want      int
	}{
		{"nothing", 0, 0, updState{Phase: "idle"}, 0},
		{"two approvals and a problem", 2, 1, updState{}, 3},
		{"update ready", 0, 0, updState{Phase: "ready", Latest: ver}, 1},
		{"update available", 1, 0, updState{Phase: "available", Latest: ver}, 2},
		{"skipped update doesn't count", 0, 0, updState{Phase: "available", Latest: ver, Skipped: true}, 0},
		{"downloading doesn't count twice", 0, 0, updState{Phase: "downloading", Latest: ver}, 0},
	}
	for _, c := range cases {
		if got := attention(c.approvals, c.problems, c.upd); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
