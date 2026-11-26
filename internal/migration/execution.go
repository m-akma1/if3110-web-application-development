package migration

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"dbplatform/internal/audit"
	"dbplatform/internal/ids"
	"dbplatform/internal/logging"
)

// ExecKind is what one execution did on the target.
type ExecKind string

const (
	ExecExecute  ExecKind = "EXECUTE"  // first run of a migration
	ExecRollback ExecKind = "ROLLBACK" // ran SQL Down; reverts an EXECUTE or FORWARD execution
	ExecForward  ExecKind = "FORWARD"  // re-applied a rolled-back migration; reverts a ROLLBACK execution
)

// ExecStatus is the outcome of one execution.
type ExecStatus string

const (
	ExecSuccess   ExecStatus = "SUCCESS"
	ExecFailed    ExecStatus = "FAILED"
	ExecUncertain ExecStatus = "UNCERTAIN" // SQL was sent, outcome unknown
)

// Execution is one attempt to run a migration. Rows are append-only (see 009_migration_executions.sql).
type Execution struct {
	ID                 string
	MigrationID        string
	DatabaseID         string
	JobID              string // empty for runs outside the queue
	Attempt            int
	Kind               ExecKind
	Status             ExecStatus
	SQLSent            bool   // false: stopped before any SQL reached the target
	SQL                string // the exact script sent; empty when SQLSent is false
	RevertsExecutionID string
	ExecutorID         string
	ExecutorName       string
	Error              string
	SQLState           string
	CorrelationID      string
	StartedAt          time.Time
	FinishedAt         time.Time
	Duration           time.Duration
}

// runInfo is the queue context of a run: which job and attempt the engine is working for.
type runInfo struct {
	JobID   string
	Attempt int
}

type runInfoKey struct{}

// WithRun tells the engine which job and attempt it is running, so the execution row it writes
// can be matched to the job (idempotency) and counted per attempt.
func WithRun(ctx context.Context, jobID string, attempt int) context.Context {
	return context.WithValue(ctx, runInfoKey{}, runInfo{JobID: jobID, Attempt: attempt})
}

func runFrom(ctx context.Context) runInfo {
	ri, _ := ctx.Value(runInfoKey{}).(runInfo)
	if ri.Attempt < 1 {
		ri.Attempt = 1
	}
	return ri
}

// newExecution fills the fields every execution shares: a new ID, the job and attempt from ctx,
// and the request's correlation ID.
func newExecution(ctx context.Context, kind ExecKind, migrationID, databaseID, executorID string) Execution {
	ri := runFrom(ctx)
	ex := Execution{
		ID:          ids.New(),
		MigrationID: migrationID,
		DatabaseID:  databaseID,
		JobID:       ri.JobID,
		Attempt:     ri.Attempt,
		Kind:        kind,
		ExecutorID:  executorID,
	}
	if info := logging.Info(ctx); info != nil {
		ex.CorrelationID = info.CorrelationID
	}
	return ex
}

// withResult copies the outcome of a run on the target into ex.
func (ex Execution) withResult(script string, startedAt time.Time, res *RunResult) Execution {
	ex.SQLSent, ex.SQL, ex.StartedAt = true, script, startedAt
	ex.Status = ExecFailed
	if res.OK {
		ex.Status = ExecSuccess
	}
	ex.Duration = res.Duration
	if res.Err != nil {
		ex.Error, ex.SQLState = res.Err.String(), res.Err.Code
	}
	return ex
}

// insertExecution appends one execution row.
func insertExecution(ctx context.Context, q querier, ex Execution) error {
	if ex.StartedAt.IsZero() {
		ex.StartedAt = time.Now()
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO migration_executions (
			id, migration_id, database_id, job_id, attempt, execution_type, status, sql_sent,
			sql_executed, reverts_execution_id, executor_id, error_message, sqlstate,
			correlation_id, started_at, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		ex.ID, ex.MigrationID, ex.DatabaseID, nullID(ex.JobID), ex.Attempt, string(ex.Kind), string(ex.Status),
		ex.SQLSent, ex.SQL, nullID(ex.RevertsExecutionID), nullID(ex.ExecutorID), ex.Error, ex.SQLState,
		ex.CorrelationID, ex.StartedAt, ex.Duration.Milliseconds())
	return err
}

// nullID turns an empty or malformed UUID into SQL NULL.
func nullID(id string) any {
	if ids.IsValid(id) {
		return id
	}
	return nil
}

// RecordExecution appends one execution row outside the engine: the worker uses it for attempts
// that never reached the target and for runs whose outcome is UNCERTAIN.
func (r *Repository) RecordExecution(ctx context.Context, ex Execution) error {
	return insertExecution(ctx, r.db, ex)
}

// ListExecutions returns the executions of one migration row, oldest first: its own runs and
// the failed direct rollback/forward attempts that point at it.
func (r *Repository) ListExecutions(ctx context.Context, migrationID string) ([]Execution, error) {
	if !ids.IsValid(migrationID) {
		return nil, ErrNotFound
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT e.id, e.migration_id, e.database_id, COALESCE(e.job_id::text, ''), e.attempt,
		       e.execution_type, e.status, e.sql_sent, e.sql_executed,
		       COALESCE(e.reverts_execution_id::text, ''), COALESCE(e.executor_id::text, ''),
		       COALESCE(u.username, ''), e.error_message, e.sqlstate, e.correlation_id,
		       e.started_at, e.finished_at, e.duration_ms
		FROM migration_executions e
		LEFT JOIN users u ON u.id = e.executor_id
		WHERE e.migration_id = $1
		ORDER BY e.finished_at, e.started_at`, migrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Execution
	for rows.Next() {
		var ex Execution
		var kind, status string
		var ms int64
		if err := rows.Scan(&ex.ID, &ex.MigrationID, &ex.DatabaseID, &ex.JobID, &ex.Attempt,
			&kind, &status, &ex.SQLSent, &ex.SQL, &ex.RevertsExecutionID, &ex.ExecutorID,
			&ex.ExecutorName, &ex.Error, &ex.SQLState, &ex.CorrelationID,
			&ex.StartedAt, &ex.FinishedAt, &ms); err != nil {
			return nil, err
		}
		ex.Kind, ex.Status, ex.Duration = ExecKind(kind), ExecStatus(status), time.Duration(ms)*time.Millisecond
		out = append(out, ex)
	}
	return out, rows.Err()
}

// jobOutcomeRecorded reports whether an execution of jobID sent SQL and has a final result.
// This is the idempotency check of the worker: such a job is never run again.
func jobOutcomeRecorded(ctx context.Context, q querier, jobID string) (bool, error) {
	if !ids.IsValid(jobID) {
		return false, nil
	}
	var done bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM migration_executions
		               WHERE job_id = $1 AND sql_sent AND status IN ('SUCCESS', 'FAILED'))`, jobID).Scan(&done)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return done, err
}

// withTx runs fn in one transaction on conn. The engine uses it on the connection that holds the
// database's advisory lock, so the migration row and its execution row are saved together.
func withTx(ctx context.Context, conn *sql.Conn, fn func(tx *sql.Tx) error) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// auditExecution writes the audit event for one recorded execution: audit.execute,
// audit.rollback or audit.forward. The actor and correlation ID come from ctx.
func auditExecution(ctx context.Context, l *slog.Logger, ex Execution) {
	attrs := []slog.Attr{
		slog.String("execution_id", ex.ID),
		slog.String("migration_id", ex.MigrationID),
		slog.String("database_id", ex.DatabaseID),
		slog.Int("attempt", ex.Attempt),
		slog.Bool("sql_sent", ex.SQLSent),
	}
	if ex.JobID != "" {
		attrs = append(attrs, slog.String("job_id", ex.JobID))
	}
	if ex.RevertsExecutionID != "" {
		attrs = append(attrs, slog.String("reverts_execution_id", ex.RevertsExecutionID))
	}
	if ex.Error != "" {
		attrs = append(attrs, slog.String("error", ex.Error))
	}
	action := map[ExecKind]string{ExecExecute: audit.Execute, ExecRollback: audit.Rollback, ExecForward: audit.Forward}[ex.Kind]
	outcome := map[ExecStatus]string{ExecSuccess: audit.Success, ExecFailed: audit.Failure, ExecUncertain: audit.Uncertain}[ex.Status]
	audit.Log(ctx, l, action, outcome, attrs...)
}
