package migration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/ids"
	"dbplatform/internal/testutil"
)

// fastWorker polls often and retries quickly so tests finish fast.
func fastWorker(f *fixture) *Worker {
	return NewWorker(f.repo, f.engine, WorkerConfig{
		Concurrency: 2, PollInterval: 20 * time.Millisecond, Lease: 3 * time.Second,
		BaseBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, ReapInterval: time.Hour,
	})
}

func (f *fixture) enqueue(t *testing.T, m *Migration) *Job {
	t.Helper()
	j, err := f.repo.EnqueueJob(context.Background(), EnqueueInput{
		MigrationID: m.ID, DatabaseID: m.DatabaseID, Type: JobExecute, ExecutorID: f.userID, MarkQueued: true,
	})
	if err != nil {
		t.Fatalf("EnqueueJob(%s): %v", m.Name, err)
	}
	return j
}

func (f *fixture) job(t *testing.T, id string) *Job {
	t.Helper()
	j, err := f.repo.GetJob(context.Background(), id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	return j
}

func (f *fixture) migration(t *testing.T, id string) *Migration {
	t.Helper()
	m, err := f.repo.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return m
}

// secondTarget registers another empty target database and returns its ID.
func (f *fixture) secondTarget(t *testing.T) string {
	t.Helper()
	id := accesstest.InsertTarget(t, f.platform, "other-"+ids.New()[:8])
	f.pools.RegisterPool(id, testutil.NewSchemaDB(t))
	return id
}

func TestEnqueueMarksQueuedAndRejectsDuplicates(t *testing.T) {
	f := newFixture(t, Limits{})
	d := f.draft(t, "orders", "CREATE TABLE orders (id int)")
	j := f.enqueue(t, d)

	if j.Status != JobQueued || j.Attempt != 0 || j.FromStatus != StatusDraft {
		t.Fatalf("job = %+v, want QUEUED attempt 0 from DRAFT", j)
	}
	if m := f.migration(t, d.ID); m.Status != StatusQueued {
		t.Fatalf("migration status = %s, want QUEUED", m.Status)
	}
	// A double click or a retried request must not create a second job.
	_, err := f.repo.EnqueueJob(context.Background(), EnqueueInput{
		MigrationID: d.ID, DatabaseID: d.DatabaseID, Type: JobExecute, MarkQueued: true,
	})
	if !errors.Is(err, ErrJobActive) {
		t.Fatalf("second enqueue err = %v, want ErrJobActive", err)
	}
}

func TestWorkerRunsQueuedJob(t *testing.T) {
	f := newFixture(t, Limits{})
	d := f.draft(t, "orders", "CREATE TABLE orders (id int)")
	j := f.enqueue(t, d)

	worked, err := fastWorker(f).RunOnce(context.Background(), "w1")
	if err != nil || !worked {
		t.Fatalf("RunOnce = %v, %v; want a processed job", worked, err)
	}
	if got := f.job(t, j.ID); got.Status != JobSuccess || got.Attempt != 1 || got.FinishedAt == nil || got.SQLStartedAt == nil {
		t.Fatalf("job = %+v, want SUCCESS after attempt 1 with sql_started_at", got)
	}
	if m := f.migration(t, d.ID); m.Status != StatusSuccess || m.Version != 1 {
		t.Fatalf("migration = %s v%d, want SUCCESS v1", m.Status, m.Version)
	}
	if !f.targetHasTable(t, "orders") {
		t.Fatal("orders should exist after the job ran")
	}
	if worked, _ := fastWorker(f).RunOnce(context.Background(), "w1"); worked {
		t.Fatal("queue should be empty")
	}
}

func TestScriptFailureIsFinalAndNotRetried(t *testing.T) {
	f := newFixture(t, Limits{})
	d := f.draft(t, "broken", "INSERT INTO missing VALUES (1)")
	j := f.enqueue(t, d)

	if _, err := fastWorker(f).RunOnce(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	got := f.job(t, j.ID)
	if got.Status != JobFailed || got.Attempt != 1 || got.LastError == "" {
		t.Fatalf("job = %+v, want FAILED after one attempt with the SQL error", got)
	}
	if m := f.migration(t, d.ID); m.Status != StatusFailed {
		t.Fatalf("migration status = %s, want FAILED", m.Status)
	}
}

func TestClaimRunsOneJobPerDatabase(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	a1 := f.enqueue(t, f.draft(t, "a1", "SELECT 1"))
	f.enqueue(t, f.draft(t, "a2", "SELECT 2"))
	otherDB := f.secondTarget(t)
	b, err := f.repo.CreateDraft(ctx, NewDraft{DatabaseID: otherDB, Name: "b1", SQL: "SELECT 3", CreatedBy: f.userID})
	if err != nil {
		t.Fatal(err)
	}
	b1 := f.enqueue(t, b)

	first, err := f.repo.ClaimNextJob(ctx, "w1", time.Minute)
	if err != nil || first == nil || first.ID != a1.ID {
		t.Fatalf("first claim = %+v, %v; want the oldest job a1", first, err)
	}
	// a2 is on the same database as the RUNNING a1, so the next claim skips it and takes b1.
	second, err := f.repo.ClaimNextJob(ctx, "w2", time.Minute)
	if err != nil || second == nil || second.ID != b1.ID {
		t.Fatalf("second claim = %+v, %v; want b1 on the other database", second, err)
	}
	if third, err := f.repo.ClaimNextJob(ctx, "w3", time.Minute); err != nil || third != nil {
		t.Fatalf("third claim = %+v, %v; want nothing (a2 must wait for a1)", third, err)
	}
}

func TestConcurrentClaimsNeverRunTwoJobsOnOneDatabase(t *testing.T) {
	f := newFixture(t, Limits{})
	for i := 0; i < 5; i++ {
		f.enqueue(t, f.draft(t, "same-db", "SELECT 1"))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	claimed := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			j, err := f.repo.ClaimNextJob(context.Background(), "w", time.Minute)
			if err != nil {
				t.Error(err)
			}
			if j != nil {
				mu.Lock()
				claimed++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if claimed != 1 {
		t.Fatalf("%d jobs of one database were claimed at once, want 1", claimed)
	}
}

func TestPoolRunsDatabasesInParallelAndOneDatabaseSerially(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	same1 := f.enqueue(t, f.draft(t, "same-1", "SELECT pg_sleep(0.4)"))
	same2 := f.enqueue(t, f.draft(t, "same-2", "SELECT pg_sleep(0.4)"))
	otherDB := f.secondTarget(t)
	b, err := f.repo.CreateDraft(ctx, NewDraft{DatabaseID: otherDB, Name: "other", SQL: "SELECT pg_sleep(0.4)", CreatedBy: f.userID})
	if err != nil {
		t.Fatal(err)
	}
	other := f.enqueue(t, b)

	w := fastWorker(f)
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if f.job(t, same1.ID).Done() && f.job(t, same2.ID).Done() && f.job(t, other.ID).Done() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("jobs did not finish in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	m1, m2 := f.migration(t, same1.MigrationID), f.migration(t, same2.MigrationID)
	if m1.Status != StatusSuccess || m2.Status != StatusSuccess || m1.Version == m2.Version {
		t.Fatalf("same-db migrations = %s v%d, %s v%d; want two SUCCESS with distinct versions", m1.Status, m1.Version, m2.Status, m2.Version)
	}
	// FIFO and serial on one database: same-2 runs after same-1 is recorded and gets the next version.
	if m2.Version != m1.Version+1 || !m2.ExecutedAt.After(m1.ExecutedAt.Add(350*time.Millisecond)) {
		t.Fatalf("same-db runs out of order or overlapped: v%d at %v, v%d at %v", m1.Version, m1.ExecutedAt, m2.Version, m2.ExecutedAt)
	}
	// Parallel across databases: the other database's run started while same-1 was still
	// running (executed_at is the start of a run; each run takes 0.4 s).
	o := f.migration(t, other.MigrationID)
	if o.Status != StatusSuccess || !o.ExecutedAt.Before(m1.ExecutedAt.Add(400*time.Millisecond)) {
		t.Fatalf("other database started at %v, same-1 at %v; want them to overlap", o.ExecutedAt, m1.ExecutedAt)
	}
}

func TestTransientErrorIsRetriedThenFailsAndRestoresStatus(t *testing.T) {
	f := newFixture(t, Limits{})
	d := f.draft(t, "orders", "CREATE TABLE orders (id int)")
	j := f.enqueue(t, d)
	f.pools.Remove(f.dbID) // the target is down: ErrTargetUnavailable before any SQL
	w := fastWorker(f)
	ctx := context.Background()

	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	got := f.job(t, j.ID)
	if got.Status != JobQueued || got.Attempt != 1 || !got.NextRunAt.After(got.UpdatedAt.Add(-time.Second)) {
		t.Fatalf("after attempt 1 job = %+v, want QUEUED for a retry", got)
	}
	if m := f.migration(t, d.ID); m.Status != StatusQueued {
		t.Fatalf("migration status = %s, want QUEUED while waiting for the retry", m.Status)
	}

	for i := 0; i < 10 && !f.job(t, j.ID).Done(); i++ {
		time.Sleep(60 * time.Millisecond) // wait for next_run_at
		if _, err := w.RunOnce(ctx, "w1"); err != nil {
			t.Fatal(err)
		}
	}
	got = f.job(t, j.ID)
	if got.Status != JobFailed || got.Attempt != got.MaxAttempts {
		t.Fatalf("final job = %+v, want FAILED after %d attempts", got, got.MaxAttempts)
	}
	// No SQL ever ran, so the user can execute the draft again later.
	if m := f.migration(t, d.ID); m.Status != StatusDraft {
		t.Fatalf("migration status = %s, want DRAFT restored", m.Status)
	}
}

// expireLease makes a RUNNING job look abandoned by a dead worker.
func (f *fixture) expireLease(t *testing.T, jobID string, sqlStarted bool) {
	t.Helper()
	q := `UPDATE migration_jobs SET lease_expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`
	if sqlStarted {
		q = `UPDATE migration_jobs SET lease_expires_at = NOW() - INTERVAL '1 minute', sql_started_at = NOW() WHERE id = $1`
	}
	if _, err := f.platform.ExecContext(context.Background(), q, jobID); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverStaleJobs(t *testing.T) {
	ctx := context.Background()

	t.Run("no SQL sent yet: back to the queue and runs after restart", func(t *testing.T) {
		f := newFixture(t, Limits{})
		j := f.enqueue(t, f.draft(t, "orders", "CREATE TABLE orders (id int)"))
		if _, err := f.repo.ClaimNextJob(ctx, "dead-worker", time.Minute); err != nil {
			t.Fatal(err)
		}
		f.expireLease(t, j.ID, false)

		w := fastWorker(f) // a fresh process after the restart
		if n, err := w.RecoverStaleJobs(ctx); err != nil || n != 1 {
			t.Fatalf("RecoverStaleJobs = %d, %v; want 1", n, err)
		}
		if got := f.job(t, j.ID); got.Status != JobQueued {
			t.Fatalf("job status = %s, want QUEUED", got.Status)
		}
		if _, err := w.RunOnce(ctx, "w1"); err != nil {
			t.Fatal(err)
		}
		if got := f.job(t, j.ID); got.Status != JobSuccess || got.Attempt != 2 {
			t.Fatalf("job = %+v, want SUCCESS on attempt 2", got)
		}
	})

	t.Run("outcome already recorded: closed without running again", func(t *testing.T) {
		f := newFixture(t, Limits{})
		d := f.draft(t, "orders", "CREATE TABLE orders (id int)")
		j := f.enqueue(t, d)
		if _, err := f.repo.ClaimNextJob(ctx, "dead-worker", time.Minute); err != nil {
			t.Fatal(err)
		}
		// The worker ran the migration and wrote the history, then died before closing the job.
		if _, _, err := f.engine.Execute(WithRun(ctx, j.ID, 1), d.ID, f.userID); err != nil {
			t.Fatal(err)
		}
		f.expireLease(t, j.ID, true)

		if _, err := fastWorker(f).RecoverStaleJobs(ctx); err != nil {
			t.Fatal(err)
		}
		if got := f.job(t, j.ID); got.Status != JobSuccess {
			t.Fatalf("job status = %s, want SUCCESS", got.Status)
		}
		if m := f.migration(t, d.ID); m.Version != 1 {
			t.Fatalf("version = %d, want 1 (no second run)", m.Version)
		}
	})

	t.Run("SQL sent but no outcome: UNCERTAIN holds the database", func(t *testing.T) {
		f := newFixture(t, Limits{})
		j := f.enqueue(t, f.draft(t, "orders", "CREATE TABLE orders (id int)"))
		next := f.enqueue(t, f.draft(t, "next", "SELECT 1"))
		if _, err := f.repo.ClaimNextJob(ctx, "dead-worker", time.Minute); err != nil {
			t.Fatal(err)
		}
		f.expireLease(t, j.ID, true)

		w := fastWorker(f)
		if _, err := w.RecoverStaleJobs(ctx); err != nil {
			t.Fatal(err)
		}
		if got := f.job(t, j.ID); got.Status != JobUncertain {
			t.Fatalf("job status = %s, want UNCERTAIN", got.Status)
		}
		if worked, err := w.RunOnce(ctx, "w1"); err != nil || worked {
			t.Fatalf("RunOnce = %v, %v; the next job on this database must be held", worked, err)
		}
		if got := f.job(t, next.ID); got.Status != JobQueued {
			t.Fatalf("next job status = %s, want QUEUED (held)", got.Status)
		}
	})
}

func TestHeartbeatKeepsLongJobFromBeingReaped(t *testing.T) {
	f := newFixture(t, Limits{})
	j := f.enqueue(t, f.draft(t, "slow", "SELECT pg_sleep(1.2)"))
	w := NewWorker(f.repo, f.engine, WorkerConfig{Lease: 600 * time.Millisecond, PollInterval: 20 * time.Millisecond})
	ctx := context.Background()

	done := make(chan struct{})
	go func() { _, _ = w.RunOnce(ctx, "w1"); close(done) }()
	time.Sleep(900 * time.Millisecond) // longer than one lease
	if n, err := w.RecoverStaleJobs(ctx); err != nil || n != 0 {
		t.Fatalf("RecoverStaleJobs = %d, %v; a job with a live heartbeat must not be reaped", n, err)
	}
	<-done
	if got := f.job(t, j.ID); got.Status != JobSuccess {
		t.Fatalf("job status = %s, want SUCCESS", got.Status)
	}
}
