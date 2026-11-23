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
	ID     string `json:"id"`
	Method string `json:"method"`
	Client string `json:"client"`
	Agent  string `json:"agent,omitempty"` // the agent app's own name (it can say anything)
	Scope  string `json:"scope,omitempty"` // the project or database it's about
	// Grantable: the person may approve the same request from the same caller for a while.
	Grantable bool   `json:"grantable"`
	Summary   string `json:"summary"` // the request's parameters, for the person to read
	// What the action does and whether it can lose work, from the method list, so the card can say so.
	Description string    `json:"description,omitempty"`
	Destructive bool      `json:"destructive"`
	Created     time.Time `json:"created"`
	Expires     time.Time `json:"expires"`
	answer      chan bool
}

type approvals struct {
	onNotice func(title, text, id string) // show a notification (with Approve / Deny on macOS)
	onDone   func(id string)              // the request isn't waiting anymore
	mu       sync.Mutex
	pending  map[string]*Approval
	grants   []grant
	onChange func() // a request arrived or ended
}

// grant: "approve this for 10 minutes": the same action, from the same kind of caller and agent, on the
// same project or database.
type grant struct {
	id, client, agent, method, scope string
	until                            time.Time
}

// Grant is an active "approve for 10 minutes", as the app shows it.
type Grant struct {
	ID     string    `json:"id"`
	Client string    `json:"client"`
	Agent  string    `json:"agent,omitempty"`
	Method string    `json:"method"`
	Scope  string    `json:"scope,omitempty"`
	Until  time.Time `json:"until"`
}

// activeGrants lists the grants that haven't run out, soonest to end first.
func (a *approvals) activeGrants() []Grant {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []Grant{}
	for _, g := range a.grants {
		if time.Now().Before(g.until) {
			out = append(out, Grant{g.id, g.client, g.agent, g.method, g.scope, g.until})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Until.Before(out[j].Until) })
	return out
}

// revoke ends a grant early: the next request of that kind asks again.
func (a *approvals) revoke(id string) error {
	a.mu.Lock()
	keep := a.grants[:0]
	found := false
	for _, g := range a.grants {
		if g.id == id {
			found = true
			continue
		}
		keep = append(keep, g)
	}
	a.grants = keep
	a.mu.Unlock()
	if !found {
		return errors.New("that approval has already ended")
	}
	a.changed()
	return nil
}

// grantMinutes is how long "approve for a while" lasts.
const grantMinutes = 10

// grantable: everyday actions an agent repeats while you watch. Deleting, installing, publishing and the
// person-only actions are never on this list: they ask every time.
var grantable = map[string]bool{
	"db.import": true, "db.export": true, "pg.import": true, "pg.export": true,
	"data.query": true, "data.save": true, "data.import": true, "data.export": true, "data.readonly": true,
	"sites.connect_db": true, "sites.restore_env": true, "sites.install": true, "sites.update": true,
	"settings.update": true, "restart": true, "stop": true, "addons.flush": true, "share.extend": true,
}

// requestScope: what a request is about, for matching a grant (the project, else the database or item).
func requestScope(params map[string]any) string {
	for _, k := range []string{"folder", "database", "db", "name", "id", "connection"} {
		if v, ok := params[k].(string); ok && v != "" {
			return k + ":" + v
		}
	}
	return ""
}

// agentName keeps an agent's self-reported name short and printable.
func agentName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	return s
}

// granted reports whether an earlier "approve for 10 minutes" covers this request.
func (a *approvals) granted(client, agent, method, scope string) bool {
	if !grantable[method] {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	keep := a.grants[:0]
	ok := false
	for _, g := range a.grants {
		if time.Now().After(g.until) {
			continue
		}
		keep = append(keep, g)
		if g.client == client && g.agent == agent && g.method == method && g.scope == scope {
			ok = true
		}
	}
	a.grants = keep
	return ok
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
func (a *approvals) wait(method, client, agent string, body []byte, done <-chan struct{}) error {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	var params map[string]any
	_ = json.Unmarshal(body, &params)
	scope := requestScope(params)
	if a.granted(client, agent, method, scope) {
		return nil
	}
	maskSecrets(params) // never show secrets in the prompt
	sum, _ := json.Marshal(params)
	m, _ := FindMethod(method)
	p := &Approval{ID: hex.EncodeToString(b), Method: method, Client: client, Agent: agent, Scope: scope, Grantable: grantable[method],
		Summary: string(sum), Description: m.Description, Destructive: m.Destructive, Created: time.Now(), Expires: time.Now().Add(approvalWait), answer: make(chan bool, 1)}
	a.mu.Lock()
	if a.pending == nil {
		a.pending = map[string]*Approval{}
	}
	a.pending[p.ID] = p
	a.mu.Unlock()
	a.changed()
	defer func() {
		a.mu.Lock()
		delete(a.pending, p.ID)
		a.mu.Unlock()
		a.changed()
		if a.onDone != nil {
			a.onDone(p.ID) // answered, timed out or cancelled: its notice is no longer waiting
		}
	}()
	who := requester(client)
	if agent != "" && client == "mcp" {
		who = agent
	}
	title, text := who+" is waiting for your approval", humanMethod(method)+". Approve or deny it here or in LocalSites."
	if a.onNotice != nil {
		a.onNotice(title, text, p.ID)
	} else {
		notify(title, text)
	}
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

func (a *approvals) decide(id string, approve, forAWhile bool) error {
	a.mu.Lock()
	p, ok := a.pending[id]
	if ok && approve && forAWhile && !p.Grantable { // still waiting for a plain answer
		a.mu.Unlock()
		return errors.New(humanMethod(p.Method) + " asks every time")
	}
	delete(a.pending, id) // a second answer to the same request finds nothing
	if ok && approve && forAWhile {
		a.grants = append(a.grants, grant{p.ID, p.Client, p.Agent, p.Method, p.Scope, time.Now().Add(grantMinutes * time.Minute)})
	}
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

// requester names who asked, for the notification: an agent over MCP, or a command run without a
// terminal (a script, or an agent using the shell).
func requester(client string) string {
	switch client {
	case "mcp":
		return "An AI agent"
	case "cli":
		return "A command in the terminal"
	case "unproven-app":
		return "A program calling itself LocalSites"
	}
	return "A script or command"
}

func humanMethod(m string) string {
	return strings.NewReplacer(".", " ", "_", " ").Replace(m)
}

var notify = notifypkg.Send // replaceable in tests
