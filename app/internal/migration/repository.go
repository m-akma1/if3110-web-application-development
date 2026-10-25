package migration

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"dbplatform/app/internal/ids"
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
	DatabaseID string
	Name       string
	SQL        string
	CreatedBy  string // user id
}

// CreateDraft inserts a DRAFT migration. Name and script are validated with NormalizeDraft.
func (r *Repository) CreateDraft(ctx context.Context, in NewDraft) (*Migration, error) {
	name, script, err := NormalizeDraft(in.Name, in.SQL)
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
	id := ids.New()
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO migrations (id, database_id, name, sql_script, status, created_by)
		VALUES ($1, $2, $3, $4, 'DRAFT', $5)`,
		id, in.DatabaseID, name, script, createdBy); err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// UpdateDraft changes the name and script of a DRAFT. Executed migrations return ErrNotDraft.
func (r *Repository) UpdateDraft(ctx context.Context, id, name, script string) (*Migration, error) {
	name, script, err := NormalizeDraft(name, script)
	if err != nil {
		return nil, err
	}
	if !ids.IsValid(id) {
		return nil, ErrNotFound
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE migrations SET name = $2, sql_script = $3, updated_at = NOW()
		WHERE id = $1 AND status = 'DRAFT'`, id, name, script)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, r.notDraftOrMissing(ctx, id)
	}
	return r.Get(ctx, id)
}

// DeleteDraft removes a DRAFT. Executed migrations return ErrNotDraft.
func (r *Repository) DeleteDraft(ctx context.Context, id string) error {
	if !ids.IsValid(id) {
		return ErrNotFound
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM migrations WHERE id = $1 AND status = 'DRAFT'`, id)
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
	       COALESCE(m.duration_ms, 0), m.error_message
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
	if err := row.Scan(&m.ID, &m.DatabaseID, &m.DatabaseName, &m.Version, &m.Name, &m.SQL, &status,
		&m.CreatedBy, &m.CreatedByName, &m.CreatedAt, &m.UpdatedAt,
		&m.ExecutedBy, &m.ExecutedByName, &executedAt, &m.DurationMs, &m.Error); err != nil {
		return nil, err
	}
	m.Status = Status(status)
	if executedAt.Valid {
		m.ExecutedAt = executedAt.Time
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

// recordExecution moves a DRAFT to SUCCESS (with the next version for its database) or FAILED.
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
			version       = CASE WHEN $2::varchar = 'SUCCESS' THEN (
			                    SELECT COALESCE(MAX(o.version), 0) + 1 FROM migrations o
			                    WHERE o.database_id = m.database_id) END,
			executed_by   = $3,
			executed_at   = $4,
			duration_ms   = $5,
			error_message = $6,
			updated_at    = NOW()
		WHERE m.id = $1 AND m.status = 'DRAFT'`,
		rec.ID, string(status), executedBy, rec.ExecutedAt, rec.Duration.Milliseconds(), rec.Error, rec.SQL)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotDraft
	}
	return nil
}
