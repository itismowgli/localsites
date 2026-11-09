// Package agentcfg connects AI agent apps (Claude Desktop, Claude Code, Cursor, VS Code, Windsurf) to the
// LocalSites MCP server. Each change backs up the app's config file first, refuses files it cannot read
// safely (comments, invalid JSON) and only adds or removes the "localsites" entry; everything else is kept.
package agentcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const entryName = "localsites"

// Client is an agent app LocalSites can connect.
type Client struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Found     bool   `json:"found"`
	Connected bool   `json:"connected"`
	Config    string `json:"config,omitempty"`  // file that holds the setting
	Manual    string `json:"manual,omitempty"`  // snippet to add by hand (Zed)
	Problem   string `json:"problem,omitempty"` // why it can't be changed automatically
}

type spec struct {
	id, name string
	file     func(home string) string // JSON config file
	detect   func(home string) bool
	key      string                                      // object holding servers
	entry    func(exe string) map[string]any             // the server entry
	cli      func(home string) (string, bool)            // Claude Code: use its own command
	manual   func(exe string) string                     // snippet only
	match    func(entry map[string]any, exe string) bool // is our entry up to date
}

func dirExists(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

func stdEntry(exe string) map[string]any { return map[string]any{"command": exe, "args": []any{"mcp"}} }
func sameCommand(e map[string]any, exe string) bool {
	c, _ := e["command"].(string)
	return c == exe
}

var specs = []spec{
	{id: "claude-desktop", name: "Claude Desktop", key: "mcpServers", entry: stdEntry, match: sameCommand,
		file: func(h string) string {
			return filepath.Join(h, "Library", "Application Support", "Claude", "claude_desktop_config.json")
		},
		detect: func(h string) bool { return dirExists(filepath.Join(h, "Library", "Application Support", "Claude")) }},
	{id: "claude-code", name: "Claude Code",
		cli: func(h string) (string, bool) {
			for _, p := range []string{filepath.Join(h, ".local", "bin", "claude"), filepath.Join(h, ".claude", "local", "claude"), "/opt/homebrew/bin/claude", "/usr/local/bin/claude"} {
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					return p, true
				}
			}
			return "", false
		}},
	{id: "cursor", name: "Cursor", key: "mcpServers", entry: stdEntry, match: sameCommand,
		file:   func(h string) string { return filepath.Join(h, ".cursor", "mcp.json") },
		detect: func(h string) bool { return dirExists(filepath.Join(h, ".cursor")) }},
	{id: "vscode", name: "VS Code", key: "servers", match: sameCommand,
		entry: func(exe string) map[string]any {
			return map[string]any{"type": "stdio", "command": exe, "args": []any{"mcp"}}
		},
		file: func(h string) string {
			return filepath.Join(h, "Library", "Application Support", "Code", "User", "mcp.json")
		},
		detect: func(h string) bool {
			return dirExists(filepath.Join(h, "Library", "Application Support", "Code", "User"))
		}},
	{id: "windsurf", name: "Windsurf", key: "mcpServers", entry: stdEntry, match: sameCommand,
		file:   func(h string) string { return filepath.Join(h, ".codeium", "windsurf", "mcp_config.json") },
		detect: func(h string) bool { return dirExists(filepath.Join(h, ".codeium", "windsurf")) }},
	{id: "zed", name: "Zed",
		detect: func(h string) bool { return dirExists(filepath.Join(h, ".config", "zed")) },
		manual: func(exe string) string {
			b, _ := json.MarshalIndent(map[string]any{"context_servers": map[string]any{entryName: map[string]any{"command": map[string]any{"path": exe, "args": []string{"mcp"}}}}}, "", "  ")
			return string(b)
		}},
}

// Home is the user's home folder (tests replace it).
var Home = func() string { h, _ := os.UserHomeDir(); return h }

// List returns every known client and whether LocalSites is connected to it.
func List(exe string) []Client {
	home := Home()
	out := make([]Client, 0, len(specs))
	for _, s := range specs {
		c := Client{ID: s.id, Name: s.name}
		switch {
		case s.cli != nil:
			bin, ok := s.cli(home)
			c.Found = ok
			if ok {
				out, err := exec.Command(bin, "mcp", "get", entryName).CombinedOutput()
				c.Connected = err == nil && bytes.Contains(out, []byte(exe))
			}
		case s.manual != nil:
			c.Found = s.detect(home)
			c.Manual = s.manual(exe)
		default:
			c.Found = s.detect(home)
			c.Config = s.file(home)
			if m, err := readConfig(c.Config); err != nil {
				c.Problem = err.Error()
			} else if servers, ok := m[s.key].(map[string]any); ok {
				if e, ok := servers[entryName].(map[string]any); ok {
					c.Connected = s.match(e, exe)
				}
			}
		}
		out = append(out, c)
	}
	return out
}

func find(id string) (spec, error) {
	for _, s := range specs {
		if s.id == id {
			return s, nil
		}
	}
	return spec{}, fmt.Errorf("unknown agent app %q", id)
}

// readConfig reads a JSON config; a missing file is an empty config.
func readConfig(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s isn't plain JSON (comments?), so it wasn't changed. Add LocalSites by hand", filepath.Base(path))
	}
	return m, nil
}

// Connect adds LocalSites to the client. It returns what was added (for the person to see).
func Connect(id, exe string) (string, error) {
	s, err := find(id)
	if err != nil {
		return "", err
	}
	home := Home()
	switch {
	case s.cli != nil:
		bin, ok := s.cli(home)
		if !ok {
			return "", errors.New(s.name + " is not installed")
		}
		_ = exec.Command(bin, "mcp", "remove", "-s", "user", entryName).Run() // replace an old entry
		if out, err := exec.Command(bin, "mcp", "add", "-s", "user", entryName, "--", exe, "mcp").CombinedOutput(); err != nil {
			return "", fmt.Errorf("claude mcp add failed: %s", strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("claude mcp add -s user %s -- %q mcp", entryName, exe), nil
	case s.manual != nil:
		return "", errors.New(s.name + " settings can contain comments, so LocalSites doesn't edit them. Copy the snippet into its settings")
	}
	if !s.detect(home) {
		return "", errors.New(s.name + " is not installed")
	}
	path := s.file(home)
	m, err := readConfig(path)
	if err != nil {
		return "", err
	}
	if err := backup(path); err != nil {
		return "", err
	}
	servers, _ := m[s.key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	e := s.entry(exe)
	servers[entryName] = e
	m[s.key] = servers
	if err := write(path, m); err != nil {
		return "", err
	}
	added, _ := json.MarshalIndent(map[string]any{s.key: map[string]any{entryName: e}}, "", "  ")
	return string(added), nil
}

// Disconnect removes only the LocalSites entry.
func Disconnect(id string) error {
	s, err := find(id)
	if err != nil {
		return err
	}
	home := Home()
	if s.cli != nil {
		bin, ok := s.cli(home)
		if !ok {
			return errors.New(s.name + " is not installed")
		}
		if out, err := exec.Command(bin, "mcp", "remove", "-s", "user", entryName).CombinedOutput(); err != nil {
			return fmt.Errorf("claude mcp remove failed: %s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	if s.manual != nil {
		return errors.New("remove it from " + s.name + "'s settings by hand")
	}
	path := s.file(home)
	m, err := readConfig(path)
	if err != nil {
		return err
	}
	servers, _ := m[s.key].(map[string]any)
	if _, ok := servers[entryName]; !ok {
		return nil
	}
	if err := backup(path); err != nil {
		return err
	}
	delete(servers, entryName)
	return write(path, m)
}

func backup(path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path+".localsites-backup-"+time.Now().Format("20060102-150405"), b, 0o600)
}

func write(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
