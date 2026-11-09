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
	if in.SQLUp != "" {
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
	if sqlUp == "" {
		sqlUp = script
	}
	id := ids.New()
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO migrations (id, database_id, name, sql_script, sql_up, sql_down, is_irreversible, irreversible_reason, execution_type, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'FORWARD', 'DRAFT', $9)`,
		id, in.DatabaseID, name, script, sqlUp, in.SQLDown, in.IsIrreversible, in.IrreversibleReason, createdBy); err != nil {
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
	scriptToValidate := in.SQL
	if in.SQLUp != "" {
		scriptToValidate = in.SQLUp
	}
	name, script, err := NormalizeDraft(in.Name, scriptToValidate)
	if err != nil {
		return nil, err
	}
	if !ids.IsValid(id) {
		return nil, ErrNotFound
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
	       COALESCE(m.rollback_target_id::text, '')
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
	if err := row.Scan(&m.ID, &m.DatabaseID, &m.DatabaseName, &m.Version, &m.Name, &m.SQL, &status,
		&m.CreatedBy, &m.CreatedByName, &m.CreatedAt, &m.UpdatedAt,
		&m.ExecutedBy, &m.ExecutedByName, &executedAt, &m.DurationMs, &m.Error, &m.RejectionReason,
		&m.SQLUp, &m.SQLDown, &m.IsIrreversible, &m.IrreversibleReason, &m.ExecutionType,
		&rollbackTargetID); err != nil {
		return nil, err
	}
	m.Status = Status(status)
	if executedAt.Valid {
		m.ExecutedAt = executedAt.Time
	}
	if rollbackTargetID.Valid {
		m.RollbackTargetID = rollbackTargetID.String
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

// CreateApprovals creates pending approval entries for groups and sets migration status to PENDING_REVIEW.
// If groupIDs is empty, the migration is auto-approved.
func (r *Repository) CreateApprovals(ctx context.Context, migrationID string, groupIDs []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear any previous approvals
	if _, err := tx.ExecContext(ctx, `DELETE FROM migration_approvals WHERE migration_id = $1`, migrationID); err != nil {
		return err
	}

	if len(groupIDs) == 0 {
		// No groups with access require approval -> auto-approved
		if _, err := tx.ExecContext(ctx, `
			UPDATE migrations SET status = 'APPROVED', rejection_reason = '', updated_at = NOW()
			WHERE id = $1`, migrationID); err != nil {
			return err
		}
		return tx.Commit()
	}

	for _, gid := range groupIDs {
		aid := ids.New()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO migration_approvals (id, migration_id, group_id, status)
			VALUES ($1, $2, $3, 'PENDING')`, aid, migrationID, gid); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE migrations SET status = 'PENDING_REVIEW', rejection_reason = '', updated_at = NOW()
		WHERE id = $1`, migrationID); err != nil {
		return err
	}

	return tx.Commit()
}

// ListApprovals returns all approval rows for a migration.
func (r *Repository) ListApprovals(ctx context.Context, migrationID string) ([]Approval, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.migration_id, a.group_id, COALESCE(g.name, ''),
		       COALESCE(a.leader_id::text, ''), COALESCE(u.username, ''),
		       a.status, a.reviewed_at, a.note, a.rejection_reason, a.created_at
		FROM migration_approvals a
		LEFT JOIN groups g ON g.id = a.group_id
		LEFT JOIN users u ON u.id = a.leader_id
		WHERE a.migration_id = $1
		ORDER BY a.created_at ASC`, migrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Approval
	for rows.Next() {
		var ap Approval
		var reviewedAt sql.NullTime
		if err := rows.Scan(&ap.ID, &ap.MigrationID, &ap.GroupID, &ap.GroupName,
			&ap.LeaderID, &ap.LeaderName, &ap.Status, &reviewedAt, &ap.Note, &ap.RejectionReason, &ap.CreatedAt); err != nil {
			return nil, err
		}
		if reviewedAt.Valid {
			ap.ReviewedAt = &reviewedAt.Time
		}
		list = append(list, ap)
	}
	return list, rows.Err()
}

// ApproveGroup marks one group's approval as APPROVED. If all groups have approved, the migration status becomes APPROVED.
func (r *Repository) ApproveGroup(ctx context.Context, migrationID, groupID, leaderID, note string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE migration_approvals
		SET status = 'APPROVED', leader_id = $3, note = $4, reviewed_at = NOW()
		WHERE migration_id = $1 AND group_id = $2 AND status = 'PENDING'`,
		migrationID, groupID, leaderID, note)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, ErrAlreadyReviewed
	}

	var pendingCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM migration_approvals
		WHERE migration_id = $1 AND status != 'APPROVED'`, migrationID).Scan(&pendingCount); err != nil {
		return false, err
	}

	allApproved := pendingCount == 0
	if allApproved {
		if _, err := tx.ExecContext(ctx, `
			UPDATE migrations SET status = 'APPROVED', updated_at = NOW()
			WHERE id = $1`, migrationID); err != nil {
			return false, err
		}
	}

	return allApproved, tx.Commit()
}

// RejectGroup marks one group's approval as REJECTED and sets the migration status to REJECTED.
func (r *Repository) RejectGroup(ctx context.Context, migrationID, groupID, leaderID, reason string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE migration_approvals
		SET status = 'REJECTED', leader_id = $3, rejection_reason = $4, reviewed_at = NOW()
		WHERE migration_id = $1 AND group_id = $2 AND status = 'PENDING'`,
		migrationID, groupID, leaderID, reason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAlreadyReviewed
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE migrations SET status = 'REJECTED', rejection_reason = $2, updated_at = NOW()
		WHERE id = $1`, migrationID, reason); err != nil {
		return err
	}

	return tx.Commit()
}

// FindPendingGroupForLeader finds a group that the user is leader of and has a PENDING approval on the migration.
func (r *Repository) FindPendingGroupForLeader(ctx context.Context, migrationID, userID string) (string, error) {
	var groupID string
	err := r.db.QueryRowContext(ctx, `
		SELECT a.group_id
		FROM migration_approvals a
		JOIN groups g ON g.id = a.group_id
		WHERE a.migration_id = $1 AND g.leader_id = $2 AND a.status = 'PENDING'
		LIMIT 1`, migrationID, userID).Scan(&groupID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotLeader
		}
		return "", err
	}
	return groupID, nil
}

// ListPendingReviews returns migrations in PENDING_REVIEW status.
// If isPlatformAdmin is true, it returns all pending migrations across the system.
// Otherwise, it returns migrations where a group led by userID is pending review.
func (r *Repository) ListPendingReviews(ctx context.Context, userID string, isPlatformAdmin bool) ([]*Migration, error) {
	var query string
	var args []any

	if isPlatformAdmin {
		query = selectMigrations + `
			WHERE m.status = 'PENDING_REVIEW'
			ORDER BY m.created_at DESC`
	} else {
		query = selectMigrations + `
			WHERE m.status = 'PENDING_REVIEW'
			  AND EXISTS (
				  SELECT 1 FROM migration_approvals a
				  JOIN groups g ON g.id = a.group_id
				  WHERE a.migration_id = m.id AND g.leader_id = $1
			  )
			ORDER BY m.created_at DESC`
		args = append(args, userID)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, m := range list {
		m.Approvals, _ = r.ListApprovals(ctx, m.ID)
		if _, err := r.FindPendingGroupForLeader(ctx, m.ID, userID); err == nil {
			m.CanApprove = true
		}
	}

	return list, nil
}
