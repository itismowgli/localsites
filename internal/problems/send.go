package problems

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DSN is where reports go (a Sentry project's client key). Empty: this build can't send reports,
// only copy them. LOCALSITES_REPORT_DSN overrides it (tests).
var DSN = ""

// Report is exactly what is sent: shown to the person before they agree.
type Report struct {
	Kind    string            `json:"kind"`
	Title   string            `json:"title"`
	Version string            `json:"version"`
	System  map[string]string `json:"system"` // macOS version, chip, LocalSites version: nothing else
	Detail  string            `json:"detail"` // cleaned
	Count   int               `json:"count"`
	First   time.Time         `json:"first"`
	Bundle  bool              `json:"bundle,omitempty"` // a cleaned debug bundle is attached
}

// Text is the report as plain text (for review and for copying into a GitHub issue).
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "LocalSites %s problem report\n\n", r.Version)
	fmt.Fprintf(&b, "What: %s (%s)\n", r.Title, r.Kind)
	fmt.Fprintf(&b, "Seen: %d time(s) since %s\n", r.Count, r.First.UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "System: macOS %s, %s\n", r.System["macos"], r.System["arch"])
	if r.Detail != "" {
		fmt.Fprintf(&b, "\nDetails:\n%s\n", r.Detail)
	}
	if r.Bundle {
		b.WriteString("\nAttached: debug bundle (logs and settings, cleaned the same way).\n")
	}
	return b.String()
}

var (
	osOnce sync.Once
	osVer  string
)

// System is the little that describes the Mac: macOS version and processor type.
func System(version string) map[string]string {
	osOnce.Do(func() {
		out, _ := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
		osVer = strings.TrimSpace(string(out))
	})
	return map[string]string{"localsites": version, "macos": osVer, "arch": runtime.GOARCH, "os": runtime.GOOS}
}

// Build makes the cleaned report for a problem.
func Build(p Problem, c Cleaner) Report {
	return Report{Kind: p.Kind, Title: c.Clean(p.Title), Version: p.Version, System: System(p.Version), Detail: c.Clean(p.Detail), Count: p.Count, First: p.Time}
}

type dsn struct {
	endpoint, key, raw string
}

func parseDSN(s string) (dsn, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.User == nil || u.Host == "" {
		return dsn{}, errors.New("the report address isn't valid")
	}
	path := strings.Trim(u.Path, "/")
	i := strings.LastIndex(path, "/")
	prefix, project := "", path
	if i >= 0 {
		prefix, project = "/"+path[:i], path[i+1:]
	}
	if project == "" {
		return dsn{}, errors.New("the report address has no project")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) {
		return dsn{}, errors.New("reports are only sent over https")
	}
	return dsn{endpoint: fmt.Sprintf("%s://%s%s/api/%s/envelope/", u.Scheme, u.Host, prefix, project), key: u.User.Username(), raw: s}, nil
}

var digits = regexp.MustCompile(`\d+`)

// ErrBlocked: the network sends the report server's name nowhere (Pi-hole, AdGuard, NextDNS and similar
// answer 0.0.0.0), so the report would reach a server on this Mac instead.
var ErrBlocked = errors.New("your network blocks the report server")

// LookupHost is replaceable in tests.
var LookupHost = net.LookupHost

func hostOf(endpoint string) string {
	u, _ := url.Parse(endpoint)
	return u.Hostname()
}

// reachable refuses to send when the server's name resolves only to "nowhere" or this Mac.
func reachable(endpoint string) error {
	host := hostOf(endpoint)
	if host == "127.0.0.1" || host == "localhost" { // tests
		return nil
	}
	addrs, err := LookupHost(host)
	if err != nil {
		return fmt.Errorf("couldn't find %s (%v). Check the internet connection, or copy the report instead", host, err)
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("%w: %s points to %s, which is how DNS blockers (Pi-hole, AdGuard, NextDNS) stop it. Allow *.sentry.io in the blocker, or copy the report instead", ErrBlocked, host, strings.Join(addrs, ", "))
}

// Send delivers a report and returns the event id. bundle, when given, is attached (a cleaned zip).
func Send(dsnStr string, r Report, bundle []byte) (string, error) {
	d, err := parseDSN(dsnStr)
	if err != nil {
		return "", err
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	level := "error"
	if r.Kind == KindCrash {
		level = "fatal"
	}
	event := map[string]any{
		"event_id":    id,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"platform":    "other",
		"level":       level,
		"logger":      "localsites." + r.Kind,
		"release":     "localsites@" + r.Version,
		"environment": "production",
		"message":     map[string]string{"formatted": r.Title},
		"exception":   map[string]any{"values": []map[string]string{{"type": strings.ToUpper(r.Kind[:1]) + r.Kind[1:], "value": r.Title}}},
		"tags":        map[string]string{"kind": r.Kind, "macos": r.System["macos"], "arch": r.System["arch"]},
		"contexts":    map[string]any{"os": map[string]string{"name": "macOS", "version": r.System["macos"]}},
		"extra":       map[string]any{"count": r.Count, "first_seen": r.First.UTC().Format(time.RFC3339)},
		// the same problem groups together even when ports, process ids or line numbers differ
		"fingerprint": []string{r.Kind, digits.ReplaceAllString(r.Title, "N")},
	}
	ev, _ := json.Marshal(event)
	var env bytes.Buffer
	head, _ := json.Marshal(map[string]string{"event_id": id, "sent_at": time.Now().UTC().Format(time.RFC3339), "dsn": d.raw})
	env.Write(head)
	env.WriteByte('\n')
	item := func(h map[string]any, body []byte) {
		h["length"] = len(body)
		hb, _ := json.Marshal(h)
		env.Write(hb)
		env.WriteByte('\n')
		env.Write(body)
		env.WriteByte('\n')
	}
	item(map[string]any{"type": "event"}, ev)
	item(map[string]any{"type": "attachment", "filename": "report.txt", "content_type": "text/plain"}, []byte(r.Text()))
	if len(bundle) > 0 {
		item(map[string]any{"type": "attachment", "filename": "debug-bundle.zip", "content_type": "application/zip"}, bundle)
	}
	if err := reachable(d.endpoint); err != nil {
		return "", err
	}
	req, _ := http.NewRequest("POST", d.endpoint, &env)
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("X-Sentry-Auth", fmt.Sprintf("Sentry sentry_version=7, sentry_key=%s, sentry_client=localsites/%s", d.key, r.Version))
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		var cert *tls.CertificateVerificationError
		if errors.As(err, &cert) {
			return "", fmt.Errorf("%w (the answer came from a different server, usually a DNS or ad blocker). Allow %s, or copy the report instead", ErrBlocked, hostOf(d.endpoint))
		}
		return "", fmt.Errorf("couldn't send the report: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode != 200 {
		return "", fmt.Errorf("the report server answered %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	return id, nil
}
