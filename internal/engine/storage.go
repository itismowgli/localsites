package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/addons"
)

// StorageItem is one kind of data LocalSites keeps, with its size and the clean-up it offers.
type StorageItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Detail   string `json:"detail"`
	Bytes    int64  `json:"bytes"`
	Files    int    `json:"files"`
	Clean    string `json:"clean,omitempty"`     // what the clean-up does, in words ("" = nothing to clean)
	CanClean bool   `json:"can_clean,omitempty"` // false when the clean-up would remove nothing or isn't safe now
}

const (
	keepBackups      = 10
	keepBackupDays   = 30
	keepEnvBackups   = 3
	keepShareLogDays = 7
)

func dirSize(root string) (int64, int) {
	var n int64
	var files int
	_ = filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
			files++
		}
		return nil
	})
	return n, files
}

// Storage lists what uses disk space.
func (e *Engine) Storage() []StorageItem {
	var out []StorageItem
	mysqlBytes, mysqlFiles := dirSize(e.P.MySQLData)
	out = append(out, StorageItem{ID: "mysql", Name: "LocalSites MySQL", Detail: "Your databases and MySQL's own files. Delete databases in Databases to free space.", Bytes: mysqlBytes, Files: mysqlFiles})

	for _, a := range addons.Catalog {
		dir := filepath.Join(e.P.Data, "addons", a.ID)
		b, f := dirSize(dir)
		if b == 0 {
			continue
		}
		v, _ := e.addon(a.ID)
		item := StorageItem{ID: "addon:" + a.ID, Name: a.Name + " data", Detail: "Stored by the " + a.Name + " add-on.", Bytes: b, Files: f,
			Clean: "Delete all " + a.Name + " data (switch the add-on off first)", CanClean: !v.Running}
		out = append(out, item)
	}

	old := e.oldBackups()
	bb, bf := dirSize(e.P.Backups)
	out = append(out, StorageItem{ID: "backups", Name: "Database backups", Detail: fmt.Sprintf("Compressed copies of databases. Keeps the newest %d and anything from the last %d days.", keepBackups, keepBackupDays),
		Bytes: bb, Files: bf, Clean: fmt.Sprintf("Remove %d older backup(s)", len(old)), CanClean: len(old) > 0})

	lb, lf := dirSize(e.P.Logs)
	rot := e.oldLogs()
	out = append(out, StorageItem{ID: "logs", Name: "Logs", Detail: "Apache, PHP, MySQL, project and share logs. Logs over 10 MB are rotated automatically.",
		Bytes: lb, Files: lf, Clean: fmt.Sprintf("Remove %d rotated or old share log(s)", len(rot)), CanClean: len(rot) > 0})

	envs := e.oldEnvBackups()
	var eb int64
	for _, f := range envs {
		if st, err := os.Stat(f); err == nil {
			eb += st.Size()
		}
	}
	out = append(out, StorageItem{ID: "env", Name: ".env backups", Detail: fmt.Sprintf("Made before LocalSites changes a project's .env. They contain passwords, so only the newest %d per project are kept.", keepEnvBackups),
		Bytes: eb, Files: len(envs), Clean: fmt.Sprintf("Remove %d older .env backup(s)", len(envs)), CanClean: len(envs) > 0})
	return out
}

// oldBackups are backups beyond the newest keepBackups that are also older than keepBackupDays.
func (e *Engine) oldBackups() []string {
	files, _ := filepath.Glob(filepath.Join(e.P.Backups, "*.sql.gz")) // not the .note files beside them
	type f struct {
		path string
		mod  time.Time
	}
	var list []f
	for _, p := range files {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			list = append(list, f{p, st.ModTime()})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mod.After(list[j].mod) })
	var out []string
	for i, x := range list {
		if i >= keepBackups && time.Since(x.mod) > keepBackupDays*24*time.Hour {
			out = append(out, x.path)
		}
	}
	return out
}

// oldLogs are rotated copies (*.1) and share logs older than a week.
func (e *Engine) oldLogs() []string {
	var out []string
	for _, dir := range []string{e.P.Logs, e.P.SiteLogs} {
		m, _ := filepath.Glob(filepath.Join(dir, "*.1"))
		out = append(out, m...)
	}
	shares, _ := filepath.Glob(filepath.Join(e.P.Logs, "share-*.log"))
	for _, p := range shares {
		if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) > keepShareLogDays*24*time.Hour {
			out = append(out, p)
		}
	}
	return out
}

// oldEnvBackups are .env backups beyond the newest keepEnvBackups of each project.
func (e *Engine) oldEnvBackups() []string {
	var out []string
	for _, s := range e.Sites() {
		m := envBackups(s.Path, e.envBackupDir(s.Folder))
		slices.Reverse(m) // newest first
		if len(m) > keepEnvBackups {
			out = append(out, m[keepEnvBackups:]...)
		}
	}
	return out
}

// CleanStorage runs one clean-up. Only files LocalSites itself created are removed.
func (e *Engine) CleanStorage(id string) (string, error) {
	var files []string
	switch {
	case id == "backups":
		files = e.oldBackups()
	case id == "logs":
		files = e.oldLogs()
	case id == "env":
		files = e.oldEnvBackups()
	case strings.HasPrefix(id, "addon:"):
		a, ok := addons.Find(strings.TrimPrefix(id, "addon:"))
		if !ok {
			return "", errors.New("unknown add-on")
		}
		if v, _ := e.addon(a.ID); v.Running {
			return "", errors.New("switch " + a.Name + " off first")
		}
		dir := filepath.Join(e.P.Data, "addons", a.ID)
		b, _ := dirSize(dir)
		if err := os.RemoveAll(dir); err != nil {
			return "", err
		}
		return fmt.Sprintf("Deleted %s data (%s)", a.Name, human(b)), nil
	default:
		return "", errors.New("nothing to clean for " + id)
	}
	var freed int64
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			if os.Remove(f) == nil {
				freed += st.Size()
				_ = os.Remove(f + ".note") // why a backup was made
			}
		}
	}
	return fmt.Sprintf("Removed %d file(s), freed %s", len(files), human(freed)), nil
}

func human(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(b)/(1<<10))
	}
	return fmt.Sprintf("%d B", b)
}
