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
	if resolverOK("darwin", "test", 5354) {
		t.Fatal("missing file counts as set up")
	}
	_ = os.WriteFile(filepath.Join(dir, "test"), []byte(resolverContent(5354)), 0o644)
	if !resolverOK("darwin", "test", 5354) {
		t.Fatal("our own file isn't recognised")
	}
	if resolverOK("darwin", "test", 5355) {
		t.Fatal("a different DNS port must not count")
	}
	// Valet's dnsmasq file: nameserver 127.0.0.1 on port 53 is someone else's DNS
	_ = os.WriteFile(filepath.Join(dir, "test"), []byte("nameserver 127.0.0.1\n"), 0o644)
	if resolverOK("darwin", "test", 5354) {
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

func TestDomainsOnLinux(t *testing.T) {
	dir := t.TempDir()
	oldDrop, oldAnchors, oldPorts := resolvedDrop, caAnchors, portStartFile
	resolvedDrop, caAnchors, portStartFile = filepath.Join(dir, "localsites.conf"), []string{filepath.Join(dir, "a.crt"), filepath.Join(dir, "b.crt")}, filepath.Join(dir, "port_start")
	defer func() { resolvedDrop, caAnchors, portStartFile = oldDrop, oldAnchors, oldPorts }()

	if resolverOK("linux", "test", 5354) {
		t.Fatal("missing drop-in counts as set up")
	}
	_ = os.WriteFile(resolvedDrop, []byte(resolvedContent("test", 5354)), 0o644)
	if !resolverOK("linux", "test", 5354) || resolverOK("linux", "test", 5355) || resolverOK("linux", "dev", 5354) {
		t.Fatal("resolved drop-in check")
	}

	_ = os.WriteFile(portStartFile, []byte("1024\n"), 0o644)
	if portsOK("linux", 80) || !portsOK("linux", 8443) || !portsOK("darwin", 80) {
		t.Fatal("ports check with the default 1024")
	}
	_ = os.WriteFile(portStartFile, []byte("80\n"), 0o644)
	if !portsOK("linux", 80) {
		t.Fatal("ports check after the setting")
	}

	ca := filepath.Join(dir, "root.crt")
	_ = os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n"), 0o600)
	if caFileTrusted(ca) {
		t.Fatal("not installed yet")
	}
	_ = os.WriteFile(caAnchors[1], []byte("-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----"), 0o644)
	if !caFileTrusted(ca) {
		t.Fatal("installed in the Fedora anchors")
	}

	d := domainsState{need: true, goos: "linux", tld: "test", dnsPort: 5354, lowPort: 80, caFile: ca, trusted: true, resolver: true}
	it, ok := domainsItem(d)
	if !ok || !it.steps[0].domains.ports || it.steps[0].domains.trust || !strings.Contains(it.Why, "ports 80 and 443") {
		t.Fatalf("ports only: %+v %v", it.SetupItem, ok)
	}
	script, args := domainsCommand(*it.steps[0].domains, "/tmp/x")
	if script != linuxDomainsScript || strings.Join(args, " ") != "/tmp/x "+ca+" 0 0 1 80" {
		t.Fatalf("linux args = %q", args)
	}
	d.ports = true
	if _, ok := domainsItem(d); ok {
		t.Fatal("all set: no item")
	}
	_, args = domainsCommand(domainsStep{goos: "darwin", resolver: true, trust: true, tld: "test", caFile: "/ca.crt"}, "/tmp/y")
	if strings.Join(args, " ") != "/tmp/y test /ca.crt 1 1" {
		t.Fatalf("mac args = %q", args)
	}
}

func TestAdminCommand(t *testing.T) {
	name, args := adminCommand("linux", "Why", "echo \"$1\"", "a b", "$(x)")
	if name != "pkexec" || strings.Join(args, "|") != "/bin/sh|-c|echo \"$1\"|sh|a b|$(x)" {
		t.Fatalf("linux = %s %q", name, args)
	}
	name, args = adminCommand("darwin", "Why", "echo hi", "a b")
	if name != "/usr/bin/osascript" || args[0] != "-e" || args[2] != "echo hi" || args[3] != "a b" || args[4] != "Why" {
		t.Fatalf("mac = %s %q", name, args)
	}
}
