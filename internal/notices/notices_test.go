package notices

import (
	"fmt"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	s := Open(dir)
	s.now = func() time.Time { return now }

	a := s.Add(Notice{Kind: KindCrash, Title: "Apache stopped", Open: "log:apache"})
	now = now.Add(time.Minute)
	b := s.Add(Notice{Kind: KindAgent, Title: "Claude Code is waiting", Ref: "req-1"})
	list, unread := s.List()
	if len(list) != 2 || unread != 2 || list[0].ID != b.ID || list[1].ID != a.ID || a.ID == "" {
		t.Fatalf("list = %+v unread %d", list, unread)
	}

	s.MarkRead(a.ID)
	if _, unread := s.List(); unread != 1 {
		t.Fatalf("after reading one: %d", unread)
	}
	if !s.ReadRef("req-1") || s.ReadRef("req-1") || s.ReadRef("") {
		t.Fatal("ReadRef marks the matching unread notice once")
	}

	// Saved and loaded again
	s2 := Open(dir)
	if list, unread := s2.List(); len(list) != 2 || unread != 0 || list[0].Title != "Claude Code is waiting" {
		t.Fatalf("reloaded = %+v %d", list, unread)
	}

	// The newest 200 are kept
	for i := 0; i < 250; i++ {
		s.Add(Notice{Kind: KindInstall, Title: fmt.Sprint(i)})
	}
	if list, _ := s.List(); len(list) != 200 || list[0].Title != "249" {
		t.Fatalf("limit: %d, newest %q", len(list), list[0].Title)
	}

	// Older than 30 days are dropped
	now = now.Add(31 * 24 * time.Hour)
	s.Add(Notice{Kind: KindUpdate, Title: "new"})
	if list, _ := s.List(); len(list) != 1 || list[0].Title != "new" {
		t.Fatalf("age limit: %d", len(list))
	}

	s.MarkRead("")
	s.Clear()
	if list, unread := Open(dir).List(); len(list) != 0 || unread != 0 {
		t.Fatal("clear")
	}
}
