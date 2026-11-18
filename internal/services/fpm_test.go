package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localsites/localsites/internal/platform"
)

func TestDecorateOnlyFrom73(t *testing.T) {
	for v, want := range map[string]bool{"5.6": false, "7.1": false, "7.2": false, "7.3": true, "7.4": true, "8.0": true, "8.5": true, "latest": true} {
		if got := strings.Contains(FPMConfig(v, "/e.log", []FPMPool{{Name: "x", Socket: "/s"}}), "decorate_workers_output"); got != want {
			t.Errorf("PHP %s: decorate_workers_output = %v, want %v", v, got, want)
		}
	}
}

// Every PHP-FPM installed here accepts the configuration LocalSites writes for it.
func TestFPMConfigLoadsInInstalledPHP(t *testing.T) {
	dir, err := os.MkdirTemp("", "fpm") // short path: unix sockets are limited to about 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	tested := 0
	for _, prefix := range platform.BrewPrefixes() {
		bins, _ := filepath.Glob(filepath.Join(prefix, "opt", "php@*", "sbin", "php-fpm"))
		for _, bin := range bins {
			v := strings.TrimPrefix(filepath.Base(filepath.Dir(filepath.Dir(bin))), "php@")
			conf := filepath.Join(dir, "php-fpm.conf")
			pool := FPMPool{Name: "site", Socket: filepath.Join(dir, "s.sock"), ErrorLog: filepath.Join(dir, "e.log"), MySQLSocket: filepath.Join(dir, "m.sock"), TmpDir: dir,
				Overrides: map[string]string{"memory_limit": "256M"}, Admin: map[string]string{"sendmail_path": "/usr/sbin/sendmail -t -i"}}
			if err := os.WriteFile(conf, []byte(FPMConfig(v, filepath.Join(dir, "fpm.log"), []FPMPool{pool})), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(bin, "-n", "-t", "-y", conf).CombinedOutput()
			if err != nil {
				if strings.Contains(string(out), "Library not loaded") || strings.Contains(string(out), "error while loading shared libraries") {
					continue // a broken build (Doctor reports those), not a config problem
				}
				t.Errorf("PHP %s rejects the configuration: %s", v, strings.TrimSpace(string(out)))
			}
			tested++
		}
	}
	t.Logf("checked %d PHP-FPM versions", tested)
}
