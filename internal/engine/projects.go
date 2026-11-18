package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/config"
)

// Removing and renaming projects. Projects are folders, so files are only ever moved (renamed, or to the
// Trash), never deleted. A project's database is never touched.

// HiddenSite is a project hidden from LocalSites.
type HiddenSite struct {
	Folder string `json:"folder"`
	Path   string `json:"path,omitempty"` // empty when the folder is gone
}

var validFolderName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,79}$`)

func (e *Engine) siteByFolder(folder string) (SiteView, bool) {
	for _, s := range e.Sites() {
		if s.Folder == folder {
			return s, true
		}
	}
	return SiteView{}, false
}

// stopSite stops what runs for one project (its dev server and share) before its folder changes.
func (e *Engine) stopSite(s SiteView) {
	if e.Sup != nil {
		if e.Sup.Status(appProc(s.Folder)).Running {
			e.Sup.Stop(appProc(s.Folder), 10*time.Second)
		}
	}
	if e.Shares != nil {
		for _, sh := range e.Shares.List() {
			if sh.Folder == s.Folder {
				e.Shares.Stop(sh.ID)
			}
		}
	}
}

// HideSite stops listing and serving a project. The folder and its settings stay, so unhiding brings it back.
func (e *Engine) HideSite(folder string) ([]string, error) {
	s, ok := e.siteByFolder(folder)
	if !ok {
		return nil, errors.New("unknown project")
	}
	e.stopSite(s)
	if err := e.Store.Update(func(c *config.Config) error {
		for _, h := range c.Settings.Hidden {
			if h == folder {
				return nil
			}
		}
		c.Settings.Hidden = append(c.Settings.Hidden, folder)
		return nil
	}); err != nil {
		return nil, err
	}
	e.Apply()
	return []string{"Hid " + s.Name + ". Its folder is untouched: " + s.Path, "Show it again from Settings → Projects folders"}, nil
}

// UnhideSite lists a hidden project again.
func (e *Engine) UnhideSite(folder string) ([]string, error) {
	found := false
	if err := e.Store.Update(func(c *config.Config) error {
		var keep []string
		for _, h := range c.Settings.Hidden {
			if h == folder {
				found = true
				continue
			}
			keep = append(keep, h)
		}
		c.Settings.Hidden = keep
		return nil
	}); err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("that project isn't hidden")
	}
	e.Apply()
	return []string{"Showing " + folder + " again"}, nil
}

// HiddenSites lists hidden projects and where their folders are now.
func (e *Engine) HiddenSites() []HiddenSite {
	cfg := e.Store.Get()
	paths := map[string]string{}
	for _, c := range scanRoots(cfg.Settings.Roots) {
		paths[c.ID] = c.Path
	}
	out := []HiddenSite{}
	for _, h := range cfg.Settings.Hidden {
		out = append(out, HiddenSite{Folder: h, Path: paths[h]})
	}
	return out
}

// RenameSite renames a project's folder (in the same place) and moves its settings, logs and .env backups.
func (e *Engine) RenameSite(folder, newName string) ([]string, error) {
	newName = strings.TrimSpace(newName)
	s, ok := e.siteByFolder(folder)
	switch {
	case !ok:
		return nil, errors.New("unknown project")
	case !validFolderName.MatchString(newName) || skipDirs[newName]:
		return nil, errors.New("use letters, digits, spaces, dots, dashes and underscores (no slashes)")
	case newName == s.Name:
		return nil, errors.New("that is already its name")
	case s.ServedBy != "":
		return nil, errors.New("Laravel Valet serves this project; rename it in Valet’s folder instead")
	case s.Installing:
		return nil, errors.New("wait for the install to finish first")
	}
	if st, err := os.Lstat(s.Path); err != nil {
		return nil, err
	} else if st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("this project folder is a link; rename the folder it points to instead")
	}
	if worktreeOf(s.Path) {
		return nil, errors.New("this folder is a git worktree; rename it with git worktree move so git keeps track of it")
	}
	target := filepath.Join(filepath.Dir(s.Path), newName)
	caseOnly := strings.EqualFold(target, s.Path)
	if _, err := os.Lstat(target); err == nil && !caseOnly {
		return nil, fmt.Errorf("a folder named %s already exists there", newName)
	}
	newID := newName
	if i := strings.LastIndex(folder, "/"); i >= 0 {
		newID = folder[:i+1] + newName
	}
	cfg := e.Store.Get()
	if _, taken := cfg.Sites[newID]; taken && !caseOnly {
		return nil, fmt.Errorf("LocalSites still has settings for an older project named %s; remove them first", newName)
	}

	e.stopSite(s)
	if caseOnly { // "Shop" → "shop" on a case-insensitive disk: go through a temporary name
		tmp := s.Path + ".renaming-" + fmt.Sprint(time.Now().UnixNano())
		if err := os.Rename(s.Path, tmp); err != nil {
			e.Apply()
			return nil, err
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Rename(tmp, s.Path)
			e.Apply()
			return nil, err
		}
	} else if err := os.Rename(s.Path, target); err != nil {
		e.Apply()
		return nil, fmt.Errorf("couldn't rename the folder: %v", err)
	}

	log := []string{fmt.Sprintf("Renamed %s to %s", s.Name, newName)}
	tld := "." + cfg.Settings.TLD
	oldDefault := config.Slug(s.Name) + tld
	newDomain := ""
	_ = e.Store.Update(func(c *config.Config) error {
		if site, ok := c.Sites[folder]; ok {
			delete(c.Sites, folder)
			if site.Domain == oldDefault && s.ManagedBy == "" { // the address followed the old name
				site.Domain = config.Slug(newName) + tld
				newDomain = site.Domain
			}
			c.Sites[newID] = site
		}
		for i, h := range c.Settings.Hidden {
			if h == folder {
				c.Settings.Hidden[i] = newID
			}
		}
		return nil
	})
	// Logs and .env backups are keyed by the project id
	if logs, _ := filepath.Glob(filepath.Join(e.P.SiteLogs, fileKey(folder)+"-*")); len(logs) > 0 {
		for _, f := range logs {
			_ = os.Rename(f, filepath.Join(e.P.SiteLogs, fileKey(newID)+strings.TrimPrefix(filepath.Base(f), fileKey(folder))))
		}
	}
	if old := e.envBackupDir(folder); isDir(old) {
		_ = os.Rename(old, e.envBackupDir(newID))
	}
	if newDomain != "" {
		log = append(log, "Its address is now https://"+newDomain)
		if b, err := os.ReadFile(filepath.Join(target, ".env")); err == nil && strings.Contains(string(b), strings.TrimSuffix(oldDefault, tld)+tld) {
			log = append(log, ".env still mentions "+oldDefault+" (for example APP_URL); change it if the project uses it")
		}
	}
	if s.Database != "" {
		log = append(log, "The database "+s.Database+" keeps its name")
	}
	e.Apply()
	return log, nil
}

// TrashSite moves a project's folder to the Trash. confirm must be the project's folder name.
func (e *Engine) TrashSite(folder, confirm string) ([]string, error) {
	s, ok := e.siteByFolder(folder)
	switch {
	case !ok:
		return nil, errors.New("unknown project")
	case confirm != s.Name:
		return nil, errors.New("type the project’s name to confirm")
	case s.ServedBy != "":
		return nil, errors.New("Laravel Valet serves this project; remove it from Valet first")
	case s.Installing:
		return nil, errors.New("wait for the install to finish first")
	}
	root := ""
	for _, r := range e.Store.Get().Settings.Roots {
		if strings.HasPrefix(s.Path, filepath.Clean(r.Path)+string(os.PathSeparator)) {
			root = r.Path
		}
	}
	if root == "" || filepath.Clean(s.Path) == filepath.Clean(root) {
		return nil, errors.New("only folders inside a projects folder can be moved to the Trash")
	}
	e.stopSite(s)
	if err := moveToTrash(s.Path); err != nil {
		e.Apply()
		return nil, fmt.Errorf("couldn't move it to the Trash: %v", err)
	}
	e.Apply()
	log := []string{"Moved " + s.Name + " to the Trash. Open the Trash to put it back"}
	if s.Database != "" {
		log = append(log, "The database "+s.Database+" is kept; delete it from Databases if you don’t need it (a backup is made first)")
	}
	return log, nil
}

// worktreeOf reports whether dir is a linked git worktree (.git is a file pointing into another repository).
func worktreeOf(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	return err == nil && strings.HasPrefix(string(b), "gitdir:") && strings.Contains(string(b), "/worktrees/")
}
