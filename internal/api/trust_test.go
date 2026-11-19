package api

import (
	"strings"
	"testing"
	"time"
)

func TestCallerTrust(t *testing.T) {
	cases := []struct {
		client           string
		proven, canProve bool
		trusted, askable bool
	}{
		// signed release on macOS: only the proven app is trusted; the terminal asks
		{"app", true, true, true, false},
		{"app", false, true, false, false}, // a program calling itself the app
		{"cli", false, true, false, true},
		{"cli", true, true, false, true}, // the CLI mode of the signed binary still isn't the app window
		{"mcp", true, true, false, false},
		{"devui", false, true, false, false},
		// development builds and Linux: names decide, as before
		{"app", false, false, true, false},
		{"cli", false, false, true, false},
		{"mcp", false, false, false, false},
		{"cli-script", false, false, false, false},
	}
	for _, c := range cases {
		tr, ask := callerTrust(c.client, c.proven, c.canProve)
		if tr != c.trusted || ask != c.askable {
			t.Errorf("%s proven=%v canProve=%v: trusted=%v askable=%v", c.client, c.proven, c.canProve, tr, ask)
		}
	}
}

func TestRiskyActionsAsk(t *testing.T) {
	for _, m := range []string{"import.database", "agents.connect", "agents.disconnect", "sites.takeover", "sites.install", "addons.install"} {
		if !NeedsApproval("mcp", m, []byte(`{}`)) {
			t.Errorf("%s runs without asking", m)
		}
		if NeedsApproval("app", m, []byte(`{}`)) {
			t.Errorf("%s asks the app itself", m)
		}
	}
	if !NeedsApproval("mcp", "tools.adminer", []byte(`{"enabled":true}`)) || NeedsApproval("mcp", "tools.adminer", []byte(`{"enabled":false}`)) {
		t.Error("Adminer should ask only when switched on")
	}
	if !needsApproval(false, "sites.list", nil) == false {
		t.Error("reading asks")
	}
}

func TestApproveForAWhile(t *testing.T) {
	a := &approvals{}
	ask := func(method, client, agent, body string) (asked bool) {
		done := make(chan struct{})
		res := make(chan error, 1)
		go func() { res <- a.wait(method, client, agent, []byte(body), done) }()
		for i := 0; i < 100; i++ {
			if l := a.list(); len(l) > 0 {
				if err := a.decide(l[0].ID, true, true); err != nil && !strings.Contains(err.Error(), "asks every time") {
					t.Fatal(err)
				} else if err != nil {
					_ = a.decide(l[0].ID, true, false)
				}
				<-res
				return true
			}
			select {
			case err := <-res:
				if err != nil {
					t.Fatal(err)
				}
				return false
			case <-time.After(5 * time.Millisecond):
			}
		}
		t.Fatal("no answer")
		return
	}
	if !ask("db.import", "mcp", "Claude Code", `{"name":"shop"}`) {
		t.Fatal("the first request must ask")
	}
	if ask("db.import", "mcp", "Claude Code", `{"name":"shop"}`) {
		t.Error("the same request was asked again within 10 minutes")
	}
	for _, other := range [][3]string{
		{"db.import", "Cursor", `{"name":"shop"}`},      // another agent
		{"db.import", "Claude Code", `{"name":"blog"}`}, // another database
		{"db.export", "Claude Code", `{"name":"shop"}`}, // another action
	} {
		if !ask(other[0], "mcp", other[1], other[2]) {
			t.Errorf("%v rode on an earlier approval", other)
		}
	}
	if !ask("db.drop", "mcp", "Claude Code", `{"name":"shop"}`) || !ask("db.drop", "mcp", "Claude Code", `{"name":"shop"}`) {
		t.Error("deleting a database must ask every time")
	}
	// expired grants are forgotten
	a.mu.Lock()
	for i := range a.grants {
		a.grants[i].until = time.Now().Add(-time.Second)
	}
	a.mu.Unlock()
	if !ask("db.import", "mcp", "Claude Code", `{"name":"shop"}`) {
		t.Error("an expired approval still counted")
	}
}

func TestAgentName(t *testing.T) {
	if got := agentName("  Claude\x00 Code\n "); got != "Claude Code" {
		t.Errorf("%q", got)
	}
	if got := agentName(strings.Repeat("x", 100)); len(got) != 40 {
		t.Errorf("not shortened: %d", len(got))
	}
}
