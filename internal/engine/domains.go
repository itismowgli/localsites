package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
)

// Setting up .test domains for the built-in front door (Macs without Laravel Valet): macOS must send
// .test lookups to LocalSites' DNS (/etc/resolver/test) and trust the LocalSites certificate authority,
// which can only sign .test names. Both need the Mac password once; ports 80 and 443 need nothing
// (see frontdoor.listenLocal).

const caName = "LocalSites Development CA"

var validTLD = regexp.MustCompile(`^[a-z]{2,20}$`)

// domainsState is what the domains step depends on, so tests can build it without touching the system.
type domainsState struct {
	need     bool // the built-in front door serves the domains (no Valet) on a supported system
	resolver bool // the system sends .<tld> lookups to LocalSites
	trusted  bool // browsers trust the LocalSites CA
	tld      string
	dnsPort  int
	caFile   string
}

// domainsStep is the system change of the domains item.
type domainsStep struct {
	resolver bool // write /etc/resolver/<tld>
	trust    bool // trust the CA
	tld      string
	dnsPort  int
	caFile   string
}

// resolverFile is where macOS looks for a per-domain DNS server (tests change it).
var resolverDir = "/etc/resolver"

func resolverContent(port int) string {
	return fmt.Sprintf("# Added by LocalSites: send .test lookups to its DNS server\nnameserver 127.0.0.1\nport %d\n", port)
}

// resolverOK reports whether /etc/resolver/<tld> points at LocalSites' DNS port.
func resolverOK(tld string, port int) bool {
	b, err := os.ReadFile(filepath.Join(resolverDir, tld))
	if err != nil {
		return false
	}
	ns, p := false, 53
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "nameserver" && f[1] == "127.0.0.1" {
			ns = true
		}
		if len(f) == 2 && f[0] == "port" {
			p, _ = strconv.Atoi(f[1])
		}
	}
	return ns && p == port
}

// caTrusted asks macOS whether the CA is trusted, system-wide (-d) or for this user (replaced in tests).
var caTrusted = func(name string) bool {
	if goruntime.GOOS != "darwin" {
		return false
	}
	for _, args := range [][]string{{"dump-trust-settings", "-d"}, {"dump-trust-settings"}} {
		out, _ := exec.Command("/usr/bin/security", args...).CombinedOutput()
		if strings.Contains(string(out), name) {
			return true
		}
	}
	return false
}

func (e *Engine) domainsState() domainsState {
	s := e.Store.Get().Settings
	d := domainsState{tld: s.TLD, dnsPort: s.DNSPort}
	if e.CA != nil {
		d.caFile = e.CA.RootPath()
	}
	if goruntime.GOOS != "darwin" || e.Front == nil || e.CA == nil || e.Front.Name() != "builtin" || !validTLD.MatchString(s.TLD) {
		return d
	}
	d.need = true
	d.resolver = resolverOK(s.TLD, s.DNSPort)
	d.trusted = caTrusted(caName)
	return d
}

// domainsItem is the Setup item, or nothing when everything is in place.
func domainsItem(d domainsState) (planItem, bool) {
	if !d.need || (d.resolver && d.trusted) {
		return planItem{}, false
	}
	var why []string
	st := domainsStep{resolver: !d.resolver, trust: !d.trusted, tld: d.tld, dnsPort: d.dnsPort, caFile: d.caFile}
	var labels []string
	if !d.resolver {
		why = append(why, "."+d.tld+" addresses don’t open yet")
		labels = append(labels, fmt.Sprintf("send .%s addresses to LocalSites (writes %s/%s)", d.tld, resolverDir, d.tld))
	}
	if !d.trusted {
		why = append(why, "browsers would warn “Your connection is not private”")
		labels = append(labels, "trust the LocalSites certificate (it can only be used for ."+d.tld+" addresses)")
	}
	it := planItem{SetupItem: SetupItem{ID: "domains", Title: "." + d.tld + " domains and HTTPS", Why: strings.Join(why, "; "),
		Kind: "system", Required: true, Password: true}}
	it.steps = []step{{label: strings.Join(labels, ", then "), domains: &st}}
	return it, true
}

// setupDomains makes the system changes with one macOS password prompt (replaced in tests).
var setupDomains = func(ctx context.Context, st domainsStep) error {
	if goruntime.GOOS != "darwin" {
		return errors.New("setting up domains isn't supported on this system yet")
	}
	if !validTLD.MatchString(st.tld) || st.dnsPort < 1024 || st.dnsPort > 65535 {
		return errors.New("invalid domain settings")
	}
	tmp, err := os.CreateTemp("", "localsites-resolver-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(resolverContent(st.dnsPort)); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	_ = os.Chmod(tmp.Name(), 0o644)
	// The script builds one shell command from quoted arguments; nothing from outside is pasted in unquoted.
	script := `on run argv
	set resolverFile to item 1 of argv
	set tmpFile to item 2 of argv
	set caFile to item 3 of argv
	set doResolver to item 4 of argv
	set doTrust to item 5 of argv
	set cmd to "true"
	if doResolver is "1" then set cmd to cmd & " && /bin/mkdir -p /etc/resolver && /usr/bin/install -m 0644 " & quoted form of tmpFile & " " & quoted form of resolverFile & " && (/usr/bin/killall -HUP mDNSResponder || true)"
	if doTrust is "1" then set cmd to cmd & " && (/usr/bin/security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain " & quoted form of caFile & " && echo TRUSTED || echo TRUST-FAILED)"
	do shell script cmd with prompt "LocalSites needs your password once to set up .test addresses and HTTPS for your projects." with administrator privileges
end run`
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	args := []string{"-e", script, filepath.Join("/etc/resolver", st.tld), tmp.Name(), st.caFile, flag(st.resolver), flag(st.trust)}
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", args...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "-128") {
			return errors.New("the password prompt was cancelled")
		}
		return fmt.Errorf("couldn't set up domains: %s", strings.TrimSpace(string(out)))
	}
	if st.trust && !strings.Contains(string(out), "TRUSTED") {
		// Some macOS versions only allow trust changes that the person confirms in their own dialog:
		// trust it for this user instead (macOS asks for the password itself).
		home, _ := os.UserHomeDir()
		kc := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
		if o, err := exec.CommandContext(ctx, "/usr/bin/security", "add-trusted-cert", "-r", "trustRoot", "-k", kc, st.caFile).CombinedOutput(); err != nil {
			return fmt.Errorf("the .test addresses work, but the certificate isn't trusted yet: %s", strings.TrimSpace(string(o)))
		}
	}
	return nil
}
