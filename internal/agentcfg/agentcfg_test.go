package agentcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectKeepsOtherSettings(t *testing.T) {
	home := t.TempDir()
	Home = func() string { return home }
	defer func() { Home = func() string { h, _ := os.UserHomeDir(); return h } }()
	cursor := filepath.Join(home, ".cursor", "mcp.json")
	os.MkdirAll(filepath.Dir(cursor), 0o755)
	os.WriteFile(cursor, []byte(`{"mcpServers":{"github":{"command":"gh-mcp"}},"other":1}`), 0o600)

	added, err := Connect("cursor", "/Apps/LocalSites")
	if err != nil || !strings.Contains(added, `"/Apps/LocalSites"`) {
		t.Fatalf("connect: %v %s", err, added)
	}
	var m map[string]any
	b, _ := os.ReadFile(cursor)
	json.Unmarshal(b, &m)
	servers := m["mcpServers"].(map[string]any)
	if servers["github"] == nil || m["other"] == nil {
		t.Fatalf("other settings lost: %s", b)
	}
	if st, _ := os.Stat(cursor); st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode changed: %v", st.Mode())
	}
	backups, _ := filepath.Glob(cursor + ".localsites-backup-*")
	if len(backups) != 1 {
		t.Fatalf("expected a backup, got %v", backups)
	}
	var found bool
	for _, c := range List("/Apps/LocalSites") {
		if c.ID == "cursor" {
			found = c.Found && c.Connected
		}
	}
	if !found {
		t.Fatal("cursor should show as connected")
	}
	if err := Disconnect("cursor"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cursor)
	if strings.Contains(string(b), "localsites") || !strings.Contains(string(b), "gh-mcp") {
		t.Fatalf("disconnect must remove only our entry: %s", b)
	}

	// VS Code uses "servers" with a type field; a missing file is created
	os.MkdirAll(filepath.Join(home, "Library", "Application Support", "Code", "User"), 0o755)
	if _, err := Connect("vscode", "/x"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(home, "Library", "Application Support", "Code", "User", "mcp.json"))
	if !strings.Contains(string(b), `"servers"`) || !strings.Contains(string(b), `"stdio"`) {
		t.Fatalf("vscode config: %s", b)
	}
}

func TestRefusesFilesWithComments(t *testing.T) {
	home := t.TempDir()
	Home = func() string { return home }
	defer func() { Home = func() string { h, _ := os.UserHomeDir(); return h } }()
	f := filepath.Join(home, ".codeium", "windsurf", "mcp_config.json")
	os.MkdirAll(filepath.Dir(f), 0o755)
	orig := "{\n  // my servers\n  \"mcpServers\": {}\n}"
	os.WriteFile(f, []byte(orig), 0o644)
	if _, err := Connect("windsurf", "/x"); err == nil {
		t.Fatal("file with comments was changed")
	}
	if b, _ := os.ReadFile(f); string(b) != orig {
		t.Fatal("file was modified")
	}
	if _, err := Connect("zed", "/x"); err == nil {
		t.Fatal("zed must be manual")
	}
}
