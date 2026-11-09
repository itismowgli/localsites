// Package secrets stores passwords in the OS keychain (macOS Keychain),
// falling back to an owner-only file on platforms without one yet.
package secrets

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

const service = "LocalSites"

var (
	ErrNotFound = errors.New("secret not found")
	keyRe       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
)

// Store reads and writes named secrets.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// New returns the best store for this platform. fallbackDir is used when no keychain exists.
func New(fallbackDir string) Store {
	// LOCALSITES_SECRETS=file forces the file store (tests, headless machines)
	if runtime.GOOS == "darwin" && os.Getenv("LOCALSITES_SECRETS") != "file" {
		if _, err := exec.LookPath("/usr/bin/security"); err == nil {
			return keychain{}
		}
	}
	return &fileStore{file: filepath.Join(fallbackDir, "secrets.json")}
}

// Generate returns a random password using letters and digits only (safe in .env files and shells).
func Generate(n int) string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	b := make([]byte, n)
	for i := range b {
		x, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[x.Int64()]
	}
	return string(b)
}

/* ------------------------------------------------------------- macOS Keychain */

type keychain struct{}

func (keychain) Get(key string) (string, error) {
	if !keyRe.MatchString(key) {
		return "", fmt.Errorf("invalid secret name %q", key)
	}
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", service, "-a", key, "-w").Output()
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// Set uses `security -i` and sends the command on stdin, so the value never shows in `ps`.
func (keychain) Set(key, value string) error {
	if !keyRe.MatchString(key) {
		return fmt.Errorf("invalid secret name %q", key)
	}
	if strings.ContainsAny(value, "\"\\\n'") {
		return errors.New("secret contains characters the keychain tool cannot take safely")
	}
	cmd := exec.Command("/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -l \"LocalSites %s\" -w \"%s\"\n", service, key, key, value))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keychain: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (keychain) Delete(key string) error {
	if !keyRe.MatchString(key) {
		return fmt.Errorf("invalid secret name %q", key)
	}
	_ = exec.Command("/usr/bin/security", "delete-generic-password", "-s", service, "-a", key).Run()
	return nil
}

/* ------------------------------------------------------------ file fallback */

type fileStore struct {
	mu   sync.Mutex
	file string
}

func (f *fileStore) load() (map[string]string, error) {
	m := map[string]string{}
	b, err := os.ReadFile(f.file)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

func (f *fileStore) save(m map[string]string) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := f.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.file)
}

func (f *fileStore) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *fileStore) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[key] = value
	return f.save(m)
}

func (f *fileStore) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	delete(m, key)
	return f.save(m)
}
