// Package importers moves projects and databases from other local tools into LocalSites.
// Source data is only ever read: databases are copied, verified, and the original stays untouched.
package importers

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Host is a virtual host found in another tool's config.
type Host struct {
	Name    string `json:"name"`
	Docroot string `json:"docroot"`
	PHP     string `json:"php,omitempty"` // full version, e.g. "8.2.0"
	Source  string `json:"source"`
}

// Source describes a tool we can import from.
type Source struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Found     bool     `json:"found"`
	HostConfs []string `json:"-"`
	MySQLSock string   `json:"mysql_socket"`
	MySQLBin  string   `json:"-"` // directory with mysqldump/mysql of the source's MySQL version
	MySQLUp   bool     `json:"mysql_running"`
	SameAs    string   `json:"same_mysql_as,omitempty"` // shares its MySQL with another listed tool
}

// Sources lists supported tools and whether they are installed.
func Sources() []Source {
	list := []Source{
		{ID: "mamp-pro", Name: "MAMP PRO", HostConfs: []string{
			"/Library/Application Support/appsolute/MAMP PRO/conf/httpd.conf",
			"/Library/Application Support/appsolute/MAMP PRO/conf/httpd-ssl.conf",
		}, MySQLSock: "/Applications/MAMP/tmp/mysql/mysql.sock", MySQLBin: "/Applications/MAMP/Library/bin"},
		{ID: "mamp", Name: "MAMP", HostConfs: []string{"/Applications/MAMP/conf/apache/extra/httpd-vhosts.conf"},
			MySQLSock: "/Applications/MAMP/tmp/mysql/mysql.sock", MySQLBin: "/Applications/MAMP/Library/bin"},
		{ID: "xampp", Name: "XAMPP", HostConfs: []string{"/Applications/XAMPP/xamppfiles/etc/extra/httpd-vhosts.conf"},
			MySQLSock: "/Applications/XAMPP/xamppfiles/var/mysql/mysql.sock", MySQLBin: "/Applications/XAMPP/xamppfiles/bin"},
	}
	for i := range list {
		for _, f := range list[i].HostConfs {
			if _, err := os.Stat(f); err == nil {
				list[i].Found = true
			}
		}
		if _, err := os.Stat(list[i].MySQLSock); err == nil {
			list[i].MySQLUp = true
		}
		for j := 0; j < i; j++ { // MAMP and MAMP PRO run the same MySQL: copy from it once
			if list[j].MySQLSock == list[i].MySQLSock && list[j].Found {
				list[i].SameAs = list[j].ID
				break
			}
		}
	}
	return list
}

func FindSource(id string) (Source, bool) {
	for _, s := range Sources() {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}

var (
	vhostRe = regexp.MustCompile(`(?s)<VirtualHost[^>]*>(.*?)</VirtualHost>`)
	rootRe  = regexp.MustCompile(`(?m)^\s*DocumentRoot\s+"?([^"\n]+?)"?\s*$`)
	nameRe  = regexp.MustCompile(`(?m)^\s*ServerName\s+(\S+)`)
	phpRe   = regexp.MustCompile(`php(\d+\.\d+\.\d+)\.fcgi`)
)

// Hosts reads virtual hosts from the source's Apache config (read-only).
func (s Source) Hosts() []Host {
	byRoot := map[string]Host{}
	for _, f := range s.HostConfs {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range vhostRe.FindAllStringSubmatch(string(b), -1) {
			root := rootRe.FindStringSubmatch(m[1])
			if root == nil {
				continue
			}
			h := Host{Docroot: strings.TrimRight(root[1], "/"), Source: s.ID}
			if n := nameRe.FindStringSubmatch(m[1]); n != nil {
				h.Name = n[1]
			}
			if p := phpRe.FindStringSubmatch(m[1]); p != nil {
				h.PHP = p[1]
			}
			if _, seen := byRoot[h.Docroot]; !seen || strings.Contains(f, "ssl") {
				byRoot[h.Docroot] = h
			}
		}
	}
	out := make([]Host, 0, len(byRoot))
	for _, h := range byRoot {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Docroot < out[j].Docroot })
	return out
}

// Connect opens the source MySQL (read-only use).
func (s Source) Connect(user, password string) (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd, cfg.Net, cfg.Addr = user, password, "unix", s.MySQLSock
	cfg.Timeout = 5 * time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot log in to %s MySQL: %w", s.Name, err)
	}
	return db, nil
}

// Dump streams a logical dump of one database to w using the source's own mysqldump.
// --single-transaction gives a consistent read for InnoDB without blocking; MyISAM tables get a
// brief read lock. Nothing is written to the source.
func (s Source) Dump(user, password, database string, w io.Writer) error {
	bin := filepath.Join(s.MySQLBin, "mysqldump")
	cmd := exec.Command(bin, "--socket="+s.MySQLSock, "--user="+user,
		"--single-transaction", "--routines", "--triggers", "--events", "--hex-blob",
		"--default-character-set=utf8mb4", "--skip-comments", "--databases", database)
	cmd.Env = append(os.Environ(), "MYSQL_PWD="+password)
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mysqldump %s: %v %s", database, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// TableCheck is the verification result for one table.
type TableCheck struct {
	Table      string `json:"table"`
	SourceRows int64  `json:"source_rows"`
	DestRows   int64  `json:"dest_rows"`
	Match      bool   `json:"match"`
	Note       string `json:"note,omitempty"`
}

// Report is the outcome of copying one database.
type Report struct {
	Database string       `json:"database"`
	Tables   []TableCheck `json:"tables"`
	OK       bool         `json:"ok"`
	Backup   string       `json:"source_backup"` // dump of the source kept on disk
	Duration string       `json:"duration"`
}

// Verify compares table lists, row counts and full row contents between source and destination.
func Verify(src, dst *sql.DB, database string) ([]TableCheck, bool, error) {
	st, err := tables(src, database)
	if err != nil {
		return nil, false, err
	}
	dt, err := tables(dst, database)
	if err != nil {
		return nil, false, err
	}
	ok := true
	var out []TableCheck
	for _, t := range st {
		c := TableCheck{Table: t}
		if !contains(dt, t) {
			c.Note, ok = "missing in LocalSites", false
			out = append(out, c)
			continue
		}
		sh, sn, err := tableHash(src, database, t)
		if err != nil {
			return nil, false, err
		}
		dh, dn, err := tableHash(dst, database, t)
		if err != nil {
			return nil, false, err
		}
		c.SourceRows, c.DestRows = sn, dn
		c.Match = sh == dh && sn == dn
		if !c.Match {
			ok = false
			if sn == dn {
				c.Note = "same row count but different contents"
			}
		}
		out = append(out, c)
	}
	return out, ok, nil
}

func tables(db *sql.DB, database string) ([]string, error) {
	rows, err := db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_type = 'BASE TABLE' ORDER BY table_name", database)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// tableHash reads every row in a stable order and hashes the raw values.
func tableHash(db *sql.DB, database, table string) (string, int64, error) {
	q := fmt.Sprintf("SELECT * FROM `%s`.`%s`", strings.ReplaceAll(database, "`", "``"), strings.ReplaceAll(table, "`", "``"))
	cols, err := columnCount(db, database, table)
	if err != nil {
		return "", 0, err
	}
	order := make([]string, cols)
	for i := range order {
		order[i] = fmt.Sprint(i + 1)
	}
	rows, err := db.Query(q + " ORDER BY " + strings.Join(order, ","))
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	h := sha256.New()
	vals := make([]sql.RawBytes, cols)
	ptrs := make([]any, cols)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var n int64
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return "", 0, err
		}
		for _, v := range vals {
			if v == nil {
				h.Write([]byte{0xff, 0})
			} else {
				fmt.Fprintf(h, "%d:", len(v))
				h.Write(v)
			}
		}
		h.Write([]byte{'\n'})
		n++
	}
	return hex.EncodeToString(h.Sum(nil)), n, rows.Err()
}

func columnCount(db *sql.DB, database, table string) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ?", database, table).Scan(&n)
	return n, err
}

// DatabaseExists reports whether name exists on db.
func DatabaseExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?", name).Scan(&n)
	return n > 0, err
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
