package dbclient

import "testing"

// Layer 1 of read-only protection: only reading statements pass.
func TestReadOnlyAllowlist(t *testing.T) {
	for _, q := range []string{"SELECT 1", "  select * from t", "SHOW TABLES", "DESCRIBE t", "EXPLAIN SELECT 1", "WITH x AS (SELECT 1) SELECT * FROM x", "PRAGMA table_info(t)"} {
		if !readOnlyRe.MatchString(q) {
			t.Errorf("reading statement refused: %q", q)
		}
	}
	for _, q := range []string{"DROP TABLE t", "delete from t", "UPDATE t SET a=1", "INSERT INTO t VALUES (1)", "ALTER TABLE t ADD c INT", "TRUNCATE t",
		"CREATE TABLE x (a INT)", "/* hi */ DROP TABLE t", "CALL proc()", "SET GLOBAL x=1", "GRANT ALL ON *.* TO u", "LOAD DATA INFILE 'x' INTO TABLE t", "selectx 1"} {
		if readOnlyRe.MatchString(q) {
			t.Errorf("changing statement allowed: %q", q)
		}
	}
}

func TestSideEffectReadsNeedWrite(t *testing.T) {
	for _, q := range []string{"SELECT * FROM t INTO OUTFILE '/tmp/x'", "select pg_terminate_backend(123)", "SELECT nextval('s')", "select dblink_exec('x','drop table t')"} {
		if !sideEffectRe.MatchString(q) {
			t.Errorf("%q passes as read-only", q)
		}
	}
	if sideEffectRe.MatchString("SELECT outfile, next_value FROM t") {
		t.Error("plain select flagged")
	}
}
