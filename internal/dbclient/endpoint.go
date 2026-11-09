package dbclient

import "fmt"

// Endpoint is how command-line tools (mysqldump, pg_dump, sqlite3) reach a connection: the address
// after any SSH tunnel, and the login. The password is only ever passed in the environment.
type Endpoint struct {
	Driver   string
	Host     string // "" with Socket
	Port     int
	Socket   string
	User     string
	Password string
	Database string // default database (Postgres)
	File     string // SQLite
	ReadOnly bool
	Name     string
	Builtin  bool
	ID       string
}

// Endpoint returns the address and login for a saved connection, starting its SSH tunnel if it has one.
func (m *Manager) Endpoint(id string) (Endpoint, error) {
	c, err := m.Get(id)
	if err != nil {
		return Endpoint{}, err
	}
	if m.Prepare != nil {
		if err := m.Prepare(c); err != nil {
			return Endpoint{}, err
		}
	}
	key := secretKey(c.ID)
	if c.PasswordKey != "" {
		key = c.PasswordKey
	}
	pw, _ := m.secrets.Get(key)
	ep := Endpoint{Driver: c.Driver, Host: c.Host, Port: c.Port, Socket: c.Socket, User: c.User, Password: pw, Database: c.Database,
		File: c.File, ReadOnly: c.ReadOnly, Name: c.Name, Builtin: c.Builtin, ID: c.ID}
	switch c.Driver {
	case "mysql":
		ep.Port = orDefault(c.Port, 3306)
	case "postgres":
		ep.Port = orDefault(c.Port, 5432)
	}
	if c.SSHHost != "" && c.Driver != "sqlite" {
		lp, err := tunnelFor(c, ep.Port)
		if err != nil {
			return Endpoint{}, fmt.Errorf("SSH tunnel: %w", err)
		}
		ep.Host, ep.Port, ep.Socket = "127.0.0.1", lp, ""
	}
	if ep.Host == "" && ep.Socket == "" && c.Driver != "sqlite" {
		ep.Host = "127.0.0.1"
	}
	return ep, nil
}
