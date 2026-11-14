package notify

import (
	"os"
	"path/filepath"
	"testing"
)

// The service only hands notifications to the real LocalSites app recorded in AppFile.
func TestAppExe(t *testing.T) {
	dir := t.TempDir()
	AppFile = filepath.Join(dir, "app-path")
	defer func() { AppFile = "" }()
	app := filepath.Join(dir, "LocalSites.app", "Contents")
	exe := filepath.Join(app, "MacOS", "LocalSites")
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	_ = os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755)
	_ = os.WriteFile(AppFile, []byte(exe+"\n"), 0o600)
	if AppExe() != "" {
		t.Fatal("accepted an app without LocalSites' bundle id")
	}
	_ = os.WriteFile(filepath.Join(app, "Info.plist"), []byte("<string>com.wails.localsites</string>"), 0o644)
	if AppExe() != exe {
		t.Fatalf("real app not found: %q", AppExe())
	}
	_ = os.WriteFile(AppFile, []byte("/bin/sh\n"), 0o600)
	if AppExe() != "" {
		t.Fatal("accepted another program")
	}
	_ = os.Remove(AppFile)
	if AppExe() != "" {
		t.Fatal("found an app without the file")
	}
}
