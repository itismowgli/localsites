package engine

import (
	"testing"
	"time"

	"github.com/localsites/localsites/internal/paths"
)

func TestUptimeHours(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{P: paths.Paths{Data: dir}}
	t0 := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	for m := 0; m < 3; m++ {
		e.recordUptime(t0.Add(time.Duration(m)*time.Minute), map[string]bool{"shop": m != 1})
	}
	e.recordUptime(t0.Add(time.Hour), map[string]bool{"shop": true, "blog": false})
	h := e.Uptime()
	if len(h["shop"]) != 2 || h["shop"][0].Up != 2 || h["shop"][0].Down != 1 || h["shop"][1].Up != 1 {
		t.Fatalf("shop = %+v", h["shop"])
	}
	if len(h["blog"]) != 1 || h["blog"][0].Down != 1 {
		t.Fatalf("blog = %+v", h["blog"])
	}
	// saved: a restarted service reads the same history
	e2 := &Engine{P: paths.Paths{Data: dir}}
	if got := e2.Uptime(); len(got["shop"]) != 2 {
		t.Fatalf("after restart = %+v", got)
	}
	// a day later the old hours are gone, and a project not sampled since disappears
	e2.recordUptime(t0.Add(26*time.Hour), map[string]bool{"blog": true})
	got := e2.Uptime()
	if _, ok := got["shop"]; ok || len(got["blog"]) != 1 {
		t.Fatalf("old hours kept: %+v", got)
	}
}
