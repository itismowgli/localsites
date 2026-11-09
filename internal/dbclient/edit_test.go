package dbclient

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func editFixture(t *testing.T) (*Manager, string, string) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)", "INSERT INTO users VALUES (1,'Ann','a@x'),(2,'Bob','b@x')", "CREATE TABLE nokey (a TEXT)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	m := New(dir, memSecrets{}, func() []Conn { return nil })
	t.Cleanup(m.Close)
	c, err := m.Save(Conn{Name: "App", Driver: "sqlite", File: file, ReadOnly: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m, c.ID, file
}

type memSecrets map[string]string

func (m memSecrets) Get(k string) (string, error) {
	if v, ok := m[k]; ok {
		return v, nil
	}
	return "", sql.ErrNoRows
}
func (m memSecrets) Set(k, v string) error { m[k] = v; return nil }
func (m memSecrets) Delete(k string) error { delete(m, k); return nil }

func s(v string) *string { return &v }

func count(t *testing.T, file, q string) string {
	db, _ := sql.Open("sqlite", file)
	defer db.Close()
	var out sql.NullString
	if err := db.QueryRow(q).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out.String
}

func TestSaveChanges(t *testing.T) {
	m, id, file := editFixture(t)
	schema := "main"
	ch := Changes{Schema: schema, Table: "users",
		Updates: []RowUpdate{{Key: map[string]*string{"id": s("1")}, Set: map[string]*string{"name": s("Ann O'Neil"), "email": nil}}},
		Deletes: []map[string]*string{{"id": s("2")}},
		Inserts: []map[string]*string{{"id": s("3"), "name": s("Cy")}}}

	if _, err := m.SaveChanges(id, ch); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only connection must refuse saving, got %v", err)
	}
	prev, err := m.Preview(id, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(prev) != 3 || !strings.Contains(prev[0], `'Ann O''Neil'`) || !strings.Contains(prev[0], "NULL") {
		t.Fatalf("preview: %v", prev)
	}
	if err := m.SetReadOnly(id, false); err != nil {
		t.Fatal(err)
	}
	res, err := m.SaveChanges(id, ch)
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Deleted != 1 || res.Inserted != 1 {
		t.Fatalf("result %+v", res)
	}
	if got := count(t, file, "SELECT group_concat(name, '|') FROM (SELECT name FROM users ORDER BY id)"); got != "Ann O'Neil|Cy" {
		t.Fatalf("rows after save: %q", got)
	}

	// A row that vanished: nothing saved, even the valid update before it
	stale := Changes{Schema: schema, Table: "users",
		Updates: []RowUpdate{{Key: map[string]*string{"id": s("1")}, Set: map[string]*string{"name": s("Changed")}}},
		Deletes: []map[string]*string{{"id": s("2")}}}
	if _, err := m.SaveChanges(id, stale); err == nil || !strings.Contains(err.Error(), "nothing was saved") {
		t.Fatalf("stale delete must fail, got %v", err)
	}
	if got := count(t, file, "SELECT name FROM users WHERE id=1"); got != "Ann O'Neil" {
		t.Fatalf("transaction not rolled back: %q", got)
	}

	// Unknown columns and keyless tables are refused
	if _, err := m.Preview(id, Changes{Schema: schema, Table: "users", Updates: []RowUpdate{{Key: map[string]*string{"id": s("1")}, Set: map[string]*string{"name = 1; --": s("x")}}}}); err == nil {
		t.Fatal("unknown column accepted")
	}
	if _, err := m.Preview(id, Changes{Schema: schema, Table: "nokey", Deletes: []map[string]*string{{"a": s("x")}}}); err == nil {
		t.Fatal("table without primary key accepted")
	}
}
