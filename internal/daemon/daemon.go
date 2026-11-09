// Package daemon runs the LocalSites background service and starts it on demand.
package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/localsites/localsites/internal/api"
	"github.com/localsites/localsites/internal/engine"
	"github.com/localsites/localsites/internal/notify"
	"github.com/localsites/localsites/internal/paths"
	"github.com/localsites/localsites/internal/problems"
	"github.com/localsites/localsites/internal/proc"
	"github.com/localsites/localsites/internal/update"
)

// Run starts every service and serves the API until SIGINT/SIGTERM, then stops everything.
func Run() error {
	// a fatal Go crash of this process is written to a file and becomes a problem on the next start
	problems.CaptureCrashes(filepath.Join(paths.Default().Data, "problems"), "service")
	e, err := engine.New()
	if err != nil {
		return err
	}
	logf, _ := os.OpenFile(filepath.Join(e.P.Logs, "localsites.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if logf != nil {
		log.SetOutput(logf)
	}
	srv := &api.Server{E: e, Quit: make(chan struct{})}
	if err := srv.Listen(); err != nil {
		return err
	}
	defer srv.Close() // runs after StopAll below: the socket disappears only once everything has stopped
	log.Printf("LocalSites started (pid %d)", os.Getpid())
	for _, l := range e.Apply() {
		log.Print(l)
	}
	go watchProcesses(e) // tell the person about crashes and finished installs
	e.ProblemStore().Collect(e.P.Home)
	go func() { // LocalSites MySQL sleeps when nothing has needed it for 20 minutes
		for {
			time.Sleep(time.Minute)
			e.ProblemStore().Collect(e.P.Home) // crash traces and macOS crash reports (one directory listing)
			if e.StopIdleMySQL() {
				log.Print("Stopped LocalSites MySQL (idle; it starts again when needed)")
			}
		}
	}()
	go func() { // app updates: a daily check only when the person turned it on
		if r := e.UpdateState().Result; r != "" {
			log.Print(r)
			notify.Send("LocalSites", r)
			if strings.HasPrefix(r, "The last update didn't install") {
				e.AddProblem(problems.KindUpdate, r, "")
			}
		}
		for {
			time.Sleep(time.Minute)
			e.UpdateTick()
			time.Sleep(59 * time.Minute)
		}
	}()
	go func() { // keep logs from growing without limit
		for {
			for _, f := range e.RotateLogs() {
				log.Printf("Rotated %s", f)
			}
			time.Sleep(30 * time.Minute)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-srv.Quit:
	}
	log.Print("Stopping")
	for _, l := range e.StopAll() {
		log.Print(l)
	}
	return nil
}

// serviceDetail is the error and the end of a service's log, for a problem report.
func serviceDetail(e *engine.Engine, name string, st proc.Status) string {
	d := "Last error: " + st.LastErr
	if spec, ok := e.Sup.Spec(name); ok && spec.Log != "" {
		d += "\n\nEnd of " + filepath.Base(spec.Log) + ":\n" + engine.LogTail(spec.Log, 60)
	}
	return d
}

// ApplyUpdate is the update helper (`LocalSites update-apply <plan>`): it swaps the app once the app and
// the service have quit, then starts whatever was running before, from the new app (or the old one if
// the swap failed and was undone).
func ApplyUpdate(planFile string) error {
	p, err := update.Apply(planFile)
	if p.To == "" {
		return err
	}
	if err != nil {
		notify.Send("LocalSites update didn't install", err.Error())
	}
	switch p.Then {
	case "open":
		_ = exec.Command("/usr/bin/open", p.To).Run()
	case "open-bg":
		_ = exec.Command("/usr/bin/open", "-g", p.To, "--args", "--hidden").Run()
	case "service":
		if _, serr := Ensure(filepath.Join(p.To, "Contents", "MacOS", "LocalSites")); serr != nil && err == nil {
			err = serr
		}
	}
	return err
}

// Ensure returns a client, starting the background service if it is not running.
// self is the executable to launch with the "daemon" argument.
func Ensure(self string) (*api.Client, error) {
	p := paths.Default()
	if c, err := api.Dial(p); err == nil {
		return c, nil
	}
	if err := p.Ensure(); err != nil {
		return nil, err
	}
	out, err := os.OpenFile(filepath.Join(p.Logs, "daemon-stdout.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	cmd := exec.Command(self, "daemon")
	cmd.Stdout, cmd.Stderr = out, out
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	_ = cmd.Process.Release()
	deadline := time.Now().Add(90 * time.Second) // first start may initialize MySQL
	for time.Now().Before(deadline) {
		if c, err := api.Dial(p); err == nil {
			return c, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil, fmt.Errorf("LocalSites did not start; see %s", filepath.Join(p.Logs, "daemon-stdout.log"))
}

// watchProcesses sends a notification when a service had to be restarted after a crash, and when a
// background install (dependencies, add-on, tunnel client) finishes.
func watchProcesses(e *engine.Engine) {
	restarts := map[string]int{}
	installing := map[string]bool{}
	gaveUp := map[string]bool{}
	lastCrash := map[string]time.Time{}
	for {
		time.Sleep(10 * time.Second)
		for _, n := range e.Sup.Names() {
			st := e.Sup.Status(n)
			if st.Restarts > restarts[n] && time.Since(lastCrash[n]) > 5*time.Minute {
				lastCrash[n] = time.Now()
				notify.Send("LocalSites", friendlyName(n)+" stopped unexpectedly and was restarted. Logs has the details.")
				e.AddProblem(problems.KindService, friendlyName(n)+" stopped unexpectedly", serviceDetail(e, n, st))
			}
			if strings.Contains(st.LastErr, "gave up") && !gaveUp[n] {
				e.AddProblem(problems.KindService, friendlyName(n)+" keeps crashing and was stopped", serviceDetail(e, n, st))
			}
			gaveUp[n] = strings.Contains(st.LastErr, "gave up")
			restarts[n] = st.Restarts
			if strings.Contains(n, "install") {
				if installing[n] && !st.Running {
					notify.Send("LocalSites", friendlyName(n)+" finished. Open LocalSites to see the result.")
					if st.LastErr != "" && st.LastErr != "finished" {
						e.AddProblem(problems.KindInstall, friendlyName(n)+" failed", serviceDetail(e, n, st))
					}
				}
				installing[n] = st.Running
			}
		}
	}
}

func friendlyName(proc string) string {
	switch {
	case proc == "apache":
		return "Apache"
	case proc == "mysql":
		return "MySQL"
	case proc == "nginx":
		return "Nginx"
	case strings.HasPrefix(proc, "php-"):
		return "PHP " + strings.TrimPrefix(proc, "php-")
	case strings.HasPrefix(proc, "addon-install-"):
		return "Installing " + strings.TrimPrefix(proc, "addon-install-")
	case strings.HasPrefix(proc, "share-install-"):
		return "Installing " + strings.TrimPrefix(proc, "share-install-")
	case strings.HasPrefix(proc, "install-"):
		return "Installing dependencies for " + strings.TrimPrefix(proc, "install-")
	case strings.HasPrefix(proc, "addon-"):
		return strings.TrimPrefix(proc, "addon-")
	case strings.HasPrefix(proc, "app-"):
		return "The dev server of " + strings.TrimPrefix(proc, "app-")
	}
	return proc
}
