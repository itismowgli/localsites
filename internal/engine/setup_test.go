package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localsites/localsites/internal/paths"
	"github.com/localsites/localsites/internal/runtime"
)

func noTap(string) string                { return "" }
func noRoot(string) ([]string, []string) { return nil, nil }
func steps(it planItem) string           { return strings.Join(it.Steps, " | ") }
func find(items []planItem, id string) *planItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func TestPlanFreshMac(t *testing.T) {
	items := buildPlan(planInput{users: map[string]int{}, tapOf: noTap, rootOwned: noRoot})
	for _, id := range []string{"httpd", "mysql@8.4", "php"} {
		if find(items, id) == nil {
			t.Fatalf("a Mac without anything needs %s: %+v", id, items)
		}
	}
	if find(items, "nginx") != nil {
		t.Fatal("nginx only when a project uses it")
	}
}

func TestPlanNothingToDo(t *testing.T) {
	in := planInput{apache: true, mysql: true, php: []runtime.PHP{{Version: "8.4"}}, users: map[string]int{"8.4": 3}, tapOf: noTap, rootOwned: noRoot}
	if items := buildPlan(in); len(items) != 0 {
		t.Fatalf("nothing should be listed: %+v", items)
	}
}

func TestPlanRepairsAndMissing(t *testing.T) {
	in := planInput{apache: true, mysql: true, trust: true, tapped: false,
		php:    []runtime.PHP{{Version: "8.4"}, {Version: "7.4", Warning: "intl can't load (icu4c@77 is missing). Run: brew reinstall php@7.4"}},
		broken: []runtime.BrokenPHP{{Version: "7.2", Reason: "libjpeg.9.dylib is missing"}},
		users:  map[string]int{"7.2": 8, "7.4": 1, "5.6": 2, "8.4": 0, "bogus;rm": 1},
		tapOf: func(f string) string {
			if f == "php@8.4" {
				return ""
			}
			return phpTap
		},
		rootOwned: func(f string) ([]string, []string) {
			if f == "php@7.4" {
				return []string{"/opt/homebrew/Cellar/php@7.4"}, []string{"/opt/homebrew/Cellar/php@7.4/7.4.33_10.reinstall"}
			}
			return nil, nil
		}}
	items := buildPlan(in)
	if len(items) != 3 {
		t.Fatalf("want 7.2, 7.4 and 5.6: %+v", items)
	}
	b := find(items, "php@7.2")
	if b.Kind != "repair" || !b.Required || steps(*b) != "brew trust shivammathur/php | brew reinstall shivammathur/php/php@7.2" || !strings.Contains(b.Why, "8 projects use it") {
		t.Fatalf("7.2: %+v", b)
	}
	w := find(items, "php@7.4")
	if !w.Password || w.steps[0].chown == nil || len(w.steps[0].clean) != 1 || !strings.HasPrefix(w.Why, "intl can't load") || strings.Contains(w.Why, "Run:") || !strings.Contains(w.Why, "1 project uses it") {
		t.Fatalf("7.4: %+v", w)
	}
	m := find(items, "php@5.6")
	if m.Kind != "install" || steps(*m) != "brew tap shivammathur/php | brew trust shivammathur/php | brew install shivammathur/php/php@5.6" {
		t.Fatalf("5.6: %+v", m)
	}
}

func TestPlanNginxWhenNeeded(t *testing.T) {
	in := planInput{apache: true, mysql: true, needNginx: true, php: []runtime.PHP{{Version: "8.4"}}, users: map[string]int{}, tapOf: noTap, rootOwned: noRoot}
	if find(buildPlan(in), "nginx") == nil {
		t.Fatal("nginx is needed")
	}
}

func TestRootOwnedFindsLeftovers(t *testing.T) {
	prefix := t.TempDir()
	keg := filepath.Join(prefix, "Cellar", "php@7.2", "7.2.34_14.reinstall", "bin")
	_ = os.MkdirAll(keg, 0o755)
	paths, left := rootOwnedIn(prefix)("php@7.2")
	if len(paths) != 0 || len(left) != 1 || !strings.HasSuffix(left[0], ".reinstall") {
		t.Fatalf("paths %v left %v", paths, left)
	}
	if p, l := rootOwnedIn(prefix)("../../etc"); p != nil || l != nil {
		t.Fatal("odd formula names are ignored")
	}
}

func TestRunSetupSteps(t *testing.T) {
	e := &Engine{P: paths.Paths{Logs: t.TempDir()}}
	oldApply := setupApply
	defer func() { setupApply = oldApply }()
	setupApply = func(*Engine) {}
	var ran []string
	oldExec, oldChown := setupExec, setupChown
	defer func() { setupExec, setupChown = oldExec, oldChown }()
	setupExec = func(_ context.Context, _ string, args []string, _ io.Writer) error {
		ran = append(ran, strings.Join(args, " "))
		if args[0] == "install" {
			return io.ErrUnexpectedEOF
		}
		return nil
	}
	var chowned []string
	setupChown = func(_ context.Context, p []string) error { chowned = append(chowned, p...); return nil }
	prefix := t.TempDir()
	left := filepath.Join(prefix, "Cellar", "php@7.4", "7.4.33_10.reinstall")
	_ = os.MkdirAll(left, 0o755)
	items := []planItem{
		{SetupItem: SetupItem{ID: "php@7.4"}, steps: []step{
			{label: "own", chown: []string{filepath.Join(prefix, "Cellar", "php@7.4")}, clean: []string{left}},
			{label: "reinstall", brew: []string{"reinstall", "x/y/php@7.4"}}}},
		{SetupItem: SetupItem{ID: "evil"}, steps: []step{{label: "own", chown: []string{"/etc"}}, {label: "never", brew: []string{"never"}}}},
		{SetupItem: SetupItem{ID: "httpd"}, steps: []step{{label: "install", brew: []string{"install", "httpd"}}}},
	}
	e.setup.running, e.setup.results = true, map[string]string{}
	e.runSetup("/bin/false", prefix, items)
	if len(chowned) != 1 || strings.Join(ran, ",") != "reinstall x/y/php@7.4,install httpd" {
		t.Fatalf("chowned %v ran %v", chowned, ran)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Fatal("the half-finished reinstall should be removed")
	}
	r := e.setup.results
	if r["php@7.4"] != "ok" || !strings.Contains(r["evil"], "outside Homebrew") || r["httpd"] == "ok" || e.setup.running {
		t.Fatalf("results %v running %v", r, e.setup.running)
	}
}
