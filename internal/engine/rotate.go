package engine

import (
	"io"
	"os"
	"path/filepath"
)

// Log rotation: a log over RotateAt is copied to name.1 (the newest KeepBytes of it) and emptied in place.
// Emptying in place (not renaming) keeps working for Apache, PHP-FPM and dev servers that hold the file open
// in append mode. One older copy is kept.
const (
	RotateAt  = 10 << 20
	KeepBytes = 5 << 20
)

// RotateLogs checks every LocalSites log once; returns the files it rotated.
func (e *Engine) RotateLogs() []string {
	var files []string
	for _, dir := range []string{e.P.Logs, e.P.SiteLogs} {
		for _, pat := range []string{"*.log", "*.jsonl"} {
			m, _ := filepath.Glob(filepath.Join(dir, pat))
			files = append(files, m...)
		}
	}
	var done []string
	for _, f := range files {
		if ok, _ := rotateFile(f, RotateAt, KeepBytes); ok {
			done = append(done, f)
		}
	}
	return done
}

func rotateFile(path string, limit, keep int64) (bool, error) {
	st, err := os.Stat(path)
	if err != nil || st.Size() <= limit || !st.Mode().IsRegular() {
		return false, err
	}
	src, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer src.Close()
	if _, err := src.Seek(-keep, io.SeekEnd); err != nil {
		return false, err
	}
	dst, err := os.OpenFile(path+".1", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return false, err
	}
	_, err = io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	return true, os.Truncate(path, 0)
}
