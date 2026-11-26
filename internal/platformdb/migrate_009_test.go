package platformdb_test

import (
	"context"
	"testing"

	"dbplatform/internal/ids"
	"dbplatform/internal/platformdb"
	"dbplatform/internal/testutil"
)

// TestMigration009BackfillsTheExecutionChain applies 001-008, writes history the way the old
// code did (the original row rewritten to ROLLED_BACK), then applies 009 and checks the result.
func TestMigration009BackfillsTheExecutionChain_Integration(t *testing.T) {
	db := testutil.NewSchemaDB(t)
	ctx := context.Background()
	all, err := platformdb.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.Version >= 9 {
			break
		}
		if _, err := db.ExecContext(ctx, m.SQL); err != nil {
			t.Fatalf("apply %s: %v", m.Name, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.Version, m.Name); err != nil {
			t.Fatal(err)
		}
	}

	user, target := ids.New(), ids.New()
	a, r, f, failed := ids.New(), ids.New(), ids.New(), ids.New()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'legacy', 'h')`, user)
	mustExec(`INSERT INTO target_databases (id, name, host, port, db_name, username, encrypted_password)
		VALUES ($1, 'legacy-db', 'localhost', 5432, 'x', 'x', '\x00')`, target)
	insert := func(id, status, execType string, version any, script string, rollbackTarget, source any) {
		t.Helper()
		mustExec(`INSERT INTO migrations (id, database_id, version, name, sql_script, sql_up, status,
			execution_type, rollback_target_id, source_migration_id, created_by, executed_by, executed_at, duration_ms)
			VALUES ($1, $2, $3, $10, $4, $4, $5, $6, $7, $8, $9, $9, NOW(), 5)`,
			id, target, version, script, status, execType, rollbackTarget, source, user, "legacy "+id[:8])
	}
	insert(a, "ROLLED_BACK", "FORWARD", 1, "CREATE TABLE t (x int)", nil, nil) // rewritten by the old rollback
	insert(r, "SUCCESS", "ROLLBACK", nil, "DROP TABLE t", a, nil)
	insert(f, "SUCCESS", "FORWARD", 2, "CREATE TABLE t (x int)", nil, a)
	insert(failed, "FAILED", "FORWARD", nil, "INSERT INTO nope VALUES (1)", nil, nil)
	mustExec(`INSERT INTO migrations (id, database_id, name, sql_script, status) VALUES ($1, $2, 'draft', 'SELECT 1', 'DRAFT')`,
		ids.New(), target) // never executed: gets no execution row

	if applied, err := platformdb.Migrate(ctx, db); err != nil || len(applied) == 0 {
		t.Fatalf("Migrate = %v, %v; want 009 and later applied", applied, err)
	}

	var stored string
	if err := db.QueryRowContext(ctx, `SELECT status FROM migrations WHERE id = $1`, a).Scan(&stored); err != nil || stored != "SUCCESS" {
		t.Fatalf("stored status of a = %q, %v; want SUCCESS (ROLLED_BACK is derived now)", stored, err)
	}
	var applied, rollback, forward string
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(applied_execution_id::text, ''), COALESCE(rollback_execution_id::text, ''),
		       COALESCE(forward_execution_id::text, '')
		FROM migration_chain WHERE migration_id = $1`, a).Scan(&applied, &rollback, &forward); err != nil {
		t.Fatal(err)
	}
	if applied != a || rollback != r || forward != f {
		t.Fatalf("chain of a = %s -> %s -> %s, want %s -> %s -> %s", applied, rollback, forward, a, r, f)
	}
	var n, failedRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'FAILED') FROM migration_executions`).
		Scan(&n, &failedRows); err != nil || n != 4 || failedRows != 1 {
		t.Fatalf("executions = %d (failed %d), %v; want 4 with 1 FAILED (drafts get none)", n, failedRows, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE migrations SET status = 'ROLLED_BACK' WHERE id = $1`, a); err == nil {
		t.Fatal("storing ROLLED_BACK again succeeded, want it rejected")
	}
}
