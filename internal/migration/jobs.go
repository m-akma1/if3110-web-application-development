package migration

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"dbplatform/internal/ids"
	"dbplatform/internal/metrics"
	"dbplatform/internal/tracing"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// JobType is what a job asks the engine to do.
type JobType string

const (
	JobExecute  JobType = "EXECUTE"  // run a draft or an approved migration (also approved rollback/forward drafts)
	JobRollback JobType = "ROLLBACK" // roll back an executed migration directly (no approval needed)
	JobForward  JobType = "FORWARD"  // re-apply a rolled-back migration directly (no approval needed)
	JobResume   JobType = "RESUME"   // continue a PARTIAL batch migration from its checkpoint
	JobAbort    JobType = "ABORT"    // undo the committed batches of a PARTIAL migration, newest first
)

// JobStatus is the lifecycle state of a job.
type JobStatus string

const (
	JobQueued    JobStatus = "QUEUED"
	JobRunning   JobStatus = "RUNNING"
	JobSuccess   JobStatus = "SUCCESS"
	JobFailed    JobStatus = "FAILED"
	JobUncertain JobStatus = "UNCERTAIN" // SQL may have run but the outcome was not recorded
	JobPartial   JobStatus = "PARTIAL"   // a batch run stopped with some batches committed
)

// ErrJobActive means the migration already has an unfinished job.
var ErrJobActive = errors.New("migration ini sudah ada di antrean atau sedang berjalan")

// claimLockNamespace is the first advisory lock key used while claiming a job for a database.
// It differs from lockNamespace (the engine's run lock), so claiming never waits on a running job.
const claimLockNamespace int32 = 0x4d4a // "MJ"

// Job is one queued run of a migration.
type Job struct {
	ID             string
	MigrationID    string
	DatabaseID     string
	Type           JobType
	ExecutorID     string
	Status         JobStatus
	FromStatus     Status
	Attempt        int
	MaxAttempts    int
	NextRunAt      time.Time
	LockedBy       string
	LeaseExpiresAt *time.Time
	SQLStartedAt   *time.Time
	LastError      string
	CorrelationID  string
	TraceParent    string // W3C traceparent of the job.enqueue span; the worker continues the trace
	CreatedAt      time.Time
	UpdatedAt      time.Time
	FinishedAt     *time.Time
}

// Done reports whether the job reached a final state.
func (j *Job) Done() bool {
	return j.Status == JobSuccess || j.Status == JobFailed || j.Status == JobPartial
}

// changesMigrationStatus reports job types that move their migration through QUEUED and
// RUNNING. Direct rollback/forward jobs leave the executed migration's status alone.
func (t JobType) changesMigrationStatus() bool {
	return t == JobExecute || t == JobResume || t == JobAbort
}

// EnqueueInput describes a job to add to the queue.
type EnqueueInput struct {
	MigrationID   string
	DatabaseID    string
	Type          JobType
	ExecutorID    string
	MaxAttempts   int    // 0 means 3
	CorrelationID string // request ID, so worker logs share it with the HTTP request
	// MarkQueued moves the migration itself to QUEUED (EXECUTE jobs). Direct rollback and
	// forward jobs leave the executed migration's status alone.
	MarkQueued bool
	// BatchRequestID is the APPROVED batch action request this job runs (RESUME/ABORT). It
	// becomes DONE in the same transaction, so an approval runs at most once.
	BatchRequestID string
}

// EnqueueJob stores a QUEUED job. With MarkQueued the migration moves to QUEUED in the same
// transaction, so the job and the status change are saved together or not at all.
// ErrJobActive means the migration already has an unfinished job.
//
// The insert is the job.enqueue span (kind producer). Its traceparent is stored on the job, so
// the worker's job.process span becomes its child and the request, queue and run share a trace.
func (r *Repository) EnqueueJob(ctx context.Context, in EnqueueInput) (job *Job, err error) {
	ctx, span := startSpan(ctx, spanEnqueue, trace.SpanKindProducer,
		attribute.String("migration_id", in.MigrationID), attribute.String("database_id", in.DatabaseID),
		attribute.String("job_type", string(in.Type)))
	defer func() {
		if job != nil {
			span.SetAttributes(attribute.String("job_id", job.ID))
		}
		failSpan(span, err)
		span.End()
	}()
	if !ids.IsValid(in.MigrationID) || !ids.IsValid(in.DatabaseID) {
		return nil, ErrNotFound
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 3
	}
	var executor any
	if ids.IsValid(in.ExecutorID) {
		executor = in.ExecutorID
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	from := ""
	if in.MarkQueued {
		// FOR UPDATE: a concurrent enqueue of the same migration waits here, then sees QUEUED.
		if err := tx.QueryRowContext(ctx,
			`SELECT status FROM migrations WHERE id = $1 FOR UPDATE`, in.MigrationID).Scan(&from); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		if from == string(StatusQueued) || from == string(StatusRunning) {
			return nil, ErrJobActive
		}
		resumable := in.Type == JobResume || in.Type == JobAbort
		switch {
		case resumable && from != string(StatusPartial):
			return nil, ErrNotPartial
		case !resumable && from != string(StatusDraft) && from != string(StatusApproved):
			return nil, ErrNotDraft
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE migrations SET status = 'QUEUED', updated_at = NOW() WHERE id = $1`, in.MigrationID); err != nil {
			return nil, err
		}
	}

	id := ids.New()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO migration_jobs (id, migration_id, database_id, job_type, executor_id,
		                            from_status, max_attempts, correlation_id, trace_parent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, in.MigrationID, in.DatabaseID, string(in.Type), executor, from, in.MaxAttempts, in.CorrelationID,
		tracing.Inject(ctx))
	if isUniqueViolation(err) {
		return nil, ErrJobActive
	}
	if err != nil {
		return nil, err
	}
	if in.BatchRequestID != "" {
		if err := completeBatchRequest(ctx, tx, in.BatchRequestID, id, in.ExecutorID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	metrics.JobsEnqueued.WithLabelValues(string(in.Type)).Inc()
	return r.GetJob(ctx, id)
}

// heldByBatch is true when job alias belongs to a database with another batch migration in the
// middle of its run: PARTIAL, or queued/running again after it ran (resume or abort). Running
// anything else then would build on a half-applied change.
func heldByBatch(alias string) string {
	return `EXISTS (
		SELECT 1 FROM migrations p
		WHERE p.database_id = ` + alias + `.database_id AND p.id <> ` + alias + `.migration_id
		  AND (p.status = 'PARTIAL'
		       OR (p.execution_mode = 'BATCH' AND p.executed_at IS NOT NULL AND p.status IN ('QUEUED', 'RUNNING'))))`
}

// ClaimNextJob moves a due QUEUED job to RUNNING for workerID and returns it. Only the oldest
// queued job of each database is eligible, and a database with a RUNNING or UNCERTAIN job is skipped, so jobs on one database run one at a
// time while jobs on different databases run in parallel. It returns nil, nil when nothing is due.
func (r *Repository) ClaimNextJob(ctx context.Context, workerID string, lease time.Duration) (*Job, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var jobID, dbID string
	err = tx.QueryRowContext(ctx, `
		SELECT j.id, j.database_id
		FROM migration_jobs j
		WHERE j.status = 'QUEUED' AND j.next_run_at <= NOW()
		  -- a batch migration that stopped half-way holds every other migration of its database
		  -- until it is finished or aborted; its own resume/abort job may run
		  AND NOT `+heldByBatch("j")+`
		  -- FIFO per database: only the oldest queued job of a database may start, so
		  -- migrations get their versions in the order they were queued. A held job does not
		  -- block the jobs behind it (otherwise a resume queued after it could never run).
		  AND NOT EXISTS (
		      SELECT 1 FROM migration_jobs e
		      WHERE e.database_id = j.database_id AND e.status = 'QUEUED' AND e.created_at < j.created_at
		        AND NOT `+heldByBatch("e")+`)
		  -- one job at a time per database; UNCERTAIN holds the database until it is resolved
		  AND NOT EXISTS (
		      SELECT 1 FROM migration_jobs o
		      WHERE o.database_id = j.database_id AND o.status IN ('RUNNING', 'UNCERTAIN'))
		ORDER BY j.created_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED`).Scan(&jobID, &dbID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Two workers can pick two different jobs of the same database at the same moment: neither
	// sees the other's RUNNING row yet. This transaction-scoped lock lets one of them through;
	// the check below then runs after the other's commit and sees its RUNNING job.
	var got bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1::int, hashtext($2::text))`,
		claimLockNamespace, dbID).Scan(&got); err != nil {
		return nil, err
	}
	if !got {
		return nil, nil
	}
	var busy bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM migration_jobs
		               WHERE database_id = $1 AND status IN ('RUNNING', 'UNCERTAIN'))`, dbID).Scan(&busy); err != nil {
		return nil, err
	}
	if busy {
		return nil, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE migration_jobs SET
			status           = 'RUNNING',
			attempt          = attempt + 1,
			locked_by        = $2,
			lease_expires_at = NOW() + $3 * INTERVAL '1 millisecond',
			sql_started_at   = NULL,
			updated_at       = NOW()
		WHERE id = $1`, jobID, workerID, lease.Milliseconds()); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE migrations m SET status = 'RUNNING', updated_at = NOW()
		FROM migration_jobs j
		WHERE j.id = $1 AND j.job_type IN ('EXECUTE', 'RESUME', 'ABORT') AND m.id = j.migration_id AND m.status = 'QUEUED'`,
		jobID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetJob(ctx, jobID)
}

// ExtendLease moves the lease of a RUNNING job forward. It reports false when workerID no longer
// owns the job (the reaper took it after the lease expired).
func (r *Repository) ExtendLease(ctx context.Context, jobID, workerID string, lease time.Duration) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE migration_jobs SET lease_expires_at = NOW() + $3 * INTERVAL '1 millisecond', updated_at = NOW()
		WHERE id = $1 AND locked_by = $2 AND status = 'RUNNING'`, jobID, workerID, lease.Milliseconds())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// MarkSQLStarted records that the script of migrationID's RUNNING job is about to be sent to
// the target. Engine.SetRunStartHook calls it.
func (r *Repository) MarkSQLStarted(ctx context.Context, migrationID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE migration_jobs SET sql_started_at = NOW(), updated_at = NOW()
		WHERE migration_id = $1 AND status = 'RUNNING'`, migrationID)
	return err
}

// FinishJob moves a RUNNING job to SUCCESS, FAILED or UNCERTAIN.
func (r *Repository) FinishJob(ctx context.Context, jobID string, status JobStatus, errMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE migration_jobs SET
			status           = $2::varchar,
			last_error       = $3,
			locked_by        = '',
			lease_expires_at = NULL,
			finished_at      = CASE WHEN $2::varchar IN ('SUCCESS', 'FAILED', 'PARTIAL') THEN NOW() END,
			updated_at       = NOW()
		WHERE id = $1 AND status = 'RUNNING'`, jobID, string(status), errMsg)
	return err
}

// RequeueJob puts a RUNNING job back in the queue to run after delay. countAttempt false gives
// the attempt back (the job never reached the target, e.g. the database was busy).
func (r *Repository) RequeueJob(ctx context.Context, jobID, errMsg string, delay time.Duration, countAttempt bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	giveBack := 1
	if countAttempt {
		giveBack = 0
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE migration_jobs SET
			status           = 'QUEUED',
			attempt          = attempt - $4,
			last_error       = $2,
			next_run_at      = NOW() + $3 * INTERVAL '1 millisecond',
			locked_by        = '',
			lease_expires_at = NULL,
			sql_started_at   = NULL,
			updated_at       = NOW()
		WHERE id = $1 AND status = 'RUNNING'`, jobID, errMsg, delay.Milliseconds(), giveBack); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE migrations m SET status = 'QUEUED', updated_at = NOW()
		FROM migration_jobs j
		WHERE j.id = $1 AND j.job_type IN ('EXECUTE', 'RESUME', 'ABORT') AND m.id = j.migration_id AND m.status = 'RUNNING'`,
		jobID); err != nil {
		return err
	}
	return tx.Commit()
}

// RestoreMigrationStatus moves the migration of an EXECUTE, RESUME or ABORT job back to the status it had before
// it was enqueued. Used when the job fails before any SQL ran, so the user can try again.
func (r *Repository) RestoreMigrationStatus(ctx context.Context, j *Job) error {
	if !j.Type.changesMigrationStatus() || j.FromStatus == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE migrations SET status = $2, updated_at = NOW()
		WHERE id = $1 AND status IN ('QUEUED', 'RUNNING')`, j.MigrationID, string(j.FromStatus))
	return err
}

// OutcomeRecorded reports whether the history already has the result of j: an execution of
// this job that sent SQL and finished (SUCCESS or FAILED). The engine writes that row in the
// same transaction as the migration's history, so this is exact even if the worker died right
// after. This is the idempotency check: a job whose outcome is recorded is never run again.
func (r *Repository) OutcomeRecorded(ctx context.Context, j *Job) (bool, error) {
	return jobOutcomeRecorded(ctx, r.db, j.ID)
}

// RecordJobAttempt appends an execution for an attempt of j that has no row from the engine:
// it stopped before any SQL was sent (sqlSent false, status FAILED), or SQL was sent but the
// outcome is unknown (sqlSent true, status UNCERTAIN). It returns the execution written.
func (r *Repository) RecordJobAttempt(ctx context.Context, j *Job, status ExecStatus, sqlSent bool, cause string) (Execution, error) {
	m, err := r.Get(ctx, j.MigrationID)
	if err != nil {
		return Execution{}, err
	}
	ex := newExecution(WithRun(ctx, j.ID, j.Attempt), jobKind(j, m), m.ID, j.DatabaseID, j.ExecutorID)
	ex.Status, ex.SQLSent, ex.Error = status, sqlSent, cause
	if sqlSent {
		ex.SQL = jobScript(j, m)
	}
	if j.SQLStartedAt != nil {
		ex.StartedAt = *j.SQLStartedAt
	}
	return ex, r.RecordExecution(ctx, ex)
}

// jobKind is the kind of execution a job produces. An EXECUTE job of an approved rollback or
// forward request runs a rollback or forward.
func jobKind(j *Job, m *Migration) ExecKind {
	switch {
	case j.Type == JobRollback, j.Type == JobAbort, m.ExecutionType == "ROLLBACK" && m.RollbackTargetID != "":
		return ExecRollback
	case j.Type == JobForward, m.SourceMigrationID != "":
		return ExecForward
	}
	return ExecExecute
}

// jobScript is the script the engine sends for j. Scripts do not change once a migration is
// queued, so this matches what was sent.
func jobScript(j *Job, m *Migration) string {
	script := m.SQL
	switch j.Type {
	case JobRollback:
		if down, err := ResolveDownScript(m); err == nil {
			script = down
		}
	case JobForward:
		if m.SQLUp != "" {
			script = m.SQLUp
		}
	case JobAbort:
		script = m.SQLDown
	}
	if script == "" {
		script = "-- (script tidak diketahui)"
	}
	return script
}

// TakeStaleJob takes over a RUNNING job whose lease expired, so a live worker that lost its
// lease can no longer extend it. It returns nil when the job is not stale (any more).
func (r *Repository) TakeStaleJob(ctx context.Context, jobID, reaperID string, lease time.Duration) (*Job, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE migration_jobs SET locked_by = $2, lease_expires_at = NOW() + $3 * INTERVAL '1 millisecond'
		WHERE id = $1 AND status = 'RUNNING' AND lease_expires_at < NOW()`, jobID, reaperID, lease.Milliseconds())
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return r.GetJob(ctx, jobID)
}

// StaleJobIDs lists RUNNING jobs whose lease expired.
func (r *Repository) StaleJobIDs(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id FROM migration_jobs WHERE status = 'RUNNING' AND lease_expires_at < NOW() ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GetJob returns one job.
func (r *Repository) GetJob(ctx context.Context, id string) (*Job, error) {
	if !ids.IsValid(id) {
		return nil, ErrNotFound
	}
	return scanJob(r.db.QueryRowContext(ctx, selectJob+` WHERE id = $1`, id))
}

// LatestJobForMigration returns the newest job of a migration, or ErrNotFound.
func (r *Repository) LatestJobForMigration(ctx context.Context, migrationID string) (*Job, error) {
	if !ids.IsValid(migrationID) {
		return nil, ErrNotFound
	}
	return scanJob(r.db.QueryRowContext(ctx,
		selectJob+` WHERE migration_id = $1 ORDER BY created_at DESC LIMIT 1`, migrationID))
}

const selectJob = `
	SELECT id, migration_id, database_id, job_type, COALESCE(executor_id::text, ''), status, from_status,
	       attempt, max_attempts, next_run_at, locked_by, lease_expires_at, sql_started_at,
	       last_error, correlation_id, trace_parent, created_at, updated_at, finished_at
	FROM migration_jobs`

func scanJob(row rowScanner) (*Job, error) {
	var j Job
	var jobType, status, from string
	var lease, started, finished sql.NullTime
	err := row.Scan(&j.ID, &j.MigrationID, &j.DatabaseID, &jobType, &j.ExecutorID, &status, &from,
		&j.Attempt, &j.MaxAttempts, &j.NextRunAt, &j.LockedBy, &lease, &started,
		&j.LastError, &j.CorrelationID, &j.TraceParent, &j.CreatedAt, &j.UpdatedAt, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	j.Type, j.Status, j.FromStatus = JobType(jobType), JobStatus(status), Status(from)
	if lease.Valid {
		j.LeaseExpiresAt = &lease.Time
	}
	if started.Valid {
		j.SQLStartedAt = &started.Time
	}
	if finished.Valid {
		j.FinishedAt = &finished.Time
	}
	return &j, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// StopBatchJob ends a batch job that ran out of attempts after its worker died mid-run. With
// committed batches the migration becomes PARTIAL (resume or abort it) and the job PARTIAL;
// without any it goes back to its status before the job, and the job is FAILED.
func (r *Repository) StopBatchJob(ctx context.Context, j *Job) (JobStatus, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE migrations m SET status = 'PARTIAL', executed_at = COALESCE(m.executed_at, NOW()), updated_at = NOW()
		WHERE m.id = $1 AND m.status IN ('QUEUED', 'RUNNING')
		  AND EXISTS (SELECT 1 FROM migration_batches b WHERE b.migration_id = m.id AND b.direction = 'UP')`, j.MigrationID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return JobPartial, nil
	}
	return JobFailed, r.RestoreMigrationStatus(ctx, j)
}

// MarkAborted ends a PARTIAL batch migration without undoing its committed batches. A non-empty
// requestID is the APPROVED request that asked for it; it becomes DONE in the same transaction.
func (r *Repository) MarkAborted(ctx context.Context, migrationID, requestID, executorID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
		UPDATE migrations SET status = 'ABORTED', updated_at = NOW() WHERE id = $1 AND status = 'PARTIAL'`, migrationID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotPartial
	}
	if requestID != "" {
		if err := completeBatchRequest(ctx, tx, requestID, "", executorID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
