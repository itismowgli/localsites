package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrokenBuildReason(t *testing.T) {
	out := "dyld[123]: Library not loaded: @loader_path/../../../../opt/libxpm/lib/libXpm.4.dylib\n  Referenced from: /opt/homebrew/Cellar/php@7.2/7.2.34_14/bin/php"
	b := brokenBuild("/nowhere/php@7.2", "/opt/homebrew/opt/php@7.2/bin/php", out)
	if b.Version != "7.2" || b.Reason != "libXpm.4.dylib (from libxpm) is missing" || b.Fix != "brew reinstall php@7.2" {
		t.Fatalf("got %+v", b)
	}
	if b := brokenBuild("/opt/homebrew/opt/php@8.1", "x", "Segmentation fault"); b.Reason != "it doesn't start" || b.Fix != "brew reinstall php@8.1" {
		t.Fatalf("unknown failure: %+v", b)
	}
}

func TestExtensionWarning(t *testing.T) {
	out := "Warning: PHP Startup: Unable to load dynamic library '/opt/homebrew/opt/php@7.4/lib/php/20190902/intl.so' (tried: ... Library not loaded: @loader_path/../../../../../../opt/icu4c@77/lib/libicuio.77.dylib"
	if w := extensionWarning(out, "/nowhere/php@7.4"); w != "intl can't load (icu4c@77 is missing). Run: brew reinstall php@7.4" {
		t.Fatalf("got %q", w)
	}
	if w := extensionWarning("Warning: something", "/nowhere/php@8.3"); !strings.Contains(w, "brew reinstall php@8.3") {
		t.Fatalf("got %q", w)
	}
}

func TestReinstallFromTap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "php@7.2")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "INSTALL_RECEIPT.json"), []byte(`{"source":{"tap":"shivammathur/php"}}`), 0o644)
	if got := reinstall(dir); got != "brew trust shivammathur/php && brew reinstall shivammathur/php/php@7.2" {
		t.Fatalf("got %q", got)
	}
	_ = os.WriteFile(filepath.Join(dir, "INSTALL_RECEIPT.json"), []byte(`{"source":{"tap":"homebrew/core"}}`), 0o644)
	if got := reinstall(dir); got != "brew reinstall php@7.2" {
		t.Fatalf("core: got %q", got)
	}
	_ = os.WriteFile(filepath.Join(dir, "INSTALL_RECEIPT.json"), []byte(`{"source":{"tap":"evil; rm -rf ~"}}`), 0o644)
	if got := reinstall(dir); got != "brew reinstall php@7.2" {
		t.Fatalf("odd tap name must be ignored: %q", got)
	}
}

func TestBrokenOnLinux(t *testing.T) {
	out := "/home/linuxbrew/.linuxbrew/opt/php@7.3/bin/php: error while loading shared libraries: libjpeg.so.9: cannot open shared object file: No such file or directory"
	b := brokenBuild("/nowhere/opt/php@7.3", "/nowhere/opt/php@7.3/bin/php", out)
	if b.Version != "7.3" || b.Reason != "libjpeg.so.9 is missing" {
		t.Fatalf("broken = %+v", b)
	}
	warn := "PHP Warning:  PHP Startup: Unable to load dynamic library 'intl' (tried: /x/intl (/x/intl: cannot open shared object file: No such file or directory), /x/intl.so (libicuio.so.77: cannot open shared object file: No such file or directory)) in Unknown on line 0"
	if w := extensionWarning(warn, "/nowhere/opt/php@7.4"); !strings.HasPrefix(w, "intl can't load (libicuio.so.77 is missing). Run: brew reinstall php@7.4") {
		t.Fatalf("warning = %q", w)
	}
}
