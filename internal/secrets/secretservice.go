package secrets

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// The freedesktop Secret Service (GNOME Keyring, KWallet), spoken over the session D-Bus. Items carry the
// attributes service=LocalSites and account=<key>.
// https://specifications.freedesktop.org/secret-service-spec/latest/

const (
	ssName       = "org.freedesktop.secrets"
	ssPath       = dbus.ObjectPath("/org/freedesktop/secrets")
	ssService    = "org.freedesktop.Secret.Service"
	ssCollection = "org.freedesktop.Secret.Collection"
	ssItem       = "org.freedesktop.Secret.Item"
	ssPrompt     = "org.freedesktop.Secret.Prompt"
)

// ssSecret is the Secret struct (oayays): session, parameters, value, content type.
type ssSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

type secretService struct {
	conn    *dbus.Conn
	session dbus.ObjectPath
}

// openSecretService connects to the keyring, or fails when none runs (no desktop session, no keyring).
func openSecretService() (*secretService, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}
	var out dbus.Variant
	var session dbus.ObjectPath
	// "plain": the secret travels unencrypted over the person's own session bus, as libsecret does by default
	if err := conn.Object(ssName, ssPath).Call(ssService+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&out, &session); err != nil {
		return nil, err
	}
	return &secretService{conn: conn, session: session}, nil
}

func (s *secretService) attrs(key string) map[string]string {
	return map[string]string{"service": service, "account": key}
}

// collection is the default keyring, unlocked (the desktop may ask for the login password once).
func (s *secretService) collection() (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	if err := s.conn.Object(ssName, ssPath).Call(ssService+".ReadAlias", 0, "default").Store(&path); err != nil {
		return "", err
	}
	if path == "/" {
		return "", errors.New("no default keyring")
	}
	return path, s.unlock(path)
}

func (s *secretService) unlock(paths ...dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := s.conn.Object(ssName, ssPath).Call(ssService+".Unlock", 0, paths).Store(&unlocked, &prompt); err != nil {
		return err
	}
	return s.wait(prompt)
}

// wait shows a prompt (when the service needs one) and waits for the person to answer it.
func (s *secretService) wait(prompt dbus.ObjectPath) error {
	if prompt == "/" || prompt == "" {
		return nil
	}
	ch := make(chan *dbus.Signal, 4)
	s.conn.Signal(ch)
	defer s.conn.RemoveSignal(ch)
	if err := s.conn.AddMatchSignal(dbus.WithMatchObjectPath(prompt), dbus.WithMatchInterface(ssPrompt), dbus.WithMatchMember("Completed")); err != nil {
		return err
	}
	if err := s.conn.Object(ssName, prompt).Call(ssPrompt+".Prompt", 0, "").Err; err != nil {
		return err
	}
	timeout := time.After(2 * time.Minute)
	for {
		select {
		case sig := <-ch:
			if sig.Path == prompt && sig.Name == ssPrompt+".Completed" {
				if len(sig.Body) > 0 {
					if dismissed, _ := sig.Body[0].(bool); dismissed {
						return errors.New("the keyring prompt was dismissed")
					}
				}
				return nil
			}
		case <-timeout:
			return errors.New("the keyring prompt wasn't answered")
		}
	}
}

func (s *secretService) find(key string) ([]dbus.ObjectPath, error) {
	col, err := s.collection()
	if err != nil {
		return nil, err
	}
	var items []dbus.ObjectPath
	err = s.conn.Object(ssName, col).Call(ssCollection+".SearchItems", 0, s.attrs(key)).Store(&items)
	return items, err
}

func (s *secretService) Get(key string) (string, error) {
	items, err := s.find(key)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", ErrNotFound
	}
	if err := s.unlock(items[0]); err != nil {
		return "", err
	}
	var sec ssSecret
	if err := s.conn.Object(ssName, items[0]).Call(ssItem+".GetSecret", 0, s.session).Store(&sec); err != nil {
		return "", err
	}
	return string(sec.Value), nil
}

func (s *secretService) Set(key, value string) error {
	col, err := s.collection()
	if err != nil {
		return err
	}
	props := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant("LocalSites " + key),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(s.attrs(key)),
	}
	sec := ssSecret{Session: s.session, Value: []byte(value), ContentType: "text/plain"}
	var item, prompt dbus.ObjectPath
	if err := s.conn.Object(ssName, col).Call(ssCollection+".CreateItem", 0, props, sec, true).Store(&item, &prompt); err != nil {
		return fmt.Errorf("keyring: %v", err)
	}
	return s.wait(prompt)
}

func (s *secretService) Delete(key string) error {
	items, err := s.find(key)
	if err != nil {
		return err
	}
	for _, it := range items {
		var prompt dbus.ObjectPath
		if err := s.conn.Object(ssName, it).Call(ssItem+".Delete", 0).Store(&prompt); err != nil {
			return err
		}
		if err := s.wait(prompt); err != nil {
			return err
		}
	}
	return nil
}

// linuxStore uses the keyring when one runs, and the owner-only file otherwise. Secrets in the file move
// into the keyring the first time they are read while it's available, so switching between the two
// (a login without a keyring, then one with it) never loses a secret.
type linuxStore struct {
	mu     sync.Mutex
	file   *fileStore
	ss     Store
	tried  time.Time
	ssOpen func() (Store, error)
}

func (l *linuxStore) keyring() Store {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ss == nil && time.Since(l.tried) > 30*time.Second {
		l.tried = time.Now()
		if ss, err := l.ssOpen(); err == nil {
			l.ss = ss
		}
	}
	return l.ss
}

func (l *linuxStore) drop() { l.mu.Lock(); l.ss = nil; l.mu.Unlock() }

func (l *linuxStore) Get(key string) (string, error) {
	if !keyRe.MatchString(key) {
		return "", fmt.Errorf("invalid secret name %q", key)
	}
	if ss := l.keyring(); ss != nil {
		v, err := ss.Get(key)
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, ErrNotFound) {
			l.drop() // the keyring went away: use the file until it's back
		} else if fv, ferr := l.file.Get(key); ferr == nil {
			if ss.Set(key, fv) == nil {
				_ = l.file.Delete(key)
			}
			return fv, nil
		}
	}
	return l.file.Get(key)
}

func (l *linuxStore) Set(key, value string) error {
	if !keyRe.MatchString(key) {
		return fmt.Errorf("invalid secret name %q", key)
	}
	if ss := l.keyring(); ss != nil {
		if err := ss.Set(key, value); err == nil {
			_ = l.file.Delete(key)
			return nil
		}
		l.drop()
	}
	return l.file.Set(key, value)
}

func (l *linuxStore) Delete(key string) error {
	if !keyRe.MatchString(key) {
		return fmt.Errorf("invalid secret name %q", key)
	}
	if ss := l.keyring(); ss != nil {
		_ = ss.Delete(key)
	}
	return l.file.Delete(key)
}

func openKeyring() (Store, error) {
	ss, err := openSecretService()
	if err != nil {
		return nil, err
	}
	return ss, nil
}
