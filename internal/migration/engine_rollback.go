package migration

import (
	"context"
	"errors"
	"fmt"
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
