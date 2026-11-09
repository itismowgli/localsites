package dbclient

import "testing"

func TestSSHHostValidation(t *testing.T) {
	for _, h := range []string{"server", "user@db.example.com", "deploy@10.0.0.5", "my_alias"} {
		if err := validSSH(Conn{Driver: "mysql", SSHHost: h}); err != nil {
			t.Errorf("%q refused: %v", h, err)
		}
	}
	// Anything ssh could read as an option or a second argument is refused
	for _, h := range []string{"-oProxyCommand=touch /tmp/x", "user@-oProxyCommand=x", "a b", "host;rm", "@host", "user@"} {
		if err := validSSH(Conn{Driver: "mysql", SSHHost: h}); err == nil {
			t.Errorf("%q accepted", h)
		}
	}
	if validSSH(Conn{Driver: "sqlite", SSHHost: "server"}) == nil {
		t.Error("SQLite over SSH accepted")
	}
}
