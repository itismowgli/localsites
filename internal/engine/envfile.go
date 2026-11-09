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
func projectEnv(path string) *EnvInfo {
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
	backups, _ := filepath.Glob(filepath.Join(path, envBackupPrefix+"*"))
	info.Backups = len(backups)
	return info
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
	info := projectEnv(s.Path)
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

	backup := filepath.Join(s.Path, envBackupPrefix+time.Now().Format("20060102-150405"))
	orig, err := os.ReadFile(info.File)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(backup, orig, 0o600); err != nil {
		return nil, err
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
	backups, _ := filepath.Glob(filepath.Join(s.Path, envBackupPrefix+"*"))
	if len(backups) == 0 {
		return nil, errors.New("no .env backup made by LocalSites")
	}
	sort.Strings(backups)
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

func writeFileAtomic(file string, data []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(file); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := file + ".localsites-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
