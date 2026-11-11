package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"dbplatform/internal/ids"

	"github.com/jackc/pgx/v5/pgconn"
)

var attemptLogger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

func logMigrationAttempt(event, execType, migrationID, executionID, databaseID, executorID string, version int, status string, duration time.Duration, sqlScript string, res *RunResult) {
	attrs := []any{
		slog.String("event", event),
		slog.String("execution_type", execType),
		slog.String("migration_id", migrationID),
		slog.String("execution_id", executionID),
		slog.String("database_id", databaseID),
		slog.String("actor", executorID),
		slog.Int("version", version),
		slog.String("status", status),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.String("sql", sqlScript),
	}
	if res != nil && res.Err != nil {
		attrs = append(attrs,
			slog.String("error", res.Err.String()),
			slog.String("sqlstate", res.Err.Code),
		)
	}
	if status == "FAILED" {
		attemptLogger.Error("migration_attempt", attrs...)
	} else {
		attemptLogger.Info("migration_attempt", attrs...)
	}
}

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
	if !m.IsDraft() && m.Status != StatusApproved {
		return nil, nil, ErrNotDraft
	}
	if m.ExecutionType == "ROLLBACK" && m.RollbackTargetID != "" {
		return e.executeApprovedRollback(ctx, m.ID, executorID)
	}
	if m.ExecutionType == "FORWARD" && m.SourceMigrationID != "" {
		return e.executeApprovedForward(ctx, m.ID, executorID)
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
	if !m.IsDraft() && m.Status != StatusApproved {
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

var reCreateTable = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z0-9_\.\"]+)`)

func extractCreatedTable(script string) string {
	matches := reCreateTable.FindStringSubmatch(script)
	if len(matches) > 1 {
		tbl := strings.TrimSpace(matches[1])
		tbl = strings.Trim(tbl, "\"")
		return tbl
	}
	return ""
}

// ResolveDownScript extracts the immutable SQL Down script for a migration,
// or infers DROP TABLE IF EXISTS when a legacy CREATE TABLE migration lacks sql_down.
func ResolveDownScript(m *Migration) (string, error) {
	if m.IsIrreversible {
		reason := m.IrreversibleReason
		if reason == "" {
			reason = "tidak ada script rollback"
		}
		return "", fmt.Errorf("migration ini ditandai sebagai irreversible: %s", reason)
	}
	downScript := strings.TrimSpace(m.SQLDown)
	if downScript != "" {
		return downScript, nil
	}
	table := extractCreatedTable(m.SQLUp)
	if table == "" {
		table = extractCreatedTable(m.SQL)
	}
	if table != "" {
		return "DROP TABLE IF EXISTS " + table + ";", nil
	}
	return "", ErrNoRollbackScript
}

func forwardName(orig string) string {
	const suffix = " (forward)"
	base := strings.TrimSpace(strings.TrimSuffix(orig, suffix))
	if r := []rune(base + suffix); len(r) > MaxNameLength {
		base = string([]rune(base)[:MaxNameLength-len([]rune(suffix))])
	}
	return base + suffix
}

func rollbackName(orig string) string {
	name := "Rollback: " + orig
	if len(name) > MaxNameLength {
		name = name[:MaxNameLength]
	}
	return name
}

func validateRollbackTarget(ctx context.Context, q querier, m *Migration) (string, error) {
	if m.Status != StatusSuccess {
		return "", ErrOnlySuccessCanRollback
	}
	if m.ExecutionType == "ROLLBACK" {
		return "", ErrCannotRollbackType
	}
	downScript, err := ResolveDownScript(m)
	if err != nil {
		return "", err
	}
	latest, err := getLatestActive(ctx, q, m.DatabaseID)
	if err != nil {
		return "", ErrOnlySuccessCanRollback
	}
	if latest.ID != m.ID {
		return "", fmt.Errorf("%w (v%d - %s)", ErrRollbackNotLatest, latest.Version, latest.Name)
	}
	return downScript, nil
}

func validateForwardSource(ctx context.Context, q querier, m *Migration) (string, error) {
	if m.Status != StatusRolledBack {
		return "", ErrOnlyRolledBackCanForward
	}
	already, err := hasAlreadyBeenForwarded(ctx, q, m.ID)
	if err != nil {
		return "", err
	}
	if already {
		return "", ErrAlreadyForwarded
	}
	earliest, err := getEarliestUnforwardedRolledBack(ctx, q, m.DatabaseID)
	if err != nil {
		return "", ErrOnlyRolledBackCanForward
	}
	if earliest.ID != m.ID {
		return "", fmt.Errorf("%w: silakan forward migrasi sebelumnya (v%d - %s) terlebih dahulu", ErrForwardNotLinear, earliest.Version, earliest.Name)
	}
	upScript := strings.TrimSpace(m.SQLUp)
	if upScript == "" {
		upScript = strings.TrimSpace(m.SQL)
	}
	if upScript == "" {
		return "", errors.New("script forward (SQL Up) tidak boleh kosong")
	}
	return upScript, nil
}

// recordRollbackSuccess stores a committed rollback row in platform DB and marks target migration ROLLED_BACK.
func recordRollbackSuccess(ctx context.Context, q querier, orig *Migration, downScript, executorID string, executedAt time.Time, duration time.Duration) (*Migration, error) {
	var executedBy any
	if ids.IsValid(executorID) {
		executedBy = executorID
	}
	rollbackID := ids.New()
	_, err := q.ExecContext(ctx, `
		INSERT INTO migrations (
			id, database_id, name, sql_script, sql_up, sql_down,
			is_irreversible, irreversible_reason, execution_type, rollback_target_id,
			status, created_by, executed_by, executed_at, duration_ms, error_message, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			false, '', 'ROLLBACK', $7,
			'SUCCESS', $8, $8, $9, $10, '', NOW()
		)`,
		rollbackID, orig.DatabaseID, rollbackName(orig.Name), downScript, orig.SQLUp, downScript,
		orig.ID, executedBy, executedAt, duration.Milliseconds())
	if err != nil {
		return nil, err
	}

	if _, err := q.ExecContext(ctx, `
		UPDATE migrations
		SET status = 'ROLLED_BACK', updated_at = NOW()
		WHERE id = $1 AND status = 'SUCCESS'`, orig.ID); err != nil {
		return nil, err
	}

	return getMigration(ctx, q, rollbackID)
}

// ExecuteRollback runs the SQL Down script of the latest active migration directly (used when no groups require approval).
func (e *Engine) ExecuteRollback(ctx context.Context, migrationID, executorID string) (*Migration, *RunResult, error) {
	m, err := getMigration(ctx, e.platform, migrationID)
	if err != nil {
		return nil, nil, err
	}
	if _, err := validateRollbackTarget(ctx, e.platform, m); err != nil {
		return nil, nil, err
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

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.runTimeout())
	defer cancel()

	m, err = getMigration(runCtx, lockConn, migrationID)
	if err != nil {
		return nil, nil, err
	}
	downScript, err := validateRollbackTarget(runCtx, lockConn, m)
	if err != nil {
		return nil, nil, err
	}

	stmts, err := CheckScript(downScript)
	if err != nil {
		return nil, nil, err
	}

	executedAt := e.now()
	res, err := e.run(runCtx, pool, downScript, stmts)
	if err != nil {
		return nil, nil, err
	}

	if !res.OK {
		logMigrationAttempt("rollback_failed", "ROLLBACK", m.ID, "", m.DatabaseID, executorID, m.Version, "FAILED", res.Duration, downScript, res)
		return nil, res, fmt.Errorf("rollback gagal: %s", res.Err.String())
	}

	rollbackRow, err := recordRollbackSuccess(runCtx, lockConn, m, downScript, executorID, executedAt, res.Duration)
	if err != nil {
		return nil, res, fmt.Errorf("rollback sudah di-COMMIT pada database target, tetapi riwayat gagal disimpan: %w", err)
	}
	logMigrationAttempt("rollback_committed", "ROLLBACK", m.ID, rollbackRow.ID, m.DatabaseID, executorID, m.Version, "SUCCESS", res.Duration, downScript, res)

	origUpdated, err := getMigration(runCtx, lockConn, migrationID)
	if err != nil {
		return rollbackRow, res, nil
	}
	return origUpdated, res, nil
}

// executeApprovedRollback runs an already-approved ROLLBACK request draft on the target DB.
func (e *Engine) executeApprovedRollback(ctx context.Context, rollbackDraftID, executorID string) (*Migration, *RunResult, error) {
	rb, err := getMigration(ctx, e.platform, rollbackDraftID)
	if err != nil {
		return nil, nil, err
	}
	pool, ok := e.pools.GetPool(rb.DatabaseID)
	if !ok {
		return nil, nil, ErrTargetUnavailable
	}

	lockConn, err := e.platform.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer lockConn.Close()
	if err := e.lock(ctx, lockConn, rb.DatabaseID); err != nil {
		return nil, nil, err
	}
	defer e.unlock(lockConn, rb.DatabaseID)

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.runTimeout())
	defer cancel()

	rb, err = getMigration(runCtx, lockConn, rollbackDraftID)
	if err != nil {
		return nil, nil, err
	}
	if !rb.IsDraft() && rb.Status != StatusApproved {
		return nil, nil, ErrNotDraft
	}
	target, err := getMigration(runCtx, lockConn, rb.RollbackTargetID)
	if err != nil {
		return nil, nil, err
	}
	downScript, err := validateRollbackTarget(runCtx, lockConn, target)
	if err != nil {
		return nil, nil, err
	}
	// Preserve immutable script stored on the rollback request
	if strings.TrimSpace(rb.SQL) != "" {
		downScript = rb.SQL
	}

	stmts, err := CheckScript(downScript)
	if err != nil {
		return nil, nil, err
	}

	executedAt := e.now()
	res, err := e.run(runCtx, pool, downScript, stmts)
	if err != nil {
		return nil, nil, err
	}
	if !res.OK {
		logMigrationAttempt("rollback_failed", "ROLLBACK", target.ID, rb.ID, rb.DatabaseID, executorID, target.Version, "FAILED", res.Duration, downScript, res)
		return nil, res, fmt.Errorf("rollback gagal: %s", res.Err.String())
	}

	var executedBy any
	if ids.IsValid(executorID) {
		executedBy = executorID
	}
	if _, err := lockConn.ExecContext(runCtx, `
		UPDATE migrations SET
			status        = 'SUCCESS',
			executed_by   = $2,
			executed_at   = $3,
			duration_ms   = $4,
			error_message = '',
			updated_at    = NOW()
		WHERE id = $1 AND status IN ('DRAFT', 'APPROVED')`,
		rb.ID, executedBy, executedAt, res.Duration.Milliseconds()); err != nil {
		return nil, res, fmt.Errorf("rollback sudah di-COMMIT pada database target, tetapi riwayat gagal disimpan: %w", err)
	}
	if _, err := lockConn.ExecContext(runCtx, `
		UPDATE migrations SET status = 'ROLLED_BACK', updated_at = NOW()
		WHERE id = $1 AND status = 'SUCCESS'`, target.ID); err != nil {
		return nil, res, fmt.Errorf("rollback berhasil, tetapi status target gagal diperbarui: %w", err)
	}

	logMigrationAttempt("rollback_committed", "ROLLBACK", target.ID, rb.ID, rb.DatabaseID, executorID, target.Version, "SUCCESS", res.Duration, downScript, res)
	rbUpdated, err := getMigration(runCtx, lockConn, rb.ID)
	return rbUpdated, res, err
}

// ExecuteForward appends a new FORWARD execution for a ROLLED_BACK migration directly (used when no groups require approval).
func (e *Engine) ExecuteForward(ctx context.Context, migrationID, executorID string) (*Migration, *RunResult, error) {
	m, err := getMigration(ctx, e.platform, migrationID)
	if err != nil {
		return nil, nil, err
	}
	if _, err := validateForwardSource(ctx, e.platform, m); err != nil {
		return nil, nil, err
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

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.runTimeout())
	defer cancel()

	m, err = getMigration(runCtx, lockConn, migrationID)
	if err != nil {
		return nil, nil, err
	}
	upScript, err := validateForwardSource(runCtx, lockConn, m)
	if err != nil {
		return nil, nil, err
	}

	stmts, err := CheckScript(upScript)
	if err != nil {
		return nil, nil, err
	}

	executedAt := e.now()
	res, err := e.run(runCtx, pool, upScript, stmts)
	if err != nil {
		return nil, nil, err
	}
	if !res.OK {
		logMigrationAttempt("forward_failed", "FORWARD", m.ID, "", m.DatabaseID, executorID, m.Version, "FAILED", res.Duration, upScript, res)
		return nil, res, fmt.Errorf("forward gagal dijalankan pada target: %s", res.Err.String())
	}

	var executedBy any
	if ids.IsValid(executorID) {
		executedBy = executorID
	}
	fwdID := ids.New()
	_, err = lockConn.ExecContext(runCtx, `
		INSERT INTO migrations (
			id, database_id, version, name, sql_script, sql_up, sql_down,
			is_irreversible, irreversible_reason, execution_type, source_migration_id,
			status, created_by, executed_by, executed_at, duration_ms, error_message, updated_at
		) VALUES (
			$1, $2, (SELECT COALESCE(MAX(o.version), 0) + 1 FROM migrations o WHERE o.database_id = $2),
			$3, $4, $5, $6,
			$7, $8, 'FORWARD', $9,
			'SUCCESS', $10, $10, $11, $12, '', NOW()
		)`,
		fwdID, m.DatabaseID, forwardName(m.Name), upScript, upScript, m.SQLDown,
		m.IsIrreversible, m.IrreversibleReason, m.ID,
		executedBy, executedAt, res.Duration.Milliseconds())
	if err != nil {
		return nil, res, fmt.Errorf("forward berhasil di-COMMIT tetapi riwayat gagal diperbarui: %w", err)
	}

	fwdRow, err := getMigration(runCtx, lockConn, fwdID)
	if err == nil {
		logMigrationAttempt("forward_committed", "FORWARD", m.ID, fwdRow.ID, m.DatabaseID, executorID, fwdRow.Version, "SUCCESS", res.Duration, upScript, res)
	}
	return fwdRow, res, err
}

// executeApprovedForward executes an already-approved FORWARD request draft and commits it append-only.
func (e *Engine) executeApprovedForward(ctx context.Context, forwardDraftID, executorID string) (*Migration, *RunResult, error) {
	fwd, err := getMigration(ctx, e.platform, forwardDraftID)
	if err != nil {
		return nil, nil, err
	}
	pool, ok := e.pools.GetPool(fwd.DatabaseID)
	if !ok {
		return nil, nil, ErrTargetUnavailable
	}

	lockConn, err := e.platform.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer lockConn.Close()
	if err := e.lock(ctx, lockConn, fwd.DatabaseID); err != nil {
		return nil, nil, err
	}
	defer e.unlock(lockConn, fwd.DatabaseID)

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.runTimeout())
	defer cancel()

	fwd, err = getMigration(runCtx, lockConn, forwardDraftID)
	if err != nil {
		return nil, nil, err
	}
	if !fwd.IsDraft() && fwd.Status != StatusApproved {
		return nil, nil, ErrNotDraft
	}
	src, err := getMigration(runCtx, lockConn, fwd.SourceMigrationID)
	if err != nil {
		return nil, nil, err
	}
	upScript, err := validateForwardSource(runCtx, lockConn, src)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(fwd.SQL) != "" {
		upScript = fwd.SQL
	}

	stmts, err := CheckScript(upScript)
	if err != nil {
		return nil, nil, err
	}

	executedAt := e.now()
	res, err := e.run(runCtx, pool, upScript, stmts)
	if err != nil {
		return nil, nil, err
	}
	if !res.OK {
		logMigrationAttempt("forward_failed", "FORWARD", src.ID, fwd.ID, fwd.DatabaseID, executorID, src.Version, "FAILED", res.Duration, upScript, res)
		return nil, res, fmt.Errorf("forward gagal dijalankan pada target: %s", res.Err.String())
	}

	var executedBy any
	if ids.IsValid(executorID) {
		executedBy = executorID
	}
	_, err = lockConn.ExecContext(runCtx, `
		UPDATE migrations m SET
			status        = 'SUCCESS',
			version       = (SELECT COALESCE(MAX(o.version), 0) + 1 FROM migrations o WHERE o.database_id = m.database_id),
			executed_by   = $2,
			executed_at   = $3,
			duration_ms   = $4,
			error_message = '',
			updated_at    = NOW()
		WHERE m.id = $1 AND m.status IN ('DRAFT', 'APPROVED')`,
		fwd.ID, executedBy, executedAt, res.Duration.Milliseconds())
	if err != nil {
		return nil, res, fmt.Errorf("forward berhasil di-COMMIT tetapi riwayat gagal diperbarui: %w", err)
	}

	fwdUpdated, err := getMigration(runCtx, lockConn, fwd.ID)
	if err == nil {
		logMigrationAttempt("forward_committed", "FORWARD", src.ID, fwdUpdated.ID, fwd.DatabaseID, executorID, fwdUpdated.Version, "SUCCESS", res.Duration, upScript, res)
	}
	return fwdUpdated, res, err
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
