package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWPDatabase(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\n// ** Database settings ** //\ndefine( 'DB_NAME', \"shop_wp\" );\ndefine('DB_USER', 'root');\n"), 0o644)
	if got := wpDatabase(dir); got != "shop_wp" {
		t.Fatalf("got %q", got)
	}
	if got := wpDatabase(t.TempDir()); got != "" {
		t.Fatalf("no wp-config.php: got %q", got)
	}
}
