// Package notices keeps every notification LocalSites shows, so one missed while away can still be read in
// the app (the bell in the status bar). Saved in <data>/notices.json: the newest 200, at most 30 days old.
package notices

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Kinds of notices. Agent requests can't be switched off: an agent would wait without anyone knowing.
const (
	KindAgent   = "agent"
	KindCrash   = "crash"
	KindInstall = "install"
	KindSetup   = "setup"
	KindShare   = "share"
	KindUpdate  = "update"
	KindProblem = "problem"
)

// Kinds in the order Settings lists them.
var Kinds = []string{KindAgent, KindCrash, KindInstall, KindSetup, KindShare, KindUpdate, KindProblem}

const (
	maxNotices = 200
	maxAge     = 30 * 24 * time.Hour
)

// Notice is one notification.
type Notice struct {
	ID    string    `json:"id"`
	Time  time.Time `json:"time"`
	Kind  string    `json:"kind"`
	Title string    `json:"title"`
	Text  string    `json:"text,omitempty"`
	// Where clicking it goes, as the app's own command: "view:settings/updates", "proj:shop", "approval:<id>",
	// "log:apache"… Empty: nowhere in particular.
	Open string `json:"open,omitempty"`
	// Ref is what the notice is about, for buttons on the system notification (an approval or share id).
	Ref  string `json:"ref,omitempty"`
	Read bool   `json:"read,omitempty"`
}

// Store is the list, safe for use from several goroutines.
type Store struct {
	mu   sync.Mutex
	file string
	list []Notice // newest first
	now  func() time.Time
}

// Open loads the list (an unreadable file starts a new, empty list).
func Open(dataDir string) *Store {
	s := &Store{now: time.Now}
	if dataDir == "" { // no data folder (tests): kept in memory only
		return s
	}
	s.file = filepath.Join(dataDir, "notices.json")
	if b, err := os.ReadFile(s.file); err == nil {
		_ = json.Unmarshal(b, &s.list)
	}
	s.prune()
	return s
}

func (s *Store) prune() {
	cut := s.now().Add(-maxAge)
	keep := s.list[:0]
	for _, n := range s.list {
		if n.Time.After(cut) {
			keep = append(keep, n)
		}
	}
	s.list = keep
	if len(s.list) > maxNotices {
		s.list = s.list[:maxNotices]
	}
}

func (s *Store) save() {
	if s.file == "" {
		return
	}
	b, err := json.Marshal(s.list)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.file), 0o700)
	tmp := s.file + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.file)
	}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Add records a notice and returns it (with its id and time).
func (s *Store) Add(n Notice) Notice {
	s.mu.Lock()
	defer s.mu.Unlock()
	n.ID, n.Time, n.Read = newID(), s.now(), false
	s.list = append([]Notice{n}, s.list...)
	s.prune()
	s.save()
	return n
}

// List returns the notices, newest first, and how many are unread.
func (s *Store) List() ([]Notice, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Notice{}, s.list...)
	unread := 0
	for _, n := range out {
		if !n.Read {
			unread++
		}
	}
	return out, unread
}

// MarkRead marks one notice as read, or all of them when id is "".
func (s *Store) MarkRead(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if id == "" || s.list[i].ID == id {
			s.list[i].Read = true
		}
	}
	s.save()
}

// ReadRef marks the notices about one thing as read (an approval that was decided, a share that ended).
func (s *Store) ReadRef(ref string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i := range s.list {
		if ref != "" && s.list[i].Ref == ref && !s.list[i].Read {
			s.list[i].Read, changed = true, true
		}
	}
	if changed {
		s.save()
	}
	return changed
}

// Clear empties the list.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = nil
	s.save()
}
