package runtime

import (
	"strings"
	"testing"
)

func TestBrokenBuildReason(t *testing.T) {
	out := "dyld[123]: Library not loaded: @loader_path/../../../../opt/libxpm/lib/libXpm.4.dylib\n  Referenced from: /opt/homebrew/Cellar/php@7.2/7.2.34_14/bin/php"
	b := brokenBuild("/opt/homebrew/opt/php@7.2", "/opt/homebrew/opt/php@7.2/bin/php", out)
	if b.Version != "7.2" || b.Reason != "libXpm.4.dylib is missing" || !strings.HasPrefix(b.Fix, "brew install libxpm") {
		t.Fatalf("got %+v", b)
	}
	if b := brokenBuild("/opt/homebrew/opt/php@8.1", "x", "Segmentation fault"); b.Reason != "it doesn't start" || b.Fix != "brew reinstall php@8.1" {
		t.Fatalf("unknown failure: %+v", b)
	}
}

func TestExtensionWarning(t *testing.T) {
	out := "Warning: PHP Startup: Unable to load dynamic library '/opt/homebrew/opt/php@7.4/lib/php/20190902/intl.so' (tried: ... Library not loaded: @loader_path/../../../../../../opt/icu4c@77/lib/libicuio.77.dylib"
	if w := extensionWarning(out, "php@7.4"); !strings.HasPrefix(w, "intl can't load (icu4c@77 is missing). Run: brew install icu4c@77") {
		t.Fatalf("got %q", w)
	}
	if w := extensionWarning("Warning: something", "php@8.3"); !strings.Contains(w, "brew reinstall php@8.3") {
		t.Fatalf("got %q", w)
	}
}
