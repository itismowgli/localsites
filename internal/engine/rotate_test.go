package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.log")
	// A writer that keeps the file open in append mode, like Apache
	w, _ := os.OpenFile(f, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	defer w.Close()
	w.WriteString(strings.Repeat("old\n", 300) + "last-old\n")
	if ok, err := rotateFile(f, 100, 9); !ok || err != nil {
		t.Fatalf("rotate: %v %v", ok, err)
	}
	if b, _ := os.ReadFile(f + ".1"); string(b) != "last-old\n" {
		t.Fatalf("kept tail %q", b)
	}
	w.WriteString("new\n")
	if b, _ := os.ReadFile(f); string(b) != "new\n" {
		t.Fatalf("after rotate the open writer must append to the emptied file, got %q", b)
	}
	if ok, _ := rotateFile(f, 100, 9); ok {
		t.Fatal("small file rotated")
	}
}
