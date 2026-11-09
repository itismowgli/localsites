//go:build e2e

package e2e

import (
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localsites/localsites/internal/dbclient"
	"github.com/localsites/localsites/internal/engine"
	lsruntime "github.com/localsites/localsites/internal/runtime"
)

func readSQL(t *testing.T, file string) string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(file, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		r = gz
	}
	b, _ := io.ReadAll(r)
	return string(b)
}

func count(t *testing.T, ev *env, q string) int {
	t.Helper()
	db := ev.adminDB(t)
	defer db.Close()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// Export, import, backups, restore, duplicate, empty and delete for LocalSites MySQL.
func TestDatabaseToolsMySQL(t *testing.T) {
	ev := setup(t)
	if _, ok := lsruntime.MySQL(); !ok {
		t.Skip("MySQL 8.4 not installed")
	}
	e := ev.e
	if _, err := e.CreateDatabase("tools"); err != nil {
		t.Fatal(err)
	}
	admin := ev.adminDB(t)
	for _, q := range []string{
		"CREATE TABLE tools.items (id INT PRIMARY KEY, name VARCHAR(50), data BLOB)",
		"INSERT INTO tools.items VALUES (1,'Ünïcödé 日本',X'00FF'),(2,'b',NULL),(3,'c','')",
		"CREATE VIEW tools.v AS SELECT id FROM tools.items",
	} {
		if _, err := admin.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	admin.Close()
	dir := t.TempDir()

	t.Run("export: full, structure only, data only; paths checked", func(t *testing.T) {
		r, err := e.ExportDatabase("tools", filepath.Join(dir, "full.sql.gz"), "full", false)
		if err != nil || r.Bytes == 0 {
			t.Fatal(r, err)
		}
		full := readSQL(t, r.File)
		if !strings.Contains(full, "CREATE TABLE `items`") || !strings.Contains(full, "INSERT INTO `items`") || strings.Contains(full, "CREATE DATABASE") || strings.Contains(full, "USE `") {
			t.Fatal("full export should have tables and rows and no database switch")
		}
		s, _ := e.ExportDatabase("tools", filepath.Join(dir, "s.sql"), "structure", false)
		if sql := readSQL(t, s.File); !strings.Contains(sql, "CREATE TABLE") || strings.Contains(sql, "INSERT INTO") {
			t.Fatal("structure export")
		}
		d, _ := e.ExportDatabase("tools", filepath.Join(dir, "d.sql"), "data", false)
		if sql := readSQL(t, d.File); strings.Contains(sql, "CREATE TABLE") || !strings.Contains(sql, "INSERT INTO") {
			t.Fatal("data export")
		}
		if _, err := e.ExportDatabase("tools", filepath.Join(dir, "s.sql"), "full", false); err == nil {
			t.Fatal("overwrote an existing file without being asked")
		}
		if _, err := e.ExportDatabase("tools", "relative.sql", "full", false); err == nil {
			t.Fatal("accepted a relative path")
		}
		if _, err := e.ExportDatabase("tools", filepath.Join(dir, "x.txt"), "full", false); err == nil {
			t.Fatal("accepted a non-.sql name")
		}
		if _, err := e.ExportDatabase("nosuch", filepath.Join(dir, "n.sql"), "full", false); err == nil {
			t.Fatal("exported a database that doesn't exist")
		}
	})

	t.Run("duplicate copies structure and data with its own login", func(t *testing.T) {
		if _, err := e.DuplicateDatabase("tools", "tools_copy"); err != nil {
			t.Fatal(err)
		}
		if n := count(t, ev, "SELECT COUNT(*) FROM tools_copy.items"); n != 3 {
			t.Fatalf("rows %d", n)
		}
		if _, err := e.DatabaseCredentials("tools_copy"); err != nil {
			t.Fatal("no login for the copy")
		}
		if _, err := e.DuplicateDatabase("tools", "tools_copy"); err == nil {
			t.Fatal("duplicated over an existing database")
		}
	})

	t.Run("import stays in the chosen database and makes a backup first", func(t *testing.T) {
		f := filepath.Join(dir, "other.sql")
		os.WriteFile(f, []byte("CREATE DATABASE IF NOT EXISTS sneaky;\nUSE `sneaky`;\nCREATE TABLE imported (x INT);\nINSERT INTO imported VALUES (7),(8);\n"), 0o600)
		if _, err := e.ImportFile("tools", f); err != nil {
			t.Fatal(err)
		}
		if n := count(t, ev, "SELECT COUNT(*) FROM tools.imported"); n != 2 {
			t.Fatalf("imported rows %d", n)
		}
		if n := count(t, ev, "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='sneaky'"); n != 0 {
			t.Fatal("the dump created another database")
		}
		list, _ := e.Backups("tools")
		if len(list) == 0 || !strings.Contains(list[0].Note, "before importing other.sql") {
			t.Fatalf("backups: %+v", list)
		}
		// a .zip with a gzipped dump inside works too
		if _, err := e.ImportFile("tools", filepath.Join(dir, "x.zip")); err == nil {
			t.Fatal("imported a missing file")
		}
	})

	t.Run("empty, then restore the backup made before emptying", func(t *testing.T) {
		if _, err := e.EmptyDatabase("tools"); err != nil {
			t.Fatal(err)
		}
		if n := count(t, ev, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='tools'"); n != 0 {
			t.Fatalf("tables left: %d", n)
		}
		list, _ := e.Backups("tools")
		var before string
		for _, b := range list {
			if b.Note == "before emptying" {
				before = b.File
			}
		}
		if before == "" {
			t.Fatalf("no backup before emptying: %+v", list)
		}
		if _, err := e.RestoreBackup("tools", before); err != nil {
			t.Fatal(err)
		}
		if n := count(t, ev, "SELECT COUNT(*) FROM tools.items"); n != 3 {
			t.Fatalf("restored rows %d", n)
		}
		if n := count(t, ev, "SELECT COUNT(*) FROM tools.imported"); n != 2 {
			t.Fatalf("restored imported rows %d", n)
		}
		if _, err := e.RestoreBackup("tools", "/etc/passwd"); err == nil {
			t.Fatal("restored from a file that isn't one of its backups")
		}
	})

	t.Run("delete removes the database and login, keeps a backup", func(t *testing.T) {
		if _, err := e.DropDatabase("tools_copy"); err != nil {
			t.Fatal(err)
		}
		if n := count(t, ev, "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='tools_copy'"); n != 0 {
			t.Fatal("still there")
		}
		if n := count(t, ev, "SELECT COUNT(*) FROM mysql.user WHERE user LIKE '%tools_copy%'"); n != 0 {
			t.Fatal("login still there")
		}
		if list, _ := e.Backups("tools_copy"); len(list) == 0 {
			t.Fatal("no backup kept")
		}
	})

	t.Run("connection import refuses a read-only connection", func(t *testing.T) {
		if _, err := e.ImportConnection("localsites", "tools", filepath.Join(dir, "d.sql")); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("got %v", err)
		}
		r, err := e.ExportConnection("localsites", "tools", filepath.Join(dir, "conn.sql"), "full", false)
		if err != nil || !strings.Contains(readSQL(t, r.File), "INSERT INTO `items`") {
			t.Fatalf("connection export: %v", err)
		}
	})
}

// Export and import through saved connections (SQLite, PostgreSQL) and the PostgreSQL add-on tools.
func TestDatabaseToolsSQLiteAndPostgres(t *testing.T) {
	ev := setup(t)
	e := ev.e
	dir := t.TempDir()

	t.Run("sqlite: export, import into another file", func(t *testing.T) {
		src := filepath.Join(dir, "app.db")
		if out, err := exec.Command("/usr/bin/sqlite3", src, "CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT); INSERT INTO notes (body) VALUES ('one'),('twø');").CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
		a, err := e.Data.Save(dbclient.Conn{Name: "src", Driver: "sqlite", File: src, ReadOnly: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		r, err := e.ExportConnection(a.ID, "", filepath.Join(dir, "notes.sql"), "full", false)
		if err != nil || !strings.Contains(readSQL(t, r.File), "INSERT INTO notes") {
			t.Fatalf("export: %v", err)
		}
		b, _ := e.Data.Save(dbclient.Conn{Name: "dst", Driver: "sqlite", File: filepath.Join(dir, "copy.db"), ReadOnly: false}, nil)
		if _, err := e.ImportConnection(b.ID, "", r.File); err != nil {
			t.Fatal(err)
		}
		out, _ := exec.Command("/usr/bin/sqlite3", filepath.Join(dir, "copy.db"), "SELECT group_concat(body) FROM notes").Output()
		if strings.TrimSpace(string(out)) != "one,twø" {
			t.Fatalf("copy has %q", out)
		}
		if _, err := e.ImportConnection(a.ID, "", r.File); err == nil {
			t.Fatal("imported into a read-only connection")
		}
	})

	if _, err := os.Stat("/opt/homebrew/opt/postgresql@17/bin/postgres"); err != nil {
		t.Skip("PostgreSQL not installed")
	}
	on, pg := true, freePort(t)
	if err := e.SetAddon("postgres", engine.AddonPatch{Enabled: &on, Port: &pg}); err != nil {
		t.Fatal(err)
	}
	e.Apply()

	t.Run("postgres add-on: create, import, export, duplicate, empty, delete", func(t *testing.T) {
		if _, err := e.PGCreate("shop"); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(dir, "pg.sql")
		os.WriteFile(f, []byte("\\connect other\nCREATE TABLE products (id serial PRIMARY KEY, title text);\nINSERT INTO products (title) VALUES ('a'),('b');\n"), 0o600)
		if _, err := e.PGImport("shop", f); err != nil {
			t.Fatal(err)
		}
		list, err := e.PGDatabases()
		if err != nil || len(list) != 1 || list[0].Name != "shop" || list[0].Tables != 1 {
			t.Fatalf("list: %v %+v", err, list)
		}
		r, err := e.PGExport("shop", filepath.Join(dir, "shop.sql.gz"), "full", false)
		if err != nil || !strings.Contains(readSQL(t, r.File), "CREATE TABLE public.products") {
			t.Fatalf("export: %v", err)
		}
		if _, err := e.PGDuplicate("shop", "shop_copy"); err != nil {
			t.Fatal(err)
		}
		if out, err := e.PGEmpty("shop"); err != nil {
			t.Fatal(out, err)
		}
		list, _ = e.PGDatabases()
		for _, d := range list {
			if d.Name == "shop" && d.Tables != 0 {
				t.Fatal("not emptied")
			}
			if d.Name == "shop_copy" && d.Tables != 1 {
				t.Fatal("copy lost its table")
			}
		}
		if _, err := e.PGImport("shop", r.File); err != nil { // the export loads back
			t.Fatal(err)
		}
		if _, err := e.PGDrop("shop_copy"); err != nil {
			t.Fatal(err)
		}
		if list, _ = e.PGDatabases(); len(list) != 1 {
			t.Fatalf("after delete: %+v", list)
		}
		backups, _ := filepath.Glob(filepath.Join(e.P.Backups, "pg-shop*-*.sql.gz"))
		if len(backups) < 3 { // before import x2, before emptying, before deleting the copy
			t.Fatalf("backups: %v", backups)
		}
	})
}
