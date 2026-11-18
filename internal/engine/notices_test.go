package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/notices"
	"github.com/localsites/localsites/internal/paths"
	"github.com/localsites/localsites/internal/share"
)

func noticeEngine(t *testing.T) (*Engine, *[]string) {
	t.Helper()
	dir := t.TempDir()
	st, err := config.Open(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{P: paths.Paths{Data: dir, Config: dir}, Store: st}
	var shown []string
	old := sendSystem
	sendSystem = func(title, text, kind, ref string) { shown = append(shown, kind+":"+title+":"+ref) }
	t.Cleanup(func() { sendSystem = old })
	return e, &shown
}

func TestNotifyKeepsAndShows(t *testing.T) {
	e, shown := noticeEngine(t)
	e.Notify(notices.Notice{Kind: notices.KindCrash, Title: "Apache stopped", Ref: "apache"})
	if v := e.Notices(); v.Unread != 1 || len(*shown) != 1 || (*shown)[0] != "crash:Apache stopped:apache" {
		t.Fatalf("view %+v shown %v", v, *shown)
	}

	// Kinds switched off are kept but don't pop up; agent requests can't be switched off
	if err := e.SetNoticesOff([]string{notices.KindCrash, notices.KindInstall}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetNoticesOff([]string{notices.KindAgent}); err == nil {
		t.Fatal("agent requests must always pop up")
	}
	if err := e.SetNoticesOff([]string{"nonsense"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	e.Notify(notices.Notice{Kind: notices.KindCrash, Title: "MySQL stopped"})
	e.Notify(notices.Notice{Kind: notices.KindUpdate, Title: "1.5 is ready"})
	if len(*shown) != 2 || e.Notices().Unread != 3 {
		t.Fatalf("off: shown %v", *shown)
	}

	// Do Not Disturb: nothing pops up except agent requests, everything is kept
	if err := e.QuietFor(60); err != nil {
		t.Fatal(err)
	}
	if v := e.Notices(); v.QuietUntil == nil || time.Until(*v.QuietUntil) < 59*time.Minute {
		t.Fatalf("quiet until %v", v.QuietUntil)
	}
	e.Notify(notices.Notice{Kind: notices.KindUpdate, Title: "quiet"})
	e.Notify(notices.Notice{Kind: notices.KindAgent, Title: "Claude Code is waiting", Ref: "r1"})
	if len(*shown) != 3 || !strings.HasPrefix((*shown)[2], "agent:") {
		t.Fatalf("quiet: shown %v", *shown)
	}
	if err := e.QuietFor(-1); err != nil || e.Notices().QuietUntil.Year() < 2100 {
		t.Fatal("until turned off")
	}
	if err := e.QuietFor(0); err != nil || e.Notices().QuietUntil != nil {
		t.Fatal("turned off")
	}
	if err := e.QuietFor(-5); err == nil {
		t.Fatal("bad minutes accepted")
	}

	// Answering a request marks its notice as read
	before := e.Notices().Unread
	e.ReadNoticesAbout("r1")
	if e.Notices().Unread != before-1 {
		t.Fatal("ReadNoticesAbout")
	}
	e.ReadNotices("")
	if e.Notices().Unread != 0 {
		t.Fatal("read all")
	}
	e.ClearNotices()
	if len(e.Notices().Notices) != 0 {
		t.Fatal("clear")
	}
}

func TestMinutesLeft(t *testing.T) {
	if minutesLeft(30*time.Second) != "a minute" || minutesLeft(4*time.Minute+40*time.Second) != "5 minutes" {
		t.Fatal(minutesLeft(4*time.Minute + 40*time.Second))
	}
}

func TestShareNotices(t *testing.T) {
	e, shown := noticeEngine(t)
	start := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	sh := share.Share{ID: "shop-1", Folder: "shop", Started: start, Expires: start.Add(time.Hour)}
	e.watchShares(start, []share.Share{sh})
	if len(*shown) != 0 {
		t.Fatalf("too early: %v", *shown)
	}
	e.watchShares(start.Add(56*time.Minute), []share.Share{sh})
	e.watchShares(start.Add(57*time.Minute), []share.Share{sh}) // only once
	if len(*shown) != 1 || (*shown)[0] != "share:shop stops being shared in 4 minutes:shop-1" {
		t.Fatalf("ending soon: %v", *shown)
	}
	// Extended: warned again before the new end
	sh.Expires = start.Add(90 * time.Minute)
	e.watchShares(start.Add(58*time.Minute), []share.Share{sh})
	e.watchShares(start.Add(86*time.Minute), []share.Share{sh})
	if len(*shown) != 2 {
		t.Fatalf("after extending: %v", *shown)
	}
	// It ran out: one more notice, and the "ends soon" one is marked read
	e.watchShares(start.Add(91*time.Minute), nil)
	if len(*shown) != 3 || (*shown)[2] != "share:shop is no longer shared:" {
		t.Fatalf("ended: %v", *shown)
	}
	for _, n := range e.Notices().Notices {
		if n.Ref == "shop-1" && !n.Read {
			t.Fatal("the ends-soon notice should be read once the share ended")
		}
	}
	// Stopped by hand long before its end: no notice
	other := share.Share{ID: "blog-1", Folder: "blog", Started: start, Expires: start.Add(10 * time.Hour)}
	e.watchShares(start.Add(time.Hour), []share.Share{other})
	e.watchShares(start.Add(2*time.Hour), nil)
	if len(*shown) != 3 {
		t.Fatalf("stopped by hand: %v", *shown)
	}
}
