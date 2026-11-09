// Package dbclient is LocalSites' database browser (TablePlus-style): connections to MySQL/MariaDB,
// PostgreSQL and SQLite; browse schemas, tables, structure and rows; run SQL.
//
// Safety: every connection is read-only unless the user turns writing on for it. Reads run inside a
// read-only transaction that the database server itself enforces, and are always rolled back.
package dbclient

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	_ "modernc.org/sqlite"             // registers "sqlite" (pure Go, no cgo)
)

// Conn is a saved or built-in connection (never contains the password).
type Conn struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Driver      string `json:"driver"` // mysql | postgres | sqlite
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	Socket      string `json:"socket,omitempty"`
	User        string `json:"user,omitempty"`
	Database    string `json:"database,omitempty"` // default database (Postgres: required)
	File        string `json:"file,omitempty"`     // SQLite file
	SSLMode     string `json:"ssl_mode,omitempty"` // Postgres: disable | require | verify-full
	SSHHost     string `json:"ssh_host,omitempty"` // optional SSH tunnel: user@host or a ~/.ssh/config alias
	SSHPort     int    `json:"ssh_port,omitempty"`
	ReadOnly    bool   `json:"read_only"`
	Builtin     bool   `json:"builtin,omitempty"`
	HasPassword bool   `json:"has_password"`
	Note        string `json:"note,omitempty"`
	// PasswordKey lets a built-in connection use an existing keychain entry (e.g. LocalSites' MySQL root).
	PasswordKey string `json:"-"`
}

// Secrets is the password store (OS keychain).
type Secrets interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// Manager keeps connections and open pools.
type Manager struct {
	file     string
	secrets  Secrets
	builtins func() []Conn

	mu    sync.Mutex
	saved []Conn
	pools map[string]*sql.DB

	// Prepare runs before a connection is used (wakes a sleeping LocalSites MySQL)
	Prepare func(c Conn) error
}

// CloseConn releases the open pools of one connection.
func (m *Manager) CloseConn(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked(id)
}

func New(configDir string, secrets Secrets, builtins func() []Conn) *Manager {
	m := &Manager{file: filepath.Join(configDir, "connections.json"), secrets: secrets, builtins: builtins, pools: map[string]*sql.DB{}}
	if b, err := os.ReadFile(m.file); err == nil {
		_ = json.Unmarshal(b, &m.saved)
	}
	return m
}

func secretKey(id string) string { return "conn-" + id }

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// List returns built-in connections first, then saved ones.
func (m *Manager) List() []Conn {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.builtins()
	for i := range out {
		out[i].Builtin = true
		if !out[i].HasPassword {
			_, err := m.secrets.Get(secretKey(out[i].ID))
			out[i].HasPassword = err == nil
		}
		if ro, ok := m.readOnlyOverride(out[i].ID); ok {
			out[i].ReadOnly = ro
		}
	}
	for _, c := range m.saved {
		if c.Driver == "override" { // remembered read-only switch of a built-in connection
			continue
		}
		_, err := m.secrets.Get(secretKey(c.ID))
		c.HasPassword = err == nil
		out = append(out, c)
	}
	return out
}

// Built-in connections can't be edited, but their read-only switch and password are remembered.
func (m *Manager) readOnlyOverride(id string) (bool, bool) {
	for _, c := range m.saved {
		if c.ID == "builtin:"+id {
			return c.ReadOnly, true
		}
	}
	return false, false
}

func (m *Manager) Get(id string) (Conn, error) {
	for _, c := range m.List() {
		if c.ID == id {
			return c, nil
		}
	}
	return Conn{}, fmt.Errorf("no connection %q", id)
}

// Save adds or updates a connection. password: nil = keep, "" = remove.
func (m *Manager) Save(c Conn, password *string) (Conn, error) {
	c.ID = strings.ToLower(strings.TrimSpace(c.ID))
	if c.ID == "" {
		c.ID = slug(c.Name)
	}
	if !idRe.MatchString(c.ID) {
		return c, errors.New("connection id: letters, digits and dashes")
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return c, errors.New("give the connection a name")
	}
	switch c.Driver {
	case "mysql", "postgres":
		if c.Socket == "" && c.Host == "" {
			return c, errors.New("enter a host (or a socket path for MySQL)")
		}
		if c.Driver == "postgres" && c.Database == "" {
			return c, errors.New("PostgreSQL needs a database name")
		}
	case "sqlite":
		if c.File == "" {
			return c, errors.New("choose the SQLite file")
		}
	default:
		return c, errors.New("driver must be mysql, postgres or sqlite")
	}
	if err := validSSH(c); err != nil {
		return c, err
	}
	if c.SSHHost != "" {
		c.Socket = ""
	}
	for _, b := range m.builtins() {
		if b.ID == c.ID {
			return c, errors.New("that id belongs to a built-in connection")
		}
	}
	c.Builtin, c.HasPassword = false, false
	m.mu.Lock()
	replaced := false
	for i := range m.saved {
		if m.saved[i].ID == c.ID {
			m.saved[i], replaced = c, true
		}
	}
	if !replaced {
		m.saved = append(m.saved, c)
	}
	err := m.persistLocked()
	m.closeLocked(c.ID)
	m.mu.Unlock()
	if err != nil {
		return c, err
	}
	if password != nil {
		if *password == "" {
			_ = m.secrets.Delete(secretKey(c.ID))
		} else if err := m.secrets.Set(secretKey(c.ID), *password); err != nil {
			return c, err
		}
	}
	return m.Get(c.ID)
}

// SetPassword remembers a password for any connection (including built-ins like MAMP PRO).
func (m *Manager) SetPassword(id, password string) error {
	if _, err := m.Get(id); err != nil {
		return err
	}
	m.mu.Lock()
	m.closeLocked(id)
	m.mu.Unlock()
	if password == "" {
		return m.secrets.Delete(secretKey(id))
	}
	return m.secrets.Set(secretKey(id), password)
}

// SetReadOnly turns writing on or off for a connection.
func (m *Manager) SetReadOnly(id string, ro bool) error {
	c, err := m.Get(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := c.ID
	if c.Builtin {
		key = "builtin:" + c.ID
	}
	found := false
	for i := range m.saved {
		if m.saved[i].ID == key {
			m.saved[i].ReadOnly, found = ro, true
		}
	}
	if !found {
		m.saved = append(m.saved, Conn{ID: key, Name: key, Driver: "override", ReadOnly: ro})
	}
	m.closeLocked(c.ID) // reopen with the new mode (SQLite opens the file read-only or writable)
	return m.persistLocked()
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.saved[:0]
	found := false
	for _, c := range m.saved {
		if c.ID == id && c.Driver != "override" {
			found = true
			continue
		}
		out = append(out, c)
	}
	if !found {
		return fmt.Errorf("no saved connection %q", id)
	}
	m.saved = out
	m.closeLocked(id)
	_ = m.secrets.Delete(secretKey(id))
	return m.persistLocked()
}

func (m *Manager) persistLocked() error {
	var keep []Conn
	for _, c := range m.saved {
		keep = append(keep, c)
	}
	b, _ := json.MarshalIndent(keep, "", "  ")
	tmp := m.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.file)
}

func (m *Manager) closeLocked(id string) {
	closeTunnel(id)
	for k, p := range m.pools {
		if k == id || strings.HasPrefix(k, id+"|") {
			p.Close()
			delete(m.pools, k)
		}
	}
}

// Close releases every open connection.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, p := range m.pools {
		p.Close()
		delete(m.pools, k)
		closeTunnel(strings.SplitN(k, "|", 2)[0])
	}
}

/* ------------------------------------------------------------------ open */

func (m *Manager) open(c Conn, database string) (*sql.DB, error) {
	if m.Prepare != nil {
		if err := m.Prepare(c); err != nil {
			return nil, err
		}
	}
	key := c.ID + "|" + database
	m.mu.Lock()
	if p, ok := m.pools[key]; ok {
		m.mu.Unlock()
		return p, nil
	}
	m.mu.Unlock()
	pwKey := secretKey(c.ID)
	if c.PasswordKey != "" {
		pwKey = c.PasswordKey
	}
	pw, _ := m.secrets.Get(pwKey)
	var driver, dsn string
	switch c.Driver {
	case "mysql":
		cfg := mysql.NewConfig()
		cfg.User, cfg.Passwd, cfg.DBName = c.User, pw, database
		if c.SSHHost != "" {
			lp, err := tunnelFor(c, orDefault(c.Port, 3306))
			if err != nil {
				return nil, err
			}
			cfg.Net, cfg.Addr = "tcp", fmt.Sprintf("127.0.0.1:%d", lp)
		} else if c.Socket != "" {
			cfg.Net, cfg.Addr = "unix", c.Socket
		} else {
			cfg.Net, cfg.Addr = "tcp", fmt.Sprintf("%s:%d", c.Host, orDefault(c.Port, 3306))
		}
		cfg.Timeout, cfg.ReadTimeout = 5*time.Second, 60*time.Second
		cfg.Params = map[string]string{"charset": "utf8mb4"}
		driver, dsn = "mysql", cfg.FormatDSN()
	case "postgres":
		if database == "" {
			database = c.Database
		}
		hostport := fmt.Sprintf("%s:%d", c.Host, orDefault(c.Port, 5432))
		if c.SSHHost != "" {
			lp, err := tunnelFor(c, orDefault(c.Port, 5432))
			if err != nil {
				return nil, err
			}
			hostport = fmt.Sprintf("127.0.0.1:%d", lp)
		}
		u := url.URL{Scheme: "postgres", User: url.UserPassword(c.User, pw), Host: hostport, Path: "/" + database}
		q := u.Query()
		q.Set("sslmode", orDefaultS(c.SSLMode, "prefer"))
		q.Set("connect_timeout", "5")
		u.RawQuery = q.Encode()
		driver, dsn = "pgx", u.String()
	case "sqlite":
		mode := "rwc" // a writable connection creates the file if needed
		if c.ReadOnly {
			mode = "ro"
		}
		driver, dsn = "sqlite", "file:"+c.File+"?mode="+mode+"&_pragma=busy_timeout(3000)"
	default:
		return nil, fmt.Errorf("unsupported driver %q", c.Driver)
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(3)
	db.SetConnMaxIdleTime(2 * time.Minute)
	m.mu.Lock()
	m.pools[key] = db
	m.mu.Unlock()
	return db, nil
}

/* --------------------------------------------------------------- browse */

// Info is what Test returns.
type Info struct {
	Server string `json:"server"`
	MS     int64  `json:"ms"`
}

func (m *Manager) Test(id string) (Info, error) {
	c, err := m.Get(id)
	if err != nil {
		return Info{}, err
	}
	db, err := m.open(c, "")
	if err != nil {
		return Info{}, err
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var v string
	q := map[string]string{"mysql": "SELECT VERSION()", "postgres": "SELECT version()", "sqlite": "SELECT 'SQLite ' || sqlite_version()"}[c.Driver]
	if err := db.QueryRowContext(ctx, q).Scan(&v); err != nil {
		return Info{}, friendly(err)
	}
	if c.Driver == "postgres" {
		v = strings.SplitN(v, " on ", 2)[0]
	} else if c.Driver == "mysql" {
		v = "MySQL " + v
	}
	return Info{Server: v, MS: time.Since(start).Milliseconds()}, nil
}

// Schemas lists databases (MySQL), schemas (PostgreSQL) or "main" (SQLite).
func (m *Manager) Schemas(id string) ([]string, error) {
	c, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	switch c.Driver {
	case "sqlite":
		return []string{"main"}, nil
	case "mysql":
		return m.strings(c, "", "SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','information_schema','performance_schema','sys') ORDER BY 1")
	default:
		return m.strings(c, "", "SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog','information_schema','pg_toast') AND schema_name NOT LIKE 'pg_temp%' AND schema_name NOT LIKE 'pg_toast_temp%' ORDER BY 1")
	}
}

// Table is one table or view.
type Table struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // table | view
	Rows   int64  `json:"rows"` // estimate
	SizeKB int64  `json:"size_kb"`
}

func (m *Manager) Tables(id, schema string) ([]Table, error) {
	c, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	var q string
	var args []any
	switch c.Driver {
	case "mysql":
		q = `SELECT table_name, IF(table_type='VIEW','view','table'), COALESCE(table_rows,0), COALESCE((data_length+index_length) DIV 1024,0)
			FROM information_schema.tables WHERE table_schema = ? ORDER BY 1`
		args = []any{schema}
	case "postgres":
		q = `SELECT c.relname, CASE WHEN c.relkind IN ('v','m') THEN 'view' ELSE 'table' END, GREATEST(c.reltuples,0)::bigint, pg_total_relation_size(c.oid)/1024
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relkind IN ('r','p','v','m') ORDER BY 1`
		args = []any{schema}
	default:
		q = `SELECT name, type, 0, 0 FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY 1`
	}
	db, err := m.open(c, dbFor(c, schema))
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, friendly(err)
	}
	defer rows.Close()
	out := []Table{}
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Name, &t.Type, &t.Rows, &t.SizeKB); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Column describes a table column.
type Column struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Nullable bool    `json:"nullable"`
	Default  *string `json:"default"`
	Key      string  `json:"key,omitempty"` // PRI | UNI | MUL
	Extra    string  `json:"extra,omitempty"`
}

// Index describes a table index.
type Index struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
}

// Structure is a table's columns and indexes.
type Structure struct {
	Columns []Column `json:"columns"`
	Indexes []Index  `json:"indexes"`
}

func (m *Manager) Structure(id, schema, table string) (Structure, error) {
	c, err := m.Get(id)
	if err != nil {
		return Structure{}, err
	}
	db, err := m.open(c, dbFor(c, schema))
	if err != nil {
		return Structure{}, err
	}
	var st Structure
	switch c.Driver {
	case "mysql":
		rows, err := db.Query(`SELECT column_name, column_type, is_nullable='YES', column_default, column_key, extra
			FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`, schema, table)
		if err != nil {
			return st, friendly(err)
		}
		for rows.Next() {
			var col Column
			if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Default, &col.Key, &col.Extra); err != nil {
				rows.Close()
				return st, err
			}
			st.Columns = append(st.Columns, col)
		}
		rows.Close()
		ir, err := db.Query(`SELECT index_name, non_unique = 0, column_name FROM information_schema.statistics
			WHERE table_schema = ? AND table_name = ? ORDER BY index_name, seq_in_index`, schema, table)
		if err != nil {
			return st, friendly(err)
		}
		st.Indexes = collectIndexes(ir)
	case "postgres":
		rows, err := db.Query(`SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull, pg_get_expr(d.adbin, d.adrelid),
			CASE WHEN EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY(i.indkey)) THEN 'PRI' ELSE '' END, ''
			FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
			WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, schema, table)
		if err != nil {
			return st, friendly(err)
		}
		for rows.Next() {
			var col Column
			if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Default, &col.Key, &col.Extra); err != nil {
				rows.Close()
				return st, err
			}
			st.Columns = append(st.Columns, col)
		}
		rows.Close()
		ir, err := db.Query(`SELECT i.relname, ix.indisunique, a.attname FROM pg_index ix
			JOIN pg_class t ON t.oid = ix.indrelid JOIN pg_class i ON i.oid = ix.indexrelid JOIN pg_namespace n ON n.oid = t.relnamespace
			JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY k(attnum, ord) ON true JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
			WHERE n.nspname = $1 AND t.relname = $2 ORDER BY i.relname, k.ord`, schema, table)
		if err != nil {
			return st, friendly(err)
		}
		st.Indexes = collectIndexes(ir)
	default:
		rows, err := db.Query(`SELECT name, type, "notnull" = 0, dflt_value, CASE WHEN pk > 0 THEN 'PRI' ELSE '' END, '' FROM pragma_table_info(?)`, table)
		if err != nil {
			return st, friendly(err)
		}
		for rows.Next() {
			var col Column
			if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Default, &col.Key, &col.Extra); err != nil {
				rows.Close()
				return st, err
			}
			st.Columns = append(st.Columns, col)
		}
		rows.Close()
		ir, err := db.Query(`SELECT il.name, il."unique", ii.name FROM pragma_index_list(?) il JOIN pragma_index_info(il.name) ii ORDER BY il.name, ii.seqno`, table)
		if err != nil {
			return st, friendly(err)
		}
		st.Indexes = collectIndexes(ir)
	}
	if len(st.Columns) == 0 {
		return st, fmt.Errorf("no table %q", table)
	}
	return st, nil
}

func collectIndexes(rows *sql.Rows) []Index {
	defer rows.Close()
	var out []Index
	for rows.Next() {
		var name, col string
		var uniq bool
		if rows.Scan(&name, &uniq, &col) != nil {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Name == name {
			out[n-1].Columns = append(out[n-1].Columns, col)
		} else {
			out = append(out, Index{Name: name, Unique: uniq, Columns: []string{col}})
		}
	}
	return out
}

/* ------------------------------------------------------------------ rows */

// Filter is one condition in the rows view (values are always bound parameters).
type Filter struct {
	Column string `json:"column"`
	Op     string `json:"op"` // = != < > <= >= contains starts null notnull
	Value  string `json:"value"`
}

// RowsRequest pages through a table.
type RowsRequest struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
	OrderBy string   `json:"order_by"`
	Desc    bool     `json:"desc"`
	Filters []Filter `json:"filters"`
}

// Result is a grid of values. Cells are strings; nil is SQL NULL. Long values are cut.
type Result struct {
	Columns   []string    `json:"columns"`
	Rows      [][]*string `json:"rows"`
	Total     int64       `json:"total"` // matching rows (Rows), -1 when not counted
	Affected  int64       `json:"affected,omitempty"`
	MS        int64       `json:"ms"`
	Truncated bool        `json:"truncated,omitempty"`
	ReadOnly  bool        `json:"read_only"`
	Statement string      `json:"statement,omitempty"`
}

const maxCell = 4000

func (m *Manager) Rows(id string, r RowsRequest) (Result, error) {
	c, err := m.Get(id)
	if err != nil {
		return Result{}, err
	}
	st, err := m.Structure(id, r.Schema, r.Table) // validates table and gives the column list
	if err != nil {
		return Result{}, err
	}
	cols := map[string]bool{}
	for _, col := range st.Columns {
		cols[col.Name] = true
	}
	if r.Limit <= 0 || r.Limit > 1000 {
		r.Limit = 100
	}
	if r.Offset < 0 {
		r.Offset = 0
	}
	from := quoteTable(c.Driver, r.Schema, r.Table)
	var where []string
	var args []any
	ph := func() string {
		if c.Driver == "postgres" {
			return fmt.Sprintf("$%d", len(args))
		}
		return "?"
	}
	for _, f := range r.Filters {
		if !cols[f.Column] {
			return Result{}, fmt.Errorf("unknown column %q", f.Column)
		}
		q := quoteIdent(c.Driver, f.Column)
		if c.Driver == "postgres" && (f.Op == "contains" || f.Op == "starts") {
			q += "::text"
		}
		switch f.Op {
		case "=", "!=", "<", ">", "<=", ">=":
			args = append(args, f.Value)
			op := f.Op
			if op == "!=" {
				op = "<>"
			}
			where = append(where, q+" "+op+" "+ph())
		case "contains":
			args = append(args, "%"+likeEscape(f.Value)+"%")
			where = append(where, q+" LIKE "+ph())
		case "starts":
			args = append(args, likeEscape(f.Value)+"%")
			where = append(where, q+" LIKE "+ph())
		case "null":
			where = append(where, q+" IS NULL")
		case "notnull":
			where = append(where, q+" IS NOT NULL")
		default:
			return Result{}, fmt.Errorf("unknown filter %q", f.Op)
		}
	}
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}
	order := ""
	if r.OrderBy != "" {
		if !cols[r.OrderBy] {
			return Result{}, fmt.Errorf("unknown column %q", r.OrderBy)
		}
		order = " ORDER BY " + quoteIdent(c.Driver, r.OrderBy)
		if r.Desc {
			order += " DESC"
		}
	}
	db, err := m.open(c, dbFor(c, r.Schema))
	if err != nil {
		return Result{}, err
	}
	start := time.Now()
	res := Result{Total: -1, ReadOnly: true}
	err = readOnly(db, c.Driver, func(tx *sql.Tx) error {
		if err := tx.QueryRow("SELECT COUNT(*) FROM "+from+w, args...).Scan(&res.Total); err != nil {
			return err
		}
		q := fmt.Sprintf("SELECT * FROM %s%s%s LIMIT %d OFFSET %d", from, w, order, r.Limit, r.Offset)
		return collect(tx.QueryContext(context.Background(), q, args...))(&res, r.Limit)
	})
	res.MS = time.Since(start).Milliseconds()
	return res, friendly(err)
}

/* ----------------------------------------------------------------- query */

// QueryRequest runs one SQL statement.
type QueryRequest struct {
	Schema     string `json:"schema"`
	SQL        string `json:"sql"`
	AllowWrite bool   `json:"allow_write"` // also needs the connection's write switch on
}

const maxQueryRows = 1000

// Query runs one statement. Unless writing is allowed for the connection and requested, it runs in a
// read-only transaction that is rolled back, so the server refuses any change.
func (m *Manager) Query(id string, r QueryRequest) (Result, error) {
	c, err := m.Get(id)
	if err != nil {
		return Result{}, err
	}
	sqlText := strings.TrimSpace(r.SQL)
	sqlText = strings.TrimSpace(strings.TrimSuffix(sqlText, ";"))
	if sqlText == "" {
		return Result{}, errors.New("write a query first")
	}
	db, err := m.open(c, dbFor(c, r.Schema))
	if err != nil {
		return Result{}, err
	}
	write := r.AllowWrite && !c.ReadOnly
	res := Result{Total: -1, ReadOnly: !write, Statement: sqlText}
	// Layer 1: in read-only mode only reading statements may run at all. MySQL ends a transaction
	// before DDL (DROP, ALTER, CREATE...), so a read-only transaction alone does not stop those.
	if !write && !readOnlyRe.MatchString(sqlText) {
		if c.ReadOnly {
			return res, errors.New("This connection is read-only, so only reading statements (SELECT, SHOW, DESCRIBE, EXPLAIN, WITH) can run. Turn on “Allow changes” for this connection to change data")
		}
		return res, errors.New("This statement can change data. Tick “Allow this query to change data” to run it")
	}
	start := time.Now()
	run := func(q interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}) error {
		return collect(q.QueryContext(context.Background(), sqlText))(&res, maxQueryRows)
	}
	if write {
		tx, err := db.Begin()
		if err != nil {
			return res, friendly(err)
		}
		if returnsRows(sqlText) {
			err = run(tx)
		} else {
			var r sql.Result
			if r, err = tx.Exec(sqlText); err == nil {
				res.Affected, _ = r.RowsAffected()
			}
		}
		if err != nil {
			_ = tx.Rollback()
			return res, friendly(err)
		}
		err = tx.Commit()
		res.MS = time.Since(start).Milliseconds()
		return res, friendly(err)
	}
	err = readOnly(db, c.Driver, func(tx *sql.Tx) error { return run(tx) })
	res.MS = time.Since(start).Milliseconds()
	if err != nil && !write && looksLikeWrite(sqlText) {
		return res, fmt.Errorf("%v — this connection is read-only. Turn on “Allow changes” for it to run statements that change data", friendly(err))
	}
	return res, friendly(err)
}

// readOnly runs fn in a transaction the server enforces as read-only, then rolls it back.
func readOnly(db *sql.DB, driver string, fn func(*sql.Tx) error) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: driver != "sqlite"}) // SQLite files are opened read-only instead
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}

func collect(rows *sql.Rows, err error) func(*Result, int) error {
	return func(res *Result, limit int) error {
		if err != nil {
			return err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		res.Columns = cols
		res.Rows = [][]*string{}
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		for rows.Next() {
			if len(res.Rows) >= limit {
				res.Truncated = true
				break
			}
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
			row := make([]*string, len(cols))
			for i, v := range raw {
				row[i] = cell(v)
			}
			res.Rows = append(res.Rows, row)
		}
		return rows.Err()
	}
}

func cell(v any) *string {
	var s string
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		if utf8.Valid(x) {
			s = string(x)
		} else {
			if len(x) > 64 {
				s = fmt.Sprintf("0x%s… (%d bytes)", hex.EncodeToString(x[:64]), len(x))
			} else {
				s = "0x" + hex.EncodeToString(x)
			}
		}
	case time.Time:
		s = x.Format("2006-01-02 15:04:05")
	default:
		s = fmt.Sprint(x)
	}
	if len(s) > maxCell {
		s = s[:maxCell] + "…"
	}
	return &s
}

/* --------------------------------------------------------------- helpers */

func (m *Manager) strings(c Conn, database, q string) ([]string, error) {
	db, err := m.open(c, database)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(q)
	if err != nil {
		return nil, friendly(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// dbFor picks the database to connect to: MySQL connects per schema, PostgreSQL to its database.
func dbFor(c Conn, schema string) string {
	if c.Driver == "mysql" {
		return schema
	}
	return ""
}

func quoteIdent(driver, name string) string {
	if driver == "mysql" {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quoteTable(driver, schema, table string) string {
	if driver == "sqlite" || schema == "" {
		return quoteIdent(driver, table)
	}
	return quoteIdent(driver, schema) + "." + quoteIdent(driver, table)
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

var (
	// Statements allowed on a read-only connection (layer 1; layer 2 is the read-only transaction).
	readOnlyRe = regexp.MustCompile(`(?is)^\s*(select|show|describe|desc|explain|with|values|table|pragma)\b`)
	rowsRe     = regexp.MustCompile(`(?is)^\s*(select|show|describe|desc|explain|with|pragma|values|table)\b`)
	writeRe    = regexp.MustCompile(`(?is)^\s*(insert|update|delete|replace|create|alter|drop|truncate|rename|grant|revoke)\b`)
)

func returnsRows(q string) bool {
	return rowsRe.MatchString(q) || strings.Contains(strings.ToUpper(q), " RETURNING ")
}
func looksLikeWrite(q string) bool { return writeRe.MatchString(q) }

// friendly turns driver errors into plain messages.
func friendly(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Access denied") || strings.Contains(msg, "password authentication failed"):
		return errors.New("The server refused the login. Check the user and password for this connection")
	case strings.Contains(msg, "connect: connection refused") || strings.Contains(msg, "no such file or directory") && strings.Contains(msg, ".sock"):
		return errors.New("The database server is not running or not reachable at this address")
	case strings.Contains(msg, "read-only transaction") || strings.Contains(msg, "READ ONLY transaction") || strings.Contains(msg, "attempt to write a readonly database"):
		return errors.New("Blocked: this connection is read-only")
	}
	return err
}

func orDefault(v, d int) int {
	if v == 0 {
		return d
	}
	return v
}

func orDefaultS(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func slug(s string) string {
	s = strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(s, "-"))
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// SortedIDs is a helper for tests.
func SortedIDs(cs []Conn) []string {
	var ids []string
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}
