package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/localsites/localsites/internal/paths"
)

func TestStorageCleanups(t *testing.T) {
	home := t.TempDir()
	p := paths.Paths{Data: home, Logs: filepath.Join(home, "logs"), SiteLogs: filepath.Join(home, "logs", "sites"), Backups: filepath.Join(home, "backups"), MySQLData: filepath.Join(home, "mysql")}
	for _, d := range []string{p.Logs, p.SiteLogs, p.Backups} {
		os.MkdirAll(d, 0o755)
	}
	e := &Engine{P: p}
	old := time.Now().Add(-60 * 24 * time.Hour)
	for i := 0; i < 13; i++ { // 13 backups: the 3 oldest are past the newest-10 limit and older than 30 days
		f := filepath.Join(p.Backups, "db-"+string(rune('a'+i))+".sql.gz")
		os.WriteFile(f, make([]byte, 100), 0o600)
		os.Chtimes(f, old.Add(time.Duration(i)*time.Hour), old.Add(time.Duration(i)*time.Hour))
	}
	os.WriteFile(filepath.Join(p.Logs, "apache-error.log.1"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(p.Logs, "apache-error.log"), []byte("keep"), 0o600)
	if n := len(e.oldBackups()); n != 3 {
		t.Fatalf("old backups: %d", n)
	}
	if n := len(e.oldLogs()); n != 1 {
		t.Fatalf("old logs: %d", n)
	}
	for _, id := range []string{"backups", "logs"} {
		if _, err := e.CleanStorage(id); err != nil {
			t.Fatal(err)
		}
	}
	left, _ := filepath.Glob(filepath.Join(p.Backups, "*"))
	if len(left) != 10 {
		t.Fatalf("kept %d backups", len(left))
	}
	if _, err := os.Stat(filepath.Join(p.Logs, "apache-error.log")); err != nil {
		t.Fatal("current log removed")
	}
	if _, err := e.CleanStorage("mysql"); err == nil {
		t.Fatal("MySQL data must never be cleaned here")
	}
}
