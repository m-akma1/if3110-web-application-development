package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// PoolSource gives the engine the connection pool of a target database.
// *dbmanager.ConnectionRegistry satisfies it.
type PoolSource interface {
	GetPool(dbID string) (*sql.DB, bool)
}

// Limits bound one run on a target database.
type Limits struct {
	LockTimeout      time.Duration // SET LOCAL lock_timeout: how long a statement waits for a table lock
	StatementTimeout time.Duration // SET LOCAL statement_timeout: how long one statement may run
	QueueTimeout     time.Duration // how long Execute waits while another migration runs on the same database
}

// DefaultLimits suit interactive use from the web UI.
func DefaultLimits() Limits {
	return Limits{LockTimeout: 5 * time.Second, StatementTimeout: 60 * time.Second, QueueTimeout: 15 * time.Second}
}

func (l Limits) normalized() Limits {
	d := DefaultLimits()
	if l.LockTimeout <= 0 {
		l.LockTimeout = d.LockTimeout
	}
	if l.StatementTimeout <= 0 {
		l.StatementTimeout = d.StatementTimeout
	}
	if l.QueueTimeout <= 0 {
		l.QueueTimeout = d.QueueTimeout
	}
	return l
}

// lockNamespace is the first key of the two-key advisory lock taken per target database
// (the second key is hashtext(database_id)). Two-key locks never collide with the one-key
// lock used by platformdb.Migrate.
const lockNamespace int32 = 0x4d49 // "MI"

const lockPollInterval = 100 * time.Millisecond

// Engine runs scripts on target databases and records executions in the platform database.
type Engine struct {
	platform *sql.DB
	pools    PoolSource
	limits   Limits
	now      func() time.Time
}

func NewEngine(platform *sql.DB, pools PoolSource, limits Limits) *Engine {
	return &Engine{platform: platform, pools: pools, limits: limits.normalized(), now: time.Now}
}

// DryRun runs script on the target inside a transaction that is always rolled back, so the
// target is never changed. Statements run one at a time and DML statements are analyzed with
// EXPLAIN ANALYZE (see explain.go), so RunResult.Steps has a report and plan per statement.
// A failing script is a normal result (RunResult.OK false), not an error.
// Errors are ErrEmptyScript, ErrScriptTooLarge, *RejectedError, ErrTargetUnavailable or a context error.
func (e *Engine) DryRun(ctx context.Context, databaseID, script string) (*RunResult, error) {
	stmts, err := CheckScript(script)
	if err != nil {
		return nil, err
	}
	pool, ok := e.pools.GetPool(databaseID)
	if !ok {
		return nil, ErrTargetUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, e.runTimeout())
	defer cancel()
	conn, tx, err := e.begin(ctx, pool)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	defer func() { _ = tx.Rollback() }()
	return dryRun(ctx, tx, script, stmts), nil
}

// Execute runs a DRAFT on its target database in one transaction: COMMIT when every statement
// succeeds, ROLLBACK otherwise. The outcome is written to the migration (SUCCESS with the next
// version, or FAILED with the error) while the target's advisory lock is held, so concurrent
// executions on the same database run one at a time and get distinct versions.
//
// A failed script is a normal result: the FAILED migration is returned with a nil error.
// Errors mean nothing ran: ErrNotFound, ErrNotDraft, *RejectedError, ErrTargetUnavailable,
// ErrTargetBusy, or a storage error.
//
// Known limitation: if the process dies after the target COMMIT but before the history write,
// the history misses that run.
func (e *Engine) Execute(ctx context.Context, migrationID, executorID string) (*Migration, *RunResult, error) {
	m, err := getMigration(ctx, e.platform, migrationID)
	if err != nil {
		return nil, nil, err
	}
	if !m.IsDraft() {
		return nil, nil, ErrNotDraft
	}
	pool, ok := e.pools.GetPool(m.DatabaseID)
	if !ok {
		return nil, nil, ErrTargetUnavailable
	}

	lockConn, err := e.platform.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer lockConn.Close()
	if err := e.lock(ctx, lockConn, m.DatabaseID); err != nil {
		return nil, nil, err
	}
	defer e.unlock(lockConn, m.DatabaseID)

	// Once the lock is held, finish the run and its history write even if the client goes away.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.runTimeout())
	defer cancel()

	// Re-read under the lock: another request may have executed or edited this draft meanwhile.
	m, err = getMigration(runCtx, lockConn, migrationID)
	if err != nil {
		return nil, nil, err
	}
	if !m.IsDraft() {
		return nil, nil, ErrNotDraft
	}
	stmts, err := CheckScript(m.SQL)
	if err != nil {
		return nil, nil, err
	}

	executedAt := e.now()
	res, err := e.run(runCtx, pool, m.SQL, stmts)
	if err != nil {
		return nil, nil, err
	}

	rec := executionRecord{
		ID:         m.ID,
		OK:         res.OK,
		SQL:        m.SQL,
		ExecutedBy: executorID,
		ExecutedAt: executedAt,
		Duration:   res.Duration,
	}
	if res.Err != nil {
		rec.Error = res.Err.String()
	}
	if err := recordExecution(runCtx, lockConn, rec); err != nil {
		if res.OK {
			return nil, res, fmt.Errorf("migration sudah di-COMMIT pada database target, tetapi riwayat gagal disimpan: %w", err)
		}
		return nil, res, fmt.Errorf("gagal menyimpan riwayat migration: %w", err)
	}
	m, err = getMigration(runCtx, lockConn, migrationID)
	return m, res, err
}

// runTimeout bounds a whole run on the client side, in case the server-side timeouts do not fire.
func (e *Engine) runTimeout() time.Duration {
	return e.limits.LockTimeout + e.limits.StatementTimeout + 15*time.Second
}

// begin opens a transaction on one pooled connection of the target with lock and statement
// timeouts set. The caller closes conn and ends tx.
func (e *Engine) begin(ctx context.Context, pool *sql.DB) (*sql.Conn, *sql.Tx, error) {
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrTargetUnavailable, err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("%w: %v", ErrTargetUnavailable, err)
	}
	// SET cannot take bind parameters; the values are integers, so formatting them in is safe.
	for _, set := range []string{
		fmt.Sprintf("SET LOCAL lock_timeout = %d", e.limits.LockTimeout.Milliseconds()),
		fmt.Sprintf("SET LOCAL statement_timeout = %d", e.limits.StatementTimeout.Milliseconds()),
	} {
		if _, err := tx.ExecContext(ctx, set); err != nil {
			_ = tx.Rollback()
			conn.Close()
			return nil, nil, fmt.Errorf("%w: %v", ErrTargetUnavailable, err)
		}
	}
	return conn, tx, nil
}

// run executes the whole script on the target in one transaction and commits it when every
// statement succeeds. It returns an error only when the transaction could not be started;
// script failures are reported in RunResult.
func (e *Engine) run(ctx context.Context, pool *sql.DB, script string, stmts []Statement) (*RunResult, error) {
	start := time.Now()
	conn, tx, err := e.begin(ctx, pool)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	defer func() { _ = tx.Rollback() }()

	// Without arguments pgx sends the script with the simple query protocol, so the server parses
	// all statements itself and error positions are relative to the whole script.
	_, execErr := tx.ExecContext(ctx, script)
	committed := false
	if execErr == nil {
		execErr = tx.Commit()
		committed = execErr == nil
	}
	res := &RunResult{OK: execErr == nil, Statements: len(stmts), Duration: time.Since(start)}
	if execErr != nil {
		res.Err = toScriptError(script, stmts, execErr)
	}

	// A committed script may have changed session settings (SET search_path, SET ROLE, ...).
	// Reset them so the pooled connection is clean for the viewer and the next migration.
	if committed {
		if _, err := conn.ExecContext(ctx, "RESET ALL"); err != nil {
			discardConn(conn)
		}
	}
	return res, nil
}

// lock waits up to QueueTimeout for the target's advisory lock on conn.
// It polls pg_try_advisory_lock instead of blocking, so a timeout never leaves a lock request
// pending on the connection.
func (e *Engine) lock(ctx context.Context, conn *sql.Conn, databaseID string) error {
	deadline := time.Now().Add(e.limits.QueueTimeout)
	for {
		var got bool
		if err := conn.QueryRowContext(ctx,
			`SELECT pg_try_advisory_lock($1::int, hashtext($2::text))`, lockNamespace, databaseID).Scan(&got); err != nil {
			return err
		}
		if got {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrTargetBusy
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

func (e *Engine) unlock(conn *sql.Conn, databaseID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var released bool
	err := conn.QueryRowContext(ctx,
		`SELECT pg_advisory_unlock($1::int, hashtext($2::text))`, lockNamespace, databaseID).Scan(&released)
	if err != nil || !released {
		// Closing the session is the only other way to release a session-level advisory lock.
		discardConn(conn)
	}
}

// discardConn makes database/sql close the connection instead of returning it to the pool.
func discardConn(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

// toScriptError turns a driver error from a whole-script run into a ScriptError, mapping
// PostgreSQL's character position to a line, column and statement number of the script.
func toScriptError(script string, stmts []Statement, err error) *ScriptError {
	se := baseScriptError(err)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if line, col, off, ok := lineColumn(script, int(pgErr.Position)); ok {
			se.Line, se.Column, se.Statement = line, col, statementAt(stmts, off)
		}
	}
	return se
}
