package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeKeyring struct {
	m    map[string]string
	down bool
}

func (f *fakeKeyring) Get(k string) (string, error) {
	if f.down {
		return "", errors.New("keyring gone")
	}
	v, ok := f.m[k]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}
func (f *fakeKeyring) Set(k, v string) error {
	if f.down {
		return errors.New("keyring gone")
	}
	f.m[k] = v
	return nil
}
func (f *fakeKeyring) Delete(k string) error { delete(f.m, k); return nil }

func TestLinuxStoreMovesFileSecretsIntoTheKeyring(t *testing.T) {
	file := &fileStore{file: filepath.Join(t.TempDir(), "secrets.json")}
	kr := &fakeKeyring{m: map[string]string{}}
	available := false
	l := &linuxStore{file: file, ssOpen: func() (Store, error) {
		if !available {
			return nil, errors.New("no keyring")
		}
		return kr, nil
	}}

	// No keyring at first: the file is used
	if err := l.Set("mysql-root", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if v, _ := file.Get("mysql-root"); v != "s3cret" {
		t.Fatal("expected the file to hold it")
	}

	// The keyring appears (next login): reading moves the secret over
	available, l.tried = true, l.tried.AddDate(0, 0, -1)
	if v, err := l.Get("mysql-root"); err != nil || v != "s3cret" {
		t.Fatalf("get = %q %v", v, err)
	}
	if kr.m["mysql-root"] != "s3cret" {
		t.Fatal("not moved into the keyring")
	}
	if _, err := file.Get("mysql-root"); !errors.Is(err, ErrNotFound) {
		t.Fatal("still in the file after the move")
	}

	// New secrets go straight to the keyring
	_ = l.Set("db-shop", "pw")
	if kr.m["db-shop"] != "pw" {
		t.Fatal("new secret not in the keyring")
	}

	// The keyring goes away: writes fall back to the file, nothing is lost
	kr.down = true
	if err := l.Set("db-blog", "pw2"); err != nil {
		t.Fatal(err)
	}
	if v, _ := file.Get("db-blog"); v != "pw2" {
		t.Fatal("fallback write")
	}
	if _, err := l.Get("nope!"); err == nil {
		t.Fatal("invalid key accepted")
	}
}

// TestRealKeyring talks to a running Secret Service. It runs only when asked:
//
//	LOCALSITES_TEST_KEYRING=1 go test ./internal/secrets -run TestRealKeyring
func TestRealKeyring(t *testing.T) {
	if os.Getenv("LOCALSITES_TEST_KEYRING") != "1" {
		t.Skip("set LOCALSITES_TEST_KEYRING=1 to test against a real keyring")
	}
	ks, err := openKeyring()
	if err != nil {
		t.Fatal(err)
	}
	if err := ks.Set("test-key", "välue with spaces"); err != nil {
		t.Fatal(err)
	}
	if err := ks.Set("test-key", "replaced"); err != nil { // the same item is replaced, not duplicated
		t.Fatal(err)
	}
	if v, err := ks.Get("test-key"); err != nil || v != "replaced" {
		t.Fatalf("get = %q %v", v, err)
	}
	if err := ks.Delete("test-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Get("test-key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	s := New(t.TempDir()) // the store LocalSites uses
	if _, ok := s.(*linuxStore); !ok {
		t.Fatalf("store = %T", s)
	}
	if err := s.Set("db-x", "pw"); err != nil {
		t.Fatal(err)
	}
	if v, _ := ks.Get("db-x"); v != "pw" {
		t.Fatal("LocalSites' store didn't write to the keyring")
	}
	_ = s.Delete("db-x")
}
