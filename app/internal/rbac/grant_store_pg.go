package rbac

import (
	"context"
	"database/sql"
)

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
