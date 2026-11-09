// Package problems keeps a local list of things that went wrong (crashes, services that keep failing,
// errors in the app's page, failed updates and installs) and sends a cleaned report when the person
// agrees. Nothing leaves the Mac without consent; reports use Sentry's envelope format, so any
// Sentry-compatible server (sentry.io, self-hosted Sentry, GlitchTip) can receive them.
package problems

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kinds of problem.
const (
	KindCrash   = "crash"   // the app or the service crashed (Go panic or a macOS crash report)
	KindService = "service" // Apache, PHP, MySQL… stopped unexpectedly
	KindUI      = "ui"      // an error in the app's page
	KindUpdate  = "update"  // an update didn't install
	KindInstall = "install" // an install (add-on, tunnel client, dependencies) failed
)

// Problem is one thing that went wrong.
type Problem struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Title   string    `json:"title"`            // one line, shown in the list
	Detail  string    `json:"detail,omitempty"` // stack trace, log tail… (raw; cleaned when previewed or sent)
	Version string    `json:"version"`          // LocalSites version it happened in
	Count   int       `json:"count"`            // the same problem again (same kind and title) only counts up
	Last    time.Time `json:"last"`
	Sent    time.Time `json:"sent,omitzero"`
	EventID string    `json:"event_id,omitempty"`
}

const keep = 50

// Store keeps problems as one JSON file in dir.
type Store struct {
	mu      sync.Mutex
	dir     string
	Version string
	OnAdd   func(Problem) // called after a new problem (not a repeat) is saved
}

// Open uses dir (created if needed).
func Open(dir, version string) *Store {
	_ = os.MkdirAll(dir, 0o700)
	return &Store{dir: dir, Version: version}
}

func (s *Store) file() string { return filepath.Join(s.dir, "problems.json") }

func (s *Store) load() []Problem {
	var list []Problem
	if b, err := os.ReadFile(s.file()); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	return list
}

func (s *Store) save(list []Problem) error {
	sort.Slice(list, func(i, j int) bool { return list[i].Last.After(list[j].Last) })
	if len(list) > keep {
		list = list[:keep]
	}
	b, _ := json.MarshalIndent(list, "", " ")
	tmp := s.file() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file())
}

// Add records a problem. A repeat (same kind and title, not yet sent) only updates its count, time and detail.
func (s *Store) Add(kind, title, detail string) Problem {
	title = strings.TrimSpace(firstLine(title))
	if len(title) > 200 {
		title = title[:200]
	}
	if len(detail) > 64<<10 { // keep the end, where the error is
		detail = "…\n" + detail[len(detail)-64<<10:]
	}
	now := time.Now()
	s.mu.Lock()
	list := s.load()
	for i := range list {
		if list[i].Kind == kind && list[i].Title == title && list[i].Sent.IsZero() && list[i].Version == s.Version {
			list[i].Count++
			list[i].Last, list[i].Detail = now, detail
			p := list[i]
			_ = s.save(list)
			s.mu.Unlock()
			return p
		}
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	p := Problem{ID: hex.EncodeToString(b), Time: now, Last: now, Kind: kind, Title: title, Detail: detail, Version: s.Version, Count: 1}
	list = append(list, p)
	_ = s.save(list)
	s.mu.Unlock()
	if s.OnAdd != nil {
		s.OnAdd(p)
	}
	return p
}

// List returns problems, newest first.
func (s *Store) List() []Problem {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	sort.Slice(list, func(i, j int) bool { return list[i].Last.After(list[j].Last) })
	return list
}

// Get returns one problem.
func (s *Store) Get(id string) (Problem, error) {
	for _, p := range s.List() {
		if p.ID == id {
			return p, nil
		}
	}
	return Problem{}, errors.New("no such problem (it may have been dismissed)")
}

// MarkSent records that a problem was reported.
func (s *Store) MarkSent(id, eventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	for i := range list {
		if list[i].ID == id {
			list[i].Sent, list[i].EventID = time.Now(), eventID
		}
	}
	_ = s.save(list)
}

// Dismiss removes one problem, or every problem when id is "".
func (s *Store) Dismiss(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		_ = s.save(nil)
		return
	}
	list := s.load()
	out := list[:0]
	for _, p := range list {
		if p.ID != id {
			out = append(out, p)
		}
	}
	_ = s.save(out)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
