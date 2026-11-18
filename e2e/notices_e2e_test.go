//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/localsites/localsites/internal/api"
	"github.com/localsites/localsites/internal/engine"
)

func TestAgentRequestNotice(t *testing.T) {
	ev := setup(t)
	srv := &api.Server{E: ev.e}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	c, err := api.Dial(ev.e.P)
	if err != nil {
		t.Fatal(err)
	}
	agent, app := c.As("mcp"), c.As("app")
	view := func() engine.NoticeView {
		var v engine.NoticeView
		raw, err := app.Call("notices.list", nil)
		if err != nil || json.Unmarshal(raw, &v) != nil {
			t.Fatalf("notices.list: %v", err)
		}
		return v
	}

	done := make(chan error, 1)
	go func() { _, err := agent.Call("stop", nil); done <- err }()
	var id string
	for i := 0; i < 50 && id == ""; i++ {
		for _, n := range view().Notices {
			if n.Kind == "agent" && !n.Read {
				id = n.Ref
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("an agent request waiting for approval must create a notice")
	}
	if _, err := app.Call("approvals.decide", map[string]any{"id": id, "approve": false}); err != nil {
		t.Fatal(err)
	}
	<-done
	for _, n := range view().Notices {
		if n.Ref == id && !n.Read {
			t.Fatal("the notice should be read once the request was answered")
		}
	}

	// Do Not Disturb is saved and reported; agents' requests still pop up (checked in unit tests)
	if _, err := app.Call("notices.quiet", map[string]any{"minutes": 30}); err != nil {
		t.Fatal(err)
	}
	if v := view(); v.QuietUntil == nil {
		t.Fatal("quiet_until not reported")
	}
	if _, err := app.Call("notices.off", map[string]any{"kinds": []string{"agent"}}); err == nil {
		t.Fatal("agent notices can't be switched off")
	}
}
