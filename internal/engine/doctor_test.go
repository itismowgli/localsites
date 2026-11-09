package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMAMPFixAdvice(t *testing.T) {
	down := mampFix(socketCheck("Services", "MAMP PRO MySQL", filepath.Join(t.TempDir(), "no.sock")))
	if down.Status != "fail" || !strings.Contains(down.Fix, "MAMP PRO") || strings.Contains(down.Fix, "Restart services") {
		t.Errorf("MAMP MySQL down: got %q", down.Fix)
	}
	if ok := mampFix(Check{Status: "ok"}); ok.Fix != "" {
		t.Errorf("a passing check should have no fix, got %q", ok.Fix)
	}
}
