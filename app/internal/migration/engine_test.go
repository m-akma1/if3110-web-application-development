package migration

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"dbplatform/app/internal/dbmanager"
)

func TestExecuteCommitsAndAssignsVersions(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()

	first := f.draft(t, "create orders", "CREATE TABLE orders (id int PRIMARY KEY);\nINSERT INTO orders VALUES (1), (2);")
	m, res, err := f.engine.Execute(ctx, first.ID, f.userID)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.OK || res.Err != nil || res.Statements != 2 {
		t.Fatalf("result = %+v, want OK with 2 statements", res)
	}
	if m.Status != StatusSuccess || m.Version != 1 || m.Error != "" {
		t.Fatalf("migration = status %s version %d error %q, want SUCCESS v1", m.Status, m.Version, m.Error)
	}
	if m.ExecutedBy != f.userID || m.ExecutedByName != "engineer" || m.ExecutedAt.IsZero() || m.DurationMs < 0 {
		t.Fatalf("execution data not recorded: %+v", m)
	}
	if !f.targetHasTable(t, "orders") {
		t.Fatal("orders should exist on the target after COMMIT")
	}
	var n int
	if err := f.target.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("orders rows = %d (%v), want 2", n, err)
	}

	second := f.draft(t, "add total", "ALTER TABLE orders ADD COLUMN total numeric NOT NULL DEFAULT 0")
	m, _, err = f.engine.Execute(ctx, second.ID, f.userID)
	if err != nil || m.Status != StatusSuccess || m.Version != 2 {
		t.Fatalf("second Execute = %+v, %v; want SUCCESS v2", m, err)
	}
}

func TestExecuteFailureRollsBackEverything(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()

	d := f.draft(t, "half broken", "CREATE TABLE t (x int);\nINSERT INTO missing VALUES (1);")
	m, res, err := f.engine.Execute(ctx, d.ID, f.userID)
	if err != nil {
		t.Fatalf("a failing script is a recorded result, not an error: %v", err)
	}
	if res.OK || res.Err == nil {
		t.Fatalf("result = %+v, want failure", res)
	}
	if res.Err.Code != "42P01" || res.Err.Line != 2 || res.Err.Column != 13 || res.Err.Statement != 2 {
		t.Fatalf("error = %+v, want 42P01 at statement 2, line 2, column 13", res.Err)
	}
	if m.Status != StatusFailed || m.Version != 0 || !strings.Contains(m.Error, "42P01") || m.ExecutedAt.IsZero() {
		t.Fatalf("migration = status %s version %d error %q, want FAILED without version", m.Status, m.Version, m.Error)
	}
	if f.targetHasTable(t, "t") {
		t.Fatal("table t must not exist: the first statement has to be rolled back with the second")
	}

	// A failed run does not consume a version.
	ok := f.draft(t, "fixed", "CREATE TABLE t (x int)")
	m, _, err = f.engine.Execute(ctx, ok.ID, f.userID)
	if err != nil || m.Version != 1 {
		t.Fatalf("Execute after a failure = %+v, %v; want version 1", m, err)
	}
}

func TestDryRunNeverChangesTheTarget(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()

	res, err := f.engine.DryRun(ctx, f.dbID, "CREATE TABLE d (x int); INSERT INTO d VALUES (1);")
	if err != nil || !res.OK || res.Statements != 2 {
		t.Fatalf("DryRun = %+v, %v; want OK with 2 statements", res, err)
	}
	if f.targetHasTable(t, "d") {
		t.Fatal("dry run must roll back")
	}

	res, err = f.engine.DryRun(ctx, f.dbID, "CREATE TABLE d (x int);\nSELECT nope FROM d;")
	if err != nil || res.OK || res.Err == nil || res.Err.Code != "42703" || res.Err.Statement != 2 {
		t.Fatalf("failing DryRun = %+v (%+v), %v; want 42703 at statement 2", res, res.Err, err)
	}

	if _, err := f.engine.DryRun(ctx, f.dbID, "BEGIN; CREATE TABLE d (x int); COMMIT;"); !errors.Is(err, ErrScriptRejected) {
		t.Fatalf("DryRun with COMMIT = %v, want ErrScriptRejected", err)
	}
	if _, err := f.engine.DryRun(ctx, "00000000-0000-0000-0000-00000000ffff", "SELECT 1"); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("DryRun on unknown database = %v, want ErrTargetUnavailable", err)
	}

	// Nothing about dry runs is written to the history.
	list, err := f.repo.ListByDatabase(ctx, f.dbID)
	if err != nil || len(list) != 0 {
		t.Fatalf("history after dry runs = %d rows (%v), want 0", len(list), err)
	}
}

func TestConcurrentExecutesGetDistinctVersions(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()

	const n = 4
	drafts := make([]*Migration, n)
	for i := range drafts {
		drafts[i] = f.draft(t, "parallel", "CREATE TABLE IF NOT EXISTS p (x int); INSERT INTO p VALUES (1); SELECT pg_sleep(0.05);")
	}
	versions := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i, d := range drafts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, _, err := f.engine.Execute(ctx, d.ID, f.userID)
			errs[i] = err
			if m != nil {
				versions[i] = m.Version
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	sort.Ints(versions)
	for i, v := range versions {
		if v != i+1 {
			t.Fatalf("versions = %v, want 1..%d with no gaps or duplicates", versions, n)
		}
	}
}

func TestSameDraftExecutesOnce(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	d := f.draft(t, "once", "CREATE TABLE once (x int); SELECT pg_sleep(0.1);")

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = f.engine.Execute(ctx, d.ID, f.userID)
		}()
	}
	wg.Wait()
	ok, notDraft := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrNotDraft):
			notDraft++
		default:
			t.Fatalf("Execute: %v", err)
		}
	}
	if ok != 1 || notDraft != 1 {
		t.Fatalf("got %d successes and %d ErrNotDraft, want exactly one of each", ok, notDraft)
	}
}

func TestExecutedMigrationsAreImmutable(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	d := f.draft(t, "done", "CREATE TABLE done (x int)")
	if _, _, err := f.engine.Execute(ctx, d.ID, f.userID); err != nil {
		t.Fatal(err)
	}

	if _, _, err := f.engine.Execute(ctx, d.ID, f.userID); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("re-execute = %v, want ErrNotDraft", err)
	}
	if _, err := f.repo.UpdateDraft(ctx, d.ID, "edited", "DROP TABLE done"); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("UpdateDraft after execute = %v, want ErrNotDraft", err)
	}
	if err := f.repo.DeleteDraft(ctx, d.ID); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("DeleteDraft after execute = %v, want ErrNotDraft", err)
	}
	// The database refuses direct edits too.
	if _, err := f.platform.ExecContext(ctx, `UPDATE migrations SET name = 'rewritten' WHERE id = $1`, d.ID); err == nil {
		t.Fatal("UPDATE of an executed migration should be blocked by the trigger")
	}
}

func TestExecuteRejectsWithoutRunning(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()

	rejected := f.draft(t, "concurrent index", "CREATE TABLE r (x int); CREATE INDEX CONCURRENTLY r_x ON r (x);")
	var rej *RejectedError
	if _, _, err := f.engine.Execute(ctx, rejected.ID, f.userID); !errors.As(err, &rej) || rej.Statement != 2 {
		t.Fatalf("Execute = %v, want *RejectedError at statement 2", err)
	}
	if f.targetHasTable(t, "r") {
		t.Fatal("a rejected script must not run at all")
	}

	noPools := NewEngine(f.platform, dbmanager.NewConnectionRegistry(), Limits{})
	offline := f.draft(t, "offline", "CREATE TABLE o (x int)")
	if _, _, err := noPools.Execute(ctx, offline.ID, f.userID); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("Execute without a pool = %v, want ErrTargetUnavailable", err)
	}

	for _, id := range []string{rejected.ID, offline.ID} {
		m, err := f.repo.Get(ctx, id)
		if err != nil || m.Status != StatusDraft {
			t.Fatalf("migration %s = %+v, %v; want it to stay DRAFT", id, m, err)
		}
	}
	if _, _, err := f.engine.Execute(ctx, "not-a-uuid", f.userID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Execute(bad id) = %v, want ErrNotFound", err)
	}
}

func TestExecuteWaitsForTheTargetLock(t *testing.T) {
	f := newFixture(t, Limits{QueueTimeout: 300 * time.Millisecond})
	ctx := context.Background()

	// Hold the per-target lock from another session, as a concurrent Execute would.
	holder, err := f.platform.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_lock($1::int, hashtext($2::text))`, lockNamespace, f.dbID); err != nil {
		t.Fatal(err)
	}

	d := f.draft(t, "queued", "CREATE TABLE q (x int)")
	start := time.Now()
	if _, _, err := f.engine.Execute(ctx, d.ID, f.userID); !errors.Is(err, ErrTargetBusy) {
		t.Fatalf("Execute while locked = %v, want ErrTargetBusy", err)
	}
	if waited := time.Since(start); waited < 300*time.Millisecond {
		t.Fatalf("gave up after %s, want at least the queue timeout", waited)
	}

	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_unlock($1::int, hashtext($2::text))`, lockNamespace, f.dbID); err != nil {
		t.Fatal(err)
	}
	if m, _, err := f.engine.Execute(ctx, d.ID, f.userID); err != nil || m.Status != StatusSuccess {
		t.Fatalf("Execute after unlock = %+v, %v; want SUCCESS", m, err)
	}
}

func TestStatementTimeoutFailsTheRun(t *testing.T) {
	f := newFixture(t, Limits{StatementTimeout: 200 * time.Millisecond})
	d := f.draft(t, "slow", "CREATE TABLE slow (x int); SELECT pg_sleep(2);")
	m, res, err := f.engine.Execute(context.Background(), d.ID, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Err.Code != "57014" || m.Status != StatusFailed {
		t.Fatalf("result = %+v, migration status %s; want FAILED with 57014 (query_canceled)", res.Err, m.Status)
	}
	if f.targetHasTable(t, "slow") {
		t.Fatal("a timed-out run must roll back")
	}
}

func TestCommittedSessionSettingsAreReset(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	f.target.SetMaxOpenConns(1) // the next query reuses the connection the migration ran on

	var before string
	if err := f.target.QueryRowContext(ctx, `SHOW work_mem`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	d := f.draft(t, "sets work_mem", "SET work_mem = '7MB'; CREATE TABLE s (x int);")
	if m, _, err := f.engine.Execute(ctx, d.ID, f.userID); err != nil || m.Status != StatusSuccess {
		t.Fatalf("Execute = %+v, %v", m, err)
	}
	var after string
	if err := f.target.QueryRowContext(ctx, `SHOW work_mem`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("work_mem leaked into the pool: before %s, after %s", before, after)
	}
}
