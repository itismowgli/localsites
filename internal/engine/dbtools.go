package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/services"
	"github.com/localsites/localsites/internal/sqlfile"
)

// Everyday database tools for LocalSites MySQL. Anything that removes or replaces data makes a
// backup first (Backups lists them, Restore puts one back).

// BackupFile is one saved backup.
type BackupFile struct {
	File  string    `json:"file"`
	Name  string    `json:"name"`
	Time  time.Time `json:"time"`
	Bytes int64     `json:"bytes"`
	Note  string    `json:"note,omitempty"` // why it was made ("before import", …)
}

// ExportResult says what was written.
type ExportResult struct {
	File  string   `json:"file"`
	Bytes int64    `json:"bytes"`
	Log   []string `json:"log"`
}

func (e *Engine) checkDB(name string) error {
	if !config.ValidDatabaseName(name) {
		return errors.New("database names may use letters, digits and _ only")
	}
	list, err := e.Databases()
	if err != nil {
		return err
	}
	for _, d := range list {
		if d.Name == name {
			return nil
		}
	}
	return fmt.Errorf("there is no database named %s in LocalSites MySQL", name)
}

// exportPath checks a file chosen for an export: a full path under the home folder (or a temp
// folder), a .sql or .sql.gz name, and no overwriting unless asked.
func (e *Engine) exportPath(file string, overwrite bool) error {
	file = filepath.Clean(file)
	if !filepath.IsAbs(file) {
		return errors.New("give a full path for the file")
	}
	low := strings.ToLower(file)
	if !strings.HasSuffix(low, ".sql") && !strings.HasSuffix(low, ".sql.gz") {
		return errors.New("the file name must end in .sql or .sql.gz")
	}
	home := e.P.Home
	if home != "" && !strings.HasPrefix(file, home+string(os.PathSeparator)) && !strings.HasPrefix(file, os.TempDir()) && !strings.HasPrefix(file, "/tmp/") && !strings.HasPrefix(file, "/private/") {
		return errors.New("save exports inside your home folder")
	}
	if st, err := os.Stat(filepath.Dir(file)); err != nil || !st.IsDir() {
		return errors.New("that folder doesn't exist")
	}
	if _, err := os.Stat(file); err == nil && !overwrite {
		return fmt.Errorf("%s already exists", filepath.Base(file))
	}
	return nil
}

// ExportDatabase writes one database to file (.sql or .sql.gz). mode: full, structure or data.
func (e *Engine) ExportDatabase(name, file, mode string, overwrite bool) (ExportResult, error) {
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "structure" && mode != "data" {
		return ExportResult{}, errors.New("mode must be full, structure or data")
	}
	if err := e.checkDB(name); err != nil {
		return ExportResult{}, err
	}
	if err := e.exportPath(file, overwrite); err != nil {
		return ExportResult{}, err
	}
	_, m, pw, err := e.admin()
	if err != nil {
		return ExportResult{}, err
	}
	w, err := sqlfile.Create(file)
	if err != nil {
		return ExportResult{}, err
	}
	if err := m.Dump(pw, name, mode, w); err != nil {
		w.Abort()
		return ExportResult{}, err
	}
	n, err := w.Commit()
	if err != nil {
		return ExportResult{}, err
	}
	what := map[string]string{"full": "structure and data", "structure": "structure only", "data": "data only"}[mode]
	return ExportResult{File: file, Bytes: n, Log: []string{fmt.Sprintf("Exported %s (%s) to %s, %s", name, what, file, human(n))}}, nil
}

// backupNote makes a backup before a risky change and remembers why.
func (e *Engine) backupNote(name, note string) (string, error) {
	f, err := e.Backup(name)
	if err != nil {
		return "", fmt.Errorf("couldn't make a backup first, so nothing was changed: %v", err)
	}
	_ = os.WriteFile(f+".note", []byte(note), 0o600)
	return f, nil
}

// ImportFile loads a .sql, .sql.gz or .zip file into an existing database, after a backup. Lines that
// would create or switch to another database are skipped, so everything lands in this one.
func (e *Engine) ImportFile(name, file string) ([]string, error) {
	if err := e.checkDB(name); err != nil {
		return nil, err
	}
	if !sqlfile.IsSQLFile(file) {
		return nil, errors.New("choose a .sql, .sql.gz or .zip file")
	}
	r, err := sqlfile.Open(file)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	before, err := e.backupNote(name, "before importing "+filepath.Base(file))
	if err != nil {
		return nil, err
	}
	_, m, pw, err := e.admin()
	if err != nil {
		return nil, err
	}
	start := time.Now()
	if err := m.Import(pw, sqlfile.KeepInDatabase(r), name); err != nil {
		return []string{"Backup made first: " + before}, fmt.Errorf("%v. The database may be partly imported; Restore puts back the backup made just before", err)
	}
	return []string{
		fmt.Sprintf("Imported %s into %s in %s", filepath.Base(file), name, time.Since(start).Round(100*time.Millisecond)),
		"Backup made first: " + filepath.Base(before),
	}, nil
}

// Backups lists saved backups of a database, newest first.
func (e *Engine) Backups(name string) ([]BackupFile, error) {
	if !config.ValidDatabaseName(name) {
		return nil, errors.New("invalid database name")
	}
	files, _ := filepath.Glob(filepath.Join(e.P.Backups, name+"-*.sql.gz"))
	out := []BackupFile{}
	for _, f := range files {
		rest := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), name+"-"), ".sql.gz")
		if len(rest) > 15 && rest[15] == '-' { // "-2": a second backup in the same second
			rest = rest[:15]
		}
		t, err := time.ParseInLocation("20060102-150405", rest, time.Local)
		if err != nil {
			continue // another database whose name starts the same way
		}
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		note, _ := os.ReadFile(f + ".note")
		out = append(out, BackupFile{File: f, Name: filepath.Base(f), Time: t, Bytes: st.Size(), Note: string(note)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// RestoreBackup replaces a database with one of its backups, after backing up what is there now.
func (e *Engine) RestoreBackup(name, file string) ([]string, error) {
	if err := e.checkDB(name); err != nil {
		return nil, err
	}
	list, _ := e.Backups(name)
	var chosen *BackupFile
	for i := range list {
		if list[i].File == filepath.Clean(file) || list[i].Name == filepath.Base(file) {
			chosen = &list[i]
		}
	}
	if chosen == nil {
		return nil, errors.New("that backup isn't in the list for this database")
	}
	before, err := e.backupNote(name, "before restoring "+chosen.Name)
	if err != nil {
		return nil, err
	}
	if err := e.recreate(name); err != nil {
		return nil, err
	}
	r, err := sqlfile.Open(chosen.File)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	_, m, pw, err := e.admin()
	if err != nil {
		return nil, err
	}
	if err := m.Import(pw, sqlfile.KeepInDatabase(r), name); err != nil {
		return nil, fmt.Errorf("%v. The state from just before is in %s", err, filepath.Base(before))
	}
	return []string{"Restored " + name + " from " + chosen.Time.Format("2 Jan 15:04"), "What was there before is kept as " + filepath.Base(before)}, nil
}

// recreate drops and creates a database again, empty. Its login keeps its rights (they are per database name).
func (e *Engine) recreate(name string) error {
	db, _, _, err := e.admin()
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(fmt.Sprintf("DROP DATABASE `%s`", name)); err != nil {
		return err
	}
	_, err = db.Exec(fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci", name))
	return err
}

// EmptyDatabase removes every table (after a backup); the database and its login stay.
func (e *Engine) EmptyDatabase(name string) ([]string, error) {
	if err := e.checkDB(name); err != nil {
		return nil, err
	}
	before, err := e.backupNote(name, "before emptying")
	if err != nil {
		return nil, err
	}
	if err := e.recreate(name); err != nil {
		return nil, err
	}
	return []string{"Emptied " + name, "Backup made first: " + filepath.Base(before)}, nil
}

// DuplicateDatabase copies a database (structure and data) into a new one with its own login.
func (e *Engine) DuplicateDatabase(name, newName string) ([]string, error) {
	if err := e.checkDB(name); err != nil {
		return nil, err
	}
	if !config.ValidDatabaseName(newName) {
		return nil, errors.New("database names may use letters, digits and _ only")
	}
	if e.checkDB(newName) == nil {
		return nil, fmt.Errorf("%s already exists", newName)
	}
	tmp := filepath.Join(e.P.Backups, ".copy-"+name+"-"+fmt.Sprint(time.Now().UnixNano())+".sql")
	defer os.Remove(tmp)
	if _, err := e.ExportDatabase(name, tmp, "full", false); err != nil {
		return nil, err
	}
	if _, err := e.CreateDatabase(newName); err != nil {
		return nil, err
	}
	r, err := sqlfile.Open(tmp)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	_, m, pw, err := e.admin()
	if err != nil {
		return nil, err
	}
	if err := m.Import(pw, sqlfile.KeepInDatabase(r), newName); err != nil {
		return nil, err
	}
	return []string{fmt.Sprintf("Copied %s to %s, with its own login %s", name, newName, services.UserFor(newName))}, nil
}

// DropDatabase deletes a database and its login, after a backup (Backups can still restore it into a new database).
func (e *Engine) DropDatabase(name string) ([]string, error) {
	if err := e.checkDB(name); err != nil {
		return nil, err
	}
	before, err := e.backupNote(name, "before deleting")
	if err != nil {
		return nil, err
	}
	db, _, _, err := e.admin()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.Exec(fmt.Sprintf("DROP DATABASE `%s`", name)); err != nil {
		return nil, err
	}
	user := services.UserFor(name)
	for _, host := range []string{"localhost", "127.0.0.1"} {
		_, _ = db.Exec(fmt.Sprintf("DROP USER IF EXISTS '%s'@'%s'", user, host))
	}
	_ = e.Secrets.Delete("db-" + name)
	return []string{"Deleted " + name + " and its login", "A backup is kept: " + before}, nil
}
