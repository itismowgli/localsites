package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a"), nil, 0o644)
	if e, ok := readDir(dir); !ok || len(e) != 1 {
		t.Fatalf("readable folder: %v %v", e, ok)
	}
	if e, ok := readDir(filepath.Join(dir, "missing")); !ok || e != nil {
		t.Fatalf("missing folder should answer at once, empty: %v %v", e, ok)
	}
	if len(WaitingForAccess()) != 0 {
		t.Fatal("nothing should be waiting")
	}
}

func TestReadDirConcurrent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(dir, string(rune('a'+i))), nil, 0o644)
	}
	errs := make(chan string, 50)
	done := make(chan bool)
	for i := 0; i < 50; i++ {
		go func() {
			if e, ok := readDir(dir); !ok || len(e) != 5 {
				errs <- "a concurrent scan saw an empty folder"
			}
			done <- true
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
