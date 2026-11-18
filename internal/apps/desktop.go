package apps

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// readDesktop reads the command line of a freedesktop .desktop file (Desktop Entry Specification 1.5).
// The file's own arguments are kept; the place for the file or URL becomes {}. Hidden entries, entries
// whose TryExec program is missing and entries that aren't applications are skipped.
func readDesktop(path string) ([]string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	vals := map[string]string{}
	inEntry := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if !inEntry {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if _, seen := vals[k]; !seen { // localized keys look like Name[de]; only plain keys are used
			vals[k] = strings.TrimSpace(v)
		}
	}
	if t := vals["Type"]; t != "" && t != "Application" {
		return nil, false
	}
	if vals["Hidden"] == "true" || vals["Exec"] == "" {
		return nil, false
	}
	if try := vals["TryExec"]; try != "" && !foundProgram(try) {
		return nil, false
	}
	args, ok := splitExec(unescapeValue(vals["Exec"]))
	if !ok || len(args) == 0 {
		return nil, false
	}
	out, placed := []string{}, false
	for _, a := range args {
		switch a {
		case "%f", "%F", "%u", "%U":
			if !placed {
				out, placed = append(out, "{}"), true
			}
			continue
		case "%i", "%c", "%k", "%d", "%D", "%n", "%N", "%v", "%m":
			continue // icon, name and file location aren't needed to open a folder
		}
		out = append(out, fieldCodes(a))
	}
	if !placed {
		out = append(out, "{}")
	}
	if !filepath.IsAbs(out[0]) {
		p, ok := lookPath(out[0])
		if !ok {
			return nil, false
		}
		out[0] = p
	}
	return out, true
}

// fieldCodes handles field codes inside a longer argument: %% becomes %, the others are dropped.
func fieldCodes(a string) string {
	if !strings.Contains(a, "%") {
		return a
	}
	var b strings.Builder
	for i := 0; i < len(a); i++ {
		if a[i] != '%' || i+1 >= len(a) {
			b.WriteByte(a[i])
			continue
		}
		i++
		if a[i] == '%' {
			b.WriteByte('%')
		}
	}
	return b.String()
}

// unescapeValue undoes the escapes of a string value (\s \n \t \r \\).
func unescapeValue(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' || i+1 >= len(v) {
			b.WriteByte(v[i])
			continue
		}
		i++
		switch v[i] {
		case 's':
			b.WriteByte(' ')
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(v[i])
		}
	}
	return b.String()
}

// splitExec splits an Exec value into arguments: spaces separate them, double quotes group them, and
// inside quotes a backslash escapes " ` $ and \.
func splitExec(s string) ([]string, bool) {
	var args []string
	var cur strings.Builder
	inQuote, have := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote && c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case c == '"':
			inQuote, have = !inQuote, true
		case !inQuote && (c == ' ' || c == '\t'):
			if have {
				args = append(args, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	if inQuote {
		return nil, false
	}
	if have {
		args = append(args, cur.String())
	}
	return args, true
}

func lookPath(name string) (string, bool) {
	if strings.Contains(name, "/") {
		return "", false
	}
	for _, d := range BinDirs() {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		if p := filepath.Join(d, name); isExec(p) {
			return p, true
		}
	}
	return "", false
}

func foundProgram(name string) bool {
	if filepath.IsAbs(name) {
		return isExec(name)
	}
	_, ok := lookPath(name)
	return ok
}
