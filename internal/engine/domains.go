package engine

import (
	"bytes"
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

// Setting up .test domains for the built-in front door (no Laravel Valet). Three things, done once with
// one password prompt:
//
//	                 macOS                                   Linux
//	.test names      /etc/resolver/<tld>                     systemd-resolved drop-in (resolved.conf.d)
//	certificate      System keychain (or the login keychain) system store + Chrome/Firefox NSS stores
//	ports 80, 443    nothing needed (frontdoor.listenLocal)  net.ipv4.ip_unprivileged_port_start=80
//
// The LocalSites certificate authority can only sign .<tld> names, so trusting it can't be used to fake
// other websites.

const caName = "LocalSites Development CA"

var validTLD = regexp.MustCompile(`^[a-z]{2,20}$`)

// domainsState is what the domains step depends on, so tests can build it without touching the system.
type domainsState struct {
	need     bool // the built-in front door serves the domains (no Valet) on a supported system
	goos     string
	resolver bool // the system sends .<tld> lookups to LocalSites
	trusted  bool // browsers trust the LocalSites CA
	ports    bool // LocalSites may listen on the HTTP and HTTPS ports
	tld      string
	dnsPort  int
	lowPort  int // the lower of the HTTP and HTTPS ports
	caFile   string
}

// domainsStep is the system change of the domains item.
type domainsStep struct {
	goos     string
	resolver bool
	trust    bool
	ports    bool
	tld      string
	dnsPort  int
	lowPort  int
	caFile   string
}

// Where each system keeps the settings (tests change them).
var (
	resolverDir   = "/etc/resolver"                                // macOS
	resolvedDrop  = "/etc/systemd/resolved.conf.d/localsites.conf" // Linux
	caAnchors     = []string{"/usr/local/share/ca-certificates/localsites-ca.crt", "/etc/pki/ca-trust/source/anchors/localsites-ca.crt"}
	portStartFile = "/proc/sys/net/ipv4/ip_unprivileged_port_start"
)

func resolverContent(port int) string {
	return fmt.Sprintf("# Added by LocalSites: send .test lookups to its DNS server\nnameserver 127.0.0.1\nport %d\n", port)
}

func resolvedContent(tld string, port int) string {
	return fmt.Sprintf("# Added by LocalSites: send .%s lookups to its DNS server\n[Resolve]\nDNS=127.0.0.1:%d\nDomains=~%s\n", tld, port, tld)
}

// resolverOK reports whether the system sends .<tld> lookups to LocalSites' DNS port.
func resolverOK(goos, tld string, port int) bool {
	if goos == "linux" {
		b, err := os.ReadFile(resolvedDrop)
		return err == nil && strings.Contains(string(b), fmt.Sprintf("DNS=127.0.0.1:%d\n", port)) && strings.Contains(string(b), "Domains=~"+tld+"\n")
	}
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

// portsOK: on Linux, programs need net.ipv4.ip_unprivileged_port_start at or below the port (macOS: always).
func portsOK(goos string, low int) bool {
	if goos != "linux" || low >= 1024 {
		return true
	}
	b, err := os.ReadFile(portStartFile)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return err == nil && n <= low
}

// caTrusted asks macOS whether the CA is trusted, system-wide or for this user (replaced in tests).
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

// caFileTrusted: on Linux, the CA file is in the system's trust anchors.
func caFileTrusted(caFile string) bool {
	want, err := os.ReadFile(caFile)
	if err != nil {
		return false
	}
	for _, a := range caAnchors {
		if got, err := os.ReadFile(a); err == nil && bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func (e *Engine) domainsState() domainsState {
	s := e.Store.Get().Settings
	d := domainsState{goos: goruntime.GOOS, tld: s.TLD, dnsPort: s.DNSPort, lowPort: min(s.HTTPPort, s.HTTPSPort)}
	if e.CA != nil {
		d.caFile = e.CA.RootPath()
	}
	if (d.goos != "darwin" && d.goos != "linux") || e.Front == nil || e.CA == nil || e.Front.Name() != "builtin" || !validTLD.MatchString(s.TLD) {
		return d
	}
	d.need = true
	d.resolver = resolverOK(d.goos, s.TLD, s.DNSPort)
	d.ports = portsOK(d.goos, d.lowPort)
	if d.goos == "linux" {
		d.trusted = caFileTrusted(d.caFile)
	} else {
		d.trusted = caTrusted(caName)
	}
	return d
}

// domainsItem is the Setup item, or nothing when everything is in place.
func domainsItem(d domainsState) (planItem, bool) {
	if d.goos == "" {
		d.goos = "darwin"
	}
	if !d.need || (d.resolver && d.trusted && (d.ports || d.goos != "linux")) {
		return planItem{}, false
	}
	st := domainsStep{goos: d.goos, resolver: !d.resolver, trust: !d.trusted, ports: d.goos == "linux" && !d.ports,
		tld: d.tld, dnsPort: d.dnsPort, lowPort: d.lowPort, caFile: d.caFile}
	var why, labels []string
	if !d.resolver {
		why = append(why, "."+d.tld+" addresses don’t open yet")
		if d.goos == "linux" {
			labels = append(labels, fmt.Sprintf("send .%s addresses to LocalSites (writes %s)", d.tld, resolvedDrop))
		} else {
			labels = append(labels, fmt.Sprintf("send .%s addresses to LocalSites (writes %s/%s)", d.tld, resolverDir, d.tld))
		}
	}
	if st.ports {
		why = append(why, "LocalSites can’t use the web ports 80 and 443 yet")
		labels = append(labels, "let programs use ports from 80 up (net.ipv4.ip_unprivileged_port_start)")
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

const macDomainsScript = `set -e
if [ "$4" = 1 ]; then
  mkdir -p /etc/resolver
  install -m 0644 "$1" "/etc/resolver/$2"
  killall -HUP mDNSResponder 2>/dev/null || true
fi
if [ "$5" = 1 ]; then
  if security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "$3"; then echo TRUSTED; else echo TRUST-FAILED; fi
fi`

const linuxDomainsScript = `set -e
if [ "$3" = 1 ]; then
  mkdir -p /etc/systemd/resolved.conf.d
  install -m 0644 "$1" /etc/systemd/resolved.conf.d/localsites.conf
  systemctl restart systemd-resolved 2>/dev/null || true
fi
if [ "$4" = 1 ]; then
  if [ -d /usr/local/share/ca-certificates ] && command -v update-ca-certificates >/dev/null 2>&1; then
    install -m 0644 "$2" /usr/local/share/ca-certificates/localsites-ca.crt
    update-ca-certificates >/dev/null
  elif [ -d /etc/pki/ca-trust/source/anchors ]; then
    install -m 0644 "$2" /etc/pki/ca-trust/source/anchors/localsites-ca.crt
    update-ca-trust
  fi
  command -v certutil >/dev/null 2>&1 || (command -v apt-get >/dev/null 2>&1 && apt-get install -y -q libnss3-tools >/dev/null) || (command -v dnf >/dev/null 2>&1 && dnf install -y -q nss-tools >/dev/null) || true
  echo TRUSTED
fi
if [ "$5" = 1 ]; then
  printf '# Added by LocalSites: let programs listen on the web ports 80 and 443\nnet.ipv4.ip_unprivileged_port_start=%s\n' "$6" > /etc/sysctl.d/60-localsites.conf
  sysctl -q -w "net.ipv4.ip_unprivileged_port_start=$6"
fi`

// domainsCommand is the script and its arguments for one system (tested without running it).
func domainsCommand(st domainsStep, tmpFile string) (script string, args []string) {
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	if st.goos == "linux" {
		return linuxDomainsScript, []string{tmpFile, st.caFile, flag(st.resolver), flag(st.trust), flag(st.ports), strconv.Itoa(st.lowPort)}
	}
	return macDomainsScript, []string{tmpFile, st.tld, st.caFile, flag(st.resolver), flag(st.trust)}
}

// setupDomains makes the system changes with one password prompt (replaced in tests).
var setupDomains = func(ctx context.Context, st domainsStep) error {
	if st.goos != goruntime.GOOS || (st.goos != "darwin" && st.goos != "linux") {
		return errors.New("setting up domains isn't supported on this system yet")
	}
	if !validTLD.MatchString(st.tld) || st.dnsPort < 1024 || st.dnsPort > 65535 || st.lowPort < 1 || st.lowPort > 65535 {
		return errors.New("invalid domain settings")
	}
	tmp, err := os.CreateTemp("", "localsites-dns-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	content := resolverContent(st.dnsPort)
	if st.goos == "linux" {
		content = resolvedContent(st.tld, st.dnsPort)
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	_ = os.Chmod(tmp.Name(), 0o644)
	script, args := domainsCommand(st, tmp.Name())
	out, err := runAdmin(ctx, "LocalSites needs your password once to set up ."+st.tld+" addresses and HTTPS for your projects.", script, args...)
	if err != nil {
		return fmt.Errorf("couldn't set up domains: %v", err)
	}
	if st.trust && st.goos == "linux" {
		trustInBrowsers(ctx, st.caFile) // Chrome, Chromium and Firefox keep their own lists (no password needed)
	}
	if st.trust && st.goos == "darwin" && !strings.Contains(out, "TRUSTED") {
		// Some macOS versions only allow trust changes that the person confirms in their own dialog:
		// trust it for this user instead (macOS asks for the password itself).
		home, _ := os.UserHomeDir()
		kc := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
		if o, err := exec.CommandContext(ctx, "/usr/bin/security", "add-trusted-cert", "-r", "trustRoot", "-k", kc, st.caFile).CombinedOutput(); err != nil {
			return fmt.Errorf("the .%s addresses work, but the certificate isn't trusted yet: %s", st.tld, strings.TrimSpace(string(o)))
		}
	}
	return nil
}

// nssDatabases are the person's browser certificate stores: Chrome/Chromium's shared one and each
// Firefox profile (also the Snap and Flatpak ones).
func nssDatabases(home string) []string {
	dbs := []string{filepath.Join(home, ".pki", "nssdb")}
	for _, pattern := range []string{".mozilla/firefox/*", "snap/firefox/common/.mozilla/firefox/*", ".var/app/org.mozilla.firefox/.mozilla/firefox/*"} {
		matches, _ := filepath.Glob(filepath.Join(home, pattern))
		for _, m := range matches {
			if _, err := os.Stat(filepath.Join(m, "cert9.db")); err == nil {
				dbs = append(dbs, m)
			}
		}
	}
	return dbs
}

// trustInBrowsers adds the CA to the browsers' own stores with certutil (from libnss3-tools / nss-tools).
func trustInBrowsers(ctx context.Context, caFile string) {
	certutil, err := exec.LookPath("certutil")
	if err != nil {
		return
	}
	home, _ := os.UserHomeDir()
	for _, db := range nssDatabases(home) {
		if _, err := os.Stat(filepath.Join(db, "cert9.db")); err != nil {
			if err := os.MkdirAll(db, 0o700); err != nil {
				continue
			}
			_ = exec.CommandContext(ctx, certutil, "-N", "--empty-password", "-d", "sql:"+db).Run()
		}
		_ = exec.CommandContext(ctx, certutil, "-d", "sql:"+db, "-A", "-t", "C,,", "-n", caName, "-i", caFile).Run()
	}
}
