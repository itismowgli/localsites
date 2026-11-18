package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localsites/localsites/internal/paths"
)

func TestResolverOK(t *testing.T) {
	dir := t.TempDir()
	old := resolverDir
	resolverDir = dir
	defer func() { resolverDir = old }()
	if resolverOK("test", 5354) {
		t.Fatal("missing file counts as set up")
	}
	_ = os.WriteFile(filepath.Join(dir, "test"), []byte(resolverContent(5354)), 0o644)
	if !resolverOK("test", 5354) {
		t.Fatal("our own file isn't recognised")
	}
	if resolverOK("test", 5355) {
		t.Fatal("a different DNS port must not count")
	}
	// Valet's dnsmasq file: nameserver 127.0.0.1 on port 53 is someone else's DNS
	_ = os.WriteFile(filepath.Join(dir, "test"), []byte("nameserver 127.0.0.1\n"), 0o644)
	if resolverOK("test", 5354) {
		t.Fatal("Valet's resolver (port 53) must not count as LocalSites'")
	}
}

func TestDomainsItem(t *testing.T) {
	d := domainsState{need: true, tld: "test", dnsPort: 5354, caFile: "/data/ca/root.crt"}
	it, ok := domainsItem(d)
	if !ok || it.ID != "domains" || !it.Password || it.Kind != "system" || !it.Required {
		t.Fatalf("item = %+v %v", it.SetupItem, ok)
	}
	if !strings.Contains(it.Why, ".test addresses don’t open yet") || !strings.Contains(it.Why, "not private") {
		t.Fatalf("why = %q", it.Why)
	}
	st := it.steps[0].domains
	if st == nil || !st.resolver || !st.trust || st.tld != "test" || st.dnsPort != 5354 || st.caFile != "/data/ca/root.crt" {
		t.Fatalf("step = %+v", st)
	}

	d.resolver = true // only the certificate is left
	it, _ = domainsItem(d)
	if st := it.steps[0].domains; st.resolver || !st.trust || strings.Contains(it.Why, "don’t open") {
		t.Fatalf("certificate only: %+v %q", st, it.Why)
	}
	d.trusted = true
	if _, ok := domainsItem(d); ok {
		t.Fatal("nothing to do, but an item was made")
	}
	if _, ok := domainsItem(domainsState{need: false}); ok {
		t.Fatal("Valet serves the domains: no item")
	}
	// The plan puts it first, before Homebrew installs
	plan := buildPlan(planInput{apache: true, mysql: true, php: nil, users: map[string]int{}, tapOf: func(string) string { return "" },
		rootOwned: func(string) ([]string, []string) { return nil, nil }, domains: domainsState{need: true, tld: "test", dnsPort: 5354}})
	if len(plan) < 2 || plan[0].ID != "domains" {
		t.Fatalf("plan = %v", plan)
	}
}

func TestRunSetupDomainsWithoutHomebrew(t *testing.T) {
	e := &Engine{P: paths.Paths{Logs: t.TempDir()}}
	oldApply, oldDomains := setupApply, setupDomains
	defer func() { setupApply, setupDomains = oldApply, oldDomains }()
	setupApply = func(*Engine) {}
	var got []domainsStep
	setupDomains = func(_ context.Context, st domainsStep) error { got = append(got, st); return nil }
	items := []planItem{{SetupItem: SetupItem{ID: "domains"}, steps: []step{{label: "set up", domains: &domainsStep{resolver: true, tld: "test", dnsPort: 5354}}}}}
	e.setup.running, e.setup.results = true, map[string]string{}
	e.runSetup("", "", items) // no Homebrew at all
	if len(got) != 1 || got[0].tld != "test" || e.setup.results["domains"] != "ok" {
		t.Fatalf("ran %v results %v", got, e.setup.results)
	}
}
