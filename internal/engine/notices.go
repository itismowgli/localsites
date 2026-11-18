package engine

import (
	"errors"
	"time"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/notices"
	"github.com/localsites/localsites/internal/notify"
)

// Notifications: every one is kept for the bell in the app; it also pops up on the screen unless its kind
// is switched off or Do Not Disturb is on. Agent requests always pop up: an agent waits for an answer.

// NoticeView is notices.list.
type NoticeView struct {
	Notices    []notices.Notice `json:"notices"`
	Unread     int              `json:"unread"`
	Off        []string         `json:"off"`                   // kinds that don't pop up
	QuietUntil *time.Time       `json:"quiet_until,omitempty"` // Do Not Disturb, when on
	Kinds      []string         `json:"kinds"`
}

// sendSystem shows a notification on the screen (replaced in tests).
var sendSystem = notify.SendNotice

func (e *Engine) noticeStore() *notices.Store {
	if e.notices == nil { // engines built by tests without New
		e.notices = notices.Open(e.P.Data)
	}
	return e.notices
}

// Notify records a notification and shows it unless it's switched off or Do Not Disturb is on.
func (e *Engine) Notify(n notices.Notice) notices.Notice {
	n = e.noticeStore().Add(n)
	e.Changes.Bump()
	if e.popsUp(n.Kind) {
		sendSystem(n.Title, n.Text, n.Kind, n.Ref)
	}
	return n
}

func (e *Engine) popsUp(kind string) bool {
	if kind == notices.KindAgent || e.Store == nil {
		return true
	}
	s := e.Store.Get().Settings
	if !s.QuietUntil.IsZero() && time.Now().Before(s.QuietUntil) {
		return false
	}
	for _, k := range s.NoticesOff {
		if k == kind {
			return false
		}
	}
	return true
}

// Notices lists the notifications with the unread count and the settings.
func (e *Engine) Notices() NoticeView {
	list, unread := e.noticeStore().List()
	var s config.Settings
	if e.Store != nil {
		s = e.Store.Get().Settings
	}
	v := NoticeView{Notices: list, Unread: unread, Off: append([]string{}, s.NoticesOff...), Kinds: notices.Kinds}
	if !s.QuietUntil.IsZero() && time.Now().Before(s.QuietUntil) {
		q := s.QuietUntil
		v.QuietUntil = &q
	}
	return v
}

// ReadNotices marks one notice (or all, id "") as read.
func (e *Engine) ReadNotices(id string) {
	e.noticeStore().MarkRead(id)
	e.Changes.Bump()
}

// ClearNotices empties the list.
func (e *Engine) ClearNotices() {
	e.noticeStore().Clear()
	e.Changes.Bump()
}

// ReadNoticesAbout marks the notices about one approval or share as read (it was answered or ended).
func (e *Engine) ReadNoticesAbout(ref string) {
	if e.noticeStore().ReadRef(ref) {
		e.Changes.Bump()
	}
}

// QuietFor turns Do Not Disturb on for minutes (-1: until turned off; 0: off now).
func (e *Engine) QuietFor(minutes int) error {
	var until time.Time
	switch {
	case minutes < -1 || minutes > 7*24*60:
		return errors.New("minutes must be between 1 and 10080, -1 (until turned off) or 0 (off)")
	case minutes == -1:
		until = time.Now().AddDate(100, 0, 0)
	case minutes > 0:
		until = time.Now().Add(time.Duration(minutes) * time.Minute)
	}
	err := e.Store.Update(func(c *config.Config) error { c.Settings.QuietUntil = until; return nil })
	e.Changes.Bump()
	return err
}

// SetNoticesOff chooses which kinds don't pop up (agent requests always do).
func (e *Engine) SetNoticesOff(kinds []string) error {
	valid := map[string]bool{}
	for _, k := range notices.Kinds {
		valid[k] = true
	}
	var off []string
	for _, k := range kinds {
		switch {
		case k == notices.KindAgent:
			return errors.New("agent requests always show: an agent waits for your answer")
		case !valid[k]:
			return errors.New("unknown kind " + k)
		}
		off = append(off, k)
	}
	err := e.Store.Update(func(c *config.Config) error { c.Settings.NoticesOff = off; return nil })
	e.Changes.Bump()
	return err
}
