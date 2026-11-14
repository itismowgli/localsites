package engine

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/config"
)

// EnvInfo describes the database settings found in a project's .env (never the password).
type EnvInfo struct {
	File     string `json:"file"`
	Style    string `json:"style"` // laravel | classic
	Database string `json:"database,omitempty"`
	User     string `json:"user,omitempty"`
	Host     string `json:"host,omitempty"`
	Backups  int    `json:"backups"` // .env backups made by LocalSites (undo is possible when > 0)
}

// Key names used by common PHP projects.
var envKeys = map[string]map[string]string{
	"laravel": {"database": "DB_DATABASE", "user": "DB_USERNAME", "password": "DB_PASSWORD", "host": "DB_HOST", "port": "DB_PORT"},
	"classic": {"database": "DB_NAME", "user": "DB_USER", "password": "DB_PASS", "host": "DB_HOST"},
}

const envBackupPrefix = ".env.localsites-backup-"

func readEnv(file string) ([]string, map[string]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var lines []string
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		lines = append(lines, line)
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(t, "export "), "=")
		if ok {
			vals[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return lines, vals, sc.Err()
}

// projectEnv reads database settings from <project>/.env, if any.
func projectEnv(path, backupDir string) *EnvInfo {
	file := filepath.Join(path, ".env")
	_, vals, err := readEnv(file)
	if err != nil {
		return nil
	}
	info := &EnvInfo{File: file}
	for _, style := range []string{"laravel", "classic"} {
		if v, ok := vals[envKeys[style]["database"]]; ok {
			info.Style, info.Database = style, v
			info.User, info.Host = vals[envKeys[style]["user"]], vals[envKeys[style]["host"]]
			break
		}
	}
	if info.Style == "" {
		return nil
	}
	info.Backups = len(envBackups(path, backupDir))
	return info
}

// envBackupDir holds a project's .env backups, outside the project (they contain its passwords, and
// a project folder may be served, shared or committed).
func (e *Engine) envBackupDir(folder string) string {
	return filepath.Join(e.P.Backups, "env", fileKey(folder))
}

// envBackups lists a project's .env backups, oldest first: LocalSites' own folder, plus regular files
// left in the project by versions before 1.2 (never links, which could point anywhere).
func envBackups(path, backupDir string) []string {
	var out []string
	own, _ := filepath.Glob(filepath.Join(backupDir, envBackupPrefix+"*"))
	legacy, _ := filepath.Glob(filepath.Join(path, envBackupPrefix+"*"))
	for _, f := range append(legacy, own...) {
		if st, err := os.Lstat(f); err == nil && st.Mode().IsRegular() {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return filepath.Base(out[i]) < filepath.Base(out[j]) })
	return out
}

// ConnectDatabase points a project at a database in LocalSites MySQL: backs up .env, writes the
// database's own login into it, and switches the project's MySQL server to LocalSites.
func (e *Engine) ConnectDatabase(folder, database string) ([]string, error) {
	s, err := e.site(folder)
	if err != nil {
		return nil, err
	}
	creds, err := e.DatabaseCredentials(database)
	if err != nil {
		return nil, fmt.Errorf("%s is not a LocalSites database with its own login; copy or create it first", database)
	}
	info := projectEnv(s.Path, e.envBackupDir(folder))
	if info == nil {
		return nil, errors.New("this project has no .env with database settings; copy the login from Databases instead")
	}
	lines, _, err := readEnv(info.File)
	if err != nil {
		return nil, err
	}
	keys := envKeys[info.Style]
	want := map[string]string{keys["database"]: creds.Database, keys["user"]: creds.User, keys["password"]: creds.Password}
	if info.Style == "laravel" {
		want[keys["host"]], want[keys["port"]] = "127.0.0.1", fmt.Sprint(creds.Port)
	} else {
		want[keys["host"]] = "localhost" // the project's PHP pool sends "localhost" to LocalSites MySQL
	}

	if st, err := os.Lstat(info.File); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is a link or not a regular file, so LocalSites won't change it", filepath.Base(info.File))
	}
	dir := e.envBackupDir(folder)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	backup := filepath.Join(dir, envBackupPrefix+time.Now().Format("20060102-150405"))
	orig, err := os.ReadFile(info.File)
	if err != nil {
		return nil, err
	}
	bf, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	_, werr := bf.Write(orig)
	if cerr := bf.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, werr
	}
	done := map[string]bool{}
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		k, _, ok := strings.Cut(strings.TrimPrefix(t, "export "), "=")
		k = strings.TrimSpace(k)
		if v, mine := want[k]; ok && mine {
			lines[i] = k + "=" + v
			done[k] = true
		}
	}
	var changed []string
	for k, v := range want {
		if !done[k] {
			lines = append(lines, k+"="+v)
		}
		changed = append(changed, k)
	}
	sort.Strings(changed)
	if err := writeFileAtomic(info.File, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return nil, err
	}
	srv, db := config.DBLocalSites, database
	if err := e.UpdateSite(folder, SitePatch{DBServer: &srv, Database: &db}); err != nil {
		return nil, err
	}
	log := []string{fmt.Sprintf("%s now uses LocalSites MySQL database %s (login %s)", folder, database, creds.User),
		"Updated " + strings.Join(changed, ", ") + " in .env; the old file is saved as " + filepath.Base(backup)}
	return append(log, e.Apply()...), nil
}

// RestoreEnv undoes the last ConnectDatabase: restores the newest .env backup and switches the
// project back to MAMP's MySQL.
func (e *Engine) RestoreEnv(folder string) ([]string, error) {
	s, err := e.site(folder)
	if err != nil {
		return nil, err
	}
	backups := envBackups(s.Path, e.envBackupDir(folder))
	if len(backups) == 0 {
		return nil, errors.New("no .env backup made by LocalSites")
	}
	last := backups[len(backups)-1]
	b, err := os.ReadFile(last)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(s.Path, ".env"), b); err != nil {
		return nil, err
	}
	if err := os.Remove(last); err != nil {
		return nil, err
	}
	srv := config.DBMAMP
	if err := e.UpdateSite(folder, SitePatch{DBServer: &srv}); err != nil {
		return nil, err
	}
	return append([]string{folder + ": restored .env from " + filepath.Base(last) + " and switched back to MAMP's MySQL"}, e.Apply()...), nil
}

func (e *Engine) site(folder string) (SiteView, error) {
	for _, s := range e.Sites() {
		if s.Folder == folder {
			return s, nil
		}
	}
	return SiteView{}, fmt.Errorf("no project folder named %q", folder)
}

// writeFileAtomic replaces file through a new temporary file with an unpredictable name (O_EXCL), so a
// link planted in the project can't redirect the write. It refuses when file itself is a link.
func writeFileAtomic(file string, data []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Lstat(file); err == nil {
		if !st.Mode().IsRegular() {
			return fmt.Errorf("%s is a link or not a regular file, so LocalSites won't change it", filepath.Base(file))
		}
		mode = st.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".*.localsites-tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, file)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
