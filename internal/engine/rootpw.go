package engine

import (
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
	"unicode"
)

const secretRootNext = "mysql-root-next"

// SetRootPassword changes the LocalSites MySQL root password (a strong random one when pw is empty) and
// keeps it in the keychain. The new password is saved before MySQL is changed, so a failure halfway can
// always be recovered (see admin). Returns the password so the person can copy it once.
func (e *Engine) SetRootPassword(pw string) (string, error) {
	if pw == "" {
		pw = randomPassword(24)
	}
	if len(pw) < 12 {
		return "", errors.New("use at least 12 characters")
	}
	if strings.ContainsAny(pw, "'\"\\`") || strings.IndexFunc(pw, unicode.IsSpace) >= 0 || strings.IndexFunc(pw, func(r rune) bool { return r > unicode.MaxASCII }) >= 0 {
		return "", errors.New("use letters, digits and symbols other than quotes, backslashes and spaces")
	}
	db, _, _, err := e.admin()
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := e.Secrets.Set(secretRootNext, pw); err != nil {
		return "", err
	}
	// ALTER USER takes no placeholders; pw was checked above to contain no quotes, backslashes or spaces
	if _, err := db.Exec("ALTER USER 'root'@'localhost' IDENTIFIED BY '" + pw + "'"); err != nil {
		_ = e.Secrets.Delete(secretRootNext)
		return "", err
	}
	if err := e.Secrets.Set(secretRoot, pw); err != nil {
		return "", err // still recoverable: the new password is under secretRootNext
	}
	_ = e.Secrets.Delete(secretRootNext)
	if e.Data != nil {
		e.Data.Close() // the data browser reconnects with the new password
	}
	return pw, nil
}

func randomPassword(n int) string {
	const chars = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789-_.!@#%"
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[k.Int64()]
	}
	return string(b)
}
