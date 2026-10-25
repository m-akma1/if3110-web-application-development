package migration

import (
	"context"
	"database/sql"
	"testing"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/dbmanager"
	"dbplatform/internal/ids"
	"dbplatform/internal/testutil"
)

// The engine must accept the registry the HTTP server already uses.
var _ PoolSource = (*dbmanager.ConnectionRegistry)(nil)

// fixture is a platform schema (with every platform migration) plus a separate, empty schema
// that plays the target database. Both live in TEST_DATABASE_URL; the test skips without it.
type fixture struct {
	platform *sql.DB
	target   *sql.DB
	pools    *dbmanager.ConnectionRegistry
	dbID     string
	userID   string
	repo     *Repository
	engine   *Engine
}

func newFixture(t *testing.T, limits Limits) *fixture {
	t.Helper()
	f := &fixture{
		platform: testutil.NewPlatformDB(t),
		target:   testutil.NewSchemaDB(t),
		pools:    dbmanager.NewConnectionRegistry(),
	}
	f.dbID = accesstest.InsertTarget(t, f.platform, "demo-"+ids.New()[:8])
	f.pools.RegisterPool(f.dbID, f.target)
	f.userID = insertUser(t, f.platform, "engineer")
	f.repo = NewRepository(f.platform)
	f.engine = NewEngine(f.platform, f.pools, limits)
	return f
}

func insertUser(t *testing.T, db *sql.DB, username string) string {
	t.Helper()
	id := ids.New()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO users (id, username, password_hash) VALUES ($1, $2, 'h')`, id, username); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	return id
}

func (f *fixture) draft(t *testing.T, name, script string) *Migration {
	t.Helper()
	m, err := f.repo.CreateDraft(context.Background(), NewDraft{DatabaseID: f.dbID, Name: name, SQL: script, CreatedBy: f.userID})
	if err != nil {
		t.Fatalf("CreateDraft(%s): %v", name, err)
	}
	return m
}

// targetHasTable reports whether table exists in the target's schema.
func (f *fixture) targetHasTable(t *testing.T, table string) bool {
	t.Helper()
	var exists bool
	if err := f.target.QueryRowContext(context.Background(),
		`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return exists
}
