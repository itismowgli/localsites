package engine

import (
	"os"
	"sort"
	"sync"
	"time"
)

// macOS asks the user before an app may read Documents, Desktop, Downloads, iCloud Drive or removable
// drives, and the read waits until someone answers. A projects folder there must never freeze the service,
// so listing a folder gives up after a short wait and the folder is reported as waiting for permission.
// The pending read keeps going in the background; once the user clicks Allow, the next scan sees the folder.
// Scans running at the same time share one read of each folder.

const listTimeout = 2 * time.Second

type dirRead struct {
	done    chan struct{}
	entries []os.DirEntry
}

var access = struct {
	sync.Mutex
	inflight map[string]*dirRead
	blocked  map[string]time.Time
}{inflight: map[string]*dirRead{}, blocked: map[string]time.Time{}}

// readDir lists a folder, or returns (nil, false) if macOS is still asking for permission.
func readDir(path string) ([]os.DirEntry, bool) {
	access.Lock()
	r, ok := access.inflight[path]
	if !ok {
		r = &dirRead{done: make(chan struct{})}
		access.inflight[path] = r
		go func() {
			r.entries, _ = os.ReadDir(path)
			access.Lock()
			delete(access.inflight, path)
			delete(access.blocked, path)
			access.Unlock()
			close(r.done)
		}()
	}
	access.Unlock()
	select {
	case <-r.done:
		return r.entries, true
	case <-time.After(listTimeout):
		access.Lock()
		access.blocked[path] = time.Now()
		access.Unlock()
		return nil, false
	}
}

// WaitingForAccess lists folders macOS hasn't let LocalSites read yet.
func WaitingForAccess() []string {
	access.Lock()
	defer access.Unlock()
	out := make([]string, 0, len(access.blocked))
	for p := range access.blocked {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
