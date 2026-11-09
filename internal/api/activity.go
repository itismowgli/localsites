package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Activity is one recorded API call.
type Activity struct {
	Time     time.Time `json:"time"`
	Client   string    `json:"client"` // app | cli | mcp | devui | unknown
	Method   string    `json:"method"`
	Summary  string    `json:"summary"`
	Error    string    `json:"error,omitempty"`
	Duration int64     `json:"ms"`
}

type activityLog struct {
	mu    sync.Mutex
	items []Activity
	file  string
}

const activityMax = 300

var clientRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)

// record keeps every change and every call made by an agent (reads from the app are too noisy).
func (s *Server) record(client, method string, body []byte, err error, took time.Duration) {
	m, known := FindMethod(method)
	if !clientRe.MatchString(client) {
		client = "unknown"
	}
	if known && !m.Mutates && client != "mcp" {
		return
	}
	a := Activity{Time: time.Now(), Client: client, Method: method, Summary: summarize(m, body), Duration: took.Milliseconds()}
	if err != nil {
		a.Error = err.Error()
	}
	s.activity.add(a, filepath.Join(s.E.P.Logs, "activity.jsonl"))
}

// summarize describes the call without secrets (only declared, non-password params are kept).
func summarize(m Method, body []byte) string {
	var in map[string]any
	_ = json.Unmarshal(body, &in)
	out := map[string]any{}
	for _, p := range m.Params {
		if p.Name == "password" {
			continue
		}
		if v, ok := in[p.Name]; ok {
			out[p.Name] = v
		}
	}
	if len(out) == 0 {
		return ""
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (l *activityLog) add(a Activity, file string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, a)
	if len(l.items) > activityMax {
		l.items = l.items[len(l.items)-activityMax:]
	}
	if f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		b, _ := json.Marshal(a)
		f.Write(append(b, '\n'))
		f.Close()
	}
}

func (l *activityLog) list() []Activity {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Activity, 0, len(l.items))
	for i := len(l.items) - 1; i >= 0; i-- {
		out = append(out, l.items[i])
	}
	return out
}
