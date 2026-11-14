package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	notifypkg "github.com/localsites/localsites/internal/notify"
	"sort"
	"strings"
	"sync"
	"time"
)

// Risky calls from AI agents (methods marked Destructive) wait here until the person approves or denies
// them in the app. Only the app may decide. This is a safety net for agents, not a lock against other
// programs: anything running as you can read the service token.

const approvalWait = 90 * time.Second

// Approval is one waiting request.
type Approval struct {
	ID      string    `json:"id"`
	Method  string    `json:"method"`
	Client  string    `json:"client"`
	Summary string    `json:"summary"` // the request's parameters, for the person to read
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
	answer  chan bool
}

type approvals struct {
	mu       sync.Mutex
	pending  map[string]*Approval
	onChange func() // a request arrived or ended
}

func (a *approvals) changed() {
	if a.onChange != nil {
		a.onChange()
	}
}

func (a *approvals) list() []Approval {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Approval, 0, len(a.pending))
	for _, p := range a.pending {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// wait registers a request and blocks until it is decided, times out, or the caller goes away.
func (a *approvals) wait(method, client string, body []byte, done <-chan struct{}) error {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	var params map[string]any
	_ = json.Unmarshal(body, &params)
	maskSecrets(params) // never show secrets in the prompt
	sum, _ := json.Marshal(params)
	p := &Approval{ID: hex.EncodeToString(b), Method: method, Client: client, Summary: string(sum), Created: time.Now(), Expires: time.Now().Add(approvalWait), answer: make(chan bool, 1)}
	a.mu.Lock()
	if a.pending == nil {
		a.pending = map[string]*Approval{}
	}
	a.pending[p.ID] = p
	a.mu.Unlock()
	a.changed()
	defer func() { a.mu.Lock(); delete(a.pending, p.ID); a.mu.Unlock(); a.changed() }()
	notify("An AI agent is waiting for your approval", humanMethod(method)+". Open LocalSites to approve or deny.")
	select {
	case ok := <-p.answer:
		if !ok {
			return errors.New("the user denied this in LocalSites. Don't retry unless they ask you to")
		}
		return nil
	case <-time.After(approvalWait):
		return errors.New("no answer in LocalSites within 90 seconds, so nothing was done. Ask the user to approve it in the app, then try again")
	case <-done:
		return errors.New("request cancelled")
	}
}

func (a *approvals) decide(id string, approve bool) error {
	a.mu.Lock()
	p, ok := a.pending[id]
	delete(a.pending, id) // a second answer to the same request finds nothing
	a.mu.Unlock()
	if !ok {
		return errors.New("that request is no longer waiting")
	}
	select {
	case p.answer <- approve:
	default:
	}
	a.changed()
	return nil
}

// secretKey: parameter names whose values are never shown or logged.
func secretKey(k string) bool {
	k = strings.ToLower(k)
	for _, w := range []string{"password", "secret", "token", "key"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// maskSecrets hides passwords and every environment variable value (often API keys), keeping the names.
func maskSecrets(m map[string]any) {
	for k, v := range m {
		switch {
		case k == "env_vars":
			if env, ok := v.(map[string]any); ok {
				for ek := range env {
					env[ek] = "••••"
				}
			}
		case secretKey(k):
			m[k] = "••••"
		}
	}
}

func humanMethod(m string) string {
	return strings.NewReplacer(".", " ", "_", " ").Replace(m)
}

var notify = notifypkg.Send // replaceable in tests
