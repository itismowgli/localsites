package sqlfile

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func read(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestKeepInDatabaseVersionedComments(t *testing.T) {
	out := read(t, KeepInDatabase(strings.NewReader("/*!40000 USE `other`*/;\n/*!40000 CREATE DATABASE other */;\nINSERT INTO t VALUES (1);\n\\restrict abc\n\\unrestrict abc\n")))
	if strings.Contains(out, "other") || strings.Contains(out, "restrict") || !strings.Contains(out, "INSERT INTO t") {
		t.Fatalf("got %q", out)
	}
}

// A database's own login can't use DEFINER=root or binary-log settings: those are removed.
func TestForMySQL(t *testing.T) {
	in := "SET @MYSQLDUMP_TEMP_LOG_BIN = @@SESSION.SQL_LOG_BIN;\n" +
		"SET @@SESSION.SQL_LOG_BIN= 0;\n" +
		"SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ 'abc:1-5';\n" +
		"/*!50013 DEFINER=`root`@`localhost` SQL SECURITY DEFINER */\n" +
		"CREATE DEFINER=`root`@`%` PROCEDURE p() SELECT 1;\n" +
		"/*!50017 DEFINER='admin'@'127.0.0.1'*/ /*!50003 TRIGGER t1 BEFORE INSERT ON t FOR EACH ROW SET @x=1 */;\n" +
		"INSERT INTO t VALUES ('DEFINER=`root`@`localhost`');\n" +
		"USE other;\n"
	out := read(t, ForMySQL(strings.NewReader(in)))
	for _, bad := range []string{"SQL_LOG_BIN= 0", "GTID_PURGED", "DEFINER=`root`@`%`", "DEFINER='admin'", "USE other"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q kept:\n%s", bad, out)
		}
	}
	for _, keep := range []string{"/*!50013 SQL SECURITY DEFINER */", "CREATE PROCEDURE p()", "TRIGGER t1", "INSERT INTO t VALUES ('DEFINER=`root`@`localhost`')", "@MYSQLDUMP_TEMP_LOG_BIN"} {
		if !strings.Contains(out, keep) {
			t.Errorf("%q missing:\n%s", keep, out)
		}
	}
}

func TestForPsql(t *testing.T) {
	out := read(t, ForPsql(strings.NewReader("\\restrict theirs\nSELECT 1;\n\\connect other\n\\unrestrict theirs\n"), "KEY"))
	if !strings.HasPrefix(out, "\\restrict KEY\n") || strings.Contains(out, "theirs") || strings.Contains(out, "other") || !strings.Contains(out, "SELECT 1;") {
		t.Fatalf("got %q", out)
	}
}

// An export never follows a planted link and, unless told to, never replaces a file.
func TestCreateIsSafe(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	_ = os.WriteFile(victim, []byte("keep"), 0o600)
	file := filepath.Join(dir, "x.sql")
	_ = os.Symlink(victim, file+".partial") // the old, predictable temp name
	w, err := Create(file)
	if err != nil {
		t.Fatal(err)
	}
	w.NoReplace = true
	_, _ = w.Write([]byte("SELECT 1;"))
	_ = os.WriteFile(file, []byte("appeared meanwhile"), 0o600)
	if _, err := w.Commit(); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("replaced a file that appeared meanwhile: %v", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatal("followed a planted link")
	}
	if b, _ := os.ReadFile(file); string(b) != "appeared meanwhile" {
		t.Fatal("file was replaced")
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".x.sql.*"))
	if len(left) != 0 {
		t.Fatalf("temp file left: %v", left)
	}
}
