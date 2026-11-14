package engine

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/notify"
	"github.com/localsites/localsites/internal/problems"
	"github.com/localsites/localsites/internal/version"
)

// ProblemView is a problem as the app lists it (details stay on the service until previewed).
type ProblemView struct {
	problems.Problem
	Detail struct{} `json:"detail,omitzero"` // not listed
}

// ReportInfo tells the app whether reports can be sent from this build.
type ReportInfo struct {
	CanSend bool   `json:"can_send"`
	Mode    string `json:"mode"` // off | ask | auto
}

// ProblemStore is the local list of problems (created on first use).
func (e *Engine) ProblemStore() *problems.Store {
	e.problemsOnce.Do(func() {
		e.problems = problems.Open(e.ProblemsDir(), version.Version)
		e.problems.OnAdd = e.problemAdded
	})
	return e.problems
}

// ProblemsDir holds problems and crash traces.
func (e *Engine) ProblemsDir() string { return filepath.Join(e.P.Data, "problems") }

func reportDSN() string {
	if d := os.Getenv("LOCALSITES_REPORT_DSN"); d != "" {
		return d
	}
	return problems.DSN
}

func (e *Engine) reportMode() string {
	if m := e.Store.Get().Settings.ReportMode; m != "" {
		return m
	}
	return "off"
}

var lastProblemNote time.Time

// problemAdded: tell the app, and send or ask as the person chose. Never sends while reports are off.
func (e *Engine) problemAdded(p problems.Problem) {
	e.Changes.Bump()
	if p.Kind != problems.KindCrash && p.Kind != problems.KindUpdate { // services and installs already notify; page errors stay quiet
		if e.reportMode() == "auto" && p.Kind != problems.KindUI && reportDSN() != "" {
			go func() { _, _ = e.sendProblem(p.ID, false, true) }()
		}
		return
	}
	switch e.reportMode() {
	case "auto":
		if reportDSN() != "" {
			go func() { _, _ = e.sendProblem(p.ID, false, true) }()
		}
	case "ask", "off":
		if time.Since(lastProblemNote) > 10*time.Minute {
			lastProblemNote = time.Now()
			notify.Send("LocalSites ran into a problem", p.Title+". Settings → Problems has the details.")
		}
	}
}

// AddProblem records a problem (used by the service's watchers and by the app for page errors).
func (e *Engine) AddProblem(kind, title, detail string) problems.Problem {
	return e.ProblemStore().Add(kind, title, detail)
}

// Problems lists problems, newest first, without their details.
func (e *Engine) Problems() map[string]any {
	list := e.ProblemStore().List()
	out := make([]ProblemView, 0, len(list))
	for _, p := range list {
		out = append(out, ProblemView{Problem: p})
	}
	return map[string]any{"problems": out, "report": ReportInfo{CanSend: reportDSN() != "", Mode: e.reportMode()}}
}

func (e *Engine) cleaner(keepNames bool) problems.Cleaner {
	c := problems.Cleaner{Home: e.P.Home, KeepNames: keepNames}
	if u, err := user.Current(); err == nil {
		c.User = u.Username
	}
	for _, s := range e.Sites() {
		c.Projects = append(c.Projects, s.Folder, s.Name, s.Database, strings.TrimSuffix(s.Domain, "."+e.Store.Get().Settings.TLD))
	}
	if e.Data != nil { // saved connections: their names, hosts, users and databases
		for _, cn := range e.Data.List() {
			if !cn.Builtin {
				for _, v := range []string{cn.Name, cn.Host, cn.User, cn.Database, cn.SSHHost} {
					if v != "root" && v != "postgres" && v != "localhost" && v != "127.0.0.1" {
						c.Projects = append(c.Projects, v)
					}
				}
			}
		}
	}
	return c
}

// PreviewProblem returns exactly what would be sent, as text.
func (e *Engine) PreviewProblem(id string, bundle, keepNames bool) (map[string]any, error) {
	p, err := e.ProblemStore().Get(id)
	if err != nil {
		return nil, err
	}
	c := e.cleaner(keepNames)
	r := problems.Build(p, c)
	r.Bundle = bundle
	out := map[string]any{"text": r.Text(), "can_send": reportDSN() != ""}
	if bundle { // name every file that would go along, so the person sees all of it before agreeing
		names, _ := e.writeBundle(io.Discard, func(s string) string { return s })
		out["bundle_files"] = names
	}
	return out, nil
}

// SendProblem sends the cleaned report, with a cleaned debug bundle when asked.
func (e *Engine) SendProblem(id string, bundle bool) (string, error) {
	return e.sendProblem(id, bundle, false)
}

// sendProblem: auto is a report nobody reviewed (report mode "auto"). It goes without the log lines
// a service problem carries (they can hold database rows or request data), only the error itself.
func (e *Engine) sendProblem(id string, bundle, auto bool) (string, error) {
	d := reportDSN()
	if d == "" {
		return "", errors.New("this build has no report address. Copy the report and open a GitHub issue instead")
	}
	st := e.ProblemStore()
	p, err := st.Get(id)
	if err != nil {
		return "", err
	}
	c := e.cleaner(false)
	if auto {
		p.Detail, _, _ = strings.Cut(p.Detail, "\n\nEnd of ")
	}
	r := problems.Build(p, c)
	var zip []byte
	if bundle {
		var buf bytes.Buffer
		if _, err := e.writeBundle(&buf, func(s string) string { return c.Clean(secretLine.ReplaceAllString(s, "$1$2[removed]")) }); err == nil && buf.Len() < 15<<20 {
			zip, r.Bundle = buf.Bytes(), true
		}
	}
	eid, err := problems.Send(d, r, zip)
	if err != nil {
		return "", err
	}
	st.MarkSent(id, eid)
	e.Changes.Bump()
	return eid, nil
}

// DismissProblem removes one problem, or all when id is "".
func (e *Engine) DismissProblem(id string) {
	e.ProblemStore().Dismiss(id)
	e.Changes.Bump()
}

// LogTail returns the last n lines of a log file.
func LogTail(file string, n int) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > 256<<10 {
		_, _ = f.Seek(st.Size()-256<<10, 0)
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}
