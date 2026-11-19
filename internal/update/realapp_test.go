package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The app as built (make app) must pass the same checks an update gets before it is installed.
func TestBuiltAppPassesBundleCheck(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the macOS app bundle is checked on macOS")
	}
	app, _ := filepath.Abs(filepath.Join("..", "..", "desktop", "build", "bin", AppName))
	if _, err := os.Stat(app); err != nil {
		t.Skip("app not built")
	}
	// the bundle's own version (it may be an older build than the source): plist, binary and checks must agree
	out, err := exec.Command("/usr/bin/plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", filepath.Join(app, "Contents", "Info.plist")).Output()
	if err != nil {
		t.Fatalf("Info.plist unreadable: %v", err)
	}
	if err := CheckBundle(app, strings.TrimSpace(string(out))); err != nil {
		t.Fatal(err)
	}
}
