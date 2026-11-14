// Package sqlfile reads and writes SQL dump files (.sql, .sql.gz, and a .zip holding one of them) and
// keeps an import inside the database the person chose.
package sqlfile

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Open returns the SQL in file, decompressed. Close the result when done.
func Open(file string) (io.ReadCloser, error) {
	lower := strings.ToLower(file)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		zr, err := zip.OpenReader(file)
		if err != nil {
			return nil, fmt.Errorf("%s isn't a readable zip: %v", filepath.Base(file), err)
		}
		for _, f := range zr.File {
			n := strings.ToLower(f.Name)
			if strings.HasPrefix(filepath.Base(n), ".") || strings.Contains(n, "__macosx/") {
				continue
			}
			if strings.HasSuffix(n, ".sql") || strings.HasSuffix(n, ".sql.gz") || strings.HasSuffix(n, ".dump") {
				rc, err := f.Open()
				if err != nil {
					zr.Close()
					return nil, err
				}
				var r io.ReadCloser = rc
				if strings.HasSuffix(n, ".gz") {
					gz, err := gzip.NewReader(rc)
					if err != nil {
						zr.Close()
						return nil, err
					}
					r = gz
				}
				return multiCloser{r, rc, zr}, nil
			}
		}
		zr.Close()
		return nil, errors.New("the zip has no .sql file")
	case strings.HasSuffix(lower, ".gz"):
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("%s isn't a readable .gz file", filepath.Base(file))
		}
		return multiCloser{gz, gz, f}, nil
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	return f, nil
}

type multiCloser struct {
	io.Reader
	a, b io.Closer
}

func (m multiCloser) Close() error {
	_ = m.a.Close()
	return m.b.Close()
}

// IsSQLFile reports whether the name looks like something Open can read.
func IsSQLFile(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".sql") || strings.HasSuffix(n, ".sql.gz") || strings.HasSuffix(n, ".gz") || strings.HasSuffix(n, ".zip") || strings.HasSuffix(n, ".dump")
}

// Create writes to file, gzip-compressed when the name ends in .gz. The file only appears (atomically)
// when Commit is called; Abort removes the partial file.
func Create(file string) (*Writer, error) {
	if !filepath.IsAbs(file) {
		return nil, errors.New("give a full path for the file")
	}
	// a new, unpredictable name (O_EXCL): a planted link can't redirect the write
	f, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".*.partial")
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, file: file, tmp: f.Name()}
	w.w = bufio.NewWriterSize(f, 1<<20)
	if strings.HasSuffix(strings.ToLower(file), ".gz") {
		w.gz = gzip.NewWriter(w.w)
	}
	return w, nil
}

// Writer is a dump file being written.
type Writer struct {
	NoReplace bool // Commit fails instead of replacing a file that appeared meanwhile
	f         *os.File
	w         *bufio.Writer
	gz        *gzip.Writer
	file, tmp string
	n         int64
}

func (w *Writer) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	if w.gz != nil {
		return w.gz.Write(p)
	}
	return w.w.Write(p)
}

// Commit finishes the file and moves it into place.
func (w *Writer) Commit() (int64, error) {
	if w.gz != nil {
		if err := w.gz.Close(); err != nil {
			w.Abort()
			return 0, err
		}
	}
	if err := w.w.Flush(); err != nil {
		w.Abort()
		return 0, err
	}
	if err := w.f.Close(); err != nil {
		_ = os.Remove(w.tmp)
		return 0, err
	}
	st, _ := os.Stat(w.tmp)
	if w.NoReplace {
		err := os.Link(w.tmp, w.file) // fails if the name exists, unlike rename
		_ = os.Remove(w.tmp)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return 0, fmt.Errorf("%s already exists", filepath.Base(w.file))
			}
			return 0, err
		}
	} else if err := os.Rename(w.tmp, w.file); err != nil {
		_ = os.Remove(w.tmp)
		return 0, err
	}
	if st != nil {
		return st.Size(), nil
	}
	return w.n, nil
}

// Abort removes the partial file.
func (w *Writer) Abort() {
	_ = w.f.Close()
	_ = os.Remove(w.tmp)
}

// statements that would switch to (or create) another database: mysqldump --databases, phpMyAdmin
// exports, psql's \connect. Also psql's \restrict lines (the import sets its own, see ForPsql).
var switchDB = regexp.MustCompile(`(?i)^\s*(/\*!\d*\s*)?(CREATE\s+DATABASE\b|CREATE\s+SCHEMA\b|USE\s+[` + "`" + `"\w]|DROP\s+DATABASE\b)|^\s*(\\c(onnect)?\s|\\connect\b|\\(un)?restrict\b)`)

// KeepInDatabase removes top-level lines that create, drop or switch databases, so a dump made with
// "--databases" lands in the database the person picked. It is a convenience, not a security
// boundary: imports run with a login that can only reach that database (MySQL) or with psql's
// meta-commands off and no superuser rights (PostgreSQL). The rest streams through unchanged, however
// long a line is (one INSERT can be many megabytes).
func KeepInDatabase(r io.Reader) io.Reader { return filter(r, switchDB.Match, nil) }

// lines that need rights a database's own login doesn't have (mysqldump with GTIDs or binary logs)
var mysqlPriv = regexp.MustCompile(`(?i)^\s*(/\*!\d*\s*)?SET\s+@@(GLOBAL|SESSION)\.(GTID_PURGED|SQL_LOG_BIN)\b`)

var definer = regexp.MustCompile("(?i)DEFINER\\s*=\\s*(`[^`]*`|'[^']*'|\"[^\"]*\"|[^\\s@]+)\\s*@\\s*(`[^`]*`|'[^']*'|\"[^\"]*\"|[A-Za-z0-9_.%-]+)\\s*")

// ForMySQL prepares a dump for a database's own login: KeepInDatabase, plus no DEFINER clauses (views,
// triggers and routines then belong to that login) and no GTID / binary log settings.
func ForMySQL(r io.Reader) io.Reader {
	return filter(r, func(head []byte) bool { return switchDB.Match(head) || mysqlPriv.Match(head) }, func(line []byte) []byte {
		h := bytes.ToUpper(bytes.TrimLeft(line[:min(len(line), 24)], " \t"))
		if bytes.HasPrefix(h, []byte("CREATE")) || bytes.HasPrefix(h, []byte("/*!")) {
			return definer.ReplaceAll(line, nil)
		}
		return line
	})
}

// ForPsql turns psql's meta-commands (\! shell, \o |program, \copy … program, \connect …) off for the
// whole file: psql 17.6+ refuses every backslash command after "\restrict <key>" except
// "\unrestrict <key>", and the key is random, so the file can't contain it.
func ForPsql(r io.Reader, key string) io.Reader {
	return io.MultiReader(strings.NewReader("\\restrict "+key+"\n"), KeepInDatabase(r))
}

// filter streams r line by line. drop sees the start of each line (up to 256 bytes); rewrite, when
// given, may change lines that fit in the buffer (long INSERT lines go through untouched).
func filter(r io.Reader, drop func(head []byte) bool, rewrite func(line []byte) []byte) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		br := bufio.NewReaderSize(r, 1<<20)
		bw := bufio.NewWriterSize(pw, 1<<20)
		start := true
		for {
			line, err := br.ReadSlice('\n')
			if start && len(line) > 0 && drop(bytes.TrimLeft(line[:min(len(line), 256)], " \t")) {
				// drop the whole line
				for errors.Is(err, bufio.ErrBufferFull) {
					line, err = br.ReadSlice('\n')
				}
				start = true
			} else if len(line) > 0 {
				if start && rewrite != nil && !errors.Is(err, bufio.ErrBufferFull) {
					line = rewrite(line)
				}
				if _, werr := bw.Write(line); werr != nil {
					pw.CloseWithError(werr)
					return
				}
				start = !errors.Is(err, bufio.ErrBufferFull)
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				if ferr := bw.Flush(); err == nil {
					err = ferr
				}
				pw.CloseWithError(err)
				return
			}
		}
	}()
	return pr
}
