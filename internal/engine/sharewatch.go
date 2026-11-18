package engine

import (
	"strconv"
	"sync"
	"time"

	"github.com/localsites/localsites/internal/notices"
	"github.com/localsites/localsites/internal/share"
)

// Shares about to end get a notice 5 minutes before (with Extend / Stop on macOS); a share that ended by
// itself gets one too. Called every 30 seconds by the service.

var shareWatch struct {
	sync.Mutex
	warned map[string]bool      // share id: the 5-minute notice was sent
	seen   map[string]seenShare // as of the last check
}

type seenShare struct {
	folder  string
	expires time.Time
}

// WatchShares sends the "ends soon" and "has ended" notices.
func (e *Engine) WatchShares(now time.Time) {
	if e.Shares != nil {
		e.watchShares(now, e.Shares.List())
	}
}

func (e *Engine) watchShares(now time.Time, list []share.Share) {
	shareWatch.Lock()
	defer shareWatch.Unlock()
	if shareWatch.warned == nil {
		shareWatch.warned, shareWatch.seen = map[string]bool{}, map[string]seenShare{}
	}
	current := map[string]seenShare{}
	for _, s := range list {
		current[s.ID] = seenShare{s.Folder, s.Expires}
		left := s.Expires.Sub(now)
		if left <= 5*time.Minute && left > 0 && !shareWatch.warned[s.ID] {
			shareWatch.warned[s.ID] = true
			e.Notify(notices.Notice{Kind: notices.KindShare, Title: s.Folder + " stops being shared in " + minutesLeft(left),
				Text: "Its public address stops working then. Extend it or stop it now.", Ref: s.ID, Open: "proj:" + s.Folder})
		}
		if left > 5*time.Minute {
			shareWatch.warned[s.ID] = false // extended: warn again before the new end
		}
	}
	for id, was := range shareWatch.seen {
		if _, still := current[id]; !still {
			e.ReadNoticesAbout(id)
			if !now.Before(was.expires.Add(-30 * time.Second)) { // it ran out (one stopped by hand needs no notice)
				e.Notify(notices.Notice{Kind: notices.KindShare, Title: was.folder + " is no longer shared", Text: "Its public address has stopped working.", Open: "proj:" + was.folder})
			}
			delete(shareWatch.warned, id)
		}
	}
	shareWatch.seen = current
}

func minutesLeft(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m <= 1 {
		return "a minute"
	}
	return strconv.Itoa(m) + " minutes"
}
