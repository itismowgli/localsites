package engine

import (
	"time"
)

// LocalSites MySQL sleeps when nothing needs it: the owner's projects may all use another MySQL (MAMP PRO),
// and an idle MySQL still holds well over 100 MB of memory.

// MySQLIdle is how long it stays up unused (tests shorten it).
var MySQLIdle = 20 * time.Minute

// touchMySQL marks MySQL as used, starting it if it is asleep.
func (e *Engine) touchMySQL() error {
	e.mysqlUsed.Store(time.Now().UnixNano())
	if e.Sup.Status(procMySQL).Running {
		return nil
	}
	return e.EnsureMySQL()
}

// StopIdleMySQL stops LocalSites MySQL when no running project needs it and nothing used it for a while.
// Returns true when it stopped it.
func (e *Engine) StopIdleMySQL() bool {
	if !e.Sup.Status(procMySQL).Running || e.mysqlWanted(e.Sites()) {
		return false
	}
	if time.Since(time.Unix(0, e.mysqlUsed.Load())) < MySQLIdle {
		return false
	}
	e.mysqlMu.Lock()
	defer e.mysqlMu.Unlock()
	if e.Data != nil {
		e.Data.CloseConn("localsites")
	}
	e.Sup.Stop(procMySQL, 30*time.Second)
	return true
}
