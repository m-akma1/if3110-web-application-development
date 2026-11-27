package migration

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// recoveryRows counts the before-images stored on the target for a migration, per batch.
func (f *fixture) recoveryRows(t *testing.T, migrationID string) map[int]int {
	t.Helper()
	rows, err := f.target.Query(`SELECT batch_no, count(*) FROM `+recoveryTable+` WHERE migration_id = $1 GROUP BY batch_no`, migrationID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int]int{}
	for rows.Next() {
		var no, n int
		if err := rows.Scan(&no, &n); err != nil {
			t.Fatal(err)
		}
		out[no] = n
	}
	return out
}

func (f *fixture) runAbort(t *testing.T, m *Migration) *Job {
	t.Helper()
	j := f.enqueueType(t, m, JobAbort)
	if _, err := fastWorker(f).RunOnce(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	return f.job(t, j.ID)
}

func TestBatchStoresBeforeImagesAndAbortReleasesThem(t *testing.T) {
	f := newFixture(t, Limits{})
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)
	f.partial(t, m, 2100) // batches 1 and 2 committed

	if got := f.recoveryRows(t, m.ID); got[1] != 1000 || got[2] != 1000 || len(got) != 2 {
		t.Fatalf("recovery rows = %v, want 1000 for batches 1 and 2", got)
	}
	var before string
	if err := f.target.QueryRow(`SELECT before::text FROM `+recoveryTable+` WHERE migration_id = $1 AND row_key = 7`, m.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != `{"v": 0, "id": 7}` {
		t.Fatalf("before-image of row 7 = %s, want the row as it was before the batch", before)
	}
	for _, b := range f.batches(t, m.ID) {
		if b.RecoveryRows == nil || *b.RecoveryRows != 1000 {
			t.Fatalf("batch %d recovery_rows = %v, want 1000", b.No, b.RecoveryRows)
		}
	}

	if _, err := f.target.Exec(`ALTER TABLE items DROP CONSTRAINT stop_here`); err != nil {
		t.Fatal(err)
	}
	if j := f.runAbort(t, m); j.Status != JobSuccess {
		t.Fatalf("abort = %s (%s), want SUCCESS", j.Status, j.LastError)
	}
	if got := f.recoveryRows(t, m.ID); len(got) != 0 {
		t.Fatalf("recovery rows after the abort = %v, want none", got)
	}
}

func TestFinishedBatchMigrationPurgesItsRecoveryData(t *testing.T) {
	f := newFixture(t, Limits{})
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)
	f.enqueue(t, m)
	if _, err := fastWorker(f).RunOnce(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if got := f.migration(t, m.ID); got.Status != StatusSuccess {
		t.Fatalf("migration = %s, want SUCCESS", got.Status)
	}
	if got := f.recoveryRows(t, m.ID); len(got) != 0 {
		t.Fatalf("recovery rows after SUCCESS = %v, want none (an abort is no longer possible)", got)
	}
}

func TestAbortStopsWhenARowChangedAfterItsBatch(t *testing.T) {
	f := newFixture(t, Limits{})
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)
	f.partial(t, m, 2100)
	if _, err := f.target.Exec(`ALTER TABLE items DROP CONSTRAINT stop_here`); err != nil {
		t.Fatal(err)
	}
	// An application changes row 10 (batch 1) after the migration did.
	if _, err := f.target.Exec(`UPDATE items SET v = 5 WHERE id = 10`); err != nil {
		t.Fatal(err)
	}

	j := f.runAbort(t, m)
	if j.Status != JobPartial || !strings.Contains(j.LastError, "tidak aman") || !strings.Contains(j.LastError, "batch 1") {
		t.Fatalf("abort = %s (%s), want PARTIAL, stopped as unsafe at batch 1", j.Status, j.LastError)
	}
	got := f.migration(t, m.ID)
	if got.Status != StatusPartiallyReverted || got.BatchesDown != 1 {
		t.Fatalf("migration = %s down %d, want PARTIALLY_REVERTED with batch 2 undone", got.Status, got.BatchesDown)
	}
	// Batch 1 was rolled back: its rows keep the migration's change and the row stays as the
	// application left it; its recovery rows are kept for the next attempt.
	var v int
	if err := f.target.QueryRow(`SELECT v FROM items WHERE id = 10`).Scan(&v); err != nil || v != 5 {
		t.Fatalf("row 10 v = %d (%v), want 5 (untouched)", v, err)
	}
	if rows := f.recoveryRows(t, m.ID); rows[1] != 1000 || len(rows) != 1 {
		t.Fatalf("recovery rows = %v, want batch 1 kept and batch 2 released", rows)
	}
	ex := f.executions(t, m.ID)
	if last := ex[len(ex)-1]; last.Kind != ExecRollback || last.Status != ExecPartial || !strings.Contains(last.Error, "tidak aman") {
		t.Fatalf("abort execution = %+v, want ROLLBACK PARTIAL with the reason", last)
	}
}

func TestAbortStopsWhenSQLDownChangesMoreRows(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	f.items(t, 30)
	// Rows 1-5 already have v = 1 before the migration; it only changes the others.
	if _, err := f.target.Exec(`UPDATE items SET v = 1 WHERE id <= 5`); err != nil {
		t.Fatal(err)
	}
	m, err := f.repo.CreateDraft(ctx, NewDraft{
		DatabaseID: f.dbID, Name: "set v", SQL: "UPDATE items SET v = 1 WHERE v = 0",
		SQLDown: "UPDATE items SET v = 0 WHERE v = 1", CreatedBy: f.userID,
		ExecutionMode: ModeBatch, BatchAttribute: "id", BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.partial(t, m, 25) // batches 1 and 2 committed

	if _, err := f.target.Exec(`ALTER TABLE items DROP CONSTRAINT stop_here`); err != nil {
		t.Fatal(err)
	}
	j := f.runAbort(t, m)
	// Batch 2 (rows 11-20) is undone exactly; batch 1's SQL Down would also reset rows 1-5.
	if j.Status != JobPartial || !strings.Contains(j.LastError, "SQL Down mengubah 10 baris, padahal batch ini hanya mengubah 5") {
		t.Fatalf("abort = %s (%s), want PARTIAL: SQL Down changes 10 rows of batch 1, only 5 were changed", j.Status, j.LastError)
	}
	var ones int
	if err := f.target.QueryRow(`SELECT count(*) FROM items WHERE id <= 5 AND v = 1`).Scan(&ones); err != nil || ones != 5 {
		t.Fatalf("rows 1-5 with v = 1: %d (%v), want 5 (the unsafe batch was rolled back)", ones, err)
	}
}

func TestAbortIsRefusedWithoutRecoveryData(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	m := e.partialBatch(t)
	// A batch committed by an older version has no recovery data.
	if _, err := e.platform.Exec(`ALTER TABLE migration_batches DISABLE TRIGGER migration_batches_append_only;
		UPDATE migration_batches SET recovery_rows = NULL WHERE migration_id = '` + m.ID + `';
		ALTER TABLE migration_batches ENABLE TRIGGER migration_batches_append_only`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchAbortRollback); !errors.Is(err, ErrRecoveryMissing) {
		t.Fatalf("abort request = %v, want ErrRecoveryMissing", err)
	}
	// Stopping without rollback still works.
	if _, req, _, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchAbortMark); err != nil || req == nil {
		t.Fatalf("mark-stopped request = %+v, %v; want a request", req, err)
	}
}

func TestEngineRefusesAbortWithoutRecoveryData(t *testing.T) {
	f := newFixture(t, Limits{})
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)
	f.partial(t, m, 1500)
	// As if batch 1 was committed by an older version: no recovery data anywhere.
	if _, err := f.platform.Exec(`ALTER TABLE migration_batches DISABLE TRIGGER migration_batches_append_only;
		UPDATE migration_batches SET recovery_rows = NULL WHERE migration_id = '` + m.ID + `';
		ALTER TABLE migration_batches ENABLE TRIGGER migration_batches_append_only`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.target.Exec(`UPDATE `+checkpointTable+` SET recovery_rows = NULL WHERE migration_id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	j := f.runAbort(t, m)
	if j.Status != JobFailed || j.LastError != ErrRecoveryMissing.Error() {
		t.Fatalf("abort job = %s (%s), want FAILED with ErrRecoveryMissing", j.Status, j.LastError)
	}
	if got := f.migration(t, m.ID); got.Status != StatusPartial || got.BatchesDown != 0 {
		t.Fatalf("migration = %s down %d, want PARTIAL with nothing undone", got.Status, got.BatchesDown)
	}
}

func TestBatchPlanRecoveryQueries(t *testing.T) {
	p, err := PrepareBatch("UPDATE orders o SET status = 'X' WHERE o.status = 'Y'", "id")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"to_jsonb(_dbplatform_row) FROM orders _dbplatform_row",
		"_dbplatform_row.id IN (SELECT o.id FROM orders o WHERE (o.status = 'Y')",
		"FOR UPDATE OF _dbplatform_row",
	} {
		if !strings.Contains(p.Capture, want) {
			t.Errorf("Capture lacks %q:\n%s", want, p.Capture)
		}
	}
	if !strings.HasSuffix(p.Returning, "\nRETURNING o.id") || !strings.HasSuffix(p.Capture, "\nRETURNING row_key") || !strings.Contains(p.Verify, "EXCEPT ALL") {
		t.Errorf("Capture/Returning/Verify:\n%s\n%s\n%s", p.Capture, p.Returning, p.Verify)
	}

	// UPDATE ... FROM: the WHERE joins another table, so the whole key range is captured.
	p, err = PrepareBatch("UPDATE orders o SET total = c.t FROM customers c WHERE c.id = o.customer_id", "id")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Capture, "customers") || strings.Contains(p.Capture, " IN (") {
		t.Errorf("Capture with FROM must take the whole key range:\n%s", p.Capture)
	}

	// DELETE is irreversible in batch mode: no recovery queries.
	p, err = PrepareBatch("DELETE FROM orders WHERE status = 'X'", "id")
	if err != nil || p.Verb != "DELETE" || p.Capture != "" {
		t.Errorf("DELETE plan = %+v, %v; want verb DELETE without recovery queries", p, err)
	}
}

func TestBatchIsRefusedWhenARowStartsToMatchDuringIt(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	f.items(t, 20)
	if _, err := f.target.Exec(`UPDATE items SET v = 7 WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	// Stand-in for a concurrent writer: right after the capture, row 3 starts to match.
	if _, err := f.target.Exec(checkpointDDL + `;
		CREATE FUNCTION sneak() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN UPDATE items SET v = 0 WHERE id = 3; RETURN NULL; END $$;
		CREATE TRIGGER sneak AFTER INSERT ON ` + recoveryTable + ` FOR EACH STATEMENT EXECUTE FUNCTION sneak()`); err != nil {
		t.Fatal(err)
	}
	m, err := f.repo.CreateDraft(ctx, NewDraft{
		DatabaseID: f.dbID, Name: "set v", SQL: "UPDATE items SET v = 1 WHERE v = 0",
		SQLDown: "UPDATE items SET v = 0 WHERE v = 1", CreatedBy: f.userID,
		ExecutionMode: ModeBatch, BatchAttribute: "id", BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	j := f.enqueue(t, m)
	if _, err := fastWorker(f).RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if got := f.job(t, j.ID); got.Status != JobFailed || !strings.Contains(got.LastError, ErrBatchRowsChanged.Error()) {
		t.Fatalf("job = %s (%s), want FAILED with ErrBatchRowsChanged", got.Status, got.LastError)
	}
	if got := f.migration(t, m.ID); got.Status != StatusFailed || got.BatchesUp != 0 {
		t.Fatalf("migration = %s up %d, want FAILED with no batch", got.Status, got.BatchesUp)
	}
	if once, _, _ := f.values(t); once != 0 {
		t.Fatalf("%d rows changed, want none (the batch was rolled back)", once)
	}
}
