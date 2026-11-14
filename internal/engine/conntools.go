package engine

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/dbclient"
	"github.com/localsites/localsites/internal/runtime"
	"github.com/localsites/localsites/internal/secrets"
	"github.com/localsites/localsites/internal/services"
	"github.com/localsites/localsites/internal/sqlfile"
)

// Export and import for any saved connection in the data browser: MySQL/MariaDB (MAMP, remote, over
// SSH), PostgreSQL and SQLite. Export only reads (MySQL: one consistent snapshot without locking
// tables). Import needs a connection that allows changes, and saves an export of what is there first.

var schemaRe = regexp.MustCompile(`^[A-Za-z0-9_$][A-Za-z0-9_$-]{0,63}$`) // no leading "-": it would reach mysqldump/psql as an option

// psqlRestricted is psql that understands \restrict (17.6, 16.10, 15.14, 14.19, 13.22 and later), which
// imports use to switch psql's own commands (\! runs a shell) off for the file.
func psqlRestricted() (string, error) {
	bin, err := pgTool("psql")
	if err != nil {
		return "", err
	}
	out, _ := exec.Command(bin, "--version").Output()
	m := regexp.MustCompile(`(\d+)\.(\d+)`).FindStringSubmatch(string(out))
	if m != nil {
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		if need, ok := map[int]int{13: 22, 14: 19, 15: 14, 16: 10, 17: 6}[major]; major >= 18 || ok && minor >= need {
			return bin, nil
		}
	}
	return "", fmt.Errorf("%s is too old to import files safely. Update it with: brew upgrade libpq", strings.TrimSpace(string(out)))
}

// pgTool finds pg_dump or psql, newest first (a newer pg_dump can read older servers, not the reverse).
func pgTool(name string) (string, error) {
	for _, d := range []string{"/opt/homebrew/opt/libpq/bin", "/opt/homebrew/opt/libpq@18/bin", "/opt/homebrew/opt/postgresql@18/bin", "/opt/homebrew/opt/postgresql@17/bin",
		"/usr/local/opt/libpq/bin", "/Applications/Postgres.app/Contents/Versions/latest/bin"} {
		if p := filepath.Join(d, name); isExec(p) {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s isn't installed. Install it with: brew install libpq", name)
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func mysqlTool(name string) (string, error) {
	base, ok := runtime.MySQL()
	if !ok {
		return "", errors.New("the MySQL tools aren't installed (LocalSites uses MySQL 8.4's mysqldump)")
	}
	return filepath.Join(base, "bin", name), nil
}

func mysqlArgs(ep dbclient.Endpoint) []string {
	a := []string{"--user=" + ep.User, "--default-character-set=utf8mb4"}
	if ep.Socket != "" {
		return append(a, "--socket="+ep.Socket)
	}
	return append(a, "--host="+ep.Host, "--port="+strconv.Itoa(ep.Port), "--protocol=TCP")
}

func pgArgs(ep dbclient.Endpoint, db string) []string {
	return []string{"--host=" + ep.Host, "--port=" + strconv.Itoa(ep.Port), "--username=" + ep.User, "--dbname=" + db, "--no-password"}
}

func run(bin string, args, env []string, stdin io.Reader, stdout io.Writer) error {
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	return runCmd(cmd, bin, stdin, stdout)
}

// runMySQL runs a MySQL client tool with the password passed privately (see services.MySQLCommand).
func runMySQL(bin, password string, args []string, stdin io.Reader, stdout io.Writer) error {
	cmd, done := services.MySQLCommand(bin, password, args...)
	defer done()
	return runCmd(cmd, bin, stdin, stdout)
}

func runCmd(cmd *exec.Cmd, bin string, stdin io.Reader, stdout io.Writer) error {
	cmd.Stdin, cmd.Stdout = stdin, stdout
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if lines := strings.Split(msg, "\n"); len(lines) > 3 {
			msg = strings.Join(lines[len(lines)-3:], " / ")
		}
		return fmt.Errorf("%s: %v %s", filepath.Base(bin), err, msg)
	}
	return nil
}

// dumpConn writes schema (a MySQL database, a PostgreSQL schema, ignored for SQLite) to w.
func (e *Engine) dumpConn(ep dbclient.Endpoint, schema, mode string, w io.Writer) error {
	switch ep.Driver {
	case "mysql":
		bin, err := mysqlTool("mysqldump")
		if err != nil {
			return err
		}
		base := append(mysqlArgs(ep), "--single-transaction", "--skip-lock-tables", "--hex-blob", "--no-tablespaces", "--column-statistics=0", "--set-gtid-purged=OFF")
		switch mode {
		case "structure":
			base = append(base, "--no-data")
		case "data":
			base = append(base, "--no-create-info", "--skip-triggers")
		}
		// routines need extra rights some logins don't have: try with them, then without
		if mode != "data" {
			if err := runMySQL(bin, ep.Password, append(append(base, "--routines"), schema), nil, w); err == nil {
				return nil
			} else if !strings.Contains(strings.ToLower(err.Error()), "denied") && !strings.Contains(err.Error(), "routines") {
				return err
			}
		}
		return runMySQL(bin, ep.Password, append(base, schema), nil, w)
	case "postgres":
		bin, err := pgTool("pg_dump")
		if err != nil {
			return err
		}
		args := append(pgArgs(ep, ep.Database), "--no-owner", "--no-privileges")
		if schema != "" {
			args = append(args, "--schema="+schema)
		}
		switch mode {
		case "structure":
			args = append(args, "--schema-only")
		case "data":
			args = append(args, "--data-only")
		}
		return run(bin, args, []string{"PGPASSWORD=" + ep.Password, "PGCONNECT_TIMEOUT=10", "PGSSLMODE=" + ep.SSLMode}, nil, w)
	case "sqlite":
		cmd := ".dump"
		switch mode {
		case "structure":
			cmd = ".schema"
		case "data":
			cmd = ".dump --data-only"
		}
		return run("/usr/bin/sqlite3", []string{"-readonly", "-safe", "--", ep.File, cmd}, nil, nil, w)
	}
	return fmt.Errorf("export isn't available for %s connections", ep.Driver)
}

// ExportConnection writes one database (MySQL), schema (PostgreSQL) or the whole file (SQLite) to a .sql or .sql.gz file.
func (e *Engine) ExportConnection(id, schema, file, mode string, overwrite bool) (ExportResult, error) {
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "structure" && mode != "data" {
		return ExportResult{}, errors.New("mode must be full, structure or data")
	}
	if schema != "" && !schemaRe.MatchString(schema) {
		return ExportResult{}, errors.New("invalid database or schema name")
	}
	if err := e.exportPath(file, overwrite); err != nil {
		return ExportResult{}, err
	}
	ep, err := e.Data.Endpoint(id)
	if err != nil {
		return ExportResult{}, err
	}
	if ep.Driver == "mysql" && schema == "" {
		return ExportResult{}, errors.New("choose a database to export")
	}
	w, err := sqlfile.Create(file)
	if err != nil {
		return ExportResult{}, err
	}
	w.NoReplace = !overwrite
	if err := e.dumpConn(ep, schema, mode, w); err != nil {
		w.Abort()
		return ExportResult{}, err
	}
	n, err := w.Commit()
	if err != nil {
		return ExportResult{}, err
	}
	what := ep.Name
	if schema != "" {
		what = schema + " (" + ep.Name + ")"
	}
	return ExportResult{File: file, Bytes: n, Log: []string{fmt.Sprintf("Exported %s to %s, %s", what, file, human(n))}}, nil
}

// ImportConnection loads a .sql, .sql.gz or .zip file into a connection's database or schema. The
// connection must allow changes; what is there is exported to the backups folder first.
func (e *Engine) ImportConnection(id, schema, file string) ([]string, error) {
	ep, err := e.Data.Endpoint(id)
	if err != nil {
		return nil, err
	}
	if ep.ReadOnly {
		return nil, fmt.Errorf("%s is read-only. Allow changes for this connection first (in Data), then import", ep.Name)
	}
	if schema != "" && !schemaRe.MatchString(schema) {
		return nil, errors.New("invalid database or schema name")
	}
	if ep.Driver == "mysql" && schema == "" {
		return nil, errors.New("choose the database to import into")
	}
	if !sqlfile.IsSQLFile(file) {
		return nil, errors.New("choose a .sql, .sql.gz or .zip file")
	}
	r, err := sqlfile.Open(file)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	// what is there now, so the import can be undone by hand
	_ = os.MkdirAll(e.P.Backups, 0o700)
	label := regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(ep.ID+"-"+schema, "_")
	before := filepath.Join(e.P.Backups, "conn-"+label+"-"+time.Now().Format("20060102-150405")+".sql.gz")
	nothing := false
	if ep.Driver == "sqlite" {
		if st, err := os.Stat(ep.File); err != nil || st.Size() == 0 { // a new file: nothing to save
			nothing = true
		}
	}
	if !nothing {
		w, err := sqlfile.Create(before)
		if err != nil {
			return nil, fmt.Errorf("couldn't save what is there first, so nothing was imported: %v", err)
		}
		if derr := e.dumpConn(ep, schema, "full", w); derr != nil {
			w.Abort()
			return nil, fmt.Errorf("couldn't save what is there first, so nothing was imported: %v", derr)
		}
		if _, err := w.Commit(); err != nil {
			return nil, fmt.Errorf("couldn't save what is there first, so nothing was imported: %v", err)
		}
	}
	start := time.Now()
	switch ep.Driver {
	case "mysql":
		in := sqlfile.ForMySQL(r)
		bin, err := mysqlTool("mysql")
		if err != nil {
			return nil, err
		}
		err = runMySQL(bin, ep.Password, append(mysqlArgs(ep), "--binary-mode", schema), in, io.Discard)
		if err != nil {
			return nil, fmt.Errorf("%v. It may be partly imported; what was there before is in %s", err, before)
		}
	case "postgres":
		bin, err := psqlRestricted()
		if err != nil {
			return nil, err
		}
		pre := ""
		if schema != "" {
			pre = fmt.Sprintf("SET search_path TO %q, public;\n", schema)
		}
		err = run(bin, append(pgArgs(ep, ep.Database), "--no-psqlrc", "--quiet", "--set=ON_ERROR_STOP=1", "--file=-"), []string{"PGPASSWORD=" + ep.Password, "PGSSLMODE=" + ep.SSLMode},
			sqlfile.ForPsql(io.MultiReader(strings.NewReader(pre), r), secrets.Generate(32)), io.Discard)
		if err != nil {
			return nil, fmt.Errorf("%v. It stopped at the first error; what was there before is in %s", err, before)
		}
	case "sqlite":
		// -safe: no .shell/.system/.load/.output, no ATTACH and no file functions from the dump
		err := run("/usr/bin/sqlite3", []string{"-safe", "-bail", "--", ep.File}, nil, sqlfile.KeepInDatabase(r), io.Discard)
		if err != nil {
			return nil, fmt.Errorf("%v. What was there before is in %s", err, before)
		}
	default:
		return nil, fmt.Errorf("import isn't available for %s connections", ep.Driver)
	}
	log := []string{fmt.Sprintf("Imported %s in %s", filepath.Base(file), time.Since(start).Round(100*time.Millisecond))}
	if !nothing {
		log = append(log, "What was there before is saved in "+filepath.Base(before))
	}
	return log, nil
}
