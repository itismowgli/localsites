package dbclient

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Changes are edits staged in the grid, saved together in one transaction.
// Values are strings; nil means SQL NULL. Keys hold the row's primary-key values as loaded.
type Changes struct {
	Schema  string               `json:"schema"`
	Table   string               `json:"table"`
	Updates []RowUpdate          `json:"updates"`
	Deletes []map[string]*string `json:"deletes"`
	Inserts []map[string]*string `json:"inserts"`
}

// RowUpdate changes some columns of one row.
type RowUpdate struct {
	Key map[string]*string `json:"key"`
	Set map[string]*string `json:"set"`
}

// SaveResult reports what was written.
type SaveResult struct {
	Updated  int64    `json:"updated"`
	Deleted  int64    `json:"deleted"`
	Inserted int64    `json:"inserted"`
	SQL      []string `json:"sql"`
	MS       int64    `json:"ms"`
}

type stmt struct {
	sql     string
	args    []any
	preview string
	kind    string // update | delete | insert
}

// build validates the changes against the table's columns and primary key and makes parameterized statements.
func (m *Manager) build(c Conn, ch Changes) ([]stmt, error) {
	st, err := m.Structure(c.ID, ch.Schema, ch.Table)
	if err != nil {
		return nil, err
	}
	cols := map[string]bool{}
	var pk []string
	for _, col := range st.Columns {
		cols[col.Name] = true
		if col.Key == "PRI" {
			pk = append(pk, col.Name)
		}
	}
	if len(pk) == 0 && (len(ch.Updates) > 0 || len(ch.Deletes) > 0) {
		return nil, errors.New("this table has no primary key, so rows can't be identified safely. Edit it with SQL instead")
	}
	table := quoteTable(c.Driver, ch.Schema, ch.Table)
	n := 0
	ph := func() string {
		n++
		if c.Driver == "postgres" {
			return fmt.Sprintf("$%d", n)
		}
		return "?"
	}
	where := func(key map[string]*string) (string, []any, string, error) {
		var parts, prev []string
		var args []any
		for _, k := range pk {
			v, ok := key[k]
			if !ok {
				return "", nil, "", fmt.Errorf("missing primary key column %s", k)
			}
			if v == nil {
				parts = append(parts, quoteIdent(c.Driver, k)+" IS NULL")
				prev = append(prev, quoteIdent(c.Driver, k)+" IS NULL")
				continue
			}
			parts = append(parts, quoteIdent(c.Driver, k)+" = "+ph())
			prev = append(prev, quoteIdent(c.Driver, k)+" = "+literal(v))
			args = append(args, *v)
		}
		return strings.Join(parts, " AND "), args, strings.Join(prev, " AND "), nil
	}
	checkCols := func(m map[string]*string) error {
		for k := range m {
			if !cols[k] {
				return fmt.Errorf("unknown column %q", k)
			}
		}
		return nil
	}
	var out []stmt
	for _, u := range ch.Updates {
		if len(u.Set) == 0 {
			continue
		}
		if err := checkCols(u.Set); err != nil {
			return nil, err
		}
		n = 0
		keys := sortedKeys(u.Set)
		var sets, prev []string
		var args []any
		for _, k := range keys {
			v := u.Set[k]
			if v == nil {
				sets = append(sets, quoteIdent(c.Driver, k)+" = NULL")
				prev = append(prev, quoteIdent(c.Driver, k)+" = NULL")
				continue
			}
			sets = append(sets, quoteIdent(c.Driver, k)+" = "+ph())
			prev = append(prev, quoteIdent(c.Driver, k)+" = "+literal(v))
			args = append(args, *v)
		}
		w, wargs, wprev, err := where(u.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, stmt{sql: "UPDATE " + table + " SET " + strings.Join(sets, ", ") + " WHERE " + w, args: append(args, wargs...),
			preview: "UPDATE " + table + " SET " + strings.Join(prev, ", ") + " WHERE " + wprev, kind: "update"})
	}
	for _, k := range ch.Deletes {
		n = 0
		w, args, wprev, err := where(k)
		if err != nil {
			return nil, err
		}
		out = append(out, stmt{sql: "DELETE FROM " + table + " WHERE " + w, args: args, preview: "DELETE FROM " + table + " WHERE " + wprev, kind: "delete"})
	}
	for _, row := range ch.Inserts {
		if err := checkCols(row); err != nil {
			return nil, err
		}
		n = 0
		keys := sortedKeys(row)
		if len(keys) == 0 {
			continue
		}
		var names, vals, prev []string
		var args []any
		for _, k := range keys {
			names = append(names, quoteIdent(c.Driver, k))
			if row[k] == nil {
				vals, prev = append(vals, "NULL"), append(prev, "NULL")
				continue
			}
			vals, prev = append(vals, ph()), append(prev, literal(row[k]))
			args = append(args, *row[k])
		}
		out = append(out, stmt{sql: "INSERT INTO " + table + " (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(vals, ", ") + ")", args: args,
			preview: "INSERT INTO " + table + " (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(prev, ", ") + ")", kind: "insert"})
	}
	if len(out) == 0 {
		return nil, errors.New("there are no changes to save")
	}
	return out, nil
}

// Preview shows the SQL that SaveChanges would run (values shown as literals, for reading only).
func (m *Manager) Preview(id string, ch Changes) ([]string, error) {
	c, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	stmts, err := m.build(c, ch)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range stmts {
		out = append(out, s.preview+";")
	}
	return out, nil
}

// SaveChanges writes staged edits in one transaction. Every update and delete must hit exactly one row,
// otherwise nothing is saved. Only for connections where changes are allowed.
func (m *Manager) SaveChanges(id string, ch Changes) (SaveResult, error) {
	var res SaveResult
	c, err := m.Get(id)
	if err != nil {
		return res, err
	}
	if c.ReadOnly {
		return res, errors.New("this connection is read-only. Turn on “Allow changes” to edit rows")
	}
	stmts, err := m.build(c, ch)
	if err != nil {
		return res, err
	}
	db, err := m.open(c, dbFor(c, ch.Schema))
	if err != nil {
		return res, err
	}
	start := time.Now()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return res, friendly(err)
	}
	for _, s := range stmts {
		args := s.args
		if c.Driver == "postgres" {
			// Send values as untyped literals so the server converts them to each column's type (int, date, enum…)
			args = append([]any{pgx.QueryExecModeSimpleProtocol}, args...)
		}
		r, err := tx.Exec(s.sql, args...)
		if err != nil {
			tx.Rollback()
			return SaveResult{}, fmt.Errorf("%v (nothing was saved)", friendly(err))
		}
		n, _ := r.RowsAffected()
		if s.kind != "insert" && n != 1 {
			tx.Rollback()
			return SaveResult{}, fmt.Errorf("a row changed or disappeared since it was loaded (%d rows matched; nothing was saved). Reload and try again", n)
		}
		switch s.kind {
		case "update":
			res.Updated += n
		case "delete":
			res.Deleted += n
		case "insert":
			res.Inserted += n
		}
		res.SQL = append(res.SQL, s.preview+";")
	}
	if err := tx.Commit(); err != nil {
		return SaveResult{}, friendly(err)
	}
	res.MS = time.Since(start).Milliseconds()
	return res, nil
}

func literal(v *string) string {
	if v == nil {
		return "NULL"
	}
	return "'" + strings.ReplaceAll(*v, "'", "''") + "'"
}

func sortedKeys(m map[string]*string) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}
