package migration

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckScriptAccepts(t *testing.T) {
	scripts := []string{
		"CREATE TABLE t (id int PRIMARY KEY);",
		"-- BEGIN;\nCREATE TABLE t (x int); /* COMMIT; */",
		"INSERT INTO log VALUES ('COMMIT; VACUUM;')",
		"SAVEPOINT s; INSERT INTO t VALUES (1); ROLLBACK TO SAVEPOINT s; RELEASE s",
		"ROLLBACK TO s",
		"CREATE INDEX t_x_idx ON t (x)",
		"REFRESH MATERIALIZED VIEW CONCURRENTLY mv",
		"CREATE FUNCTION f() RETURNS void AS $$ BEGIN COMMIT; END $$ LANGUAGE plpgsql",
		"CREATE FUNCTION g() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; END",
		"ALTER DATABASE demo SET work_mem = '8MB'",
		"COPY (SELECT 1) TO '/tmp/out.csv'",
		"PREPARE q AS SELECT 1",
	}
	for _, s := range scripts {
		stmts, err := CheckScript(s)
		if err != nil {
			t.Errorf("CheckScript(%q) = %v, want accepted", s, err)
		}
		if len(stmts) == 0 {
			t.Errorf("CheckScript(%q) returned no statements", s)
		}
	}
}

func TestCheckScriptRejects(t *testing.T) {
	cases := []struct {
		script  string
		keyword string
		stmt    int
	}{
		{"BEGIN; CREATE TABLE t (x int); COMMIT;", "BEGIN", 1},
		{"CREATE TABLE t (x int); COMMIT", "COMMIT", 2},
		{"START TRANSACTION", "START", 1},
		{"CREATE TABLE t (x int); END;", "END", 2},
		{"ROLLBACK", "ROLLBACK", 1},
		{"rollback and chain", "ROLLBACK", 1},
		{"ABORT", "ABORT", 1},
		{"PREPARE TRANSACTION 'x'", "PREPARE TRANSACTION", 1},
		{"VACUUM ANALYZE t", "VACUUM", 1},
		{"CREATE DATABASE other", "CREATE DATABASE", 1},
		{"DROP DATABASE other", "DROP DATABASE", 1},
		{"CREATE TABLESPACE ts LOCATION '/x'", "CREATE TABLESPACE", 1},
		{"CREATE INDEX CONCURRENTLY i ON t (x)", "CREATE INDEX CONCURRENTLY", 1},
		{"create unique index concurrently if not exists i on t (x)", "CREATE INDEX CONCURRENTLY", 1},
		{"DROP INDEX CONCURRENTLY i", "DROP INDEX CONCURRENTLY", 1},
		{"REINDEX TABLE CONCURRENTLY t", "REINDEX TABLE", 1},
		{"REINDEX DATABASE demo", "REINDEX DATABASE", 1},
		{"ALTER SYSTEM SET work_mem = '1MB'", "ALTER SYSTEM", 1},
		{"ALTER DATABASE demo SET TABLESPACE ts", "ALTER DATABASE SET TABLESPACE", 1},
		{"ALTER TABLE p DETACH PARTITION c CONCURRENTLY", "DETACH PARTITION CONCURRENTLY", 1},
		{"DISCARD ALL", "DISCARD", 1},
		{"COPY t FROM STDIN", "COPY", 1},
		{"COPY t TO STDOUT WITH CSV", "COPY", 1},
	}
	for _, tc := range cases {
		_, err := CheckScript(tc.script)
		var rej *RejectedError
		if !errors.As(err, &rej) {
			t.Errorf("CheckScript(%q) = %v, want *RejectedError", tc.script, err)
			continue
		}
		if !errors.Is(err, ErrScriptRejected) {
			t.Errorf("CheckScript(%q): error does not unwrap to ErrScriptRejected", tc.script)
		}
		if rej.Keyword != tc.keyword || rej.Statement != tc.stmt {
			t.Errorf("CheckScript(%q) rejected %q at statement %d, want %q at %d", tc.script, rej.Keyword, rej.Statement, tc.keyword, tc.stmt)
		}
		if rej.Reason == "" || !strings.Contains(rej.Error(), rej.Keyword) {
			t.Errorf("CheckScript(%q): unhelpful message %q", tc.script, rej.Error())
		}
	}
}

func TestCheckScriptEmptyAndTooLarge(t *testing.T) {
	for _, s := range []string{"", "   \n", "-- only a comment", ";;"} {
		if _, err := CheckScript(s); !errors.Is(err, ErrEmptyScript) {
			t.Errorf("CheckScript(%q) = %v, want ErrEmptyScript", s, err)
		}
	}
	big := "SELECT 1;" + strings.Repeat(" ", MaxScriptLength)
	if _, err := CheckScript(big); !errors.Is(err, ErrScriptTooLarge) {
		t.Errorf("CheckScript(big) = %v, want ErrScriptTooLarge", err)
	}
}
