package engine

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
)

// moveToTrash moves a folder to the Trash the way the system's file manager does, so it can be put back
// (replaced in tests).
var moveToTrash = func(path string) error {
	if dir := os.Getenv("LOCALSITES_TRASH_DIR"); dir != "" { // test sandboxes: never the person's own Trash
		return trashFreedesktop(path, dir, time.Now())
	}
	switch goruntime.GOOS {
	case "darwin":
		// NSFileManager's trashItemAtURL: the same as Finder's Move to Trash, Put Back included, and no
		// permission prompt to control Finder. The path is an argument, never part of the script.
		script := `function run(argv) {
  ObjC.import('Foundation');
  const err = Ref();
  const ok = $.NSFileManager.defaultManager.trashItemAtURLResultingItemURLError($.NSURL.fileURLWithPath(argv[0]), null, err);
  if (!ok) throw new Error(ObjC.unwrap(err[0].localizedDescription));
  return 'ok';
}`
		out, err := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", script, path).CombinedOutput()
		if err != nil {
			return errors.New(strings.TrimSpace(string(out)))
		}
		return nil
	case "linux":
		home, _ := os.UserHomeDir()
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		return trashFreedesktop(path, filepath.Join(data, "Trash"), time.Now())
	}
	return errors.New("not supported on this system yet")
}

// trashFreedesktop moves path into a freedesktop.org Trash folder (Trash specification 1.0): the item goes
// to files/, and info/<name>.trashinfo records where it came from so the file manager can restore it.
func trashFreedesktop(path, trash string, now time.Time) error {
	path = filepath.Clean(path)
	files, info := filepath.Join(trash, "files"), filepath.Join(trash, "info")
	for _, d := range []string{files, info} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if !sameDevice(path, files) {
		return errors.New("the folder is on another disk than your home folder")
	}
	base := filepath.Base(path)
	for n := 1; n < 1000; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s.%d", base, n)
		}
		// Reserve the name with the info file first (O_EXCL), as the specification asks.
		f, err := os.OpenFile(filepath.Join(info, name+".trashinfo"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		u := (&url.URL{Path: path}).EscapedPath()
		_, werr := fmt.Fprintf(f, "[Trash Info]\nPath=%s\nDeletionDate=%s\n", u, now.Format("2006-01-02T15:04:05"))
		f.Close()
		if werr == nil {
			if _, err := os.Lstat(filepath.Join(files, name)); err == nil {
				os.Remove(filepath.Join(info, name+".trashinfo"))
				continue
			}
			werr = os.Rename(path, filepath.Join(files, name))
		}
		if werr != nil {
			os.Remove(filepath.Join(info, name+".trashinfo"))
			return werr
		}
		return nil
	}
	return errors.New("too many items with this name in the Trash")
}

func sameDevice(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil {
		return false
	}
	da, ok1 := fileDevice(sa)
	db, ok2 := fileDevice(sb)
	if !ok1 || !ok2 {
		return true // no device numbers here: let the rename decide
	}
	return da == db
}
