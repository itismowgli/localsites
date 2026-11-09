package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/localsites/localsites/internal/addons"
	"github.com/localsites/localsites/internal/proc"
	"github.com/localsites/localsites/internal/share"
)

// ProviderView is a tunnel provider with its install state.
type ProviderView struct {
	share.Provider
	Installed  bool   `json:"installed"`
	Installing bool   `json:"installing"`
	Install    string `json:"install_command"`
}

func shareInstallProc(id string) string { return "share-install-" + id }

// ShareProviders lists the tunnel providers.
func (e *Engine) ShareProviders() []ProviderView {
	out := make([]ProviderView, 0, len(share.Providers))
	for _, p := range share.Providers {
		_, ok := p.Binary()
		out = append(out, ProviderView{Provider: p, Installed: ok, Installing: e.Sup.Status(shareInstallProc(p.ID)).Running, Install: "brew install " + p.Formula})
	}
	return out
}

// InstallShareProvider installs a tunnel client with Homebrew in the background.
func (e *Engine) InstallShareProvider(id string) (string, error) {
	p, ok := share.FindProvider(id)
	if !ok {
		return "", fmt.Errorf("no provider named %q", id)
	}
	if _, ok := p.Binary(); ok {
		return "", errors.New(p.Name + " is already installed")
	}
	return e.brewInstall(shareInstallProc(id), p.Formula, filepath.Join(e.P.Logs, "share-"+id+"-install.log"))
}

// brewInstall runs "brew install <formula>" under the supervisor; progress goes to logf.
func (e *Engine) brewInstall(name, formula, logf string) (string, error) {
	brew, ok := addons.Brew()
	if !ok {
		return "", errors.New("Homebrew is not installed. See https://brew.sh")
	}
	if e.Sup.Status(name).Running {
		return "", errors.New("already installing")
	}
	_ = os.Remove(logf)
	spec := proc.Spec{Name: name, Cmd: brew, Args: []string{"install", formula}, OneShot: true, Log: logf, Dir: e.P.Home,
		Env: []string{"PATH=" + filepath.Dir(brew) + ":/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + e.P.Home,
			"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_INSTALL_CLEANUP=1", "HOMEBREW_NO_ENV_HINTS=1", "NONINTERACTIVE=1"}}
	if err := e.Sup.Start(spec); err != nil {
		return "", err
	}
	return logf, nil
}

// StartShare puts a running project on the internet for a limited time.
func (e *Engine) StartShare(folder, provider, password string, minutes int, noPassword bool) (share.Share, error) {
	if password == "" && !noPassword {
		return share.Share{}, errors.New("set a password for visitors, or explicitly choose to share without one")
	}
	s, err := e.site(folder)
	if err != nil {
		return share.Share{}, err
	}
	if minutes <= 0 {
		minutes = 60
	}
	t := share.Target{Domain: s.Domain}
	switch {
	case s.ServedBy == "valet":
		t.Backend = "https://127.0.0.1:443" // Valet's nginx; its own certificate (checks skipped on loopback only)
	case !s.Enabled:
		return share.Share{}, errors.New("switch " + s.Name + " on first")
	case s.Runtime == "app":
		t.Backend = fmt.Sprintf("http://127.0.0.1:%d", s.Port)
	case s.Runtime == "php":
		t.Backend = fmt.Sprintf("https://127.0.0.1:%d", e.Store.Get().Settings.ApachePort)
		t.RootCAs = e.CA.Pool()
	default:
		return share.Share{}, errors.New(s.Name + " is not a website")
	}
	return e.Shares.Start(folder, t, provider, password, time.Duration(minutes)*time.Minute)
}
