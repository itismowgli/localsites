package engine

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/secrets"
	"github.com/localsites/localsites/internal/services"
	"github.com/localsites/localsites/internal/sqlfile"
)

// Database tools for the PostgreSQL add-on. They log in as "postgres" over the add-on's socket, which is
// in a folder only this Mac user can open (TCP logins need the keychain password).
// The same rules as LocalSites MySQL: removing or replacing data makes a backup first.

// PGDatabase is one database in the add-on.
type PGDatabase struct {
	Name   string  `json:"name"`
	SizeMB float64 `json:"size_mb"`
	Tables int     `json:"tables"`
}

func (e *Engine) pgPort() (int, error) {
	for _, a := range e.Addons() {
		if a.ID == "postgres" {
			if !a.Enabled || !a.Installed {
				return 0, errors.New("the PostgreSQL add-on is off. Switch it on in Services")
			}
			return a.Port, nil
		}
	}
	return 0, errors.New("the PostgreSQL add-on isn't available")
}

func (e *Engine) pgRun(db string, stdin []byte, args ...string) (string, error) {
	port, err := e.pgPort()
	if err != nil {
		return "", err
	}
	bin, err := pgTool("psql")
	if err != nil {
		return "", err
	}
	full := append([]string{"--host=" + e.pgSocketDir(), "--port=" + strconv.Itoa(port), "--username=postgres", "--dbname=" + db, "--no-password", "--no-psqlrc", "--quiet", "--set=ON_ERROR_STOP=1"}, args...)
	var out bytes.Buffer
	var in *bytes.Reader
	if stdin != nil {
		in = bytes.NewReader(stdin)
	}
	if in == nil {
		err = run(bin, full, nil, nil, &out)
	} else {
		err = run(bin, full, nil, in, &out)
	}
	return out.String(), err
}

// pgSocketDir is where the add-on's socket is (its own 0700 folder).
func (e *Engine) pgSocketDir() string { return filepath.Join(e.P.Data, "addons", "postgres") }

// PGConnID is the data browser connection for one add-on database.
func PGConnID(db string) string {
	id := strings.ToLower(strings.ReplaceAll(db, "_", "-"))
	if len(id) > 26 {
		id = id[:26]
	}
	return "localsites-pg-" + id
}

// pgNames is the last list of add-on databases (kept by PGDatabases, so listing connections never runs psql).
func (e *Engine) pgNames() []string {
	e.pgMu.Lock()
	defer e.pgMu.Unlock()
	return append([]string(nil), e.pgList...)
}

// PGDatabases lists the add-on's databases (not the templates or the "postgres" maintenance database).
func (e *Engine) PGDatabases() ([]PGDatabase, error) {
	out, err := e.pgRun("postgres", nil, "--tuples-only", "--no-align", "--field-separator=|", "--command=SELECT datname, pg_database_size(datname) FROM pg_database WHERE NOT datistemplate AND datname <> 'postgres' ORDER BY datname")
	if err != nil {
		return nil, err
	}
	list := []PGDatabase{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(l, "|")
		if len(f) != 2 {
			continue
		}
		size, _ := strconv.ParseFloat(f[1], 64)
		d := PGDatabase{Name: f[0], SizeMB: size / (1 << 20)}
		if n, err := e.pgRun(d.Name, nil, "--tuples-only", "--no-align", "--command=SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')"); err == nil {
			d.Tables, _ = strconv.Atoi(strings.TrimSpace(n))
		}
		list = append(list, d)
	}
	names := make([]string, 0, len(list))
	for _, d := range list {
		names = append(names, d.Name)
	}
	e.pgMu.Lock()
	e.pgList = names
	e.pgMu.Unlock()
	return list, nil
}

func (e *Engine) pgCheck(name string, mustExist bool) error {
	if !config.ValidDatabaseName(name) {
		return errors.New("database names may use letters, digits and _ only")
	}
	list, err := e.PGDatabases()
	if err != nil {
		return err
	}
	found := false
	for _, d := range list {
		found = found || d.Name == name
	}
	if mustExist && !found {
		return fmt.Errorf("there is no PostgreSQL database named %s", name)
	}
	if !mustExist && found {
		return fmt.Errorf("%s already exists", name)
	}
	return nil
}

// PGCreate makes a database.
func (e *Engine) PGCreate(name string) ([]string, error) {
	if err := e.pgCheck(name, false); err != nil {
		return nil, err
	}
	if _, err := e.pgRun("postgres", nil, "--command=CREATE DATABASE \""+name+"\" ENCODING 'UTF8'"); err != nil {
		return nil, err
	}
	if _, err := e.pgOwner(name); err != nil {
		return nil, err
	}
	port, _ := e.pgPort()
	return []string{fmt.Sprintf("Created %s. Connect with postgresql://postgres@127.0.0.1:%d/%s", name, port, name)}, nil
}

func (e *Engine) pgDump(name, mode string, w *sqlfile.Writer) error {
	port, err := e.pgPort()
	if err != nil {
		return err
	}
	bin, err := pgTool("pg_dump")
	if err != nil {
		return err
	}
	args := []string{"--host=" + e.pgSocketDir(), "--port=" + strconv.Itoa(port), "--username=postgres", "--dbname=" + name, "--no-password", "--no-owner", "--no-privileges"}
	switch mode {
	case "structure":
		args = append(args, "--schema-only")
	case "data":
		args = append(args, "--data-only")
	}
	return run(bin, args, nil, nil, w)
}

// pgOwner makes sure a database has its own login without superuser rights ("ls_<name>") that owns
// it and everything in it. Imports run as that login, so a file can't use superuser powers (COPY …
// TO PROGRAM runs commands on this Mac). The login has no password: it can only log in over the
// add-on's private socket.
func (e *Engine) pgOwner(name string) (string, error) {
	role := services.UserFor(name)
	if _, err := e.pgRun("postgres", []byte(fmt.Sprintf(`DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN
    CREATE ROLE "%[1]s" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
  END IF;
END $$;
ALTER DATABASE "%[2]s" OWNER TO "%[1]s";
`, role, name)), "--file=-"); err != nil {
		return "", err
	}
	// tables, views, sequences (not those that belong to a column) and schemas made by the superuser
	_, err := e.pgRun(name, []byte(fmt.Sprintf(`DO $$ DECLARE r record; BEGIN
  FOR r IN SELECT n.nspname, c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
      WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg_toast%%'
        AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f') AND pg_get_userbyid(c.relowner) <> '%[1]s'
        AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype IN ('a', 'i'))
  LOOP
    EXECUTE format('ALTER %%s %%I.%%I OWNER TO %%I', CASE r.relkind WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW' WHEN 'S' THEN 'SEQUENCE' WHEN 'f' THEN 'FOREIGN TABLE' ELSE 'TABLE' END, r.nspname, r.relname, '%[1]s');
  END LOOP;
  FOR r IN SELECT nspname FROM pg_namespace WHERE nspname NOT IN ('pg_catalog', 'information_schema') AND nspname NOT LIKE 'pg\_%%'
      AND pg_get_userbyid(nspowner) NOT IN ('%[1]s', 'pg_database_owner')
  LOOP
    EXECUTE format('ALTER SCHEMA %%I OWNER TO %%I', r.nspname, '%[1]s');
  END LOOP;
END $$;
`, role)), "--file=-")
	return role, err
}

// pgBackup saves a compressed dump in the backups folder ("pg-<name>-<time>.sql.gz").
func (e *Engine) pgBackup(name, note string) (string, error) {
	_ = os.MkdirAll(e.P.Backups, 0o700)
	stamp := time.Now().Format("20060102-150405")
	f := filepath.Join(e.P.Backups, "pg-"+name+"-"+stamp+".sql.gz")
	for i := 2; i < 100; i++ { // two backups in the same second: "-2", "-3" …
		if _, err := os.Lstat(f); err != nil {
			break
		}
		f = filepath.Join(e.P.Backups, fmt.Sprintf("pg-%s-%s-%d.sql.gz", name, stamp, i))
	}
	w, err := sqlfile.Create(f)
	if err != nil {
		return "", err
	}
	w.NoReplace = true
	if err := e.pgDump(name, "full", w); err != nil {
		w.Abort()
		return "", fmt.Errorf("couldn't make a backup first, so nothing was changed: %v", err)
	}
	if _, err := w.Commit(); err != nil {
		return "", err
	}
	if note != "" {
		_ = os.WriteFile(f+".note", []byte(note), 0o600)
	}
	return f, nil
}

// PGExport writes a database to a .sql or .sql.gz file.
func (e *Engine) PGExport(name, file, mode string, overwrite bool) (ExportResult, error) {
	if mode == "" {
		mode = "full"
	}
	if err := e.pgCheck(name, true); err != nil {
		return ExportResult{}, err
	}
	if err := e.exportPath(file, overwrite); err != nil {
		return ExportResult{}, err
	}
	w, err := sqlfile.Create(file)
	if err != nil {
		return ExportResult{}, err
	}
	w.NoReplace = !overwrite
	if err := e.pgDump(name, mode, w); err != nil {
		w.Abort()
		return ExportResult{}, err
	}
	n, err := w.Commit()
	if err != nil {
		return ExportResult{}, err
	}
	return ExportResult{File: file, Bytes: n, Log: []string{fmt.Sprintf("Exported %s to %s, %s", name, file, human(n))}}, nil
}

// PGImport loads a .sql, .sql.gz or .zip file into a database, after a backup; it stops at the first error.
func (e *Engine) PGImport(name, file string) ([]string, error) {
	if err := e.pgCheck(name, true); err != nil {
		return nil, err
	}
	r, err := sqlfile.Open(file)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	before, err := e.pgBackup(name, "before importing "+filepath.Base(file))
	if err != nil {
		return nil, err
	}
	port, _ := e.pgPort()
	bin, err := psqlRestricted()
	if err != nil {
		return nil, err
	}
	role, err := e.pgOwner(name)
	if err != nil {
		return nil, err
	}
	// as the database's owner (no superuser rights) and with psql's own commands off
	err = run(bin, []string{"--host=" + e.pgSocketDir(), "--port=" + strconv.Itoa(port), "--username=" + role, "--dbname=" + name, "--no-password", "--no-psqlrc", "--quiet", "--set=ON_ERROR_STOP=1", "--file=-"},
		nil, sqlfile.ForPsql(r, secrets.Generate(32)), nil)
	if err != nil {
		return nil, fmt.Errorf("%v. It stopped at the first error; the backup from just before is %s", err, filepath.Base(before))
	}
	return []string{"Imported " + filepath.Base(file) + " into " + name, "Backup made first: " + filepath.Base(before)}, nil
}

// PGDuplicate copies a database (structure and data) into a new one.
func (e *Engine) PGDuplicate(name, newName string) ([]string, error) {
	if err := e.pgCheck(name, true); err != nil {
		return nil, err
	}
	if err := e.pgCheck(newName, false); err != nil {
		return nil, err
	}
	e.Data.CloseConn("localsites-pg") // a template can't have open connections
	if _, err := e.pgRun("postgres", nil, fmt.Sprintf("--command=CREATE DATABASE %q TEMPLATE %q", newName, name)); err != nil {
		return nil, fmt.Errorf("%v (close other apps connected to %s and try again)", err, name)
	}
	return []string{"Copied " + name + " to " + newName}, nil
}

// PGEmpty removes every table, view and function in the public schema (after a backup).
func (e *Engine) PGEmpty(name string) ([]string, error) {
	if err := e.pgCheck(name, true); err != nil {
		return nil, err
	}
	before, err := e.pgBackup(name, "before emptying")
	if err != nil {
		return nil, err
	}
	if _, err := e.pgRun(name, nil, "--command=DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		return nil, err
	}
	return []string{"Emptied " + name, "Backup made first: " + filepath.Base(before)}, nil
}

// PGDrop deletes a database after a backup.
func (e *Engine) PGDrop(name string) ([]string, error) {
	if err := e.pgCheck(name, true); err != nil {
		return nil, err
	}
	before, err := e.pgBackup(name, "before deleting")
	if err != nil {
		return nil, err
	}
	e.Data.CloseConn("localsites-pg")
	if _, err := e.pgRun("postgres", nil, fmt.Sprintf("--command=DROP DATABASE %q WITH (FORCE)", name)); err != nil {
		return nil, err
	}
	return []string{"Deleted " + name, "A backup is kept: " + before}, nil
}
