package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/localsites/localsites/internal/engine"
	"github.com/localsites/localsites/internal/paths"
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
	// the app lists the grants and can end one early
	gs := a.activeGrants()
	if len(gs) == 0 || gs[0].Method != "db.import" || gs[0].Agent != "Claude Code" || gs[0].Scope != "name:shop" {
		t.Fatalf("grants = %+v", gs)
	}
	for _, g := range gs {
		if err := a.revoke(g.ID); err != nil {
			t.Fatal(err)
		}
	}
	if a.revoke(gs[0].ID) == nil {
		t.Error("ending an approval twice should say it already ended")
	}
	if !ask("db.import", "mcp", "Claude Code", `{"name":"shop"}`) {
		t.Error("a revoked approval still counted")
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

// Only the person can end an approval early: an agent's request to end one is refused and the grant stays.
func TestRevokeIsPersonOnly(t *testing.T) {
	s := &Server{E: &engine.Engine{P: paths.Paths{Logs: t.TempDir()}}, token: "tok"}
	s.pending.grants = []grant{{id: "g1", client: "mcp", agent: "Claude Code", method: "db.import", scope: "name:shop", until: time.Now().Add(time.Minute)}}
	call := func(client, method, body string) (int, string) {
		r := httptest.NewRequest("POST", "/call/"+method, strings.NewReader(body))
		r.SetPathValue("method", method)
		r.Header.Set("Authorization", "Bearer tok")
		r.Header.Set("X-LocalSites-Client", client)
		w := httptest.NewRecorder()
		s.handle(w, r)
		return w.Code, w.Body.String()
	}
	if code, out := call("mcp", "approvals.revoke", `{"id":"g1"}`); code == 200 || !strings.Contains(out, "only the person") {
		t.Fatalf("an agent ended an approval: %d %s", code, out)
	}
	if len(s.pending.activeGrants()) != 1 {
		t.Fatal("the grant was removed by an agent")
	}
	if _, out := call("mcp", "approvals.grants", `{}`); !strings.Contains(out, `"method":"db.import"`) {
		t.Errorf("grants = %s", out)
	}
	if !canProveApp() { // without signatures the app is who it says it is (development builds, Linux)
		if code, out := call("app", "approvals.revoke", `{"id":"g1"}`); code != 200 {
			t.Fatalf("the app couldn't end an approval: %d %s", code, out)
		}
		if len(s.pending.activeGrants()) != 0 {
			t.Error("the grant is still there")
		}
	}
}
