// Package changes lets clients wait for "something changed" instead of asking every few seconds.
// The service bumps a version whenever services start or stop, settings or projects change, shares
// end, agent requests arrive or updates move on; clients wait for the version to move past theirs.
package changes

import (
	"sync"
	"time"
)

// Bus is a version number plus a broadcast. The zero value is ready to use.
type Bus struct {
	mu  sync.Mutex
	ver uint64
	ch  chan struct{}
}

func (b *Bus) chLocked() chan struct{} {
	if b.ch == nil {
		b.ch = make(chan struct{})
	}
	return b.ch
}

// Bump records a change and wakes every waiter.
func (b *Bus) Bump() {
	b.mu.Lock()
	b.ver++
	close(b.chLocked())
	b.ch = make(chan struct{})
	b.mu.Unlock()
}

// Version is the current version.
func (b *Bus) Version() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ver
}

// Wait returns as soon as the version differs from since, after max, or when done closes.
// It returns the version at that moment.
func (b *Bus) Wait(done <-chan struct{}, since uint64, max time.Duration) uint64 {
	b.mu.Lock()
	if b.ver != since {
		v := b.ver
		b.mu.Unlock()
		return v
	}
	ch := b.chLocked()
	b.mu.Unlock()
	t := time.NewTimer(max)
	defer t.Stop()
	select {
	case <-ch:
	case <-t.C:
	case <-done:
	}
	return b.Version()
}
