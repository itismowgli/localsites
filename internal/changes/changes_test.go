package changes

import (
	"testing"
	"time"
)

func TestWaitWakesOnBump(t *testing.T) {
	var b Bus
	v := b.Version()
	got := make(chan uint64, 1)
	go func() { got <- b.Wait(nil, v, 5*time.Second) }()
	time.Sleep(20 * time.Millisecond)
	b.Bump()
	select {
	case n := <-got:
		if n != v+1 {
			t.Fatalf("version %d", n)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not woken")
	}
	if n := b.Wait(nil, v, time.Second); n != v+1 { // already moved: returns at once
		t.Fatalf("version %d", n)
	}
	start := time.Now()
	if n := b.Wait(nil, v+1, 50*time.Millisecond); n != v+1 || time.Since(start) < 40*time.Millisecond {
		t.Fatal("timeout path")
	}
	done := make(chan struct{})
	close(done)
	if n := b.Wait(done, v+1, time.Minute); n != v+1 {
		t.Fatal("done path")
	}
}
