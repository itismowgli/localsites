package addons

import (
	"strings"
	"testing"
)

func TestCatalogListensLocallyOnly(t *testing.T) {
	for _, d := range Catalog {
		args := strings.Join(d.Args(Options{Port: d.Port, UIPort: d.UIPort}, "/tmp/x"), " ")
		if !strings.Contains(args, "127.0.0.1") {
			t.Errorf("%s does not bind to 127.0.0.1: %s", d.ID, args)
		}
		if strings.Contains(args, "0.0.0.0") {
			t.Errorf("%s listens on every interface: %s", d.ID, args)
		}
	}
}

func TestSnippetsCoverEveryStack(t *testing.T) {
	want := []string{"Laravel", "Node / Next.js", "Django", "Rails", "Any language"}
	for _, d := range Catalog {
		got := map[string]string{}
		for _, s := range d.Snippets(Options{Port: 4242}) {
			got[s.Stack] = s.Text
		}
		for _, w := range want {
			if !strings.Contains(got[w], "4242") {
				t.Errorf("%s: %s snippet missing or without the port: %q", d.ID, w, got[w])
			}
		}
	}
}

func TestOptions(t *testing.T) {
	m, _ := Find("mailpit")
	if a := strings.Join(m.Args(Options{Port: 1025, UIPort: 8025}, "/d"), " "); strings.Contains(a, "--database") {
		t.Errorf("mail kept without asking: %s", a)
	}
	if a := strings.Join(m.Args(Options{Port: 1025, UIPort: 8025, KeepMail: true}, "/d"), " "); !strings.Contains(a, "--database /d/mailpit.db") {
		t.Errorf("keep mail: %s", a)
	}
	v, _ := Find("valkey")
	if a := v.Args(Options{Port: 6380}, "/d"); !strings.Contains(strings.Join(a, " "), "--save ") || a[len(a)-1] != "" {
		t.Errorf("valkey must not write to disk by default: %v", a)
	}
	mc, _ := Find("memcached")
	if a := strings.Join(mc.Args(Options{Port: 11211, MemoryMB: 128}, ""), " "); !strings.Contains(a, "-m 128") || !strings.Contains(a, "-U 0") {
		t.Errorf("memcached: %s", a)
	}
	if SendmailPath("/opt/homebrew/bin/mailpit", 1025) != "/opt/homebrew/bin/mailpit sendmail -S 127.0.0.1:1025" {
		t.Error("sendmail path")
	}
}
