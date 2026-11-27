package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"dbplatform/internal/ids"
)

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

// insertRollbackRow appends the SUCCESS rollback row of a direct rollback of orig. The row is
// what the history table shows; the execution row (written with it) is what links the rollback
// to the execution it undid. orig itself is not changed.
func insertRollbackRow(ctx context.Context, q querier, id string, orig *Migration, downScript, executorID string, executedAt time.Time, duration time.Duration) error {
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
		id, orig.DatabaseID, rollbackName(orig.Name), downScript, orig.SQLUp, downScript,
		orig.ID, nullID(executorID), executedAt, duration.Milliseconds())
	return err
}

// recordFailedRun appends a FAILED execution of a direct rollback or forward. No migration row is
// created for it; the execution points at the migration the user asked to roll back or forward.
func (e *Engine) recordFailedRun(ctx context.Context, q querier, ex Execution, execType string, version int, res *RunResult) {
	if err := insertExecution(ctx, q, ex); err != nil {
		e.logger.ErrorContext(ctx, "migration.history_failed", slog.String("execution_id", ex.ID), slog.String("error", err.Error()))
		ex.ID = ""
	}
	e.logAttempt(ctx, execType, ex.MigrationID, ex.ID, ex.DatabaseID, version, string(ex.Status), res.Duration, ex.SQL, res)
	if ex.ID != "" {
		auditExecution(ctx, e.logger, ex)
	}
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
	if err := e.runStarted(runCtx, m.ID); err != nil {
		return nil, nil, err
	}

	executedAt := e.now()
	res, err := e.run(runCtx, pool, migrationSQLLog(m.DatabaseID, m.ID, "ROLLBACK", downScript), stmts)
	if err != nil {
		return nil, nil, err
	}

	ex := newExecution(runCtx, ExecRollback, m.ID, m.DatabaseID, executorID).withResult(downScript, executedAt, res)
	ex.RevertsExecutionID = m.AppliedExecutionID
	if !res.OK {
		e.recordFailedRun(runCtx, lockConn, ex, "ROLLBACK", m.Version, res)
		return nil, res, fmt.Errorf("rollback gagal: %s", res.Err.String())
	}

	rollbackID := ids.New()
	ex.MigrationID = rollbackID
	if err := withTx(runCtx, lockConn, func(tx *sql.Tx) error {
		if err := insertRollbackRow(runCtx, tx, rollbackID, m, downScript, executorID, executedAt, res.Duration); err != nil {
			return err
		}
		return insertExecution(runCtx, tx, ex)
	}); err != nil {
		return nil, res, fmt.Errorf("rollback sudah di-COMMIT pada database target, tetapi riwayat gagal disimpan: %w", err)
	}
	e.logAttempt(runCtx, "ROLLBACK", m.ID, ex.ID, m.DatabaseID, m.Version, "SUCCESS", res.Duration, downScript, res)
	auditExecution(runCtx, e.logger, ex)

	// The original row is unchanged; it reads as ROLLED_BACK because of the new execution.
	orig, err := getMigration(runCtx, lockConn, migrationID)
	return orig, res, err
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
	if !rb.IsExecutable() {
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

	if err := e.runStarted(runCtx, rb.ID); err != nil {
		return nil, nil, err
	}

	executedAt := e.now()
	res, err := e.run(runCtx, pool, migrationSQLLog(rb.DatabaseID, rb.ID, "ROLLBACK", downScript,
		slog.String("rollback_target_id", target.ID)), stmts)
	if err != nil {
		return nil, nil, err
	}
	ex := newExecution(runCtx, ExecRollback, rb.ID, rb.DatabaseID, executorID).withResult(downScript, executedAt, res)
	ex.RevertsExecutionID = target.AppliedExecutionID
	if err := withTx(runCtx, lockConn, func(tx *sql.Tx) error {
		if res.OK {
			if err := markRequestSucceeded(runCtx, tx, rb.ID, executorID, executedAt, res.Duration, false); err != nil {
				return err
			}
		} else if err := recordExecution(runCtx, tx, failedRecord(rb.ID, downScript, executorID, executedAt, res)); err != nil {
			return err
		}
		return insertExecution(runCtx, tx, ex)
	}); err != nil {
		if res.OK {
			return nil, res, fmt.Errorf("rollback sudah di-COMMIT pada database target, tetapi riwayat gagal disimpan: %w", err)
		}
		return nil, res, fmt.Errorf("gagal menyimpan riwayat rollback: %w", err)
	}
	e.logAttempt(runCtx, "ROLLBACK", target.ID, ex.ID, rb.DatabaseID, target.Version, string(ex.Status), res.Duration, downScript, res)
	auditExecution(runCtx, e.logger, ex)
	if !res.OK {
		return nil, res, fmt.Errorf("rollback gagal: %s", res.Err.String())
	}
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

	if err := e.runStarted(runCtx, m.ID); err != nil {
		return nil, nil, err
	}

	executedAt := e.now()
	res, err := e.run(runCtx, pool, migrationSQLLog(m.DatabaseID, m.ID, "FORWARD", upScript), stmts)
	if err != nil {
		return nil, nil, err
	}
	ex := newExecution(runCtx, ExecForward, m.ID, m.DatabaseID, executorID).withResult(upScript, executedAt, res)
	ex.RevertsExecutionID = m.RollbackExecutionID
	if !res.OK {
		e.recordFailedRun(runCtx, lockConn, ex, "FORWARD", m.Version, res)
		return nil, res, fmt.Errorf("forward gagal dijalankan pada target: %s", res.Err.String())
	}

	fwdID := ids.New()
	ex.MigrationID = fwdID
	err = withTx(runCtx, lockConn, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(runCtx, `
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
			nullID(executorID), executedAt, res.Duration.Milliseconds()); err != nil {
			return err
		}
		return insertExecution(runCtx, tx, ex)
	})
	if err != nil {
		return nil, res, fmt.Errorf("forward berhasil di-COMMIT tetapi riwayat gagal diperbarui: %w", err)
	}

	fwdRow, err := getMigration(runCtx, lockConn, fwdID)
	if err == nil {
		e.logAttempt(runCtx, "FORWARD", m.ID, ex.ID, m.DatabaseID, fwdRow.Version, "SUCCESS", res.Duration, upScript, res)
		auditExecution(runCtx, e.logger, ex)
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
	if !fwd.IsExecutable() {
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

	if err := e.runStarted(runCtx, fwd.ID); err != nil {
		return nil, nil, err
	}

	executedAt := e.now()
	res, err := e.run(runCtx, pool, migrationSQLLog(fwd.DatabaseID, fwd.ID, "FORWARD", upScript,
		slog.String("source_migration_id", src.ID)), stmts)
	if err != nil {
		return nil, nil, err
	}
	ex := newExecution(runCtx, ExecForward, fwd.ID, fwd.DatabaseID, executorID).withResult(upScript, executedAt, res)
	ex.RevertsExecutionID = src.RollbackExecutionID
	if err := withTx(runCtx, lockConn, func(tx *sql.Tx) error {
		if res.OK {
			if err := markRequestSucceeded(runCtx, tx, fwd.ID, executorID, executedAt, res.Duration, true); err != nil {
				return err
			}
		} else if err := recordExecution(runCtx, tx, failedRecord(fwd.ID, upScript, executorID, executedAt, res)); err != nil {
			return err
		}
		return insertExecution(runCtx, tx, ex)
	}); err != nil {
		if res.OK {
			return nil, res, fmt.Errorf("forward berhasil di-COMMIT tetapi riwayat gagal diperbarui: %w", err)
		}
		return nil, res, fmt.Errorf("gagal menyimpan riwayat forward: %w", err)
	}
	if !res.OK {
		e.logAttempt(runCtx, "FORWARD", src.ID, ex.ID, fwd.DatabaseID, src.Version, "FAILED", res.Duration, upScript, res)
		auditExecution(runCtx, e.logger, ex)
		return nil, res, fmt.Errorf("forward gagal dijalankan pada target: %s", res.Err.String())
	}

	fwdUpdated, err := getMigration(runCtx, lockConn, fwd.ID)
	if err == nil {
		e.logAttempt(runCtx, "FORWARD", src.ID, ex.ID, fwd.DatabaseID, fwdUpdated.Version, "SUCCESS", res.Duration, upScript, res)
		auditExecution(runCtx, e.logger, ex)
	}
	return fwdUpdated, res, err
}

// markRequestSucceeded moves an approved rollback or forward request row to SUCCESS. A forward
// gets the next version (it is active again); a rollback row has no version.
func markRequestSucceeded(ctx context.Context, q querier, id, executorID string, executedAt time.Time, duration time.Duration, withVersion bool) error {
	res, err := q.ExecContext(ctx, `
		UPDATE migrations m SET
			status        = 'SUCCESS',
			version       = CASE WHEN $5 THEN (SELECT COALESCE(MAX(o.version), 0) + 1
			                                   FROM migrations o WHERE o.database_id = m.database_id) END,
			executed_by   = $2,
			executed_at   = $3,
			duration_ms   = $4,
			error_message = '',
			updated_at    = NOW()
		WHERE m.id = $1 AND m.status IN ('DRAFT', 'APPROVED', 'QUEUED', 'RUNNING')`,
		id, nullID(executorID), executedAt, duration.Milliseconds(), withVersion)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotDraft
	}
	return nil
}

// failedRecord is the history write for an approved rollback/forward request whose script
// failed: the request row becomes FAILED, like a failed migration.
func failedRecord(id, script, executorID string, executedAt time.Time, res *RunResult) executionRecord {
	rec := executionRecord{ID: id, SQL: script, ExecutedBy: executorID, ExecutedAt: executedAt, Duration: res.Duration}
	if res.Err != nil {
		rec.Error = res.Err.String()
	}
	return rec
}
