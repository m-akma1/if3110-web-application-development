package migration

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"testing"
	"time"
)

// items fills the target with n rows (id 1..n, v = 0).
func (f *fixture) items(t *testing.T, n int) {
	t.Helper()
	if _, err := f.target.Exec(`CREATE TABLE items (id bigint PRIMARY KEY, v int NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.target.Exec(`INSERT INTO items (id) SELECT g FROM generate_series(1, $1::int) g`, n); err != nil {
		t.Fatal(err)
	}
}

// values counts rows by v: how many were changed exactly once, never, or more than once.
func (f *fixture) values(t *testing.T) (once, never, twice int) {
	t.Helper()
	if err := f.target.QueryRow(`SELECT count(*) FILTER (WHERE v = 1), count(*) FILTER (WHERE v = 0),
		count(*) FILTER (WHERE v > 1 OR v < 0) FROM items`).Scan(&once, &never, &twice); err != nil {
		t.Fatal(err)
	}
	return
}

func (f *fixture) batchDraft(t *testing.T, size int) *Migration {
	t.Helper()
	m, err := f.repo.CreateDraft(context.Background(), NewDraft{
		DatabaseID: f.dbID, Name: "bump items", SQL: "UPDATE items SET v = v + 1",
		SQLDown: "UPDATE items SET v = v - 1", CreatedBy: f.userID,
		ExecutionMode: ModeBatch, BatchAttribute: "id", BatchSize: size,
	})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	return m
}

func (f *fixture) enqueueType(t *testing.T, m *Migration, typ JobType) *Job {
	t.Helper()
	j, err := f.repo.EnqueueJob(context.Background(), EnqueueInput{
		MigrationID: m.ID, DatabaseID: m.DatabaseID, Type: typ, ExecutorID: f.userID, MarkQueued: true,
	})
	if err != nil {
		t.Fatalf("EnqueueJob(%s): %v", typ, err)
	}
	return j
}

func (f *fixture) batches(t *testing.T, id string) []Batch {
	t.Helper()
	list, err := f.repo.ListBatches(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// partial runs m once with a check constraint that makes the row failAt violate it, so the
// batch holding failAt fails and the run stops PARTIAL. It returns the PARTIAL job.
func (f *fixture) partial(t *testing.T, m *Migration, failAt int) *Job {
	t.Helper()
	// DDL cannot take bind parameters; failAt is an int, so formatting it in is safe.
	if _, err := f.target.Exec(`ALTER TABLE items ADD CONSTRAINT stop_here CHECK (id <> ` + strconv.Itoa(failAt) + ` OR v = 0)`); err != nil {
		t.Fatal(err)
	}
	j := f.enqueue(t, m)
	if _, err := fastWorker(f).RunOnce(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if got := f.job(t, j.ID); got.Status != JobPartial {
		t.Fatalf("job = %s (%s), want PARTIAL", got.Status, got.LastError)
	}
	return j
}

func TestBatchRunCommitsOneTransactionPerBatch(t *testing.T) {
	f := newFixture(t, Limits{})
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)
	j := f.enqueue(t, m)
	if _, err := fastWorker(f).RunOnce(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	if got := f.job(t, j.ID); got.Status != JobSuccess {
		t.Fatalf("job = %s (%s), want SUCCESS", got.Status, got.LastError)
	}
	got := f.migration(t, m.ID)
	if got.Status != StatusSuccess || got.Version != 1 || got.BatchesUp != 3 || got.BatchTotal != 3 ||
		got.CheckpointCursor == nil || *got.CheckpointCursor != 2500 {
		t.Fatalf("migration = %s v%d batches %d/%d cursor %v, want SUCCESS v1 3/3 cursor 2500",
			got.Status, got.Version, got.BatchesUp, got.BatchTotal, got.CheckpointCursor)
	}
	if once, never, twice := f.values(t); once != 2500 || never != 0 || twice != 0 {
		t.Fatalf("rows changed once/never/twice = %d/%d/%d, want 2500/0/0", once, never, twice)
	}
	list := f.batches(t, m.ID)
	wantTo := []int64{1000, 2000, 2500}
	for i, b := range list {
		if b.Direction != DirUp || b.No != i+1 || b.To != wantTo[i] || b.Rows != b.To-maybe(b.From) || b.ExecutionID == "" {
			t.Errorf("batch %d = %+v", i+1, b)
		}
	}
	if list[0].From != nil {
		t.Errorf("first batch from = %v, want nil", *list[0].From)
	}
	var onTarget int
	if err := f.target.QueryRow(`SELECT count(*) FROM ` + checkpointTable).Scan(&onTarget); err != nil || onTarget != 3 {
		t.Fatalf("target checkpoints = %d, %v; want 3", onTarget, err)
	}
	ex := f.executions(t, m.ID)
	if len(ex) != 1 || ex[0].Status != ExecSuccess || ex[0].ID != list[0].ExecutionID {
		t.Fatalf("executions = %+v, want one SUCCESS that owns the batches", ex)
	}
}

func maybe(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func TestBatchFailureIsPartialHoldsTheDatabaseAndResumes(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)
	f.partial(t, m, 1500) // batch 2 fails

	got := f.migration(t, m.ID)
	if got.Status != StatusPartial || got.BatchesUp != 1 || got.Error == "" {
		t.Fatalf("migration = %s batches %d error %q, want PARTIAL after batch 1", got.Status, got.BatchesUp, got.Error)
	}
	if once, never, _ := f.values(t); once != 1000 || never != 1500 {
		t.Fatalf("once/never = %d/%d, want 1000/1500 (batch 2 rolled back)", once, never)
	}
	ex := f.executions(t, m.ID)
	if len(ex) != 1 || ex[0].Status != ExecPartial || ex[0].SQLState != "23514" {
		t.Fatalf("executions = %+v, want one PARTIAL 23514", ex)
	}

	// PARTIAL holds every other migration of the database.
	other := f.enqueue(t, f.draft(t, "other", "CREATE TABLE other (x int)"))
	w := fastWorker(f)
	if worked, err := w.RunOnce(ctx, "w1"); err != nil || worked {
		t.Fatalf("RunOnce = %v, %v; another migration must wait while one is PARTIAL", worked, err)
	}

	// Fix the data, then resume: batches 2 and 3 run, batch 1 is not run again.
	if _, err := f.target.Exec(`ALTER TABLE items DROP CONSTRAINT stop_here`); err != nil {
		t.Fatal(err)
	}
	resume := f.enqueueType(t, m, JobResume)
	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if j := f.job(t, resume.ID); j.Status != JobSuccess {
		t.Fatalf("resume job = %s (%s), want SUCCESS", j.Status, j.LastError)
	}
	if once, never, twice := f.values(t); once != 2500 || never != 0 || twice != 0 {
		t.Fatalf("once/never/twice = %d/%d/%d, want 2500/0/0", once, never, twice)
	}
	if got := f.migration(t, m.ID); got.Status != StatusSuccess || got.BatchesUp != 3 {
		t.Fatalf("after resume = %s batches %d, want SUCCESS 3", got.Status, got.BatchesUp)
	}
	if ex := f.executions(t, m.ID); len(ex) != 2 || ex[1].Status != ExecSuccess {
		t.Fatalf("executions = %+v, want PARTIAL then SUCCESS", ex)
	}

	// The held migration runs now.
	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if j := f.job(t, other.ID); j.Status != JobSuccess {
		t.Fatalf("held job = %s, want SUCCESS once the batch migration finished", j.Status)
	}
}

func TestBatchCommittedBeforeACrashIsNotRunAgain(t *testing.T) {
	f := newFixture(t, Limits{})
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)
	f.partial(t, m, 1500)

	// Simulate a crash right after batch 2 committed on the target: its data change and its
	// checkpoint exist there, but the platform never heard of it.
	if _, err := f.target.Exec(`ALTER TABLE items DROP CONSTRAINT stop_here;
		UPDATE items SET v = v + 1 WHERE id > 1000 AND id <= 2000;
		INSERT INTO ` + checkpointTable + ` (migration_id, direction, batch_no, cursor_from, cursor_to, rows_affected)
		VALUES ('` + m.ID + `', 'UP', 2, 1000, 2000, 1000)`); err != nil {
		t.Fatal(err)
	}
	if got := f.migration(t, m.ID); got.BatchesUp != 1 {
		t.Fatalf("platform batches = %d, want 1 before the resume", got.BatchesUp)
	}

	f.enqueueType(t, m, JobResume)
	if _, err := fastWorker(f).RunOnce(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	// Batch 2 was copied from the target checkpoint, so only batch 3 ran.
	if once, never, twice := f.values(t); once != 2500 || never != 0 || twice != 0 {
		t.Fatalf("once/never/twice = %d/%d/%d, want 2500/0/0 (no batch twice)", once, never, twice)
	}
	list := f.batches(t, m.ID)
	if len(list) != 3 || list[1].ExecutionID != "" || list[2].ExecutionID == "" {
		t.Fatalf("batches = %+v, want batch 2 recovered (no execution) and batch 3 run", list)
	}
}

func TestAbortUndoesBatchesNewestFirst(t *testing.T) {
	f := newFixture(t, Limits{})
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)
	f.partial(t, m, 2100) // batches 1 and 2 committed, batch 3 failed
	if _, err := f.target.Exec(`ALTER TABLE items DROP CONSTRAINT stop_here`); err != nil {
		t.Fatal(err)
	}

	j := f.enqueueType(t, m, JobAbort)
	if _, err := fastWorker(f).RunOnce(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if got := f.job(t, j.ID); got.Status != JobSuccess {
		t.Fatalf("abort job = %s (%s), want SUCCESS", got.Status, got.LastError)
	}
	if got := f.migration(t, m.ID); got.Status != StatusAborted || got.BatchesDown != 2 {
		t.Fatalf("migration = %s down %d, want ABORTED with 2 batches undone", got.Status, got.BatchesDown)
	}
	if once, never, _ := f.values(t); once != 0 || never != 2500 {
		t.Fatalf("once/never = %d/%d, want 0/2500 (every batch undone)", once, never)
	}
	var down []Batch
	for _, b := range f.batches(t, m.ID) {
		if b.Direction == DirDown {
			down = append(down, b)
		}
	}
	if len(down) != 2 || !down[1].CommittedAt.Before(down[0].CommittedAt) {
		t.Fatalf("down batches = %+v, want batch 2 undone before batch 1", down)
	}
	ex := f.executions(t, m.ID)
	last := ex[len(ex)-1]
	if last.Kind != ExecRollback || last.Status != ExecSuccess || last.RevertsExecutionID != ex[0].ID {
		t.Fatalf("abort execution = %+v, want ROLLBACK SUCCESS reverting %s", last, ex[0].ID)
	}
}

func TestAbortThatStopsIsPartiallyReverted(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)
	f.partial(t, m, 2100)
	// Undoing batch 1 will fail on row 500; batch 2 can be undone.
	if _, err := f.target.Exec(`ALTER TABLE items DROP CONSTRAINT stop_here;
		ALTER TABLE items ADD CONSTRAINT keep_500 CHECK (id <> 500 OR v = 1)`); err != nil {
		t.Fatal(err)
	}
	w := fastWorker(f)
	abort := f.enqueueType(t, m, JobAbort)
	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if got := f.job(t, abort.ID); got.Status != JobPartial {
		t.Fatalf("abort job = %s (%s), want PARTIAL", got.Status, got.LastError)
	}
	if got := f.migration(t, m.ID); got.Status != StatusPartiallyReverted || got.BatchesUp != 2 || got.BatchesDown != 1 {
		t.Fatalf("migration = %s up %d down %d, want PARTIALLY_REVERTED 2/1", got.Status, got.BatchesUp, got.BatchesDown)
	}

	// Resuming forward is no longer allowed: the abort must be finished.
	resume := f.enqueueType(t, m, JobResume)
	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if got := f.job(t, resume.ID); got.Status != JobFailed || got.LastError != ErrBatchReverting.Error() {
		t.Fatalf("resume job = %s (%s), want FAILED with ErrBatchReverting", got.Status, got.LastError)
	}
	if got := f.migration(t, m.ID); got.Status != StatusPartiallyReverted {
		t.Fatalf("after refused resume = %s, want PARTIALLY_REVERTED again", got.Status)
	}

	if _, err := f.target.Exec(`ALTER TABLE items DROP CONSTRAINT keep_500`); err != nil {
		t.Fatal(err)
	}
	f.enqueueType(t, m, JobAbort)
	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if got := f.migration(t, m.ID); got.Status != StatusAborted {
		t.Fatalf("after second abort = %s, want ABORTED", got.Status)
	}
	if once, never, _ := f.values(t); once != 0 || never != 2500 {
		t.Fatalf("once/never = %d/%d, want 0/2500", once, never)
	}
}

// cancelOnBatch cancels a context once the engine logs a committed batch: a shutdown signal
// that arrives in the middle of a batch run.
type cancelOnBatch struct {
	slog.Handler
	cancel func()
}

func (h cancelOnBatch) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "migration.batch" {
		h.cancel()
	}
	return h.Handler.Handle(ctx, r)
}

func TestShutdownPausesABatchRunAtACheckpoint(t *testing.T) {
	f := newFixture(t, Limits{})
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)
	j := f.enqueue(t, m)

	ctx, cancel := context.WithCancel(context.Background())
	f.engine.SetLogger(slog.New(cancelOnBatch{Handler: slog.Default().Handler(), cancel: cancel}))
	w := fastWorker(f)
	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	got := f.job(t, j.ID)
	if got.Status != JobQueued || got.Attempt != 0 {
		t.Fatalf("job = %s attempt %d, want QUEUED with the attempt given back", got.Status, got.Attempt)
	}
	if mm := f.migration(t, m.ID); mm.Status != StatusQueued || mm.BatchesUp != 1 {
		t.Fatalf("migration = %s batches %d, want QUEUED after 1 batch", mm.Status, mm.BatchesUp)
	}

	f.engine.SetLogger(slog.Default())
	if _, err := w.RunOnce(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if got := f.job(t, j.ID); got.Status != JobSuccess {
		t.Fatalf("job after restart = %s (%s), want SUCCESS", got.Status, got.LastError)
	}
	if once, never, twice := f.values(t); once != 2500 || never != 0 || twice != 0 {
		t.Fatalf("once/never/twice = %d/%d/%d, want 2500/0/0", once, never, twice)
	}
	ex := f.executions(t, m.ID)
	if len(ex) != 2 || ex[0].Status != ExecPartial || ex[1].Status != ExecSuccess {
		t.Fatalf("executions = %+v, want PARTIAL (paused) then SUCCESS", ex)
	}
}

func TestStaleBatchJobContinuesInsteadOfUncertain(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	f.items(t, 1500)
	m := f.batchDraft(t, 1000)
	j := f.enqueue(t, m)
	if _, err := f.repo.ClaimNextJob(ctx, "dead-worker", time.Minute); err != nil {
		t.Fatal(err)
	}
	f.expireLease(t, j.ID, true) // died after sending SQL

	w := fastWorker(f)
	if _, err := w.RecoverStaleJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.job(t, j.ID); got.Status != JobQueued {
		t.Fatalf("job = %s, want QUEUED: checkpoints make a batch run safe to continue", got.Status)
	}
	if ex := f.executions(t, m.ID); len(ex) != 1 || ex[0].Status != ExecPartial || !ex[0].SQLSent {
		t.Fatalf("executions = %+v, want one PARTIAL attempt", ex)
	}
	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if got := f.job(t, j.ID); got.Status != JobSuccess {
		t.Fatalf("job = %s (%s), want SUCCESS", got.Status, got.LastError)
	}
}

func TestBatchDraftValidation(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	draft := func(up, down, attr string, size int) error {
		_, err := f.repo.CreateDraft(ctx, NewDraft{DatabaseID: f.dbID, Name: "x", SQL: up, SQLDown: down,
			CreatedBy: f.userID, ExecutionMode: ModeBatch, BatchAttribute: attr, BatchSize: size})
		return err
	}
	if err := draft("INSERT INTO items VALUES (1)", "", "id", 10); !errors.Is(err, ErrBatchScript) {
		t.Errorf("insert = %v, want ErrBatchScript", err)
	}
	if err := draft("UPDATE items SET v = 1", "DROP TABLE items", "id", 10); !errors.Is(err, ErrBatchScript) {
		t.Errorf("bad SQL Down = %v, want ErrBatchScript", err)
	}
	if err := draft("UPDATE items SET v = 1", "", "Id", 10); !errors.Is(err, ErrBatchConfig) {
		t.Errorf("attribute = %v, want ErrBatchConfig", err)
	}
	if err := draft("UPDATE items SET v = 1", "", "id", MaxBatchSize+1); !errors.Is(err, ErrBatchConfig) {
		t.Errorf("size = %v, want ErrBatchConfig", err)
	}
	m, err := f.repo.CreateDraft(ctx, NewDraft{DatabaseID: f.dbID, Name: "y", SQL: "UPDATE items SET v = 1",
		CreatedBy: f.userID, ExecutionMode: ModeBatch, BatchAttribute: "id"})
	if err != nil || m.BatchSize != DefaultBatchSize || m.ExecutionMode != ModeBatch {
		t.Fatalf("default size: %+v, %v", m, err)
	}
}
