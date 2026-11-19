package update

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Updates on Linux: LocalSites was installed from a .deb or .rpm, so an update is the newer package of the
// same kind, checked against the signed manifest and installed by the system's package manager.

var (
	formatOnce sync.Once
	format     string
)

// installedFormat is how this copy was installed on Linux: "deb", "rpm", or "" (not from a package, for
// example built from source, which updates itself only from the release page).
var installedFormat = func() string {
	formatOnce.Do(func() {
		if runtime.GOOS != "linux" {
			return
		}
		if _, err := os.Stat("/var/lib/dpkg/info/localsites.list"); err == nil {
			format = "deb"
		} else if err := exec.Command("rpm", "-q", "localsites").Run(); err == nil {
			format = "rpm"
		}
	})
	return format
}

// PackageFile returns the downloaded package for the latest version, checked again (signature, then
// checksum) right before it is installed, and that version.
func (u *Updater) PackageFile() (string, string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.st.Latest == nil || !u.ready(u.st.Latest.Version) {
		return "", "", errors.New("the update isn't downloaded yet")
	}
	v := u.st.Latest.Version
	dir := u.verDir(v)
	raw, _ := os.ReadFile(filepath.Join(dir, "update.json"))
	sig, _ := os.ReadFile(filepath.Join(dir, "update.json.sig"))
	m, err := Verify(raw, sig)
	if err != nil || m.Version != v {
		return "", "", errors.New("the downloaded update no longer matches its signature; download it again")
	}
	f, ok := pick(m.Files)
	if !ok || f.Format == "" {
		return "", "", errors.New("this release has no package for this system")
	}
	file := filepath.Join(dir, "update."+f.Format)
	if hashFile(file) != strings.ToLower(f.SHA256) {
		return "", "", errors.New("the downloaded update was changed on disk; download it again")
	}
	u.st.Phase = Installing
	return file, v, nil
}

// Installed records the result of a package install, shown after the restart ("Updated from 1.4.0 to 1.5.0").
func (u *Updater) Installed(to string, err error) {
	r := Result{From: u.Current, To: to, At: time.Now()}
	if err != nil {
		r.Error = err.Error()
		u.set(func(s *Status) { s.Phase, s.Error = Ready, err.Error() })
	}
	if b, e := jsonMarshal(r); e == nil {
		_ = os.WriteFile(filepath.Join(u.Dir, "result.json"), b, 0o600)
	}
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
