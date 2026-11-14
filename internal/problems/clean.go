package problems

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// Cleaner removes personal and secret data from report text before it is shown for review or sent.
type Cleaner struct {
	Home      string   // the person's home folder: becomes ~
	User      string   // their account name: becomes "user"
	Projects  []string // project folder names: become project-<hash> unless KeepNames
	KeepNames bool
}

var (
	// key = value / key: value where the key looks secret
	secretPair = regexp.MustCompile(`(?i)\b([A-Z0-9_.-]*(?:password|passwd|pwd|secret|token|key|auth|credential|dsn|salt|cookie|session)[A-Z0-9_.-]*)(\s*[=:]\s*|"\s*:\s*")("[^"]*"|'[^']*'|[^\s,;&"']+)`)
	// Authorization: Bearer xyz
	bearer = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	// scheme://user:pass@host
	urlCreds = regexp.MustCompile(`([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`)
	// password in a query string
	queryPw = regexp.MustCompile(`(?i)([?&](?:password|pass|pwd|token|key|secret)=)[^&\s]+`)
	// long random-looking strings (API keys, session ids): 32+ chars of base64/hex
	longToken = regexp.MustCompile(`\b[A-Za-z0-9_+/=-]{32,}\b`)
	email     = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	ipv4      = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`)
)

// Clean returns s with secrets removed, the home folder and user name replaced, e-mail addresses
// and IP addresses (except 127.0.0.1) removed, and project names hashed.
func (c Cleaner) Clean(s string) string {
	s = urlCreds.ReplaceAllString(s, "${1}[removed]@")
	s = bearer.ReplaceAllString(s, "$1 [removed]") // before secretPair, which would take "Bearer" as the value
	s = secretPair.ReplaceAllString(s, "$1$2[removed]")
	s = queryPw.ReplaceAllString(s, "${1}[removed]")
	s = email.ReplaceAllString(s, "[email]")
	s = ipv4.ReplaceAllStringFunc(s, func(ip string) string {
		if ip == "127.0.0.1" || ip == "0.0.0.0" {
			return ip
		}
		return "[ip]"
	})
	s = longToken.ReplaceAllStringFunc(s, func(t string) string {
		if strings.Count(t, "/") > 2 || strings.HasPrefix(t, "github.com") { // a path, not a token
			return t
		}
		return "[removed]"
	})
	if c.Home != "" && len(c.Home) > 1 {
		s = strings.ReplaceAll(s, c.Home, "~")
	}
	if c.User != "" && len(c.User) > 2 {
		s = regexp.MustCompile(`\b`+regexp.QuoteMeta(c.User)+`\b`).ReplaceAllString(s, "user")
	}
	if !c.KeepNames {
		names := append([]string(nil), c.Projects...)
		sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) }) // longest first
		for _, n := range names {
			if len(n) < 3 {
				continue
			}
			s = regexp.MustCompile(`\b`+regexp.QuoteMeta(n)+`\b`).ReplaceAllString(s, Hash(n))
		}
	}
	return s
}

// Hash is the stable stand-in for a project name ("project-3fa2c1").
func Hash(name string) string {
	h := sha256.Sum256([]byte("localsites:" + name))
	return "project-" + hex.EncodeToString(h[:3])
}
