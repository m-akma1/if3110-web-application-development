package migration

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"dbplatform/internal/ids"
)

// querier is satisfied by *sql.DB, *sql.Conn and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Repository stores drafts and executed runs in the platform database (table migrations).
// It does not check access; callers authorize first.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// NewDraft is the input for CreateDraft.
type NewDraft struct {
	DatabaseID         string
	Name               string
	SQL                string
	SQLUp              string
	SQLDown            string
	IsIrreversible     bool
	IrreversibleReason string
	ExecutionType      string
	RollbackTargetID   string
	SourceMigrationID  string
	CreatedBy          string // user id
}

// UpdateDraftInput is the input for UpdateDraftFull.
type UpdateDraftInput struct {
	Name               string
	SQL                string
	SQLUp              string
	SQLDown            string
	IsIrreversible     bool
	IrreversibleReason string
}

// CreateDraft inserts a DRAFT migration. Name and script are validated with NormalizeDraft.
func (r *Repository) CreateDraft(ctx context.Context, in NewDraft) (*Migration, error) {
	scriptToValidate := in.SQL
	if in.ExecutionType != "ROLLBACK" && in.SQLUp != "" {
		scriptToValidate = in.SQLUp
	}
	name, script, err := NormalizeDraft(in.Name, scriptToValidate)
	if err != nil {
		return nil, err
	}
	if !ids.IsValid(in.DatabaseID) {
		return nil, ErrTargetUnavailable
	}
	var createdBy any
	if ids.IsValid(in.CreatedBy) {
		createdBy = in.CreatedBy
	}
	sqlUp := in.SQLUp
	if sqlUp == "" && in.ExecutionType != "ROLLBACK" {
		sqlUp = script
	}
	execType := in.ExecutionType
	if execType == "" {
		execType = "FORWARD"
	}
	var rollbackTargetID any
	if ids.IsValid(in.RollbackTargetID) {
		rollbackTargetID = in.RollbackTargetID
	}
	var sourceMigrationID any
	if ids.IsValid(in.SourceMigrationID) {
		sourceMigrationID = in.SourceMigrationID
	}
	id := ids.New()
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO migrations (
			id, database_id, name, sql_script, sql_up, sql_down,
			is_irreversible, irreversible_reason, execution_type, rollback_target_id, source_migration_id,
			status, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'DRAFT', $12)`,
		id, in.DatabaseID, name, script, sqlUp, in.SQLDown, in.IsIrreversible, in.IrreversibleReason,
		execType, rollbackTargetID, sourceMigrationID, createdBy); err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// UpdateDraft changes the name and script of a DRAFT or REJECTED migration. Executed migrations return ErrNotDraft.
func (r *Repository) UpdateDraft(ctx context.Context, id, name, script string) (*Migration, error) {
	return r.UpdateDraftFull(ctx, id, UpdateDraftInput{Name: name, SQL: script})
}

// UpdateDraftFull changes the name, scripts (up/down), and irreversible flags of a DRAFT or REJECTED migration.
func (r *Repository) UpdateDraftFull(ctx context.Context, id string, in UpdateDraftInput) (*Migration, error) {
	if !ids.IsValid(id) {
		return nil, ErrNotFound
	}
	cur, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.ExecutionType == "ROLLBACK" || cur.RollbackTargetID != "" || cur.SourceMigrationID != "" {
		return nil, ErrImmutableRollbackScript
	}
	scriptToValidate := in.SQL
	if in.SQLUp != "" {
		scriptToValidate = in.SQLUp
	}
	name, script, err := NormalizeDraft(in.Name, scriptToValidate)
	if err != nil {
		return nil, err
	}
	sqlUp := in.SQLUp
	if sqlUp == "" {
		sqlUp = script
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE migrations SET
			name = $2, sql_script = $3, sql_up = $4, sql_down = $5,
			is_irreversible = $6, irreversible_reason = $7,
			status = 'DRAFT', rejection_reason = '', updated_at = NOW()
		WHERE id = $1 AND status IN ('DRAFT', 'REJECTED')`,
		id, name, script, sqlUp, in.SQLDown, in.IsIrreversible, in.IrreversibleReason)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, r.notDraftOrMissing(ctx, id)
	}
	return r.Get(ctx, id)
}

// DeleteDraft removes a DRAFT, REJECTED, or APPROVED migration. Executed migrations return ErrNotDraft.
func (r *Repository) DeleteDraft(ctx context.Context, id string) error {
	if !ids.IsValid(id) {
		return ErrNotFound
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM migrations WHERE id = $1 AND status IN ('DRAFT', 'REJECTED', 'APPROVED')`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return r.notDraftOrMissing(ctx, id)
	}
	return nil
}

// notDraftOrMissing explains why a "WHERE status = 'DRAFT'" write matched no row.
func (r *Repository) notDraftOrMissing(ctx context.Context, id string) error {
	if _, err := r.Get(ctx, id); err != nil {
		return err // ErrNotFound or a storage error
	}
	return ErrNotDraft
}

// Get returns one migration with author, executor and database names.
func (r *Repository) Get(ctx context.Context, id string) (*Migration, error) {
	return getMigration(ctx, r.db, id)
}

// ListByDatabase returns the migrations of one database, newest first.
// With statuses given, only those statuses are returned.
func (r *Repository) ListByDatabase(ctx context.Context, databaseID string, statuses ...Status) ([]*Migration, error) {
	if !ids.IsValid(databaseID) {
		return nil, nil
	}
	filter := make([]string, 0, len(statuses))
	for _, s := range statuses {
		filter = append(filter, string(s))
	}
	rows, err := r.db.QueryContext(ctx, selectMigrations+`
		WHERE m.database_id = $1 AND (cardinality($2::text[]) = 0 OR m.status = ANY($2::text[]))
		ORDER BY COALESCE(m.executed_at, m.updated_at) DESC, m.created_at DESC`,
		databaseID, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*Migration
	for rows.Next() {
		m, err := scanMigration(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

const selectMigrations = `
	SELECT m.id, m.database_id, COALESCE(t.name, ''), COALESCE(m.version, 0), m.name, m.sql_script, m.status,
	       COALESCE(m.created_by::text, ''), COALESCE(cu.username, ''), m.created_at, m.updated_at,
	       COALESCE(m.executed_by::text, ''), COALESCE(eu.username, ''), m.executed_at,
	       COALESCE(m.duration_ms, 0), m.error_message, COALESCE(m.rejection_reason, ''),
	       COALESCE(m.sql_up, ''), COALESCE(m.sql_down, ''), COALESCE(m.is_irreversible, false),
	       COALESCE(m.irreversible_reason, ''), COALESCE(m.execution_type, 'FORWARD'),
	       COALESCE(m.rollback_target_id::text, ''), COALESCE(m.source_migration_id::text, '')
	FROM migrations m
	LEFT JOIN target_databases t ON t.id = m.database_id
	LEFT JOIN users cu ON cu.id = m.created_by
	LEFT JOIN users eu ON eu.id = m.executed_by`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMigration(row rowScanner) (*Migration, error) {
	var m Migration
	var status string
	var executedAt sql.NullTime
	var rollbackTargetID sql.NullString
	var sourceMigrationID sql.NullString
	if err := row.Scan(&m.ID, &m.DatabaseID, &m.DatabaseName, &m.Version, &m.Name, &m.SQL, &status,
		&m.CreatedBy, &m.CreatedByName, &m.CreatedAt, &m.UpdatedAt,
		&m.ExecutedBy, &m.ExecutedByName, &executedAt, &m.DurationMs, &m.Error, &m.RejectionReason,
		&m.SQLUp, &m.SQLDown, &m.IsIrreversible, &m.IrreversibleReason, &m.ExecutionType,
		&rollbackTargetID, &sourceMigrationID); err != nil {
		return nil, err
	}
	m.Status = Status(status)
	if executedAt.Valid {
		m.ExecutedAt = executedAt.Time
	}
	if rollbackTargetID.Valid {
		m.RollbackTargetID = rollbackTargetID.String
	}
	if sourceMigrationID.Valid {
		m.SourceMigrationID = sourceMigrationID.String
	}
	if m.SQLUp == "" && m.SQL != "" {
		m.SQLUp = m.SQL
	}
	return &m, nil
}

func getMigration(ctx context.Context, q querier, id string) (*Migration, error) {
	if !ids.IsValid(id) {
		return nil, ErrNotFound
	}
	m, err := scanMigration(q.QueryRowContext(ctx, selectMigrations+` WHERE m.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// executionRecord is what the engine writes when a draft has run.
type executionRecord struct {
	ID         string
	OK         bool
	SQL        string // the script that ran, stored as is so history matches what was executed
	ExecutedBy string
	ExecutedAt time.Time
	Duration   time.Duration
	Error      string
}

// recordExecution moves an APPROVED or DRAFT migration to SUCCESS (with the next version for its database) or FAILED.
// The caller must hold the database's advisory lock, so max(version)+1 cannot race.
func recordExecution(ctx context.Context, q querier, rec executionRecord) error {
	var executedBy any
	if ids.IsValid(rec.ExecutedBy) {
		executedBy = rec.ExecutedBy
	}
	status := StatusFailed
	if rec.OK {
		status = StatusSuccess
	}
	res, err := q.ExecContext(ctx, `
		UPDATE migrations m SET
			status        = $2::varchar,
			sql_script    = $7,
			sql_up        = CASE WHEN m.sql_up = '' THEN $7 ELSE m.sql_up END,
			version       = CASE WHEN $2::varchar = 'SUCCESS' THEN (
			                    SELECT COALESCE(MAX(o.version), 0) + 1 FROM migrations o
			                    WHERE o.database_id = m.database_id) END,
			executed_by   = $3,
			executed_at   = $4,
			duration_ms   = $5,
			error_message = $6,
			updated_at    = NOW()
		WHERE m.id = $1 AND m.status IN ('DRAFT', 'APPROVED')`,
		rec.ID, string(status), executedBy, rec.ExecutedAt, rec.Duration.Milliseconds(), rec.Error, rec.SQL)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotDraft
	}
	return nil
}

// GetLatestActive returns the latest active (SUCCESS, non-ROLLBACK) migration on databaseID.
func (r *Repository) GetLatestActive(ctx context.Context, databaseID string) (*Migration, error) {
	return getLatestActive(ctx, r.db, databaseID)
}

func getLatestActive(ctx context.Context, q querier, databaseID string) (*Migration, error) {
	m, err := scanMigration(q.QueryRowContext(ctx, selectMigrations+`
		WHERE m.database_id = $1 AND m.status = 'SUCCESS' AND m.execution_type <> 'ROLLBACK'
		ORDER BY m.version DESC, m.executed_at DESC
		LIMIT 1`, databaseID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// GetEarliestUnforwardedRolledBack returns the earliest ROLLED_BACK migration that has not yet been re-forwarded.
func (r *Repository) GetEarliestUnforwardedRolledBack(ctx context.Context, databaseID string) (*Migration, error) {
	return getEarliestUnforwardedRolledBack(ctx, r.db, databaseID)
}

func getEarliestUnforwardedRolledBack(ctx context.Context, q querier, databaseID string) (*Migration, error) {
	m, err := scanMigration(q.QueryRowContext(ctx, selectMigrations+`
		WHERE m.database_id = $1
		  AND m.status = 'ROLLED_BACK'
		  AND m.execution_type <> 'ROLLBACK'
		  AND NOT EXISTS (
			  SELECT 1 FROM migrations f
			  WHERE f.source_migration_id = m.id
			    AND f.status IN ('SUCCESS', 'ROLLED_BACK')
		  )
		ORDER BY m.version ASC, m.created_at ASC
		LIMIT 1`, databaseID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// HasAlreadyBeenForwarded reports whether a ROLLED_BACK migration has already been superseded by a forward execution.
func (r *Repository) HasAlreadyBeenForwarded(ctx context.Context, migrationID string) (bool, error) {
	return hasAlreadyBeenForwarded(ctx, r.db, migrationID)
}

func hasAlreadyBeenForwarded(ctx context.Context, q querier, migrationID string) (bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM migrations
			WHERE source_migration_id = $1
			  AND execution_type = 'FORWARD'
			  AND status IN ('SUCCESS', 'ROLLED_BACK')
		)`, migrationID).Scan(&exists)
	return exists, err
}

// HasPendingActionFor reports whether there is an active draft/review/approved rollback or forward request for migrationID.
func (r *Repository) HasPendingActionFor(ctx context.Context, migrationID, execType string) (bool, error) {
	return hasPendingActionFor(ctx, r.db, migrationID, execType)
}

func hasPendingActionFor(ctx context.Context, q querier, migrationID, execType string) (bool, error) {
	var col string
	if execType == "ROLLBACK" {
		col = "rollback_target_id"
	} else {
		col = "source_migration_id"
	}
	var exists bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM migrations
			WHERE `+col+` = $1
			  AND execution_type = $2
			  AND status IN ('DRAFT', 'PENDING_REVIEW', 'APPROVED')
		)`, migrationID, execType).Scan(&exists)
	return exists, err
}
