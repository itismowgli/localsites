package sqlfile

import (
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dump = "-- MySQL dump\nCREATE DATABASE /*!32312 IF NOT EXISTS*/ `old` ;\nUSE `old`;\nCREATE TABLE t (id int);\nINSERT INTO t VALUES (1),(2);\n  use other;\nDROP DATABASE x;\n"

func TestKeepInDatabase(t *testing.T) {
	b, _ := io.ReadAll(KeepInDatabase(strings.NewReader(dump)))
	out := string(b)
	for _, gone := range []string{"CREATE DATABASE", "USE `old`", "use other", "DROP DATABASE"} {
		if strings.Contains(out, gone) {
			t.Errorf("kept %q:\n%s", gone, out)
		}
	}
	if !strings.Contains(out, "CREATE TABLE t") || !strings.Contains(out, "INSERT INTO t VALUES (1),(2);") {
		t.Fatal(out)
	}
	// a 3 MB line passes through whole (mysqldump never splits a value over lines)
	long := "INSERT INTO t VALUES ('" + strings.Repeat("x", 3<<20) + "');\nSELECT 1;\n"
	b, _ = io.ReadAll(KeepInDatabase(strings.NewReader(long)))
	if string(b) != long {
		t.Fatalf("long line changed: %d vs %d bytes", len(b), len(long))
	}
}

func TestOpenAndCreateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.sql", "a.sql.gz"} {
		w, err := Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, dump)
		if _, err := w.Commit(); err != nil {
			t.Fatal(err)
		}
		r, err := Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		r.Close()
		if string(b) != dump {
			t.Fatalf("%s round trip", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "a.sql.partial")); !os.IsNotExist(err) {
		t.Fatal("partial file left")
	}
	// zip with a gzipped dump and macOS junk
	zf, _ := os.Create(filepath.Join(dir, "x.zip"))
	zw := zip.NewWriter(zf)
	j, _ := zw.Create("__MACOSX/._x.sql")
	j.Write([]byte("junk"))
	e, _ := zw.Create("backup/x.sql.gz")
	gz := gzip.NewWriter(e)
	gz.Write([]byte(dump))
	gz.Close()
	zw.Close()
	zf.Close()
	r, err := Open(filepath.Join(dir, "x.zip"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	if string(b) != dump {
		t.Fatal("zip")
	}
}
