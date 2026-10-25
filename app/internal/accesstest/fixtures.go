// Package accesstest inserts platform rows directly for integration tests
// (target databases and group grants, before the admin console exists to create them).
package accesstest

import (
	"context"
	"database/sql"
	"testing"

	"dbplatform/app/internal/ids"
)

// InsertTarget adds a target_databases row with a dummy sealed password and returns its id.
func InsertTarget(t testing.TB, db *sql.DB, name string) string {
	t.Helper()
	id := ids.New()
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO target_databases (id, name, host, port, db_name, username, encrypted_password)
		VALUES ($1, $2, 'localhost', 5432, 'test', 'test', '\x00')`, id, name); err != nil {
		t.Fatalf("accesstest: insert target %s: %v", name, err)
	}
	return id
}

// Grant gives a group an access level on a database (VIEW_ONLY or VIEW_EDIT).
func Grant(t testing.TB, db *sql.DB, groupID, databaseID, level string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO group_db_grants (group_id, database_id, access_level) VALUES ($1, $2, $3)
		ON CONFLICT (group_id, database_id) DO UPDATE SET access_level = EXCLUDED.access_level`,
		groupID, databaseID, level); err != nil {
		t.Fatalf("accesstest: grant %s on %s: %v", level, databaseID, err)
	}
}
