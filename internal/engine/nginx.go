package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/localsites/localsites/internal/proc"
	"github.com/localsites/localsites/internal/runtime"
	"github.com/localsites/localsites/internal/services"
)

const procNginx = "nginx"

// nginxPort is the saved internal port, or the default when it is free.
func (e *Engine) nginxPort() int {
	if p := e.Store.Get().Settings.NginxPort; p != 0 {
		return p
	}
	return 8746
}

// applyNginx writes the nginx config for projects that chose nginx and starts, reloads or stops it.
// Returns the port nginx listens on (0 when it isn't running).
func (e *Engine) applyNginx(sites []SiteView, add func(string, ...any)) int {
	if len(sites) == 0 {
		if e.Sup.Status(procNginx).Running {
			e.Sup.Stop(procNginx, 5*time.Second)
			add("Stopped Nginx")
		}
		return 0
	}
	bin, mime, fastcgi, ok := runtime.Nginx()
	if !ok {
		add("Nginx is not installed (brew install nginx); %d project(s) need it", len(sites))
		return 0
	}
	port := e.nginxPort()
	for _, d := range []string{"client", "proxy", "fastcgi", "uwsgi", "scgi"} {
		_ = os.MkdirAll(filepath.Join(e.P.Run, "nginx", d), 0o700)
	}
	c := services.NginxConfig{Port: port, RunDir: e.P.Run, LogDir: e.P.Logs, MimeTypes: mime, FastCGI: fastcgi}
	for _, s := range sites {
		c.Sites = append(c.Sites, services.NginxSite{Domain: s.Domain, Docroot: filepath.Join(s.Path, s.Docroot), FPMSock: e.fpmSock(s.Folder),
			LogBase: filepath.Join(e.P.SiteLogs, fileKey(s.Folder))})
	}
	conf := filepath.Join(e.P.Generated, "nginx.conf")
	errLog := filepath.Join(e.P.Logs, "nginx-error.log")
	if err := os.WriteFile(conf, []byte(c.Render()), 0o600); err != nil {
		add("Nginx: %v", err)
		return 0
	}
	if out, err := exec.Command(bin, "-t", "-c", conf, "-e", errLog).CombinedOutput(); err != nil {
		add("Nginx config error: %s", lastLine(string(out)))
		return 0
	}
	if e.Sup.Status(procNginx).Running {
		_ = e.Sup.Signal(procNginx, proc.SigReloadNginx)
		add("Reloaded Nginx")
		return port
	}
	if !portFree(port) {
		add("Nginx: port %d is used by another program. Change it in Settings → Domains & ports", port)
		return 0
	}
	if err := e.Sup.Start(proc.Spec{Name: procNginx, Cmd: bin, Args: []string{"-c", conf, "-e", errLog}, Log: filepath.Join(e.P.Logs, "nginx-stdout.log")}); err != nil {
		add("Nginx failed to start: %v", err)
		return 0
	}
	add("Started Nginx")
	return port
}
