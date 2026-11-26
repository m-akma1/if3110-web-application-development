package migration

import (
	"context"
	"errors"
	"testing"
	"time"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/ids"
)

func (f *fixture) executions(t *testing.T, migrationID string) []Execution {
	t.Helper()
	list, err := f.repo.ListExecutions(context.Background(), migrationID)
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	return list
}

func (f *fixture) execution(t *testing.T, id string) Execution {
	t.Helper()
	var ex Execution
	var kind, status string
	err := f.platform.QueryRowContext(context.Background(), `
		SELECT id, migration_id, execution_type, status, sql_sent, COALESCE(reverts_execution_id::text, '')
		FROM migration_executions WHERE id = $1`, id).
		Scan(&ex.ID, &ex.MigrationID, &kind, &status, &ex.SQLSent, &ex.RevertsExecutionID)
	if err != nil {
		t.Fatalf("execution %s: %v", id, err)
	}
	ex.Kind, ex.Status = ExecKind(kind), ExecStatus(status)
	return ex
}

// storedRow reads what is physically stored for a migration, without the derived status.
func (f *fixture) storedRow(t *testing.T, id string) (status string, updatedAt time.Time) {
	t.Helper()
	if err := f.platform.QueryRowContext(context.Background(),
		`SELECT status, updated_at FROM migrations WHERE id = $1`, id).Scan(&status, &updatedAt); err != nil {
		t.Fatalf("stored row %s: %v", id, err)
	}
	return status, updatedAt
}

func TestExecuteAppendsOneExecution(t *testing.T) {
	f := newFixture(t, Limits{})
	jobID := ids.New()
	ctx := WithRun(context.Background(), jobID, 2)

	ok := f.draft(t, "orders", "CREATE TABLE orders (id int)")
	if _, _, err := f.engine.Execute(ctx, ok.ID, f.userID); err != nil {
		t.Fatal(err)
	}
	list := f.executions(t, ok.ID)
	if len(list) != 1 {
		t.Fatalf("executions = %d, want 1", len(list))
	}
	ex := list[0]
	if ex.Kind != ExecExecute || ex.Status != ExecSuccess || !ex.SQLSent || ex.SQL != "CREATE TABLE orders (id int)" {
		t.Errorf("execution = %+v, want EXECUTE SUCCESS with the script that ran", ex)
	}
	if ex.JobID != jobID || ex.Attempt != 2 || ex.ExecutorID != f.userID || ex.ExecutorName != "engineer" {
		t.Errorf("execution = %+v, want job %s attempt 2 by engineer", ex, jobID)
	}
	if m := f.migration(t, ok.ID); m.AppliedExecutionID != ex.ID {
		t.Errorf("AppliedExecutionID = %q, want %q", m.AppliedExecutionID, ex.ID)
	}

	bad := f.draft(t, "broken", "INSERT INTO missing VALUES (1)")
	if _, _, err := f.engine.Execute(context.Background(), bad.ID, f.userID); err != nil {
		t.Fatal(err)
	}
	list = f.executions(t, bad.ID)
	if len(list) != 1 || list[0].Status != ExecFailed || !list[0].SQLSent || list[0].SQLState != "42P01" || list[0].JobID != "" {
		t.Fatalf("failed run executions = %+v, want one FAILED 42P01 without a job", list)
	}
}

func TestRollbackAndForwardAppendToTheChain(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	a := f.draft(t, "orders", "CREATE TABLE orders (id int)")
	if _, _, err := f.engine.Execute(ctx, a.ID, f.userID); err != nil {
		t.Fatal(err)
	}
	statusBefore, updatedBefore := f.storedRow(t, a.ID)

	rolledBack, res, err := f.engine.ExecuteRollback(ctx, a.ID, f.userID)
	if err != nil || !res.OK {
		t.Fatalf("ExecuteRollback = %v, %+v", err, res)
	}
	// The original row is not rewritten: the stored row is byte-for-byte the same...
	if status, updated := f.storedRow(t, a.ID); status != statusBefore || !updated.Equal(updatedBefore) {
		t.Fatalf("stored row = %s @ %v, want unchanged %s @ %v", status, updated, statusBefore, updatedBefore)
	}
	// ...but it reads as ROLLED_BACK because a rollback execution reverts its execution.
	if rolledBack.Status != StatusRolledBack || rolledBack.RollbackExecutionID == "" {
		t.Fatalf("after rollback = %s (rollback exec %q), want ROLLED_BACK", rolledBack.Status, rolledBack.RollbackExecutionID)
	}
	rb := f.execution(t, rolledBack.RollbackExecutionID)
	if rb.Kind != ExecRollback || rb.RevertsExecutionID != rolledBack.AppliedExecutionID || rb.MigrationID == a.ID {
		t.Fatalf("rollback execution = %+v, want ROLLBACK reverting %s on the rollback row", rb, rolledBack.AppliedExecutionID)
	}

	fwd, res, err := f.engine.ExecuteForward(ctx, a.ID, f.userID)
	if err != nil || !res.OK {
		t.Fatalf("ExecuteForward = %v, %+v", err, res)
	}
	fe := f.execution(t, fwd.AppliedExecutionID)
	if fe.Kind != ExecForward || fe.RevertsExecutionID != rb.ID {
		t.Fatalf("forward execution = %+v, want FORWARD reverting rollback %s", fe, rb.ID)
	}
	// a stays rolled back (its forward is a new row) and cannot be forwarded twice.
	if again := f.migration(t, a.ID); again.Status != StatusRolledBack || again.ForwardExecutionID != fe.ID {
		t.Fatalf("a = %s forward %q, want ROLLED_BACK with forward %s", again.Status, again.ForwardExecutionID, fe.ID)
	}
	if _, _, err := f.engine.ExecuteForward(ctx, a.ID, f.userID); !errors.Is(err, ErrAlreadyForwarded) {
		t.Fatalf("second forward err = %v, want ErrAlreadyForwarded", err)
	}
	if latest, err := f.repo.GetLatestActive(ctx, f.dbID); err != nil || latest.ID != fwd.ID {
		t.Fatalf("latest active = %v, %v; want the forward row %s", latest, err, fwd.ID)
	}

	// The forward row can be rolled back in turn: a second link in the chain.
	back, res, err := f.engine.ExecuteRollback(ctx, fwd.ID, f.userID)
	if err != nil || !res.OK || back.Status != StatusRolledBack {
		t.Fatalf("rollback of forward = %v, %+v, %v", back, res, err)
	}
	if r2 := f.execution(t, back.RollbackExecutionID); r2.RevertsExecutionID != fe.ID {
		t.Fatalf("second rollback reverts %s, want %s", r2.RevertsExecutionID, fe.ID)
	}
	if f.targetHasTable(t, "orders") {
		t.Fatal("orders should be dropped after the second rollback")
	}
}

func TestFailedDirectRollbackIsRecordedAndChangesNothing(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	a, err := f.repo.CreateDraft(ctx, NewDraft{
		DatabaseID: f.dbID, Name: "orders", SQL: "CREATE TABLE orders (id int)",
		SQLDown: "DROP TABLE does_not_exist", CreatedBy: f.userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.engine.Execute(ctx, a.ID, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, res, err := f.engine.ExecuteRollback(ctx, a.ID, f.userID); err == nil || res == nil || res.OK {
		t.Fatalf("rollback = %+v, %v; want a failed script", res, err)
	}
	list := f.executions(t, a.ID)
	if len(list) != 2 || list[1].Kind != ExecRollback || list[1].Status != ExecFailed || !list[1].SQLSent ||
		list[1].RevertsExecutionID != list[0].ID {
		t.Fatalf("executions = %+v, want EXECUTE then a FAILED ROLLBACK aimed at it", list)
	}
	if m := f.migration(t, a.ID); m.Status != StatusSuccess || !f.targetHasTable(t, "orders") {
		t.Fatalf("after failed rollback a = %s, want SUCCESS and orders still there", m.Status)
	}
}

func TestFailedApprovedRollbackMarksTheRequestFailed(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	a, err := f.repo.CreateDraft(ctx, NewDraft{
		DatabaseID: f.dbID, Name: "orders", SQL: "CREATE TABLE orders (id int)",
		SQLDown: "DROP TABLE does_not_exist", CreatedBy: f.userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.engine.Execute(ctx, a.ID, f.userID); err != nil {
		t.Fatal(err)
	}
	req, err := f.repo.CreateDraft(ctx, NewDraft{
		DatabaseID: f.dbID, Name: rollbackName(a.Name), SQL: "DROP TABLE does_not_exist",
		SQLDown: "DROP TABLE does_not_exist", ExecutionType: "ROLLBACK", RollbackTargetID: a.ID, CreatedBy: f.userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.platform.ExecContext(ctx, `UPDATE migrations SET status = 'APPROVED' WHERE id = $1`, req.ID); err != nil {
		t.Fatal(err)
	}

	if _, res, err := f.engine.Execute(ctx, req.ID, f.userID); err == nil || res == nil || res.OK {
		t.Fatalf("approved rollback = %+v, %v; want a failed script", res, err)
	}
	// Before 009 the request stayed APPROVED/RUNNING forever; now it is a FAILED run.
	if m := f.migration(t, req.ID); m.Status != StatusFailed || m.Error == "" {
		t.Fatalf("request = %s (%q), want FAILED with the error", m.Status, m.Error)
	}
	list := f.executions(t, req.ID)
	if len(list) != 1 || list[0].Status != ExecFailed || list[0].RevertsExecutionID != f.migration(t, a.ID).AppliedExecutionID {
		t.Fatalf("executions = %+v, want one FAILED rollback aimed at a", list)
	}
	// The target is still active, so a new rollback request is allowed.
	if pending, err := f.repo.HasPendingActionFor(ctx, a.ID, "ROLLBACK"); err != nil || pending {
		t.Fatalf("pending rollback = %v, %v; want none after the failed request", pending, err)
	}
}

func TestExecutionsAreAppendOnly(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	other := insertUser(t, f.platform, "other")
	d := f.draft(t, "orders", "CREATE TABLE orders (id int)")
	if _, _, err := f.engine.Execute(ctx, d.ID, other); err != nil {
		t.Fatal(err)
	}
	exID := f.executions(t, d.ID)[0].ID

	if _, err := f.platform.ExecContext(ctx, `UPDATE migration_executions SET status = 'FAILED' WHERE id = $1`, exID); err == nil {
		t.Fatal("UPDATE of an execution succeeded, want it rejected")
	}
	if _, err := f.platform.ExecContext(ctx, `DELETE FROM migration_executions WHERE id = $1`, exID); err == nil {
		t.Fatal("DELETE of an execution succeeded, want it rejected")
	}
	// Deleting the executor is allowed: the row keeps everything except who ran it.
	if _, err := f.platform.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, other); err != nil {
		t.Fatalf("deleting the executor: %v", err)
	}
	if ex := f.executions(t, d.ID)[0]; ex.ExecutorID != "" || ex.Status != ExecSuccess {
		t.Fatalf("after deleting the user execution = %+v, want executor cleared, rest kept", ex)
	}
	// Removing the whole target database removes its history too.
	if _, err := f.platform.ExecContext(ctx, `DELETE FROM target_databases WHERE id = $1`, f.dbID); err != nil {
		t.Fatalf("deleting the target: %v", err)
	}
	var n int
	if err := f.platform.QueryRowContext(ctx, `SELECT COUNT(*) FROM migration_executions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("executions after removing the target = %d, %v; want 0", n, err)
	}
}

func TestWorkerRecordsAttemptsThatNeverReachedTheTarget(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	d := f.draft(t, "orders", "CREATE TABLE orders (id int)")
	j := f.enqueue(t, d)
	f.pools.Remove(f.dbID) // target down: every attempt fails before SQL
	w := fastWorker(f)

	for i := 0; i < 20 && !f.job(t, j.ID).Done(); i++ {
		if _, err := w.RunOnce(ctx, "w1"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	if got := f.job(t, j.ID); got.Status != JobFailed {
		t.Fatalf("job = %s, want FAILED", got.Status)
	}
	list := f.executions(t, d.ID)
	if len(list) != j.MaxAttempts {
		t.Fatalf("executions = %d, want one per attempt (%d)", len(list), j.MaxAttempts)
	}
	for i, ex := range list {
		if ex.Status != ExecFailed || ex.SQLSent || ex.SQL != "" || ex.Attempt != i+1 || ex.JobID != j.ID || ex.Error == "" {
			t.Errorf("attempt %d = %+v, want FAILED before SQL with the error", i+1, ex)
		}
	}
	// Attempts before SQL are not an outcome: the job may run again, and the draft is kept.
	if done, err := f.repo.OutcomeRecorded(ctx, j); err != nil || done {
		t.Fatalf("OutcomeRecorded = %v, %v; want false", done, err)
	}
	if err := f.repo.DeleteDraft(ctx, d.ID); !errors.Is(err, ErrHasHistory) {
		t.Fatalf("DeleteDraft = %v, want ErrHasHistory", err)
	}
}

func TestOutcomeRecordedMatchesTheJob(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	d := f.draft(t, "orders", "CREATE TABLE orders (id int)")
	j := f.enqueue(t, d)
	if done, err := f.repo.OutcomeRecorded(ctx, j); err != nil || done {
		t.Fatalf("before running OutcomeRecorded = %v, %v; want false", done, err)
	}
	if _, _, err := f.engine.Execute(WithRun(ctx, j.ID, 1), d.ID, f.userID); err != nil {
		t.Fatal(err)
	}
	if done, err := f.repo.OutcomeRecorded(ctx, j); err != nil || !done {
		t.Fatalf("after running OutcomeRecorded = %v, %v; want true", done, err)
	}
	// Another job of another database is not affected.
	otherDB := accesstest.InsertTarget(t, f.platform, "x-"+ids.New()[:8])
	other := &Job{ID: ids.New(), DatabaseID: otherDB}
	if done, err := f.repo.OutcomeRecorded(ctx, other); err != nil || done {
		t.Fatalf("unrelated job OutcomeRecorded = %v, %v; want false", done, err)
	}
}

func TestUncertainRunIsRecorded(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	d := f.draft(t, "orders", "CREATE TABLE orders (id int)")
	j := f.enqueue(t, d)
	if _, err := f.repo.ClaimNextJob(ctx, "dead-worker", time.Minute); err != nil {
		t.Fatal(err)
	}
	f.expireLease(t, j.ID, true) // the worker died after sending SQL

	if _, err := fastWorker(f).RecoverStaleJobs(ctx); err != nil {
		t.Fatal(err)
	}
	list := f.executions(t, d.ID)
	if len(list) != 1 || list[0].Status != ExecUncertain || !list[0].SQLSent || list[0].SQL != d.SQL || list[0].JobID != j.ID {
		t.Fatalf("executions = %+v, want one UNCERTAIN with the script", list)
	}
}
