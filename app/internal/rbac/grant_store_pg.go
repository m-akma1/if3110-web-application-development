package rbac

import (
	"context"
	"database/sql"
	"errors"

	"dbplatform/app/internal/ids"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrUnknownGrantTarget means the group or database of a grant does not exist.
var ErrUnknownGrantTarget = errors.New("group atau database tidak ditemukan")

// PostgresGrantStore reads grants from group_members ⋈ group_db_grants on the platform DB.
type PostgresGrantStore struct {
	db *sql.DB
}

func NewPostgresGrantStore(db *sql.DB) *PostgresGrantStore {
	return &PostgresGrantStore{db: db}
}

func (s *PostgresGrantStore) LevelsForUser(ctx context.Context, userID, dbID string) ([]AccessLevel, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.access_level
		FROM group_db_grants g
		JOIN group_members m ON m.group_id = g.group_id
		WHERE m.user_id = $1 AND g.database_id = $2`, userID, dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var levels []AccessLevel
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		levels = append(levels, AccessLevel(l))
	}
	return levels, rows.Err()
}

func (s *PostgresGrantStore) GrantsForUser(ctx context.Context, userID string) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, g.access_level
		FROM group_db_grants g
		JOIN group_members m ON m.group_id = g.group_id
		JOIN target_databases t ON t.id = g.database_id
		WHERE m.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var grants []Grant
	for rows.Next() {
		var g Grant
		var level string
		if err := rows.Scan(&g.DatabaseID, &g.DatabaseName, &level); err != nil {
			return nil, err
		}
		g.Level = AccessLevel(level)
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// GroupGrant is one cell of the admin grant matrix.
type GroupGrant struct {
	GroupID    string
	DatabaseID string
	Level      AccessLevel
}

// ListGrants returns every stored grant (NONE is never stored).
func (s *PostgresGrantStore) ListGrants(ctx context.Context) ([]GroupGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT group_id, database_id, access_level FROM group_db_grants`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []GroupGrant
	for rows.Next() {
		var g GroupGrant
		var level string
		if err := rows.Scan(&g.GroupID, &g.DatabaseID, &level); err != nil {
			return nil, err
		}
		g.Level = AccessLevel(level)
		list = append(list, g)
	}
	return list, rows.Err()
}

// SetGroupGrant sets a group's level on a database and returns the previous level.
// NONE removes the grant. Unknown group or database returns ErrUnknownGrantTarget.
func (s *PostgresGrantStore) SetGroupGrant(ctx context.Context, groupID, databaseID string, level AccessLevel) (AccessLevel, error) {
	if !ids.IsValid(groupID) || !ids.IsValid(databaseID) {
		return AccessNone, ErrUnknownGrantTarget
	}
	level = Normalize(level)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccessNone, err
	}
	defer func() { _ = tx.Rollback() }()

	previous := AccessNone
	var prev string
	err = tx.QueryRowContext(ctx, `
		SELECT access_level FROM group_db_grants WHERE group_id = $1 AND database_id = $2 FOR UPDATE`,
		groupID, databaseID).Scan(&prev)
	switch {
	case err == nil:
		previous = AccessLevel(prev)
	case !errors.Is(err, sql.ErrNoRows):
		return AccessNone, err
	}

	if level == AccessNone {
		_, err = tx.ExecContext(ctx, `DELETE FROM group_db_grants WHERE group_id = $1 AND database_id = $2`, groupID, databaseID)
	} else {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO group_db_grants (group_id, database_id, access_level) VALUES ($1, $2, $3)
			ON CONFLICT (group_id, database_id) DO UPDATE SET access_level = EXCLUDED.access_level, granted_at = NOW()`,
			groupID, databaseID, string(level))
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return AccessNone, ErrUnknownGrantTarget
	}
	if err != nil {
		return AccessNone, err
	}
	return previous, tx.Commit()
}
